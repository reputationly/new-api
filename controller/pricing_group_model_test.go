package controller

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/require"
)

func seedPricingRatios(t *testing.T, groupRatio, groupGroupRatio, groupModelRatio string) {
	t.Helper()
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(groupRatio))
	require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(groupGroupRatio))
	require.NoError(t, ratio_setting.UpdateGroupModelRatioByJSONString(groupModelRatio))
	t.Cleanup(func() {
		_ = ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`)
		_ = ratio_setting.UpdateGroupGroupRatioByJSONString(`{}`)
		_ = ratio_setting.UpdateGroupModelRatioByJSONString(`{}`)
	})
}

// resolveRatio 是 resolveGroupModelRatio 的测试便捷版。本文件的用例都不配时段规则，
// 只关心终值表那一半返回值；时段那一半由 TestResolveGroupModelRatio_TimeRule* 覆盖。
func resolveRatio(userGroup string, groupRatio map[string]float64, pricing []model.Pricing) map[string]map[string]float64 {
	out, _ := resolveGroupModelRatio(userGroup, groupRatio, pricing, time.Now())
	return out
}

func pricingOf(names ...string) []model.Pricing {
	out := make([]model.Pricing, 0, len(names))
	for _, n := range names {
		out = append(out, model.Pricing{ModelName: n})
	}
	return out
}

// TestResolveGroupModelRatio_ExpandsWildcards 通配必须在后端展开成具体模型名。
// 下发 "wan2.2-*" 给前端，就意味着 classic / default / mobile 各写一遍匹配，
// 也就是三份可能算错的价。
func TestResolveGroupModelRatio_ExpandsWildcards(t *testing.T) {
	seedPricingRatios(t,
		`{"default":1,"premium":1.5}`,
		`{}`,
		`{"premium":{"wan2.2-*":{"mode":"multiply","value":0.8}}}`,
	)

	got := resolveRatio("default",
		map[string]float64{"default": 1, "premium": 1.5},
		pricingOf("wan2.2-t2v", "wan2.2-i2v", "GLM-5"))

	require.InDelta(t, 1.2, got["premium"]["wan2.2-t2v"], 1e-9)
	require.InDelta(t, 1.2, got["premium"]["wan2.2-i2v"], 1e-9)

	_, hasUnmatched := got["premium"]["GLM-5"]
	require.False(t, hasUnmatched, "未命中规则的模型不该出现，前端要退回 group_ratio")
	require.NotContains(t, got["premium"], "wan2.2-*", "通配模式串不能下发给前端")
	require.NotContains(t, got, "default", "没配规则的分组不该出现")
}

// TestResolveGroupModelRatio_CarriesIdentityDiscount 下发的必须是**终值**：
// 三层都算完。只算 Layer 2 的话，vip 用户在模型广场看到的价会比实际扣费高。
func TestResolveGroupModelRatio_CarriesIdentityDiscount(t *testing.T) {
	seedPricingRatios(t,
		`{"premium":1.5}`,
		`{"vip":{"premium":0.7}}`,
		`{"premium":{"GLM-5":{"mode":"multiply","value":0.5}}}`,
	)
	groupRatio := map[string]float64{"premium": 1.5}
	pricing := pricingOf("GLM-5")

	vip := resolveRatio("vip", groupRatio, pricing)
	require.InDelta(t, 0.35, vip["premium"]["GLM-5"], 1e-9, "0.7 × 0.5")

	plain := resolveRatio("default", groupRatio, pricing)
	require.InDelta(t, 0.75, plain["premium"]["GLM-5"], 1e-9, "1.5 × 0.5")
}

// TestResolveGroupModelRatio_EmptyWhenUnconfigured 未配置时返回空 map，
// 前端一路走原来的 group_ratio 分支——这是 P1 能安全上线的依据。
func TestResolveGroupModelRatio_EmptyWhenUnconfigured(t *testing.T) {
	seedPricingRatios(t, `{"premium":1.5}`, `{}`, `{}`)

	got := resolveRatio("default",
		map[string]float64{"premium": 1.5}, pricingOf("GLM-5"))
	require.Empty(t, got)
}

// TestResolveGroupModelRatio_SkipsInvisibleGroups groupRatio 传进来时已按用户可用
// 分组裁剪过，展开结果不能把用户无权的分组的价泄回去。
func TestResolveGroupModelRatio_SkipsInvisibleGroups(t *testing.T) {
	seedPricingRatios(t,
		`{"default":1,"premium":1.5}`,
		`{}`,
		`{"premium":{"GLM-5":{"mode":"override","value":2.2}}}`,
	)

	got := resolveRatio("default",
		map[string]float64{"default": 1}, // premium 不在用户可见范围内
		pricingOf("GLM-5"))
	require.Empty(t, got)
}

// seedTimeRules 铺设时段配置并在用例结束后清空。
func seedTimeRules(t *testing.T, jsonStr string) {
	t.Helper()
	require.NoError(t, ratio_setting.UpdateGroupTimeRatioByJSONString(jsonStr))
	t.Cleanup(func() {
		_ = ratio_setting.UpdateGroupTimeRatioByJSONString(`{}`)
	})
}

func atShanghai(hour int) time.Time {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		loc = time.FixedZone("CST", 8*3600)
	}
	return time.Date(2026, 9, 14, hour, 0, 0, 0, loc)
}

// TestResolveGroupModelRatio_TimeRuleEntersSparseTable 是这次改造里最容易漏的一条：
// 只配了时段折扣、没配 Layer 2/3 的模型，也必须进稀疏表。
//
// 漏掉的话前端会走 `groupModelRatio[g]?.[m] ?? groupRatio[g]` 的 fallback 分支拿到
// 原价，而后端实扣已经打折——显示价与实扣不一致里最难发现的那种（设计文档 §8.0）。
func TestResolveGroupModelRatio_TimeRuleEntersSparseTable(t *testing.T) {
	seedPricingRatios(t, `{"default":1}`, `{}`, `{}`) // Layer 2/3 全空
	seedTimeRules(t, `{
		"windows": {"night": {"label":"深夜档","start":"00:00","end":"08:00"}},
		"rules":   {"default": {"wan2.2-*": [{"window":"night","value":0.7}]}}
	}`)
	groupRatio := map[string]float64{"default": 1}
	pricing := pricingOf("wan2.2-t2v", "GLM-5")

	t.Run("空闲时段内下发打折终值", func(t *testing.T) {
		got, view := resolveGroupModelRatio("default", groupRatio, pricing, atShanghai(2))
		require.InDelta(t, 0.7, got["default"]["wan2.2-t2v"], 1e-9)
		require.NotContains(t, got["default"], "GLM-5", "没配规则的模型不该进表")

		v := view["default"]["wan2.2-t2v"]
		require.True(t, v.Active)
		require.Equal(t, "深夜档", v.Label)
		// 下发的时段终值必须与列表价读的那个终值是同一个数
		require.InDelta(t, got["default"]["wan2.2-t2v"], v.Windows[0].Ratio, 1e-9)
		require.InDelta(t, 0.7, v.BestRatio, 1e-9)
		require.NotEmpty(t, v.Until, "前端靠它渲染「至 08:00」")
	})

	t.Run("高峰时段仍要进表且为原价", func(t *testing.T) {
		got, view := resolveGroupModelRatio("default", groupRatio, pricing, atShanghai(10))
		require.Contains(t, got["default"], "wan2.2-t2v",
			"高峰时段也必须下发，否则空闲时段一到前端仍走 fallback 显示原价而实扣已打折")
		require.InDelta(t, 1.0, got["default"]["wan2.2-t2v"], 1e-9)

		v := view["default"]["wan2.2-t2v"]
		require.False(t, v.Active)
		require.InDelta(t, 0.7, v.BestRatio, 1e-9, "高峰时段靠 best_ratio 渲染「空闲时段 7 折」")
	})
}

// TestResolveGroupModelRatio_TimeRuleReplacesModelRule 时段规则取代模型折扣，
// 下发的是取代后的终值——前端只查表，不做任何二次计算。
func TestResolveGroupModelRatio_TimeRuleReplacesModelRule(t *testing.T) {
	seedPricingRatios(t, `{"default":1,"premium":1.5}`, `{}`,
		`{"premium":{"wan2.2-*":{"mode":"multiply","value":0.8}}}`)
	seedTimeRules(t, `{
		"windows": {"night": {"start":"00:00","end":"08:00"}},
		"rules":   {"premium": {"wan2.2-*": [{"window":"night","value":0.5}]}}
	}`)

	got, _ := resolveGroupModelRatio("default",
		map[string]float64{"default": 1, "premium": 1.5},
		pricingOf("wan2.2-t2v"), atShanghai(2))
	require.InDelta(t, 0.75, got["premium"]["wan2.2-t2v"], 1e-9, "1.5 × 0.5，模型折扣被取代")
}
