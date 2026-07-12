// 语音合成(TTS)体验区常量。链路复用视频体验区的异步任务门面
// (POST /pg/videos, task_type=tts),仅参数与结果形态不同(音频 .wav)。
// 状态机/轮询/内容地址等通用工具直接复用 videoPlayground.constants。

export {
  VIDEO_API_ENDPOINTS as AUDIO_API_ENDPOINTS,
  VIDEO_STATUS as AUDIO_STATUS,
  VIDEO_POLL_INTERVAL_MS as AUDIO_POLL_INTERVAL_MS,
  normalizeVideoStatus as normalizeAudioStatus,
  parseProgress,
  buildVideoContentUrl as buildAudioContentUrl,
} from './videoPlayground.constants';

// 语音任务比视频快(短句 RTF~3,数秒~数十秒),但排队深时(单实例 FIFO 8)
// 也可能等几分钟;沿用 4s 间隔,上限略低于视频。
export const AUDIO_POLL_MAX_TIMES = 75; // 约 5 分钟

// 体验区能力标签(= 运营后台「视频模型配置」里给模型声明的能力;中文即值)。
// 新增能力时同步维护后端 constant/model_capability.go 的 AudioCapabilities。
export const AUDIO_PAGE_CAPABILITY = '语音合成';

// 预置音色:IndexTTS-2 官方 demo 示例音(HuggingFace spaces/IndexTeam/IndexTTS-2-Demo
// 的 examples/,官方仓已随「移除 LFS」提交删除)。文件随前端打包(public/audio-presets/),
// 发送时前端 fetch → base64 → metadata.voice,与上传自定义音频走同一条路。
// 换素材:替换 public/audio-presets/ 下的 wav 并按需改本表 label。
// 注:官方素材无 voice_10(发布时即跳号),label 按连续序号展示,与文件名解耦;
// 试听后建议把 label 换成描述性名字(如「温柔女声」)。
export const PRESET_VOICES = [
  { id: 'voice_01', label: '音色 01', url: '/audio-presets/voice_01.wav' },
  { id: 'voice_02', label: '音色 02', url: '/audio-presets/voice_02.wav' },
  { id: 'voice_03', label: '音色 03', url: '/audio-presets/voice_03.wav' },
  { id: 'voice_04', label: '音色 04', url: '/audio-presets/voice_04.wav' },
  { id: 'voice_05', label: '音色 05', url: '/audio-presets/voice_05.wav' },
  { id: 'voice_06', label: '音色 06', url: '/audio-presets/voice_06.wav' },
  { id: 'voice_07', label: '音色 07', url: '/audio-presets/voice_07.wav' },
  { id: 'voice_08', label: '音色 08', url: '/audio-presets/voice_08.wav' },
  { id: 'voice_09', label: '音色 09', url: '/audio-presets/voice_09.wav' },
  { id: 'voice_11', label: '音色 10', url: '/audio-presets/voice_11.wav' },
  { id: 'voice_12', label: '音色 11', url: '/audio-presets/voice_12.wav' },
];

// 「上传自定义音频」在音色下拉里的特殊值。
export const VOICE_UPLOAD_VALUE = '__upload__';

// 上传参考音大小上限(base64 后随请求体走,过大拖慢提交)。
export const VOICE_UPLOAD_MAX_MB = 10;

// 情感预设:选中某情绪 → 前端拼 one-hot 8 维向量发 metadata.emo_vector。
// 维度次序与 IndexTTS-2 一致:[喜,怒,哀,惧,厌恶,低落,惊喜,平静]
// (官方 webui 的 8 个滑块次序)。空值 = 跟随参考音色,不发情感参数。
export const EMOTION_PRESETS = [
  { value: '', label: '跟随音色(默认)' },
  { value: 'happy', label: '喜', index: 0 },
  { value: 'angry', label: '怒', index: 1 },
  { value: 'sad', label: '哀', index: 2 },
  { value: 'afraid', label: '惧', index: 3 },
  { value: 'disgusted', label: '厌恶', index: 4 },
  { value: 'melancholic', label: '低落', index: 5 },
  { value: 'surprised', label: '惊喜', index: 6 },
  { value: 'calm', label: '平静', index: 7 },
];

// 情感值 → one-hot 8 维向量(强度作为该维的值,其余为 0)。未知/空返回 null(不发)。
export const emotionToVector = (emotion, weight) => {
  const preset = EMOTION_PRESETS.find((e) => e.value === emotion && e.value);
  if (!preset) return null;
  const vec = [0, 0, 0, 0, 0, 0, 0, 0];
  const w = typeof weight === 'number' && weight >= 0 && weight <= 1 ? weight : 1;
  vec[preset.index] = w;
  return vec;
};

// 情感强度(emo_alpha)默认值,与官方 demo 默认一致。
export const AUDIO_DEFAULT_EMO_WEIGHT = 0.65;

// 提示词预设(合成文本示例,短剧配音风)。
export const AUDIO_PROMPT_PRESETS = [
  '大家好,欢迎收听今天的节目,我们将带来一段精彩的故事。',
  '你怎么能这样对我?我们说好了要一起走到最后的!',
  '别怕,有我在。无论发生什么,我都会站在你这边。',
  '哈哈哈,真是太有意思了,快跟我说说后来怎么样了?',
];

export const AUDIO_HISTORY_STORAGE_KEY = 'audio_playground_conversations';
export const AUDIO_HISTORY_LIMIT = 10; // 对话段数上限
export const AUDIO_CONV_TURN_LIMIT = 10; // 单段对话生成次数上限

// 语音能力枚举(中文即值,也是体验区标签页名)。与后端 constant/model_capability.go 的
// AudioCapabilities 保持一致。新增能力(如 语音转文字)时两处同步。
export const AUDIO_CAPABILITIES = [AUDIO_PAGE_CAPABILITY];

// 兜底默认:未在「语音模型配置」里显式配置时使用。maxChars=0 表示不限制。
export const AUDIO_DEFAULT_MAX_CHARS = 2000;
export const AUDIO_DEFAULT_REF_AUDIO_MB = VOICE_UPLOAD_MAX_MB;

// 解析 status 中的 AudioModelConfig(字符串或对象)。形如:
//   { default: { maxChars, refAudioMaxMB }, models: { <model>: { capabilities:[], maxChars, refAudioMaxMB } } }
export const parseAudioModelConfig = (raw) => {
  const empty = {
    default: {
      maxChars: AUDIO_DEFAULT_MAX_CHARS,
      refAudioMaxMB: AUDIO_DEFAULT_REF_AUDIO_MB,
    },
    models: {},
  };
  if (!raw) return empty;
  try {
    const parsed = typeof raw === 'string' ? JSON.parse(raw) : raw;
    const def = parsed.default || {};
    const models = {};
    if (parsed.models && typeof parsed.models === 'object') {
      Object.entries(parsed.models).forEach(([name, cfg]) => {
        models[name] = {
          capabilities: normalizeList(cfg?.capabilities),
          maxChars: toPositiveInt(cfg?.maxChars),
          refAudioMaxMB: toPositiveInt(cfg?.refAudioMaxMB),
        };
      });
    }
    return {
      default: {
        maxChars:
          toPositiveInt(def.maxChars) ?? AUDIO_DEFAULT_MAX_CHARS,
        refAudioMaxMB:
          toPositiveInt(def.refAudioMaxMB) ?? AUDIO_DEFAULT_REF_AUDIO_MB,
      },
      models,
    };
  } catch (e) {
    return empty;
  }
};

// 解析非负整数;非法/空返回 null(供 ?? 兜底)。
const toPositiveInt = (v) => {
  const n = parseInt(v, 10);
  return Number.isFinite(n) && n >= 0 ? n : null;
};

// 复用视频配置的列表规范化(去空格/去空/去重)。
const normalizeList = (list) =>
  Array.isArray(list)
    ? Array.from(new Set(list.map((x) => String(x).trim()).filter(Boolean)))
    : [];

// 该模型可用的语音模型集合(勾选了「语音合成」的模型)。
export const getAudioModelSet = (config) => {
  const set = new Set();
  Object.entries(config?.models || {}).forEach(([model, cfg]) => {
    const caps = Array.isArray(cfg?.capabilities) ? cfg.capabilities : [];
    if (caps.includes(AUDIO_PAGE_CAPABILITY)) set.add(model);
  });
  return set;
};

// 字数上限:按模型配置 → 全局默认 → 兜底常量。0 表示不限制。
export const getMaxCharsForModel = (config, model) => {
  const m = config?.models?.[model];
  if (m && m.maxChars != null) return m.maxChars;
  if (config?.default?.maxChars != null) return config.default.maxChars;
  return AUDIO_DEFAULT_MAX_CHARS;
};

// 参考音大小上限(MB):按模型配置 → 全局默认 → 兜底常量。
export const getRefAudioMaxMBForModel = (config, model) => {
  const m = config?.models?.[model];
  if (m && m.refAudioMaxMB != null) return m.refAudioMaxMB;
  if (config?.default?.refAudioMaxMB != null) return config.default.refAudioMaxMB;
  return AUDIO_DEFAULT_REF_AUDIO_MB;
};
