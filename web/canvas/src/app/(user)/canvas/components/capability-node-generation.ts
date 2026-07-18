// 能力节点的上游输入映射(设计文档 §3.5):把连线上游节点的产物按能力注册表的
// inputs 声明分槽,产出可直接进请求体的槽位值。
//
// 值优先级(每个媒体输入):
//   1. 上游节点是 gpustackplus 任务产物(metadata.taskId 存在)→ `task:<task_id>`
//      (后端 NFS 直读,前端零搬运);
//   2. 本地素材(IndexedDB storageKey / data-url)→ base64 data-url;
//   3. http(s) URL → 原样(后端下载物化)。
// 同 kind 多输入按连线创建顺序填充(flf2v 首帧→尾帧即按连线顺序,见设计文档 Q2)。

import { getMediaBlob } from "@/services/file-storage";
import { imageToDataUrl } from "@/services/image-storage";
import type { CapabilitySpec, InputSlotKind } from "@/services/capabilities/registry";
import type { SlotValues } from "@/services/capabilities/execute";
import { CanvasNodeType, type CanvasConnection, type CanvasNodeData } from "../types";
import { getGenerationResourceNodes } from "../utils/canvas-resource-references";

const NODE_KIND: Partial<Record<CanvasNodeType, InputSlotKind>> = {
    [CanvasNodeType.Image]: "image",
    [CanvasNodeType.Video]: "video",
    [CanvasNodeType.Audio]: "audio",
    [CanvasNodeType.Text]: "text",
};

export type CapabilitySlotResult = {
    slots: SlotValues;
    /** 上游文本节点内容(与既有行为一致,并入提示词) */
    upstreamText: string;
};

async function mediaSlotValue(node: CanvasNodeData): Promise<string | null> {
    const meta = node.metadata;
    if (!meta) return null;
    // 任务产物引用优先:后端同盘直读,免 base64 往返。
    // 仅当节点当前媒体确实来自该任务(storageKey === taskMediaKey)才引用——
    // 媒体被上传/替换后 storageKey 变化即失配,回退到当前内容,不消费旧任务产物。
    if (meta.taskId && meta.status === "success" && meta.storageKey && meta.storageKey === meta.taskMediaKey) return `task:${meta.taskId}`;
    const content = (meta.content || "").trim();
    if (content.startsWith("data:")) return content;
    if (node.type === CanvasNodeType.Image) {
        // 图片存在独立的 image_files store(键 image:*/ca:*),必须用 image-storage 解析器;
        // getMediaBlob 只读 media_files(视频/音频),对图片键恒为 null。
        // imageToDataUrl 同时覆盖 objectURL(blob:)与换设备后经服务端签名 URL 恢复的素材。
        const dataUrl = await imageToDataUrl({ url: content, storageKey: meta.storageKey });
        if (dataUrl) return dataUrl;
    } else if (meta.storageKey) {
        const blob = await getMediaBlob(meta.storageKey);
        if (blob) return blobToDataUrl(blob);
    }
    if (content.startsWith("http://") || content.startsWith("https://")) return content;
    return null;
}

function blobToDataUrl(blob: Blob): Promise<string> {
    return new Promise((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => resolve(String(reader.result || ""));
        reader.onerror = () => reject(new Error("读取本地媒体失败"));
        reader.readAsDataURL(blob);
    });
}

/** 遍历上游资源节点,按 spec.inputs 分槽。同 kind 的多个槽位按声明顺序依次填满。 */
export async function buildCapabilitySlots(spec: CapabilitySpec, nodeId: string, nodes: CanvasNodeData[], connections: CanvasConnection[]): Promise<CapabilitySlotResult> {
    const resourceNodes = getGenerationResourceNodes(nodeId, nodes, connections);
    const valuesByKind: Record<InputSlotKind, string[]> = { text: [], image: [], video: [], audio: [] };
    const texts: string[] = [];

    for (const node of resourceNodes) {
        const kind = NODE_KIND[node.type];
        if (!kind) continue;
        if (kind === "text") {
            const text = (node.metadata?.content || node.metadata?.prompt || "").trim();
            if (text) texts.push(text);
            continue;
        }
        const value = await mediaSlotValue(node);
        if (value) valuesByKind[kind].push(value);
    }

    const slots: SlotValues = {};
    for (const slot of spec.inputs) {
        if (slot.key === "prompt") continue;
        const pool = valuesByKind[slot.kind];
        if (!pool.length) continue;
        const take = slot.max ?? 1;
        slots[slot.key] = pool.splice(0, take);
    }
    return { slots, upstreamText: texts.join("\n\n") };
}
