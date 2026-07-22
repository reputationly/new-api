// 能力执行器(设计文档 §3.4/§3.5):由注册表 spec + 槽位值 + 参数组装任务请求并执行。
// 仅覆盖 channel === "task" 的能力;图片能力(t2i/i2i)复用画布既有同步生图链路。
// 槽位值可以是 task:<task_id>(上游 gpustackplus 产物引用,后端 NFS 直读)或
// base64 data-url / http(s) URL,后端 nfsinput.AddString 统一识别。
// 本文件保持零能力分支:能力私有逻辑一律走 spec.postProcess(注册表内声明)。

import { pollPlaygroundTask, submitPlaygroundTask, taskContentUrl, type PlaygroundTaskPayload } from "@/services/api/task";
import type { CapabilitySpec } from "./registry";

/** 槽位 key → 值列表(单值槽取首个) */
export type SlotValues = Record<string, string[]>;
export type CapabilityParams = Record<string, string | number>;

function hasParamValue(params: CapabilityParams, key: string): boolean {
    const raw = params[key];
    return raw !== undefined && raw !== null && String(raw).trim() !== "";
}

/** 生成前校验:返回缺失/超限的槽位与必填参数描述(空数组 = 通过) */
export function validateCapabilityInputs(spec: CapabilitySpec, prompt: string, slots: SlotValues, params: CapabilityParams = {}): string[] {
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
    if (spec.requireOneOf && !(slots[spec.requireOneOf.slotKey] || []).length && !hasParamValue(params, spec.requireOneOf.paramKey)) {
        problems.push(spec.requireOneOf.message);
    }
    for (const param of spec.params) {
        if (param.required && !hasParamValue(params, param.key)) problems.push(`请在参数面板填写「${param.label}」`);
    }
    // 能力私有的条件校验(如语音合成:连了克隆参考音且未开仅音色向量时,参考文本必填)
    if (spec.validate) problems.push(...spec.validate({ prompt, slots, params }));
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

/** 按 spec 组装 /pg/videos 提交体;参数值为空串/undefined 时不下发(走引擎默认,defaultValue 例外) */
export function buildTaskPayload(spec: CapabilitySpec, model: string, prompt: string, params: CapabilityParams, slots: SlotValues, group?: string): PlaygroundTaskPayload {
    const effectivePrompt = prompt.trim() || spec.promptFallback || "";
    const payload: PlaygroundTaskPayload = { model, prompt: effectivePrompt };
    if (group) payload.group = group;
    const metadata: Record<string, unknown> = {};
    // task_type:提示词非空时可切换(AudioX v2a→tv2a / v2m→tv2m,与体验区 resolveTaskType 一致)
    const taskType = prompt.trim() && spec.taskTypeWithPrompt ? spec.taskTypeWithPrompt : spec.taskType;
    if (taskType) metadata.task_type = taskType;
    if (taskType && spec.mirrorTaskTypeAs) metadata[spec.mirrorTaskTypeAs] = taskType;

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

    const virtualKeys = new Set(spec.params.filter((param) => param.virtual).map((param) => param.key));
    for (const [key, raw] of Object.entries(params)) {
        if (raw === "" || raw === undefined || raw === null) continue;
        if (virtualKeys.has(key)) continue; // 仅供 postProcess 消费
        if (key.startsWith("metadata.")) {
            metadata[key.slice("metadata.".length)] = raw;
        } else {
            payload[key] = typeof raw === "number" ? String(raw) : raw;
        }
    }
    // 未填参数兜底默认值(如 AudioX 引擎硬要 num_inference_steps,无 deploy-config 兜底)
    for (const param of spec.params) {
        if (param.defaultValue === undefined || param.virtual || hasParamValue(params, param.key)) continue;
        if (param.key.startsWith("metadata.")) {
            metadata[param.key.slice("metadata.".length)] = param.defaultValue;
        } else {
            payload[param.key] = String(param.defaultValue);
        }
    }

    spec.postProcess?.({ prompt: effectivePrompt, params, slots, metadata, payload });

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
    args: { model: string; prompt: string; params: CapabilityParams; slots: SlotValues; group?: string; signal?: AbortSignal; onTaskId?: (taskId: string) => void },
): Promise<TaskCapabilityResult> {
    const payload = buildTaskPayload(spec, args.model, args.prompt, args.params, args.slots, args.group);
    const { taskId } = await submitPlaygroundTask(payload, { signal: args.signal });
    args.onTaskId?.(taskId);
    await pollPlaygroundTask(taskId, { signal: args.signal });
    return { taskId, contentUrl: taskContentUrl(taskId) };
}

/** 恢复一个进行中的任务(刷新后按节点存的 taskId 续轮询;stalled「继续等待」同入口) */
export async function resumeTaskCapability(taskId: string, options?: { signal?: AbortSignal }): Promise<TaskCapabilityResult> {
    await pollPlaygroundTask(taskId, options);
    return { taskId, contentUrl: taskContentUrl(taskId) };
}
