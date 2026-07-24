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
  // 仅当玩法需翻译且模型启用译文时,才加载语言模型下拉数据。
  useEffect(() => {
    if (userState?.user && showTranslation) loadTranslationGroups();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [userState?.user, showTranslation, chatGroups]);
  useEffect(() => {
    if (userState?.user && showTranslation) loadTranslationModels();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [userState?.user, showTranslation, inputs.translationGroup, chatModelSet]);

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
              content: forVideo ? TRANSLATE_SYSTEM_VIDEO : TRANSLATE_SYSTEM_BASE,
            },
            { role: 'user', content: rawText },
          ],
        },
        { skipErrorHandler: true },
      );
      const out = (
        res?.data?.choices?.[0]?.message?.content || ''
      ).trim();
      if (!out) throw new Error('translate-empty');
      return out;
    },
    [inputs.translationModel, inputs.translationGroup],
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
        if (needsAudio && !(inputs.audioData || '').startsWith('data:')) {
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
        audioDataURL = params.audioData || '';
        if (!audioDataURL.startsWith('data:')) {
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
        needsTranslation && !!text && containsCJK(text) && translationCfg.enabled;

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

  // 缺必填上传 → 发送置灰。
  const missingRequiredAudio =
    !locked &&
    ((needsAudio && !(inputs.audioData || '').startsWith('data:')) ||
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
