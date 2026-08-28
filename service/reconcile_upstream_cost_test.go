package service

import (
	"math"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

// expectedCNY 按 upstreamAmountCNY 的换算口径算出期望值，避免把汇率常量硬编码进
// 断言——汇率是可配置的，写死会让这组测试在改汇率时集体假红。
func expectedCNY(upstreamQuota float64) float64 {
	return upstreamQuota / common.QuotaPerUnit * operation_setting.USDExchangeRate
}

// TestUpstreamAmountCNY_PrefersCostQuota 新日志走正算路径，不再依赖 group_ratio 反推。
//
// 这里刻意让两条路径给出不同答案：group_ratio=1.5 反推出 1000，而落库的 cost_quota
// 是 600。若实现仍先看 group_ratio，结果会是 1000——对账每个月都会多算 66%，
// 而且因为数量级正常，只有拿供应商账单逐行核才发现得了。
func TestUpstreamAmountCNY_PrefersCostQuota(t *testing.T) {
	other := map[string]interface{}{
		"group_ratio": 1.5,
		"cost_quota":  600.0,
		"cost_ratio":  0.6,
	}

	got := upstreamAmountCNY(1500, other)

	require.InDelta(t, expectedCNY(600), got, 1e-6)
	require.Greater(t, math.Abs(got-expectedCNY(1000)), 1e-6,
		"不得再走 quota ÷ group_ratio 的反推")
}

// TestUpstreamAmountCNY_FallsBackForLegacyLogs 存量日志没有 cost_quota，必须逐位
// 保持改造前的行为——否则历史区间的对账结果会在升级瞬间集体漂移，而运营手里的
// 供应商账单是不变的。
func TestUpstreamAmountCNY_FallsBackForLegacyLogs(t *testing.T) {
	t.Run("有 group_ratio", func(t *testing.T) {
		other := map[string]interface{}{"group_ratio": 1.5}
		require.InDelta(t, expectedCNY(1000), upstreamAmountCNY(1500, other), 1e-6)
	})

	t.Run("无 group_ratio 视为 1", func(t *testing.T) {
		require.InDelta(t, expectedCNY(1500), upstreamAmountCNY(1500, map[string]interface{}{}), 1e-6)
	})

	t.Run("group_ratio 为 0 视为无倍率", func(t *testing.T) {
		other := map[string]interface{}{"group_ratio": 0.0}
		require.InDelta(t, expectedCNY(1500), upstreamAmountCNY(1500, other), 1e-6)
	})
}

// TestUpstreamAmountCNY_ZeroCostQuotaFallsBack cost_quota 为 0（成本比配成 0 的
// 自建模型）时回退到反推，而不是直接返回 0。
//
// 这两种情形在数值上撞车了：`getFloat` 拿不到字段也返回 0。选择回退是保守的一侧——
// 自建模型本就不参与供应商对账，多算一点不会让账对不上；反过来若直接采信 0，
// 一旦是「字段缺失」被误当成「成本为零」，账面上就是供应商白送。
func TestUpstreamAmountCNY_ZeroCostQuotaFallsBack(t *testing.T) {
	other := map[string]interface{}{
		"group_ratio": 1.0,
		"cost_quota":  0.0,
		"cost_ratio":  0.0,
	}
	require.InDelta(t, expectedCNY(1000), upstreamAmountCNY(1000, other), 1e-6)
}

func TestUpstreamAmountCNY_ZeroQuota(t *testing.T) {
	require.Equal(t, 0.0, upstreamAmountCNY(0, map[string]interface{}{"cost_quota": 600.0}))
}
