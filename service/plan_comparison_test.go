package service

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func comparisonPricing() []model.Pricing {
	return []model.Pricing{
		{ModelName: "ltx2.5", QuotaType: 1, ModelPrice: 0.1},
		{ModelName: "img-hd", QuotaType: 1, ModelPrice: 0.04},
		{ModelName: "gpt-5", QuotaType: 0, ModelRatio: 2.5},
	}
}

func ent(planId, sort int, models string, over func(*model.SubscriptionPlanEntitlement)) *model.SubscriptionPlanEntitlement {
	e := &model.SubscriptionPlanEntitlement{
		PlanId: planId, SortOrder: sort, Models: models,
		ConsumePoints: true, ConsumeDiscount: 1, ResetPeriod: "monthly",
	}
	if over != nil {
		over(e)
	}
	return e
}

func TestBuildPlanComparison_SkipsLegacyQuotaPlans(t *testing.T) {
	plans := []model.SubscriptionPlan{
		{Id: 1, ComputePointsPerPeriod: 0},     // 老式按额度套餐
		{Id: 2, ComputePointsPerPeriod: 50000}, // 算力点套餐
	}
	got := BuildPlanComparison(plans, nil, nil, comparisonPricing())
	require.NotNil(t, got)
	require.Len(t, got.Plans, 1)
	require.Equal(t, 2, got.Plans[0].PlanId)
}

// 一个算力点套餐都没有时整张表不出——返回空表的话前端会画出一个只有表头的框。
func TestBuildPlanComparison_NilWhenNoComputePlans(t *testing.T) {
	plans := []model.SubscriptionPlan{{Id: 1}}
	require.Nil(t, BuildPlanComparison(plans, nil, []string{"gpt-5"}, comparisonPricing()))
}

// 展示模型按运营配置的顺序输出；已下架的模型略过，重复的去重。
func TestBuildPlanComparison_ModelsFollowConfigOrder(t *testing.T) {
	plans := []model.SubscriptionPlan{{Id: 1, ComputePointsPerPeriod: 1}}
	got := BuildPlanComparison(plans, nil,
		[]string{"img-hd", "gone-model", "ltx2.5", "img-hd"}, comparisonPricing())
	names := make([]string, 0, len(got.Models))
	for _, m := range got.Models {
		names = append(names, m.ModelName)
	}
	require.Equal(t, []string{"img-hd", "ltx2.5"}, names)
}

// 覆盖判定必须与扣费侧同一套规则：通配符 + 套餐内 sort_order 第一条命中。
func TestBuildPlanComparison_CoverageUsesBillingMatchOrder(t *testing.T) {
	plans := []model.SubscriptionPlan{{Id: 1, ComputePointsPerPeriod: 1}}
	ents := map[int][]*model.SubscriptionPlanEntitlement{
		1: {
			ent(1, 1, "ltx*", func(e *model.SubscriptionPlanEntitlement) { e.ConsumeDiscount = 0.5 }),
			ent(1, 2, "ltx2.5", nil), // 被上一条盖住，扣费永远走不到
		},
	}
	got := BuildPlanComparison(plans, ents, []string{"ltx2.5", "gpt-5"}, comparisonPricing())
	cov := got.Plans[0].Coverage
	require.Contains(t, cov, "ltx2.5")
	require.Equal(t, 0.5, cov["ltx2.5"].Discount)
	// 套餐没覆盖的模型不能出现：点数花不到它身上，换算出数字就是虚假宣传
	require.NotContains(t, cov, "gpt-5")
}

func TestBuildPlanComparison_ChannelLimitedIsFlaggedWithoutIds(t *testing.T) {
	plans := []model.SubscriptionPlan{{Id: 1, ComputePointsPerPeriod: 1}}
	ents := map[int][]*model.SubscriptionPlanEntitlement{
		1: {ent(1, 1, "img-hd", func(e *model.SubscriptionPlanEntitlement) { e.ChannelIds = "3,5" })},
	}
	got := BuildPlanComparison(plans, ents, []string{"img-hd"}, comparisonPricing())
	require.True(t, got.Plans[0].Coverage["img-hd"].ChannelLimited)
}

// 次数是硬承诺，单独列出；不限次的权益不进这一段。
func TestBuildPlanComparison_LimitsListOnlyCountedEntitlements(t *testing.T) {
	plans := []model.SubscriptionPlan{{Id: 1, ComputePointsPerPeriod: 1}}
	ents := map[int][]*model.SubscriptionPlanEntitlement{
		1: {
			ent(1, 1, "gpt-5", func(e *model.SubscriptionPlanEntitlement) { e.LimitCount = 500 }),
			ent(1, 2, "img-*", nil),
		},
	}
	got := BuildPlanComparison(plans, ents, nil, comparisonPricing())
	require.Equal(t, []PlanCountLimit{{Models: "gpt-5", LimitCount: 500, ResetPeriod: "monthly"}},
		got.Plans[0].Limits)
}

// 只有权益、没配算力点的套餐也要出列：它的次数上限同样是要告诉客户的承诺。
func TestBuildPlanComparison_EntitlementOnlyPlanIsIncluded(t *testing.T) {
	plans := []model.SubscriptionPlan{{Id: 1, ComputePointsPerPeriod: 0}}
	ents := map[int][]*model.SubscriptionPlanEntitlement{
		1: {ent(1, 1, "gpt-5", func(e *model.SubscriptionPlanEntitlement) { e.LimitCount = 100 })},
	}
	got := BuildPlanComparison(plans, ents, nil, comparisonPricing())
	require.NotNil(t, got)
	require.Len(t, got.Plans, 1)
}

// 排在前面的限渠道权益盖不住后面的：扣费侧对其他渠道会跳过它。
// 与模型广场同一条规则——取请求落在不限渠道时扣费命中的那条。
func TestBuildPlanComparison_CoverageSkipsChannelLimitedFirst(t *testing.T) {
	plans := []model.SubscriptionPlan{{Id: 1, ComputePointsPerPeriod: 1}}
	ents := map[int][]*model.SubscriptionPlanEntitlement{
		1: {
			ent(1, 1, "img-hd", func(e *model.SubscriptionPlanEntitlement) {
				e.ChannelIds = "7"
				e.ConsumeDiscount = 0.1
			}),
			ent(1, 2, "img-hd", func(e *model.SubscriptionPlanEntitlement) { e.ConsumeDiscount = 0.5 }),
		},
	}
	got := BuildPlanComparison(plans, ents, []string{"img-hd"}, comparisonPricing())
	cov := got.Plans[0].Coverage["img-hd"]
	require.False(t, cov.ChannelLimited)
	require.Equal(t, 0.5, cov.Discount)
}

func TestBuildPlanComparison_CoverageAllChannelLimitedTakesFirst(t *testing.T) {
	plans := []model.SubscriptionPlan{{Id: 1, ComputePointsPerPeriod: 1}}
	ents := map[int][]*model.SubscriptionPlanEntitlement{
		1: {
			ent(1, 1, "img-hd", func(e *model.SubscriptionPlanEntitlement) {
				e.ChannelIds = "7"
				e.ConsumeDiscount = 0.4
			}),
			ent(1, 2, "img-hd", func(e *model.SubscriptionPlanEntitlement) {
				e.ChannelIds = "8"
				e.ConsumeDiscount = 0.9
			}),
		},
	}
	got := BuildPlanComparison(plans, ents, []string{"img-hd"}, comparisonPricing())
	cov := got.Plans[0].Coverage["img-hd"]
	require.True(t, cov.ChannelLimited)
	require.Equal(t, 0.4, cov.Discount)
}

// 被前面不限渠道的权益整个盖住的次数上限，扣费时永远不会执行，不能对外承诺。
func TestBuildPlanComparison_DropsFullyShadowedLimit(t *testing.T) {
	plans := []model.SubscriptionPlan{{Id: 1, ComputePointsPerPeriod: 1}}
	ents := map[int][]*model.SubscriptionPlanEntitlement{
		1: {
			ent(1, 1, "gpt-*", nil), // 不限次
			ent(1, 2, "gpt-5", func(e *model.SubscriptionPlanEntitlement) { e.LimitCount = 500 }),
		},
	}
	got := BuildPlanComparison(plans, ents, nil, comparisonPricing())
	require.Empty(t, got.Plans[0].Limits)
}

// 前面那条限了渠道就不算盖住：请求落到其他渠道时后面这条照样命中。
func TestBuildPlanComparison_ChannelLimitedDoesNotShadowLimit(t *testing.T) {
	plans := []model.SubscriptionPlan{{Id: 1, ComputePointsPerPeriod: 1}}
	ents := map[int][]*model.SubscriptionPlanEntitlement{
		1: {
			ent(1, 1, "gpt-*", func(e *model.SubscriptionPlanEntitlement) { e.ChannelIds = "7" }),
			ent(1, 2, "gpt-5", func(e *model.SubscriptionPlanEntitlement) { e.LimitCount = 500 }),
		},
	}
	got := BuildPlanComparison(plans, ents, nil, comparisonPricing())
	require.Len(t, got.Plans[0].Limits, 1)
	require.False(t, got.Plans[0].Limits[0].ChannelLimited)
}

// 部分被盖住的仍然列出：gpt-* 里除了 gpt-5 以外的模型确实受这 500 次约束。
func TestBuildPlanComparison_KeepsPartiallyShadowedLimit(t *testing.T) {
	plans := []model.SubscriptionPlan{{Id: 1, ComputePointsPerPeriod: 1}}
	pricing := append(comparisonPricing(), model.Pricing{ModelName: "gpt-5-mini", ModelRatio: 1})
	ents := map[int][]*model.SubscriptionPlanEntitlement{
		1: {
			ent(1, 1, "gpt-5", nil),
			ent(1, 2, "gpt-*", func(e *model.SubscriptionPlanEntitlement) { e.LimitCount = 500 }),
		},
	}
	got := BuildPlanComparison(plans, ents, nil, pricing)
	require.Len(t, got.Plans[0].Limits, 1)
	require.Equal(t, "gpt-*", got.Plans[0].Limits[0].Models)
}

// 范围在站点上匹配不到任何模型：这条上限对客户没有意义。
func TestBuildPlanComparison_DropsLimitMatchingNoSiteModel(t *testing.T) {
	plans := []model.SubscriptionPlan{{Id: 1, ComputePointsPerPeriod: 1}}
	ents := map[int][]*model.SubscriptionPlanEntitlement{
		1: {ent(1, 1, "retired-*", func(e *model.SubscriptionPlanEntitlement) { e.LimitCount = 100 })},
	}
	got := BuildPlanComparison(plans, ents, nil, comparisonPricing())
	require.Empty(t, got.Plans[0].Limits)
}

func TestBuildPlanComparison_LimitCarriesChannelFlag(t *testing.T) {
	plans := []model.SubscriptionPlan{{Id: 1, ComputePointsPerPeriod: 1}}
	ents := map[int][]*model.SubscriptionPlanEntitlement{
		1: {ent(1, 1, "gpt-5", func(e *model.SubscriptionPlanEntitlement) {
			e.LimitCount = 100
			e.ChannelIds = "3"
		})},
	}
	got := BuildPlanComparison(plans, ents, nil, comparisonPricing())
	require.True(t, got.Plans[0].Limits[0].ChannelLimited)
}
