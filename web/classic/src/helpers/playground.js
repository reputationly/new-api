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
