package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// withCostCache 直接铺设成本缓存，绕开 DB——本组测试验证的是换算与守卫逻辑，
// 不是表的读写。
func withCostCache(t *testing.T, cache map[int]map[string]float64) {
	t.Helper()
	channelModelCostLock.Lock()
	prev := channelModelCostCache
	channelModelCostCache = cache
	channelModelCostLock.Unlock()
	t.Cleanup(func() {
		channelModelCostLock.Lock()
		channelModelCostCache = prev
		channelModelCostLock.Unlock()
	})
}

// TestAppendUpstreamCost_Conversion 成本换算：cost = quota × costRatio / groupRatio。
//
// 用一个能手算的例子钉死方向：quota=1500 是「base 1000 按 1.5 倍卖」的结果，
// 成本比 0.6 意味着上游收我们 600。若把除法写成乘法（cost = quota × ratio × group），
// 会得到 1350——数量级接近、不会崩，只会让每月对账悄悄多算一倍多。
func TestAppendUpstreamCost_Conversion(t *testing.T) {
	withCostCache(t, map[int]map[string]float64{
		7: {"GLM-5": 0.6},
	})

	other := map[string]interface{}{"group_ratio": 1.5}
	appendUpstreamCost(other, 1500, 7, "GLM-5")

	require.InDelta(t, 600.0, other["cost_quota"], 1e-9)
	require.InDelta(t, 0.6, other["cost_ratio"], 1e-9)
}

// TestAppendUpstreamCost_SkipsWhenUnresolvable 三种写不出成本的情形必须**不写字段**，
// 而不是写 0。
//
// 对账端靠「字段在不在」决定走新路径还是回退到存量的 quota ÷ group_ratio
// （service/reconcile_helpers.go）。写一个 0 进去，对账会把它当成「上游成本为零」
// 直接采信——账面上表现为供应商白送，没有任何告警。
func TestAppendUpstreamCost_SkipsWhenUnresolvable(t *testing.T) {
	withCostCache(t, map[int]map[string]float64{
		7: {"GLM-5": 0.6},
	})

	t.Run("渠道未配成本", func(t *testing.T) {
		other := map[string]interface{}{"group_ratio": 1.5}
		appendUpstreamCost(other, 1500, 99, "GLM-5")
		require.NotContains(t, other, "cost_quota")
		require.NotContains(t, other, "cost_ratio")
	})

	t.Run("该模型未配成本", func(t *testing.T) {
		other := map[string]interface{}{"group_ratio": 1.5}
		appendUpstreamCost(other, 1500, 7, "Kimi-K3")
		require.NotContains(t, other, "cost_quota")
	})

	t.Run("免费体验区 group_ratio=0 无法反推 base", func(t *testing.T) {
		other := map[string]interface{}{"group_ratio": 0.0}
		appendUpstreamCost(other, 1500, 7, "GLM-5")
		require.NotContains(t, other, "cost_quota",
			"group_ratio 为 0 时 base 无法反推，必须留给对账端回退")
	})

	t.Run("other 缺 group_ratio", func(t *testing.T) {
		other := map[string]interface{}{}
		appendUpstreamCost(other, 1500, 7, "GLM-5")
		require.NotContains(t, other, "cost_quota")
	})

	t.Run("quota 为 0", func(t *testing.T) {
		other := map[string]interface{}{"group_ratio": 1.5}
		appendUpstreamCost(other, 0, 7, "GLM-5")
		require.NotContains(t, other, "cost_quota")
	})

	t.Run("nil other 不 panic", func(t *testing.T) {
		require.NotPanics(t, func() {
			appendUpstreamCost(nil, 1500, 7, "GLM-5")
		})
	})
}

// TestAppendUpstreamCost_ZeroCostRatioIsRecorded 成本比显式配成 0（自建算力按零成本
// 记账）与「没配过」必须区分：前者要写进日志，后者不写。
func TestAppendUpstreamCost_ZeroCostRatioIsRecorded(t *testing.T) {
	withCostCache(t, map[int]map[string]float64{
		7: {"glm-5.2-w4a8": 0},
	})

	other := map[string]interface{}{"group_ratio": 1.0}
	appendUpstreamCost(other, 1000, 7, "glm-5.2-w4a8")

	require.Contains(t, other, "cost_ratio", "显式配成 0 与未配置必须能区分")
	require.InDelta(t, 0.0, other["cost_quota"], 1e-9)
}

// TestGetChannelModelCostRatio 「配了 0」与「没配」在返回值上必须可区分。
func TestGetChannelModelCostRatio(t *testing.T) {
	withCostCache(t, map[int]map[string]float64{
		7: {"GLM-5": 0.6, "free-model": 0},
	})

	t.Run("已配置", func(t *testing.T) {
		ratio, ok := GetChannelModelCostRatio(7, "GLM-5")
		require.True(t, ok)
		require.InDelta(t, 0.6, ratio, 1e-9)
	})

	t.Run("配成 0 仍算已配置", func(t *testing.T) {
		ratio, ok := GetChannelModelCostRatio(7, "free-model")
		require.True(t, ok, "0 是合法成本比，不是未配置")
		require.InDelta(t, 0.0, ratio, 1e-9)
	})

	t.Run("渠道不存在", func(t *testing.T) {
		_, ok := GetChannelModelCostRatio(99, "GLM-5")
		require.False(t, ok)
	})

	t.Run("模型不存在", func(t *testing.T) {
		_, ok := GetChannelModelCostRatio(7, "Kimi-K3")
		require.False(t, ok)
	})
}
