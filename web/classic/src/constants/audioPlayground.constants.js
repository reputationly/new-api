// 语音合成(TTS)体验区常量。链路复用视频体验区的异步任务门面
// (POST /pg/videos, task_type=tts),仅参数与结果形态不同(音频 .wav)。
// 状态机/轮询/内容地址等通用工具直接复用 videoPlayground.constants。

import {
  normalizeModelNote,
  tabScopedValue,
} from './playgroundAdmin.constants';

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

// 四个能力标签(= 体验区子标签页名;中文即值)。区分 IndexTTS-2 的情感合成与 vLLM-Omni
// 家族的语音合成(音色来源/语言合并为一个玩法的面板内选项)/对话/设计。与后端
// constant/model_capability.go 的 AudioCapabilities 保持一致(新增能力两处同步)。
export const AUDIO_EMOTION_CAPABILITY = '情感合成'; // IndexTTS-2(= 原「语音合成」)
// 语音合成:Qwen3-TTS / VoxCPM2 / CosyVoice3 / GLM-TTS / MOSS-TTS-Nano。单模型同时覆盖
// 预设音色 / 声音克隆 / 多语言方言 —— 它们是一次 TTS 请求的不同维度(音色来源 + 语言),
// 不是独立能力,故合并为一个玩法,音色来源与语言在面板内以选项呈现。
export const AUDIO_SYNTHESIS_CAPABILITY = '语音合成';
export const AUDIO_DIALOGUE_CAPABILITY = '双人对话'; // MOSS-TTSD
export const AUDIO_DESIGN_CAPABILITY = '声音设计'; // MOSS-VoiceGenerator

// 兼容旧引用:情感合成即原单一「语音合成」标签。
export const AUDIO_PAGE_CAPABILITY = AUDIO_EMOTION_CAPABILITY;

// 语音合成玩法的「音色来源」两个选项(面板内 radio/toggle)。默认上传克隆(对所有 Omni
// TTS 模型可用;预设音色为 Qwen3-TTS 专属)。
export const AUDIO_VOICE_SOURCE_UPLOAD = 'upload'; // 上传克隆 → metadata.ref_audio(+可选 ref_text)
export const AUDIO_VOICE_SOURCE_PRESET = 'preset'; // 预设音色 → metadata.speaker(标量透传)
export const AUDIO_DEFAULT_VOICE_SOURCE = AUDIO_VOICE_SOURCE_UPLOAD;
export const AUDIO_VOICE_SOURCE_OPTIONS = [
  { value: AUDIO_VOICE_SOURCE_UPLOAD, label: '上传克隆' },
  { value: AUDIO_VOICE_SOURCE_PRESET, label: '预设音色' },
];

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

// 现场录制参考音的引导文案。IndexTTS 官方(README/文档)并未给出推荐朗读句,此处为自拟:
// 中性叙述语气 + 常用字 + 四声齐全,正常语速约 8-9 秒,落在官方建议的几秒干净人声区间。
// 刻意不带情绪:情感合成的情感由 emo_vector/情感参考音单独控制,音色参考音带情绪反而
// 会干扰克隆结果。
export const VOICE_RECORD_SCRIPT =
  '今天天气不错，我打算下午去公园走一走，顺便把上周借的书还掉，回来的路上再买点水果。';

// 录制时长:低于 MIN 音色特征不足;到 MAX 自动停止,防止误录长音频撑大请求体。
export const VOICE_RECORD_MIN_SEC = 3;
export const VOICE_RECORD_MAX_SEC = 20;

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
  const w =
    typeof weight === 'number' && weight >= 0 && weight <= 1 ? weight : 1;
  vec[preset.index] = w;
  return vec;
};

// ── IndexTTS-2.5 扩展能力 ──────────────────────────────────────────────────
//
// 以下全部在真实实例(dev-gpustack-a100-0011)上逐条验证过,不是照文档抄:
//   speed=1.5 → 音频字节数 0.64x、speed=0.6 → 1.72x(与 1/speed 成正比,引擎把它
//   映射成 duration_factor);speed=3.0 → 400。lang 非法值 → 400 并回列全部合法值。
//   emo_vector 多维同时非零可用(悲0.7+低落0.4 正常出音),越界 1.5 → 400。
//   use_emo_text 首次 10.4s、之后 5.3s(基线 1.5s)——QwenEmotion 推理开销。

// 引擎族:模型级声明,不随 tab 变。判据是配置声明而非模型名 substring
// (前端拿对外模型名、后端拿上游名,靠名字判两边必然分叉)。
// 常量本体定义在 playgroundAdmin.constants.js(管理页下拉也要用它),这里再导出，
// 依赖方向保持单向 —— 与 VIDEO_ENGINE_MINIMAX_H3 同一处理。
export { AUDIO_ENGINE_INDEXTTS25 } from './playgroundAdmin.constants';
export const getEngineForAudioModel = (config, model) =>
  config?.models?.[model]?.engine || '';

// 情感来源:引擎侧三者**互斥且有优先级**(indextts2_talker.py:832
// use_emo_text > emo_vector > emo_audio),所以 UI 必须是单选,不能让用户以为可叠加。
export const AUDIO_EMOTION_SOURCE_FOLLOW = 'follow';
export const AUDIO_EMOTION_SOURCE_VECTOR = 'vector';
export const AUDIO_EMOTION_SOURCE_AUDIO = 'audio';
export const AUDIO_EMOTION_SOURCE_TEXT = 'text';

export const AUDIO_EMOTION_SOURCES = [
  {
    value: AUDIO_EMOTION_SOURCE_FOLLOW,
    label: '跟随音色',
    hint: '不发情感参数',
  },
  {
    value: AUDIO_EMOTION_SOURCE_VECTOR,
    label: '手动调节',
    hint: '八维情感可混合',
  },
  {
    value: AUDIO_EMOTION_SOURCE_AUDIO,
    label: '情感参考音',
    hint: '另传一段音频定情绪',
  },
  {
    value: AUDIO_EMOTION_SOURCE_TEXT,
    label: '文本推断',
    hint: '由模型读文本定情绪，慢 3~4 秒',
  },
];

// 八维情感,次序必须与引擎的 _DESIRED_ORDER 一致
// (indextts2_talker.py:934 ["高兴","愤怒","悲伤","恐惧","反感","低落","惊讶","自然"])。
// 错位不会报错,只会让"选悲伤"出成愤怒。
export const AUDIO_EMOTION_DIMENSIONS = [
  { key: 'happy', label: '高兴' },
  { key: 'angry', label: '愤怒' },
  { key: 'sad', label: '悲伤' },
  { key: 'afraid', label: '恐惧' },
  { key: 'disgusted', label: '反感' },
  { key: 'melancholic', label: '低落' },
  { key: 'surprised', label: '惊讶' },
  { key: 'calm', label: '自然' },
];

// 引擎硬校验 [0, 1.2](超出即 400)。UI 上限取 1.2,与引擎一致。
export const AUDIO_EMOTION_DIM_MAX = 1.2;
export const AUDIO_EMOTION_DIM_STEP = 0.05;

// 全零向量视为"没选情绪",不下发 —— 发一个全零向量会让引擎按零情感合成,
// 与"跟随音色"是不同结果,用户却分辨不出自己漏调了滑块。
export const emotionVectorIsEmpty = (vec) =>
  !Array.isArray(vec) || vec.every((v) => !(Number(v) > 0));

export const makeEmptyEmotionVector = () =>
  AUDIO_EMOTION_DIMENSIONS.map(() => 0);

// 语速:2.5 原生支持(native_speed_control),映射引擎的 duration_factor。
// 范围是引擎硬校验的 [0.5, 2.0],不是 OpenAI 通用的 [0.25, 4]。
export const AUDIO_SPEED_MIN = 0.5;
export const AUDIO_SPEED_MAX = 2.0;
export const AUDIO_SPEED_STEP = 0.05;
export const AUDIO_SPEED_DEFAULT = 1.0;

// IndexTTS-2.5 的语种(extra_params.lang)。
//
// ⚠️ 这里只列**官方声明支持**的,不是引擎能接受的全集。三个口径必须分清:
//   - tokenizer_v2_5.LANGUAGES 有 106 个槽位,但**前 99 个逐字逐序等于 Whisper 的
//     语种表**(继承来的),槽位存在 ≠ 模型在该语言上训练过;
//   - 模型卡 (README «Languages») 只声明五种:中、英、日、西、阿;
//   - 分词器文件名 multilingual_zh_ja_yue_char_del.tiktoken 显示字符集覆盖 中/日/粤。
//
// 取交集并保守收敛:官方五种 + 粤语(分词器与槽位双重佐证) + 中英混合(槽位有 zh/en)。
//
// 2026-08-29 逐条实测(dev-gpustack-a100-0012),全部用同一段参考音、各发一条对应语言的
// 文本:zh / en / es / ar / yue / zh-en混合 与不传 lang 都 200 且音频 4.7~5.6s(合理),
// **只有 ja 报 400**"Japanese preprocessing requires fugashi and unidic" ——
// 与 wetext 那次同根:pyproject 的 [indextts2] extra 整个没进镜像。fugashi 有
// aarch64 wheel、unidic-lite 是纯词典,17s 就能装上(不像同 extra 的 pynini 要编
// OpenFst),已加进 docker/Dockerfile.cuda 的 best-effort 安装块。
// ⚠️ 所以 ja 需要 2026-08-29 之后出的镜像;更早的镜像上选日语会 400。
// 别把 Whisper 的 99 语种表当成 TTS 的能力清单 —— 要开更多语种,先各测一条再加。
// 引擎仍接受任意合法值,API 直连不受这个列表限制。不传时引擎默认 zh。
//
// 与下方的 AUDIO_LANGUAGES 是两回事:那个是**语音合成 tab 的方言**(自动/北京话/
// 四川话,Qwen3-TTS 的 language 标量),两者不能互相复用。
export const AUDIO_TTS25_LANGUAGES = [
  { value: '', label: '默认（中文）' },
  { value: 'zh', label: '中文' },
  { value: 'en', label: 'English' },
  { value: 'ja', label: '日本語' },
  { value: 'es', label: 'Español' },
  { value: 'ar', label: 'العربية' },
  { value: 'yue', label: '粤语' },
  { value: 'zh/en', label: '中英混合' },
];

// 情感强度(emo_alpha)默认值,与官方 demo 默认一致。
export const AUDIO_DEFAULT_EMO_WEIGHT = 0.65;

// 提示词预设(合成文本示例,短剧配音风)。
export const AUDIO_PROMPT_PRESETS = [
  '大家好,欢迎收听今天的节目,我们将带来一段精彩的故事。',
  '你怎么能这样对我?我们说好了要一起走到最后的!',
  '别怕,有我在。无论发生什么,我都会站在你这边。',
  '哈哈哈,真是太有意思了,快跟我说说后来怎么样了?',
];

// 历史 localStorage 键按 mode 区分(各玩法各自独立历史)。旧单一键保留供迁移/兜底。
export const AUDIO_HISTORY_STORAGE_KEY = 'audio_playground_conversations';
export const AUDIO_HISTORY_STORAGE_PREFIX = 'audio_playground_conversations';
export const audioHistoryStorageKey = (mode) =>
  `${AUDIO_HISTORY_STORAGE_PREFIX}_${mode}`;
export const AUDIO_HISTORY_LIMIT = 10; // 对话段数上限
export const AUDIO_CONV_TURN_LIMIT = 10; // 单段对话生成次数上限

// 语音能力枚举(中文即值,也是体验区标签页名)。与后端 constant/model_capability.go 的
// AudioCapabilities 保持一致。新增能力时两处同步。
export const AUDIO_CAPABILITIES = [
  AUDIO_EMOTION_CAPABILITY,
  AUDIO_SYNTHESIS_CAPABILITY,
  AUDIO_DIALOGUE_CAPABILITY,
  AUDIO_DESIGN_CAPABILITY,
];

// mode → 门面契约映射。四个玩法都发 task_type='tts'(POST /pg/videos 异步门面),只在输入
// 面板与所需 metadata 键上不同。与 new-api 任务适配器(relay/channel/task/gpustackplus/
// adaptor.go)的物化逻辑精确对齐:
//   - emotion(情感合成,IndexTTS-2):参考音色 → metadata.voice(必填,materializeTTSInputs);
//     情感参考音 → metadata.emotion_audio(可选);emo_vector/emo_alpha 标量透传。
//   - synthesis(语音合成,Qwen3-TTS/VoxCPM2/CosyVoice3/GLM-TTS/MOSS-TTS-Nano):单玩法覆盖
//     音色来源 + 语言两个维度。音色来源 toggle:
//       · 上传克隆(默认):克隆源 → metadata.ref_audio(materializeOmniTTSInputs);
//         可选参考文本 → metadata.ref_text(标量透传)。
//       · 预设音色:音色 → metadata.speaker(标量透传,非上传;Qwen3-TTS 专属)。
//       两者互斥(选预设不发 ref_audio;选上传不发 speaker)。
//       语言 → metadata.language(标量,可选;留空=模型默认),两种来源都可带。
//   - dialogue(双人对话,MOSS-TTSD):对话脚本([S1]/[S2])作 prompt;说话人1 →
//     metadata.ref_audio + 说话人2 → metadata.ref_audio_2(materializeOmniTTSInputs)。
//   - design(声音设计,MOSS-VoiceGenerator):声线描述 → metadata.instructions(标量,无参考音)。
// 字段说明:
//   engine:indextts(情感合成走 materializeTTSInputs)/ omni(其余走 materializeOmniTTSInputs
//           或纯标量透传)。仅用于文案/校验分支,task_type 恒为 'tts'。
//   needsVoice:情感合成的参考音色(预置/上传,→ metadata.voice,必填)。
//   needsEmotion:情感合成的情感预设 + 强度 UI。
//   needsVoiceSource:语音合成的「音色来源」toggle(上传克隆 | 预设音色),按选择切换下面两项。
//   needsRefAudio:单个克隆参考音上传(→ metadata.ref_audio)。语音合成里由 toggle 决定是否必填。
//   refAudioRequired:该参考音是否必填(synthesis 上传克隆时必填)。
//   needsDualRef:双人对话双参考音上传(ref_audio + ref_audio_2,均必填)。
//   needsSpeaker:预设音色下拉(→ metadata.speaker 标量;synthesis 选预设音色时用)。
//   needsLanguage:语言下拉(→ metadata.language 标量)。
//   needsRefText:可选参考文本(→ metadata.ref_text 标量;synthesis 上传克隆时用)。
//   needsInstructions:声线/情感指令文本(→ metadata.instructions 标量)。design 必填。
//   instructionsRequired:指令是否必填(design 必填)。
export const AUDIO_MODES = {
  emotion: {
    capability: AUDIO_EMOTION_CAPABILITY,
    engine: 'indextts',
    needsVoice: true,
    needsEmotion: true,
    needsVoiceSource: false,
    needsRefAudio: false,
    refAudioRequired: false,
    needsDualRef: false,
    needsSpeaker: false,
    needsLanguage: false,
    needsRefText: false,
    needsInstructions: false,
    instructionsRequired: false,
  },
  synthesis: {
    capability: AUDIO_SYNTHESIS_CAPABILITY,
    engine: 'omni',
    needsVoice: false,
    needsEmotion: false,
    // 语音融合(Qwen3-TTS CustomVoice)只做预设音色 + 语言/方言。不暴露上传克隆:
    // CustomVoice checkpoint 无 speaker encoder 权重,克隆请求会让引擎维度不匹配崩溃
    // (需 Base checkpoint 才支持克隆),故此处仅预设音色(speaker) + 方言(language)。
    // 扩展限制:synthesis 原设计是多模型共享能力,VoxCPM2/CosyVoice3/MOSS-TTS 等靠
    // ref_audio 零样本克隆(无预设音色)。当前只配了 qwen3-tts,故一刀切为预设音色;若将来
    // 接入那些克隆模型,需改为按模型「音色来源」能力区分(后端 AudioModelConfig 加标注,
    // 前端按当前模型动态显示 预设音色下拉 / 克隆上传),而非对整个 tab 一刀切。
    needsVoiceSource: false,
    needsRefAudio: false,
    refAudioRequired: false,
    needsDualRef: false,
    needsSpeaker: true,
    needsLanguage: true,
    needsRefText: false,
    needsInstructions: false,
    instructionsRequired: false,
  },
  dialogue: {
    capability: AUDIO_DIALOGUE_CAPABILITY,
    engine: 'omni',
    needsVoice: false,
    needsEmotion: false,
    needsVoiceSource: false,
    needsRefAudio: false,
    refAudioRequired: false,
    needsDualRef: true,
    needsSpeaker: false,
    needsLanguage: false,
    needsRefText: false,
    needsInstructions: false,
    instructionsRequired: false,
  },
  design: {
    capability: AUDIO_DESIGN_CAPABILITY,
    engine: 'omni',
    needsVoice: false,
    needsEmotion: false,
    needsVoiceSource: false,
    needsRefAudio: false,
    refAudioRequired: false,
    needsDualRef: false,
    needsSpeaker: false,
    needsLanguage: false,
    needsRefText: false,
    needsInstructions: true,
    instructionsRequired: true,
  },
  // 视频配音(LTX-2.3,task_type=v2a):入口挂在语音页,但输入(上传视频)与产物
  // (配好音的视频)是视频形态 —— 页面渲染走 VideoPlaygroundBody(mode='dub',见
  // pages/Audio/index.jsx 分支),不经 useAudioGeneration,本表仅提供 tab 文案。
  dub: {
    capability: '视频配音',
  },
};

// 体验区子标签页顺序(5 个)。
export const AUDIO_TAB_ORDER = [
  'emotion',
  'synthesis',
  'dialogue',
  'design',
  'dub',
];

// 预设音色(语音合成 → 音色来源=预设音色,Qwen3-TTS):随 metadata.speaker 透传,门面不
// 物化、引擎按 voice/speaker 别名读。提供常用列表 + 允许自由输入。
// 预设音色 = Qwen3-TTS CustomVoice checkpoint 内置的 9 个说话人(与引擎
// /v1/audio/voices 返回一致;此前只列 6 个且含引擎不存在的 chelsie/ethan,已修正)。
//
// desc/native 逐字来自官方仓 README 的 speaker 表(QwenLM/Qwen3-TTS,§Supported Speakers)。
// 光给一个英文名,用户根本不知道 Sohee 和 Serena 差在哪,只能挨个合成去试。
//
// sample 是试听样音,放在 public/audio-presets/speakers/ 下(与情感合成的预置参考音同一
// 套路:静态文件,<audio> 直接播,不花额度、不等异步任务)。**官方仓与引擎都不提供这些
// 样音**,得拿现网 Qwen3-TTS 容器按 SPEAKER_SAMPLE_TEXT 各合成一条导出。文件不存在时
// 播放器会自行隐藏(见 AudioConfigPanel 的 onError),不会留一个点不响的空壳。
//
// native 只是「母语」建议,不是限制:每个音色都能说模型支持的任一语言,只是母语最自然。
export const AUDIO_SPEAKER_PRESETS = [
  {
    value: 'vivian',
    label: 'Vivian',
    desc: '明亮、略带锋芒的年轻女声',
    native: '中文',
  },
  { value: 'ryan', label: 'Ryan', desc: '节奏感强的活力男声', native: '英文' },
  {
    value: 'aiden',
    label: 'Aiden',
    desc: '阳光的美式男声,中频清晰',
    native: '英文',
  },
  {
    value: 'serena',
    label: 'Serena',
    desc: '温暖柔和的年轻女声',
    native: '中文',
  },
  {
    value: 'dylan',
    label: 'Dylan',
    desc: '清亮自然的北京年轻男声',
    native: '中文(北京话)',
  },
  {
    value: 'eric',
    label: 'Eric',
    desc: '明快、略带沙哑的成都男声',
    native: '中文(四川话)',
  },
  {
    value: 'ono_anna',
    label: 'Ono Anna',
    desc: '俏皮轻盈的日语女声',
    native: '日语',
  },
  {
    value: 'sohee',
    label: 'Sohee',
    desc: '温暖、情感饱满的韩语女声',
    native: '韩语',
  },
  {
    value: 'uncle_fu',
    label: 'Uncle Fu',
    desc: '低沉醇厚的成熟男声',
    native: '中文',
  },
];
export const AUDIO_DEFAULT_SPEAKER = 'vivian';

// 试听样音的静态目录。文件名即 speaker 的 value(vivian.wav / ono_anna.wav …)。
export const AUDIO_SPEAKER_SAMPLE_DIR = '/audio-presets/speakers';
export const speakerSampleUrl = (value) =>
  value ? `${AUDIO_SPEAKER_SAMPLE_DIR}/${value}.wav` : '';

// 口音(语音融合):TTS 不翻译,文本什么语言就念什么,引擎 Auto 自动识别语言,故不让用户选
// 语言(英文文本选日文无意义)。用户唯一有意义的主动选择是「中文方言口音」——同一段中文用
// 普通话(=自动)/北京话/四川话念,口音不同。value 是引擎 supported_languages 枚举(引擎对
// language 做 .title() 归一化,serving_speech.py:1648)。方言仅对中文文本生效;当前
// checkpoint 只有北京话/四川话两种(能力由 checkpoint 的 codec_language_id 决定,换
// checkpoint 可扩;非中文文本请留「自动」)。
export const AUDIO_LANGUAGES = [
  { value: '', label: '自动' },
  { value: 'Beijing_Dialect', label: '北京话' },
  { value: 'Sichuan_Dialect', label: '四川话' },
];
export const AUDIO_DEFAULT_LANGUAGE = '';

// 双人对话(dialogue 玩法)脚本示例:含 [S1]/[S2] 说话人标记。
export const AUDIO_DIALOGUE_PRESETS = [
  '[S1]今天天气真不错,我们出去走走吧。[S2]好啊,正好可以透透气。',
  '[S1]你听说了吗?公司要搬新办公室了。[S2]真的假的?什么时候的事?',
];

// 声音设计(design 玩法)声线描述示例。
export const AUDIO_DESIGN_PRESETS = [
  '一位温柔知性的中年女性,声音低沉富有磁性,语速平缓',
  '活泼开朗的少年,声音清亮,语速偏快,充满活力',
  '威严沉稳的老者,声音略带沙哑,吐字缓慢有力',
];

// ── 一键示例(带预置文件/参数)────────────────────────────────────────────
// 示例对象:{ label(按钮名), prompt(填入输入框的合成文本), params?(直接写入 inputs 的
// 标量字段), files?(inputs 文件字段 → 素材 URL;点击时 fetch→base64 写入) }。ChatArea
// 兼容纯字符串示例(向后兼容)。素材见 public/audio-presets/ 与 public/playground-samples/。

// 情感合成:①情感参考音驱动(emo_sad.wav)②情感向量驱动 ③纯预置音色。参考音色走
// voicePreset(generate 内解析),情感参考音走 emotionAudioData(→ metadata.emotion_audio)。
export const AUDIO_EMOTION_EXAMPLES = [
  {
    label: '悲伤·情感参考音',
    prompt: '酒楼丧尽天良,开始借机竞拍房间,哎,一群蠢货。',
    params: {
      voicePreset: 'voice_07',
      emotion: '',
      emotionAudioName: 'emo_sad.wav',
    },
    files: { emotionAudioData: '/audio-presets/emo_sad.wav' },
  },
  {
    label: '愤怒·情感向量',
    prompt: '你到底在搞什么?这件事必须现在给我一个交代!',
    params: { voicePreset: 'voice_08', emotion: 'angry', emoWeight: 0.7 },
  },
  {
    label: '平静·预置音色',
    prompt:
      '这个呀,就是我们精心制作准备的纪念品,大家可以看到这个色泽和材质,多么光彩照人。',
    params: { voicePreset: 'voice_03', emotion: '' },
  },
];

// 语音融合:预设音色(speaker)+ 可选方言(language)。展示不同音色与方言组合。
export const AUDIO_SYNTHESIS_EXAMPLES = [
  {
    label: '预设音色 Vivian',
    prompt: '其实我真的有发现,我是一个特别善于观察别人情绪的人。',
    params: { speaker: 'vivian' },
  },
  {
    label: '男声 Ryan',
    prompt: '大家好,欢迎来到今天的节目,我们准备了很多精彩的内容。',
    params: { speaker: 'ryan' },
  },
  {
    label: '四川话·Serena',
    prompt: '今天天气巴适得很,不如一起出去耍哈嘛。',
    params: { speaker: 'serena', language: 'Sichuan_Dialect' },
  },
];

// 双人对话:两位说话人参考音(MOSS-TTSD 官方示例)。refAudioData/refAudio2Data →
// metadata.ref_audio / ref_audio_2;脚本用 [S1]/[S2] 标记。
export const AUDIO_DIALOGUE_EXAMPLES = [
  {
    label: '双人对话',
    prompt: '[S1]今天天气真不错,我们出去走走吧。[S2]好啊,正好可以透透气。',
    params: {
      refAudioName: 'mosstts-speaker1.wav',
      refAudio2Name: 'mosstts-speaker2.wav',
    },
    files: {
      refAudioData: '/playground-samples/audio/mosstts-speaker1.wav',
      refAudio2Data: '/playground-samples/audio/mosstts-speaker2.wav',
    },
  },
];

// 声音设计:prompt=要合成的文本;声线描述 → instructions(必填,→ metadata.instructions)。
export const AUDIO_DESIGN_EXAMPLES = [
  {
    label: '美食节目主持',
    prompt:
      '亲爱的观众们,今天我要为大家做一道传说中的龙须面,请大家仔细观看我的每一个动作。',
    params: {
      instructions:
        '热情的美食节目主持人,语调生动活泼,充满对美食的热爱和专业精神。',
    },
  },
  {
    label: '温柔知性女声',
    prompt: '夜深了,愿你放下一天的疲惫,好好休息,明天又是崭新的一天。',
    params: {
      instructions: '一位温柔知性的中年女性,声音低沉富有磁性,语速平缓。',
    },
  },
];

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
          // **白名单式重建，漏一个字段就等于每次管理页保存都把它删掉**——管理页草稿
          // 正是用本函数水合(usePlaygroundAdminDraft 的 AudioModelConfig.toDraft)，
          // 保存时 recomputeModelLevel 只是 {...model} 展开，所以 parse 保不住的字段
          // 会被静默写没。engine 丢了有两重后果，且都不报错:
          //   前端 getEngineForAudioModel 恒返回空 → 2.5 的语速/语种/文本归一化控件
          //   整体不渲染;后端 AudioEngineFamilyForModel 读原始 JSON 本来还能工作，
          //   但只要运营开一次语音配置页保存，声明就被抹掉，折 extra_params 也跟着失效。
          // lower+trim 与后端比较口径一致(见 musicPlayground 同名处理)。
          engine: String(cfg?.engine || '')
            .trim()
            .toLowerCase(),
          capabilities: normalizeList(cfg?.capabilities),
          maxChars: toPositiveInt(cfg?.maxChars),
          refAudioMaxMB: toPositiveInt(cfg?.refAudioMaxMB),
          tabs: normalizeAudioTabs(cfg?.tabs),
        };
      });
    }
    return {
      default: {
        maxChars: toPositiveInt(def.maxChars) ?? AUDIO_DEFAULT_MAX_CHARS,
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

// tab 子层规范化:models[name].tabs[tabKey] 只放该 tab 声明用得到的字段。
// 空对象保留(= 该模型挂进了这个 tab,参数全走兜底);未配的字段不落键,好让
// tabScopedValue 正确降级。
const normalizeAudioTabs = (raw) => {
  if (!raw || typeof raw !== 'object') return {};
  const out = {};
  Object.entries(raw).forEach(([tabKey, cfg]) => {
    const entry = {};
    const chars = toPositiveInt(cfg?.maxChars);
    if (chars != null) entry.maxChars = chars;
    const mb = toPositiveInt(cfg?.refAudioMaxMB);
    if (mb != null) entry.refAudioMaxMB = mb;
    const note = normalizeModelNote(cfg?.note);
    if (note) entry.note = note;
    out[tabKey] = entry;
  });
  return out;
};

// 复用视频配置的列表规范化(去空格/去空/去重)。
const normalizeList = (list) =>
  Array.isArray(list)
    ? Array.from(new Set(list.map((x) => String(x).trim()).filter(Boolean)))
    : [];

// 指定能力(= 当前 tab)的语音模型集合(勾选了该能力的模型)。未传 capability 时回退到
// 情感合成(兼容旧调用)。
export const getAudioModelSet = (
  config,
  capability = AUDIO_EMOTION_CAPABILITY,
) => {
  const set = new Set();
  Object.entries(config?.models || {}).forEach(([model, cfg]) => {
    const caps = Array.isArray(cfg?.capabilities) ? cfg.capabilities : [];
    if (caps.includes(capability)) set.add(model);
  });
  return set;
};

// 字数上限:tab 级 → 模型级 → 全局默认 → 兜底常量。0 表示不限制。
// tabKey 传空时退化为改造前的「只按模型名」语义(直连请求/非体验区调用)。
export const getMaxCharsForModel = (config, model, tabKey) => {
  const m = config?.models?.[model];
  const scoped = tabScopedValue(m, tabKey, 'maxChars');
  if (scoped != null) return scoped;
  if (m && m.maxChars != null) return m.maxChars;
  if (config?.default?.maxChars != null) return config.default.maxChars;
  return AUDIO_DEFAULT_MAX_CHARS;
};

// 参考音大小上限(MB):tab 级 → 模型级 → 全局默认 → 兜底常量。
export const getRefAudioMaxMBForModel = (config, model, tabKey) => {
  const m = config?.models?.[model];
  const scoped = tabScopedValue(m, tabKey, 'refAudioMaxMB');
  if (scoped != null) return scoped;
  if (m && m.refAudioMaxMB != null) return m.refAudioMaxMB;
  if (config?.default?.refAudioMaxMB != null)
    return config.default.refAudioMaxMB;
  return AUDIO_DEFAULT_REF_AUDIO_MB;
};
