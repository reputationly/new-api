// 体验区「分类 → tab」中央元数据：新「体验区管理」admin 页与各页 tab 显隐过滤
// 共用的唯一真相源。key=稳定标识（分类=侧栏 itemKey / 存储配置键；tab=各页 mode
// key，不用显示名以免改名破坏配置）；capability=该 tab 过滤模型用的能力标签。
// 文本模型（对话）无 tab、无能力标签、无媒体配置（靠排除媒体模型过滤），仅参与分类显隐。

export const PLAYGROUND_CATEGORIES = [
  {
    key: 'playground',
    label: '文本模型',
    configKey: null,
    tabs: [],
  },
  {
    key: 'image',
    label: '图像模型', // 由「图片模型」改名
    configKey: 'ImageModelSizeConfig',
    tabs: [
      { key: 'text2image', label: '文生图', capability: '文生图' },
      { key: 'image2image', label: '图生图', capability: '图生图' },
    ],
  },
  {
    key: 'video',
    label: '视频模型',
    configKey: 'VideoModelConfig',
    tabs: [
      { key: 'text2video', label: '文生视频', capability: '文生视频' },
      { key: 'image2video', label: '图生视频', capability: '图生视频' },
      { key: 'flf2v', label: '关键帧', capability: '关键帧' },
      { key: 's2v', label: '数字人', capability: '数字人' },
      { key: 'vace', label: '视频编辑', capability: '视频编辑' },
    ],
  },
  {
    key: 'audio',
    label: '语音模型',
    configKey: 'AudioModelConfig',
    tabs: [
      { key: 'emotion', label: '情感合成', capability: '情感合成' },
      { key: 'synthesis', label: '语音合成', capability: '语音合成' },
      { key: 'dialogue', label: '双人对话', capability: '双人对话' },
      { key: 'design', label: '声音设计', capability: '声音设计' },
      // 视频配音：入口挂在语音页，产物是视频（走 VideoPlaygroundBody mode=dub）。
      { key: 'dub', label: '视频配音', capability: '视频配音' },
    ],
  },
  {
    key: 'music',
    label: '音乐模型',
    configKey: 'MusicModelConfig',
    tabs: [
      { key: 't2m', label: '文生音乐', capability: '文生音乐' },
      { key: 'cover', label: '音乐改编', capability: '音乐改编' },
      { key: 'repaint', label: '音乐重绘', capability: '音乐重绘' },
      { key: 't2a', label: '文生音效', capability: '文生音效' },
      { key: 'svs', label: '歌声合成', capability: '歌声合成' },
    ],
  },
];

export const getPlaygroundCategory = (key) =>
  PLAYGROUND_CATEGORIES.find((c) => c.key === key) || null;

// ---------------------------------------------------------------------------
// 模型大类（模型广场筛选用）
// ---------------------------------------------------------------------------
// 模型广场按「文本/图像/视频/语音/音乐」五个大类筛选，而不是按单个能力标签（文生图、
// 数字人……）—— 能力标签是 tab 粒度、数量多且只对体验区有意义，做筛选项太碎。
// 大类判定复用体验区那四份 ModelConfig：运营把某模型配进哪份配置，它就属于哪个大类
// （同一份配置也决定它出现在哪个体验区分类页），无需再引入第二套标注。

export const MODEL_CATEGORIES = PLAYGROUND_CATEGORIES.map((c) => ({
  key: c.key,
  label: c.label,
}));

// 文本模型没有 ModelConfig（靠"不是媒体模型"反推），作为兜底大类。
export const MODEL_CATEGORY_TEXT = 'playground';

// 只上架、未配进体验区的模型（如仅供 API 调用的绘图模型）没有配置可查，按端点类型兜底。
// audio-speech 必须排在 openai-video 前：TTS 模型同时声明这两种端点。
const ENDPOINT_CATEGORY_FALLBACK = [
  ['image-generation', 'image'],
  ['audio-speech', 'audio'],
  ['openai-video', 'video'],
];

const parseModelConfig = (raw) => {
  if (!raw) return null;
  if (typeof raw === 'object') return raw;
  try {
    return JSON.parse(raw);
  } catch (e) {
    return null;
  }
};

// 由 /api/status 建「模型名 -> 大类 key」索引。同名模型只认第一份命中的配置。
export const buildModelCategoryIndex = (status) => {
  const index = new Map();
  PLAYGROUND_CATEGORIES.forEach((cat) => {
    if (!cat.configKey) return;
    const parsed = parseModelConfig(status?.[cat.configKey]);
    Object.keys(parsed?.models || {}).forEach((name) => {
      if (!index.has(name)) index.set(name, cat.key);
    });
  });
  return index;
};

// model 需带 model_name 与 supported_endpoint_types（/api/pricing 的形态）。
export const resolveModelCategory = (model, index) => {
  const hit = index?.get(model?.model_name);
  if (hit) return hit;
  const types = model?.supported_endpoint_types || [];
  const fallback = ENDPOINT_CATEGORY_FALLBACK.find(([endpoint]) =>
    types.includes(endpoint),
  );
  return fallback ? fallback[1] : MODEL_CATEGORY_TEXT;
};

// 解析 /api/status 的 PlaygroundTabConfig（{category:{modeKey:bool}}）。
export const parsePlaygroundTabConfig = (raw) => {
  if (!raw) return {};
  if (typeof raw === 'object') return raw;
  try {
    const parsed = JSON.parse(raw);
    return parsed && typeof parsed === 'object' ? parsed : {};
  } catch (e) {
    return {};
  }
};

// tab 是否显示：缺省（未配置）=显示；仅显式 false 才隐藏。
export const isPlaygroundTabVisible = (tabConfig, category, modeKey) => {
  const cat = tabConfig && tabConfig[category];
  if (!cat) return true;
  return cat[modeKey] !== false;
};
