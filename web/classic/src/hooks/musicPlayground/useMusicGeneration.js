import {
  useState,
  useEffect,
  useCallback,
  useContext,
  useMemo,
  useRef,
} from 'react';
import { useTranslation } from 'react-i18next';
import { StatusContext } from '../../context/Status';
import { UserContext } from '../../context/User';
import {
  persistWithMedia,
  hydrateConversationsFromStorage,
  stripUnresolvedMediaRefs,
} from '../../helpers/playgroundMediaStorage';
import { urlToDataUrl } from '../../utils/playgroundMedia';
import {
  API,
  showError,
  processGroupsData,
  processModelsData,
  getUserModelsCached,
  cachedGet,
  containsCJK,
} from '../../helpers';
import {
  MUSIC_API_ENDPOINTS,
  MUSIC_STATUS,
  MUSIC_MODES,
  MUSIC_HISTORY_LIMIT,
  MUSIC_CONV_TURN_LIMIT,
  MUSIC_POLL_INTERVAL_MS,
  MUSIC_POLL_MAX_TIMES,
  MUSIC_DEFAULT_DURATION,
  MUSIC_DEFAULT_SECONDS_TOTAL,
  MUSIC_AUDIOX_DEFAULT_STEPS,
  MUSIC_AUDIOX_DEFAULT_GUIDANCE,
  MUSIC_SVS_DEFAULT_LANGUAGE,
  MUSIC_SVS_DEFAULT_CONTROL,
  MUSIC_DEFAULT_REPAINT_MODE,
  musicHistoryStorageKey,
  normalizeMusicStatus,
  parseProgress,
  buildMusicContentUrl,
  parseMusicModelConfig,
  getMusicModelSet,
  getMaxCharsForModel,
  getRefAudioMaxMBForModel,
  getVideoMaxMBForModel,
  getTranslationForModel,
} from '../../constants/musicPlayground.constants';

// 中译英走体验区聊天门面(单次非流式);后端按会话身份注入上游 key。
const MUSIC_TRANSLATE_ENDPOINT = '/pg/chat/completions';

// 语言模型下拉过滤:仅保留 chat completions 兼容端点,排除嵌入/重排序/音频/视频/图片。
// 纯图片模型后端会附带 openai 兜底端点,故用"含 chat 且不含任一非 chat"双条件。
// 注意:translatePrompt 固定打 /pg/chat/completions,故不含 openai-response——
// 仅声明 Responses 端点的模型走 chat completions 会失败,不应列入(含 openai 的仍保留)。
const CHAT_ENDPOINT_TYPES = ['openai', 'anthropic', 'gemini'];
const NON_CHAT_ENDPOINT_TYPES = [
  'embeddings',
  'jina-rerank',
  'audio-speech',
  'openai-video',
  'image-generation',
];
const isChatModel = (types) => {
  if (!Array.isArray(types) || types.length === 0) return false;
  const hasChat = types.some((x) => CHAT_ENDPOINT_TYPES.includes(x));
  const hasNonChat = types.some((x) => NON_CHAT_ENDPOINT_TYPES.includes(x));
  return hasChat && !hasNonChat;
};

// 内置翻译模板(设计 §8):把用户输入转成一句 AudioCaps 风格英文音频描述。
const TRANSLATE_SYSTEM_BASE = `You convert a user's sound request into ONE concise English caption for an audio generator (AudioX, trained on AudioCaps-style natural-language captions).
Rules:
- Output English only. One line. <= 40 words. No quotes, no brackets/tags, no music notation, no BPM, no [verse]/[chorus] style markers.
- Output ONLY the caption text itself. Do NOT add any preface, explanation, notes, labels, headings, or markdown. Never begin with phrases like "Sure", "Here is", "Caption:", or "好的". Return the caption and nothing else.
- Describe the SOUND SCENE: sound sources + environment + acoustic qualities (distant / close / loud / faint / continuous / sudden ...). Comma-separated events.
- If already English, lightly normalize; do not add unrelated content.
- Preserve the user's intent faithfully; do not invent a different scene.`;
// 视频生音(tv2a)追加:引导描述贴合视频画面(文字主导视频,见设计 §3 约束 3)。
const TRANSLATE_SYSTEM_VIDEO = `${TRANSLATE_SYSTEM_BASE}
- the sound should stay consistent with the video scene.`;

// 拟方案(文生音乐两步流程的第一步)。
//
// 官方 harness 的 Simple Mode 是两步:一句话 →【Create Sample】让 5Hz LM 产出
// caption/歌词/BPM/调式/时长 → 用户审阅编辑 →【Generate Music】。我们原来把两步压成一步,
// 在"未填歌词"时静默下发 sample_mode,而引擎在 sample_mode 下会用 LM 自己推的
// duration/bpm/keyscale 无条件覆盖用户下发值(llm_generation_inputs.py),于是"选了 60 秒
// 出 2:30"。补上审阅这一步后,提交时 caption+lyrics 都有值 → 不再命中 sample_mode 分支 →
// 时长/BPM 自然生效。
//
// 这里用的是网关自己的通用语言模型,不是 ACE-Step 的 5Hz LM(那要给门面加 create_sample
// 直通 + 两仓部署)。官方文档称模型对 caption 格式不敏感,且"用 LLM 改写模板"本就是官方
// 给的写 caption 方法之一,所以这条路是被认可的;若实测音乐性不足,再考虑接 5Hz LM。
// 每个字段的取值域都照 ACE-Step 1.5 引擎侧的硬约束写(acestep/constants.py):
//   VALID_LANGUAGES / VALID_KEYSCALES(注意 major|minor 小写、"A minor" 而非 "Am")/
//   BPM_MIN..MAX=30..300 / DURATION_MIN..MAX=10..600。
// 写错格式引擎会静默忽略该字段,退回自动推断 —— 用户以为设了,其实没生效。
const DRAFT_SYSTEM = `You are a music production assistant for ACE-Step 1.5, a text-to-music diffusion model. Given a user's one-line song idea, produce a complete production plan that the model can consume directly.

Return ONLY a JSON object. No markdown fence, no explanation, no preface:
{"caption": string, "lyrics": string, "bpm": number, "keyScale": string, "duration": number, "vocalLanguage": string}

Field rules — these are hard constraints from the engine; a malformed value is silently dropped and the setting is lost:

- caption: ENGLISH only. This is the single most important input. Cover these dimensions: genre/style, instrumentation, mood and atmosphere, timbre and production texture, vocal gender and delivery, arrangement or progression. Be concrete ("dreamy shoegaze with reverb-heavy guitars and whispered female vocals"), not generic ("nice music"). Comma-separated tags and natural prose both work.
  NEVER put tempo, BPM, key, or time signature in the caption — the model gets confused when the caption contradicts the dedicated fields. Put them in bpm / keyScale only.

- lyrics: actual singable lyrics in the language the user asked for (default: the language of the user's own request). Structure with section tags on their own line: [Verse 1], [Chorus], [Verse 2], [Bridge], [Outro]. Also available: [Interlude], [Instrumental] for non-vocal sections. Keep verses 4-8 lines; make the chorus memorable and repetitive.
  For a purely instrumental piece output exactly "[Instrumental]" and nothing else.

- bpm: integer, 30-300. Typical: slow ballad 60-80, mid-tempo 90-120, fast 130-180. Must not contradict the mood described in the caption.

- keyScale: EXACTLY the format "<note><accidental> <mode>" where note is A-G, accidental is empty / # / b, and mode is lowercase "major" or "minor". Valid: "C major", "A minor", "F# minor", "Bb major". INVALID: "C Major", "Am", "F#m", "C". Common keys (C, G, D, A minor, E minor) are the most stable.

- duration: integer seconds, 10-600. Prefer 30-60 for a short piece or 120-240 for a full song — those ranges are the most stable; very long generations tend to repeat or lose structure.

- vocalLanguage: one BCP-47-ish code from the engine's list. Common: zh (Mandarin), yue (Cantonese), en, ja, ko, es, fr, de, ru, pt, it, ar, hi, th, vi. Use "unknown" for instrumental tracks. Must match the language the lyrics are actually written in.

Keep the user's intent faithfully. Do not substitute a different genre, mood, or language than the one asked for.`;

// 音乐模型体验区 hook。一个 hook 覆盖全部 7 个玩法(mode),同一异步任务门面
// (/pg/videos)、同一轮询/历史/锁定模式;按 mode 的 engine 分支输入形态与 metadata:
//   - acestep(t2m/cover/repaint):描述 caption(prompt)+ 可选歌词/时长 +
//     (cover/repaint)驱动音频(单音频)。
//   - audiox(t2a/v2a/v2m):t2a 纯文本;v2a/v2m 视频上传(metadata.video)+ 可选文本
//     (有文本→tv2a/tv2m,否则 v2a/v2m)。
//   - soulx(svs):双音频上传(metadata.prompt_audio + metadata.target_audio),无需文本。
// 上传的音频/视频是 base64 data-url,以 Blob 存 IndexedDB,localStorage 只留短引用;
// 刷新后可恢复、可续问。纯文本玩法(t2m/t2a)无上传,不受影响。

const MUSIC_MEDIA_SCHEMA = {
  convArrayFields: [],
  // 覆盖全部上传字段:acestep 驱动音频 + audiox 视频 + soulx 双音频。
  convStringFields: [
    'audioData',
    'videoData',
    'promptAudioData',
    'targetAudioData',
  ],
  msgArrayFields: [],
  // 生成的音频结果:抓 Blob 缓存进 IDB,同视频/语音。格式无关(.mp3 / .wav)。
  msgMediaFields: ['musicUrl'],
  markNotPersisted: false,
};

const loadConversations = (storageKey) => {
  try {
    const raw = localStorage.getItem(storageKey);
    const parsed = raw ? JSON.parse(raw) : [];
    return Array.isArray(parsed) ? parsed : [];
  } catch (e) {
    return [];
  }
};

const persistConversations = (storageKey, list) => {
  persistWithMedia(storageKey, list, {
    ...MUSIC_MEDIA_SCHEMA,
    limit: MUSIC_HISTORY_LIMIT,
  });
};

let idSeq = 0;
const genId = () => `mus-${Date.now()}-${idSeq++}`;

const extractApiErrMsg = (error, fallback) => {
  const d = error?.response?.data || {};
  return d.error?.message || d.message || error?.message || fallback;
};

// 会话内需持久化/回填的全部参数字段(随 mode 使用其子集)。
const PARAM_FIELDS = [
  'group',
  'model',
  // acestep
  'lyrics',
  'duration',
  'audioData',
  'audioName',
  'bpm',
  'vocalLanguage',
  'keyScale',
  // acestep 改编/重绘
  'coverStrength',
  'repaintStart',
  'repaintEnd',
  'repaintMode',
  'repaintStrength',
  // 引用上一首生成结果作为源音频(task:<task_id>);有值时不需要上传
  'srcTaskId',
  'srcTaskLabel',
  // audiox / soulx 上传
  'videoData',
  'videoName',
  'promptAudioData',
  'promptAudioName',
  'targetAudioData',
  'targetAudioName',
  // audiox 标量
  'secondsTotal',
  // soulx
  'language',
  'control',
  // 通用
  'seed',
  'guidanceScale',
  'inferenceSteps',
  // 中译英语言模型:随会话持久化,保证锁定会话后续轮次/刷新后仍用同一语言模型。
  'translationGroup',
  'translationModel',
];

const pickParams = (src) => {
  const out = {};
  PARAM_FIELDS.forEach((f) => {
    out[f] = src[f];
  });
  return out;
};

// mode 参数化:t2m/cover/repaint(ACE-Step)+ t2a/v2a/v2m/svs(AudioX/SoulX)。
export const useMusicGeneration = (mode = 't2m') => {
  const { t } = useTranslation();
  const [statusState] = useContext(StatusContext);
  const [userState] = useContext(UserContext);

  const modeDef = MUSIC_MODES[mode] || MUSIC_MODES.t2m;
  const {
    capability,
    matchCapabilities,
    engine,
    needsAudio,
    audioMetaKey,
    needsVideo,
    needsDualAudio,
    needsText,
    needsTranslation,
    videoMetaKey,
    promptAudioMetaKey,
    targetAudioMetaKey,
    resolveTaskType,
  } = modeDef;
  const storageKey = musicHistoryStorageKey(mode);

  const [inputs, setInputs] = useState({
    group: '',
    model: '',
    // acestep:歌词/时长/驱动音频
    lyrics: '', // 可选歌词(metadata.lyrics);留空则由引擎按 caption 自动生成
    duration: MUSIC_DEFAULT_DURATION, // 秒;'' = 引擎默认
    audioData: '', // 驱动音频(base64 data-url);仅 cover/repaint 使用
    audioName: '',
    bpm: '', // 速度;空 = 自动
    vocalLanguage: '', // 演唱语言;空 = 自动
    keyScale: '', // 调式(如 C Major / Am);空 = 自动
    // acestep 改编(cover):保留原曲结构的程度;空 = 引擎默认 1.0
    coverStrength: '',
    // acestep 重绘(repaint):重绘区间与力度。区间空 = 全曲重绘(引擎默认,与改编无异)
    repaintStart: '',
    repaintEnd: '',
    repaintMode: MUSIC_DEFAULT_REPAINT_MODE,
    repaintStrength: '',
    // 引用上一首生成结果当源音频:有值时走 task:<id>,免去"下载再上传"
    srcTaskId: '',
    srcTaskLabel: '',
    // audiox / soulx 上传(base64 data-url)+ 文件名(展示用)
    videoData: '', // v2a/v2m:源视频 → metadata.video
    videoName: '',
    promptAudioData: '', // svs:音色参考 → metadata.prompt_audio
    promptAudioName: '',
    targetAudioData: '', // svs:目标曲/伴奏 → metadata.target_audio
    targetAudioName: '',
    // audiox 标量
    secondsTotal: '', // AudioX 时长(秒);默认 10
    // soulx(svs)专属
    language: MUSIC_SVS_DEFAULT_LANGUAGE,
    control: MUSIC_SVS_DEFAULT_CONTROL,
    // 通用高级参数:留空即不下发,走引擎默认。
    seed: '', // 指定后可复现;空 = 随机
    guidanceScale: '', // 贴合描述程度;空 = 引擎默认
    inferenceSteps: '', // 采样步数;空 = 引擎默认
    // 中译英用的语言模型(分组+模型两级);仅 needsTranslation 且模型启用译文时使用。
    translationGroup: '',
    translationModel: '',
  });
  const [groups, setGroups] = useState([]);
  const [models, setModels] = useState([]);
  const [modelGroupsMap, setModelGroupsMap] = useState(new Map());
  const [modelEndpointTypes, setModelEndpointTypes] = useState(new Map());
  const [translationGroups, setTranslationGroups] = useState([]);
  const [translationModels, setTranslationModels] = useState([]);

  const initialConvsRef = useRef(null);
  const [conversations, setConversations] = useState(() => {
    const raw = loadConversations(storageKey);
    const stripped = stripUnresolvedMediaRefs(raw, MUSIC_MEDIA_SCHEMA);
    initialConvsRef.current = { raw, stripped };
    return stripped;
  });
  const [currentConvId, setCurrentConvId] = useState(null);
  const [generating, setGenerating] = useState(false);
  // 「AI 帮我写词」调用中(单次非流式,1~3 秒);按钮据此转圈并禁用。
  const [drafting, setDrafting] = useState(false);

  const messages = useMemo(() => {
    const conv = conversations.find((c) => c.id === currentConvId);
    return conv ? conv.messages : [];
  }, [conversations, currentConvId]);

  // 对话内已生成过 → 参数锁定(同语音):模型/上传/参数均不可改,直到新对话。
  const locked = currentConvId !== null;

  const conversationsRef = useRef(conversations);
  conversationsRef.current = conversations;
  const lockedRef = useRef(locked);
  lockedRef.current = locked;
  const groupRef = useRef(inputs.group);
  groupRef.current = inputs.group;
  const translationGroupRef = useRef(inputs.translationGroup);
  translationGroupRef.current = inputs.translationGroup;
  const activePollRef = useRef(null);

  // mount 后从 IDB 还原上传的音频/视频,按初始对象引用逐条合并(不整体覆盖)。
  useEffect(() => {
    let canceled = false;
    const init = initialConvsRef.current;
    if (!init || !(init.raw || []).length) return;
    (async () => {
      const hydrated = await hydrateConversationsFromStorage(
        init.raw,
        MUSIC_MEDIA_SCHEMA,
      );
      if (canceled) return;
      const hydratedById = new Map(hydrated.map((c) => [c.id, c]));
      const initialSet = new Set(init.stripped);
      const mediaFields = [
        ...MUSIC_MEDIA_SCHEMA.convArrayFields,
        ...MUSIC_MEDIA_SCHEMA.convStringFields,
      ];
      setConversations((prev) =>
        prev.map((c) => {
          const h = hydratedById.get(c.id);
          if (!h) return c;
          if (initialSet.has(c)) return h;
          const merged = { ...c };
          mediaFields.forEach((f) => {
            merged[f] = h[f];
          });
          return merged;
        }),
      );
    })();
    return () => {
      canceled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const handleInputChange = useCallback((key, value) => {
    if (lockedRef.current) return;
    setInputs((prev) => ({ ...prev, [key]: value }));
  }, []);

  // 一键示例:标量参数(params)+ 文件(files:字段→素材 URL)一次性写入 inputs。
  // 文件 URL fetch→base64 data-url(与手动上传同形态);数组字段逐个转。锁定时忽略。
  const applyExample = useCallback(
    async (ex) => {
      if (lockedRef.current || !ex || typeof ex !== 'object') return;
      try {
        const patch = { ...(ex.params || {}) };
        const entries = await Promise.all(
          Object.entries(ex.files || {}).map(async ([field, url]) => [
            field,
            Array.isArray(url)
              ? await Promise.all(url.map(urlToDataUrl))
              : await urlToDataUrl(url),
          ]),
        );
        entries.forEach(([field, value]) => {
          patch[field] = value;
        });
        if (lockedRef.current) return;
        setInputs((prev) => ({ ...prev, ...patch }));
      } catch (e) {
        showError(t('加载示例素材失败,请重试'));
      }
    },
    [t],
  );

  // 音乐模型集合 = 「音乐模型配置」里声明、且能力含当前 tab 能力的模型。
  const modelConfig = useMemo(
    () => parseMusicModelConfig(statusState?.status?.MusicModelConfig),
    [statusState?.status?.MusicModelConfig],
  );

  const musicModelSet = useMemo(
    () => getMusicModelSet(modelConfig, capability, matchCapabilities),
    [modelConfig, capability, matchCapabilities],
  );

  // 当前模型的译文配置(是否启用中译英 + 默认语言模型)。
  const translationCfg = useMemo(
    () => getTranslationForModel(modelConfig, inputs.model),
    [modelConfig, inputs.model],
  );
  // 是否在面板展示「语言模型」下拉:玩法需翻译 且 当前模型启用译文。
  const showTranslation = !!needsTranslation && translationCfg.enabled;
  // 文生音乐的「AI 帮我写词」也要挑一个语言模型,与中译英共用同一套下拉。
  const isT2M = resolveTaskType(true) === 't2m';
  const showAssistModel = showTranslation || isT2M;

  // 当前模型的字数上限(0=不限制)。
  const maxChars = useMemo(
    () => getMaxCharsForModel(modelConfig, inputs.model),
    [modelConfig, inputs.model],
  );
  // 当前模型的驱动/参考音大小上限(MB)。
  const refAudioMaxMB = useMemo(
    () => getRefAudioMaxMBForModel(modelConfig, inputs.model),
    [modelConfig, inputs.model],
  );
  // 当前模型的视频大小上限(MB)。
  const videoMaxMB = useMemo(
    () => getVideoMaxMBForModel(modelConfig, inputs.model),
    [modelConfig, inputs.model],
  );

  const musicGroups = useMemo(() => {
    const set = new Set();
    musicModelSet.forEach((model) => {
      (modelGroupsMap.get(model) || []).forEach((g) => set.add(g));
    });
    return set;
  }, [musicModelSet, modelGroupsMap]);

  // chat 模型集合(可作翻译语言模型)= supported_endpoint_types 命中 chat 过滤。
  const chatModelSet = useMemo(() => {
    const set = new Set();
    modelEndpointTypes.forEach((types, model) => {
      if (isChatModel(types)) set.add(model);
    });
    return set;
  }, [modelEndpointTypes]);
  // 含 chat 模型的分组集合。
  const chatGroups = useMemo(() => {
    const set = new Set();
    chatModelSet.forEach((model) => {
      (modelGroupsMap.get(model) || []).forEach((g) => set.add(g));
    });
    return set;
  }, [chatModelSet, modelGroupsMap]);

  const loadPricing = useCallback(async () => {
    try {
      const payload = await cachedGet(MUSIC_API_ENDPOINTS.PRICING, {
        config: { skipErrorHandler: true },
      });
      const { success, data } = payload || {};
      if (!success || !Array.isArray(data)) return;
      const groupsMap = new Map();
      const endpointMap = new Map();
      data.forEach((item) => {
        if (!item || !item.model_name) return;
        groupsMap.set(item.model_name, item.enable_groups || []);
        endpointMap.set(item.model_name, item.supported_endpoint_types || []);
      });
      setModelGroupsMap(groupsMap);
      setModelEndpointTypes(endpointMap);
    } catch (e) {
      // 留空:分组不按 enable_groups 收窄
    }
  }, []);

  const loadGroups = useCallback(async () => {
    try {
      const { success, data } = await cachedGet(
        MUSIC_API_ENDPOINTS.USER_GROUPS,
      );
      if (!success) return;
      const userGroup =
        userState?.user?.group ||
        JSON.parse(localStorage.getItem('user') || '{}')?.group;
      let groupOptions = processGroupsData(data, userGroup);
      const allowAllGroups = musicGroups.has('all');
      if (musicGroups.size > 0 && !allowAllGroups) {
        groupOptions = groupOptions.filter(
          (g) => musicGroups.has(g.value) || g.value === 'auto',
        );
      }
      setGroups(groupOptions);
      setInputs((prev) => {
        if (lockedRef.current) return prev;
        const has = groupOptions.some((g) => g.value === prev.group);
        return has ? prev : { ...prev, group: groupOptions[0]?.value || '' };
      });
    } catch (e) {
      showError(t('加载分组失败'));
    }
  }, [userState, musicGroups, t]);

  const loadModels = useCallback(async () => {
    const requestedGroup = inputs.group;
    try {
      const { success, data } = await getUserModelsCached(requestedGroup);
      if (!success) return;
      if (requestedGroup !== groupRef.current) return;
      let list = Array.isArray(data) ? data : [];
      list = list.filter((m) => musicModelSet.has(m));
      const { modelOptions, selectedModel } = processModelsData(
        list,
        inputs.model,
      );
      setModels(modelOptions);
      setInputs((prev) => {
        if (lockedRef.current) return prev;
        return prev.model === selectedModel
          ? prev
          : { ...prev, model: selectedModel || '' };
      });
    } catch (e) {
      showError(t('加载模型失败'));
    }
  }, [inputs.group, inputs.model, musicModelSet, t]);

  // 翻译语言模型的分组下拉:仅含 chat 模型的分组。
  const loadTranslationGroups = useCallback(async () => {
    try {
      const { success, data } = await cachedGet(
        MUSIC_API_ENDPOINTS.USER_GROUPS,
      );
      if (!success) return;
      const userGroup =
        userState?.user?.group ||
        JSON.parse(localStorage.getItem('user') || '{}')?.group;
      let opts = processGroupsData(data, userGroup);
      const allowAll = chatGroups.has('all');
      if (chatGroups.size > 0 && !allowAll) {
        opts = opts.filter(
          (g) => chatGroups.has(g.value) || g.value === 'auto',
        );
      }
      setTranslationGroups(opts);
      setInputs((prev) => {
        const has = opts.some((g) => g.value === prev.translationGroup);
        if (has) return prev;
        // 未选定分组时:优先选包含默认语言模型的分组,让管理员配的 defaultModel 可命中;
        // 匹配不到再退回第一个可用分组。
        let target = opts[0]?.value || '';
        const wantModel = translationCfg.defaultModel;
        if (wantModel) {
          const groupsOfModel = modelGroupsMap.get(wantModel) || [];
          const hit = opts.find((g) => groupsOfModel.includes(g.value));
          if (hit) target = hit.value;
        }
        return { ...prev, translationGroup: target };
      });
    } catch (e) {
      // 静默:翻译分组加载失败不阻塞主流程
    }
  }, [userState, chatGroups, modelGroupsMap, translationCfg.defaultModel]);

  // 翻译语言模型下拉:所选翻译分组下的 chat 模型;默认优先取模型配置的 defaultModel。
  const loadTranslationModels = useCallback(async () => {
    const requestedGroup = inputs.translationGroup;
    try {
      const { success, data } = await getUserModelsCached(requestedGroup);
      if (!success) return;
      // 陈旧响应守卫:请求在途时若已切换翻译分组,丢弃旧组结果(同 loadModels)。
      if (requestedGroup !== translationGroupRef.current) return;
      let list = Array.isArray(data) ? data : [];
      // pricing 就绪时按 chat 端点精确过滤;若 pricing 未就绪(端点信息缺失),
      // 无从判断则 fail open——保留全部模型,避免下拉全空导致翻译整条不可用。
      if (modelEndpointTypes.size > 0) {
        list = list.filter((m) => chatModelSet.has(m));
      }
      const { modelOptions } = processModelsData(list, inputs.translationModel);
      setTranslationModels(modelOptions);
      setInputs((prev) => {
        const has = modelOptions.some((o) => o.value === prev.translationModel);
        if (has) return prev;
        const wanted = translationCfg.defaultModel;
        const fallback = modelOptions.some((o) => o.value === wanted)
          ? wanted
          : modelOptions[0]?.value || '';
        return { ...prev, translationModel: fallback };
      });
    } catch (e) {
      // 静默
    }
  }, [
    inputs.translationGroup,
    inputs.translationModel,
    chatModelSet,
    modelEndpointTypes,
    translationCfg.defaultModel,
  ]);

  useEffect(() => {
    if (userState?.user) loadPricing();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [userState?.user]);
  useEffect(() => {
    if (userState?.user) loadGroups();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [userState?.user, musicGroups]);
  useEffect(() => {
    if (userState?.user) loadModels();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [userState?.user, inputs.group, musicModelSet]);
  // 辅助语言模型下拉:两个用途共用一套选择 —— 音效的中译英,和文生音乐的「AI 帮我写词」。
  // 都是「单次非流式打 /pg/chat/completions」,没必要让用户选两次。
  useEffect(() => {
    if (userState?.user && showAssistModel) loadTranslationGroups();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [userState?.user, showAssistModel, chatGroups]);
  useEffect(() => {
    if (userState?.user && showAssistModel) loadTranslationModels();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [userState?.user, showAssistModel, inputs.translationGroup, chatModelSet]);

  const patchConvMessage = useCallback(
    (convId, msgId, patch) => {
      setConversations((prev) => {
        const next = prev.map((c) =>
          c.id === convId
            ? {
                ...c,
                messages: c.messages.map((m) =>
                  m.id === msgId ? { ...m, ...patch } : m,
                ),
              }
            : c,
        );
        persistConversations(storageKey, next);
        return next;
      });
    },
    [storageKey],
  );

  const turnsUsed = useMemo(
    () => messages.filter((m) => m.role === 'user').length,
    [messages],
  );
  const turnLimitReached = turnsUsed >= MUSIC_CONV_TURN_LIMIT;

  const finishPoll = useCallback(() => {
    if (activePollRef.current?.timer) clearTimeout(activePollRef.current.timer);
    activePollRef.current = null;
    setGenerating(false);
  }, []);

  const pollOnce = useCallback(
    async (convId, msgId, taskId, count) => {
      const active = activePollRef.current;
      if (!active || active.canceled || active.taskId !== taskId) return;
      try {
        const res = await API.get(
          `${MUSIC_API_ENDPOINTS.VIDEO_FETCH}/${encodeURIComponent(taskId)}`,
          { skipErrorHandler: true },
        );
        const data = res.data || {};
        const inner = data.data || {};
        const status = normalizeMusicStatus(data.status || inner.status);
        const progress = parseProgress(
          data.progress != null ? data.progress : inner.progress,
        );

        if (status === MUSIC_STATUS.COMPLETED) {
          patchConvMessage(convId, msgId, {
            status: MUSIC_STATUS.COMPLETED,
            progress: 100,
            musicUrl: buildMusicContentUrl(taskId),
          });
          finishPoll();
          return;
        }
        if (status === MUSIC_STATUS.FAILED) {
          const msg =
            data.error?.message ||
            inner.error?.message ||
            inner.fail_reason ||
            data.fail_reason ||
            t('生成失败');
          patchConvMessage(convId, msgId, {
            status: MUSIC_STATUS.FAILED,
            error: msg,
          });
          showError(msg);
          finishPoll();
          return;
        }
        patchConvMessage(convId, msgId, {
          status: status || MUSIC_STATUS.IN_PROGRESS,
          ...(progress !== undefined ? { progress } : {}),
        });
        if (count >= MUSIC_POLL_MAX_TIMES) {
          patchConvMessage(convId, msgId, { pollTimedOut: true });
          finishPoll();
          return;
        }
      } catch (e) {
        if (count >= MUSIC_POLL_MAX_TIMES) {
          patchConvMessage(convId, msgId, { pollTimedOut: true });
          finishPoll();
          return;
        }
      }
      const cur = activePollRef.current;
      if (!cur || cur.canceled || cur.taskId !== taskId) return;
      cur.timer = setTimeout(
        () => pollOnce(convId, msgId, taskId, count + 1),
        MUSIC_POLL_INTERVAL_MS,
      );
    },
    [patchConvMessage, finishPoll, t],
  );

  const resumePoll = useCallback(
    (convId, msgId, taskId) => {
      if (!taskId) return;
      const active = activePollRef.current;
      if (active && active.taskId === taskId && !active.canceled) return;
      if (active?.timer) clearTimeout(active.timer);
      patchConvMessage(convId, msgId, { pollTimedOut: false });
      activePollRef.current = {
        convId,
        msgId,
        taskId,
        timer: null,
        canceled: false,
      };
      setGenerating(true);
      activePollRef.current.timer = setTimeout(
        () => pollOnce(convId, msgId, taskId, 1),
        MUSIC_POLL_INTERVAL_MS,
      );
    },
    [pollOnce, patchConvMessage],
  );

  // 挂载后为最近一个仍在进行中的任务恢复轮询。
  useEffect(() => {
    if (!userState?.user || activePollRef.current) return;
    let best = null;
    conversationsRef.current.forEach((conv) => {
      (conv.messages || []).forEach((m) => {
        if (m.role !== 'assistant') return;
        const active =
          m.status === MUSIC_STATUS.QUEUED ||
          m.status === MUSIC_STATUS.IN_PROGRESS;
        if (!active) return;
        if (m.taskId) {
          const ts = Number(String(m.id).split('-')[1]) || 0;
          if (!best || ts > best.ts) {
            best = { convId: conv.id, msgId: m.id, taskId: m.taskId, ts };
          }
        } else {
          // 孤儿助手消息:建消息后未拿到 taskId 即中断(如翻译那几秒内刷新页面),
          // 无从恢复。清「翻译中」并置 FAILED(带重试),避免气泡永久转圈。
          patchConvMessage(conv.id, m.id, {
            translating: false,
            status: MUSIC_STATUS.FAILED,
            error: t('翻译失败,请改用英文描述'),
          });
        }
      });
    });
    if (best) resumePoll(best.convId, best.msgId, best.taskId);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [userState?.user]);

  const refetch = useCallback(
    (msgId, taskId) => {
      if (currentConvId == null || !taskId) return;
      resumePoll(currentConvId, msgId, taskId);
    },
    [currentConvId, resumePoll],
  );

  // 单次非流式调用选中的语言模型,把中文 rawText 转成一句英文音频描述。
  // forVideo=true 时用带"贴合画面"约束的模板(tv2a)。失败抛错,交由 generate 走降级。
  const translatePrompt = useCallback(
    async (rawText, forVideo) => {
      const model = inputs.translationModel;
      const group = inputs.translationGroup;
      if (!model) throw new Error('no-translation-model');
      // 复用 axios API 实例:自动带 baseURL(分离部署时打到 API 而非前端 origin)与
      // New-API-User 认证头,与 /pg/videos 提交同构。skipErrorHandler 交由本地 catch 降级。
      const res = await API.post(
        MUSIC_TRANSLATE_ENDPOINT,
        {
          model,
          group,
          stream: false,
          messages: [
            {
              role: 'system',
              content: forVideo
                ? TRANSLATE_SYSTEM_VIDEO
                : TRANSLATE_SYSTEM_BASE,
            },
            { role: 'user', content: rawText },
          ],
        },
        { skipErrorHandler: true },
      );
      const out = (res?.data?.choices?.[0]?.message?.content || '').trim();
      if (!out) throw new Error('translate-empty');
      return out;
    },
    [inputs.translationModel, inputs.translationGroup],
  );

  // 「AI 帮我写词」= 官方 Simple Mode 里【Create Sample】那一步:据一句话描述拟出
  // caption/歌词/BPM/调式/时长,直接回填到配置面板的各个控件,由用户过目再改。
  //
  // 这一步的意义不只是省事:填了歌词之后提交就不再命中 sample_mode 分支,引擎那边
  // "用 LM 自己推的时长覆盖用户值"的逻辑也就不会触发,时长/BPM 才真正生效。
  const draftPlan = useCallback(
    async (rawText) => {
      const text = (rawText || '').trim();
      if (!text) {
        showError(t('请先在下方输入框描述你想要的音乐'));
        return false;
      }
      const model = inputs.translationModel;
      if (!model) {
        showError(t('请先在「辅助语言模型」里选择一个模型'));
        return false;
      }
      setDrafting(true);
      try {
        const res = await API.post(
          MUSIC_TRANSLATE_ENDPOINT,
          {
            model,
            group: inputs.translationGroup,
            stream: false,
            messages: [
              { role: 'system', content: DRAFT_SYSTEM },
              { role: 'user', content: text },
            ],
          },
          { skipErrorHandler: true },
        );
        let out = (res?.data?.choices?.[0]?.message?.content || '').trim();
        // 模型常自作主张包一层 ```json 围栏,剥掉再解析。
        out = out
          .replace(/^```(?:json)?\s*/i, '')
          .replace(/\s*```$/, '')
          .trim();
        const plan = JSON.parse(out);
        setInputs((prev) => {
          const next = { ...prev };
          if (plan.lyrics) next.lyrics = String(plan.lyrics).trim();
          if (Number.isFinite(Number(plan.bpm)) && Number(plan.bpm) > 0)
            next.bpm = String(Math.round(Number(plan.bpm)));
          if (plan.keyScale) next.keyScale = String(plan.keyScale).trim();
          if (
            Number.isFinite(Number(plan.duration)) &&
            Number(plan.duration) > 0
          )
            next.duration = String(Math.round(Number(plan.duration)));
          if (plan.vocalLanguage)
            next.vocalLanguage = String(plan.vocalLanguage).trim();
          return next;
        });
        // caption 单独返回:它要替换输入框里的描述,由调用方决定怎么用。
        return typeof plan.caption === 'string' ? plan.caption.trim() : true;
      } catch (e) {
        showError(t('生成方案失败,请重试或换一个语言模型'));
        return false;
      } finally {
        setDrafting(false);
      }
    },
    [inputs.translationModel, inputs.translationGroup, t],
  );

  const generate = useCallback(
    async (prompt) => {
      const text = (prompt || '').trim();
      // t2m/cover/repaint/t2a 需文本;v2*/tv2* 文本可选;svs 无需文本。
      if (needsText && !text) return;
      if (generating) return;

      // 字数上限(0=不限制):按当前模型配置就地拦截(仅对有文本时)。
      if (text) {
        const charLimit = getMaxCharsForModel(modelConfig, inputs.model);
        if (charLimit > 0 && text.length > charLimit) {
          showError(
            t('描述文本超过字数上限 {{max}} 字(当前 {{cur}} 字)', {
              max: charLimit,
              cur: text.length,
            }),
          );
          return;
        }
      }

      let convId = currentConvId;
      let params;
      if (convId == null) {
        if (!inputs.model) {
          showError(t('请先选择一个音乐模型'));
          return;
        }
        if (
          needsAudio &&
          !inputs.srcTaskId &&
          !(inputs.audioData || '').startsWith('data:')
        ) {
          showError(t('请先上传驱动音频'));
          return;
        }
        if (needsVideo && !(inputs.videoData || '').startsWith('data:')) {
          showError(t('请先上传源视频'));
          return;
        }
        if (
          needsDualAudio &&
          (!(inputs.promptAudioData || '').startsWith('data:') ||
            !(inputs.targetAudioData || '').startsWith('data:'))
        ) {
          showError(t('请先上传音色参考与目标曲/伴奏'));
          return;
        }
        convId = genId();
        params = pickParams(inputs);
      } else {
        const conv = conversationsRef.current.find((c) => c.id === convId);
        const used = conv
          ? conv.messages.filter((m) => m.role === 'user').length
          : 0;
        if (used >= MUSIC_CONV_TURN_LIMIT) {
          showError(
            t('本轮对话生成次数已达上限（{{count}} 次），请开启新对话', {
              count: MUSIC_CONV_TURN_LIMIT,
            }),
          );
          return;
        }
        params = conv ? pickParams(conv) : pickParams(inputs);
      }

      // 上传的驱动/参考媒体:刷新后 localStorage 已剥离 → 提示重开对话重传。
      let audioDataURL = '';
      let videoDataURL = '';
      let promptAudioURL = '';
      let targetAudioURL = '';
      if (needsAudio) {
        // 引用上一首生成结果:发 task:<task_id>,由后端 nfsinput/taskref.go 在共享盘上
        // 直读产物(零网络、已做归属与终态校验),不必把音频拉成 base64 再传一遍。
        // 它也不受 localStorage 剥离上传数据的影响,所以放在失效校验之前。
        audioDataURL = params.srcTaskId
          ? `task:${params.srcTaskId}`
          : params.audioData || '';
        if (!params.srcTaskId && !audioDataURL.startsWith('data:')) {
          showError(t('驱动音频已失效,请开启新对话并重新上传'));
          return;
        }
      }
      if (needsVideo) {
        videoDataURL = params.videoData || '';
        if (!videoDataURL.startsWith('data:')) {
          showError(t('源视频已失效,请开启新对话并重新上传'));
          return;
        }
      }
      if (needsDualAudio) {
        promptAudioURL = params.promptAudioData || '';
        targetAudioURL = params.targetAudioData || '';
        if (
          !promptAudioURL.startsWith('data:') ||
          !targetAudioURL.startsWith('data:')
        ) {
          showError(t('参考音频已失效,请开启新对话并重新上传'));
          return;
        }
      }

      // ── 中译英(前端编排)──────────────────────────────────────────
      // 命中「玩法需翻译 + 有文本 + 含中文 + 该模型启用译文」时,先调语言模型转英文,
      // 再用英文提交(AudioX 文本编码器仅认英文,中文会塌成 <unk>)。已是英文则不触发。
      // 失败降级(设计 §11):视频生音 → 丢文字改纯视频 v2a;文生音效 → 报错不提交。
      // 时序:消息先建(点发送即可见),翻译放在建消息之后 —— 译文回填 userMsg 展示对照,
      // 助手气泡在拿到 taskId 前先显示「翻译中…」,避免翻译那几秒聊天区空白。
      const willTranslate =
        needsTranslation &&
        !!text &&
        containsCJK(text) &&
        translationCfg.enabled;

      const reqId = genId();
      const now = new Date().toISOString();
      const userMsg = {
        id: `${reqId}-u`,
        role: 'user',
        // 空文本时用当前玩法的能力标签(视频配音效/视频配乐/歌声合成…),而非硬编码
        // "歌声合成" —— 否则 v2a/v2m 的纯视频任务在历史里被误标成唱歌。
        content: text || `（${capability}）`,
      };
      const asstId = `${reqId}-a`;
      const asstMsg = {
        id: asstId,
        role: 'assistant',
        status: MUSIC_STATUS.QUEUED,
        model: params.model,
        prompt: text,
        progress: 0,
        taskId: null,
        musicUrl: null,
        // 翻译中标志:渲染层据此优先显示「翻译中…」;译文回填/降级/失败后一律置 false。
        translating: willTranslate,
      };

      setConversations((prev) => {
        const idx = prev.findIndex((c) => c.id === convId);
        let next;
        if (idx === -1) {
          next = [
            {
              id: convId,
              ...params,
              title: text || capability,
              createdAt: now,
              updatedAt: now,
              messages: [userMsg, asstMsg],
            },
            ...prev,
          ];
        } else {
          const conv = {
            ...prev[idx],
            updatedAt: now,
            messages: [...prev[idx].messages, userMsg, asstMsg],
          };
          next = [conv, ...prev.filter((_, i) => i !== idx)];
        }
        next = next.slice(0, MUSIC_HISTORY_LIMIT);
        persistConversations(storageKey, next);
        return next;
      });
      if (currentConvId == null) setCurrentConvId(convId);
      setGenerating(true);

      // 建消息之后再翻译:成功回填译文小字(userMsg)并清「翻译中」;失败按玩法降级。
      let effectiveText = text;
      if (willTranslate) {
        try {
          effectiveText = await translatePrompt(text, needsVideo);
          patchConvMessage(convId, userMsg.id, {
            translatedText: effectiveText,
          });
          patchConvMessage(convId, asstId, { translating: false });
        } catch (e) {
          if (needsVideo) {
            // 视频生音降级:丢弃文字,按纯视频 v2a 继续提交(气泡保留原文,不显译文)。
            effectiveText = '';
            patchConvMessage(convId, asstId, { translating: false });
            showError(t('文字未生效,已按纯视频生成'));
          } else {
            // 文生音效降级:消息已建 → asstMsg 直接置 FAILED(带重试),不提交。
            patchConvMessage(convId, asstId, {
              status: MUSIC_STATUS.FAILED,
              translating: false,
              error: t('翻译失败,请改用英文描述'),
            });
            showError(t('翻译失败,请改用英文描述'));
            setGenerating(false);
            return;
          }
        }
      }

      // 解析 task_type:视频生音按是否带(译后)文本分支到 tv2a/v2a;其余与文本无关。
      const resolvedTaskType = resolveTaskType(effectiveText.length > 0);
      // 占位符仅用于 svs(歌声合成引擎需非空 input,文本仅占位);v2a 是纯视频输入,
      // 后端明确允许空 prompt —— 绝不能塞占位,否则会拿"歌声合成"去条件化 AudioX。
      const promptField =
        effectiveText || (resolvedTaskType === 'svs' ? t('歌声合成') : '');

      try {
        // gpustackplus 门面契约:task_type + 输入(音频/视频)+ 标量参数经 metadata 透传
        // (adaptor 把上传物化 NFS → input_refs → 引擎)。
        const metadata = { task_type: resolvedTaskType };

        if (engine === 'acestep') {
          // ── ACE-Step:歌词/时长/驱动音频/BPM/演唱语言 ──
          const lyrics = (params.lyrics || '').trim();
          const dur = parseFloat(params.duration);
          if (Number.isFinite(dur) && dur > 0) metadata.audio_duration = dur;
          if (needsAudio && audioMetaKey) metadata[audioMetaKey] = audioDataURL;

          // thinking=true 让 5Hz LM 先出音频语义码再喂 DiT(llm_dit 两阶段);默认的 false
          // 只跑单阶段 dit。官方把它列为质量第一条建议(GRADIO_GUIDE.md「Use thinking mode」)。
          metadata.thinking = true;
          // 引擎 batch_size 默认 2,但 ACE-Step 侧的门面适配层只把 raw_audio_paths[0]
          // 搬到 save_result_path(tasks_facade_service.materialize_output),第二首直接丢。
          // 显式压到 1:同样拿一首,少烧一半算力。要真拿多首得改 ACE-Step/gpustack/new-api
          // 三层的单产物契约,不在本次范围——这里先并发提交多个任务代替。
          metadata.batch_size = 1;

          // t2m 且未填歌词 → 额外开启 sample 模式:引擎按描述用 LM 自动生成 caption+歌词。
          // prompt 仍保持=描述文本 —— 既满足门面「prompt 必填」校验,也让不认 sample_mode
          // 的路径能靠 prompt + LM 补词兜底。其余情况(已填歌词 或 cover/repaint):描述作
          // 为 caption(prompt),歌词直接透传。
          if (resolvedTaskType === 't2m' && !lyrics) {
            metadata.sample_mode = true;
            metadata.sample_query = text;
          } else if (lyrics) {
            metadata.lyrics = lyrics;
          }

          const bpm = parseInt(params.bpm, 10);
          if (Number.isFinite(bpm) && bpm > 0) metadata.bpm = bpm;
          const lang = (params.vocalLanguage || '').trim();
          if (lang) metadata.vocal_language = lang;
          const keyScale = (params.keyScale || '').trim();
          if (keyScale) metadata.key_scale = keyScale;

          // 改编:保留原曲结构的程度。官方标为 cover 的 Key parameter,原先没暴露,
          // 用户只能吃引擎默认的 1.0(最大保留),等于"改编"改不动。
          if (resolvedTaskType === 'cover') {
            const cs = parseFloat(params.coverStrength);
            if (Number.isFinite(cs) && cs >= 0 && cs <= 1)
              metadata.audio_cover_strength = cs;
          }

          // 重绘:区间 + 力度。区间不填时引擎默认 start=0/end=-1 → 全曲重绘,那就跟改编
          // 没区别了 —— repaint 的价值就在只改一段,所以这里如实透传用户填的区间。
          if (resolvedTaskType === 'repaint') {
            const rs = parseFloat(params.repaintStart);
            const re = parseFloat(params.repaintEnd);
            if (Number.isFinite(rs) && rs >= 0) metadata.repainting_start = rs;
            if (Number.isFinite(re) && re > 0) metadata.repainting_end = re;
            const rm = (params.repaintMode || '').trim();
            if (rm) metadata.repaint_mode = rm;
            // repaint_strength 仅 balanced 模式生效(引擎侧语义),其余模式不下发。
            if (rm === 'balanced') {
              const rst = parseFloat(params.repaintStrength);
              if (Number.isFinite(rst) && rst >= 0 && rst <= 1)
                metadata.repaint_strength = rst;
            }
          }

          const gs = parseFloat(params.guidanceScale);
          if (Number.isFinite(gs) && gs > 0) metadata.guidance_scale = gs;
          const steps = parseInt(params.inferenceSteps, 10);
          if (Number.isFinite(steps) && steps > 0)
            metadata.inference_steps = steps;
          const seedStr = String(params.seed ?? '').trim();
          if (seedStr !== '') {
            metadata.seed = seedStr;
            metadata.use_random_seed = false;
          }
        } else {
          // ── AudioX / SoulX:视频/双音频 + 标量 ──
          // AudioX 另需 audiox_task 与 task_type 同值。
          if (engine === 'audiox') metadata.audiox_task = resolvedTaskType;

          if (needsVideo && videoMetaKey) metadata[videoMetaKey] = videoDataURL;
          if (needsDualAudio) {
            if (promptAudioMetaKey)
              metadata[promptAudioMetaKey] = promptAudioURL;
            if (targetAudioMetaKey)
              metadata[targetAudioMetaKey] = targetAudioURL;
          }

          // AudioX 专属:时长(秒);SoulX 无此参数,不下发。所见即所发:留空补 UI 默认 10。
          if (engine === 'audiox') {
            const secs = parseFloat(params.secondsTotal);
            metadata.seconds_total =
              Number.isFinite(secs) && secs > 0
                ? secs
                : MUSIC_DEFAULT_SECONDS_TOTAL;
          }
          // 采样步数:AudioX(AudioXPipeline)硬要 num_inference_steps 且**无** deploy-config
          // 兜底,留空必须补上 UI 默认(placeholder 承诺的 250),否则引擎报
          // "AudioXPipeline requires sampling_params.num_inference_steps"。SoulX(svs)有
          // deploy-config 默认(32),留空交给引擎,不在此下发。
          const steps = parseInt(params.inferenceSteps, 10);
          if (Number.isFinite(steps) && steps > 0) {
            metadata.num_inference_steps = steps;
          } else if (engine === 'audiox') {
            metadata.num_inference_steps = MUSIC_AUDIOX_DEFAULT_STEPS;
          }
          // guidance:AudioX 留空补 UI 默认 7(所见即所发);SoulX 交给引擎 deploy-config
          // 默认 3(ConfigPanel 的 SoulX 占位也已改成 3,显示=生效),不在此下发。
          const gs = parseFloat(params.guidanceScale);
          if (Number.isFinite(gs) && gs > 0) {
            metadata.guidance_scale = gs;
          } else if (engine === 'audiox') {
            metadata.guidance_scale = MUSIC_AUDIOX_DEFAULT_GUIDANCE;
          }
          const seedStr = String(params.seed ?? '').trim();
          if (seedStr !== '') {
            const seedNum = parseInt(seedStr, 10);
            if (Number.isFinite(seedNum)) metadata.seed = seedNum;
          }

          // SoulX(svs)专属:演唱语言 + 控制方式。
          if (engine === 'soulx') {
            const lang = (params.language || '').trim();
            if (lang) metadata.language = lang;
            const control = (params.control || '').trim();
            if (control) metadata.control = control;
          }
        }

        const body = {
          model: params.model,
          group: params.group,
          prompt: promptField,
          metadata,
        };
        const res = await API.post(
          MUSIC_API_ENDPOINTS.VIDEO_GENERATIONS,
          body,
          {
            skipErrorHandler: true,
          },
        );
        const data = res.data || {};
        const inner = data.data || {};
        const taskId = data.id || data.task_id || inner.task_id || inner.id;
        if (!taskId) throw new Error(t('提交任务失败'));
        const status = normalizeMusicStatus(data.status || inner.status);
        if (status === MUSIC_STATUS.FAILED) {
          const msg =
            data.error?.message ||
            inner.error?.message ||
            inner.fail_reason ||
            data.fail_reason ||
            t('生成失败');
          patchConvMessage(convId, asstId, {
            status: MUSIC_STATUS.FAILED,
            error: msg,
          });
          showError(msg);
          setGenerating(false);
          return;
        }
        patchConvMessage(convId, asstId, { taskId, status, progress: 0 });
        activePollRef.current = {
          convId,
          msgId: asstId,
          taskId,
          timer: null,
          canceled: false,
        };
        activePollRef.current.timer = setTimeout(
          () => pollOnce(convId, asstId, taskId, 1),
          MUSIC_POLL_INTERVAL_MS,
        );
      } catch (error) {
        const msg = extractApiErrMsg(error, t('生成失败'));
        patchConvMessage(convId, asstId, {
          status: MUSIC_STATUS.FAILED,
          error: msg,
        });
        showError(msg);
        setGenerating(false);
      }
    },
    [
      currentConvId,
      inputs,
      generating,
      engine,
      needsAudio,
      audioMetaKey,
      needsVideo,
      needsDualAudio,
      needsText,
      needsTranslation,
      translationCfg.enabled,
      translatePrompt,
      videoMetaKey,
      promptAudioMetaKey,
      targetAudioMetaKey,
      resolveTaskType,
      storageKey,
      patchConvMessage,
      pollOnce,
      modelConfig,
      t,
    ],
  );

  const regenerate = useCallback((prompt) => generate(prompt), [generate]);

  const newConversation = useCallback(() => {
    setCurrentConvId(null);
  }, []);

  const clearHistory = useCallback(() => {
    if (activePollRef.current) activePollRef.current.canceled = true;
    finishPoll();
    setConversations([]);
    persistConversations(storageKey, []);
    setCurrentConvId(null);
  }, [finishPoll, storageKey]);

  const deleteHistoryItem = useCallback(
    (id) => {
      const active = activePollRef.current;
      if (active && active.convId === id) {
        active.canceled = true;
        finishPoll();
      }
      setConversations((prev) => {
        const next = prev.filter((c) => c.id !== id);
        persistConversations(storageKey, next);
        return next;
      });
      setCurrentConvId((cur) => (cur === id ? null : cur));
    },
    [finishPoll, storageKey],
  );

  const openHistoryItem = useCallback(
    (conv) => {
      setCurrentConvId(conv.id);
      setInputs((prev) => {
        const next = { ...prev };
        PARAM_FIELDS.forEach((f) => {
          if (conv[f] != null) next[f] = conv[f];
        });
        return next;
      });
      const assts = (conv.messages || []).filter((m) => m.role === 'assistant');
      const last = assts[assts.length - 1];
      if (
        last?.taskId &&
        (last.status === MUSIC_STATUS.QUEUED ||
          last.status === MUSIC_STATUS.IN_PROGRESS)
      ) {
        resumePoll(conv.id, last.id, last.taskId);
      }
    },
    [resumePoll],
  );

  useEffect(() => {
    return () => {
      if (activePollRef.current?.timer)
        clearTimeout(activePollRef.current.timer);
      activePollRef.current = null;
    };
  }, []);

  // 缺必填上传 → 发送置灰。引用上一首生成结果(srcTaskId)时不需要上传,故同样算已备齐。
  const missingRequiredAudio =
    !locked &&
    ((needsAudio &&
      !inputs.srcTaskId &&
      !(inputs.audioData || '').startsWith('data:')) ||
      (needsDualAudio &&
        (!(inputs.promptAudioData || '').startsWith('data:') ||
          !(inputs.targetAudioData || '').startsWith('data:'))));
  const missingRequiredVideo =
    !locked && needsVideo && !(inputs.videoData || '').startsWith('data:');

  return {
    inputs,
    handleInputChange,
    applyExample,
    groups,
    models,
    messages,
    conversations,
    currentConvId,
    generating,
    locked,
    turnLimitReached,
    missingRequiredAudio,
    missingRequiredVideo,
    engine,
    needsAudio,
    needsVideo,
    needsDualAudio,
    needsText,
    needsTranslation,
    showTranslation,
    showAssistModel,
    drafting,
    draftPlan,
    translationGroups,
    translationModels,
    maxChars,
    refAudioMaxMB,
    videoMaxMB,
    generate,
    regenerate,
    refetch,
    newConversation,
    clearHistory,
    deleteHistoryItem,
    openHistoryItem,
  };
};
