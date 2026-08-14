// 技能路由:按用户意图 + 当前选中节点类型挂载对应的领域技能,拼成系统提示词。
//
// 为什么不是一份大提示词:全量挂载约 1.4 万字,每轮都发既贵又稀释重点。按意图挂载后
// 典型一轮只带 core + 1~2 个领域技能。
//
// 判定只用「用户本轮文本 + 选中节点类型 + 选中节点的标题/提示词」,不引入会话阶段状态——
// 阶段机需要 set_agent_state 之类的持久化工具,那是独立的一档增量。

import { CanvasNodeType, type CanvasNodeData } from "@/types/canvas";
import { AUDIO_SKILL } from "./skills/audio";
import { CORE_SKILL } from "./skills/core";
import { IMAGE_CHARACTER_SHEET_SKILL, IMAGE_SKILL, IMAGE_STORYBOARD_SKILL } from "./skills/image";
import { ORGANIZE_SKILL, SCRIPT_SKILL } from "./skills/script";
import { VIDEO_CONTINUATION_SKILL, VIDEO_EDITING_SKILL, VIDEO_MULTI_SHOT_SKILL, VIDEO_SINGLE_SHOT_SKILL, VIDEO_SKILL } from "./skills/video";

const ONE_OFF_MEDIA = /生成一张|生成图片|生图|改图|生成视频|视频生成|文生视频|图生视频|生成音频|生成旁白|配音|音色|朗读|念一段/;
// 注意:「旁白」「对白」不能进 STORY——它们同时是 ONE_OFF_MEDIA 的词,
// 两边都命中会让 oneOff && !STORY 互相抵消,导致「念一段旁白」被当成要写剧本。
// 叙事意图由 剧本/脚本/分镜头/短片 这些词承担已经够了。
const STORY = /故事|剧情|剧本|脚本|文案|分镜头|拆镜头|镜头拆解|梗概|短片|完整制作|从头开始|人物小传/;
const IMAGE = /图片|生图|图像|角色图|产品图|场景图|参考图|设定图|设定表|四视图|海报|插画|封面|换装|肖像|底图|改图/;
const CHARACTER_SHEET = /四视图|设定表|角色设定|人物设定|产品设定|角色参考|身份一致|角色一致|换装|服装变体|多角度|转面|标准图/;
const STORYBOARD = /分镜拼图|分镜板|故事板|storyboard|宫格|拼图|镜头预演/i;
const VIDEO = /视频|动起来|动画化|渲染镜头|生成片段|运镜|一镜到底|超分|插帧|数字人|关键帧|首尾帧/;
const CONTINUATION = /续写|续接|继续视频|接着|延长|下一段|后续片段|连续片段|连续镜头|一镜到底|接力/;
const VIDEO_EDIT = /编辑视频|修改视频|重绘视频|重制|重渲染|换风格|改风格|调色|替换主体|替换产品|视频里.*换成|局部修改/;
const MULTI_SHOT = /多镜头|多段|镜头序列|广告片|品牌片|宣传片|音乐视频|MV|蒙太奇|系列镜头|一组镜头/i;
const AUDIO = /音频|配音|音色|旁白|对白|朗读|语音|念|音效|配乐|音乐|歌词|歌声|BGM/i;
const ORGANIZE = /整理|布局|排版|排列|归类|收纳|画布太乱|对齐|摆一下/;

/** 组装本轮系统提示词 */
export function buildCanvasAgentSkillPrompt(userText: string, nodes: CanvasNodeData[], selectedNodeIds: string[]): string {
    const selected = nodes.filter((node) => selectedNodeIds.includes(node.id));
    const selectedTypes = new Set(selected.map((node) => node.type));
    // 选中节点的标题与提示词并入意图判定:用户说「把这个改成夜景」时,意图词在节点上而不在这句话里
    const intent = [userText, ...selected.map((node) => [node.title, node.metadata?.prompt, node.metadata?.content].filter(Boolean).join(" "))].join(" ");

    const skills = [CORE_SKILL];
    const oneOff = ONE_OFF_MEDIA.test(userText) && !STORY.test(userText);

    const multiShotIntent = MULTI_SHOT.test(intent);
    const wantsScript = !oneOff && (STORY.test(intent) || multiShotIntent);
    const wantsImage = IMAGE.test(intent) || selectedTypes.has(CanvasNodeType.Image);
    // 宣传片/广告片/MV 这些词只在 MULTI_SHOT 里,但它们本质就是要出视频,
    // 不并进来的话「做一支30秒宣传片」拿不到任何视频技能
    const wantsVideo = VIDEO.test(intent) || multiShotIntent || selectedTypes.has(CanvasNodeType.Video);
    const wantsAudio = AUDIO.test(intent) || selectedTypes.has(CanvasNodeType.Audio);
    const wantsOrganize = ORGANIZE.test(intent);

    if (wantsScript) skills.push(SCRIPT_SKILL);

    if (wantsImage) {
        skills.push(IMAGE_SKILL);
        if (CHARACTER_SHEET.test(intent)) skills.push(IMAGE_CHARACTER_SHEET_SKILL);
        if (STORYBOARD.test(intent)) skills.push(IMAGE_STORYBOARD_SKILL);
    }

    if (wantsVideo) {
        skills.push(VIDEO_SKILL);
        const continuation = CONTINUATION.test(intent);
        const editing = VIDEO_EDIT.test(intent) || (selectedTypes.has(CanvasNodeType.Video) && /改|修改|调整|换|替换|编辑|重做|重绘/.test(userText));
        const multiShot = multiShotIntent || wantsScript;
        if (continuation) skills.push(VIDEO_CONTINUATION_SKILL);
        if (editing) skills.push(VIDEO_EDITING_SKILL);
        if (multiShot) skills.push(VIDEO_MULTI_SHOT_SKILL);
        // 三个专项都不命中 = 就是要一个镜头,明确告诉它不要擅自扩展成series
        if (!continuation && !editing && !multiShot) skills.push(VIDEO_SINGLE_SHOT_SKILL);
    }

    if (wantsAudio) skills.push(AUDIO_SKILL);
    if (wantsOrganize) skills.push(ORGANIZE_SKILL);

    return skills.join("\n\n");
}
