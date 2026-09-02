// 视频模型相关常量

import {
  VIDEO_ENGINE_MINIMAX_H3,
  normalizeModelNote,
  normalizeModelOptimizePrompt,
  tabScopedValue,
} from './playgroundAdmin.constants';

export const VIDEO_API_ENDPOINTS = {
  VIDEO_GENERATIONS: '/pg/videos', // POST 提交任务
  VIDEO_FETCH: '/pg/videos', // GET /pg/videos/:id 轮询
  VIDEO_CONTENT: '/v1/videos', // GET /v1/videos/:id/content 取内容（会话鉴权）
  USER_MODELS: '/api/user/models',
  USER_GROUPS: '/api/user/self/groups',
  PRICING: '/api/pricing',
};

// 视频模型能力枚举（中文即值，也是体验区标签页名）。业内常用完整集。
// 新增能力时同步维护后端 constant/model_capability.go 的 VideoCapabilities。
export const VIDEO_CAPABILITIES = [
  '文生视频',
  '图生视频',
  '关键帧',
  '参考生视频',
  '数字人',
  '视频超分',
  '视频编辑',
  // 视频配音(task_type=v2a):原画面逐帧不动 + AI 音轨,LTX-2.3 首发,可挂多模型。
  // 2026-07 从音乐词表迁入(AudioX 视频生音下线);体验区入口在「语音模型」页。
  '视频配音',
];

// 提示词预设:点击对应按钮清空输入框并填入该提示词(体验区快速试玩,仅文生视频展示)。
export const VIDEO_PROMPT_PRESETS = [
  '中景，一位穿米色针织衫的年轻女性坐在临窗的咖啡馆座位上，桌上的咖啡冒着热气。她轻轻搅动咖啡，抬头微笑，窗外阳光透过百叶窗在她脸上投下条纹光影。镜头缓慢左移，晨光，柔光，暖色调，低对比度，浅景深，生活方式广告质感。',
  '特写，一块厚切和牛牛排在铸铁锅中煎烤，金黄色的黄油在肉块边缘融化冒泡。油脂滋滋作响，厨师用勺子将热黄油缓缓淋在牛排表面。固定镜头微距，暖色调，侧光突出油脂光泽，浅景深，高端美食广告质感。',
  '低角度仰拍，一位穿着发光机能外套的女性站在未来都市的雨夜街头，身后是层层叠叠的全息广告牌和飞行器航线。她转身走入霓虹小巷，外套光纹随步伐流动。镜头跟随移动，荧光加霓虹混合光源，紫红色调，高对比度，赛博朋克风格。',
  '三维卡通动画，皮克斯动画电影质感。中景，一台方头方脑的黄色小机器人，履带底盘，两只大大的双筒望远镜式眼睛，在洒满阳光的花园里。它伸出机械手轻轻碰了碰一朵向日葵，被弹回的花瓣吓得后退，眼睛惊讶地放大，随后歪头发出好奇的姿态。镜头低角度缓慢环绕，清晨柔光，暖色调，金属漆面反射细腻，全局光照，三维渲染，皮克斯风格。',
];

// ── 一键示例(带预置文件/参数,按 mode)──────────────────────────────────
// 结构同音频/音乐:{ label, prompt, params?, files? }。i2v/flf2v/s2v/vace/sr 预置官方示例
// 素材(见 public/playground-samples/);text2video 纯文本。ChatArea 兼容纯字符串。
// mode 键与 VideoModel 的 tab itemKey 一致:text2video/image2video/flf2v/s2v/sr/vace。
export const VIDEO_EXAMPLES = {
  text2video: VIDEO_PROMPT_PRESETS,
  // 图生视频(Bernini r2v):参考图(1~3 张)生成视频 —— 参考图定义主体/服装/道具/
  // 场景等元素,由提示词组合成片(非首帧约束;首帧约束在「关键帧」模式)。
  image2video: [
    {
      label: '图生视频(参考图)',
      prompt:
        '以第一张参考图中的大理石雕像为主体,给他戴上第二张参考图里的粉色猫耳耳机,坐在第三张参考图的海边落日长椅上,正对镜头、中景固定机位,随音乐轻轻点头晃动身体。保持雕像的白色石质、卷曲雕刻发型与肌肉体格,以及海滩长椅、棕榈树与橙粉紫落日天空的场景不变,动作自然流畅、俏皮不夸张。',
      files: {
        refImages: [
          '/playground-samples/images/bernini-r2v-statue.jpg',
          '/playground-samples/images/bernini-r2v-headphones.jpg',
          '/playground-samples/images/bernini-r2v-beach.jpg',
        ],
      },
    },
  ],
  // 关键帧:两个示例分别服务两类模型,由 videoExamplesForMode 按所选模型过滤——
  // i2v 模型只出「仅首帧」,flf2v 模型只出「首帧+尾帧」。
  flf2v: [
    {
      label: '仅首帧(i2v)',
      prompt:
        '画面中的人物微微转头并露出微笑,发丝随微风轻轻飘动,背景虚化的光斑缓慢晃动,镜头缓缓向前推进。',
      files: { firstFrame: '/playground-samples/images/wan-i2v-first.jpg' },
    },
    {
      label: '首帧+尾帧(flf2v)',
      prompt:
        '镜头从首帧场景平滑过渡到尾帧,运动连贯自然,光影随时间流畅变化,电影级插帧质感。',
      files: {
        firstFrame: '/playground-samples/images/wan-flf2v-first.png',
        lastFrame: '/playground-samples/images/wan-flf2v-last.png',
      },
    },
  ],
  s2v: [
    {
      label: '数字人',
      prompt:
        'A woman is passionately singing into a professional microphone in a recording studio.',
      files: {
        firstFrame: '/playground-samples/images/infinitetalk-person.png',
        audioData: '/playground-samples/audio/infinitetalk-driving.wav',
      },
    },
  ],
  sr: [
    {
      label: '超分示例视频',
      prompt: '',
      files: { sourceVideo: '/playground-samples/video/seedvr2-lowres.mp4' },
    },
  ],
  // 视频编辑(Bernini):上传 1 个源视频,玩法由是否带参考图自动分流——
  //   1 视频 → v2v(纯提示词编辑)、1 视频+参考图 → rv2v。
  //   仅参考图的 r2v 已迁到「图生视频」模式,本模式必须有视频。
  // 双视频玩法(mv2v 多源编辑 / ads2v 广告植入)后端仍全量支持,只是体验区不给第二个
  // 上传口,故这里也没有对应示例——要跑它们走 API 直连。
  // 示例素材取自 Bernini 官方 testcases（v2v/rv2v），提示词按其真实用例翻译。
  vace: [
    {
      label: '视频编辑(纯提示词 · v2v)',
      prompt:
        '把画面中站在深色反光地面上的白色人形机器人替换成一只造型流畅的机械狗,位置与比例不变:未来感四足金属狗,白色外壳、黑色关节细节、微微发光的眼睛,金属腿部有关节。保持原有运动节奏做出相称的机械动作,地面上的阴影与倒影自然一致,深色影棚背景、灯光与镜头构图均保持不变。',
      files: {
        srcVideo: '/playground-samples/video/bernini-v2v-robot.mp4',
      },
    },
    {
      label: '参考图视频编辑(rv2v)',
      prompt:
        '把人物的外层衬衫替换成参考图中的衬衫,保留里面的打底衫不变;身姿、镜头构图、光影、裤子、发型、肤色与整体动作全部保持原样。人物仍站在同样的浅灰影棚背景前,里面仍是黄白横条纹打底衫,外层换成带细竖条纹的白色立领衬衫、黑色纽扣、左胸口袋,穿在身上有自然的布料垂坠与随动,其余场景元素不变。',
      files: {
        srcVideo: '/playground-samples/video/bernini-rv2v-person.mp4',
        refImages: ['/playground-samples/images/bernini-rv2v-shirt.jpg'],
      },
    },
  ],
};

// ── MiniMax H3 专属示例 ─────────────────────────────────────────────────
// 整张表换掉,不在上面那份里混一个 engine 字段。同 defaultOptimizeSystemPrompt 的
// 处置:H3 与通用系的形状是**相反**的,打补丁只会两边都不像。另外关键帧那条过滤已经
// 占用了 keyframeMode 这个轴,再塞一个 engine 判据进同一个列表,两个条件会绞在一起。
//
// 多出来的 h3 字段承载另外两段(音景 / 背景音乐),点示例时一并填进 H3PromptFields。
// 不加这个字段的话示例就只填画面描述那一格 —— 而 H3 是**视听一体**模型
// (T2VA = video + audio,输出必带 overall_soundscape / non_diegetic_music),
// 只示范画面等于当着用户的面演了一半,剩下两段丢给优化模型凭空编。
//
// 提示词写中文:H3 下输入框的角色是「你的想法」(H3_INPUT_FIELDS 的 placeholder 也是
// 中文),点了「AI 优化」由 Context-IR 那层译成英文分段结构;不点则由 buildLocalH3Prompt
// 包上字段名发出去。
//
// 素材一律复用现有的 wan / Bernini 样例图,不新增二进制资产 —— 都在 H3 的校验范围内
// (短边 ≥256、长边 ≤5760、宽高比 [0.4,2.5];实测 832×1104、1024×1024、730×1024)。
export const VIDEO_EXAMPLES_H3 = {
  text2video: [
    {
      label: '有台词 · 咖啡馆',
      prompt:
        '中景,一位穿米色针织衫的年轻女性坐在临窗的咖啡馆座位上,端起咖啡抿了一口,抬头看向镜头轻声说「今天天气真好」。晨光透过百叶窗在她脸上投下条纹光影,镜头缓慢左移。',
      h3: {
        overall_soundscape:
          '咖啡馆的环境底噪:邻桌低声交谈、咖啡机蒸汽、瓷杯轻碰桌面;她说话时人声清晰靠前,咬字自然。',
        non_diegetic_music: '轻柔的钢琴独奏,节奏舒缓,音量始终压在人声之下。',
      },
    },
    {
      label: '纯环境音 · 雨夜街头',
      prompt:
        '低角度仰拍,一位穿着发光机能外套的女性走过雨夜的未来都市街头,身后是层层叠叠的全息广告牌与飞行器航线。她转身走入霓虹小巷,外套光纹随步伐流动,镜头跟随移动。',
      h3: {
        overall_soundscape:
          '大雨打在地面与雨棚上,脚步踩碎积水的水花声,远处车辆驶过的胎噪与低频轰鸣,广告牌持续的电流嗡鸣。',
        // 「不要配乐」与「留空」是两回事:留空是让模型自己看着办,明确说不要才会落成
        // guide 里的 N/A(无配乐)。这条示例就是来示范后者的。
        non_diegetic_music: '不要配乐,只留现场环境声。',
      },
    },
    {
      label: '音效特写 · 煎牛排',
      prompt:
        '特写,一块厚切牛排在铸铁锅中煎烤,黄油在肉块边缘融化冒泡。厨师用勺子将热黄油缓缓淋在牛排表面。固定镜头微距,侧光突出油脂光泽,浅景深。',
      h3: {
        overall_soundscape:
          '油脂在高温铸铁锅里持续滋滋作响,黄油浇淋时细密的气泡声,金属勺碰到锅沿清脆的一声。',
        non_diegetic_music: '轻快的爵士鼓刷与贝斯,音量克制,衬托而不抢戏。',
      },
    },
  ],
  // 三条正好覆盖 H3 关键帧的三态(靠 extra_params.frame_indices 区分):
  // 仅首帧 I2VA / 首尾帧 FL2VA / 仅尾帧 L2VA。第三条尤其要有 —— 「只给尾帧」是 H3
  // 独有的玩法,wan 的两类模型都表达不出来,没有示例用户基本发现不了它的存在。
  flf2v: [
    {
      label: '仅首帧(I2VA)',
      prompt:
        '画面中的人物微微转头并露出微笑,发丝随微风轻轻飘动,背景虚化的光斑缓慢晃动,镜头缓缓向前推进。',
      h3: {
        overall_soundscape:
          '户外轻风拂过,远处隐约的鸟鸣与树叶摩擦声,环境安静、混响很少。',
        non_diegetic_music: '温柔的弦乐衬底,从很轻开始缓慢渐强。',
      },
      files: { firstFrame: '/playground-samples/images/wan-i2v-first.jpg' },
    },
    {
      label: '首尾帧(FL2VA)',
      prompt:
        '从首帧的场景平滑过渡到尾帧,运动连贯自然,光影随时间流畅变化,保持单镜头不切。',
      h3: {
        overall_soundscape: '环境声随画面一起渐变,不要出现突兀的切换点。',
        non_diegetic_music: '一条连贯的合成器长音,随画面变化缓慢起伏。',
      },
      files: {
        firstFrame: '/playground-samples/images/wan-flf2v-first.png',
        lastFrame: '/playground-samples/images/wan-flf2v-last.png',
      },
    },
    {
      label: '仅尾帧(L2VA)',
      prompt:
        '由一个合理的前置状态自然演变到尾帧画面,最后一帧稳稳落在给定的构图上,单镜头,运动收束平缓。',
      h3: {
        overall_soundscape: '环境声由稀疏渐渐充实,结尾归于安静。',
        non_diegetic_music: '极简的钢琴单音,末尾收在一个长音上。',
      },
      files: { lastFrame: '/playground-samples/images/wan-flf2v-last.png' },
    },
  ],
  // 参考生视频在此之前一条示例都没有(VIDEO_EXAMPLES 根本没有 r2va 键),按钮整排不渲染。
  // 素材沿用 Bernini r2v 那三张:参考图定义主体/道具/场景,由提示词组合成片,这套用法
  // 与 H3 Ref2VA 一致。
  r2va: [
    {
      label: '参考图生视频',
      prompt:
        '以第一张参考图中的大理石雕像为主体,给他戴上第二张参考图里的粉色猫耳耳机,坐在第三张参考图的海边落日长椅上,正对镜头、中景固定机位,随音乐轻轻点头晃动身体。保持雕像的白色石质与卷曲雕刻发型,以及海滩长椅、棕榈树与橙粉紫落日天空的场景不变。',
      h3: {
        overall_soundscape:
          '海浪一层层拍上沙滩,海风掠过棕榈叶,远处偶尔有海鸟叫声。',
        non_diegetic_music:
          '慵懒的 lo-fi 嘻哈节拍,鼓点轻缓,与他点头的节奏合拍。',
      },
      files: {
        refImages: [
          '/playground-samples/images/bernini-r2v-statue.jpg',
          '/playground-samples/images/bernini-r2v-headphones.jpg',
          '/playground-samples/images/bernini-r2v-beach.jpg',
        ],
      },
    },
  ],
};

// 「关键帧」tab 同时承载两类 wan 模型:--task i2v 的「首帧生视频」和 --task flf2v 的
// 「首尾帧」。它们是同一份权重、不同启动参数的两个引擎实例,task 在实例启动期就定死了:
// i2v 实例收到尾帧会静默丢弃(I2VInputInfo 没有 last_frame_path 字段),flf2v 实例缺尾帧
// 会读空路径直接崩。所以尾帧「能不能传/要不要传」只能由所选模型决定,不能按用户输入派生。
// 判据优先读运营在体验区管理「关键帧」一格里给该模型声明的 taskType;没声明才退回
// 名字里含不含 flf2v。与后端 taskTypeOfRequest 的优先级链同源(声明 → 输入形态 → 名字)。
//
// 为什么要声明字段:名字判据的**对象**前后端不同 —— 这里拿到的是对外模型名(/api/pricing
// 的 key),后端兜底推断拿到的是渠道重定向后的上游名。两者分叉就会错配:对外名叫
// wan2.2-keyframe、上游是 wan2.2-flf2v-a14b 时,前端判成 i2v、隐藏尾帧槽并显式下发
// task_type=i2v,该模型在体验区直接不可用;反向同样错配。声明字段把这个判断从「猜名字」
// 变成「运营说了算」,前后端读的是同一份声明,不可能再分叉。
//
// 未声明时仍受原约束:GPUStack 上游名与对外名**都**要带 flf2v,做了模型重定向的别名也
// 要保留这个标识(体验区管理「关键帧」一格的说明里同步了这条)。
// ── 三态（2026-08，MiniMax H3）────────────────────────────────────────────
// 上面那段约束是 wan 的：task 在实例启动期定死，所以尾帧「能不能传」只能由模型决定。
// H3 不一样：一个 FL2VA checkpoint 同时吃首帧 / 尾帧 / 首尾帧，由 extra_params
// .frame_indices（[0] / [-1] / [0,-1]）区分，二选一对它是过度约束——「只给尾帧」
// （L2VA）这个玩法在二选一下根本表达不出来。
//
// 故返回值由 boolean 改为四态：
//   'flf2v'   尾帧必填（wan flf2v 实例）
//   'i2v'     尾帧不可传（wan i2v 实例）
//   'auto'    首帧/尾帧/首尾帧全支持，两槽都可选、至少填一个（MiniMax H3）
//   'auto_fl' 首帧 或 首尾帧，首帧必填、尾帧可选（Seedance 2.0，不支持仅尾帧）
//
// **刻意改了函数名**：老名字 isFlf2vModel 会让调用方写 `if (isFlf2vModel(...))`，
// 而三态下 'i2v' 和 'auto' 都是 truthy，那种写法会静默地把两者都当成首尾帧模式。
// 改名逼编译期暴露所有调用点。
export const keyframeModeOf = (model, config) => {
  const declared =
    config?.models?.[String(model || '').trim()]?.tabs?.flf2v?.taskType;
  if (declared === 'auto') return 'auto';
  // Seedance 2.0 支持首帧与首尾帧,但**不支持只给尾帧** —— 官方文档的互斥场景只列了
  // 「首帧、首尾帧、多模态参考」三种,last_frame 只作为首尾帧的一半出现。
  // 它两种都支持,所以选 i2v 或 flf2v 都会砍掉一半能力;选 auto 又会开出它做不了的
  // 仅尾帧。故需要这第四档。
  if (declared === 'auto_fl') return 'auto_fl';
  if (declared) return declared === 'flf2v' ? 'flf2v' : 'i2v';
  return String(model || '')
    .toLowerCase()
    .includes('flf2v')
    ? 'flf2v'
    : 'i2v';
};

// 提交时按用户实际填了哪个槽派生门面 task_type。仅 'auto' 模式走这里；
// wan 的两态仍按所选模型下发，不看输入（见上方注释）。
//
// 与后端 constant/playground_tab.go 的 l2va → 关键帧 tab 映射、以及门面
// _H3_TASK_MAP 的 l2va → fl2va + frame_indices=[-1] 是同一条链，三处须同步。
export const deriveKeyframeTaskType = (hasFirst, hasLast) => {
  if (hasFirst && hasLast) return 'flf2v';
  if (hasLast) return 'l2va';
  return 'i2v';
};

// 一键示例按 mode 取;「关键帧」下再按所选模型过滤——i2v 模型只能用仅首帧的示例,
// flf2v 模型只能用带尾帧的示例,否则点了示例反而凑不出该模型要求的输入组合。
// 判断结果由调用方传入(isFlf2vModel 现在要配合配置声明读,见上),这里不再自己推。
//
// engine 是所选模型声明的引擎族:H3 换整张表(见 VIDEO_EXAMPLES_H3)。
// **落空必须回退到通用表**而不是返回空数组 —— 超分/视频编辑/配音不走 H3,H3 表里不会
// 有它们的键,不兜底就是「选了 H3 模型,超分那一排示例按钮整个消失」。
export const videoExamplesForMode = (mode, keyframeMode, engine) => {
  const h3List =
    engine === VIDEO_ENGINE_MINIMAX_H3 ? VIDEO_EXAMPLES_H3[mode] : null;
  const list = h3List?.length ? h3List : VIDEO_EXAMPLES[mode] || [];
  if (mode !== 'flf2v') return list;
  // auto(单 checkpoint 全能模型):首帧示例与首尾帧示例都能跑,不过滤。
  if (keyframeMode === 'auto') return list;
  const wantLast = keyframeMode === 'flf2v';
  return list.filter((ex) => Boolean(ex?.files?.lastFrame) === wantLast);
};

// 视频宽高比(文生视频):可在运营后台按模型配置允许集,未配置默认全集。
// 21:9 是 MiniMax H3 与 Seedance 2.0 都支持的具名比例(H3:
// MINIMAX_H3_SUPPORTED_ASPECT_RATIOS;Seedance: ratio 枚举含 21:9),原表缺它。
// 这只是全集,各模型实际可选由运营配的 aspectRatios 收窄。
export const VIDEO_ASPECT_RATIOS = [
  '21:9',
  '16:9',
  '9:16',
  '1:1',
  '4:3',
  '3:4',
];
// 默认选中的宽高比(minimax 无宽高比可参考;取 16:9 = wan 引擎默认 1280×720)。
export const VIDEO_DEFAULT_ASPECT_RATIO = '16:9';

// 「跟随上传素材」档。只出现在画幅由输入决定的玩法(关键帧)里,选中即完全不干预:
// 既不改图也不下发比例字段,画幅就是上传那张图的比例。它是这些玩法一直以来的行为,
// 所以是默认选中项。
//
// 这些玩法选具名比例时,比例**不经请求字段表达,而是把图改成那个比例**再提交
// (composeImageToRatio)。理由见 helpers/imageCompose.js 顶部的实测记录:引擎按
// images[0] 的比例推画布,想靠参数盖掉画幅只会让图被拉伸变形。
export const VIDEO_ASPECT_RATIO_AUTO = 'auto';
// 宽高比 → 引擎 target_shape:[height,width](720p 级,均为 16 的倍数)。
// wan t2v runner 的 get_latent_shape_with_target_hw 优先采用 target_shape,不认识 aspect_ratio。
export const VIDEO_ASPECT_RATIO_TO_SHAPE = {
  '16:9': [720, 1280],
  '9:16': [1280, 720],
  '1:1': [960, 960],
  '4:3': [768, 1024],
  '3:4': [1024, 768],
};

// 宽高比 → target_shape:[height,width]。预设 5 种走上表(手调过的固定值);其它自定义 "W:H"
// (后台 allowCreate 可能录入,如 2:1)按 ~720p 面积等比算,并对齐到 16 的倍数,避免被静默丢弃。
export const aspectRatioToShape = (ratio) => {
  if (VIDEO_ASPECT_RATIO_TO_SHAPE[ratio])
    return VIDEO_ASPECT_RATIO_TO_SHAPE[ratio];
  const m = /^\s*(\d+)\s*:\s*(\d+)\s*$/.exec(String(ratio || ''));
  if (!m) return null;
  const w = parseInt(m[1], 10);
  const h = parseInt(m[2], 10);
  if (w <= 0 || h <= 0) return null;
  const scale = Math.sqrt((1280 * 720) / (w * h));
  const round16 = (x) => Math.max(16, Math.round((x * scale) / 16) * 16);
  return [round16(h), round16(w)]; // [height, width]
};

// 当前视频体验区页面代表的能力（= 标签页名）
export const VIDEO_PAGE_CAPABILITY = '文生视频';
// 图生视频 / 首尾帧 / 数字人 / 视频超分 / 视频编辑能力标签,与文生视频共用体验区,
// 通过 mode 区分。门面 task_type 对应:s2v→数字人(音频驱动人像说话,行业通称)、
// sr→视频超分、vace→视频编辑。
export const VIDEO_I2V_CAPABILITY = '图生视频';
// 2026-07「首尾帧」改名「关键帧」:同一 tab 承载 wan2.2 的 i2v 与 flf2v 两个模型,
// task_type 按所选模型下发(见 isFlf2vModel),不再按输入张数派生。旧标签走 LEGACY_ALIASES 兼容。
export const VIDEO_FLF2V_CAPABILITY = '关键帧';
export const VIDEO_S2V_CAPABILITY = '数字人';
export const VIDEO_SR_CAPABILITY = '视频超分';
export const VIDEO_VACE_CAPABILITY = '视频编辑';
// 参考生视频(门面 task_type=r2va):参考图/视频/音频 → 带语音的视频。
// 挂 MiniMax H3 Ref2VA(自建)与 Seedance 2.0(doubao 渠道)两类模型 —— 输入字段名
// 已在后端统一(src_ref_images / reference_videos / reference_audios),前端不分支。
export const VIDEO_R2VA_CAPABILITY = '参考生视频';

// 各 tab 的参考图像素约束(前置校验用)。**按 tab 取,不要写成一个全局常量** ——
// 多模型共享的 tab 必须用各模型的最小交集,写死一个值要么误拒、要么放行了会被上游拒的。
//
//   MiniMax H3 : 短边 ≥256、长边 ≤5760、宽高比 [0.4, 2.5]
//                (pipeline_minimax_h3:463-471)
//   Seedance2.0: 边长 300–6000、宽高比 [0.4, 2.5](火山官方文档「素材限制」)
//
// 交集 = 短边 ≥300、长边 ≤5760、宽高比 [0.4, 2.5]。
// 三个视频 tab 现在都挂了 Seedance,故都用交集值;文生视频无图片输入,不涉及。
// 依据与来源见 docs/minimax-h3-playground-design.md §5.3.1。
export const VIDEO_IMAGE_CONSTRAINTS = {
  flf2v: {
    minShortEdge: 300,
    maxLongEdge: 5760,
    minAspect: 0.4,
    maxAspect: 2.5,
  },
  r2va: {
    minShortEdge: 300,
    maxLongEdge: 5760,
    minAspect: 0.4,
    maxAspect: 2.5,
  },
  // 图生视频(Bernini r2v)与数字人(InfiniteTalk)未声明像素约束,不校验。
};

// 取该 tab 的图片像素约束;未声明返回空对象(= 不校验)。
export const imageConstraintsForMode = (mode) =>
  VIDEO_IMAGE_CONSTRAINTS[mode] || {};
// 视频配音(dub → 门面 task_type=v2a):上传视频 + 声音描述,产物=配好音的视频。
// 2026-07 由「视频配乐」改名为「视频配音」;旧配置靠下方 legacy alias 兼容。
export const VIDEO_DUB_CAPABILITY = '视频配音';

// 能力标签重命名的向后兼容:重命名前已在「视频模型配置」里用旧标签配过的模型,仍能匹配
// 到新 Tab(否则那些模型会从体验区消失,直到手动改配置)。key=新标签,value=旧标签。
export const VIDEO_CAPABILITY_LEGACY_ALIASES = {
  [VIDEO_S2V_CAPABILITY]: '音频驱动',
  [VIDEO_SR_CAPABILITY]: '视频转视频',
  // ⚠️ 原来这里有一条 [VIDEO_VACE_CAPABILITY]: '参考生视频'(视频编辑的旧名)。
  // 2026-08 新增了正式的「参考生视频」tab,这条别名必须摘掉 —— 否则声明了
  // 「参考生视频」的模型会**同时命中新 tab 与视频编辑**。
  // 上线前检查:若还有模型的 capabilities 里留着旧标签,在管理页重存一次即可
  // (deriveCapabilities 会按 tabs 重算),或手工改成「视频编辑」。
  [VIDEO_FLF2V_CAPABILITY]: '首尾帧',
  [VIDEO_DUB_CAPABILITY]: '视频配乐',
};

// 视频模型「策略类别」：不同类上游对尺寸/时长参数的要求不同。
// - sora 类（真·OpenAI Sora）：像素尺寸（后端 relay_utils 校验器要求 720x1280 等）+ seconds 字段；
// - minimax 类（MiniMax / MiniMax-compat）：分辨率档位（720P）+ duration 字段。
// durationField 决定提交时把时长写进哪个字段（只发该字段，避免多发被严格上游拒绝）。
export const VIDEO_MODEL_STRATEGIES = {
  sora: {
    sizes: ['720x1280', '1280x720'],
    durations: ['4', '8', '12'],
    durationField: 'seconds',
  },
  minimax: {
    sizes: ['720P', '1080P'],
    durations: ['5'],
    durationField: 'duration',
  },
};

// 按模型名归类；未识别的一律按 minimax-compat（当前默认部署）。
// 新增某类模型时，只需在这里补匹配规则。
export const resolveVideoStrategy = (model) => {
  const m = String(model || '').toLowerCase();
  if (m.startsWith('sora')) return VIDEO_MODEL_STRATEGIES.sora;
  return VIDEO_MODEL_STRATEGIES.minimax;
};

// 兼容旧引用：通用兜底 = minimax 类（管理端「默认尺寸/时长」留空时的展示用）。
export const FALLBACK_VIDEO_SIZES = VIDEO_MODEL_STRATEGIES.minimax.sizes;
export const FALLBACK_VIDEO_DURATIONS =
  VIDEO_MODEL_STRATEGIES.minimax.durations;

export const VIDEO_HISTORY_STORAGE_KEY = 'video_playground_conversations';
export const VIDEO_HISTORY_LIMIT = 10; // 对话段数上限
export const VIDEO_CONV_TURN_LIMIT = 10; // 单段对话生成次数上限

// 同一浏览器里最多同时在跑几个视频任务。
//
// 从 1 抬到 3：视频是异步任务（提交拿 task_id → 轮询），后端本来就支持并发，此前
// 卡在 1 纯粹是前端只留了一个轮询槽——上一条没出结果就发不出下一条，也没法新建
// 会话去发别的。抬到 3 让用户能并行几件事，又不至于一个人把 GPU 队列排满
// （后端**没有**任何按用户的在途任务数限制，这里是唯一的闸）。
//
// 别和 VIDEO_HISTORY_LIMIT 搞混：那个是本地保留多少段历史会话（兼 IndexedDB 媒体
// 的回收触发器），跟同时有几个任务在跑无关。
export const VIDEO_MAX_CONCURRENT_TASKS = 3;

// 轮询参数
// 插帧(RIFE 帧率翻倍):开启时随 metadata 透传 target_fps 给引擎(gpustack 门面
// 对该字段免验证直通)。LightX2V 生成默认 16fps;Bernini RIFE v1 仅支持 16→32。
// 统一按 32 下发。
export const VIDEO_INTERPOLATION_TARGET_FPS = 32;

// 「插帧」总闸门。与 DUB_PIPELINE_ENABLED 同类：当前部署没有可用的插帧能力，置
// false 后开关在桌面端与移动端都不渲染，历史会话里存了 interpolation:true 的续问
// 也不会再下发 target_fps —— 生成段与超分段两处都读它。
//
// 能力缺口在引擎侧，两条链路各有各的：生成段的 target_fps 要求该引擎装了 RIFE
// 权重（H3 走的 vLLM-Omni 这条没有）；超分段的插帧要求 SR 节点 config 里有
// video_frame_interpolation 块，而只有 seedvr2_3b_seg121.json 一份挂了它，别的
// 配置收到 target_fps 会静默丢弃（worker 只打一条 warning）。发一个不生效的参数
// 比不发更糟：用户勾了开关、付了钱，产物却和没勾一样。
//
// 恢复时把这里改回 true 即可，无需动别处。但先掂量一个实测代价：超分段插帧会让
// seg_parallel 强制退回串行（跨段有全局帧栅格依赖），2026-08-15 实测同一条 362 帧
// 素材从 207s 涨到 673s、慢 3.25 倍，且另外三张卡全程闲置。
export const INTERPOLATION_ENABLED = false;

// 超分段下发的倍率。**别把 SR 理解成「放大 N 倍」——它是按分辨率档位出片的**，
// 这个字段只是个够不着的上限。引擎算的是
//   min(源面积开方 × sr_ratio, config target 面积开方)   （seedvr_runner.py:151）
// 右项来自超分模型部署 config 的 target_height/target_width（现网 seedvr2 各档均为
// 1920×1080，即 1080p 这一档），且会按源的朝向配对长短边（_oriented_target：横出
// 1920×1080、竖出 1080×1920、方出 1080×1080）。
//
// 这里给一个足够大的定值，就是为了让 min 恒取右项、输出分辨率完全由部署 config 决定：
// 发小了会静默掉档，起步档位的差异也被自动抹平，运营不必为每个起步档算一个倍率。
// 关键帧起步 768P（1344×768，面积开方约 1016）时 1016×4=4064 远超 1440，取右项。
//
// 曾经按标称档位算出的 2.25 就栽在这：标称 854x480 与 wan 实际生成的 832x480 差
// 1.3%，min() 取不到 target，输出落在 1872x1072，标着 1080P 却不是。
export const VIDEO_SR_RATIO_UNCAPPED = 4.0;

// 超分段固定下发 resize_mode。引擎按 DivisibleCrop(16) 对齐，1080 不是 16 的倍数，
// 默认的 adaptive 会出 1920x1104（_restore_target_size 在 adaptive 下直接 return），
// 属性与界面承诺的 1080P 对不上。fixed_shape 让引擎中心裁到 config 的精确 target，
// 代价是上下各裁 12 像素（约 1.1% 画面）—— 2026-08-15 实测确认，已取得确认。
export const VIDEO_SR_RESIZE_MODE = 'fixed_shape';

// 超分段的目标短边：把「超分档位」规则里的目标档换算成一个像素数，交引擎按**源的真实
// 画幅**等比放大到该短边（引擎字段 target_short_edge）。
//
// 超分档位本质就是「短边档」——1080P / 2K / 4K 说的都是短边，长边由画幅决定。所以这里
// 对所有能解析出短边的写法一视同仁：档位词（1080P / 768P）、短边档位词（2K / 4K）、
// 精确像素串（2560x1440，取其短边）。解析不出的（空、乱填）返回 0，退回老行为。
//
// 为什么下发短边而不是完整的 target_shape：只有引擎知道源有多大。这里手里只有用户选的
// 比例标签，而标签和实际画幅并不相等——H3 的 768P/16:9 实际出 1344×768（=1.75，它有面积
// 钳位），wan 的 720P 是 1280×720（=1.778）——照标签算完整尺寸会带约 1.6% 的横向拉伸，
// 且档位越高越明显。下发短边则画幅零形变、短边精确命中目标档。
//
// 这也从根上消掉了当初那个「标着 1080P 却不是」的 bug：它的成因是输出落在 1872×1072、
// 短边没命中 1080（按标称档位算倍率、min() 取不到 target）。按短边对齐后短边必然精确
// 等于档位值，不再依赖倍率和封顶的相互作用。
//
// 为什么要下发而不是继续只靠引擎按部署 config 封顶：那条路一个部署只能出一档（config 里
// 的 target_height/target_width），要同时提供 1080P/2K/4K 就得为每档单开一套部署、各吃一
// 份卡。SwiftVR 一个实例就能覆盖所有档。对不读该字段的引擎（SeedVR2 只认 sr_ratio +
// config 档位）这是个惰性字段，多发无害、行为不变，所以规则仍指向旧模型时不会出错。
export const upscaleTargetShortEdge = (tier) => videoSizeShortEdge(tier);

// 该模型是否跑在自建 gpustackplus 引擎上（「视频模型配置」里按模型勾选）。
// 自动超分/自动配音/插帧(target_fps)都是自建引擎特有的玩法：超分要把 1080P 拆成
// 「先低档位生成再走 sr 模型」两段，插帧是 gpustack 门面直通给引擎 RIFE 的字段。
// 第三方渠道(Sora/MiniMax 等)原生支持 1080P 直出、也不认识 target_fps，参数必须原样
// 透传，不能替用户改写。故判据只认显式标记：未标记 = 透传，新接入的第三方模型天然安全。
// 只按模型判，不设 default 层兜底——兜底会让新模型默认被编排，正是要消除的行为。
export const isPipelineModel = (config, model) =>
  !!config?.models?.[model]?.pipeline;

// 模型级超分规则：[{ to, model, from }]。运营在「视频模型配置」的模型级字段里填，
// to=目标档位、model=超分模型名、from=起步档位（必填）。留空整体 = 不提供超分档位
// （纯 opt-in，与 sizes 同惯例：不给未配置的模型凭空长出一个档位）。
export const getUpscaleRulesForModel = (config, model) =>
  normalizeUpscaleList(
    config?.models?.[model]?.upscale,
    // 一段式的规则行不需要 model（放大由引擎做），必须跟着放行，否则运营配好的
    // 档位在运行时被丢掉、体验区下拉里就是不出现。
    isNativeDeliveryModel(config, model),
  );

// 起步档位：只认运营显式指定的那一档，且必须仍在该模型原生档里（防止改了 sizes 之后
// 留下悬空值）。没填、或填的档位已不在原生档里时返回 ''，调用方据此整档不渲染。
//
// 曾经支持 from 留空=「自动取小于目标的最大原生档」，已删：管理端看不出最终会用哪一档，
// 配完常常整档不出现（如目标档本身就在原生档里，或压根没有更小的档），排查成本远高于
// 让运营在下拉里点一下。现在管理端列的候选就是运行时会用的值，两边同一份判据。
export const resolveUpscaleFrom = (rule, nativeSizes) => {
  const explicit = normalizeVideoSize(rule?.from);
  if (!explicit) return '';
  return (nativeSizes || []).includes(explicit) ? explicit : '';
};

// 尺寸下拉的完整选项 = 原生档（保持运营配置的顺序）+ 超分档（按输出分辨率升序追加在后）。
// 超分档带上 srModel / fromSize，让 UI 的标识文案与提交时的编排读同一份推导结果 ——
// 两处各推一次迟早推出不同答案，keyframeTaskType 当初就是这么栽的。
//
// usableModels = 当前分组的可用模型列表；超分模型对该分组不可用时整档不产出（不置灰）：
// 分组权限在体验区解释不清，给个点不动的选项只会招来「为什么我不能选」。
// 传 null/undefined 表示尚未取到列表，此时不做可用性过滤（避免加载期档位闪烁）。
export const buildVideoSizeChoices = (
  config,
  model,
  nativeSizes,
  usableModels,
) => {
  const native = (nativeSizes || []).map((s) => ({
    value: s,
    label: s,
    isUpscale: false,
  }));
  const taken = new Set(native.map((o) => o.value));
  const upscale = [];
  getUpscaleRulesForModel(config, model).forEach((rule) => {
    // 原生已有同档 → 原生优先，超分规则让位（配置侧的冗余，不该让用户看见两个 1080P）
    if (taken.has(rule.to)) return;
    if (Array.isArray(usableModels) && !usableModels.includes(rule.model))
      return;
    const from = resolveUpscaleFrom(rule, nativeSizes);
    if (!from) return;
    taken.add(rule.to);
    upscale.push({
      value: rule.to,
      label: rule.to,
      isUpscale: true,
      srModel: rule.model,
      fromSize: from,
    });
  });
  upscale.sort(
    (a, b) => videoSizeShortEdge(a.value) - videoSizeShortEdge(b.value),
  );
  return [...native, ...upscale];
};

// 从给定「可用模型列表」中取首个声明了指定能力的模型名（超分/配音流水线模型识别）。
// 按分组可用列表挑而非全局取首个：多模型同能力、按分组分别启用时，避免钉死在
// 对当前分组不可用的那个。list 空/未传时返回 ''（无可用能力模型）。
export const findCapabilityModelIn = (videoConfig, list, capability) => {
  const models = videoConfig?.models || {};
  const legacy = VIDEO_CAPABILITY_LEGACY_ALIASES[capability];
  return (
    (list || []).find((m) => {
      const caps = models[m]?.capabilities;
      if (!Array.isArray(caps)) return false;
      return caps.includes(capability) || (legacy && caps.includes(legacy));
    }) || ''
  );
};

// 支持「配音」流水线的体验区模式（生成后接 v2a 配音段）：文生/图生/视频编辑。
export const DUB_PIPELINE_MODES = ['text2video', 'image2video', 'vace'];

// 「生成后自动配音」总闸门。2026-08 暂时全端关闭：v2a 配出的音频与画面内容常常无关，
// 在质量达标前不该让用户按次付费去开它。置 false 后 dubAvailable 恒假 —— 开关在
// 桌面端与移动端都不渲染，且历史会话里存了 dubbing:true 的续问也不会再接配音段
// （见 useVideoGeneration 的 dubAvailable / maybeDub）。
//
// 恢复时把这里改回 true 即可，无需动别处。注意这与移动端的 allowDub:false 是两回事：
// 那个是「手机上要多排一次 v2a、等待久失败面大」的长期取舍，恢复本闸门时不要一并撤掉。
// 语音页的独立「视频配乐」入口（用户自己上传视频去配音）不受此闸门影响，仍然可用。
export const DUB_PIPELINE_ENABLED = false;

export const VIDEO_POLL_INTERVAL_MS = 4000;
// 生成阶段的轮询预算（不含排队）：约 6 分钟。
export const VIDEO_POLL_MAX_TIMES = 90;
// 排队阶段单独计预算：约 40 分钟。
//
// 为什么要分开：这两段的时长由完全不同的东西决定。生成时长由模型和载荷决定，
// 是个可预期的常数量级；排队时长由集群忙闲和准入阈值决定，慢模型（h3-ref2va 单条
// ~660s、8 实例）排到第二轮就是二十来分钟。合并计算的话，排队一长就会在还没轮到
// 时耗光预算停轮，逼用户去点「继续获取」——而那时任务其实好好地待在队列里。
export const VIDEO_QUEUE_POLL_MAX_TIMES = 600;

// 任务状态（与后端 dto/openai_video.go 对齐 + 前端补充）
export const VIDEO_STATUS = {
  QUEUED: 'queued',
  IN_PROGRESS: 'in_progress',
  COMPLETED: 'completed',
  FAILED: 'failed',
  CANCELED: 'canceled',
};

// 内容地址：/v1/videos/:id/content
export const buildVideoContentUrl = (id) =>
  `${VIDEO_API_ENDPOINTS.VIDEO_CONTENT}/${encodeURIComponent(id)}/content`;

// 尺寸规范化：乘号/星号统一为 x，去空格。
// 分辨率档位（如 720p）统一为大写 P（上游如 MiniMax 区分大小写）；
// 像素尺寸（如 1280x720）保持小写 x。
export const normalizeVideoSize = (s) => {
  const v = String(s || '')
    .trim()
    .toLowerCase()
    .replace(/\s+/g, '')
    .replace(/[×✕╳*]/g, 'x');
  return /^\d+p$/.test(v) ? v.toUpperCase() : v;
};

// 档位 → 短边像素。sizes 是混形态的：档位词(480P/768P)、像素串(1280x720)、以及
// H3 专有的自定义档位词(n)。超分档要按「实际输出分辨率」排序、并自动挑出「小于目标
// 的最大一档」作起步，两件事都需要一个统一口径的可比数值。
//
// 与后端 minimax_h3.go:82 的 h3ShortEdgeFromSizeToken 同口径("n"→480 那条后端也硬
// 编码了同一个值)。两边漂移的后果是静默的 —— 选错起步档、不报错，所以这里是前端的
// 单一来源，不要在别处再写一份。解析不出返回 0，调用方据此让它不参与排序与推导。
export const videoSizeShortEdge = (s) => {
  const v = normalizeVideoSize(s);
  if (!v) return 0;
  const wh = v.match(/^(\d+)x(\d+)$/);
  if (wh) return Math.min(Number(wh[1]), Number(wh[2]));
  const p = v.match(/^(\d+)P$/);
  if (p) return Number(p[1]);
  if (v === 'n') return 480;
  // 2K / 4K 按消费端通行口径取短边(QHD 2560x1440、UHD 3840x2160)。加它们是因为超分
  // 档位就是「短边档」——出片短边精确命中、长边随源画幅,用 2K/4K 表达比逼运营记
  // 2560x1440 自然。⚠️ 后端 h3ShortEdgeFromSizeToken 必须同步加同一份映射,两边漂移
  // 的后果是静默的(选错起步档、不报错)。
  if (v === '2k') return 1440;
  if (v === '4k') return 2160;
  return 0;
};

// 通用列表规范化（时长/能力）：去空格、去空、去重（解析与设置页保存共用，避免两条路径分叉）
export const normalizeList = (list) =>
  Array.isArray(list)
    ? Array.from(new Set(list.map((x) => String(x).trim()).filter(Boolean)))
    : [];

// 尺寸列表规范化（解析与设置页保存共用）
// 引擎族标识。定义在 playgroundAdmin.constants.js（避免与本文件成环，见那边的注释），
// 这里转出一份给体验区侧用。与后端 common.VideoEngineFamilyForModel 读同一个键。
export { VIDEO_ENGINE_MINIMAX_H3 } from './playgroundAdmin.constants';

// 归一：后端比较前会 lower + trim，这里做同样处理，避免运营输入 " MiniMax-H3 "
// 时前后端判据分叉。
export const normalizeEngine = (v) =>
  String(v || '')
    .trim()
    .toLowerCase();

export const normalizeSizeList = (list) =>
  Array.isArray(list)
    ? Array.from(new Set(list.map(normalizeVideoSize).filter(Boolean)))
    : [];

// 超分规则列表规范化（解析与管理端保存共用，避免两条路径分叉）。
// 丢弃缺 to / model 的行；同一个 to 只留第一条 —— 多条同 to 会在尺寸下拉里出现两个
// 同名档位，只能靠 label 区分，等于把心智负担还给用户，不如在配置侧就收敛。
// allowEmptyModel：该模型走「高分辨率档用纯放大」时，规则行的 model 是**可选**的。
//
// 两段式下 model 必填天经地义 —— 没有超分模型就没有第二段可跑，留着一条跑不动的规则
// 只会在体验区长出一个点了不生效的档位。但一段式整条路上根本没有第二个模型：放大由
// 引擎在出片前做，这一格填什么都不会被读到。
//
// 曾经在这里踩过:管理页把这一格置灰(因为它确实不生效),结果「添加超分档位」变成
// 不可能完成的操作 —— 新行填不了 model，保存后被本函数整行丢弃，而且不报错，运营
// 只会看到「新加的档位怎么没了」。**可选**才是对的，置灰不是。
export const normalizeUpscaleList = (list, allowEmptyModel = false) => {
  if (!Array.isArray(list)) return [];
  const seen = new Set();
  const out = [];
  list.forEach((r) => {
    const to = normalizeVideoSize(r?.to);
    const model = String(r?.model || '').trim();
    if (!to || seen.has(to)) return;
    if (!model && !allowEmptyModel) return;
    seen.add(to);
    out.push({ to, model, from: normalizeVideoSize(r?.from) || '' });
  });
  return out;
};

// 一段式交付可选的目标档位。**短边档**：1080P / 2K / 4K 说的都是短边，长边由源画幅
// 决定（与 upscaleTargetShortEdge 同一套语义）。
//
// 两段式的目标档位取自「超分模型登记的 sizes」——那是它部署 config 里的目标尺寸，
// 是部署事实。一段式没有那个模型，缩放由引擎的编码漏斗做，能缩到任意短边，所以这里
// 给一份通行档位表让运营点，而不是逼他手打像素串。
// ⚠️ 取值必须是 normalizeVideoSize 归一**之后**的形态:它只对 `\d+p` 形态转大写,
// 其余一律小写("4K" → "4k")。写成 '4K' 的话,存下来是 '4k'、而下拉选项是 '4K',
// Select 的 value 匹配不到任何 option —— 界面上那一格看起来是空的。
export const VIDEO_DELIVERY_TIERS = ['1080P', '2k', '4k'];

// 现场拍摄档位。存在的理由见 hooks/videoPlayground/useVideoRecorder.js 顶部注释:
// 系统相机的分辨率/帧率网页管不着(华为 4K30 约 5-7 MB/s,录十几秒就顶穿 maxInputMB),
// 只有自己开 getUserMedia 才谈得上「预设」。720p/24fps/2Mbps ≈ 0.25 MB/s,10 秒约 2.5MB。
export const VIDEO_RECORD_WIDTH = 1280;
export const VIDEO_RECORD_HEIGHT = 720;
export const VIDEO_RECORD_FPS = 24;
export const VIDEO_RECORD_VIDEO_BPS = 2_000_000;
export const VIDEO_RECORD_AUDIO_BPS = 128_000;

// 到 MAX 自动停止,防止误录长视频撑大请求体(按上面的码率,180 秒约 45MB)。
export const VIDEO_RECORD_MAX_SEC = 180;

// 解析 status 中的 VideoModelConfig（字符串或对象）
// 形如 { default: { sizes:[], durations:[] }, models: { name: { sizes:[], durations:[] } } }
// maxInputMB:输入文件大小上限(MB)。适用于吃用户上传的模式(i2v/flf2v 帧图、s2v 人物图/
// 驱动音频、sr 源视频、视频编辑 源视频/参考图);0/未配=不限。生成侧 sizes/durations/
// aspectRatios 对这些输入驱动能力无意义(见 followsInput),maxInputMB 才是它们的护栏。
// maxAudioSec:驱动音频时长上限(秒);0/未配=不限。与 maxInputMB 是两个正交的轴——
// 体积挡不住时长(1 MB 的 mp3 可能有 60 秒)。只对数字人(s2v)有意义:它的输出时长 =
// min(驱动音频时长, video_duration, 参考视频时长),音频越长生成越久,过长会让引擎
// OOM 或长时间占卡。后端还会把本值作为 video_duration 下发给引擎,所以它同时是
// "拒绝超长音频"和"告诉引擎最多生成多久"两件事的唯一来源。

// 音频时长闸的容差(秒)。真实音频时长几乎从不是整数——编码器帧对齐、mp3 的 encoder
// delay/padding 会让"一分钟"变成 60.024 秒;卡死整数会把用户眼里合法的一分钟音频拒掉,
// 报错还显示成"60.0 秒超过 60 秒",读起来像我们的 bug。
//
// 必须与 Go 侧 relay/channel/gpustackplus/nfsinput 的 AudioDurationToleranceSec 保持
// 同值。前端这道闸只是「选完文件当场反馈」,权威判定在后端;两边阈值不一致时,严的那边
// 说了算——前端更严就会出现"后端明明放行了、界面却不让选"的怪象(这正是 2026-08 只改了
// 后端容差留下的缺口)。
export const AUDIO_DURATION_TOLERANCE_SEC = 1;

const toInputMB = (v) => {
  const n = parseInt(v, 10);
  return Number.isFinite(n) && n >= 0 ? n : null;
};

const toAudioSec = (v) => {
  const n = Number(v);
  return Number.isFinite(n) && n >= 0 ? n : null;
};

// 采样步数:必须 > 0，0/负数/非数一律当没配（后端回落引擎族的基座档），
// 与 maxInputMB 那种「0 = 不限」的语义相反，别套用 toInputMB。
const toSteps = (v) => {
  const n = parseInt(v, 10);
  return Number.isFinite(n) && n > 0 ? n : null;
};

// tab 子层规范化：只保留声明过的字段，值的清洗规则与模型级一致。
// 未配的字段一律不落键（undefined），这样 tabScopedValue 才能正确地"落空即降级"。
const normalizeVideoTabEntry = (cfg) => {
  const out = {};
  const sizes = normalizeSizeList(cfg?.sizes);
  if (sizes.length) out.sizes = sizes;
  const durations = normalizeList(cfg?.durations);
  if (durations.length) out.durations = durations;
  const ratios = normalizeList(cfg?.aspectRatios);
  if (ratios.length) out.aspectRatios = ratios;
  const mb = toInputMB(cfg?.maxInputMB);
  if (mb != null) out.maxInputMB = mb;
  const sec = toAudioSec(cfg?.maxAudioSec);
  if (sec != null) out.maxAudioSec = sec;
  // 参考素材三模态各配各的。**这里同样是白名单式重建,漏一个键 = 管理页保存一次就把
  // 运营刚配的值删掉**(与模型级 engine 是同一类坑)。
  // 注意 0 与「未配」必须区分,所以用 != null 判而不是 truthy:
  //   未配 → 不落键 → 读取时降级到内置默认;
  //   显式 0 → 落键 0 → 该模态不开放,上传框不渲染。
  const refImages = toInputMB(cfg?.maxRefImages);
  if (refImages != null) out.maxRefImages = refImages;
  const refVideos = toInputMB(cfg?.maxRefVideos);
  if (refVideos != null) out.maxRefVideos = refVideos;
  const refVideoMB = toInputMB(cfg?.refVideoMaxMB);
  if (refVideoMB != null) out.refVideoMaxMB = refVideoMB;
  const refVideoSec = toAudioSec(cfg?.refVideoMaxSec);
  if (refVideoSec != null) out.refVideoMaxSec = refVideoSec;
  // taskType:该 tab 覆盖多个门面 task_type 时(「关键帧」= i2v/flf2v),由运营在体验区
  // 管理里指明这个模型属于哪一个。不是参数,是玩法声明——所以不进 tab.fields,
  // 也就不会被 recomputeModelLevel 反推到模型级。
  const taskType = String(cfg?.taskType || '')
    .trim()
    .toLowerCase();
  if (taskType) out.taskType = taskType;
  // 模型备注：纯展示项(体验区模型下拉里给用户看)，与 taskType 同理不进 tab.fields。
  const note = normalizeModelNote(cfg?.note);
  if (note) out.note = note;
  // 「AI 优化提示词」的模型级系统提示词覆盖(留空=用 tab 那份通用的)。同上不进
  // tab.fields。⚠️ 这里同样是白名单式重建：漏了它 = 运营在管理页保存一次就把刚写的
  // 模板删掉，而症状是「优化效果某天起悄悄退回通用版」。
  const optimizePrompt = normalizeModelOptimizePrompt(cfg?.optimizePrompt);
  if (optimizePrompt) out.optimizePrompt = optimizePrompt;
  return out;
};

const normalizeTabsMap = (raw, normalizeEntry) => {
  if (!raw || typeof raw !== 'object') return {};
  const out = {};
  Object.entries(raw).forEach(([tabKey, cfg]) => {
    // 空对象也保留：它是「该模型已挂进这个 tab、但参数全用兜底」的显式声明，
    // 能力标签正是由这些键派生的，丢了会让模型从 tab 里消失。
    out[tabKey] = normalizeEntry(cfg);
  });
  return out;
};

export const parseVideoModelConfig = (raw) => {
  // 未配置时默认留空，交由 getSizes/DurationsForVideoModel 按模型类别兜底
  const empty = {
    default: {
      sizes: [],
      durations: [],
      aspectRatios: [],
      maxInputMB: null,
      maxAudioSec: null,
    },
    models: {},
  };
  if (!raw) return empty;
  try {
    const parsed = typeof raw === 'string' ? JSON.parse(raw) : raw;
    const def = parsed.default || {};
    const models = {};
    if (parsed.models && typeof parsed.models === 'object') {
      Object.entries(parsed.models).forEach(([name, cfg]) => {
        models[name] = {
          sizes: normalizeSizeList(cfg?.sizes),
          durations: normalizeList(cfg?.durations),
          aspectRatios: normalizeList(cfg?.aspectRatios),
          capabilities: normalizeList(cfg?.capabilities),
          maxInputMB: toInputMB(cfg?.maxInputMB),
          maxAudioSec: toAudioSec(cfg?.maxAudioSec),
          pipeline: !!cfg?.pipeline,
          // 同上,白名单式重建:漏了它每次保存都会把「一段式交付」的勾抹掉,
          // 而症状是"某天起 1080P 又开始接超分模型了、画面又开始沸腾"。
          nativeDelivery: !!cfg?.nativeDelivery,
          // 同上,白名单式重建:漏了它每次保存都会把整条超分链配置抹掉,而症状是
          // 「体验区的 1080P 档位莫名消失」,查起来要绕一大圈。
          // 一段式允许 model 为空，判据取同一份配置里的 nativeDelivery。
          upscale: normalizeUpscaleList(cfg?.upscale, !!cfg?.nativeDelivery),
          // 引擎族声明。**这是白名单式重建，漏一个字段就等于每次管理页保存都把它删掉**——
          // engine 决定后端走不走 MiniMax H3 那套请求整形(帧数约定/时长字段/画布推导),
          // 丢了不会报错,只会让 H3 悄悄退回 wan 的形态。
          // 序列化侧不用管:recomputeModelLevel 是 {...model} 整体展开,只要 parse 保住就能往返。
          engine: normalizeEngine(cfg?.engine),
          // 同上,白名单式重建:漏了它每次保存都会把步数抹掉,蒸馏模型悄悄退回基座档。
          defaultSteps: toSteps(cfg?.defaultSteps),
          tabs: normalizeTabsMap(cfg?.tabs, normalizeVideoTabEntry),
        };
      });
    }
    return {
      default: {
        sizes: normalizeSizeList(def.sizes),
        durations: normalizeList(def.durations),
        aspectRatios: normalizeList(def.aspectRatios),
        maxInputMB: toInputMB(def.maxInputMB),
        maxAudioSec: toAudioSec(def.maxAudioSec),
      },
      models,
    };
  } catch (e) {
    return empty;
  }
};

// ── 参数读取(全部 tab 感知)────────────────────────────────────────────
// 优先级一律 tab 级 → 模型级 → 管理端全局默认 → 内置兜底。tabKey 传空时退化成
// 改造前的「只按模型名」语义(直连请求解析不出 tab、或非体验区调用时)。
// 同一模型挂多个玩法时,靠 tab 级把参数分开:文生视频给尺寸/宽高比,图生视频给上传
// 上限,互不串扰。

// 输入文件大小上限(MB):0(不限)兜底。
export const getMaxInputMBForModel = (config, model, tabKey) => {
  const m = config?.models?.[model];
  const scoped = tabScopedValue(m, tabKey, 'maxInputMB');
  if (scoped != null) return scoped;
  if (m && m.maxInputMB != null) return m.maxInputMB;
  if (config?.default?.maxInputMB != null) return config.default.maxInputMB;
  return 0;
};

// 驱动音频时长上限(秒):0(不限)兜底。
export const getMaxAudioSecForModel = (config, model, tabKey) => {
  const m = config?.models?.[model];
  const scoped = tabScopedValue(m, tabKey, 'maxAudioSec');
  if (scoped != null) return scoped;
  if (m && m.maxAudioSec != null) return m.maxAudioSec;
  if (config?.default?.maxAudioSec != null) return config.default.maxAudioSec;
  return 0;
};

// 尺寸/分辨率:纯 opt-in——留空即"不支持选择",避免给未配置的模型误显尺寸选择器。
export const getSizesForVideoModel = (config, model, tabKey) => {
  const m = config?.models?.[model];
  const scoped = tabScopedValue(m, tabKey, 'sizes');
  if (scoped) return scoped;
  if (m && Array.isArray(m.sizes) && m.sizes.length > 0) return m.sizes;
  if (config?.default?.sizes?.length) return config.default.sizes;
  return [];
};

// 宽高比:纯 opt-in。不做全集兜底,避免给 minimax 等不支持宽高比的模型误显选择器。
export const getAspectRatiosForVideoModel = (config, model, tabKey) => {
  const m = config?.models?.[model];
  const scoped = tabScopedValue(m, tabKey, 'aspectRatios');
  if (scoped) return scoped;
  if (m && Array.isArray(m.aspectRatios) && m.aspectRatios.length > 0)
    return m.aspectRatios;
  if (config?.default?.aspectRatios?.length) return config.default.aspectRatios;
  return [];
};

// 引擎族:模型级声明,不随 tab 变(与 pipeline 同层)。未配即空串 = LightX2V 系。
export const getEngineForVideoModel = (config, model) =>
  config?.models?.[model]?.engine || '';

// 一段式交付:该模型按**原生档生成**,高分辨率档由引擎在出片前纯放大(lanczos+unsharp,
// 挂在编码漏斗上),不再接超分模型跑第二段。
//
// 为什么要改掉两段式:超分模型在干净的 AIGC 素材上是**净负收益**,已实测 ——
// 把一段 2beat 恒等 1.0000 的完美合成片喂进管线,H3 生成后 ~1.11(轻微),
// 过 SwiftVR 之后掉到 0.5631(2beat = 帧间差奇偶比,偏离 1 即"两帧一顿")。
// 根因在 SwiftVR 模型本体(TemporalGrow 把帧轴 unsqueeze 成 batch 轴,3 抽头
// Conv3d 退化成同一特征帧的两个投影),官方权重原样跑指标重合,配置层面绕不过去。
// 生产几何 1344×768→1920×1080 实测:纯放大 2beat 1.1059 / corr_all +0.5294,
// SwiftVR 0.5954 / +0.3891 —— 它锐度高一倍,但高频时序相关性掉了、暗部幅度放大
// 1.88×,"更锐"完全由沸腾贡献。成本上纯放大 8 核 CPU 6.32s(2.39× 实时、0 GPU),
// SwiftVR 要 4×A100 跑 43~47s。两段式还额外付一次编解码代际损失。
//
// ⚠️ **纯 opt-in,且必须确认该模型所在镜像支持交付缩放后才能勾**:那是 vllm-omni 侧
// _encode_video_bytes 上的能力,镜像没带时下发交付短边会被静默丢弃(引擎的 Pydantic
// 模型没有 extra="forbid"),结果是用户选了 1080P、拿到原生档、还不报错 ——
// 而网关这侧**看不出区别**,只能靠引擎在响应里同时回传「生成分辨率」与「交付分辨率」
// 才能发现。未勾选 = 一律走原有的两段式,新接入模型天然安全。
//
// 用户上传真实素材的裸 task_type=sr 路径不受影响:那是真实退化素材,退化域里超分
// 大概率是正收益(实测降清+crf38 重压后纯放大只剩 29.68 dB,已掉进 SwiftVR 的常数带)。
export const isNativeDeliveryModel = (config, model) =>
  !!config?.models?.[model]?.nativeDelivery;

// 交付短边字段名。**与 short_edge / target_short_edge 都不是一回事**:
//   short_edge         —— H3 自算生成画布用的(引擎只在 width/height 都缺省时才读)
//   target_short_edge  —— SwiftVR 那条 task_type=sr 的超分段用的
//   delivery_short_edge—— 本字段:生成请求里声明"出片前缩放到这个短边"
// 三者混用不会报错,只会静默走错路径,所以刻意取一个不重名的键。
//
// 键名已与 vllm-omni 侧确认(2026-08-30),不是我们自拟的占位值,别"顺手"改成
// 看起来更统一的 target_short_edge —— 那是 SwiftVR 超分段的键,撞名的后果是
// 两条路径静默串线。
//
// ⚠️ 名字定了不等于每个部署都支持:引擎的 Pydantic 模型没有 extra="forbid",
// 旧镜像收到这个键照样静默丢弃、请求仍然 200,只是视频是原生档的。所以
// isNativeDeliveryModel 仍然默认关闭,由运营确认该模型跑在带交付缩放能力的镜像上
// 之后再逐个勾 —— 这道闸拦的是镜像版本,与键名无关。
export const VIDEO_DELIVERY_SHORT_EDGE_KEY = 'delivery_short_edge';

// 开放「采样步数」调节的玩法(= 体验区 tab 的 mode)。
//
// 只给这三个:它们的画面完全由采样过程生成,步数直接换画质/耗时。其余玩法要么由源素材
// 决定形态(超分 sr、配音 dub、视频编辑 vace),要么跟随驱动输入(数字人 s2v),给一个
// 调不出所以然的旋钮只会误导。
export const VIDEO_STEPS_MODES = ['text2video', 'flf2v', 'r2va'];

// 采样步数:模型级声明(运营在「视频模型配置」里填的 defaultSteps),与 engine 同层、
// 没有 tab 层 —— 跑多少步与用户选哪个玩法无关。未配返回 null,表示体验区的步数框留空、
// 不下发,由后端回落引擎族基座档(H3 为 20)。
//
// 只是**默认值**:用户在高级参数里改了就按用户的发。后端 applyMiniMaxH3Request 对
// num_inference_steps 是「已有则不覆盖」,所以下发即生效。
export const getDefaultStepsForVideoModel = (config, model) => {
  const v = config?.models?.[model]?.defaultSteps;
  return typeof v === 'number' && v > 0 ? v : null;
};

// ── 参考素材的三模态上限(纯 tab 级,不做模型级/全局降级)────────────────
// 为什么不降级:这几项描述的是「这个玩法开放什么」,同一个模型在图生视频里给 3 张图、
// 在参考生视频里给 9 张图 + 3 个视频是完全正常的,往模型级兜底只会把两边串在一起。
//
// **0 与未配是两回事**,调用方必须按 fallback 区分:
//   未配(undefined)→ 用调用方给的内置默认;
//   显式 0        → 该模态不开放,上传框不渲染。

// 参考图张数。fallback 由调用方按玩法给(参考生视频 9 / 图生视频 3 / 其余 5)。
export const getMaxRefImagesForModel = (config, model, tabKey, fallback) => {
  const v = tabScopedValue(config?.models?.[model], tabKey, 'maxRefImages');
  return v != null ? v : fallback;
};

// 参考视频个数。默认 0 = 不开放(纯 opt-in:运营没配就当这个玩法不收参考视频,
// 与 sizes/aspectRatios 留空即不展示选择器是同一套风格)。
export const getMaxRefVideosForModel = (config, model, tabKey) => {
  const v = tabScopedValue(config?.models?.[model], tabKey, 'maxRefVideos');
  return v != null ? v : 0;
};

// 单个参考视频体积上限(MB);0=不限。刻意与 maxInputMB 分开——参考图与参考视频的
// 合理体积差一个量级,共用一个旋钮必然误伤一边。
export const getRefVideoMaxMBForModel = (config, model, tabKey) => {
  const v = tabScopedValue(config?.models?.[model], tabKey, 'refVideoMaxMB');
  return v != null ? v : 0;
};

// 单段参考视频时长上限(秒);0=不限。与体积正交:低码率的长视频体积可以很小。
export const getRefVideoMaxSecForModel = (config, model, tabKey) => {
  const v = tabScopedValue(config?.models?.[model], tabKey, 'refVideoMaxSec');
  return v != null ? v : 0;
};

// 兼容多种状态取值：OpenAIVideo(queued/in_progress/completed/failed)
// 与内部任务状态(QUEUED/IN_PROGRESS/SUCCESS/FAILURE 等)、各供应商状态。
export const normalizeVideoStatus = (raw) => {
  const s = String(raw || '')
    .toLowerCase()
    .trim();
  if (['completed', 'success', 'succeeded', 'finished'].includes(s))
    return VIDEO_STATUS.COMPLETED;
  if (['failed', 'failure', 'error', 'fail'].includes(s))
    return VIDEO_STATUS.FAILED;
  if (['canceled', 'cancelled', 'cancel'].includes(s))
    return VIDEO_STATUS.CANCELED;
  if (['in_progress', 'processing', 'running', 'generating'].includes(s))
    return VIDEO_STATUS.IN_PROGRESS;
  if (
    [
      'queued',
      'submitted',
      'not_start',
      'preparing',
      'queueing',
      'pending',
      '',
    ].includes(s)
  )
    return VIDEO_STATUS.QUEUED;
  // 未知的非终态：按生成中处理，避免卡在排队
  return VIDEO_STATUS.IN_PROGRESS;
};

// progress 可能是数字或 "50%" 字符串
export const parseProgress = (raw) => {
  if (typeof raw === 'number') return raw;
  if (typeof raw === 'string') {
    const n = parseInt(raw.replace('%', ''), 10);
    return Number.isFinite(n) ? n : undefined;
  }
  return undefined;
};

// 时长优先级：tab → 模型 → 管理端全局默认 → 按模型类别兜底（sora seconds / minimax duration）
export const getDurationsForVideoModel = (config, model, tabKey) => {
  const m = config?.models?.[model];
  const scoped = tabScopedValue(m, tabKey, 'durations');
  if (scoped) return scoped;
  if (m && Array.isArray(m.durations) && m.durations.length > 0)
    return m.durations;
  if (config?.default?.durations?.length) return config.default.durations;
  return resolveVideoStrategy(model).durations;
};
