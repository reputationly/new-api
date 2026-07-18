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
import {
  API,
  showError,
  processGroupsData,
  processModelsData,
  getUserModelsCached,
  cachedGet,
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
  musicHistoryStorageKey,
  normalizeMusicStatus,
  parseProgress,
  buildMusicContentUrl,
  parseMusicModelConfig,
  getMusicModelSet,
  getMaxCharsForModel,
  getRefAudioMaxMBForModel,
} from '../../constants/musicPlayground.constants';

// 文生音乐体验区 hook,镜像 useAudioGeneration:同一异步任务门面(/pg/videos)、
// 同一轮询/历史/锁定模式;按 mode(t2m/cover/repaint)映射 task_type,输入换成
// 描述 caption(输入框 prompt)+ 可选歌词/时长 +(cover/repaint)驱动音频。

// 上传的驱动音频是 base64 data-url,以 Blob 存 IndexedDB,localStorage 只留短引用;
// 刷新后可恢复、可续问。t2m 无音频,不受影响。
const MUSIC_MEDIA_SCHEMA = {
  convArrayFields: [],
  convStringFields: ['audioData'],
  msgArrayFields: [],
  // 生成的音频结果:抓 Blob 缓存进 IDB,同视频/语音。
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

// mode 参数化:t2m(纯文本)/cover(参考音频)/repaint(源音频)。
export const useMusicGeneration = (mode = 't2m') => {
  const { t } = useTranslation();
  const [statusState] = useContext(StatusContext);
  const [userState] = useContext(UserContext);

  const modeDef = MUSIC_MODES[mode] || MUSIC_MODES.t2m;
  const { taskType, capability, needsAudio, audioMetaKey } = modeDef;
  const storageKey = musicHistoryStorageKey(mode);

  const [inputs, setInputs] = useState({
    group: '',
    model: '',
    lyrics: '', // 可选歌词(metadata.lyrics);留空则由引擎按 caption 自动生成
    duration: MUSIC_DEFAULT_DURATION, // 秒;'' = 引擎默认
    audioData: '', // 上传的驱动音频(base64 data-url);仅 cover/repaint 使用
    audioName: '', // 上传文件名(展示用)
    // 高级参数:留空即不下发,走引擎默认(见 metadata 组装)。
    seed: '', // 指定后可复现;空 = 随机
    bpm: '', // 速度;空 = 自动
    vocalLanguage: '', // 演唱语言;空 = 自动
    guidanceScale: '', // 贴合描述程度;空 = 引擎默认 7.0
    inferenceSteps: '', // 采样步数;空 = 引擎默认 8
  });
  const [groups, setGroups] = useState([]);
  const [models, setModels] = useState([]);
  const [modelGroupsMap, setModelGroupsMap] = useState(new Map());

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

  // 对话内已生成过 → 参数锁定(同语音):模型/歌词/时长/音频均不可改,直到新对话。
  const locked = currentConvId !== null;

  const conversationsRef = useRef(conversations);
  conversationsRef.current = conversations;
  const lockedRef = useRef(locked);
  lockedRef.current = locked;
  const groupRef = useRef(inputs.group);
  groupRef.current = inputs.group;
  const activePollRef = useRef(null);

  // mount 后从 IDB 还原驱动音频,按初始对象引用逐条合并(不整体覆盖)。
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

  // 音乐模型集合 = 「音乐模型配置」里声明、且能力含当前 tab 能力(文生音乐/音乐改编/
  // 音乐重绘)的模型。
  const modelConfig = useMemo(
    () => parseMusicModelConfig(statusState?.status?.MusicModelConfig),
    [statusState?.status?.MusicModelConfig],
  );

  const musicModelSet = useMemo(
    () => getMusicModelSet(modelConfig, capability),
    [modelConfig, capability],
  );

  // 当前模型的字数上限(0=不限制)。
  const maxChars = useMemo(
    () => getMaxCharsForModel(modelConfig, inputs.model),
    [modelConfig, inputs.model],
  );
  // 当前模型的驱动音频大小上限(MB)。
  const refAudioMaxMB = useMemo(
    () => getRefAudioMaxMBForModel(modelConfig, inputs.model),
    [modelConfig, inputs.model],
  );

  const musicGroups = useMemo(() => {
    const set = new Set();
    musicModelSet.forEach((model) => {
      (modelGroupsMap.get(model) || []).forEach((g) => set.add(g));
    });
    return set;
  }, [musicModelSet, modelGroupsMap]);

  const loadPricing = useCallback(async () => {
    try {
      const payload = await cachedGet(MUSIC_API_ENDPOINTS.PRICING, {
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
            t('音乐生成失败');
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
        if (
          m.role === 'assistant' &&
          m.taskId &&
          (m.status === MUSIC_STATUS.QUEUED ||
            m.status === MUSIC_STATUS.IN_PROGRESS)
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

      // 字数上限(0=不限制):按当前模型配置就地拦截。
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
        convId = genId();
        params = {
          group: inputs.group,
          model: inputs.model,
          lyrics: inputs.lyrics,
          duration: inputs.duration,
          audioData: inputs.audioData,
          audioName: inputs.audioName,
          seed: inputs.seed,
          bpm: inputs.bpm,
          vocalLanguage: inputs.vocalLanguage,
          guidanceScale: inputs.guidanceScale,
          inferenceSteps: inputs.inferenceSteps,
        };
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
        params = conv
          ? {
              group: conv.group,
              model: conv.model,
              lyrics: conv.lyrics,
              duration: conv.duration,
              audioData: conv.audioData,
              audioName: conv.audioName,
              seed: conv.seed,
              bpm: conv.bpm,
              vocalLanguage: conv.vocalLanguage,
              guidanceScale: conv.guidanceScale,
              inferenceSteps: conv.inferenceSteps,
            }
          : {
              group: inputs.group,
              model: inputs.model,
              lyrics: inputs.lyrics,
              duration: inputs.duration,
              audioData: inputs.audioData,
              audioName: inputs.audioName,
              seed: inputs.seed,
              bpm: inputs.bpm,
              vocalLanguage: inputs.vocalLanguage,
              guidanceScale: inputs.guidanceScale,
              inferenceSteps: inputs.inferenceSteps,
            };
      }

      // cover/repaint 的驱动音频:刷新后 localStorage 已剥离 → 提示重开对话重传。
      let audioDataURL = '';
      if (needsAudio) {
        audioDataURL = params.audioData || '';
        if (!audioDataURL.startsWith('data:')) {
          showError(t('驱动音频已失效,请开启新对话并重新上传'));
          return;
        }
      }

      const reqId = genId();
      const now = new Date().toISOString();
      const userMsg = { id: `${reqId}-u`, role: 'user', content: text };
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
      };

      setConversations((prev) => {
        const idx = prev.findIndex((c) => c.id === convId);
        let next;
        if (idx === -1) {
          next = [
            {
              id: convId,
              group: params.group,
              model: params.model,
              lyrics: params.lyrics,
              duration: params.duration,
              audioData: params.audioData,
              audioName: params.audioName,
              seed: params.seed,
              bpm: params.bpm,
              vocalLanguage: params.vocalLanguage,
              guidanceScale: params.guidanceScale,
              inferenceSteps: params.inferenceSteps,
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
        next = next.slice(0, MUSIC_HISTORY_LIMIT);
        persistConversations(storageKey, next);
        return next;
      });
      if (currentConvId == null) setCurrentConvId(convId);
      setGenerating(true);

      try {
        // gpustackplus 门面契约:task_type/歌词/时长/驱动音频经 metadata 透传
        // (adaptor 把音频物化 NFS → input_refs → 引擎)。
        const metadata = { task_type: taskType };
        const lyrics = (params.lyrics || '').trim();
        const dur = parseFloat(params.duration);
        if (Number.isFinite(dur) && dur > 0) metadata.audio_duration = dur;
        if (needsAudio && audioMetaKey) metadata[audioMetaKey] = audioDataURL;

        // t2m 且未填歌词 → 额外开启 sample 模式:引擎按描述用 LM 自动生成 caption+歌词,
        // 产出整首带词歌(官方 examples/simple_mode 用法)。create_sample 的触发条件是
        // sample_mode 或非空 sample_query,与 prompt 是否为空无关,所以 prompt 仍保持=描述
        // 文本 —— 既满足门面「prompt 必填(除 sr)」校验(见 relay/common/relay_utils.go),
        // 也让不认 sample_mode 的路径能靠 prompt + LM 补词兜底。
        // 其余情况(已填歌词 或 cover/repaint):描述作为 caption(prompt),歌词直接透传。
        const promptField = text;
        if (taskType === 't2m' && !lyrics) {
          metadata.sample_mode = true;
          metadata.sample_query = text;
        } else if (lyrics) {
          metadata.lyrics = lyrics;
        }

        // 高级参数:留空即不下发,走引擎默认。字段名对齐 GenerateMusicRequest。
        const seedStr = String(params.seed ?? '').trim();
        if (seedStr !== '') {
          metadata.seed = seedStr;
          metadata.use_random_seed = false;
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
        if (!taskId) throw new Error(t('提交音乐任务失败'));
        const status = normalizeMusicStatus(data.status || inner.status);
        if (status === MUSIC_STATUS.FAILED) {
          const msg =
            data.error?.message ||
            inner.error?.message ||
            inner.fail_reason ||
            data.fail_reason ||
            t('音乐生成失败');
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
        const msg = extractApiErrMsg(error, t('音乐生成失败'));
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
      needsAudio,
      audioMetaKey,
      taskType,
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
      setInputs((prev) => ({
        ...prev,
        group: conv.group != null ? conv.group : prev.group,
        model: conv.model != null ? conv.model : prev.model,
        lyrics: conv.lyrics != null ? conv.lyrics : prev.lyrics,
        duration: conv.duration != null ? conv.duration : prev.duration,
        audioData: conv.audioData != null ? conv.audioData : prev.audioData,
        audioName: conv.audioName != null ? conv.audioName : prev.audioName,
        seed: conv.seed != null ? conv.seed : prev.seed,
        bpm: conv.bpm != null ? conv.bpm : prev.bpm,
        vocalLanguage:
          conv.vocalLanguage != null ? conv.vocalLanguage : prev.vocalLanguage,
        guidanceScale:
          conv.guidanceScale != null ? conv.guidanceScale : prev.guidanceScale,
        inferenceSteps:
          conv.inferenceSteps != null
            ? conv.inferenceSteps
            : prev.inferenceSteps,
      }));
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

  // cover/repaint 缺驱动音频 → 发送置灰。t2m 无此约束。
  const missingRequiredAudio =
    !locked && needsAudio && !(inputs.audioData || '').startsWith('data:');

  return {
    inputs,
    handleInputChange,
    groups,
    models,
    messages,
    conversations,
    generating,
    locked,
    turnLimitReached,
    missingRequiredAudio,
    needsAudio,
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
