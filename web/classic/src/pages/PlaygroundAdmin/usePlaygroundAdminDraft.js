import { useCallback, useContext, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { API, showError, showSuccess } from '../../helpers';
import { StatusContext } from '../../context/Status';
import {
  deriveCapabilities,
  parsePlaygroundTabConfig,
  recomputeModelLevel,
} from '../../constants/playgroundAdmin.constants';
import { parseImageSizeConfig } from '../../constants/imagePlayground.constants';
import { parseVideoModelConfig } from '../../constants/videoPlayground.constants';
import { parseAudioModelConfig } from '../../constants/audioPlayground.constants';
import { parseMusicModelConfig } from '../../constants/musicPlayground.constants';

// 体验区管理页的草稿层：一次把五份 option（四份模型配置 + 一份 tab 显示配置）读进来，
// 页面上随便改，最后按「哪几份真的动过」逐一 PUT 回去。
//
// 之所以要草稿而不是各区块自己存：新页面是 tab 中心式的，一个 tab 的一次编辑会同时
// 改到模型配置（模型进/出该 tab、该 tab 的参数）和 tab 显示配置（网页端/手机端开关、
// AI 优化提示词），拆成多个保存按钮运营就得记住点几下。
//
// 模型级平铺字段与 capabilities 都在保存时由 tabs 反推（见 recomputeModelLevel /
// deriveCapabilities），页面上不直接编辑，避免两处口径打架。

// 四份配置的形态不完全一致（图像的 default 是数组，音乐的 default 多一个 videoMaxMB），
// 这里统一成 { defaults, models } 的草稿形态，保存时再各自还原。
// defaultFields：分类默认值里允许编辑的字段；未列出的（如音乐的 videoMaxMB，其玩法
// 「视频生音」目前没有 tab）原样保留不渲染。
export const STORE_META = {
  ImageModelSizeConfig: {
    label: '图像模型',
    defaultFields: ['sizes'],
    toDraft: (raw) => {
      const p = parseImageSizeConfig(raw);
      return { defaults: { sizes: p.default }, models: p.models };
    },
    toValue: (d) => ({ default: d.defaults.sizes || [], models: d.models }),
  },
  VideoModelConfig: {
    label: '视频模型',
    defaultFields: [
      'sizes',
      'durations',
      'aspectRatios',
      'maxInputMB',
      'maxAudioSec',
    ],
    toDraft: (raw) => {
      const p = parseVideoModelConfig(raw);
      return { defaults: p.default, models: p.models };
    },
    toValue: (d) => ({ default: d.defaults, models: d.models }),
  },
  AudioModelConfig: {
    label: '语音模型',
    defaultFields: ['maxChars', 'refAudioMaxMB'],
    toDraft: (raw) => {
      const p = parseAudioModelConfig(raw);
      return { defaults: p.default, models: p.models };
    },
    toValue: (d) => ({ default: d.defaults, models: d.models }),
  },
  MusicModelConfig: {
    label: '音乐模型',
    // videoMaxMB 没有 tab 认领（「视频生音」已下线），但服务端护栏还在跑，
    // 所以默认值这一层必须能编辑，否则它就成了改不了的暗配置。
    defaultFields: ['maxChars', 'refAudioMaxMB', 'videoMaxMB'],
    toDraft: (raw) => {
      const p = parseMusicModelConfig(raw);
      return { defaults: p.default, models: p.models };
    },
    toValue: (d) => ({ default: d.defaults, models: d.models }),
  },
};

const STORE_KEYS = Object.keys(STORE_META);
const TAB_CONFIG_KEY = 'PlaygroundTabConfig';

// 保存时把草稿里的 models 落成最终 JSON：tabs 原样写，模型级平铺字段与能力标签由
// tabs 反推。空 tabs 且无遗留能力标签的模型在删除时就已经从草稿里摘掉了。
const serializeModels = (storeKey, models) => {
  const out = {};
  Object.entries(models || {}).forEach(([name, m]) => {
    const tabs = m.tabs || {};
    out[name] = {
      ...recomputeModelLevel(storeKey, m),
      capabilities: deriveCapabilities(storeKey, tabs, m.capabilities),
      tabs,
    };
  });
  return out;
};

export const usePlaygroundAdminDraft = () => {
  const { t } = useTranslation();
  const [statusState, statusDispatch] = useContext(StatusContext);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [options, setOptions] = useState({});
  const [tabConfig, setTabConfig] = useState({});
  const [stores, setStores] = useState({});
  const [dirty, setDirty] = useState(() => new Set());
  // 模型选择器的候选：上架模型全集。走管理员专用的 /api/models/pricing 而**不是**
  // /api/pricing —— 后者按调用者自己的分组裁剪（模型广场语义），配置页拿它当候选，
  // 管理员一旦在专用分组里下拉就整个空掉。拉不到就退化成手填。
  const [allModels, setAllModels] = useState([]);
  // 分组候选：/api/group/（AdminAuth）给的是分组倍率表的全部键。用户侧
  // /api/user/self/groups 是它再 ∩ GetUserUsableGroups(自己分组) 的结果——配置页
  // 要的是裁剪之前那个上界，运营配的是给全体用户用的东西，不是自己能用什么。
  const [allGroups, setAllGroups] = useState([]);

  const hydrate = useCallback((opts) => {
    setTabConfig(parsePlaygroundTabConfig(opts[TAB_CONFIG_KEY]));
    const next = {};
    STORE_KEYS.forEach((k) => {
      next[k] = STORE_META[k].toDraft(opts[k]);
    });
    setStores(next);
    setDirty(new Set());
  }, []);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      // 走专属的 AdminAuth 接口而不是 RootAuth 的 /api/option/：后者能写任意键
      // （SMTP 凭据、OAuth secret 等），放给管理员等于交出超管权限。
      const res = await API.get('/api/playground_admin/options');
      const { success, message, data } = res.data;
      if (!success) {
        showError(message);
        return;
      }
      const opts = {};
      data.forEach((item) => {
        opts[item.key] = item.value;
      });
      setOptions(opts);
      hydrate(opts);
    } catch (e) {
      showError(t('加载配置失败'));
    } finally {
      setLoading(false);
    }
  }, [hydrate, t]);

  useEffect(() => {
    load();
  }, [load]);

  useEffect(() => {
    (async () => {
      try {
        const res = await API.get('/api/models/pricing', {
          skipErrorHandler: true,
        });
        const { success, data } = res.data || {};
        if (success && Array.isArray(data)) setAllModels(data);
      } catch (e) {
        // 拉不到就不给候选列表，模型名仍可手填
      }
      try {
        const res = await API.get('/api/group/', { skipErrorHandler: true });
        const { success, data } = res.data || {};
        if (success && Array.isArray(data)) setAllGroups(data.filter(Boolean));
      } catch (e) {
        // 同上：分组名仍可手填
      }
    })();
  }, []);

  const markDirty = (key) =>
    setDirty((prev) => {
      const next = new Set(prev);
      next.add(key);
      return next;
    });

  // ── tab 显示配置（PlaygroundTabConfig）────────────────────────────────
  // 老配置里 tab 的值可能是布尔，改一次就升格成对象；__global 走同一套写入。
  const patchTabConfig = useCallback((category, tabKey, patch) => {
    setTabConfig((prev) => {
      const cat = { ...(prev[category] || {}) };
      const old = cat[tabKey];
      const base =
        typeof old === 'boolean'
          ? { enabled: old }
          : old && typeof old === 'object'
            ? old
            : {};
      cat[tabKey] = { ...base, ...patch };
      return { ...prev, [category]: cat };
    });
    markDirty(TAB_CONFIG_KEY);
  }, []);

  // ── 模型配置（四份 ModelConfig）──────────────────────────────────────
  const updateStore = useCallback((storeKey, updater) => {
    setStores((prev) => ({ ...prev, [storeKey]: updater(prev[storeKey]) }));
    markDirty(storeKey);
  }, []);

  const setDefaultField = useCallback(
    (storeKey, field, value) =>
      updateStore(storeKey, (s) => ({
        ...s,
        defaults: { ...s.defaults, [field]: value },
      })),
    [updateStore],
  );

  // 把模型挂进某个 tab：空对象即「已挂进，参数全走兜底」的声明本身。
  const addModelToTab = useCallback(
    (storeKey, tabKey, name) => {
      const model = (name || '').trim();
      if (!model) return;
      updateStore(storeKey, (s) => {
        const cur = s.models[model] || { capabilities: [], tabs: {} };
        if (cur.tabs?.[tabKey]) return s;
        return {
          ...s,
          models: {
            ...s.models,
            [model]: { ...cur, tabs: { ...(cur.tabs || {}), [tabKey]: {} } },
          },
        };
      });
    },
    [updateStore],
  );

  // 从 tab 摘掉模型；它若已不属于任何 tab、也没有遗留的无 tab 能力标签，整条删掉
  // ——否则模型广场还会按「配进了这份 ModelConfig」把它算进该大类。
  const removeModelFromTab = useCallback(
    (storeKey, tabKey, name) =>
      updateStore(storeKey, (s) => {
        const cur = s.models[name];
        if (!cur) return s;
        const tabs = { ...(cur.tabs || {}) };
        delete tabs[tabKey];
        const models = { ...s.models };
        const leftoverCaps = deriveCapabilities(
          storeKey,
          tabs,
          cur.capabilities,
        );
        if (!Object.keys(tabs).length && !leftoverCaps.length) {
          delete models[name];
        } else {
          models[name] = { ...cur, tabs };
        }
        return { ...s, models };
      }),
    [updateStore],
  );

  const setTabField = useCallback(
    (storeKey, tabKey, name, field, value) =>
      updateStore(storeKey, (s) => {
        const cur = s.models[name];
        if (!cur) return s;
        const entry = { ...(cur.tabs?.[tabKey] || {}) };
        if (value === undefined || value === null || value === '') {
          delete entry[field];
        } else {
          entry[field] = value;
        }
        return {
          ...s,
          models: {
            ...s.models,
            [name]: { ...cur, tabs: { ...(cur.tabs || {}), [tabKey]: entry } },
          },
        };
      }),
    [updateStore],
  );

  // 模型级（不随 tab 变）的字段，如视频的 pipeline。
  const setModelField = useCallback(
    (storeKey, name, field, value) =>
      updateStore(storeKey, (s) => {
        const cur = s.models[name];
        if (!cur) return s;
        return {
          ...s,
          models: { ...s.models, [name]: { ...cur, [field]: value } },
        };
      }),
    [updateStore],
  );

  const save = useCallback(async () => {
    if (!dirty.size) return;
    setSaving(true);
    try {
      const payload = {};
      dirty.forEach((key) => {
        if (key === TAB_CONFIG_KEY) {
          payload[key] = JSON.stringify(tabConfig);
        } else {
          const d = stores[key];
          payload[key] = JSON.stringify(
            STORE_META[key].toValue({
              defaults: d.defaults,
              models: serializeModels(key, d.models),
            }),
          );
        }
      });
      for (const [key, value] of Object.entries(payload)) {
        const res = await API.put('/api/playground_admin/option', {
          key,
          value,
        });
        if (!res.data.success) {
          showError(res.data.message);
          return;
        }
      }
      // 体验区读的是 /api/status 里的副本，保存后同步一份，免得要刷新页面才生效。
      statusDispatch({
        type: 'set',
        payload: { ...statusState.status, ...payload },
      });
      setOptions((prev) => ({ ...prev, ...payload }));
      setDirty(new Set());
      showSuccess(t('保存成功'));
    } catch (e) {
      showError(t('保存失败，请重试'));
    } finally {
      setSaving(false);
    }
  }, [dirty, stores, tabConfig, statusDispatch, statusState.status, t]);

  const reset = useCallback(() => hydrate(options), [hydrate, options]);

  return {
    loading,
    saving,
    dirty,
    options,
    tabConfig,
    stores,
    allModels,
    allGroups,
    patchTabConfig,
    setDefaultField,
    addModelToTab,
    removeModelFromTab,
    setTabField,
    setModelField,
    save,
    reset,
    reload: load,
  };
};
