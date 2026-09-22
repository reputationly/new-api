package model

import (
	"errors"
	"strconv"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

// 权益匹配与次数闸门。设计见 docs/subscription-entitlement-design.md §六。
//
// 匹配发生在**渠道已选定之后**：渠道仍由 Ability(group, model, channel) 按现有逻辑
// 选，选完再判断是否落在权益限定内（§6.1）。把权益约束注入渠道选择会与分组能力表、
// 成本路由、亲和缓存纠缠，改动面过大。代价是「权益限定自有 GPU、请求却被路由到
// 外采渠道」时享受不到套餐优惠——不是错误，只是降级扣余额。

// EntitlementMatch 一次命中的权益及其用户侧计数器。
type EntitlementMatch struct {
	Entitlement        SubscriptionPlanEntitlement
	CounterId          int
	UserSubscriptionId int
	PlanId             int
	// LimitCount / UsedCount 是匹配时刻的快照，仅供展示与日志。
	// 真正的闸门判定在 TryConsumeEntitlementCount 的条件更新里，不看这两个值——
	// 读一次再判一次就是 check-then-act，并发下必然超发。
	LimitCount int64
	UsedCount  int64
}

// Unlimited 该权益是否不限次。
func (m *EntitlementMatch) Unlimited() bool {
	return m != nil && m.LimitCount <= 0
}

// MatchUserEntitlement 找出这次请求命中的权益，未命中返回 (nil, nil)。
//
// 顺序：活跃订阅按 end_time 近者优先 → 订阅内权益按 sort_order。
//
// ⚠️ 与设计文档 §六 的一处偏离：文档写的是「按套餐优先级 → 同级按 end_time 近者
// 优先」，但「套餐优先级」在库里没有对应字段，最接近的 SubscriptionPlan.SortOrder
// 是编辑页标着「排序」的展示字段。拿展示字段当计费依据，日后运营调展示顺序会
// 悄悄改变扣费归属。这里改用计费路径已有的先例——PreConsumeUserSubscription 与
// 算力点批次都是「快过期的先烧」——若确实需要套餐级优先级，应新增独立字段而不是
// 复用 SortOrder。
func MatchUserEntitlement(userId int, modelName string, channelId int) (*EntitlementMatch, error) {
	if userId <= 0 || modelName == "" {
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

	// 一次把涉及的套餐权益全捞出来再按 plan_id 归并：每个订阅查一次是 N+1，
	// 而这是每请求都会走的热路径。
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

	for _, sub := range subs {
		for _, ent := range byPlan[sub.PlanId] {
			if !ent.MatchesModel(modelName) {
				continue
			}
			if !entitlementAllowsChannel(&ent, channelId) {
				continue
			}
			counter, err := findEntitlementCounter(sub.Id, ent.Id)
			if err != nil {
				return nil, err
			}
			if counter == nil {
				// 权益是在这笔订阅售出之后才加的，计数器还没实例化。
				// 跳过而不是当场补建：补建要决定「本期额度从哪个时点起算」，
				// 那是重置逻辑的职责；这里当作未命中，降级扣余额，服务不中断。
				continue
			}
			return &EntitlementMatch{
				Entitlement:        ent,
				CounterId:          counter.Id,
				UserSubscriptionId: sub.Id,
				PlanId:             sub.PlanId,
				LimitCount:         counter.LimitCount,
				UsedCount:          counter.UsedCount,
			}, nil
		}
	}
	return nil, nil
}

// entitlementAllowsChannel 渠道限定判定。留空 = 不限渠道。
//
// 取不到渠道号（channelId<=0）时，只有「不限渠道」的权益才算命中：限定了渠道却
// 不知道落在哪个渠道，不能假定它命中——那等于把套餐优惠发给了本不该享受的请求。
func entitlementAllowsChannel(ent *SubscriptionPlanEntitlement, channelId int) bool {
	allowed := ent.ChannelIdList()
	if len(allowed) == 0 {
		return true
	}
	if channelId <= 0 {
		return false
	}
	target := strconv.Itoa(channelId)
	for _, id := range allowed {
		if id == target {
			return true
		}
	}
	return false
}

func findEntitlementCounter(subId, planEntitlementId int) (*UserSubscriptionEntitlement, error) {
	var row UserSubscriptionEntitlement
	err := DB.Where("user_subscription_id = ? AND plan_entitlement_id = ?", subId, planEntitlementId).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// TryConsumeEntitlementCount 消耗一次权益调用次数，用尽返回 false（由调用方降级）。
//
// 判定与自增在同一条 UPDATE 的 WHERE 里完成，不先读后判：读一次再判一次是
// check-then-act，并发请求会双双读到「还剩 1 次」然后各扣一次，超发。
//
// 不限次（limit_count<=0）同样自增 used_count：它不构成闸门，但用量要留痕，
// 否则套餐履约率报表看不到这部分调用。
func TryConsumeEntitlementCount(counterId int) (bool, error) {
	if counterId <= 0 {
		return false, errors.New("invalid counterId")
	}
	res := DB.Model(&UserSubscriptionEntitlement{}).
		Where("id = ? AND (limit_count <= 0 OR used_count < limit_count)", counterId).
		Update("used_count", gorm.Expr("used_count + 1"))
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// RefundEntitlementCount 退还一次调用次数（请求失败时）。
//
// 条件更新保证不把 used_count 减成负数：若本期已被重置归零，这次退还就没有对应的
// 消耗可退，跳过而不是减成负数——负的已用次数会让闸门凭空多放行一次。
func RefundEntitlementCount(counterId int) error {
	if counterId <= 0 {
		return nil
	}
	res := DB.Model(&UserSubscriptionEntitlement{}).
		Where("id = ? AND used_count > 0", counterId).
		Update("used_count", gorm.Expr("used_count - 1"))
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		common.SysLog("entitlement count refund skipped: counter=" + strconv.Itoa(counterId) + " (used_count 已为 0，可能本期已重置)")
	}
	return nil
}
