package model

import (
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func seedComputePointLot(t *testing.T, userId int, points, used, expiresAt int64) *ComputePointLot {
	t.Helper()
	lot := &ComputePointLot{
		UserId:      userId,
		Source:      ComputePointLotSourceSubscription,
		RefId:       1,
		PointsTotal: points,
		PointsUsed:  used,
		GrantedAt:   GetDBTimestamp(),
		ExpiresAt:   expiresAt,
		Status:      ComputePointLotStatusActive,
	}
	require.NoError(t, DB.Create(lot).Error)
	return lot
}

// ---------------------------------------------------------------------------
// GrantComputePointLotTx
// ---------------------------------------------------------------------------

func TestGrantComputePointLotTx(t *testing.T) {
	truncateTables(t)
	// expiresAt 必须在事务外先算好：GetDBTimestamp() 查的是包级 DB（连接池），
	// 若把它写成闭包里的参数表达式，就是在事务已经开着、占住测试环境唯一连接
	// （MaxOpenConns(1)）的时候再发一次查询——永久阻塞。此坑与
	// GrantComputePointLotTx 内部曾经犯的是同一个，这次是测试代码自己踩上。
	expiresAt := GetDBTimestamp() + 3600
	err := DB.Transaction(func(tx *gorm.DB) error {
		return GrantComputePointLotTx(tx, 701, ComputePointLotSourceSubscription, 9, 50000, expiresAt)
	})
	require.NoError(t, err)

	var lot ComputePointLot
	require.NoError(t, DB.Where("user_id = ?", 701).First(&lot).Error)
	require.Equal(t, int64(50000), lot.PointsTotal)
	require.Equal(t, int64(0), lot.PointsUsed)
	require.Equal(t, ComputePointLotStatusActive, lot.Status)
	require.Equal(t, 9, lot.RefId)
}

// points<=0 是 no-op：套餐没配算力点时不该产生一条空批次占用行数、污染报表。
func TestGrantComputePointLotTx_NonPositiveIsNoOp(t *testing.T) {
	truncateTables(t)
	expiresAt := GetDBTimestamp() + 3600 // 事务外先算好，理由同上一个测试
	err := DB.Transaction(func(tx *gorm.DB) error {
		return GrantComputePointLotTx(tx, 702, ComputePointLotSourceSubscription, 1, 0, expiresAt)
	})
	require.NoError(t, err)

	var n int64
	require.NoError(t, DB.Model(&ComputePointLot{}).Where("user_id = ?", 702).Count(&n).Error)
	require.Equal(t, int64(0), n)
}

func TestGrantComputePointLotTx_NilTxRejected(t *testing.T) {
	err := GrantComputePointLotTx(nil, 703, ComputePointLotSourceSubscription, 1, 1000, 0)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// TryConsumeComputePoints
// ---------------------------------------------------------------------------

func TestTryConsumeComputePoints_SingleLotSufficient(t *testing.T) {
	truncateTables(t)
	lot := seedComputePointLot(t, 711, 1000, 0, GetDBTimestamp()+3600)

	ok, spent, err := TryConsumeComputePoints(711, 300)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, []ComputePointSpend{{LotId: lot.Id, Amount: 300}}, spent)

	var updated ComputePointLot
	require.NoError(t, DB.First(&updated, lot.Id).Error)
	require.Equal(t, int64(300), updated.PointsUsed)
}

// 全额扣不到时必须整体失败、不留下任何部分扣减——不能出现「扣了一半」的账。
func TestTryConsumeComputePoints_InsufficientFailsAtomically(t *testing.T) {
	truncateTables(t)
	lot := seedComputePointLot(t, 712, 500, 0, GetDBTimestamp()+3600)

	ok, spent, err := TryConsumeComputePoints(712, 800)
	require.NoError(t, err)
	require.False(t, ok)
	require.Nil(t, spent)

	var updated ComputePointLot
	require.NoError(t, DB.First(&updated, lot.Id).Error)
	require.Equal(t, int64(0), updated.PointsUsed, "失败时不得留下任何部分扣减")
}

// 跨批次消费：一个批次余量不够时接续扣下一个，按到期时间由近到远——
// 这是「先烧快过期的」在扣费侧的唯一执行点。
func TestTryConsumeComputePoints_SpansMultipleLotsNearestExpiryFirst(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()
	// 三个批次，创建顺序（id 序）刻意既不等于到期顺序、也不等于其反序：只有两个
	// 批次时，正确答案必然等于 id 升序或降序之一，实现退化成裸的 id 排序会有一半
	// 概率蒙混过关（这个坑真实踩到过，回退验证 Order("id DESC") 时两个批次版本的
	// 这个测试没有失败）。创建顺序 mid→near→far，到期顺序 near→mid→far，
	// 两者互不为正序或反序关系。
	mid := seedComputePointLot(t, 713, 1000, 0, now+3*24*3600)  // 创建顺序第 1，到期顺序第 2
	near := seedComputePointLot(t, 713, 1000, 0, now+3600)      // 创建顺序第 2，到期顺序第 1
	far := seedComputePointLot(t, 713, 1000, 0, now+30*24*3600) // 创建顺序第 3，到期顺序第 3

	ok, spent, err := TryConsumeComputePoints(713, 2500)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, []ComputePointSpend{
		{LotId: near.Id, Amount: 1000}, // 最快到期的先耗尽
		{LotId: mid.Id, Amount: 1000},  // 次快到期的接续耗尽
		{LotId: far.Id, Amount: 500},   // 最后从最长期的批次扣剩余
	}, spent)

	var updatedNear, updatedMid, updatedFar ComputePointLot
	require.NoError(t, DB.First(&updatedNear, near.Id).Error)
	require.NoError(t, DB.First(&updatedMid, mid.Id).Error)
	require.NoError(t, DB.First(&updatedFar, far.Id).Error)
	require.Equal(t, int64(1000), updatedNear.PointsUsed, "最快到期批次应被耗尽")
	require.Equal(t, int64(1000), updatedMid.PointsUsed, "次快到期批次应被耗尽")
	require.Equal(t, int64(500), updatedFar.PointsUsed, "最长期批次只应扣剩余部分")
}

// 永不过期的批次（expires_at=0）排在最后：它没有「更快过期」这回事，
// 优先烧有明确期限的批次才是「先烧快过期的」的正确含义。
//
// 三个批次且创建顺序（id 序）刻意既不等于正确顺序、也不等于其反序：只有两个
// 批次时，正确答案必然等于 id 升序或降序之一，若实现退化成裸的 id 排序，
// 有一半概率被巧合蒙混过去测不出来（另一个测试就踩过这个坑）。这里插入第三个
// 批次并打乱创建顺序，让「按 id 排」在两个方向上都得不出期望结果。
func TestTryConsumeComputePoints_NeverExpiringLotConsumedLast(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()
	near := seedComputePointLot(t, 714, 500, 0, now+3600)      // 创建顺序第 1，到期顺序第 1
	unlimited := seedComputePointLot(t, 714, 1000, 0, 0)       // 创建顺序第 2，到期顺序第 3（不过期恒排最后）
	far := seedComputePointLot(t, 714, 500, 0, now+30*24*3600) // 创建顺序第 3，到期顺序第 2

	ok, spent, err := TryConsumeComputePoints(714, 1200)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, []ComputePointSpend{
		{LotId: near.Id, Amount: 500},
		{LotId: far.Id, Amount: 500},
		{LotId: unlimited.Id, Amount: 200},
	}, spent)
}

// 已过期的批次不得参与扣费——这是第一原则的核心：查询时间条件，不依赖后台任务。
func TestTryConsumeComputePoints_ExpiredLotExcluded(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()
	seedComputePointLot(t, 715, 1000, 0, now-10) // 已过期，Status 仍是 active（任务没跑）

	ok, spent, err := TryConsumeComputePoints(715, 100)
	require.NoError(t, err)
	require.False(t, ok, "即使 Status 字段还是 active，过期批次也不可被扣费")
	require.Nil(t, spent)
}

// 已用尽的批次不得重复参与扣费。
func TestTryConsumeComputePoints_ExhaustedLotExcluded(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()
	seedComputePointLot(t, 716, 500, 500, now+3600)

	ok, spent, err := TryConsumeComputePoints(716, 100)
	require.NoError(t, err)
	require.False(t, ok)
	require.Nil(t, spent)
}

func TestTryConsumeComputePoints_ZeroAmountIsNoOp(t *testing.T) {
	truncateTables(t)
	seedComputePointLot(t, 717, 500, 0, GetDBTimestamp()+3600)

	ok, spent, err := TryConsumeComputePoints(717, 0)
	require.NoError(t, err)
	require.True(t, ok)
	require.Nil(t, spent)
}

// ---------------------------------------------------------------------------
// RefundComputePoints
// ---------------------------------------------------------------------------

// 退款必须逐笔原路退回：这是整个批次化设计存在的理由之一——不能把一笔跨批次
// 消费的退款笼统退给某一个批次，否则快过期的批次被提前烧光、退款却进了长期批次。
func TestRefundComputePoints_ReturnsToOriginalLots(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()
	near := seedComputePointLot(t, 721, 1000, 900, now+3600)
	far := seedComputePointLot(t, 721, 1000, 0, now+30*24*3600)

	ok, spent, err := TryConsumeComputePoints(721, 300)
	require.NoError(t, err)
	require.True(t, ok)

	require.NoError(t, RefundComputePoints(spent))

	var updatedNear, updatedFar ComputePointLot
	require.NoError(t, DB.First(&updatedNear, near.Id).Error)
	require.NoError(t, DB.First(&updatedFar, far.Id).Error)
	require.Equal(t, int64(900), updatedNear.PointsUsed, "退款应精确原路回到扣款时的批次")
	require.Equal(t, int64(0), updatedFar.PointsUsed)
}

// 退款金额超过批次已用量时静默跳过，不产生负的 PointsUsed——
// 与信用账户的 ReduceUserCreditUsed 同一防御性模式。
func TestRefundComputePoints_SkipsWhenInsufficientUsed(t *testing.T) {
	truncateTables(t)
	lot := seedComputePointLot(t, 722, 1000, 100, GetDBTimestamp()+3600)

	require.NoError(t, RefundComputePoints([]ComputePointSpend{{LotId: lot.Id, Amount: 500}}))

	var updated ComputePointLot
	require.NoError(t, DB.First(&updated, lot.Id).Error)
	require.Equal(t, int64(100), updated.PointsUsed, "不得把 PointsUsed 减成负数")
}

func TestRefundComputePoints_EmptyIsNoOp(t *testing.T) {
	require.NoError(t, RefundComputePoints(nil))
	require.NoError(t, RefundComputePoints([]ComputePointSpend{}))
}

// 消费 → 退款的完整闭环：退完后余额应与消费前一致。
func TestTryConsumeThenRefund_RestoresBalance(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()
	seedComputePointLot(t, 723, 1000, 0, now+3600)
	seedComputePointLot(t, 723, 2000, 0, now+7200)

	before, err := GetComputePointBalance(723)
	require.NoError(t, err)

	ok, spent, err := TryConsumeComputePoints(723, 1500) // 跨两个批次
	require.NoError(t, err)
	require.True(t, ok)

	require.NoError(t, RefundComputePoints(spent))

	after, err := GetComputePointBalance(723)
	require.NoError(t, err)
	require.Equal(t, before, after, "全额退款后余额应与消费前一致")
}

// ---------------------------------------------------------------------------
// GetComputePointBalance
// ---------------------------------------------------------------------------

func TestGetComputePointBalance_SumsOnlyUnexpiredLots(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()
	seedComputePointLot(t, 731, 1000, 300, now+3600) // 有效
	seedComputePointLot(t, 731, 5000, 100, now-10)   // 已过期，不计入
	seedComputePointLot(t, 731, 2000, 500, 0)        // 永不过期，计入

	balance, err := GetComputePointBalance(731)
	require.NoError(t, err)
	require.Equal(t, int64(3000), balance.Total, "过期批次不计入总额")
	require.Equal(t, int64(800), balance.Used)
	require.Equal(t, int64(2200), balance.Available)
}

func TestGetComputePointBalance_NoLotsReturnsZero(t *testing.T) {
	truncateTables(t)
	balance, err := GetComputePointBalance(732)
	require.NoError(t, err)
	require.Equal(t, int64(0), balance.Total)
	require.Equal(t, int64(0), balance.Available)
}

// ---------------------------------------------------------------------------
// ExpireDueComputePointLots
// ---------------------------------------------------------------------------

func TestExpireDueComputePointLots_MarksExpired(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()
	due := seedComputePointLot(t, 741, 1000, 0, now-10)
	notDue := seedComputePointLot(t, 741, 1000, 0, now+3600)

	n, err := ExpireDueComputePointLots(500)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	var updatedDue, updatedNotDue ComputePointLot
	require.NoError(t, DB.First(&updatedDue, due.Id).Error)
	require.NoError(t, DB.First(&updatedNotDue, notDue.Id).Error)
	require.Equal(t, ComputePointLotStatusExpired, updatedDue.Status)
	require.Equal(t, ComputePointLotStatusActive, updatedNotDue.Status, "未到期批次不受影响")
}

func TestExpireDueComputePointLots_MarksExhausted(t *testing.T) {
	truncateTables(t)
	lot := seedComputePointLot(t, 742, 500, 500, GetDBTimestamp()+3600)

	n, err := ExpireDueComputePointLots(500)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	var updated ComputePointLot
	require.NoError(t, DB.First(&updated, lot.Id).Error)
	require.Equal(t, ComputePointLotStatusExhausted, updated.Status)
}

// limit 必须真正生效——这条测试是为了不让 §「.Limit(n).Update(...) 在这个 GORM
// 版本里对 UPDATE/DELETE 是静默 no-op」的坑在这个函数里重演。若实现退回那种写法，
// 这条测试测不出行为差异（因为两条都会被处理），所以额外用行为断言钉住「只处理
// 了 limit 条」这件事本身。
func TestExpireDueComputePointLots_RespectsLimit(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()
	seedComputePointLot(t, 743, 1000, 0, now-10)
	seedComputePointLot(t, 743, 1000, 0, now-10)
	seedComputePointLot(t, 743, 1000, 0, now-10)

	n, err := ExpireDueComputePointLots(2)
	require.NoError(t, err)
	require.Equal(t, 2, n, "一次只应处理 limit 条")

	var expiredCount int64
	require.NoError(t, DB.Model(&ComputePointLot{}).
		Where("status = ?", ComputePointLotStatusExpired).Count(&expiredCount).Error)
	require.Equal(t, int64(2), expiredCount)
}

// 已经是 expired/exhausted 的批次不重复处理，也不计入本次返回的 n。
func TestExpireDueComputePointLots_SkipsAlreadySynced(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()
	lot := seedComputePointLot(t, 744, 1000, 0, now-10)
	require.NoError(t, DB.Model(&ComputePointLot{}).Where("id = ?", lot.Id).
		Update("status", ComputePointLotStatusExpired).Error)

	n, err := ExpireDueComputePointLots(500)
	require.NoError(t, err)
	require.Equal(t, 0, n)
}

// ---------------------------------------------------------------------------
// 与订阅生命周期的集成：首次发放 + 周期重置
// ---------------------------------------------------------------------------

func seedComputePointPlan(t *testing.T, points int64, resetPeriod string) *SubscriptionPlan {
	t.Helper()
	plan := &SubscriptionPlan{
		Title:                  "算力点测试套餐",
		PriceAmount:            299,
		Currency:               "CNY",
		DurationUnit:           SubscriptionDurationMonth,
		DurationValue:          1,
		Enabled:                true,
		TotalAmount:            0,
		QuotaResetPeriod:       resetPeriod,
		ComputePointsPerPeriod: points,
	}
	// BeforeCreate 由 GORM 在 Create 时自动调用，这里不必手动触发。
	require.NoError(t, DB.Create(plan).Error)
	return plan
}

// 调用 CreateUserSubscriptionFromPlanTx 时刻意传裸 DB 而非手动包一层
// DB.Transaction(func(tx){...})：这个函数自己开头有 nowUnix := GetDBTimestamp()，
// 而 GetDBTimestamp() 永远查包级 DB（连接池）。若外层已经手动开了一个事务
// （持有测试环境 MaxOpenConns(1) 的唯一连接），函数内部这次查询会永久阻塞——
// 等一个不可能被释放的连接。这是发放算力点批次之外、这个函数本身既有的问题，
// 与本次改动无关，此处只是绕开它、不在测试里踩中，不代表已经修复
// （生产环境连接池通常 > 1，只在池被打满的极端情况下才会真正触发）。
//
// 传裸 DB 不影响这里要验证的行为：CreateUserSubscriptionFromPlanTx 内部只用
// 传入的这个 db 句柄做增删改，不再自行开子事务，语义与包一层事务等价，
// 只是不再验证「创建订阅与发放批次是否原子」——那由生产路径
// CompleteSubscriptionOrder 的真实外层事务保证，不是这个函数自己的职责。

// 购买套餐时应同时发放首期算力点批次，到期时间取下一次重置边界。
func TestCreateUserSubscriptionFromPlanTx_GrantsInitialLot(t *testing.T) {
	truncateTables(t)
	plan := seedComputePointPlan(t, 50000, SubscriptionResetMonthly)

	sub, err := CreateUserSubscriptionFromPlanTx(DB, 751, plan, "order")
	require.NoError(t, err)

	balance, err := GetComputePointBalance(751)
	require.NoError(t, err)
	require.Equal(t, int64(50000), balance.Total)

	var lot ComputePointLot
	require.NoError(t, DB.Where("user_id = ?", 751).First(&lot).Error)
	require.Equal(t, sub.NextResetTime, lot.ExpiresAt,
		"到期时间应取下一次重置边界，不该跨周期累积")
}

// ComputePointsPerPeriod=0 的套餐（现有全部存量套餐）不产生任何批次——
// 零配置的向后兼容，不因为这个功能上线就影响老套餐。
func TestCreateUserSubscriptionFromPlanTx_ZeroPointsGrantsNothing(t *testing.T) {
	truncateTables(t)
	plan := seedComputePointPlan(t, 0, SubscriptionResetNever)

	_, err := CreateUserSubscriptionFromPlanTx(DB, 752, plan, "order") // 见上方裸 DB 说明
	require.NoError(t, err)

	var n int64
	require.NoError(t, DB.Model(&ComputePointLot{}).Where("user_id = ?", 752).Count(&n).Error)
	require.Equal(t, int64(0), n)
}

// 不设重置周期时，首期批次的到期时间应退回订阅结束时间——一次性发放同样
// 必须有明确到期，不能让算力点跨越已失效的订阅继续可用。
func TestCreateUserSubscriptionFromPlanTx_NoResetPeriodExpiresWithSubscription(t *testing.T) {
	truncateTables(t)
	plan := seedComputePointPlan(t, 30000, SubscriptionResetNever)

	sub, err := CreateUserSubscriptionFromPlanTx(DB, 753, plan, "order") // 见上方裸 DB 说明
	require.NoError(t, err)
	require.Equal(t, int64(0), sub.NextResetTime, "永不重置的套餐不应有下次重置时间")

	var lot ComputePointLot
	require.NoError(t, DB.Where("user_id = ?", 753).First(&lot).Error)
	require.Equal(t, sub.EndTime, lot.ExpiresAt,
		"没有重置周期时应退回订阅结束时间兜底，不能发出永不过期的算力点")
}

// 周期性重置：与 AmountUsed 归零同一个「重置事件」触发一次算力点补发，
// 不随跳过的周期数重复发放——躺过多个周期只补发当期一份。
func TestMaybeResetUserSubscriptionWithPlanTx_GrantsNewLotOnReset(t *testing.T) {
	truncateTables(t)
	plan := seedComputePointPlan(t, 50000, SubscriptionResetMonthly)

	sub, err := CreateUserSubscriptionFromPlanTx(DB, 754, plan, "order") // 见上方裸 DB 说明
	require.NoError(t, err)

	// 首期批次用掉一部分
	ok, _, err := TryConsumeComputePoints(754, 20000)
	require.NoError(t, err)
	require.True(t, ok)

	// 模拟时间推进到下一次重置边界之后
	future := sub.NextResetTime + 10
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		var locked UserSubscription
		if err := tx.Where("id = ?", sub.Id).First(&locked).Error; err != nil {
			return err
		}
		return maybeResetUserSubscriptionWithPlanTx(tx, &locked, plan, future)
	}))

	balance, err := GetComputePointBalance(754)
	require.NoError(t, err)
	// 首期批次剩余 30000（未过期，因为它的 ExpiresAt 恰好等于本次重置的触发时刻，
	// 严格大于判定不成立，视具体边界处理可能仍计入或不计入——这里只断言新批次已发放）
	require.GreaterOrEqual(t, balance.Total, int64(50000),
		"重置后应有一条新批次的 50000 点")

	var lots []ComputePointLot
	require.NoError(t, DB.Where("user_id = ? AND source = ?", 754, ComputePointLotSourceSubscription).
		Order("id asc").Find(&lots).Error)
	require.Len(t, lots, 2, "重置应新增恰好一条批次，不随跳过的周期数重复发放")
}

// PreConsumeUserSubscription 与 ResetDueSubscriptions 用来保护本函数调用的
// gorm:query_option FOR UPDATE 在 GORM v2 下是死代码（不消费该设置，等于没锁，
// 见 compute_point_lot.go 里 TryConsumeComputePoints 的同名说明），后台重置任务
// 与一次实时预扣完全可能各自拿到同一份「重置前」快照并发闯入本函数。
// AmountUsed 归零重复执行是幂等的无所谓，但算力点发放不是——这里用两份独立的
// 陈旧快照模拟这个竞态，验证幂等检查确实只让其中一次真正发出批次。
func TestMaybeResetUserSubscriptionWithPlanTx_DuplicateResetDoesNotDoubleGrant(t *testing.T) {
	truncateTables(t)
	plan := seedComputePointPlan(t, 50000, SubscriptionResetMonthly)

	sub, err := CreateUserSubscriptionFromPlanTx(DB, 755, plan, "order") // 见上方裸 DB 说明
	require.NoError(t, err)

	future := sub.NextResetTime + 10
	var staleA, staleB UserSubscription
	require.NoError(t, DB.Where("id = ?", sub.Id).First(&staleA).Error)
	require.NoError(t, DB.Where("id = ?", sub.Id).First(&staleB).Error)

	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return maybeResetUserSubscriptionWithPlanTx(tx, &staleA, plan, future)
	}))
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return maybeResetUserSubscriptionWithPlanTx(tx, &staleB, plan, future)
	}))

	var lots []ComputePointLot
	require.NoError(t, DB.Where("user_id = ? AND source = ?", 755, ComputePointLotSourceSubscription).
		Order("id asc").Find(&lots).Error)
	require.Len(t, lots, 2, "初始发放 1 条 + 重置发放 1 条，两次并发触发的同一次重置不应多发")
}

// 重置由 tx.Save 全字段覆盖改成 map 形式的条件更新后，落库结果必须与原来等价：
// AmountUsed 归零、两个重置时间推进、updated_at 刷新（BeforeUpdate 钩子改的是
// 结构体字段，map 更新带不上，只能显式写进 map）。列名写错会静默不生效或直接
// 报错，这里把落库结果钉死，不让它悄悄退化。
func TestMaybeResetUserSubscriptionWithPlanTx_PersistsResetFields(t *testing.T) {
	truncateTables(t)
	plan := seedComputePointPlan(t, 50000, SubscriptionResetMonthly)

	sub, err := CreateUserSubscriptionFromPlanTx(DB, 756, plan, "order") // 见上方裸 DB 说明
	require.NoError(t, err)

	// 制造「已用量非零 + updated_at 是旧值」的初始状态
	require.NoError(t, DB.Model(&UserSubscription{}).Where("id = ?", sub.Id).
		Updates(map[string]interface{}{"amount_used": 4321, "updated_at": 1}).Error)

	future := sub.NextResetTime + 10
	var locked UserSubscription
	require.NoError(t, DB.Where("id = ?", sub.Id).First(&locked).Error)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return maybeResetUserSubscriptionWithPlanTx(tx, &locked, plan, future)
	}))

	var got UserSubscription
	require.NoError(t, DB.First(&got, sub.Id).Error)
	require.Equal(t, int64(0), got.AmountUsed, "重置必须把已用量归零")
	require.Equal(t, sub.NextResetTime, got.LastResetTime, "上次重置时间应推进到刚跨过的那个边界")
	// next_reset_time 必须离开刚消费掉的那个边界值——CAS 抢占正是靠它区分
	// 「这次重置还没人做」和「已经有人做完了」。本套餐是 1 个月周期，跨过首个
	// 边界后下个边界已超出订阅结束时间，calcNextResetTime 返回 0 表示不再重置。
	require.NotEqual(t, sub.NextResetTime, got.NextResetTime, "下次重置时间必须离开旧边界")
	require.Greater(t, got.UpdatedAt, int64(1), "updated_at 不能停在旧值")
}

// ---------------------------------------------------------------------------
// 并发与展示状态一致性
// ---------------------------------------------------------------------------

// 批次被后台任务标记 exhausted 后，若退款腾出余量，展示状态必须跟着回到
// active——否则报表会显示「已耗尽」但实际余量>0，与真实可用性脱节
// （扣费判定本身不看 Status，不受这个问题影响）。
func TestRefundComputePoints_RevivesExhaustedLotToActive(t *testing.T) {
	truncateTables(t)
	lot := seedComputePointLot(t, 761, 1000, 1000, GetDBTimestamp()+3600)
	require.NoError(t, DB.Model(&ComputePointLot{}).Where("id = ?", lot.Id).
		Update("status", ComputePointLotStatusExhausted).Error)

	require.NoError(t, RefundComputePoints([]ComputePointSpend{{LotId: lot.Id, Amount: 300}}))

	var updated ComputePointLot
	require.NoError(t, DB.First(&updated, lot.Id).Error)
	require.Equal(t, int64(700), updated.PointsUsed)
	require.Equal(t, ComputePointLotStatusActive, updated.Status,
		"退款腾出余量后不能停留在耗尽状态")
}

// 已过期的批次即使退款腾出余量也不可用（过期判定看 ExpiresAt，与余量无关），
// 不能被退款误标为 active、造成「看起来能用」的假象。
func TestRefundComputePoints_DoesNotReviveExpiredLot(t *testing.T) {
	truncateTables(t)
	lot := seedComputePointLot(t, 762, 1000, 1000, GetDBTimestamp()-10)
	require.NoError(t, DB.Model(&ComputePointLot{}).Where("id = ?", lot.Id).
		Update("status", ComputePointLotStatusExpired).Error)

	require.NoError(t, RefundComputePoints([]ComputePointSpend{{LotId: lot.Id, Amount: 300}}))

	var updated ComputePointLot
	require.NoError(t, DB.First(&updated, lot.Id).Error)
	require.Equal(t, ComputePointLotStatusExpired, updated.Status,
		"已过期的批次不能被退款救回 active")
}

// 模拟 TryConsumeComputePoints 内部「读取批次」与「条件更新」之间，points_used
// 被别的写入抢先改动——用 GORM 的 Before(gorm:update) 钩子在条件更新执行前、
// 挂在同一事务内插一笔旁路写入。这笔旁路写入是否随本次失败的尝试一起回滚
// 不影响这里要验证的行为：条件更新一旦落空（RowsAffected=0），旧实现会直接
// continue 跳过这个批次、把它的容量凭空丢掉，导致明明还有余量的请求被误判
// 成"整体余额不足"；修复后必须整体重试、用最新数据重新计算，而不是就地放弃。
//
// 没有用真正独立的第二条连接去做这笔旁路写入：SQLite 单写者，事务持有连接期间
// 第二条连接的写入会直接拿到 SQLITE_BUSY，这是引擎本身的序列化保证（也是这里
// 選 SQLite 而不必对这条路径做跨库并发测试的原因）——生产上真正的并发窗口
// 出现在 MySQL/PostgreSQL，这里只验证"条件更新落空后代码怎么处理"这一段逻辑，
// 与旁路写入具体怎么产生的无关。
func TestTryConsumeComputePoints_RetriesOnConcurrentConflict(t *testing.T) {
	truncateTables(t)
	lot := seedComputePointLot(t, 763, 100, 0, GetDBTimestamp()+3600)

	var fired int32
	const hookName = "test:simulate-concurrent-write"
	require.NoError(t, DB.Callback().Update().Before("gorm:update").Register(hookName, func(tx *gorm.DB) {
		if tx.Statement.Table != "compute_point_lots" {
			return
		}
		if !atomic.CompareAndSwapInt32(&fired, 0, 1) {
			return
		}
		if uerr := tx.Exec("UPDATE compute_point_lots SET points_used = ? WHERE id = ?",
			int64(20), lot.Id).Error; uerr != nil {
			t.Errorf("failed to simulate concurrent write: %v", uerr)
		}
	}))
	t.Cleanup(func() { _ = DB.Callback().Update().Remove(hookName) })

	ok, spent, err := TryConsumeComputePoints(763, 30)
	require.NoError(t, err)
	require.True(t, ok, "命中一次条件更新冲突后应整体重试成功，而不是误判余额不足或丢弃部分容量")
	require.Len(t, spent, 1)
	require.Equal(t, int64(30), spent[0].Amount)

	var got ComputePointLot
	require.NoError(t, DB.First(&got, lot.Id).Error)
	require.Equal(t, int64(30), got.PointsUsed, "旁路写入随失败的第一次尝试一起回滚，重试后是干净的 0->30")
	require.Equal(t, int32(1), atomic.LoadInt32(&fired))
}
