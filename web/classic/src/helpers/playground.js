import { PLAYGROUND_UNSUPPORTED_ENDPOINTS } from '../constants/playground.constants';

// 当前模型是否可在操练场调试。
// 规则：endpoint_types 里有任何一个**已知的非 chat 端点**就拦截。
// 列表为空 / 拉不到 / 模型不在 map → 放行（fail-open）。
//
// 注意：不能用「至少有一个 chat 端点就放行」的反向逻辑——后端的
// GetEndpointTypesByChannelType 会给纯 image-gen 模型（dall-e-3 / gpt-image-1 / flux-*）
// 自动加上 openai 兜底端点，这只是结构上的产物，并不代表模型真能 chat。
export function isPlaygroundSupported(model, modelEndpointTypes) {
  if (!model || !modelEndpointTypes) return true;
  const types = modelEndpointTypes.get(model);
  if (!types || types.length === 0) return true;
  return !types.some((t) => t in PLAYGROUND_UNSUPPORTED_ENDPOINTS);
}

// 语言模型判据：仅保留 chat completions 兼容端点，排除嵌入/重排序/音频/视频/图片。
// 纯图片模型后端会附带 openai 兜底端点，故用"含 chat 且不含任一非 chat"双条件。
// 注意：不含 openai-response —— 调用方（音乐翻译、提示词 AI 优化）都固定打
// /pg/chat/completions，仅声明 Responses 端点的模型走 chat completions 会失败，
// 不应列入（同时含 openai 的仍保留）。
//
// 放在这里而不是各调用点各写一份：判据只有一份，端点类型增补时不会两边分叉。
const CHAT_ENDPOINT_TYPES = ['openai', 'anthropic', 'gemini'];
const NON_CHAT_ENDPOINT_TYPES = [
  'embeddings',
  'jina-rerank',
  'audio-speech',
  'openai-video',
  'image-generation',
];

// types = /api/pricing 行的 supported_endpoint_types 数组。
export function isChatModel(types) {
  if (!Array.isArray(types) || types.length === 0) return false;
  const hasChat = types.some((x) => CHAT_ENDPOINT_TYPES.includes(x));
  const hasNonChat = types.some((x) => NON_CHAT_ENDPOINT_TYPES.includes(x));
  return hasChat && !hasNonChat;
}

// 体验区里那些「单次非流式打 /pg/chat/completions」的辅助调用（AI 优化提示词、
// AI 优化提示词、中译英）失败时，判断报错是不是「模型/分组配错了」这一类 ——
// 是的话给用户一句能行动的提示，而不是把原始报错甩出去。
//
// **不能按 403 一刀切**：/pg 这条路上 distributor.go 还有渠道禁用（:51）、亲和渠道
// 禁用（:108）、令牌 ACL（:62,:72）等多种 403，把它们也指向「检查模型与分组配置」
// 等于给出错误的排查方向。
//
// 这几处 abortWithOpenAiMessage 都没传 ErrorCode，没有稳定的机器可读标识，只能匹配
// 文案。下面这些串**逐字来自 i18n/locales/{en,zh-CN,zh-TW}.yaml 的
// distributor.group_access_denied 与 distributor.no_available_channel**（`_with_hint`
// 变体含同样关键词，一并覆盖）。注意繁体用「管道」不是「渠道」、英文是
// "No permission to access this group" —— 凭印象写正则会漏掉它们。后端哪天改了文案
// 这里就失效，根治办法是给那几处补上 ErrorCode 再按 code 判。
const PLAYGROUND_CONFIG_ISSUE_RE =
  /无权访问该分组|無權存取該分組|No permission to access this group|无可用渠道|無可用管道|No available channel/i;

export function isPlaygroundConfigIssue(message) {
  return PLAYGROUND_CONFIG_ISSUE_RE.test(String(message || ''));
}

// 从模型回复里取出 Render JSON（SenseNova-U1.5 的 Image PE 产物）。
//
// 逻辑照搬官方 src/sensenova_u1_5/image_pe.py 的 extract_json：剥掉 ``` 围栏，再取**首个
// `{` 到末个 `}`** 之间的部分。后半条不是多余的保险 —— 模型在 JSON 前后多说一句
// （"Here is the render brief:"）是常态，官方脚本正是这么兜的。
//
// 与官方的一处差别：官方 parse 失败就抛错终止，我们**降级返回原文**。理由是这里的产物
// 直接回填用户输入框：拿到一段不合规的文本，用户还能自己改改再发；抛错等于把这次优化
// 的结果整个丢掉，对用户更糟。
//
// 解析成功也只返回原始切片、不重新序列化：官方打印的是紧凑 JSON，但那只是 CLI 的事，
// 保留模型自己的换行缩进在输入框里可读得多，而作为提示词两者等价。
export function extractRenderJson(text) {
  const original = String(text || '').trim();
  const s = original.replace(/^```(?:\w+)?\s*/i, '').replace(/\s*```$/, '');
  const start = s.indexOf('{');
  const end = s.lastIndexOf('}');
  if (start < 0 || end < start) return original;
  const sliced = s.slice(start, end + 1);
  // 只 parse 一次用来判合规，不接结果:切片必然以 { 开头、} 结尾,parse 成功时一定是
  // 非空对象,所以「是不是 object / 是不是数组」那类判断在这里全都不可达 —— 写了也是
  // 死代码,而死代码会让"我测过了"变成错觉(那条断言实际走的是上面的 start < 0)。
  try {
    JSON.parse(sliced);
  } catch (e) {
    return original;
  }
  return sliced;
}

// 剥掉推理模型混在正文里的思考段。
//
// 上面那三处辅助调用(AI 优化提示词 / 中译英 / ACE-Step 生成方案)拿到 content 之后
// 都直接用:优化的结果回填输入框、翻译的结果发给引擎、方案的结果 JSON.parse。
// 一旦运营把辅助模型配成推理模型(Qwen3.8、DeepSeek V4 Flash 这类现在是常态),
// 而上游又把思考拼进 content 而不是单独的 reasoning_content 字段,后果分别是:
// 输入框里灌进一整段思考过程(不报错)、把思考发给生成引擎、JSON.parse 直接失败。
//
// **取最后一个 </think> 之后的部分**,而不是用 constants/playground.constants.js 的
// THINK_TAG_REGEX 去匹配成对标签 —— 那个正则要求 <think> 与 </think> 都在正文里,
// 而最常见的形态恰恰是只有闭标签:很多模型的 chat template 会在助手轮的开头就替模型
// 写好 <think>,于是 completion 里只剩「思考…</think>\n\n正文」。用成对匹配的正则
// 一个字都剥不掉。取最后一个闭标签之后的做法对两种形态都成立。
//
// 没有闭标签时原样返回(包括「只有 <think> 开头、被截断」这种):这时正文本就不存在,
// 与其猜着截,不如让调用方按「模型未返回内容」报错——那是实话。
export function stripModelThinking(text) {
  const s = String(text || '');
  const end = s.lastIndexOf('</think>');
  return end === -1 ? s : s.slice(end + '</think>'.length);
}

// 一个模型挂多个非 chat 端点时，按 priority 选第一个用于弹框展示。
export function pickPrimaryUnsupportedEndpoint(modelEndpointTypes, model) {
  const types = modelEndpointTypes?.get?.(model) || [];
  let picked = null;
  for (const t of types) {
    const cfg = PLAYGROUND_UNSUPPORTED_ENDPOINTS[t];
    if (!cfg) continue;
    if (!picked || cfg.priority < picked.priority) {
      picked = { type: t, ...cfg };
    }
  }
  return picked;
}

// 生成可直接 paste 的 curl 字符串。
// API Key 用占位符 $YOUR_API_KEY；origin 由调用方传入（优先 API origin，fallback 到 window）。
// 注意：body 中的单引号要用 POSIX shell 的 '\'' 形式转义，避免 prompt 含 don't 这类
// 单撇号时把外层 -d '...' 的单引号字符串截断。
export function buildCurlExample(model, endpoint, userPrompt, origin) {
  if (!endpoint) return '';
  const body = endpoint.buildBody(model, userPrompt || '');
  const bodyStr = JSON.stringify(body, null, 2)
    .split('\n')
    .map((line, idx) => (idx === 0 ? line : `    ${line}`))
    .join('\n');
  const safeBody = bodyStr.replace(/'/g, "'\\''");
  return [
    `curl -X POST '${origin}${endpoint.path}' \\`,
    `  -H 'Authorization: Bearer $YOUR_API_KEY' \\`,
    `  -H 'Content-Type: application/json' \\`,
    `  -d '${safeBody}'`,
  ].join('\n');
}

// 返回 curl 应当指向的 API origin。
// 优先级：构建期注入的 VITE_REACT_APP_SERVER_URL（dashboard 与 API 跨域部署）→ 当前 window.location.origin。
export function getApiOrigin() {
  const envUrl = import.meta.env?.VITE_REACT_APP_SERVER_URL;
  if (envUrl && /^https?:\/\//.test(envUrl)) {
    return envUrl.replace(/\/$/, '');
  }
  return window.location.origin;
}
