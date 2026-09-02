// 图片模型相关常量

import {
  normalizeModelNote,
  normalizeModelOptimizePrompt,
  tabScopedValue,
} from './playgroundAdmin.constants';

// 常量本体定义在 playgroundAdmin.constants.js（管理页下拉也要用它），这里再导出，
// 依赖方向保持单向 —— 与 VIDEO_ENGINE_MINIMAX_H3 / MUSIC_ENGINE_MINIMAX_MUSIC3 同一处理。
export { IMAGE_ENGINE_SENSENOVA_U15 } from './playgroundAdmin.constants';

// 提示词预设:点击对应按钮清空输入框并填入该提示词(体验区快速试玩,仅文生图展示)。
export const IMAGE_PROMPT_PRESETS = [
  '远景镜头，在壮丽的雪山背景下，两个小小的人影站在远处山顶，背对着镜头，静静地观赏着日落的美景。夕阳的余晖洒在雪山上，呈现出一片金黄色的光辉，与蔚蓝的天空形成鲜明对比。两人仿佛被这壮观的自然景象所吸引，整个画面充满了宁静与和谐。',
  '长焦镜头下，一只猎豹在郁郁葱葱的森林中站立，面对镜头，背景被巧妙地虚化，猎豹的面部成为画面的绝对焦点。阳光透过树叶的缝隙，洒在猎豹身上，形成斑驳的光影效果，增强了视觉冲击力。',
  '18岁的中国女孩，古代服饰，圆脸，看着镜头，民族优雅的服装，商业摄影，室外，电影级光照，半身特写，精致的淡妆，锐利的边缘。',
  '电影感健身宣传活动，超大哑铃斜放如同标志性道具，穿红色运动装和白色短裤的女性模特坐在哑铃一侧，一条腿弯曲，一条伸直，极简黑色工作室，反光地面，背后用大号字体写着醒目的“STRENGTH”，光线锐利，构图超级干净，奢华运动美学。',
];

export const IMAGE_API_ENDPOINTS = {
  IMAGE_GENERATIONS: '/pg/images/generations',
  IMAGE_EDITS: '/pg/images/edits',
  IMAGE_PROXY: '/pg/images/proxy',
  USER_MODELS: '/api/user/models',
  USER_GROUPS: '/api/user/self/groups',
  PRICING: '/api/pricing',
};

// 图片模型能力枚举（中文即值，也是体验区标签页名）。业内常用完整集。
// 新增能力时同步维护后端 constant/model_capability.go 的 ImageCapabilities。
export const IMAGE_CAPABILITIES = [
  '文生图',
  '图生图',
  '图像编辑',
  '局部重绘',
  '扩图',
  '高清放大',
];

// 当前图片体验区页面代表的能力（= 标签页名）
export const IMAGE_PAGE_CAPABILITY = '文生图';
// 图生图（i2i）能力标签，与文生图共用体验区,通过 mode 区分
export const IMAGE_I2I_CAPABILITY = '图生图';
// 图生图最多上传底图数。前端限 3 张(≤ 后端 gpustackplus maxEditImages / 门面
// _MAX_INPUT_IMAGES=5,前端更严不会被门面拒),控制单次请求体大小与体验。
export const IMAGE_MAX_EDIT_IMAGES = 3;

// 当管理员未配置时的全局兜底：用最兼容的精确像素（dall-e/gpt-image 等只认像素的模型也能过）。
// "默认用宽高比"应通过运营配置的 default 六种比例实现，而非这里的全局兜底。
export const FALLBACK_IMAGE_SIZES = [
  '1024x1024',
  '1024x1792',
  '1792x1024',
  '512x512',
];

// 「提示词智能优化」开关打开时,附带下发的 HunyuanImage-3.0 自回归(AR)档:
// 引擎先思考并改写提示词再去噪,短提示词下出图更贴合描述,代价是耗时约 2.8 倍
// (后端实测)。关闭时不下发该字段,引擎缺省 bot_task=None 即快速档。
//
// ⚠️ 这是 Hunyuan 专有参数,与同一开关下发的通用字段 use_prompt_enhancer
// (ERNIE 侧映射为 extra_args.apply_pe)是两套机制,故两个都发:不认的引擎
// 会直接忽略(其 ImageTaskRequest 是 extra="allow")。
// 也刻意不按模型名过滤——渠道映射可以把任意模型名指向 HunyuanImage-3.0,
// 前端看不到映射结果,按名字硬拦会误伤。
export const IMAGE_QUALITY_BOT_TASK = 'think_recaption';

// localStorage key：图片生成历史
export const IMAGE_HISTORY_STORAGE_KEY = 'image_playground_history';

// 对话（历史）数量上限
export const IMAGE_HISTORY_LIMIT = 10;

// 单段对话内最多生成次数
export const IMAGE_CONV_TURN_LIMIT = 10;

// ——— 异步生图（提交拿 task_id → 轮询）———
//
// 只有自建渠道（GPUStackPlus）支持异步；第三方渠道会返回 async_not_supported，
// 前端据此按模型回落同步（见 useImageGeneration 的 asyncCapableRef）。
// 异步的意义在慢模型：HunyuanImage-3.0 端到端约 110s、冷启可达 260s，
// 同步模式下页面得一直挂着，刷一下就前功尽弃。

// 提交异步任务的端点与同步同址，靠这个 body 字段区分。
export const IMAGE_ASYNC_FIELD = 'async';

// 查询 / 取消端点（:task_id 由调用方拼接）
export const IMAGE_ASYNC_TASK_ENDPOINT = '/pg/images/generations';

// 后端 402/400 的能力缺失码：收到即说明该模型所在渠道不支持异步。
export const IMAGE_ASYNC_UNSUPPORTED_CODE = 'async_not_supported';

// 轮询间隔兜底（秒）。正常走响应头 Retry-After —— 后端按模型快慢给 3 或 10，
// 拿不到才用这个值。
export const IMAGE_POLL_INTERVAL_SEC = 3;

// 单个任务的轮询上限（次）。按最坏情况估：冷启 260s + 排队，留足余量。
// 撞上限不判失败，只停轮并标记，允许用户手动「继续获取」——任务还在后台跑，
// 判失败会让已经扣掉的钱看起来白花。
export const IMAGE_POLL_MAX_TRIES = 200;

export const IMAGE_GEN_STATUS = {
  PENDING: 'pending',
  SUCCESS: 'success',
  FAILED: 'failed',
};

// 规范化尺寸字符串：统一用小写字母 x 作分隔，去空格，
// 把乘号 ×/✕/╳、星号 * 都替换成 x（上游校验会拒绝 '×'）
export const normalizeImageSize = (s) =>
  String(s || '')
    .trim()
    .toLowerCase()
    .replace(/\s+/g, '')
    .replace(/[×✕╳*]/g, 'x');

// 尺寸列表规范化（解析与设置页保存共用，避免两条路径分叉）
export const normalizeSizeList = (list) =>
  Array.isArray(list)
    ? Array.from(new Set(list.map(normalizeImageSize).filter(Boolean)))
    : [];

// 能力列表规范化：去空格、去空、去重（不改大小写，中文原样；解析与保存共用）
export const normalizeCapabilityList = (list) =>
  Array.isArray(list)
    ? Array.from(new Set(list.map((x) => String(x).trim()).filter(Boolean)))
    : [];

// tab 子层规范化：models[name].tabs[tabKey] 只放该 tab 声明用得到的字段（图像目前
// 只有 sizes）。空对象保留（= 该模型挂进了这个 tab、尺寸走兜底）；未配的字段不落键，
// 好让 tabScopedValue 正确降级。
const normalizeImageTabs = (raw) => {
  if (!raw || typeof raw !== 'object') return {};
  const out = {};
  Object.entries(raw).forEach(([tabKey, cfg]) => {
    const entry = {};
    const sizes = normalizeSizeList(cfg?.sizes);
    if (sizes.length) entry.sizes = sizes;
    // 宽高比与分辨率档。**白名单式重建,漏了就是"运营每保存一次删一次"**
    // (与 engine / optimizePrompt 同一类坑)。
    const ratios = normalizeSizeList(cfg?.aspectRatios);
    if (ratios.length) entry.aspectRatios = ratios;
    const tiers = normalizeTierList(cfg?.sizeTiers);
    if (tiers.length) entry.sizeTiers = tiers;
    const note = normalizeModelNote(cfg?.note);
    if (note) entry.note = note;
    // 「AI 优化提示词」的模型级系统提示词覆盖(留空=用 tab 那份通用的)。白名单式重建，
    // 漏了它 = 管理页保存一次就把运营刚写的模板删掉。
    const optimizePrompt = normalizeModelOptimizePrompt(cfg?.optimizePrompt);
    if (optimizePrompt) entry.optimizePrompt = optimizePrompt;
    out[tabKey] = entry;
  });
  return out;
};

// 只取运营在该 tab 下**显式**配置的 sizes，不走模型级/全局/内置兜底；没配返回 undefined。
//
// 图生图的「输出尺寸」框据此决定是否出现。不能像文生图那样对所有模型默认打开：
// 下发的 size 会流向该 tab 下的所有渠道，而 gpt-image / dall-e 的 edits 只认
// 1024x1024 / 1536x1024 / 1024x1536 / auto 这几档，收到底图的原生像素（如
// 1472x1104）会被上游直接判错。所以这是个逐模型开启的能力：运营给自建模型
// （qwen-image-edit / z-image）在 image2image 下配一份 sizes，框才出现、size 才下发。
export const getExplicitTabSizes = (config, model, tabKey) => {
  if (!config || typeof config !== 'object') return undefined;
  return tabScopedValue(config.models && config.models[model], tabKey, 'sizes');
};

// 比例词判据。运营两种写法混着用（历史上文生图填的就是比例词），拆语义要靠它。
export const isRatioWord = (s) => /^\d+:\d+$/.test(normalizeImageSize(s));

// 非空数组才算"配了"。**这个判断不能省**：parseImageSizeConfig 对每个模型都会落一个
// sizes 键（没配就是 []），而空数组是 truthy —— 直接用 `||` 串起来会停在 `[]` 上，
// 运营配的分类默认值一次都不会被读到，所有模型静默退回内置兜底像素。
const nonEmptyList = (v) => (Array.isArray(v) && v.length ? v : null);

// 该模型在该 tab 下的**画幅配置总账**。只有两种模式，由"运营配了什么"推导：
//
//   area   本 tab 配齐了「宽高比 + 分辨率档」→ 算出精确像素下发（实测这是拿到高分辨率
//          的唯一路：同一模型收到 "16:9" 出 1344x768=1.03MP，收到 "2720x1536" 原样出
//          4.18MP，差 4 倍）
//   table  其余一切 → 把配好的值列表原样给用户选、原样下发
//
// **sizes 的语义是"发什么"，不是"什么比例"**：像素与比例词都可以填，后端两种都认
// （gpustackplus 的 setImageShape：比例词 → aspect_ratio、精确像素 → target_shape）。
// 老配置里两种混填过，照样工作，行为与改造前一字不差。
//
// ⚠️ 曾经还有第三种 ratio 模式（把 sizes 里的比例词"提升"成宽高比、单独配 aspectRatios
// 也能生效）。砍掉了：它让 sizes 与 aspectRatios 各自都有两种归宿，四轮评审里有一半的
// 缺陷出在这些组合上（判定链短路、告警判据分叉、i2i 闸被绕过）。收敛成两种模式之后，
// 每个字段只有一个语义 —— 要按比例算像素就把 aspectRatios 与 sizeTiers 配齐，
// 只想直接发比例词就填进 sizes。
//
// 宽高比与分辨率档**只在 tab 级读，没有模型级回落**。三个理由：后端根本不读图像的画幅
// 配置（media_model_config.go 文件头写明 sizes 只驱动前端），模型级对图像没有"直连请求
// 兜底"的意义；体验区取值永远带 tabKey；而 recomputeModelLevel 会把 tab 值的并集写到
// 模型级、parse 又把它丢掉 —— 写/读/parse 三处口径不一致。收成 tab-only 后三处自动一致
// （recomputeModelLevel 那边也已跳过这两个字段）。
export const getImageShapeConfig = (config, model, tabKey) => {
  const entry = config?.models?.[model];
  const ratios = tabScopedValue(entry, tabKey, 'aspectRatios') || [];
  const tiers = tabScopedValue(entry, tabKey, 'sizeTiers') || [];

  // sizes 保留模型级回落（parse 保住了它）；
  // 分类默认值与内置兜底只在"本模型什么都没配"时出场。
  const explicitList =
    tabScopedValue(entry, tabKey, 'sizes') ||
    nonEmptyList(Array.isArray(entry) ? entry : entry?.sizes);

  const mode = ratios.length && tiers.length ? 'area' : 'table';
  const sizes =
    mode === 'area'
      ? []
      : explicitList || nonEmptyList(config?.default) || FALLBACK_IMAGE_SIZES;

  return {
    mode,
    sizes,
    ratios,
    tiers,
    align:
      parseInt(entry?.sizeAlign, 10) > 0
        ? parseInt(entry.sizeAlign, 10)
        : DEFAULT_IMAGE_SIZE_ALIGN,
    // 本 tab 有没有**显式**声明画幅。图生图据此决定要不要下发 size：那是 tab 级 opt-in
    // 的能力（见 getExplicitTabSizes 的注释——size 会流向该 tab 下所有渠道，而
    // gpt-image / dall-e 的 edits 只认固定档位，后端对 dall-e 系不合规尺寸直接 400），
    // 从模型级 / 分类默认值继承来的值不能替运营开这个能力。
    tabScoped: Boolean(
      tabScopedValue(entry, tabKey, 'sizes') || ratios.length || tiers.length,
    ),
  };
};

// 下发 size 的**唯一判据**。返回要发的值，空串表示"一个 size 字段都不发"。
//
// 抽成纯函数不是为了复用（只有一个调用点），是为了**能被穷举测试**：这条判断散在
// generate 里的时候，接连两轮评审各挑出它的一个漏洞 ——
//   - 算不出像素时 computeImageSize 返回 ''，而调用方照发，给每次请求塞 "size": ""；
//   - 'auto' 哨兵只在旧的那一支里排除了，新加的那一支忘了抄。
// 两次都是"改了一支忘了另一支"，抽出来之后就只有一支。
//
// 三个来源任一成立即可下发：文生图（一直如此）/ 画幅由「比例 × 档位」算出来 /
// 图生图显式配了尺寸白名单。前两条前提是共用的：值得存在，且不是 auto
// （auto 的语义就是"交给引擎决定"，见 IMAGE_SIZE_AUTO 的注释）。
export const resolveSubmitImageSize = (
  size,
  { isI2I, usesComputedShape, canPickI2ISize } = {},
) => {
  const s = normalizeImageSize(size || '');
  // 空值不用单独判：归一化后本来就是空串，顺着往下走返回的还是空串（调用方按
  // "空=不发"处理）。写成 `if (!s) return ''` 是个等价分支——留着只会让人以为它承重。
  if (s === IMAGE_SIZE_AUTO) return '';
  if (!isI2I || usesComputedShape || canPickI2ISize) return s;
  return '';
};

// 内置推荐档位（管理页「填入推荐档位」一键写入）。
//
// **每一行都是在现网实测出来的，不是照抄文档**——文档与实机对不上的地方不止一处：
//   - sensenova-u1.5：收到比例词 "16:9" 出 1344x768（1.03MP），收到 "2720x1536"
//     原样出 4.18MP。官方五档是等面积阶梯，用面积基准 2048 + 32 对齐能逐个精确复现；
//     表外的 4:3（算出 2336x1760）实测也原样生效，3072x3072 同样能出（9.44MP）。
//   - qwen-image：引擎侧有自己的分辨率表并会**静默吸附**——发 1760x992 出的是
//     1664x928。所以它只能枚举官方表，配面积档不会报错但会让界面说谎。
//     用的是 2512 版的七行（4:3 是 1472x1104，初版的 1140 不是精确 4:3）。
//   - z-image：**长边上限 1664**，发 2208x1248 出 1664x928（按比例缩回上限带）。
//     所以它也只能枚举。另外"必须被 64 整除"是网上的讹传：1472x1104 实测正常出图。
//   - hunyuan-image-3：只验过 auto（不发 size → 1024x1024，1.05MP），显式尺寸没测，
//     故**不给推荐档**——没验证过的东西不该摆在"一键填入"里。
//
// 按模型名精确匹配（lower+trim）。这里用模型名而不是引擎族声明是可以的：它只是个
// 一键填充的便利，填完运营看得见、能改；与"按模型名 substring 猜引擎"完全不是一回事。
export const IMAGE_SHAPE_PRESETS = {
  'sensenova-u1.5': {
    label: 'SenseNova-U1.5 官方 2K 档（实测）',
    aspectRatios: ['1:1', '3:2', '2:3', '16:9', '9:16'],
    sizeTiers: ['2048'],
    sizeAlign: 32,
  },
  'qwen-image': {
    label: 'Qwen-Image-2512 官方七档（实测，引擎会吸附到表内）',
    sizes: [
      '1328x1328',
      '1664x928',
      '928x1664',
      '1472x1104',
      '1104x1472',
      '1584x1056',
      '1056x1584',
    ],
    sizeAlign: 16,
  },
  'z-image': {
    label: 'Z-Image 档位（实测，长边上限 1664）',
    sizes: [
      '1664x1664',
      '1664x928',
      '928x1664',
      '1472x1104',
      '1104x1472',
      '1024x1024',
    ],
    sizeAlign: 16,
  },
};
// qwen-image-edit 与 qwen-image 同一套权重与分辨率表。
IMAGE_SHAPE_PRESETS['qwen-image-edit'] = IMAGE_SHAPE_PRESETS['qwen-image'];

export const getImageShapePreset = (model) =>
  IMAGE_SHAPE_PRESETS[
    String(model || '')
      .trim()
      .toLowerCase()
  ] || null;

// 图生图「输出尺寸」里的自动档：选中它就一个 size 字段都不下发，把画幅交回引擎。
//
// 两个自建引擎在"不传尺寸"时的行为正好相反,所以这一档不能省:
//   - vllm-omni(hunyuan-image-3)不传 = auto,由模型 AR 预测的 <img_ratio_*> 定画幅
//     (其 tests/entrypoints/test_image_task_request.py::test_no_size_hint_is_auto
//     专门守着这个行为),多图融合时这往往比人工指定更合适;
//   - lightx2v(qwen-image-edit)不传 = ImageTaskRequest.aspect_ratio 的默认值
//     "16:9",等于强制横屏——正是 4:3 底图出 16:9 成品那个 bug。
// 故默认仍选具体档位(qwen 需要),想要模型自己判断时手动切到这一档。
export const IMAGE_SIZE_AUTO = 'auto';

// 尺寸档位 → 宽高比数值。精确像素("1664x928")与比例词("16:9")两种写法都要认:
// 运营两种都在用(文生图历来填比例词),少认一种就会让那一半配置在自动选档和画幅
// 提示上变成哑弹。normalizeImageSize 已把 × 归一成 x,冒号原样保留。
export const sizeToRatio = (raw) => {
  const m = /^(\d+)\s*[x:]\s*(\d+)$/.exec(normalizeImageSize(raw));
  if (!m) return null;
  const a = Number(m[1]);
  const b = Number(m[2]);
  if (!a || !b) return null;
  return a / b;
};

// 从档位白名单里挑一个最贴合底图画幅的。
//
// 白名单是运营配的、模型确实支持的档位；底图只用来决定默认选中哪一档，绝不会
// 把底图的原生像素直接下发——那正是 gpt-image 这类只认固定档位的模型会报错的地方。
// 先精确匹配像素（qwen 系生成的图往往本就落在档位上，此时能原样保持），否则按
// 宽高比取最近的一档。比例距离用对数比值而不是差值：画幅是乘性的，log 让 16:9 与
// 9:16 这种互为倒数的关系对称，不会偏向数值大的一侧。
export const pickClosestSize = (dim, sizes) => {
  if (!dim || !dim.w || !dim.h || !Array.isArray(sizes) || sizes.length === 0) {
    return undefined;
  }
  const target = dim.w / dim.h;
  let best;
  let bestDiff = Infinity;
  sizes.forEach((raw) => {
    const value = normalizeImageSize(raw);
    if (!value || value === IMAGE_SIZE_AUTO) return;
    // 精确像素且与底图完全一致时直接命中（qwen 系生成的图往往本就落在档位上，
    // 此时能原样保持尺寸而不只是保持比例）。
    const px = /^(\d+)x(\d+)$/.exec(value);
    if (px && Number(px[1]) === dim.w && Number(px[2]) === dim.h) {
      best = value;
      bestDiff = -1;
      return;
    }
    if (bestDiff === -1) return;
    const ratio = sizeToRatio(value);
    if (!ratio) return;
    const diff = Math.abs(Math.log(ratio / target));
    if (diff < bestDiff) {
      bestDiff = diff;
      best = value;
    }
  });
  return best;
};

// 读一张 data-url 图片的原生像素。图片已在内存里，浏览器解到头部就能给出尺寸，
// 不产生网络请求；解不出（损坏/非图片）时返回 null 而不是抛错，让调用方跳过该张。
export const readImageDimensions = (dataUrl) =>
  new Promise((resolve) => {
    if (!dataUrl) {
      resolve(null);
      return;
    }
    const img = new Image();
    img.onload = () =>
      resolve(
        img.naturalWidth && img.naturalHeight
          ? { w: img.naturalWidth, h: img.naturalHeight }
          : null,
      );
    img.onerror = () => resolve(null);
    img.src = dataUrl;
  });

// 引擎族取值口径与视频/语音/音乐一致：lower + trim（那三处是为了与后端比较；图像这边
// 后端没有对应物，仍保持同一口径，免得运营在四个分类之间形成两套记忆）。
const normalizeEngine = (v) =>
  typeof v === 'string' ? v.trim().toLowerCase() : '';

// 分辨率档列表：正整数（边长基准 px），去重后从小到大。
const normalizeTierList = (list) =>
  Array.isArray(list)
    ? Array.from(
        new Set(
          list
            .map((x) => parseInt(String(x).trim(), 10))
            .filter((n) => Number.isFinite(n) && n > 0),
        ),
      ).sort((a, b) => a - b)
    : [];

// 默认像素对齐粒度。实测 SenseNova-U1.5 的引擎按 32 上取整（发 2368x1776 出 2368x1792），
// 用 32 算出来的就是最终值、所见即所得；且 32 对齐能逐个精确复现它官方那五档。
export const DEFAULT_IMAGE_SIZE_ALIGN = 32;

// 「面积档 × 比例」→ 精确像素。
//
//   面积 A = base²，h = √(A/r)，w = r·h，两边各自**向下**取整到 align 的倍数。
//
// 向下而不是四舍五入：宁可比档位略小，也不要越过模型的显存/训练上限。实测这条对
// SenseNova-U1.5 官方五档是精确命中的（base=2048、align=32）：
//   1:1 → 2048x2048   3:2 → 2496x1664   16:9 → 2720x1536（原始算出 2730.67）
//
// ratio 支持 "16:9" 与 "1664x928" 两种写法（sizeToRatio 已经统一）。取不到比例或档位
// 非法时返回 ''，调用方据此退回"不下发 size"。
export const computeImageSize = (ratio, tierBase, align) => {
  const r = sizeToRatio(ratio);
  const base = parseInt(tierBase, 10);
  const step =
    parseInt(align, 10) > 0 ? parseInt(align, 10) : DEFAULT_IMAGE_SIZE_ALIGN;
  if (!r || !Number.isFinite(base) || base <= 0) return '';
  const area = base * base;
  const h = Math.sqrt(area / r);
  const floorTo = (v) => Math.max(step, Math.floor(v / step) * step);
  return `${floorTo(r * h)}x${floorTo(h)}`;
};

// 引擎族：模型级声明，不随 tab 变。未声明返回空串 = 用通用模板。
// 判据是配置声明而不是模型名 substring —— 前端拿对外模型名、后端拿渠道重定向后的
// 上游名，靠名字判两边必然分叉（与 getEngineForVideoModel / getEngineForMusicModel 同）。
export const getEngineForImageModel = (config, model) =>
  config?.models?.[model]?.engine || '';

// 解析 status 中的 ImageModelSizeConfig（字符串或对象）
// models[name] 统一产出 { sizes:[], capabilities:[] }；兼容旧形态（值为尺寸数组）
export const parseImageSizeConfig = (raw) => {
  if (!raw) return { default: FALLBACK_IMAGE_SIZES, models: {} };
  try {
    const parsed = typeof raw === 'string' ? JSON.parse(raw) : raw;
    const defaults = normalizeSizeList(parsed.default);
    const models = {};
    if (parsed.models && typeof parsed.models === 'object') {
      Object.entries(parsed.models).forEach(([model, cfg]) => {
        if (Array.isArray(cfg)) {
          // 旧形态：值为尺寸数组，无能力声明
          models[model] = { sizes: normalizeSizeList(cfg), capabilities: [] };
        } else {
          models[model] = {
            sizes: normalizeSizeList(cfg?.sizes),
            capabilities: normalizeCapabilityList(cfg?.capabilities),
            // 引擎族声明。**白名单式重建,漏了它 = 管理页每保存一次就把它删一次**
            // (与视频那份 parse 同一类坑)。图像这边它只决定优化模板走哪份,
            // 丢了不报错、只是 SenseNova-U1.5 悄悄退回通用模板。
            engine: normalizeEngine(cfg?.engine),
            // 像素对齐粒度(模型级)。同上,漏了就是保存一次删一次;丢了会退回默认 32,
            // 对 Qwen-Image 这类 16 对齐的模型算出来的档位会整体错开。
            sizeAlign:
              parseInt(cfg?.sizeAlign, 10) > 0
                ? parseInt(cfg.sizeAlign, 10)
                : null,
            tabs: normalizeImageTabs(cfg?.tabs),
          };
        }
      });
    }
    return {
      default: defaults.length > 0 ? defaults : FALLBACK_IMAGE_SIZES,
      models,
    };
  } catch (e) {
    return { default: FALLBACK_IMAGE_SIZES, models: {} };
  }
};
