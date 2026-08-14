"use client";

// 左侧节点树。节点一多,靠在无限画布上平移找东西就不现实了——这里按分组/类型
// 归拢成一份可搜索的清单,点一下定位过去。
//
// 只读 + 定位,不做任何编辑:改名、删除、连线都还在画布上做,避免两处入口行为不一致。

import { useMemo, useState } from "react";
import { ChevronDown, ChevronRight, Frame, Image as ImageIcon, Music2, PanelLeftClose, PanelLeftOpen, Search, Settings2, Type, Video } from "lucide-react";
import { Input } from "antd";

import { canvasThemes } from "@/lib/canvas-theme";
import { useThemeStore } from "@/stores/use-theme-store";
import { capabilitySpec } from "@/services/capabilities/registry";
import { CanvasNodeType, type CanvasNodeData } from "../types";

const TYPE_META: Record<string, { label: string; icon: typeof Type }> = {
    [CanvasNodeType.Text]: { label: "文本", icon: Type },
    [CanvasNodeType.Image]: { label: "图片", icon: ImageIcon },
    [CanvasNodeType.Video]: { label: "视频", icon: Video },
    [CanvasNodeType.Audio]: { label: "音频", icon: Music2 },
    [CanvasNodeType.Config]: { label: "生成配置", icon: Settings2 },
};
// 清单里的固定顺序,不随节点创建顺序抖动
const TYPE_ORDER = [CanvasNodeType.Text, CanvasNodeType.Image, CanvasNodeType.Video, CanvasNodeType.Audio, CanvasNodeType.Config];

type Section = { key: string; title: string; icon: typeof Type; nodes: CanvasNodeData[] };

export function CanvasSidePanel({ nodes, selectedNodeIds, open, onToggle, onFocusNode }: { nodes: CanvasNodeData[]; selectedNodeIds: Set<string>; open: boolean; onToggle: () => void; onFocusNode: (nodeId: string) => void }) {
    const theme = canvasThemes[useThemeStore((state) => state.theme)];
    const [keyword, setKeyword] = useState("");
    const [collapsed, setCollapsed] = useState<Set<string>>(new Set());

    const sections = useMemo(() => {
        const word = keyword.trim().toLowerCase();
        const match = (node: CanvasNodeData) => {
            if (!word) return true;
            const spec = capabilitySpec(node.metadata?.capability);
            return [node.title, node.metadata?.prompt, node.metadata?.content, spec?.label].filter(Boolean).some((text) => String(text).toLowerCase().includes(word));
        };

        const result: Section[] = [];
        const grouped = new Set<string>();

        // 分组在前:每个分组一节,成员按类型顺序排在组内
        for (const group of nodes.filter((node) => node.type === CanvasNodeType.Group)) {
            const members = nodes.filter((node) => node.metadata?.groupId === group.id && match(node));
            members.forEach((node) => grouped.add(node.id));
            if (!members.length && !match(group)) continue;
            result.push({ key: `group:${group.id}`, title: group.title || "分组", icon: Frame, nodes: sortByType(members) });
        }

        // 未入组的按类型归类
        for (const type of TYPE_ORDER) {
            const items = nodes.filter((node) => node.type === type && !grouped.has(node.id) && !node.metadata?.groupId && match(node));
            if (!items.length) continue;
            result.push({ key: `type:${type}`, title: TYPE_META[type].label, icon: TYPE_META[type].icon, nodes: items });
        }
        return result;
    }, [keyword, nodes]);

    const total = nodes.filter((node) => node.type !== CanvasNodeType.Group).length;

    if (!open) {
        return (
            <button
                type="button"
                className="pointer-events-auto absolute left-4 top-20 z-40 grid size-9 place-items-center rounded-xl border shadow-lg backdrop-blur transition hover:opacity-80"
                style={{ background: theme.toolbar.panel, borderColor: theme.toolbar.border, color: theme.toolbar.item }}
                aria-label="展开节点列表"
                title="节点列表"
                onClick={onToggle}
            >
                <PanelLeftOpen className="size-4.5" />
            </button>
        );
    }

    return (
        <div
            className="pointer-events-auto absolute bottom-24 left-4 top-20 z-40 flex w-[272px] flex-col overflow-hidden rounded-xl border shadow-lg backdrop-blur"
            style={{ background: theme.toolbar.panel, borderColor: theme.toolbar.border, color: theme.node.text }}
            onWheel={(event) => event.stopPropagation()}
            onPointerDown={(event) => event.stopPropagation()}
        >
            <div className="flex items-center justify-between border-b px-3 py-2.5" style={{ borderColor: theme.toolbar.border }}>
                <span className="text-sm font-medium">节点列表 · {total}</span>
                <button type="button" className="grid size-7 place-items-center rounded-lg transition hover:opacity-70" style={{ color: theme.node.muted }} aria-label="收起节点列表" onClick={onToggle}>
                    <PanelLeftClose className="size-4" />
                </button>
            </div>

            <div className="px-3 py-2">
                <Input allowClear size="small" value={keyword} placeholder="搜索标题或提示词" prefix={<Search className="size-3.5" style={{ color: theme.node.faint }} />} onChange={(event) => setKeyword(event.target.value)} />
            </div>

            <div className="thin-scrollbar flex-1 overflow-y-auto px-2 pb-2">
                {sections.length ? (
                    sections.map((section) => {
                        const isCollapsed = collapsed.has(section.key);
                        const Icon = section.icon;
                        return (
                            <div key={section.key} className="mb-1">
                                <button
                                    type="button"
                                    className="flex w-full items-center gap-1.5 rounded-lg px-2 py-1.5 text-left text-xs transition hover:opacity-80"
                                    style={{ color: theme.node.muted }}
                                    onClick={() =>
                                        setCollapsed((prev) => {
                                            const next = new Set(prev);
                                            if (next.has(section.key)) next.delete(section.key);
                                            else next.add(section.key);
                                            return next;
                                        })
                                    }
                                >
                                    {isCollapsed ? <ChevronRight className="size-3.5 shrink-0" /> : <ChevronDown className="size-3.5 shrink-0" />}
                                    <Icon className="size-3.5 shrink-0" />
                                    <span className="truncate">{section.title}</span>
                                    <span className="ml-auto shrink-0 opacity-60">{section.nodes.length}</span>
                                </button>
                                {isCollapsed
                                    ? null
                                    : section.nodes.map((node) => {
                                          const spec = capabilitySpec(node.metadata?.capability);
                                          const isSelected = selectedNodeIds.has(node.id);
                                          const status = node.metadata?.status;
                                          return (
                                              <button
                                                  key={node.id}
                                                  type="button"
                                                  className="flex w-full items-center gap-2 rounded-lg py-1.5 pl-7 pr-2 text-left text-xs transition"
                                                  style={{ background: isSelected ? theme.toolbar.activeBg : "transparent", color: isSelected ? theme.toolbar.activeText : theme.node.text }}
                                                  onClick={() => onFocusNode(node.id)}
                                              >
                                                  <span className="truncate">{node.title || node.id}</span>
                                                  {spec ? (
                                                      <span className="ml-auto shrink-0 rounded px-1 py-px text-[10px]" style={{ background: theme.node.fill, color: theme.node.muted }}>
                                                          {spec.label}
                                                      </span>
                                                  ) : null}
                                                  {status === "loading" || status === "error" || status === "stalled" ? (
                                                      <span className="shrink-0 text-[10px]" style={{ color: status === "error" ? "#e5484d" : theme.node.faint }}>
                                                          {status === "loading" ? "生成中" : status === "error" ? "失败" : "等待中"}
                                                      </span>
                                                  ) : null}
                                              </button>
                                          );
                                      })}
                            </div>
                        );
                    })
                ) : (
                    <div className="px-3 py-6 text-center text-xs" style={{ color: theme.node.faint }}>
                        {keyword.trim() ? "没有匹配的节点" : "画布暂无节点"}
                    </div>
                )}
            </div>
        </div>
    );
}

function sortByType(nodes: CanvasNodeData[]) {
    return [...nodes].sort((a, b) => TYPE_ORDER.indexOf(a.type) - TYPE_ORDER.indexOf(b.type));
}
