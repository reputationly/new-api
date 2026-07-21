// 官方工作流模板(设计文档 §五 端到端验收场景的开箱即用版):预置已连线的能力节点链,
// 提示词预填、模型按当前用户可用集合自动选第一个,打开后从左到右逐节点点「生成」即可出片。
// 模型选不到(运营未配能力标签/用户无可用模型)时留空,节点面板会提示选择,链路结构不受影响。

import { nanoid } from "nanoid";

import { capabilitySpec } from "@/services/capabilities/registry";
import { NODE_SPECS } from "./constants";
import { CanvasNodeType, type CanvasConnection, type CanvasNodeData, type CanvasNodeMetadata } from "./types";

export type CanvasTemplate = {
    key: string;
    title: string;
    description: string;
    /** 能力 key 列表(用于预取每个能力的默认模型) */
    capabilities: string[];
    build: (pickModel: (capabilityKey: string) => string) => { nodes: CanvasNodeData[]; connections: CanvasConnection[] };
};

function templateNode(type: CanvasNodeType, position: { x: number; y: number }, title: string, metadata: CanvasNodeMetadata): CanvasNodeData {
    const spec = NODE_SPECS[type];
    return {
        id: `${type}-${nanoid(8)}`,
        type,
        title,
        position,
        width: spec.width,
        height: spec.height,
        metadata: { ...spec.metadata, ...metadata },
    };
}

/** 能力节点:媒体类型 = 能力产物类型,与编辑器 createCapabilityNode 的 metadata 语义一致 */
function capabilityNode(capabilityKey: string, position: { x: number; y: number }, pickModel: (key: string) => string, extra?: CanvasNodeMetadata): CanvasNodeData {
    const spec = capabilitySpec(capabilityKey)!;
    const generationMode = spec.output === CanvasNodeType.Video ? ("video" as const) : spec.output === CanvasNodeType.Audio ? ("audio" as const) : ("image" as const);
    return templateNode(spec.output, position, spec.label, { capability: spec.key, generationMode, model: pickModel(capabilityKey) || undefined, ...extra });
}

function connect(from: CanvasNodeData, to: CanvasNodeData): CanvasConnection {
    return { id: `conn-${nanoid(8)}`, fromNodeId: from.id, toNodeId: to.id };
}

export const CANVAS_TEMPLATES: CanvasTemplate[] = [
    {
        key: "healing-cat",
        title: "治愈系·猫咪日落短片",
        description: "文生图 → 图生视频 → 超分:从一句提示词到 1080p/32fps 成片",
        capabilities: ["t2i", "i2v", "sr"],
        build: (pickModel) => {
            const promptNode = templateNode(CanvasNodeType.Text, { x: 0, y: 150 }, "提示词", {
                content: "一只橘猫趴在洒满夕阳的窗台上打盹,毛发蓬松发光,电影感光线,浅景深,4k 质感",
            });
            const t2i = capabilityNode("t2i", { x: 460, y: 140 }, pickModel);
            const i2v = capabilityNode("i2v", { x: 920, y: 140 }, pickModel, {
                prompt: "镜头缓慢推近,猫咪睁开眼睛打了个哈欠,尾巴轻轻摆动,自然光影流动",
            });
            // sr 链尾统一插帧(§3.9):上游 i2v 因存在 sr 下游会自动以 16fps 出片(fps 方案A)
            const sr = capabilityNode("sr", { x: 1440, y: 140 }, pickModel, {
                capabilityParams: { "metadata.sr_ratio": 2 },
            });
            return {
                nodes: [promptNode, t2i, i2v, sr],
                connections: [connect(promptNode, t2i), connect(t2i, i2v), connect(i2v, sr)],
            };
        },
    },
    {
        key: "cyberpunk-score",
        title: "赛博朋克·夜城短片+自动配乐",
        description: "文生图 → 图生视频 → 视频配乐:出片后按画面自动生成背景音乐",
        capabilities: ["t2i", "i2v", "v2m"],
        build: (pickModel) => {
            const promptNode = templateNode(CanvasNodeType.Text, { x: 0, y: 150 }, "提示词", {
                content: "赛博朋克雨夜都市,霓虹灯牌倒映在湿漉漉的街面,飞行器穿梭于摩天楼之间,蒸汽从下水道升起,青紫色调,电影级构图",
            });
            const t2i = capabilityNode("t2i", { x: 460, y: 140 }, pickModel);
            const i2v = capabilityNode("i2v", { x: 920, y: 140 }, pickModel, {
                prompt: "镜头低空穿行,雨滴划过霓虹光晕,飞行器从头顶掠过,灯牌闪烁",
            });
            // 有提示词 → tv2m(文本引导配乐);清空提示词则退回 v2m 纯画面配乐
            const v2m = capabilityNode("v2m", { x: 1440, y: 170 }, pickModel, {
                prompt: "黑暗合成器浪潮,低音鼓点渐强,冷冽电子氛围",
            });
            return {
                nodes: [promptNode, t2i, i2v, v2m],
                connections: [connect(promptNode, t2i), connect(t2i, i2v), connect(i2v, v2m)],
            };
        },
    },
    {
        key: "ink-landscape",
        title: "国风·水墨山水动画",
        description: "文生图 → 图生视频 → 超分:水墨意境从纸面流动起来",
        capabilities: ["t2i", "i2v", "sr"],
        build: (pickModel) => {
            const promptNode = templateNode(CanvasNodeType.Text, { x: 0, y: 150 }, "提示词", {
                content: "中国水墨山水画,远山如黛云雾缭绕,近处孤舟渔翁,留白意境,墨色浓淡相宜,宣纸质感",
            });
            const t2i = capabilityNode("t2i", { x: 460, y: 140 }, pickModel);
            const i2v = capabilityNode("i2v", { x: 920, y: 140 }, pickModel, {
                prompt: "云雾缓缓流动,水面泛起涟漪,渔舟轻轻摇曳,墨色在纸面晕染开来",
            });
            const sr = capabilityNode("sr", { x: 1440, y: 140 }, pickModel, {
                capabilityParams: { "metadata.sr_ratio": 2 },
            });
            return {
                nodes: [promptNode, t2i, i2v, sr],
                connections: [connect(promptNode, t2i), connect(t2i, i2v), connect(i2v, sr)],
            };
        },
    },
    {
        key: "digital-human",
        title: "数字人·口播视频",
        description: "文生图(人物) + 声音设计(配音) → 数字人:图片和声音都由 AI 生成,直接驱动口播",
        capabilities: ["t2i", "tts_design", "s2v"],
        build: (pickModel) => {
            // 双分支 DAG:人物图与配音分别生成,汇入 s2v(按 kind 自动分槽:图→人物图,音频→驱动音频)
            const portraitPrompt = templateNode(CanvasNodeType.Text, { x: 0, y: 0 }, "人物描述", {
                content: "专业女主播半身像,正面面对镜头,得体的深色西装,柔和演播室灯光,纯色背景,真实照片质感",
            });
            const t2i = capabilityNode("t2i", { x: 460, y: -10 }, pickModel);
            const speechPrompt = templateNode(CanvasNodeType.Text, { x: 0, y: 330 }, "口播台词", {
                content: "大家好,欢迎收看今天的科技快讯。人工智能正在改变我们创作的方式,现在,一句话就能生成一段视频。",
            });
            const ttsDesign = capabilityNode("tts_design", { x: 460, y: 350 }, pickModel, {
                capabilityParams: { "metadata.instructions": "知性女声,吐字清晰,语速适中,新闻播报腔" },
            });
            const s2v = capabilityNode("s2v", { x: 920, y: 150 }, pickModel, {
                prompt: "新闻主播口播,表情自然,口型与语音同步",
            });
            return {
                nodes: [portraitPrompt, t2i, speechPrompt, ttsDesign, s2v],
                connections: [connect(portraitPrompt, t2i), connect(speechPrompt, ttsDesign), connect(t2i, s2v), connect(ttsDesign, s2v)],
            };
        },
    },
    {
        key: "folk-to-jazz",
        title: "音乐工坊·民谣变爵士",
        description: "文生音乐 → 音乐改编:先生成一段民谣,再改编成爵士风",
        capabilities: ["t2m", "cover"],
        build: (pickModel) => {
            const promptNode = templateNode(CanvasNodeType.Text, { x: 0, y: 150 }, "音乐描述", {
                content: "温暖的民谣吉他小品,轻快的节奏,午后阳光的慵懒感",
            });
            const t2m = capabilityNode("t2m", { x: 460, y: 170 }, pickModel);
            const cover = capabilityNode("cover", { x: 920, y: 170 }, pickModel, {
                prompt: "改编成爵士风格,加入钢琴和刷鼓,慵懒摇摆",
            });
            return {
                nodes: [promptNode, t2m, cover],
                connections: [connect(promptNode, t2m), connect(t2m, cover)],
            };
        },
    },
];
