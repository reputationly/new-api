package service

import (
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/bytedance/gopkg/util/gopool"
)

// DirectPaymentExpiryScanHook is set by controller in init() to break the
// service→controller import cycle. It must close expired pending orders for all
// direct payment channels (TopUp + Subscription × Alipay + WeChat Pay).
var DirectPaymentExpiryScanHook func()

const directPaymentExpiryScanInterval = 5 * time.Minute

// StartDirectPaymentExpiryScan kicks off a goroutine that periodically expires
// stale pending direct-payment orders. Safe to call when the hook is unset
// (e.g. running tests or builds without the controller wired in) — it becomes
// a no-op.
//
// 只在主节点上跑：这个任务对所有待支付订单调上游 TradeClose。多副本下每个 Pod 各扫一遍，
// 订单状态本身翻不坏（收尾走 model.UpdatePendingTopUpStatus，事务 + FOR UPDATE +
// pending 状态守卫，只会翻一次；支付宝的 ACQ.TRADE_HAS_CLOSE 也当正常处理），
// 但上游调用量会乘以副本数，属于纯浪费。
func StartDirectPaymentExpiryScan() {
	if DirectPaymentExpiryScanHook == nil {
		return
	}
	if !common.IsMasterNode {
		return
	}
	gopool.Go(func() {
		ticker := time.NewTicker(directPaymentExpiryScanInterval)
		defer ticker.Stop()
		for range ticker.C {
			DirectPaymentExpiryScanHook()
		}
	})
}
