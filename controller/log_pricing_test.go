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

func TestUSDUsageLogPricingReconcilesToRecordedQuota(t *testing.T) {
	expression := `v2:tier("base", (p * 5 + c * 10) * fx)`
	for _, tc := range []struct {
		name    string
		fields  map[string]any
		quota   int
		catalog []string
		process string
	}{
		{"tokens", nil, 68_000_000, []string{"输入单价：$5 / 1M tokens", "输出单价：$10 / 1M tokens"},
			"$5 × 1000 / 1M tokens × 6.8 + $10 × 500 / 1M tokens × 6.8 = ¥0.068（按 quota 舍入）"},
		{"call halves round away from zero", map[string]any{"model_price": 0.0000000075, "model_source_rate": 7.5, "group_ratio": 0.2},
			2, []string{"按次单价：$0.000000001 / 次"}, "$0.000000001 × 1 × 0.2 × 7.5 = ¥0.000000002（按 quota 舍入）"},
		{"time window", map[string]any{"completion_ratio": 1, "group_ratio": 0.4, "time_factors": map[string]float64{"input": 0.5, "output": 0.25}},
			20_400_000, []string{"输入单价：$2.5 / 1M tokens", "输出单价：$2.5 / 1M tokens"},
			"$2.5 × 1000 / 1M tokens × 0.8 × 6.8 + $2.5 × 500 / 1M tokens × 0.8 × 6.8 = ¥0.0204（按 quota 舍入）"},
		{"tools cache image audio", map[string]any{"cache_tokens": 100, "cache_ratio": 0.2, "image_output": 200, "image_ratio": 3, "audio_input_token_count": 50, "audio_input_price": 14, "web_search_price": 42, "web_search_call_count": 2, "tool_source_rate": 7, "usd_tool_price_fields": []string{"web_search_price", "audio_input_price"}},
			161_880_000, []string{"缓存命中单价：$1 / 1M tokens", "图片输入单价：$15 / 1M tokens", "音频输入单价：$2 / 1M tokens", "Web搜索单价：$6 / 1K 次"},
			"$5 × 650 / 1M tokens × 6.8 + $10 × 500 / 1M tokens × 6.8 + $1 × 100 / 1M tokens × 6.8 + $15 × 200 / 1M tokens × 6.8 + $6 × 2 / 1K 次 × 7 + $2 × 50 / 1M tokens × 7 = ¥0.16188（按 quota 舍入）"},
		{"tier with its own snapshot", map[string]any{"billing_mode": "tiered_expr", "matched_tier": "base", "expr_b64": base64.StdEncoding.EncodeToString([]byte(expression)), "expr_fx": 6.9},
			69_000_000, []string{"命中档位：base", "输入单价：$5 / 1M tokens", "输出单价：$10 / 1M tokens"},
			"$5 × 1000 / 1M tokens × 6.9 + $10 × 500 / 1M tokens × 6.9 = ¥0.069（按 quota 舍入）"},
		{"tier uses normalized quantities", map[string]any{"billing_mode": "tiered_expr", "matched_tier": "base",
			"expr_b64": base64.StdEncoding.EncodeToString([]byte(`v2:tier("base", (p * 5 + cr) * fx)`)), "expr_fx": 6.8,
			"expr_tokens": map[string]any{"p": 800, "cr": 200}, "expr_group_ratio": 0.5, "expr_quota_before_group": 28_560_000},
			14_280_000, []string{"输入单价：$5 / 1M tokens", "缓存命中单价：$1 / 1M tokens"},
			"$5 × 800 / 1M tokens × 0.5 × 6.8 + $1 × 200 / 1M tokens × 0.5 × 6.8 = ¥0.01428（按 quota 舍入）"},
		{"video uses billed duration", map[string]any{"video_price_mode": "per_second", "video_unit_price": 6.8, "video_seconds": 1, "video_billed_seconds": 1.625},
			11_050_000_000, []string{"视频单价：$1 / 秒"}, "$1 × 1.625 × 6.8 = ¥11.05（按 quota 舍入）"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			detail := buildUsageLogPricing(usdUsageLog(t, tc.quota, tc.fields))
			require.NotNil(t, detail)
			for _, price := range tc.catalog {
				require.Contains(t, detail.Catalog, price)
			}
			require.Contains(t, detail.Process, tc.process)
			require.Contains(t, detail.Process, "实际扣费：¥"+logQuotaYuan(tc.quota).String())
		})
	}
}

func TestUsageLogPricingDoesNotInventHistoricalFXOrForceAnEquation(t *testing.T) {
	legacy := buildUsageLogPricing(usdUsageLog(t, 13, map[string]any{"model_source_rate": nil}))
	require.Empty(t, legacy.Catalog)
	require.Empty(t, legacy.Rate)
	require.Contains(t, strings.Join(legacy.Process, "\n"), "不回算")
	require.Contains(t, legacy.Process, "实际扣费：¥0.000000013")
	mismatch := buildUsageLogPricing(usdUsageLog(t, 13, nil))
	require.Contains(t, mismatch.Process, "此笔日志无法仅凭已记录明细复算扣费。")
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
		fields := make(map[string]string)
		for i, header := range rows[0] {
			fields[header] = rows[1][i]
		}
		require.Equal(t, page["currency"], fields["目录价币种"])
		require.Equal(t, page["rate"], fields["本次模型汇率"])
		require.Equal(t, page["tool_rate"], fields["本次工具汇率"])
		for header, key := range map[string]string{"目录价": "catalog", "计费过程": "process"} {
			var lines []any
			for _, line := range strings.Split(fields[header], "\n") {
				lines = append(lines, line)
			}
			require.Equal(t, page[key], lines)
		}
		require.Equal(t, "0.068000000", fields["费用(CNY)"])
	}
}
