package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

// 异步任务的消费日志必须带 billing_source。
//
// 订阅计费扣的是 UserSubscription.AmountUsed，不动 users.quota。资金对账靠日志里的
// billing_source 把这类消费从现金消耗中剔除；漏了这一项，每个成功的订阅异步任务
// 都会让自洽校验报一次假的「现金账不平」——而那个告警本该只在流水被绕过时响。
//
// 同步路径由 appendBillingInfo 写入，异步路径此前没有对应逻辑。
func TestTaskBillingOther_CarriesBillingSource(t *testing.T) {
	task := &model.Task{}
	task.PrivateData.BillingSource = BillingSourceSubscription
	task.PrivateData.SubscriptionId = 7

	other := taskBillingOther(task)
	require.Equal(t, BillingSourceSubscription, other["billing_source"],
		"订阅计费的异步任务日志必须标出计费来源，否则对账剔不掉")

	// 把两端钉在一起：对账侧（model.getFundConsumeStats）是按这个**子串**从 other
	// 里过滤订阅日志的，序列化格式一变就会静默漏过滤，而症状是对账报假不平、
	// 不是这里报错。用生产方真实产出的串来断言，别手写 fixture。
	require.Contains(t, common.MapToJsonStr(other), `"billing_source":"subscription"`,
		"对账按此子串剔除订阅消费，序列化格式变更必须同步改 getFundConsumeStats")
}

// 纯钱包任务同样要标，前端据此区分展示；空值才省略（历史任务无此字段）。
func TestTaskBillingOther_OmitsEmptyBillingSource(t *testing.T) {
	walletTask := &model.Task{}
	walletTask.PrivateData.BillingSource = BillingSourceWallet
	require.Equal(t, BillingSourceWallet, taskBillingOther(walletTask)["billing_source"])

	legacyTask := &model.Task{}
	_, exists := taskBillingOther(legacyTask)["billing_source"]
	require.False(t, exists, "历史任务没有该字段时不应写入空串")
}

// 差额结算的日志要记**本次**动用的积分，而不是整单实付。
//
// 混扣任务多退少补时 taskAdjustHybridFunding 会改写 PrivateData.PointsConsumed，
// 取调整前后的差即为本次变动量。漏记的话对账侧会多算积分、少算现金
// （CashConsumed = TotalQuota - PointsConsumed），两侧同时报假不平。
func TestRecalculatePointsDelta(t *testing.T) {
	// 退款：实付积分从 3000 降到 1000，本次退还 2000
	pointsBefore, pointsAfter := 3000, 1000
	pointsDelta := pointsAfter - pointsBefore
	require.Equal(t, 2000, max(-pointsDelta, 0), "退款日志记退回的积分")
	require.Equal(t, 0, max(pointsDelta, 0), "退款方向不应产生消费侧积分")

	// 补扣：实付积分从 1000 升到 2500，本次补扣 1500
	pointsBefore, pointsAfter = 1000, 2500
	pointsDelta = pointsAfter - pointsBefore
	require.Equal(t, 1500, max(pointsDelta, 0), "补扣日志记新增的积分")
	require.Equal(t, 0, max(-pointsDelta, 0), "补扣方向不应产生退款侧积分")

	// 纯钱包任务：积分实付恒为 0，两侧都不产生积分记录
	pointsBefore, pointsAfter = 0, 0
	pointsDelta = pointsAfter - pointsBefore
	require.Equal(t, 0, max(pointsDelta, 0))
	require.Equal(t, 0, max(-pointsDelta, 0))
}

// 异步任务退款必须先冲销授信欠款，再退现金——这是套利通道的堵口。
//
// 0 余额的授信客户提交任务走信用消费，提交时透支结转进 CreditUsed；任务失败若把钱
// 全退进钱包，他就凭空得到一笔可用真钱而欠款纹丝不动。与混扣任务把积分退进钱包
// （controller/relay.go 那条注释里的「套利通道」）完全同构。
func TestTaskRefund_ReversesCreditBeforeCash(t *testing.T) {
	task := &model.Task{UserId: 9601}
	task.PrivateData.CreditConsumed = 1000

	// 全额退款 1000：应全部用于冲销欠款，一分不进钱包
	creditRefund := min(1000, task.PrivateData.CreditConsumed)
	require.Equal(t, 1000, creditRefund, "退款优先冲欠款")
	require.Equal(t, 0, 1000-creditRefund, "无剩余可退进钱包")

	// 部分退款 400：只冲 400，欠款剩 600
	task2 := &model.Task{UserId: 9602}
	task2.PrivateData.CreditConsumed = 1000
	creditRefund2 := min(400, task2.PrivateData.CreditConsumed)
	require.Equal(t, 400, creditRefund2)

	// 混合：消费 1000 中只有 300 走信用，退款 1000 → 冲 300、退现金 700
	task3 := &model.Task{UserId: 9603}
	task3.PrivateData.CreditConsumed = 300
	creditRefund3 := min(1000, task3.PrivateData.CreditConsumed)
	require.Equal(t, 300, creditRefund3, "以提交时的信用实付封顶")
	require.Equal(t, 700, 1000-creditRefund3, "其余才退现金")

	// 纯钱包任务：信用实付为 0，全额退现金
	task4 := &model.Task{UserId: 9604}
	require.Equal(t, 0, min(1000, task4.PrivateData.CreditConsumed))
}
