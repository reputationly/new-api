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
  API,
  showError,
  processGroupsData,
  processModelsData,
  getUserModelsCached,
  cachedGet,
} from '../../helpers';
import {
  parseVideoModelConfig,
} from '../../constants/videoPlayground.constants';
import {
  AUDIO_API_ENDPOINTS,
  AUDIO_STATUS,
  AUDIO_PAGE_CAPABILITY,
  AUDIO_HISTORY_STORAGE_KEY,
  AUDIO_HISTORY_LIMIT,
  AUDIO_CONV_TURN_LIMIT,
  AUDIO_POLL_INTERVAL_MS,
  AUDIO_POLL_MAX_TIMES,
  AUDIO_DEFAULT_EMO_WEIGHT,
  PRESET_VOICES,
  VOICE_UPLOAD_VALUE,
  emotionToVector,
  normalizeAudioStatus,
  parseProgress,
  buildAudioContentUrl,
} from '../../constants/audioPlayground.constants';

// 语音合成体验区 hook,镜像 useVideoGeneration:同一异步任务门面(/pg/videos,
// task_type=tts)、同一轮询/历史/锁定模式;参数换成 参考音色 + 情感预设。

const loadConversations = () => {
  try {
    const raw = localStorage.getItem(AUDIO_HISTORY_STORAGE_KEY);
    const parsed = raw ? JSON.parse(raw) : [];
    return Array.isArray(parsed) ? parsed : [];
  } catch (e) {
    return [];
  }
};

// 上传的参考音是 base64 data-url,落 localStorage 会撑爆配额(同视频帧图);
// 落盘前剥掉 data: 音频。预置音色只存 id,刷新后续问可按 id 重取,不受影响。
const stripVoiceForPersist = (list) =>
  list.map((conv) =>
    String(conv.voiceData || '').startsWith('data:')
      ? { ...conv, voiceData: '' }
      : conv,
  );

const persistConversations = (list) => {
  try {
    localStorage.setItem(
      AUDIO_HISTORY_STORAGE_KEY,
      JSON.stringify(stripVoiceForPersist(list.slice(0, AUDIO_HISTORY_LIMIT))),
    );
  } catch (e) {
    // ignore quota errors
  }
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

export const useAudioGeneration = () => {
  const { t } = useTranslation();
  const [statusState] = useContext(StatusContext);
  const [userState] = useContext(UserContext);

  const [inputs, setInputs] = useState({
    group: '',
    model: '',
    voicePreset: PRESET_VOICES[0]?.id || '', // 预置音色 id 或 VOICE_UPLOAD_VALUE
    voiceData: '', // 上传的参考音(base64 data-url);仅 voicePreset===上传 时使用
    voiceName: '', // 上传文件名(展示用)
    emotion: '', // 情感预设值;'' = 跟随音色
    emoWeight: AUDIO_DEFAULT_EMO_WEIGHT,
  });
  const [groups, setGroups] = useState([]);
  const [models, setModels] = useState([]);
  const [modelGroupsMap, setModelGroupsMap] = useState(new Map());

  const [conversations, setConversations] = useState(() => loadConversations());
  const [currentConvId, setCurrentConvId] = useState(null);
  const [generating, setGenerating] = useState(false);

  const messages = useMemo(() => {
    const conv = conversations.find((c) => c.id === currentConvId);
    return conv ? conv.messages : [];
  }, [conversations, currentConvId]);

  // 对话内已生成过 → 参数锁定(同视频):模型/音色/情感均不可改,直到新对话。
  const locked = currentConvId !== null;

  const conversationsRef = useRef(conversations);
  conversationsRef.current = conversations;
  const lockedRef = useRef(locked);
  lockedRef.current = locked;
  const activePollRef = useRef(null);

  const handleInputChange = useCallback((key, value) => {
    if (lockedRef.current) return;
    setInputs((prev) => ({ ...prev, [key]: value }));
  }, []);

  // 音频模型集合 = 运营后台「视频模型配置」里声明、且能力含「语音合成」的模型。
  // 复用同一份配置(引擎侧同一门面),只是能力标签不同。
  const modelConfig = useMemo(
    () => parseVideoModelConfig(statusState?.status?.VideoModelConfig),
    [statusState?.status?.VideoModelConfig],
  );

  const audioModelSet = useMemo(() => {
    const set = new Set();
    Object.entries(modelConfig.models || {}).forEach(([model, cfg]) => {
      const caps = Array.isArray(cfg?.capabilities) ? cfg.capabilities : [];
      if (caps.includes(AUDIO_PAGE_CAPABILITY)) set.add(model);
    });
    return set;
  }, [modelConfig]);

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
      const { success, data } = await cachedGet(AUDIO_API_ENDPOINTS.USER_GROUPS);
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
    try {
      const { success, data } = await getUserModelsCached(inputs.group);
      if (!success) return;
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

  const patchConvMessage = useCallback((convId, msgId, patch) => {
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
      persistConversations(next);
      return next;
    });
  }, []);

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

      let convId = currentConvId;
      let params;
      if (convId == null) {
        if (!inputs.model) {
          showError(t('请先选择一个语音模型'));
          return;
        }
        if (inputs.voicePreset === VOICE_UPLOAD_VALUE && !inputs.voiceData) {
          showError(t('请先上传参考音频'));
          return;
        }
        if (!inputs.voicePreset) {
          showError(t('请先选择参考音色'));
          return;
        }
        convId = genId();
        params = {
          group: inputs.group,
          model: inputs.model,
          voicePreset: inputs.voicePreset,
          voiceData: inputs.voiceData,
          voiceName: inputs.voiceName,
          emotion: inputs.emotion,
          emoWeight: inputs.emoWeight,
        };
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
        params = conv
          ? {
              group: conv.group,
              model: conv.model,
              voicePreset: conv.voicePreset,
              voiceData: conv.voiceData,
              voiceName: conv.voiceName,
              emotion: conv.emotion,
              emoWeight: conv.emoWeight,
            }
          : {
              group: inputs.group,
              model: inputs.model,
              voicePreset: inputs.voicePreset,
              voiceData: inputs.voiceData,
              voiceName: inputs.voiceName,
              emotion: inputs.emotion,
              emoWeight: inputs.emoWeight,
            };
      }

      // 解析参考音 → base64:预置按 id 取(刷新后仍可续问);上传取会话内 data-url,
      // 刷新后已从 localStorage 剥离 → 提示重开对话重新上传(同视频帧图语义)。
      let voiceDataURL = '';
      try {
        if (params.voicePreset === VOICE_UPLOAD_VALUE) {
          voiceDataURL = params.voiceData || '';
          if (!voiceDataURL.startsWith('data:')) {
            showError(t('参考音频已失效,请开启新对话并重新上传'));
            return;
          }
        } else {
          voiceDataURL = await resolvePresetVoice(params.voicePreset);
        }
      } catch (e) {
        showError(t('加载参考音色失败,请重试'));
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
              group: params.group,
              model: params.model,
              voicePreset: params.voicePreset,
              voiceData: params.voiceData,
              voiceName: params.voiceName,
              emotion: params.emotion,
              emoWeight: params.emoWeight,
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
        persistConversations(next);
        return next;
      });
      if (currentConvId == null) setCurrentConvId(convId);
      setGenerating(true);

      try {
        // gpustackplus 门面契约:文本走 prompt;task_type/参考音/情感经 metadata 透传
        // (adaptor 把 metadata.voice 物化 NFS → input_refs → 引擎 spk_audio_path)。
        const metadata = { task_type: 'tts', voice: voiceDataURL };
        const vec = emotionToVector(params.emotion, params.emoWeight);
        if (vec) {
          metadata.emo_vector = vec;
          metadata.emo_alpha =
            typeof params.emoWeight === 'number'
              ? params.emoWeight
              : AUDIO_DEFAULT_EMO_WEIGHT;
        }
        const body = {
          model: params.model,
          group: params.group,
          prompt: text,
          metadata,
        };
        const res = await API.post(AUDIO_API_ENDPOINTS.VIDEO_GENERATIONS, body, {
          skipErrorHandler: true,
        });
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
    [currentConvId, inputs, generating, patchConvMessage, pollOnce, t],
  );

  const regenerate = useCallback((prompt) => generate(prompt), [generate]);

  const newConversation = useCallback(() => {
    setCurrentConvId(null);
  }, []);

  const clearHistory = useCallback(() => {
    if (activePollRef.current) activePollRef.current.canceled = true;
    finishPoll();
    setConversations([]);
    persistConversations([]);
    setCurrentConvId(null);
  }, [finishPoll]);

  const deleteHistoryItem = useCallback(
    (id) => {
      const active = activePollRef.current;
      if (active && active.convId === id) {
        active.canceled = true;
        finishPoll();
      }
      setConversations((prev) => {
        const next = prev.filter((c) => c.id !== id);
        persistConversations(next);
        return next;
      });
      setCurrentConvId((cur) => (cur === id ? null : cur));
    },
    [finishPoll],
  );

  const openHistoryItem = useCallback(
    (conv) => {
      setCurrentConvId(conv.id);
      setInputs((prev) => ({
        ...prev,
        group: conv.group != null ? conv.group : prev.group,
        model: conv.model != null ? conv.model : prev.model,
        voicePreset:
          conv.voicePreset != null ? conv.voicePreset : prev.voicePreset,
        voiceData: conv.voiceData != null ? conv.voiceData : prev.voiceData,
        voiceName: conv.voiceName != null ? conv.voiceName : prev.voiceName,
        emotion: conv.emotion != null ? conv.emotion : prev.emotion,
        emoWeight: conv.emoWeight != null ? conv.emoWeight : prev.emoWeight,
      }));
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

  // 未选预置音色且未上传参考音 → 发送置灰(同视频缺帧图语义)。
  const missingRequiredVoice =
    !locked &&
    (!inputs.voicePreset ||
      (inputs.voicePreset === VOICE_UPLOAD_VALUE &&
        !(inputs.voiceData || '').startsWith('data:')));

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
    missingRequiredVoice,
    generate,
    regenerate,
    refetch,
    newConversation,
    clearHistory,
    deleteHistoryItem,
    openHistoryItem,
  };
};
