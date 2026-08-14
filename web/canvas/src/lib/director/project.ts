// 导演台工程的数据形状与规范化。
//
// 工程整体随画布节点的 metadata 走(见 CanvasNodeMetadata.director),所以这里
// 的约束是:纯 JSON、无二进制、体积小。人体是程序化生成的,不需要存模型;姿势是
// 稀疏字典;一个摆了七八个角色的场景序列化后也就几 KB。
//
// 版本号只在结构发生破坏性变化时递增,规范化函数负责把旧版本读成新结构——
// 用户的场景存在节点里,不能因为我们改了字段就打不开。

import { normalizeRigPose, type RigPose } from "./rig";

export const DIRECTOR_PROJECT_VERSION = 1;

export type Vec3 = [number, number, number];

export type DirectorTransform = {
    position: Vec3;
    /** 角度制,绕 XYZ */
    rotation: Vec3;
    scale: Vec3;
};

export type CharacterBuild = "male" | "female" | "slim" | "heavy" | "child" | "teen" | "muscular" | "mannequin";

export type DirectorCharacter = {
    id: string;
    kind: "character";
    name: string;
    build: CharacterBuild;
    color: string;
    transform: DirectorTransform;
    pose: RigPose;
    visible: boolean;
    locked: boolean;
};

export type PrimitiveShape = "box" | "sphere" | "cylinder" | "cone" | "torus" | "plane" | "pyramid";

export type DirectorPrimitive = {
    id: string;
    kind: "primitive";
    name: string;
    shape: PrimitiveShape;
    color: string;
    transform: DirectorTransform;
    visible: boolean;
    locked: boolean;
};

export type DirectorObject = DirectorCharacter | DirectorPrimitive;

export type DirectorCamera = {
    id: string;
    name: string;
    position: Vec3;
    /** 注视点。auto 模式下由场景内容算,manual 用这个值 */
    target: Vec3;
    targetMode: "auto" | "manual";
    /** 垂直视野角度。与画布摄像机面板的焦距是同一套语言:35mm 等效焦距越长,FOV 越小 */
    fov: number;
};

export type DirectorScene = {
    /** 天空/背景色 */
    skyColor: string;
    groundColor: string;
    groundOpacity: number;
    groundHeight: number;
    showGrid: boolean;
};

export type DirectorProject = {
    version: number;
    scene: DirectorScene;
    objects: DirectorObject[];
    cameras: DirectorCamera[];
    activeCameraId: string;
    /** 取景框比例,如 "16:9";影响截图尺寸与视口遮罩 */
    aspect: string;
    /** 九宫格构图辅助线 */
    showThirds: boolean;
};

export const ASPECT_OPTIONS = ["1:1", "4:3", "3:4", "16:9", "9:16", "3:2", "2:3", "21:9"] as const;

export const CHARACTER_BUILDS: Array<{ id: CharacterBuild; label: string }> = [
    { id: "male", label: "男性素体" },
    { id: "female", label: "女性素体" },
    { id: "slim", label: "纤细" },
    { id: "heavy", label: "壮硕" },
    { id: "muscular", label: "健美" },
    { id: "teen", label: "少年" },
    { id: "child", label: "儿童" },
    { id: "mannequin", label: "木偶" },
];

export const PRIMITIVE_SHAPES: Array<{ id: PrimitiveShape; label: string }> = [
    { id: "box", label: "立方体" },
    { id: "sphere", label: "球体" },
    { id: "cylinder", label: "圆柱体" },
    { id: "cone", label: "圆锥" },
    { id: "pyramid", label: "棱锥" },
    { id: "torus", label: "环状体" },
    { id: "plane", label: "平面" },
];

export function identityTransform(): DirectorTransform {
    return { position: [0, 0, 0], rotation: [0, 0, 0], scale: [1, 1, 1] };
}

export function defaultCamera(id: string, name = "机位 1"): DirectorCamera {
    return { id, name, position: [3.2, 1.7, 4.6], target: [0, 0.95, 0], targetMode: "auto", fov: 40 };
}

export function createDirectorProject(idFactory: () => string): DirectorProject {
    const cameraId = idFactory();
    return {
        version: DIRECTOR_PROJECT_VERSION,
        scene: { skyColor: "#eceae6", groundColor: "#cfcac2", groundOpacity: 1, groundHeight: 0, showGrid: true },
        objects: [
            {
                id: idFactory(),
                kind: "character",
                name: "角色 1",
                build: "male",
                color: "#b8b2a8",
                transform: identityTransform(),
                pose: {},
                visible: true,
                locked: false,
            },
        ],
        cameras: [defaultCamera(cameraId)],
        activeCameraId: cameraId,
        aspect: "16:9",
        showThirds: false,
    };
}

// ── 规范化 ─────────────────────────────────────────────────────────────────
// 入口有三个:新建、从节点 metadata 读回、导入 JSON。后两个的数据都不可信,
// 统一从这里过一遍,保证下游拿到的一定是完整结构。

const num = (value: unknown, fallback: number) => (typeof value === "number" && Number.isFinite(value) ? value : fallback);
const str = (value: unknown, fallback: string) => (typeof value === "string" && value.trim() ? value.trim() : fallback);
const bool = (value: unknown, fallback: boolean) => (typeof value === "boolean" ? value : fallback);

function vec3(value: unknown, fallback: Vec3): Vec3 {
    if (!Array.isArray(value) || value.length !== 3) return [...fallback];
    return [num(value[0], fallback[0]), num(value[1], fallback[1]), num(value[2], fallback[2])];
}

function normalizeTransform(value: unknown): DirectorTransform {
    const raw = (value || {}) as Partial<DirectorTransform>;
    const scale = vec3(raw.scale, [1, 1, 1]);
    return {
        position: vec3(raw.position, [0, 0, 0]),
        rotation: vec3(raw.rotation, [0, 0, 0]),
        // 缩放为 0 会让物体消失且再也调不回来(乘法归零),兜底到一个极小正值
        scale: scale.map((value) => (Math.abs(value) < 0.001 ? 0.001 : value)) as Vec3,
    };
}

function normalizeObject(value: unknown, idFactory: () => string): DirectorObject | null {
    if (!value || typeof value !== "object") return null;
    const raw = value as Record<string, unknown>;
    const base = {
        id: str(raw.id, idFactory()),
        name: str(raw.name, "对象"),
        color: str(raw.color, "#b8b2a8"),
        transform: normalizeTransform(raw.transform),
        visible: bool(raw.visible, true),
        locked: bool(raw.locked, false),
    };
    if (raw.kind === "primitive") {
        const shape = PRIMITIVE_SHAPES.some((item) => item.id === raw.shape) ? (raw.shape as PrimitiveShape) : "box";
        return { ...base, kind: "primitive", shape };
    }
    // 认不出 kind 的一律当角色:角色是这个工具的主体,退化成方块没有意义
    const build = CHARACTER_BUILDS.some((item) => item.id === raw.build) ? (raw.build as CharacterBuild) : "male";
    return { ...base, kind: "character", build, pose: normalizeRigPose(raw.pose as RigPose) };
}

function normalizeCamera(value: unknown, idFactory: () => string): DirectorCamera {
    const raw = (value || {}) as Record<string, unknown>;
    const fallback = defaultCamera(str(raw.id, idFactory()), str(raw.name, "机位"));
    return {
        ...fallback,
        position: vec3(raw.position, fallback.position),
        target: vec3(raw.target, fallback.target),
        targetMode: raw.targetMode === "manual" ? "manual" : "auto",
        // FOV 超出这个范围要么畸变到无法辨认,要么窄到什么也看不见
        fov: Math.min(120, Math.max(5, num(raw.fov, fallback.fov))),
    };
}

export function normalizeDirectorProject(value: unknown, idFactory: () => string): DirectorProject {
    const raw = (value || {}) as Record<string, unknown>;
    const objects = (Array.isArray(raw.objects) ? raw.objects : []).map((item) => normalizeObject(item, idFactory)).filter((item): item is DirectorObject => Boolean(item));
    const cameras = (Array.isArray(raw.cameras) && raw.cameras.length ? raw.cameras : [null]).map((item) => normalizeCamera(item, idFactory));
    const scene = (raw.scene || {}) as Record<string, unknown>;
    const activeCameraId = cameras.some((camera) => camera.id === raw.activeCameraId) ? String(raw.activeCameraId) : cameras[0].id;
    return {
        version: DIRECTOR_PROJECT_VERSION,
        scene: {
            skyColor: str(scene.skyColor, "#eceae6"),
            groundColor: str(scene.groundColor, "#cfcac2"),
            groundOpacity: Math.min(1, Math.max(0, num(scene.groundOpacity, 1))),
            groundHeight: num(scene.groundHeight, 0),
            showGrid: bool(scene.showGrid, true),
        },
        objects,
        cameras,
        activeCameraId,
        aspect: ASPECT_OPTIONS.includes(raw.aspect as (typeof ASPECT_OPTIONS)[number]) ? String(raw.aspect) : "16:9",
        showThirds: bool(raw.showThirds, false),
    };
}

export function aspectRatio(aspect: string) {
    const [w, h] = aspect.split(":").map((part) => Number(part));
    return Number.isFinite(w) && Number.isFinite(h) && w > 0 && h > 0 ? w / h : 16 / 9;
}
