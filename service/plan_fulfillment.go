package service

import (
	"math"
	"sort"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// 套餐履约率报表（设计文档 §8.4、P7）。
//
//	履约率 = 外采成本 ÷ 套餐收入
//
// 这个数直接回答「套餐定价亏不亏」。口径：
//   - 收入：fund_entries 里的套餐售卖流水（与收入对账同源）。
//   - 外采成本：套餐内调用日志里请求时落库的 cost_quota（渠道成本比 × 原价），**不按
//     当前成本比重算**——运营调过成本比后重算，历史数字就变了。退款日志按负数冲销。
//   - 没有 cost_quota 的套餐内调用（自有算力，或运营没配成本比）不计入成本，单独列出
//     次数与等值金额：履约率只会偏低不会虚高，且运营能看到有多少没算进来。
//   - 超额：被套餐覆盖却降级按余额扣的调用，是套餐带来的额外收入。
//   - 过期作废：期内到期的套餐批次里没用完的点数。
//
// 收入与成本不在同一时刻发生（月初售卖、全月消耗），短窗口下履约率会波动；看趋势
// 应当用整月或更长的窗口。

// PlanFulfillmentRow 一个套餐的汇总。金额一律为人民币分。
type PlanFulfillmentRow struct {
	PlanId    int    `json:"plan_id"`
	PlanTitle string `json:"plan_title"`

	OrderCount int64 `json:"order_count"`
	RevenueFen int64 `json:"revenue_fen"`

	// CallCount 套餐内调用次数（只数消费日志，退款不减次数：请求确实发生过）
	CallCount int64 `json:"call_count"`
	// ValueFen 套餐内调用按原价折算的等值金额（净额，已冲销退款）——用户省了多少
	ValueFen int64 `json:"value_fen"`
	// CostFen 外采成本（净额）
	CostFen int64 `json:"cost_fen"`
	// UncostedCallCount / UncostedValueFen 没有成本记录的套餐内调用
	UncostedCallCount int64 `json:"uncosted_call_count"`
	UncostedValueFen  int64 `json:"uncosted_value_fen"`

	OverageCount int64 `json:"overage_count"`
	OverageFen   int64 `json:"overage_fen"`

	// 期内到期的算力点批次（展示点数）
	PointsExpiredGranted int      `json:"points_expired_granted"`
	PointsExpiredUnused  int      `json:"points_expired_unused"`
	ExpiredUnusedRatio   *float64 `json:"expired_unused_ratio"`

	// FulfillmentRate 外采成本 ÷ 收入；期内没有收入时为 nil（除以 0 没有意义，
	// 管理员开通的套餐就是这种情况——成本照样列出）
	FulfillmentRate *float64 `json:"fulfillment_rate"`
}

type PlanFulfillmentReport struct {
	Start int64                `json:"start"`
	End   int64                `json:"end"`
	Rows  []PlanFulfillmentRow `json:"rows"`
	Total PlanFulfillmentRow   `json:"total"`
}

// planAccum 汇总过程中的累加值，quota 用浮点保留精度，最后一次换算成分。
type planAccum struct {
	orderCount, revenueFen        int64
	callCount, uncostedCalls      int64
	valueQuota, costQuota         float64
	uncostedQuota                 float64
	overageCount                  int64
	overageQuota                  float64
	expiredGranted, expiredUnused int64
}

// quotaToFen quota → 人民币分，与成本对账（reconcile_helpers.upstreamAmountCNY）同一换算。
func quotaToFen(q float64) int64 {
	return int64(math.Round(q / common.QuotaPerUnit * operation_setting.USDExchangeRate * 100))
}

func ratio(num, den int64) *float64 {
	if den <= 0 {
		return nil
	}
	v := float64(num) / float64(den)
	return &v
}

func numberOf(other map[string]interface{}, key string) (float64, bool) {
	v, ok := other[key].(float64)
	return v, ok
}

// BuildPlanFulfillmentReport 汇总 [start, end] 内各套餐的履约情况。
func BuildPlanFulfillmentReport(start, end int64) (*PlanFulfillmentReport, error) {
	acc := map[int]*planAccum{}
	get := func(planId int) *planAccum {
		a := acc[planId]
		if a == nil {
			a = &planAccum{}
			acc[planId] = a
		}
		return a
	}

	revenue, err := model.GetPlanRevenue(start, end)
	if err != nil {
		return nil, err
	}
	for _, r := range revenue {
		a := get(r.PlanId)
		a.orderCount += r.OrderCount
		a.revenueFen += r.RevenueFen
	}

	// 退款 / 差额日志只有订阅 id，先按订阅 id 暂存，扫完再批量反查套餐
	bySub := map[int]*planAccum{}
	err = model.ScanPlanRelatedLogs(start, end, func(logs []*model.Log) error {
		for _, l := range logs {
			var other map[string]interface{}
			if err := common.UnmarshalJsonStr(l.Other, &other); err != nil || other == nil {
				continue
			}
			sign := 1.0
			if l.Type == model.LogTypeRefund {
				sign = -1
			}
			quota := float64(l.Quota)

			if other["billing_source"] == BillingSourceEntitlement {
				var a *planAccum
				if pid, ok := numberOf(other, "entitlement_plan_id"); ok && pid > 0 {
					a = get(int(pid))
				} else if sid, ok := numberOf(other, "subscription_id"); ok && sid > 0 {
					a = bySub[int(sid)]
					if a == nil {
						a = &planAccum{}
						bySub[int(sid)] = a
					}
				} else {
					continue // 归不到任何套餐：老日志或字段缺失，宁可不算也不瞎算
				}
				a.valueQuota += sign * quota
				cost, hasCost := numberOf(other, "cost_quota")
				if hasCost {
					a.costQuota += sign * cost
				} else {
					a.uncostedQuota += sign * quota
				}
				if l.Type == model.LogTypeConsume {
					a.callCount++
					if !hasCost {
						a.uncostedCalls++
					}
				}
				continue
			}

			// 超额只数消费：它是按余额扣的钱，退款在资金侧已有净额，这里只看「发生了几次」
			if fb, ok := other["entitlement_fallback"].(map[string]interface{}); ok && l.Type == model.LogTypeConsume {
				if pid, ok := numberOf(fb, "plan_id"); ok && pid > 0 {
					a := get(int(pid))
					a.overageCount++
					a.overageQuota += quota
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(bySub) > 0 {
		subIds := make([]int, 0, len(bySub))
		for id := range bySub {
			subIds = append(subIds, id)
		}
		planOf, err := model.SubscriptionPlanIds(subIds)
		if err != nil {
			return nil, err
		}
		for sid, part := range bySub {
			pid, ok := planOf[sid]
			if !ok {
				continue
			}
			a := get(pid)
			a.valueQuota += part.valueQuota
			a.costQuota += part.costQuota
			a.uncostedQuota += part.uncostedQuota
			a.callCount += part.callCount
			a.uncostedCalls += part.uncostedCalls
		}
	}

	expired, err := model.GetPlanExpiredPoints(start, end, common.GetTimestamp())
	if err != nil {
		return nil, err
	}
	for _, r := range expired {
		a := get(r.PlanId)
		a.expiredGranted += r.Granted
		a.expiredUnused += r.Unused
	}

	report := &PlanFulfillmentReport{Start: start, End: end, Rows: []PlanFulfillmentRow{}}
	var total planAccum
	for planId, a := range acc {
		row := toFulfillmentRow(a)
		row.PlanId = planId
		if plan, err := model.GetSubscriptionPlanById(planId); err == nil && plan != nil {
			row.PlanTitle = plan.Title
		}
		report.Rows = append(report.Rows, row)

		total.orderCount += a.orderCount
		total.revenueFen += a.revenueFen
		total.callCount += a.callCount
		total.uncostedCalls += a.uncostedCalls
		total.valueQuota += a.valueQuota
		total.costQuota += a.costQuota
		total.uncostedQuota += a.uncostedQuota
		total.overageCount += a.overageCount
		total.overageQuota += a.overageQuota
		total.expiredGranted += a.expiredGranted
		total.expiredUnused += a.expiredUnused
	}
	// 收入高的排前面：运营最关心卖得最多的那几个套餐亏不亏
	sort.Slice(report.Rows, func(i, j int) bool {
		if report.Rows[i].RevenueFen != report.Rows[j].RevenueFen {
			return report.Rows[i].RevenueFen > report.Rows[j].RevenueFen
		}
		return report.Rows[i].PlanId < report.Rows[j].PlanId
	})
	report.Total = toFulfillmentRow(&total)
	return report, nil
}

func toFulfillmentRow(a *planAccum) PlanFulfillmentRow {
	row := PlanFulfillmentRow{
		OrderCount:           a.orderCount,
		RevenueFen:           a.revenueFen,
		CallCount:            a.callCount,
		ValueFen:             quotaToFen(a.valueQuota),
		CostFen:              quotaToFen(a.costQuota),
		UncostedCallCount:    a.uncostedCalls,
		UncostedValueFen:     quotaToFen(a.uncostedQuota),
		OverageCount:         a.overageCount,
		OverageFen:           quotaToFen(a.overageQuota),
		PointsExpiredGranted: common.QuotaToComputePoints(int(a.expiredGranted)),
		PointsExpiredUnused:  common.QuotaToComputePoints(int(a.expiredUnused)),
	}
	row.ExpiredUnusedRatio = ratio(a.expiredUnused, a.expiredGranted)
	row.FulfillmentRate = ratio(row.CostFen, row.RevenueFen)
	return row
}
