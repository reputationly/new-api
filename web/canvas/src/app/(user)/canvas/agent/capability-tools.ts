// 让在线画布 Agent 够得着能力编排(设计文档 docs/canvas-orchestration-design.md)。
//
// 背景:Agent 原有的生成工具只认旧的四种 mode(text/image/video/audio),对应的是
// 「生成配置节点」这条老链路;而画布真正的能力面是注册表里的 19 个能力标签
// (t2i/i2v/flf2v/sr/tts_synth/t2m/...),每个能力有自己的输入槽位、参数白名单和
// 模型集合。Agent 够不着能力节点 = 编排能力对它不存在。
//
// 这里补四个工具:
//   canvas_list_capabilities      读:有哪些能力、各自要什么输入/参数、当前用户有哪些可用模型
//   canvas_create_capability_node 写:按注册表语义建能力节点,可选自动连上游 + 立即生成
//   canvas_extract_video_frame    写:视频节点截帧成图片节点(视频续接取尾帧接下一段首帧)
//   canvas_concat_videos          写:多段视频 remux 成一条成片(多镜头收尾;浏览器端不重编码)
//
// 节点构造语义与编辑器 createCapabilityNode()、模板 capabilityNode() 保持一致:
// 节点媒体类型 = 能力产物类型(spec.output),metadata 带 capability/generationMode/model。

import { nanoid } from "nanoid";

import type { ResponseFunctionTool } from "@/services/api/image";
import { CAPABILITIES, capabilitySpec, type CapabilitySpec } from "@/services/capabilities/registry";
import { uploadMediaFile } from "@/services/file-storage";
import { uploadImage } from "@/services/image-storage";
import { modelsForCapability, paramOptionsForModel, useMediaConfigStore } from "@/stores/use-media-config-store";
import { NODE_DEFAULT_SIZE } from "../constants";
import { CanvasNodeType, type CanvasNodeMetadata } from "../types";
import type { CanvasAgentOp, CanvasAgentSnapshot } from "../utils/canvas-agent-ops";
import { fitNodeSize } from "../utils/canvas-node-size";
import { concatVideos, toConcatSources } from "../utils/canvas-video-concat";
import { extractVideoFrame } from "../utils/canvas-video-frame";

const CAPABILITY_KEYS = CAPABILITIES.map((spec) => spec.key);

export const CAPABILITY_AGENT_TOOLS: ResponseFunctionTool[] = [
    {
        type: "function",
        function: {
            name: "canvas_list_capabilities",
            description: "读取画布支持的全部生成能力(文生图/图生视频/关键帧/视频超分/语音合成/文生音乐等)，含每个能力的输入槽位、可调参数和当前账号可用模型。需要编排能力节点前必须先调用，不要凭能力名猜测输入和参数。",
            parameters: { type: "object", properties: { capability: { type: "string", enum: CAPABILITY_KEYS, description: "只看某一个能力时传，缺省返回全部" } }, required: [], additionalProperties: false },
            strict: false,
        },
    },
    {
        type: "function",
        function: {
            name: "canvas_create_capability_node",
            description:
                "创建一个能力节点并按需连接上游、立即生成。能力节点是画布编排的基本单元：上游节点的产物按 canvas_list_capabilities 返回的输入槽位自动成为它的输入。需要串联多步时(如 文生图 → 图生视频 → 视频超分)，逐个创建并用 sourceNodeIds 连成链。",
            parameters: {
                type: "object",
                properties: {
                    capability: { type: "string", enum: CAPABILITY_KEYS, description: "能力 key，取值见 canvas_list_capabilities" },
                    prompt: { type: "string", description: "该节点自身的提示词；上游文本节点的内容会另行并入，不要重复抄写" },
                    title: { type: "string", description: "节点标题，缺省用能力名" },
                    model: { type: "string", description: "指定模型，必须来自该能力的 availableModels；缺省自动选第一个可用模型" },
                    params: { type: "object", additionalProperties: true, description: "能力参数，键取自该能力 params[].key（如 size、seconds、metadata.speaker）" },
                    sourceNodeIds: { type: "array", items: { type: "string" }, description: "真实上游节点 id，按顺序连线并按顺序填入同类输入槽位；无上游时传空数组" },
                    x: { type: "number" },
                    y: { type: "number" },
                    autoRun: { type: "boolean", description: "true 表示创建后立即提交生成；上游媒体尚未生成完成时不要设 true" },
                },
                required: ["capability"],
                additionalProperties: false,
            },
            strict: false,
        },
    },
    {
        type: "function",
        function: {
            name: "canvas_concat_videos",
            description:
                "把多个已生成完成的视频节点按给定顺序首尾拼成一条成片，产出新的视频节点并把各段连过去。多镜头作品的收尾步骤。纯 stream copy 不重编码，所以各段的分辨率和编码必须一致——不一致会报错，先给它们统一过一遍视频超分(sr)再拼。视频还在 loading 时不可用，先 canvas_wait_generation。",
            parameters: {
                type: "object",
                properties: { nodeIds: { type: "array", items: { type: "string" }, minItems: 2, description: "视频节点 id，数组顺序即拼接顺序" }, title: { type: "string", description: "成片节点标题" } },
                required: ["nodeIds"],
                additionalProperties: false,
            },
            strict: false,
        },
    },
    {
        type: "function",
        function: {
            name: "canvas_extract_video_frame",
            description: "从一个已生成完成的视频节点截取单帧，产出图片节点并自动连线。视频续接的关键一步：取上一段的尾帧作为下一段 flf2v 的首帧，画面才能真正接上。视频还在 loading 时不可用。",
            parameters: {
                type: "object",
                properties: {
                    nodeId: { type: "string", description: "视频节点 id" },
                    // 拆成两个有明确 type 的字段:无 type 的属性会被部分渠道的 schema 校验拒掉
                    at: { type: "string", enum: ["last", "first"], description: "last 尾帧（默认，用于续接）、first 首帧" },
                    atSeconds: { type: "number", description: "截取指定秒数的帧；填了则忽略 at" },
                },
                required: ["nodeId"],
                additionalProperties: false,
            },
            strict: false,
        },
    },
];

/** canvas_list_capabilities 的返回体 */
export function listCapabilitiesResult(capabilityKey?: string) {
    const store = useMediaConfigStore.getState();
    const specs = capabilityKey ? [capabilitySpec(capabilityKey)].filter((spec): spec is CapabilitySpec => Boolean(spec)) : CAPABILITIES;
    if (capabilityKey && !specs.length) return { ok: false as const, message: `不存在的能力：${capabilityKey}。可用能力：${CAPABILITY_KEYS.join("、")}` };

    const items = specs.map((spec) => {
        const models = modelsForCapability(store, spec);
        // 参数白名单(尺寸/时长枚举)按模型走,取首个可用模型作为代表值;无可用模型时留空
        const entry = models.length ? paramOptionsForModel(store.configs, spec.modality, models[0]) : null;
        return {
            key: spec.key,
            label: spec.label,
            modality: spec.modality,
            outputNodeType: spec.output,
            inputs: spec.inputs.map((slot) => ({ key: slot.key, role: slot.role, kind: slot.kind, required: slot.required, max: slot.max ?? 1 })),
            params: spec.params.map((param) => ({
                key: param.key,
                label: param.label,
                type: param.type,
                required: Boolean(param.required),
                options: param.options === "sizes" ? entry?.sizes || [] : param.options === "durations" ? entry?.durations || [] : param.options || undefined,
            })),
            availableModels: models,
            ...(models.length ? {} : { unavailableReason: "当前账号没有为该能力配置可用模型，创建节点后需要用户手动选择" }),
        };
    });

    const usable = items.filter((item) => item.availableModels.length).length;
    return { ok: true as const, message: `共 ${items.length} 个能力，其中 ${usable} 个当前有可用模型。`, data: { capabilities: items } };
}

/** canvas_extract_video_frame → ops（异步：先解码取帧并上传，拿到 dataUrl 才能组节点） */
export async function extractVideoFrameOps(input: Record<string, unknown>, snapshot: CanvasAgentSnapshot): Promise<CanvasAgentOp[]> {
    const nodeId = typeof input.nodeId === "string" ? input.nodeId : "";
    const source = snapshot.nodes.find((node) => node.id === nodeId);
    if (!source) throw new Error(`节点不存在：${nodeId || "(空)"}。请先用 canvas_get_state 读取真实节点 id。`);
    if (source.type !== CanvasNodeType.Video) throw new Error(`节点 ${nodeId} 不是视频节点（当前是 ${source.type}），无法截帧。`);
    if (!source.metadata?.content) throw new Error(`视频节点 ${nodeId} 还没有内容，可能仍在生成中。等它 status=success 再截帧。`);

    const at = typeof input.atSeconds === "number" ? input.atSeconds : input.at === "first" ? ("first" as const) : ("last" as const);
    const frame = await extractVideoFrame({ content: source.metadata.content, storageKey: source.metadata.storageKey }, at);
    const image = await uploadImage(frame.dataUrl);

    const label = at === "first" ? "首帧" : at === "last" ? "尾帧" : `${frame.time.toFixed(2)}s 帧`;
    const size = fitNodeSize(image.width, image.height, NODE_DEFAULT_SIZE[CanvasNodeType.Image].width, NODE_DEFAULT_SIZE[CanvasNodeType.Image].height);
    const childId = `image-${nanoid(8)}`;
    return [
        {
            type: "add_node",
            id: childId,
            nodeType: CanvasNodeType.Image,
            title: `${source.title || "视频"} ${label}`,
            position: { x: source.position.x + source.width + 96, y: source.position.y },
            width: size.width,
            height: size.height,
            metadata: { content: image.url, storageKey: image.storageKey, status: "success", mimeType: image.mimeType, bytes: image.bytes, naturalWidth: image.width, naturalHeight: image.height },
        },
        { type: "connect_nodes", fromNodeId: source.id, toNodeId: childId },
        { type: "select_nodes", ids: [childId] },
    ];
}

/** canvas_concat_videos → ops（异步：解析各段 MP4、remux、上传后才能组节点） */
export async function concatVideosOps(input: Record<string, unknown>, snapshot: CanvasAgentSnapshot): Promise<CanvasAgentOp[]> {
    const ids = Array.isArray(input.nodeIds) ? input.nodeIds.filter((id): id is string => typeof id === "string") : [];
    if (ids.length < 2) throw new Error("至少需要 2 个视频节点才能拼接。");

    const picked = ids.map((id) => {
        const node = snapshot.nodes.find((item) => item.id === id);
        if (!node) throw new Error(`节点不存在：${id}。请先用 canvas_get_state 读取真实节点 id。`);
        if (node.type !== CanvasNodeType.Video) throw new Error(`节点 ${id} 不是视频节点（当前是 ${node.type}）。`);
        if (!node.metadata?.content) throw new Error(`视频节点 ${id} 还没有内容，可能仍在生成中。先用 canvas_wait_generation 等它完成。`);
        return node;
    });

    const result = await concatVideos(toConcatSources(picked));
    const stored = await uploadMediaFile(result.blob, "concat");
    const last = picked[picked.length - 1];
    const size = fitNodeSize(result.width || last.width, result.height || last.height, 420, 236);
    const nodeId = `video-${nanoid(8)}`;
    return [
        {
            type: "add_node",
            id: nodeId,
            nodeType: CanvasNodeType.Video,
            title: typeof input.title === "string" && input.title ? input.title : `成片（${result.clips} 段）`,
            position: { x: last.position.x + last.width + 96, y: last.position.y },
            width: size.width,
            height: size.height,
            metadata: { content: stored.url, storageKey: stored.storageKey, status: "success", mimeType: "video/mp4", bytes: stored.bytes, naturalWidth: result.width, naturalHeight: result.height },
        },
        ...picked.map((node) => ({ type: "connect_nodes" as const, fromNodeId: node.id, toNodeId: nodeId })),
        { type: "select_nodes", ids: [nodeId] },
    ];
}

/** canvas_create_capability_node → ops */
export function capabilityNodeOps(input: Record<string, unknown>, snapshot: CanvasAgentSnapshot, fallbackX: number): CanvasAgentOp[] {
    const capabilityKey = typeof input.capability === "string" ? input.capability : "";
    const spec = capabilitySpec(capabilityKey);
    if (!spec) throw new Error(`不存在的能力：${capabilityKey || "(空)"}。可用能力：${CAPABILITY_KEYS.join("、")}`);

    const store = useMediaConfigStore.getState();
    const models = modelsForCapability(store, spec);
    const requested = typeof input.model === "string" && input.model ? input.model : "";
    if (requested && !models.includes(requested)) throw new Error(`模型「${requested}」不在能力「${spec.label}」的可用模型内。可用：${models.length ? models.join("、") : "(无，需用户先在后台配置)"}`);
    const model = requested || models[0] || "";

    // 上游节点必须真实存在:agent 用过期 id 连线会静默丢失,这里直接报错让它重新读画布
    const sourceNodeIds = Array.isArray(input.sourceNodeIds) ? input.sourceNodeIds.filter((id): id is string => typeof id === "string") : [];
    const missing = sourceNodeIds.filter((id) => !snapshot.nodes.some((node) => node.id === id));
    if (missing.length) throw new Error(`上游节点不存在：${missing.join("、")}。请先用 canvas_get_state 读取真实节点 id。`);

    const nodeId = `${spec.output}-${nanoid(8)}`;
    const generationMode = spec.output === CanvasNodeType.Video ? ("video" as const) : spec.output === CanvasNodeType.Audio ? ("audio" as const) : ("image" as const);
    const params = input.params && typeof input.params === "object" ? (input.params as Record<string, string | number>) : undefined;
    const metadata: CanvasNodeMetadata = {
        capability: spec.key,
        generationMode,
        ...(model ? { model } : {}),
        ...(typeof input.prompt === "string" && input.prompt ? { prompt: input.prompt } : {}),
        ...(params && Object.keys(params).length ? { capabilityParams: params } : {}),
    };

    const x = typeof input.x === "number" ? input.x : fallbackX;
    const y = typeof input.y === "number" ? input.y : 0;

    return [
        { type: "add_node", id: nodeId, nodeType: spec.output, title: typeof input.title === "string" && input.title ? input.title : spec.label, position: { x, y }, metadata },
        ...sourceNodeIds.map((fromNodeId) => ({ type: "connect_nodes" as const, fromNodeId, toNodeId: nodeId })),
        { type: "select_nodes", ids: [nodeId] },
        ...(input.autoRun === true ? [{ type: "run_generation" as const, nodeId, mode: generationMode }] : []),
    ];
}
