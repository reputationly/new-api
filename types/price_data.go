package types

import "fmt"

// 分组内按模型折扣的两种模式。定义在 types 而非 ratio_setting，是因为 ModelRuleMode
// 会随 GroupRatioInfo 一路流到日志与前端展示，属于共享词汇表。
const (
	// RatioModeMultiply final = base × value。促销折扣，跟随分组基础倍率变化。
	RatioModeMultiply = "multiply"
	// RatioModeOverride final = value。精确定价，与分组基础倍率、身份折扣脱钩。
	RatioModeOverride = "override"
)

type GroupRatioInfo struct {
	// GroupRatio 是解析后的**最终**倍率，下游一切计费只读这个字段。
	// 加入模型级折扣后它可能已包含 GroupModelRatio 的影响，语义仍是「这一单用的倍率」。
	GroupRatio        float64
	GroupSpecialRatio float64
	HasSpecialRatio   bool

	// 以下为模型级折扣的解析痕迹（docs/group-management-redesign.md §5.4）。
	// 只用于日志与展示：光看最终倍率无法反算它是怎么来的，运营对不上账。
	BaseRatio      float64 // Layer 0/1 之后、套用模型规则之前的基准
	ModelRuleMatch string  // 命中的模型模式串，如 "wan2.2-*"；"" = 未命中
	ModelRuleMode  string  // multiply | override
	ModelRuleValue float64

	// Layer 3 用户档折扣的解析痕迹
	// （docs/user-tier-pricing-and-topup-package-design.md §4）。
	UserRuleMatch string  // 命中的模式串；"" = 未命中
	UserRuleValue float64 // 恒为 multiply 的乘数
}

// ModelRuleLog 返回模型级折扣规则的紧凑表示，供日志 other 字段使用。
// 未命中返回空串。
func (g GroupRatioInfo) ModelRuleLog() string {
	if g.ModelRuleMatch == "" {
		return ""
	}
	if g.ModelRuleMode == RatioModeOverride {
		return fmt.Sprintf("%s:=%g", g.ModelRuleMatch, g.ModelRuleValue)
	}
	return fmt.Sprintf("%s:×%g", g.ModelRuleMatch, g.ModelRuleValue)
}

// UserRuleLog 返回用户档折扣规则的紧凑表示，供日志 other 字段使用。
// Layer 3 恒为 multiply，故不区分模式。未命中返回空串。
func (g GroupRatioInfo) UserRuleLog() string {
	if g.UserRuleMatch == "" {
		return ""
	}
	return fmt.Sprintf("%s:×%g", g.UserRuleMatch, g.UserRuleValue)
}

type PriceData struct {
	FreeModel            bool
	ModelPrice           float64
	ModelRatio           float64
	CompletionRatio      float64
	CacheRatio           float64
	CacheCreationRatio   float64
	CacheCreation5mRatio float64
	CacheCreation1hRatio float64
	ImageRatio           float64
	AudioRatio           float64
	AudioCompletionRatio float64
	OtherRatios          map[string]float64
	UsePrice             bool
	Quota                int // 按次计费的最终额度（MJ / Task）
	QuotaToPreConsume    int // 按量计费的预消耗额度
	GroupRatioInfo       GroupRatioInfo
}

func (p *PriceData) AddOtherRatio(key string, ratio float64) {
	if p.OtherRatios == nil {
		p.OtherRatios = make(map[string]float64)
	}
	if ratio <= 0 {
		return
	}
	p.OtherRatios[key] = ratio
}

func (p *PriceData) ToSetting() string {
	return fmt.Sprintf("ModelPrice: %f, ModelRatio: %f, CompletionRatio: %f, CacheRatio: %f, GroupRatio: %f, UsePrice: %t, CacheCreationRatio: %f, CacheCreation5mRatio: %f, CacheCreation1hRatio: %f, QuotaToPreConsume: %d, ImageRatio: %f, AudioRatio: %f, AudioCompletionRatio: %f", p.ModelPrice, p.ModelRatio, p.CompletionRatio, p.CacheRatio, p.GroupRatioInfo.GroupRatio, p.UsePrice, p.CacheCreationRatio, p.CacheCreation5mRatio, p.CacheCreation1hRatio, p.QuotaToPreConsume, p.ImageRatio, p.AudioRatio, p.AudioCompletionRatio)
}
