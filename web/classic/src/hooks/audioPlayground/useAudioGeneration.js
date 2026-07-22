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
} from '../../helpers';
import {
  AUDIO_API_ENDPOINTS,
  AUDIO_STATUS,
  AUDIO_MODES,
  AUDIO_HISTORY_LIMIT,
  AUDIO_CONV_TURN_LIMIT,
  AUDIO_POLL_INTERVAL_MS,
  AUDIO_POLL_MAX_TIMES,
  AUDIO_DEFAULT_EMO_WEIGHT,
  AUDIO_DEFAULT_SPEAKER,
  AUDIO_DEFAULT_LANGUAGE,
  AUDIO_DEFAULT_VOICE_SOURCE,
  AUDIO_VOICE_SOURCE_UPLOAD,
  AUDIO_VOICE_SOURCE_PRESET,
  PRESET_VOICES,
  VOICE_UPLOAD_VALUE,
  emotionToVector,
  audioHistoryStorageKey,
  normalizeAudioStatus,
  parseProgress,
  buildAudioContentUrl,
  parseAudioModelConfig,
  getAudioModelSet,
  getMaxCharsForModel,
  getRefAudioMaxMBForModel,
} from '../../constants/audioPlayground.constants';

// 语音合成体验区 hook。一个 hook 覆盖全部 4 个玩法(mode),同一异步任务门面
// (/pg/videos,task_type=tts)、同一轮询/历史/锁定模式;按 mode 分支输入形态与 metadata:
//   - emotion(情感合成,IndexTTS-2):参考音色(预置/上传)→ metadata.voice + 可选情感参考音
//     → metadata.emotion_audio + emo_vector/emo_alpha 标量。
//   - synthesis(语音合成,Omni 家族):音色来源 toggle:
//       · 上传克隆(默认):克隆源上传 → metadata.ref_audio + 可选参考文本 → metadata.ref_text;
//       · 预设音色:音色 → metadata.speaker(标量,不发 ref_audio)。
//       语言(可选)→ metadata.language(标量),两种来源都可带。
//   - dialogue(双人对话):脚本([S1]/[S2])作 prompt + 双参考音 → ref_audio / ref_audio_2。
//   - design(声音设计):声线描述 → metadata.instructions(无参考音)。
// 上传的参考音是 base64 data-url,以 Blob 存 IndexedDB,localStorage 只留短引用;刷新后可
// 恢复、可续问。预置音色只存 id;synthesis/design 的标量参数随会话直接持久化。

const AUDIO_MEDIA_SCHEMA = {
  convArrayFields: [],
  // 覆盖全部上传字段:情感合成参考音色/情感参考音 + Omni 克隆参考音(单/双)。
  convStringFields: [
    'voiceData',
    'emotionAudioData',
    'refAudioData',
    'refAudio2Data',
  ],
  msgArrayFields: [],
  // 生成的音频结果:抓 Blob 缓存进 IDB,同视频/音乐。
  msgMediaFields: ['audioUrl'],
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
    ...AUDIO_MEDIA_SCHEMA,
    limit: AUDIO_HISTORY_LIMIT,
  });
};

let idSeq = 0;
const genId = () => `aud-${Date.now()}-${idSeq++}`;

// 预置音色 base64 内存缓存:同一会话内每个音色只 fetch+编码一次。
const presetVoiceCache = new Map();

const blobToDataURL = (blob) =>
  new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(reader.result);
    reader.onerror = reject;
    reader.readAsDataURL(blob);
  });

// 解析预置音色 id → base64 data-url(浏览器 HTTP 缓存 + 内存缓存,成本可忽略)。
const resolvePresetVoice = async (presetId) => {
  if (presetVoiceCache.has(presetId)) return presetVoiceCache.get(presetId);
  const preset = PRESET_VOICES.find((v) => v.id === presetId);
  if (!preset) throw new Error(`unknown preset voice: ${presetId}`);
  const resp = await fetch(preset.url);
  if (!resp.ok) throw new Error(`fetch preset voice failed: ${resp.status}`);
  const dataURL = await blobToDataURL(await resp.blob());
  presetVoiceCache.set(presetId, dataURL);
  return dataURL;
};

const extractApiErrMsg = (error, fallback) => {
  const d = error?.response?.data || {};
  return d.error?.message || d.message || error?.message || fallback;
};

// 会话内需持久化/回填的全部参数字段(随 mode 使用其子集)。
const PARAM_FIELDS = [
  'group',
  'model',
  // emotion(情感合成)
  'voicePreset',
  'voiceData',
  'voiceName',
  'emotionAudioData',
  'emotionAudioName',
  'emotion',
  'emoWeight',
  // Omni 上传参考音(clone/dialect/dialogue)
  'refAudioData',
  'refAudioName',
  'refAudio2Data',
  'refAudio2Name',
  // 语音合成音色来源 toggle(upload | preset)
  'voiceSource',
  // Omni 标量
  'speaker',
  'language',
  'instructions',
  'refText',
  'xVectorOnlyMode',
];

const pickParams = (src) => {
  const out = {};
  PARAM_FIELDS.forEach((f) => {
    out[f] = src[f];
  });
  return out;
};

// mode 参数化:emotion(IndexTTS-2)+ clone/preset/dialect/dialogue/design(vLLM-Omni)。
export const useAudioGeneration = (mode = 'emotion') => {
  const { t } = useTranslation();
  const [statusState] = useContext(StatusContext);
  const [userState] = useContext(UserContext);

  const modeDef = AUDIO_MODES[mode] || AUDIO_MODES.emotion;
  const {
    capability,
    engine,
    needsVoice,
    needsEmotion,
    needsVoiceSource,
    needsDualRef,
    needsLanguage,
    needsInstructions,
    instructionsRequired,
  } = modeDef;
  const storageKey = audioHistoryStorageKey(mode);

  const [inputs, setInputs] = useState({
    group: '',
    model: '',
    // emotion(情感合成):参考音色 + 情感参考音 + 情感预设
    voicePreset: PRESET_VOICES[0]?.id || '', // 预置音色 id 或 VOICE_UPLOAD_VALUE
    voiceData: '', // 上传的参考音(base64 data-url)
    voiceName: '',
    emotionAudioData: '', // 可选情感参考音(base64 data-url)→ metadata.emotion_audio
    emotionAudioName: '',
    emotion: '', // 情感预设值;'' = 跟随音色
    emoWeight: AUDIO_DEFAULT_EMO_WEIGHT,
    // Omni 上传参考音(base64 data-url)+ 文件名
    refAudioData: '', // clone 克隆源 / dialect 参考音 / dialogue 说话人1 → metadata.ref_audio
    refAudioName: '',
    refAudio2Data: '', // dialogue 说话人2 → metadata.ref_audio_2
    refAudio2Name: '',
    // 语音合成音色来源:上传克隆(默认)| 预设音色。
    voiceSource: AUDIO_DEFAULT_VOICE_SOURCE,
    // Omni 标量参数
    speaker: AUDIO_DEFAULT_SPEAKER, // 语音合成(预设音色)→ metadata.speaker
    language: AUDIO_DEFAULT_LANGUAGE, // 语音合成语言下拉 → metadata.language
    instructions: '', // design(必填)→ metadata.instructions
    refText: '', // 语音合成(上传克隆)参考文本 → metadata.ref_text(未开仅音色向量时必填)
    xVectorOnlyMode: false, // 语音合成(上传克隆):仅用音色向量克隆、免参考文本 → metadata.x_vector_only_mode
  });
  const [groups, setGroups] = useState([]);
  const [models, setModels] = useState([]);
  const [modelGroupsMap, setModelGroupsMap] = useState(new Map());

  const initialConvsRef = useRef(null);
  const [conversations, setConversations] = useState(() => {
    const raw = loadConversations(storageKey);
    const stripped = stripUnresolvedMediaRefs(raw, AUDIO_MEDIA_SCHEMA);
    initialConvsRef.current = { raw, stripped };
    return stripped;
  });
  const [currentConvId, setCurrentConvId] = useState(null);
  const [generating, setGenerating] = useState(false);

  const messages = useMemo(() => {
    const conv = conversations.find((c) => c.id === currentConvId);
    return conv ? conv.messages : [];
  }, [conversations, currentConvId]);

  // 对话内已生成过 → 参数锁定(同视频):模型/音色/参数均不可改,直到新对话。
  const locked = currentConvId !== null;

  // 语音合成的音色来源 toggle 决定运行时有效标志:上传克隆 → ref_audio(必填)+ 可选
  // ref_text;预设音色 → speaker(标量)。非 synthesis 玩法沿用各自 modeDef(全 false)。
  const isPreset =
    needsVoiceSource && inputs.voiceSource === AUDIO_VOICE_SOURCE_PRESET;
  const isUploadSource =
    needsVoiceSource && inputs.voiceSource === AUDIO_VOICE_SOURCE_UPLOAD;
  const needsRefAudio = isUploadSource;
  const refAudioRequired = isUploadSource;
  const needsRefText = isUploadSource;
  const needsSpeaker = isPreset;

  const conversationsRef = useRef(conversations);
  conversationsRef.current = conversations;
  const lockedRef = useRef(locked);
  lockedRef.current = locked;
  const groupRef = useRef(inputs.group);
  groupRef.current = inputs.group;
  const activePollRef = useRef(null);

  // mount 后从 IDB 还原参考音,按初始对象引用逐条合并(不整体覆盖,见设计 §4.3)。
  useEffect(() => {
    let canceled = false;
    const init = initialConvsRef.current;
    if (!init || !(init.raw || []).length) return;
    (async () => {
      const hydrated = await hydrateConversationsFromStorage(
        init.raw,
        AUDIO_MEDIA_SCHEMA,
      );
      if (canceled) return;
      const hydratedById = new Map(hydrated.map((c) => [c.id, c]));
      const initialSet = new Set(init.stripped);
      const mediaFields = [
        ...AUDIO_MEDIA_SCHEMA.convArrayFields,
        ...AUDIO_MEDIA_SCHEMA.convStringFields,
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

  // 一键示例:把示例的标量参数(params)与文件(files:字段→素材 URL)一次性写入 inputs。
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

  // 语音模型集合 = 「语音模型配置」里声明、且能力含当前 tab 能力的模型。
  const modelConfig = useMemo(
    () => parseAudioModelConfig(statusState?.status?.AudioModelConfig),
    [statusState?.status?.AudioModelConfig],
  );

  const audioModelSet = useMemo(
    () => getAudioModelSet(modelConfig, capability),
    [modelConfig, capability],
  );

  // 当前模型的字数上限(0=不限制)。
  const maxChars = useMemo(
    () => getMaxCharsForModel(modelConfig, inputs.model),
    [modelConfig, inputs.model],
  );
  // 当前模型的参考音大小上限(MB)。
  const refAudioMaxMB = useMemo(
    () => getRefAudioMaxMBForModel(modelConfig, inputs.model),
    [modelConfig, inputs.model],
  );

  const audioGroups = useMemo(() => {
    const set = new Set();
    audioModelSet.forEach((model) => {
      (modelGroupsMap.get(model) || []).forEach((g) => set.add(g));
    });
    return set;
  }, [audioModelSet, modelGroupsMap]);

  const loadPricing = useCallback(async () => {
    try {
      const payload = await cachedGet(AUDIO_API_ENDPOINTS.PRICING, {
        config: { skipErrorHandler: true },
      });
      const { success, data } = payload || {};
      if (!success || !Array.isArray(data)) return;
      const groupsMap = new Map();
      data.forEach((item) => {
        if (!item || !item.model_name) return;
        groupsMap.set(item.model_name, item.enable_groups || []);
      });
      setModelGroupsMap(groupsMap);
    } catch (e) {
      // 留空:分组不按 enable_groups 收窄
    }
  }, []);

  const loadGroups = useCallback(async () => {
    try {
      const { success, data } = await cachedGet(
        AUDIO_API_ENDPOINTS.USER_GROUPS,
      );
      if (!success) return;
      const userGroup =
        userState?.user?.group ||
        JSON.parse(localStorage.getItem('user') || '{}')?.group;
      let groupOptions = processGroupsData(data, userGroup);
      const allowAllGroups = audioGroups.has('all');
      if (audioGroups.size > 0 && !allowAllGroups) {
        groupOptions = groupOptions.filter(
          (g) => audioGroups.has(g.value) || g.value === 'auto',
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
  }, [userState, audioGroups, t]);

  const loadModels = useCallback(async () => {
    const requestedGroup = inputs.group;
    try {
      const { success, data } = await getUserModelsCached(requestedGroup);
      if (!success) return;
      // 分组在等待响应期间已切换:过期响应直接丢弃,否则旧分组的空结果会覆盖正确列表。
      if (requestedGroup !== groupRef.current) return;
      let list = Array.isArray(data) ? data : [];
      list = list.filter((m) => audioModelSet.has(m));
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
  }, [inputs.group, inputs.model, audioModelSet, t]);

  useEffect(() => {
    if (userState?.user) loadPricing();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [userState?.user]);
  useEffect(() => {
    if (userState?.user) loadGroups();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [userState?.user, audioGroups]);
  useEffect(() => {
    if (userState?.user) loadModels();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [userState?.user, inputs.group, audioModelSet]);

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
  const turnLimitReached = turnsUsed >= AUDIO_CONV_TURN_LIMIT;

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
          `${AUDIO_API_ENDPOINTS.VIDEO_FETCH}/${encodeURIComponent(taskId)}`,
          { skipErrorHandler: true },
        );
        const data = res.data || {};
        const inner = data.data || {};
        const status = normalizeAudioStatus(data.status || inner.status);
        const progress = parseProgress(
          data.progress != null ? data.progress : inner.progress,
        );

        if (status === AUDIO_STATUS.COMPLETED) {
          patchConvMessage(convId, msgId, {
            status: AUDIO_STATUS.COMPLETED,
            progress: 100,
            audioUrl: buildAudioContentUrl(taskId),
          });
          finishPoll();
          return;
        }
        if (status === AUDIO_STATUS.FAILED) {
          const msg =
            data.error?.message ||
            inner.error?.message ||
            inner.fail_reason ||
            data.fail_reason ||
            t('语音合成失败');
          patchConvMessage(convId, msgId, {
            status: AUDIO_STATUS.FAILED,
            error: msg,
          });
          showError(msg);
          finishPoll();
          return;
        }
        patchConvMessage(convId, msgId, {
          status: status || AUDIO_STATUS.IN_PROGRESS,
          ...(progress !== undefined ? { progress } : {}),
        });
        if (count >= AUDIO_POLL_MAX_TIMES) {
          // 客户端轮询超时:任务可能仍在后端进行,标记「继续获取」可续查,不判失败。
          patchConvMessage(convId, msgId, { pollTimedOut: true });
          finishPoll();
          return;
        }
      } catch (e) {
        if (count >= AUDIO_POLL_MAX_TIMES) {
          patchConvMessage(convId, msgId, { pollTimedOut: true });
          finishPoll();
          return;
        }
      }
      const cur = activePollRef.current;
      if (!cur || cur.canceled || cur.taskId !== taskId) return;
      cur.timer = setTimeout(
        () => pollOnce(convId, msgId, taskId, count + 1),
        AUDIO_POLL_INTERVAL_MS,
      );
    },
    [patchConvMessage, finishPoll, t],
  );

  // 为进行中的任务(重新)启动轮询:刷新/切页面回来、点历史时用。
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
        AUDIO_POLL_INTERVAL_MS,
      );
    },
    [pollOnce, patchConvMessage],
  );

  // 挂载后为最近一个仍在进行中的任务恢复轮询(切页面/刷新后任务在后台继续)。
  useEffect(() => {
    if (!userState?.user || activePollRef.current) return;
    let best = null;
    conversationsRef.current.forEach((conv) => {
      (conv.messages || []).forEach((m) => {
        if (
          m.role === 'assistant' &&
          m.taskId &&
          (m.status === AUDIO_STATUS.QUEUED ||
            m.status === AUDIO_STATUS.IN_PROGRESS)
        ) {
          const ts = Number(String(m.id).split('-')[1]) || 0;
          if (!best || ts > best.ts) {
            best = { convId: conv.id, msgId: m.id, taskId: m.taskId, ts };
          }
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

  const generate = useCallback(
    async (prompt) => {
      const text = (prompt || '').trim();
      if (!text || generating) return;

      // 字数上限(0=不限制):按当前模型配置就地拦截,不占用后端提交。
      const charLimit = getMaxCharsForModel(modelConfig, inputs.model);
      if (charLimit > 0 && text.length > charLimit) {
        showError(
          t('合成文本超过字数上限 {{max}} 字(当前 {{cur}} 字)', {
            max: charLimit,
            cur: text.length,
          }),
        );
        return;
      }

      let convId = currentConvId;
      let params;
      if (convId == null) {
        if (!inputs.model) {
          showError(t('请先选择一个语音模型'));
          return;
        }
        // emotion:必须选预置音色或上传参考音。
        if (needsVoice) {
          if (inputs.voicePreset === VOICE_UPLOAD_VALUE && !inputs.voiceData) {
            showError(t('请先上传参考音频'));
            return;
          }
          if (!inputs.voicePreset) {
            showError(t('请先选择参考音色'));
            return;
          }
        }
        // clone:克隆源必填。
        if (
          needsRefAudio &&
          refAudioRequired &&
          !(inputs.refAudioData || '').startsWith('data:')
        ) {
          showError(t('请先上传参考音(克隆源)'));
          return;
        }
        // clone:未开启「仅用音色向量」时参考文本必填(引擎克隆需参考音转录,
        // 否则上游报 "requires non-empty 'ref_text'")。
        if (
          needsRefText &&
          !inputs.xVectorOnlyMode &&
          !(inputs.refText || '').trim()
        ) {
          showError(t('请填写参考文本(参考音的文字稿),或开启「仅用音色向量」'));
          return;
        }
        // dialogue:两个说话人参考音均必填。
        if (
          needsDualRef &&
          (!(inputs.refAudioData || '').startsWith('data:') ||
            !(inputs.refAudio2Data || '').startsWith('data:'))
        ) {
          showError(t('请先上传说话人1与说话人2的参考音'));
          return;
        }
        // design:声线描述必填。
        if (instructionsRequired && !(inputs.instructions || '').trim()) {
          showError(t('请先填写声线描述'));
          return;
        }
        convId = genId();
        params = pickParams(inputs);
      } else {
        const conv = conversationsRef.current.find((c) => c.id === convId);
        const used = conv
          ? conv.messages.filter((m) => m.role === 'user').length
          : 0;
        if (used >= AUDIO_CONV_TURN_LIMIT) {
          showError(
            t('本轮对话生成次数已达上限（{{count}} 次），请开启新对话', {
              count: AUDIO_CONV_TURN_LIMIT,
            }),
          );
          return;
        }
        params = conv ? pickParams(conv) : pickParams(inputs);
      }

      // 解析各玩法所需的参考音 → base64 data-url。上传项刷新后已从 localStorage 剥离
      // → 提示重开对话重传(同视频帧图语义)。预置音色按 id 取(刷新后仍可续问)。
      let voiceDataURL = '';
      let emotionAudioURL = '';
      let refAudioURL = '';
      let refAudio2URL = '';
      try {
        if (needsVoice) {
          if (params.voicePreset === VOICE_UPLOAD_VALUE) {
            voiceDataURL = params.voiceData || '';
            if (!voiceDataURL.startsWith('data:')) {
              showError(t('参考音频已失效,请开启新对话并重新上传'));
              return;
            }
          } else {
            voiceDataURL = await resolvePresetVoice(params.voicePreset);
          }
          // 可选情感参考音。
          if ((params.emotionAudioData || '').startsWith('data:')) {
            emotionAudioURL = params.emotionAudioData;
          }
        }
        if (needsRefAudio || needsDualRef) {
          refAudioURL = params.refAudioData || '';
          if (refAudioRequired || needsDualRef) {
            if (!refAudioURL.startsWith('data:')) {
              showError(t('参考音已失效,请开启新对话并重新上传'));
              return;
            }
          } else if (!refAudioURL.startsWith('data:')) {
            refAudioURL = ''; // 可选参考音缺失 → 不下发
          }
        }
        if (needsDualRef) {
          refAudio2URL = params.refAudio2Data || '';
          if (!refAudio2URL.startsWith('data:')) {
            showError(t('参考音已失效,请开启新对话并重新上传'));
            return;
          }
        }
      } catch (e) {
        showError(t('加载参考音失败,请重试'));
        return;
      }

      const reqId = genId();
      const now = new Date().toISOString();
      const userMsg = { id: `${reqId}-u`, role: 'user', content: text };
      const asstId = `${reqId}-a`;
      const asstMsg = {
        id: asstId,
        role: 'assistant',
        status: AUDIO_STATUS.QUEUED,
        model: params.model,
        prompt: text,
        progress: 0,
        taskId: null,
        audioUrl: null,
      };

      setConversations((prev) => {
        const idx = prev.findIndex((c) => c.id === convId);
        let next;
        if (idx === -1) {
          next = [
            {
              id: convId,
              ...params,
              title: text,
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
        next = next.slice(0, AUDIO_HISTORY_LIMIT);
        persistConversations(storageKey, next);
        return next;
      });
      if (currentConvId == null) setCurrentConvId(convId);
      setGenerating(true);

      try {
        // gpustackplus 门面契约:文本走 prompt;task_type + 参考音/标量参数经 metadata 透传。
        // adaptor 按模型分派:IndexTTS 物化 metadata.voice→spk_audio_path;vLLM-Omni 物化
        // metadata.ref_audio(/_2)→ref_audio_path,speaker/language/instructions 标量透传。
        const metadata = { task_type: 'tts' };

        if (engine === 'indextts') {
          // ── 情感合成(IndexTTS-2):参考音色 + 情感参考音 + emo_vector/emo_alpha ──
          metadata.voice = voiceDataURL;
          if (emotionAudioURL) metadata.emotion_audio = emotionAudioURL;
          const vec = emotionToVector(params.emotion, params.emoWeight);
          if (vec) {
            metadata.emo_vector = vec;
            metadata.emo_alpha =
              typeof params.emoWeight === 'number'
                ? params.emoWeight
                : AUDIO_DEFAULT_EMO_WEIGHT;
          }
        } else {
          // ── vLLM-Omni 家族:语音合成 / 双人对话 / 声音设计 ──
          // 语音合成的「音色来源」互斥:预设音色发 speaker(不发 ref_audio);上传克隆发
          // ref_audio(+ 可选 ref_text)。按 params.voiceSource 判定,与面板 toggle 对齐。
          const presetSource =
            needsVoiceSource &&
            params.voiceSource === AUDIO_VOICE_SOURCE_PRESET;
          if (needsVoiceSource && presetSource) {
            // 预设音色(标量透传,门面不物化)。
            const sp = (params.speaker || '').trim();
            if (sp) metadata.speaker = sp;
          } else {
            // 参考音(上传克隆源 / dialogue 说话人1;dialogue 走 needsDualRef)。
            if ((needsRefAudio || needsDualRef) && refAudioURL) {
              metadata.ref_audio = refAudioURL;
            }
            // 双人对话第二说话人。
            if (needsDualRef && refAudio2URL) {
              metadata.ref_audio_2 = refAudio2URL;
            }
            // 克隆参考文本 / 仅音色向量(仅上传克隆时,标量透传)。开启「仅用音色向量」→
            // 发 x_vector_only_mode=true(引擎跳过参考文本 ICL);否则发参考文本(必填,已在
            // 提交前校验非空)。
            if (needsRefText) {
              if (params.xVectorOnlyMode) {
                metadata.x_vector_only_mode = true;
              } else {
                const rt = (params.refText || '').trim();
                if (rt) metadata.ref_text = rt;
              }
            }
          }
          // 语言/方言(标量透传;两种音色来源都可带)。
          if (needsLanguage) {
            const lang = (params.language || '').trim();
            if (lang) metadata.language = lang;
          }
          // 声线描述(design 必填,标量透传)。
          if (needsInstructions) {
            const ins = (params.instructions || '').trim();
            if (ins) metadata.instructions = ins;
          }
        }

        const body = {
          model: params.model,
          group: params.group,
          prompt: text,
          metadata,
        };
        const res = await API.post(
          AUDIO_API_ENDPOINTS.VIDEO_GENERATIONS,
          body,
          {
            skipErrorHandler: true,
          },
        );
        const data = res.data || {};
        const inner = data.data || {};
        const taskId = data.id || data.task_id || inner.task_id || inner.id;
        if (!taskId) throw new Error(t('提交语音任务失败'));
        const status = normalizeAudioStatus(data.status || inner.status);
        if (status === AUDIO_STATUS.FAILED) {
          const msg =
            data.error?.message ||
            inner.error?.message ||
            inner.fail_reason ||
            data.fail_reason ||
            t('语音合成失败');
          patchConvMessage(convId, asstId, {
            status: AUDIO_STATUS.FAILED,
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
          AUDIO_POLL_INTERVAL_MS,
        );
      } catch (error) {
        const msg = extractApiErrMsg(error, t('语音合成失败'));
        patchConvMessage(convId, asstId, {
          status: AUDIO_STATUS.FAILED,
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
      needsVoice,
      needsVoiceSource,
      needsRefAudio,
      refAudioRequired,
      needsDualRef,
      needsSpeaker,
      needsLanguage,
      needsRefText,
      needsInstructions,
      instructionsRequired,
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
      // 若该会话最后一个任务仍在进行中,恢复轮询(切页面后任务在后台继续)。
      const assts = (conv.messages || []).filter((m) => m.role === 'assistant');
      const last = assts[assts.length - 1];
      if (
        last?.taskId &&
        (last.status === AUDIO_STATUS.QUEUED ||
          last.status === AUDIO_STATUS.IN_PROGRESS)
      ) {
        resumePoll(conv.id, last.id, last.taskId);
      }
    },
    [resumePoll],
  );

  // 卸载时清理轮询定时器(任务本身在服务端继续;回来后挂载恢复逻辑接管)。
  useEffect(() => {
    return () => {
      if (activePollRef.current?.timer)
        clearTimeout(activePollRef.current.timer);
      activePollRef.current = null;
    };
  }, []);

  // 缺必填参考音/声线描述 → 发送置灰(同视频缺帧图语义)。
  const missingRequiredVoice =
    !locked &&
    ((needsVoice &&
      (!inputs.voicePreset ||
        (inputs.voicePreset === VOICE_UPLOAD_VALUE &&
          !(inputs.voiceData || '').startsWith('data:')))) ||
      (needsRefAudio &&
        refAudioRequired &&
        !(inputs.refAudioData || '').startsWith('data:')) ||
      (needsRefText &&
        !inputs.xVectorOnlyMode &&
        !(inputs.refText || '').trim()) ||
      (needsDualRef &&
        (!(inputs.refAudioData || '').startsWith('data:') ||
          !(inputs.refAudio2Data || '').startsWith('data:'))) ||
      (instructionsRequired && !(inputs.instructions || '').trim()));

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
    missingRequiredVoice,
    engine,
    needsVoice,
    needsEmotion,
    needsVoiceSource,
    needsRefAudio,
    refAudioRequired,
    needsDualRef,
    needsSpeaker,
    needsLanguage,
    needsRefText,
    needsInstructions,
    maxChars,
    refAudioMaxMB,
    generate,
    regenerate,
    refetch,
    newConversation,
    clearHistory,
    deleteHistoryItem,
    openHistoryItem,
  };
};
