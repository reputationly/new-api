package gpustackplus

import "strings"

// ERNIE-Image-Turbo 的生产采样参数(异步生图链路)。
//
// **这份逻辑与同步链路 relay/channel/gpustackplus/adaptor.go 的
// applyErnieImageTurboDefaults 是刻意同语义的一对**,与 image_shape.go 同一处境:
// 图片异步化之后体验区生图走 middleware.ImageAsyncConvert → RelayTask → 本适配器,
// 完全不经过同步链路的 ConvertImageRequest。
//
// 为什么不 import 同步侧那份而要复刻:relay/channel/gpustackplus/speech.go 已经
// import 了本包(task/gpustackplus),反向再 import 就成环。
//
// 为什么透传救不了它:ImageAsyncConvert 把原始 body 整体塞进 metadata、适配器再展开到
// body 顶层,所以 seed / negative_prompt / hunyuan 那几个键是**原样转发**、天然没问题。
// 但 ERNIE 这三项是**转换**不是转发 —— 对外只有一个 use_prompt_enhancer,引擎要的是
// extra_args.apply_pe,而步数与 guidance 根本不来自请求、是我们锁死的生产档。
// 不复刻的后果全是静默的:
//
//   - num_inference_steps 不发 → 引擎缺省 50 步(生产档 8 步),**慢 6.25 倍**;
//   - guidance_scale 不发   → 引擎缺省 4.0,等于给一个蒸馏 Turbo 模型开了 CFG;
//   - extra_args 不发       → 引擎 _should_apply_pe 读不到时缺省 **True**,
//     提示词被改写 —— 与「严格文案/排版不被改写」的产品默认正好相反。
//
// ⚠️ 键名必须是 extra_args,不是 extra_params。异步任务端点(/v1/tasks/image/)只把
// gen_params 上**已声明**的字段从 extra_body 搬过去(hasattr 过滤),而
// OmniDiffusionSamplingParams 有 extra_args、没有 extra_params —— 发 extra_params 会被
// 静默丢弃。同步端点 /v1/images/generations 认 extra_params,两条路的契约在引擎侧就是
// 分叉的,别照搬另一边的写法。(这条警告原样抄自同步侧,它当初正是为这个异步端点写的。)
const ernieImageTurboModel = "ernie-image-turbo"

func isErnieImageTurboModel(model string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(model)), ernieImageTurboModel)
}

// applyErnieImageTurboDefaults 把 ERNIE Turbo 的生产档写进 body。
//
// 与同步侧一样有意锁死采样参数、不向外部暴露任意 extra_args / guidance:
// 后续若接 ERNIE Base,应单独建模型 profile,不要放宽这个 Turbo 分支。
//
// modelName 用重定向后的上游名(与同步侧同源);body 里的 use_prompt_enhancer 是对外的
// 通用产品字段,由 metadata 透传进来,取完即收敛成引擎参数。
func applyErnieImageTurboDefaults(body map[string]any, modelName string) {
	if !isErnieImageTurboModel(modelName) {
		return
	}
	body["num_inference_steps"] = 8
	body["guidance_scale"] = 1.0
	// 必须**显式**发 false:引擎读不到时缺省 True。
	body["extra_args"] = map[string]any{
		"apply_pe": imageBoolFromBody(body, "use_prompt_enhancer"),
	}
}

// imageBoolFromBody 从已展开的 body 里取一个布尔产品字段。
//
// 值来自 metadata 的 JSON 反序列化,数字/布尔两种形态都可能落地;缺省、null、非法值
// 一律为 false —— 与同步侧 imageBoolExtraFrom 同口径(调用方对「没传」和「显式 false」
// 的处理相同,不区分三态)。
func imageBoolFromBody(body map[string]any, key string) bool {
	switch v := body[key].(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	case float64:
		return v != 0
	}
	return false
}
