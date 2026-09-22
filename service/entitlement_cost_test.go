package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/require"
)

// 按次计费模型：单价明确，成本口径必须与记账侧一致
//
//	单次成本 = ModelPrice × QuotaPerUnit × cost_ratio
//
// 分组折扣刻意不参与：它影响收客户多少，不影响付供应商多少。
func perCallPricing() []model.Pricing {
	return []model.Pricing{
		{ModelName: "img-hd", QuotaType: 1, ModelPrice: 0.04},
		{ModelName: "img-cheap", QuotaType: 1, ModelPrice: 0.01},
		{ModelName: "gpt-5", QuotaType: 0, ModelRatio: 2.5},
	}
}

func fixedCost(ratio float64) CostRatioLookup {
	return func(int, string) (float64, bool) { return ratio, true }
}

func noChannels() ModelChannelsLookup {
	return func(string) []int { return nil }
}

func TestEstimateWorstCost_PerCallModel(t *testing.T) {
	est := estimateWorstCost(perCallPricing(), fixedCost(0.5), noChannels(),
		[]string{"img-hd"}, []int{3}, 500, 1, false)

	require.Len(t, est.Models, 1)
	require.True(t, est.Models[0].Resolvable)
	require.Equal(t, "img-hd", est.WorstModel)

	want := int64(0.04 * common.QuotaPerUnit * 0.5)
	require.Equal(t, want, est.WorstUnitQuota)
	require.Equal(t, want*500, est.WorstTotalQuota)
	require.False(t, est.Unlimited)
}

// 最坏情况是客户把次数全部打在最贵的模型上，所以取 max 而不是求和或取平均。
func TestEstimateWorstCost_PicksMostExpensiveModel(t *testing.T) {
	est := estimateWorstCost(perCallPricing(), fixedCost(1), noChannels(),
		[]string{"img-hd", "img-cheap"}, []int{3}, 10, 1, false)

	require.Equal(t, "img-hd", est.WorstModel)
	require.Equal(t, int64(0.04*common.QuotaPerUnit)*10, est.WorstTotalQuota)
}

// 按量计费没有「单次单价」这回事，必须如实说明，而不是拍一个 token 数糊弄。
func TestEstimateWorstCost_PerTokenModelIsUnresolvable(t *testing.T) {
	est := estimateWorstCost(perCallPricing(), fixedCost(1), noChannels(),
		[]string{"gpt-5"}, []int{3}, 500, 1, false)

	require.Len(t, est.Models, 1)
	require.False(t, est.Models[0].Resolvable)
	require.Equal(t, EntitlementCostReasonPerToken, est.Models[0].Reason)
	require.Equal(t, int64(0), est.WorstTotalQuota)
	require.Empty(t, est.WorstModel)
}

// 算不准的模型不能把算得准的那些也带沟里：混配时仍要给出可算部分的最坏值。
func TestEstimateWorstCost_MixedResolvableAndNot(t *testing.T) {
	est := estimateWorstCost(perCallPricing(), fixedCost(1), noChannels(),
		[]string{"img-hd", "gpt-5"}, []int{3}, 100, 1, false)

	require.Len(t, est.Models, 2)
	require.Equal(t, "img-hd", est.WorstModel)
	require.Equal(t, int64(0.04*common.QuotaPerUnit)*100, est.WorstTotalQuota)
}

// 不限次的最坏成本是无穷大，给任何有限数字都是错的。
func TestEstimateWorstCost_UnlimitedHasNoTotal(t *testing.T) {
	est := estimateWorstCost(perCallPricing(), fixedCost(1), noChannels(),
		[]string{"img-hd"}, []int{3}, 0, 1, false)

	require.True(t, est.Unlimited)
	require.Equal(t, int64(0), est.WorstTotalQuota)
	require.Greater(t, est.WorstUnitQuota, int64(0), "单次成本仍然算得出来")
}

// 没配成本的渠道要跳过，不能当成 0——那会把「不知道成本」显示成「零成本」，
// 正好是最危险的方向。
func TestEstimateWorstCost_MissingCostRatioIsUnresolvable(t *testing.T) {
	missing := func(int, string) (float64, bool) { return 0, false }
	est := estimateWorstCost(perCallPricing(), missing, noChannels(),
		[]string{"img-hd"}, []int{3}, 500, 1, false)

	require.False(t, est.Models[0].Resolvable)
	require.Equal(t, EntitlementCostReasonNoCostRatio, est.Models[0].Reason)
	require.Equal(t, int64(0), est.WorstTotalQuota)
}

// 多个渠道时取最贵的那个：最坏成本不能按便宜的渠道算。
func TestEstimateWorstCost_TakesMaxCostRatioAcrossChannels(t *testing.T) {
	perChannel := func(chId int, _ string) (float64, bool) {
		switch chId {
		case 1:
			return 0.2, true
		case 2:
			return 0.9, true
		}
		return 0, false
	}
	est := estimateWorstCost(perCallPricing(), perChannel, noChannels(),
		[]string{"img-hd"}, []int{1, 2}, 1, 1, false)

	require.Equal(t, 0.9, est.Models[0].CostRatio)
	require.Equal(t, int64(0.04*common.QuotaPerUnit*0.9), est.WorstUnitQuota)
}

// 未限定渠道时要扫该模型的全部可用渠道，而不是当作「没有渠道」直接算不出来。
func TestEstimateWorstCost_UnrestrictedChannelsFallsBackToModelChannels(t *testing.T) {
	channelsOf := func(string) []int { return []int{7} }
	costOf := func(chId int, _ string) (float64, bool) {
		if chId == 7 {
			return 0.3, true
		}
		return 0, false
	}
	est := estimateWorstCost(perCallPricing(), costOf, channelsOf,
		[]string{"img-hd"}, nil, 2, 1, false)

	require.True(t, est.Models[0].Resolvable)
	require.Equal(t, 0.3, est.Models[0].CostRatio)
}

// 通配符要展开成站点上真实存在的模型——模式本身没有价格。
func TestEstimateWorstCost_ExpandsWildcards(t *testing.T) {
	est := estimateWorstCost(perCallPricing(), fixedCost(1), noChannels(),
		[]string{"img-*"}, []int{3}, 1, 1, false)

	require.Len(t, est.Models, 2)
	require.Equal(t, "img-hd", est.WorstModel)
}

func TestEstimateWorstCost_EmptyPatternsYieldNothing(t *testing.T) {
	est := estimateWorstCost(perCallPricing(), fixedCost(1), noChannels(),
		[]string{"   ", ""}, []int{3}, 10, 1, false)
	require.Empty(t, est.Models)
	require.Equal(t, int64(0), est.WorstTotalQuota)
}

// ---- 视频计费矩阵 ----

func videoPricing(mode string) []model.Pricing {
	entry := &ratio_setting.VideoPriceEntry{Mode: mode}
	switch mode {
	case "per_call":
		entry.PerCall = map[string]map[string]float64{
			"720p":  {"5s": 0.2, "10s": 0.4},
			"1080p": {"5s": 0.5, "10s": 1.2},
		}
	case "per_second":
		entry.PerSecond = map[string]float64{"720p": 0.05}
	default:
		entry.Token = map[string]map[string]float64{"a": {"b": 1}}
	}
	return []model.Pricing{{ModelName: "vid", QuotaType: 1, ModelPrice: 0.01, VideoPricing: entry}}
}

// per_call 矩阵取最贵的一格：最坏情况就是客户专挑那一档打。
// 同时验证矩阵优先于 ModelPrice——后者是预扣锚点，不是真实单价。
func TestEstimateWorstCost_VideoPerCallTakesMostExpensiveCell(t *testing.T) {
	est := estimateWorstCost(videoPricing("per_call"), fixedCost(1), noChannels(),
		[]string{"vid"}, []int{3}, 10, 1, false)

	require.True(t, est.Models[0].Resolvable)
	require.Equal(t, int64(1.2*common.QuotaPerUnit), est.WorstUnitQuota)
}

func TestEstimateWorstCost_VideoPerSecondIsUnresolvable(t *testing.T) {
	est := estimateWorstCost(videoPricing("per_second"), fixedCost(1), noChannels(),
		[]string{"vid"}, []int{3}, 10, 1, false)

	require.False(t, est.Models[0].Resolvable)
	require.Equal(t, EntitlementCostReasonPerSecond, est.Models[0].Reason)
}

func TestEstimateWorstCost_VideoTokenIsUnresolvable(t *testing.T) {
	est := estimateWorstCost(videoPricing("token"), fixedCost(1), noChannels(),
		[]string{"vid"}, []int{3}, 10, 1, false)

	require.False(t, est.Models[0].Resolvable)
	require.Equal(t, EntitlementCostReasonVideoToken, est.Models[0].Reason)
}

func TestParseChannelIds(t *testing.T) {
	require.Equal(t, []int{3, 5}, ParseChannelIds(" 3 , 5 "))
	require.Equal(t, []int{}, ParseChannelIds(""))
	require.Equal(t, []int{}, ParseChannelIds("abc, -1, 0"))
}

// 次数上限是每个重置窗口的额度，售价对应整个套餐周期。月付套餐配每周重置时，
// 客户一个计费周期内能用四五倍的量——只按单窗口算会系统性低估最坏成本，
// 而低估正是「保证最坏不亏」最不能出的方向。
func TestEstimateWorstCost_ScalesByResetWindows(t *testing.T) {
	single := estimateWorstCost(perCallPricing(), fixedCost(1), noChannels(),
		[]string{"img-hd"}, []int{3}, 500, 1, false)
	weekly := estimateWorstCost(perCallPricing(), fixedCost(1), noChannels(),
		[]string{"img-hd"}, []int{3}, 500, 5, false)

	require.Equal(t, single.WorstTotalQuota, weekly.WorstTotalQuota,
		"单窗口成本不受窗口数影响")
	require.Equal(t, single.WorstTotalQuota*5, weekly.WorstPeriodQuota,
		"整个计费周期的最坏成本要按窗口数放大")
	require.Equal(t, 5, weekly.ResetWindows)
}

func TestEstimateWorstCost_UnlimitedHasNoPeriodTotal(t *testing.T) {
	est := estimateWorstCost(perCallPricing(), fixedCost(1), noChannels(),
		[]string{"img-hd"}, []int{3}, 0, 5, false)
	require.True(t, est.Unlimited)
	require.Equal(t, int64(0), est.WorstPeriodQuota)
}

// 窗口数缺省/非法时退回 1，而不是让总额变成 0 —— 后者会显示成「最坏成本 0」，
// 比不显示更糟。
func TestEstimateWorstCost_InvalidWindowsFallsBackToOne(t *testing.T) {
	est := estimateWorstCost(perCallPricing(), fixedCost(1), noChannels(),
		[]string{"img-hd"}, []int{3}, 10, 0, false)
	require.Equal(t, 1, est.ResetWindows)
	require.Equal(t, est.WorstTotalQuota, est.WorstPeriodQuota)
}
