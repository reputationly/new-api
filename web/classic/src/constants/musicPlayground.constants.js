// 音乐模型体验区常量。链路复用视频体验区的异步任务门面(POST /pg/videos),按 mode 映射
// task_type。涵盖两类引擎:
//   - ACE-Step 文生音乐/音乐改编/音乐重绘(t2m/cover/repaint),结果为音频(.mp3);
//   - AudioX + SoulX-Singer 扩散音频(文生音效/视频配音效/视频配乐/歌声合成
//     = t2a/v2a/tv2a/v2m/tv2m/svs),结果为音频(.wav)。
// 通用状态机/轮询/内容地址等工具直接复用 videoPlayground.constants;结果播放/下载按返回的
// content-url + media-type 处理,格式无关(见 MusicChatArea)。

export {
  VIDEO_API_ENDPOINTS as MUSIC_API_ENDPOINTS,
  VIDEO_STATUS as MUSIC_STATUS,
  VIDEO_POLL_INTERVAL_MS as MUSIC_POLL_INTERVAL_MS,
  normalizeVideoStatus as normalizeMusicStatus,
  parseProgress,
  buildVideoContentUrl as buildMusicContentUrl,
} from './videoPlayground.constants';

// 音乐/扩散音频生成较慢(30~120s 的曲子 / AudioX 默认 250 步,单实例 FIFO),沿用 4s
// 间隔,上限同视频。
export const MUSIC_POLL_MAX_TIMES = 90; // 约 6 分钟后超时

// 七个能力标签(= 体验区子标签页名;中文即值)。前三个为 ACE-Step,后四个为 AudioX/SoulX。
// 与后端 constant/model_capability.go 的 MusicCapabilities 保持一致(新增能力两处同步)。
export const MUSIC_T2M_CAPABILITY = '文生音乐';
export const MUSIC_COVER_CAPABILITY = '音乐改编';
export const MUSIC_REPAINT_CAPABILITY = '音乐重绘';
export const MUSIC_T2A_CAPABILITY = '文生音效';
export const MUSIC_V2A_CAPABILITY = '视频配音效';
export const MUSIC_V2M_CAPABILITY = '视频配乐';
export const MUSIC_SVS_CAPABILITY = '歌声合成';
export const MUSIC_CAPABILITIES = [
  MUSIC_T2M_CAPABILITY,
  MUSIC_COVER_CAPABILITY,
  MUSIC_REPAINT_CAPABILITY,
  MUSIC_T2A_CAPABILITY,
  MUSIC_V2A_CAPABILITY,
  MUSIC_V2M_CAPABILITY,
  MUSIC_SVS_CAPABILITY,
];

// mode → 门面契约映射。engine 区分参数形态:
//   - acestep:文本描述(prompt)+ 可选歌词/时长 +(cover/repaint)驱动音频。
//   - audiox / soulx:与 new-api 任务适配器(relay/channel/task/gpustackplus/adaptor.go)
//     的 task_type 与 metadata 键精确对齐:
//       * 文生音效  t2a  :纯文本 prompt,无输入物化。
//       * 视频配音效 v2a/tv2a:上传视频(metadata.video)+ 可选文本;有文本→tv2a,否则 v2a。
//       * 视频配乐  v2m/tv2m:上传视频(metadata.video)+ 可选文本;有文本→tv2m,否则 v2m。
//       * 歌声合成  svs  :两个音频——音色参考(metadata.prompt_audio)+ 目标曲/伴奏
//                         (metadata.target_audio),均必填;无需文本(发送固定标签占位)。
// 字段说明:
//   needsAudio:acestep 的驱动音频(单音频,audioMetaKey 透传)。
//   needsVideo:audiox 视频条件输入(单视频上传器 → metadata.video)。
//   needsDualAudio:soulx 双音频上传器(音色参考 + 目标曲/伴奏)。
//   needsText:文本是否必填(t2m/t2a 必填;v2*/tv2* 可选;svs 无需)。
//   resolveTaskType(hasText):acestep/t2a/svs 与文本无关;v2*/tv2* 按是否带文本分支。
export const MUSIC_MODES = {
  t2m: {
    taskType: 't2m',
    capability: MUSIC_T2M_CAPABILITY,
    engine: 'acestep',
    needsAudio: false,
    audioMetaKey: '',
    needsVideo: false,
    needsDualAudio: false,
    needsText: true,
    resolveTaskType: () => 't2m',
  },
  cover: {
    taskType: 'cover',
    capability: MUSIC_COVER_CAPABILITY,
    engine: 'acestep',
    needsAudio: true,
    audioMetaKey: 'reference_audio',
    needsVideo: false,
    needsDualAudio: false,
    needsText: true,
    resolveTaskType: () => 'cover',
  },
  repaint: {
    taskType: 'repaint',
    capability: MUSIC_REPAINT_CAPABILITY,
    engine: 'acestep',
    needsAudio: true,
    audioMetaKey: 'src_audio',
    needsVideo: false,
    needsDualAudio: false,
    needsText: true,
    resolveTaskType: () => 'repaint',
  },
  t2a: {
    taskType: 't2a',
    capability: MUSIC_T2A_CAPABILITY,
    engine: 'audiox',
    needsAudio: false,
    audioMetaKey: '',
    needsVideo: false,
    needsDualAudio: false,
    needsText: true, // 文生音效:文本必填
    videoMetaKey: 'video',
    promptAudioMetaKey: 'prompt_audio',
    targetAudioMetaKey: 'target_audio',
    resolveTaskType: () => 't2a',
  },
  v2a: {
    taskType: 'v2a',
    capability: MUSIC_V2A_CAPABILITY,
    engine: 'audiox',
    needsAudio: false,
    audioMetaKey: '',
    needsVideo: true,
    needsDualAudio: false,
    needsText: false, // 视频配音效:文本可选;有文本→tv2a,否则 v2a
    videoMetaKey: 'video',
    promptAudioMetaKey: 'prompt_audio',
    targetAudioMetaKey: 'target_audio',
    resolveTaskType: (hasText) => (hasText ? 'tv2a' : 'v2a'),
  },
  v2m: {
    taskType: 'v2m',
    capability: MUSIC_V2M_CAPABILITY,
    engine: 'audiox',
    needsAudio: false,
    audioMetaKey: '',
    needsVideo: true,
    needsDualAudio: false,
    needsText: false, // 视频配乐:文本可选;有文本→tv2m,否则 v2m
    videoMetaKey: 'video',
    promptAudioMetaKey: 'prompt_audio',
    targetAudioMetaKey: 'target_audio',
    resolveTaskType: (hasText) => (hasText ? 'tv2m' : 'v2m'),
  },
  svs: {
    taskType: 'svs',
    capability: MUSIC_SVS_CAPABILITY,
    engine: 'soulx',
    needsAudio: false,
    audioMetaKey: '',
    needsVideo: false,
    needsDualAudio: true,
    needsText: false, // 歌声合成:无需文本(发送固定标签占位)
    videoMetaKey: 'video',
    promptAudioMetaKey: 'prompt_audio',
    targetAudioMetaKey: 'target_audio',
    resolveTaskType: () => 'svs',
  },
};

// 体验区子标签页顺序(3 个 ACE-Step + 4 个 AudioX/SoulX)。
export const MUSIC_TAB_ORDER = [
  't2m',
  'cover',
  'repaint',
  't2a',
  'v2a',
  'v2m',
  'svs',
];

// ── ACE-Step 参数 ──────────────────────────────────────────────
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

// 音效/配乐提示词预设(t2a/v2*/tv2* 展示)。
export const MUSIC_AUDIOX_PROMPT_PRESETS = [
  '雨点打在窗户上的滴答声,伴随远处闷雷',
  '繁忙街道上的汽车鸣笛与人群嘈杂声',
  '悠扬舒缓的钢琴独奏,适合宁静的夜晚',
  '激昂的管弦乐,气势磅礴的史诗配乐',
];

// ── 一键示例(带预置文件/参数,按 mode)──────────────────────────────────
// 结构同音频:{ label, prompt, params?, files? }。cover/repaint 预置驱动音(ACE-Step 官方
// test_track),svs 预置双音频(SoulX 官方示例);t2m/t2a 纯文本;v2a/v2m 的 AudioX 官方
// 示例视频为 CC-BY-NC,暂不打包,先给纯文本(需自行上传视频)。ChatArea 兼容纯字符串。
export const MUSIC_EXAMPLES = {
  t2m: [
    {
      label: '国风电子',
      prompt: '中国风电子舞曲,融合古典乐器与现代节拍',
      params: { vocalLanguage: 'zh' },
    },
    { label: '深情抒情', prompt: '一首深情的中文抒情歌曲,适合夜晚独自聆听' },
    {
      label: '史诗配乐',
      prompt: '磅礴大气的史诗级电影配乐,气势恢宏震撼人心',
      params: { vocalLanguage: 'unknown' },
    },
  ],
  cover: [
    {
      label: '音乐改编(示例参考音)',
      prompt: '改编成轻快的流行电子风格,加入合成器与鼓点',
      params: { audioName: 'acestep-reference.mp3' },
      files: { audioData: '/playground-samples/audio/acestep-reference.mp3' },
    },
  ],
  repaint: [
    {
      label: '音乐重绘(示例源音)',
      prompt: '保持主旋律,重绘为更抒情的钢琴伴奏版本',
      params: { audioName: 'acestep-reference.mp3' },
      files: { audioData: '/playground-samples/audio/acestep-reference.mp3' },
    },
  ],
  t2a: [
    { label: '雨声闷雷', prompt: '雨点打在窗户上的滴答声,伴随远处闷雷' },
    { label: '街道嘈杂', prompt: '繁忙街道上的汽车鸣笛与人群嘈杂声' },
    { label: '钢琴独奏', prompt: '悠扬舒缓的钢琴独奏,适合宁静的夜晚' },
  ],
  v2a: [
    '为视频生成贴合画面的音效',
    'Ocean waves crashing with people laughing',
  ],
  v2m: ['为视频生成贴合氛围的背景音乐', 'Generate music with piano instrument'],
  svs: [
    {
      label: '歌声合成(普通话)',
      prompt: '',
      params: {
        language: 'Mandarin', // = MUSIC_SVS_DEFAULT_LANGUAGE
        control: 'melody', // = MUSIC_SVS_DEFAULT_CONTROL
        promptAudioName: 'soulx-prompt-zh.mp3',
        targetAudioName: 'soulx-target-music.mp3',
      },
      files: {
        promptAudioData: '/playground-samples/audio/soulx-prompt-zh.mp3',
        targetAudioData: '/playground-samples/audio/soulx-target-music.mp3',
      },
    },
  ],
};

export const musicExamplesForMode = (mode) => MUSIC_EXAMPLES[mode] || [];

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

// ── AudioX / SoulX 参数 ────────────────────────────────────────
// 标量参数默认(仅作输入框占位提示;留空即不下发,走引擎默认)。
// AudioX:seconds_total(仅 AudioX)默认 10、num_inference_steps 默认 250;
// SoulX(svs):num_inference_steps 默认 32。guidance_scale/seed 两引擎共用。
export const MUSIC_DEFAULT_SECONDS_TOTAL = 10;
export const MUSIC_AUDIOX_DEFAULT_STEPS = 250;
export const MUSIC_SOULX_DEFAULT_STEPS = 32;
export const MUSIC_AUDIOX_DEFAULT_GUIDANCE = 7.0;
// SoulX(svs)guidance 默认 = deploy-config soulxsinger_svs.yaml 的 guidance_scale: 3.0。
// ConfigPanel 占位与实际引擎默认必须一致(所见即所发)。
export const MUSIC_SOULX_DEFAULT_GUIDANCE = 3.0;

// SoulX 歌声合成的语言与控制方式(metadata.language / metadata.control)。
export const MUSIC_SVS_LANGUAGES = [
  { value: 'Mandarin', label: '普通话' },
  { value: 'Cantonese', label: '粤语' },
  { value: 'English', label: '英文' },
];
export const MUSIC_SVS_DEFAULT_LANGUAGE = 'Mandarin';
export const MUSIC_SVS_CONTROLS = [
  { value: 'melody', label: '旋律(melody)' },
  { value: 'score', label: '曲谱(score)' },
];
export const MUSIC_SVS_DEFAULT_CONTROL = 'melody';

// 采样步数占位默认按引擎选择。
export const musicDefaultStepsForEngine = (engine) => {
  if (engine === 'audiox') return MUSIC_AUDIOX_DEFAULT_STEPS;
  if (engine === 'soulx') return MUSIC_SOULX_DEFAULT_STEPS;
  return MUSIC_DEFAULT_STEPS;
};

// guidance 占位默认按引擎选择(SoulX=3 与 deploy-config 一致;AudioX/ACE-Step=7)。
export const musicDefaultGuidanceForEngine = (engine) => {
  if (engine === 'soulx') return MUSIC_SOULX_DEFAULT_GUIDANCE;
  if (engine === 'audiox') return MUSIC_AUDIOX_DEFAULT_GUIDANCE;
  return MUSIC_DEFAULT_GUIDANCE;
};

// ── 上传大小上限 ───────────────────────────────────────────────
// 上传参考/源音大小上限(MB;base64 随请求体走,过大拖慢提交)。
export const MUSIC_AUDIO_UPLOAD_MAX_MB = 20;
// 上传视频(v2*/tv2*)大小上限(MB)。
export const MUSIC_VIDEO_UPLOAD_MAX_MB = 50;

// ── 历史 ───────────────────────────────────────────────────────
// 历史 localStorage 键按 mode 区分(各玩法各自独立历史)。
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
export const MUSIC_DEFAULT_VIDEO_MB = MUSIC_VIDEO_UPLOAD_MAX_MB;

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
//   { default: { maxChars, refAudioMaxMB, videoMaxMB },
//     models: { <model>: { capabilities:[], maxChars, refAudioMaxMB, videoMaxMB } } }
export const parseMusicModelConfig = (raw) => {
  const empty = {
    default: {
      maxChars: MUSIC_DEFAULT_MAX_CHARS,
      refAudioMaxMB: MUSIC_DEFAULT_REF_AUDIO_MB,
      videoMaxMB: MUSIC_DEFAULT_VIDEO_MB,
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
          videoMaxMB: toPositiveInt(cfg?.videoMaxMB),
        };
      });
    }
    return {
      default: {
        maxChars: toPositiveInt(def.maxChars) ?? MUSIC_DEFAULT_MAX_CHARS,
        refAudioMaxMB:
          toPositiveInt(def.refAudioMaxMB) ?? MUSIC_DEFAULT_REF_AUDIO_MB,
        videoMaxMB: toPositiveInt(def.videoMaxMB) ?? MUSIC_DEFAULT_VIDEO_MB,
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

// 视频大小上限(MB):按模型配置 → 全局默认 → 兜底常量。
export const getVideoMaxMBForModel = (config, model) => {
  const m = config?.models?.[model];
  if (m && m.videoMaxMB != null) return m.videoMaxMB;
  if (config?.default?.videoMaxMB != null) return config.default.videoMaxMB;
  return MUSIC_DEFAULT_VIDEO_MB;
};
