// 音乐模型体验区常量。链路复用视频体验区的异步任务门面(POST /pg/videos),按 mode 映射
// task_type。涵盖两类引擎:
//   - ACE-Step 文生音乐/音乐改编/音乐重绘(t2m/cover/repaint),结果为音频(.mp3);
//   - MiniMax-Music3 文生音乐(与 ACE-Step 同挂 t2m,按引擎族分流),结果为音频(.wav)。
// 2026-08 下线:AudioX(文生音效 t2a、视频生音乐 v2m/tv2m)与 SoulX-Singer(歌声合成 svs)
// —— 实例已收到 0,体验区入口、参数与模板一并摘除。任务日志仍保留这几个 task_type
// 的中文标签,以免历史记录只显示原始值。
// 通用状态机/轮询/内容地址等工具直接复用 videoPlayground.constants;结果播放/下载按返回的
// content-url + media-type 处理,格式无关(见 MusicChatArea)。

import {
  normalizeModelNote,
  tabScopedValue,
  MUSIC_ENGINE_MINIMAX_MUSIC3,
} from './playgroundAdmin.constants';

export {
  VIDEO_API_ENDPOINTS as MUSIC_API_ENDPOINTS,
  VIDEO_STATUS as MUSIC_STATUS,
  VIDEO_POLL_INTERVAL_MS as MUSIC_POLL_INTERVAL_MS,
  normalizeVideoStatus as normalizeMusicStatus,
  parseProgress,
  buildVideoContentUrl as buildMusicContentUrl,
} from './videoPlayground.constants';

// 音乐生成较慢(30~120s 的曲子,单实例 FIFO),沿用 4s
// 间隔,上限同视频。
export const MUSIC_POLL_MAX_TIMES = 90; // 约 6 分钟后超时

// 三个能力标签(= 体验区子标签页名;中文即值),都由 ACE-Step 承载;文生音乐另可挂
// MiniMax-Music3(按引擎族分流,不占新能力词)。
// 与后端 constant/model_capability.go 的 MusicCapabilities 保持一致(新增能力两处同步)。
export const MUSIC_T2M_CAPABILITY = '文生音乐';
export const MUSIC_COVER_CAPABILITY = '音乐改编';
export const MUSIC_REPAINT_CAPABILITY = '音乐重绘';
// 2026-07 下线:视频生音(AudioX v2a/tv2a,出 .wav)及其旧标签 视频配音效/视频配乐。
// 视频配乐产品线移交 LTX-2.3(task_type=v2a 契约改判,产物=配好音的视频),入口在
// 体验区「语音模型 → 视频配乐」(见 audioPlayground 侧),不再归音乐页。
export const MUSIC_CAPABILITIES = [
  MUSIC_T2M_CAPABILITY,
  MUSIC_COVER_CAPABILITY,
  MUSIC_REPAINT_CAPABILITY,
];

// mode → 门面契约映射。engine 区分参数形态:
//   - acestep:文本描述(prompt)+ 可选歌词/时长 +(cover/repaint)驱动音频。
// 字段说明:
//   needsAudio:acestep 的驱动音频(单音频,audioMetaKey 透传)。
//   needsText:文本是否必填。
//   resolveTaskType(hasText):三个玩法都与文本无关,保留这个形状是因为下线前的
//     v2*/tv2* 曾按「有没有文本」分叉;现在无分叉,但接口不变以免调用方跟着改。
//   needsVideo / needsDualAudio 随 AudioX/SoulX 一并移除。
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
};

// 体验区子标签页顺序。
// v2a(视频生音)已下线:视频配乐移交 LTX-2.3,入口在语音模型页。
export const MUSIC_TAB_ORDER = ['t2m', 'cover', 'repaint'];

// ── ACE-Step 参数 ──────────────────────────────────────────────
// 时长预设(秒),经 metadata.audio_duration 透传给引擎。'' = 引擎默认(不下发)。
export const MUSIC_DURATIONS = ['', '30', '60', '90', '120'];
export const MUSIC_DEFAULT_DURATION = '';

// ── MiniMax-Music3 参数 ────────────────────────────────────────
// 时长与 ACE-Step 的语义**不同**,不能共用那个下拉。
//   ACE-Step:audio_duration 是"参考锚点",成品在附近浮动。
//   Music3  :max_new_tokens 是**帧数上限**(25 fps),模型吐出 end-of-audio 就提前结束。
//            所以它是"最长不超过",不是"大约多长"。文案必须区分,否则用户会以为选 60
//            就一定出 60 秒。
// 官方 curl 即 "max_new_tokens": 750(= 30 秒);README 限制一节写明单次上限 9000 帧
// (= 360 秒),模型卡称可出五分钟级的完整歌曲。
export const MUSIC3_FRAMES_PER_SECOND = 25;
export const MUSIC3_MAX_FRAMES = 9000;
// 档位最高给到 300 秒 = **模型卡声明的五分钟**,不是引擎那个 9000 帧硬上限
// (360 秒)。两个数字都真实,但含义不同:300 是厂商说它能写完整歌曲的长度,360 只是
// 请求侧钳位的边界。给到 360 等于替厂商声明一个它没声明的能力;而 max_new_tokens
// 是上限、模型唱完自己收尾,给不到 360 也不损失什么。
export const MUSIC3_DURATIONS = [
  '',
  '30',
  '60',
  '90',
  '120',
  '180',
  '240',
  '300',
];
export const MUSIC3_DEFAULT_DURATION = '';

// 秒 → max_new_tokens。留空/非法返回 null(不下发,由引擎按自己的默认走)。
// 上限按 MUSIC3_MAX_FRAMES 封顶:发超了引擎侧要么拒、要么截断,都不如这里先钳住。
export const music3FramesForSeconds = (seconds) => {
  const sec = parseInt(seconds, 10);
  if (!Number.isFinite(sec) || sec <= 0) return null;
  return Math.min(sec * MUSIC3_FRAMES_PER_SECOND, MUSIC3_MAX_FRAMES);
};

// 提示词预设(风格/描述 caption,点击填入输入框)。取自 ACE-Step 官方
// examples/simple_mode 的 description 风格(自然语言描述,sample 模式据此自动配词),
// 刻意拉开风格分布:人声抒情 / 国风电子 / 影视器乐 / 冥想器乐,快慢与人声器乐都覆盖。
export const MUSIC_PROMPT_PRESETS = [
  '一首深情的中文抒情歌曲,适合夜晚独自聆听',
  '中国风电子舞曲,融合古典乐器与现代节拍',
  '磅礴大气的史诗级电影配乐,气势恢宏震撼人心',
  '空灵的禅意音乐,适合瑜伽冥想',
];

// ── 一键示例(带预置文件/参数,按 mode)──────────────────────────────────
// 结构同音频:{ label, prompt, params?, files? }。cover/repaint 预置驱动音(ACE-Step 官方
// test_track);t2m 纯文本。ChatArea 兼容纯字符串。
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
};

// MiniMax-Music3 的文生音乐示例。与 ACE-Step 的 t2m 示例是两套:
//   - prompt 位在 Music3 上是**编曲说明**(→ instructions),不是 caption;
//   - 必须连歌词一起给(歌词才是引擎的 input,空着提交不了);
//   - 不带 vocalLanguage —— Music3 没有这个参数,给了也只是在 inputs 里留个死值。
//
// 描述照官方 README 的 Structured Caption 写法:带标签的英文句子
// (Genre / BPM / Key / Vocals / Arrangement),官方 reproducible example 即此格式。
// 歌词按官方要求把 [Verse] / [Chorus] 这类段落标签**单独占一行**。
//
// ⚠️ 歌词用中文是产品判断,不是官方背书:README 通篇示例都是英文,也没有关于歌词语种的
// 任何说明。首次部署后要拿这三条各跑一次听结果,中文咬字不行就把示例换成英文。
const MUSIC3_T2M_EXAMPLES = [
  {
    label: '深情抒情',
    prompt:
      'Genre: acoustic pop. BPM: 72. Key: C major. Warm and intimate, building gently into the chorus. Vocals: soft male lead, close and breathy, light stacked harmonies in the chorus. Arrangement: fingerpicked guitar and soft piano in the verse; brushed drums and upright bass enter in the chorus; wide reverb on the final chorus.',
    params: {
      lyrics:
        '[Verse]\n晚风吹过安静的街\n路灯把影子拉得很长\n[Chorus]\n我还站在原地等你\n等一句没说出口的话',
    },
  },
  {
    label: '国风电子',
    prompt:
      'Genre: Chinese-style electronic dance. BPM: 128. Key: A minor. Bright and driving, with a cinematic lift into the chorus. Vocals: clear female lead, agile and forward, doubled octave in the chorus. Arrangement: guzheng and dizi carry the main melody; 808 kick and sub-bass with a warm analog pad underneath; filtered build in the pre-chorus, full drums and stacked synths in the chorus.',
    params: {
      lyrics:
        '[Verse]\n灯火沿着长街淌\n我把心事折成纸船\n[Chorus]\n随风去了远方\n不必再问归期',
    },
  },
  {
    label: '史诗合唱',
    // 这条**不能写成纯器乐**:歌词是必填的(门面对空 prompt 直接 400),写了词就一定会
    // 被唱出来。描述里说「instrumental, no lead vocal」而歌词里给了词,等于一边告诉
    // 引擎没有人声、一边给它词唱,出来的东西两头不靠。所以人声改成合唱团,与歌词对齐。
    prompt:
      'Genre: epic orchestral score with choir. BPM: 90. Key: D minor. Solemn and grand, rising to a triumphant final section. Vocals: full mixed choir, no solo lead; unison and open-vowel in the opening, wide four-part harmony in the final section. Arrangement: low strings and timpani establish the pulse; brass answers in the second section; full orchestra and choir with cymbal swells and wide hall reverb at the climax.',
    params: {
      lyrics: '[Intro]\n风起于荒原之上\n[Chorus]\n山河在身后\n我们向前',
    },
  },
];

// 示例按 mode 取;文生音乐这个 tab 挂着两个引擎,再按引擎族分一层。
export const musicExamplesForMode = (mode, engine) =>
  (mode === 't2m' && engine === MUSIC_ENGINE_MINIMAX_MUSIC3
    ? MUSIC3_T2M_EXAMPLES
    : MUSIC_EXAMPLES[mode]) || [];

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

// ── ACE-Step 改编(cover)参数 ───────────────────────────────────
// audio_cover_strength:官方标为 cover 的 Key parameter。越高越贴原曲结构,越低越自由。
// 引擎默认 1.0(最大保留)。'' = 不下发。
export const MUSIC_DEFAULT_COVER_STRENGTH = 1.0;

// ── ACE-Step 重绘(repaint)参数 ─────────────────────────────────
// 重绘区间 [start, end)(秒)。引擎默认 start=0 / end=None(→ -1 = 到结尾),即"全曲重绘",
// 那样跟 cover 就没区别了 —— repaint 的价值在于只改一段,所以区间必须让用户填。
// 官方给的可操作范围是 3~90 秒(Tutorial「Operation range: 3 seconds to 90 seconds」)。
export const MUSIC_REPAINT_MIN_SEC = 3;
export const MUSIC_REPAINT_MAX_SEC = 90;
// repaint_mode:保守=最大保留源音频,平衡=按 repaint_strength 调,激进=纯扩散。
export const MUSIC_REPAINT_MODES = [
  { value: 'conservative', label: '保守(最大保留原曲)' },
  { value: 'balanced', label: '平衡(可调强度)' },
  { value: 'aggressive', label: '激进(完全重生成)' },
];
export const MUSIC_DEFAULT_REPAINT_MODE = 'balanced';
export const MUSIC_DEFAULT_REPAINT_STRENGTH = 0.5;

// 采样步数占位默认。AudioX/SoulX 下线后只剩 ACE-Step 一档(Music3 不暴露步数),
// 函数保留是因为面板与调用方都按「按引擎取默认」的形状写,退化成常量返回即可,
// 日后再进引擎时不用改调用点。
export const musicDefaultStepsForEngine = () => MUSIC_DEFAULT_STEPS;

// guidance 占位默认。同上,只剩 ACE-Step。
export const musicDefaultGuidanceForEngine = () => MUSIC_DEFAULT_GUIDANCE;

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

// 解析 per-model 的 translation 配置(中译英)。形如 { enabled:bool, defaultModel:string }。
// 缺省 enabled=false、defaultModel=''。
const parseTranslationCfg = (cfg) => ({
  enabled: cfg?.enabled === true,
  defaultModel:
    typeof cfg?.defaultModel === 'string' ? cfg.defaultModel.trim() : '',
});

// tab 子层规范化:models[name].tabs[tabKey] 只放该 tab 声明用得到的字段。
// 空对象保留(= 该模型挂进了这个 tab,参数全走兜底);未配的字段不落键,好让
// tabScopedValue 正确降级。translation 是复合项,只在显式给了对象时才落键。
const normalizeMusicTabs = (raw) => {
  if (!raw || typeof raw !== 'object') return {};
  const out = {};
  Object.entries(raw).forEach(([tabKey, cfg]) => {
    const entry = {};
    const chars = toPositiveInt(cfg?.maxChars);
    if (chars != null) entry.maxChars = chars;
    const mb = toPositiveInt(cfg?.refAudioMaxMB);
    if (mb != null) entry.refAudioMaxMB = mb;
    if (cfg?.translation && typeof cfg.translation === 'object') {
      entry.translation = parseTranslationCfg(cfg.translation);
    }
    const note = normalizeModelNote(cfg?.note);
    if (note) entry.note = note;
    out[tabKey] = entry;
  });
  return out;
};

// 引擎族:模型级声明,不随 tab 变。
//
// 必须按模型取而不是按 tab 取:MUSIC_MODES[mode].engine 是 tab 级的硬编码
// (「文生音乐」恒为 acestep),而同一个 tab 完全可以挂多个引擎的模型 ——
// MiniMax-Music3 也是文生音乐,按 tab 判就会走 ACE-Step 分支,拿到 lyrics/thinking
// 这些它不认的键,而它必需的 instructions 一个都不下发,引擎侧直接 400。
// 未声明返回空串,调用方回退到 tab 默认引擎(保持既有部署不变)。
export const getEngineForMusicModel = (config, model) =>
  config?.models?.[model]?.engine || '';

// 常量本体定义在 playgroundAdmin.constants.js(管理页下拉也要用它),这里再导出，
// 依赖方向保持单向 —— 与 VIDEO_ENGINE_MINIMAX_H3 / AUDIO_ENGINE_INDEXTTS25 同一处理。
// 用「import 再 export」而不是 `export ... from`:后者不产生本地绑定,本文件下面的
// musicExamplesForMode 就用不到它了。
export { MUSIC_ENGINE_MINIMAX_MUSIC3 };

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
          engine: String(cfg?.engine || '')
            .trim()
            .toLowerCase(),
          capabilities: normalizeList(cfg?.capabilities),
          maxChars: toPositiveInt(cfg?.maxChars),
          refAudioMaxMB: toPositiveInt(cfg?.refAudioMaxMB),
          videoMaxMB: toPositiveInt(cfg?.videoMaxMB),
          translation: parseTranslationCfg(cfg?.translation),
          tabs: normalizeMusicTabs(cfg?.tabs),
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
// matchCaps 为可选别名数组:命中其中任一能力即算(用于「视频生音」兼容旧标签)。
export const getMusicModelSet = (config, capability, matchCaps) => {
  const wanted =
    Array.isArray(matchCaps) && matchCaps.length ? matchCaps : [capability];
  const set = new Set();
  Object.entries(config?.models || {}).forEach(([model, cfg]) => {
    const caps = Array.isArray(cfg?.capabilities) ? cfg.capabilities : [];
    if (caps.some((c) => wanted.includes(c))) set.add(model);
  });
  return set;
};

// 某模型的翻译配置(是否启用中译英 + 默认语言模型):tab 级 → 模型级。无全局兜底。
// 只回「这个模型要不要中译英」。原先还带一个 defaultModel(体验区那个语言模型下拉的
// 默认选项),下拉已撤 —— 翻译改用运营在「体验区管理 → 通用设置」里配的那个语言模型,
// 与「AI 优化提示词」(两条实现)同一个。老配置里残留的 defaultModel 读不到就是了。
export const getTranslationForModel = (config, model, tabKey) => {
  const m = config?.models?.[model];
  const t = tabScopedValue(m, tabKey, 'translation') || m?.translation;
  return { enabled: t?.enabled === true };
};

// 字数上限:tab 级 → 模型级 → 全局默认 → 兜底常量。0 表示不限制。
// tabKey 传空时退化为改造前的「只按模型名」语义(直连请求/非体验区调用)。
export const getMaxCharsForModel = (config, model, tabKey) => {
  const m = config?.models?.[model];
  const scoped = tabScopedValue(m, tabKey, 'maxChars');
  if (scoped != null) return scoped;
  if (m && m.maxChars != null) return m.maxChars;
  if (config?.default?.maxChars != null) return config.default.maxChars;
  return MUSIC_DEFAULT_MAX_CHARS;
};

// 参考音大小上限(MB):tab 级 → 模型级 → 全局默认 → 兜底常量。
export const getRefAudioMaxMBForModel = (config, model, tabKey) => {
  const m = config?.models?.[model];
  const scoped = tabScopedValue(m, tabKey, 'refAudioMaxMB');
  if (scoped != null) return scoped;
  if (m && m.refAudioMaxMB != null) return m.refAudioMaxMB;
  if (config?.default?.refAudioMaxMB != null)
    return config.default.refAudioMaxMB;
  return MUSIC_DEFAULT_REF_AUDIO_MB;
};

// 视频大小上限(MB):按模型配置 → 全局默认 → 兜底常量。
// 无 tab 级:AudioX 视频生音(v2m/tv2m)已下线,音乐页当前没有吃视频的玩法,故不进
// 任何 tab 的 fields;它只剩服务端对直连请求的兜底,保持模型级语义。
export const getVideoMaxMBForModel = (config, model) => {
  const m = config?.models?.[model];
  if (m && m.videoMaxMB != null) return m.videoMaxMB;
  if (config?.default?.videoMaxMB != null) return config.default.videoMaxMB;
  return MUSIC_DEFAULT_VIDEO_MB;
};
