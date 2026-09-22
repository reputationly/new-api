package service

import (
	"errors"
	"fmt"
	"math"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// ErrEntitlementUnavailable 权益不可用（未命中、次数用尽、算力点不足）。
// 它不是错误而是**降级信号**：调用方收到后退回现有资金链路，服务不中断。
var ErrEntitlementUnavailable = errors.New("套餐权益不可用")

// ---------------------------------------------------------------------------
// EntitlementFunding — 套餐权益资金来源（次数闸门 + 算力点）
// ---------------------------------------------------------------------------
//
// 设计见 docs/subscription-entitlement-design.md §5、§6.2。
//
// 次数与算力点是正交的两个维度，叠加生效：一次请求两者同时扣，任一用尽即降级。
// 次数是风控闸门（防止套餐客户把外采额度刷爆），算力点是计费单位。
// 权益配「不消耗算力点」时只走次数闸门与速率限制，那就是「无限制模型」。
type EntitlementFunding struct {
	userId int
	match  *model.EntitlementMatch

	countTaken bool
	// spent 记录算力点在各批次上的精确拆分，退款必须原路退回——否则快过期的批次
	// 被提前烧光、钱退到长期批次，用户凭空损失额度（§十一 风险 5）。
	spent []model.ComputePointSpend
}

func (e *EntitlementFunding) Source() string { return BillingSourceEntitlement }

// Match 命中的权益，供会话同步日志字段。
func (e *EntitlementFunding) Match() *model.EntitlementMatch { return e.match }

// PointsSpent 本次累计消耗的算力点（quota unit），供日志与对账。
func (e *EntitlementFunding) PointsSpent() int64 {
	total := int64(0)
	for _, s := range e.spent {
		total += s.Amount
	}
	return total
}

// pointsNeeded 把请求的 quota 换算成要从批次池里扣的算力点量。
//
// 折扣系数作用在消耗侧而不是定价侧：年卡配 ×0.5 就是「同样的调用只烧一半点数」。
// 向上取整——不足 1 个 quota unit 按 1 个扣，避免极小额调用因取整归零而白嫖。
func (e *EntitlementFunding) pointsNeeded(amount int) int64 {
	if amount <= 0 {
		return 0
	}
	discount := e.match.Entitlement.ConsumeDiscount
	if discount <= 0 {
		discount = 1
	}
	return int64(math.Ceil(float64(amount) * discount))
}

// PreConsume 预扣：先过次数闸门，再扣算力点。任一不通过都整笔失败并回滚，
// 由调用方降级到现有资金链路。
func (e *EntitlementFunding) PreConsume(amount int) error {
	ok, err := model.TryConsumeEntitlementCount(e.match.CounterId)
	if err != nil {
		return err
	}
	if !ok {
		return ErrEntitlementUnavailable
	}
	e.countTaken = true

	if !e.match.Entitlement.ConsumePoints {
		// 不消耗算力点的权益（无限制模型）：只有次数闸门与速率限制兜底
		return nil
	}
	need := e.pointsNeeded(amount)
	if need <= 0 {
		return nil
	}
	okPoints, spent, err := model.TryConsumeComputePoints(e.userId, need)
	if err != nil {
		e.releaseCount()
		return err
	}
	if !okPoints {
		// 算力点不足也要把刚占的次数还回去，否则降级走钱包的同时白白烧掉一次配额
		e.releaseCount()
		return ErrEntitlementUnavailable
	}
	e.spent = append(e.spent, spent...)
	return nil
}

// Settle 结算差额。
//
// 补扣（delta>0）时算力点可能已经不够——服务已经交付，不能失败。能扣多少扣多少，
// 扣不到的部分由平台承担并记日志：预扣时已经按估算占用过额度，这里的差额只是
// 估算与实际的偏差，敞口有界。堵死它需要在交付前就精确知道用量，做不到。
func (e *EntitlementFunding) Settle(delta int) error {
	if delta == 0 || !e.match.Entitlement.ConsumePoints {
		return nil
	}
	if delta > 0 {
		need := e.pointsNeeded(delta)
		if need <= 0 {
			return nil
		}
		ok, spent, err := model.TryConsumeComputePoints(e.userId, need)
		if err != nil {
			return err
		}
		if !ok {
			common.SysLog(fmt.Sprintf(
				"entitlement settle shortfall: user=%d entitlement=%d need=%d (算力点不足，差额由平台承担)",
				e.userId, e.match.Entitlement.Id, need))
			return nil
		}
		e.spent = append(e.spent, spent...)
		return nil
	}
	return e.refundPoints(e.pointsNeeded(-delta))
}

// reserveExtra 追加预扣：请求中途发现用量超出预估时补占算力点。
//
// 与 Settle(delta>0) 的区别是时机——这里服务还没交付，扣不到就必须让调用方知道
// （返回 false），不能像结算那样「扣不到就由平台承担」。返回本次的批次拆分，
// 供 unreserveExtra 精确原路回滚。
func (e *EntitlementFunding) reserveExtra(delta int) ([]model.ComputePointSpend, bool, error) {
	if delta <= 0 || !e.match.Entitlement.ConsumePoints {
		return nil, true, nil
	}
	need := e.pointsNeeded(delta)
	if need <= 0 {
		return nil, true, nil
	}
	ok, spent, err := model.TryConsumeComputePoints(e.userId, need)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, nil
	}
	e.spent = append(e.spent, spent...)
	return spent, true, nil
}

// unreserveExtra 按快照精确逆转刚追加的那一笔，并从累计拆分里扣掉。
//
// 必须按快照逆转而不是按总额退：追加那笔可能落在与原始预扣完全不同的批次上
// （原始预扣把近期批次扣光了，追加这笔只能走远期批次），按总额退会退错批次，
// 让快过期的那份凭空多出余量、远期的却没还回去。
func (e *EntitlementFunding) unreserveExtra(spent []model.ComputePointSpend) {
	if len(spent) == 0 {
		return
	}
	if err := model.RefundComputePoints(spent); err != nil {
		common.SysLog("error unreserving entitlement points: " + err.Error())
		return
	}
	for _, sp := range spent {
		for i := len(e.spent) - 1; i >= 0; i-- {
			if e.spent[i].LotId != sp.LotId {
				continue
			}
			take := sp.Amount
			if take > e.spent[i].Amount {
				take = e.spent[i].Amount
			}
			e.spent[i].Amount -= take
			break
		}
	}
}

// refundPoints 退还 amount 个算力点，按消耗的逆序原路退回原批次。
//
// 逆序退：最后扣的那笔最可能来自「后备」批次，先把它还回去，让快过期的批次
// 继续保持已消耗状态——与「先烧快过期的」是同一条原则的另一面。
func (e *EntitlementFunding) refundPoints(amount int64) error {
	if amount <= 0 {
		return nil
	}
	remaining := amount
	refunds := make([]model.ComputePointSpend, 0, len(e.spent))
	for i := len(e.spent) - 1; i >= 0 && remaining > 0; i-- {
		part := e.spent[i].Amount
		if part > remaining {
			part = remaining
		}
		if part <= 0 {
			continue
		}
		refunds = append(refunds, model.ComputePointSpend{LotId: e.spent[i].LotId, Amount: part})
		e.spent[i].Amount -= part
		remaining -= part
	}
	if len(refunds) == 0 {
		return nil
	}
	return model.RefundComputePoints(refunds)
}

// releaseCount 退还已占用的一次调用次数。
func (e *EntitlementFunding) releaseCount() {
	if !e.countTaken {
		return
	}
	if err := model.RefundEntitlementCount(e.match.CounterId); err != nil {
		common.SysLog("error refunding entitlement count: " + err.Error())
		return
	}
	e.countTaken = false
}

// Refund 请求失败时全额退还：次数退回 1 次，算力点原路退回各自批次。
//
// 与 WalletFunding.Refund 一样是非幂等操作，不可重试（幂等由 BillingSession
// 的 refunded 标志保证）。
func (e *EntitlementFunding) Refund() error {
	if len(e.spent) > 0 {
		if err := model.RefundComputePoints(e.spent); err != nil {
			return err
		}
		e.spent = nil
	}
	e.releaseCount()
	return nil
}
