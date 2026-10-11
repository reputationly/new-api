package ratio_setting

import (
	"math"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/require"
)

// 金额守恒（设计文档 §7.1）：同一个模型、同一笔请求，
// 「V1 配置 + V1 公式」算出的人民币金额与「迁移后配置 + V2 公式」算出的金额
// 差值不超过 1 个 V1 quota（¥0.000001）。测试数据取 defaultModelRatio 的全部模型。
//
//	V1：配置是美元口径的原始倍率 r（1 = $2/百万 tokens）；倍率路径 quota/token = r × 14.6
//	    （14.6 = 2 × 7.3 = QuotaScale），1 quota = ¥1e-6 → 人民币 = r × 14.6 × 1e-6。
//	V2：迁移后配置是 cnyModelRatios(r) = round6(r × 14.6)（¥/百万 tokens）；
//	    quota/token = cny × 100，1 quota = ¥1e-8 → 人民币 = cny × 100 × 1e-8 = cny × 1e-6。
//
// 因此差值只来自 round6，上界 5e-7 < 1e-6。
func TestDefaultModelRatioAmountConservation(t *testing.T) {
	const v1QuotaCNY = 1e-6 // V1：1 quota = ¥0.000001

	require.NotEmpty(t, defaultModelRatio, "前置：默认倍率表必须非空")

	for name, raw := range defaultModelRatio {
		// V1 配置 + V1 公式（倍率路径 ×14.6），tokens = 1e6 时结果就是「元/百万 tokens」
		v1Quota := 1e6 * raw * 14.6
		v1CNY := v1Quota * v1QuotaCNY

		// 迁移后配置 + V2 公式
		migrated := cnyModelRatios(defaultModelRatio)[name]
		v2Quota := 1e6 * migrated * float64(common.QuotaPerMillionTokens)
		v2CNY := v2Quota / float64(common.QuotaPerYuan)

		diff := math.Abs(v1CNY - v2CNY)
		require.LessOrEqualf(t, diff, v1QuotaCNY,
			"模型 %s：V1 ¥%.9f vs V2 ¥%.9f，差 %.9f 超过 1 个 V1 quota", name, v1CNY, v2CNY, diff)
	}
}

// 默认倍率表的换算必须同时作用在 InitRatioSettings 与 GetDefaultModelRatioMap 上，
// 否则 hasCustomModelRatio 会把所有默认价模型判成「已被管理员改价」（评审 S1）。
func TestDefaultModelRatioMapMatchesInit(t *testing.T) {
	before := ModelRatio2JSONString()
	t.Cleanup(func() { _ = UpdateModelRatioByJSONString(before) })

	InitRatioSettings()
	checked := 0
	for name, want := range GetDefaultModelRatioMap() {
		if strings.HasSuffix(name, "*") {
			continue // 通配键本身不是可解析的模型名
		}
		got, ok, _ := GetModelRatio(name)
		require.Truef(t, ok, "模型 %s 应能查到倍率", name)
		require.Equalf(t, want, got, "模型 %s：配置里的默认值必须等于换算后的默认表", name)
		checked++
	}
	require.Positive(t, checked, "至少要检查到一些具体模型")

	// B3 完成标准：¥2/百万 tokens 的 gpt-4.1 = 14.6
	require.Equal(t, 14.6, GetDefaultModelRatioMap()["gpt-4.1"])
}

// 6 位小数的舍入误差上界（D6）：|round6(r×14.6) − r×14.6| ≤ 5e-7 ¥/百万 tokens。
func TestCnyModelRatiosRoundingBound(t *testing.T) {
	for name, raw := range defaultModelRatio {
		want := raw * 2 * USD2RMB
		got := cnyModelRatios(defaultModelRatio)[name]
		require.LessOrEqualf(t, math.Abs(want-got), 5e-7, "模型 %s 的舍入误差超界", name)
	}
}
