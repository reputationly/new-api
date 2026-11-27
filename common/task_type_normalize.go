package common

import "strings"

// 门面 task_type：平台统一任务契约里 metadata.task_type 的那五个玩法词。
//
// **这份常量是唯一的**：聚合配置(generate.task_models 的 key)、路由分派、能力目录的
// 玩法过滤全读它。抄第二份必然漂移,而漂移的症状是「配置里写的 key 永远不命中」——
// 不报错,只是每个请求都回落到默认生成段。
const (
	FacadeTaskTypeT2V   = "t2v"
	FacadeTaskTypeI2V   = "i2v"
	FacadeTaskTypeL2VA  = "l2va"
	FacadeTaskTypeFLF2V = "flf2v"
	FacadeTaskTypeR2VA  = "r2va"
)

// FacadeTaskTypeKeys 五个门面玩法，顺序固定（生成配置模板与校验消息都读它）。
func FacadeTaskTypeKeys() []string {
	return []string{
		FacadeTaskTypeT2V, FacadeTaskTypeI2V, FacadeTaskTypeL2VA,
		FacadeTaskTypeFLF2V, FacadeTaskTypeR2VA,
	}
}

// IsFacadeTaskTypeKey 这个 key 是不是**规范写法**的门面玩法（trim + 小写后等于五个之一）。
//
// **别名不算**：`r2v` 语义上是 r2va，但它不是配置里该写的 key —— 运行时的查找键是
// 归一后的 `r2va`，写别名永远不命中，而且不报错。聚合配置的干跑校验用这个函数
// 判 key 合不合法；判断"请求属于哪个玩法"用 NormalizeRoutedTaskType。
func IsFacadeTaskTypeKey(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case FacadeTaskTypeT2V, FacadeTaskTypeI2V, FacadeTaskTypeL2VA,
		FacadeTaskTypeFLF2V, FacadeTaskTypeR2VA:
		return true
	}
	return false
}

// NormalizeRoutedTaskType 把**路由口径**的 task_type 归一成五个门面玩法之一。
//
// 路由口径比编译口径宽一格：
//
//	r2v / rv2v     参考族的两种写法,语义就是 r2va(见 relay/common 的素材取值口径)
//	continuation   续写。**展开时它还没被改成 i2v** —— /v1 上 Distribute 是 group 级
//	               中间件,跑在 route 级的 ContinuationConvert 之前,所以聚合展开看到的
//	               metadata.task_type 仍是 "continuation"。它在流水线里最终一定会变成
//	               一次 i2v(源片末帧当首帧),所以这里按 i2v 分派。
//
// **不要拿它做增强/编译的判据**：那样编译侧会按"有一张首帧"去写分镜,而展开阶段那张
// 首帧还不存在。编译侧用 NormalizeCompilerTaskType。
func NormalizeRoutedTaskType(raw string) (string, bool) {
	if key, ok := normalizeTaskTypeKey(raw); ok {
		return key, true
	}
	if strings.EqualFold(strings.TrimSpace(raw), "continuation") {
		return FacadeTaskTypeI2V, true
	}
	return "", false
}

// NormalizeCompilerTaskType **增强/编译口径**的归一。
//
// 与 NormalizeRoutedTaskType 的唯一差别是**不认续写**：continuation 返回 false,
// 调用方据此跳过编译、直接走 text 改写。理由见 middleware.inferCompilerTaskType:
// 续写的首帧是运行时抽出来的,这时还没有,照 i2v 编出来的分镜描述的是一张不存在的图,
// 而且整套传输检查都会通过 —— 静默出差档。
func NormalizeCompilerTaskType(raw string) (string, bool) {
	return normalizeTaskTypeKey(raw)
}

// normalizeTaskTypeKey 五个门面玩法的严格归一：trim + 小写，参考族三种写法收成 r2va。
func normalizeTaskTypeKey(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case FacadeTaskTypeT2V:
		return FacadeTaskTypeT2V, true
	case FacadeTaskTypeI2V:
		return FacadeTaskTypeI2V, true
	case FacadeTaskTypeL2VA:
		return FacadeTaskTypeL2VA, true
	case FacadeTaskTypeFLF2V:
		return FacadeTaskTypeFLF2V, true
	case FacadeTaskTypeR2VA, "r2v", "rv2v":
		return FacadeTaskTypeR2VA, true
	}
	return "", false
}
