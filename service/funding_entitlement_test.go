package service

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/require"
)

// 造一个已命中的权益 + 一批算力点。权益匹配本身由 model 侧测试覆盖，
// 这里只关心「命中之后怎么扣、怎么退」。
func seedEntitlementFunding(t *testing.T, userId int, limit int64, consumePoints bool, discount float64, lotPoints int64) *EntitlementFunding {
	t.Helper()
	ent := &model.SubscriptionPlanEntitlement{
		Id:              1,
		Models:          "m",
		ConsumePoints:   consumePoints,
		ConsumeDiscount: discount,
		LimitCount:      limit,
	}
	counter := &model.UserSubscriptionEntitlement{
		UserId: userId, UserSubscriptionId: 1, PlanEntitlementId: 1, LimitCount: limit,
	}
	require.NoError(t, model.DB.Create(counter).Error)

	if lotPoints > 0 {
		require.NoError(t, model.DB.Create(&model.ComputePointLot{
			UserId: userId, Source: model.ComputePointLotSourceSubscription, RefId: 1,
			PointsTotal: lotPoints, ExpiresAt: model.GetDBTimestamp() + 3600,
			Status: model.ComputePointLotStatusActive,
		}).Error)
	}
	return &EntitlementFunding{
		userId: userId,
		match: &model.EntitlementMatch{
			Entitlement: *ent, CounterId: counter.Id,
			UserSubscriptionId: 1, PlanId: 1, LimitCount: limit,
		},
	}
}

func counterUsed(t *testing.T, id int) int64 {
	t.Helper()
	var row model.UserSubscriptionEntitlement
	require.NoError(t, model.DB.First(&row, id).Error)
	return row.UsedCount
}

func lotUsed(t *testing.T, userId int) int64 {
	t.Helper()
	balance, err := model.GetComputePointBalance(userId)
	require.NoError(t, err)
	return balance.Used
}

func TestEntitlementPreConsume_TakesCountAndPoints(t *testing.T) {
	truncate(t)
	f := seedEntitlementFunding(t, 1001, 10, true, 1, 100000)

	require.NoError(t, f.PreConsume(500))
	require.Equal(t, int64(1), counterUsed(t, f.match.CounterId))
	require.Equal(t, int64(500), lotUsed(t, 1001))
	require.Equal(t, int64(500), f.PointsSpent())
}

// 折扣系数作用在消耗侧：×0.5 就是同样的调用只烧一半点数。
func TestEntitlementPreConsume_AppliesDiscount(t *testing.T) {
	truncate(t)
	f := seedEntitlementFunding(t, 1002, 10, true, 0.5, 100000)

	require.NoError(t, f.PreConsume(1000))
	require.Equal(t, int64(500), lotUsed(t, 1002))
}

// 不消耗算力点的权益（无限制模型）只过次数闸门。
func TestEntitlementPreConsume_NoPointsWhenDisabled(t *testing.T) {
	truncate(t)
	f := seedEntitlementFunding(t, 1003, 10, false, 1, 100000)

	require.NoError(t, f.PreConsume(500))
	require.Equal(t, int64(1), counterUsed(t, f.match.CounterId))
	require.Equal(t, int64(0), lotUsed(t, 1003), "不消耗算力点的权益不该动批次")
}

// 次数用尽 → 返回降级信号，且不得扣算力点。
func TestEntitlementPreConsume_CountExhausted(t *testing.T) {
	truncate(t)
	f := seedEntitlementFunding(t, 1004, 1, true, 1, 100000)

	require.NoError(t, f.PreConsume(500))
	f2 := &EntitlementFunding{userId: 1004, match: f.match}
	err := f2.PreConsume(500)
	require.ErrorIs(t, err, ErrEntitlementUnavailable)
	require.Equal(t, int64(500), lotUsed(t, 1004), "被拒的那次不得扣算力点")
}

// 算力点不足时要把刚占的次数还回去——否则降级走钱包的同时白烧一次配额。
func TestEntitlementPreConsume_PointsShortfallReleasesCount(t *testing.T) {
	truncate(t)
	f := seedEntitlementFunding(t, 1005, 10, true, 1, 100)

	err := f.PreConsume(500)
	require.ErrorIs(t, err, ErrEntitlementUnavailable)
	require.Equal(t, int64(0), counterUsed(t, f.match.CounterId), "算力点不足必须归还已占的次数")
	require.Equal(t, int64(0), lotUsed(t, 1005))
}

func TestEntitlementSettle_ExtraCharge(t *testing.T) {
	truncate(t)
	f := seedEntitlementFunding(t, 1006, 10, true, 1, 100000)
	require.NoError(t, f.PreConsume(500))

	require.NoError(t, f.Settle(300))
	require.Equal(t, int64(800), lotUsed(t, 1006))
	require.Equal(t, int64(800), f.PointsSpent())
}

// 补扣时算力点已不够：服务已交付不能失败，能扣多少扣多少，差额由平台承担。
func TestEntitlementSettle_ShortfallDoesNotFail(t *testing.T) {
	truncate(t)
	f := seedEntitlementFunding(t, 1007, 10, true, 1, 600)
	require.NoError(t, f.PreConsume(500))

	require.NoError(t, f.Settle(5000), "服务已交付，结算不得失败")
	require.Equal(t, int64(500), lotUsed(t, 1007), "扣不到就维持原样，不透支批次")
}

// 退还要原路退回原批次，不能笼统退总额。
func TestEntitlementSettle_RefundReturnsToLot(t *testing.T) {
	truncate(t)
	f := seedEntitlementFunding(t, 1008, 10, true, 1, 100000)
	require.NoError(t, f.PreConsume(500))

	require.NoError(t, f.Settle(-200))
	require.Equal(t, int64(300), lotUsed(t, 1008))
	require.Equal(t, int64(300), f.PointsSpent())
}

// 请求失败全额退：次数退回、算力点原路退回。
func TestEntitlementRefund_All(t *testing.T) {
	truncate(t)
	f := seedEntitlementFunding(t, 1009, 10, true, 1, 100000)
	require.NoError(t, f.PreConsume(500))
	require.Equal(t, int64(1), counterUsed(t, f.match.CounterId))

	require.NoError(t, f.Refund())
	require.Equal(t, int64(0), counterUsed(t, f.match.CounterId), "次数必须退回")
	require.Equal(t, int64(0), lotUsed(t, 1009), "算力点必须原路退回")
}

// 不消耗算力点的权益退款只退次数。
func TestEntitlementRefund_CountOnly(t *testing.T) {
	truncate(t)
	f := seedEntitlementFunding(t, 1010, 10, false, 1, 0)
	require.NoError(t, f.PreConsume(500))

	require.NoError(t, f.Refund())
	require.Equal(t, int64(0), counterUsed(t, f.match.CounterId))
}

// 跨批次消耗后部分退款：按消耗逆序退，不能把快过期批次的消耗先还掉——
// 那等于让用户先烧长期批次，快过期的白白过期。
func TestEntitlementSettle_PartialRefundReverseOrder(t *testing.T) {
	truncate(t)
	now := model.GetDBTimestamp()
	// 近期批次 300，远期批次 1000
	require.NoError(t, model.DB.Create(&model.ComputePointLot{
		UserId: 1011, Source: model.ComputePointLotSourceSubscription, RefId: 1,
		PointsTotal: 300, ExpiresAt: now + 3600, Status: model.ComputePointLotStatusActive,
	}).Error)
	require.NoError(t, model.DB.Create(&model.ComputePointLot{
		UserId: 1011, Source: model.ComputePointLotSourceSubscription, RefId: 1,
		PointsTotal: 1000, ExpiresAt: now + 30*24*3600, Status: model.ComputePointLotStatusActive,
	}).Error)
	counter := &model.UserSubscriptionEntitlement{
		UserId: 1011, UserSubscriptionId: 1, PlanEntitlementId: 1, LimitCount: 10,
	}
	require.NoError(t, model.DB.Create(counter).Error)
	f := &EntitlementFunding{
		userId: 1011,
		match: &model.EntitlementMatch{
			Entitlement: model.SubscriptionPlanEntitlement{
				Id: 1, Models: "m", ConsumePoints: true, ConsumeDiscount: 1, LimitCount: 10,
			},
			CounterId: counter.Id, LimitCount: 10,
		},
	}

	require.NoError(t, f.PreConsume(500)) // 300 近期 + 200 远期
	require.Equal(t, int64(500), lotUsed(t, 1011))

	require.NoError(t, f.Settle(-200)) // 退 200，应全部退给最后扣的远期批次
	var lots []model.ComputePointLot
	require.NoError(t, model.DB.Where("user_id = ?", 1011).Order("expires_at asc").Find(&lots).Error)
	require.Equal(t, int64(300), lots[0].PointsUsed, "近期批次的消耗不该被退掉")
	require.Equal(t, int64(0), lots[1].PointsUsed, "退款应回到最后扣的远期批次")
}

// ---------------------------------------------------------------------------
// 异步任务的权益资金调整（codex review：权益没接进 taskAdjustFunding）
// ---------------------------------------------------------------------------

func seedEntitlementTask(t *testing.T, userId int, quota int, spent []model.ComputePointSpend, counterId int) *model.Task {
	t.Helper()
	task := &model.Task{
		UserId: userId, Quota: quota, TaskID: "t-ent",
		PrivateData: model.TaskPrivateData{
			BillingSource:        BillingSourceEntitlement,
			EntitlementCounterId: counterId,
			EntitlementDiscount:  1,
			EntitlementSpent:     spent,
		},
	}
	return task
}

// 任务失败全额退款：算力点原路退回批次、次数归还，**钱包一分不动**。
// 落到钱包分支会退还一笔从未扣过的钱——凭空送真钱。
func TestTaskAdjustEntitlement_FullRefund(t *testing.T) {
	truncate(t)
	seedBillingUser(t, 1201, 100000, 0)
	f := seedEntitlementFunding(t, 1201, 10, true, 1, 100000)
	require.NoError(t, f.PreConsume(500))

	before, err := model.GetUserQuota(1201, false)
	require.NoError(t, err)

	task := seedEntitlementTask(t, 1201, 500, f.spent, f.match.CounterId)
	require.NoError(t, taskAdjustFunding(task, -500))

	require.Equal(t, int64(0), lotUsed(t, 1201), "算力点必须原路退回")
	require.Equal(t, int64(0), counterUsed(t, f.match.CounterId), "次数必须归还")
	after, err := model.GetUserQuota(1201, false)
	require.NoError(t, err)
	require.Equal(t, before, after, "钱包余额不得变动——权益任务从没扣过钱包")
}

// 重算下调（部分退款）：按比例退算力点，次数不退——这次调用确实发生了。
func TestTaskAdjustEntitlement_PartialRefundKeepsCount(t *testing.T) {
	truncate(t)
	seedBillingUser(t, 1202, 100000, 0)
	f := seedEntitlementFunding(t, 1202, 10, true, 1, 100000)
	require.NoError(t, f.PreConsume(1000))

	task := seedEntitlementTask(t, 1202, 1000, f.spent, f.match.CounterId)
	require.NoError(t, taskAdjustFunding(task, -400))

	require.Equal(t, int64(600), lotUsed(t, 1202), "按比例退 400")
	require.Equal(t, int64(1), counterUsed(t, f.match.CounterId), "部分退款不该归还次数")
}

// 重算补扣：继续扣算力点，不碰钱包。
func TestTaskAdjustEntitlement_ExtraChargeUsesPoints(t *testing.T) {
	truncate(t)
	seedBillingUser(t, 1203, 100000, 0)
	f := seedEntitlementFunding(t, 1203, 10, true, 1, 100000)
	require.NoError(t, f.PreConsume(500))

	before, err := model.GetUserQuota(1203, false)
	require.NoError(t, err)

	task := seedEntitlementTask(t, 1203, 500, f.spent, f.match.CounterId)
	require.NoError(t, taskAdjustFunding(task, 300))

	require.Equal(t, int64(800), lotUsed(t, 1203), "补扣应继续走算力点")
	after, err := model.GetUserQuota(1203, false)
	require.NoError(t, err)
	require.Equal(t, before, after, "补扣不得落到钱包")
}

// 折扣权益：拆分是打完折的实扣量，全额退款要退回实扣而不是按计费额退。
func TestTaskAdjustEntitlement_DiscountedFullRefund(t *testing.T) {
	truncate(t)
	seedBillingUser(t, 1204, 100000, 0)
	f := seedEntitlementFunding(t, 1204, 10, true, 0.5, 100000)
	require.NoError(t, f.PreConsume(1000)) // 打完折实扣 500

	require.Equal(t, int64(500), lotUsed(t, 1204))
	task := seedEntitlementTask(t, 1204, 1000, f.spent, f.match.CounterId)
	require.NoError(t, taskAdjustFunding(task, -1000))

	require.Equal(t, int64(0), lotUsed(t, 1204), "应退回实扣的 500 而不是计费额 1000")
}

// 不消耗算力点的权益任务：没有拆分可退，但次数仍要归还,且不得动钱包。
func TestTaskAdjustEntitlement_NoPointsStillRefundsCount(t *testing.T) {
	truncate(t)
	seedBillingUser(t, 1205, 100000, 0)
	f := seedEntitlementFunding(t, 1205, 10, false, 1, 0)
	require.NoError(t, f.PreConsume(500))

	before, err := model.GetUserQuota(1205, false)
	require.NoError(t, err)

	task := seedEntitlementTask(t, 1205, 500, nil, f.match.CounterId)
	require.NoError(t, taskAdjustFunding(task, -500))

	require.Equal(t, int64(0), counterUsed(t, f.match.CounterId))
	after, err := model.GetUserQuota(1205, false)
	require.NoError(t, err)
	require.Equal(t, before, after)
}

// 权益会话不得把与本请求无关的钱包透支结转成信用欠款。
//
// 权益扣的是次数与算力点，一分钱没动 User.Quota。若此时用户 quota 恰好因别的原因
// 为负（上一笔钱包请求的透支还没结转），结转会把那笔无关欠款记到本请求头上——
// 而对账侧把权益日志整个排除在外，于是 credit_used 涨了、对账看不到，信用账假不平。
// 这与订阅会话的守卫是同一条理由。
func TestEntitlementSettle_DoesNotConvertUnrelatedOverdraft(t *testing.T) {
	truncate(t)
	require.NoError(t, model.DB.Create(&model.User{
		Id: 1301, Username: "ent_credit", Role: 1, Status: 1, Group: "default",
		Quota: -5000, CreditLimit: 100000,
	}).Error)
	t.Cleanup(func() { model.DB.Unscoped().Where("id = ?", 1301).Delete(&model.User{}) })

	f := seedEntitlementFunding(t, 1301, 10, true, 1, 100000)
	relayInfo := &relaycommon.RelayInfo{UserId: 1301, IsPlayground: true}
	s := &BillingSession{relayInfo: relayInfo, funding: f}

	require.NoError(t, s.Settle(500))

	var used int64
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", 1301).
		Select("credit_used").Find(&used).Error)
	require.Equal(t, int64(0), used,
		"权益请求不得把无关的钱包透支结转成欠款")

	var quota int
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", 1301).
		Select("quota").Find(&quota).Error)
	require.Equal(t, -5000, quota, "那笔透支应原样留着，等真正的钱包请求去结转")
}

// 异步补扣时不得把无关的钱包透支结转到权益任务头上。
//
// 与同步路径 syncCreditConsumed 的守卫是镜像关系：上一轮只改了同步侧、漏了
// recalculateTaskQuota 这条异步入口。权益任务同样不动 User.Quota。
func TestTaskAdjustEntitlement_DoesNotSettleUnrelatedOverdraft(t *testing.T) {
	truncate(t)
	require.NoError(t, model.DB.Create(&model.User{
		Id: 1401, Username: "ent_task_credit", Role: 1, Status: 1, Group: "default",
		Quota: -5000, CreditLimit: 100000,
	}).Error)
	t.Cleanup(func() { model.DB.Unscoped().Where("id = ?", 1401).Delete(&model.User{}) })

	f := seedEntitlementFunding(t, 1401, 10, true, 1, 100000)
	require.NoError(t, f.PreConsume(500))
	task := seedEntitlementTask(t, 1401, 500, f.spent, f.match.CounterId)

	require.False(t, taskTouchesWallet(task), "权益任务不该被当成扣钱包的来源")

	var used int64
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", 1401).
		Select("credit_used").Find(&used).Error)
	require.Equal(t, int64(0), used)
}

// 补扣量超过已结算额时不得被封顶——封顶等于少扣算力点。
//
// 退款封顶是对的（退不能超过实扣），补扣封顶是错的，两者共用一个缩放函数时
// 极容易混为一谈。这里钉住的正是这条边界：delta >= billed。
func TestTaskAdjustEntitlement_ExtraChargeNotCappedAtSpent(t *testing.T) {
	truncate(t)
	seedBillingUser(t, 1402, 100000, 0)
	f := seedEntitlementFunding(t, 1402, 10, true, 1, 100000)
	require.NoError(t, f.PreConsume(500))
	require.Equal(t, int64(500), lotUsed(t, 1402))

	// 实际用量是已结算额的三倍：补扣 1000（delta = 2 × billed）
	task := seedEntitlementTask(t, 1402, 500, f.spent, f.match.CounterId)
	require.NoError(t, taskAdjustFunding(task, 1000))

	require.Equal(t, int64(1500), lotUsed(t, 1402),
		"补扣 1000 应真的扣 1000，而不是被封顶成已扣的 500")
}

func TestEntitlementPointsFor(t *testing.T) {
	require.Equal(t, int64(5), entitlementPointsFor(10, 0.5), "打折")
	require.Equal(t, int64(10), entitlementPointsFor(10, 1), "原价")
	require.Equal(t, int64(1), entitlementPointsFor(1, 0.5), "不足 1 点按 1 点，不抹成 0")
	require.Equal(t, int64(10), entitlementPointsFor(10, 0), "折扣非法时按原价")
	require.Equal(t, int64(0), entitlementPointsFor(0, 0.5))
	require.Equal(t, int64(0), entitlementPointsFor(-5, 0.5))
}

// 小额 + 折扣是「按比例反推折扣」那版实现的致命场景：
// totalSpent = ceil(billed × discount)，billed 小时这一次 ceil 会把隐含比值整个抬高
// （billed=1、discount=0.5 → 反推出 1.0），再乘 delta 就是成倍多扣。
// 之前的用例全用大额,所以那个错误论证一路活了下来。
func TestTaskAdjustEntitlement_SmallBilledDiscountNotAmplified(t *testing.T) {
	truncate(t)
	seedBillingUser(t, 1601, 100000, 0)
	f := seedEntitlementFunding(t, 1601, 10, true, 0.5, 100000)
	require.NoError(t, f.PreConsume(1)) // ceil(1 × 0.5) = 1 点
	require.Equal(t, int64(1), lotUsed(t, 1601))

	task := seedEntitlementTask(t, 1601, 1, f.spent, f.match.CounterId)
	task.PrivateData.EntitlementDiscount = 0.5
	require.NoError(t, taskAdjustFunding(task, 10))

	// 正确：补扣 ceil(10 × 0.5) = 5，总计 6
	// 按比例反推会扣 10，总计 11
	require.Equal(t, int64(6), lotUsed(t, 1601),
		"补扣应按真实折扣算，不得被 ceil 抬高的隐含比值放大")
}

// ---------------------------------------------------------------------------
// 追加预扣（Reserve）
// ---------------------------------------------------------------------------
//
// 这条路当前没有调用方（Reserve 在 BillingSettler 接口里声明、BillingSession 实现，
// 但全仓库无人调用），补分支是为了不留雷：泛化前 reserveFunding 的 default 分支
// 直接返回「unsupported funding source」错误，哪天有人接上流式超额追扣，
// 权益请求就会集体失败。

func TestEntitlementReserveExtra_TakesMorePoints(t *testing.T) {
	truncate(t)
	f := seedEntitlementFunding(t, 1501, 10, true, 1, 100000)
	require.NoError(t, f.PreConsume(500))

	spent, ok, err := f.reserveExtra(300)
	require.NoError(t, err)
	require.True(t, ok)
	require.NotEmpty(t, spent)
	require.Equal(t, int64(800), lotUsed(t, 1501))
}

// 服务未交付时扣不到必须如实返回 false，不能像结算那样由平台兜底——
// 那会让用户白嫖一段超出套餐的用量。
func TestEntitlementReserveExtra_InsufficientReportsFalse(t *testing.T) {
	truncate(t)
	f := seedEntitlementFunding(t, 1502, 10, true, 1, 600)
	require.NoError(t, f.PreConsume(500))

	_, ok, err := f.reserveExtra(5000)
	require.NoError(t, err)
	require.False(t, ok, "扣不到必须让调用方知道")
	require.Equal(t, int64(500), lotUsed(t, 1502), "扣不动时不得留下半笔")
}

// 回滚要按快照精确逆转：追加那笔可能落在与原始预扣不同的批次上。
func TestEntitlementUnreserveExtra_ReversesExactLots(t *testing.T) {
	truncate(t)
	now := model.GetDBTimestamp()
	// 近期批次 500（会被原始预扣扣光），远期批次 1000
	require.NoError(t, model.DB.Create(&model.ComputePointLot{
		UserId: 1503, Source: model.ComputePointLotSourceSubscription, RefId: 1,
		PointsTotal: 500, ExpiresAt: now + 3600, Status: model.ComputePointLotStatusActive,
	}).Error)
	require.NoError(t, model.DB.Create(&model.ComputePointLot{
		UserId: 1503, Source: model.ComputePointLotSourceSubscription, RefId: 1,
		PointsTotal: 1000, ExpiresAt: now + 30*24*3600, Status: model.ComputePointLotStatusActive,
	}).Error)
	counter := &model.UserSubscriptionEntitlement{
		UserId: 1503, UserSubscriptionId: 1, PlanEntitlementId: 1, LimitCount: 10,
	}
	require.NoError(t, model.DB.Create(counter).Error)
	f := &EntitlementFunding{
		userId: 1503,
		match: &model.EntitlementMatch{
			Entitlement: model.SubscriptionPlanEntitlement{
				Id: 1, Models: "m", ConsumePoints: true, ConsumeDiscount: 1, LimitCount: 10,
			},
			CounterId: counter.Id, LimitCount: 10,
		},
	}

	require.NoError(t, f.PreConsume(500)) // 扣光近期批次
	spent, ok, err := f.reserveExtra(200) // 只能走远期批次
	require.NoError(t, err)
	require.True(t, ok)

	f.unreserveExtra(spent)

	var lots []model.ComputePointLot
	require.NoError(t, model.DB.Where("user_id = ?", 1503).Order("expires_at asc").Find(&lots).Error)
	require.Equal(t, int64(500), lots[0].PointsUsed, "原始预扣不受影响")
	require.Equal(t, int64(0), lots[1].PointsUsed, "追加那笔原路退回远期批次")
	require.Equal(t, int64(500), f.PointsSpent(), "累计拆分要同步扣掉")
}

// 不消耗算力点的权益追加预扣是 no-op，不该报错。
func TestEntitlementReserveExtra_NoPointsIsNoOp(t *testing.T) {
	truncate(t)
	f := seedEntitlementFunding(t, 1504, 10, false, 1, 0)
	require.NoError(t, f.PreConsume(500))

	spent, ok, err := f.reserveExtra(300)
	require.NoError(t, err)
	require.True(t, ok)
	require.Empty(t, spent)
}
