package service

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// ErrWalletInsufficient 预扣时钱包（含可用授信）无法覆盖所需额度——拒绝请求而非
// 无限透支。哨兵错误供 billing_session 映射为 403 额度不足。
var ErrWalletInsufficient = errors.New("用户额度不足")

// ErrHybridWalletInsufficient 混扣预扣时积分不足（被并发抢占）且钱包无法覆盖剩余部分。
// 与 ErrWalletInsufficient 同值：两条路径的失败语义一致，errors.Is 互通，
// 调用方（billing_session 的 403 映射）无需区分。
var ErrHybridWalletInsufficient = ErrWalletInsufficient

// ---------------------------------------------------------------------------
// HybridFunding — 积分 + 钱包混合资金来源
// ---------------------------------------------------------------------------
//
// 积分优先扣、不足部分扣钱包余额（§6.2）。积分扣减走 TryDecreaseUserPoints 条件更新，
// 保证积分永不透支为负（§6.4）；并发被抢时降级由钱包承担。
//
// 扣减/退还的编排已下沉到 LayeredFunding（见 funding_layered.go）——本类型现在只
// 负责「积分层与钱包层各自怎么扣」，顺序、回滚、逆序退还都由引擎给出。加第三层
// （信用/权益）时构造一个层数不同的引擎即可，不必再动这里被多轮 review 打磨过的分支。
//
// pointsConsumed / walletConsumed 保持为字段而非引擎内部切片：它们是引擎里两个层的
// counter 指向的同一块内存，不存在两份真相。

type HybridFunding struct {
	userId         int
	pointsConsumed int // 累计从积分扣除(含预扣与补扣)
	walletConsumed int // 累计从钱包扣除
	engine         *LayeredFunding
}

func (h *HybridFunding) Source() string { return BillingSourceHybrid }

// PointsConsumed 返回本会话累计的积分抵扣量（quota unit），供结算写 PointsUsed / 日志。
func (h *HybridFunding) PointsConsumed() int { return h.pointsConsumed }

// layered 懒构造引擎。调用方一律用 &HybridFunding{userId: N} 构造（测试与生产都是），
// 没有统一的构造函数可挂，所以在入口处按需建。
func (h *HybridFunding) layered() *LayeredFunding {
	if h.engine == nil {
		h.engine = &LayeredFunding{layers: []fundingLayer{
			h.pointsLayer(),
			h.walletLayer(),
		}}
	}
	return h.engine
}

// pointsLayer 积分层：条件扣减，永不透支为负。
//
// CAS 失败说明余额被并发变动，重读后再扣；上限 3 次防高并发自旋。首次走 Redis
// 热路径；CAS 失败后强制回源 DB 权威值——并发扣减的缓存同步是异步的，Redis 可能
// 短暂超前 DB，重读同一旧值会三连败、整笔误甩钱包；回源 DB 同时会把权威值刷回缓存
// （GetUserPoints 的回填逻辑）。
//
// ⚠️ 已知的既有边界：重试途中 GetUserPoints 报错时返回 0 而非已扣量，于是引擎
// 不会回滚这次循环里已经 CAS 扣掉的积分，它们既不在账上也不在计数器里。这是泛化
// 之前就有的行为，本次重构以「行为零变化」为验收标准，原样保留而不顺手改掉——
// 改它等于在一次本该可证明等价的重构里夹带一个未经单独评审的资金行为变更。
func (h *HybridFunding) pointsLayer() fundingLayer {
	return fundingLayer{
		name:    "points",
		counter: &h.pointsConsumed,
		tryTake: func(amount int, _ bool) (int, error) {
			taken := 0
			remaining := amount
			for attempt := 0; attempt < 3 && remaining > 0; attempt++ {
				points, err := model.GetUserPoints(h.userId, attempt > 0)
				if err != nil {
					return 0, err
				}
				if points <= 0 {
					break
				}
				take := min(remaining, points)
				ok, err := model.TryDecreaseUserPoints(h.userId, take)
				if err != nil {
					return 0, err
				}
				if ok {
					taken += take
					remaining -= take
				}
			}
			return taken, nil
		},
		giveBack: func(amount int) error {
			// db=true 直写：批量模式下若进队列延迟落库，Redis 先行超前，
			// 下一笔 TryDecreaseUserPoints（直击 DB）会误判不足。
			return model.IncreaseUserPoints(h.userId, amount, true)
		},
	}
}

// walletLayer 钱包层，兜底承担积分扣不到的部分。
//
// enforce 区分两种时机：
//   - true（PreConsume/reserveExtra，服务未交付）：允许透支到可用授信之内
//     （未开授信者等价于「余额必须充足」），扣不动则整笔失败、由引擎回滚已扣积分。
//     混扣用户可能从未充值，积分被并发抢光后不允许钱包为积分的承诺无限透支；
//     而用 TryDecreaseUserQuota 的话，授信客户只要还持有一点营销积分就会走到这条
//     混扣分支、然后因钱包不足被 403——可用额检查刚刚才把授信算进去放行了它。
//     透支部分在结算时由 syncCreditConsumed 结转进 credit_used。
//   - false（Settle 补扣，服务已交付）：无条件扣减，成本已发生允许欠费，
//     与 WalletFunding.Settle 同语义（改成条件扣会造成结算失败但 token 已消耗）。
func (h *HybridFunding) walletLayer() fundingLayer {
	return fundingLayer{
		name:    "wallet",
		counter: &h.walletConsumed,
		tryTake: func(amount int, enforce bool) (int, error) {
			if !enforce {
				if err := model.DecreaseUserQuota(h.userId, amount, false); err != nil {
					return 0, err
				}
				return amount, nil
			}
			ok, err := model.TryDecreaseUserQuotaWithinCredit(h.userId, amount)
			if err != nil {
				return 0, err
			}
			if !ok {
				return 0, ErrHybridWalletInsufficient
			}
			return amount, nil
		},
		giveBack: func(amount int) error {
			return model.IncreaseUserQuota(h.userId, amount, false)
		},
	}
}

func (h *HybridFunding) deduct(amount int, enforceWallet bool) error {
	_, err := h.layered().deduct(amount, enforceWallet, ErrHybridWalletInsufficient)
	return err
}

func (h *HybridFunding) PreConsume(amount int) error {
	return h.deduct(amount, true)
}

// reserveExtra 追加预扣 delta，返回本次的积分/钱包拆分，供 unreserveExtra 精确回滚。
func (h *HybridFunding) reserveExtra(delta int) (pPart, wPart int, err error) {
	// 补预扣仍在交付前，钱包同样强制余额充足
	taken, err := h.layered().deduct(delta, true, ErrHybridWalletInsufficient)
	if err != nil {
		return 0, 0, err
	}
	return taken[0], taken[1], nil
}

// unreserveExtra 按 reserveExtra 返回的拆分精确原路退还，并反向修正内部计数。
func (h *HybridFunding) unreserveExtra(pPart, wPart int) {
	h.layered().unreserve([]int{pPart, wPart})
}

func (h *HybridFunding) Settle(delta int) error {
	if delta == 0 {
		return nil
	}
	if delta > 0 {
		// 补扣：仍按积分优先；服务已交付，钱包保持无条件扣减（允许欠费）
		return h.deduct(delta, false)
	}
	// 退还：按扣减顺序的逆序，即先退钱包（保护用户真钱，§6.2），退完再退积分
	return h.layered().refundReverse(-delta)
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
	return h.layered().refundAll()
}
