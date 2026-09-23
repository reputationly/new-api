package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 展示侧与扣费侧必须同口径。页面显示「专业版 8 折」而实际扣费走了别的权益，
// 用户看到的价和账单就对不上——这类不一致解释成本极高，而且没有任何报错，
// 只能靠客诉发现。这个文件守的就是两者的一致性。

func TestListUserEntitlementCoverage_BasicFields(t *testing.T) {
	truncateTables(t)
	plan := seedEntitlementPlan(t, SubscriptionResetMonthly)
	require.NoError(t, replaceEntitlements(t, plan.Id, []SubscriptionPlanEntitlement{
		{Models: "gpt-5", ConsumePoints: true, ConsumeDiscount: 0.5,
			LimitCount: 500, ResetPeriod: SubscriptionResetMonthly},
	}))
	seedActiveSubscription(t, 1701, plan, -3600, 3600)

	cov, err := ListUserEntitlementCoverage(1701, []string{"gpt-5", "other"})
	require.NoError(t, err)
	require.Len(t, cov, 1)

	c := cov["gpt-5"]
	require.NotNil(t, c)
	require.True(t, c.ConsumePoints)
	require.Equal(t, 0.5, c.Discount)
	require.Equal(t, int64(500), c.LimitCount)
	require.False(t, c.ChannelLimited)
	require.Equal(t, plan.Id, c.PlanId)
}

// 通配范围要展开到具体模型上。
func TestListUserEntitlementCoverage_ExpandsWildcard(t *testing.T) {
	truncateTables(t)
	plan := seedEntitlementPlan(t, SubscriptionResetMonthly)
	require.NoError(t, replaceEntitlements(t, plan.Id, []SubscriptionPlanEntitlement{
		{Models: "claude-opus-*", ConsumePoints: true, RateLimitRPM: 60},
	}))
	seedActiveSubscription(t, 1702, plan, -3600, 3600)

	cov, err := ListUserEntitlementCoverage(1702,
		[]string{"claude-opus-4", "claude-opus-4-1", "claude-sonnet-4"})
	require.NoError(t, err)
	require.Len(t, cov, 2)
	require.NotNil(t, cov["claude-opus-4"])
	require.NotNil(t, cov["claude-opus-4-1"])
	require.Nil(t, cov["claude-sonnet-4"])
}

// 限定了渠道的权益要如实标注：展示侧不知道请求会落到哪个渠道，
// 这个价是**有条件的**。不标的话用户会以为必然拿得到。
func TestListUserEntitlementCoverage_FlagsChannelLimited(t *testing.T) {
	truncateTables(t)
	plan := seedEntitlementPlan(t, SubscriptionResetMonthly)
	require.NoError(t, replaceEntitlements(t, plan.Id, []SubscriptionPlanEntitlement{
		{Models: "m", ChannelIds: "7", ConsumePoints: true, RateLimitRPM: 60},
	}))
	seedActiveSubscription(t, 1703, plan, -3600, 3600)

	cov, err := ListUserEntitlementCoverage(1703, []string{"m"})
	require.NoError(t, err)
	require.NotNil(t, cov["m"])
	require.True(t, cov["m"].ChannelLimited)
}

// 与 MatchUserEntitlement 同序：多套餐时命中到期近的那个。
// 显示「最优的那个」而按另一个扣费，等于告诉用户一个他拿不到的价。
func TestListUserEntitlementCoverage_SameOrderAsBilling(t *testing.T) {
	truncateTables(t)
	near := seedEntitlementPlan(t, SubscriptionResetMonthly)
	far := seedEntitlementPlan(t, SubscriptionResetMonthly)
	// 远期套餐折扣更优，但到期更晚——扣费不会走它，展示也不该显示它
	require.NoError(t, replaceEntitlements(t, near.Id, []SubscriptionPlanEntitlement{
		{Models: "shared", ConsumePoints: true, ConsumeDiscount: 1, RateLimitRPM: 60},
	}))
	require.NoError(t, replaceEntitlements(t, far.Id, []SubscriptionPlanEntitlement{
		{Models: "shared", ConsumePoints: true, ConsumeDiscount: 0.1, RateLimitRPM: 60},
	}))
	seedActiveSubscription(t, 1704, far, -3600, 99999)
	seedActiveSubscription(t, 1704, near, -3600, 3600)

	cov, err := ListUserEntitlementCoverage(1704, []string{"shared"})
	require.NoError(t, err)
	require.NotNil(t, cov["shared"])
	require.Equal(t, near.Id, cov["shared"].PlanId, "展示必须与扣费命中同一个套餐")
	require.Equal(t, float64(1), cov["shared"].Discount, "不得显示一个拿不到的折扣")

	// 同一份输入下，扣费侧的判定必须指向同一条权益
	m, err := MatchUserEntitlement(1704, "shared", 1)
	require.NoError(t, err)
	require.NotNil(t, m)
	require.Equal(t, cov["shared"].EntitlementId, m.Entitlement.Id,
		"展示与扣费必须命中同一条权益")
}

// 同一订阅内按 sort_order，与扣费侧一致。
func TestListUserEntitlementCoverage_SortOrderMatchesBilling(t *testing.T) {
	truncateTables(t)
	plan := seedEntitlementPlan(t, SubscriptionResetMonthly)
	require.NoError(t, replaceEntitlements(t, plan.Id, []SubscriptionPlanEntitlement{
		{Models: "glm-*", ConsumePoints: true, ConsumeDiscount: 1, RateLimitRPM: 60},
		{Models: "glm-4-plus", ConsumePoints: true, ConsumeDiscount: 0.1, RateLimitRPM: 60},
	}))
	seedActiveSubscription(t, 1705, plan, -3600, 3600)

	cov, err := ListUserEntitlementCoverage(1705, []string{"glm-4-plus"})
	require.NoError(t, err)
	require.Equal(t, float64(1), cov["glm-4-plus"].Discount, "靠前的权益盖住后面的")

	m, err := MatchUserEntitlement(1705, "glm-4-plus", 1)
	require.NoError(t, err)
	require.Equal(t, cov["glm-4-plus"].EntitlementId, m.Entitlement.Id)
}

// 计数器未实例化时扣费侧会降级，展示侧必须同口径——
// 否则页面显示「套餐内」而实际扣的是余额。
func TestListUserEntitlementCoverage_MissingCounterNotCovered(t *testing.T) {
	truncateTables(t)
	plan := seedEntitlementPlan(t, SubscriptionResetMonthly)
	require.NoError(t, replaceEntitlements(t, plan.Id, []SubscriptionPlanEntitlement{
		{Models: "m", ConsumePoints: true, RateLimitRPM: 60},
	}))
	sub := seedActiveSubscription(t, 1706, plan, -3600, 3600)
	require.NoError(t, DB.Where("user_subscription_id = ?", sub.Id).
		Delete(&UserSubscriptionEntitlement{}).Error)

	cov, err := ListUserEntitlementCoverage(1706, []string{"m"})
	require.NoError(t, err)
	require.Nil(t, cov["m"], "扣费侧会降级，展示侧不得显示为套餐内")

	m, err := MatchUserEntitlement(1706, "m", 1)
	require.NoError(t, err)
	require.Nil(t, m)
}

// 过期订阅不算覆盖——到期即消失，不等后台任务。
func TestListUserEntitlementCoverage_ExpiredNotCovered(t *testing.T) {
	truncateTables(t)
	plan := seedEntitlementPlan(t, SubscriptionResetMonthly)
	require.NoError(t, replaceEntitlements(t, plan.Id, []SubscriptionPlanEntitlement{
		{Models: "m", ConsumePoints: true, RateLimitRPM: 60},
	}))
	seedActiveSubscription(t, 1707, plan, -7200, -3600)

	cov, err := ListUserEntitlementCoverage(1707, []string{"m"})
	require.NoError(t, err)
	require.Empty(t, cov)
}

func TestListUserEntitlementCoverage_NoSubscription(t *testing.T) {
	truncateTables(t)
	cov, err := ListUserEntitlementCoverage(1708, []string{"m"})
	require.NoError(t, err)
	require.Empty(t, cov)
}

// 多套餐覆盖同一模型时，all 要列全供 tooltip 说明「当前走哪个」，
// 且 all[0] 必须是实际命中的那条——顺序错了 tooltip 就会把说明写反。
func TestListUserEntitlementCoverage_AllListsEveryCoveringPlan(t *testing.T) {
	truncateTables(t)
	near := seedEntitlementPlan(t, SubscriptionResetMonthly)
	far := seedEntitlementPlan(t, SubscriptionResetMonthly)
	require.NoError(t, replaceEntitlements(t, near.Id, []SubscriptionPlanEntitlement{
		{Models: "shared", ConsumePoints: true, ConsumeDiscount: 1, RateLimitRPM: 60},
	}))
	require.NoError(t, replaceEntitlements(t, far.Id, []SubscriptionPlanEntitlement{
		{Models: "shared", ConsumePoints: true, ConsumeDiscount: 0.1, RateLimitRPM: 60},
	}))
	seedActiveSubscription(t, 1709, far, -3600, 99999)
	seedActiveSubscription(t, 1709, near, -3600, 3600)

	cov, err := ListUserEntitlementCoverage(1709, []string{"shared"})
	require.NoError(t, err)
	c := cov["shared"]
	require.NotNil(t, c)
	require.Len(t, c.All, 2, "两个套餐都覆盖了，tooltip 要列全")
	require.Equal(t, near.Id, c.All[0].PlanId, "第一项必须是实际命中的那条")
	require.Equal(t, far.Id, c.All[1].PlanId)
	require.Equal(t, c.All[0].EntitlementId, c.EntitlementId, "顶层字段与 all[0] 同源")
}

// 同一订阅内被盖住的权益不列进 all：扣费永远走不到它，
// 列出来只会让用户以为自己有得选。
func TestListUserEntitlementCoverage_AllSkipsShadowedEntitlement(t *testing.T) {
	truncateTables(t)
	plan := seedEntitlementPlan(t, SubscriptionResetMonthly)
	require.NoError(t, replaceEntitlements(t, plan.Id, []SubscriptionPlanEntitlement{
		{Models: "glm-*", ConsumePoints: true, ConsumeDiscount: 1, RateLimitRPM: 60},
		{Models: "glm-4-plus", ConsumePoints: true, ConsumeDiscount: 0.1, RateLimitRPM: 60},
	}))
	seedActiveSubscription(t, 1710, plan, -3600, 3600)

	cov, err := ListUserEntitlementCoverage(1710, []string{"glm-4-plus"})
	require.NoError(t, err)
	require.Len(t, cov["glm-4-plus"].All, 1, "被盖住的那条不该出现")
	require.Equal(t, float64(1), cov["glm-4-plus"].All[0].Discount)
}

// 「有套餐但一个模型都没覆盖」与「没有套餐」在展示上是同一件事，
// 返回 {} 会让前端的真值判断把两者分开处理。
func TestListUserEntitlementCoverage_NilWhenNothingCovered(t *testing.T) {
	truncateTables(t)
	plan := seedEntitlementPlan(t, SubscriptionResetMonthly)
	require.NoError(t, replaceEntitlements(t, plan.Id, []SubscriptionPlanEntitlement{
		{Models: "covered-model", ConsumePoints: true, RateLimitRPM: 60},
	}))
	seedActiveSubscription(t, 1711, plan, -3600, 3600)

	cov, err := ListUserEntitlementCoverage(1711, []string{"unrelated-model"})
	require.NoError(t, err)
	require.Nil(t, cov, "有套餐但没覆盖 → 必须与无套餐返回同一个值")
}

// 限渠道的权益排在前面时，扣费侧对其他渠道会跳过它、命中后面那条。
// 展示侧不能在它这里停：主展示要取「不限渠道时扣费命中的那条」，
// 限渠道那条进 all 由 tooltip 说明。
func TestListUserEntitlementCoverage_ChannelLimitedFirstDoesNotHideLater(t *testing.T) {
	truncateTables(t)
	plan := seedEntitlementPlan(t, SubscriptionResetMonthly)
	require.NoError(t, replaceEntitlements(t, plan.Id, []SubscriptionPlanEntitlement{
		{Models: "m", ChannelIds: "7", ConsumePoints: false, RateLimitRPM: 60},
		{Models: "m", ConsumePoints: true, ConsumeDiscount: 0.5, RateLimitRPM: 60},
	}))
	seedActiveSubscription(t, 1712, plan, -3600, 3600)

	cov, err := ListUserEntitlementCoverage(1712, []string{"m"})
	require.NoError(t, err)
	c := cov["m"]
	require.NotNil(t, c)
	require.Len(t, c.All, 2, "限渠道那条之后的条目对其他渠道仍可达")
	require.True(t, c.All[0].ChannelLimited)
	require.False(t, c.ChannelLimited, "主展示取确定拿得到的那条")
	require.Equal(t, 0.5, c.Discount)

	onOther, err := MatchUserEntitlement(1712, "m", 99)
	require.NoError(t, err)
	require.Equal(t, onOther.Entitlement.Id, c.EntitlementId,
		"主展示必须等于请求落在不限渠道时扣费命中的那条")
	onLimited, err := MatchUserEntitlement(1712, "m", 7)
	require.NoError(t, err)
	require.Equal(t, onLimited.Entitlement.Id, c.All[0].EntitlementId)
}

// 跨订阅同理：到期近的订阅只有限渠道权益时，不限渠道的请求会落到下一个订阅。
func TestListUserEntitlementCoverage_ChannelLimitedAcrossSubscriptions(t *testing.T) {
	truncateTables(t)
	near := seedEntitlementPlan(t, SubscriptionResetMonthly)
	far := seedEntitlementPlan(t, SubscriptionResetMonthly)
	require.NoError(t, replaceEntitlements(t, near.Id, []SubscriptionPlanEntitlement{
		{Models: "m", ChannelIds: "7", ConsumePoints: true, RateLimitRPM: 60},
	}))
	require.NoError(t, replaceEntitlements(t, far.Id, []SubscriptionPlanEntitlement{
		{Models: "m", ConsumePoints: true, ConsumeDiscount: 0.3, RateLimitRPM: 60},
	}))
	seedActiveSubscription(t, 1713, far, -3600, 99999)
	seedActiveSubscription(t, 1713, near, -3600, 3600)

	cov, err := ListUserEntitlementCoverage(1713, []string{"m"})
	require.NoError(t, err)
	c := cov["m"]
	require.Equal(t, far.Id, c.PlanId)
	onOther, err := MatchUserEntitlement(1713, "m", 99)
	require.NoError(t, err)
	require.Equal(t, onOther.Entitlement.Id, c.EntitlementId)
}

// 全是限渠道权益时没有「确定拿得到」的价：取第一条，由角标标明有条件。
func TestListUserEntitlementCoverage_AllChannelLimitedFallsBackToFirst(t *testing.T) {
	truncateTables(t)
	plan := seedEntitlementPlan(t, SubscriptionResetMonthly)
	require.NoError(t, replaceEntitlements(t, plan.Id, []SubscriptionPlanEntitlement{
		{Models: "m", ChannelIds: "7", ConsumePoints: true, ConsumeDiscount: 0.4, RateLimitRPM: 60},
		{Models: "m", ChannelIds: "8", ConsumePoints: true, ConsumeDiscount: 0.9, RateLimitRPM: 60},
	}))
	seedActiveSubscription(t, 1714, plan, -3600, 3600)

	cov, err := ListUserEntitlementCoverage(1714, []string{"m"})
	require.NoError(t, err)
	c := cov["m"]
	require.Len(t, c.All, 2)
	require.True(t, c.ChannelLimited)
	require.Equal(t, 0.4, c.Discount)
}
