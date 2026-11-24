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
	// （原设计文档 §4，已丢失；解析链见 docs/group-concepts.md §2）。
	UserRuleMatch string  // 命中的模式串；"" = 未命中
	UserRuleValue float64 // 恒为 multiply 的乘数

	// Layer 4 时段价目的解析痕迹。与上面几个「只落库不展示」的字段不同，
	// 这一组会渲染到使用日志详情里：用户要能自己验证「夜里下单确实便宜了」，
	// 看不见的优惠等于没有优惠。
	TimeWindow string  // 命中的时段模板键；"" = 未命中
	TimeLabel  string  // 时段模板显示名，如「深夜档」
	TimeValue  float64 // 输入侧系数（乘在常规价上）
	// TimeOutputFactor 是**输出侧系数**。分时段价目不一定等比例变化
	// （忙时输入 2/输出 8、闲时输入 1/输出 7 是合法且常见的价目），
	// 而整单只乘 GroupRatio（= 输入侧），所以输出价要靠它把差额折回 completion_ratio。
	// 未命中时段或与输入同比例时，它等于 TimeValue。
	// TimeFactors 是命中时段的**分量系数表**（分量 → 系数，稀疏）。
	// 整单只乘 input 那一项（TimeValue），其余分量的差额由 ScaleComponent
	// 折回各自的相对倍率（completion / cache / image / audio…）。
	TimeFactors map[string]float64
}

// ScaleComponent 返回应折进该分量相对倍率的系数 = f_component / f_input。
//
// 计费公式里整单只乘一个标量（GroupRatio = NormalRatio × f_input），所以除 input 外的
// 每个分量只要把 f_c / f_input 折进自己的相对倍率，最终价格就等于
// 「该分量的时段目录价 × 折扣」。这样 160 余处读 CompletionRatio / CacheRatio … 的
// 地方一行都不用改，只需在取值的那 9 个点调这个函数。
//
// 未配置某分量（表里没有该键）= 该分量不随时段变 → f_c = 1。
func ScaleComponent(component string, factors map[string]float64) float64 {
	if len(factors) == 0 {
		return 1
	}
	fIn := factors["input"]
	if fIn <= 0 {
		fIn = 1
	}
	fC := 1.0
	if v, ok := factors[component]; ok && v > 0 {
		fC = v
	}
	if fC == fIn {
		return 1
	}
	return fC / fIn
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

// TimeRuleLog 返回时段折扣的紧凑表示，供日志 other 字段使用。
// 取显示名而非模板键：这一条会直接呈现给用户，「深夜档:×0.7」比「night:×0.7」可读。
// Layer 4 恒为 multiply，故不区分模式。未命中返回空串。
func (g GroupRatioInfo) TimeRuleLog() string {
	if g.TimeWindow == "" {
		return ""
	}
	name := g.TimeLabel
	if name == "" {
		name = g.TimeWindow
	}
	return fmt.Sprintf("%s:×%g", name, g.TimeValue)
}

type PriceData struct {
	PricingCurrency      string
	ModelSourceRate      float64
	ToolSourceRate       float64
	ToolPrices           map[string]float64
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

func (p PriceData) ModelPriceFactor() float64 {
	if p.ModelSourceRate > 0 {
		return p.ModelSourceRate
	}
	return 1
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
