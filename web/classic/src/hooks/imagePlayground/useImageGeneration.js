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
  isMediaRef,
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
  PLAYGROUND_BATCH_DEFAULT,
  normalizeBatchCount,
  deriveSeeds,
} from '../../constants/playgroundBatch.constants';
import {
  IMAGE_API_ENDPOINTS,
  IMAGE_PAGE_CAPABILITY,
  IMAGE_I2I_CAPABILITY,
  IMAGE_MAX_EDIT_IMAGES,
  IMAGE_GEN_STATUS,
  IMAGE_ASYNC_FIELD,
  IMAGE_ASYNC_TASK_ENDPOINT,
  IMAGE_ASYNC_UNSUPPORTED_CODE,
  IMAGE_POLL_INTERVAL_SEC,
  IMAGE_POLL_MAX_TRIES,
  IMAGE_HISTORY_LIMIT,
  IMAGE_CONV_TURN_LIMIT,
  getSizesForModel,
  getExplicitTabSizes,
  readImageDimensions,
  pickClosestSize,
  sizeToRatio,
  IMAGE_SIZE_AUTO,
  parseImageSizeConfig,
  normalizeImageSize,
  IMAGE_QUALITY_BOT_TASK,
  getEngineForImageModel,
} from '../../constants/imagePlayground.constants';

// 文生图 / 图生图共用本 hook,按 mode 区分能力过滤、请求端点、是否带底图。
// 两种模式各自独立的历史存储 key,互不串扰。
const CONV_STORAGE_KEY_BASE = 'image_playground_conversations';
const storageKeyFor = (mode) =>
  mode === 'image2image'
    ? `${CONV_STORAGE_KEY_BASE}_i2i`
    : CONV_STORAGE_KEY_BASE;

const loadConversations = (storageKey) => {
  try {
    const raw = localStorage.getItem(storageKey);
    const parsed = raw ? JSON.parse(raw) : [];
    return Array.isArray(parsed) ? parsed : [];
  } catch (e) {
    return [];
  }
};

// base64 媒体以 Blob 存 IndexedDB,localStorage 只留短引用(见
// docs/playground-idb-media-design.md §4.1)。conv.images = 图生图底图(续问要发后端
// → hydrate 回 data:);messages[].images = 生成结果(仅展示 → hydrate 成 objectURL);
// markNotPersisted:true → miss/IDB 不可用时给空图消息打占位标(沿用旧语义)。
const IMAGE_MEDIA_SCHEMA = {
  convArrayFields: ['images'],
  convStringFields: [],
  msgArrayFields: ['images'],
  markNotPersisted: true,
};

const persistConversations = (storageKey, list) => {
  persistWithMedia(storageKey, list, {
    ...IMAGE_MEDIA_SCHEMA,
    limit: IMAGE_HISTORY_LIMIT,
  });
};

let idSeq = 0;
const genId = () => `img-${Date.now()}-${idSeq++}`;

// 同步生图是一次性请求,没有可续查的句柄:切走页面会卸载本页,在途请求随之丢弃、
// 其完成回调落在已卸载实例上失效 → 结果连 localStorage 都没落。所以初始加载时残留的
// pending 同步消息一定是被打断的,判为失败(可重发),避免历史里永远卡在「生成中」。
//
// **异步消息是例外,必须放过**:它有 imageTasks[].taskId,任务在服务端继续跑,刷新后
// 可以接着轮询(resumeImagePolling)。把它一并判失败会让用户看到「失败」、而钱已经扣了、
// 图其实几十秒后就好了 —— 比同步被打断更糟。判据是「有没有 taskId」,不是「是不是
// pending」。仅在 mount 载入时调用。
const hasPendingTask = (m) =>
  Array.isArray(m?.imageTasks) &&
  m.imageTasks.some((t) => t && t.taskId && t.status === 'pending');

// mount 时该把用户放回哪条会话:第一条「还有异步任务在跑」的。没有则返回 null
// (维持「新对话」)。
//
// ⚠️ **依赖 conversations 是新在前**:两个写入分支都是前插 —— 新会话
// `[{...}, ...prev]`,续问的会话 `[conv, ...prev.filter(...)]` 被移到队首。
// 所以「第一个匹配」= 最新那条。曾经在这里写成 forEach 里无条件覆盖(取到最后一个
// 匹配),多条并行时用户被放回**最旧**那条 —— 不丢数据、轮询照常,只是这个功能的意图
// 整个反了,而且没有任何报错。提成纯函数就是为了让这个顺序约定能被测试锁住。
export const pickResumeConvId = (conversations) =>
  (conversations || []).find((conv) =>
    (conv?.messages || []).some((m) => hasPendingTask(m)),
  )?.id ?? null;

const markInterruptedAsFailed = (list, errText) =>
  (Array.isArray(list) ? list : []).map((conv) => ({
    ...conv,
    messages: (conv.messages || []).map((m) =>
      m.role === 'assistant' &&
      m.status === IMAGE_GEN_STATUS.PENDING &&
      !hasPendingTask(m)
        ? { ...m, status: IMAGE_GEN_STATUS.FAILED, error: errText }
        : m,
    ),
  }));

// allowBatch:是否允许一次生成多张(不同 seed 的候选)。**默认关,由调用方显式开**。
// 两个前端应用共用这个 hook,但多并发只在 web/classic 提供。
// **判据是"哪个应用",不是"屏幕多宽"**:web/mobile 是独立应用,它调这个 hook 时不传
// allowBatch(默认 false);web/classic 传 true。classic 里也有个 useIsMobile,但那是
// 实时媒体查询、只管响应式布局 —— **不能拿它当这个闸门**:拖窄窗口就会翻转,等于纯视觉
// 操作改变了发出去的请求和计费,会话中途还会把已锁定的张数悄悄降到 1。
// 手机 UA 的用户主动选「前往电脑端」时拿到的就是 classic,那是他自己要的桌面能力。
//
// 默认关不只是"web/mobile 不渲染控件"那么简单:续问时 batchCount 是从**会话**里读的
// (与 model/size/seed 同一模式),在 classic 生成过多张的对话在 web/mobile 打开续问,
// 会照着 conv.batchCount 并发多次 —— 那边没有这个控件,用户看不见也拦不住。
// 所以闸门必须在 hook 里,不能只靠"那个应用不给控件"。
export const useImageGeneration = ({
  mode = 'text2image',
  allowBatch = false,
} = {}) => {
  const { t } = useTranslation();
  const [statusState] = useContext(StatusContext);
  const [userState] = useContext(UserContext);

  const isI2I = mode === 'image2image';
  const pageCapability = isI2I ? IMAGE_I2I_CAPABILITY : IMAGE_PAGE_CAPABILITY;
  const storageKey = storageKeyFor(mode);

  const [inputs, setInputs] = useState({
    group: '',
    model: '',
    size: '',
    seed: '', // 随机种子;'' 表示随机(不下发,引擎自动随机)
    // 一次生成几张:多张的意义是**给不同 seed 让用户挑**,见 playgroundBatch.constants。
    // 默认 1 —— 多花钱的事不该是默认值。
    batchCount: PLAYGROUND_BATCH_DEFAULT,
    qualityMode: false, // 提示词智能优化；默认关
    imageUrls: [], // 图生图底图（base64 data-url 数组,≤IMAGE_MAX_EDIT_IMAGES）
  });
  const [groups, setGroups] = useState([]);
  const [models, setModels] = useState([]);
  // 来自 /api/pricing：model -> enable_groups[]（用于分组过滤）
  const [modelGroupsMap, setModelGroupsMap] = useState(new Map());

  // 以「对话」为单位的历史；每个对话 = { id, group, model, size, title, createdAt, updatedAt, messages: [...] }
  // currentConvId 为 null 表示「新对话」（尚未开始生成）
  const initialConvsRef = useRef(null);
  const [conversations, setConversations] = useState(() => {
    // 先把被打断的残留 pending 判为失败,再喂给 strip / hydrate(raw 亦供 hydrate,
    // 一并修正才能保证媒体还原后的版本不会把 pending 带回来)。
    const raw = markInterruptedAsFailed(
      loadConversations(storageKey),
      t('生成已中断，请重试'),
    );
    const stripped = stripUnresolvedMediaRefs(raw, IMAGE_MEDIA_SCHEMA);
    initialConvsRef.current = { raw, stripped };
    return stripped;
  });
  const [currentConvId, setCurrentConvId] = useState(null);
  // submitting 只覆盖「请求在飞」那一小段；异步模式下提交完成 ≠ 生成完成，
  // 对外的 generating 还要算上仍在轮询的任务（见下方 generating 的定义）。
  const [submitting, setSubmitting] = useState(false);

  // 当前对话的消息（中间区显示）
  const messages = useMemo(() => {
    const conv = conversations.find((c) => c.id === currentConvId);
    return conv ? conv.messages : [];
  }, [conversations, currentConvId]);

  // 一旦进入某个对话（已生成或打开了历史）即锁定参数，直到「新对话」
  const locked = currentConvId !== null;

  // 当前会话是否有「切走再回来还能接着查」的异步任务。
  // 给 UI 用：同步生成一断就没了，必须警告用户别切页；异步不用警告，切了也能恢复。
  // 两种模式会同时存在（第三方模型走同步、自建模型走异步），所以不能按全局开关判，
  // 只能看这条会话里实际有没有任务句柄。
  const hasResumableTask = useMemo(
    () => messages.some((m) => hasPendingTask(m)),
    [messages],
  );

  // 当前对话已生成次数 / 是否到达上限
  const turnsUsed = useMemo(
    () => messages.filter((m) => m.role === 'user').length,
    [messages],
  );
  const turnLimitReached = turnsUsed >= IMAGE_CONV_TURN_LIMIT;

  const conversationsRef = useRef(conversations);
  conversationsRef.current = conversations;
  const lockedRef = useRef(locked);
  lockedRef.current = locked;
  const groupRef = useRef(inputs.group);
  groupRef.current = inputs.group;

  // mount 后从 IDB 还原媒体,按初始对象引用逐条合并(不整体覆盖,见设计 §4.1.3)。
  useEffect(() => {
    let canceled = false;
    const init = initialConvsRef.current;
    if (!init || !(init.raw || []).length) return;
    (async () => {
      const hydrated = await hydrateConversationsFromStorage(
        init.raw,
        IMAGE_MEDIA_SCHEMA,
      );
      if (canceled) return;
      const hydratedById = new Map(hydrated.map((c) => [c.id, c]));
      const initialSet = new Set(init.stripped);
      setConversations((prev) =>
        prev.map((c) =>
          initialSet.has(c) && hydratedById.has(c.id)
            ? hydratedById.get(c.id)
            : c,
        ),
      );
    })();
    return () => {
      canceled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // 图生图「输出尺寸」的白名单与开关。用 ref 让 handleInputChange 保持零依赖——
  // 它被当回调传给 ImageUrlInput，每次重建都会让上传组件白白重渲染。
  // 声明在此、赋值在下方算出选项之后。
  const i2iSizesRef = useRef([]);
  const canPickI2ISizeRef = useRef(false);
  // 最近一次上传的那张底图的原生画幅：只在上传那一刻解析一次并记下，不随渲染重算。
  // 连同被解析的那张图一起存（{ url, w, h }）——因为它只在「仍对应当前末张底图」时
  // 才有意义：openHistoryItem 用 setInputs 直设 imageUrls、newConversation 又只清
  // currentConvId 不清 inputs，两条路径都不经 handleInputChange，留着上一轮的画幅
  // 去跟新恢复出来的 size 比，会报出张冠李戴的偏差。带上 url 就能识别并跳过。
  const [sourceDim, setSourceDim] = useState(null);

  const handleInputChange = useCallback((key, value) => {
    // 锁定后不允许修改分组/模型/尺寸
    if (lockedRef.current) return;
    setInputs((prev) => ({ ...prev, [key]: value }));

    // 底图每变动一次就解析一次「当次最后一张」的原生画幅，当场把「输出尺寸」
    // 对到最接近的档位。取最后一张而不是第一张:上传是逐次触发的,连传三张会依次
    // 收到 [a] / [a,b] / [a,b,c],每次都取末尾才等于"刚拖进来的那张说了算",
    // 最终停在 c 上;取首张则三次都在解析 a,后两张形同虚设。删图时同理——数组
    // 变短后仍以剩下的最后一张为准。
    //
    // 解析放在这里而不是 effect 里:上传是一次性动作,解析一次赋值一次即可;
    // 挂在 imageUrls 上的 effect 会随每次渲染的引用变化重跑,把用户手动改过的
    // 档位又拽回默认值。
    if (key !== 'imageUrls') return;
    const list = (value || []).filter(Boolean);
    const latest = list[list.length - 1];
    if (!latest) {
      setSourceDim(null);
      return;
    }
    readImageDimensions(latest).then((dim) => {
      // 画幅无条件记下来。白名单还没就绪时（尚未选模型、或选的模型没配 sizes）
      // 只是不动尺寸,不能连解析都跳过——下面那条合法性 effect 承诺的「先传图、
      // 后选模型」兜底,靠的就是这里留下的 sourceDim;跳过解析会让它永远拿到 null,
      // 退回白名单首档而不是最贴合的那一档,画幅偏差提示也不会出现。
      setSourceDim(dim ? { url: latest, w: dim.w, h: dim.h } : null);
      if (!dim || lockedRef.current || !canPickI2ISizeRef.current) return;
      const picked = pickClosestSize(dim, i2iSizesRef.current);
      if (picked) setInputs((prev) => ({ ...prev, size: picked }));
    });
  }, []);

  // 解析按模型尺寸配置
  const sizeConfig = useMemo(
    () => parseImageSizeConfig(statusState?.status?.ImageModelSizeConfig),
    [statusState?.status?.ImageModelSizeConfig],
  );

  const availableSizes = useMemo(
    () => getSizesForModel(sizeConfig, inputs.model, mode),
    [sizeConfig, inputs.model, mode],
  );

  // 所选模型的引擎族，只喂给「AI 优化提示词」挑模板（图像这边后端不按它分支，
  // 见 playgroundAdmin.constants 的 IMAGE_ENGINE_SENSENOVA_U15 注释）。
  const optimizeEngine = getEngineForImageModel(sizeConfig, inputs.model);

  // 图生图的输出尺寸：仅当运营给该模型在本 tab 下显式配了 sizes 才开放（见
  // getExplicitTabSizes 的注释——gpt-image 这类只认固定档位，不能无差别下发）。
  const explicitI2ISizes = useMemo(
    () =>
      isI2I ? getExplicitTabSizes(sizeConfig, inputs.model, mode) : undefined,
    [isI2I, sizeConfig, inputs.model, mode],
  );
  const canPickI2ISize =
    isI2I && Array.isArray(explicitI2ISizes) && explicitI2ISizes.length > 0;

  // 选项 = 自动档 + 运营配的白名单。白名单之外不给别的：能选的一定是模型支持的
  // 档位。底图不进选项，它只决定上传那一刻把哪一档填进框里。
  const i2iSizeOptions = useMemo(() => {
    if (!canPickI2ISize) return [];
    const seen = new Set();
    const opts = [{ value: IMAGE_SIZE_AUTO, label: t('自动（由模型决定）') }];
    (explicitI2ISizes || []).forEach((s) => {
      const value = normalizeImageSize(s);
      if (!value || value === IMAGE_SIZE_AUTO || seen.has(value)) return;
      seen.add(value);
      opts.push({ value, label: value });
    });
    return opts;
  }, [canPickI2ISize, explicitI2ISizes, t]);

  // 兜底用的具体档位（排除自动档）。默认必须落在具体档位上：对 qwen-image-edit
  // 而言「自动」等于退回引擎写死的 16:9，正是要修的那个 bug。
  const i2iConcreteSizes = useMemo(
    () =>
      i2iSizeOptions.map((o) => o.value).filter((v) => v !== IMAGE_SIZE_AUTO),
    [i2iSizeOptions],
  );

  // 上传回调只在具体档位里挑，不会挑中自动档。
  i2iSizesRef.current = i2iConcreteSizes;
  canPickI2ISizeRef.current = canPickI2ISize;

  // 已解析的画幅是否仍属于当前这批底图的末张。不匹配说明底图是被历史恢复 /
  // 新对话残留带进来的、并非本轮上传，此时那份画幅与界面上的底图无关，宁可不提示
  // 也不能拿它去比对。
  const latestImageUrl =
    (inputs.imageUrls || []).filter(Boolean).slice(-1)[0] || null;
  const activeSourceDim =
    sourceDim && sourceDim.url === latestImageUrl ? sourceDim : null;

  // 选中档位与最近一次上传那张底图的画幅相差多少。运营没把该比例配进白名单时
  // （例如只配了 16:9 却传了 1:1），输出画幅一定会变，这里算出偏差交给面板显式
  // 提示——否则用户只会看到出图被改构图，却不知道是配置不全导致的。
  const i2iAspectMismatch = useMemo(() => {
    if (!canPickI2ISize || !activeSourceDim || !inputs.size) return null;
    // 自动档由引擎/模型定画幅,谈不上"与底图不同",不提示。
    if (inputs.size === IMAGE_SIZE_AUTO) return null;
    const selected = sizeToRatio(inputs.size);
    const source = activeSourceDim.w / activeSourceDim.h;
    if (!selected || !source) return null;
    // 2% 以内视作同一画幅（1664x928=1.793 与标称 16:9=1.778 差 0.8%，不该报）
    if (Math.abs(Math.log(selected / source)) < 0.02) return null;
    return {
      sourceLabel: `${activeSourceDim.w}×${activeSourceDim.h}`,
      selectedLabel: inputs.size,
    };
  }, [canPickI2ISize, activeSourceDim, inputs.size]);

  // 图片模型集合 = 管理员在「图片模型尺寸配置」里声明、且能力含「文生图」的模型。
  // 只认运营设置里的能力声明，不再按后端端点类型识别。
  const imageModelSet = useMemo(() => {
    const set = new Set();
    Object.entries(sizeConfig.models || {}).forEach(([model, cfg]) => {
      const caps = Array.isArray(cfg?.capabilities) ? cfg.capabilities : [];
      if (caps.includes(pageCapability)) set.add(model);
    });
    return set;
  }, [sizeConfig]);

  // 含图片模型的分组集合：对图片模型集合取其 enable_groups 的并集
  const imageGroups = useMemo(() => {
    const set = new Set();
    imageModelSet.forEach((model) => {
      (modelGroupsMap.get(model) || []).forEach((g) => set.add(g));
    });
    return set;
  }, [imageModelSet, modelGroupsMap]);

  // 选中模型变化或尺寸列表变化时，确保 size 合法（锁定时不改动）。
  //
  // 图生图要拿白名单（i2iSizeOptions）而不是 availableSizes 来校验:后者带
  // 内置兜底，任何模型都非空，用它会把上传时刚对好的档位覆盖成兜底首档。
  // 这一步同时兜住「先传图、后选模型」——那时上传回调因白名单尚未就绪只记下了
  // 画幅、没动尺寸，等选项到位再用那份 sourceDim 就近补上。
  useEffect(() => {
    if (locked) return;
    const valid = canPickI2ISize
      ? i2iSizeOptions.map((o) => o.value)
      : availableSizes;
    if (!valid || valid.length === 0) return;
    if (valid.includes(inputs.size)) return;
    // 兜底落在具体档位而非自动档:自动对 qwen-image-edit 等于退回写死的 16:9。
    const fallback = canPickI2ISize ? i2iConcreteSizes : valid;
    const picked =
      (canPickI2ISize && pickClosestSize(activeSourceDim, i2iConcreteSizes)) ||
      fallback[0] ||
      valid[0];
    setInputs((prev) => ({ ...prev, size: picked }));
  }, [
    availableSizes,
    i2iSizeOptions,
    i2iConcreteSizes,
    canPickI2ISize,
    activeSourceDim,
    inputs.size,
    locked,
  ]);

  // 加载 pricing：构建 model -> 端点类型、model -> 分组 两个映射（覆盖全部模型）
  const loadPricing = useCallback(async () => {
    try {
      const payload = await cachedGet(IMAGE_API_ENDPOINTS.PRICING, {
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
      // pricing 不可用时映射为空：分组不再按 enable_groups 收窄（模型仍按能力声明过滤）
    }
  }, []);

  const loadGroups = useCallback(async () => {
    try {
      const { success, data } = await cachedGet(
        IMAGE_API_ENDPOINTS.USER_GROUPS,
      );
      if (!success) return;
      const userGroup =
        userState?.user?.group ||
        JSON.parse(localStorage.getItem('user') || '{}')?.group;
      let groupOptions = processGroupsData(data, userGroup);
      // 仅保留含图片模型的分组（auto 始终保留）。
      // enable_groups 含哨兵 "all" 表示该模型对所有分组可用，此时不做过滤。
      const allowAllGroups = imageGroups.has('all');
      if (imageGroups.size > 0 && !allowAllGroups) {
        groupOptions = groupOptions.filter(
          (g) => imageGroups.has(g.value) || g.value === 'auto',
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
  }, [userState, imageGroups, t]);

  const loadModels = useCallback(async () => {
    const requestedGroup = inputs.group;
    try {
      const { success, data } = await getUserModelsCached(requestedGroup);
      if (!success) return;
      // 分组在等待响应期间已切换(初始 '' → 用户分组 → 按图片模型过滤后的分组会连续
      // 变化数次):过期响应直接丢弃,否则旧分组的空结果会最后到达并覆盖正确的模型列表。
      if (requestedGroup !== groupRef.current) return;
      let list = Array.isArray(data) ? data : [];
      // 严格过滤：仅保留图片模型（后端识别 ∪ 管理员声明）
      list = list.filter((m) => imageModelSet.has(m));
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
  }, [inputs.group, inputs.model, imageModelSet, t]);

  // 初始化：pricing -> groups
  useEffect(() => {
    if (userState?.user) loadPricing();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [userState?.user]);

  useEffect(() => {
    if (userState?.user) loadGroups();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [userState?.user, imageGroups]);

  useEffect(() => {
    if (userState?.user) loadModels();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [userState?.user, inputs.group, imageModelSet]);

  // 更新某对话内某条消息
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
      persistConversations(storageKey, next);
      return next;
    });
  }, []);

  // ——— 异步生图：能力缓存 + 轮询 ———

  // 模型 → 是否支持异步。前端拿不到「模型属于哪个渠道」（models 来自 /api/user/models，
  // 不带渠道信息），所以只能试错：首次带 async 提交，收到 async_not_supported 就记下来，
  // 之后这个模型直接走同步，不再白试。ref 而非 state：它不参与渲染，改它不该触发重渲。
  const asyncCapableRef = useRef(new Map());

  // 进行中的轮询槽：msgId → { convId, timer, canceled }。
  // 与视频 hook 同一手法——轮询回调要读最新值，不能走渲染。
  const pollSlotsRef = useRef(new Map());
  // generating 由「还剩几个活跃槽」派生，槽是 ref 所以要手动同步到 state。
  const [activePolls, setActivePolls] = useState(0);
  const syncActivePolls = useCallback(() => {
    setActivePolls(pollSlotsRef.current.size);
  }, []);

  const stopPolling = useCallback(
    (msgId) => {
      const slot = pollSlotsRef.current.get(msgId);
      if (!slot) return;
      slot.canceled = true;
      if (slot.timer) clearTimeout(slot.timer);
      pollSlotsRef.current.delete(msgId);
      syncActivePolls();
    },
    [syncActivePolls],
  );

  // 卸载时清掉所有定时器：回调落在已卸载实例上除了报警告没有意义，
  // 任务本身在服务端继续跑，下次进页面由 resume 接手。
  useEffect(
    () => () => {
      pollSlotsRef.current.forEach((slot) => {
        slot.canceled = true;
        if (slot.timer) clearTimeout(slot.timer);
      });
      pollSlotsRef.current.clear();
    },
    [],
  );

  // pollOnce 要在自己的定时器回调里递归调度，经 ref 间接引用绕开 TDZ。
  const pollOnceRef = useRef(null);

  // 轮询一条消息名下的所有任务。每完成一个就写回一个（渐进展示）——
  // 多候选是 N 个独立任务，等齐了再一次性显示会白白浪费先出来的那几张。
  const pollOnce = useCallback(
    async (convId, msgId, tries) => {
      const slot = pollSlotsRef.current.get(msgId);
      if (!slot || slot.canceled) return;

      const snapshot = slot.tasks;
      const pending = snapshot.filter((x) => x.status === 'pending');
      if (pending.length === 0) return;

      await Promise.allSettled(
        pending.map(async (task) => {
          try {
            const res = await API.get(
              `${IMAGE_ASYNC_TASK_ENDPOINT}/${task.taskId}`,
              { skipErrorHandler: true },
            );
            const d = res?.data || {};
            if (d.status === 'completed') {
              const url = (d.data || [])[0]?.url || '';
              task.status = url ? 'done' : 'failed';
              task.url = url;
              if (!url) task.error = t('未返回图片数据');
            } else if (d.status === 'failed' || d.status === 'cancelled') {
              task.status = 'failed';
              task.error = d.error?.message || t('图片生成失败');
            }
            // queued / in_progress：保持 pending，下一轮再看
          } catch (e) {
            // 轮询瞬时错误不判失败，继续重试直到撞上限：网络抖一下就把任务判死，
            // 用户会看到「失败」而服务端其实出图了。
            void e;
          }
        }),
      );

      if (slot.canceled) return;

      const done = snapshot.filter((x) => x.status === 'done');
      const failed = snapshot.filter((x) => x.status === 'failed');
      const stillPending = snapshot.filter((x) => x.status === 'pending');

      const patch = {
        imageTasks: snapshot.map((x) => ({ ...x })),
        images: done.map((x) => x.url),
        imageSeeds: done.map((x) => x.seed ?? null),
      };

      if (stillPending.length === 0) {
        // 全部终态：有一张出来就算成功（与同步路径「部分成功仍展示」的口径一致）。
        patch.status =
          done.length > 0 ? IMAGE_GEN_STATUS.SUCCESS : IMAGE_GEN_STATUS.FAILED;
        if (done.length === 0) {
          patch.error = failed[0]?.error || t('图片生成失败');
        }
        patchConvMessage(convId, msgId, patch);
        stopPolling(msgId);
        if (done.length > 0 && failed.length > 0) {
          showError(
            t('已生成 {{ok}} 张，{{fail}} 张失败：', {
              ok: done.length,
              fail: failed.length,
            }) + (failed[0]?.error || ''),
          );
        }
        return;
      }

      patchConvMessage(convId, msgId, patch);

      if (tries >= IMAGE_POLL_MAX_TRIES) {
        // 撞上限只停轮 + 标记，不判失败：任务还在服务端跑，钱也扣了，
        // 判失败等于告诉用户「白花了」。留 pollTimedOut 供 UI 提供「继续获取」。
        patchConvMessage(convId, msgId, { pollTimedOut: true });
        stopPolling(msgId);
        return;
      }

      slot.timer = setTimeout(
        () => pollOnceRef.current(convId, msgId, tries + 1),
        slot.intervalMs,
      );
    },
    [patchConvMessage, stopPolling, t],
  );
  pollOnceRef.current = pollOnce;

  // 为一条消息（重新）开轮询。已在轮询同一条则跳过，避免重复定时器。
  const startPolling = useCallback(
    (convId, msgId, tasks, intervalSec) => {
      if (pollSlotsRef.current.has(msgId)) return;
      pollSlotsRef.current.set(msgId, {
        convId,
        canceled: false,
        timer: null,
        tasks: tasks.map((x) => ({ ...x })),
        intervalMs: Math.max(1, intervalSec || IMAGE_POLL_INTERVAL_SEC) * 1000,
      });
      syncActivePolls();
      const slot = pollSlotsRef.current.get(msgId);
      slot.timer = setTimeout(
        () => pollOnceRef.current(convId, msgId, 1),
        slot.intervalMs,
      );
    },
    [syncActivePolls],
  );

  // 挂载后为仍在进行中的异步任务恢复轮询：刷新 / 切走再回来不丢进度。
  // 依赖 conversations 首次就绪即可，故只跑一次（跑多次会被 startPolling 的去重挡掉，
  // 但没必要每次 conversations 变都扫一遍）。
  // 对外的「生成中」：请求在飞，或还有任务在轮询。
  // 异步下这两段是分离的——提交毫秒级返回，真正的等待发生在轮询阶段，
  // 只看 submitting 会让输入框在图还没出来时就解锁，用户能连着发第二次。
  const generating = submitting || activePolls > 0;

  // 本次等待「刷新就没了」吗？—— 决定要不要摆出「请勿刷新页面」那条警告。
  //
  // 异步任务一旦建起来就有 taskId（hasResumableTask），刷新后 mount 会按它续上轮询，
  // 切走再回来也接得住 —— 这时还警告，等于把用户按在页面上干等本可以离开的几十秒。
  // 手机端那条同名警告在异步化时已按同一理由删掉，网页端这条当时漏了。
  //
  // **不能无条件删**：异步只有自建渠道（GPUStackPlus）支持，第三方模型
  // （gpt-image / replicate / siliconflow）会被 async_not_supported 打回、回落同步，
  // 那条路上刷新确实会丢，警告仍然是对的。所以是「按本次是不是异步」显示，不是删。
  //
  // 复用 hasResumableTask 而不是另立判据（如 activePolls === 0）：那个值本来就是为这件
  // 事算的（见它的注释），只是一直没有消费方。两处各写一份迟早分叉。
  const interruptible = generating && !hasResumableTask;

  const resumedRef = useRef(false);
  useEffect(() => {
    if (resumedRef.current || conversations.length === 0) return;
    resumedRef.current = true;
    conversations.forEach((conv) => {
      (conv.messages || []).forEach((m) => {
        if (m.role !== 'assistant' || m.status !== IMAGE_GEN_STATUS.PENDING)
          return;
        if (!hasPendingTask(m)) return;
        startPolling(conv.id, m.id, m.imageTasks, IMAGE_POLL_INTERVAL_SEC);
      });
    });
    // 有正在跑的任务时,直接把用户放回**最新**那条会话(见 pickResumeConvId:
    // conversations 是新在前,取第一个匹配),而不是落在「新对话」上让他自己去历史里
    // 翻 —— 切走再回来的人十有八九就是回来看结果的。
    //
    // **只在 mount 这一次做**(resumedRef 已经保证):否则用户在生成过程中主动点
    // 「新对话」会被立刻拽回去,变成点不动的按钮。
    // 只在用户还没自己选过会话时才接管(currentConvId 恒初始化为 null)。
    const resumeConvId = pickResumeConvId(conversations);
    if (resumeConvId != null) setCurrentConvId((cur) => cur ?? resumeConvId);
  }, [conversations, startPolling]);

  // 核心：生成图片（追加到当前对话；无当前对话则新建一个并锁定参数）
  const generate = useCallback(
    async (prompt) => {
      const text = (prompt || '').trim();
      if (!text || generating) return;

      // 图生图:底图取自新对话的 inputs.imageUrls;后续追问沿用对话首条锁定的底图。
      let convImages = [];
      let convId = currentConvId;
      let params;
      if (convId == null) {
        if (!inputs.model) {
          showError(t('请先选择一个图片模型'));
          return;
        }
        if (isI2I) {
          const imgs = (inputs.imageUrls || []).filter(Boolean);
          if (imgs.length === 0) {
            showError(t('请先上传至少一张底图'));
            return;
          }
          if (imgs.length > IMAGE_MAX_EDIT_IMAGES) {
            showError(
              t('最多上传 {{count}} 张底图', { count: IMAGE_MAX_EDIT_IMAGES }),
            );
            return;
          }
          convImages = imgs;
        }
        convId = genId();
        params = {
          group: inputs.group,
          model: inputs.model,
          size: normalizeImageSize(inputs.size),
          seed: inputs.seed,
          batchCount: inputs.batchCount,
          qualityMode: inputs.qualityMode,
          images: convImages,
        };
      } else {
        const conv = conversationsRef.current.find((c) => c.id === convId);
        // 单段对话生成次数上限
        const used = conv
          ? conv.messages.filter((m) => m.role === 'user').length
          : 0;
        if (used >= IMAGE_CONV_TURN_LIMIT) {
          showError(
            t('本轮对话生成次数已达上限（{{count}} 次），请开启新对话', {
              count: IMAGE_CONV_TURN_LIMIT,
            }),
          );
          return;
        }
        params = conv
          ? {
              group: conv.group,
              model: conv.model,
              size: conv.size,
              seed: conv.seed,
              // 老对话没有这个字段 → 归一成 1,与改造前行为一致。
              batchCount: normalizeBatchCount(conv.batchCount),
              qualityMode: conv.qualityMode,
              images: conv.images || [],
            }
          : {
              group: inputs.group,
              model: inputs.model,
              size: normalizeImageSize(inputs.size),
              seed: inputs.seed,
              batchCount: inputs.batchCount,
              qualityMode: inputs.qualityMode,
              images: convImages,
            };
      }

      // 图生图续问:底图取自锁定的对话;刷新后 base64 底图已从 localStorage 剥离,
      // 此时无法续问,提示重开对话重新上传(避免向后端发空底图被拒)。
      if (isI2I) {
        // 防御(§2 硬规则):hydrate 已保证无 idb-media: 残留,再过滤一遍——裸引用绝不
        // 能作为底图参数发后端;顺带剥掉 hydrate miss 的空值。
        params.images = (params.images || []).filter(
          (s) => s && !isMediaRef(s),
        );
        if (params.images.length === 0) {
          showError(t('底图已失效,请开启新对话并重新上传底图'));
          return;
        }
      }

      const reqId = genId();
      const now = new Date().toISOString();
      const userMsg = {
        id: `${reqId}-u`,
        role: 'user',
        content: text,
        // 图生图:用户消息展示底图
        images: isI2I ? params.images || [] : undefined,
      };
      const asstMsg = {
        id: `${reqId}-a`,
        role: 'assistant',
        status: IMAGE_GEN_STATUS.PENDING,
        model: params.model,
        size: params.size,
        prompt: text,
        images: [],
        // 异步任务槽（每个候选一个）。同步路径留空，markInterruptedAsFailed 据此
        // 区分「同步被打断」与「异步可续查」。
        imageTasks: [],
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
              size: params.size,
              seed: params.seed,
              batchCount: params.batchCount,
              qualityMode: params.qualityMode,
              images: params.images || [],
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
        next = next.slice(0, IMAGE_HISTORY_LIMIT);
        persistConversations(storageKey, next);
        return next;
      });
      if (currentConvId == null) setCurrentConvId(convId);
      setSubmitting(true);

      try {
        const reqBody = {
          model: params.model,
          group: params.group,
          prompt: text,
          n: 1,
          // 不强制 response_format：各供应商返回原生格式（url 或 base64），前端均兼容
        };
        // 文生图一律下发；图生图只在运营为该模型显式开了输出尺寸、且用户没选
        // 「自动」时下发。没开或选了自动就一个字段都不带,把画幅交回引擎——
        // hunyuan-image-3 会用模型 AR 预测的 <img_ratio_*> 定画幅(多图融合时
        // 常比人工指定更合适),而 qwen-image-edit 不传只会落到写死的 16:9,
        // 所以它的默认值始终是具体档位、不是自动。第三方渠道(gpt-image 等)
        // 因为运营不会给它配 sizes,同样一个字段都收不到。
        if (
          !isI2I ||
          (canPickI2ISize && params.size && params.size !== IMAGE_SIZE_AUTO)
        ) {
          reqBody.size = normalizeImageSize(params.size);
        }
        // 随机种子见下方并发段:多张时由前端逐张下发,单张时维持原行为。
        // 提示词智能优化:开了才发,关闭时一个字段都不带。
        //
        // ⚠️ 关闭时不能发 use_prompt_enhancer: false ——这些未知字段落在
        // dto.ImageRequest.Extra,虽然 MarshalJSON 不合并 Extra,但部分适配器绕开它
        // 直接读:replicate(adaptor.go:148)与 siliconflow(adaptor.go:42)会把 Extra
        // 全量转发给上游 input,不认的字段可能报错或改变行为。minimax / ali 是白名单,
        // 不受影响。关闭时省掉该字段不影响 ERNIE:gpustackplus 的 imageBoolExtraFrom
        // 读不到即 false,仍会显式下发 extra_args.apply_pe=false 给引擎。
        if (params.qualityMode) {
          reqBody.use_prompt_enhancer = true;
          reqBody.bot_task = IMAGE_QUALITY_BOT_TASK;
        }
        // 图生图:走 edits 端点,带底图数组(gpustackplus 后端接受 image 数组)
        if (isI2I) {
          reqBody.image = params.images || [];
        }
        const endpoint = isI2I
          ? IMAGE_API_ENDPOINTS.IMAGE_EDITS
          : IMAGE_API_ENDPOINTS.IMAGE_GENERATIONS;
        // 不允许多张时一律按 1 走,连带 seed 也回到"用户填了才发"的原口径。
        const count = allowBatch ? normalizeBatchCount(params.batchCount) : 1;

        // seed 的两种口径,**刻意不统一**:
        //   count === 1 → 维持改造前:用户填了才发,留空不发。
        //   count > 1  → 由前端逐张下发不同 seed(deriveSeeds),否则 N 张一模一样。
        // 为什么单张不顺手也发一个:seed 不是 dto.ImageRequest 的声明字段,它落进
        // Extra,而 replicate / siliconflow 这两个适配器会把 Extra **全量转发**给上游
        // (见上面 use_prompt_enhancer 处的同一条警告)。今天 seed 只在用户手填时下发,
        // 风险由用户自己担;改成每次都发,等于替所有第三方渠道担了这个风险。
        // 代价是单张时结果上显示不出 seed —— 那与改造前一致,不是回退。
        const seeds =
          count > 1
            ? deriveSeeds(params.seed, count)
            : params.seed !== '' && params.seed != null
              ? [Number(params.seed)]
              : [null];

        const extractImages = (res) => {
          const items = Array.isArray(res?.data?.data) ? res.data.data : [];
          return items
            .map((it) =>
              it.url
                ? it.url
                : it.b64_json
                  ? `data:image/png;base64,${it.b64_json}`
                  : null,
            )
            .filter(Boolean);
        };

        const bodyFor = (seed, extra) => ({
          ...reqBody,
          ...(seed == null ? {} : { seed }),
          ...extra,
        });

        // ——— 先试异步 ———
        //
        // 异步只有自建渠道（GPUStackPlus）支持，而前端不知道模型属于哪个渠道，
        // 所以只能试错：收到 async_not_supported 就记下这个模型、当场改走同步。
        // 代价是第三方模型首次多一次 400 往返（后端在选完渠道后立刻拒，不碰上游，
        // 也不建任务、不扣费），之后由 asyncCapableRef 直接短路。
        if (asyncCapableRef.current.get(params.model) !== false) {
          const submits = await Promise.allSettled(
            seeds.map((seed) =>
              API.post(endpoint, bodyFor(seed, { [IMAGE_ASYNC_FIELD]: true }), {
                skipErrorHandler: true,
              }),
            ),
          );

          // 判据是「有没有真的建起任务」，不是「有没有人报 unsupported」。
          // 顺序反过来的话，一旦出现「部分成功 + 部分报不支持」，已建的任务会被
          // 丢在服务端没人轮询 —— 那笔钱已经扣了，图也会照常生成，只是没人来取。
          const tasks = [];
          const submitFailures = [];
          let intervalSec = IMAGE_POLL_INTERVAL_SEC;
          submits.forEach((r, i) => {
            if (r.status !== 'fulfilled') {
              submitFailures.push(
                r.reason?.response?.data?.error?.message ||
                  r.reason?.response?.data?.message ||
                  r.reason?.message ||
                  t('图片生成失败'),
              );
              return;
            }
            const taskId = r.value?.data?.id;
            if (!taskId) {
              submitFailures.push(t('未返回任务 ID'));
              return;
            }
            // Retry-After 由后端按模型快慢给（快模型 3s、慢模型 10s），
            // 拿不到才退回默认值。慢模型用 3s 轮询等于空打三十多次。
            const ra = Number(r.value?.headers?.['retry-after']);
            if (Number.isFinite(ra) && ra > 0) intervalSec = ra;
            tasks.push({
              taskId,
              seed: seeds[i],
              status: 'pending',
              url: '',
              error: '',
            });
          });

          if (tasks.length > 0) {
            asyncCapableRef.current.set(params.model, true);
            patchConvMessage(convId, `${reqId}-a`, { imageTasks: tasks });
            startPolling(convId, `${reqId}-a`, tasks, intervalSec);
            if (submitFailures.length > 0) {
              showError(
                t('{{fail}} 张提交失败：', {
                  fail: submitFailures.length,
                }) + submitFailures[0],
              );
            }
            // 异步路径到此为止：消息保持 PENDING，由轮询接手推进。
            return;
          }

          // 一个任务都没建起来。若是渠道不支持异步，记下这个模型，之后不再白试；
          // 无论哪种原因都落到下面的同步路径再试一次，不让用户为一次探测白等。
          if (
            submits.some(
              (r) =>
                r.status === 'rejected' &&
                r.reason?.response?.data?.code === IMAGE_ASYNC_UNSUPPORTED_CODE,
            )
          ) {
            asyncCapableRef.current.set(params.model, false);
          }
        }

        // ——— 同步路径（原行为，一字未改） ———
        // allSettled 而不是 all:一路失败不该把已经出来的另外几张一起丢掉。
        // 并发本身是这个功能的实现方式 —— 门面的任务契约是单产物,发 batch_size 没用
        // (见 playgroundBatch.constants 的说明)。
        const settled = await Promise.allSettled(
          seeds.map((seed) =>
            API.post(endpoint, bodyFor(seed), {
              skipErrorHandler: true,
            }),
          ),
        );

        const images = [];
        const imageSeeds = [];
        const failures = [];
        settled.forEach((r, i) => {
          if (r.status !== 'fulfilled') {
            failures.push(
              r.reason?.response?.data?.error?.message ||
                r.reason?.message ||
                t('图片生成失败'),
            );
            return;
          }
          const urls = extractImages(r.value);
          if (urls.length === 0) {
            failures.push(t('未返回图片数据'));
            return;
          }
          // 一次请求理论上只回一张(n=1),但真回多张也照单收下,seed 按请求对齐。
          urls.forEach((u) => {
            images.push(u);
            imageSeeds.push(seeds[i]);
          });
        });

        // 全军覆没才算失败;部分成功仍展示已出的图,另给一句提示。
        if (images.length === 0) {
          throw new Error(failures[0] || t('未返回图片数据'));
        }
        patchConvMessage(convId, `${reqId}-a`, {
          status: IMAGE_GEN_STATUS.SUCCESS,
          images,
          // 与 images 等长、按下标对齐。单张且用户没填 seed 时是 [null],渲染侧不显示。
          imageSeeds,
        });
        if (failures.length > 0) {
          showError(
            t('已生成 {{ok}} 张，{{fail}} 张失败：', {
              ok: images.length,
              fail: failures.length,
            }) + failures[0],
          );
        }
      } catch (error) {
        const msg =
          error?.response?.data?.error?.message ||
          error?.message ||
          t('图片生成失败');
        patchConvMessage(convId, `${reqId}-a`, {
          status: IMAGE_GEN_STATUS.FAILED,
          error: msg,
        });
        showError(msg);
      } finally {
        setSubmitting(false);
      }
    },
    [
      currentConvId,
      inputs,
      generating,
      interruptible,
      allowBatch,
      patchConvMessage,
      storageKey,
      isI2I,
      canPickI2ISize,
      t,
    ],
  );

  const regenerate = useCallback((prompt) => generate(prompt), [generate]);

  // 轮询撞上限后手动续查：用原 taskId 接着轮，不重新提交。
  // 重新提交会再扣一次费，而任务其实还在服务端跑着 —— 与视频的「继续获取」同一语义。
  const refetchImage = useCallback(
    (msgId) => {
      const conv = conversationsRef.current.find((c) =>
        (c.messages || []).some((m) => m.id === msgId),
      );
      if (!conv) return;
      const msg = conv.messages.find((m) => m.id === msgId);
      if (!msg || !hasPendingTask(msg)) return;
      patchConvMessage(conv.id, msgId, { pollTimedOut: false });
      startPolling(conv.id, msgId, msg.imageTasks, IMAGE_POLL_INTERVAL_SEC);
    },
    [patchConvMessage, startPolling],
  );

  // 新对话：解锁参数，清空中间区
  const newConversation = useCallback(() => {
    setCurrentConvId(null);
  }, []);

  // 删会话前必须先收掉它名下的轮询槽。
  //
  // 槽只在「任务全终态」或「撞轮询上限」时自己收，会话被删掉不在其列 —— 留下的孤儿
  // 定时器会让 activePolls 一直大于 0，而 generating 由它派生、generate 又以
  // generating 为闸门，结果是**新的生成被静默挡住**（连报错都没有），直到孤儿轮询
  // 自然结束。慢模型下这可能是十几分钟。同步时代没有这个问题：那时没有后台定时器。
  const stopPollingForConv = useCallback(
    (convId) => {
      Array.from(pollSlotsRef.current.entries()).forEach(([msgId, slot]) => {
        if (slot.convId === convId) stopPolling(msgId);
      });
    },
    [stopPolling],
  );

  const clearHistory = useCallback(() => {
    Array.from(pollSlotsRef.current.keys()).forEach(stopPolling);
    setConversations([]);
    persistConversations(storageKey, []);
    setCurrentConvId(null);
  }, [stopPolling]);

  const deleteHistoryItem = useCallback(
    (id) => {
      stopPollingForConv(id);
      setConversations((prev) => {
        const next = prev.filter((c) => c.id !== id);
        persistConversations(storageKey, next);
        return next;
      });
      setCurrentConvId((cur) => (cur === id ? null : cur));
    },
    [stopPollingForConv],
  );

  // 点击历史：恢复整段对话，并带出当时锁定的分组/模型/尺寸/种子
  const openHistoryItem = useCallback(
    (conv) => {
      setCurrentConvId(conv.id);
      setInputs((prev) => ({
        ...prev,
        group: conv.group != null ? conv.group : prev.group,
        model: conv.model != null ? conv.model : prev.model,
        size: conv.size != null ? conv.size : prev.size,
        seed: conv.seed != null ? conv.seed : prev.seed,
        batchCount: normalizeBatchCount(conv.batchCount),
        qualityMode:
          conv.qualityMode != null ? conv.qualityMode : prev.qualityMode,
        // 图生图历史会话恢复底图，供左侧锁定态只读预览；媒体已由 IDB hydrate。
        imageUrls: isI2I ? conv.images || [] : prev.imageUrls,
      }));
    },
    [isI2I],
  );

  // 图生图必须先上传底图:新对话(未锁定)且无底图时发送置灰,
  // 避免只填提示词就点发送(点了才报错且 Semi 会清空已输入的提示词)。
  const missingRequiredImage =
    isI2I && !locked && (inputs.imageUrls || []).length === 0;

  return {
    isI2I,
    // 页面据此决定要不要渲染「生成张数」控件 —— 与上面 count 处的闸门同一个开关,
    // 不会出现"控件在但不生效"或"能生效却没控件"。
    allowBatch,
    inputs,
    handleInputChange,
    groups,
    models,
    availableSizes,
    canPickI2ISize,
    i2iSizeOptions,
    i2iAspectMismatch,
    optimizeEngine,
    messages,
    conversations,
    currentConvId,
    generating,
    interruptible,
    hasResumableTask,
    locked,
    turnLimitReached,
    missingRequiredImage,
    generate,
    regenerate,
    refetchImage,
    newConversation,
    clearHistory,
    deleteHistoryItem,
    openHistoryItem,
  };
};
