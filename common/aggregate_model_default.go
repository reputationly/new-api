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
// ── 为什么只有两条,且都不带提示词增强段 ──────────────────────────────
//
// **增强段暂时给不出可用的出厂值**。配置结构的注释写着「SystemPrompt 空 = 继承体验区
// 模板(模型级 → tab 级 → 内置默认)」,但那个继承**目前没有实现**:
// service.EnhancePrompt 直接读 cfg.SystemPrompt,为空即 degrade("未配置增强模板"),
// 干跑校验也把空模板判为错误。
//
// 于是出厂若配一个"带增强"的条目而不写模板,得到的是最坏的一种状态:名字叫 enhanced、
// 配置里 enabled 为真,实际每次都用原始提示词生成,且不报错。而把模板写死进这里又与
// 「模板知识不抄第二份」冲突 —— 它是模型相关的(H3 要带字段名的分段结构、LTX-2.5 要长段
// 视听描述),抄两份必然漂移。
//
// 所以出厂只给**不依赖增强**的那条流水线:生成 → 超分。它是聚合模型对集成方最实的价值
// (体验区的 1080P 两段编排搬到后端),不需要任何模板知识。
//
// 等模板继承落地后,再加这几条才有意义:
//   - `minimax-h3-2k-enhanced` / `minimax-h3-ref-2k-enhanced`:同下,外加增强段;
//   - `qwen-image-enhanced` / `qwen-image-edit-enhanced`:图片只有增强段有价值
//     (没有增强的图片聚合等于绕一圈还是裸模型)。图生图那条的 send_input_images
//     必须为真:增强模型看不到底图只能从文字猜,会写出与底图打架的描述。
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
const DefaultAggregateModelConfig = `[
  {
    "name": "minimax-h3-2k",
    "type": "video",
    "enabled": true,
    "note": "H3 帧族(文生/图生/首尾帧/尾帧)2K:生成 → SwiftVR 超分。等价于体验区的两段编排,集成方只看到一个模型名和一个任务。提示词请自行扩写后再传。",
    "generate": { "model": "minimax-h3-fl2va" },
    "upscale": { "model": "swiftvr", "target_size": "2k" }
  },
  {
    "name": "minimax-h3-ref-2k",
    "type": "video",
    "enabled": true,
    "note": "H3 参考族(参考图/参考视频生视频)2K。参考族是另一个 checkpoint,不能和帧族共用一条流水线。",
    "generate": { "model": "minimax-h3-ref2va" },
    "upscale": { "model": "swiftvr", "target_size": "2k" }
  }
]`
