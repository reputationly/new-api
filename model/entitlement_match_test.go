package model

import (
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

// 建一个「已生效、未过期」的订阅并实例化权益计数器。
// 直接构造而不走 CreateUserSubscriptionFromPlanTx：那条路会按当前时刻算 start_time，
// 而这里要精确控制生效窗口来验证「活跃判定」。
func seedActiveSubscription(t *testing.T, userId int, plan *SubscriptionPlan, startOffset, endOffset int64) *UserSubscription {
	t.Helper()
	now := common.GetTimestamp()
	sub := &UserSubscription{
		UserId:    userId,
		PlanId:    plan.Id,
		Status:    "active",
		StartTime: now + startOffset,
		EndTime:   now + endOffset,
	}
	require.NoError(t, DB.Create(sub).Error)

	ents, err := ListPlanEntitlements(plan.Id)
	require.NoError(t, err)
	for _, e := range ents {
		require.NoError(t, DB.Create(&UserSubscriptionEntitlement{
			UserId:             userId,
			UserSubscriptionId: sub.Id,
			PlanEntitlementId:  e.Id,
			LimitCount:         e.LimitCount,
		}).Error)
	}
	return sub
}

func TestMatchUserEntitlement_ModelAndChannel(t *testing.T) {
	truncateTables(t)
	plan := seedEntitlementPlan(t, SubscriptionResetMonthly)
	require.NoError(t, replaceEntitlements(t, plan.Id, []SubscriptionPlanEntitlement{
		{Models: "qwen3-*", ChannelIds: "7", ConsumePoints: true, RateLimitRPM: 60},
		{Models: "gpt-5", ConsumePoints: true, LimitCount: 500, ResetPeriod: SubscriptionResetMonthly},
	}))
	seedActiveSubscription(t, 901, plan, -3600, 3600)

	t.Run("模型与渠道都命中", func(t *testing.T) {
		m, err := MatchUserEntitlement(901, "qwen3-max", 7)
		require.NoError(t, err)
		require.NotNil(t, m)
		require.Equal(t, "qwen3-*", m.Entitlement.Models)
	})

	t.Run("模型命中但渠道不在限定内则不命中", func(t *testing.T) {
		m, err := MatchUserEntitlement(901, "qwen3-max", 9)
		require.NoError(t, err)
		require.Nil(t, m, "限定了渠道就必须落在限定内，否则降级")
	})

	t.Run("未限定渠道的权益任何渠道都命中", func(t *testing.T) {
		m, err := MatchUserEntitlement(901, "gpt-5", 9)
		require.NoError(t, err)
		require.NotNil(t, m)
		require.Equal(t, "gpt-5", m.Entitlement.Models)
	})

	t.Run("模型不在任何范围内不命中", func(t *testing.T) {
		m, err := MatchUserEntitlement(901, "claude-opus-4", 7)
		require.NoError(t, err)
		require.Nil(t, m)
	})
}

// 限定了渠道却取不到渠道号时不能命中：不知道落在哪个渠道就假定命中，
// 等于把套餐优惠发给本不该享受的请求。
func TestMatchUserEntitlement_UnknownChannelOnlyMatchesUnrestricted(t *testing.T) {
	truncateTables(t)
	plan := seedEntitlementPlan(t, SubscriptionResetMonthly)
	require.NoError(t, replaceEntitlements(t, plan.Id, []SubscriptionPlanEntitlement{
		{Models: "a-model", ChannelIds: "7", ConsumePoints: true, RateLimitRPM: 60},
		{Models: "b-model", ConsumePoints: true, RateLimitRPM: 60},
	}))
	seedActiveSubscription(t, 902, plan, -3600, 3600)

	m, err := MatchUserEntitlement(902, "a-model", 0)
	require.NoError(t, err)
	require.Nil(t, m, "限定渠道 + 渠道未知 = 不命中")

	m, err = MatchUserEntitlement(902, "b-model", 0)
	require.NoError(t, err)
	require.NotNil(t, m, "不限渠道的仍然命中")
}

// 尚未生效与已过期的订阅都不参与匹配。
func TestMatchUserEntitlement_OnlyActiveWindow(t *testing.T) {
	truncateTables(t)
	plan := seedEntitlementPlan(t, SubscriptionResetMonthly)
	require.NoError(t, replaceEntitlements(t, plan.Id, []SubscriptionPlanEntitlement{
		{Models: "m", ConsumePoints: true, RateLimitRPM: 60},
	}))

	seedActiveSubscription(t, 903, plan, 3600, 7200) // 未生效
	m, err := MatchUserEntitlement(903, "m", 1)
	require.NoError(t, err)
	require.Nil(t, m, "未到生效时间不该命中")

	seedActiveSubscription(t, 904, plan, -7200, -3600) // 已过期
	m, err = MatchUserEntitlement(904, "m", 1)
	require.NoError(t, err)
	require.Nil(t, m, "已过期不该命中")
}

// 多个活跃订阅时先用快过期的那个——与算力点批次、订阅预扣是同一条原则。
func TestMatchUserEntitlement_NearestExpiryFirst(t *testing.T) {
	truncateTables(t)
	near := seedEntitlementPlan(t, SubscriptionResetMonthly)
	far := seedEntitlementPlan(t, SubscriptionResetMonthly)
	require.NoError(t, replaceEntitlements(t, near.Id, []SubscriptionPlanEntitlement{
		{Models: "shared", ConsumePoints: true, LimitCount: 10, ResetPeriod: SubscriptionResetMonthly},
	}))
	require.NoError(t, replaceEntitlements(t, far.Id, []SubscriptionPlanEntitlement{
		{Models: "shared", ConsumePoints: true, LimitCount: 999, ResetPeriod: SubscriptionResetMonthly},
	}))

	// 刻意先建「远期」那个，确保命中的是按到期时间排序而不是插入顺序
	farSub := seedActiveSubscription(t, 905, far, -3600, 99999)
	nearSub := seedActiveSubscription(t, 905, near, -3600, 3600)

	m, err := MatchUserEntitlement(905, "shared", 1)
	require.NoError(t, err)
	require.NotNil(t, m)
	require.Equal(t, nearSub.Id, m.UserSubscriptionId, "应命中快过期的那个订阅")
	require.NotEqual(t, farSub.Id, m.UserSubscriptionId)
}

// 同一订阅内按 sort_order 先命中先生效：重叠时靠前的那条说了算。
func TestMatchUserEntitlement_SortOrderWins(t *testing.T) {
	truncateTables(t)
	plan := seedEntitlementPlan(t, SubscriptionResetMonthly)
	require.NoError(t, replaceEntitlements(t, plan.Id, []SubscriptionPlanEntitlement{
		{Models: "glm-*", ConsumePoints: true, RateLimitRPM: 60},
		{Models: "glm-4-plus", ConsumePoints: true, LimitCount: 500, ResetPeriod: SubscriptionResetMonthly},
	}))
	seedActiveSubscription(t, 906, plan, -3600, 3600)

	m, err := MatchUserEntitlement(906, "glm-4-plus", 1)
	require.NoError(t, err)
	require.NotNil(t, m)
	require.Equal(t, "glm-*", m.Entitlement.Models, "靠前的权益盖住后面的")
}

// 权益是订阅售出之后才加的 → 计数器没实例化 → 当作未命中降级，而不是报错或补建。
func TestMatchUserEntitlement_MissingCounterFallsThrough(t *testing.T) {
	truncateTables(t)
	plan := seedEntitlementPlan(t, SubscriptionResetMonthly)
	require.NoError(t, replaceEntitlements(t, plan.Id, []SubscriptionPlanEntitlement{
		{Models: "m", ConsumePoints: true, RateLimitRPM: 60},
	}))
	sub := seedActiveSubscription(t, 907, plan, -3600, 3600)

	// 模拟「售出在先、权益在后」：把计数器删掉
	require.NoError(t, DB.Where("user_subscription_id = ?", sub.Id).
		Delete(&UserSubscriptionEntitlement{}).Error)

	m, err := MatchUserEntitlement(907, "m", 1)
	require.NoError(t, err)
	require.Nil(t, m, "没有计数器时应降级，不报错")
}

func TestMatchUserEntitlement_NoSubscription(t *testing.T) {
	truncateTables(t)
	m, err := MatchUserEntitlement(908, "m", 1)
	require.NoError(t, err)
	require.Nil(t, m)
}

// ---------------------------------------------------------------------------
// 次数闸门
// ---------------------------------------------------------------------------

func seedCounter(t *testing.T, limit, used int64) *UserSubscriptionEntitlement {
	t.Helper()
	row := &UserSubscriptionEntitlement{
		UserId: 910, UserSubscriptionId: 1, PlanEntitlementId: 1,
		LimitCount: limit, UsedCount: used,
	}
	require.NoError(t, DB.Create(row).Error)
	return row
}

func TestTryConsumeEntitlementCount_WithinLimit(t *testing.T) {
	truncateTables(t)
	c := seedCounter(t, 3, 0)

	for i := 1; i <= 3; i++ {
		ok, err := TryConsumeEntitlementCount(c.Id)
		require.NoError(t, err)
		require.True(t, ok, "第 %d 次应放行", i)
	}
	ok, err := TryConsumeEntitlementCount(c.Id)
	require.NoError(t, err)
	require.False(t, ok, "用尽后必须拒绝，由调用方降级")

	var after UserSubscriptionEntitlement
	require.NoError(t, DB.First(&after, c.Id).Error)
	require.Equal(t, int64(3), after.UsedCount, "拒绝的那次不得计数")
}

// 不限次不构成闸门，但用量要留痕，否则履约率报表看不到这部分调用。
func TestTryConsumeEntitlementCount_UnlimitedStillCounts(t *testing.T) {
	truncateTables(t)
	c := seedCounter(t, 0, 0)

	for i := 0; i < 5; i++ {
		ok, err := TryConsumeEntitlementCount(c.Id)
		require.NoError(t, err)
		require.True(t, ok)
	}
	var after UserSubscriptionEntitlement
	require.NoError(t, DB.First(&after, c.Id).Error)
	require.Equal(t, int64(5), after.UsedCount)
}

// 并发下总放行数不得超过上限。判定与自增必须在同一条 UPDATE 里——
// 先读后判是 check-then-act，并发请求会双双读到「还剩 1 次」各扣一次。
func TestTryConsumeEntitlementCount_ConcurrentNeverExceedsLimit(t *testing.T) {
	truncateTables(t)
	const limit = 5
	c := seedCounter(t, limit, 0)

	var wg sync.WaitGroup
	var mu sync.Mutex
	granted := 0
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := TryConsumeEntitlementCount(c.Id)
			if err == nil && ok {
				mu.Lock()
				granted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	require.Equal(t, limit, granted, "放行数必须恰好等于上限")
	var after UserSubscriptionEntitlement
	require.NoError(t, DB.First(&after, c.Id).Error)
	require.Equal(t, int64(limit), after.UsedCount)
}

func TestRefundEntitlementCount(t *testing.T) {
	truncateTables(t)
	c := seedCounter(t, 3, 2)

	require.NoError(t, RefundEntitlementCount(c.Id))
	var after UserSubscriptionEntitlement
	require.NoError(t, DB.First(&after, c.Id).Error)
	require.Equal(t, int64(1), after.UsedCount)
}

// 本期已被重置归零后再退还，不得把已用次数减成负数——
// 负的已用次数会让闸门凭空多放行一次。
func TestRefundEntitlementCount_NeverGoesNegative(t *testing.T) {
	truncateTables(t)
	c := seedCounter(t, 3, 0)

	require.NoError(t, RefundEntitlementCount(c.Id))
	var after UserSubscriptionEntitlement
	require.NoError(t, DB.First(&after, c.Id).Error)
	require.Equal(t, int64(0), after.UsedCount)
}
