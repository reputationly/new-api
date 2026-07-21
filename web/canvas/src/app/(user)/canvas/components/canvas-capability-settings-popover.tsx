"use client";

// 能力节点的模型 + 参数设置弹层(设计文档 §3.6)。
// - 模型下拉 = 用户可用模型 ∩ 运营设置声明该能力标签的模型(use-media-config-store);
// - 尺寸/时长参数 = 该模型在 MediaModelConfig 的白名单原样呈现:配置写宽高比("1:1")
//   就只出宽高比,写分辨率("1024x1024"/"720P")就只出分辨率,只配一个值(如仅 6s)则锁定;
//   未配置该维度 → 自由输入(后端不校验该维度);
// - 切换模型时自动把已选参数收敛进新模型的白名单。

import { useEffect, useRef, useState, type RefObject } from "react";
import { createPortal } from "react-dom";
import { Settings2 } from "lucide-react";
import { Button, Input, InputNumber, Select } from "antd";

import { canvasThemes } from "@/lib/canvas-theme";
import { useThemeStore } from "@/stores/use-theme-store";
import type { CapabilitySpec, InputSlotKind, ParamSpec, SelectOption } from "@/services/capabilities/registry";
import { groupsForModel, modelsForCapability, paramOptionsForModel, useMediaConfigStore, type MediaModelEntry } from "@/stores/use-media-config-store";
import type { CanvasNodeData } from "../types";

export type UpstreamMediaNode = { id: string; title: string; kind: InputSlotKind };

type CanvasCapabilitySettingsPopoverProps = {
    node: CanvasNodeData;
    spec: CapabilitySpec;
    onConfigChange: (nodeId: string, patch: Partial<CanvasNodeData["metadata"]>) => void;
    /** 上游媒体节点(槽位指定用,§3.5);未传则不渲染输入分配区 */
    upstreamMedia?: UpstreamMediaNode[];
    buttonClassName?: string;
};

/** 把已选参数收敛进模型白名单:白名单存在且当前值不在其中 → 取白名单首项 */
export function conformParamsToEntry(spec: CapabilitySpec, params: Record<string, string | number>, entry: MediaModelEntry): Record<string, string | number> {
    const next = { ...params };
    for (const param of spec.params) {
        const whitelist = param.options === "sizes" ? entry.sizes : param.options === "durations" ? entry.durations : null;
        if (!whitelist || !whitelist.length) continue;
        const current = String(next[param.key] ?? "");
        if (!current || !whitelist.includes(current)) next[param.key] = whitelist[0];
    }
    return next;
}

export function CanvasCapabilitySettingsPopover({ node, spec, onConfigChange, upstreamMedia = [], buttonClassName }: CanvasCapabilitySettingsPopoverProps) {
    const theme = canvasThemes[useThemeStore((state) => state.theme)];
    const configs = useMediaConfigStore((state) => state.configs);
    const availableModels = useMediaConfigStore((state) => state.availableModels);
    const groups = useMediaConfigStore((state) => state.groups);
    const modelGroups = useMediaConfigStore((state) => state.modelGroups);
    const loading = useMediaConfigStore((state) => state.loading);
    const ensureLoaded = useMediaConfigStore((state) => state.ensureLoaded);
    const buttonRef = useRef<HTMLSpanElement>(null);
    const panelRef = useRef<HTMLDivElement>(null);
    const [open, setOpen] = useState(false);
    const [buttonRect, setButtonRect] = useState<DOMRect | null>(null);

    useEffect(() => {
        void ensureLoaded();
    }, [ensureLoaded]);

    useEffect(() => {
        if (!open) return;
        const syncPosition = () => setButtonRect(buttonRef.current?.getBoundingClientRect() || null);
        const closeOnOutsidePointer = (event: PointerEvent) => {
            const target = event.target;
            if (!(target instanceof Node)) return;
            if (buttonRef.current?.contains(target) || panelRef.current?.contains(target)) return;
            // antd Select 下拉挂 body,点击选项不应关闭
            if (target instanceof Element && target.closest(".ant-select-dropdown")) return;
            setOpen(false);
        };
        syncPosition();
        window.addEventListener("resize", syncPosition);
        window.addEventListener("scroll", syncPosition, true);
        window.addEventListener("pointerdown", closeOnOutsidePointer, true);
        return () => {
            window.removeEventListener("resize", syncPosition);
            window.removeEventListener("scroll", syncPosition, true);
            window.removeEventListener("pointerdown", closeOnOutsidePointer, true);
        };
    }, [open]);

    const models = modelsForCapability({ configs, availableModels }, spec);
    const model = node.metadata?.model || "";
    const entry = paramOptionsForModel(configs, spec.modality, model);
    const params = node.metadata?.capabilityParams || {};
    // 按所选模型收窄分组:只列该模型已启用的分组,避免选到「无可用渠道」的分组(§3.2)
    const groupOptions = groupsForModel({ groups, modelGroups }, model);

    const applyModel = (nextModel: string) => {
        const nextEntry = paramOptionsForModel(configs, spec.modality, nextModel);
        const patch: Partial<CanvasNodeData["metadata"]> = { model: nextModel, capabilityParams: conformParamsToEntry(spec, params, nextEntry) };
        // 换模型后原分组若不在新模型的可用分组内 → 清空回落默认分组,不残留会失败的选择
        const currentGroup = node.metadata?.group;
        if (currentGroup && !groupsForModel({ groups, modelGroups }, nextModel).some((option) => option.value === currentGroup)) {
            patch.group = undefined;
        }
        // 图片能力(同步链路)复用既有 metadata.size 语义
        if (spec.channel !== "task") {
            const size = patch.capabilityParams?.size;
            if (size !== undefined) patch.size = String(size);
        }
        onConfigChange(node.id, patch);
    };

    const applyParam = (key: string, value: string | number | null) => {
        const nextParams = { ...params };
        if (value === null || value === "") delete nextParams[key];
        else nextParams[key] = value;
        const patch: Partial<CanvasNodeData["metadata"]> = { capabilityParams: nextParams };
        if (spec.channel !== "task" && key === "size") patch.size = value === null ? undefined : String(value);
        onConfigChange(node.id, patch);
    };

    // 槽位指定(§3.5):某上游节点当前绑定的槽位 key(未绑定 = undefined = 自动)
    const bindings = node.metadata?.slotBindings || {};
    const boundSlotOf = (upstreamId: string) => Object.keys(bindings).find((slotKey) => (bindings[slotKey] || []).includes(upstreamId));
    const rebindUpstream = (upstreamId: string, slotKey: string | null) => {
        const next: Record<string, string[]> = {};
        for (const [key, ids] of Object.entries(bindings)) {
            const kept = (ids || []).filter((id) => id !== upstreamId);
            if (kept.length) next[key] = kept;
        }
        if (slotKey) next[slotKey] = [...(next[slotKey] || []), upstreamId];
        onConfigChange(node.id, { slotBindings: Object.keys(next).length ? next : undefined });
    };
    const assignableUpstream = upstreamMedia.filter((item) => spec.inputs.some((slot) => slot.key !== "prompt" && slot.kind === item.kind));
    // 仅当存在同 kind 多槽位、多上游或可选槽位歧义时才需要人工指定;单槽单上游自动分配即可
    const showSlotAssignment = assignableUpstream.length > 0 && spec.inputs.filter((slot) => slot.key !== "prompt").length > 1;

    const summary = model ? `${model}` : "选择模型";
    const panel =
        open && buttonRect ? (
            <CapabilityPanelPortal buttonRect={buttonRect} panelRef={panelRef} theme={theme}>
                <div className="text-sm font-semibold">{spec.label}</div>
                <div>
                    <div className="mb-1 text-xs opacity-60">分组</div>
                    <Select
                        className="w-full"
                        value={node.metadata?.group || undefined}
                        placeholder="默认分组(跟随账户)"
                        options={groupOptions.map((group) => ({ value: group.value, label: group.ratio !== undefined && group.ratio !== "" ? `${group.label}(倍率 ${group.ratio})` : group.label }))}
                        onChange={(value) => onConfigChange(node.id, { group: value || undefined })}
                        allowClear
                        notFoundContent={loading ? "加载中…" : model ? "该模型无可选分组" : "无可选分组"}
                    />
                </div>
                <div>
                    <div className="mb-1 text-xs opacity-60">模型</div>
                    <Select
                        className="w-full"
                        showSearch
                        value={model || undefined}
                        placeholder={loading ? "加载模型中…" : models.length ? "选择模型" : "无可用模型"}
                        options={models.map((name) => ({ value: name, label: name }))}
                        onChange={applyModel}
                        notFoundContent={loading ? "加载中…" : "无可用模型:请在运营设置为模型声明该能力"}
                    />
                </div>
                {showSlotAssignment ? (
                    <div>
                        <div className="mb-1 text-xs opacity-60">输入分配(未指定按连线顺序自动)</div>
                        <div className="space-y-2">
                            {assignableUpstream.map((item) => {
                                const compatible = spec.inputs.filter((slot) => slot.key !== "prompt" && slot.kind === item.kind);
                                return (
                                    <div key={item.id} className="flex items-center gap-2">
                                        <span className="min-w-0 flex-1 truncate text-xs" title={item.title}>
                                            {item.title}
                                        </span>
                                        <Select
                                            className="w-[160px]"
                                            size="small"
                                            value={boundSlotOf(item.id)}
                                            placeholder="自动"
                                            options={compatible.map((slot) => ({ value: slot.key, label: slot.role }))}
                                            onChange={(value) => rebindUpstream(item.id, value || null)}
                                            allowClear
                                        />
                                    </div>
                                );
                            })}
                        </div>
                    </div>
                ) : null}
                {spec.params.map((param) => (
                    <CapabilityParamField key={param.key} param={param} entry={entry} value={params[param.key]} onChange={(value) => applyParam(param.key, value)} />
                ))}
                {entry.maxChars ? <div className="text-[11px] opacity-50">该模型文本上限 {entry.maxChars} 字</div> : null}
            </CapabilityPanelPortal>
        ) : null;

    return (
        <>
            <span ref={buttonRef} className="inline-flex min-w-0">
                <Button
                    size="small"
                    type="text"
                    className={buttonClassName || "!h-10 !max-w-[220px] !justify-start !rounded-full !px-3"}
                    style={{ background: theme.node.fill, color: theme.node.text }}
                    icon={<Settings2 className="size-3.5" />}
                    onClick={() => setOpen((current) => !current)}
                >
                    <span className="truncate">
                        {spec.label} · {summary}
                    </span>
                </Button>
            </span>
            {panel}
        </>
    );
}

function CapabilityParamField({ param, entry, value, onChange }: { param: ParamSpec; entry: MediaModelEntry; value: string | number | undefined; onChange: (value: string | number | null) => void }) {
    const rawOptions = param.options === "sizes" ? entry.sizes : param.options === "durations" ? entry.durations : Array.isArray(param.options) ? param.options : null;
    // 归一为 {value,label}:注册表固定列表可带展示名(SelectOption),配置白名单为纯字符串
    const whitelist = rawOptions ? rawOptions.map((item: string | SelectOption) => (typeof item === "string" ? { value: item, label: item } : item)) : null;

    return (
        <div>
            <div className="mb-1 text-xs opacity-60">
                {param.label}
                {param.required ? <span className="ml-1 text-red-400">*</span> : null}
                {whitelist && whitelist.length === 1 ? <span className="ml-1 opacity-60">(该模型仅支持 {whitelist[0].label})</span> : null}
            </div>
            {whitelist && whitelist.length ? (
                <Select
                    className="w-full"
                    value={value === undefined || value === "" ? undefined : String(value)}
                    placeholder={param.placeholder || `选择${param.label}`}
                    disabled={whitelist.length === 1 && String(value ?? "") === whitelist[0].value}
                    options={whitelist}
                    onChange={(next) => onChange(next)}
                    allowClear={whitelist.length > 1}
                />
            ) : param.type === "number" ? (
                <InputNumber className="!w-full" value={value === undefined ? null : Number(value)} min={param.min} max={param.max} step={param.step} placeholder={param.placeholder} onChange={(next) => onChange(next === null ? null : Number(next))} />
            ) : param.type === "textarea" ? (
                <Input.TextArea rows={3} value={value === undefined ? "" : String(value)} placeholder={param.placeholder} onChange={(event) => onChange(event.target.value)} />
            ) : (
                <Input value={value === undefined ? "" : String(value)} placeholder={param.placeholder || (param.options === "sizes" ? "未配置白名单,自由填写(如 1:1 或 1024x1024)" : undefined)} onChange={(event) => onChange(event.target.value)} />
            )}
        </div>
    );
}

function CapabilityPanelPortal({ buttonRect, panelRef, theme, children }: { buttonRect: DOMRect; panelRef: RefObject<HTMLDivElement | null>; theme: (typeof canvasThemes)[keyof typeof canvasThemes]; children: React.ReactNode }) {
    const width = 356;
    const gap = 8;
    const margin = 12;
    const style = {
        position: "fixed",
        zIndex: 1200,
        width,
        left: Math.max(margin, Math.min(window.innerWidth - width - margin, buttonRect.left)),
        bottom: window.innerHeight - buttonRect.top + gap,
        maxHeight: Math.max(260, buttonRect.top - margin * 2),
        background: theme.toolbar.panel,
        borderRadius: 18,
        boxShadow: "0 18px 54px rgba(28, 25, 23, 0.16)",
        padding: 18,
        overflowY: "auto",
        color: theme.node.text,
    } as const;

    return createPortal(
        <div ref={panelRef} className="space-y-3" style={style} onPointerDown={(event) => event.stopPropagation()} onMouseDown={(event) => event.stopPropagation()} onClick={(event) => event.stopPropagation()}>
            {children}
        </div>,
        document.body,
    );
}
