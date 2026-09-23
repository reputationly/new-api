package model

import (
	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// 用户侧套餐详情的余量数据（设计文档 §10.2）：每个活跃订阅的算力点余量与各条权益的
// 次数状态。「我的订阅」此前只能看到老式的总额度，套餐的两个核心维度——点数和次数——
// 用户在任何地方都看不到还剩多少，只能等到被按余额扣费时才发现用完了。

// SubscriptionComputePoints 某个订阅发放的算力点当前余量（quota unit，展示时由前端换算）。
//
// 按订阅统计的是「这个套餐发的点还剩多少」。扣费时点数是跨订阅共用的——先烧快过期的
// 批次，不看是哪个套餐发的——所以持有多个套餐时，某个套餐的点可能被另一个套餐覆盖的
// 模型用掉。单套餐（绝大多数情况）两者一致。
type SubscriptionComputePoints struct {
	Total     int64 `json:"total"`
	Used      int64 `json:"used"`
	Available int64 `json:"available"`
	// ExpiresAt 未过期批次里最早的到期时间，0 = 不过期。点数不结转，到期即作废。
	ExpiresAt int64 `json:"expires_at"`
}

// SubscriptionEntitlementStatus 某个订阅下一条权益的本期状态。
type SubscriptionEntitlementStatus struct {
	EntitlementId int     `json:"entitlement_id"`
	Models        string  `json:"models"`
	ConsumePoints bool    `json:"consume_points"`
	Discount      float64 `json:"discount"`
	// LimitCount=0 表示不限次
	LimitCount    int64  `json:"limit_count"`
	UsedCount     int64  `json:"used_count"`
	ResetPeriod   string `json:"reset_period"`
	NextResetTime int64  `json:"next_reset_time"`
	RateLimitRPM  int    `json:"rate_limit_rpm"`
	// ChannelLimited 只给布尔不给渠道 ID：渠道配置是内部信息
	ChannelLimited bool `json:"channel_limited"`
}

// AttachSubscriptionUsage 给订阅摘要补上算力点余量与权益次数状态。
//
// 三张表各一次批量查询，不按订阅逐个查：这是「我的订阅」每次打开都会走的路径。
func AttachSubscriptionUsage(summaries []SubscriptionSummary) error {
	if len(summaries) == 0 {
		return nil
	}
	subIds := make([]int, 0, len(summaries))
	planIds := make([]int, 0, len(summaries))
	seenPlan := make(map[int]struct{}, len(summaries))
	for _, s := range summaries {
		if s.Subscription == nil {
			continue
		}
		subIds = append(subIds, s.Subscription.Id)
		if _, ok := seenPlan[s.Subscription.PlanId]; !ok {
			seenPlan[s.Subscription.PlanId] = struct{}{}
			planIds = append(planIds, s.Subscription.PlanId)
		}
	}
	if len(subIds) == 0 {
		return nil
	}

	// 与 GetComputePointBalance / TryConsumeComputePoints 同一个过期判定，否则用户会看到
	// 「显示有余额但花不出去」
	now := GetDBTimestamp()
	var lots []ComputePointLot
	if err := DB.Where("source = ? AND ref_id IN ? AND (expires_at = 0 OR expires_at > ?)",
		ComputePointLotSourceSubscription, subIds, now).Find(&lots).Error; err != nil {
		return err
	}
	pointsBySub := make(map[int]*SubscriptionComputePoints, len(subIds))
	for _, lot := range lots {
		p := pointsBySub[lot.RefId]
		if p == nil {
			p = &SubscriptionComputePoints{}
			pointsBySub[lot.RefId] = p
		}
		p.Total += lot.PointsTotal
		p.Used += lot.PointsUsed
		if lot.ExpiresAt > 0 && (p.ExpiresAt == 0 || lot.ExpiresAt < p.ExpiresAt) {
			p.ExpiresAt = lot.ExpiresAt
		}
	}
	for _, p := range pointsBySub {
		p.Available = p.Total - p.Used
		if p.Available < 0 {
			p.Available = 0
		}
	}

	var ents []SubscriptionPlanEntitlement
	if err := DB.Where("plan_id IN ?", planIds).
		Order("plan_id asc, sort_order asc, id asc").Find(&ents).Error; err != nil {
		return err
	}
	entsByPlan := make(map[int][]SubscriptionPlanEntitlement, len(planIds))
	for _, e := range ents {
		entsByPlan[e.PlanId] = append(entsByPlan[e.PlanId], e)
	}
	var counters []UserSubscriptionEntitlement
	if err := DB.Where("user_subscription_id IN ?", subIds).Find(&counters).Error; err != nil {
		return err
	}
	counterOf := make(map[[2]int]UserSubscriptionEntitlement, len(counters))
	for _, c := range counters {
		counterOf[[2]int{c.UserSubscriptionId, c.PlanEntitlementId}] = c
	}

	for i := range summaries {
		sub := summaries[i].Subscription
		if sub == nil {
			continue
		}
		// 套餐名随摘要下发：mobile 没有套餐列表可查，classic 也省一次按 id 对表
		if plan, err := getSubscriptionPlanByIdTx(nil, sub.PlanId); err == nil && plan != nil {
			summaries[i].PlanTitle = plan.Title
		}
		summaries[i].ComputePoints = pointsBySub[sub.Id]
		statuses := make([]SubscriptionEntitlementStatus, 0)
		for _, ent := range entsByPlan[sub.PlanId] {
			counter, ok := counterOf[[2]int{sub.Id, ent.Id}]
			if !ok {
				// 权益是这笔订阅售出之后才加的，计数器还没实例化——扣费侧不会命中它，
				// 列出来就是承诺一个用户拿不到的权益。与模型广场的覆盖判定同口径。
				continue
			}
			discount := ent.ConsumeDiscount
			if discount <= 0 {
				discount = 1
			}
			statuses = append(statuses, SubscriptionEntitlementStatus{
				EntitlementId:  ent.Id,
				Models:         ent.Models,
				ConsumePoints:  ent.ConsumePoints,
				Discount:       discount,
				LimitCount:     counter.LimitCount,
				UsedCount:      counter.UsedCount,
				ResetPeriod:    NormalizeResetPeriod(ent.ResetPeriod),
				NextResetTime:  counter.NextResetTime,
				RateLimitRPM:   ent.RateLimitRPM,
				ChannelLimited: len(ent.ChannelIdList()) > 0,
			})
		}
		summaries[i].Entitlements = statuses
	}
	return nil
}

// ---------------------------------------------------------------------------
// 老式订阅额度：「无」与「不限」
// ---------------------------------------------------------------------------
//
// total_amount = 0 在老式套餐里表示「额度不限」。新式套餐（配了算力点或权益）的运营
// 留 0，本意是「不提供通用额度」——套餐外的请求按余额计费。沿用老语义的话，一次请求
// 没走成权益（模型不在套餐里、次数用完、点数不够）就会落到老式订阅额度上，而那边是
// 无限的：等于任意模型免费用。
//
// 规则只在这里定义一次：扣费（PreConsumeUserSubscription）、用户侧展示、管理端展示
// 全部读它，前端不自己推断。

// noLegacyQuota 额度为 0 且套餐是新式（配了算力点或权益）→ 没有老式额度。
func noLegacyQuota(amountTotal int64, plan *SubscriptionPlan, hasEntitlements bool) bool {
	if amountTotal != 0 || plan == nil {
		return false
	}
	return plan.ComputePointsPerPeriod > 0 || hasEntitlements
}

// PlanHasNoLegacyQuota 套餐层面的判定，供套餐列表（用户侧、管理端）展示。
func PlanHasNoLegacyQuota(plan *SubscriptionPlan, hasEntitlements bool) bool {
	if plan == nil {
		return false
	}
	return noLegacyQuota(plan.TotalAmount, plan, hasEntitlements)
}

// planHasEntitlementsTx 套餐是否配了任何权益。
func planHasEntitlementsTx(tx *gorm.DB, planId int) (bool, error) {
	if tx == nil {
		tx = DB
	}
	var count int64
	if err := tx.Model(&SubscriptionPlanEntitlement{}).
		Where("plan_id = ?", planId).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// subscriptionHasNoLegacyQuotaTx 扣费侧的判定。额度不为 0 时不查库（老式限额套餐
// 走不到这里的查询）；为 0 时才看套餐是不是新式。
func subscriptionHasNoLegacyQuotaTx(tx *gorm.DB, sub *UserSubscription, plan *SubscriptionPlan) (bool, error) {
	if sub.AmountTotal != 0 || plan == nil {
		return false, nil
	}
	if plan.ComputePointsPerPeriod > 0 {
		return true, nil
	}
	return planHasEntitlementsTx(tx, plan.Id)
}

// attachLegacyQuotaFlags 给订阅摘要标上 NoLegacyQuota。纯展示，失败只记日志。
func attachLegacyQuotaFlags(summaries []SubscriptionSummary) {
	planIds := make([]int, 0, len(summaries))
	seen := make(map[int]struct{}, len(summaries))
	for _, s := range summaries {
		if s.Subscription == nil || s.Subscription.AmountTotal != 0 {
			continue
		}
		if _, ok := seen[s.Subscription.PlanId]; ok {
			continue
		}
		seen[s.Subscription.PlanId] = struct{}{}
		planIds = append(planIds, s.Subscription.PlanId)
	}
	if len(planIds) == 0 {
		return
	}
	var withEnts []int
	if err := DB.Model(&SubscriptionPlanEntitlement{}).
		Where("plan_id IN ?", planIds).Distinct().Pluck("plan_id", &withEnts).Error; err != nil {
		common.SysLog("attach legacy quota flags failed: " + err.Error())
		return
	}
	hasEnts := make(map[int]bool, len(withEnts))
	for _, id := range withEnts {
		hasEnts[id] = true
	}
	for i := range summaries {
		sub := summaries[i].Subscription
		if sub == nil || sub.AmountTotal != 0 {
			continue
		}
		plan, err := getSubscriptionPlanByIdTx(nil, sub.PlanId)
		if err != nil {
			continue
		}
		summaries[i].NoLegacyQuota = noLegacyQuota(sub.AmountTotal, plan, hasEnts[sub.PlanId])
	}
}
