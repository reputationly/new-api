"use client";

import { useEffect, useRef, useState, type ReactNode, type RefObject } from "react";
import { createPortal } from "react-dom";
import { Camera, ChevronDown, ChevronUp, X } from "lucide-react";
import { Button, Switch, Tooltip } from "antd";

import { canvasThemes, type CanvasTheme } from "@/lib/canvas-theme";
import { useThemeStore } from "@/stores/use-theme-store";
import { APERTURES, APERTURE_META, CAMERA_PROFILES, DEFAULT_CAMERA_CONTROL, FOCAL_LENGTHS, FOCAL_LENGTH_META, LENS_PROFILES, type CameraProfile, type LensProfile } from "../utils/canvas-camera";
import type { CameraControlOptions } from "../types";

type CanvasCameraControlProps = {
    value?: CameraControlOptions;
    onChange: (value: CameraControlOptions) => void;
    buttonClassName?: string;
};

const CAMERA_IDS = CAMERA_PROFILES.map((item) => item.id);
const LENS_IDS = LENS_PROFILES.map((item) => item.id);

export function CanvasCameraControl({ value, onChange, buttonClassName }: CanvasCameraControlProps) {
    const theme = canvasThemes[useThemeStore((state) => state.theme)];
    const buttonRef = useRef<HTMLSpanElement>(null);
    const panelRef = useRef<HTMLDivElement>(null);
    const [open, setOpen] = useState(false);
    const [buttonRect, setButtonRect] = useState<DOMRect | null>(null);
    const control = value || DEFAULT_CAMERA_CONTROL;
    const enabled = Boolean(value?.enabled);

    useEffect(() => {
        if (!open) return;
        const syncPosition = () => setButtonRect(buttonRef.current?.getBoundingClientRect() || null);
        const closeOnOutsidePointer = (event: PointerEvent) => {
            const target = event.target;
            if (!(target instanceof Node)) return;
            if (buttonRef.current?.contains(target) || panelRef.current?.contains(target)) return;
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

    return (
        <>
            <span ref={buttonRef} className="inline-flex min-w-0">
                <Button
                    size="small"
                    type="text"
                    className={buttonClassName || "!h-8 !max-w-[180px] !justify-start !rounded-full !px-2.5"}
                    style={{
                        background: enabled ? theme.toolbar.activeBg : theme.node.fill,
                        color: enabled ? theme.toolbar.activeText : theme.node.text,
                    }}
                    icon={<Camera className="size-3.5" />}
                    aria-expanded={open}
                    onClick={() => setOpen(!open)}
                >
                    <span className="truncate">{enabled ? `${control.focalLength}mm · f/${control.aperture}` : "摄像机"}</span>
                </Button>
            </span>
            {open && buttonRect ? <CameraControlPortal buttonRect={buttonRect} panelRef={panelRef} theme={theme} control={control} onChange={onChange} onClose={() => setOpen(false)} /> : null}
        </>
    );
}

function CameraControlPortal({
    buttonRect,
    panelRef,
    theme,
    control,
    onChange,
    onClose,
}: {
    buttonRect: DOMRect;
    panelRef: RefObject<HTMLDivElement | null>;
    theme: CanvasTheme;
    control: CameraControlOptions;
    onChange: (value: CameraControlOptions) => void;
    onClose: () => void;
}) {
    const width = 720;
    const gap = 8;
    const margin = 12;
    const style = {
        position: "fixed",
        zIndex: 1200,
        width,
        left: Math.max(margin, Math.min(window.innerWidth - width - margin, buttonRect.left + buttonRect.width / 2 - width / 2)),
        bottom: window.innerHeight - buttonRect.top + gap,
        maxHeight: Math.max(320, buttonRect.top - margin * 2),
        background: theme.toolbar.panel,
        border: `1px solid ${theme.toolbar.border}`,
        borderRadius: 18,
        boxShadow: "0 18px 54px rgba(28, 25, 23, 0.16)",
        overflowY: "auto",
        color: theme.node.text,
    } as const;

    const camera = CAMERA_PROFILES.find((item) => item.id === control.camera) || CAMERA_PROFILES[0];
    const lens = LENS_PROFILES.find((item) => item.id === control.lens) || LENS_PROFILES[0];
    const focalMeta = FOCAL_LENGTH_META[control.focalLength];
    const apertureMeta = APERTURE_META[control.aperture];
    const update = (patch: Partial<CameraControlOptions>) => onChange({ ...control, ...patch });

    return createPortal(
        <div ref={panelRef} style={style} onPointerDown={(event) => event.stopPropagation()} onMouseDown={(event) => event.stopPropagation()} onClick={(event) => event.stopPropagation()} onWheel={(event) => event.stopPropagation()}>
            <div className="flex items-center justify-between border-b px-5 py-3" style={{ borderColor: theme.toolbar.border }}>
                <h2 className="text-sm font-semibold">摄像机</h2>
                <button type="button" className="grid size-7 place-items-center rounded-lg transition hover:opacity-70" style={{ color: theme.node.muted }} aria-label="关闭" onClick={onClose}>
                    <X className="size-4" />
                </button>
            </div>

            <div className="px-5 py-4">
                <div className="grid grid-cols-4">
                    <SettingColumn
                        theme={theme}
                        label="相机"
                        tooltipTitle={`${camera.zhName} · ${camera.label}`}
                        tooltipDesc={camera.description}
                        tooltipUseCase={camera.useCase}
                        visual={<CameraVisual profile={camera} theme={theme} />}
                        caption={camera.label}
                        onPrevious={control.camera === CAMERA_IDS[0] ? undefined : () => update({ camera: cycleValue(CAMERA_IDS, control.camera, -1) })}
                        onNext={control.camera === CAMERA_IDS[CAMERA_IDS.length - 1] ? undefined : () => update({ camera: cycleValue(CAMERA_IDS, control.camera, 1) })}
                    />
                    <SettingColumn
                        theme={theme}
                        separator
                        label="镜头"
                        tooltipTitle={`${lens.zhName} · ${lens.label}`}
                        tooltipDesc={lens.description}
                        tooltipUseCase={lens.useCase}
                        visual={<LensVisual profile={lens} theme={theme} />}
                        caption={lens.label}
                        onPrevious={control.lens === LENS_IDS[0] ? undefined : () => update({ lens: cycleValue(LENS_IDS, control.lens, -1) })}
                        onNext={control.lens === LENS_IDS[LENS_IDS.length - 1] ? undefined : () => update({ lens: cycleValue(LENS_IDS, control.lens, 1) })}
                    />
                    <SettingColumn
                        theme={theme}
                        separator
                        label="焦距"
                        tooltipTitle={`${control.focalLength}mm · ${focalMeta?.zhName || ""}`}
                        tooltipDesc={focalMeta?.description}
                        tooltipUseCase={focalMeta?.useCase}
                        badge={focalMeta?.zhName}
                        visual={
                            <div className="flex flex-col items-center">
                                <div className="text-4xl font-light leading-none" style={{ color: theme.node.text }}>
                                    {control.focalLength}
                                </div>
                                <div className="mt-2 text-xs tracking-wider" style={{ color: theme.node.faint }}>
                                    mm
                                </div>
                            </div>
                        }
                        caption="mm"
                        onPrevious={control.focalLength === FOCAL_LENGTHS[0] ? undefined : () => update({ focalLength: cycleValue(FOCAL_LENGTHS, control.focalLength, -1) })}
                        onNext={control.focalLength === FOCAL_LENGTHS[FOCAL_LENGTHS.length - 1] ? undefined : () => update({ focalLength: cycleValue(FOCAL_LENGTHS, control.focalLength, 1) })}
                    />
                    <SettingColumn
                        theme={theme}
                        separator
                        label="光圈"
                        tooltipTitle={`f/${control.aperture} · ${apertureMeta?.zhName || ""}`}
                        tooltipDesc={apertureMeta?.description}
                        tooltipUseCase={apertureMeta?.useCase}
                        badge={apertureMeta?.zhName}
                        visual={
                            <div className="flex items-baseline">
                                <span className="text-xl font-light" style={{ color: theme.node.muted }}>
                                    f/
                                </span>
                                <span className="text-4xl font-light leading-none" style={{ color: theme.node.text }}>
                                    {control.aperture}
                                </span>
                            </div>
                        }
                        caption={`f/${control.aperture}`}
                        onPrevious={control.aperture === APERTURES[0] ? undefined : () => update({ aperture: cycleValue(APERTURES, control.aperture, -1) })}
                        onNext={control.aperture === APERTURES[APERTURES.length - 1] ? undefined : () => update({ aperture: cycleValue(APERTURES, control.aperture, 1) })}
                    />
                </div>

                <div className="mt-4 flex items-center justify-between gap-2">
                    <span className="text-xs" style={{ color: theme.node.faint }}>
                        开启后镜头参数会拼进生成提示词，不改变画面内容
                    </span>
                    <div className="flex shrink-0 items-center gap-2">
                        <span className="text-sm" style={{ color: control.enabled ? theme.node.text : theme.node.muted }}>
                            {control.enabled ? "开启" : "关闭"}
                        </span>
                        <Switch size="small" checked={control.enabled} aria-label="摄像机控制" onChange={(enabled) => update({ enabled })} />
                    </div>
                </div>
            </div>
        </div>,
        document.body,
    );
}

type SettingColumnProps = {
    theme: CanvasTheme;
    separator?: boolean;
    label: string;
    tooltipTitle: string;
    tooltipDesc?: string;
    tooltipUseCase?: string;
    badge?: ReactNode;
    visual: ReactNode;
    caption: string;
    onPrevious?: () => void;
    onNext?: () => void;
};

function SettingColumn({ theme, separator, label, tooltipTitle, tooltipDesc, tooltipUseCase, badge, visual, caption, onPrevious, onNext }: SettingColumnProps) {
    const tooltip = (
        <div className="max-w-64">
            <div className="font-medium">{tooltipTitle}</div>
            {tooltipDesc ? <div className="mt-1 text-xs opacity-80">{tooltipDesc}</div> : null}
            {tooltipUseCase ? <div className="mt-2 text-xs opacity-70">使用场景：{tooltipUseCase}</div> : null}
        </div>
    );

    return (
        <div className="flex min-w-0 flex-col items-center px-3" style={{ borderLeft: separator ? `1px solid ${theme.node.stroke}` : undefined }}>
            <Button type="text" disabled={!onPrevious} className="!h-7 !w-full !p-0 hover:!bg-transparent" style={{ color: theme.node.faint }} icon={<ChevronUp className="size-4" />} aria-label={`上一项${label}`} onClick={onPrevious} />
            <Tooltip title={tooltip} mouseEnterDelay={0.7} color={theme.node.panel} zIndex={1300}>
                <div className="relative flex h-[150px] w-full flex-col items-center justify-between rounded-2xl border px-3 py-2.5 transition-colors" style={{ background: theme.node.fill, borderColor: theme.node.stroke }}>
                    <span className="text-xs font-medium" style={{ color: theme.node.muted }}>
                        {label}
                    </span>
                    <div className="flex flex-1 items-center justify-center">{visual}</div>
                    {badge ? (
                        <span className="absolute right-2 top-2 rounded-md px-1.5 py-0.5 text-[10px] font-medium" style={{ background: theme.toolbar.activeBg, color: theme.toolbar.activeText }}>
                            {badge}
                        </span>
                    ) : null}
                </div>
            </Tooltip>
            <Button type="text" disabled={!onNext} className="!h-7 !w-full !p-0 hover:!bg-transparent" style={{ color: theme.node.faint }} icon={<ChevronDown className="size-4" />} aria-label={`下一项${label}`} onClick={onNext} />
            <span className="max-w-full truncate text-center text-xs" style={{ color: theme.node.muted }}>
                {caption}
            </span>
        </div>
    );
}

function CameraVisual({ profile, theme }: { profile: CameraProfile; theme: CanvasTheme }) {
    const body = profile.bodyColor;
    const accent = profile.accentColor;
    const dark = theme.canvas.background;
    const detail = theme.node.panel;
    const text = theme.node.muted;
    const svg = (children: ReactNode) => (
        <svg viewBox="0 0 72 52" className="h-16 w-full" xmlns="http://www.w3.org/2000/svg" aria-hidden="true">
            {children}
        </svg>
    );

    switch (profile.id) {
        case "panavision_dxl2":
            return svg(
                <>
                    <rect x="18" y="8" width="30" height="32" rx="3" fill={body} stroke={accent} />
                    <rect x="10" y="12" width="12" height="24" rx="2" fill={detail} stroke={accent} />
                    <rect x="48" y="14" width="16" height="20" rx="2" fill={detail} stroke={accent} />
                    <circle cx="56" cy="24" r="6" fill={dark} stroke={accent} />
                    <rect x="26" y="3" width="14" height="6" rx="1" fill={body} />
                    <rect x="20" y="42" width="26" height="4" rx="1" fill={accent} opacity="0.6" />
                </>,
            );
        case "arri_alexa_mini_lf":
            return svg(
                <>
                    <rect x="14" y="12" width="38" height="28" rx="4" fill={body} stroke={accent} />
                    <circle cx="52" cy="26" r="10" fill={dark} stroke={accent} />
                    <circle cx="52" cy="26" r="6" fill={detail} stroke={text} />
                    <rect x="20" y="6" width="18" height="8" rx="1.5" fill={body} />
                    <rect x="18" y="18" width="8" height="6" rx="1" fill={accent} opacity="0.3" />
                </>,
            );
        case "red_komodo_6k":
        case "red_v_raptor_8k": {
            const raptor = profile.id === "red_v_raptor_8k";
            return svg(
                <>
                    <rect x="18" y="14" width={raptor ? 32 : 28} height="26" rx="2" fill={body} stroke={accent} />
                    <circle cx={raptor ? 50 : 46} cy="27" r="9" fill={dark} stroke={accent} />
                    <circle cx={raptor ? 50 : 46} cy="27" r="5" fill={detail} stroke={text} />
                    <rect x="20" y="8" width="8" height="7" rx="1" fill={accent} opacity="0.85" />
                    <text x="22" y="36" fontSize="6" fill={text} fontWeight="bold">
                        RED
                    </text>
                </>,
            );
        }
        case "sony_venice_2":
        case "sony_fx6":
            return svg(
                <>
                    <rect x="14" y="14" width="36" height="26" rx="3" fill={body} stroke={accent} />
                    <circle cx="52" cy="27" r="9" fill={dark} stroke={accent} />
                    <rect x="16" y="18" width="6" height="6" rx="1" fill={accent} opacity="0.5" />
                    <rect x="22" y="8" width="16" height="8" rx="1.5" fill={body} />
                    <text x="22" y="36" fontSize="5.5" fill={text}>
                        SONY
                    </text>
                </>,
            );
        case "blackmagic_ursa_12k":
            return svg(
                <>
                    <rect x="16" y="12" width="32" height="28" rx="2" fill={body} stroke={accent} />
                    <circle cx="50" cy="26" r="10" fill={dark} stroke={accent} />
                    <circle cx="50" cy="26" r="6" fill={detail} stroke={text} />
                    <rect x="24" y="5" width="12" height="8" rx="1" fill={body} />
                    <text x="18" y="35" fontSize="5" fill={accent} fontWeight="bold">
                        URSA
                    </text>
                </>,
            );
        case "canon_c500_mk2":
            return svg(
                <>
                    <rect x="16" y="12" width="34" height="28" rx="3" fill={body} stroke={accent} />
                    <circle cx="50" cy="26" r="9" fill={dark} stroke={accent} />
                    <rect x="18" y="16" width="10" height="5" rx="1" fill={accent} opacity="0.7" />
                    <text x="20" y="36" fontSize="5.5" fill={text}>
                        Canon
                    </text>
                </>,
            );
        default:
            return svg(
                <>
                    <rect x="16" y="14" width="36" height="26" rx="3" fill={body} stroke={accent} />
                    <circle cx="52" cy="27" r="9" fill={dark} stroke={accent} />
                </>,
            );
    }
}

function LensVisual({ profile, theme }: { profile: LensProfile; theme: CanvasTheme }) {
    const rings = profile.id.startsWith("anamorphic") ? 4 : 3;
    return (
        <svg viewBox="0 0 88 56" className="h-16 w-full" xmlns="http://www.w3.org/2000/svg" aria-hidden="true">
            <rect x="8" y="17" width="68" height="24" rx="5" fill={profile.lensColor} stroke={theme.node.faint} />
            {Array.from({ length: rings }).map((_, index) => (
                <rect key={index} x={18 + index * 14} y="17" width="5" height="24" fill={profile.ringColor} opacity="0.65" />
            ))}
            <circle cx="72" cy="29" r="10" fill={theme.canvas.background} stroke={profile.ringColor} strokeWidth="2" />
            <circle cx="72" cy="29" r="5" fill={theme.node.panel} stroke={theme.node.faint} />
            {profile.id.startsWith("anamorphic") ? <ellipse cx="72" cy="29" rx="10" ry="4" fill="none" stroke={profile.ringColor} opacity="0.75" /> : null}
        </svg>
    );
}

function cycleValue<T>(values: readonly T[], value: T, direction: -1 | 1): T {
    const index = values.indexOf(value);
    return values[Math.min(Math.max(index + direction, 0), values.length - 1)];
}
