// 文生音乐(ACE-Step)体验区常量。链路复用视频体验区的异步任务门面
// (POST /pg/videos),按 mode 映射 task_type=t2m/cover/repaint,结果为音频(.mp3)。
// 通用状态机/轮询/内容地址等工具直接复用 videoPlayground.constants。

export {
  VIDEO_API_ENDPOINTS as MUSIC_API_ENDPOINTS,
  VIDEO_STATUS as MUSIC_STATUS,
  VIDEO_POLL_INTERVAL_MS as MUSIC_POLL_INTERVAL_MS,
  normalizeVideoStatus as normalizeMusicStatus,
  parseProgress,
  buildVideoContentUrl as buildMusicContentUrl,
} from './videoPlayground.constants';

// 音乐生成较慢(30~120s 的曲子,单实例 FIFO),沿用 4s 间隔,上限同视频。
export const MUSIC_POLL_MAX_TIMES = 90; // 约 6 分钟后超时

// 三种玩法(= 门面 task_type)。中文能力标签即体验区标签页名,新增时同步维护
// 后端 constant/model_capability.go 的 MusicCapabilities。
export const MUSIC_T2M_CAPABILITY = '文生音乐';
export const MUSIC_COVER_CAPABILITY = '音乐改编';
export const MUSIC_REPAINT_CAPABILITY = '音乐重绘';
export const MUSIC_CAPABILITIES = [
  MUSIC_T2M_CAPABILITY,
  MUSIC_COVER_CAPABILITY,
  MUSIC_REPAINT_CAPABILITY,
];

// mode → 门面契约映射。needsAudio 的模式在配置面板要求上传驱动音频,经 metadata
// 的 audioMetaKey 透传(adaptor 物化为 NFS input_refs → 引擎)。
export const MUSIC_MODES = {
  t2m: {
    taskType: 't2m',
    capability: MUSIC_T2M_CAPABILITY,
    needsAudio: false,
    audioMetaKey: '',
  },
  cover: {
    taskType: 'cover',
    capability: MUSIC_COVER_CAPABILITY,
    needsAudio: true,
    audioMetaKey: 'reference_audio',
  },
  repaint: {
    taskType: 'repaint',
    capability: MUSIC_REPAINT_CAPABILITY,
    needsAudio: true,
    audioMetaKey: 'src_audio',
  },
};

// 时长预设(秒),经 metadata.audio_duration 透传给引擎。'' = 引擎默认(不下发)。
export const MUSIC_DURATIONS = ['', '30', '60', '90', '120'];
export const MUSIC_DEFAULT_DURATION = '';

// 提示词预设(风格/描述 caption,点击填入输入框)。取自 ACE-Step 官方
// examples/simple_mode 的 description 风格(自然语言描述,sample 模式据此自动配词),
// 刻意拉开风格分布:人声抒情 / 国风电子 / 影视器乐 / 冥想器乐,快慢与人声器乐都覆盖。
export const MUSIC_PROMPT_PRESETS = [
  '一首深情的中文抒情歌曲,适合夜晚独自聆听',
  '中国风电子舞曲,融合古典乐器与现代节拍',
  '磅礴大气的史诗级电影配乐,气势恢宏震撼人心',
  '空灵的禅意音乐,适合瑜伽冥想',
];

// 演唱语言(metadata.vocal_language)。'' = 不指定(sample 模式自动检测);
// unknown = 纯器乐。取自 ACE-Step constants.py VALID_LANGUAGES 的常用子集。
export const MUSIC_VOCAL_LANGUAGES = [
  { value: '', label: '自动' },
  { value: 'zh', label: '中文' },
  { value: 'yue', label: '粤语' },
  { value: 'en', label: '英文' },
  { value: 'ja', label: '日文' },
  { value: 'ko', label: '韩文' },
  { value: 'unknown', label: '纯器乐' },
];

// 高级参数默认(仅作输入框占位提示;留空即不下发,走引擎默认)。
export const MUSIC_DEFAULT_GUIDANCE = 7.0;
export const MUSIC_DEFAULT_STEPS = 8;

// 上传参考/源音大小上限(MB;base64 随请求体走,过大拖慢提交)。
export const MUSIC_AUDIO_UPLOAD_MAX_MB = 20;

// 历史 localStorage 键按 mode 区分(t2m/cover/repaint 各自独立历史)。
export const MUSIC_HISTORY_STORAGE_PREFIX = 'music_playground_conversations';
export const musicHistoryStorageKey = (mode) =>
  `${MUSIC_HISTORY_STORAGE_PREFIX}_${mode}`;
export const MUSIC_HISTORY_LIMIT = 10; // 对话段数上限
export const MUSIC_CONV_TURN_LIMIT = 10; // 单段对话生成次数上限

// 音乐能力枚举(中文即值)。与后端 constant/model_capability.go 的 MusicCapabilities 一致。
export { MUSIC_CAPABILITIES as MUSIC_ALL_CAPABILITIES };

// 兜底默认:未在「音乐模型配置」里显式配置时使用。maxChars=0 表示不限制。
export const MUSIC_DEFAULT_MAX_CHARS = 2000;
export const MUSIC_DEFAULT_REF_AUDIO_MB = MUSIC_AUDIO_UPLOAD_MAX_MB;

// 解析非负整数;非法/空返回 null(供 ?? 兜底)。
const toPositiveInt = (v) => {
  const n = parseInt(v, 10);
  return Number.isFinite(n) && n >= 0 ? n : null;
};

// 列表规范化(去空格/去空/去重)。
const normalizeList = (list) =>
  Array.isArray(list)
    ? Array.from(new Set(list.map((x) => String(x).trim()).filter(Boolean)))
    : [];

// 解析 status 中的 MusicModelConfig(字符串或对象)。形如:
//   { default: { maxChars, refAudioMaxMB }, models: { <model>: { capabilities:[], maxChars, refAudioMaxMB } } }
export const parseMusicModelConfig = (raw) => {
  const empty = {
    default: {
      maxChars: MUSIC_DEFAULT_MAX_CHARS,
      refAudioMaxMB: MUSIC_DEFAULT_REF_AUDIO_MB,
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
        maxChars: toPositiveInt(def.maxChars) ?? MUSIC_DEFAULT_MAX_CHARS,
        refAudioMaxMB:
          toPositiveInt(def.refAudioMaxMB) ?? MUSIC_DEFAULT_REF_AUDIO_MB,
      },
      models,
    };
  } catch (e) {
    return empty;
  }
};

// 指定能力(= 当前 tab)的音乐模型集合(勾选了该能力的模型)。
export const getMusicModelSet = (config, capability) => {
  const set = new Set();
  Object.entries(config?.models || {}).forEach(([model, cfg]) => {
    const caps = Array.isArray(cfg?.capabilities) ? cfg.capabilities : [];
    if (caps.includes(capability)) set.add(model);
  });
  return set;
};

// 字数上限:按模型配置 → 全局默认 → 兜底常量。0 表示不限制。
export const getMaxCharsForModel = (config, model) => {
  const m = config?.models?.[model];
  if (m && m.maxChars != null) return m.maxChars;
  if (config?.default?.maxChars != null) return config.default.maxChars;
  return MUSIC_DEFAULT_MAX_CHARS;
};

// 参考音大小上限(MB):按模型配置 → 全局默认 → 兜底常量。
export const getRefAudioMaxMBForModel = (config, model) => {
  const m = config?.models?.[model];
  if (m && m.refAudioMaxMB != null) return m.refAudioMaxMB;
  if (config?.default?.refAudioMaxMB != null)
    return config.default.refAudioMaxMB;
  return MUSIC_DEFAULT_REF_AUDIO_MB;
};
