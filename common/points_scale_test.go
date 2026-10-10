package common

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// B5：积分 / 算力点的换算率必须按当前 QPU 与汇率实时推算，不能在包初始化时固定。
//
// 换算率 = QuotaPerUnit / (汇率 × 100)：汇率 7.3 时 QPU 500000 → 684.93（与改造前一致）、
// QPU 7300000 → 10000。两种单位下 1 元都应该是 100 积分 / 100 算力点。
func TestQuotaPerPointFollowsRuntimeConfig(t *testing.T) {
	originalQPU := QuotaPerUnit
	originalRate := USDExchangeRateFunc
	originalPoint := QuotaPerPointFunc
	originalCompute := QuotaPerComputePointFunc
	t.Cleanup(func() {
		QuotaPerUnit = originalQPU
		USDExchangeRateFunc = originalRate
		QuotaPerPointFunc = originalPoint
		QuotaPerComputePointFunc = originalCompute
	})

	USDExchangeRateFunc = func() float64 { return 7.3 }
	QuotaPerPointFunc = nil // 配置未设置 → 自动
	QuotaPerComputePointFunc = nil

	QuotaPerUnit = 500000
	require.InDelta(t, 500000.0/730.0, GetQuotaPerPoint(), 1e-9, "与改造前的固定默认值一致")
	require.InDelta(t, 500000.0/730.0, GetQuotaPerComputePoint(), 1e-9)
	require.Equal(t, 100, YuanToComputePoints(1))

	QuotaPerUnit = 7300000
	require.Equal(t, 10000.0, GetQuotaPerPoint())
	require.Equal(t, 10000.0, GetQuotaPerComputePoint())
	require.Equal(t, 100, YuanToComputePoints(1), "切到新单位后 1 元仍应是 100 点")

	// 往返仍然精确：切单位不会让「配 N 点显示 N-1 点」
	require.Equal(t, 50000, QuotaToComputePoints(ComputePointsToQuota(50000)))
	require.Equal(t, 100, QuotaToPoints(PointsToQuota(100)))
	require.Equal(t, 100, QuotaToPointsCeil(100*10000))
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

	QuotaPerUnit = 7300000
	QuotaPerPointFunc = func() float64 { return 5000 }        // 1 元 = 200 积分
	QuotaPerComputePointFunc = func() float64 { return 1234 } // 自定义

	require.Equal(t, 5000.0, GetQuotaPerPoint())
	require.Equal(t, 1234.0, GetQuotaPerComputePoint())

	// 0 = 自动，退回实时推算
	QuotaPerPointFunc = func() float64 { return 0 }
	QuotaPerComputePointFunc = func() float64 { return 0 }
	require.Equal(t, 10000.0, GetQuotaPerPoint())
	require.Equal(t, 10000.0, GetQuotaPerComputePoint())
}

// 配置值路径（生产迁移后走的就是这条）：换算率固定为配置值，与汇率、QPU 都无关。
// 「同一笔 quota 消耗的积分数 / 算力点数不随汇率变化」这条业务性质必须在这里成立——
// 自动路径按定义跟着汇率走，所以那份保证由这条用例承担。
func TestConfiguredPointRateIsIndependentOfExchangeRate(t *testing.T) {
	originalRate := USDExchangeRateFunc
	originalQPU := QuotaPerUnit
	originalPoint := QuotaPerPointFunc
	originalCompute := QuotaPerComputePointFunc
	t.Cleanup(func() {
		USDExchangeRateFunc = originalRate
		QuotaPerUnit = originalQPU
		QuotaPerPointFunc = originalPoint
		QuotaPerComputePointFunc = originalCompute
	})

	QuotaPerUnit = 7300000
	QuotaPerPointFunc = func() float64 { return 10000 }
	QuotaPerComputePointFunc = func() float64 { return 10000 }

	USDExchangeRateFunc = func() float64 { return 7.3 }
	pointsAt73 := QuotaToPoints(1_000_000)
	computeAt73 := QuotaToComputePoints(1_000_000)
	require.Equal(t, 100, pointsAt73)
	require.Equal(t, 100, computeAt73)

	USDExchangeRateFunc = func() float64 { return 8.5 }
	require.Equal(t, pointsAt73, QuotaToPoints(1_000_000),
		"配置值生效时，同一笔 quota 消耗的积分数不得随汇率变化")
	require.Equal(t, computeAt73, QuotaToComputePoints(1_000_000),
		"配置值生效时，同一笔 quota 消耗的算力点数不得随汇率变化")
}
