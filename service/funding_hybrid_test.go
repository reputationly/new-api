package service

import (
	"fmt"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/require"
)

// 复用本包 TestMain（内存 SQLite、RedisEnabled=false、BatchUpdateEnabled=false，
// 钱包/积分增减同步落库）。

func seedHybridUser(t *testing.T, id, points, quota int) {
	t.Helper()
	err := model.DB.Create(&model.User{
		Id:            id,
		Username:      fmt.Sprintf("hf_%d", id),
		Role:          1,
		Status:        1,
		PointsBalance: points,
		Quota:         quota,
	}).Error
	require.NoError(t, err)
}

func hybridBalances(t *testing.T, id int) (points, quota int) {
	t.Helper()
	var u model.User
	require.NoError(t, model.DB.Select("points_balance", "quota").Where("id = ?", id).First(&u).Error)
	return u.PointsBalance, u.Quota
}

func cleanHybridUsers(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { model.DB.Exec("DELETE FROM users") })
}

// 积分充足 → 全走积分，钱包不动。
func TestHybridPreConsume_AllPoints(t *testing.T) {
	cleanHybridUsers(t)
	seedHybridUser(t, 201, 1000, 1000)

	h := &HybridFunding{userId: 201}
	require.NoError(t, h.PreConsume(500))

	require.Equal(t, 500, h.pointsConsumed)
	require.Equal(t, 0, h.walletConsumed)
	p, q := hybridBalances(t, 201)
	require.Equal(t, 500, p)
	require.Equal(t, 1000, q, "钱包不应被动用")
}

// 积分不足 → 积分扣光、剩余混扣钱包。
func TestHybridPreConsume_Mix(t *testing.T) {
	cleanHybridUsers(t)
	seedHybridUser(t, 202, 300, 1000)

	h := &HybridFunding{userId: 202}
	require.NoError(t, h.PreConsume(500))

	require.Equal(t, 300, h.pointsConsumed)
	require.Equal(t, 200, h.walletConsumed)
	p, q := hybridBalances(t, 202)
	require.Equal(t, 0, p)
	require.Equal(t, 800, q)
}

// 无积分 → 全走钱包。
func TestHybridPreConsume_AllWallet(t *testing.T) {
	cleanHybridUsers(t)
	seedHybridUser(t, 203, 0, 1000)

	h := &HybridFunding{userId: 203}
	require.NoError(t, h.PreConsume(500))

	require.Equal(t, 0, h.pointsConsumed)
	require.Equal(t, 500, h.walletConsumed)
	p, q := hybridBalances(t, 203)
	require.Equal(t, 0, p)
	require.Equal(t, 500, q)
}

// 预扣钱包条件扣减（codex review 第四轮）：积分不足（被抢光）且钱包余额不够时
// 拒绝请求，钱包不得为积分的承诺透支为负。
func TestHybridPreConsume_WalletInsufficientRejected(t *testing.T) {
	cleanHybridUsers(t)
	seedHybridUser(t, 212, 0, 300) // 无积分,钱包 300 < 500

	h := &HybridFunding{userId: 212}
	require.Error(t, h.PreConsume(500))

	require.Equal(t, 0, h.pointsConsumed)
	require.Equal(t, 0, h.walletConsumed)
	p, q := hybridBalances(t, 212)
	require.Equal(t, 0, p)
	require.Equal(t, 300, q, "钱包必须原封不动,不得透支为负")
}

// 部分积分已扣、钱包不足以覆盖剩余 → 回滚已扣积分,整体原子失败。
func TestHybridPreConsume_RollsBackPointsOnWalletShortfall(t *testing.T) {
	cleanHybridUsers(t)
	seedHybridUser(t, 213, 200, 100) // 积分 200 + 钱包 100 < 500

	h := &HybridFunding{userId: 213}
	require.Error(t, h.PreConsume(500)) // 积分扣 200 后剩 300 > 钱包 100 → 拒绝

	require.Equal(t, 0, h.pointsConsumed)
	require.Equal(t, 0, h.walletConsumed)
	p, q := hybridBalances(t, 213)
	require.Equal(t, 200, p, "已扣积分必须回滚")
	require.Equal(t, 100, q)
}

// Settle 补扣（服务已交付）保持无条件语义：钱包允许欠费为负,不得因余额不足失败。
func TestHybridSettleExtra_AllowsWalletOverdraft(t *testing.T) {
	cleanHybridUsers(t)
	seedHybridUser(t, 214, 0, 100)

	h := &HybridFunding{userId: 214}
	require.NoError(t, h.PreConsume(100)) // 钱包恰好扣光

	require.NoError(t, h.Settle(500)) // 补扣 500:成本已发生,允许欠费
	require.Equal(t, 600, h.walletConsumed)
	_, q := hybridBalances(t, 214)
	require.Equal(t, -500, q, "结算补扣保持无条件,与 WalletFunding 同语义")
}

// Settle(delta>0) 补扣仍积分优先。
func TestHybridSettle_ExtraCharge(t *testing.T) {
	cleanHybridUsers(t)
	seedHybridUser(t, 204, 1000, 1000)

	h := &HybridFunding{userId: 204}
	require.NoError(t, h.PreConsume(500)) // 积分 500 剩 500

	require.NoError(t, h.Settle(200)) // 再扣 200，积分优先
	require.Equal(t, 700, h.pointsConsumed)
	require.Equal(t, 0, h.walletConsumed)
	p, q := hybridBalances(t, 204)
	require.Equal(t, 300, p)
	require.Equal(t, 1000, q)
}

// Settle(delta<0) 退还：优先退钱包（保护真钱），退完再退积分。
func TestHybridSettle_Refund(t *testing.T) {
	cleanHybridUsers(t)
	seedHybridUser(t, 205, 300, 1000)

	h := &HybridFunding{userId: 205}
	require.NoError(t, h.PreConsume(500)) // pc=300 wc=200

	require.NoError(t, h.Settle(-300)) // 退 300：先退钱包 200，再退积分 100
	require.Equal(t, 200, h.pointsConsumed)
	require.Equal(t, 0, h.walletConsumed)
	p, q := hybridBalances(t, 205)
	require.Equal(t, 100, p)
	require.Equal(t, 1000, q)
}

// 并发争抢积分时（CAS 失败重读重试，codex review P1）：积分必须被扣干净后钱包才兜底，
// 不允许「CAS 失败整笔甩钱包、积分却还有剩」；且总量守恒、积分不为负。
func TestHybridDeduct_ConcurrentPointsContention(t *testing.T) {
	cleanHybridUsers(t)
	seedHybridUser(t, 207, 600, 10000)

	const n = 2
	const amount = 500 // 两笔共 1000 > 积分 600，必然有一笔要动钱包
	fundings := make([]*HybridFunding, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		fundings[i] = &HybridFunding{userId: 207}
		wg.Add(1)
		go func(h *HybridFunding) {
			defer wg.Done()
			require.NoError(t, h.PreConsume(amount))
		}(fundings[i])
	}
	wg.Wait()

	totalPoints, totalWallet := 0, 0
	for _, h := range fundings {
		totalPoints += h.pointsConsumed
		totalWallet += h.walletConsumed
	}
	p, q := hybridBalances(t, 207)
	require.Equal(t, 0, p, "总需求超过积分时积分应被扣干净（重试保证积分优先）")
	require.Equal(t, 600, totalPoints, "积分抵扣总量 = 初始积分")
	require.Equal(t, n*amount-600, totalWallet, "钱包只承担积分真正扣不到的部分")
	require.Equal(t, 10000-totalWallet, q)
}

// 追加预扣的精确回滚（codex review P2）：原始预扣走过钱包、中途积分被补回、
// 追加预扣走了积分——回滚必须退这笔积分，不能按 Settle 的「先退钱包」策略退错桶。
func TestHybridReserveExtra_ExactRollback(t *testing.T) {
	cleanHybridUsers(t)
	seedHybridUser(t, 208, 0, 1000)

	h := &HybridFunding{userId: 208}
	require.NoError(t, h.PreConsume(500)) // 无积分 → 全走钱包：pc=0 wc=500

	// 中途积分被补回（另一笔请求退款/运营发放）
	require.NoError(t, model.IncreaseUserPoints(208, 300, true))

	// 追加预扣 200 → 走积分
	pPart, wPart, err := h.reserveExtra(200)
	require.NoError(t, err)
	require.Equal(t, 200, pPart)
	require.Equal(t, 0, wPart)

	// 精确回滚：退的是这 200 积分，钱包不动
	h.unreserveExtra(pPart, wPart)
	require.Equal(t, 0, h.pointsConsumed)
	require.Equal(t, 500, h.walletConsumed, "原始钱包预扣必须保持在账")
	p, q := hybridBalances(t, 208)
	require.Equal(t, 300, p, "积分应完整退回")
	require.Equal(t, 500, q, "钱包不得被错误退款（旧 Settle(-delta) 会退到 700）")

	// 会话继续可正常退款：原始 500 原路退回钱包
	require.NoError(t, h.Refund())
	p, q = hybridBalances(t, 208)
	require.Equal(t, 300, p)
	require.Equal(t, 1000, q)
}

// 消费结算向上取整到整积分：不足 1 积分按 1 积分烧（qpp≈684.93 默认值：
// 1000 unit ≈ 1.46 积分 → 取整 2 积分 = 1370 unit，补烧 370）。
func TestHybridRoundUp_WholePoints(t *testing.T) {
	cleanHybridUsers(t)
	seedHybridUser(t, 209, 10000, 0)

	h := &HybridFunding{userId: 209}
	require.NoError(t, h.PreConsume(1000)) // pc=1000，余额 9000

	h.roundUpToWholePoints()
	require.Equal(t, 1370, h.pointsConsumed, "1.46 积分应取整为 2 积分 = 1370 unit")
	p, _ := hybridBalances(t, 209)
	require.Equal(t, 10000-1370, p, "差额 370 从余额补烧")
}

// 取整差额余额不足（或并发被抢）→ 放弃取整，pointsConsumed 与实际扣减保持一致。
func TestHybridRoundUp_InsufficientExtra(t *testing.T) {
	cleanHybridUsers(t)
	seedHybridUser(t, 210, 1000, 5000)

	h := &HybridFunding{userId: 210}
	require.NoError(t, h.PreConsume(1000)) // 积分恰好扣光，余额 0

	h.roundUpToWholePoints()
	require.Equal(t, 1000, h.pointsConsumed, "余额不足补烧时保持实际扣减量")
	p, q := hybridBalances(t, 210)
	require.Equal(t, 0, p)
	require.Equal(t, 5000, q, "钱包不参与取整补烧")
}

// 走 BillingSession.Settle 全链路：取整在 settled 闸门内执行一次，
// PointsConsumed（日志）与 points_used（对账）均为取整后的值，重复 Settle 不再取整。
func TestBillingSessionSettle_RoundsUpPoints(t *testing.T) {
	cleanHybridUsers(t)
	seedHybridUser(t, 211, 10000, 0)

	relayInfo := &relaycommon.RelayInfo{UserId: 211, IsPlayground: true}
	s := &BillingSession{relayInfo: relayInfo, funding: &HybridFunding{userId: 211}}

	require.NoError(t, s.Settle(1000)) // 信任旁路语义：预扣 0，结算全额
	require.Equal(t, 1370, relayInfo.PointsConsumed)
	p, _ := hybridBalances(t, 211)
	require.Equal(t, 10000-1370, p)
	var used int
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", 211).Select("points_used").Find(&used).Error)
	require.Equal(t, 1370, used)

	// 幂等：重复 Settle 不重复取整/记账
	require.NoError(t, s.Settle(1000))
	p2, _ := hybridBalances(t, 211)
	require.Equal(t, p, p2)
}

// 批量模式下混扣退款必须直写 DB（codex review 第七轮）：db=false 会进批量队列延迟
// 落库，窗口内 TryDecreaseUserPoints（直击 DB）误判不足。退款后立即再扣必须成功。
func TestHybridRefund_BatchModeWritesDBImmediately(t *testing.T) {
	cleanHybridUsers(t)
	seedHybridUser(t, 215, 1000, 0)

	common.BatchUpdateEnabled = true
	t.Cleanup(func() { common.BatchUpdateEnabled = false })

	h := &HybridFunding{userId: 215}
	require.NoError(t, h.PreConsume(1000)) // 积分扣光
	require.NoError(t, h.Refund())         // 退款须直写 DB，不得进批量队列

	p, _ := hybridBalances(t, 215)
	require.Equal(t, 1000, p, "退款必须立即落库")

	// 退款后立即再扣（新会话）：DB 已就绪，条件扣减必须成功
	h2 := &HybridFunding{userId: 215}
	require.NoError(t, h2.PreConsume(800))
	require.Equal(t, 800, h2.pointsConsumed, "积分优先不得因批量队列滞后失效")
	p, _ = hybridBalances(t, 215)
	require.Equal(t, 200, p)
}

// ---------------------------------------------------------------------------
// 异步任务混扣退款/重算（codex review 第九轮）：轮询期按持久化拆分原路调整
// ---------------------------------------------------------------------------

func hybridTask(userId, quota, pointsConsumed int) *model.Task {
	return &model.Task{
		UserId: userId,
		Quota:  quota,
		PrivateData: model.TaskPrivateData{
			BillingSource:  BillingSourceHybrid,
			PointsConsumed: pointsConsumed,
		},
	}
}

// 全额退款精确还原拆分：积分回积分、钱包回钱包——堵死积分→钱包套利。
func TestTaskAdjustHybrid_FullRefundRestoresSplit(t *testing.T) {
	cleanHybridUsers(t)
	seedHybridUser(t, 216, 0, 0) // 提交时已扣光：pc=300 wc=200

	task := hybridTask(216, 500, 300)
	require.NoError(t, taskAdjustFunding(task, -500))

	p, q := hybridBalances(t, 216)
	require.Equal(t, 300, p, "积分实付部分必须退回积分，不得洗进钱包")
	require.Equal(t, 200, q)
	require.Equal(t, 0, task.PrivateData.PointsConsumed)
}

// 部分退款钱包优先（真钱保护）：不动积分份额。
func TestTaskAdjustHybrid_PartialRefundWalletFirst(t *testing.T) {
	cleanHybridUsers(t)
	seedHybridUser(t, 217, 0, 0)

	task := hybridTask(217, 500, 300) // wc=200
	require.NoError(t, taskAdjustFunding(task, -100))
	task.Quota -= 100 // 调用方契约：RecalculateTaskQuota 调整后更新 task.Quota

	p, q := hybridBalances(t, 217)
	require.Equal(t, 0, p)
	require.Equal(t, 100, q, "退款额未超钱包份额时全退钱包")
	require.Equal(t, 300, task.PrivateData.PointsConsumed)

	// 继续退 300：钱包份额剩 100，越界部分 200 退积分
	require.NoError(t, taskAdjustFunding(task, -300))
	p, q = hybridBalances(t, 217)
	require.Equal(t, 200, p)
	require.Equal(t, 200, q)
	require.Equal(t, 100, task.PrivateData.PointsConsumed)
}

// 重算补扣积分优先，拆分与 PointsUsed 同步累加。
func TestTaskAdjustHybrid_ExtraChargePointsFirst(t *testing.T) {
	cleanHybridUsers(t)
	seedHybridUser(t, 218, 10000, 0)

	task := hybridTask(218, 500, 500)
	require.NoError(t, taskAdjustFunding(task, 1000))

	p, q := hybridBalances(t, 218)
	require.Equal(t, 9000, p, "补扣应积分优先")
	require.Equal(t, 0, q)
	require.Equal(t, 1500, task.PrivateData.PointsConsumed)
	var used int
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", 218).Select("points_used").Find(&used).Error)
	require.Equal(t, 1000, used)
}

// 纯钱包任务回归：行为不变，只动 quota。
func TestTaskAdjustFunding_WalletTaskUnchanged(t *testing.T) {
	cleanHybridUsers(t)
	seedHybridUser(t, 219, 1000, 0)

	task := &model.Task{
		UserId:      219,
		Quota:       500,
		PrivateData: model.TaskPrivateData{BillingSource: BillingSourceWallet},
	}
	require.NoError(t, taskAdjustFunding(task, -500))
	p, q := hybridBalances(t, 219)
	require.Equal(t, 1000, p, "钱包任务不得动积分")
	require.Equal(t, 500, q)
}

// Refund 按内部计数原路全额退还，计数清零。
func TestHybridRefund_All(t *testing.T) {
	cleanHybridUsers(t)
	seedHybridUser(t, 206, 300, 1000)

	h := &HybridFunding{userId: 206}
	require.NoError(t, h.PreConsume(500)) // pc=300 wc=200

	require.NoError(t, h.Refund())
	require.Equal(t, 0, h.pointsConsumed)
	require.Equal(t, 0, h.walletConsumed)
	p, q := hybridBalances(t, 206)
	require.Equal(t, 300, p, "积分原路退还")
	require.Equal(t, 1000, q, "钱包原路退还")
}
