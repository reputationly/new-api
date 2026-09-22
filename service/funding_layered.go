package service

import (
	"github.com/QuantumNous/new-api/common"
)

// ---------------------------------------------------------------------------
// LayeredFunding — 多层资金来源的通用引擎
// ---------------------------------------------------------------------------
//
// 设计见 docs/revenue-reconciliation-design.md §5.2。
//
// 此前「积分 → 钱包」是两层硬编码，用 pointsConsumed / walletConsumed 两个变量
// 互相咬。加第三层意味着五条路径（PreConsume / reserveExtra / unreserveExtra /
// Settle 正负两支 / Refund）全部要改，而其中 unreserveExtra 必须**精确逆转刚扣的
// 那一刀**而非套用全局策略——这个约束随层数增加组合爆炸，是这个项目里最容易
// 产出资金事故的写法。
//
// 泛化后：
//   - 扣减快照是 []int，精确回滚天然成立，不再靠两个变量互相咬；
//   - 退还顺序 = 扣减顺序的逆序（先退最后扣的），不需要特例；
//   - 现有「积分 + 钱包」= 恰好两层。

// fundingLayer 一个资金层。
//
// counter 指向该层累计扣减量的存放位置。用指针而不是引擎内部的切片，是为了让
// HybridFunding 那两个被测试直接读取的字段（pointsConsumed / walletConsumed）
// 继续作为唯一真相——否则引擎与外壳各存一份，同步漏一处就是账不平。
type fundingLayer struct {
	name string
	// tryTake 尝试从本层扣 amount，返回**实际扣到的量**。
	//
	// enforce 区分两种时机：true 表示服务未交付（预扣/补预扣），本层不得为这笔
	// 扣减制造亏空，扣不动就报错拒绝请求；false 表示服务已交付（结算补扣），
	// 成本已发生，允许欠费。
	tryTake func(amount int, enforce bool) (int, error)
	// giveBack 原路退还 amount。
	giveBack func(amount int) error
	counter  *int
}

// LayeredFunding 按层序扣减、按逆序退还的通用引擎。
type LayeredFunding struct {
	layers []fundingLayer
}

// deduct 按层序扣减 amount，返回每层本次实际扣到的量（与 layers 同序）。
//
// 整笔原子：任一层报错，或全部层扣完仍有缺口，都会把**本次**已扣的部分逐层退回
// 并返回错误，计数器不变。计数器只在整笔成功后才提交——这保证「计数器所记」与
// 「账上实扣」始终一致，退款时才不会退多或退少。
//
// shortfall 是全部层都扣不动时返回的错误，由调用方决定语义（混扣路径要求它与
// ErrWalletInsufficient 同值，好让上层 403 映射不必区分来源）。
func (l *LayeredFunding) deduct(amount int, enforce bool, shortfall error) ([]int, error) {
	taken := make([]int, len(l.layers))
	if amount <= 0 {
		return taken, nil
	}
	remaining := amount
	for i := range l.layers {
		if remaining <= 0 {
			break
		}
		got, err := l.layers[i].tryTake(remaining, enforce)
		if got > 0 {
			taken[i] = got
			remaining -= got
		}
		if err != nil {
			l.giveBackSnapshot(taken)
			return nil, err
		}
	}
	if remaining > 0 {
		l.giveBackSnapshot(taken)
		return nil, shortfall
	}
	for i := range l.layers {
		if taken[i] > 0 {
			*l.layers[i].counter += taken[i]
		}
	}
	return taken, nil
}

// giveBackSnapshot 按快照逐层精确退还，用于扣减失败时回滚**本次**已扣的部分。
//
// 不碰计数器：调用它的时机都是「这一刀还没提交」，计数器里本就没有这部分。
// 失败只记日志不中断——此时已经在错误路径上，退还失败要留给对账发现，
// 让它覆盖掉原始错误会把根因弄丢。
func (l *LayeredFunding) giveBackSnapshot(taken []int) {
	for i := len(taken) - 1; i >= 0; i-- {
		if taken[i] <= 0 {
			continue
		}
		if err := l.layers[i].giveBack(taken[i]); err != nil {
			common.SysLog("error rolling back funding layer " + l.layers[i].name + ": " + err.Error())
		}
	}
}

// unreserve 按快照精确逆转一笔已提交的扣减，并同步回退计数器。
//
// 与 giveBackSnapshot 的区别是它要改计数器：这笔已经提交过了。必须按快照逆转
// 而不是套用「先退某一层」的全局策略——若原始预扣走过钱包、追加这笔却走了积分
// （中途积分被补回），按全局策略会退错桶，把原始钱包的钱退掉、留着这笔积分不退，
// 余额与计数器双双错位。
func (l *LayeredFunding) unreserve(taken []int) {
	for i := len(taken) - 1; i >= 0; i-- {
		if i >= len(l.layers) || taken[i] <= 0 {
			continue
		}
		if err := l.layers[i].giveBack(taken[i]); err != nil {
			common.SysLog("error unreserving funding layer " + l.layers[i].name + ": " + err.Error())
			continue
		}
		*l.layers[i].counter -= taken[i]
	}
}

// refundReverse 退还 amount，顺序为扣减顺序的逆序（先退最后扣的那层）。
//
// 逆序是自然语义：最后扣的那层是「兜底垫付」的，优先把它还回去。现有
// 「积分 → 钱包」两层下，逆序恰好等于「先退钱包」——保护用户真钱，
// 与泛化前的策略一致，不是巧合而是同一条原则的两种说法。
//
// 每层最多退到它自己的累计扣减量，退不完的余额留给下一层，避免某层被退成负数。
func (l *LayeredFunding) refundReverse(amount int) error {
	remaining := amount
	for i := len(l.layers) - 1; i >= 0 && remaining > 0; i-- {
		layer := l.layers[i]
		part := remaining
		if part > *layer.counter {
			part = *layer.counter
		}
		if part <= 0 {
			continue
		}
		if err := layer.giveBack(part); err != nil {
			return err
		}
		*layer.counter -= part
		remaining -= part
	}
	return nil
}

// refundAll 退还各层全部累计扣减量并清零计数器。
//
// 按层序（而非逆序）退：全额退款下顺序不影响最终结果，保持与泛化前一致的
// 「先积分后钱包」，少一处无谓的行为差异。任一层失败即中断——与泛化前一样，
// 幂等由调用方的 refunded 标志保证，这里不能重试（加法非幂等，重试会多退）。
func (l *LayeredFunding) refundAll() error {
	for i := range l.layers {
		layer := l.layers[i]
		if *layer.counter <= 0 {
			continue
		}
		if err := layer.giveBack(*layer.counter); err != nil {
			return err
		}
		*layer.counter = 0
	}
	return nil
}
