// 体验区「分类 → tab」中央元数据：新「体验区管理」admin 页与各页 tab 显隐过滤
// 共用的唯一真相源。key=稳定标识（分类=侧栏 itemKey / 存储配置键；tab=各页 mode
// key，不用显示名以免改名破坏配置）；capability=该 tab 过滤模型用的能力标签。
// 文本模型（对话）无 tab、无能力标签、无媒体配置（靠排除媒体模型过滤），仅参与分类显隐。
//
// ── tab 专用配置（fields）────────────────────────────────────────────────
// 每个 tab 声明自己「真正用得到」的配置字段。同一模型挂多个能力时，各 tab 的参数
// 互不干扰（存储见下），运营也只会看到当前 tab 用得上的输入框——不再出现「数字人
// 配了尺寸但页面根本不显示尺寸选择器」这类无效项。
// 这份 schema 同时喂给两侧：admin 页决定渲染哪些输入框，体验区决定显示哪些控件，
// 避免两边各写一套 if 判断而对不上（历史上 VideoConfigPanel / 手机端 Video.jsx 各
// 硬编码过一份）。
//
// ── 存储形态 ────────────────────────────────────────────────────────────
// 仍复用四份 option（ImageModelSizeConfig / VideoModelConfig / AudioModelConfig /
// MusicModelConfig），在 models[name] 下新增 tabs 子层：
//   "wan2.2": {
//     "pipeline": true,                       // 模型级（不随 tab 变）
//     "capabilities": ["文生视频","图生视频"],  // 由 tabs 的键派生回写，供模型广场用
//     "tabs": {
//       "text2video":  { "sizes": [...], "durations": [...], "aspectRatios": [...] },
//       "image2video": { "durations": [...], "maxInputMB": 20 }
//     },
//     "durations": [...]                       // 模型级兜底（迁移写入，直连请求兜底用）
//   }
// 读取优先级：tab 级 → 模型级 → 分类 default → 内置兜底。老配置（无 tabs）读到的
// 就是模型级，行为与改造前一致。
//
// storeIn：个别 tab 的入口分类与模型配置所在的 option 不是一份——「视频配音」入口挂
// 在语音页，但产物是视频、模型配在 VideoModelConfig。缺省=所属分类的 configKey。
//
// promptOptimize：该 tab 的提示词框是否提供「AI 优化提示词」。开关、优化用的语言模型
// 与优化系统提示词全由运营在体验区管理里配（用户不选模型），见 PlaygroundTabConfig
// 的 __global.promptOptimize 与各 tab 的 promptOptimize。

// 字段元信息：admin 页据此渲染输入控件，也给体验区/校验提示复用。
// type: list=字符串列表（Semi Select tags）；int=非负整数（0/留空=不限）；
//       translation=中译英开关+默认语言模型（音乐专用复合项）。
export const PLAYGROUND_FIELD_META = {
  sizes: {
    label: '尺寸 / 分辨率',
    type: 'list',
    placeholder: '如 1280x720、1920x1080、1080P',
    help: '留空=该 tab 不展示尺寸选择器（纯 opt-in）。档位词（1080P）与精确像素混用由引擎判定，服务端不校验。',
  },
  durations: {
    label: '时长（秒）',
    type: 'list',
    placeholder: '如 5、10',
    help: '留空=不限制。服务端会按 task_type 对应的 tab 校验，直连请求同样受限。',
  },
  aspectRatios: {
    label: '宽高比',
    type: 'list',
    placeholder: '如 16:9、9:16、1:1',
    help: '留空=该 tab 不展示宽高比选择器。图像玩法里它与下面的「分辨率档」配合：两者都配=按「面积档 × 比例」算出精确像素下发（推荐）；只配比例=下发比例词，由引擎按自己的离散表定画幅（分辨率通常偏低）。',
  },
  // 图像专用。**它不是短边档,是面积档**(总像素预算,值为边长基准 px,面积 = 基准²)。
  //
  // 为什么是面积而不是短边:实测 SenseNova-U1.5 官方五档是等面积阶梯(4.15–4.19M,
  // 离散度 1.0%)而短边在 1536/1664/2048 之间跳。用面积基准 2048 + 32 对齐能逐个
  // 精确复现那五行;用短边档一行都对不上。视频那边用短边是因为视频引擎按短边校验
  // (H3 的 short_edge),两者不能照搬。
  sizeTiers: {
    label: '分辨率档（面积基准 px）',
    type: 'list',
    placeholder: '如 2048（=2048² 的像素预算）',
    help: '留空=不展示分辨率档选择器，画幅只由上面的宽高比决定。填了以后与宽高比组合算出精确像素：面积 = 基准²，再按该比例分配长短边并对齐。配一个值就只有一档（不展示选择器、直接生效），配多个则用户可选。⚠️ **配之前务必实测一次**：引擎超限时不会报错，而是按比例缩回自己的上限带，界面写的像素和实际出图就对不上了。实测 sensenova-u1.5 表外档位（2336×1760）原样生效，而 z-image 的长边上限是 1664（请求 2208×1248 实际出 1664×928），所以 z-image 应该用上面的「尺寸/分辨率」枚举、而不是本项。',
  },
  maxInputMB: {
    label: '上传文件上限（MB）',
    type: 'int',
    help: '0 或留空=不限制。服务端物化输入时兜底，防直连绕过前端。',
  },
  maxAudioSec: {
    label: '驱动音频时长上限（秒）',
    type: 'int',
    help: '0 或留空=不限制。同时用作数字人 video_duration 的下发默认值。',
  },
  // ── 参考素材：三个模态各配各的，不合并成一个总数闸 ────────────────────
  // 引擎那边确实还有一道跨模态总数上限（H3 是 12），但它由服务端兜底
  // （adaptor.go 的 maxR2VARefTotal），前端不重复实现：运营真正要调的是「这个玩法
  // 开放几张图 / 几个视频」，而不是去凑一个总数。
  maxRefImages: {
    label: '参考图张数上限',
    type: 'int',
    help: '留空=用内置默认（参考生视频 9 张、图生视频 3 张）。填 0=不展示参考图上传框——仅对「参考生视频」（可改用参考视频）与「视频编辑」（参考图本就可选）有意义；「图生视频」的参考图是唯一输入，填 0 会被当作 1，要停用该玩法请在上方关掉这个 tab。填得比内置默认大无效（只能收窄）。',
  },
  maxRefVideos: {
    label: '参考视频个数上限',
    type: 'int',
    help: '0 或留空=不展示参考视频上传框（纯 opt-in，与尺寸/宽高比同风格）。填 N 则展示 N 个上传槽。引擎上限 3，填更大也按 3 生效。',
  },
  refVideoMaxMB: {
    label: '单个参考视频上限（MB）',
    type: 'int',
    help: '0 或留空=不限制。与「上传文件上限」分开配：参考图与参考视频的合理体积差一个量级，共用一个旋钮必然有一边被误伤。',
  },
  refVideoMaxSec: {
    label: '单段参考视频时长上限（秒）',
    type: 'int',
    help: '0 或留空=不限制。体积挡不住时长——几 MB 的低码率视频也可能很长。选完文件当场校验，浏览器解不出时长则放行。⚠️ 引擎另有「所有参考视频加起来 ≤15 秒」的硬约束，这条前后端都不校验：开 N 个槽位时请把本项配到 15/N 以内（如 3 个槽配 5 秒），否则用户能把槽位传满、却在提交时才被引擎拒。另：引擎要求每段 ≥2 秒，下限同样无人校验。',
  },
  maxChars: {
    label: '文本字数上限',
    type: 'int',
    help: '0 或留空=不限制。',
  },
  refAudioMaxMB: {
    label: '参考音上限（MB）',
    type: 'int',
    help: '0 或留空=不限制。',
  },
  // 「视频生音」已下线，没有 tab 认领这个字段，但服务端护栏
  // （MusicVideoMaxBytesForModel）仍在跑。放进 FIELD_META 是为了让它能在「分类默认值」
  // 和「按模型交叉检查」的孤儿字段里被编辑到——否则值照样生效，运营却改不了。
  videoMaxMB: {
    label: '视频输入上限（MB）',
    type: 'int',
    help: '0 或留空=不限制。体验区已无「视频生音」入口，此项只对直连请求生效。',
  },
  translation: {
    label: '提示词中译英',
    type: 'translation',
    help: '开启后中文提示词提交前自动译英（文本编码器只认英文的模型需要）。',
  },
};

// 引擎族标识与可选值。**定义在这里而不是 videoPlayground.constants.js**：后者已经
// import 本文件的 tabScopedValue，反向再 import 就成环了。videoPlayground 从这里再导出，
// 依赖方向保持单向。
//
// 取值必须与后端 common.VideoEngineMinimaxH3 一致（后端比较前 lower+trim）。
export const VIDEO_ENGINE_MINIMAX_H3 = 'minimax-h3';

// LTX-2.5：22B 音视频联合扩散（画面与音轨同步生成），24fps + 8k+1 帧栅格，时长走顶层
// num_frames。与 wan 的 4n+1 @16fps、H3 的 17n+5 @24fps 都不同。
// 取值必须与后端 common.VideoEngineLTX25 一致（后端比较前 lower+trim）。
//
// ⚠️ 这一项**必须出现在下拉里**，不是可选的补全：后端 ltx25.go 那套整形（秒→帧换算、
// seconds 剥离、尺寸与显存包络准入）只在 engine === 'ltx-2.5' 时才跑，而 engine 的唯一
// 来源就是运营在这个下拉里选的值。少了这一项，那套代码一次都不会被触发 —— 而症状是
// 每个带时长的请求 500（整数秒 × 24 恒 ≡ 0 mod 8，取不到 8k+1 需要的余数 1）。
export const VIDEO_ENGINE_LTX25 = 'ltx-2.5';

// 「画布由网关按比例合成」的引擎族。这些引擎**只认请求里的 width/height**，画布必须
// 由请求给出，网关据比例 + 档位词合成精确像素（后端 ltx25ApplyCanvas）。
//
// 它决定的是关键帧类玩法要不要下发比例。默认那条路（H3 / wan）恰恰相反：引擎按
// images[0] 自己推画布，传来的比例被静默忽略，体验区靠 composeImageToRatio 改图来表达
// 画幅，比例字段**不能**发——发了会与改过的图打架（第三方渠道认 ratio，图已是 16:9 又
// 收到一个 9:16，出片按谁的来取决于渠道）。
//
// ⚠️ 判据必须是这张显式名单，不能写成「非 H3」：那样每接入一个新的自建引擎都会被默认
// 归进这一类，而这一类的错是「画幅静默不对」。名单外的引擎维持原行为。
export const VIDEO_ENGINES_GATEWAY_CANVAS = [VIDEO_ENGINE_LTX25];

export const VIDEO_ENGINE_OPTIONS_INLINE = [
  {
    value: '',
    label: '默认（LightX2V 系：wan / seedvr2 / infinitetalk / bernini）',
  },
  { value: VIDEO_ENGINE_MINIMAX_H3, label: 'MiniMax H3（vLLM-Omni）' },
  { value: VIDEO_ENGINE_LTX25, label: 'LTX-2.5（vLLM-Omni，音视频同步生成）' },
];

// 语音引擎族。同上，定义在这里、由 audioPlayground.constants.js 再导出。
// 取值必须与后端 common.AudioEngineIndexTTS25 一致（后端比较前 lower+trim）。
export const AUDIO_ENGINE_INDEXTTS25 = 'indextts2.5';

export const AUDIO_ENGINE_OPTIONS_INLINE = [
  { value: '', label: '默认（IndexTTS-2 及其他语音模型）' },
  { value: AUDIO_ENGINE_INDEXTTS25, label: 'IndexTTS-2.5（vLLM-Omni）' },
];

// 音乐引擎族。同上，由 musicPlayground.constants.js 再导出。
// 取值必须与后端 common.MusicEngineMinimaxMusic3 一致（后端比较前 lower+trim）。
export const MUSIC_ENGINE_MINIMAX_MUSIC3 = 'minimax-music3';

// 只列 MiniMax-Music3:AudioX/SoulX 下线后音乐页只剩 ACE-Step 与 Music3 两族，
// 而三个玩法的默认引擎都是 ACE-Step，需要模型级覆盖的只有「与 ACE-Step 同挂文生音乐」
// 的 MiniMax-Music3。多列几个只会让运营选到与玩法不匹配的引擎，而后果是静默的
// （拿到该引擎不认的键、必需的键一个不发）。与视频侧同一取舍。
export const MUSIC_ENGINE_OPTIONS_INLINE = [
  { value: '', label: '默认（ACE-Step）' },
  { value: MUSIC_ENGINE_MINIMAX_MUSIC3, label: 'MiniMax-Music3（vLLM-Omni）' },
];

// 图像引擎族。**与上面三个不是一回事,必须说清**:视频/语音/音乐的 engine 同时驱动后端
// 的请求整形(帧数约定、extra_params 折叠、引擎分支),选错会静默出错档;图像这一层
// **后端没有对应物**(common 里没有 ImageEngineFamilyForModel),它今天唯一的作用是
// 决定「AI 优化提示词」用哪份内置模板。选错的后果仅仅是模板回落到通用版,不影响出图。
//
// SenseNova-U1.5:统一多模态模型,能在画面里渲染可读文字,擅长海报/信息图/UI 稿。
// 它的官方推理链路里本来就有一步 Prompt Enhancement(由更强的 LLM 改写提示词),
// 与体验区这个「AI 优化提示词」按钮是同一件事 —— 所以给它配专用模板不是锦上添花,
// 是把官方要求的那一步补上。模板见 promptOptimize.constants.js。
export const IMAGE_ENGINE_SENSENOVA_U15 = 'sensenova-u1.5';

export const IMAGE_ENGINE_OPTIONS_INLINE = [
  { value: '', label: '默认（通用扩散模型）' },
  {
    value: IMAGE_ENGINE_SENSENOVA_U15,
    label: 'SenseNova-U1.5（统一多模态，强文字渲染）',
  },
];

// 模型级（不随 tab 变化）的字段：单独渲染在模型行上，不进 tabs 子层。
export const PLAYGROUND_MODEL_LEVEL_FIELDS = {
  ImageModelSizeConfig: [
    {
      key: 'sizeAlign',
      label: '像素对齐粒度',
      type: 'int',
      placeholder: '留空=32',
      help: '「面积档 × 比例」算出来的长宽会各自向下取整到它的倍数。留空=32。⚠️ 这个值要跟引擎一致，否则会出现「界面显示 2368×1776、实际出图 2368×1792」这种对不上：实测 SenseNova-U1.5 的引擎按 32 上取整，而 Qwen-Image 官方表是 16 的倍数且没有一个是 64 的倍数（1328/64=20.75），所以不能全局写死。属模型能力，与 tab 无关。',
    },
    {
      key: 'engine',
      label: '引擎族',
      type: 'select',
      options: IMAGE_ENGINE_OPTIONS_INLINE,
      help: '只影响「AI 优化提示词」用哪份内置模板，不改变发给上游的请求（图像这边后端没有按引擎族分支）。选 SenseNova-U1.5 后用的是**官方原文模板**：文生图走官方 Image PE，**产物是一段 Render JSON**（那段 JSON 原样就是提示词，直接提交即可，不必再翻译成句子）——点完优化看到输入框里是 JSON 属正常；图生图走官方 Edit Instruction Rewriter，产物仍是自然语言指令。两者都会把可见文案逐字保留、数量与版面写死、并显式列出排除项，与通用模板差别很大。⚠️ 官方 Image PE 只认五档 2K 画布（2048x2048 / 2496x1664 / 1664x2496 / 2720x1536 / 1536x2720），请在上方「尺寸 / 分辨率」里配齐，否则会按默认档出图、分辨率远低于官方示例。选错引擎族不会报错，只是优化效果退回通用版。属模型能力，与 tab 无关。',
    },
  ],
  VideoModelConfig: [
    {
      key: 'pipeline',
      label: '自建引擎（可编排超分/配音/插帧）',
      type: 'bool',
      help: '该模型是否跑在自建 gpustackplus 引擎上。超分、配音、插帧都是自建引擎特有的玩法：第三方渠道（Sora/MiniMax 等）原生直出、也不认识 target_fps，参数必须原样透传。未勾选 = 一律透传，新接入的第三方模型天然安全。属模型能力，与 tab 无关。',
    },
    {
      key: 'nativeDelivery',
      label: '高分辨率档用纯放大（不接超分模型）',
      type: 'bool',
      help: '勾选后，选中高分辨率档时**不再跑第二段超分**：模型按原生档生成，由引擎在出片前把画面纯放大到交付档（lanczos + 轻锐化）。超分模型在干净的 AIGC 素材上已实测是净负收益——把一段完美合成片喂进管线，生成后帧间平滑度指标 1.11，过 SwiftVR 后掉到 0.56（画面「两帧一顿」的沸腾感），暗部噪点幅度放大 1.88 倍；它锐度确实高一倍，但那份「更锐」完全由沸腾贡献。纯放大还省掉一次编解码损失，以及 4×A100 跑 40 多秒的开销（纯放大 8 核 CPU 6 秒、不占卡）。⚠️ **必须等引擎侧支持了交付缩放才能勾**：没上线时下发的交付短边会被引擎静默丢弃，用户选了 1080P 却拿到原生档、还不报错。未勾选 = 维持原有两段式，新接入模型天然安全。用户自己上传视频走的「超分」玩法不受本开关影响。',
    },
    {
      key: 'upscale',
      label: '超分档位',
      type: 'upscale',
      help: '给这个模型额外提供「先低档位生成、再交超分模型放大」的尺寸档位。留空=不提供（纯 opt-in）。起步档位必选，候选取该模型已配的、比目标更低的档位——没选或目标档位本身已在已配档位里，这条规则不生效。超分档位本质是**短边档**：1080P / 2K / 4K 说的都是短边，长边由源视频的画幅决定。三种写法等价，都会取出短边下发给引擎（1080 / 1440 / 2160），引擎按源的真实画幅等比放大到该短边——画幅零形变、短边精确命中：①档位词 1080P；②短边档位词 2K / 4K；③精确像素串 2560x1440（只取其短边）。⚠️ **出片长边不等于档位名隐含的数**：源 1344×768（H3 的 768P 实际画幅）选 2K 出的是2520×1440 而非 2560×1440，选 1080P 出的是 1890×1080 而非 1920×1080——这是对的，短边精确命中、画幅不失真；硬凑长边反而要横向拉伸约 1.6%。超宽画幅（21:9）选高档位时，超出显存预算的部分会被引擎按面积等比压回，画幅仍保持。注：只有支持按请求给尺寸的超分引擎（SwiftVR）会响应；SeedVR2 只认自己部署 config 里的档位，对它这个字段是惰性的、行为不变。',
    },
    {
      key: 'engine',
      label: '引擎族',
      type: 'select',
      options: VIDEO_ENGINE_OPTIONS_INLINE,
      help: '决定后端按哪套约定整形请求（帧数约定 / 时长字段 / 画布推导）。MiniMax H3 与 LightX2V 系在这三处完全不同，选错不会报错、只会静默出错档。属模型能力，与 tab 无关。',
    },
    {
      key: 'defaultSteps',
      label: '采样步数',
      type: 'int',
      placeholder: '留空=引擎族默认（H3 为 20）',
      // 与引擎族正交的理由见后端 common.VideoInferenceStepsForModel：蒸馏版必须照样
      // 声明 engine 才能拿到请求整形，若步数只能按引擎族给，它就会被强塞基座档。
      help: '蒸馏版（如 Turbo8 标定 8 步）与基座共用引擎族但步数不同，须按模型单独配。留空则按引擎族默认。属模型能力，与 tab 无关。',
    },
  ],
  AudioModelConfig: [
    {
      key: 'engine',
      label: '引擎族',
      type: 'select',
      options: AUDIO_ENGINE_OPTIONS_INLINE,
      help: 'IndexTTS-2.5 是 2 的能力超集，额外支持语速、语种、文本归一化三项。声明后体验区才会展示这些控件、后端才会把 lang / text_normalization 折进 extra_params。选错不会报错，只会让 2.5 的独有能力整体不可见。属模型能力，与 tab 无关。',
    },
  ],
  MusicModelConfig: [
    {
      key: 'engine',
      label: '引擎族',
      type: 'select',
      options: MUSIC_ENGINE_OPTIONS_INLINE,
      help: '玩法默认引擎是 ACE-Step，同一玩法挂了别的引擎的模型时必须在这里声明。MiniMax-Music3 也是文生音乐，不声明就会走 ACE-Step 分支：拿到 lyrics/thinking 这些它不认的键，而它必需的 instructions 一个都不下发，引擎直接 400。属模型能力，与 tab 无关。',
    },
  ],
};

export const PLAYGROUND_CATEGORIES = [
  {
    key: 'playground',
    label: '文本模型',
    configKey: null,
    tabs: [],
  },
  {
    key: 'image',
    label: '图像模型', // 由「图片模型」改名
    configKey: 'ImageModelSizeConfig',
    tabs: [
      // sizes / aspectRatios / sizeTiers 三者的关系见 PLAYGROUND_FIELD_META.sizeTiers
      // 与 getImageShapeConfig:配了什么就出什么控件,不需要另外声明"规则"。
      {
        key: 'text2image',
        label: '文生图',
        capability: '文生图',
        fields: ['sizes', 'aspectRatios', 'sizeTiers'],
        promptOptimize: true,
      },
      {
        key: 'image2image',
        label: '图生图',
        capability: '图生图',
        fields: ['sizes', 'aspectRatios', 'sizeTiers'],
        promptOptimize: true,
      },
    ],
  },
  {
    key: 'video',
    label: '视频模型',
    configKey: 'VideoModelConfig',
    tabs: [
      {
        key: 'text2video',
        label: '文生视频',
        capability: '文生视频',
        // 纯文本输入：没有上传，故无 maxInputMB。
        fields: ['sizes', 'durations', 'aspectRatios'],
        promptOptimize: true,
      },
      {
        key: 'image2video',
        label: '图生视频',
        capability: '图生视频',
        // 画幅跟随输入图，尺寸/宽高比不可选。
        // 不给参考视频三项：Bernini r2v 只吃参考图，开了框也无处可发。
        fields: ['durations', 'maxInputMB', 'maxRefImages'],
        promptOptimize: true,
      },
      {
        key: 'flf2v',
        label: '关键帧',
        capability: '关键帧',
        // sizes 被 fieldLocks 锁死成 768P，运营改不了 —— 这不是产品策略，是引擎硬约束。
        //
        // H3 关键帧（i2va/l2va/fl2va）的画布**永远由引擎按 images[0] 推**：网关刻意不
        // 下发 width/height（minimax_h3.go 的 h3ApplyCanvas 只对 t2v/r2va 生效），而引
        // 擎那条自算路径把 short_edge 硬校验成 768。填别的档位不会报错：档位词会被
        // h3DropResolutionToken 清掉、引擎照旧出 768 —— 静默失效，最难查的一类。
        //
        // 锁死而不是「填个默认值 + 写行提醒」，是因为光有提醒挡不住真正的漏法：
        // getSizesForVideoModel 的取值是 tab 级 → 模型级 → 分类默认值三级回落，运营
        // 为文生视频配的 480P 会顺着回落链漏到这个 tab 上 —— 关键帧一个字没填却冒出
        // 失效档位，那种情况下提醒根本不会被看到。
        //
        // 那为什么还要保留 sizes 这个字段？因为超分档的闸门是 sendsSize（见
        // useVideoGeneration 的 sizeChoices）：字段不在 fields 里，连 1080P 超分档都出
        // 不来，关键帧永远用不上「先出 768P 再走 sr 模型」那条流水线。
        //
        // 于是两条路都不需要网关算画布，这正是关键帧能开放分辨率选择的原因：
        // 选 768P 走原生（档位词被清掉＝引擎默认），选 1080P 走超分段（起步档同样是
        // 768P，一样被清掉）。1080P 由模型级「超分档位」规则提供，运营可加可不加。
        //
        // aspectRatios 给，但它在这个 tab 里的语义与文生视频**完全不同**：引擎对关键帧
        // 静默忽略传来的比例，所以体验区不发比例字段，而是在提交前把图本身改成该比例
        // （居中裁剪或虚化补边，纯浏览器 canvas，见 helpers/imageCompose.js）。选择器里
        // 会自动多出「跟随上传素材」并默认选中 = 完全不干预。留空则整个选择器不出现。
        fields: ['sizes', 'durations', 'maxInputMB', 'aspectRatios'],
        fieldLocks: {
          sizes: {
            value: ['768P'],
            reason:
              '引擎按首图推画布、短边硬校验为 768，配其它档位不会报错也不会生效（仍出 768P），故此项锁定、不开放编辑。要额外提供 1080P，请在下方模型级「超分档位」加一条 768P → 1080P 的规则——先出 768P 再自动接超分模型；不加则只有 768P 一档。出片分辨率由超分模型的部署配置决定（现网 1920×1080），并自动跟随首图朝向：横图出 1920×1080、竖图出 1080×1920、方图出 1080×1080。',
            // 这把锁描述的是**某些引擎**的硬约束，不是关键帧这个玩法本身的性质。
            // exemptEngines 列出「自己认 width/height、不需要被锁」的引擎族。
            //
            // 为什么是豁免名单而不是「只对 minimax-h3 生效」的适用名单：wan 的两类
            // 关键帧实例（engine 留空）自这把锁上线起就一直吃着它，改成适用名单等于
            // 顺手把它们解锁、让运营为文生视频配的档位顺着三级回落漏进关键帧——那正是
            // 当初加锁要挡的事，且症状静默。要动 wan 得单独论证，不该搭这次的车。
            //
            // LTX-2.5 必须豁免：它**认请求里的 width/height**（首帧图由引擎等比放大到
            // 覆盖后居中裁剪去适配画布），不是按图推画布。锁不解，它的关键帧会拿到
            // '768P' 这个档位词发给引擎——而清档位词的 h3DropResolutionToken 是 H3 专属的
            // （minimax_h3.go），LTX 这条路上没有，引擎收到非 WIDTHxHEIGHT 的 size 直接报错。
            exemptEngines: [VIDEO_ENGINE_LTX25],
          },
        },
        promptOptimize: true,
        taskTypeChoices: [
          { value: 'flf2v', label: '首尾帧（flf2v，尾帧必填）' },
          { value: 'i2v', label: '只吃首帧（i2v）' },
          {
            value: 'auto',
            label: '按输入派生·三态全支持（如 MiniMax H3）',
          },
          {
            value: 'auto_fl',
            label: '按输入派生·不支持仅尾帧（如 Seedance 2.0）',
          },
        ],
        hint: '「关键帧」承载三类模型。wan 那两类是同权重、不同启动参数的两个引擎实例，task 在实例启动期就定死，认错会静默丢尾帧或直接崩，所以必须显式选：首尾帧（flf2v，尾帧必填）或只吃首帧（i2v）。第三类是 MiniMax H3 这种一个 checkpoint 同时吃首帧/尾帧/首尾帧的模型（靠 frame_indices 区分），选「按输入派生」后首尾两槽都可选、至少填一个，提交时按实际填了哪个派生 i2v / l2va / flf2v。第四类是 Seedance 2.0：首帧与首尾帧都支持，但官方互斥场景里没有「仅尾帧」，选「不支持仅尾帧」后首帧必填、尾帧可选。留空则回退到看模型名里含不含 flf2v——那要求上游名与对外名都带这个标识，做了模型重定向时容易错配。',
      },
      {
        key: 'r2va',
        label: '参考生视频',
        capability: '参考生视频',
        // 参考音频有长度闸（2-15s）。参考视频三项默认全空 = 不开放视频上传，运营按需 opt-in。
        //
        // sizes / aspectRatios：Ref2VA **接受具名 aspect_ratio**（不传默认 16:9），参考图
        // 不绑定输出画布，与「关键帧」不同 —— 那边画幅永远跟随上传的图、传比例也被引擎
        // 静默忽略（见 flf2v tab 的注释）。所以这里能算画布，
        // 也就能出 480P 档 —— 不配的话引擎按 short_edge=768 自推，每条多花一倍时间
        // （768p 约 190s vs 480p 量级）。
        fields: [
          'sizes',
          'aspectRatios',
          'durations',
          'maxInputMB',
          'maxAudioSec',
          'maxRefImages',
          'maxRefVideos',
          'refVideoMaxMB',
          'refVideoMaxSec',
        ],
        promptOptimize: true,
        hint: '参考图/视频/音频 → 带语音的视频。挂 MiniMax H3 Ref2VA 与 Seedance 2.0 两类模型，输入字段已在后端统一，前端不按渠道分支。⚠️ 两家规格取最小交集：图片边长 [300,5760]（Seedance 下限更严）、单参考视频 ≤50MB（H3 更严）、参考视频 ≤3 个且总时长 ≤15s。独立音频必须搭配至少一个视觉参考，两家规则一致。',
      },
      {
        key: 's2v',
        label: '数字人',
        capability: '数字人',
        // 时长由驱动音频决定，不给选。
        fields: ['maxInputMB', 'maxAudioSec'],
        promptOptimize: true,
      },
      {
        key: 'vace',
        label: '视频编辑',
        capability: '视频编辑',
        fields: ['durations', 'maxInputMB'],
        promptOptimize: true,
      },
    ],
  },
  {
    key: 'audio',
    label: '语音模型',
    configKey: 'AudioModelConfig',
    tabs: [
      {
        key: 'emotion',
        label: '情感合成',
        capability: '情感合成',
        fields: ['maxChars', 'refAudioMaxMB'],
      },
      {
        key: 'synthesis',
        label: '语音合成',
        capability: '语音合成',
        fields: ['maxChars', 'refAudioMaxMB'],
      },
      {
        key: 'dialogue',
        label: '双人对话',
        capability: '双人对话',
        fields: ['maxChars', 'refAudioMaxMB'],
      },
      {
        // 声线描述纯文本，无参考音上传。
        key: 'design',
        label: '声音设计',
        capability: '声音设计',
        fields: ['maxChars'],
      },
      // 视频配音：入口挂在语音页，产物是视频（走 VideoPlaygroundBody mode=dub），
      // 模型也配在 VideoModelConfig —— 故 storeIn 指回视频配置。
      {
        key: 'dub',
        label: '视频配音',
        capability: '视频配音',
        storeIn: 'VideoModelConfig',
        fields: ['maxInputMB'],
        promptOptimize: true,
      },
    ],
  },
  {
    key: 'music',
    label: '音乐模型',
    configKey: 'MusicModelConfig',
    tabs: [
      {
        // 文生音乐挂「AI 优化提示词」。界面上永远只有这一个按钮，但**底下按引擎族走
        // 两条实现**，因为两族的描述位语义不同：
        //   ACE-Step → draftPlan 分支：一次产出 caption/歌词/BPM/调式/时长，caption 回填
        //              输入框、其余回填左侧控件。不能换成通用的"只重写输入框"——填了歌词
        //              提交才不走 sample_mode，否则引擎用自己推的时长覆盖用户选的值。
        //   MiniMax-Music3 → 通用优化 + 专用模板：它没有 BPM/调式/时长这些位，
        //              draftPlan 回填的字段无处可去；描述位是 instructions
        //              （官方 Structured Caption），正是优化模板能发力的地方。
        //   分流在两处实现：draftAvailable 排除 Music3，优化模板按 engine 换；
        //   渲染侧用 !onDraftPlan 取反，保证两条实现不会同时出按钮。
        //
        // 没有 translation 字段：ACE-Step 的文本编码器认中文，本就不需要中译英；此前
        // 挂它是因为 draftPlan 曾借用中译英那个下拉挑模型，现在不借了。
        key: 't2m',
        label: '文生音乐',
        capability: '文生音乐',
        fields: ['maxChars'],
        promptOptimize: true,
      },
      {
        key: 'cover',
        label: '音乐改编',
        capability: '音乐改编',
        fields: ['maxChars', 'refAudioMaxMB'],
      },
      {
        key: 'repaint',
        label: '音乐重绘',
        capability: '音乐重绘',
        fields: ['maxChars', 'refAudioMaxMB'],
      },
    ],
  },
];

export const getPlaygroundCategory = (key) =>
  PLAYGROUND_CATEGORIES.find((c) => c.key === key) || null;

export const getPlaygroundTab = (categoryKey, tabKey) =>
  getPlaygroundCategory(categoryKey)?.tabs.find((t) => t.key === tabKey) ||
  null;

// 该 tab 的模型配置落在哪份 option。
export const getTabStoreKey = (categoryKey, tabKey) => {
  const cat = getPlaygroundCategory(categoryKey);
  const tab = cat?.tabs.find((t) => t.key === tabKey);
  if (!tab) return null;
  return tab.storeIn || cat.configKey || null;
};

export const getTabFields = (categoryKey, tabKey) =>
  getPlaygroundTab(categoryKey, tabKey)?.fields || [];

// 体验区面板据此决定「这个玩法要不要显示某控件」，不再各自硬编码 mode 判断。
export const tabHasField = (categoryKey, tabKey, field) =>
  getTabFields(categoryKey, tabKey).includes(field);

// 该 tab 下被**引擎硬约束**锁死的字段：返回 { value, reason } 或 null。
//
// 与「运营没配」不是一回事：锁死的字段压根不接受配置，运营配了也不作数。admin 页据此
// 渲染成只读并展示 reason，体验区据此绕过 getXxxForModel 的三级回落直接用 value。
// 两侧读同一个函数是关键 —— 各写一份判断，迟早出现「管理端显示 A、体验区用 B」。
//
// engine 是所选模型声明的引擎族（VideoModelConfig.models[name].engine）。**锁是按引擎
// 生效的**：同一个 tab 下挂着两个引擎族的模型时，一方的硬约束不该套到另一方头上。
// 锁在 exemptEngines 里列出自己认画布、不需要被锁的引擎族，命中即返回 null（回落到
// 运营配置）。不传 engine 时按「不豁免」处理 —— 取不到引擎族就按更保守的那边走。
//
// 目前只有关键帧的 sizes（H3 与 wan 按首图推画布、短边硬校验 768；LTX-2.5 认
// width/height 故豁免，见 flf2v tab 注释）。
export const getTabFieldLock = (categoryKey, tabKey, field, engine) => {
  const lock =
    getPlaygroundTab(categoryKey, tabKey)?.fieldLocks?.[field] || null;
  if (!lock) return null;
  const exempt = lock.exemptEngines || [];
  return exempt.includes(
    String(engine || '')
      .trim()
      .toLowerCase(),
  )
    ? null
    : lock;
};

// 落在同一份 option 的全部 tab（含跨分类的「视频配音」）。迁移、能力派生、
// admin 保存都要按 option 维度遍历。返回 [{category, tab}]。
export const listTabsByStoreKey = (storeKey) => {
  const out = [];
  PLAYGROUND_CATEGORIES.forEach((cat) => {
    cat.tabs.forEach((tab) => {
      if ((tab.storeIn || cat.configKey) === storeKey) {
        out.push({ category: cat.key, tab });
      }
    });
  });
  return out;
};

// 能力标签 → tab（同一份 option 内）。用于老配置按 capabilities 反查 tab。
export const tabKeyForCapability = (storeKey, capability) =>
  listTabsByStoreKey(storeKey).find((x) => x.tab.capability === capability)?.tab
    .key || null;

// ---------------------------------------------------------------------------
// 模型大类（模型广场筛选用）
// ---------------------------------------------------------------------------
// 模型广场按「文本/图像/视频/语音/音乐」五个大类筛选，而不是按单个能力标签（文生图、
// 数字人……）—— 能力标签是 tab 粒度、数量多且只对体验区有意义，做筛选项太碎。
// 大类判定复用体验区那四份 ModelConfig：运营把某模型配进哪份配置，它就属于哪个大类
// （同一份配置也决定它出现在哪个体验区分类页），无需再引入第二套标注。

export const MODEL_CATEGORIES = PLAYGROUND_CATEGORIES.map((c) => ({
  key: c.key,
  label: c.label,
}));

// 文本模型没有 ModelConfig（靠"不是媒体模型"反推），作为兜底大类。
export const MODEL_CATEGORY_TEXT = 'playground';

// 只上架、未配进体验区的模型（如仅供 API 调用的绘图模型）没有配置可查，按端点类型兜底。
// audio-speech 必须排在 openai-video 前：TTS 模型同时声明这两种端点。
const ENDPOINT_CATEGORY_FALLBACK = [
  ['image-generation', 'image'],
  ['audio-speech', 'audio'],
  ['openai-video', 'video'],
];

const parseModelConfig = (raw) => {
  if (!raw) return null;
  if (typeof raw === 'object') return raw;
  try {
    return JSON.parse(raw);
  } catch (e) {
    return null;
  }
};

// 由 /api/status 建「模型名 -> 大类 key」索引。同名模型只认第一份命中的配置。
export const buildModelCategoryIndex = (status) => {
  const index = new Map();
  PLAYGROUND_CATEGORIES.forEach((cat) => {
    if (!cat.configKey) return;
    const parsed = parseModelConfig(status?.[cat.configKey]);
    Object.keys(parsed?.models || {}).forEach((name) => {
      if (!index.has(name)) index.set(name, cat.key);
    });
  });
  return index;
};

// model 需带 model_name 与 supported_endpoint_types（/api/pricing 的形态）。
export const resolveModelCategory = (model, index) => {
  const hit = index?.get(model?.model_name);
  if (hit) return hit;
  const types = model?.supported_endpoint_types || [];
  const fallback = ENDPOINT_CATEGORY_FALLBACK.find(([endpoint]) =>
    types.includes(endpoint),
  );
  return fallback ? fallback[1] : MODEL_CATEGORY_TEXT;
};

// ---------------------------------------------------------------------------
// tab 显示配置（PlaygroundTabConfig）
// ---------------------------------------------------------------------------
// 形态：{ [category]: { [tabKey]: true | false | { enabled, mobile, order, label } } }
// 布尔是旧形态（只有网页端显隐），等价于 { enabled: <bool> }，读时按需升维，不做
// 破坏性改写——运营在 admin 页保存时才会写成对象。
// 缺省语义一律「未配置=显示」：新增能力上线即可见，不用先去后台开一遍。

const TAB_DISPLAY_DEFAULT = {
  enabled: true,
  mobile: true,
  order: null, // null=按声明顺序
  label: '', // ''=用内置显示名
};

export const parsePlaygroundTabConfig = (raw) => {
  if (!raw) return {};
  if (typeof raw === 'object') return raw;
  try {
    const parsed = JSON.parse(raw);
    return parsed && typeof parsed === 'object' ? parsed : {};
  } catch (e) {
    return {};
  }
};

// 取某 tab 的显示配置，统一补齐成对象形态（兼容旧布尔）。
export const getTabDisplay = (tabConfig, category, tabKey) => {
  const v = tabConfig?.[category]?.[tabKey];
  if (v === undefined || v === null) return { ...TAB_DISPLAY_DEFAULT };
  if (typeof v === 'boolean') return { ...TAB_DISPLAY_DEFAULT, enabled: v };
  if (typeof v !== 'object') return { ...TAB_DISPLAY_DEFAULT };
  return {
    enabled: v.enabled !== false,
    mobile: v.mobile !== false,
    order: Number.isFinite(v.order) ? v.order : null,
    label: typeof v.label === 'string' ? v.label.trim() : '',
  };
};

// tab 是否显示（网页端）：缺省=显示；仅显式 false 才隐藏。
export const isPlaygroundTabVisible = (tabConfig, category, modeKey) =>
  getTabDisplay(tabConfig, category, modeKey).enabled;

// tab 是否在手机端显示：先得网页端开着（后台关掉的两端都没有），再看 mobile 开关。
export const isPlaygroundTabVisibleOnMobile = (
  tabConfig,
  category,
  modeKey,
) => {
  const d = getTabDisplay(tabConfig, category, modeKey);
  return d.enabled && d.mobile;
};

// 跨分类的全局项挂在保留键 __global 下（它不是分类 key，分类查询天然不受影响）。
export const PLAYGROUND_GLOBAL_KEY = '__global';

// 「AI 优化提示词」的全局配置：总开关、用哪个语言模型、用哪个分组。
//
// group 留空 = 走用户自己的分组（早先唯一的行为）。但优化模型通常是个便宜小模型、
// 被放在通用分组里，而 VIP/内部用户反而在只挂业务模型的专用分组——于是「分组越专用
// 越用不了优化功能」，正好反了。所以分组要能单独配。
//
// 带 group 的请求由 middleware/distributor.go 校验权限（GroupInUserUsableGroups），
// 不存在越权：运营配了用户无权访问的分组时请求会被 403，前端据此给一句
// 「请联系管理员」的提示（不藏按钮——见 usePromptOptimize 头部注释）。
export const getPromptOptimizeGlobal = (tabConfig) => {
  const g = tabConfig?.[PLAYGROUND_GLOBAL_KEY]?.promptOptimize;
  return {
    enabled: g?.enabled === true,
    model: typeof g?.model === 'string' ? g.model.trim() : '',
    group: typeof g?.group === 'string' ? g.group.trim() : '',
  };
};

// 某 tab 的「AI 优化提示词」配置。systemPrompt 留空=用内置默认（见
// constants/promptOptimize.constants.js 的 defaultOptimizeSystemPrompt）。
// tab 级 enabled 缺省为 true：全局开了就都能用，个别 tab 不想要再单独关。
export const getTabPromptOptimize = (tabConfig, category, tabKey) => {
  const v = tabConfig?.[category]?.[tabKey]?.promptOptimize;
  return {
    enabled: v?.enabled !== false,
    systemPrompt: typeof v?.systemPrompt === 'string' ? v.systemPrompt : '',
  };
};

// 某 tab 的「提示词写作建议」：运营写的一段「这个玩法的提示词该怎么写」，体验区在
// 提示词框上方挂个问号，鼠标移上去就是它。留空则退回内置默认（见
// promptGuide.constants.js，按玩法各写各的，没写的玩法就没有），两处都空才不展示
// ——与「AI 优化提示词」的系统提示词同一套语义。
//
// 粒度是 tab 级而非模型级：怎么写主要由场景决定（文生视频要写镜头运动、数字人写的
// 是台词情绪、声音设计写的是声线描述），与选哪个模型关系小得多。模型这一维已经有
// 「模型备注」（models[x].tabs[y].note，展示在模型下拉里）——一个说选哪个，一个说
// 怎么写，各管一头。
//
// 存在 PlaygroundTabConfig 而不是四份 ModelConfig：它跟 promptOptimize.systemPrompt
// 同类——tab 级、纯文案、与模型无关。
//
// 原样返回不 trim：admin 页拿它当受控输入框的值，trim 会把运营正在敲的换行/缩进
// 吞掉。「有没有配」由展示端自己 trim 后判断。
export const getTabPromptGuide = (tabConfig, category, tabKey) => {
  const v = tabConfig?.[category]?.[tabKey]?.promptGuide;
  return typeof v === 'string' ? v : '';
};

// 按运营配置排序 + 应用显示名覆盖，返回带 display 的 tab 列表（不做显隐过滤）。
// order 未配置的排在已配置的之后，同 order 按声明顺序稳定。
export const resolvePlaygroundTabs = (category, tabConfig) => {
  const tabs = getPlaygroundCategory(category)?.tabs || [];
  return tabs
    .map((tab, idx) => {
      const display = getTabDisplay(tabConfig, category, tab.key);
      return {
        ...tab,
        idx,
        display,
        label: display.label || tab.label,
      };
    })
    .sort((a, b) => {
      const ao = a.display.order;
      const bo = b.display.order;
      if (ao === bo) return a.idx - b.idx;
      if (ao === null) return 1;
      if (bo === null) return -1;
      return ao - bo;
    });
};

// ---------------------------------------------------------------------------
// 模型配置里的 tab 子层读取
// ---------------------------------------------------------------------------
// 取某模型在某 tab 下显式配置的字段值；未配置返回 undefined，由调用方继续按
// 模型级 → 分类 default → 内置兜底降级。tabKey 为空（非体验区调用、或直连请求
// 解析不出 tab）时直接返回 undefined，退回原有的模型级语义。
// 列表字段的空数组视为「未配置」（与既有 opt-in 语义一致：留空=不展示/不限制）。
export const tabScopedValue = (modelEntry, tabKey, field) => {
  if (!tabKey) return undefined;
  const t = modelEntry?.tabs?.[tabKey];
  if (!t || typeof t !== 'object') return undefined;
  const v = t[field];
  if (v === undefined || v === null) return undefined;
  if (Array.isArray(v) && v.length === 0) return undefined;
  return v;
};

// ---------------------------------------------------------------------------
// 模型备注（tab 级）
// ---------------------------------------------------------------------------
// 运营在体验区管理的模型卡片里写一句「这个模型在这个玩法下适合什么场景」，体验区的
// 模型下拉直接展示，省得用户对着一堆模型名瞎猜。
//
// 粒度是 tab 级（存在 models[name].tabs[tabKey].note）而不是模型级：同一个模型挂在
// 文生视频与图生视频下，适用场景本来就不是一句话——模型级只能写一条，等于逼运营写
// 一句放之四海皆准的废话。tab 级各写各的，缺省=不展示。
//
// 它是纯展示项，不进 tab.fields：fields 是「参数」，会被 recomputeModelLevel 反推到
// 模型级供直连请求兜底，备注没有这层语义。
export const normalizeModelNote = (v) =>
  typeof v === 'string' ? v.trim() : '';

// ---------------------------------------------------------------------------
// 模型级「AI 优化提示词」系统提示词（tab 级存放）
// ---------------------------------------------------------------------------
// 运营为**某个 tab 下的某个模型**单独改写的优化系统提示词，存在
// models[name].tabs[tabKey].optimizePrompt。取值链：
//   模型级（这里）→ tab 级通用（PlaygroundTabConfig 的 promptOptimize.systemPrompt）
//   → 内置默认（promptOptimize.constants.js，按 tab + 引擎族）
//
// 为什么需要这一层：系统提示词此前只有 tab 级一份，而同一个 tab 完全可以挂多个引擎族
// 的模型（文生视频同时挂 wan / MiniMax H3 / LTX-2.5，文生音乐同时挂 ACE-Step /
// MiniMax-Music3），各家要的模板形状彼此相反 —— 运营一旦改写 tab 那份，其余引擎族的
// 模型就被迫用它，不报错、只是默默出差档。有了模型级覆盖，「给这一个模型单独写一份」
// 不再需要把它拆到另一个 tab。
//
// 与 note 同层同理：纯文案、不是参数，故不进 tab.fields，也就不会被 recomputeModelLevel
// 反推到模型级。
export const normalizeModelOptimizePrompt = (v) =>
  typeof v === 'string' ? v.trim() : '';

// 由 /api/status 里的原始 option（字符串或对象）取某模型在某 tab 下的优化系统提示词。
// 未配置返回 ''，调用方据此回落 tab 级。与 buildModelNoteIndex 同一取舍：用户端只要
// 这一个字段，走通用 JSON 解析，不把四份配置各自的规范化函数都拖进来。
export const getModelOptimizePrompt = (raw, tabKey, model) => {
  if (!tabKey || !model) return '';
  const parsed = parseModelConfig(raw);
  return normalizeModelOptimizePrompt(
    parsed?.models?.[model]?.tabs?.[tabKey]?.optimizePrompt,
  );
};

// 由 /api/status 里的原始 option（字符串或对象）建「模型名 -> 该 tab 备注」索引。
// 体验区侧只要备注这一个字段，走通用 JSON 解析即可，不必把四份配置各自的规范化函数
// 都拖进用户端。
export const buildModelNoteIndex = (raw, tabKey) => {
  const index = new Map();
  if (!tabKey) return index;
  const parsed = parseModelConfig(raw);
  Object.entries(parsed?.models || {}).forEach(([name, cfg]) => {
    const note = normalizeModelNote(cfg?.tabs?.[tabKey]?.note);
    if (note) index.set(name, note);
  });
  return index;
};

// 由 tabs 的键派生能力标签（模型广场展示用）。运营把模型加进哪个 tab，它就有哪个
// 能力——不再单独勾一遍能力标签，避免「勾了能力却没配参数」两处对不上。
// extra：配置里已存在、但当前没有对应 tab 的能力标签（如图像的「图像编辑」等尚未
// 开体验区玩法的能力），原样保留，避免保存一次就把模型广场的标签抹掉。
export const deriveCapabilities = (storeKey, tabsObj, existingCaps) => {
  const owned = listTabsByStoreKey(storeKey);
  const byKey = new Map(owned.map((x) => [x.tab.key, x.tab.capability]));
  const ownedCaps = new Set(owned.map((x) => x.tab.capability));
  const out = [];
  Object.keys(tabsObj || {}).forEach((k) => {
    const cap = byKey.get(k);
    if (cap && !out.includes(cap)) out.push(cap);
  });
  (existingCaps || []).forEach((c) => {
    // 没有对应 tab 的能力标签透传保留（只读，admin 页在「按模型」视图里展示）。
    if (!ownedCaps.has(c) && !out.includes(c)) out.push(c);
  });
  return out;
};

// 与 deriveCapabilities 对称：挑出「无对应 tab」的能力标签，供 admin 页只读展示。
export const capabilitiesWithoutTab = (storeKey, caps) => {
  const owned = new Set(
    listTabsByStoreKey(storeKey).map((x) => x.tab.capability),
  );
  return (caps || []).filter((c) => !owned.has(c));
};

// 「孤儿字段」＝这个模型身上存在、却没有任何一个 tab 认领的参数。
//
// 两种来路：模型没挂任何玩法（如超分模型 seedvr2，超分只作 1080P 流水线的内部一段，
// 体验区没有入口），或字段本身无玩法认领（如音乐的 videoMaxMB，「视频生音」已下线）。
// 这类值仍会被服务端护栏读到——task_type 解析不出 tab 时正是退回模型级——所以必须
// 留一个可编辑的地方，否则就成了「照样生效但改不了」的暗配置。admin 页在「按模型
// 交叉检查」里渲染它们。recomputeModelLevel 也不会动这些字段（见其实现）。
export const orphanFields = (storeKey, model) => {
  const claimed = new Set();
  listTabsByStoreKey(storeKey)
    .filter((x) => model?.tabs?.[x.tab.key] !== undefined)
    .forEach((x) => (x.tab.fields || []).forEach((f) => claimed.add(f)));
  return Object.keys(PLAYGROUND_FIELD_META).filter(
    (f) => !claimed.has(f) && model?.[f] !== undefined && model?.[f] !== null,
  );
};

// 由 tabs 反推模型级平铺字段（保存时调用）。
//
// 模型级字段不再由运营直接编辑，但不能删：它是「解析不出 tab」时的取值来源——直连
// 请求里 task_type 缺失或一个 task_type 对应多个 tab（语音四玩法共用 tts）时，护栏
// 只能退回模型级。取「最宽松」的口径：列表取并集、上限取最大值，且只要有一个 tab 没
// 设上限就整个不落键（= 不限）。这样模型级永远不会比任何一个 tab 更严，不会出现
// 「体验区里选得到、直连却被模型级挡掉」。
//
// 只重算「该模型至少参与了一个声明该字段的 tab」的字段，其余平铺字段原样保留：
// 没进任何 tab 的模型（如只挂了「图像编辑」这种暂无玩法的能力）配置不会被抹掉。
// **tab-only 字段**：反推到模型级只会产生一份没人读、还会被 parse 丢掉的噪声键。
//
// 图像的宽高比与分辨率档就是这类：后端根本不读图像的画幅配置（见
// common/media_model_config.go 文件头「sizes 只驱动前端体验区的可选值」），体验区取值
// 又永远带 tabKey。早先没跳过它们，结果是「保存时写进模型级 → parse 白名单重建时丢掉
// → 读取侧的模型级回落永远取不到」，写/读/parse 三处口径全不一样。
const MODEL_LEVEL_SKIP = {
  ImageModelSizeConfig: ['aspectRatios', 'sizeTiers'],
};

export const recomputeModelLevel = (storeKey, model) => {
  const out = { ...(model || {}) };
  delete out.tabs;
  delete out.capabilities;
  const tabsObj = model?.tabs || {};
  const owned = listTabsByStoreKey(storeKey).filter(
    (x) => tabsObj[x.tab.key] !== undefined,
  );
  const fields = new Set();
  owned.forEach((x) => (x.tab.fields || []).forEach((f) => fields.add(f)));
  const skip = MODEL_LEVEL_SKIP[storeKey] || [];
  fields.forEach((field) => {
    if (skip.includes(field)) return;
    const entries = owned
      .filter((x) => (x.tab.fields || []).includes(field))
      .map((x) => tabsObj[x.tab.key]?.[field]);
    switch (PLAYGROUND_FIELD_META[field]?.type) {
      case 'list': {
        const union = [];
        entries.forEach((v) =>
          (Array.isArray(v) ? v : []).forEach((item) => {
            if (!union.includes(item)) union.push(item);
          }),
        );
        if (union.length) out[field] = union;
        else delete out[field];
        break;
      }
      case 'int': {
        // 0 与留空同义（不限），任一 tab 不限则模型级不限。
        const unlimited = entries.some((v) => v == null || v === 0);
        const max = Math.max(0, ...entries.map((v) => (v == null ? 0 : v)));
        if (unlimited || max <= 0) delete out[field];
        else out[field] = max;
        break;
      }
      case 'translation': {
        // 任一 tab 开了就算开(字段只剩 enabled,翻译模型走「通用设置」的优化模型)。
        if (entries.some((v) => v && v.enabled === true))
          out[field] = { enabled: true };
        else delete out[field];
        break;
      }
      default:
        break;
    }
  });
  return out;
};
