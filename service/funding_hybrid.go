package service

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// ErrHybridWalletInsufficient 混扣预扣时积分不足（被并发抢占）且钱包余额无法覆盖剩余
// 部分——拒绝请求而非透支钱包。哨兵错误供 billing_session 映射为 403 额度不足。
var ErrHybridWalletInsufficient = errors.New("用户额度不足")

// ---------------------------------------------------------------------------
// HybridFunding — 积分 + 钱包混合资金来源
// ---------------------------------------------------------------------------
//
// 积分优先扣、不足部分扣钱包余额（§6.2）。积分扣减走 TryDecreaseUserPoints 条件更新，
// 保证积分永不透支为负（§6.4）；并发被抢时降级由钱包承担。内部累计 pointsConsumed /
// walletConsumed 供退款原路退还与结算写 PointsUsed / 日志使用。

type HybridFunding struct {
	userId         int
	pointsConsumed int // 累计从积分扣除(含预扣与补扣)
	walletConsumed int // 累计从钱包扣除
}

func (h *HybridFunding) Source() string { return BillingSourceHybrid }

// PointsConsumed 返回本会话累计的积分抵扣量（quota unit），供结算写 PointsUsed / 日志。
func (h *HybridFunding) PointsConsumed() int { return h.pointsConsumed }

// deduct 按「积分优先、不足扣钱包」扣减 amount，成功后累加内部计数。
// 积分条件扣减失败（并发被抢/缓存偏旧）时重读余额重试，只把真正扣不到的部分交给
// 钱包——此前失败即整笔甩给钱包，既违背积分优先、又放大钱包透支（codex review P1）。
//
// enforceWallet 区分两种时机的钱包语义（codex review 第四轮）：
//   - true（PreConsume/reserveExtra，服务未交付）：钱包走 TryDecreaseUserQuota 条件
//     扣减，余额不足则回滚已扣积分并拒绝请求——混扣用户可能从未充值，积分被并发
//     抢光后不允许钱包为积分的承诺透支为负（纯钱包路径靠预检兜底，结构上无此问题）；
//   - false（Settle 补扣，服务已交付）：保持无条件扣减，成本已发生允许欠费，
//     与 WalletFunding.Settle 同语义（改成条件扣会造成结算失败但 token 已消耗）。
func (h *HybridFunding) deduct(amount int, enforceWallet bool) error {
	if amount <= 0 {
		return nil
	}
	remaining := amount
	pTaken := 0
	// ①② 积分优先：CAS 失败说明余额被并发变动，重读后再扣；上限 3 次防高并发自旋。
	// 首次走 Redis 热路径；CAS 失败后强制回源 DB 权威值（codex review 第十轮）——
	// 并发扣减的缓存同步是异步的，Redis 可能短暂超前 DB，重读同一旧值会三连败、
	// 整笔误甩钱包；回源 DB 同时会把权威值刷回缓存（GetUserPoints 的回填逻辑）
	for attempt := 0; attempt < 3 && remaining > 0; attempt++ {
		points, err := model.GetUserPoints(h.userId, attempt > 0)
		if err != nil {
			return err
		}
		if points <= 0 {
			break
		}
		take := min(remaining, points)
		ok, err := model.TryDecreaseUserPoints(h.userId, take)
		if err != nil {
			return err
		}
		if ok {
			pTaken += take
			remaining -= take
		}
	}
	// ③ 剩余（积分真正扣不到的部分）走钱包
	if remaining > 0 {
		var walletErr error
		if enforceWallet {
			ok, err := model.TryDecreaseUserQuota(h.userId, remaining)
			if err != nil {
				walletErr = err
			} else if !ok {
				walletErr = ErrHybridWalletInsufficient
			}
		} else {
			walletErr = model.DecreaseUserQuota(h.userId, remaining, false)
		}
		if walletErr != nil {
			// ④ 钱包扣减失败/不足，回滚已扣积分。
			// 积分回补一律 db=true 直写：批量模式下若进队列延迟落库，Redis 先行超前，
			// 下一笔 TryDecreaseUserPoints（直击 DB）会误判不足（codex review 第七轮）
			if pTaken > 0 {
				_ = model.IncreaseUserPoints(h.userId, pTaken, true)
			}
			return walletErr
		}
	}
	h.pointsConsumed += pTaken
	h.walletConsumed += remaining
	return nil
}

func (h *HybridFunding) PreConsume(amount int) error {
	return h.deduct(amount, true)
}

// reserveExtra 追加预扣 delta，返回本次的积分/钱包拆分，供 unreserveExtra 精确回滚。
// 回滚必须逆转「刚扣的这一刀」而非套用 Settle 的「先退钱包」全局策略——若原始预扣
// 走过钱包、追加这笔却走了积分（中途积分被补回），Settle(-delta) 会退错桶，
// 把原始钱包钱退掉、留着这笔积分不退，余额与内部计数双双错位（codex review P2）。
func (h *HybridFunding) reserveExtra(delta int) (pPart, wPart int, err error) {
	pBefore, wBefore := h.pointsConsumed, h.walletConsumed
	// 补预扣仍在交付前，钱包同样强制余额充足
	if err = h.deduct(delta, true); err != nil {
		return 0, 0, err
	}
	return h.pointsConsumed - pBefore, h.walletConsumed - wBefore, nil
}

// unreserveExtra 按 reserveExtra 返回的拆分精确原路退还，并反向修正内部计数。
// 失败仅记日志（与 rollbackFundingReserve 其它分支口径一致），不中断上层错误返回。
func (h *HybridFunding) unreserveExtra(pPart, wPart int) {
	if wPart > 0 {
		if err := model.IncreaseUserQuota(h.userId, wPart, false); err != nil {
			common.SysLog("error unreserving hybrid wallet part: " + err.Error())
		} else {
			h.walletConsumed -= wPart
		}
	}
	if pPart > 0 {
		// db=true 直写，防批量队列与 TryDecrease 顺序倒置
		if err := model.IncreaseUserPoints(h.userId, pPart, true); err != nil {
			common.SysLog("error unreserving hybrid points part: " + err.Error())
		} else {
			h.pointsConsumed -= pPart
		}
	}
}

func (h *HybridFunding) Settle(delta int) error {
	if delta == 0 {
		return nil
	}
	if delta > 0 {
		// 补扣：仍按积分优先；服务已交付，钱包保持无条件扣减（允许欠费）
		return h.deduct(delta, false)
	}
	// 退还：优先退钱包（保护用户真钱，§6.2），退完再退积分
	refund := -delta
	wRefund := refund
	if wRefund > h.walletConsumed {
		wRefund = h.walletConsumed
	}
	if wRefund > 0 {
		if err := model.IncreaseUserQuota(h.userId, wRefund, false); err != nil {
			return err
		}
		h.walletConsumed -= wRefund
	}
	pRefund := refund - wRefund
	if pRefund > 0 {
		// db=true 直写，防批量队列与 TryDecrease 顺序倒置
		if err := model.IncreaseUserPoints(h.userId, pRefund, true); err != nil {
			return err
		}
		h.pointsConsumed -= pRefund
	}
	return nil
}

// roundUpToWholePoints 结算收尾：把本次积分抵扣量向上取整到整积分——不足 1 积分按
// 1 积分烧（营销积分是平台成本而非真钱，取整加速消耗）。差额从积分余额 best-effort
// 补扣：余额不足或并发被抢则放弃取整（只多烧不虚记，pointsConsumed 与实际扣减恒一致）。
// 仅在 BillingSession.Settle 的 settled 闸门内调用一次；失败请求走 Refund 不取整。
func (h *HybridFunding) roundUpToWholePoints() {
	pc := h.pointsConsumed
	if pc <= 0 {
		return
	}
	extra := common.PointsToQuota(common.QuotaToPointsCeil(pc)) - pc
	if extra <= 0 {
		return
	}
	ok, err := model.TryDecreaseUserPoints(h.userId, extra)
	if err != nil {
		common.SysLog("error rounding up points consumption: " + err.Error())
		return
	}
	if ok {
		h.pointsConsumed += extra
	}
}

// Refund 按内部计数原路退还（积分 + 钱包）。与 WalletFunding.Refund 一样是非幂等加法，
// 不可重试（幂等由 BillingSession.refunded 标志保证）。
func (h *HybridFunding) Refund() error {
	if h.pointsConsumed > 0 {
		// db=true 直写，防批量队列与 TryDecrease 顺序倒置
		if err := model.IncreaseUserPoints(h.userId, h.pointsConsumed, true); err != nil {
			return err
		}
		h.pointsConsumed = 0
	}
	if h.walletConsumed > 0 {
		if err := model.IncreaseUserQuota(h.userId, h.walletConsumed, false); err != nil {
			return err
		}
		h.walletConsumed = 0
	}
	return nil
}
