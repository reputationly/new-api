package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/require"
)

// seedPricingUserTier 在 seedPricingRatios 的三层之上再铺 Layer 3。
func seedPricingUserTier(t *testing.T, groupRatio, groupGroupRatio, groupModelRatio, userTier string) {
	t.Helper()
	seedPricingRatios(t, groupRatio, groupGroupRatio, groupModelRatio)
	require.NoError(t, ratio_setting.UpdateUserGroupModelRatioByJSONString(userTier))
	t.Cleanup(func() {
		_ = ratio_setting.UpdateUserGroupModelRatioByJSONString(`{}`)
	})
}

// effectiveRatio 复刻前端那一行查表逻辑：
//
//	groupModelRatio[g]?.[m] ?? groupRatio[g]
//
// 三个主题都只做这一步、不含任何解析。用它来断言「显示价 == 实扣价」，
// 等价于断言后端下发的两个 map 对任意模型都是自洽的。
func effectiveRatio(groupModelRatio map[string]map[string]float64, groupRatio map[string]float64, g, m string) float64 {
	if inner, ok := groupModelRatio[g]; ok {
		if v, ok := inner[m]; ok {
			return v
		}
	}
	return groupRatio[g]
}

// applyUserFallback 复刻 GetPricing 里把 Layer 3 的 "*" 兜底折进 group_ratio 的那步。
func applyUserFallback(userGroup string, groupRatio map[string]float64) map[string]float64 {
	fallback := ratio_setting.GetUserGroupFallbackRatio(userGroup)
	out := make(map[string]float64, len(groupRatio))
	for g, v := range groupRatio {
		if fallback != 1 {
			v *= fallback
		}
		out[g] = v
	}
	return out
}

// TestPricingUserTier_DisplayMatchesCharge 是 P0 的核心验收：**模型广场显示价必须
// 等于实际扣费**，对每一个模型都成立。
//
// 前端只做一次查表（effectiveRatio），后端计费走 ResolveGroupRatio。两者对不上就是
// 用户可见的错误，而且方向通常是「显示价偏高、实扣正确」——用户不会投诉，只会默默
// 觉得贵，没有任何告警会响。
//
// 覆盖三种模型：命中 Layer 3 具体规则的、只有 "*" 兜底的、以及 Layer 2 与 Layer 3
// 同时命中的。
func TestPricingUserTier_DisplayMatchesCharge(t *testing.T) {
	seedPricingUserTier(t,
		`{"default":1,"premium":1.5}`,
		`{}`,
		`{"premium":{"GLM-5":{"mode":"multiply","value":0.5}}}`,
		`{"vip":{"*":0.9,"Kimi-K3":0.6}}`,
	)

	models := []string{"GLM-5", "Kimi-K3", "Qwen3.8-Max"}
	baseRatio := map[string]float64{"default": 1, "premium": 1.5}

	groupModelRatio := resolveGroupModelRatio("vip", baseRatio, pricingOf(models...))
	groupRatio := applyUserFallback("vip", baseRatio)

	for _, g := range []string{"default", "premium"} {
		for _, m := range models {
			t.Run(g+"/"+m, func(t *testing.T) {
				displayed := effectiveRatio(groupModelRatio, groupRatio, g, m)
				charged := ratio_setting.ResolveGroupRatio("vip", g, m).Final
				require.InDelta(t, charged, displayed, 1e-9,
					"分组 %s 模型 %s：显示价与实扣价不符", g, m)
			})
		}
	}
}

// TestPricingUserTier_OnlyUserRulesConfigured 锁死 §8.0 那条跳过条件。
//
// 若 resolveGroupModelRatio 仍只看 GroupModelRatio 就 return，「只配了用户档折扣」
// 的站点拿到的是空表，前端全部 fallback 到 group_ratio。此时 "*" 兜底虽然折进了
// group_ratio 而侥幸正确，但**逐模型的用户档规则会整个丢失**——下面 Kimi-K3 的
// 断言就是防这个的。
func TestPricingUserTier_OnlyUserRulesConfigured(t *testing.T) {
	seedPricingUserTier(t,
		`{"default":1}`,
		`{}`,
		`{}`, // Layer 2 完全没配
		`{"vip":{"*":0.9,"Kimi-K3":0.6}}`,
	)

	baseRatio := map[string]float64{"default": 1}
	got := resolveGroupModelRatio("vip", baseRatio, pricingOf("Kimi-K3", "GLM-5"))

	require.InDelta(t, 0.6, got["default"]["Kimi-K3"], 1e-9,
		"逐模型的用户档规则必须进终值表")

	groupRatio := applyUserFallback("vip", baseRatio)
	require.InDelta(t, 0.9, effectiveRatio(got, groupRatio, "default", "GLM-5"), 1e-9,
		"未命中具体规则的模型走 group_ratio，应含 * 兜底")
}

// TestPricingUserTier_WildcardFallbackStaysSparse "*" 兜底不得把全表展开。
//
// 它是模型无关的，已经折进 group_ratio；若也算作命中，配一条 "*" 就会让三位数的
// 模型全部进 group_model_ratio，响应体积暴涨而每一项都等于 fallback 值。
func TestPricingUserTier_WildcardFallbackStaysSparse(t *testing.T) {
	seedPricingUserTier(t, `{"default":1}`, `{}`, `{}`, `{"vip":{"*":0.9}}`)

	got := resolveGroupModelRatio("vip",
		map[string]float64{"default": 1},
		pricingOf("GLM-5", "Kimi-K3", "Qwen3.8-Max"))

	require.Empty(t, got, "只有 \"*\" 兜底时终值表应为空")
}

// TestPricingUserTier_ScopedToUserGroup 其他用户档不受影响——Layer 3 按用户档索引，
// 不是全站生效。
func TestPricingUserTier_ScopedToUserGroup(t *testing.T) {
	seedPricingUserTier(t, `{"default":1}`, `{}`, `{}`, `{"vip":{"Kimi-K3":0.6}}`)

	baseRatio := map[string]float64{"default": 1}

	got := resolveGroupModelRatio("default", baseRatio, pricingOf("Kimi-K3"))
	require.Empty(t, got["default"], "default 档没有折扣，不该出现在终值表里")

	groupRatio := applyUserFallback("default", baseRatio)
	require.InDelta(t, 1.0, effectiveRatio(got, groupRatio, "default", "Kimi-K3"), 1e-9)
}

// TestPricingUserTier_EmptyConfigUnchanged Layer 3 未配置时，下发内容与改造前逐位相同。
func TestPricingUserTier_EmptyConfigUnchanged(t *testing.T) {
	seedPricingUserTier(t,
		`{"default":1,"premium":1.5}`,
		`{}`,
		`{"premium":{"GLM-5":{"mode":"multiply","value":0.5}}}`,
		`{}`,
	)

	baseRatio := map[string]float64{"default": 1, "premium": 1.5}
	got := resolveGroupModelRatio("vip", baseRatio, pricingOf("GLM-5", "Kimi-K3"))

	require.InDelta(t, 0.75, got["premium"]["GLM-5"], 1e-9)
	require.NotContains(t, got["premium"], "Kimi-K3")

	groupRatio := applyUserFallback("vip", baseRatio)
	require.InDelta(t, 1.0, groupRatio["default"], 1e-9)
	require.InDelta(t, 1.5, groupRatio["premium"], 1e-9)
}
