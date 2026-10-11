package service

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/stretchr/testify/require"
)

// 人民币口径的计费样例回归（设计文档 §2.3、§2.4、§7.1）。
// 倍率是 ¥/百万 tokens，所以 quota = tokens × 倍率 × 100。

// 低价：¥0.01/百万 tokens 时 1 token 恰好 1 quota（G1 的目标场景）。
//
//	1 / 50 / 149 / 150 tokens → 1 / 50 / 149 / 150 quota（§7.1 的低价一行）。
func TestTextQuotaLowPriceExactTokens(t *testing.T) {
	ctx := newTextQuotaCtx(t)

	cases := []struct {
		tokens int
		want   int
	}{
		{1, 1},     // E2
		{50, 50},   //
		{149, 149}, // E3
		{150, 150}, //
	}
	for _, c := range cases {
		relayInfo := textRelayInfo(types.PriceData{
			ModelRatio: 0.01, CompletionRatio: 1, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1},
		})
		summary := calculateTextQuotaSummary(ctx, relayInfo, &dto.Usage{PromptTokens: c.tokens})
		require.Equalf(t, c.want, summary.Quota, "¥0.01/百万、%d tokens", c.tokens)
	}
}

// 最低扣费（§2.4）：倍率非 0、有 token，但算出 0 时扣 1 quota = ¥0.00000001。
//
// ¥0.003/百万 → 0.3 → round 0 → 1（E4）；¥0.001/百万 → 0.1 → 0 → 1；
// ¥0.005/百万 → 0.5 → decimal.Round（half away from zero）→ 1。
func TestTextQuotaMinimumChargeAndHalfRounding(t *testing.T) {
	ctx := newTextQuotaCtx(t)

	cases := []struct {
		name      string
		ratio     float64
		tokens    int
		want      int
		reasoning string
	}{
		{"E4 低于半 quota", 0.003, 1, 1, "1 × 0.003 × 100 = 0.3 → 0 → 最低扣费 1"},
		{"更低价", 0.001, 1, 1, "0.1 → 0 → 最低扣费 1"},
		{".5 向上取整", 0.005, 1, 1, "0.5 → decimal.Round half-away-from-zero → 1"},
		{"零 token 不扣费", 0.003, 0, 0, "无 token 时不套用最低扣费"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			relayInfo := textRelayInfo(types.PriceData{
				ModelRatio: c.ratio, CompletionRatio: 1, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1},
			})
			summary := calculateTextQuotaSummary(ctx, relayInfo, &dto.Usage{PromptTokens: c.tokens})
			require.Equal(t, c.want, summary.Quota, c.reasoning)
		})
	}
}

// 溢出：1e7 tokens × ¥1000/百万 = ¥10,000 = 10¹² quota，int64 无压力。
func TestTextQuotaOverflowBoundary(t *testing.T) {
	ctx := newTextQuotaCtx(t)
	relayInfo := textRelayInfo(types.PriceData{
		ModelRatio: 1000, CompletionRatio: 1, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1},
	})

	summary := calculateTextQuotaSummary(ctx, relayInfo, &dto.Usage{PromptTokens: 10_000_000})
	require.Equal(t, 1_000_000_000_000, summary.Quota, "1e7 × ¥1000/百万 = 1e12 quota")
	require.Equal(t, 10_000.0, float64(summary.Quota)/float64(common.QuotaPerYuan), "折算回人民币应是 ¥10,000")
}

// 音频倍率路径：tokens × 倍率 × 100，与 QPU/汇率无关。
func TestAudioQuotaRatioPathExact(t *testing.T) {
	info := QuotaInfo{
		InputDetails:  TokenDetails{TextTokens: 10_000},
		OutputDetails: TokenDetails{TextTokens: 0},
		ModelName:     "pricing-cny-audio-model",
		ModelRatio:    2, // ¥2/百万 tokens
		GroupRatio:    0.5,
	}
	require.Equal(t, 1_000_000, calculateAudioQuota(info), "10000 × 2 × 100 × 0.5")
}

// E6：联网搜索 ¥73/千次（原 $10），调用 3 次，分组 1。
//
//	73 / 1000 × 3 × 10⁸ = 21,900,000 quota = ¥0.219
func TestToolCallSurchargeE6(t *testing.T) {
	// 前置：o1 不匹配任何模型前缀覆盖，取默认价 ¥73/千次
	require.Equal(t, 73.0, operation_setting.GetToolPriceForModel("web_search_preview", "o1"),
		"默认工具价必须已是人民币（B3）")

	res := ComputeToolCallQuota(ToolCallUsage{
		ModelName:         "o1",
		WebSearchCalls:    3,
		WebSearchToolName: "web_search_preview",
	}, 1)
	require.Equal(t, 21_900_000, res.TotalQuota)
	require.Len(t, res.Items, 1)
	require.Equal(t, 21_900_000, res.Items[0].Quota)
	require.InDelta(t, 0.219, float64(res.TotalQuota)/float64(common.QuotaPerYuan), 1e-12)
}

// 违规罚金：¥0.365/次 × 10⁸ × 分组。grok 默认罚金已是人民币（B3）。
func TestViolationFeeQuotaExact(t *testing.T) {
	// B3：代码里的美元常量必须已换成人民币（原 $0.05 × 7.3 = ¥0.365）
	require.InDelta(t, 0.365, model_setting.GetGrokSettings().ViolationDeductionAmount, 1e-12)

	require.Equal(t, 36_500_000, calcViolationFeeQuota(0.365, 1))
	require.Equal(t, 18_250_000, calcViolationFeeQuota(0.365, 0.5))
	require.Equal(t, 0, calcViolationFeeQuota(0, 1), "金额为 0 不收费")
	require.Equal(t, 0, calcViolationFeeQuota(0.365, 0), "免费分组不收费")
}

// 视频「按 token」矩阵：quota = tokens ÷ 10⁶ × 单价(¥/百万) × QuotaPerYuan × 分组。
//
//	216900 tokens × ¥46/百万 = ¥9.9774 = 997,740,000 quota
func TestVideoMatrixTokenModeExactQuota(t *testing.T) {
	truncate(t)
	seedUser(t, 9701, 10_000_000)

	task := makeVideoTask(t, 9701, 1, 0, &model.TaskVideoBilling{
		Mode: "token", UnitPrice: 46, Resolution: "720p",
	})
	task.PrivateData.BillingContext.GroupRatio = 1

	require.True(t, RecalculateTaskQuotaByVideoMatrix(context.Background(), task, 216_900, ""))
	require.Equal(t, 997_740_000, task.Quota)
	require.InDelta(t, 9.9774, float64(task.Quota)/float64(common.QuotaPerYuan), 1e-9)
}

// 渠道测试的倍率路径与价格路径（controller/channel_test_scale_test.go 有更细的覆盖），
// 这里再从 service 侧确认「按次价 × QuotaPerYuan」的边界值。
func TestPerCallPriceBoundaryValues(t *testing.T) {
	ctx := newTextQuotaCtx(t)
	usage := &dto.Usage{PromptTokens: 1, CompletionTokens: 1}

	for _, c := range []struct {
		price float64
		want  int
	}{
		{0.00000001, 1},   // ¥1e-8 = 恰好 1 quota
		{0.005, 500_000},  // 半分钱
		{0.2, 20_000_000}, // E5：按次生图
		{1000, 100_000_000_000},
	} {
		relayInfo := textRelayInfo(types.PriceData{
			UsePrice: true, ModelPrice: c.price,
			GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1},
		})
		require.Equal(t, c.want, calculateTextQuotaSummary(ctx, relayInfo, usage).Quota,
			"¥%v/次", c.price)
	}
}

// S1 护栏：默认价模型不能被判成「管理员已改价」。
// InitRatioSettings 与 GetDefaultModelRatioMap 用的是同一条换算，这里把它钉住。
func TestDefaultPriceModelsAreNotMarkedCustomized(t *testing.T) {
	before := ratio_setting.ModelRatio2JSONString()
	t.Cleanup(func() { _ = ratio_setting.UpdateModelRatioByJSONString(before) })
	ratio_setting.InitRatioSettings()

	for _, name := range []string{"gpt-4o", "gpt-4.1"} {
		ratio, ok, _ := ratio_setting.GetModelRatio(name)
		require.Truef(t, ok, "前置：%s 有默认倍率", name)
		require.Falsef(t, hasCustomModelRatio(name, ratio),
			"%s 的默认倍率与默认表不一致 → 定价页会到处显示「已修改」", name)
	}
}
