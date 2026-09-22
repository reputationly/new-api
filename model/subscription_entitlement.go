package model

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

// SubscriptionPlanEntitlement 套餐权益模板。设计见 docs/subscription-entitlement-design.md §五、§十二。
//
// 权益是风控层而不是计费层：计费一律走算力点（按现有倍率体系换算），权益只回答
// 「这个模型算不算在套餐内」「按什么折扣扣」「还能用几次」。次数上限与算力点是两个
// 正交维度，叠加生效——自有算力通常不限次（靠 RPM 兜底），外采算力必须限次，
// 否则套餐客户能把外采额度刷爆。
//
// 匹配顺序由 SortOrder 决定，先命中先生效；模型范围重叠时由运营在编辑页调整顺序。
type SubscriptionPlanEntitlement struct {
	Id     int `json:"id"`
	PlanId int `json:"plan_id" gorm:"index"`

	// SortOrder 匹配优先级，小的先匹配。
	SortOrder int `json:"sort_order" gorm:"type:int;not null;default:0"`

	// Models 逗号分隔的模型名，支持通配符（claude-opus-*）。
	// 用 text 而不是 JSONB：变长内容要三库通吃（CLAUDE.md Rule 2）。
	Models string `json:"models" gorm:"type:text"`
	// ChannelIds 逗号分隔的渠道 ID，空 = 不限渠道。
	// 渠道限定发生在渠道已选定之后的匹配阶段，不参与渠道选择本身（设计文档 §6.1）。
	ChannelIds string `json:"channel_ids" gorm:"type:varchar(255)"`

	// ConsumePoints=false 即「无限制模型」：落在这条权益里的模型完全不扣算力点。
	//
	// ⚠️ 刻意不加 gorm:"default:true" 标签。GORM 对带 default 的字段会在值为零值时
	// 把它从 INSERT 里省掉、让数据库默认值生效——那样 ConsumePoints=false 存进去会
	// 变成 true，「无限制模型」这个核心功能静默失效。这与 Rule 6 要保住显式零值是
	// 同一类问题。默认值改由 NormalizeEntitlement 在应用层显式赋。
	ConsumePoints bool `json:"consume_points" gorm:"not null"`
	// ConsumeDiscount 消耗折扣系数，1.0 = 原价，0.5 = 半价。
	// 用 precision/scale 而不是 type:decimal(6,4)：SQLite 驱动的 DDL 解析器正则
	// 字符集不含逗号，会把类型读坏，导致「第一次启动正常、第二次启动 FATAL」
	// （subscription.go 的 PriceAmount 已记录这个坑）。
	ConsumeDiscount float64 `json:"consume_discount" gorm:"precision:6;scale:4;not null;default:1"`

	// LimitCount 次数上限，0 = 不限次。
	LimitCount int64 `json:"limit_count" gorm:"type:bigint;not null;default:0"`
	// ResetPeriod 次数的重置周期，复用套餐那套取值；custom 时沿用所属套餐的
	// QuotaResetCustomSeconds，权益自己不再单独存一份秒数。
	ResetPeriod string `json:"reset_period" gorm:"type:varchar(16);default:'never'"`

	// RateLimitRPM 每分钟请求数上限。不限次或不消耗算力点的权益必填——
	// 这两种情况都不等于零成本（自有算力同样挤占 GPU），速率限制是唯一兜底。
	RateLimitRPM int `json:"rate_limit_rpm" gorm:"type:int;not null;default:0"`

	CreatedAt int64 `json:"created_at" gorm:"bigint"`
	UpdatedAt int64 `json:"updated_at" gorm:"bigint"`
}

// UserSubscriptionEntitlement 用户权益实例，只承担「本期还能用几次」这一件事。
//
// LimitCount 是从套餐权益复制过来的本期额度，不是不可变快照：运营改套餐实时生效，
// 下一次重置时按套餐当前值刷新（这是明确选定的语义，与 ComputePointsPerPeriod、
// TotalAmount 的行为一致——它们同样是重置时读当前套餐）。模型范围、折扣、渠道限定
// 一律不在这里冗余存储，匹配时实时读套餐权益，避免两份真相对不上。
type UserSubscriptionEntitlement struct {
	Id                 int `json:"id"`
	UserId             int `json:"user_id" gorm:"index"`
	UserSubscriptionId int `json:"user_subscription_id" gorm:"index"`
	PlanEntitlementId  int `json:"plan_entitlement_id" gorm:"index"`

	LimitCount int64 `json:"limit_count" gorm:"type:bigint;not null;default:0"`
	UsedCount  int64 `json:"used_count" gorm:"type:bigint;not null;default:0"`

	LastResetTime int64 `json:"last_reset_time" gorm:"type:bigint;default:0"`
	NextResetTime int64 `json:"next_reset_time" gorm:"type:bigint;default:0;index"`
}

func (e *SubscriptionPlanEntitlement) BeforeCreate(tx *gorm.DB) error {
	now := common.GetTimestamp()
	e.CreatedAt = now
	e.UpdatedAt = now
	return nil
}

func (e *SubscriptionPlanEntitlement) BeforeUpdate(tx *gorm.DB) error {
	e.UpdatedAt = common.GetTimestamp()
	return nil
}

// ModelList 拆出模型名列表。存储层是逗号分隔的字符串，读取方不该各自再写一遍拆分。
func (e *SubscriptionPlanEntitlement) ModelList() []string {
	return splitEntitlementList(e.Models)
}

// ChannelIdList 拆出渠道 ID 列表，空表示不限渠道。
func (e *SubscriptionPlanEntitlement) ChannelIdList() []string {
	return splitEntitlementList(e.ChannelIds)
}

func splitEntitlementList(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// normalizeEntitlementList 去空白、去重、保序后重新拼回逗号分隔串。
// 保序而不是排序：模型范围的书写顺序是运营的表达，不该被存储层重排。
func normalizeEntitlementList(raw string) string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, p := range splitEntitlementList(raw) {
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return strings.Join(out, ",")
}

// NormalizeEntitlement 落库前的统一规整，Create/Update 两条路径都必须先过这里，
// 免得校验规则在两处各写一遍、然后慢慢长歪。
func NormalizeEntitlement(e *SubscriptionPlanEntitlement) {
	if e == nil {
		return
	}
	e.Models = normalizeEntitlementList(e.Models)
	e.ChannelIds = normalizeEntitlementList(e.ChannelIds)
	e.ResetPeriod = NormalizeResetPeriod(e.ResetPeriod)
	if e.ConsumeDiscount <= 0 {
		e.ConsumeDiscount = 1
	}
	if e.LimitCount < 0 {
		e.LimitCount = 0
	}
	if e.RateLimitRPM < 0 {
		e.RateLimitRPM = 0
	}
}

var (
	ErrEntitlementNoModels     = errors.New("权益的模型范围不能为空")
	ErrEntitlementRateRequired = errors.New("不限次或不消耗算力点的权益必须设置速率限制")
	ErrEntitlementDiscount     = errors.New("消耗折扣系数必须大于 0")
)

// ValidateEntitlement 保存校验。
//
// 唯一的硬性业务校验是速率限制：不限次（LimitCount=0）或不消耗算力点的权益，
// 意味着客户在这条权益里可以无成本地打无限量请求，RPM 是唯一的兜底闸门，
// 所以它在这两种情况下是必填而非可选（设计文档 §5.1、§9.1）。
func ValidateEntitlement(e *SubscriptionPlanEntitlement) error {
	if e == nil {
		return errors.New("权益为空")
	}
	if len(e.ModelList()) == 0 {
		return ErrEntitlementNoModels
	}
	if e.ConsumeDiscount <= 0 {
		return ErrEntitlementDiscount
	}
	if (e.LimitCount <= 0 || !e.ConsumePoints) && e.RateLimitRPM <= 0 {
		return ErrEntitlementRateRequired
	}
	return nil
}

// ListPlanEntitlements 按匹配顺序读出某个套餐的全部权益。
func ListPlanEntitlements(planId int) ([]*SubscriptionPlanEntitlement, error) {
	if planId <= 0 {
		return nil, errors.New("invalid planId")
	}
	var list []*SubscriptionPlanEntitlement
	if err := DB.Where("plan_id = ?", planId).
		Order("sort_order asc, id asc").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// GroupPlanEntitlementsByPlan 一次读出全部权益并按套餐归并，供管理端列表页使用，
// 避免「每个套餐查一次」的 N+1。
func GroupPlanEntitlementsByPlan() (map[int][]*SubscriptionPlanEntitlement, error) {
	var list []*SubscriptionPlanEntitlement
	if err := DB.Order("plan_id asc, sort_order asc, id asc").Find(&list).Error; err != nil {
		return nil, err
	}
	grouped := make(map[int][]*SubscriptionPlanEntitlement)
	for _, e := range list {
		grouped[e.PlanId] = append(grouped[e.PlanId], e)
	}
	return grouped, nil
}

func listPlanEntitlementsTx(tx *gorm.DB, planId int) ([]*SubscriptionPlanEntitlement, error) {
	var list []*SubscriptionPlanEntitlement
	if err := tx.Where("plan_id = ?", planId).
		Order("sort_order asc, id asc").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// ReplacePlanEntitlementsTx 用提交上来的列表整体替换某个套餐的权益配置。
//
// 按 Id 做增量比对，而不是「全删再全插」：已售出的订阅有 UserSubscriptionEntitlement
// 行通过 PlanEntitlementId 指向这些记录，全删再插会让 Id 全部变化、把存量客户的
// 次数计数器指成孤儿。被真正移除的那些才删，并连带清掉指向它的用户计数器——
// 权益都不存在了，它的闸门计数留着没有意义，也不该在报表里继续出现。
//
// SortOrder 按提交顺序重排，不信任前端传的值：顺序即匹配优先级，让它由列表位置
// 唯一决定，避免前端漏传或传重导致匹配顺序玄学。
func ReplacePlanEntitlementsTx(tx *gorm.DB, planId int, items []SubscriptionPlanEntitlement) error {
	if tx == nil {
		return errors.New("tx is nil")
	}
	if planId <= 0 {
		return errors.New("invalid planId")
	}

	existing, err := listPlanEntitlementsTx(tx, planId)
	if err != nil {
		return err
	}
	existingIds := make(map[int]struct{}, len(existing))
	for _, e := range existing {
		existingIds[e.Id] = struct{}{}
	}

	keptIds := make(map[int]struct{}, len(items))
	for i := range items {
		item := items[i]
		item.PlanId = planId
		item.SortOrder = i
		NormalizeEntitlement(&item)
		if err := ValidateEntitlement(&item); err != nil {
			return err
		}
		if _, ok := existingIds[item.Id]; ok && item.Id > 0 {
			keptIds[item.Id] = struct{}{}
			// 用 Select 显式列出要写的列：ConsumePoints 为 false 时若靠结构体更新，
			// GORM 会把零值当作「未设置」而跳过，导致「消耗算力点」永远关不掉。
			if err := tx.Model(&SubscriptionPlanEntitlement{}).
				Where("id = ? AND plan_id = ?", item.Id, planId).
				Select("sort_order", "models", "channel_ids", "consume_points",
					"consume_discount", "limit_count", "reset_period", "rate_limit_rpm", "updated_at").
				Updates(&item).Error; err != nil {
				return err
			}
			continue
		}
		item.Id = 0
		if err := tx.Create(&item).Error; err != nil {
			return err
		}
	}

	for _, e := range existing {
		if _, ok := keptIds[e.Id]; ok {
			continue
		}
		if err := tx.Where("plan_entitlement_id = ?", e.Id).
			Delete(&UserSubscriptionEntitlement{}).Error; err != nil {
			return err
		}
		if err := tx.Where("id = ?", e.Id).
			Delete(&SubscriptionPlanEntitlement{}).Error; err != nil {
			return err
		}
	}
	return nil
}

// calcEntitlementNextResetTime 算权益下一次次数重置的时刻。
//
// 复用套餐那套周期对齐逻辑（日/周/月各自对齐到自然边界），只是周期取自权益自己的
// ResetPeriod；选 custom 时沿用所属套餐的 QuotaResetCustomSeconds——权益不单独存
// 一份秒数，一个套餐里出现两种自定义周期没有业务含义，徒增配置面。
func calcEntitlementNextResetTime(base time.Time, ent *SubscriptionPlanEntitlement, plan *SubscriptionPlan, endUnix int64) int64 {
	if ent == nil {
		return 0
	}
	var customSeconds int64
	if plan != nil {
		customSeconds = plan.QuotaResetCustomSeconds
	}
	return calcNextResetTimeFor(base, ent.ResetPeriod, customSeconds, endUnix)
}

// instantiateUserSubscriptionEntitlementsTx 订阅生效时，把套餐权益实例化成用户侧的
// 次数计数器。不限次的权益（LimitCount=0）同样建行：它将来可能被运营改成限次，
// 有行在才能在下一次重置时自然跟上，而不是要求运营记得去补建。
func instantiateUserSubscriptionEntitlementsTx(tx *gorm.DB, sub *UserSubscription, plan *SubscriptionPlan, nowUnix int64) error {
	if tx == nil || sub == nil || plan == nil {
		return errors.New("invalid entitlement instantiate args")
	}
	ents, err := listPlanEntitlementsTx(tx, plan.Id)
	if err != nil {
		return err
	}
	base := time.Unix(nowUnix, 0)
	for _, ent := range ents {
		row := &UserSubscriptionEntitlement{
			UserId:             sub.UserId,
			UserSubscriptionId: sub.Id,
			PlanEntitlementId:  ent.Id,
			LimitCount:         ent.LimitCount,
			UsedCount:          0,
			LastResetTime:      nowUnix,
			NextResetTime:      calcEntitlementNextResetTime(base, ent, plan, sub.EndTime),
		}
		if err := tx.Create(row).Error; err != nil {
			return err
		}
	}
	return nil
}

// ResetDueUserSubscriptionEntitlements 把到期的权益计数器归零，并按套餐当前配置
// 刷新本期额度。与 ResetDueSubscriptions 同一节奏，供 service 层定时任务调用。
//
// 归零用条件更新抢占（WHERE next_reset_time = 取出时的值），不是无条件更新：
// 后台任务与将来的实时扣次路径可能并发闯进同一个到期窗口，只有抢赢的那个事务
// 才推进周期，其余拿到 RowsAffected=0 直接跳过。这与 maybeResetUserSubscriptionWithPlanTx
// 是同一套路——那里已经吃过「以为有锁其实没锁」的亏（gorm:query_option 在 GORM v2
// 下是死代码），此处不再重蹈。
func ResetDueUserSubscriptionEntitlements(limit int) (int, error) {
	if limit <= 0 {
		limit = 200
	}
	now := GetDBTimestamp()

	var due []UserSubscriptionEntitlement
	if err := DB.Where("next_reset_time > 0 AND next_reset_time <= ?", now).
		Order("next_reset_time asc").Limit(limit).Find(&due).Error; err != nil {
		return 0, err
	}
	if len(due) == 0 {
		return 0, nil
	}

	resetCount := 0
	for i := range due {
		row := due[i]
		ent, plan, sub, err := loadEntitlementResetContext(row)
		if err != nil {
			return resetCount, err
		}
		if ent == nil || plan == nil || sub == nil {
			// 归属对象已不存在。正常的清理路径是 ReplacePlanEntitlementsTx（删权益时
			// 连带删计数器），能走到这里说明数据被绕过那条路径改动了。这里只把它摘出
			// 重置队列、不删行：后台任务基于一次「查不到」就删计费计数器，一旦哪天是
			// 迁移中途或人为误删导致的，证据也跟着没了。留着行，交给对账去发现。
			if err := DB.Model(&UserSubscriptionEntitlement{}).
				Where("id = ? AND next_reset_time = ?", row.Id, row.NextResetTime).
				Update("next_reset_time", 0).Error; err != nil {
				return resetCount, err
			}
			common.SysLog(fmt.Sprintf(
				"entitlement reset skipped: row=%d plan_entitlement=%d subscription=%d (归属对象已不存在，已移出重置队列)",
				row.Id, row.PlanEntitlementId, row.UserSubscriptionId))
			continue
		}
		base := time.Unix(row.NextResetTime, 0)
		next := calcEntitlementNextResetTime(base, ent, plan, sub.EndTime)
		res := DB.Model(&UserSubscriptionEntitlement{}).
			Where("id = ? AND next_reset_time = ?", row.Id, row.NextResetTime).
			Updates(map[string]interface{}{
				"used_count":      0,
				"limit_count":     ent.LimitCount,
				"last_reset_time": row.NextResetTime,
				"next_reset_time": next,
			})
		if res.Error != nil {
			return resetCount, res.Error
		}
		if res.RowsAffected > 0 {
			resetCount++
		}
	}
	return resetCount, nil
}

func loadEntitlementResetContext(row UserSubscriptionEntitlement) (*SubscriptionPlanEntitlement, *SubscriptionPlan, *UserSubscription, error) {
	var ent SubscriptionPlanEntitlement
	err := DB.Where("id = ?", row.PlanEntitlementId).First(&ent).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, nil, nil
	}
	if err != nil {
		return nil, nil, nil, err
	}
	var sub UserSubscription
	err = DB.Where("id = ?", row.UserSubscriptionId).First(&sub).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, nil, nil
	}
	if err != nil {
		return nil, nil, nil, err
	}
	// 只有「确实查不到」才算孤儿。不能把所有 err 都当成孤儿：那样一次连接抖动
	// 就会让计费计数器被当作无主数据处理，而这类误判在日志里看起来与真的孤儿一模一样。
	plan, err := getSubscriptionPlanByIdTx(nil, ent.PlanId)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, nil, nil
	}
	if err != nil {
		return nil, nil, nil, err
	}
	return &ent, plan, &sub, nil
}
