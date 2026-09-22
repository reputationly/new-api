package service

import (
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// 权益「最坏成本」估算。设计见 docs/subscription-entitlement-design.md §9.1。
//
// 运营给外采模型配次数上限时，唯一要看的数字是「这条权益最坏情况会花掉我多少钱」——
// 客户把次数全部打在最贵的那个模型上。这个数与套餐售价并排显示，保证最坏不亏。
//
// 成本口径与记账侧完全一致（model/log.go 的 appendUpstreamCost）：
//
//	单次外采成本 = 模型单价 × QuotaPerUnit × cost_ratio
//
// 其中「模型单价」是未打分组折扣的基准价——分组折扣影响的是**收客户多少**，
// 不影响**付供应商多少**，把它折进来会系统性低估成本。
//
// 刻意只估算算得准的：按次计费与视频 per_call 有明确单价；按量计费与视频
// per_second / token 模式的单次成本取决于 token 数或时长，配置时根本不知道。
// 那些一律如实标注「无法估算」，而不是拍一个 token 数糊弄过去——这个数字存在的
// 意义就是「保证最坏不亏」，给一个基于臆测的数比不给更危险。
const (
	EntitlementCostReasonPerToken    = "按量计费，单次成本取决于 token 数，无法在配置时估算"
	EntitlementCostReasonPerSecond   = "视频按秒计费，单次成本取决于时长，无法在配置时估算"
	EntitlementCostReasonVideoToken  = "视频按 token 计费，单次成本取决于用量，无法在配置时估算"
	EntitlementCostReasonNoCostRatio = "所选渠道均未配置外采成本，无法估算"
	EntitlementCostReasonNoPrice     = "未配置单价，无法估算"
)

// EntitlementModelCost 单个模型的成本估算结果。
type EntitlementModelCost struct {
	Model string `json:"model"`
	// UnitQuota 单次外采成本（quota unit）。Resolvable 为 false 时无意义。
	UnitQuota int64 `json:"unit_quota"`
	// CostRatio 取自所选渠道里最贵的那个——最坏成本要按最贵的算。
	CostRatio  float64 `json:"cost_ratio"`
	Resolvable bool    `json:"resolvable"`
	Reason     string  `json:"reason,omitempty"`
}

// EntitlementCostEstimate 一条权益的最坏成本。
type EntitlementCostEstimate struct {
	LimitCount int64 `json:"limit_count"`
	// WorstModel / WorstUnitQuota 最贵的那个模型及其单次成本。
	WorstModel     string `json:"worst_model"`
	WorstUnitQuota int64  `json:"worst_unit_quota"`
	// WorstTotalQuota = LimitCount × WorstUnitQuota，即**单个重置窗口**的最坏成本。
	// LimitCount<=0（不限次）时为 0：不限次的最坏成本是无穷大，给任何有限数字
	// 都是错的，由前端提示「不限次」。
	WorstTotalQuota int64 `json:"worst_total_quota"`
	// ResetWindows 一个套餐周期内会经历几个次数窗口。次数上限是**每窗口**的额度，
	// 而售价对应**整个套餐周期**，月付套餐配每周重置时客户实际能用四五倍的量。
	ResetWindows int `json:"reset_windows"`
	// WorstPeriodQuota = ResetWindows × WorstTotalQuota，这才是能和售价直接比大小的数。
	WorstPeriodQuota int64 `json:"worst_period_quota"`
	// ResetWindowsCapped 窗口数撞到护栏上限（自定义周期填了极小的秒数）。
	ResetWindowsCapped bool                   `json:"reset_windows_capped"`
	Unlimited          bool                   `json:"unlimited"`
	Models             []EntitlementModelCost `json:"models"`
}

// CostRatioLookup / ModelChannelsLookup 是为了让成本口径可被单元测试直接验证而留的
// 注入点。真实数据来自 model 包的缓存，而那份缓存由渠道、能力表、倍率配置共同推导，
// 在测试里把它完整喂出来既笨重又脆弱——而这里要守住的恰恰是那几行乘法。
type CostRatioLookup func(channelId int, modelName string) (float64, bool)
type ModelChannelsLookup func(modelName string) []int

// EstimateEntitlementWorstCost 估算一条权益的最坏外采成本。
//
// modelPatterns 支持通配符；channelIds 为空表示不限渠道，此时在该模型的全部可用
// 渠道里取最贵的。limitCount<=0 表示不限次。
func EstimateEntitlementWorstCost(
	modelPatterns []string,
	channelIds []int,
	limitCount int64,
	resetWindows int,
	windowsCapped bool,
) *EntitlementCostEstimate {
	return estimateWorstCost(model.GetPricing(), model.GetChannelModelCostRatio,
		model.GetModelEnableChannels, modelPatterns, channelIds, limitCount,
		resetWindows, windowsCapped)
}

func estimateWorstCost(
	pricing []model.Pricing,
	costOf CostRatioLookup,
	channelsOf ModelChannelsLookup,
	modelPatterns []string,
	channelIds []int,
	limitCount int64,
	resetWindows int,
	windowsCapped bool,
) *EntitlementCostEstimate {
	if resetWindows < 1 {
		resetWindows = 1
	}
	est := &EntitlementCostEstimate{
		LimitCount:         limitCount,
		Unlimited:          limitCount <= 0,
		ResetWindows:       resetWindows,
		ResetWindowsCapped: windowsCapped,
		Models:             make([]EntitlementModelCost, 0),
	}

	matched := expandModelPatterns(pricing, modelPatterns)
	for _, name := range matched {
		item := estimateModelCost(pricing, costOf, channelsOf, name, channelIds)
		est.Models = append(est.Models, item)
		if item.Resolvable && item.UnitQuota > est.WorstUnitQuota {
			est.WorstUnitQuota = item.UnitQuota
			est.WorstModel = name
		}
	}
	if !est.Unlimited && est.WorstUnitQuota > 0 {
		est.WorstTotalQuota = limitCount * est.WorstUnitQuota
		est.WorstPeriodQuota = est.WorstTotalQuota * int64(resetWindows)
	}
	return est
}

// expandModelPatterns 把带通配符的范围展开成站点上真实存在的模型名。
// 展开而不是直接拿模式去查价：模式本身没有价格，必须落到具体模型才谈得上成本。
func expandModelPatterns(pricing []model.Pricing, patterns []string) []string {
	cleaned := make([]string, 0, len(patterns))
	for _, p := range patterns {
		if p = strings.TrimSpace(p); p != "" {
			cleaned = append(cleaned, p)
		}
	}
	if len(cleaned) == 0 {
		return nil
	}
	probe := &model.SubscriptionPlanEntitlement{Models: strings.Join(cleaned, ",")}

	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, p := range pricing {
		if _, ok := seen[p.ModelName]; ok {
			continue
		}
		if probe.MatchesModel(p.ModelName) {
			seen[p.ModelName] = struct{}{}
			out = append(out, p.ModelName)
		}
	}
	return out
}

func estimateModelCost(
	pricing []model.Pricing,
	costOf CostRatioLookup,
	channelsOf ModelChannelsLookup,
	name string,
	channelIds []int,
) EntitlementModelCost {
	item := EntitlementModelCost{Model: name}

	unitPrice, reason := modelUnitPrice(pricing, name)
	if reason != "" {
		item.Reason = reason
		return item
	}

	ratio, ok := maxCostRatioForModel(costOf, channelsOf, name, channelIds)
	if !ok {
		item.Reason = EntitlementCostReasonNoCostRatio
		return item
	}

	item.CostRatio = ratio
	item.UnitQuota = int64(unitPrice * common.QuotaPerUnit * ratio)
	item.Resolvable = true
	return item
}

// modelUnitPrice 返回单次调用的基准单价（美元口径，与 ModelPrice / 视频矩阵同单位）。
// 返回的 reason 非空表示这个模型的单次成本在配置时不可知。
func modelUnitPrice(pricing []model.Pricing, name string) (float64, string) {
	for _, p := range pricing {
		if p.ModelName != name {
			continue
		}
		if p.VideoPricing != nil && p.VideoPricing.Mode != "" {
			switch p.VideoPricing.Mode {
			case "per_call":
				if v, ok := maxNestedPrice(p.VideoPricing.PerCall); ok {
					return v, ""
				}
				return 0, EntitlementCostReasonNoPrice
			case "per_second":
				return 0, EntitlementCostReasonPerSecond
			default:
				return 0, EntitlementCostReasonVideoToken
			}
		}
		if p.QuotaType == 1 {
			if p.ModelPrice <= 0 {
				return 0, EntitlementCostReasonNoPrice
			}
			return p.ModelPrice, ""
		}
		return 0, EntitlementCostReasonPerToken
	}
	return 0, EntitlementCostReasonNoPrice
}

// maxNestedPrice 取视频 per_call 矩阵里最贵的一格——最坏情况就是客户专挑那一档打。
func maxNestedPrice(matrix map[string]map[string]float64) (float64, bool) {
	best := 0.0
	found := false
	for _, row := range matrix {
		for _, v := range row {
			if v > best {
				best = v
				found = true
			}
		}
	}
	return best, found && best > 0
}

// maxCostRatioForModel 在允许的渠道里取最贵的成本比。
//
// channelIds 为空 = 不限渠道，此时扫该模型的全部可用渠道。没配成本的渠道跳过
// 而不是当作 0：当作 0 会把「不知道成本」显示成「零成本」，正好是最危险的方向。
func maxCostRatioForModel(
	costOf CostRatioLookup,
	channelsOf ModelChannelsLookup,
	name string,
	channelIds []int,
) (float64, bool) {
	candidates := channelIds
	if len(candidates) == 0 {
		candidates = channelsOf(name)
	}
	best := 0.0
	found := false
	for _, chId := range candidates {
		ratio, ok := costOf(chId, name)
		if !ok {
			continue
		}
		if !found || ratio > best {
			best = ratio
			found = true
		}
	}
	return best, found
}

// ParseChannelIds 把逗号分隔的渠道 ID 串转成整数列表，非法项直接丢弃。
func ParseChannelIds(raw string) []int {
	out := make([]int, 0)
	for _, s := range strings.Split(raw, ",") {
		if s = strings.TrimSpace(s); s == "" {
			continue
		}
		if id, err := strconv.Atoi(s); err == nil && id > 0 {
			out = append(out, id)
		}
	}
	return out
}
