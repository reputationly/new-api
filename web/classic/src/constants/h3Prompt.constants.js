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
const ROLE_HEADER = `You are H3-Context-IR, the prompt-rewriting front end of the MiniMax H3 audiovisual generation model. The user gives you a rough idea — often one short sentence, usually in Chinese, sometimes in English or a mix of both. Rewrite it into ONE H3 prompt that follows the guide below exactly. Keep the user's subject, action, and intent faithfully; fill in the concrete visual and audio detail the format requires.\n\n`;

// 语言 与「不要这层声音」两条,base / ref 共用。两条 guide 里都写了,但都压不住 few-shot:
//
// 1. 台词语言(guide §4.4)。规则写的是「`<d>` 内逐字保留用户原话,不翻译不改写」,可
//    §5 那几个 case 清一色 `[English]` —— 优化模型照着例子走,用户写的中文台词被译成
//    英文。H3 是音画一起出的,译了就等于画面里的人开口说英文,而引擎不解析 prompt,
//    不会报错。所以必须把「例子只是例子」这句挑明。
//
// 2. 不要音景 / 不要配乐(guide §4.6 / §4.7)。这两段「没有」的写法是 `N/A`,不是不写、
//    更不是写一句话解释。而输入侧那两个框收到的是「不要背景音乐」这类中文否定句,不点
//    破的话模型会把这句否定**翻译成英文**填进字段——那是在描述一段声音,语义正好反了。
const SHARED_OUTPUT_RULES = `- Write everything in English, with two exceptions. (a) Spoken content inside \`<d>\`: copy the user's words character for character and set the language tag to the language they actually wrote that line in — \`<d>[Chinese] 我明天就走了。</d>\` for a Chinese line, \`<d>[English] I leave tomorrow.</d>\` for an English one. Never translate, transliterate, re-punctuate, or paraphrase a spoken line — H3 renders the speech you write, so a translated line makes the character speak the wrong language. The guide's examples all show \`[English]\` because their sample inputs were English; treat the tag as following the input, not as a fixed value. Tag each line independently when different characters speak different languages, and when one line itself mixes languages, keep it verbatim and tag it with the language most of that line is in. (b) Text visibly present on screen, which likewise keeps its original language verbatim inside the double quotes.
- When the user's soundscape or music input says they do NOT want that layer (不要 / 不需要 / 无 / 没有 / 静音 / none / no music / silent …), output exactly \`N/A\` as that field's entire value. Do not translate the refusal into an English sound description — that describes the very sound they asked you to drop. A refusal often carries a clarifier and is still a refusal: \`不要配乐,只留现场环境声。\` means \`non_diegetic_music: N/A\`, and the clarifier belongs to \`overall_soundscape\` instead. But a qualified request is NOT a refusal — \`不要太吵的配乐\` still asks for music, only quieter. Judge by whether the negation governs the layer itself. An input the user left empty is not a refusal either; it still gets a written description.
- Never echo back the user's original sentence as the prompt.`;

// H3 专用输出契约。**与通用契约冲突,不能复用**:通用契约要求「只回提示词正文,不要
// 字段名」,H3 要的恰恰是带字段名的分段结构——回一段光溜溜的散文等于白优化。
const BASE_OUTPUT_CONTRACT = `\n\n---\n\nOutput rules (these override any habit of answering conversationally):

- Output ONLY the rewritten prompt. No explanation, no preface, no closing remark, no markdown fence, no surrounding quotes.
- Keep the field names verbatim and lowercase, in this order, separated by one blank line: \`integrated_multimodal_description:\`, \`overall_soundscape:\`, \`non_diegetic_music:\`.
- If the task has reference frames, the alignment instruction is the first line, followed by one blank line, before the three fields.
${SHARED_OUTPUT_RULES}`;

const REF_OUTPUT_CONTRACT = `\n\n---\n\nOutput rules (these override any habit of answering conversationally):

- Output ONLY the rewritten prompt. No explanation, no preface, no closing remark, no markdown fence, no surrounding quotes.
- Keep the field names verbatim and lowercase, in this order, separated by one blank line: \`subject_definitions:\`, \`summary:\`, \`retention_analysis:\`, \`detailed_description:\`, \`overall_soundscape:\`, \`non_diegetic_music:\`.
${SHARED_OUTPUT_RULES}`;

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
    placeholder:
      '现场声:风雨、脚步、器物碰撞、呼吸(留空则由模型自己配;想全程静音就写「不要」)',
    defaultOpen: false,
  },
  {
    key: 'non_diegetic_music',
    label: '背景音乐',
    placeholder:
      '只有观众听得见的那层配乐:乐器、速度、起伏(留空则由模型自己配;不想要配乐就写「不要」)',
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

// 「这层声音我不要」的判定。不做语义理解,只认一种形状:**否定词直接管住那一层的名词**。
//
// 判据落在「直接」两个字上,而不是「整段是否只有一句否定」:
//   · 「不要配乐,只留现场环境声。」——「不要」紧跟「配乐」,后半句只是补充说明,是拒绝;
//     (这正是 VIDEO_EXAMPLES_H3 里「纯环境音 · 雨夜街头」那条示例的写法)
//   · 「不要太吵的配乐」—— 中间隔了修饰语,否定的是「太吵」不是「配乐」,是**要求**。
// 两类的差别只在名词前有没有插东西,所以名词必须紧跟否定词;插了就一律按描述原样带过去。
// 宁可漏判:漏判只是把中文原样发给 H3(改动前全部如此),误判则把用户明确要的那层声音
// 整段抹成 N/A。
//
// 名词表**按字段分开**,不能合成一张:在「音景」框里写「不要音乐,只要环境音」的人,
// 要的恰恰是环境音;共用名词表会让这句命中,把他要的东西写成 N/A。
//
// **中英两支的松紧刻意不同**,因为修饰语的位置正好相反:中文修饰在名词前
// (`不要太吵的配乐`),名词一旦紧跟否定词,后面就只剩补充说明,可以放开;英文修饰在
// 名词后(`no sound of traffic, just wind` / `no music except the ending`),同样放开
// 尾巴立刻变成误判。所以英文支只允许接标点与 at all / please 这类无实义收尾。
const H3_REFUSAL_NEGATOR =
  '不要|不用|不需要|不想要|不加|不带|去掉|去除|没有|无|禁用';
const H3_REFUSAL_NEGATOR_EN =
  "don'?t want|do not want|there'?s no|there is no|without|no more|not|no|zero|skip|remove|drop|exclude";
// 只有整段就是这几个词时才算(后面不接内容),它们本身就是「全程静音 / 无配乐」。
const H3_SILENCE_WORDS =
  '全程静音|静音|无声|默片|none|n/a|no|silent|silence|muted?';
const H3_REFUSAL_TAIL = '[\\s的了吧呀啊!！。.，,、;；]*';
// 英文支的收尾:只认标点与无实义的语气词。多一个实词就说明后面还有内容,不是拒绝。
const H3_REFUSAL_TAIL_EN =
  '(?:\\s|[,.;!]|at all|whatsoever|please|thank you|thanks?)*';
const H3_SOUND_NOUNS = {
  overall_soundscape: {
    zh: '音景|环境音效|环境音|环境声|现场环境声|现场声|现场音|背景音|音效|声音',
    en: 'ambient sounds?|ambient noise|ambient audio|ambient|ambien[ct]e|ambiance|room tone|soundscape|sound effects?|sfx|sound|audio|noise',
  },
  non_diegetic_music: {
    zh: '背景音乐|配乐|音乐',
    en: 'background music|non-?diegetic music|music|bgm|score|soundtrack|underscore',
  },
};

// 中英名词在两支里都放:中文否定词后接英文名词(`不要 BGM`)与反过来都很常见。
const H3_REFUSAL_RES = Object.fromEntries(
  Object.entries(H3_SOUND_NOUNS).map(([key, { zh, en }]) => [
    key,
    new RegExp(
      `^(?:` +
        `(?:${H3_REFUSAL_NEGATOR})\\s*(?:任何|所有|一切|一点)?\\s*(?:(?:${zh}|${en})[\\s\\S]*|${H3_REFUSAL_TAIL})` +
        `|(?:${H3_REFUSAL_NEGATOR_EN})\\s+(?:(?:any|the|all|an|a)\\s+)?(?:${en}|${zh})${H3_REFUSAL_TAIL_EN}` +
        `|(?:${H3_SILENCE_WORDS})${H3_REFUSAL_TAIL}` +
        `)$`,
      'i',
    ),
  ]),
);

export const isH3SoundRefusal = (key, text) =>
  H3_REFUSAL_RES[key]?.test(String(text || '').trim()) ?? false;

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
//
// 明确写了「不要」则相反:那正是 guide 说的 N/A,原样带过去会让 H3 收到
// `non_diegetic_music: 不要` —— 一句它当描述读的中文。占位文案里把「不想要就写不要」
// 教给了用户,这条路(运营没配优化模型)得同样兑现,不能只有 AI 优化那条认。
export const buildLocalH3Prompt = (fields, ctx = {}) => {
  const parts = [];
  const align = h3AlignmentInstruction(ctx);
  if (align) parts.push(align);
  const main = (fields?.main || '').trim();
  if (main) parts.push(`${h3MainKey(ctx.tabKey)}: ${main}`);
  ['overall_soundscape', 'non_diegetic_music'].forEach((key) => {
    const v = (fields?.[key] || '').trim();
    if (v) parts.push(`${key}: ${isH3SoundRefusal(key, v) ? 'N/A' : v}`);
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
