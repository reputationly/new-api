package service

import (
	"testing"
)

// 产物审核拦截后「正常计费、不退款」的唯一依托，是 BillingSession 结算之后
// Refund 自动作废。
//
// 这条链路横跨三层：适配器判违规 → ImageHelper 显式 PostTextConsumeQuota →
// controller/relay.go 的 defer 无条件调 Billing.Refund(c)。中间任何一环变了，
// 钱都会静悄悄退回去，而拦截记录里写着已按实际消耗计费——账实不符，
// 且没有任何报错提示。
func TestSettledSessionDoesNotRefund(t *testing.T) {
	s := &BillingSession{}

	// 未结算、有预扣 → 该退
	s.tokenConsumed = 100
	if !s.needsRefundLocked() {
		t.Fatal("未结算且有预扣时应当需要退款——否则普通的失败请求会白扣用户的钱")
	}

	// 结算过 → 不该退。这正是产物拦截依赖的那一条。
	s.settled = true
	if s.needsRefundLocked() {
		t.Fatal("结算之后必须不再退款：产物审核拦截靠显式结算把 defer 里的退款顶掉，" +
			"这个保护没了就变成「计了费又退回去」")
	}

	// 资金侧已提交同样不能退（两个标记各自独立生效）
	s2 := &BillingSession{tokenConsumed: 100, fundingSettled: true}
	if s2.needsRefundLocked() {
		t.Fatal("资金来源已提交后不能再退预扣费")
	}

	// 已退过不重复退
	s3 := &BillingSession{tokenConsumed: 100, refunded: true}
	if s3.needsRefundLocked() {
		t.Fatal("退款必须幂等")
	}
}
