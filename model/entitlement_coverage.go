package model

import (
	"github.com/QuantumNous/new-api/common"
)

// 模型广场的权益覆盖信息。设计见 docs/subscription-entitlement-design.md §8.2。
//
// 与 MatchUserEntitlement 的分工：那个是**扣费时**的判定，输入里有确定的 channelId；
// 这个是**展示时**的查询，一次算出全部模型的覆盖情况，而且没有渠道上下文。
//
// 两者的排序口径必须一致（活跃订阅按 end_time 近者优先 → 权益按 sort_order），
// 否则页面显示「专业版 8 折」、实际扣费走了别的权益，用户看到的价和账单对不上。
//
// 限渠道的权益是例外：扣费侧遇到不允许当前渠道的权益会跳过、继续往下找，而展示侧
// 不知道请求会落到哪个渠道。所以主展示取「不限渠道时扣费会命中的那条」——那是用户
// 确定拿得到的价；限渠道的条目进 All，由 tooltip 说明它只在特定渠道生效。

// EntitlementCoverageEntry 一条权益对某个模型的覆盖。
type EntitlementCoverageEntry struct {
	EntitlementId int    `json:"entitlement_id"`
	PlanId        int    `json:"plan_id"`
	PlanTitle     string `json:"plan_title"`
	// ConsumePoints=false 即「无限制模型」：落在这条权益里不扣算力点。
	ConsumePoints bool    `json:"consume_points"`
	Discount      float64 `json:"discount"`
	// LimitCount=0 表示不限次。UsedCount 是本期已用，供页面显示「剩余 N 次」。
	LimitCount   int64 `json:"limit_count"`
	UsedCount    int64 `json:"used_count"`
	RateLimitRPM int   `json:"rate_limit_rpm"`
	// ResetPeriod 次数的重置周期，供角标渲染「500 次/月」。
	ResetPeriod string `json:"reset_period"`
	// ChannelLimited 该权益限定了渠道。展示侧无法预知请求会落到哪个渠道，
	// 所以这类权益的覆盖是**有条件的**——页面要如实说明，否则用户会以为
	// 这个价必然拿得到，实际请求被路由到别的渠道时却按原价扣了。
	ChannelLimited bool `json:"channel_limited"`
}

// EntitlementCoverage 某个模型在当前用户套餐下的覆盖情况。
//
// 内嵌的那条是**请求落在不限渠道时扣费会命中的**那条（全是限渠道权益时取第一条）；
// All 按扣费顺序列出全部覆盖该模型的条目，供 tooltip 说明「本模型被 N 个套餐覆盖，
// 当前走哪个」以及「哪条只在特定渠道生效」。
// 持有多个套餐时用户最想知道的正是这件事，只给一个数字解释不了为什么是这个价。
type EntitlementCoverage struct {
	EntitlementCoverageEntry
	All []EntitlementCoverageEntry `json:"all,omitempty"`
}

// ListUserEntitlementCoverage 算出用户全部活跃套餐对给定模型集合的覆盖情况。
//
// modelNames 由调用方给出（定价接口已经裁剪过可见性与分组），这里只负责把权益
// 的通配范围展开到这些具体模型上。
//
// 顶层字段是**扣费会命中的那一条**权益，而不是「最省点数的那条」：显示一个用户
// 拿不到的价比不显示更糟，账单对不上时解释成本极高。All 按扣费顺序列出全部覆盖
// 该模型的条目：每个订阅内列到第一条不限渠道的为止（它后面的扣费永远走不到）。
//
// 无覆盖时返回 nil 而不是空 map：「有套餐但没覆盖这些模型」与「没有套餐」在展示上
// 是同一件事（设计文档 §8.2），返回 {} 会让前端的真值判断把两者分开处理。
func ListUserEntitlementCoverage(userId int, modelNames []string) (map[string]*EntitlementCoverage, error) {
	if userId <= 0 || len(modelNames) == 0 {
		return nil, nil
	}
	now := common.GetTimestamp()

	var subs []UserSubscription
	if err := DB.Where("user_id = ? AND status = ? AND start_time <= ? AND end_time > ?",
		userId, "active", now, now).
		Order("end_time asc, id asc").Find(&subs).Error; err != nil {
		return nil, err
	}
	if len(subs) == 0 {
		return nil, nil
	}

	planIds := make([]int, 0, len(subs))
	seenPlan := make(map[int]struct{}, len(subs))
	for _, s := range subs {
		if _, ok := seenPlan[s.PlanId]; ok {
			continue
		}
		seenPlan[s.PlanId] = struct{}{}
		planIds = append(planIds, s.PlanId)
	}

	var ents []SubscriptionPlanEntitlement
	if err := DB.Where("plan_id IN ?", planIds).
		Order("plan_id asc, sort_order asc, id asc").Find(&ents).Error; err != nil {
		return nil, err
	}
	if len(ents) == 0 {
		return nil, nil
	}
	byPlan := make(map[int][]SubscriptionPlanEntitlement, len(planIds))
	for _, e := range ents {
		byPlan[e.PlanId] = append(byPlan[e.PlanId], e)
	}

	// 计数器一次捞全：逐个权益查是 N+1，而这是定价页每次刷新都会走的路径
	subIds := make([]int, 0, len(subs))
	for _, s := range subs {
		subIds = append(subIds, s.Id)
	}
	var counters []UserSubscriptionEntitlement
	if err := DB.Where("user_subscription_id IN ?", subIds).Find(&counters).Error; err != nil {
		return nil, err
	}
	counterKey := func(subId, entId int) [2]int { return [2]int{subId, entId} }
	counterMap := make(map[[2]int]UserSubscriptionEntitlement, len(counters))
	for _, c := range counters {
		counterMap[counterKey(c.UserSubscriptionId, c.PlanEntitlementId)] = c
	}

	planTitles := make(map[int]string, len(planIds))
	for _, id := range planIds {
		if plan, err := getSubscriptionPlanByIdTx(nil, id); err == nil && plan != nil {
			planTitles[id] = plan.Title
		}
	}

	result := make(map[string]*EntitlementCoverage)
	for _, name := range modelNames {
		if _, done := result[name]; done {
			continue
		}
		// 与 MatchUserEntitlement 同序：订阅按到期近者优先，权益按 sort_order。
		// 不在第一个订阅就停——要把全部覆盖该模型的套餐收齐供 tooltip 使用。
		var all []EntitlementCoverageEntry
		primary := -1
		for _, sub := range subs {
			for _, ent := range byPlan[sub.PlanId] {
				if !ent.MatchesModel(name) {
					continue
				}
				counter, ok := counterMap[counterKey(sub.Id, ent.Id)]
				if !ok {
					// 权益是这笔订阅售出之后才加的，计数器还没实例化——
					// 扣费侧会当作未命中降级，展示侧必须同口径，否则页面显示
					// 「套餐内」而实际扣的是余额。
					continue
				}
				discount := ent.ConsumeDiscount
				if discount <= 0 {
					discount = 1
				}
				entry := EntitlementCoverageEntry{
					EntitlementId:  ent.Id,
					PlanId:         sub.PlanId,
					PlanTitle:      planTitles[sub.PlanId],
					ConsumePoints:  ent.ConsumePoints,
					Discount:       discount,
					LimitCount:     counter.LimitCount,
					UsedCount:      counter.UsedCount,
					RateLimitRPM:   ent.RateLimitRPM,
					ResetPeriod:    NormalizeResetPeriod(ent.ResetPeriod),
					ChannelLimited: len(ent.ChannelIdList()) > 0,
				}
				all = append(all, entry)
				if entry.ChannelLimited {
					// 扣费侧遇到渠道不符会跳过它继续往下找，所以它后面的条目
					// 对其他渠道仍然可达，不能在这里停。
					continue
				}
				if primary < 0 {
					// 请求落在不限渠道时，扣费命中的就是按顺序第一条不限渠道的
					primary = len(all) - 1
				}
				// 同一个订阅内，不限渠道的这条之后的条目被它盖住，扣费永远走不到，
				// 列出来只会让用户以为自己有得选。
				break
			}
		}
		if len(all) == 0 {
			continue
		}
		if primary < 0 {
			// 全是限渠道权益：没有「确定拿得到」的价，取第一条并由角标标明有条件
			primary = 0
		}
		result[name] = &EntitlementCoverage{
			EntitlementCoverageEntry: all[primary],
			All:                      all,
		}
	}
	if len(result) == 0 {
		return nil, nil
	}
	return result, nil
}
