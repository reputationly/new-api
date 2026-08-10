// MiniMax H3 的「Context-IR」提示词层。
//
// H3 由三个模块组成，只有 H3-Base 开源;把自由输入理解并序列化成 Base 能直接消费的
// 结构化中间表示的 H3-Context-IR **没有开源**。引擎侧对 prompt 完全不做解析(prompt
// 原样进 Qwen3-VL tokenizer),所以格式写错**不会报错、只会默默变差**——这一层必须由
// 我们保证。
//
// 我们不调官方 /v2/h3_context_ir(那是运行时外部依赖 + 计费 + 一次异步两跳),而是把
// 官方 skill 的两份 reference **原文**当 system prompt,优化模型由运营在「体验区管理 →
// 通用设置」里配(指到自建的 deepseek-v4-flash-0731 即可,零代码)。
//
// base-en.txt / ref-en.txt 是从 MiniMax-H3/skills/h3-prompt-writing/references/ 逐字
// 拷来的,**不要缩写、不要"精简掉示例"**:里面那几个完整 case 是保证格式正确的关键
// few-shot,删了就等于把格式定义交回给优化模型去猜。要同步上游版本时整份替换,
// 不要手改。
import BASE_GUIDE from './h3/base-en.txt?raw';
import REF_GUIDE from './h3/ref-en.txt?raw';

// 角色框定。两份 guide 本身写的是「产出长什么样」,没写「你在干什么」,补这两句让
// 优化模型知道输入是用户的一句大白话、输出是改写结果,而不是让它去点评这份指南。
const ROLE_HEADER = `You are H3-Context-IR, the prompt-rewriting front end of the MiniMax H3 audiovisual generation model. The user gives you a rough idea — often one short sentence, usually in Chinese. Rewrite it into ONE H3 prompt that follows the guide below exactly. Keep the user's subject, action, and intent faithfully; fill in the concrete visual and audio detail the format requires.\n\n`;

// H3 专用输出契约。**与通用契约冲突,不能复用**:通用契约要求「只回提示词正文,不要
// 字段名」,H3 要的恰恰是带字段名的分段结构——回一段光溜溜的散文等于白优化。
const BASE_OUTPUT_CONTRACT = `\n\n---\n\nOutput rules (these override any habit of answering conversationally):

- Output ONLY the rewritten prompt. No explanation, no preface, no closing remark, no markdown fence, no surrounding quotes.
- Keep the field names verbatim and lowercase, in this order, separated by one blank line: \`integrated_multimodal_description:\`, \`overall_soundscape:\`, \`non_diegetic_music:\`.
- If the task has reference frames, the alignment instruction is the first line, followed by one blank line, before the three fields.
- Write everything in English. The only exceptions are the spoken content inside \`<d>\` and text visibly present on screen, which keep their original language verbatim.
- Never echo back the user's original sentence as the prompt.`;

const REF_OUTPUT_CONTRACT = `\n\n---\n\nOutput rules (these override any habit of answering conversationally):

- Output ONLY the rewritten prompt. No explanation, no preface, no closing remark, no markdown fence, no surrounding quotes.
- Keep the field names verbatim and lowercase, in this order, separated by one blank line: \`subject_definitions:\`, \`summary:\`, \`retention_analysis:\`, \`detailed_description:\`, \`overall_soundscape:\`, \`non_diegetic_music:\`.
- Write everything in English. The only exceptions are the spoken content inside \`<d>\` and text visibly present on screen, which keep their original language verbatim.
- Never echo back the user's original sentence as the prompt.`;

const H3_BASE_SYSTEM_PROMPT = ROLE_HEADER + BASE_GUIDE + BASE_OUTPUT_CONTRACT;
const H3_REF_SYSTEM_PROMPT = ROLE_HEADER + REF_GUIDE + REF_OUTPUT_CONTRACT;

// tab → 用哪份 guide。参考生视频是六段式全参考模式(ref-en.txt),其余玩法(文生视频 /
// 图生视频 / 关键帧)都在 base-en.txt 的 T2VA / I2VA / FL2VA / L2VA 四类里。
export const h3OptimizeSystemPrompt = (tabKey) =>
  tabKey === 'r2va' ? H3_REF_SYSTEM_PROMPT : H3_BASE_SYSTEM_PROMPT;

// ── 本次请求的任务上下文 ────────────────────────────────────────────────
// guide 覆盖四类任务,但**优化模型看不到用户传了哪几张图、选了几秒**——只凭一句
// "一只猫在窗台打盹"分不出 I2VA 还是 FL2VA,更写不出对齐指令里那个两位小数的时长。
// 猜错同样不会报错,只会默默出错档,所以把这几个事实显式喂给它。
const durationLine = (seconds) => {
  const n = Number(seconds);
  if (!Number.isFinite(n) || n <= 0) return '';
  return `- Effective video duration: ${n.toFixed(2)} seconds. Every cut timestamp must fall strictly inside it.\n`;
};

export const buildH3OptimizeContext = ({
  tabKey,
  seconds,
  hasFirstFrame = false,
  hasLastFrame = false,
  refImageCount = 0,
  refVideoCount = 0,
  hasRefAudio = false,
} = {}) => {
  let assets = '';
  if (tabKey === 'r2va') {
    const parts = [];
    if (refImageCount > 0) {
      parts.push(
        `${refImageCount} reference image(s), labelled <Picture 1>${refImageCount > 1 ? `..<Picture ${refImageCount}>` : ''}`,
      );
    }
    if (refVideoCount > 0) {
      parts.push(
        `${refVideoCount} reference video(s), labelled <Video 1>${refVideoCount > 1 ? `..<Video ${refVideoCount}>` : ''}`,
      );
    }
    if (hasRefAudio) {
      parts.push(
        '1 reference audio, labelled <Audio 1> (voice-timbre reference)',
      );
    }
    assets = `- Task: full-reference rewrite. Available reference assets: ${parts.length ? parts.join('; ') : 'none yet'}.\n`;
  } else if (hasFirstFrame && hasLastFrame) {
    assets =
      '- Task type: FL2VA. <Picture 1> is the first frame and <Picture 2> is the last frame. Emit the FL2VA alignment instruction, and prefer a single shot.\n';
  } else if (hasLastFrame) {
    assets =
      '- Task type: L2VA. <Picture 1> is the LAST frame, not the first. Emit the L2VA alignment instruction and converge onto it at the end.\n';
  } else if (hasFirstFrame) {
    assets =
      '- Task type: I2VA. <Picture 1> is the first frame. Emit the I2VA alignment instruction and develop forward from it.\n';
  } else if (tabKey === 'text2video') {
    assets =
      '- Task type: T2VA. There is no reference image. Do NOT emit any alignment instruction; begin directly with integrated_multimodal_description.\n';
  }
  const body = assets + durationLine(seconds);
  return body ? `\n\n---\n\nCurrent request:\n\n${body}` : '';
};

// ── 优化结果的切分与回拼 ────────────────────────────────────────────────
// 优化接口返回的仍是一段带字段名的纯文本(不改 /pg/chat/completions 链路),前端按
// 字段名切开渲染成可折叠块,用户改完再按同样格式拼回去提交——所见即所发。
//
// **只认下面这几个字段名**:放开成"任意 xxx:"会把正文里的 "The camera pushes in:"
// 之类切成假字段。切不出来(模型没按格式返回,是常态)就返回 null,由调用方降级成
// 单框显示原文——不能白屏,更不能把没优化的原文吞掉。
const H3_FIELD_KEYS = [
  'subject_definitions',
  'summary',
  'retention_analysis',
  'integrated_multimodal_description',
  'detailed_description',
  'overall_soundscape',
  'non_diegetic_music',
];

// 正文主字段:base 是 integrated_multimodal_description,ref 是 detailed_description。
// 两者都缺就说明这份返回压根不是 H3 结构,按降级处理。
const H3_MAIN_KEYS = [
  'integrated_multimodal_description',
  'detailed_description',
];

export const isH3MainSection = (key) => H3_MAIN_KEYS.includes(key);

// 字段名 → 中文标签。key 为空串的是对齐指令段(Part One):它不是具名字段,但必须
// 原样保留并回拼(里面那个时长/帧序关系一改就错),所以照样渲染成一块,只是默认收起。
export const H3_SECTION_LABELS = {
  '': '画面对齐说明',
  subject_definitions: '参考素材定义',
  summary: '任务摘要',
  retention_analysis: '参考保留分析',
  integrated_multimodal_description: '画面描述',
  detailed_description: '画面描述',
  overall_soundscape: '音景',
  non_diegetic_music: '背景音乐',
};

// 容忍模型顺手加的 markdown 装饰(### 标题 / **加粗**)与中文冒号,但字段名本身必须
// 独占行首——行中出现的同名词不切。
const FIELD_RE = new RegExp(
  `^[ \\t]*(?:#{1,6}[ \\t]*)?\\*{0,2}(${H3_FIELD_KEYS.join('|')})\\*{0,2}[ \\t]*[:：][ \\t]*`,
  'gm',
);

// 返回 [{ key, value, sep }]（key='' 为对齐指令段；sep 记录原文里字段名与正文之间是
// 换行还是空格，回拼时照抄，尽量不改动模型看惯的排版）；不像 H3 结构时返回 null。
export const parseH3Prompt = (raw) => {
  const text = String(raw || '').replace(/\r\n/g, '\n');
  const matches = [];
  FIELD_RE.lastIndex = 0;
  let m;
  while ((m = FIELD_RE.exec(text)) !== null) {
    matches.push({ key: m[1], start: m.index, end: m.index + m[0].length });
  }
  if (!matches.some((x) => isH3MainSection(x.key))) return null;

  const sections = [];
  const preamble = text.slice(0, matches[0].start).trim();
  if (preamble) sections.push({ key: '', value: preamble, sep: ' ' });
  matches.forEach((cur, i) => {
    const stop = i + 1 < matches.length ? matches[i + 1].start : text.length;
    sections.push({
      key: cur.key,
      value: text.slice(cur.end, stop).trim(),
      sep: text[cur.end] === '\n' ? '\n' : ' ',
    });
  });
  return sections;
};

export const joinH3Prompt = (sections) =>
  (sections || [])
    .map((s) =>
      s.key ? `${s.key}:${s.sep === '\n' ? '\n' : ' '}${s.value}` : s.value,
    )
    .join('\n\n')
    .trim();
