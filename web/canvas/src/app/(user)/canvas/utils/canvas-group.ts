"use client";

// 节点分组。
//
// 归属判定是**几何包含**而不是父子树:节点中心落在分组矩形内即属于它。
// 这样拖进拖出都是自然手势,不需要维护一棵会和位置打架的层级结构。
// metadata.groupId 只是判定结果的缓存,用于渲染标注和快速查询,真值永远是几何位置。
//
// 分组不参与生成:它表达「同属一个角色/场景/章节」,不传递任何输入给下游。
// 生产依赖一律用连线表达。

import { CanvasNodeType, type CanvasNodeData } from "../types";

/** 分组矩形与内部节点的留白 */
export const GROUP_PADDING = 32;
/** 顶部留给标题栏 */
export const GROUP_HEADER = 36;

export function getNodeBounds(nodes: CanvasNodeData[]) {
    return nodes.reduce(
        (bounds, node) => ({
            left: Math.min(bounds.left, node.position.x),
            top: Math.min(bounds.top, node.position.y),
            right: Math.max(bounds.right, node.position.x + node.width),
            bottom: Math.max(bounds.bottom, node.position.y + node.height),
        }),
        { left: Infinity, top: Infinity, right: -Infinity, bottom: -Infinity },
    );
}

/** 把一批节点包起来的分组矩形 */
export function groupRectFor(members: CanvasNodeData[]) {
    const bounds = getNodeBounds(members);
    return {
        position: { x: bounds.left - GROUP_PADDING, y: bounds.top - GROUP_PADDING - GROUP_HEADER },
        width: bounds.right - bounds.left + GROUP_PADDING * 2,
        height: bounds.bottom - bounds.top + GROUP_PADDING * 2 + GROUP_HEADER,
    };
}

function centerInside(node: CanvasNodeData, group: CanvasNodeData) {
    const centerX = node.position.x + node.width / 2;
    const centerY = node.position.y + node.height / 2;
    return centerX >= group.position.x && centerX <= group.position.x + group.width && centerY >= group.position.y && centerY <= group.position.y + group.height;
}

/** 某个节点当前落在哪个分组里。后创建的分组优先(渲染在上层,视觉上就是它) */
export function findContainingGroupId(node: CanvasNodeData, nodes: CanvasNodeData[]): string | undefined {
    if (node.type === CanvasNodeType.Group) return undefined;
    return [...nodes].reverse().find((group) => group.type === CanvasNodeType.Group && group.id !== node.id && centerInside(node, group))?.id;
}

export function groupMemberIds(groupId: string, nodes: CanvasNodeData[]): string[] {
    return nodes.filter((node) => node.type !== CanvasNodeType.Group && node.metadata?.groupId === groupId).map((node) => node.id);
}

/**
 * 拖拽结束后重算归属。只重算真正被移动过的节点——没动的节点归属不该因为
 * 别人移动而改变(除非它所在的分组被移走了,那种情况由分组自身带着成员一起动)。
 */
export function reassignGroups(nodes: CanvasNodeData[], movedIds: Set<string>): CanvasNodeData[] {
    if (!movedIds.size) return nodes;
    const hasGroup = nodes.some((node) => node.type === CanvasNodeType.Group);
    if (!hasGroup) return nodes;

    return nodes.map((node) => {
        if (!movedIds.has(node.id) || node.type === CanvasNodeType.Group) return node;
        const groupId = findContainingGroupId(node, nodes);
        if ((node.metadata?.groupId || undefined) === groupId) return node;
        const metadata = { ...node.metadata };
        if (groupId) metadata.groupId = groupId;
        else delete metadata.groupId;
        return { ...node, metadata };
    });
}

/** 分组随之移动时要一起带走的成员 id(拖分组 = 拖整组) */
export function expandDragIdsWithGroupMembers(dragIds: Set<string>, nodes: CanvasNodeData[]): Set<string> {
    const next = new Set(dragIds);
    for (const node of nodes) {
        if (node.type !== CanvasNodeType.Group || !dragIds.has(node.id)) continue;
        for (const memberId of groupMemberIds(node.id, nodes)) next.add(memberId);
    }
    return next;
}

/**
 * 渲染顺序:分组永远排在最前面(= 最底层),否则矩形会盖住成员节点。
 * 同类之间保持数组原有顺序,避免每次渲染抖动。
 */
export function sortNodesForRender(nodes: CanvasNodeData[]): CanvasNodeData[] {
    const groups: CanvasNodeData[] = [];
    const rest: CanvasNodeData[] = [];
    for (const node of nodes) (node.type === CanvasNodeType.Group ? groups : rest).push(node);
    return groups.length ? [...groups, ...rest] : nodes;
}
