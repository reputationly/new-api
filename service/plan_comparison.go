package service

import (
	"github.com/QuantumNous/new-api/model"
)

// 套餐对比表（对外换算表）。设计见 docs/subscription-entitlement-design.md §8.3。
//
// 后端只回答「哪个套餐覆盖了哪个展示模型、按什么条件」，点数换成秒数/张数由前端
// 用同一份价格记录算——与模型广场同一个分工：覆盖判定必须用扣费侧的 MatchesModel，
// 在 JS 里重写一遍通配匹配就是在制造第二个真相来源。

// PlanModelCoverage 某个套餐对某个展示模型的覆盖。
type PlanModelCoverage struct {
	ConsumePoints bool    `json:"consume_points"`
	Discount      float64 `json:"discount"`
	// ChannelLimited 该权益限定了渠道，换算值只在请求落到那些渠道时才成立。
	// 只给布尔不给渠道 ID：渠道配置是内部信息（见 AdminSubscriptionPlanDTO 的说明）。
	ChannelLimited bool `json:"channel_limited"`
}

// PlanCountLimit 一条限次权益。次数是硬承诺，与点数换算出来的估算值性质不同，
// 所以单独成段，不混进展示模型那几行。
type PlanCountLimit struct {
	Models      string `json:"models"`
	LimitCount  int64  `json:"limit_count"`
	ResetPeriod string `json:"reset_period"`
	// ChannelLimited 这条限次只在特定渠道上生效。
	ChannelLimited bool `json:"channel_limited"`
}

type PlanComparisonColumn struct {
	PlanId                 int                          `json:"plan_id"`
	ComputePointsPerPeriod int64                        `json:"compute_points_per_period"`
	Coverage               map[string]PlanModelCoverage `json:"coverage"`
	Limits                 []PlanCountLimit             `json:"limits"`
}

type PlanComparison struct {
	// Models 展示模型的价格记录，按运营配置的顺序；站点上已不存在的模型直接略过——
	// 保留一行空数据只会让客户以为这个模型还能用。
	Models []model.Pricing        `json:"models"`
	Plans  []PlanComparisonColumn `json:"plans"`
}

// BuildPlanComparison 生成对比表数据。
//
// 只收有算力点或有权益的套餐：老式按额度的套餐在这张表里每一格都是「—」，
// 放进来只是占列。一个都没有时返回 nil，前端据此整张表不出。
func BuildPlanComparison(
	plans []model.SubscriptionPlan,
	entsByPlan map[int][]*model.SubscriptionPlanEntitlement,
	showcase []string,
	pricing []model.Pricing,
) *PlanComparison {
	priceOf := make(map[string]model.Pricing, len(pricing))
	for _, p := range pricing {
		priceOf[p.ModelName] = p
	}
	models := make([]model.Pricing, 0, len(showcase))
	seen := make(map[string]struct{}, len(showcase))
	for _, name := range showcase {
		if _, dup := seen[name]; dup {
			continue
		}
		p, ok := priceOf[name]
		if !ok {
			continue
		}
		seen[name] = struct{}{}
		models = append(models, p)
	}

	columns := make([]PlanComparisonColumn, 0, len(plans))
	for _, plan := range plans {
		ents := entsByPlan[plan.Id]
		if plan.ComputePointsPerPeriod <= 0 && len(ents) == 0 {
			continue
		}
		col := PlanComparisonColumn{
			PlanId:                 plan.Id,
			ComputePointsPerPeriod: plan.ComputePointsPerPeriod,
			Coverage:               make(map[string]PlanModelCoverage),
			Limits:                 []PlanCountLimit{},
		}
		for _, m := range models {
			ent := planCoverageFor(ents, m.ModelName)
			if ent == nil {
				continue
			}
			discount := ent.ConsumeDiscount
			if discount <= 0 {
				discount = 1
			}
			col.Coverage[m.ModelName] = PlanModelCoverage{
				ConsumePoints:  ent.ConsumePoints,
				Discount:       discount,
				ChannelLimited: isChannelLimited(ent),
			}
		}
		for i, ent := range ents {
			if ent.LimitCount <= 0 || !limitReachable(ents, i, pricing) {
				continue
			}
			col.Limits = append(col.Limits, PlanCountLimit{
				Models:         ent.Models,
				LimitCount:     ent.LimitCount,
				ResetPeriod:    model.NormalizeResetPeriod(ent.ResetPeriod),
				ChannelLimited: isChannelLimited(ent),
			})
		}
		columns = append(columns, col)
	}
	if len(columns) == 0 {
		return nil
	}
	return &PlanComparison{Models: models, Plans: columns}
}

func isChannelLimited(ent *model.SubscriptionPlanEntitlement) bool {
	return len(ent.ChannelIdList()) > 0
}

// planCoverageFor 某个套餐对某个模型展示哪条权益。
//
// 与模型广场（model.ListUserEntitlementCoverage）同一条规则：取请求落在不限渠道时
// 扣费会命中的那条——套餐内按 sort_order 第一条不限渠道的。扣费侧遇到渠道不符的
// 权益会跳过继续往下找，所以排在前面的限渠道权益盖不住后面的。全是限渠道权益时
// 取第一条，由前端标注「仅特定渠道」。
func planCoverageFor(ents []*model.SubscriptionPlanEntitlement, name string) *model.SubscriptionPlanEntitlement {
	var firstLimited *model.SubscriptionPlanEntitlement
	for _, ent := range ents {
		if !ent.MatchesModel(name) {
			continue
		}
		if !isChannelLimited(ent) {
			return ent
		}
		if firstLimited == nil {
			firstLimited = ent
		}
	}
	return firstLimited
}

// limitReachable 第 i 条限次权益是否至少对站点上一个模型真正生效。
//
// 权益按顺序匹配、先命中先生效，编辑页对范围重叠只告警不拦截——「glm-* 不限次」
// 排在「glm-4-plus 500 次」前面是能存进来的，那 500 次永远不会被执行。对外承诺
// 一个扣费时根本不存在的上限，比不写更糟。
//
// 只有不限渠道的前序权益才算盖住：限渠道的那条对其他渠道会被跳过，后面这条照样
// 命中。部分被盖住的仍然按原范围列出：被盖住的那部分模型由前面的权益管，前面那条
// 若也限次，它自己的范围更具体、会作为单独一行出现在同一段里。
func limitReachable(ents []*model.SubscriptionPlanEntitlement, i int, pricing []model.Pricing) bool {
	for _, p := range pricing {
		if !ents[i].MatchesModel(p.ModelName) {
			continue
		}
		shadowed := false
		for _, prev := range ents[:i] {
			if !isChannelLimited(prev) && prev.MatchesModel(p.ModelName) {
				shadowed = true
				break
			}
		}
		if !shadowed {
			return true
		}
	}
	return false
}
