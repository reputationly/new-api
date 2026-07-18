// 能力注册表(设计文档 docs/canvas-orchestration-design.md §3.1)。
// 把体验区 12 个能力标签形式化为注册表记录:节点的模型下拉、参数面板、输入校验、
// 请求构建全部由本表驱动,不在业务代码里写能力分支。
//
// label(中文能力标签)是与后台运营设置对齐的协议键——MediaModelConfig.models[name]
// .capabilities 里存的就是这些中文标签(见 new-api constant/model_capability.go),
// 代码逻辑一律用英文 key,仅在与配置比对/展示时用 label。

import { CanvasNodeType } from "@/app/(user)/canvas/types";

export type CapabilityModality = "image" | "video" | "audio" | "music";
export type CapabilityChannel = "image-generation" | "image-edit" | "task";
export type InputSlotKind = "text" | "image" | "video" | "audio";

export type InputSlot = {
    /** 请求字段映射: "prompt" | "images" | "metadata.audio" | "metadata.src_video" ... */
    key: string;
    kind: InputSlotKind;
    required: boolean;
    /** 多值上限(images / src_ref_images);缺省单值 */
    max?: number;
    /** 面板/校验提示用槽位名 */
    role: string;
};

export type ParamSpec = {
    /** 请求字段映射: "size" | "seconds" | "metadata.sr_ratio" | "metadata.lyrics" ... */
    key: string;
    label: string;
    type: "select" | "number" | "text" | "textarea";
    /** select 选项来源:配置白名单(sizes/durations)或固定列表 */
    options?: "sizes" | "durations" | string[];
    placeholder?: string;
    min?: number;
    max?: number;
    step?: number;
};

export type CapabilitySpec = {
    key: string;
    /** 中文能力标签,与 constant/model_capability.go 严格一致(协议键) */
    label: string;
    modality: CapabilityModality;
    /** 产物节点媒体类型 */
    output: CanvasNodeType;
    channel: CapabilityChannel;
    /** channel === "task" 时显式下发的 metadata.task_type;体验区约定 t2v/i2v/flf2v 靠模型推断不下发 */
    taskType?: string;
    inputs: InputSlot[];
    params: ParamSpec[];
    /** 跨槽位约束:keys 中至少一个槽位有值(如 vace 的 源视频/参考图 至少其一) */
    atLeastOne?: { keys: string[]; message: string };
};

const PROMPT_SLOT: InputSlot = { key: "prompt", kind: "text", required: true, role: "提示词" };
const OPTIONAL_PROMPT_SLOT: InputSlot = { key: "prompt", kind: "text", required: false, role: "提示词" };

export const CAPABILITIES: CapabilitySpec[] = [
    {
        key: "t2i",
        label: "文生图",
        modality: "image",
        output: CanvasNodeType.Image,
        channel: "image-generation",
        inputs: [PROMPT_SLOT],
        // 图片能力复用画布既有同步生图链路(requestGeneration),参数以其支持为准
        params: [{ key: "size", label: "尺寸", type: "select", options: "sizes" }],
    },
    {
        key: "i2i",
        label: "图生图",
        modality: "image",
        output: CanvasNodeType.Image,
        channel: "image-edit",
        inputs: [PROMPT_SLOT, { key: "image", kind: "image", required: true, max: 5, role: "底图" }],
        params: [],
    },
    {
        key: "t2v",
        label: "文生视频",
        modality: "video",
        output: CanvasNodeType.Video,
        channel: "task",
        inputs: [PROMPT_SLOT],
        params: [
            { key: "size", label: "尺寸", type: "select", options: "sizes" },
            { key: "seconds", label: "时长(秒)", type: "select", options: "durations" },
            { key: "metadata.seed", label: "随机种子", type: "number", placeholder: "留空随机" },
            { key: "metadata.negative_prompt", label: "负向提示", type: "text", placeholder: "可选" },
        ],
    },
    {
        key: "i2v",
        label: "图生视频",
        modality: "video",
        output: CanvasNodeType.Video,
        channel: "task",
        inputs: [PROMPT_SLOT, { key: "images", kind: "image", required: true, role: "首帧图片" }],
        params: [
            { key: "seconds", label: "时长(秒)", type: "select", options: "durations" },
            { key: "metadata.seed", label: "随机种子", type: "number", placeholder: "留空随机" },
            { key: "metadata.negative_prompt", label: "负向提示", type: "text", placeholder: "可选" },
        ],
    },
    {
        key: "flf2v",
        label: "首尾帧",
        modality: "video",
        output: CanvasNodeType.Video,
        channel: "task",
        inputs: [PROMPT_SLOT, { key: "images", kind: "image", required: true, max: 2, role: "首帧+尾帧(按连线顺序)" }],
        params: [
            { key: "seconds", label: "时长(秒)", type: "select", options: "durations" },
            { key: "metadata.seed", label: "随机种子", type: "number", placeholder: "留空随机" },
        ],
    },
    {
        key: "s2v",
        label: "数字人",
        modality: "video",
        output: CanvasNodeType.Video,
        channel: "task",
        taskType: "s2v",
        inputs: [PROMPT_SLOT, { key: "images", kind: "image", required: true, role: "人物图" }, { key: "metadata.audio", kind: "audio", required: true, role: "驱动音频" }],
        params: [{ key: "metadata.seed", label: "随机种子", type: "number", placeholder: "留空随机" }],
    },
    {
        key: "sr",
        label: "视频超分",
        modality: "video",
        output: CanvasNodeType.Video,
        channel: "task",
        taskType: "sr",
        inputs: [OPTIONAL_PROMPT_SLOT, { key: "metadata.video", kind: "video", required: true, role: "源视频" }],
        params: [
            // sr_ratio=1 → 仅插帧/不放大(输出被引擎按配置目标尺寸封顶,见设计文档 §3.9)
            { key: "metadata.sr_ratio", label: "放大倍率", type: "number", min: 1, max: 4, step: 0.5, placeholder: "默认 2;1=不放大" },
            { key: "metadata.target_fps", label: "插帧目标 FPS", type: "number", min: 16, max: 60, step: 1, placeholder: "留空不插帧" },
        ],
    },
    {
        key: "vace",
        label: "视频编辑",
        modality: "video",
        output: CanvasNodeType.Video,
        channel: "task",
        taskType: "vace",
        inputs: [
            PROMPT_SLOT,
            // 后端约束(materializeVACEInputs):源视频/参考图至少其一——R2V 仅参考图也是合法模式
            { key: "metadata.src_video", kind: "video", required: false, role: "源视频" },
            { key: "metadata.src_ref_images", kind: "image", required: false, max: 5, role: "参考图" },
        ],
        params: [{ key: "metadata.seed", label: "随机种子", type: "number", placeholder: "留空随机" }],
        atLeastOne: { keys: ["metadata.src_video", "metadata.src_ref_images"], message: "视频编辑至少需要连接 源视频 或 参考图 之一" },
    },
    {
        key: "tts",
        label: "语音合成",
        modality: "audio",
        output: CanvasNodeType.Audio,
        channel: "task",
        taskType: "tts",
        inputs: [PROMPT_SLOT, { key: "metadata.voice", kind: "audio", required: true, role: "参考音色" }],
        params: [{ key: "metadata.emo_alpha", label: "情感强度", type: "number", min: 0, max: 1, step: 0.05, placeholder: "默认 0.65" }],
    },
    {
        key: "t2m",
        label: "文生音乐",
        modality: "music",
        output: CanvasNodeType.Audio,
        channel: "task",
        taskType: "t2m",
        inputs: [PROMPT_SLOT],
        params: [
            { key: "metadata.lyrics", label: "歌词", type: "textarea", placeholder: "留空自动生成" },
            { key: "metadata.audio_duration", label: "时长(秒)", type: "number", min: 10, max: 240, step: 1, placeholder: "留空默认" },
            { key: "metadata.vocal_language", label: "人声语种", type: "select", options: ["zh", "yue", "en", "ja", "ko", "unknown"] },
            { key: "metadata.bpm", label: "BPM", type: "number", min: 40, max: 220, step: 1, placeholder: "留空默认" },
            { key: "metadata.seed", label: "随机种子", type: "number", placeholder: "留空随机" },
        ],
    },
    {
        key: "cover",
        label: "音乐改编",
        modality: "music",
        output: CanvasNodeType.Audio,
        channel: "task",
        taskType: "cover",
        inputs: [PROMPT_SLOT, { key: "metadata.reference_audio", kind: "audio", required: true, role: "参考音频" }],
        params: [
            { key: "metadata.lyrics", label: "歌词", type: "textarea", placeholder: "留空沿用原曲" },
            { key: "metadata.seed", label: "随机种子", type: "number", placeholder: "留空随机" },
        ],
    },
    {
        key: "repaint",
        label: "音乐重绘",
        modality: "music",
        output: CanvasNodeType.Audio,
        channel: "task",
        taskType: "repaint",
        inputs: [PROMPT_SLOT, { key: "metadata.src_audio", kind: "audio", required: true, role: "源音频" }],
        params: [
            { key: "metadata.lyrics", label: "歌词", type: "textarea", placeholder: "留空沿用原曲" },
            { key: "metadata.seed", label: "随机种子", type: "number", placeholder: "留空随机" },
        ],
    },
];

const byKey = new Map(CAPABILITIES.map((spec) => [spec.key, spec]));

export function capabilitySpec(key: string | undefined): CapabilitySpec | null {
    return (key && byKey.get(key)) || null;
}

/** 按模态分组(添加生成节点选择器用) */
export function capabilitiesByModality(): Array<{ modality: CapabilityModality; label: string; items: CapabilitySpec[] }> {
    const groups: Array<{ modality: CapabilityModality; label: string }> = [
        { modality: "image", label: "图片" },
        { modality: "video", label: "视频" },
        { modality: "audio", label: "音频" },
        { modality: "music", label: "音乐" },
    ];
    return groups.map((group) => ({ ...group, items: CAPABILITIES.filter((spec) => spec.modality === group.modality) }));
}
