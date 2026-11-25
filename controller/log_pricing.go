package controller

import (
	"encoding/base64"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/types"
	"github.com/shopspring/decimal"
)

type usageLogPricing struct {
	Currency string   `json:"currency"`
	Rate     string   `json:"rate"`
	ToolRate string   `json:"tool_rate"`
	Catalog  []string `json:"catalog"`
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

func logQuotaYuan(quota int) decimal.Decimal {
	return decimal.NewFromInt(int64(quota)).DivRound(decimal.NewFromInt(common.QuotaPerYuan), 9)
}

func formatCatalogAmount(value decimal.Decimal, minDigits int32) string {
	rounded := value.Round(9)
	if rounded.IsZero() && !value.IsZero() {
		rounded = value
	}
	if rounded.Exponent() >= -minDigits || rounded.Equal(rounded.Round(minDigits)) {
		return rounded.StringFixed(minDigits)
	}
	return rounded.String()
}

func catalogPrice(label, unit string, price, rate decimal.Decimal, sourceUSD bool) string {
	if !sourceUSD && !rate.IsPositive() {
		return label + "：¥" + formatCatalogAmount(price, 0) + " / " + unit
	}
	usd, cny := price, price.Mul(rate)
	if !sourceUSD {
		usd, cny = price.DivRound(rate, 24), price
	}
	line := label + "：$" + formatCatalogAmount(usd, 2) + " / " + unit
	if rate.IsPositive() {
		line += "（≈ ¥" + formatCatalogAmount(cny, 2) + "）"
	}
	return line
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

	detail := &usageLogPricing{Currency: "USD", Catalog: []string{}}
	rate := m.num("model_source_rate")
	switch m.str("billing_mode") {
	case "tiered_expr":
		rawExpr, _ := base64.StdEncoding.DecodeString(m.str("expr_b64"))
		if matched := m.str("matched_tier"); matched != "" {
			detail.Catalog = append(detail.Catalog, "命中档位："+matched)
		}
		prices, modelRate := tierLogCatalog(m, string(rawExpr), rate)
		rate = modelRate
		detail.Catalog = append(detail.Catalog, prices...)
	default:
		switch m.str("price_unit") {
		case common.PriceUnitCNY:
			if rate.IsPositive() {
				detail.Catalog = append(detail.Catalog, modelLogCatalog(m, rate, false)...)
			}
		default:
			detail.Catalog = append(detail.Catalog, "未记录可识别的单价口径")
		}
	}
	if rate.IsPositive() {
		detail.Rate = rate.String()
	} else {
		detail.Catalog = append(detail.Catalog, "未记录当次换算汇率")
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
	for _, tool := range []struct{ key, label, unit string }{
		{"web_search_price", "Web搜索单价", "1K 次"},
		{"file_search_price", "文件搜索单价", "1K 次"},
		{"audio_input_price", "音频输入单价", "1M tokens"},
		{"image_generation_call_price", "图片生成调用单价", "次"},
	} {
		price, ok := m.number(tool.key)
		if !ok {
			continue
		}
		// price_unit 缺失的历史日志无法判断工具价的币种，不再猜（旧分支按美元解释并乘汇率）。
		if m.str("price_unit") != common.PriceUnitCNY {
			continue
		}
		if !toolFields[tool.key] {
			detail.Catalog = append(detail.Catalog, catalogPrice(tool.label, tool.unit, price, decimal.Zero, false))
		} else if toolRate.IsPositive() {
			detail.Catalog = append(detail.Catalog, catalogPrice(tool.label, tool.unit, price, toolRate, false))
			detail.ToolRate = toolRate.String()
		} else {
			detail.Catalog = append(detail.Catalog, tool.label+"未记录当次工具汇率")
		}
	}
	return detail
}

func modelLogCatalog(m pricingLogFields, rate decimal.Decimal, sourceUSD bool) []string {
	price := func(label, unit string, amount decimal.Decimal) string {
		return catalogPrice(label, unit, amount, rate, sourceUSD)
	}
	if mode := m.str("video_price_mode"); mode != "" {
		amount, ok := m.number("video_unit_price")
		if !ok {
			return nil
		}
		unit := "次"
		switch mode {
		case "token":
			unit = "1M tokens"
		case "per_second":
			unit = "秒"
		}
		return []string{price("视频单价", unit, amount)}
	}

	factors := make(map[string]float64)
	if frozen, ok := m["time_factors"].(map[string]any); ok {
		for key := range frozen {
			factors[key] = pricingLogFields(frozen).num(key).InexactFloat64()
		}
	}
	one := decimal.NewFromInt(1)
	inputFactor := decimal.NewFromFloat(factors["input"])
	if !inputFactor.IsPositive() {
		inputFactor = one
	}
	if amount, ok := m.number("model_price"); ok && !amount.IsNegative() {
		factor := decimal.NewFromFloat(types.ScaleComponent("per_call", factors))
		return []string{price("按次单价", "次", amount.Mul(factor).Mul(inputFactor))}
	}
	base, ok := m.number("model_ratio")
	if !ok {
		return nil
	}
	if sourceUSD {
		base = base.Mul(decimal.NewFromInt(2))
	}
	base = base.Mul(inputFactor)
	tokenPrice := func(label string, ratio decimal.Decimal) string {
		return price(label, "1M tokens", base.Mul(ratio))
	}
	prices := []string{
		tokenPrice("输入单价", one),
		tokenPrice("输出单价", m.num("completion_ratio")),
	}
	if m.flag("audio") || m.flag("ws") {
		audio := m.num("audio_ratio")
		return append(prices, tokenPrice("音频输入单价", audio),
			tokenPrice("音频输出单价", audio.Mul(m.num("audio_completion_ratio"))))
	}
	for _, part := range []struct{ label, key string }{
		{"缓存命中单价", "cache_ratio"},
		{"缓存创建单价", "cache_creation_ratio"},
		{"缓存创建 (5m) 单价", "cache_creation_ratio_5m"},
		{"缓存创建 (1h) 单价", "cache_creation_ratio_1h"},
		{"图片输入单价", "image_ratio"},
	} {
		if ratio, exists := m.number(part.key); exists {
			prices = append(prices, tokenPrice(part.label, ratio))
		}
	}
	return prices
}
