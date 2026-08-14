// Agent 的图结构工具:定向上下游遍历 + 按 DAG 拓扑自动排版 + 等待生成任务落地。
//
// 为什么需要定向遍历:canvas_get_state 会把整张画布 dump 给模型,几十个节点时提示词
// 里塞满无关内容,既贵又容易让模型抓错 id。「这张图是从哪来的」「它生成了什么」
// 这类问题只需要一两跳邻居。
//
// 为什么需要 arrange:Agent 原来只能用 canvas_move_nodes 手填绝对坐标,它对布局没有
// 全局视野,结果往往重叠或跑飞。按连线拓扑分层排版是确定性计算,交给代码做。
//
// 为什么需要 wait:视频/音频任务是异步的,提交后 status=loading。Agent 不等它落地就
// 接下游(超分、配乐、续接),拿到的是空内容。

import type { ResponseFunctionTool } from "@/services/api/image";
import { NODE_DEFAULT_SIZE } from "../constants";
import { groupRectFor } from "../utils/canvas-group";
import { CanvasNodeType, type CanvasNodeData } from "../types";
import type { CanvasAgentOp, CanvasAgentSnapshot } from "../utils/canvas-agent-ops";

/** 终态:到了这几个状态就不会再自己变了 */
const SETTLED_STATUS = new Set(["success", "error", "stalled"]);
/** 排版:层间距与同层间距 */
const LAYER_GAP = 160;
const ROW_GAP = 80;

export const GRAPH_AGENT_TOOLS: ResponseFunctionTool[] = [
    tool("canvas_get_node", "按 id 读取单个节点。已经知道 id 时用它，不要为看一个节点去 dump 整张画布。", { nodeId: { type: "string" } }, ["nodeId"]),
    tool(
        "canvas_get_upstream_nodes",
        "读取指定节点的上游（它的输入来自哪些节点）。回答“这个是基于什么生成的”“它的来源”时用，比 canvas_get_state 省得多。",
        { nodeId: { type: "string" }, depth: { type: "number", description: "向上追溯几跳，默认 1（直接上游）" } },
        ["nodeId"],
    ),
    tool("canvas_get_downstream_nodes", "读取指定节点的下游（哪些节点用它作输入）。回答“它生成了什么”“后续结果”时用。", { nodeId: { type: "string" }, depth: { type: "number", description: "向下追溯几跳，默认 1（直接下游）" } }, ["nodeId"]),
    tool("canvas_get_connected_nodes", "一次读取指定节点的直接上游 + 直接下游。回答“相关内容”“上下游”时用。", { nodeId: { type: "string" } }, ["nodeId"]),
    tool(
        "canvas_arrange_nodes",
        "按连线拓扑自动排版：横向按管线阶段分层（上游在左、下游在右），纵向排开同层的并列节点。整理画布时用它，不要自己算坐标去调 canvas_move_nodes。只改位置，不动内容和连线。",
        {
            nodeIds: { type: "array", items: { type: "string" }, description: "只排这些节点；缺省排全部" },
            direction: { type: "string", enum: ["horizontal", "vertical"], description: "horizontal（默认）按阶段横向分层；vertical 纵向分层" },
            originX: { type: "number", description: "排版起点 x，缺省沿用当前最左位置" },
            originY: { type: "number", description: "排版起点 y，缺省沿用当前最上位置" },
        },
        [],
    ),
    tool(
        "canvas_create_group",
        "把若干节点框进一个分组里。分组只表达「同属一个角色/场景/章节」，不参与生成、不传递输入——生产依赖一律用连线。归属靠几何包含：分组框会自动包住这批节点，之后谁被拖进框里谁就入组。",
        { nodeIds: { type: "array", items: { type: "string" }, minItems: 1 }, title: { type: "string", description: "分组名，如「主角参考」「第二幕」" }, color: { type: "string", description: "十六进制色值，如 #4F8EF7" } },
        ["nodeIds"],
    ),
    tool(
        "canvas_wait_generation",
        "等待指定节点的生成任务落地后再返回（success / error / stalled）。视频、音频等异步任务提交后是 loading，必须等它真的完成才能接下游（超分、配乐、续接、截帧）。不要用轮询 canvas_get_state 代替。",
        { nodeIds: { type: "array", items: { type: "string" }, minItems: 1 }, timeoutSeconds: { type: "number", description: "最长等待秒数，默认 180，上限 600" } },
        ["nodeIds"],
    ),
];

function tool(name: string, description: string, properties: Record<string, unknown>, required: string[]): ResponseFunctionTool {
    return { type: "function", function: { name, description, parameters: { type: "object", properties, required, additionalProperties: false }, strict: false } };
}

/** 给模型看的精简节点:够它判断类型、状态、内容大意和引用关系,不塞 base64 */
export function briefNode(node: CanvasNodeData) {
    return {
        id: node.id,
        type: node.type,
        title: node.title,
        capability: node.metadata?.capability,
        // 素材语义角色:决定这份素材该填进哪个输入槽位(角色图 vs 场景图 vs 道具图)
        assetRole: node.metadata?.assetRole,
        status: node.metadata?.status,
        model: node.metadata?.model,
        prompt: String(node.metadata?.prompt || node.metadata?.composerContent || "").slice(0, 300),
        text: node.type === CanvasNodeType.Text ? String(node.metadata?.content || "").slice(0, 500) : undefined,
        hasContent: Boolean(node.metadata?.content),
        position: node.position,
    };
}

export function getNodeResult(snapshot: CanvasAgentSnapshot, nodeId: string) {
    const node = snapshot.nodes.find((item) => item.id === nodeId);
    if (!node) return { ok: false as const, message: `节点不存在：${nodeId}。请先用 canvas_get_state 读取真实节点 id。` };
    return { ok: true as const, message: `节点 ${node.title || node.id}（${node.type}）。`, data: briefNode(node) };
}

/** 定向遍历。direction=up 找输入来源,down 找派生结果 */
export function traverseResult(snapshot: CanvasAgentSnapshot, nodeId: string, direction: "up" | "down" | "both", depth = 1) {
    const node = snapshot.nodes.find((item) => item.id === nodeId);
    if (!node) return { ok: false as const, message: `节点不存在：${nodeId}。请先用 canvas_get_state 读取真实节点 id。` };

    const walk = (dir: "up" | "down") => {
        const byId = new Map(snapshot.nodes.map((item) => [item.id, item]));
        const seen = new Set([nodeId]);
        const layers: Array<Array<ReturnType<typeof briefNode>>> = [];
        let frontier = [nodeId];
        for (let hop = 0; hop < Math.max(1, Math.min(depth, 8)); hop++) {
            const next: string[] = [];
            for (const id of frontier) {
                for (const conn of snapshot.connections) {
                    const neighbor = dir === "up" ? (conn.toNodeId === id ? conn.fromNodeId : null) : conn.fromNodeId === id ? conn.toNodeId : null;
                    if (neighbor && !seen.has(neighbor) && byId.has(neighbor)) {
                        seen.add(neighbor);
                        next.push(neighbor);
                    }
                }
            }
            if (!next.length) break;
            layers.push(next.map((id) => briefNode(byId.get(id)!)));
            frontier = next;
        }
        return layers;
    };

    if (direction === "both") {
        const upstream = walk("up")[0] || [];
        const downstream = walk("down")[0] || [];
        return { ok: true as const, message: `${node.title || nodeId}：直接上游 ${upstream.length} 个，直接下游 ${downstream.length} 个。`, data: { node: briefNode(node), upstream, downstream } };
    }
    const layers = walk(direction);
    const flat = layers.flat();
    const label = direction === "up" ? "上游" : "下游";
    return { ok: true as const, message: flat.length ? `${node.title || nodeId} 的${label}共 ${flat.length} 个（${layers.length} 跳）。` : `${node.title || nodeId} 没有${label}节点。`, data: { node: briefNode(node), byHop: layers, all: flat } };
}

/**
 * 按 DAG 拓扑排版。层号取「最长路径」而不是 BFS 最短路径——最短路径会把一个
 * 三步链的中间产物和它的旁支排到同一列,看起来像并列关系;最长路径能保证任何一条边
 * 都严格指向右边一列,管线方向一眼可读。
 */
export function arrangeNodesOps(input: Record<string, unknown>, snapshot: CanvasAgentSnapshot): CanvasAgentOp[] {
    const requested = Array.isArray(input.nodeIds) ? input.nodeIds.filter((id): id is string => typeof id === "string") : [];
    const missing = requested.filter((id) => !snapshot.nodes.some((node) => node.id === id));
    if (missing.length) throw new Error(`节点不存在：${missing.join("、")}。请先用 canvas_get_state 读取真实节点 id。`);

    // 分组不参与拓扑排版:它没有连线,会被算成第 0 层从而与成员分离。
    // 分组框的位置由它包住谁决定,排完成员后另行重算。
    const pool = snapshot.nodes.filter((node) => node.type !== CanvasNodeType.Group);
    const targets = requested.length ? pool.filter((node) => requested.includes(node.id)) : pool;
    if (!targets.length) throw new Error("画布上没有可排版的节点。");

    const ids = new Set(targets.map((node) => node.id));
    // 只考虑两端都在排版集合内的边:排版一个子集时,与集合外的连线不该影响分层
    const edges = snapshot.connections.filter((conn) => ids.has(conn.fromNodeId) && ids.has(conn.toNodeId));

    const layer = new Map<string, number>(targets.map((node) => [node.id, 0]));
    // 迭代松弛求最长路径。上限 targets.length 轮:无环时必定收敛,有环时到轮次上限
    // 强制停止(画布理论上不该有环,但 Agent 可能连出来,这里不能挂死)。
    for (let round = 0; round < targets.length; round++) {
        let changed = false;
        for (const conn of edges) {
            const next = (layer.get(conn.fromNodeId) ?? 0) + 1;
            if (next > (layer.get(conn.toNodeId) ?? 0)) {
                layer.set(conn.toNodeId, next);
                changed = true;
            }
        }
        if (!changed) break;
    }

    const vertical = input.direction === "vertical";
    const byLayer = new Map<number, CanvasNodeData[]>();
    for (const node of targets) {
        const index = layer.get(node.id) ?? 0;
        byLayer.set(index, [...(byLayer.get(index) || []), node]);
    }

    const originX = typeof input.originX === "number" ? input.originX : Math.min(...targets.map((node) => node.position.x));
    const originY = typeof input.originY === "number" ? input.originY : Math.min(...targets.map((node) => node.position.y));

    const ops: CanvasAgentOp[] = [];
    let cursor = vertical ? originY : originX;
    for (const index of [...byLayer.keys()].sort((a, b) => a - b)) {
        // 同层内沿用当前相对次序(纵向排版时按 x),避免排版把用户已有的排列打乱
        const nodes = (byLayer.get(index) || []).slice().sort((a, b) => (vertical ? a.position.x - b.position.x : a.position.y - b.position.y));
        let offset = vertical ? originX : originY;
        let extent = 0;
        for (const node of nodes) {
            const width = node.width || NODE_DEFAULT_SIZE[node.type]?.width || 320;
            const height = node.height || NODE_DEFAULT_SIZE[node.type]?.height || 220;
            const position = vertical ? { x: offset, y: cursor } : { x: cursor, y: offset };
            ops.push({ type: "update_node", id: node.id, patch: { position } });
            offset += (vertical ? width : height) + ROW_GAP;
            extent = Math.max(extent, vertical ? height : width);
        }
        cursor += extent + LAYER_GAP;
    }

    // 成员移动后,分组框要重新包住它们(按新坐标算);组内已空的分组保持原样不动
    const movedById = new Map(ops.filter((op): op is Extract<CanvasAgentOp, { type: "update_node" }> => op.type === "update_node").map((op) => [op.id, op.patch?.position]));
    for (const group of snapshot.nodes) {
        if (group.type !== CanvasNodeType.Group) continue;
        const members = snapshot.nodes.filter((node) => node.metadata?.groupId === group.id).map((node) => ({ ...node, position: movedById.get(node.id) || node.position }));
        if (!members.length) continue;
        const rect = groupRectFor(members);
        ops.push({ type: "update_node", id: group.id, patch: { position: rect.position, width: rect.width, height: rect.height } });
    }
    return ops;
}

/** canvas_create_group → ops。分组框按成员包围盒摆放,成员的 groupId 由几何包含算出 */
export function createGroupOps(input: Record<string, unknown>, snapshot: CanvasAgentSnapshot): CanvasAgentOp[] {
    const requested = Array.isArray(input.nodeIds) ? input.nodeIds.filter((id): id is string => typeof id === "string") : [];
    if (!requested.length) throw new Error("nodeIds 不能为空。");
    const missing = requested.filter((id) => !snapshot.nodes.some((node) => node.id === id));
    if (missing.length) throw new Error(`节点不存在：${missing.join("、")}。请先用 canvas_get_state 读取真实节点 id。`);

    const members = snapshot.nodes.filter((node) => requested.includes(node.id) && node.type !== CanvasNodeType.Group);
    if (!members.length) throw new Error("这批 id 里没有可分组的节点（分组本身不能再被分组）。");

    const rect = groupRectFor(members);
    const groupId = `group-${simpleId(snapshot)}`;
    return [
        {
            type: "add_node",
            id: groupId,
            nodeType: CanvasNodeType.Group,
            title: typeof input.title === "string" && input.title ? input.title : "分组",
            position: rect.position,
            width: rect.width,
            height: rect.height,
            metadata: { status: "idle", ...(typeof input.color === "string" && input.color ? { groupColor: input.color } : {}) },
        },
        // groupId 写进成员是几何判定结果的缓存,拖拽后由 reassignGroups 重算
        ...members.map((node) => ({ type: "update_node" as const, id: node.id, metadata: { groupId } })),
        { type: "select_nodes", ids: [groupId] },
    ];
}

// 不用 nanoid:本模块只在 agent 侧组 ops,避免为一个 id 再引一个依赖
function simpleId(snapshot: CanvasAgentSnapshot) {
    return `${snapshot.nodes.length}${Math.abs(hashString(snapshot.nodes.map((node) => node.id).join(""))).toString(36)}`;
}

function hashString(value: string) {
    let hash = 0;
    for (let i = 0; i < value.length; i++) hash = (hash * 31 + value.charCodeAt(i)) | 0;
    return hash;
}

export type WaitTarget = { id: string; status: string; title: string; hasContent: boolean };

/** 判断一批节点是否都已落地(不再是 loading/idle) */
export function collectWaitTargets(snapshot: CanvasAgentSnapshot, nodeIds: string[]): { pending: WaitTarget[]; settled: WaitTarget[] } {
    const pending: WaitTarget[] = [];
    const settled: WaitTarget[] = [];
    for (const id of nodeIds) {
        const node = snapshot.nodes.find((item) => item.id === id);
        if (!node) {
            settled.push({ id, status: "missing", title: "", hasContent: false });
            continue;
        }
        const status = node.metadata?.status || "idle";
        const target: WaitTarget = { id, status, title: node.title, hasContent: Boolean(node.metadata?.content) };
        // idle + 已有内容 = 本来就是现成素材(上传的图/视频),不需要等
        if (SETTLED_STATUS.has(status) || (status === "idle" && target.hasContent)) settled.push(target);
        else pending.push(target);
    }
    return { pending, settled };
}

export function summarizeWaitResult(targets: WaitTarget[], timedOut: boolean) {
    const ok = targets.filter((item) => item.status === "success");
    const failed = targets.filter((item) => item.status === "error");
    const stalled = targets.filter((item) => item.status === "stalled");
    const missing = targets.filter((item) => item.status === "missing");
    const parts = [
        ok.length ? `${ok.length} 个成功` : "",
        failed.length ? `${failed.length} 个失败` : "",
        stalled.length ? `${stalled.length} 个轮询超时但仍在服务端运行（可继续等待，不要重复提交）` : "",
        missing.length ? `${missing.length} 个节点已不存在` : "",
        timedOut ? "等待超时，其余仍在生成中" : "",
    ].filter(Boolean);
    return parts.join("，") || "全部已落地。";
}
