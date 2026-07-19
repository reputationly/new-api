// 能力执行器(设计文档 §3.4/§3.5):由注册表 spec + 槽位值 + 参数组装任务请求并执行。
// 仅覆盖 channel === "task" 的能力;图片能力(t2i/i2i)复用画布既有同步生图链路。
// 槽位值可以是 task:<task_id>(上游 gpustackplus 产物引用,后端 NFS 直读)或
// base64 data-url / http(s) URL,后端 nfsinput.AddString 统一识别。

import { pollPlaygroundTask, submitPlaygroundTask, taskContentUrl, type PlaygroundTaskPayload } from "@/services/api/task";
import type { CapabilitySpec } from "./registry";

/** 槽位 key → 值列表(单值槽取首个) */
export type SlotValues = Record<string, string[]>;
export type CapabilityParams = Record<string, string | number>;

/** 生成前校验:返回缺失/超限的槽位描述(空数组 = 通过) */
export function validateCapabilityInputs(spec: CapabilitySpec, prompt: string, slots: SlotValues): string[] {
    const problems: string[] = [];
    for (const slot of spec.inputs) {
        if (slot.key === "prompt") {
            if (slot.required && !prompt.trim()) problems.push(`缺少${slot.role}`);
            continue;
        }
        const values = slots[slot.key] || [];
        if (slot.required && !values.length) problems.push(`缺少${slot.role}(请连接上游${kindLabel(slot.kind)}节点)`);
        if (slot.max && values.length > slot.max) problems.push(`${slot.role}最多 ${slot.max} 个,当前 ${values.length} 个`);
    }
    if (spec.atLeastOne && !spec.atLeastOne.keys.some((key) => (slots[key] || []).length)) {
        problems.push(spec.atLeastOne.message);
    }
    return problems;
}

function kindLabel(kind: string) {
    switch (kind) {
        case "image":
            return "图片";
        case "video":
            return "视频";
        case "audio":
            return "音频";
        default:
            return "文本";
    }
}

/** 按 spec 组装 /pg/videos 提交体;参数值为空串/undefined 时不下发(走引擎默认) */
export function buildTaskPayload(spec: CapabilitySpec, model: string, prompt: string, params: CapabilityParams, slots: SlotValues): PlaygroundTaskPayload {
    const payload: PlaygroundTaskPayload = { model, prompt };
    const metadata: Record<string, unknown> = {};
    if (spec.taskType) metadata.task_type = spec.taskType;

    for (const slot of spec.inputs) {
        if (slot.key === "prompt") continue;
        const values = (slots[slot.key] || []).filter(Boolean);
        if (!values.length) continue;
        const capped = slot.max ? values.slice(0, slot.max) : values.slice(0, 1);
        if (slot.key === "images") {
            payload.images = capped;
        } else if (slot.key.startsWith("metadata.")) {
            const name = slot.key.slice("metadata.".length);
            metadata[name] = slot.max && slot.max > 1 ? capped : capped[0];
        }
    }

    for (const [key, raw] of Object.entries(params)) {
        if (raw === "" || raw === undefined || raw === null) continue;
        if (key.startsWith("metadata.")) {
            metadata[key.slice("metadata.".length)] = raw;
        } else {
            payload[key] = typeof raw === "number" ? String(raw) : raw;
        }
    }

    if (Object.keys(metadata).length) payload.metadata = metadata;
    return payload;
}

export type TaskCapabilityResult = {
    taskId: string;
    contentUrl: string;
};

/** 提交 → 轮询到完成。onTaskId 在提交成功后立刻回调(节点持久化 taskId,刷新可恢复)。 */
export async function runTaskCapability(
    spec: CapabilitySpec,
    args: { model: string; prompt: string; params: CapabilityParams; slots: SlotValues; signal?: AbortSignal; onTaskId?: (taskId: string) => void },
): Promise<TaskCapabilityResult> {
    const payload = buildTaskPayload(spec, args.model, args.prompt, args.params, args.slots);
    const { taskId } = await submitPlaygroundTask(payload, { signal: args.signal });
    args.onTaskId?.(taskId);
    await pollPlaygroundTask(taskId, { signal: args.signal });
    return { taskId, contentUrl: taskContentUrl(taskId) };
}

/** 恢复一个进行中的任务(刷新后按节点存的 taskId 续轮询) */
export async function resumeTaskCapability(taskId: string, options?: { signal?: AbortSignal }): Promise<TaskCapabilityResult> {
    await pollPlaygroundTask(taskId, options);
    return { taskId, contentUrl: taskContentUrl(taskId) };
}
