package common

// DefaultAggregateModelConfig 聚合(编排)模型的出厂配置。
//
// 存在的理由:聚合模型是给**直连 API 的集成方**用的,而"2K 视频"这个诉求在每个部署里
// 都一样 —— 让运营从空白 JSON 开始手写流水线,只会写出几种略有差异的错法。
// 出厂给一份能直接跑的,改比写容易。
//
// 首次初始化时写入 OptionMap;管理员在系统设置里保存过之后以库里的为准,
// 这份默认值不会再覆盖回去(与其余 OptionMap 默认值同一套语义)。
//
// ── 为什么出厂就带提示词增强段 ────────────────────────────────────────
//
// 这里原来是**不带**的,理由是「增强段暂时给不出可用的出厂值」:配置结构写着
// 「SystemPrompt 空 = 继承体验区模板(模型级 → tab 级 → 内置默认)」,而那条链的
// 最后一级从来没实现,拿到空模板只能 degrade。出厂若配一个带增强的条目而不写模板,
// 得到的是最坏状态:配置说 enabled、实际每次都用原始提示词,且不报错。
//
// 现在那一级补上了(service/aggregate_enhance_template.go),按生成段模型挑内置默认。
// 模板取自三份逐字对齐过的材料:官方 H3 skill、官方客户端真正在用的 vendor 卡、
// 以及 XINGSHEN2/minimax-H3-context-IR。所以这里可以只写 model,模板留空继承。
//
// **不写 system_prompt 是刻意的**:模板知识只留一份(在 service 那边),抄进配置必然
// 漂移,而漂移的症状是"不报错、默默出差档"。运营要定制时再在配置里覆盖。
//
// 增强对 H3 尤其值:H3 吃的是分镜结构的长提示词,而客户端 agent 给的往往是一句话。
// 图片那边暂时不配 —— 没有增强的图片聚合等于绕一圈还是裸模型,而图片的改写知识
// 还没对齐过。图生图那条的 send_input_images
//
//	必须为真:增强模型看不到底图只能从文字猜,会写出与底图打架的描述。
//
// ── 为什么帧族和参考族要分两条 ──────────────────────────────────────
//
// H3 的帧族(t2v/i2v/l2va/flf2v)与参考族(r2va)是两个不同的 checkpoint,而一个聚合模型
// 只能固定一个生成段模型,合并不了。
//
// ── 画幅上限不在这里配 ──────────────────────────────────────────────
//
// H3 的面积上限(768×1344)由适配器统一钳(h3ApplyCanvas → h3ClampPixels),档位词与
// 像素串两条入口过同一道闸。聚合配置不重复表达它 —— 引擎契约在适配器里只有一份,
// 抄进配置必然漂移。
//
// ── generate.overrides 为什么必须有 ────────────────────────────────
//
// **档位词这条入口不是"钳",是"拒"**:relay/minimaxv2 的 resolveResolution 对 2K
// 直接返回 400(「self-hosted MiniMax-H3 deployment tops out at 768P」),不做钳位。
// 所以客户调 minimax-h3-2k 时传 resolution=2K —— 也就是这个聚合模型名承诺的东西 ——
// 请求会原地失败,而不是"被钳到 768P 再超分"。
//
// overrides 把生成段固定成 768P,最终的 2K 交给超分段产出。这正是
// middleware/aggregate_expand.go 里那段注释说的「客户传的是**最终**尺寸,
// 生成段收到的必须是**中间**尺寸」。
//
// ⚠️ **键名是 `size`,不是 `resolution`。** applyAggregateExpansion 改写的是
// `Distribute()` 里那份**统一任务契约**的 body(prompt/model/images/size/…),
// H3 也是从 `body["size"]` 取档位词(h3ApplyCanvas → h3ShortEdgeFromSizeToken)。
// 写成 `resolution` 的话这条路上没人读它,客户的 size=2K 原样打到引擎上 ——
// 覆盖落空且不报错,正是这个机制要解决的问题本身。
//
// `resolution` 是**另一条路**(官方形状的 /v2/video_generation)上的字段名,
// 但那条路的 MiniMaxV2CreateConvert 跑在 TokenAuth/Distribute **之前**
// (router/video-router.go),它自己就会把 resolution 转成 size 并调
// resolveResolution —— 2K 在那里直接 400,聚合展开根本轮不到。
const DefaultAggregateModelConfig = `[
  {
    "name": "minimax-h3-2k",
    "type": "video",
    "enabled": true,
    "note": "H3 帧族(文生/图生/首尾帧/尾帧)2K:生成 → SwiftVR 超分。等价于体验区的两段编排,集成方只看到一个模型名和一个任务。提示词请自行扩写后再传。",
    "prompt_enhance": { "model": "MiniMax-M3" },
    "generate": { "model": "minimax-h3-fl2va", "overrides": { "size": "768P" } },
    "upscale": { "model": "swiftvr", "target_size": "2k" }
  },
  {
    "name": "minimax-h3-ref-2k",
    "type": "video",
    "enabled": true,
    "note": "H3 参考族(参考图/参考视频生视频)2K。参考族是另一个 checkpoint,不能和帧族共用一条流水线。",
    "prompt_enhance": { "model": "MiniMax-M3" },
    "generate": { "model": "minimax-h3-ref2va", "overrides": { "size": "768P" } },
    "upscale": { "model": "swiftvr", "target_size": "2k" }
  }
]`
