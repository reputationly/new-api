// 统一任务客户端(设计文档 §3.4):按体验区契约调用 new-api 任务门面。
//   提交  POST /pg/videos  { model, prompt, metadata:{task_type,...}, images?, ... }
//   轮询  GET  /pg/videos/:task_id   status ∈ queued/in_progress/completed/failed/canceled
//   成品  GET  /pg/videos/:task_id/content(会话鉴权代理)
// 响应兼容 OpenAIVideo(顶层 id/status)与通用 TaskResponse(data.task_id)两种形态,
// 与 web/classic useVideoGeneration 的解析对齐。请求组装由能力注册表驱动,本文件不含能力分支。

import axios from "axios";

import { builtinHeaders } from "@/lib/builtin-auth";

export const TASK_POLL_INTERVAL_MS = 4000;
export const TASK_POLL_MAX_TIMES = 90; // ≈ 6 分钟,与体验区一致

export type PlaygroundTaskPayload = {
    model: string;
    prompt: string;
    group?: string;
    metadata?: Record<string, unknown>;
    images?: string[];
    size?: string;
    seconds?: string;
    [key: string]: unknown;
};

/** 轮询超时(≠任务失败:任务仍在服务端运行,节点转 stalled,可「继续等待」恢复,§3.4) */
export class TaskPollTimeoutError extends Error {
    constructor(public readonly taskId: string) {
        super("任务仍在生成中,已超过预计等待时间");
        this.name = "TaskPollTimeoutError";
    }
}

export type PlaygroundTaskStatus = "queued" | "in_progress" | "completed" | "failed" | "canceled";

type RawTaskEnvelope = {
    id?: string;
    task_id?: string;
    status?: string;
    progress?: unknown;
    fail_reason?: string;
    error?: { message?: string };
    data?: RawTaskEnvelope;
};

function normalizeTaskStatus(status: string | undefined): PlaygroundTaskStatus {
    switch ((status || "").toLowerCase()) {
        case "completed":
        case "succeed":
        case "success":
            return "completed";
        case "failed":
        case "failure":
        case "error":
            return "failed";
        case "canceled":
        case "cancelled":
            return "canceled";
        case "in_progress":
        case "running":
        case "processing":
            return "in_progress";
        default:
            return "queued";
    }
}

function parseEnvelope(data: RawTaskEnvelope | undefined) {
    const outer = data || {};
    const inner = outer.data || {};
    return {
        taskId: outer.id || outer.task_id || inner.task_id || inner.id || "",
        status: normalizeTaskStatus(outer.status || inner.status),
        errorMessage: outer.error?.message || inner.error?.message || inner.fail_reason || outer.fail_reason || "",
    };
}

export function taskContentUrl(taskId: string) {
    return `/pg/videos/${encodeURIComponent(taskId)}/content`;
}

/** 提交任务;提交即失败(响应 status=failed)时抛错 */
export async function submitPlaygroundTask(payload: PlaygroundTaskPayload, options?: { signal?: AbortSignal }): Promise<{ taskId: string }> {
    const response = await axios.post<RawTaskEnvelope>("/pg/videos", payload, { headers: builtinHeaders(), signal: options?.signal });
    const { taskId, status, errorMessage } = parseEnvelope(response.data);
    if (!taskId) throw new Error(errorMessage || "提交任务失败");
    if (status === "failed" || status === "canceled") throw new Error(errorMessage || "任务提交即失败");
    return { taskId };
}

export async function fetchPlaygroundTask(taskId: string, options?: { signal?: AbortSignal }): Promise<{ status: PlaygroundTaskStatus; errorMessage: string }> {
    const response = await axios.get<RawTaskEnvelope>(`/pg/videos/${encodeURIComponent(taskId)}`, { headers: builtinHeaders(), signal: options?.signal });
    const { status, errorMessage } = parseEnvelope(response.data);
    return { status, errorMessage };
}

/** 轮询到终态。completed → 返回;failed/canceled → 抛错;超时 → TaskPollTimeoutError(可恢复)。 */
export async function pollPlaygroundTask(taskId: string, options?: { signal?: AbortSignal; onProgress?: (status: PlaygroundTaskStatus) => void }): Promise<void> {
    for (let attempt = 0; attempt < TASK_POLL_MAX_TIMES; attempt++) {
        await new Promise((resolve) => setTimeout(resolve, TASK_POLL_INTERVAL_MS));
        if (options?.signal?.aborted) throw new DOMException("生成已取消", "AbortError");
        const { status, errorMessage } = await fetchPlaygroundTask(taskId, options);
        options?.onProgress?.(status);
        if (status === "completed") return;
        if (status === "failed" || status === "canceled") throw new Error(errorMessage || "任务失败");
    }
    throw new TaskPollTimeoutError(taskId);
}

/** 拉取成品字节(带会话头),供落 IndexedDB/展示 */
export async function fetchTaskContentBlob(taskId: string, options?: { signal?: AbortSignal }): Promise<Blob> {
    const response = await axios.get<Blob>(taskContentUrl(taskId), { headers: builtinHeaders(), responseType: "blob", signal: options?.signal });
    return response.data;
}
