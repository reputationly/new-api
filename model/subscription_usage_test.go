package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 「我的订阅」的余量数据。守三件事：点数只算本订阅、未过期的批次（与扣费同一个
// 过期判定）；次数取计数器实时值；计数器未实例化的权益不列（扣费侧不会命中它）。

func seedLot(t *testing.T, userId, subId int, total, used, expiresAt int64) {
	t.Helper()
	require.NoError(t, DB.Create(&ComputePointLot{
		UserId: userId, Source: ComputePointLotSourceSubscription, RefId: subId,
		PointsTotal: total, PointsUsed: used, ExpiresAt: expiresAt,
		Status: ComputePointLotStatusActive,
	}).Error)
}

func activeSummaries(t *testing.T, userId int) []SubscriptionSummary {
	t.Helper()
	s, err := GetAllActiveUserSubscriptions(userId)
	require.NoError(t, err)
	require.NoError(t, AttachSubscriptionUsage(s))
	return s
}

func TestAttachSubscriptionUsage_PointsOnlyCountThisSubsLiveLots(t *testing.T) {
	truncateTables(t)
	plan := seedEntitlementPlan(t, SubscriptionResetMonthly)
	sub := seedActiveSubscription(t, 1801, plan, -3600, 3600)
	other := seedActiveSubscription(t, 1801, plan, -3600, 7200)
	now := GetDBTimestamp()

	seedLot(t, 1801, sub.Id, 50000, 17860, now+3600)
	seedLot(t, 1801, sub.Id, 99999, 0, now-10) // 已过期：到期即作废，不能算进余量
	seedLot(t, 1801, other.Id, 7777, 0, now+7200)
	// 同一用户的非套餐批次（加购 / 管理员发放）不属于任何订阅
	require.NoError(t, DB.Create(&ComputePointLot{
		UserId: 1801, Source: ComputePointLotSourceAdminGrant, RefId: sub.Id,
		PointsTotal: 5555, ExpiresAt: now + 3600,
	}).Error)

	var got *SubscriptionComputePoints
	for _, s := range activeSummaries(t, 1801) {
		if s.Subscription.Id == sub.Id {
			got = s.ComputePoints
		}
	}
	require.NotNil(t, got)
	require.Equal(t, int64(50000), got.Total)
	require.Equal(t, int64(17860), got.Used)
	require.Equal(t, int64(32140), got.Available)
	require.Equal(t, now+3600, got.ExpiresAt)
	for _, s := range activeSummaries(t, 1801) {
		require.Equal(t, plan.Title, s.PlanTitle)
	}
}

func TestAttachSubscriptionUsage_NoLotsMeansNoPoints(t *testing.T) {
	truncateTables(t)
	plan := seedEntitlementPlan(t, SubscriptionResetMonthly)
	seedActiveSubscription(t, 1802, plan, -3600, 3600)

	s := activeSummaries(t, 1802)
	require.Len(t, s, 1)
	require.Nil(t, s[0].ComputePoints, "老式套餐没有算力点，不该显示一个 0/0 的进度条")
}

func TestAttachSubscriptionUsage_EntitlementCountersInSortOrder(t *testing.T) {
	truncateTables(t)
	plan := seedEntitlementPlan(t, SubscriptionResetMonthly)
	require.NoError(t, replaceEntitlements(t, plan.Id, []SubscriptionPlanEntitlement{
		{Models: "gpt-5", ConsumePoints: true, ConsumeDiscount: 1,
			LimitCount: 500, ResetPeriod: SubscriptionResetMonthly},
		{Models: "qwen3-*", ChannelIds: "7", ConsumePoints: false, RateLimitRPM: 60},
	}))
	sub := seedActiveSubscription(t, 1803, plan, -3600, 3600)
	require.NoError(t, DB.Model(&UserSubscriptionEntitlement{}).
		Where("user_subscription_id = ? AND limit_count = ?", sub.Id, 500).
		Updates(map[string]interface{}{"used_count": 347, "next_reset_time": 1234567}).Error)

	s := activeSummaries(t, 1803)
	ents := s[0].Entitlements
	require.Len(t, ents, 2)
	require.Equal(t, "gpt-5", ents[0].Models)
	require.Equal(t, int64(500), ents[0].LimitCount)
	require.Equal(t, int64(347), ents[0].UsedCount)
	require.Equal(t, int64(1234567), ents[0].NextResetTime)
	require.Equal(t, "qwen3-*", ents[1].Models)
	require.False(t, ents[1].ConsumePoints)
	require.Equal(t, 60, ents[1].RateLimitRPM)
	require.True(t, ents[1].ChannelLimited)
}

// 权益在订阅售出之后才加：计数器没实例化，扣费不会命中，页面也不能承诺它
func TestAttachSubscriptionUsage_SkipsUninstantiatedEntitlement(t *testing.T) {
	truncateTables(t)
	plan := seedEntitlementPlan(t, SubscriptionResetMonthly)
	require.NoError(t, replaceEntitlements(t, plan.Id, []SubscriptionPlanEntitlement{
		{Models: "old", ConsumePoints: true, RateLimitRPM: 60},
	}))
	seedActiveSubscription(t, 1804, plan, -3600, 3600)
	list, err := ListPlanEntitlements(plan.Id)
	require.NoError(t, err)
	require.NoError(t, replaceEntitlements(t, plan.Id, []SubscriptionPlanEntitlement{
		{Id: list[0].Id, Models: "old", ConsumePoints: true, RateLimitRPM: 60},
		{Models: "added-later", ConsumePoints: true, RateLimitRPM: 60},
	}))

	ents := activeSummaries(t, 1804)[0].Entitlements
	require.Len(t, ents, 1)
	require.Equal(t, "old", ents[0].Models)
}

// ---- 老式额度：「无」与「不限」 ----

// 直接改库要连带失效套餐缓存，与生产侧编辑套餐的路径一致
func setPlanComputePoints(t *testing.T, plan *SubscriptionPlan, n int64) {
	t.Helper()
	require.NoError(t, DB.Model(plan).Update("compute_points_per_period", n).Error)
	InvalidateSubscriptionPlanCache(plan.Id)
}

func preConsume(t *testing.T, userId int, reqId string) error {
	t.Helper()
	_, err := PreConsumeUserSubscription(reqId, userId, "any-model", 0, 1000)
	return err
}

// 只配算力点、总额度留 0：不提供老式额度，套餐外的请求不能从订阅走
func TestPreConsumeUserSubscription_ComputePointPlanWithZeroTotalHasNoQuota(t *testing.T) {
	truncateTables(t)
	plan := seedEntitlementPlan(t, SubscriptionResetMonthly)
	setPlanComputePoints(t, plan, 50000)
	seedActiveSubscription(t, 1811, plan, -3600, 3600)

	err := preConsume(t, 1811, "r-1811")
	require.Error(t, err)
	require.Contains(t, err.Error(), "subscription quota insufficient",
		"必须报额度不足，调用方才会按偏好降级到余额")
}

// 只配权益（例如自有模型不消耗算力点）、没配算力点：同样是新式套餐
func TestPreConsumeUserSubscription_EntitlementOnlyPlanWithZeroTotalHasNoQuota(t *testing.T) {
	truncateTables(t)
	plan := seedEntitlementPlan(t, SubscriptionResetMonthly)
	require.NoError(t, replaceEntitlements(t, plan.Id, []SubscriptionPlanEntitlement{
		{Models: "self-hosted", ConsumePoints: false, RateLimitRPM: 60},
	}))
	seedActiveSubscription(t, 1812, plan, -3600, 3600)

	require.Error(t, preConsume(t, 1812, "r-1812"))
}

// 老式套餐的 0 仍是「不限」：存量套餐零影响
func TestPreConsumeUserSubscription_LegacyZeroTotalStillUnlimited(t *testing.T) {
	truncateTables(t)
	plan := seedEntitlementPlan(t, SubscriptionResetMonthly)
	seedActiveSubscription(t, 1813, plan, -3600, 3600)

	require.NoError(t, preConsume(t, 1813, "r-1813"))
}

// 新式套餐显式配了总额度：照常可用
func TestPreConsumeUserSubscription_ComputePointPlanWithExplicitTotal(t *testing.T) {
	truncateTables(t)
	plan := seedEntitlementPlan(t, SubscriptionResetMonthly)
	setPlanComputePoints(t, plan, 50000)
	sub := seedActiveSubscription(t, 1814, plan, -3600, 3600)
	require.NoError(t, DB.Model(sub).Update("amount_total", 100000).Error)

	require.NoError(t, preConsume(t, 1814, "r-1814"))
}

// 多个订阅时跳过的是新式那个，老式的照常可用
func TestPreConsumeUserSubscription_SkipsNewStyleButUsesLegacy(t *testing.T) {
	truncateTables(t)
	newStyle := seedEntitlementPlan(t, SubscriptionResetMonthly)
	setPlanComputePoints(t, newStyle, 50000)
	legacy := seedEntitlementPlan(t, SubscriptionResetMonthly)
	seedActiveSubscription(t, 1815, newStyle, -3600, 3600) // 到期更近，先被尝试
	legacySub := seedActiveSubscription(t, 1815, legacy, -3600, 7200)

	res, err := PreConsumeUserSubscription("r-1815", 1815, "m", 0, 1000)
	require.NoError(t, err)
	require.Equal(t, legacySub.Id, res.UserSubscriptionId)
}

func TestSubscriptionSummaries_FlagNoLegacyQuota(t *testing.T) {
	truncateTables(t)
	newStyle := seedEntitlementPlan(t, SubscriptionResetMonthly)
	setPlanComputePoints(t, newStyle, 50000)
	legacy := seedEntitlementPlan(t, SubscriptionResetMonthly)
	newSub := seedActiveSubscription(t, 1816, newStyle, -3600, 3600)
	seedActiveSubscription(t, 1816, legacy, -3600, 7200)

	all, err := GetAllUserSubscriptions(1816)
	require.NoError(t, err)
	for _, s := range all {
		require.Equal(t, s.Subscription.Id == newSub.Id, s.NoLegacyQuota,
			"只有新式套餐的 0 是「无」")
	}
}

func TestPlanHasNoLegacyQuota(t *testing.T) {
	require.True(t, PlanHasNoLegacyQuota(&SubscriptionPlan{ComputePointsPerPeriod: 1}, false))
	require.True(t, PlanHasNoLegacyQuota(&SubscriptionPlan{}, true))
	require.False(t, PlanHasNoLegacyQuota(&SubscriptionPlan{}, false), "老式 0 = 不限")
	require.False(t, PlanHasNoLegacyQuota(&SubscriptionPlan{TotalAmount: 5, ComputePointsPerPeriod: 1}, true))
}
