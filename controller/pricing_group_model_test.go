package controller

import (
	"testing"

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

	got := resolveGroupModelRatio("default",
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

	vip := resolveGroupModelRatio("vip", groupRatio, pricing)
	require.InDelta(t, 0.35, vip["premium"]["GLM-5"], 1e-9, "0.7 × 0.5")

	plain := resolveGroupModelRatio("default", groupRatio, pricing)
	require.InDelta(t, 0.75, plain["premium"]["GLM-5"], 1e-9, "1.5 × 0.5")
}

// TestResolveGroupModelRatio_EmptyWhenUnconfigured 未配置时返回空 map，
// 前端一路走原来的 group_ratio 分支——这是 P1 能安全上线的依据。
func TestResolveGroupModelRatio_EmptyWhenUnconfigured(t *testing.T) {
	seedPricingRatios(t, `{"premium":1.5}`, `{}`, `{}`)

	got := resolveGroupModelRatio("default",
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

	got := resolveGroupModelRatio("default",
		map[string]float64{"default": 1}, // premium 不在用户可见范围内
		pricingOf("GLM-5"))
	require.Empty(t, got)
}
