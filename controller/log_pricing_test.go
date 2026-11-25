package controller

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func usdUsageLog(t *testing.T, quota int, more map[string]any) *model.Log {
	t.Helper()
	other := map[string]any{
		"pricing_currency": "USD", "price_unit": "cny", "model_source_rate": 6.8,
		"model_ratio": 34, "completion_ratio": 2, "model_price": -1, "group_ratio": 1,
	}
	for k, v := range more {
		other[k] = v
	}
	raw, err := common.Marshal(other)
	require.NoError(t, err)
	return &model.Log{Type: model.LogTypeConsume, Quota: quota, PromptTokens: 1000, CompletionTokens: 500, Other: string(raw)}
}

func TestUSDUsageLogPricingShowsDualCurrencyCatalog(t *testing.T) {
	expression := `v2:tier("base", (p * 5 + c * 10) * fx)`
	for _, tc := range []struct {
		name     string
		fields   map[string]any
		catalog  []string
		rate     string
		toolRate string
	}{
		{"tokens", nil, []string{"输入单价：$5.00 / 1M tokens（≈ ¥34.00）", "输出单价：$10.00 / 1M tokens（≈ ¥68.00）"}, "6.8", ""},
		{"small per-call price", map[string]any{"model_price": 0.0000000075, "model_source_rate": 7.5, "group_ratio": 0.2},
			[]string{"按次单价：$0.000000001 / 次（≈ ¥0.000000008）"}, "7.5", ""},
		{"time window", map[string]any{"completion_ratio": 1, "group_ratio": 0.4, "time_factors": map[string]float64{"input": 0.5, "output": 0.25}},
			[]string{"输入单价：$2.50 / 1M tokens（≈ ¥17.00）", "输出单价：$2.50 / 1M tokens（≈ ¥17.00）"}, "6.8", ""},
		{"tools cache image audio", map[string]any{"cache_ratio": 0.2, "image_ratio": 3, "audio_input_price": 14, "web_search_price": 42, "file_search_price": 3, "tool_source_rate": 7, "usd_tool_price_fields": []string{"web_search_price", "audio_input_price"}},
			[]string{"缓存命中单价：$1.00 / 1M tokens（≈ ¥6.80）", "图片输入单价：$15.00 / 1M tokens（≈ ¥102.00）", "音频输入单价：$2.00 / 1M tokens（≈ ¥14.00）", "Web搜索单价：$6.00 / 1K 次（≈ ¥42.00）", "文件搜索单价：¥3 / 1K 次"}, "6.8", "7"},
		{"tier with its own snapshot", map[string]any{"billing_mode": "tiered_expr", "matched_tier": "base", "expr_b64": base64.StdEncoding.EncodeToString([]byte(expression)), "expr_fx": 6.9},
			[]string{"命中档位：base", "输入价格：$5.00 / 1M tokens（≈ ¥34.50）", "输出价格：$10.00 / 1M tokens（≈ ¥69.00）"}, "6.9", ""},
		{"tier cache and image output", map[string]any{"billing_mode": "tiered_expr", "matched_tier": "base", "cache_tokens": 100,
			"expr_b64": base64.StdEncoding.EncodeToString([]byte(`v2:tier("base", (p * 5 + cr + cc1h * 0.5 + img_o * 30) * fx)`)), "expr_fx": 6.8},
			[]string{"缓存命中价格：$1.00 / 1M tokens（≈ ¥6.80）", "缓存创建价格（长 TTL：1h/12h）：$0.50 / 1M tokens（≈ ¥3.40）", "图片输出价格：$30.00 / 1M tokens（≈ ¥204.00）"}, "6.8", ""},
		{"video per-second catalog", map[string]any{"video_price_mode": "per_second", "video_unit_price": 6.8, "video_billed_seconds": 1.625},
			[]string{"视频单价：$1.00 / 秒（≈ ¥6.80）"}, "6.8", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			detail := buildUsageLogPricing(usdUsageLog(t, 68_000_000, tc.fields))
			require.NotNil(t, detail)
			require.Equal(t, tc.rate, detail.Rate)
			require.Equal(t, tc.toolRate, detail.ToolRate)
			for _, price := range tc.catalog {
				require.Contains(t, detail.Catalog, price)
			}
		})
	}
}

func TestUsageLogPricingDoesNotInventHistoricalFX(t *testing.T) {
	legacy := buildUsageLogPricing(usdUsageLog(t, 13, map[string]any{"price_unit": "", "model_price": 0.2, "model_source_rate": nil}))
	require.Equal(t, []string{"按次单价：$0.20 / 次", "未记录当次换算汇率"}, legacy.Catalog)
	require.Empty(t, legacy.Rate)

	tier := func(expr string) *usageLogPricing {
		return buildUsageLogPricing(usdUsageLog(t, 13, map[string]any{
			"billing_mode": "tiered_expr", "matched_tier": "base", "expr_b64": base64.StdEncoding.EncodeToString([]byte(expr)),
		}))
	}
	require.Equal(t, []string{"命中档位：base", "输入价格：$5.00 / 1M tokens", "未记录当次换算汇率"},
		tier(`v2:tier("base", p * 5 * fx)`).Catalog)
	require.Equal(t, []string{"命中档位：base", "未记录当次换算汇率"},
		tier(`v2:tier("base", p * 5 * fx * fx)`).Catalog)

	noRateCNYFields := buildUsageLogPricing(usdUsageLog(t, 13, map[string]any{"model_source_rate": nil}))
	require.Equal(t, []string{"未记录当次换算汇率"}, noRateCNYFields.Catalog)
	require.Nil(t, buildUsageLogPricing(usdUsageLog(t, 13, map[string]any{"pricing_currency": "CNY"})))
}

func TestUSDUsageLogCSVMatchesLogPageForAdminAndSelf(t *testing.T) {
	db := setupExportLimitDB(t)
	const user = 9610
	seedExportLimitUser(t, db, user, "csv_usd_pricing")
	log := usdUsageLog(t, 68_000_000, nil)
	log.UserId, log.Username = user, "csv_usd_pricing"
	require.NoError(t, db.Create(log).Error)
	for _, handlers := range []struct{ list, export func(*gin.Context) }{
		{GetAllLogs, ExportAllLogs}, {GetUserLogs, ExportUserLogs},
	} {
		resp := callLogListHandler(t, handlers.list, user, "/api/log/?p=1&page_size=10")
		items := resp["data"].(map[string]any)["items"].([]any)
		require.Len(t, items, 1)
		page := items[0].(map[string]any)["pricing_detail"].(map[string]any)
		rec := callExportHandler(t, handlers.export, user, "type=2")
		rows := parseCSVBody(t, rec.Body.Bytes())
		require.Len(t, rows, 2)
		require.Len(t, rows[1], len(rows[0]))
		fields := make(map[string]string)
		for i, header := range rows[0] {
			fields[header] = rows[1][i]
		}
		require.NotContains(t, fields, "计费过程")
		require.NotContains(t, page, "process")
		require.Equal(t, page["currency"], fields["目录价币种"])
		require.Equal(t, page["rate"], fields["本次模型汇率"])
		require.Equal(t, page["tool_rate"], fields["本次工具汇率"])
		var lines []any
		for _, line := range strings.Split(fields["目录价"], "\n") {
			lines = append(lines, line)
		}
		require.Equal(t, page["catalog"], lines)
		require.Equal(t, "0.068000000", fields["费用(CNY)"])
	}
}
