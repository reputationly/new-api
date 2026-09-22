package service

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/require"
)

// 权益在 NewBillingSession 里的接入行为。
//
// 单独测 EntitlementFunding 与 MatchUserEntitlement 是不够的：它们都正确，接线接错了
// 照样全绿。这里守的是两条只存在于接线处的性质——命中时真的走权益，以及**任何一个
// 维度不满足时必须降级而不是让请求失败**。降级是这条路径的核心语义：权益是优惠不是
// 准入门槛。
func seedMatchableEntitlement(t *testing.T, userId int, limit int64, lotPoints int64) {
	t.Helper()
	plan := &model.SubscriptionPlan{
		Title: "e2e", PriceAmount: 1, Currency: "CNY",
		DurationUnit: model.SubscriptionDurationMonth, DurationValue: 1, Enabled: true,
	}
	require.NoError(t, model.DB.Create(plan).Error)

	ent := &model.SubscriptionPlanEntitlement{
		PlanId: plan.Id, Models: "gpt-test", ConsumePoints: true,
		ConsumeDiscount: 1, LimitCount: limit, RateLimitRPM: 60,
	}
	require.NoError(t, model.DB.Create(ent).Error)

	now := model.GetDBTimestamp()
	sub := &model.UserSubscription{
		UserId: userId, PlanId: plan.Id, Status: "active",
		StartTime: now - 3600, EndTime: now + 3600,
	}
	require.NoError(t, model.DB.Create(sub).Error)
	require.NoError(t, model.DB.Create(&model.UserSubscriptionEntitlement{
		UserId: userId, UserSubscriptionId: sub.Id, PlanEntitlementId: ent.Id,
		LimitCount: limit,
	}).Error)

	if lotPoints > 0 {
		require.NoError(t, model.DB.Create(&model.ComputePointLot{
			UserId: userId, Source: model.ComputePointLotSourceSubscription, RefId: sub.Id,
			PointsTotal: lotPoints, ExpiresAt: now + 3600,
			Status: model.ComputePointLotStatusActive,
		}).Error)
	}
}

// IsPlayground 置 true 只为绕开令牌额度扣减那一段：本包 TestMain 没初始化
// commonKeyCol，走到那里会拼出 ` = ?` 的残缺 SQL（既有测试文件里已记录这个缺口）。
// 资金来源的选择逻辑不读 IsPlayground，所以绕开它不影响这里要守的行为。
func relayInfoForModel(userId int, modelName string) *relaycommon.RelayInfo {
	info := newTestRelayInfoWithoutChannelMeta(userId, "default")
	info.OriginModelName = modelName
	info.IsPlayground = true
	// 降级到订阅额度那条路要求非空 requestId（幂等键），不给会在那里报错，
	// 让「降级是否成功」被一个与降级无关的原因掩盖掉。
	info.RequestId = "test-" + modelName
	return info
}

func TestNewBillingSession_UsesEntitlementWhenMatched(t *testing.T) {
	truncate(t)
	seedBillingUser(t, 90101, 1000000, 0)
	seedMatchableEntitlement(t, 90101, 10, 100000)

	session, apiErr := NewBillingSession(
		newTestGinContext(1),
		relayInfoForModel(90101, "gpt-test"),
		1000,
	)
	require.Nil(t, apiErr)
	require.NotNil(t, session)
	require.Equal(t, BillingSourceEntitlement, session.funding.Source(),
		"命中权益时应走权益扣费而不是钱包")
}

// 次数用尽 → 必须降级到钱包，而不是让请求失败。
func TestNewBillingSession_FallsBackWhenCountExhausted(t *testing.T) {
	truncate(t)
	seedBillingUser(t, 90102, 1000000, 0)
	seedMatchableEntitlement(t, 90102, 0, 100000) // LimitCount=0 在计数器上表示不限次

	// 把计数器改成「上限 1、已用 1」= 用尽
	require.NoError(t, model.DB.Model(&model.UserSubscriptionEntitlement{}).
		Where("user_id = ?", 90102).
		Updates(map[string]interface{}{"limit_count": 1, "used_count": 1}).Error)

	session, apiErr := NewBillingSession(
		newTestGinContext(1),
		relayInfoForModel(90102, "gpt-test"),
		1000,
	)
	require.Nil(t, apiErr, "次数用尽不该让请求失败")
	require.NotNil(t, session)
	require.NotEqual(t, BillingSourceEntitlement, session.funding.Source(),
		"次数用尽必须降级到现有资金链路")
}

// 算力点不足 → 同样降级，且不得白烧一次调用配额。
func TestNewBillingSession_FallsBackWhenPointsInsufficient(t *testing.T) {
	truncate(t)
	seedBillingUser(t, 90103, 1000000, 0)
	seedMatchableEntitlement(t, 90103, 10, 100) // 批次只有 100，请求要 1000

	session, apiErr := NewBillingSession(
		newTestGinContext(1),
		relayInfoForModel(90103, "gpt-test"),
		1000,
	)
	require.Nil(t, apiErr)
	require.NotNil(t, session)
	require.NotEqual(t, BillingSourceEntitlement, session.funding.Source())

	var counter model.UserSubscriptionEntitlement
	require.NoError(t, model.DB.Where("user_id = ?", 90103).First(&counter).Error)
	require.Equal(t, int64(0), counter.UsedCount,
		"降级走钱包时不得白白烧掉一次配额")
}

// 模型不在任何权益范围内 → 走原有链路，行为与加权益之前一致。
func TestNewBillingSession_UnmatchedModelKeepsLegacyPath(t *testing.T) {
	truncate(t)
	seedBillingUser(t, 90104, 1000000, 0)
	seedMatchableEntitlement(t, 90104, 10, 100000)

	session, apiErr := NewBillingSession(
		newTestGinContext(1),
		relayInfoForModel(90104, "other-model"),
		1000,
	)
	require.Nil(t, apiErr)
	require.NotNil(t, session)
	require.NotEqual(t, BillingSourceEntitlement, session.funding.Source())
}

// 追加预扣的接线。
//
// 直接测 EntitlementFunding.reserveExtra 是不够的：reserveFunding 的 switch 里漏掉
// 权益分支时，那些单元测试照样全绿，而真实路径会落到 default 的
// 「unsupported funding source」直接失败。这条守的就是接线本身。
func TestBillingSessionReserve_EntitlementTakesMorePoints(t *testing.T) {
	truncate(t)
	seedBillingUser(t, 90201, 1000000, 0)
	seedMatchableEntitlement(t, 90201, 10, 100000)

	session, apiErr := NewBillingSession(
		newTestGinContext(1),
		relayInfoForModel(90201, "gpt-test"),
		1000,
	)
	require.Nil(t, apiErr)
	require.Equal(t, BillingSourceEntitlement, session.funding.Source())
	require.Equal(t, int64(1000), lotUsed(t, 90201))

	// 请求中途发现用量超预估，补到 1500
	require.NoError(t, session.Reserve(1500))
	require.Equal(t, int64(1500), lotUsed(t, 90201), "追加预扣应继续扣算力点")
}

// 算力点不够时追加预扣必须拒绝：服务未交付，不能由平台兜底，
// 也不能像预扣阶段那样降级——那时早已过去。
func TestBillingSessionReserve_EntitlementInsufficientRejects(t *testing.T) {
	truncate(t)
	seedBillingUser(t, 90202, 1000000, 0)
	seedMatchableEntitlement(t, 90202, 10, 1200)

	session, apiErr := NewBillingSession(
		newTestGinContext(1),
		relayInfoForModel(90202, "gpt-test"),
		1000,
	)
	require.Nil(t, apiErr)
	require.Equal(t, BillingSourceEntitlement, session.funding.Source())

	err := session.Reserve(9000)
	require.Error(t, err, "算力点不足时追加预扣必须失败")
	require.Equal(t, int64(1000), lotUsed(t, 90202), "失败不得留下半笔")
}
