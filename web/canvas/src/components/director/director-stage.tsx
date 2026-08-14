// 3D 导演台主界面。
//
// 定位:给图片/视频生成提供「构图与姿势」参考图。摆好人和道具、架好机位、
// 截图,截出来的图直接进画布当参考。所以取景框、九宫格、多方位截图这些是
// 主线功能,而材质、贴图、渲染质量刻意不做——参考图不需要好看,需要姿势
// 和视角准确。
//
// 工程状态只在这一层维护,向上以 onChange 整包回吐(由画布节点持久化),
// 向下拆成 props 给视口与面板。

import { useCallback, useMemo, useRef, useState } from "react";
import { App, Button, InputNumber, Segmented, Select, Slider, Tooltip } from "antd";
import { Box, Camera, Copy, Eye, EyeOff, Grid3x3, Lock, LockOpen, Maximize, Send, Trash2, User, X } from "lucide-react";

import { DirectorViewport, type ViewportHandle } from "./director-viewport";
import { POSE_PRESETS, applyPosePreset } from "@/lib/director/poses";
import { RIG_CONTROL_GROUPS, mirrorRigPose, normalizeRigPose, type RigControlKey } from "@/lib/director/rig";
import { ASPECT_OPTIONS, CHARACTER_BUILDS, PRIMITIVE_SHAPES, identityTransform, type CharacterBuild, type DirectorCamera, type DirectorCharacter, type DirectorObject, type DirectorProject, type PrimitiveShape, type Vec3 } from "@/lib/director/project";

export type DirectorCapture = { dataUrl: string; fileName: string };

/** 四方位:正面 / 右侧 / 背面 / 左侧。生成多视角参考图时最常用的一组。 */
const ORBIT_4 = [0, 90, 180, 270];
/** 十二方位:每 30° 一张,给需要严格多视角一致性的生成任务用 */
const ORBIT_12 = Array.from({ length: 12 }, (_, index) => index * 30);

let seed = 0;
const nextId = () => `d${Date.now().toString(36)}${(seed++).toString(36)}`;

export function DirectorStage({ project, onChange, onSendCaptures, onClose }: { project: DirectorProject; onChange: (project: DirectorProject) => void; onSendCaptures: (captures: DirectorCapture[]) => void; onClose: () => void }) {
    const { message } = App.useApp();
    const viewportRef = useRef<ViewportHandle | null>(null);
    const [selectedId, setSelectedId] = useState<string | null>(project.objects[0]?.id ?? null);
    const [tab, setTab] = useState<"properties" | "pose">("properties");
    const [captures, setCaptures] = useState<DirectorCapture[]>([]);
    const [busy, setBusy] = useState(false);

    const camera = useMemo(() => project.cameras.find((item) => item.id === project.activeCameraId) || project.cameras[0], [project.activeCameraId, project.cameras]);
    const selected = useMemo(() => project.objects.find((item) => item.id === selectedId) || null, [project.objects, selectedId]);

    const patchProject = useCallback((patch: Partial<DirectorProject>) => onChange({ ...project, ...patch }), [onChange, project]);

    const patchObject = useCallback(
        (id: string, patch: Partial<DirectorObject>) => {
            patchProject({ objects: project.objects.map((item) => (item.id === id ? ({ ...item, ...patch } as DirectorObject) : item)) });
        },
        [patchProject, project.objects],
    );

    const patchCamera = useCallback(
        (patch: Partial<DirectorCamera>) => {
            patchProject({ cameras: project.cameras.map((item) => (item.id === camera.id ? { ...item, ...patch } : item)) });
        },
        [camera.id, patchProject, project.cameras],
    );

    const addCharacter = (build: CharacterBuild) => {
        const id = nextId();
        const count = project.objects.filter((item) => item.kind === "character").length + 1;
        // 新角色沿 X 轴排开,免得叠在一起还得手动挪
        const transform = identityTransform();
        transform.position = [count === 1 ? 0 : (count % 2 ? 1 : -1) * Math.ceil((count - 1) / 2) * 0.9, 0, 0];
        patchProject({ objects: [...project.objects, { id, kind: "character", name: `角色 ${count}`, build, color: "#b8b2a8", transform, pose: {}, visible: true, locked: false }] });
        setSelectedId(id);
        setTab("pose");
    };

    const addPrimitive = (shape: PrimitiveShape) => {
        const id = nextId();
        const label = PRIMITIVE_SHAPES.find((item) => item.id === shape)?.label || "几何体";
        const transform = identityTransform();
        // 图元默认坐在地面上而不是埋进地里(单位尺寸的一半)
        transform.position = [0, shape === "plane" ? 0.01 : 0.5, 1.2];
        patchProject({ objects: [...project.objects, { id, kind: "primitive", name: label, shape, color: "#8f8880", transform, visible: true, locked: false }] });
        setSelectedId(id);
        setTab("properties");
    };

    const duplicateObject = (object: DirectorObject) => {
        const id = nextId();
        const transform = { ...object.transform, position: [object.transform.position[0] + 0.8, object.transform.position[1], object.transform.position[2]] as Vec3 };
        patchProject({ objects: [...project.objects, { ...object, id, name: `${object.name} 副本`, transform }] });
        setSelectedId(id);
    };

    const removeObject = (id: string) => {
        patchProject({ objects: project.objects.filter((item) => item.id !== id) });
        if (selectedId === id) setSelectedId(null);
    };

    const setPose = (pose: DirectorCharacter["pose"]) => {
        if (!selected || selected.kind !== "character") return;
        patchObject(selected.id, { pose: normalizeRigPose(pose) } as Partial<DirectorObject>);
    };

    // ── 截图 ──────────────────────────────────────────────────────────────
    const shoot = (label: string) => {
        const dataUrl = viewportRef.current?.capture();
        if (!dataUrl) {
            message.error("截图失败,请重试");
            return null;
        }
        return { dataUrl, fileName: `${label}.png` };
    };

    const captureCurrent = () => {
        const shot = shoot(`导演台-${captures.length + 1}`);
        if (shot) {
            setCaptures((value) => [...value, shot]);
            message.success("已截取当前视角");
        }
    };

    /**
     * 环绕截图。绕注视点转一圈,每个角度截一张。
     *
     * 逐帧 await 一次 rAF 是必须的:setOrbit 只改了相机,渲染发生在下一帧,
     * 不等就会连拍出同一个角度的 N 张图。
     */
    const captureOrbit = async (angles: number[]) => {
        const handle = viewportRef.current;
        if (!handle || busy) return;
        setBusy(true);
        const origin = { position: [...camera.position] as Vec3, target: [...camera.target] as Vec3 };
        const target = camera.target;
        const dx = camera.position[0] - target[0];
        const dz = camera.position[2] - target[2];
        const radius = Math.hypot(dx, dz) || 4;
        const base = Math.atan2(dz, dx);
        const shots: DirectorCapture[] = [];
        try {
            for (const angle of angles) {
                const theta = base + (angle * Math.PI) / 180;
                handle.setOrbit([target[0] + Math.cos(theta) * radius, camera.position[1], target[2] + Math.sin(theta) * radius], target);
                await new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve)));
                const dataUrl = handle.capture();
                if (dataUrl) shots.push({ dataUrl, fileName: `导演台-${String(angle).padStart(3, "0")}度.png` });
            }
            handle.setOrbit(origin.position, origin.target);
            setCaptures((value) => [...value, ...shots]);
            message.success(`已截取 ${shots.length} 张`);
        } finally {
            setBusy(false);
        }
    };

    const sendCaptures = () => {
        if (!captures.length) {
            message.warning("还没有截图");
            return;
        }
        onSendCaptures(captures);
        setCaptures([]);
    };

    return (
        <div className="fixed inset-0 z-[2000] flex flex-col bg-stone-100 text-stone-900">
            <header className="flex h-12 shrink-0 items-center gap-3 border-b border-stone-300 bg-white px-4">
                <span className="text-sm font-semibold">3D 导演台</span>
                <span className="text-xs text-stone-500">摆好姿势与机位,截图直接进画布当参考</span>
                <div className="ml-auto flex items-center gap-2">
                    <Select size="small" className="w-24" value={project.aspect} options={ASPECT_OPTIONS.map((value) => ({ value, label: value }))} onChange={(aspect) => patchProject({ aspect })} />
                    <Tooltip title="九宫格构图辅助线">
                        <Button size="small" type={project.showThirds ? "primary" : "default"} icon={<Grid3x3 className="size-3.5" />} onClick={() => patchProject({ showThirds: !project.showThirds })} />
                    </Tooltip>
                    <Tooltip title="取景到全部对象">
                        <Button size="small" icon={<Maximize className="size-3.5" />} onClick={() => viewportRef.current?.frameAll()} />
                    </Tooltip>
                    <Button size="small" icon={<Camera className="size-3.5" />} onClick={captureCurrent}>
                        截图
                    </Button>
                    <Button size="small" loading={busy} onClick={() => void captureOrbit(ORBIT_4)}>
                        四方位
                    </Button>
                    <Button size="small" loading={busy} onClick={() => void captureOrbit(ORBIT_12)}>
                        十二方位
                    </Button>
                    <Button size="small" type="primary" icon={<Send className="size-3.5" />} disabled={!captures.length} onClick={sendCaptures}>
                        发送到画布{captures.length ? ` (${captures.length})` : ""}
                    </Button>
                    <Button size="small" type="text" icon={<X className="size-4" />} onClick={onClose} />
                </div>
            </header>

            <div className="flex min-h-0 flex-1">
                <aside className="flex w-56 shrink-0 flex-col border-r border-stone-300 bg-white">
                    <div className="flex items-center gap-1 border-b border-stone-200 p-2">
                        <Select<CharacterBuild>
                            size="small"
                            className="flex-1"
                            placeholder="添加角色"
                            value={null as unknown as CharacterBuild}
                            suffixIcon={<User className="size-3.5" />}
                            options={CHARACTER_BUILDS.map((item) => ({ value: item.id, label: item.label }))}
                            onChange={addCharacter}
                        />
                        <Select<PrimitiveShape>
                            size="small"
                            className="flex-1"
                            placeholder="添加几何体"
                            value={null as unknown as PrimitiveShape}
                            suffixIcon={<Box className="size-3.5" />}
                            options={PRIMITIVE_SHAPES.map((item) => ({ value: item.id, label: item.label }))}
                            onChange={addPrimitive}
                        />
                    </div>
                    <div className="thin-scrollbar min-h-0 flex-1 overflow-y-auto p-1">
                        {project.objects.map((object) => (
                            <div
                                key={object.id}
                                className={`group flex cursor-pointer items-center gap-1 rounded px-2 py-1.5 text-xs ${object.id === selectedId ? "bg-blue-50 text-blue-700" : "hover:bg-stone-100"}`}
                                onClick={() => setSelectedId(object.id)}
                            >
                                {object.kind === "character" ? <User className="size-3.5 shrink-0" /> : <Box className="size-3.5 shrink-0" />}
                                <span className="min-w-0 flex-1 truncate">{object.name}</span>
                                <button
                                    type="button"
                                    className="shrink-0 opacity-0 transition group-hover:opacity-100"
                                    title={object.visible ? "隐藏" : "显示"}
                                    onClick={(event) => (event.stopPropagation(), patchObject(object.id, { visible: !object.visible }))}
                                >
                                    {object.visible ? <Eye className="size-3.5" /> : <EyeOff className="size-3.5 text-stone-400" />}
                                </button>
                                <button
                                    type="button"
                                    className="shrink-0 opacity-0 transition group-hover:opacity-100"
                                    title={object.locked ? "解锁" : "锁定"}
                                    onClick={(event) => (event.stopPropagation(), patchObject(object.id, { locked: !object.locked }))}
                                >
                                    {object.locked ? <Lock className="size-3.5 text-stone-400" /> : <LockOpen className="size-3.5" />}
                                </button>
                            </div>
                        ))}
                        {!project.objects.length ? <div className="p-4 text-center text-xs text-stone-400">场景是空的,先添加一个角色</div> : null}
                    </div>
                    {captures.length ? (
                        <div className="border-t border-stone-200 p-2">
                            <div className="mb-1 text-[11px] text-stone-500">待发送截图 {captures.length} 张</div>
                            <div className="thin-scrollbar flex max-h-28 flex-wrap gap-1 overflow-y-auto">
                                {captures.map((capture, index) => (
                                    <div key={capture.fileName + index} className="group relative">
                                        <img src={capture.dataUrl} alt={capture.fileName} className="size-11 rounded border border-stone-300 object-cover" />
                                        <button
                                            type="button"
                                            className="absolute -right-1 -top-1 hidden size-4 place-items-center rounded-full bg-stone-800 text-white group-hover:grid"
                                            onClick={() => setCaptures((value) => value.filter((_, current) => current !== index))}
                                        >
                                            <X className="size-2.5" />
                                        </button>
                                    </div>
                                ))}
                            </div>
                        </div>
                    ) : null}
                </aside>

                <DirectorViewport project={project} camera={camera} selectedId={selectedId} onSelect={setSelectedId} onCameraChange={patchCamera} handleRef={viewportRef} />

                <aside className="thin-scrollbar w-72 shrink-0 overflow-y-auto border-l border-stone-300 bg-white">
                    <div className="space-y-3 p-3">
                        <section className="space-y-2">
                            <div className="text-xs font-semibold text-stone-500">机位</div>
                            <LabeledSlider label="视野 FOV" value={camera.fov} min={5} max={120} step={1} suffix="°" onChange={(fov) => patchCamera({ fov })} />
                            <Vec3Row label="位置" value={camera.position} step={0.1} onChange={(position) => patchCamera({ position })} />
                            <div className="flex items-center gap-2">
                                <span className="w-12 shrink-0 text-[11px] text-stone-500">注视</span>
                                <Segmented
                                    size="small"
                                    value={camera.targetMode}
                                    options={[
                                        { value: "auto", label: "自动" },
                                        { value: "manual", label: "手动" },
                                    ]}
                                    onChange={(value) => patchCamera({ targetMode: value as "auto" | "manual" })}
                                />
                            </div>
                            {camera.targetMode === "manual" ? <Vec3Row label="目标" value={camera.target} step={0.1} onChange={(target) => patchCamera({ target })} /> : null}
                        </section>

                        <section className="space-y-2 border-t border-stone-200 pt-3">
                            <div className="text-xs font-semibold text-stone-500">场景</div>
                            <ColorRow label="天空" value={project.scene.skyColor} onChange={(skyColor) => patchProject({ scene: { ...project.scene, skyColor } })} />
                            <ColorRow label="地面" value={project.scene.groundColor} onChange={(groundColor) => patchProject({ scene: { ...project.scene, groundColor } })} />
                            <LabeledSlider label="地面透明" value={project.scene.groundOpacity} min={0} max={1} step={0.05} onChange={(groundOpacity) => patchProject({ scene: { ...project.scene, groundOpacity } })} />
                            <label className="flex items-center gap-2 text-[11px] text-stone-600">
                                <input type="checkbox" checked={project.scene.showGrid} onChange={(event) => patchProject({ scene: { ...project.scene, showGrid: event.target.checked } })} />
                                显示网格
                            </label>
                        </section>

                        {selected ? (
                            <section className="space-y-2 border-t border-stone-200 pt-3">
                                <div className="flex items-center gap-2">
                                    <input className="min-w-0 flex-1 rounded border border-stone-300 px-2 py-1 text-xs" value={selected.name} onChange={(event) => patchObject(selected.id, { name: event.target.value })} />
                                    <Tooltip title="复制">
                                        <Button size="small" type="text" icon={<Copy className="size-3.5" />} onClick={() => duplicateObject(selected)} />
                                    </Tooltip>
                                    <Tooltip title="删除">
                                        <Button size="small" type="text" danger icon={<Trash2 className="size-3.5" />} onClick={() => removeObject(selected.id)} />
                                    </Tooltip>
                                </div>
                                {selected.kind === "character" ? (
                                    <Segmented
                                        block
                                        size="small"
                                        value={tab}
                                        options={[
                                            { value: "properties", label: "属性" },
                                            { value: "pose", label: "姿势" },
                                        ]}
                                        onChange={(value) => setTab(value as "properties" | "pose")}
                                    />
                                ) : null}

                                {selected.kind !== "character" || tab === "properties" ? (
                                    <>
                                        <Vec3Row label="位置" value={selected.transform.position} step={0.1} onChange={(position) => patchObject(selected.id, { transform: { ...selected.transform, position } })} />
                                        <Vec3Row label="旋转" value={selected.transform.rotation} step={5} onChange={(rotation) => patchObject(selected.id, { transform: { ...selected.transform, rotation } })} />
                                        <Vec3Row label="缩放" value={selected.transform.scale} step={0.05} onChange={(scale) => patchObject(selected.id, { transform: { ...selected.transform, scale } })} />
                                        <ColorRow label="颜色" value={selected.color} onChange={(color) => patchObject(selected.id, { color })} />
                                        {selected.kind === "character" ? (
                                            <div className="flex items-center gap-2">
                                                <span className="w-12 shrink-0 text-[11px] text-stone-500">体型</span>
                                                <Select
                                                    size="small"
                                                    className="flex-1"
                                                    value={selected.build}
                                                    options={CHARACTER_BUILDS.map((item) => ({ value: item.id, label: item.label }))}
                                                    onChange={(build) => patchObject(selected.id, { build } as Partial<DirectorObject>)}
                                                />
                                            </div>
                                        ) : null}
                                    </>
                                ) : (
                                    <>
                                        <div className="flex flex-wrap gap-1">
                                            {POSE_PRESETS.map((preset) => (
                                                <button
                                                    key={preset.id}
                                                    type="button"
                                                    className="rounded border border-stone-300 px-2 py-1 text-[11px] transition hover:border-blue-400 hover:text-blue-600"
                                                    onClick={() => setPose(applyPosePreset(selected.pose, preset))}
                                                >
                                                    {preset.label}
                                                </button>
                                            ))}
                                        </div>
                                        <div className="flex gap-1">
                                            <Button size="small" className="flex-1" onClick={() => setPose(mirrorRigPose(selected.pose))}>
                                                左右镜像
                                            </Button>
                                            <Button size="small" className="flex-1" onClick={() => setPose({})}>
                                                重置姿势
                                            </Button>
                                        </div>
                                        {RIG_CONTROL_GROUPS.map((group) => (
                                            <div key={group.title} className="rounded border border-stone-200 p-2">
                                                <div className="mb-1 text-[11px] font-medium text-stone-500">{group.title}</div>
                                                {group.controls.map((control) => (
                                                    <LabeledSlider
                                                        key={control.key}
                                                        label={control.label}
                                                        value={selected.pose[control.key as RigControlKey] || 0}
                                                        min={control.min}
                                                        max={control.max}
                                                        step={control.unit === "m" ? 0.01 : 1}
                                                        suffix={control.unit === "m" ? "m" : "°"}
                                                        onChange={(value) => setPose({ ...selected.pose, [control.key]: value })}
                                                    />
                                                ))}
                                            </div>
                                        ))}
                                    </>
                                )}
                            </section>
                        ) : (
                            <div className="border-t border-stone-200 pt-3 text-center text-xs text-stone-400">在视口里点选一个对象</div>
                        )}
                    </div>
                </aside>
            </div>
        </div>
    );
}

function LabeledSlider({ label, value, min, max, step, suffix, onChange }: { label: string; value: number; min: number; max: number; step: number; suffix?: string; onChange: (value: number) => void }) {
    return (
        <div className="flex items-center gap-2">
            <span className="w-12 shrink-0 truncate text-[11px] text-stone-500">{label}</span>
            <Slider className="min-w-0 flex-1" min={min} max={max} step={step} value={value} onChange={onChange} tooltip={{ open: false }} />
            <span className="w-11 shrink-0 text-right text-[11px] tabular-nums text-stone-600">
                {Number(value.toFixed(2))}
                {suffix}
            </span>
        </div>
    );
}

function Vec3Row({ label, value, step, onChange }: { label: string; value: Vec3; step: number; onChange: (value: Vec3) => void }) {
    return (
        <div className="flex items-center gap-1">
            <span className="w-12 shrink-0 text-[11px] text-stone-500">{label}</span>
            {(["X", "Y", "Z"] as const).map((axis, index) => (
                <InputNumber
                    key={axis}
                    size="small"
                    className="min-w-0 flex-1"
                    step={step}
                    value={Number(value[index].toFixed(3))}
                    onChange={(next) => {
                        const list = [...value] as Vec3;
                        list[index] = Number(next) || 0;
                        onChange(list);
                    }}
                />
            ))}
        </div>
    );
}

function ColorRow({ label, value, onChange }: { label: string; value: string; onChange: (value: string) => void }) {
    return (
        <div className="flex items-center gap-2">
            <span className="w-12 shrink-0 text-[11px] text-stone-500">{label}</span>
            <input type="color" className="h-7 w-9 cursor-pointer rounded border border-stone-300 bg-white" value={value} onChange={(event) => onChange(event.target.value)} />
            <input className="min-w-0 flex-1 rounded border border-stone-300 px-2 py-1 text-[11px]" value={value} onChange={(event) => onChange(event.target.value)} />
        </div>
    );
}
