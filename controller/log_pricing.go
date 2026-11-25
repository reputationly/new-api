package controller

import (
	"encoding/base64"
	"fmt"
	"math"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/types"
	"github.com/shopspring/decimal"
)

type usageLogPricing struct {
	Currency string   `json:"currency"`
	Rate     string   `json:"rate"`
	ToolRate string   `json:"tool_rate"`
	Catalog  []string `json:"catalog"`
	Process  []string `json:"process"`
}

type usageLogView struct {
	*model.Log
	Pricing *usageLogPricing `json:"pricing_detail,omitempty"`
}

func usageLogViews(logs []*model.Log) []usageLogView {
	views := make([]usageLogView, 0, len(logs))
	for _, log := range logs {
		views = append(views, usageLogView{log, buildUsageLogPricing(log)})
	}
	return views
}

type pricingLogFields map[string]any

func (m pricingLogFields) str(key string) string {
	v, _ := m[key].(string)
	return v
}

func (m pricingLogFields) flag(key string) bool {
	v, _ := m[key].(bool)
	return v
}

func (m pricingLogFields) number(key string) (decimal.Decimal, bool) {
	switch v := m[key].(type) {
	case float64:
		return decimal.NewFromFloat(v), true
	case string:
		d, err := decimal.NewFromString(v)
		return d, err == nil
	default:
		return decimal.Zero, false
	}
}

func (m pricingLogFields) num(key string) decimal.Decimal {
	d, _ := m.number(key)
	return d
}

func (m pricingLogFields) tokens(key string) int {
	return int(m.num(key).IntPart())
}

var usageQuotaPerYuan = decimal.NewFromInt(common.QuotaPerYuan)

func logQuotaYuan(quota int) decimal.Decimal {
	return decimal.NewFromInt(int64(quota)).DivRound(usageQuotaPerYuan, 9)
}

type usagePriceTerm struct {
	label, unit             string
	price, rate, multiplier decimal.Decimal
	quantity                int
	quantityOverride        decimal.Decimal
	unitPower               int32
	settledAmount           *decimal.Decimal
}

func (t usagePriceTerm) displayPrice() string {
	if t.rate.IsPositive() {
		return "$" + t.price.DivRound(t.rate, 24).String()
	}
	return "¥" + t.price.String()
}

func (t usagePriceTerm) formula() string {
	units := fmt.Sprint(t.quantity)
	if t.quantityOverride.IsPositive() {
		units = t.quantityOverride.String()
	}
	if t.unitPower > 0 {
		units += " / " + t.unit
	}
	s := t.displayPrice() + " × " + units
	if !t.multiplier.Equal(decimal.NewFromInt(1)) {
		s += " × " + t.multiplier.Round(24).String()
	}
	if t.rate.IsPositive() {
		s += " × " + t.rate.String()
	}
	return s
}

func (t usagePriceTerm) amount() decimal.Decimal {
	if t.settledAmount != nil {
		return *t.settledAmount
	}
	return t.price.Mul(decimal.NewFromInt(int64(t.quantity))).Shift(-t.unitPower).Mul(t.multiplier)
}

func buildUsageLogPricing(log *model.Log) *usageLogPricing {
	if log.Type != model.LogTypeConsume {
		return nil
	}
	raw, err := common.StrToMap(log.Other)
	m := pricingLogFields(raw)
	if err != nil || m.str("pricing_currency") != "USD" ||
		m.flag("violation_fee") || m.str("violation_fee_code") != "" || m.flag("violation_fee_marker") || m.str("violation_fee_marker") != "" {
		return nil
	}
	detail := &usageLogPricing{Currency: "USD"}
	charged := "¥" + logQuotaYuan(log.Quota).String()
	tiered := m.str("billing_mode") == "tiered_expr"
	rate := m.num("model_source_rate")
	exprRate := m.num("expr_fx")
	var expression string
	if tiered {
		rawExpr, _ := base64.StdEncoding.DecodeString(m.str("expr_b64"))
		expression = string(rawExpr)
		if expression != "" {
			detail.Process = append(detail.Process, "计费表达式："+expression)
		}
		if matched := m.str("matched_tier"); matched != "" {
			detail.Catalog = append(detail.Catalog, "命中档位："+matched)
		}
		if exprRate.IsPositive() {
			rate = exprRate
			detail.Process = append(detail.Process, "本次 fx = "+exprRate.String())
		} else if billingexpr.UsedVars(expression)["fx"] {
			rate = decimal.Zero
		}
	}
	if !rate.IsPositive() || m.str("price_unit") != common.PriceUnitCNY {
		detail.Process = append(detail.Process, "历史日志缺少汇率或价格口径快照，不回算美元单价与计费过程。", "实际扣费："+charged)
		return detail
	}
	detail.Rate = rate.String()
	detail.Process = append(detail.Process, "本次换算：1 USD = "+detail.Rate+" CNY")

	group, hasGroup := m.number("group_ratio")
	one := decimal.NewFromInt(1)
	factors := make(map[string]float64)
	if frozen, ok := m["time_factors"].(map[string]any); ok {
		for key := range frozen {
			factors[key] = pricingLogFields(frozen).num(key).InexactFloat64()
		}
	}
	inputFactor := decimal.NewFromFloat(factors["input"])
	if !inputFactor.IsPositive() {
		inputFactor = one
	}
	discount := group.DivRound(inputFactor, 28)
	add := func(terms *[]usagePriceTerm, label, unit string, amount, rate, multiplier decimal.Decimal, quantity int, unitPower int32) {
		*terms = append(*terms, usagePriceTerm{label: label, unit: unit, price: amount, rate: rate, multiplier: multiplier, quantity: quantity, unitPower: unitPower})
	}
	var terms []usagePriceTerm
	modelPrice, hasModelPrice := m.number("model_price")
	basePrice, hasBasePrice := m.number("model_ratio")
	addModelToken := func(label string, ratio decimal.Decimal, quantity int) {
		price := basePrice.Mul(ratio)
		add(&terms, label, "1M tokens", price.Mul(inputFactor), rate, discount, quantity, 6)
		settled := price.Mul(group).Mul(decimal.NewFromInt(int64(quantity))).Shift(-6)
		terms[len(terms)-1].settledAmount = &settled
	}
	verifiable := hasGroup
	var tierQuota float64
	var hasTierQuota bool
	var tierHasQuantities bool
	var videoQuota *int
	switch {
	case tiered:
		var result tierLogDetail
		result = tierLogPricing(m, log, expression, exprRate, rate)
		terms = result.terms
		detail.Process = append(detail.Process, result.lines...)
		tierQuota, hasTierQuota = result.quota, result.valid
		tierHasQuantities = result.hasQuantities
		verifiable = hasTierQuota
	case m.str("video_price_mode") != "":
		unit, quantity, power := "次", 1, int32(0)
		price, hasUnit := m.number("video_unit_price")
		verifiable = verifiable && hasUnit
		rawPrice := price.InexactFloat64()
		billedSeconds := decimal.Zero
		switch m.str("video_price_mode") {
		case "token":
			unit, quantity, power = "1M tokens", log.CompletionTokens, 6
			rawPrice = float64(quantity) / 1e6 * rawPrice
		case "per_second":
			unit, quantity = "秒", m.tokens("video_seconds")
			billedSeconds = m.num("video_billed_seconds")
			if billedSeconds.IsPositive() {
				rawPrice *= billedSeconds.InexactFloat64()
			} else {
				rawPrice *= float64(quantity)
			}
		}
		if hasUnit {
			add(&terms, "视频单价", unit, price, rate, group, quantity, power)
			terms[len(terms)-1].quantityOverride = billedSeconds
			calculated := int(math.Round(rawPrice * common.QuotaPerYuan * group.InexactFloat64()))
			videoQuota = &calculated
		}
	case hasModelPrice && !modelPrice.IsNegative():
		factor := decimal.NewFromFloat(types.ScaleComponent("per_call", factors))
		add(&terms, "按次单价", "次", modelPrice.Mul(factor).Mul(inputFactor), rate, discount, 1, 0)
		settled := modelPrice.Mul(factor).Mul(group)
		terms[len(terms)-1].settledAmount = &settled
	case hasBasePrice:
		if m.flag("audio") || m.flag("ws") {
			addModelToken("输入单价", one, m.tokens("text_input"))
			addModelToken("输出单价", m.num("completion_ratio"), m.tokens("text_output"))
			addModelToken("音频输入单价", m.num("audio_ratio"), m.tokens("audio_input"))
			addModelToken("音频输出单价", m.num("audio_ratio").Mul(m.num("audio_completion_ratio")), m.tokens("audio_output"))
			break
		}
		cache, writes := m.tokens("cache_tokens"), m.tokens("cache_creation_tokens")
		short, long := m.tokens("cache_creation_tokens_5m"), m.tokens("cache_creation_tokens_1h")
		image, audio := m.tokens("image_output"), m.tokens("audio_input_token_count")
		input := log.PromptTokens - image - audio
		if !m.flag("claude") && m.str("usage_semantic") != "anthropic" && !m.flag("legacy_claude_input") {
			input -= cache + writes
		}
		addModelToken("输入单价", one, input)
		addModelToken("输出单价", m.num("completion_ratio"), log.CompletionTokens)
		for _, part := range []struct {
			label, key string
			quantity   int
		}{
			{"缓存命中单价", "cache_ratio", cache},
			{"缓存创建单价", "cache_creation_ratio", max(writes-short-long, 0)},
			{"缓存创建 (5m) 单价", "cache_creation_ratio_5m", short},
			{"缓存创建 (1h) 单价", "cache_creation_ratio_1h", long},
			{"图片输入单价", "image_ratio", image},
		} {
			if ratio, exists := m.number(part.key); exists {
				addModelToken(part.label, ratio, part.quantity)
			}
		}
	default:
		verifiable = false
	}

	toolFields := make(map[string]bool)
	if fields, ok := m["usd_tool_price_fields"].([]any); ok {
		for _, field := range fields {
			if key, ok := field.(string); ok {
				toolFields[key] = true
			}
		}
	}
	toolRate := m.num("tool_source_rate")
	toolStart := len(terms)
	for _, tool := range []struct {
		key, label, unit, count string
		power                   int32
	}{
		{"web_search_price", "Web搜索单价", "1K 次", "web_search_call_count", 3},
		{"file_search_price", "文件搜索单价", "1K 次", "file_search_call_count", 3},
		{"audio_input_price", "音频输入单价", "1M tokens", "audio_input_token_count", 6},
		{"image_generation_call_price", "图片生成调用单价", "次", "", 0},
	} {
		price, ok := m.number(tool.key)
		if !ok {
			continue
		}
		quantity := 1
		if tool.count != "" {
			quantity = m.tokens(tool.count)
		}
		sourceRate := decimal.Zero
		if toolFields[tool.key] {
			if !toolRate.IsPositive() {
				detail.Process = append(detail.Process, tool.label+"缺少当次工具汇率快照。")
				verifiable = false
				continue
			}
			sourceRate = toolRate
			detail.ToolRate = toolRate.String()
		}
		add(&terms, tool.label, tool.unit, price, sourceRate, group, quantity, tool.power)
	}
	if detail.ToolRate != "" {
		detail.Process = append(detail.Process, "工具本次换算：1 USD = "+detail.ToolRate+" CNY")
	}
	total := decimal.Zero
	var formula []string
	for i, term := range terms {
		detail.Catalog = append(detail.Catalog, term.label+"："+term.displayPrice()+" / "+term.unit)
		if tiered && i < toolStart || term.quantity == 0 && !term.quantityOverride.IsPositive() {
			continue
		}
		total = total.Add(term.amount())
		formula = append(formula, term.formula())
	}
	otherMultiplier := one
	if ratios, ok := m["billing_other_ratios"].(map[string]any); ok && !tiered {
		for key := range ratios {
			otherMultiplier = otherMultiplier.Mul(pricingLogFields(ratios).num(key))
		}
	}
	if tiered && hasTierQuota {
		tierGroup := m.num("expr_group_ratio")
		if _, exists := m.number("expr_group_ratio"); !exists {
			tierGroup = group
		}
		beforeGroup := decimal.NewFromFloat(tierQuota).DivRound(usageQuotaPerYuan, 28)
		total = total.Add(beforeGroup.Mul(tierGroup))
		var exprFormula []string
		if tierHasQuantities {
			for _, term := range terms[:toolStart] {
				if term.quantity != 0 {
					term.multiplier = tierGroup
					exprFormula = append(exprFormula, term.formula())
				}
			}
		}
		if len(exprFormula) == 0 {
			exprFormula = []string{"$" + beforeGroup.DivRound(rate, 24).String() + "（表达式结果） × " + tierGroup.String() + " × " + rate.String()}
		}
		formula = append(exprFormula, formula...)
	}
	if !otherMultiplier.Equal(one) {
		total = total.Mul(otherMultiplier)
		formula = []string{"(" + strings.Join(formula, " + ") + ") × " + otherMultiplier.String()}
	}
	calculated := int(total.Mul(usageQuotaPerYuan).Round(0).IntPart())
	if videoQuota != nil {
		calculated = *videoQuota
	}
	if tiered && hasTierQuota && len(terms) == toolStart {
		tierGroup, ok := m.number("expr_group_ratio")
		if !ok {
			tierGroup = group
		}
		calculated = billingexpr.QuotaRound(tierQuota * tierGroup.InexactFloat64())
	}
	switch {
	case verifiable && len(formula) > 0 && calculated == log.Quota:
		detail.Process = append(detail.Process, strings.Join(formula, " + ")+" = "+charged+"（按 quota 舍入）")
	case !tiered && log.Quota == 0 && log.PromptTokens+log.CompletionTokens == 0 &&
		m.str("video_price_mode") == "" && !m.flag("is_task") && m.str("task_id") == "":
		detail.Process = append(detail.Process, "本次无计费用量 = "+charged)
	case verifiable && !tiered && basePrice.Mul(group).IsPositive() && calculated <= 0 &&
		log.Quota == 1 && log.PromptTokens+log.CompletionTokens > 0:
		detail.Process = append(detail.Process, "最低计费 1 quota = "+charged)
	default:
		detail.Process = append(detail.Process, "此笔日志无法仅凭已记录明细复算扣费。")
	}
	detail.Process = append(detail.Process, "实际扣费："+charged)
	return detail
}
