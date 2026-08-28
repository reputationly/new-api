package ratio_setting

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// seedUserTier 在 seedRatios 的三层之上再铺 Layer 3，并在用例结束后一并恢复。
func seedUserTier(t *testing.T, groupRatio, groupGroupRatio, groupModelRatio, userGroupModelRatio string) {
	t.Helper()
	seedRatios(t, groupRatio, groupGroupRatio, groupModelRatio)
	require.NoError(t, UpdateUserGroupModelRatioByJSONString(userGroupModelRatio))
	t.Cleanup(func() {
		_ = UpdateUserGroupModelRatioByJSONString(`{}`)
	})
}

// TestResolveGroupRatio_EmptyUserTierIsIdentical 是 P0 能安全上线的依据：
// UserGroupModelRatio 为空时，四层解析必须与加 Layer 3 之前逐位相同。
//
// 这条一旦红，说明 Layer 3 在未配置时也参与了运算——存量站点的所有账单会在
// 上线瞬间集体漂移，且没有任何配置能解释这个变化。
func TestResolveGroupRatio_EmptyUserTierIsIdentical(t *testing.T) {
	seedUserTier(t,
		`{"default":1,"premium":1.5}`,
		`{"vip":{"premium":0.7}}`,
		`{"premium":{"GLM-5":{"mode":"multiply","value":0.5}}}`,
		`{}`,
	)

	cases := []struct {
		name      string
		userGroup string
		using     string
		model     string
		want      float64
	}{
		{"仅基础倍率", "default", "premium", "Kimi-K3", 1.5},
		{"命中身份折扣", "vip", "premium", "Kimi-K3", 0.7},
		{"命中模型规则", "default", "premium", "GLM-5", 0.75},
		{"身份+模型叠加", "vip", "premium", "GLM-5", 0.35},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := ResolveGroupRatio(c.userGroup, c.using, c.model)
			require.InDelta(t, c.want, res.Final, 1e-9)
			require.Empty(t, res.UserRuleMatch, "Layer 3 未配置时不得命中")
			require.InDelta(t, res.Final, res.AfterModelRule, 1e-9,
				"Layer 3 未命中时 Final 必须等于 Layer 2 的结果")
		})
	}
}

// TestResolveGroupRatio_UserTierMultipliesOverride 锁死本设计最容易写错的一条
// （docs/user-tier-pricing-and-topup-package-design.md §4）：
// Layer 2 命中 override 时，Layer 3 **照样乘**。
//
// override 说的是「这条供应链上这个模型的成本就是这个价」，属于成本侧；用户档
// 折扣是售价侧。两个维度正交，谁也不该吃掉谁。
//
// 若实现被改成「override 命中即 return，跳过 Layer 3」，Final 会是 2.2 而不是
// 1.98——用户的专属折扣在配了精确定价的模型上**静默失效**，账单上看不出任何异常，
// 只有逐笔反算才能发现。下面对 2.2 的显式否定断言就是防这个回归的。
func TestResolveGroupRatio_UserTierMultipliesOverride(t *testing.T) {
	seedUserTier(t,
		`{"premium":1.5}`,
		`{}`,
		`{"premium":{"GLM-5":{"mode":"override","value":2.2}}}`,
		`{"vip":{"*":0.9}}`,
	)

	res := ResolveGroupRatio("vip", "premium", "GLM-5")

	require.InDelta(t, 2.2, res.AfterModelRule, 1e-9, "Layer 2 的 override 值")
	require.InDelta(t, 1.98, res.Final, 1e-9)
	require.NotEqual(t, 2.2, res.Final, "override 不得吃掉 Layer 3 的用户档折扣")
	require.Equal(t, "*", res.UserRuleMatch)
	require.InDelta(t, 0.9, res.UserRuleValue, 1e-9)
}

// TestResolveGroupRatio_FourLayersCompose 四层全部命中时逐层叠加。
func TestResolveGroupRatio_FourLayersCompose(t *testing.T) {
	seedUserTier(t,
		`{"premium":1.5}`,
		`{"vip":{"premium":0.7}}`,
		`{"premium":{"GLM-5":{"mode":"multiply","value":0.5}}}`,
		`{"vip":{"GLM-5":0.8}}`,
	)

	res := ResolveGroupRatio("vip", "premium", "GLM-5")

	require.InDelta(t, 1.5, res.GroupRatio, 1e-9, "Layer 0")
	require.InDelta(t, 0.7, res.Base, 1e-9, "Layer 1 覆盖后的基准")
	require.InDelta(t, 0.35, res.AfterModelRule, 1e-9, "Layer 2: 0.7 × 0.5")
	require.InDelta(t, 0.28, res.Final, 1e-9, "Layer 3: 0.35 × 0.8")
}

// TestUserTier_PatternSpecificity Layer 3 的模式特异性必须与 Layer 2 逐位一致：
// 精确 > 长通配 > 短通配 > "*" 兜底。两层各写一份匹配逻辑一旦分叉，就会出现
// 「Layer 2 命中而 Layer 3 不命中」这种没人能解释的价格。
func TestUserTier_PatternSpecificity(t *testing.T) {
	seedUserTier(t, `{"default":1}`, `{}`, `{}`,
		`{"vip":{"*":0.9,"wan2.2-*":0.8,"wan2.2-t2v-*":0.75,"wan2.2-t2v-plus":0.6}}`)

	cases := []struct {
		model     string
		wantMatch string
		wantFinal float64
	}{
		{"wan2.2-t2v-plus", "wan2.2-t2v-plus", 0.6}, // 精确
		{"wan2.2-t2v-lite", "wan2.2-t2v-*", 0.75},   // 长通配
		{"wan2.2-i2v", "wan2.2-*", 0.8},             // 短通配
		{"GLM-5", "*", 0.9},                         // 兜底
	}
	for _, c := range cases {
		t.Run(c.model, func(t *testing.T) {
			res := ResolveGroupRatio("vip", "default", c.model)
			require.Equal(t, c.wantMatch, res.UserRuleMatch)
			require.InDelta(t, c.wantFinal, res.Final, 1e-9)
		})
	}
}

// TestUserTier_ScopedToUserGroup Layer 3 按**用户分组**索引，与使用分组无关。
// 这是售价与供应链解耦的实现保证（§3.2）：同一个用户走哪条链都是同一个折扣。
func TestUserTier_ScopedToUserGroup(t *testing.T) {
	seedUserTier(t, `{"default":1,"premium":1}`, `{}`, `{}`, `{"vip":{"*":0.9}}`)

	t.Run("同一用户跨供应链折扣一致", func(t *testing.T) {
		viaDefault := ResolveGroupRatio("vip", "default", "GLM-5").Final
		viaPremium := ResolveGroupRatio("vip", "premium", "GLM-5").Final
		require.InDelta(t, viaDefault, viaPremium, 1e-9)
		require.InDelta(t, 0.9, viaDefault, 1e-9)
	})

	t.Run("其他用户档不受影响", func(t *testing.T) {
		res := ResolveGroupRatio("default", "premium", "GLM-5")
		require.InDelta(t, 1.0, res.Final, 1e-9)
		require.Empty(t, res.UserRuleMatch)
	})
}

// TestUserTier_NoModelContext 无模型上下文的调用点（modelName 为空）Layer 3 恒不命中，
// 与 Layer 2 同语义。
func TestUserTier_NoModelContext(t *testing.T) {
	seedUserTier(t, `{"premium":1.5}`, `{}`, `{}`, `{"vip":{"*":0.9}}`)

	res := ResolveGroupRatio("vip", "premium", "")
	require.InDelta(t, 1.5, res.Final, 1e-9)
	require.Empty(t, res.UserRuleMatch)
}

// TestUserTier_BareNumberCompat 裸数字写法与 Layer 2 一致地按 multiply 处理，
// 手工编辑 JSON 的人少踩一个坑。
func TestUserTier_BareNumberCompat(t *testing.T) {
	seedUserTier(t, `{"default":1}`, `{}`, `{}`, `{"vip":{"GLM-5":0.6}}`)

	res := ResolveGroupRatio("vip", "default", "GLM-5")
	require.InDelta(t, 0.6, res.Final, 1e-9)
	require.InDelta(t, 0.6, res.UserRuleValue, 1e-9)
}

// TestCheckUserGroupModelRatio_RejectsOverride override 在 Layer 3 必须被拒绝。
//
// 它一旦被允许，就会吃掉 Layer 0/1/2 承载的全部成本信息——在成本高的模型上直接
// 亏损，而日志里只剩一个最终倍率，反算不出是哪一层拍的板。校验拦在保存时，
// 比事后从账单里发现便宜得多。
func TestCheckUserGroupModelRatio_RejectsOverride(t *testing.T) {
	err := CheckUserGroupModelRatio(`{"vip":{"GLM-5":{"mode":"override","value":2.2}}}`)
	require.Error(t, err)
	require.Contains(t, err.Error(), "override is not allowed")
}

func TestCheckUserGroupModelRatio(t *testing.T) {
	t.Run("空配置合法", func(t *testing.T) {
		require.NoError(t, CheckUserGroupModelRatio(""))
		require.NoError(t, CheckUserGroupModelRatio("   "))
		require.NoError(t, CheckUserGroupModelRatio(`{}`))
	})

	t.Run("合法配置", func(t *testing.T) {
		require.NoError(t, CheckUserGroupModelRatio(
			`{"vip":{"*":0.9,"GLM-5":{"mode":"multiply","value":0.6,"remark":"年框客户"}}}`))
	})

	t.Run("非法JSON", func(t *testing.T) {
		require.Error(t, CheckUserGroupModelRatio(`{`))
	})

	t.Run("空模式串", func(t *testing.T) {
		require.Error(t, CheckUserGroupModelRatio(`{"vip":{"":0.9}}`))
	})

	t.Run("中缀通配被拒", func(t *testing.T) {
		err := CheckUserGroupModelRatio(`{"vip":{"wan*v":0.9}}`)
		require.Error(t, err)
		require.Contains(t, err.Error(), "trailing wildcard")
	})

	t.Run("负值被拒", func(t *testing.T) {
		require.Error(t, CheckUserGroupModelRatio(`{"vip":{"GLM-5":-0.1}}`))
	})

	t.Run("未知模式被拒", func(t *testing.T) {
		require.Error(t, CheckUserGroupModelRatio(`{"vip":{"GLM-5":{"mode":"divide","value":2}}}`))
	})
}

// TestHasUserGroupModelRules 供 pricing 接口决定是否展开终值表。漏了它，只配了
// Layer 3 的档位不会进 group_model_ratio，模型广场显示价偏高而实扣正确（§8.0）。
func TestHasUserGroupModelRules(t *testing.T) {
	seedUserTier(t, `{"default":1}`, `{}`, `{}`, `{"vip":{"*":0.9},"empty":{}}`)

	require.True(t, HasUserGroupModelRules("vip"))
	require.False(t, HasUserGroupModelRules("empty"))
	require.False(t, HasUserGroupModelRules("nonexistent"))
}
