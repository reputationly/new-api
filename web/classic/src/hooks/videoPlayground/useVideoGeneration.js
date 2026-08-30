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
  VIDEO_API_ENDPOINTS,
  VIDEO_PAGE_CAPABILITY,
  VIDEO_I2V_CAPABILITY,
  VIDEO_FLF2V_CAPABILITY,
  VIDEO_S2V_CAPABILITY,
  VIDEO_R2VA_CAPABILITY,
  VIDEO_SR_CAPABILITY,
  VIDEO_VACE_CAPABILITY,
  VIDEO_DUB_CAPABILITY,
  VIDEO_CAPABILITY_LEGACY_ALIASES,
  VIDEO_ASPECT_RATIO_AUTO,
  VIDEO_DEFAULT_ASPECT_RATIO,
  aspectRatioToShape,
  getAspectRatiosForVideoModel,
  VIDEO_STATUS,
  VIDEO_HISTORY_LIMIT,
  VIDEO_CONV_TURN_LIMIT,
  VIDEO_MAX_CONCURRENT_TASKS,
  VIDEO_INTERPOLATION_TARGET_FPS,
  INTERPOLATION_ENABLED,
  VIDEO_SR_RATIO_UNCAPPED,
  VIDEO_SR_RESIZE_MODE,
  upscaleTargetShortEdge,
  buildVideoSizeChoices,
  isPipelineModel,
  isNativeDeliveryModel,
  VIDEO_DELIVERY_SHORT_EDGE_KEY,
  keyframeModeOf,
  deriveKeyframeTaskType,
  findCapabilityModelIn,
  DUB_PIPELINE_MODES,
  DUB_PIPELINE_ENABLED,
  VIDEO_POLL_INTERVAL_MS,
  VIDEO_POLL_MAX_TIMES,
  parseVideoModelConfig,
  getSizesForVideoModel,
  getDurationsForVideoModel,
  getMaxInputMBForModel,
  getMaxAudioSecForModel,
  getEngineForVideoModel,
  getDefaultStepsForVideoModel,
  VIDEO_STEPS_MODES,
  getMaxRefImagesForModel,
  getMaxRefVideosForModel,
  getRefVideoMaxMBForModel,
  getRefVideoMaxSecForModel,
  resolveVideoStrategy,
  normalizeVideoSize,
  normalizeVideoStatus,
  parseProgress,
  buildVideoContentUrl,
  VIDEO_ENGINE_MINIMAX_H3,
} from '../../constants/videoPlayground.constants';
import {
  PLAYGROUND_BATCH_DEFAULT,
  normalizeBatchCount,
  deriveSeeds,
} from '../../constants/playgroundBatch.constants';
import { composeImageToRatio, FIT_BLUR } from '../../helpers/imageCompose';
import {
  getTabFieldLock,
  tabHasField,
} from '../../constants/playgroundAdmin.constants';
import { buildH3OptimizeContext } from '../../constants/h3Prompt.constants';

// 文生视频 / 图生视频 / 首尾帧 / 数字人 / 视频超分 / 视频编辑共用本 hook,按 mode
// 区分能力过滤、需要哪些输入(帧图 / 音频 / 视频 / 蒙版 / 参考图)。
const CONV_STORAGE_KEY_BASE = 'video_playground_conversations';
const VIDEO_MODES = {
  // text2video 显式下发 t2v(不再靠模型名推断):Bernini 同名模型横跨 t2v 与
  // v2v/rv2v/r2v,inferTaskType 按名恒判 v2v,故这里必须显式;对其它 t2v 模型无影响。
  text2video: {
    capability: VIDEO_PAGE_CAPABILITY,
    suffix: '',
    taskType: 't2v',
  },
  // 图生视频(2026-07 改判 Bernini r2v):参考图(1~3 张)生成视频,显式 task_type=r2v
  // (Bernini 模型名推断恒 v2v,必须显式)。旧 wan i2v 的「首帧生视频」迁到关键帧模式。
  image2video: {
    capability: VIDEO_I2V_CAPABILITY,
    suffix: '_i2v',
    taskType: 'r2v',
  },
  // 关键帧(原「首尾帧」):同一 tab 承载 wan2.2 的 i2v 与 flf2v 两个模型(同权重、不同
  // --task 的两个实例)。task_type 按所选模型显式下发(见 isFlf2vModel),故不设静态 taskType。
  flf2v: { capability: VIDEO_FLF2V_CAPABILITY, suffix: '_flf2v' },
  // 参考生视频(r2va):参考图/视频/音频 → 带语音的视频。与 s2v 的关键差别是音频语义 ——
  // s2v 的音频是驱动音轨(决定输出时长),r2va 的是音色/说话风格参考(长度与输出无关,
  // 台词写在 prompt 里)。故两者不能合并成一个 task_type。
  r2va: {
    capability: VIDEO_R2VA_CAPABILITY,
    suffix: '_r2va',
    taskType: 'r2va',
  },
  // 门面 task_type：s2v(音频生视频)/ sr(视频超分)。
  s2v: { capability: VIDEO_S2V_CAPABILITY, suffix: '_s2v', taskType: 's2v' },
  sr: { capability: VIDEO_SR_CAPABILITY, suffix: '_sr', taskType: 'sr' },
  // 视频配乐(LTX-2.3 首发):源视频复用 sr 的 sourceVideo 输入,提示词可选(描述想要
  // 的声音:音效/环境音/BGM/台词),输出=原画面 + AI 音轨的 mp4。task_type 显式 v2a。
  dub: { capability: VIDEO_DUB_CAPABILITY, suffix: '_dub', taskType: 'v2a' },
  // 「视频编辑」mode 键沿用 vace(避免动 localStorage 历史键 / 示例 key),但现驱动 Bernini:
  // 必须有 1 个源视频,task_type 提交时按有无参考图分流 v2v/rv2v(见 isVACE 提交块);
  // 仅参考图的 r2v 已迁到「图生视频」模式。
  //
  // ⚠️ **srcVideo2 / taskTypeOverride 是「只读遗留字段」,不是死代码,别顺手删。**
  // 双视频玩法(mv2v 多源编辑 / ads2v 广告植入)后端与门面仍全量支持,只是体验区收了
  // 第二个上传口(与「参考生视频」同一套处理:体验区收窄、API 给全量),故**没有写入源**
  // ——新会话恒无这两个字段。但 2026-08 之前存下来的双视频会话仍在用户本地(localStorage
  // + IDB,Blob 不会被孤儿清理误删:runCleanup 扫的是原始串,与字段 schema 无关),这些
  // 会话的续问/重新生成必须仍按原 task_type 原样发出、锁定态也照旧展示第二个视频 ——
  // 否则就是 VideoConfigPanel 里警告过的那个形状:素材仍在会话里、仍会被发出去,界面
  // 却不显示。所以读路径(schema/续问重建/提交分流/openHistoryItem)全部保留,只关写路径。
  vace: {
    capability: VIDEO_VACE_CAPABILITY,
    suffix: '_vace',
  },
};
// vace 参考图最多张数(与门面 _MAX_INPUT_IMAGES 对齐);图生视频(r2v)产品档 3 张
// (与 adaptor maxR2VRefImages 对齐)。
const MAX_REF_IMAGES = 5;
const MAX_R2V_REF_IMAGES = 3;
// 「参考生视频」(r2va) 的参考图上限。取 MiniMax H3 与 Seedance 2.0 的最小交集 —— 两家
// 都是 9(H3 核实自引擎 _validate_ref2va_reference_counts;Seedance 官方文档未声明上限,
// 超限由上游报错)。与 Bernini r2v 的 3 张不同:那是「验证报告 5 张可控、8 张难控」的
// 产品档位,不是技术限制,两者不该共用一个常量。
const MAX_R2VA_REF_IMAGES = 9;
// 参考视频个数的引擎上限(与 adaptor 的 maxR2VARefVideos、门面 _TASK_INPUT_CAPS 对齐)。
// 运营配得再大也没意义:多出来的槽发出去必被拒,不如就地钳住。
const MAX_R2VA_REF_VIDEOS = 3;
const modeMeta = (mode) => VIDEO_MODES[mode] || VIDEO_MODES.text2video;
const storageKeyFor = (mode) =>
  `${CONV_STORAGE_KEY_BASE}${modeMeta(mode).suffix}`;

const loadConversations = (storageKey) => {
  try {
    const raw = localStorage.getItem(storageKey);
    const parsed = raw ? JSON.parse(raw) : [];
    return Array.isArray(parsed) ? parsed : [];
  } catch (e) {
    return [];
  }
};

// 视频体验区的媒体字段 schema(哪些字段是 base64 媒体):
//   续问要发后端的(hydrate 回 data:):帧图/人物图 images、vace 参考图 refImages、
//     r2va 参考视频 refVideos(数组);s2v 音频 audioData、sr 源视频 sourceVideo、
//     vace 源视频 srcVideo(单值);
//   仅展示的(hydrate 成 objectURL):消息级 images。
// 媒体以 Blob 存 IndexedDB,localStorage 只留短引用,刷新后可恢复、可续问、可回看。
//
// ⚠️ **新增媒体字段必须登记到这里**,尤其是视频这种大件:漏登记不是「历史差点意思」,
// 而是 base64 原样进 localStorage —— 一个 50 MB 的视频转 base64 约 67 MB,直接顶爆
// 配额,整条会话历史都写不进去。
const VIDEO_MEDIA_SCHEMA = {
  convArrayFields: ['images', 'refImages', 'refVideos'],
  // srcVideo2 只剩老会话会有(见 VIDEO_MODES.vace 的说明),但**必须留在 schema 里**:
  // 摘掉它,老会话续问时拿到的就是没 hydrate 的 idb-media: 裸串,而不是可提交的 data-url。
  convStringFields: ['audioData', 'sourceVideo', 'srcVideo', 'srcVideo2'],
  msgArrayFields: ['images'],
  // 生成的视频结果(原为 /v1/videos/{id}/content 实时下载):抓 Blob 缓存进 IDB,刷新后
  // 直接读、后端按保留天数清理后仍可回看。
  msgMediaFields: ['videoUrl'],
  markNotPersisted: false,
};

const persistConversations = (storageKey, list) => {
  persistWithMedia(storageKey, list, {
    ...VIDEO_MEDIA_SCHEMA,
    limit: VIDEO_HISTORY_LIMIT,
  });
};

let idSeq = 0;
const genId = () => `vid-${Date.now()}-${idSeq++}`;

// completed 视频的 videoUrl 可由 taskId 重建(/v1/videos/{taskId}/content,后端每次代理/签名)。
// 初始加载 strip 会把 idb 引用剥成 '',若在 hydrate 未完成的窗口发生一次 persist,空值会覆盖
// localStorage 里的引用而永久丢失 → 渲染兜底成「生成中 100%」。这里对「已完成、有 taskId、
// videoUrl 为空」的消息用 taskId 重建直连 URL:内存里始终非空,persist 落的是可重建的直连
// URL(isDirectUrl 原样保留),localStorage 自愈;已损坏的历史数据加载即恢复。identity 保持:
// 无改动的 conv/message 原样返回,不破坏 hydrate 的引用比对。

// 持久化 pipeline 的兼容归一，两条。无需归一时返回原引用，保持 identity 不触发多余克隆。
//
// 一条是长期的：配音段的提示词字段在「配音改为复用视频提示词」那次改动里由 dubPrompt
// 更名为 prompt，改动前存下、且此刻仍在跑的流水线要在这里补一次，否则刷新后续跑配音段
// 会把用户当初填的提示词丢成空串。
//
// 另一条是**过渡期兜底**：更早那版两段流水线把结构存成 { srModel, interpolation }，
// 而现在下游只认 pipeline.upscale —— 不归一的话，那种数据在刷新后取不到下一段，会
// 停在低分辨率的第一段成品上。该特性未曾上线，理论上不存在这种数据，但 localStorage
// 在用户浏览器里、我们无从验证（开发/测试环境用过旧版的浏览器就可能残留），十行的
// 兜底比一个查不了的风险划算。**历史会话滚动淘汰（VIDEO_HISTORY_LIMIT=10 段）之后
// 即可连同这段注释一起删。**
const migratePipeline = (pipeline) => {
  if (!pipeline) return pipeline;
  if (!('upscale' in pipeline) && !('dub' in pipeline) && pipeline.srModel) {
    return {
      group: pipeline.group,
      upscale: {
        srModel: pipeline.srModel,
        interpolation: !!pipeline.interpolation,
      },
      dub: null,
    };
  }
  const dub = pipeline.dub;
  if (dub && dub.prompt === undefined && dub.dubPrompt !== undefined) {
    const { dubPrompt, ...rest } = dub;
    return { ...pipeline, dub: { ...rest, prompt: dubPrompt } };
  }
  return pipeline;
};

// 加载漏斗：重建 completed 视频的空 videoUrl + 迁移旧结构 pipeline（初始态与
// hydrate 两条路径都经此，一处覆盖所有 m.pipeline 读取点）。identity 保持：
// 无改动的 conv/message 原样返回，不破坏 hydrate 的引用比对。
const ensureVideoUrls = (list) => {
  if (!Array.isArray(list)) return list;
  return list.map((conv) => {
    let changed = false;
    const messages = (conv.messages || []).map((m) => {
      let nm = m;
      const migrated = migratePipeline(m.pipeline);
      if (migrated !== m.pipeline) {
        nm = { ...nm, pipeline: migrated };
        changed = true;
      }
      if (
        nm.role === 'assistant' &&
        nm.status === VIDEO_STATUS.COMPLETED &&
        nm.taskId &&
        !nm.videoUrl
      ) {
        nm = { ...nm, videoUrl: buildVideoContentUrl(nm.taskId) };
        changed = true;
      }
      return nm;
    });
    return changed ? { ...conv, messages } : conv;
  });
};

// 兼容 OpenAI 错误({error:{message}})与任务错误({code,message,data})两种形态
const extractApiErrMsg = (error, fallback) => {
  const d = error?.response?.data || {};
  return d.error?.message || d.message || error?.message || fallback;
};

// allowDub=false:整端关闭「生成后自动配音」。移动端用它——配音要额外排一次 v2a,
// 手机上等待更久、失败面更大,统一去桌面端做。它同时压住 dubAvailable(开关不出现、
// 残留的 on 状态被既有 effect 清掉)与 maybeDub(历史会话里存了 dubbing:true 的续问
// 也不会再接配音段)——只删 UI 开关堵不住后者。
export const useVideoGeneration = ({
  mode = 'text2video',
  // 尺寸/宽高比「发不发」与「展不展示」必须同源:两者都读中央元数据的 fields 声明
  // (tabHasField),而 tab 是按 category + mode 定位的 —— 「视频配乐」(dub)的 tab
  // 就挂在 audio 分类下。缺省 'video' 只是兜底:查不到 tab → fields 为空 → 一律不发,
  // 与改动前的行为一致,不会误发。
  category = 'video',
  allowDub = true,
  // allowBatch:是否允许一次生成多条(不同 seed 的候选)。**默认关,由调用方显式开**。
  // 两个前端应用共用这个 hook,多并发只在 web/classic 提供。
  // **判据是"哪个应用",不是"屏幕多宽"**:web/mobile 是独立应用,它调这个 hook 时不传
  // allowBatch(默认 false);web/classic 传 true。classic 里也有个 useIsMobile,但那是
  // 实时媒体查询、只管响应式布局 —— **不能拿它当这个闸门**:拖窄窗口就会翻转,等于纯
  // 视觉操作改变了发出去的请求和计费,会话中途还会把已锁定的条数悄悄降到 1。
  // 手机 UA 的用户主动选「前往电脑端」时拿到的就是 classic,那是他自己要的桌面能力。
  // 闸门必须在 hook 里而不是只靠"那个应用不给控件":续问时 batchCount 从**会话**读
  // (与 model/size/seed 同一模式),在 classic 生成过多条的对话在 web/mobile 打开续问
  // 会照着 conv.batchCount 并发提交,那边没有这个控件,用户看不见也拦不住。
  allowBatch = false,
} = {}) => {
  const { t } = useTranslation();
  const [statusState] = useContext(StatusContext);
  const [userState] = useContext(UserContext);

  const isI2V = mode === 'image2video';
  const isFLF2V = mode === 'flf2v';
  const isS2V = mode === 's2v';
  const isSR = mode === 'sr';
  const isVACE = mode === 'vace';
  const isR2VA = mode === 'r2va';
  const isDub = mode === 'dub';
  // 多条候选只对**生成类**玩法有意义:文生视频与关键帧是"同一提示词能出不同结果",
  // 换个 seed 就是另一个候选。超分/配音/数字人/VACE 是对给定素材做变换,多跑几遍
  // 只是把同一件事做 N 遍,既无候选可挑又白花 N 倍的钱。
  const supportsBatch = allowBatch && (mode === 'text2video' || isFLF2V);
  // 需要上传一张「主图」的模式:关键帧首帧、s2v 人物图(都复用 inputs.firstFrame)。
  // 图生视频(Bernini r2v)改用参考图 refImages,不再走 firstFrame。
  //
  // ⚠️ 这个仓库里有过两次同一个坑:曾用 `mode !== 'text2video'` 当「要不要下发
  // 尺寸/宽高比」的判据(叫 followsInput),覆盖面比实际需要的宽——它把参考生视频
  // (r2va)也划进去了,而 r2va 明明会下发尺寸/宽高比(只是不改图、不需要主图上传)。
  // 第一次是尺寸/宽高比的**展示与下发**闸门,已改用 tabHasField 产出的 sendsSize /
  // sendsAspectRatio;第二次是关键帧改图(composeImageToRatio)要用哪个判据来决定
  // "这个玩法会改图、所以宽高比不经参数下发",当时又写成了 followsInput,同一个洞
  // 又把 r2va 的宽高比下发挡掉了一次。教训是:**判据要精确等于要控制的那件事**,
  // "会改图/需要主图上传"就该用 needsImage,不能借用一个覆盖面更宽的相近概念。
  const needsImage = isFLF2V || isS2V;
  // 尺寸/宽高比下发闸,与 VideoConfigPanel 的展示闸同一判据(见上)。
  const sendsSize = tabHasField(category, mode, 'sizes');
  const sendsAspectRatio = tabHasField(category, mode, 'aspectRatios');
  // 采样步数闸:文生视频 / 关键帧 / 参考生视频三个玩法开放,展示与下发同读它。
  //
  // 不走 tabHasField:那个读的是运营在每个 tab 上配的字段(sizes/aspectRatios 之类),
  // 而步数是**模型级**的(defaultSteps 与 engine 同层,管理页上就没有 tab 维度),
  // 借它判会得出「运营没配过这个 tab 的 sizes,步数框也跟着消失」这种毫无关系的联动。
  const sendsSteps = VIDEO_STEPS_MODES.includes(mode);
  const taskType = modeMeta(mode).taskType; // s2v/sr 显式下发;vace(Bernini)按输入分流(见 isVACE 提交块),其余靠模型名推断
  const pageCapability = modeMeta(mode).capability;
  const storageKey = storageKeyFor(mode);

  const [inputs, setInputs] = useState({
    group: '',
    model: '',
    size: '',
    seconds: '',
    seed: '', // 随机种子;'' 表示随机(不下发)
    // 一次生成几条:多条 = 同一提示词、不同 seed 的候选。默认 1,与改造前一致。
    batchCount: PLAYGROUND_BATCH_DEFAULT,
    aspectRatio: '', // 宽高比;仅当该模型在后台配了宽高比才由 effect 选中默认值并下发
    // 画幅适配方式:画幅跟随输入的玩法(关键帧)选了具名比例时,原图怎么变成那个比例。
    // 只在那种情况下有意义,其余玩法这个值一直被忽略(见提交处 composeImageToRatio)。
    fitMode: FIT_BLUR,
    // 采样步数(高级参数)。切模型时由 effect 填成该模型配的 defaultSteps,用户可改。
    // '' = 不下发,由后端回落引擎族基座档 —— 运营没配 defaultSteps 时就是这个状态。
    steps: '',
    firstFrame: '', // i2v/flf2v 首帧 / s2v 人物图(base64 data-url)
    lastFrame: '', // flf2v 尾帧
    audioData: '', // s2v 驱动音频(base64 data-url)
    sourceVideo: '', // sr 源视频(base64 data-url)
    srRatio: 2, // sr 超分倍率(请求级,门面透传 metadata.sr_ratio)
    interpolation: false, // 插帧开关(默认关):开启才透传 metadata.target_fps,超分/配乐不适用
    dubbing: false, // 配音开关(默认关):开启则生成后接 v2a 配音段(文生/图生/视频编辑)
    srcVideo: '', // 视频编辑(Bernini)源视频(base64 data-url)
    // 老会话的第二源视频(mv2v/ads2v)。只由 openHistoryItem 写入、只在锁定态展示,
    // 没有上传口(见 VIDEO_MODES.vace);新会话恒为空。
    srcVideo2: '',
    refImages: [], // 视频编辑 rv2v / 图生视频 r2v 参考图(base64 data-url 数组)
    refVideos: [], // 参考生视频 r2va 参考视频(base64 data-url 数组;运营未开放则恒为空)
  });
  const [groups, setGroups] = useState([]);
  const [models, setModels] = useState([]);
  // 来自 /api/pricing：model -> enable_groups[]（用于分组过滤）
  const [modelGroupsMap, setModelGroupsMap] = useState(new Map());
  // 当前选中分组下后端权威可用模型全集（未按能力过滤）：判定配音/超分模型
  // 对该分组是否可用，与生成模型同一套来源（GetUserModels）。
  const [groupUsableModels, setGroupUsableModels] = useState([]);

  // 初值:同步剥掉未 hydrate 的 idb-media: 引用(避免首帧断图/裸引用误发后端);
  // 保留初始 conv 对象引用,mount 后 hydrate 完成再按引用逐条合并(不整体覆盖)。
  const initialConvsRef = useRef(null);
  const [conversations, setConversations] = useState(() => {
    const raw = loadConversations(storageKey);
    // strip 后立刻用 taskId 重建 completed 视频的空 videoUrl,再存进 initialConvsRef——
    // 保证 initialSet 与 state 引用一致(hydrate 的引用比对不被破坏)。
    const stripped = ensureVideoUrls(
      stripUnresolvedMediaRefs(raw, VIDEO_MEDIA_SCHEMA),
    );
    initialConvsRef.current = { raw, stripped };
    return stripped;
  });
  const [currentConvId, setCurrentConvId] = useState(null);
  const [generating, setGenerating] = useState(false);
  // 在途任务数是否已顶到 VIDEO_MAX_CONCURRENT_TASKS。与 generating 分开是因为两者
  // 含义已经不同：generating = 有任务在跑（进度条/停止按钮看它），taskSlotsFull =
  // 不能再发了（发送键/重新生成看它）。合成一个的话，一跑起来就全锁死，等于没放开。
  const [taskSlotsFull, setTaskSlotsFull] = useState(false);

  const messages = useMemo(() => {
    const conv = conversations.find((c) => c.id === currentConvId);
    return conv ? conv.messages : [];
  }, [conversations, currentConvId]);

  const locked = currentConvId !== null;

  const conversationsRef = useRef(conversations);
  conversationsRef.current = conversations;
  const lockedRef = useRef(locked);
  lockedRef.current = locked;
  const groupRef = useRef(inputs.group);
  groupRef.current = inputs.group;
  // 进行中的轮询槽，按 msgId 索引：msgId → { convId, msgId, taskId, timer, canceled }。
  //
  // 曾经是单个 ref（一次只能跑一个任务），所以上一条没出结果就发不出下一条。改成 Map
  // 后同时最多跑 VIDEO_MAX_CONCURRENT_TASKS 个，超了由 generate 提示。
  //
  // **键必须是 msgId 而不是 taskId**：流水线（生成→超分→配音）会在同一条消息上换 taskId
  // （submitPipelineStage），用 taskId 当键的话换一次就多一个槽、旧槽永远回收不掉。
  const activePollsRef = useRef(new Map());
  // 槽位是 ref（轮询回调里要读最新值，不能走渲染），派生的两个布尔量要手动同步到
  // state 供渲染用。每处增删槽位后都必须调它，否则界面会停在上一次的状态。
  const syncPollState = useCallback(() => {
    const n = activePollsRef.current.size;
    setGenerating(n > 0);
    setTaskSlotsFull(n >= VIDEO_MAX_CONCURRENT_TASKS);
  }, []);

  // mount 后从 IDB 还原媒体,按初始对象引用逐条合并——只替换"挂载至今未被任何 setState
  // 换过引用"的 conv(hydrate 期间用户新建/正在生成被 patch 的会话原样保留)。不整体覆盖。
  useEffect(() => {
    let canceled = false; // 兜 StrictMode dev 双挂载
    const init = initialConvsRef.current;
    if (!init || !(init.raw || []).length) return;
    (async () => {
      const hydrated = await hydrateConversationsFromStorage(
        init.raw,
        VIDEO_MEDIA_SCHEMA,
      );
      if (canceled) return;
      const hydratedById = new Map(hydrated.map((c) => [c.id, c]));
      const initialSet = new Set(init.stripped);
      // conv 级媒体字段(续问要复用):即使会话已被 resume-poll patch 过(换了引用),
      // 也要把这些字段从 hydrated 版本补回去,否则重载后进行中的任务完成后续问会误报
      // "媒体失效"(IDB 里其实还在)。
      const mediaFields = [
        ...VIDEO_MEDIA_SCHEMA.convArrayFields,
        ...VIDEO_MEDIA_SCHEMA.convStringFields,
      ];
      setConversations((prev) =>
        // hydrated 版本若 IDB blob 缺失,videoUrl 会是 '';外面再兜一层 taskId 重建,
        // 避免 completed 消息被还原成空 URL 而渲染成「生成中」。
        ensureVideoUrls(
          prev.map((c) => {
            const h = hydratedById.get(c.id);
            if (!h) return c;
            // 挂载至今未被换过引用 → 整条用 hydrated(含还原的媒体 + 原消息)。
            if (initialSet.has(c)) return h;
            // 已被 patch(如 resume-poll):只把 conv 级媒体字段还原到实时会话上,
            // 保留其实时消息/状态。
            const merged = { ...c };
            mediaFields.forEach((f) => {
              merged[f] = h[f];
            });
            return merged;
          }),
        ),
      );
    })();
    return () => {
      canceled = true;
    };
    // 挂载一次:storageKey 在本组件生命周期内固定(切 tab 整体重挂载)。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const handleInputChange = useCallback((key, value) => {
    if (lockedRef.current) return;
    setInputs((prev) => {
      const next = { ...prev, [key]: value };
      // 切模型时清尾帧:关键帧 tab 下 i2v/flf2v 两类模型共用这一组输入框,从 flf2v 切到
      // i2v 后尾帧槽不再渲染,残留值会变成看不见却仍在 state 里的脏数据。
      // 只有明确「不吃尾帧」的 i2v 模型才清:auto(H3 这类单 checkpoint 全能模型)
      // 的尾帧槽照样渲染,清掉等于用户切个模型就丢输入。
      if (
        key === 'model' &&
        keyframeModeOf(value, videoConfigRef.current) === 'i2v'
      )
        next.lastFrame = '';
      return next;
    });
  }, []);

  // 一键示例:标量参数(params)+ 文件(files:字段→素材 URL)一次性写入 inputs。
  // 文件 URL fetch→base64 data-url(与手动上传同形态);数组字段(refImages)逐个转。锁定时忽略。
  const applyExample = useCallback(
    async (ex) => {
      if (lockedRef.current || !ex || typeof ex !== 'object') return;
      try {
        // 载入示例 = 干净状态:先清空所有媒体输入字段,再套用本示例声明的。否则切换
        // 示例时上一组残留——尤其视频编辑 v2v/rv2v/r2v 字段组不同,残留的 srcVideo 会让
        // 「仅参考图」的 r2v 被自动分流误判为 rv2v。
        const patch = {
          firstFrame: '',
          lastFrame: '',
          audioData: '',
          sourceVideo: '',
          srcVideo: '',
          // 老会话看完再点示例时,把只读的第二源视频一并清掉(它没有上传口,留着只会
          // 在本次新会话锁定后冒出来一个不属于它的只读视频)。
          srcVideo2: '',
          refImages: [],
          refVideos: [],
          ...(ex.params || {}),
        };
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

  const videoConfig = useMemo(
    () => parseVideoModelConfig(statusState?.status?.VideoModelConfig),
    [statusState?.status?.VideoModelConfig],
  );
  // handleInputChange 的依赖必须保持为空(它被下游当稳定引用用),所以走 ref 读配置。
  const videoConfigRef = useRef(videoConfig);
  videoConfigRef.current = videoConfig;

  // 关键帧 tab 的三态(判据见 keyframeModeOf:先读运营声明,再退回名字):
  //   'flf2v' 尾帧必填 | 'i2v' 尾帧不可传 | 'auto' 两槽都可选、至少填一个
  // wan 的两态是硬约束(task 由引擎实例启动参数定死);'auto' 是 MiniMax H3 这类一个
  // checkpoint 同时吃首帧/尾帧/首尾帧的模型,靠 frame_indices 区分。
  const keyframeMode = keyframeModeOf(inputs.model, videoConfig);
  const isFlf2vSelected = keyframeMode === 'flf2v';
  // 两种「按输入派生」:auto 首尾两槽都可选(H3);auto_fl 首帧必填、尾帧可选
  // (Seedance 2.0 不支持仅尾帧)。
  const isKeyframeAutoFull = keyframeMode === 'auto';
  const isKeyframeAutoFirstOrBoth = keyframeMode === 'auto_fl';
  const isKeyframeAuto = isKeyframeAutoFull || isKeyframeAutoFirstOrBoth;
  // 尾帧槽渲染与否:flf2v 必填、两种 auto 可选,i2v 不渲染。
  const allowLastFrame = isFlf2vSelected || isKeyframeAuto;

  // 尺寸档位 = 原生档 + 超分档（运营在模型级 upscale 里配）。合成一份供 UI 与编排共用：
  // 超分档带着 srModel / fromSize，选择器的标识文案与提交时的起步档位读同一个推导结果，
  // 两处各推一次迟早推出不同答案。
  //
  // 两个前提缺一不可，否则只出原生档：
  //   - 该玩法真的会下发 size（sendsSize，即运营给这个 tab 配了尺寸档位）。画幅完全
  //     跟随输入、提交时不发 size 的玩法，长一个超分档出来只会点了不生效。
  //     ⚠️ 这里曾经卡的是 followsInput（mode !== 'text2video'），与下发侧的判据分叉：
  //     参考生视频(r2va)配了尺寸档位、提交时照常发 size，超分档却一个都不出——运营在
  //     后台配了超分规则，体验区看不到任何选项。判据统一为 sendsSize。
  //   - 模型勾了「自建引擎」。提交侧 maybeUpscale 的第一个条件就是 usePipeline，
  //     没勾却把超分档摆出来，用户选中后会被静默降级成普通档位 —— 界面承诺了一件
  //     提交时不会发生的事，比不显示更糟。
  // groupUsableModels 为空表示还没取到分组可用列表（初次渲染/切分组期间），此时传 null
  // 表示"不过滤"，避免超分档先闪一下再出现。
  const sizeChoices = useMemo(() => {
    // 被引擎硬约束锁死的玩法（关键帧：short_edge 恒 768）直接用锁定值，**不读运营
    // 配置**。不能只靠 admin 页锁住输入框：getSizesForVideoModel 是 tab 级 → 模型级
    // → 分类默认值三级回落，运营为文生视频配的 480P 会顺着回落链漏到关键帧上——
    // 那时关键帧的 tab 上一个字都没填，界面却摆出一个点了不生效的档位。
    // 锁按**引擎族**生效：H3/wan 的关键帧画布由引擎按首图推，锁死才对；LTX-2.5 认
    // 请求里的 width/height，锁不解就会把 '768P' 这个档位词发给它（清档位词的
    // h3DropResolutionToken 是 H3 专属的），引擎直接报错。见 flf2v tab 的注释。
    const lock = getTabFieldLock(
      category,
      mode,
      'sizes',
      getEngineForVideoModel(videoConfig, inputs.model),
    );
    const native =
      lock?.value || getSizesForVideoModel(videoConfig, inputs.model, mode);
    if (!sendsSize || !isPipelineModel(videoConfig, inputs.model)) {
      return native.map((s) => ({ value: s, label: s, isUpscale: false }));
    }
    // 可用性过滤只对**两段式**成立:它要真的去调那个超分模型,对当前分组不可用就
    // 该整档不出。一段式(纯放大)整条路上没有第二个模型,放大由引擎在出片前做 ——
    // 这时还按超分模型的可用性去筛,就会出现「运营把 SR 模型对某分组停用了,某个
    // 压根不需要它的模型的 1080P 档跟着消失」,而且是静默的。传 null = 不过滤。
    const filterBySrModel = !isNativeDeliveryModel(videoConfig, inputs.model);
    return buildVideoSizeChoices(
      videoConfig,
      inputs.model,
      native,
      filterBySrModel && groupUsableModels.length ? groupUsableModels : null,
    );
  }, [videoConfig, inputs.model, mode, category, sendsSize, groupUsableModels]);
  const availableSizes = useMemo(
    () => sizeChoices.map((c) => c.value),
    [sizeChoices],
  );
  const availableDurations = useMemo(
    () => getDurationsForVideoModel(videoConfig, inputs.model, mode),
    [videoConfig, inputs.model, mode],
  );
  // 宽高比档位。画幅由上传图决定的玩法(needsImage:关键帧/数字人)要在运营配的具名档前
  // 补一个「跟随上传素材」档并默认选中:那些玩法选具名比例是靠**把图改成该比例**实现的
  // (见提交处的 composeImageToRatio),会裁掉内容或加虚化边,得是用户主动点的,不能由
  // 默认值替他打开。
  //
  // ⚠️ 判据必须是 needsImage,不是 followsInput —— 后者 = mode !== 'text2video',
  // 覆盖面比"会改图的玩法"宽得多,参考生视频(r2va)也在其中。r2va 的画幅走的是原生
  // aspect_ratio 直发(见下面提交处的注释),不改图、没有"跟随上传素材"这回事,补错了
  // 会把它的宽高比选择器插进一个不存在的默认档。needsImage 精确等于会走 compose 的
  // 那几个玩法(目前只有 flf2v 声明了 aspectRatios,s2v 没有)。
  const availableAspectRatios = useMemo(() => {
    const configured = getAspectRatiosForVideoModel(
      videoConfig,
      inputs.model,
      mode,
    );
    if (!configured.length || !needsImage) return configured;
    return [VIDEO_ASPECT_RATIO_AUTO, ...configured];
  }, [videoConfig, inputs.model, mode, needsImage]);
  // 该模型配的采样步数(模型级,不随 mode 变)。null = 运营没配。
  const modelDefaultSteps = useMemo(
    () => getDefaultStepsForVideoModel(videoConfig, inputs.model),
    [videoConfig, inputs.model],
  );
  // 输入文件大小上限(MB;0=不限)。i2v/flf2v/s2v/sr/vace 上传帧图/音频/视频的护栏。
  const maxInputMB = useMemo(
    () => getMaxInputMBForModel(videoConfig, inputs.model, mode),
    [videoConfig, inputs.model, mode],
  );
  // 驱动音频时长上限(秒;0=不限)。只对数字人有意义:产出视频长度就是音频长度。
  const maxAudioSec = useMemo(
    () => getMaxAudioSecForModel(videoConfig, inputs.model, mode),
    [videoConfig, inputs.model, mode],
  );

  // 参考素材三模态各自的闸,全部可由运营按 tab 配(见 playgroundAdmin 的 FIELD_META)。
  //
  // **刻意不做跨模态总数闸**:引擎那边确实还有一道(H3 是 12),但由服务端兜底
  // (adaptor 的 maxR2VARefTotal),前端重复实现只会让运营去凑一个他并不关心的总数。
  //
  // **配置只能收窄,不能突破内置上限**:下面一律 Math.min。内置值就是引擎/产品的
  // 天花板,配得更大不会让请求通过,只会开出一批发出去必被拒的槽位 —— 那种错要等到
  // 用户传满了才暴露,比当场少给一个槽难查得多。
  const maxRefImages = useMemo(() => {
    // 未配时的内置默认:各玩法本来就不同,不能共用一个常量。
    const ceiling = isI2V
      ? MAX_R2V_REF_IMAGES
      : isR2VA
        ? MAX_R2VA_REF_IMAGES
        : MAX_REF_IMAGES;
    const configured = Math.min(
      getMaxRefImagesForModel(videoConfig, inputs.model, mode, ceiling),
      ceiling,
    );
    // 「0=不开放该模态」对参考生视频(可以纯参考视频)和视频编辑(参考图本就可选)成立,
    // 但对图生视频不成立 —— 参考图是它**唯一**的视觉输入,配成 0 不是「关掉一个模态」,
    // 而是把整个玩法变成死胡同:控件不渲染、missingRequiredImage 又要求必须有图,
    // 发送键从此永远灰着且没有任何上传入口。要停用这个玩法应该去关 tab,不是把上限填 0。
    return isI2V ? Math.max(1, configured) : configured;
  }, [videoConfig, inputs.model, mode, isI2V, isR2VA]);
  // 参考视频个数:0 = 运营没开放,上传框整个不渲染(纯 opt-in)。
  const maxRefVideos = useMemo(
    () =>
      Math.min(
        getMaxRefVideosForModel(videoConfig, inputs.model, mode),
        MAX_R2VA_REF_VIDEOS,
      ),
    [videoConfig, inputs.model, mode],
  );
  const refVideoMaxMB = useMemo(
    () => getRefVideoMaxMBForModel(videoConfig, inputs.model, mode),
    [videoConfig, inputs.model, mode],
  );
  const refVideoMaxSec = useMemo(
    () => getRefVideoMaxSecForModel(videoConfig, inputs.model, mode),
    [videoConfig, inputs.model, mode],
  );

  // 上限变小时裁掉超出的参考素材。
  //
  // **不裁的后果是「看不见却照发」**:参考视频的槽位按 maxRefVideos 逐个渲染,cap 一降
  // 超出的那几个就只是不再显示,而提交侧拿的是整个数组(filter(Boolean)),照样发给后端 ——
  // 用户看不见的素材在参与生成,报错也指不到地方。参考图在 cap=0(控件整个不渲染)时同理。
  //
  // 盯 cap 而不是盯「换模型」:这两个值现在是**模型级**的(以前只随 tab 变,所以同一个
  // tab 内换模型不会出问题,这是本轮新引入的面),而让它们变化的路不止模型下拉——配置
  // 晚到、运营改完配置刷新回来都算。盯着结果比枚举原因可靠。
  //
  // 先 filter 再 slice:用户可能只填了第 3 个槽,直接截断会把它切掉,压紧后再截才留得住。
  useEffect(() => {
    // 锁定态是在看历史会话,裁剪等于改写记录。
    if (lockedRef.current) return;
    setInputs((prev) => {
      const imgs = (prev.refImages || []).filter(Boolean);
      const vids = (prev.refVideos || []).filter(Boolean);
      if (imgs.length <= maxRefImages && vids.length <= maxRefVideos) {
        return prev;
      }
      return {
        ...prev,
        refImages: imgs.slice(0, maxRefImages),
        refVideos: vids.slice(0, maxRefVideos),
      };
    });
  }, [maxRefImages, maxRefVideos]);

  // 「AI 优化提示词」要知道的两件事:所选模型属哪个引擎族(H3 换一套分段结构的系统
  // 提示词),以及本次请求的输入形态与时长(H3 靠它区分 I2VA / L2VA / FL2VA,并写出
  // 对齐指令里那个两位小数的时长)。非 H3 模型 context 为空,行为与改动前一致。
  const optimizeEngine = getEngineForVideoModel(videoConfig, inputs.model);
  // 对齐指令要用到的原始事实(不是给模型看的文本,是给前端本地拼接用的)。用户不点
  // 「AI 优化」直接发送时,提示词框那边靠它本地拼出 I2VA / FL2VA / L2VA 的对齐指令
  // —— 那几句是纯模板 + 已知时长,前端能 100% 拼对,而它恰恰是错了代价最大、用户最
  // 不可能自己写对的一段。
  const h3AlignContext = useMemo(
    () => ({
      tabKey: mode,
      seconds: inputs.seconds,
      hasFirstFrame: !!(inputs.firstFrame || '').trim(),
      hasLastFrame: !!(inputs.lastFrame || '').trim(),
    }),
    [mode, inputs.seconds, inputs.firstFrame, inputs.lastFrame],
  );
  const optimizeContext = useMemo(
    () =>
      optimizeEngine === VIDEO_ENGINE_MINIMAX_H3
        ? buildH3OptimizeContext({
            tabKey: mode,
            seconds: inputs.seconds,
            hasFirstFrame: !!(inputs.firstFrame || '').trim(),
            hasLastFrame: !!(inputs.lastFrame || '').trim(),
            refImageCount: (inputs.refImages || []).filter(Boolean).length,
            refVideoCount: (inputs.refVideos || []).filter(Boolean).length,
            hasRefAudio: !!(inputs.audioData || '').trim(),
          })
        : '',
    [
      optimizeEngine,
      mode,
      inputs.seconds,
      inputs.firstFrame,
      inputs.lastFrame,
      inputs.refImages,
      inputs.refVideos,
      inputs.audioData,
    ],
  );

  // 视频模型集合 = 管理员在「视频模型配置」里声明、且能力含「文生视频」的模型。
  // 只认运营设置里的能力声明，不再按后端端点类型识别。
  const videoModelSet = useMemo(() => {
    // 兼容旧能力标签:重命名前用旧标签(音频驱动/视频转视频/参考生视频)配过的模型仍能
    // 匹配到对应新 Tab,不会从体验区消失。
    const legacy = VIDEO_CAPABILITY_LEGACY_ALIASES[pageCapability];
    const set = new Set();
    Object.entries(videoConfig.models || {}).forEach(([model, cfg]) => {
      const caps = Array.isArray(cfg?.capabilities) ? cfg.capabilities : [];
      if (caps.includes(pageCapability) || (legacy && caps.includes(legacy))) {
        set.add(model);
      }
    });
    return set;
  }, [videoConfig]);

  // 当前选中的生成模型是否跑在自建 gpustackplus 引擎上（后台按模型勾选）。自动超分/
  // 自动配音/插帧都只对它成立，其余渠道原样透传，见 isPipelineModel 的注释。这里按
  // inputs.model 算，只用于 UI 门控（开关与提示要不要出现）；提交时的权威判据按
  // params.model 另算，见 generate——锁定会话/续问时权威值在 params 而非当前选中项。
  const pipelineModel = useMemo(
    () => isPipelineModel(videoConfig, inputs.model),
    [videoConfig, inputs.model],
  );

  // 配音开关是否可用：模型是自建流水线模型 + 当前模式支持配音流水线 + 选中分组的可用
  // 模型里有「视频配乐」能力模型（从分组可用列表按能力挑，兼容多配音模型按分组分别
  // 启用）。超分/配音的具体模型在提交时从 params.group 的权威可用列表按能力挑，见 generate。
  const dubAvailable = useMemo(
    () =>
      DUB_PIPELINE_ENABLED &&
      allowDub &&
      pipelineModel &&
      DUB_PIPELINE_MODES.includes(mode) &&
      !!findCapabilityModelIn(
        videoConfig,
        groupUsableModels,
        VIDEO_DUB_CAPABILITY,
      ),
    [allowDub, pipelineModel, mode, videoConfig, groupUsableModels],
  );

  const videoGroups = useMemo(() => {
    const set = new Set();
    videoModelSet.forEach((model) => {
      (modelGroupsMap.get(model) || []).forEach((g) => set.add(g));
    });
    return set;
  }, [videoModelSet, modelGroupsMap]);

  // size 合法性（锁定时不动）
  useEffect(() => {
    if (locked) return;
    if (!availableSizes.length) {
      // 未配尺寸的模型（如图生视频/首尾帧或未配置的文生视频）清空残留，避免误发旧 size
      if (inputs.size !== '') setInputs((prev) => ({ ...prev, size: '' }));
      return;
    }
    if (!availableSizes.includes(inputs.size)) {
      setInputs((prev) => ({ ...prev, size: availableSizes[0] }));
    }
  }, [availableSizes, inputs.size, locked]);

  // seconds 合法性
  useEffect(() => {
    if (locked) return;
    if (
      availableDurations.length &&
      !availableDurations.includes(inputs.seconds)
    ) {
      setInputs((prev) => ({ ...prev, seconds: availableDurations[0] }));
    }
  }, [availableDurations, inputs.seconds, locked]);

  // 宽高比合法性(锁定时不动):该模型配了宽高比 → 当前值非法则选默认(优先 16:9,否则首项);
  // 未配置 → 清空(不展示、不下发)。纯 opt-in,不给不支持的模型强塞。
  useEffect(() => {
    if (locked) return;
    if (availableAspectRatios.length === 0) {
      if (inputs.aspectRatio !== '') {
        setInputs((prev) => ({ ...prev, aspectRatio: '' }));
      }
      return;
    }
    if (!availableAspectRatios.includes(inputs.aspectRatio)) {
      // 有「跟随上传素材」档就选它:那些玩法的画幅本来就跟随上传的图,默认落到 16:9
      // 会让每条任务都去改图(裁掉内容或加虚化边),而用户什么都没选。
      const next = availableAspectRatios.includes(VIDEO_ASPECT_RATIO_AUTO)
        ? VIDEO_ASPECT_RATIO_AUTO
        : availableAspectRatios.includes(VIDEO_DEFAULT_ASPECT_RATIO)
          ? VIDEO_DEFAULT_ASPECT_RATIO
          : availableAspectRatios[0];
      setInputs((prev) => ({ ...prev, aspectRatio: next }));
    }
  }, [availableAspectRatios, inputs.aspectRatio, locked]);

  // 采样步数的默认值:切模型时填成该模型在「视频模型配置」里配的 defaultSteps
  // (没配则留空 = 不下发,后端回落引擎族基座档)。
  //
  // 判据是「模型变了」而不是「当前值 != 默认值」:后者会在用户每次输入后立刻把值冲回
  // 默认,框子根本改不动。用 ref 记住上次应用默认值的模型,同一模型内不再干预。
  const stepsModelRef = useRef(null);
  useEffect(() => {
    if (locked) return;
    if (stepsModelRef.current === inputs.model) return;
    stepsModelRef.current = inputs.model;
    setInputs((prev) => ({
      ...prev,
      steps: modelDefaultSteps == null ? '' : modelDefaultSteps,
    }));
  }, [inputs.model, modelDefaultSteps, locked]);

  // 配音开关不再可用（切到无配音模型的分组/模式）时关掉残留的 on 状态，
  // 避免开关隐藏后 inputs.dubbing 仍为 true 导致的困惑（锁定的会话不动）。
  useEffect(() => {
    if (locked) return;
    if (!dubAvailable && inputs.dubbing) {
      setInputs((prev) => ({ ...prev, dubbing: false }));
    }
  }, [dubAvailable, inputs.dubbing, locked]);

  // 同理:切到非自建流水线模型时插帧开关会消失(target_fps 是自建引擎的字段),
  // 关掉残留的 on 状态，免得开关看不见却还留着（锁定的会话不动）。
  useEffect(() => {
    if (locked) return;
    if (!pipelineModel && inputs.interpolation) {
      setInputs((prev) => ({ ...prev, interpolation: false }));
    }
  }, [pipelineModel, inputs.interpolation, locked]);

  const loadPricing = useCallback(async () => {
    try {
      const payload = await cachedGet(VIDEO_API_ENDPOINTS.PRICING, {
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
      // 留空：分组不再按 enable_groups 收窄（模型仍按能力声明过滤）
    }
  }, []);

  const loadGroups = useCallback(async () => {
    try {
      const { success, data } = await cachedGet(
        VIDEO_API_ENDPOINTS.USER_GROUPS,
      );
      if (!success) return;
      const userGroup =
        userState?.user?.group ||
        JSON.parse(localStorage.getItem('user') || '{}')?.group;
      let groupOptions = processGroupsData(data, userGroup);
      const allowAllGroups = videoGroups.has('all');
      if (videoGroups.size > 0 && !allowAllGroups) {
        groupOptions = groupOptions.filter(
          (g) => videoGroups.has(g.value) || g.value === 'auto',
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
  }, [userState, videoGroups, t]);

  const loadModels = useCallback(async () => {
    const requestedGroup = inputs.group;
    try {
      const { success, data } = await getUserModelsCached(requestedGroup);
      if (!success) return;
      // 分组在等待响应期间已切换(初始 '' → 用户分组 → 按视频模型过滤后的分组会连续
      // 变化数次):过期响应直接丢弃,否则旧分组的空结果会最后到达并覆盖正确的模型列表。
      if (requestedGroup !== groupRef.current) return;
      let list = Array.isArray(data) ? data : [];
      // 存该分组可用模型全集（未按能力过滤）供配音/超分开关的分组可用性判定
      setGroupUsableModels(list);
      list = list.filter((m) => videoModelSet.has(m));
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
  }, [inputs.group, inputs.model, videoModelSet, t]);

  useEffect(() => {
    if (userState?.user) loadPricing();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [userState?.user]);
  useEffect(() => {
    if (userState?.user) loadGroups();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [userState?.user, videoGroups]);
  useEffect(() => {
    if (userState?.user) loadModels();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [userState?.user, inputs.group, videoModelSet]);

  // 挂载后为仍在进行中的任务恢复轮询（刷新/重进页面不丢进度）。
  //
  // 从「只恢复最近一个」改成「按时间倒序恢复最多 VIDEO_MAX_CONCURRENT_TASKS 个」：
  // 既然允许同时跑 3 个，刷新后只捡回一个的话，另外两个的进度会永久冻结在最后一次
  // 写入的百分比上（任务其实还在后端跑，只是没人再问它）。
  useEffect(() => {
    if (!userState?.user || activePollsRef.current.size) return;
    const pending = []; // { convId, msgId, taskId, ts }
    conversationsRef.current.forEach((conv) => {
      (conv.messages || []).forEach((m) => {
        if (
          m.role === 'assistant' &&
          m.taskId &&
          (m.status === VIDEO_STATUS.QUEUED ||
            m.status === VIDEO_STATUS.IN_PROGRESS)
        ) {
          const ts = Number(String(m.id).split('-')[1]) || 0;
          pending.push({ convId: conv.id, msgId: m.id, taskId: m.taskId, ts });
        }
      });
    });
    pending
      .sort((a, b) => b.ts - a.ts)
      .slice(0, VIDEO_MAX_CONCURRENT_TASKS)
      .forEach((p) => resumePoll(p.convId, p.msgId, p.taskId));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [userState?.user]);

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

  const turnsUsed = useMemo(
    () => messages.filter((m) => m.role === 'user').length,
    [messages],
  );
  const turnLimitReached = turnsUsed >= VIDEO_CONV_TURN_LIMIT;

  // 收掉某一条消息的轮询槽。generating / taskSlotsFull 都由「还剩几个槽」派生，
  // 不能再无脑置 false：三个任务在跑时，先完成的那个若把 generating 关掉，
  // 界面会显示成全部结束。
  const finishPoll = useCallback(
    (msgId) => {
      const slot = activePollsRef.current.get(msgId);
      if (slot?.timer) clearTimeout(slot.timer);
      activePollsRef.current.delete(msgId);
      syncPollState();
    },
    [syncPollState],
  );

  // submitPipelineStage 定义在 pollOnce 之前但要调度它，经 ref 间接引用
  const pollOnceRef = useRef(null);

  // 查找会话内消息（读取流水线状态用）
  const findConvMessage = useCallback((convId, msgId) => {
    const conv = conversationsRef.current.find((c) => c.id === convId);
    return conv?.messages?.find((m) => m.id === msgId) || null;
  }, []);

  // 流水线阶段顺序：生成 → [超分] → [配音]。返回当前 stage 之后应跑的下一段，
  // 无则 null（结束落终态）。
  const nextPipelineStage = (stage, pipeline) => {
    if (!pipeline) return null;
    if (stage === 'generating') {
      if (pipeline.upscale) return 'upscaling';
      if (pipeline.dub) return 'dubbing';
    } else if (stage === 'upscaling') {
      if (pipeline.dub) return 'dubbing';
    }
    return null;
  };

  // 提交流水线的某一后置阶段（超分/配音）：用 task:<id> 引用上一段产物，
  // 成功则把轮询槽切到新任务继续轮询。失败返回 false，由调用方降级展示上一段成品。
  const submitPipelineStage = useCallback(
    async (convId, msgId, prevTaskId, pipeline, stage) => {
      // 超分段：倍率发定值（靠引擎按部署 config 的目标尺寸封顶）+ resize_mode 让输出
      // 落在精确目标尺寸上 + 可选插帧 target_fps；配音段：v2a，透传源视频。
      // 两个字段都随 metadata 展开成请求体顶层，门面原样转交引擎（既不在它的控制键里，
      // 也不属于它自己拥有的路径字段）。
      const metadata =
        stage === 'upscaling'
          ? {
              task_type: 'sr',
              video: `task:${prevTaskId}`,
              sr_ratio: VIDEO_SR_RATIO_UNCAPPED,
              resize_mode: VIDEO_SR_RESIZE_MODE,
              // 目标档位写成精确像素串时才有值(见 upscaleTargetShortEdge)。有它引擎
              // 按源的真实画幅等比放大到该短边,没有就退回原来的「按部署 config 档位
              // 封顶」——所以既有的 1080P 档位词规则行为不变,老会话(pipeline 里没这个
              // 字段)也不受影响。
              ...(pipeline.upscale?.targetShortEdge > 0
                ? { target_short_edge: pipeline.upscale.targetShortEdge }
                : {}),
              ...(INTERPOLATION_ENABLED && pipeline.upscale?.interpolation
                ? { target_fps: VIDEO_INTERPOLATION_TARGET_FPS }
                : {}),
            }
          : { task_type: 'v2a', video: `task:${prevTaskId}` };
      const model =
        stage === 'upscaling'
          ? pipeline.upscale?.srModel
          : pipeline.dub?.dubModel;
      // 配音段沿用生成该视频的提示词（见 pipeline.dub 构造处）。超分段无提示词。
      const stagePrompt = stage === 'dubbing' ? pipeline.dub?.prompt || '' : '';
      // 面向用户不说「超分」这个行话，与进度条的阶段名（画质增强）保持同一个词。
      const failMsg =
        stage === 'upscaling'
          ? t('画质增强未能开始，已为你保留原始分辨率的视频')
          : t('配音未能开始，已为你保留无配音的视频');
      try {
        const res = await API.post(
          VIDEO_API_ENDPOINTS.VIDEO_GENERATIONS,
          {
            model,
            group: pipeline.group || undefined,
            prompt: stagePrompt,
            metadata,
          },
          { skipErrorHandler: true },
        );
        const data = res.data || {};
        const inner = data.data || {};
        const nextTaskId = data.id || data.task_id || inner.task_id || inner.id;
        if (!nextTaskId) {
          throw new Error(
            data.message || data.error?.message || 'submit stage failed',
          );
        }
        patchConvMessage(convId, msgId, {
          taskId: nextTaskId,
          stage,
          status: VIDEO_STATUS.IN_PROGRESS,
          progress: 0,
        });
        // 换 taskId 但**不换槽**：槽按 msgId 索引，流水线的下一段仍属同一条消息，
        // 不占用新的并发名额。
        const cur = activePollsRef.current.get(msgId);
        if (cur && !cur.canceled) {
          cur.taskId = nextTaskId;
          cur.timer = setTimeout(
            () => pollOnceRef.current(convId, msgId, nextTaskId, 1),
            VIDEO_POLL_INTERVAL_MS,
          );
        }
        return true;
      } catch (e) {
        // 把后端的具体原因带出来：额度/积分不足是这一步最常见的失败，只说「未能开始」
        // 用户会以为是系统故障，看到「额度不足」才知道该去充值。
        const detail =
          e?.response?.data?.message ||
          e?.response?.data?.error?.message ||
          e?.message ||
          '';
        showError(detail ? `${failMsg}（${detail}）` : failMsg);
        return false;
      }
    },
    [patchConvMessage, t],
  );

  const pollOnce = useCallback(
    async (convId, msgId, taskId, count) => {
      const active = activePollsRef.current.get(msgId);
      if (!active || active.canceled || active.taskId !== taskId) return;
      try {
        const res = await API.get(
          `${VIDEO_API_ENDPOINTS.VIDEO_FETCH}/${encodeURIComponent(taskId)}`,
          { skipErrorHandler: true },
        );
        const data = res.data || {};
        // 兼容 OpenAIVideo（顶层）与通用 TaskResponse（data.data）两种形态
        const inner = data.data || {};
        const status = normalizeVideoStatus(data.status || inner.status);
        const progress = parseProgress(
          data.progress != null ? data.progress : inner.progress,
        );

        if (status === VIDEO_STATUS.COMPLETED) {
          // 流水线：当前段完成 → 若还有后置段（超分/配音）则不落终态、自动提交下一段。
          // 页面中途关闭再回来时，恢复轮询到这里同样会按 stage 续走剩余段。
          const msg = findConvMessage(convId, msgId);
          const next = nextPipelineStage(msg?.stage, msg?.pipeline);
          if (next) {
            const switched = await submitPipelineStage(
              convId,
              msgId,
              taskId,
              msg.pipeline,
              next,
            );
            if (switched) return;
            // 下一段提交失败：降级展示当前段成品（已生成的产物不浪费）
          }
          patchConvMessage(convId, msgId, {
            status: VIDEO_STATUS.COMPLETED,
            progress: 100,
            videoUrl: buildVideoContentUrl(taskId),
          });
          finishPoll(msgId);
          return;
        }
        if (status === VIDEO_STATUS.FAILED) {
          const msg =
            data.error?.message ||
            inner.error?.message ||
            inner.fail_reason ||
            data.fail_reason ||
            t('视频生成失败');
          patchConvMessage(convId, msgId, {
            status: VIDEO_STATUS.FAILED,
            error: msg,
          });
          showError(msg);
          finishPoll(msgId);
          return;
        }
        // queued / in_progress
        patchConvMessage(convId, msgId, {
          status: status || VIDEO_STATUS.IN_PROGRESS,
          ...(progress !== undefined ? { progress } : {}),
        });
        if (count >= VIDEO_POLL_MAX_TIMES) {
          // 客户端轮询超时：不判失败，保留可恢复状态，仅标记以便展示「继续获取」；
          // 任务可能仍在后端进行/已完成，用原 taskId 续查即可，无需重新提交。
          patchConvMessage(convId, msgId, { pollTimedOut: true });
          finishPoll(msgId);
          return;
        }
      } catch (e) {
        // 轮询瞬时错误：继续重试直至超时
        if (count >= VIDEO_POLL_MAX_TIMES) {
          patchConvMessage(convId, msgId, { pollTimedOut: true });
          finishPoll(msgId);
          return;
        }
      }
      const cur = activePollsRef.current.get(msgId);
      if (!cur || cur.canceled || cur.taskId !== taskId) return;
      cur.timer = setTimeout(
        () => pollOnce(convId, msgId, taskId, count + 1),
        VIDEO_POLL_INTERVAL_MS,
      );
    },
    [patchConvMessage, finishPoll, t, findConvMessage, submitPipelineStage],
  );
  pollOnceRef.current = pollOnce;

  // 为某个仍在进行中的任务（重新）启动轮询：刷新页面或切走再回来时用，
  // 避免进度冻结在最后一次写入的值。已在轮询同一任务则跳过。
  //
  // 只认这条消息自己的槽（按 msgId），不再看「有没有别的任务在跑」——同时轮询多个
  // 正是要的行为。重复调用（如刷新恢复 + openHistoryItem 撞在一起）会先清掉旧定时器
  // 再重建，不会留下两个定时器对同一条消息重复轮询。
  const resumePoll = useCallback(
    (convId, msgId, taskId) => {
      if (!taskId) return;
      const active = activePollsRef.current.get(msgId);
      if (active && active.taskId === taskId && !active.canceled) return;
      if (active?.timer) clearTimeout(active.timer);
      // 重新轮询即回到「生成中」，清掉超时标记
      patchConvMessage(convId, msgId, { pollTimedOut: false });
      const slot = {
        convId,
        msgId,
        taskId,
        timer: null,
        canceled: false,
      };
      activePollsRef.current.set(msgId, slot);
      syncPollState();
      slot.timer = setTimeout(
        () => pollOnce(convId, msgId, taskId, 1),
        VIDEO_POLL_INTERVAL_MS,
      );
    },
    [pollOnce, patchConvMessage, syncPollState],
  );

  // 超时任务「继续获取」：用原 taskId 续查当前会话中的该消息（方案 A：直接顶掉当前轮询槽）
  const refetch = useCallback(
    (msgId, taskId) => {
      if (currentConvId == null || !taskId) return;
      resumePoll(currentConvId, msgId, taskId);
    },
    [currentConvId, resumePoll],
  );

  const generate = useCallback(
    async (prompt) => {
      // 视频超分无提示词框(输出完全由源视频决定),允许空提示词提交;视频配乐提示词
      // 可选(空提示词=让模型按画面自由配环境音);其余模式必填。
      const text = (prompt || '').trim();
      if (!text && !isSR && !isDub) return;
      // 并发闸：此前是「有任务在跑就一律不让发」，现在按在途任务数放到
      // VIDEO_MAX_CONCURRENT_TASKS。读 ref 而不是 taskSlotsFull state —— 连点两下
      // 发送时第二下拿到的 state 还是上一次渲染的旧值，会漏放一个进来。
      // 一次要占几个槽:多条候选每条各占一个。档位上限(3)= 这个闸,所以空闲时选
      // 满档正好填满、不越界;但已有任务在跑时就会不够 —— 那时**整批拒掉**而不是
      // 截断成能塞下的条数:用户选了 3 条,悄悄只发 2 条比直接说"发不了"更难查。
      const wantSlots = supportsBatch
        ? normalizeBatchCount(inputs.batchCount)
        : 1;
      if (
        activePollsRef.current.size + wantSlots >
        VIDEO_MAX_CONCURRENT_TASKS
      ) {
        showError(
          wantSlots > 1
            ? t(
                '最多同时进行 {{count}} 个视频任务，当前还剩 {{free}} 个空位，放不下这 {{want}} 条',
                {
                  count: VIDEO_MAX_CONCURRENT_TASKS,
                  free: Math.max(
                    0,
                    VIDEO_MAX_CONCURRENT_TASKS - activePollsRef.current.size,
                  ),
                  want: wantSlots,
                },
              )
            : t('最多同时进行 {{count}} 个视频任务，请等其中一个完成后再发', {
                count: VIDEO_MAX_CONCURRENT_TASKS,
              }),
        );
        return;
      }

      // 关键帧:images=[首帧(,尾帧)];s2v:images=[人物图]。
      // 后续追问沿用对话首条锁定的帧图 / 媒体输入。
      let convImages = [];
      // 新增能力的媒体输入(base64),与帧图一起锁进对话、随对话复用、落盘前剥离。
      let media = {
        audioData: '',
        sourceVideo: '',
        srRatio: 2,
        srcVideo: '',
        refImages: [],
        refVideos: [],
      };
      let convId = currentConvId;
      let params;
      if (convId == null) {
        if (!inputs.model) {
          showError(t('请先选择一个视频模型'));
          return;
        }
        if (needsImage) {
          const first = (inputs.firstFrame || '').trim();
          const last = (inputs.lastFrame || '').trim();
          if (isFLF2V && isKeyframeAutoFull) {
            // 单 checkpoint 全能模型(H3):首尾两槽都可选,至少填一个。
            // **首帧不再是必填** —— 只给尾帧(l2va)是合法玩法,引擎按
            // frame_indices=[-1] 反推开头。所以这里不能套用下面那条「必须有首帧」。
            if (!first && !last) {
              showError(t('请至少上传首帧或尾帧其中一张'));
              return;
            }
            // 只给尾帧时 images 只有一张,由 metadata.task_type=l2va 表明它是尾帧
            // (输入形态与 i2v 完全一样,后端靠张数分不出来)。
            convImages = [first, last].filter(Boolean);
          } else if (!first) {
            showError(isS2V ? t('请先上传人物图') : t('请先上传首帧图片'));
            return;
          } else if (isFLF2V && isFlf2vSelected) {
            // 首尾帧模型:尾帧必填。引擎实例的 task 由启动期 --task 定死,缺尾帧发过去
            // 会读空路径直接崩,不能就地降级成 i2v(那是另一个实例、另一档 shift/插帧配置)。
            if (!last) {
              showError(t('该模型是首尾帧模型,需要上传尾帧图片'));
              return;
            }
            convImages = [first, last];
          } else if (isFLF2V && isKeyframeAutoFirstOrBoth) {
            // auto_fl(Seedance 2.0):首帧必填(上面那条 !first 已挡)、尾帧可选。
            // **这条分支不能省** —— 少了它 auto_fl 会掉进下面的 i2v 兜底,尾帧被静默
            // 丢弃,而 keyframeTaskType 那侧照旧按 isKeyframeAuto 派生出 flf2v:
            // 上游收到的是「task_type=flf2v + 只有一张 first_frame」,合法、不报错、
            // 只按首帧生成 —— 用户传了尾帧却看不出没生效。
            convImages = [first, last].filter(Boolean);
          } else {
            // i2v 模型(含关键帧 tab 下选中的 i2v 模型):只发首帧。切模型时 lastFrame 已被
            // 清空,这里再兜一道——多发的尾帧会被门面 400 拒(adaptor.go 的 i2v 反向防呆)。
            convImages = [first];
          }
        }
        // 数字人:必填驱动音频;超分:必填源视频;图生视频:必填参考图;
        // 视频编辑:必填至少 1 个源视频(仅参考图的 r2v 已迁到图生视频)。
        if (isS2V && !(inputs.audioData || '').trim()) {
          showError(t('数字人需要上传驱动音频'));
          return;
        }
        if (isSR && !(inputs.sourceVideo || '').trim()) {
          showError(t('视频超分需要上传源视频'));
          return;
        }
        if (isDub && !(inputs.sourceVideo || '').trim()) {
          showError(t('视频配音需要上传待配音视频'));
          return;
        }
        if (isI2V && !(inputs.refImages || []).filter(Boolean).length) {
          // 与 r2va 分开写:两个玩法共用参考图控件,但张数上限不同(r2v 3 张是产品
          // 档位,r2va 9 张是 H3 ∩ Seedance 的交集),文案不能共用。
          showError(t('图生视频需要上传 1~3 张参考图'));
          return;
        }
        // 参考生视频:视觉参考「图或视频至少其一」——引擎允许纯参考视频(adaptor 的
        // materializeR2VAInputs 判的正是 len(images)==0 && len(videos)==0)。
        // 音色参考是可选的,但**不能单独出现**:两家引擎的规则一致(独立音频必须搭配
        // 视觉参考),这里就地拦下,免得写完 NFS 占了队列槽才被拒。
        if (
          isR2VA &&
          !(inputs.refImages || []).filter(Boolean).length &&
          !(inputs.refVideos || []).filter(Boolean).length
        ) {
          showError(
            maxRefVideos > 0
              ? t('参考生视频需要上传至少 1 张参考图或 1 个参考视频')
              : t('参考生视频需要上传至少 1 张参考图'),
          );
          return;
        }
        if (isVACE && !(inputs.srcVideo || '').trim()) {
          showError(t('视频编辑需要上传源视频'));
          return;
        }
        media = {
          audioData: (inputs.audioData || '').trim(),
          sourceVideo: (inputs.sourceVideo || '').trim(),
          srRatio: inputs.srRatio,
          srcVideo: (inputs.srcVideo || '').trim(),
          refImages: (inputs.refImages || []).filter(Boolean),
          refVideos: (inputs.refVideos || []).filter(Boolean),
        };
        convId = genId();
        params = {
          group: inputs.group,
          model: inputs.model,
          size: normalizeVideoSize(inputs.size),
          seconds: inputs.seconds,
          seed: inputs.seed,
          batchCount: inputs.batchCount,
          aspectRatio: inputs.aspectRatio,
          fitMode: inputs.fitMode,
          steps: inputs.steps,
          images: convImages,
          // 关键帧 auto 的派生结果**随会话锁定**:images 数组分不出「这 1 张是首帧
          // 还是尾帧」(l2va 与 i2v 输入形态相同),续问时重新推必然推错。
          keyframeTaskType:
            isFLF2V && isKeyframeAuto
              ? deriveKeyframeTaskType(
                  !!(inputs.firstFrame || '').trim(),
                  !!(inputs.lastFrame || '').trim(),
                )
              : '',
          // 插帧/配音随会话锁定：续会话或刷新后按会话原设置而非当前开关判定流水线。
          interpolation: !!inputs.interpolation,
          dubbing: !!inputs.dubbing,
          ...media,
        };
      } else {
        const conv = conversationsRef.current.find((c) => c.id === convId);
        const used = conv
          ? conv.messages.filter((m) => m.role === 'user').length
          : 0;
        if (used >= VIDEO_CONV_TURN_LIMIT) {
          showError(
            t('本轮对话生成次数已达上限（{{count}} 次），请开启新对话', {
              count: VIDEO_CONV_TURN_LIMIT,
            }),
          );
          return;
        }
        params = conv
          ? {
              group: conv.group,
              model: conv.model,
              size: conv.size,
              seconds: conv.seconds,
              seed: conv.seed,
              // 老会话没有这个字段 → 归一成 1,与改造前行为一致。
              batchCount: normalizeBatchCount(conv.batchCount),
              aspectRatio: conv.aspectRatio,
              fitMode: conv.fitMode || FIT_BLUR,
              steps: conv.steps != null ? conv.steps : '',
              images: conv.images || [],
              audioData: conv.audioData || '',
              sourceVideo: conv.sourceVideo || '',
              srRatio: conv.srRatio != null ? conv.srRatio : 2,
              srcVideo: conv.srcVideo || '',
              // 老双视频会话的两个只读字段:续问要原样带上,否则同一个会话第二轮起
              // 会静默从 mv2v/ads2v 降级成 v2v/rv2v(见 VIDEO_MODES.vace)。
              srcVideo2: conv.srcVideo2 || '',
              taskTypeOverride: conv.taskTypeOverride || '',
              refImages: conv.refImages || [],
              refVideos: conv.refVideos || [],
              keyframeTaskType: conv.keyframeTaskType || '',
              interpolation: !!conv.interpolation,
              dubbing: !!conv.dubbing,
            }
          : {
              group: inputs.group,
              model: inputs.model,
              size: normalizeVideoSize(inputs.size),
              seconds: inputs.seconds,
              seed: inputs.seed,
              batchCount: inputs.batchCount,
              aspectRatio: inputs.aspectRatio,
              fitMode: inputs.fitMode,
              steps: inputs.steps,
              images: convImages,
              keyframeTaskType:
                isFLF2V && isKeyframeAuto
                  ? deriveKeyframeTaskType(
                      !!(inputs.firstFrame || '').trim(),
                      !!(inputs.lastFrame || '').trim(),
                    )
                  : '',
              interpolation: !!inputs.interpolation,
              dubbing: !!inputs.dubbing,
              ...media,
            };
      }

      // 防御(§2 硬规则):hydrate 已保证无 idb-media: 残留,这里再过滤一遍双保险——
      // 裸引用绝不能作为媒体参数发后端。同时剥掉 hydrate miss 留下的空值。
      const cleanMedia = (v) => (isMediaRef(v) ? '' : v);
      const cleanArr = (arr) => (arr || []).filter((s) => s && !isMediaRef(s));

      // i2v/flf2v/s2v 续问:帧图/人物图取自锁定的对话;刷新后媒体 miss(Blob 被清/IDB 不可用)
      // 时缺失,提示重开对话重新上传。
      if (needsImage) {
        params.images = cleanArr(params.images);
        // 首尾帧模型续问需要首帧+尾帧都在;i2v 模型只需首帧。
        // auto 模式下 1 张(仅首帧 / 仅尾帧)与 2 张(首尾)都合法,只要求至少 1 张;
        // flf2v 模型固定要 2 张。
        const need = isFLF2V && isFlf2vSelected ? 2 : 1;
        if (params.images.length < need) {
          showError(t('帧图已失效,请开启新对话并重新上传'));
          return;
        }
      }
      params.audioData = cleanMedia(params.audioData);
      params.sourceVideo = cleanMedia(params.sourceVideo);
      params.srcVideo = cleanMedia(params.srcVideo);
      params.srcVideo2 = cleanMedia(params.srcVideo2);
      params.refImages = cleanArr(params.refImages);
      params.refVideos = cleanArr(params.refVideos);
      if (isS2V && !(params.audioData || '').trim()) {
        showError(t('驱动音频已失效,请开启新对话并重新上传'));
        return;
      }
      if ((isSR || isDub) && !(params.sourceVideo || '').trim()) {
        showError(t('源视频已失效,请开启新对话并重新上传'));
        return;
      }
      if (isVACE && !(params.srcVideo || '').trim()) {
        showError(t('源视频已失效,请开启新对话并重新上传'));
        return;
      }
      if (isI2V && !(params.refImages || []).length) {
        showError(t('参考图已失效,请开启新对话并重新上传'));
        return;
      }
      // 参考生视频:与提交时同一条判据(图或视频至少其一)。**这里最容易漏**——它走
      // params 而不是 inputs,只改提交侧的话,纯参考视频的历史会话点「重新生成」必被拒。
      if (
        isR2VA &&
        !(params.refImages || []).length &&
        !(params.refVideos || []).length
      ) {
        showError(t('参考素材已失效,请开启新对话并重新上传'));
        return;
      }

      const reqId = genId();
      const now = new Date().toISOString();
      // 超分无提示词/配乐提示词可选:空提示词时会话气泡/历史标题用固定文案占位。
      const displayText = text || t(isDub ? '视频配音' : '视频超分');
      const userMsg = {
        id: `${reqId}-u`,
        role: 'user',
        content: displayText,
        images: needsImage ? params.images || [] : undefined,
      };
      // 一批多条:每条候选一条独立的助手消息,而**不是**一条消息挂 N 个任务。
      // 不只是省事 —— 轮询槽按 msgId 索引,而流水线(生成→超分→配音)靠在同一条
      // 消息上换 taskId 实现(submitPipelineStage),一条消息挂多个任务会同时破坏
      // 这两处。N 条消息反而语义正确:用户挑中哪条候选,超分/配音就作用在那一条上,
      // 现有流水线一行不用改。
      const count = supportsBatch ? normalizeBatchCount(params.batchCount) : 1;
      // seed 的两种口径,与图像侧一致:
      //   count === 1 → 维持改造前:用户填了才发,留空不发。
      //   count > 1  → 前端逐条派生不同 seed,否则 N 条一模一样。
      const seeds =
        count > 1
          ? deriveSeeds(params.seed, count)
          : params.seed !== '' && params.seed != null
            ? [Number(params.seed)]
            : [null];
      // 单条时 id 保持 `${reqId}-a` 不变:历史恢复、流水线换 taskId、IDB 媒体引用
      // 都按 msgId 索引,改 id 形态等于给存量会话换了一套键。
      const asstIds =
        count === 1 ? [`${reqId}-a`] : seeds.map((_, i) => `${reqId}-a${i}`);
      const asstMsgs = asstIds.map((id, i) => ({
        id,
        role: 'assistant',
        status: VIDEO_STATUS.QUEUED,
        model: params.model,
        size: params.size,
        seconds: params.seconds,
        prompt: text,
        progress: 0,
        taskId: null,
        videoUrl: null,
        // 多条时记下各自的 seed 与序号,结果区据此显示"第 n/N 条 · 种子 x"。
        // 单条不写这几个字段 —— 存量消息也没有,渲染侧一视同仁地按"没有就不显示"处理。
        ...(count > 1
          ? { seed: seeds[i], batchIndex: i, batchTotal: count }
          : {}),
      }));

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
              seconds: params.seconds,
              seed: params.seed,
              batchCount: params.batchCount,
              aspectRatio: params.aspectRatio,
              fitMode: params.fitMode,
              // 步数随会话锁定:续问/刷新后按会话原设置发,不受当前框里的值影响
              // (与 seed / interpolation 同一处理)。
              steps: params.steps != null ? params.steps : '',
              images: params.images || [],
              // 新增能力媒体输入(base64):锁进对话供续问复用,落盘时按
              // VIDEO_MEDIA_SCHEMA 换成 IDB 引用。
              //
              // ⚠️ **媒体字段在本 hook 里有三处逐字段白名单**,加字段必须三处一起过:
              //   1. 这里(新建会话)  2. 上面续问时从 conv 重建 params  3. openHistoryItem
              // 漏任意一处都不报错,只会让素材在某一步悄悄消失 —— 漏 1 或 2 的表现是
              // 「第二轮起参考素材没了」,漏 3 是「打开历史看不到用过什么」。
              // 例外:srcVideo2 / taskTypeOverride **只在 2 和 3 里有**,这里故意不写 ——
              // 它们是老会话的只读遗留字段,新会话没有写入源(见 VIDEO_MODES.vace)。
              audioData: params.audioData || '',
              sourceVideo: params.sourceVideo || '',
              srRatio: params.srRatio != null ? params.srRatio : 2,
              srcVideo: params.srcVideo || '',
              refImages: params.refImages || [],
              refVideos: params.refVideos || [],
              keyframeTaskType: params.keyframeTaskType || '',
              // 插帧/配音随会话锁定：刷新/续会话按此判定流水线，不受当前开关影响。
              interpolation: !!params.interpolation,
              dubbing: !!params.dubbing,
              title: displayText,
              createdAt: now,
              updatedAt: now,
              messages: [userMsg, ...asstMsgs],
            },
            ...prev,
          ];
        } else {
          const conv = {
            ...prev[idx],
            updatedAt: now,
            messages: [...prev[idx].messages, userMsg, ...asstMsgs],
          };
          next = [conv, ...prev.filter((_, i) => i !== idx)];
        }
        next = next.slice(0, VIDEO_HISTORY_LIMIT);
        persistConversations(storageKey, next);
        return next;
      });
      if (currentConvId == null) setCurrentConvId(convId);
      // **先占名额再发请求**。taskId 要等响应回来才有，但名额必须在请求发出的那一刻
      // 就占住：否则连点三下，三次都在第一个响应到达前跑到上面的并发闸，那时
      // activePollsRef 还是空的，三个全放过去。占位槽的 taskId 先留 null，成功后补上，
      // 失败/取消时由 finishPoll 收掉。
      asstIds.forEach((id) => {
        activePollsRef.current.set(id, {
          convId,
          msgId: id,
          taskId: null,
          timer: null,
          canceled: false,
        });
      });
      syncPollState();

      try {
        // 按模型类别只发对应的时长字段：sora→seconds(字符串)，minimax→duration(整数秒)
        const strategy = resolveVideoStrategy(params.model);
        const body = {
          model: params.model,
          group: params.group,
          prompt: text,
        };
        // task_type:数字人/超分/编辑显式下发(门面据此路由),其余靠模型名推断,不发。
        if (taskType) {
          body.metadata = { ...(body.metadata || {}), task_type: taskType };
        }
        // 关键帧:task_type 按所选模型下发,不再按输入张数派生。后端 inferTaskType 其实
        // 也能从模型名判出同样的结果,但显式下发才能保证前后端判据一次对齐、不靠巧合。
        if (isFLF2V) {
          // 三态:wan 的两类按所选模型定死(引擎实例的 task 在启动期就固定);
          // auto(H3)按用户实际填了哪个槽派生 i2v / l2va / flf2v ——
          // 「只给尾帧」(l2va)在输入形态上与 i2v 完全一样(都是 1 张图),
          // 后端靠张数推不出来,必须由这里显式下发。
          // auto 的派生结果随会话锁定(params.keyframeTaskType):续问/刷新后必须与
          // 首次提交同解 —— images 数组本身分不出「1 张是首帧还是尾帧」,这正是
          // l2va 存在的理由,重新推只会推错。
          const kfTaskType =
            params.keyframeTaskType || (isFlf2vSelected ? 'flf2v' : 'i2v');
          body.metadata = { ...(body.metadata || {}), task_type: kfTaskType };
        }
        // 尺寸/分辨率仅文生视频、且该值仍在当前模型允许集内才下发（对齐宽高比的闸门，
        // 避免切到未配尺寸的模型时把残留旧值误发）；其余模式输出跟随上传输入，不发 size。
        const videoSizeVal = normalizeVideoSize(params.size);
        // 流水线（前端编排）：生成 →[超分]→[配音]。后置段用 task:<id> 引用上一段产物，
        // 在 pollOnce 里自动提交。超分（选中的是运营配的超分档时，stage1 降到该档的起步
        // 分辨率）与配音（文生/图生/视频编辑，开关开启）可各自独立启用，也可叠加成三段。
        // 后置段模型是否可用统一查后端「该分组可用模型」列表（GetUserModels：auto→
        // GetUserAutoGroup、显式→该组已启用模型），与生成模型同一套判定，缓存命中即时。
        //
        // 总前提：生成模型跑在自建 gpustackplus 引擎上（后台按模型勾选）。第三方渠道
        // 原生直出高分辨率、也没有我们的 sr/v2a 模型可接，参数必须原样透传，不能替用户
        // 改写档位再拼两段。判据按 params.model（随会话锁定）而非当前选中模型，续会话/
        // 刷新后与首次提交同解，见 isPipelineModel 的注释。
        const usePipeline = isPipelineModel(videoConfig, params.model);
        // 引擎族按 params.model 重新解（随会话锁定，同 usePipeline 的理由）。宽高比
        // 下发要用它：H3 与 wan 同挂自建引擎，但两边认的宽高比字段不同。
        const paramEngine = getEngineForVideoModel(videoConfig, params.model);
        // 选中的档位是不是超分档：用 params.model 的 upscale 规则重新解一次，与选择器
        // 那次走同一个 buildVideoSizeChoices，保证续问/刷新后推出同一个答案——重新推却
        // 推出别的答案，正是 keyframeTaskType 当初的教训。
        // 分组可用性留到下面拿到权威列表再判，这里传 null 表示先不过滤。
        // 锁定值优先，与展示侧 sizeChoices 同一份来源：两处各读各的，就会出现
        // 「选择器按锁定值只给 768P，提交侧却按回落链认得 480P」这种分叉。
        // 锁按引擎族生效，且这里要用 paramEngine（随会话锁定）而不是当前选中模型的
        // 引擎族——与 usePipeline / paramEngine 同一个理由，续问/刷新后才和首次提交同解。
        const paramNativeSizes =
          getTabFieldLock(category, mode, 'sizes', paramEngine)?.value ||
          getSizesForVideoModel(videoConfig, params.model, mode);
        // 闸门与展示侧同为 sendsSize（见 sizeChoices 的注释）：只要这个玩法会下发
        // size，超分档就成立，与是不是文生视频无关。
        const upscaleChoice = !sendsSize
          ? null
          : buildVideoSizeChoices(
              videoConfig,
              params.model,
              paramNativeSizes,
              null,
            ).find((c) => c.isUpscale && c.value === videoSizeVal) || null;
        // 选中了高分辨率档。它有两种实现方式,由模型级「纯放大」开关决定走哪条:
        //   一段式(nativeDelivery) → 原生档生成 + 引擎出片前纯放大,不跑第二段;
        //   两段式(默认)          → 原生档生成 + 接超分模型跑 task_type=sr。
        // 判据按 params.model(随会话锁定)而非当前选中模型,续问/刷新后与首次提交同解,
        // 与 usePipeline / paramEngine 同一个理由。
        const maybeHighRes = usePipeline && !isSR && !isDub && !!upscaleChoice;
        const nativeDelivery = isNativeDeliveryModel(videoConfig, params.model);
        // 一段式不需要超分模型,也就不需要去查分组可用列表 —— 它整条路上没有第二个模型。
        const maybeUpscale = maybeHighRes && !nativeDelivery;
        // 配音段：文生/图生/视频编辑，会话配音开关开时可能启用。
        // 读 params.dubbing（随会话锁定）而非当前开关，续会话/刷新后仍按原设置。
        const maybeDub =
          DUB_PIPELINE_ENABLED &&
          allowDub &&
          usePipeline &&
          !isSR &&
          !isDub &&
          !!params.dubbing &&
          DUB_PIPELINE_MODES.includes(mode);

        // 从 params.group 的权威可用模型列表按能力挑超分/配音模型（既保证对该分组
        // 可用又匹配能力，兼容多同能力模型分组分别启用）。仅在可能用到时才拉列表。
        let usableModels = [];
        if (maybeUpscale || maybeDub) {
          try {
            const { success, data } = await getUserModelsCached(params.group);
            usableModels = success && Array.isArray(data) ? data : [];
          } catch (e) {
            usableModels = [];
          }
        }
        // 超分模型由运营在规则里显式指定，不再按能力标签猜第一个；这里只校验它对
        // 当前分组确实可用（选择器已按同一份列表过滤过，但那是渲染时的快照，提交时
        // 以权威列表为准）。
        const srModel =
          maybeUpscale && usableModels.includes(upscaleChoice.srModel)
            ? upscaleChoice.srModel
            : '';
        const dubModel = maybeDub
          ? findCapabilityModelIn(
              videoConfig,
              usableModels,
              VIDEO_DUB_CAPABILITY,
            )
          : '';
        const wantUpscale = maybeUpscale && !!srModel;
        const wantDub = maybeDub && !!dubModel;
        // 一段式交付:没有第二段,所以不进 pipeline —— nextPipelineStage 见
        // pipeline.upscale 为空就直接跳到配音或结束,不需要为它加分支。
        const wantDelivery = maybeHighRes && nativeDelivery;
        // 交付短边与两段式的目标短边取自同一个函数:档位语义(1080P/2K/4K 说的都是短边,
        // 长边由画幅决定)两条路完全一致,只是执行者从超分模型换成了引擎的编码漏斗。
        const deliveryShortEdge = wantDelivery
          ? upscaleTargetShortEdge(upscaleChoice.value)
          : 0;

        let pipeline = null;
        if (wantUpscale || wantDub) {
          pipeline = {
            group: params.group,
            upscale: wantUpscale
              ? {
                  srModel,
                  interpolation: !!params.interpolation,
                  // 目标短边在这里算好存下,而不是提交超分段时再算:那时已经拿不到
                  // 选中的档位(params 不随会话持久化),续问/刷新后会算出别的答案
                  // ——与 keyframeTaskType 当初同一个教训。
                  targetShortEdge: upscaleTargetShortEdge(upscaleChoice.value),
                }
              : null,
            // 配音段直接复用本次生成视频的提示词：它本就是这段画面的描述,而 foley 模型
            // 要的正是「画面里什么在发声」。原来这里读的是一个独立的「配音提示词」输入框,
            // 用户不填就下发空串——而空 prompt 恰恰是该模型配出无关背景音乐的主因。
            dub: wantDub ? { dubModel, prompt: text } : null,
          };
          // 有超分段 → stage1 降到该超分档的起步分辨率（运营在规则里指定的那一档）；
          // 无超分段（仅配音）→ stage1 按选中尺寸正常生成。
          if (wantUpscale) {
            body.size = normalizeVideoSize(upscaleChoice.fromSize);
          }
        }
        // 一段式交付:生成仍然按起步档（= 该模型的原生档）下发，只是把交付短边一并
        // 声明给引擎，由它在出片前缩放。
        //
        // **生成尺寸必须照常下发**，不能因为「不跑第二段了」就省掉：省掉之后下面那个
        // `!wantUpscale && ...` 分支也不会命中（选中的是超分档、不在原生档列表里），
        // 于是 size 一个都不发 —— 引擎按自己的默认档出片，用户选的档位彻底失效。
        //
        // ⚠️ 绝不能把交付短边当成生成尺寸下发：H3 显式给了 width/height 就会跳过引擎
        // 侧的 768 校验与面积钳位，静默地真按 1080p 去生成 —— 那条路已实测更差更贵
        // （耗时 2.68×、峰值显存 35.5/40 GiB、时序相干性反而降 30%），因为 Turbo8 是在
        // 768p 上蒸馏的，跑出训练分辨率高频就不稳。生成档与交付档必须是两个值。
        if (wantDelivery) {
          body.size = normalizeVideoSize(upscaleChoice.fromSize);
          if (deliveryShortEdge > 0) {
            body.metadata = {
              ...(body.metadata || {}),
              [VIDEO_DELIVERY_SHORT_EDGE_KEY]: deliveryShortEdge,
            };
          }
        }
        // 未走超分段时只认**原生**档位：档位列表里现在混着超分档，若超分因故没启用
        // （运营改了规则、超分模型对该分组停用、或续问时规则已变），把超分档的值直接
        // 发给生成模型，等于发了一个它并不支持的档位。宁可不发 size 让引擎走默认。
        if (
          !wantUpscale &&
          sendsSize &&
          paramNativeSizes.includes(videoSizeVal)
        ) {
          body.size = videoSizeVal;
        }
        // 超分/配乐跟随源视频、数字人跟随驱动音频,均不发时长字段(配置面板也不展示时长框)。
        // 数字人这条是实测结论:引擎不读由 duration 换算出的 target_video_length,产出长度
        // 就是音频长度。发了既无效又误导,长度管控走 maxAudioSec 在物化时按真实音频长度执行。
        if (!isSR && !isDub && !isS2V) {
          if (strategy.durationField === 'seconds') {
            body.seconds = params.seconds;
          } else {
            body.duration = parseInt(params.seconds, 10) || undefined;
          }
        }
        // 随机种子不在这里下发 —— 见下方 submitOne:一批多条时每条的 seed 不同,
        // 塞进共享的 body 会让最后一次覆盖前面几次。塞进 metadata 的理由不变
        // (gpustackplus task adaptor 整体透传 metadata 给引擎;TaskSubmitReq.Metadata
        // 只从请求的 metadata 对象取,故不能放顶层)。留空则不发、由引擎随机。
        // 采样步数:同样走 metadata(adaptor 把它平铺到 body 顶层)。后端
        // applyMiniMaxH3Request 对 num_inference_steps 是「已有则不覆盖」,所以这里发了
        // 就是最终值、不发才回落到运营配的 defaultSteps / 引擎族基座档。
        //
        // 只对展示步数框的玩法下发,与 sendsSteps 同一判据 —— 超分/配音/数字人这些
        // 由源素材决定形态的玩法界面上没有这个框,却因为 inputs.steps 还留着上一个模型
        // 的值而把它发出去,是典型的「看不见的参数在生效」。
        if (sendsSteps) {
          const stepsVal = parseInt(params.steps, 10);
          if (Number.isFinite(stepsVal) && stepsVal > 0) {
            body.metadata = {
              ...(body.metadata || {}),
              num_inference_steps: stepsVal,
            };
          }
        }
        // 插帧(默认关):按提交时的开关状态透传 target_fps(引擎 RIFE 帧率翻倍)。
        // 仅自建引擎认这个字段,第三方渠道不下发(usePipeline);超分/配乐不适用;
        // 有超分段时插帧后移到超分段(stage1 不发),仅配音段无超分时插帧仍作用于生成任务。
        // INTERPOLATION_ENABLED 是总闸门:当前部署没有可用的插帧能力,历史会话里存了
        // interpolation:true 的续问也不会再下发(见常量处的注释)。
        if (
          INTERPOLATION_ENABLED &&
          params.interpolation &&
          usePipeline &&
          !isSR &&
          !isDub &&
          !pipeline?.upscale
        ) {
          body.metadata = {
            ...(body.metadata || {}),
            target_fps: VIDEO_INTERPOLATION_TARGET_FPS,
          };
        }
        // 宽高比。各家认的字段不一样,按**引擎族**分发(而不是只按 usePipeline):
        // - wan(自建,未声明引擎族):target_shape:[h,w]。它的 t2v runner 的
        //   get_latent_shape_with_target_hw 优先采用这个,不认识 aspect_ratio。
        // - MiniMax H3(自建):具名 aspect_ratio。**它同样是自建引擎**(跑在
        //   gpustackplus 上,还会被 video_pipeline_flag_migrated 迁移自动标成
        //   pipeline:true),所以不能只看 usePipeline 就发 target_shape —— 那是 wan 的
        //   720p 级固定值表,H3 侧读不到比例,一路缺到缺省分支补成 16:9,用户选什么都
        //   出 16:9。发 ratio,由网关的 h3NormalizeAspectRatio 归一成 aspect_ratio。
        // - 其他渠道:ratio("16:9" 这种原生形态)。Ark/Seedance 只认 ratio,收到
        //   target_shape 会整个忽略、只能出默认比例——界面上摆着宽高比选择器却不生效。
        // 纯 opt-in:该 tab 在中央元数据里声明了 aspectRatios、且该值仍在当前模型的允许集
        // 内才下发(续问历史会话时 conv.aspectRatio 可能是后台已改/删的旧值,校验一遍避免
        // 绕过白名单)。
        //
        // ⚠️ **画幅由上传图决定的玩法(needsImage:关键帧)一个比例字段都不发**,它的
        // 比例已经在下面通过改图表达了(composeImageToRatio)。两条路同时走会打架:引擎
        // 按图推画布,而第三方渠道(Seedance 等)认 ratio —— 图已是 16:9 又发一个 9:16,
        // 出片按谁的来取决于渠道,静默不一致。
        //
        // 判据是 needsImage,不是"非文生视频都不发"——参考生视频(r2va)的
        // sendsAspectRatio 为 true 但 needsImage 为 false(它用 refImages 不是
        // firstFrame,不走 compose),画幅由原生 aspect_ratio 直发决定(参考图不绑定
        // 输出画布,与关键帧完全不同,见 playgroundAdmin.constants.js 里 r2va tab 的
        // 注释)。needsImage 之外的判据都会连带挡住 r2va,见 needsImage 定义处注释
        // 记录过的教训。
        if (
          sendsAspectRatio &&
          !needsImage &&
          params.aspectRatio &&
          availableAspectRatios.includes(params.aspectRatio)
        ) {
          const usesTargetShape =
            usePipeline && paramEngine !== VIDEO_ENGINE_MINIMAX_H3;
          if (usesTargetShape) {
            const shape = aspectRatioToShape(params.aspectRatio);
            if (shape) {
              body.metadata = { ...(body.metadata || {}), target_shape: shape };
            }
          } else {
            body.metadata = {
              ...(body.metadata || {}),
              ratio: params.aspectRatio,
            };
          }
        }
        // i2v/flf2v/s2v:带主图。后端 gpustackplus:images[0]=首帧/人物图,flf2v 时 images[1]=尾帧。
        //
        // 画幅跟随输入的玩法选了具名比例时,在这里**把图改成那个比例**再发 —— 这是唯一
        // 能让画幅生效又不毁画面的做法(引擎按 images[0] 的比例推画布,靠参数盖画布只会
        // 把图拉伸变形;详见 helpers/imageCompose.js 顶部的现网实测记录)。
        //
        // 两张必须用同一比例同一模式:画布只跟首帧,尾帧比例不一致会被引擎裁切或拉伸。
        // 合成失败(解不出图/编码被拒)时 composeImageToRatio 原样返回,画幅回落到跟随
        // 原图 —— 那始终是个安全结果,不该为此把整条提交拦下来。
        if (needsImage && (params.images || []).length > 0) {
          const wantsCompose =
            sendsAspectRatio &&
            params.aspectRatio &&
            params.aspectRatio !== VIDEO_ASPECT_RATIO_AUTO &&
            availableAspectRatios.includes(params.aspectRatio);
          body.images = wantsCompose
            ? await Promise.all(
                params.images.map((img) =>
                  composeImageToRatio(img, params.aspectRatio, params.fitMode),
                ),
              )
            : params.images;
        }
        // 图生视频(Bernini r2v):参考图(1~3)→ metadata.src_ref_images,
        // 门面物化后引擎按参考图组合主体/服装/道具/场景生成视频。
        // 参考生视频(r2va)复用同一个键:后端 materializeR2VAInputs 与 doubao adaptor
        // 读的都是 src_ref_images —— 两家渠道字段名已统一,前端不按渠道分支。
        if ((isI2V || isR2VA) && (params.refImages || []).length > 0) {
          body.metadata = {
            ...(body.metadata || {}),
            src_ref_images: params.refImages,
          };
        }
        // 参考生视频的参考视频(可选,运营 opt-in 才有)。键名 reference_videos(复数),
        // 后端 metadataStringListAny 单复数都收,doubao 侧同名。
        // **不要复用 metadata.video**:那个键现有语义是「被加工的源视频」(SeedVR2 超分 /
        // v2a 配音的素材),而这里是「参考」——重载会让 taskTypesCompatibleWithInputs
        // 的输入形态判定失准。
        if (isR2VA && (params.refVideos || []).length > 0) {
          body.metadata = {
            ...(body.metadata || {}),
            reference_videos: params.refVideos,
          };
        }
        // 参考生视频的音色参考(可选)。键名是 reference_audios(复数)而**不是** audio:
        // metadata.audio 现有语义是 InfiniteTalk 的「驱动音轨」(决定输出时长),
        // 而这里是「音色/说话风格参考」(长度与输出无关,台词写在 prompt 里)。
        // 重载同一个键会让两种语义打架。doubao 侧读的也是 reference_audios。
        if (isR2VA && (params.audioData || '').trim()) {
          body.metadata = {
            ...(body.metadata || {}),
            reference_audios: [params.audioData],
          };
        }
        // 数字人:驱动音频 → metadata.audio(门面物化到 audio_path 喂 InfiniteTalk)。
        if (isS2V && (params.audioData || '').trim()) {
          body.metadata = { ...(body.metadata || {}), audio: params.audioData };
        }
        // 视频超分:源视频 → metadata.video;倍率 → metadata.sr_ratio(门面透传,引擎按 config 封顶)。
        if (isSR) {
          if ((params.sourceVideo || '').trim()) {
            body.metadata = {
              ...(body.metadata || {}),
              video: params.sourceVideo,
            };
          }
          const ratio = Number(params.srRatio);
          if (Number.isFinite(ratio) && ratio > 0) {
            body.metadata = { ...(body.metadata || {}), sr_ratio: ratio };
          }
        }
        // 视频配乐:源视频 → metadata.video(门面物化到 video_path 喂 LTX-2.3);
        // task_type=v2a 已在上方显式下发;无倍率/时长等额外标量。
        if (isDub && (params.sourceVideo || '').trim()) {
          body.metadata = {
            ...(body.metadata || {}),
            video: params.sourceVideo,
          };
        }
        // 视频编辑(Bernini):必有 1 源视频,按输入自动分流 task_type ——
        // 无参考图=v2v、带参考图=rv2v。仅参考图的 r2v 已迁到「图生视频」模式。
        // v2(第二源视频)只可能来自老会话:体验区已收掉那个上传口,新会话到不了这两个
        // 分支;老会话续问仍按 mv2v / ads2v(override)原样发出(见 VIDEO_MODES.vace)。
        if (isVACE) {
          const md = { ...(body.metadata || {}) };
          const v1 = (params.srcVideo || '').trim();
          const v2 = (params.srcVideo2 || '').trim();
          const hasRefs = (params.refImages || []).length > 0;
          md.src_video = v2 ? [v1, v2] : v1;
          if (hasRefs && !v2) md.src_ref_images = params.refImages;
          const override = (params.taskTypeOverride || '').trim();
          // override(ads2v)只在真双视频时生效:第二视频丢失(Blob 被清)时回落自动分流,
          // 避免残留 override 让 1 视频提交被后端「需要恰好 2 个视频」拒掉。
          md.task_type = v2 ? override || 'mv2v' : hasRefs ? 'rv2v' : 'v2v';
          body.metadata = md;
        }
        // 一条候选的提交 + 起轮询。body 只组一次(上面那 400 行),这里按 seed 分发 ——
        // 每条**各自深拷 metadata**:共享同一个 body 对象再改 seed,会让最后一次覆盖
        // 前面几次,N 条拿到同一个 seed,而且不报错。
        const submitOne = async (asstId, seed) => {
          try {
            const oneBody =
              seed == null
                ? body
                : { ...body, metadata: { ...(body.metadata || {}), seed } };
            const res = await API.post(
              VIDEO_API_ENDPOINTS.VIDEO_GENERATIONS,
              oneBody,
              {
                skipErrorHandler: true,
              },
            );
            const data = res.data || {};
            // 兼容两种响应形态：OpenAIVideo（顶层 id/status）与通用 TaskResponse（data.task_id）
            const inner = data.data || {};
            const taskId = data.id || data.task_id || inner.task_id || inner.id;
            if (!taskId) throw new Error(t('提交视频任务失败'));
            const status = normalizeVideoStatus(data.status || inner.status);
            const progress =
              parseProgress(
                data.progress != null ? data.progress : inner.progress,
              ) || 0;
            // 提交即失败：直接标记，不启动轮询
            if (status === VIDEO_STATUS.FAILED) {
              const msg =
                data.error?.message ||
                inner.error?.message ||
                inner.fail_reason ||
                data.fail_reason ||
                t('视频生成失败');
              patchConvMessage(convId, asstId, {
                status: VIDEO_STATUS.FAILED,
                error: msg,
              });
              finishPoll(asstId);
              return msg;
            }
            patchConvMessage(convId, asstId, {
              taskId,
              status,
              progress,
              ...(pipeline ? { pipeline, stage: 'generating' } : {}),
            });
            // 把 taskId 补进上面那个占位槽。槽可能已经不在了（等响应期间用户清空了历史
            // 或删掉了这条会话），那就别再起轮询——重建槽等于让一个已被取消的任务复活。
            const slot = activePollsRef.current.get(asstId);
            if (!slot || slot.canceled) return null;
            slot.taskId = taskId;
            slot.timer = setTimeout(
              () => pollOnce(convId, asstId, taskId, 1),
              VIDEO_POLL_INTERVAL_MS,
            );
            return null;
          } catch (error) {
            const msg = extractApiErrMsg(error, t('视频生成失败'));
            patchConvMessage(convId, asstId, {
              status: VIDEO_STATUS.FAILED,
              error: msg,
            });
            finishPoll(asstId);
            return msg;
          }
        };

        // all 而不是 allSettled:submitOne 自己吞了异常并回错误文案,不会 reject。
        // 一条失败不拖累其余 —— 后端准入控制在并发时正是"前几条过、后几条 429"。
        const errs = (
          await Promise.all(asstIds.map((id, i) => submitOne(id, seeds[i])))
        ).filter(Boolean);
        if (errs.length > 0) {
          // 只弹一次:N 条都被 429 时弹 N 个一样的 toast 只会把界面刷满。
          showError(
            count > 1
              ? t('{{fail}}/{{total}} 条提交失败：', {
                  fail: errs.length,
                  total: count,
                }) + errs[0]
              : errs[0],
          );
        }
      } catch (error) {
        // body 组装阶段就出错 → 整批都发不出去,N 条消息一起置失败。
        const msg = extractApiErrMsg(error, t('视频生成失败'));
        asstIds.forEach((id) => {
          patchConvMessage(convId, id, {
            status: VIDEO_STATUS.FAILED,
            error: msg,
          });
          finishPoll(id);
        });
        showError(msg);
      }
    },
    [
      currentConvId,
      inputs,
      supportsBatch,
      patchConvMessage,
      pollOnce,
      finishPoll,
      syncPollState,
      storageKey,
      needsImage,
      sendsSize,
      isFLF2V,
      isS2V,
      isSR,
      isVACE,
      isDub,
      taskType,
      availableSizes,
      availableAspectRatios,
      videoConfig,
      mode,
      category,
      allowDub,
      t,
    ],
  );

  const regenerate = useCallback((prompt) => generate(prompt), [generate]);

  const newConversation = useCallback(() => {
    setCurrentConvId(null);
    // 其余输入刻意保留(用户常拿同一批素材再发一次),只清 srcVideo2:它是老会话的
    // 只读遗留字段,没有上传口也不会被新会话提交,留着会在新会话锁定后显示出一个
    // 不属于它的第二视频。
    setInputs((prev) => (prev.srcVideo2 ? { ...prev, srcVideo2: '' } : prev));
  }, []);

  const clearHistory = useCallback(() => {
    // 清空历史时把**所有**进行中的轮询一并停掉，避免 generating/taskSlotsFull 卡住
    // 导致发送按钮一直禁用。canceled 要逐个打上：请求还在飞的占位槽靠它拦住回填。
    activePollsRef.current.forEach((slot) => {
      slot.canceled = true;
      if (slot.timer) clearTimeout(slot.timer);
    });
    activePollsRef.current.clear();
    syncPollState();
    setConversations([]);
    persistConversations(storageKey, []);
    setCurrentConvId(null);
  }, [syncPollState]);

  const deleteHistoryItem = useCallback(
    (id) => {
      // 删掉的会话里可能有多个任务在轮询（同一会话可连发），逐个停掉。
      // 边遍历边删同一个 Map 不安全，先收集 msgId 再收。
      const doomed = [];
      activePollsRef.current.forEach((slot, msgId) => {
        if (slot.convId === id) {
          slot.canceled = true;
          doomed.push(msgId);
        }
      });
      doomed.forEach((msgId) => finishPoll(msgId));
      setConversations((prev) => {
        const next = prev.filter((c) => c.id !== id);
        persistConversations(storageKey, next);
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
        size: conv.size != null ? conv.size : prev.size,
        seconds: conv.seconds != null ? conv.seconds : prev.seconds,
        seed: conv.seed != null ? conv.seed : prev.seed,
        batchCount: normalizeBatchCount(conv.batchCount),
        aspectRatio:
          conv.aspectRatio != null ? conv.aspectRatio : prev.aspectRatio,
        fitMode: conv.fitMode != null ? conv.fitMode : prev.fitMode,
        steps: conv.steps != null ? conv.steps : prev.steps,
        srRatio: conv.srRatio != null ? conv.srRatio : prev.srRatio,
        // 打开历史:恢复该会话上传过的输入媒体(已从 IDB hydrate),供只读查看/播放。
        // 帧图存为 images 数组(i2v/s2v 首帧=images[0];flf2v 首帧/尾帧=images[0/1])。
        // 锁定态下 ConfigPanel 的上传控件 disabled → 只展示预览/播放器,不能删改/重传。
        firstFrame: (conv.images || [])[0] || '',
        lastFrame: (conv.images || [])[1] || '',
        audioData: conv.audioData || '',
        sourceVideo: conv.sourceVideo || '',
        srcVideo: conv.srcVideo || '',
        // 老双视频会话:恢复第二源视频供锁定态只读展示(新会话恒为 '',顺带把上一条
        // 老会话残留的值清掉 —— 它没有上传口,留着会显示在不相干的会话里)。
        srcVideo2: conv.srcVideo2 || '',
        refImages: conv.refImages || [],
        refVideos: conv.refVideos || [],
        // 恢复插帧/配音开关显示（锁定态下只读展示，续会话仍读 params 里的会话值）
        interpolation: !!conv.interpolation,
        dubbing: !!conv.dubbing,
      }));
      // 若该会话最后一个任务仍在进行中，恢复轮询
      const assts = (conv.messages || []).filter((m) => m.role === 'assistant');
      const last = assts[assts.length - 1];
      if (
        last?.taskId &&
        (last.status === VIDEO_STATUS.QUEUED ||
          last.status === VIDEO_STATUS.IN_PROGRESS)
      ) {
        resumePoll(conv.id, last.id, last.taskId);
      }
    },
    [resumePoll],
  );

  // 卸载时清理所有轮询
  useEffect(() => {
    const polls = activePollsRef.current;
    return () => {
      polls.forEach((slot) => {
        if (slot.timer) clearTimeout(slot.timer);
      });
      polls.clear();
    };
  }, []);

  // 必填输入缺失时发送置灰(新对话/未锁定):避免只填提示词就点发送(点了才报错且 Semi
  // 会清空已输入的提示词)。关键帧需首帧,选中首尾帧模型时还需尾帧;图生视频需参考图;
  // s2v 需主图+音频;sr/dub 需源视频;vace 需 ≥1 源视频。
  const missingRequiredImage =
    !locked &&
    ((needsImage && (inputs.firstFrame || '').trim() === '') ||
      (isFLF2V && isFlf2vSelected && (inputs.lastFrame || '').trim() === '') ||
      // auto:首尾两槽至少填一个。首帧那条通用校验(needsImage)对 auto 不适用 ——
      // 只给尾帧是合法玩法(l2va),不能要求必须有首帧。
      (isFLF2V &&
        isKeyframeAutoFull &&
        (inputs.firstFrame || '').trim() === '' &&
        (inputs.lastFrame || '').trim() === '') ||
      (isI2V && !(inputs.refImages || []).filter(Boolean).length) ||
      // 参考生视频:视觉参考「图或视频至少其一」。只判图会让纯参考视频的组合一直灰着
      // 发送键 —— 与提交侧那条判据必须同步,改一处不改另一处等于白改。
      (isR2VA &&
        !(inputs.refImages || []).filter(Boolean).length &&
        !(inputs.refVideos || []).filter(Boolean).length) ||
      (isS2V && (inputs.audioData || '').trim() === '') ||
      ((isSR || isDub) && (inputs.sourceVideo || '').trim() === '') ||
      (isVACE && (inputs.srcVideo || '').trim() === ''));

  return {
    // 页面据此决定要不要渲染「生成条数」控件 —— 与 generate 里的闸门同一个开关,
    // 不会出现"控件在但不生效"或"能生效却没控件"。
    supportsBatch,
    isI2V,
    isR2VA,
    isFLF2V,
    isS2V,
    isSR,
    isVACE,
    isDub,
    isFlf2vSelected,
    keyframeMode,
    allowLastFrame,
    isKeyframeAuto,
    isKeyframeAutoFull,
    needsImage,
    dubAvailable,
    pipelineModel,
    // 三个模态各自的闸,已在上面读过运营配置(未配才回落到内置默认)。
    maxRefImages,
    maxRefVideos,
    refVideoMaxMB,
    refVideoMaxSec,
    maxInputMB,
    maxAudioSec,
    optimizeEngine,
    optimizeContext,
    h3AlignContext,
    inputs,
    handleInputChange,
    applyExample,
    groups,
    models,
    availableSizes,
    // 带超分语义的档位（value/isUpscale/srModel/fromSize），供选择器加标识与副文案。
    // availableSizes 是它的纯值投影，保留给只关心「有哪些档位」的既有消费方。
    sizeChoices,
    availableDurations,
    availableAspectRatios,
    // 步数框:展不展示 + 占位文案里那个「默认 N」。默认值为 null 表示运营没配,
    // 框子留空、提示按引擎族基座档。
    sendsSteps,
    defaultSteps: modelDefaultSteps,
    messages,
    conversations,
    currentConvId,
    generating,
    // 在途任务已顶到并发上限：发送/重新生成按它置灰，而不是按 generating
    // （按 generating 的话一跑起来就全锁死，等于并发没放开）。
    taskSlotsFull,
    locked,
    turnLimitReached,
    missingRequiredImage,
    generate,
    regenerate,
    refetch,
    newConversation,
    clearHistory,
    deleteHistoryItem,
    openHistoryItem,
  };
};
