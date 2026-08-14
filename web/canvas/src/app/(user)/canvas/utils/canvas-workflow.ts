"use client";

// 用户自定义工作流:把画布上的一段子图存成可复用的模板,以后一键插回来。
//
// 与内置模板(templates.ts)的区别:内置模板是代码里写死的官方链路,工作流是用户
// 自己攒的。两者插入画布后没有任何区别,都是普通节点。
//
// 存什么、不存什么:
//   存 —— 节点结构、连线、能力/模型/参数、提示词、标题、相对位置
//   不存 —— 媒体二进制与 storageKey、任务 id、生成状态
// 理由同画布项目的序列化策略:工作流是「怎么做」,不是「做出来的东西」。带上产物
// 既撑爆存储,插回来也是一堆别人的旧图。
//
// 变量:提示词里写 {{主体}} 这样的占位符,插入时填一次,替换到所有用到的地方。
// 这是工作流区别于「复制粘贴一段节点」的地方。

import { nanoid } from "nanoid";

import { CanvasNodeType, type CanvasConnection, type CanvasNodeData, type CanvasNodeMetadata } from "../types";

export type CanvasWorkflow = {
    id: string;
    title: string;
    description: string;
    /** 提取出的变量名,按首次出现顺序 */
    variables: string[];
    nodes: CanvasNodeData[];
    connections: CanvasConnection[];
    createdAt: string;
    updatedAt: string;
};

/** 变量占位符:{{名字}},名字里不允许再有大括号 */
const VARIABLE_PATTERN = /\{\{\s*([^{}]+?)\s*\}\}/g;

/** 只有这些字段跟「怎么做」有关,其余(产物、任务、状态)一律不进工作流 */
const KEPT_METADATA_KEYS: Array<keyof CanvasNodeMetadata> = [
    "content",
    "composerContent",
    "prompt",
    "fontSize",
    "generationMode",
    "generationType",
    "model",
    "size",
    "quality",
    "count",
    "seconds",
    "vquality",
    "generateAudio",
    "watermark",
    "audioVoice",
    "audioFormat",
    "audioSpeed",
    "audioInstructions",
    "capability",
    "capabilityParams",
    "group",
    "camera",
    "groupColor",
    "assetRole",
    "freeResize",
    // 槽位绑定是结构信息(哪个上游进哪个槽),属于「怎么做」;里面的节点 id 在
    // instantiateWorkflow 里会重映射到新 id。
    "slotBindings",
];

/** 从画布子图提取工作流。位置归一化到左上角原点,插入时再整体平移 */
export function extractWorkflow(nodes: CanvasNodeData[], connections: CanvasConnection[], title: string, description = ""): CanvasWorkflow {
    if (!nodes.length) throw new Error("请先选中要保存的节点。");
    const ids = new Set(nodes.map((node) => node.id));
    const originX = Math.min(...nodes.map((node) => node.position.x));
    const originY = Math.min(...nodes.map((node) => node.position.y));

    const cleanNodes = nodes.map((node) => ({
        ...node,
        position: { x: node.position.x - originX, y: node.position.y - originY },
        metadata: pickMetadata(node.type, node.metadata),
    }));

    // 只保留两端都在选区内的连线:半截连线插回来会指向不存在的节点
    const innerConnections = connections.filter((conn) => ids.has(conn.fromNodeId) && ids.has(conn.toNodeId));
    const now = new Date().toISOString();
    return {
        id: `wf-${nanoid(8)}`,
        title: title.trim() || "未命名工作流",
        description: description.trim(),
        variables: collectVariables(cleanNodes),
        nodes: cleanNodes,
        connections: innerConnections,
        createdAt: now,
        updatedAt: now,
    };
}

/** 实例化:换全新 id、平移到目标位置、替换变量 */
export function instantiateWorkflow(workflow: CanvasWorkflow, origin: { x: number; y: number }, values: Record<string, string> = {}) {
    const idMap = new Map(workflow.nodes.map((node) => [node.id, `${node.type}-${nanoid(8)}`]));
    const nodes = workflow.nodes.map((node) => ({
        ...node,
        id: idMap.get(node.id)!,
        position: { x: node.position.x + origin.x, y: node.position.y + origin.y },
        title: substitute(node.title, values),
        metadata: substituteMetadata(node.metadata, values, idMap),
    }));
    const connections = workflow.connections.map((conn) => ({ id: `conn-${nanoid(8)}`, fromNodeId: idMap.get(conn.fromNodeId), toNodeId: idMap.get(conn.toNodeId) })).filter((conn): conn is CanvasConnection => Boolean(conn.fromNodeId && conn.toNodeId));
    return { nodes, connections };
}

/** 扫出所有 {{变量}},按首次出现顺序去重 */
export function collectVariables(nodes: CanvasNodeData[]): string[] {
    const found: string[] = [];
    const seen = new Set<string>();
    const scan = (text?: string) => {
        if (!text) return;
        for (const match of text.matchAll(VARIABLE_PATTERN)) {
            const name = match[1].trim();
            if (name && !seen.has(name)) {
                seen.add(name);
                found.push(name);
            }
        }
    };
    for (const node of nodes) {
        scan(node.title);
        scan(node.metadata?.content);
        scan(node.metadata?.prompt);
        scan(node.metadata?.composerContent);
    }
    return found;
}

function pickMetadata(type: CanvasNodeType, metadata?: CanvasNodeMetadata): CanvasNodeMetadata {
    if (!metadata) return {};
    const next: CanvasNodeMetadata = {};
    for (const key of KEPT_METADATA_KEYS) {
        const value = metadata[key];
        if (value !== undefined) (next as Record<string, unknown>)[key] = value;
    }
    // content 对文本节点是正文(必须留),对媒体节点却是产物地址:blob: 刷新即失效,
    // data: 会把工作流 JSON 撑到几 MB。媒体节点一律清空,插回来是等待生成的空节点。
    if (type !== CanvasNodeType.Text) delete next.content;
    next.status = "idle";
    return next;
}

function substitute(text: string, values: Record<string, string>) {
    return text.replace(VARIABLE_PATTERN, (whole, name: string) => {
        const value = values[String(name).trim()];
        // 没填的变量原样留着,用户能一眼看出哪里还没填,而不是变成空字符串
        return value === undefined || value === "" ? whole : value;
    });
}

function substituteMetadata(metadata: CanvasNodeMetadata | undefined, values: Record<string, string>, idMap: Map<string, string>): CanvasNodeMetadata {
    if (!metadata) return {};
    const next: CanvasNodeMetadata = { ...metadata };
    for (const key of ["content", "prompt", "composerContent"] as const) {
        if (typeof next[key] === "string") next[key] = substitute(next[key], values);
    }
    // 提示词里的 @[node:旧id] 引用要跟着换成新 id,否则插回来指向别人的节点
    for (const key of ["prompt", "composerContent"] as const) {
        const text = next[key];
        if (typeof text !== "string") continue;
        next[key] = text.replace(/@\[node:([^\]]+)\]/g, (whole, oldId: string) => {
            const mapped = idMap.get(oldId);
            return mapped ? `@[node:${mapped}]` : whole;
        });
    }
    // 槽位绑定同理。**先按旧 id 过滤再映射**——映射完再查 idMap 恒为 false,会把绑定全清空
    if (next.slotBindings) {
        next.slotBindings = Object.fromEntries(Object.entries(next.slotBindings).map(([slot, list]) => [slot, list.filter((id) => idMap.has(id)).map((id) => idMap.get(id)!)]));
    }
    return next;
}

/** 分组节点在工作流里保留,但成员归属靠几何包含重算,不带旧 groupId */
export function stripStaleGroupIds(nodes: CanvasNodeData[]): CanvasNodeData[] {
    return nodes.map((node) => {
        if (node.type === CanvasNodeType.Group || !node.metadata?.groupId) return node;
        const metadata = { ...node.metadata };
        delete metadata.groupId;
        return { ...node, metadata };
    });
}
