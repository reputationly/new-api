package model

import "gorm.io/gorm"

// 套餐履约率报表的数据查询（设计文档 §8.4、P7）。汇总与换算在 service/plan_fulfillment.go。

// PlanRevenueRow 某个套餐在时间窗内的售卖收入。
type PlanRevenueRow struct {
	PlanId     int   `json:"plan_id"`
	RevenueFen int64 `json:"revenue_fen"`
	OrderCount int64 `json:"order_count"`
}

// GetPlanRevenue 按套餐汇总售卖收入。
//
// 收入以 fund_entries 为准（资金事实的唯一来源，与收入对账同一口径），按 ref_id 关联
// 订单拿到 plan_id——流水本身没有 plan_id 列。管理员直接开通的订阅不产生订单与流水，
// 天然不计收入：它们的履约成本照算，这正是报表要暴露的「白送的成本」。
func GetPlanRevenue(start, end int64) ([]PlanRevenueRow, error) {
	var rows []PlanRevenueRow
	err := DB.Table("fund_entries").
		Joins("JOIN subscription_orders ON subscription_orders.trade_no = fund_entries.ref_id").
		Where("fund_entries.ref_type = ? AND fund_entries.kind = ? AND fund_entries.created_at >= ? AND fund_entries.created_at <= ?",
			FundRefSubscriptionOrder, FundKindPrepay, start, end).
		Select("subscription_orders.plan_id AS plan_id, " +
			"COALESCE(SUM(fund_entries.cash_fen),0) AS revenue_fen, " +
			"COUNT(*) AS order_count").
		Group("subscription_orders.plan_id").
		Scan(&rows).Error
	return rows, err
}

// PlanExpiredPointsRow 某个套餐在时间窗内到期的算力点批次。
type PlanExpiredPointsRow struct {
	PlanId  int   `json:"plan_id"`
	Granted int64 `json:"granted"` // quota unit
	Unused  int64 `json:"unused"`  // 到期时没用完、随之作废的量
}

// GetPlanExpiredPoints 统计 [start, min(end, now)] 内到期的套餐批次。
//
// 「过期作废比例」是 P4 留下的信号：算力点不足时整笔降级而不是逐层级联，代价是余量
// 残渣卡到期末作废。这个比例高了，才值得为级联付出拆分记账的复杂度。
// 只看已经到期的批次——还没到期的余量不是作废，只是还没用。
func GetPlanExpiredPoints(start, end, now int64) ([]PlanExpiredPointsRow, error) {
	if end > now {
		end = now
	}
	var rows []PlanExpiredPointsRow
	err := DB.Table("compute_point_lots").
		Joins("JOIN user_subscriptions ON user_subscriptions.id = compute_point_lots.ref_id").
		Where("compute_point_lots.source = ? AND compute_point_lots.expires_at > 0 AND compute_point_lots.expires_at >= ? AND compute_point_lots.expires_at <= ?",
			ComputePointLotSourceSubscription, start, end).
		Select("user_subscriptions.plan_id AS plan_id, " +
			"COALESCE(SUM(compute_point_lots.points_total),0) AS granted, " +
			"COALESCE(SUM(compute_point_lots.points_total - compute_point_lots.points_used),0) AS unused").
		Group("user_subscriptions.plan_id").
		Scan(&rows).Error
	return rows, err
}

// ScanPlanRelatedLogs 逐批扫描时间窗内与套餐有关的消费 / 退款日志：走了权益的，
// 以及被套餐覆盖却降级（超额）的。
//
// 两类都只能靠 other 里的文本匹配（没有独立列），与日志「计费来源」筛选同一个写法；
// 由 created_at 索引先收窄到时间窗。走 LOG_DB：日志可能配在独立库。
func ScanPlanRelatedLogs(start, end int64, fn func(logs []*Log) error) error {
	var batch []*Log
	return LOG_DB.Model(&Log{}).
		Select("id, type, quota, other").
		Where("created_at >= ? AND created_at <= ? AND type IN ?", start, end,
			[]int{LogTypeConsume, LogTypeRefund}).
		Where("(other LIKE ? OR other LIKE ?)",
			logOtherEntitlementPattern, logOtherOveragePattern).
		FindInBatches(&batch, 1000, func(tx *gorm.DB, _ int) error {
			return fn(batch)
		}).Error
}

// SubscriptionPlanIds 订阅 id → 套餐 id。异步任务的退款 / 差额日志只冻结了订阅 id。
func SubscriptionPlanIds(subIds []int) (map[int]int, error) {
	out := make(map[int]int, len(subIds))
	if len(subIds) == 0 {
		return out, nil
	}
	var rows []struct {
		Id     int
		PlanId int
	}
	if err := DB.Model(&UserSubscription{}).Select("id, plan_id").
		Where("id IN ?", subIds).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.Id] = r.PlanId
	}
	return out, nil
}
