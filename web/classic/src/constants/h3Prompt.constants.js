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

// ── 中文分段输入 ────────────────────────────────────────────────────────
// 输入侧固定三段:画面描述 / 音景 / 背景音乐,**不按 tab 分叉**。
//
// 参考生视频产出的是六段,但多出来的前三段(subject_definitions / summary /
// retention_analysis)不是用户的创作意图,是**对已上传素材的分析**:谁是 <Subject 1>、
// 编号怎么排、哪个 fully_preserved 哪个只参考音色,全取决于素材本身。让用户用中文填
// 一个「参考保留分析」,他要么填不出来,要么填的与实际传的素材对不上,反而污染优化
// 输入。那三段交给优化模型按素材清单生成 —— buildH3OptimizeContext 已经把图/视频/
// 音频的数量与 <Picture N> 标号喂进去了。
//
// 同理,关键帧的对齐指令也不在输入侧:它整句都是事实(见下面 h3AlignmentInstruction)。
export const H3_INPUT_FIELDS = [
  {
    key: 'main',
    label: '画面描述',
    // 展开的那一段 = 现有的提示词输入框本身,占位沿用各玩法原有文案。
    defaultOpen: true,
  },
  {
    key: 'overall_soundscape',
    label: '音景',
    placeholder: '现场声:风雨、脚步、器物碰撞、呼吸(留空则由模型自己配)',
    defaultOpen: false,
  },
  {
    key: 'non_diegetic_music',
    label: '背景音乐',
    placeholder:
      '只有观众听得见的那层配乐:乐器、速度、起伏(留空则由模型自己配)',
    defaultOpen: false,
  },
];

// 正文主字段名:base 三段式是 integrated_multimodal_description,参考生视频是
// detailed_description。两处字段名不同但在界面上是同一段「画面描述」。
export const h3MainKey = (tabKey) =>
  tabKey === 'r2va'
    ? 'detailed_description'
    : 'integrated_multimodal_description';

// ── 对齐指令(Part One)────────────────────────────────────────────────
// 告诉 H3「你给的那张图对应目标视频的第几秒」。三个模板在 base-en.txt §2.1 里是逐字
// 写死的,这里**必须原样照抄**(含那个 em dash),改一个字符就不再是 guide 里的形状。
//
// 它整句都是事实而非创作:0.00 / S.SS / 首尾帧角色写错,引擎一律不报错(H3 对 prompt
// 不做解析),只会默默把尾帧当首帧用、或把图对齐到一个不存在的时刻。所以既不让用户
// 手写,在优化结果里也按只读段展示。
//
// N(实际最终镜号)在没有正文镜头划分时取 1 —— guide 自己的 Case 3(FL2VA)与 Case 4
// (L2VA)都是单镜头,写的正是 (from Shot 1) / (from [Shot 1])。
//
// 时长拿不到时**整句省略而不是填个 0.00**:少一句对齐指令模型还能按语义猜,填一个
// 错的秒数则是明确的错误指令。
export const h3AlignmentInstruction = ({
  tabKey,
  seconds,
  hasFirstFrame = false,
  hasLastFrame = false,
} = {}) => {
  if (tabKey === 'r2va') return '';
  const n = Number(seconds);
  const dur = Number.isFinite(n) && n > 0 ? n.toFixed(2) : '';
  if (hasFirstFrame && hasLastFrame) {
    return dur
      ? `How the reference pictures align with the target video — Picture 1 (from Shot 1) aligns with the 0.00-second mark of the target video; Picture 2 (from Shot 1) aligns with the ${dur}-second mark of the target video.`
      : '';
  }
  if (hasLastFrame) {
    return dur
      ? `How the reference pictures align with the target video — <Picture 1> (from [Shot 1]) aligns with the ${dur}-second mark of the target video.`
      : '';
  }
  if (hasFirstFrame) {
    return 'For the target video, at 0.00 seconds into the target video, <Picture 1> (from [Shot 1]) is fully referenced.';
  }
  return '';
};

// ── 不点「AI 优化」时的本地兜底 ─────────────────────────────────────────
// 优化按钮可能**根本不存在**:usePromptOptimize 里 available 要求运营配了优化模型,
// 没配就不渲染按钮。所以「不优化不让发」会把整个玩法对这些部署锁死,兜底必须存在。
//
// 做的事只有一件:把三段中文按 H3 的字段名拼出去,再补上前端能 100% 拼对的对齐指令。
// 正文是中文仍然是劣化(guide 的示例与 IR 层见过的都是英文),但字段结构对了,比把
// 一句中文散文裸发上去强 —— 而后者正是改动前的行为。
//
// **空段省略,不补 N/A**:按 guide,`overall_soundscape: N/A` 的语义是「用户明确要求
// 全程静音」,`non_diegetic_music: N/A` 是「无配乐」。用户留空想说的是「随便,你看着
// 办」,补 N/A 等于替他下了一个他没下的指令。省略才是把这块交还给模型。
export const buildLocalH3Prompt = (fields, ctx = {}) => {
  const parts = [];
  const align = h3AlignmentInstruction(ctx);
  if (align) parts.push(align);
  const main = (fields?.main || '').trim();
  if (main) parts.push(`${h3MainKey(ctx.tabKey)}: ${main}`);
  ['overall_soundscape', 'non_diegetic_music'].forEach((key) => {
    const v = (fields?.[key] || '').trim();
    if (v) parts.push(`${key}: ${v}`);
  });
  return parts.join('\n\n');
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

// 派生段:不是用户写的,是从素材与参数推出来的。对齐指令(key='')每个数字都是事实;
// 参考生视频那三段是对已上传素材的分析(谁是 <Subject 1>、保留到什么程度)。
// 用户手改它们只会改错,故在优化结果里按只读展示 —— 仍然完整回拼提交,只是不给编辑。
const H3_DERIVED_KEYS = [
  '',
  'subject_definitions',
  'summary',
  'retention_analysis',
];

export const isH3DerivedSection = (key) => H3_DERIVED_KEYS.includes(key);

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

// 某个字段名是否已在文本里独占行首出现过。
//
// 给降级路径用:parseH3Prompt 判空的依据是**没找到主字段**,不是「一个字段名都没有」
// —— 返回里带着 overall_soundscape: 却缺主字段是常见的失败形态。那种时候若再把用户
// 填的音景原样追加一遍,同一字段就会在提示词里出现两次。装饰容忍度与 FIELD_RE 一致,
// 两处必须同步改。
export const h3HasField = (text, key) =>
  new RegExp(
    `^[ \\t]*(?:#{1,6}[ \\t]*)?\\*{0,2}${key}\\*{0,2}[ \\t]*[:：]`,
    'm',
  ).test(String(text || ''));

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
