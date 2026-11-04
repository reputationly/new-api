// Qwen-Image-2.1（现网对外叫 qwen-image-pro）的「AI 优化提示词」层。
//
// 官方给 Qwen-Image-2.1 配了两份改写系统提示词（文生图 / 图像编辑），下面是**逐字原文**，
// 用 ?raw 引入 —— 与 H3 的 base-en.txt 同一手法，也与后端 service/qwenpe/ 下那两份是
// 同一份字节。不要在 JS 里改写它们、也不要把它们"精简掉示例"：官方那几段排版/文案规则
// 就是产出形状的定义，删一处等于把定义交回给优化模型去猜，而猜错不报错、只是效果变差。
// 要同步上游版本时整份替换，不要手改。
//
// 后端那条路（直连 API 的聚合模型，prompt_enhance.mode = "qwen_pe"）用的就是这对 txt，
// 外加下面两条我们自己补的规则，拼装顺序也一致 —— 两边产出的系统提示词应当逐字节相同。
//
// 与后端唯一的实质差别：**没有传输检查与重修**。后端会核对 rewritten_prompt 非空、
// 残留 <imageN> 标签、用户写明的可见文案是否逐字保留（含七条针对 qwen3.8 的实测修补），
// 不过就把错清单发回模型重修一轮；体验区是一次性调用，只能靠下面的兜底解析，失败就让
// 用户再点一次。这条差距就是「先按 A 做、量过效果再决定要不要切到 B（体验区也走后端
// 那套）」里要量的东西：如果线上失败率明显偏高，那是该切 B 的信号。
import QWEN_T2I_PROMPT from './qwenpe/system_prompt_t2i.txt?raw';
import QWEN_EDIT_PROMPT from './qwenpe/system_prompt_edit.txt?raw';

// 后端 service/aggregate_enhance_qwenpe.go 里我们自己补的两条规则,**逐字照抄**。
//
// 它们不改官方原文,只针对通用模型(运营配的优化模型,如 qwen3.8)偏离官方要求的地方
// 做补充。改一处必须同时改两处 —— 两处漂移不会报错,只会让体验区与直连 API 的改写产出
// 慢慢各走一路。
const QWEN_ESCAPE_RULE = `JSON escaping: the whole reply is parsed with a strict JSON parser. Every straight double quote that appears INSIDE a string value — including the quotes around text rendered in the image — must be escaped as \\" (for example: {"rewritten_prompt": "a sign that reads \\"OPEN\\""}). Never emit a bare " inside a value. The reply must begin with { and end with } — no code fences, no tags, no words before or after the object.

Additional rules (they refine, never override, the instructions above):
- Orientation first. Before writing, decide the canvas from the subject's natural shape: labels, tables, menus, couplets, posters, phone screens, book pages and standing figures are vertical; landscapes, desks, storefronts and wide signs are horizontal; icons, dials and badges are square. Never fall back to 3:2 out of habit — pick it only when the subject is genuinely horizontal.
- Commit. Give every object and every string of text one exact position and size. Never hedge with "or", "such as", "possibly", "或", "如", "可能" — decide, and state it as fact.
- Content before finish. The opening sentence names the medium, style, subject and orientation. Then describe what is in the frame — subjects, layout, every text string in quotes — and only after that the lighting, materials and mood.
- When several input images are combined into a new scene, there is no canvas: set wh_ratio to the ratio you chose and leave ratio_follow empty. ratio_follow is only for editing one existing picture.
- Read the request narrowly. Change exactly the element the user named and nothing next to it (a "main title" is only the largest title line, not the subtitle). When the input contains readable text that must survive, list each of those strings verbatim in the preservation clause instead of summarising them.
- Objects taken from an input image keep their own materials and colours exactly: clear glass stays clear, a liquid keeps its tint, a label keeps its text and layout. Scene lighting may add highlights, reflections and shadows on them, but it never recolours them — describe the object's own colour, then the light falling on it, as two separate facts.
- When several objects are arranged together, give each its own non-overlapping spot on the surface with a concrete position ("upper left", "lower right", "centre"), resting flat or standing on the surface. Objects never stack on, lean against or overlap each other unless the user asks for that.
- When editing text, replace only the characters the user changed; the label or words next to them (such as "电话" before a phone number) stay exactly where they are.`;

// 只给**文生图**加(编辑侧加了反而更差,见后端同名常量的注释:编辑侧多图 <imageN> 用齐
// 从 12/12 掉到 10/12)。作用是让模型把一段很短的分析写成 JSON 的第一个字段,把"思考"
// 搬进输出里 —— 关思考后最明显的退化是画幅不稳。
const QWEN_ANALYSIS_RULE = `Think inside the reply. Make "analysis" the FIRST key of the JSON object, before rewritten_prompt:
{"analysis": {"fixed": ["every text string the user wants shown, verbatim", "every named object, count, colour, position"], "canvas": "for edits: which input image is the canvas, what changes, what stays; for new scenes: none", "orientation": "vertical | horizontal | square — and the subject-based reason in ten words", "ratio": "the wh_ratio or ratio_follow you will output"}, "rewritten_prompt": "...", "wh_ratio": "...", "ratio_follow": "..."}
Keep analysis under 80 words. It is discarded by the caller — only rewritten_prompt and the ratio fields are used — so decide everything in analysis first, then write rewritten_prompt and the ratio fields so they agree with it exactly.`;

// 官方编辑提示词的收尾句，它的下一行就是用户要改写的指令本身。
const QWEN_EDIT_CLOSING = "The user's edit instruction to rewrite is:";

const trimEnd = (s) => String(s || '').replace(/\s+$/, '');

// 拼装方式照后端 buildQwenPESystem：
//   - 文生图：官方原文 + 转义说明 + 「先分析后改写」；
//   - 编辑：转义说明必须插在收尾句**之前** —— 插在它后面，模型会把这段英文当成要改写的
//     指令本身（收尾句的语义就是"下面紧跟原文"）。
// 两条都不加篇幅要求：任何篇幅要求都会把画幅自洽率拉回 67~80%（后端实测）。
const QWEN_T2I_SYSTEM = `${trimEnd(QWEN_T2I_PROMPT)}\n\n${QWEN_ESCAPE_RULE}\n\n${QWEN_ANALYSIS_RULE}\n`;
const QWEN_EDIT_BODY = trimEnd(QWEN_EDIT_PROMPT);
const QWEN_EDIT_SYSTEM = QWEN_EDIT_BODY.endsWith(QWEN_EDIT_CLOSING)
  ? `${trimEnd(QWEN_EDIT_BODY.slice(0, -QWEN_EDIT_CLOSING.length))}\n\n${QWEN_ESCAPE_RULE}\n\n${QWEN_EDIT_CLOSING}\n`
  : // 上游换了措辞时退回追加末尾：宁可位置差一点，也别丢掉转义说明（后端同一兜底）
    `${QWEN_EDIT_BODY}\n\n${QWEN_ESCAPE_RULE}\n`;

// 图像两个 tab 之外返回空串 —— 这个引擎族没有对应的改写知识，硬套只会写出模型不认的
// 字段名（调用方据此回落通用模板）。
export const qwenImageOptimizeSystemPrompt = (tabKey) => {
  if (tabKey === 'text2image') return QWEN_T2I_SYSTEM;
  if (tabKey === 'image2image') return QWEN_EDIT_SYSTEM;
  return '';
};

// 这张模板的「下面就是原文」收尾句，供 appendOptimizeContext 判插入位置用。
// 与后端 qwenPEEditClosing 是同一个判据（跨语言只能各写一份，改一处必须改两处）。
export const QWEN_EDIT_CLOSING_MARKER = `\n${QWEN_EDIT_CLOSING}`;

// 从模型回复里取出改写结果。**只取 rewritten_prompt**：官方约定的另外两个字段
// （wh_ratio / ratio_follow）决定画幅，而体验区的画幅是用户在左侧面板选的、由 size 下发，
// 忽略它们不影响出图 —— "按这个画幅构图"这件事由 buildImageOptimizeContext 拼在系统
// 提示词末尾的那行 Target canvas 负责（缺了它，模型会写出与画布打架的构图）。
//
// 取法与后端 extractQwenPE 一致，两级：
//   1. 合法 JSON：容忍 ``` 围栏与前后废话（官方脚本也是"取首个 { 到末个 }"这么兜的）；
//   2. 裸引号兜底：正文里只要有一个没转义的引号，整段 JSON.parse 就失败，而字段本身
//      往往完好。实测四家模型都会犯这个，按字段名把整段抠出来即可。
// 两级都取不到返回空串，由调用方报错 —— **绝不把原样 JSON 填回输入框**：那会把一段
// analysis + 比例字段当成提示词发给生成模型。
const unescapeJsonString = (s) =>
  String(s).replace(/\\(["nt\\])/g, (_, ch) =>
    ch === 'n' ? '\n' : ch === 't' ? '\t' : ch,
  );

const QWEN_LOOSE_REWRITTEN =
  /"rewritten_prompt"\s*:\s*"([\s\S]*?)"\s*,\s*"wh_ratio"/;

export const extractQwenRewrittenPrompt = (text) => {
  const raw = String(text || '').trim();
  if (!raw) return '';
  const unfenced = raw.replace(/^```(?:\w+)?\s*/i, '').replace(/\s*```$/, '');
  const start = unfenced.indexOf('{');
  const end = unfenced.lastIndexOf('}');
  if (start >= 0 && end > start) {
    try {
      const parsed = JSON.parse(unfenced.slice(start, end + 1));
      const picked =
        typeof parsed?.rewritten_prompt === 'string'
          ? parsed.rewritten_prompt.trim()
          : '';
      if (picked) return picked;
    } catch (e) {
      // 落到裸引号兜底
    }
  }
  const m = QWEN_LOOSE_REWRITTEN.exec(raw);
  if (m) {
    const picked = unescapeJsonString(m[1]).trim();
    if (picked) return picked;
  }
  return '';
};
