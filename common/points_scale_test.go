package common

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// B5（新口径）：积分 / 算力点的换算率固定为「1 元 = 100 点」，即 QuotaPerYuan / 100 = 10⁶，
// 不再由 QuotaPerUnit ÷ 汇率推算。
//
// 这条不变量必须在单位迁移后仍然成立：QPU 从 500000 变到 730000000，自动换算率恒为 10⁶。
func TestQuotaPerPointFollowsRuntimeConfig(t *testing.T) {
	originalQPU := QuotaPerUnit
	originalPoint := QuotaPerPointFunc
	originalCompute := QuotaPerComputePointFunc
	t.Cleanup(func() {
		QuotaPerUnit = originalQPU
		QuotaPerPointFunc = originalPoint
		QuotaPerComputePointFunc = originalCompute
	})

	QuotaPerPointFunc = nil // 配置未设置 → 自动
	QuotaPerComputePointFunc = nil

	for _, qpu := range []float64{500_000, 7_300_000, 730_000_000} {
		QuotaPerUnit = qpu
		require.Equal(t, 1_000_000.0, GetQuotaPerPoint(),
			"QuotaPerUnit=%v 时积分换算率应为 10⁶", qpu)
		require.Equal(t, 1_000_000.0, GetQuotaPerComputePoint(), "QuotaPerUnit=%v", qpu)
		require.Equal(t, 100, YuanToComputePoints(1),
			"QuotaPerUnit=%v 时 1 元仍应是 100 点", qpu)

		// 往返仍然精确：切单位不会让「配 N 点显示 N-1 点」
		require.Equal(t, 50000, QuotaToComputePoints(ComputePointsToQuota(50000)))
		require.Equal(t, 100, QuotaToPoints(PointsToQuota(100)))
		require.Equal(t, 100, QuotaToPointsCeil(100*1_000_000))
	}
}

// 配置值 > 0 时以配置值为准（面值可运营调整），0 / 未注入一律视为「自动」。
func TestGetQuotaPerPointPrefersConfiguredValue(t *testing.T) {
	originalQPU := QuotaPerUnit
	originalPoint := QuotaPerPointFunc
	originalCompute := QuotaPerComputePointFunc
	t.Cleanup(func() {
		QuotaPerUnit = originalQPU
		QuotaPerPointFunc = originalPoint
		QuotaPerComputePointFunc = originalCompute
	})

	QuotaPerUnit = 7_300_000
	QuotaPerPointFunc = func() float64 { return 5000 }        // 1 元 = 200 积分
	QuotaPerComputePointFunc = func() float64 { return 1234 } // 自定义

	require.Equal(t, 5000.0, GetQuotaPerPoint())
	require.Equal(t, 1234.0, GetQuotaPerComputePoint())

	// 0 = 自动，退回固定换算率 10⁶
	QuotaPerPointFunc = func() float64 { return 0 }
	QuotaPerComputePointFunc = func() float64 { return 0 }
	require.Equal(t, 1_000_000.0, GetQuotaPerPoint())
	require.Equal(t, 1_000_000.0, GetQuotaPerComputePoint())
}

// 配置值路径（生产迁移后走的就是这条）：换算率固定为配置值，与 QuotaPerUnit 无关。
// 「同一笔 quota 消耗的积分数 / 算力点数不随记账单位变化」这条业务性质必须在这里成立。
func TestConfiguredPointRateIsIndependentOfQuotaPerUnit(t *testing.T) {
	originalQPU := QuotaPerUnit
	originalPoint := QuotaPerPointFunc
	originalCompute := QuotaPerComputePointFunc
	t.Cleanup(func() {
		QuotaPerUnit = originalQPU
		QuotaPerPointFunc = originalPoint
		QuotaPerComputePointFunc = originalCompute
	})

	QuotaPerPointFunc = func() float64 { return 1_000_000 }
	QuotaPerComputePointFunc = func() float64 { return 1_000_000 }

	QuotaPerUnit = 7_300_000
	pointsBefore := QuotaToPoints(1_000_000)
	computeBefore := QuotaToComputePoints(1_000_000)
	require.Equal(t, 1, pointsBefore)
	require.Equal(t, 1, computeBefore)

	QuotaPerUnit = 730_000_000
	require.Equal(t, pointsBefore, QuotaToPoints(1_000_000),
		"配置值生效时，同一笔 quota 消耗的积分数不得随 QuotaPerUnit 变化")
	require.Equal(t, computeBefore, QuotaToComputePoints(1_000_000),
		"配置值生效时，同一笔 quota 消耗的算力点数不得随 QuotaPerUnit 变化")
}
