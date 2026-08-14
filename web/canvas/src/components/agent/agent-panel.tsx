import { useState, type PointerEvent as ReactPointerEvent } from "react";
import { motion } from "motion/react";
import { useTranslation } from "react-i18next";

import { LocalAgentPanel } from "./local-agent-panel";
import { CanvasAssistantPanel } from "@/components/canvas/canvas-assistant-panel";
import type { CanvasAgentMode } from "@/components/canvas/canvas-agent-chat-ui";
import { BUILTIN_MODE } from "@/stores/use-config-store";
import type { CanvasAssistantSession } from "@/types/canvas";
import { canvasThemes } from "@/lib/canvas-theme";
import { CANVAS_AGENT_PANEL_MOTION_MS, useAgentStore } from "@/stores/use-agent-store";
import { useThemeStore } from "@/stores/use-theme-store";

const PANEL_MOTION_SECONDS = CANVAS_AGENT_PANEL_MOTION_MS / 1000;

const EMPTY_SESSIONS: CanvasAssistantSession[] = [];
const noop = () => {};

export function AgentPanel() {
    const { t } = useTranslation();
    const theme = canvasThemes[useThemeStore((state) => state.theme)];
    const width = useAgentStore((state) => state.width);
    const [resizing, setResizing] = useState(false);
    const [agentMode, setAgentMode] = useState<CanvasAgentMode>("online");
    const canvasContext = useAgentStore((state) => state.canvasContext);
    const panelMounted = useAgentStore((state) => state.panelMounted);
    const panelOpen = useAgentStore((state) => state.panelOpen);
    const panelClosing = useAgentStore((state) => state.panelClosing);
    const setAgentState = useAgentStore((state) => state.setAgentState);
    const startResize = (event: ReactPointerEvent<HTMLButtonElement>) => {
        event.preventDefault();
        const startX = event.clientX;
        const startWidth = width;
        let nextWidth = startWidth;
        const onMove = (moveEvent: PointerEvent) => {
            nextWidth = Math.min(760, Math.max(360, startWidth + startX - moveEvent.clientX));
            setAgentState({ width: nextWidth });
        };
        const onUp = () => {
            localStorage.setItem("canvas-agent-panel-width", String(nextWidth));
            window.removeEventListener("pointermove", onMove);
            window.removeEventListener("pointerup", onUp);
            setResizing(false);
        };
        setResizing(true);
        window.addEventListener("pointermove", onMove);
        window.addEventListener("pointerup", onUp);
    };

    if (!panelMounted) return null;

    return (
        <motion.div
            className="relative z-[70] flex h-full shrink-0"
            initial={{ width: 0, opacity: 0 }}
            animate={{ width: panelOpen ? width + 1 : 0, opacity: panelOpen ? 1 : 0 }}
            transition={{ duration: resizing ? 0 : PANEL_MOTION_SECONDS, ease: [0.22, 1, 0.36, 1] }}
            style={{ overflow: "clip", pointerEvents: panelOpen && !panelClosing ? undefined : "none" }}
        >
            <motion.aside
                className="relative flex h-full shrink-0 flex-col border-l"
                data-canvas-shortcuts-ignore
                initial={{ x: 48 }}
                animate={{ x: panelClosing ? 28 : 0 }}
                transition={{ duration: resizing ? 0 : PANEL_MOTION_SECONDS, ease: [0.22, 1, 0.36, 1] }}
                style={{ width, background: theme.node.panel, borderColor: theme.node.stroke, color: theme.node.text }}
            >
                <button type="button" className="absolute inset-y-0 left-0 z-40 w-4 -translate-x-1/2 cursor-col-resize" onPointerDown={startResize} aria-label={t("agent.panel.resize")} />
                {/* BUILTIN_MODE: 内置版用我们的 Agent 面板 —— 它自带「在线/本地」切换,
                    在线走 /pg(32 个工具 + 领域技能手册),本地仍是上游的 Codex 桥接。
                    画布上下文从 useAgentStore.canvasContext 取,与上游同一条通道。 */}
                {BUILTIN_MODE && canvasContext ? (
                    <CanvasAssistantPanel
                        nodes={canvasContext.snapshot.nodes}
                        selectedNodeIds={new Set(canvasContext.snapshot.selectedNodeIds)}
                        snapshot={canvasContext.snapshot}
                        sessions={canvasContext.sessions || EMPTY_SESSIONS}
                        activeSessionId={canvasContext.activeSessionId ?? null}
                        onSelectNodeIds={canvasContext.onSelectNodeIds || noop}
                        onSessionsChange={canvasContext.onSessionsChange || noop}
                        onApplyOps={canvasContext.applyOps}
                        canUndoOps={canvasContext.canUndo}
                        onUndoOps={canvasContext.undoOps}
                        onPasteImage={canvasContext.onPasteImage || noop}
                        agentMode={agentMode}
                        onAgentModeChange={setAgentMode}
                        closing={panelClosing}
                        onCollapse={() => setAgentState({ panelOpen: false })}
                    />
                ) : (
                    <LocalAgentPanel embedded />
                )}
            </motion.aside>
        </motion.div>
    );
}
