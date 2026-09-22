package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

// 营收口径：只有 CashFen > 0 的流水计入营收，即 prepay 与 ar_settle。
// 赠送与授信开额恒为 0——前者是市场成本，后者只是给了额度、钱还没到。
// 这条规则把「授信额度」和「真实转账」从根上分开，报表数字的可信度全押在它上面。
func TestGetFundSummary_RevenueOnlyCountsCash(t *testing.T) {
	truncateTables(t)
	now := common.GetTimestamp()

	seed := func(kind, account string, quota, cashFen int64, ref string) {
		_, err := InsertFundEntry(&FundEntry{
			UserId: 901, CreatedAt: now,
			Account: account, Kind: kind,
			QuotaDelta: quota, CashFen: cashFen,
			Source: FundSourceAdminCash, RefType: FundRefAdminOp, RefId: ref,
		})
		require.NoError(t, err)
	}
	seed(FundKindPrepay, FundAccountCash, 68493, 10000, "R1")      // ¥100 充值
	seed(FundKindARSettle, FundAccountCredit, -68493, 10000, "R2") // ¥100 回款
	seed(FundKindGift, FundAccountPoints, 342466, 0, "R3")         // 赠送，不计营收
	seed(FundKindCreditGrant, FundAccountCredit, 6849300, 0, "R4") // 开额，不计营收
	seed(FundKindAdjust, FundAccountCash, -1000, 0, "R5")          // 性质不明

	s, err := GetFundSummary(now-3600, now+3600)
	require.NoError(t, err)

	require.Equal(t, int64(20000), s.Revenue.CashFenTotal,
		"营收 = 充值 + 回款，赠送与开额不得计入")
	require.Equal(t, int64(342466), s.Revenue.GiftQuota)
	require.Equal(t, int64(6849300), s.Revenue.CreditGrant)
	require.Equal(t, int64(-1000), s.Revenue.AdjustQuota,
		"性质不明的调整要单独可见，供异常监控")
}

// 期初基线必须幂等：重复执行会把余额再记一遍，自洽校验从此恒偏一倍。
func TestInitFundBaseline_Idempotent(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.Create(&User{Id: 902, Username: "fb_902", Role: 1, Status: 1,
		Quota: 50000, PointsBalance: 3000}).Error)

	created, err := InitFundBaseline(1)
	require.NoError(t, err)
	require.Equal(t, 2, created, "现金与积分各一条（标记不计入 created）")

	created2, err := InitFundBaseline(1)
	require.NoError(t, err)
	require.Equal(t, 0, created2, "重复初始化不得再写入")

	var n int64
	require.NoError(t, DB.Model(&FundEntry{}).Where("kind = ?", FundKindOpening).Count(&n).Error)
	require.Equal(t, int64(3), n, "两条余额期初 + 一条基线标记")
}

// 全站无人持有余额时（全新部署），基线仍须成立——否则自洽校验永远不工作，
// 且页面只会显示「未初始化」，没有任何线索说明为什么点了按钮还是没用。
func TestInitFundBaseline_MarkerCoversZeroBalance(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.Create(&User{Id: 920, Username: "fb_920", Role: 1, Status: 1,
		Quota: 0, AffCode: "aff920"}).Error)

	_, err := InitFundBaseline(1)
	require.NoError(t, err)

	rep, err := CheckFundConsistency()
	require.NoError(t, err)
	require.True(t, rep.HasBaseline, "零余额部署也要能建立基线")
	require.True(t, rep.AllOK)
}

// 没有基线时不产出校验结论：流水表是后加的，历史余额无从解释，
// 此时报「不平」全是假告警，会把真信号淹掉。
func TestCheckFundConsistency_NoBaseline(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.Create(&User{Id: 903, Username: "fb_903", Role: 1, Status: 1,
		Quota: 12345}).Error)

	rep, err := CheckFundConsistency()
	require.NoError(t, err)
	require.False(t, rep.HasBaseline)
	require.Empty(t, rep.Items, "无基线时不应产出任何校验项")
}

// 建立基线后，未发生任何余额变动时应当账平。
func TestCheckFundConsistency_BalancedAfterBaseline(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.Create(&User{Id: 904, Username: "fb_904", Role: 1, Status: 1,
		Quota: 50000, PointsBalance: 3000}).Error)
	_, err := InitFundBaseline(1)
	require.NoError(t, err)

	rep, err := CheckFundConsistency()
	require.NoError(t, err)
	require.True(t, rep.HasBaseline)
	require.True(t, rep.AllOK, "刚建基线就应当账平：%+v", rep.Items)
}

// 这条是整个功能存在的理由：有代码绕过流水表直接改了余额，校验必须报不平。
//
// 不平几乎不是算错——余额和流水是两套独立写入，对不上就说明有一条写入路径漏了埋点。
// 这是发现此类 bug 最早的信号，比等财务月底核对早一个月。
func TestCheckFundConsistency_DetectsLedgerBypass(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.Create(&User{Id: 905, Username: "fb_905", Role: 1, Status: 1,
		Quota: 50000}).Error)
	_, err := InitFundBaseline(1)
	require.NoError(t, err)

	// 模拟「绕过流水表」：直接改余额，不写流水
	require.NoError(t, DB.Model(&User{}).Where("id = ?", 905).
		Update("quota", 90000).Error)

	rep, err := CheckFundConsistency()
	require.NoError(t, err)
	require.True(t, rep.HasBaseline)
	require.False(t, rep.AllOK, "绕过流水表改余额必须被校验抓到")

	var cashItem *FundCheckItem
	for i := range rep.Items {
		if rep.Items[i].Name == "现金" {
			cashItem = &rep.Items[i]
		}
	}
	require.NotNil(t, cashItem)
	require.False(t, cashItem.OK)
	require.Equal(t, int64(40000), cashItem.Diff, "差额应精确等于绕过的金额")
}

// 走正规路径（余额 + 流水同时变更）时校验应当仍然平。
func TestCheckFundConsistency_StaysBalancedViaLedger(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.Create(&User{Id: 906, Username: "fb_906", Role: 1, Status: 1,
		Quota: 50000}).Error)
	_, err := InitFundBaseline(1)
	require.NoError(t, err)

	// 正规入账：余额与流水一起动
	require.NoError(t, IncreaseUserQuota(906, 30000, true))
	_, err = InsertFundEntry(&FundEntry{
		UserId: 906, Account: FundAccountCash, Kind: FundKindPrepay,
		QuotaDelta: 30000, CashFen: 4380,
		Source: FundSourceAdminCash, RefType: FundRefAdminOp, RefId: "OK-1",
	})
	require.NoError(t, err)

	rep, err := CheckFundConsistency()
	require.NoError(t, err)
	require.True(t, rep.AllOK, "余额与流水同步变更时应保持账平：%+v", rep.Items)
}

// 退款是正常业务路径，不得触发假不平。
//
// 异步任务失败时 RefundTaskQuota 把预扣额退回 User.quota 并写一条 LogTypeRefund。
// 若消耗统计只认 LogTypeConsume，消耗被高估、期望余额偏低，每次任务失败都会报一次
// 「账不平」——而这个告警本该只在「有代码绕过流水表」时响。
func TestCheckFundConsistency_RefundIsNotFalseAlarm(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.Create(&User{Id: 907, Username: "fb_907", Role: 1, Status: 1,
		Quota: 50000, PointsBalance: 20000}).Error)
	_, err := InitFundBaseline(1)
	require.NoError(t, err)
	now := common.GetTimestamp()

	// 预扣：余额扣掉，写一条消费日志（其中一半走积分抵扣）
	require.NoError(t, DecreaseUserQuota(907, 3000, true))
	applied, err := DecreaseUserPoints(907, 3000, true)
	require.NoError(t, err)
	require.Equal(t, 3000, applied)
	require.NoError(t, LOG_DB.Create(&Log{
		UserId: 907, CreatedAt: now, Type: LogTypeConsume,
		Quota: 6000, PointsConsumed: 3000,
	}).Error)

	rep, err := CheckFundConsistency()
	require.NoError(t, err)
	require.True(t, rep.AllOK, "正常消费后应账平：%+v", rep.Items)

	// 任务失败全额退款：余额退回，写一条退款日志（含退还的积分）
	require.NoError(t, IncreaseUserQuota(907, 3000, true))
	require.NoError(t, IncreaseUserPoints(907, 3000, true))
	require.NoError(t, LOG_DB.Create(&Log{
		UserId: 907, CreatedAt: now, Type: LogTypeRefund,
		Quota: 6000, PointsConsumed: 3000,
	}).Error)

	rep, err = CheckFundConsistency()
	require.NoError(t, err)
	require.True(t, rep.AllOK, "退款后仍应账平，否则每次任务失败都是一次假告警：%+v", rep.Items)
}

// 软删用户不得触发假不平：fund_entries 不会随软删消失，余额统计也必须涵盖软删用户。
func TestCheckFundConsistency_SoftDeletedUserNoFalseAlarm(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.Create(&User{Id: 908, Username: "fb_908", Role: 1, Status: 1,
		Quota: 40000}).Error)
	_, err := InitFundBaseline(1)
	require.NoError(t, err)

	// 注销时账上还有余额——这是常见情况，不是异常
	require.NoError(t, DB.Delete(&User{}, 908).Error)

	rep, err := CheckFundConsistency()
	require.NoError(t, err)
	require.True(t, rep.AllOK, "软删用户残留余额不应被算成账不平：%+v", rep.Items)
}

// 硬删用户：users 行不复存在，靠一条冲销流水把余额抹平，否则凭空差出该用户的余额。
func TestCheckFundConsistency_HardDeletedUserNoFalseAlarm(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.Create(&User{Id: 909, Username: "fb_909", Role: 1, Status: 1,
		Quota: 40000, AffCode: "aff909"}).Error)
	// 旁观用户：硬删会连同该用户的期初流水一并清除，只有一个用户时基线会整个清空，
	// 校验直接退化成「无基线」而不是「账平」，测不出要测的东西。
	require.NoError(t, DB.Create(&User{Id: 910, Username: "fb_910", Role: 1, Status: 1,
		Quota: 10000, AffCode: "aff910"}).Error)
	_, err := InitFundBaseline(1)
	require.NoError(t, err)

	require.NoError(t, HardDeleteUserById(909))

	// 流水**保留**（审计记录不丢），另写一条冲销把余额抹平
	var closed FundEntry
	require.NoError(t, DB.Where("user_id = ? AND kind = ?", 909, FundKindClosed).
		First(&closed).Error)
	require.Equal(t, int64(-40000), closed.QuotaDelta,
		"冲销额应等于删除时的余额，否则抵不平")

	rep, err := CheckFundConsistency()
	require.NoError(t, err)
	require.True(t, rep.AllOK, "硬删用户不应被算成账不平：%+v", rep.Items)
}

// 基线初始化必须涵盖软删用户：余额统计（Unscoped）算上了他们，基线若漏掉，
// 初始化完成的那一刻就凭空差出「软删用户的残留余额」——开局就存在的假不平。
func TestInitFundBaseline_CoversSoftDeletedUsers(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.Create(&User{Id: 911, Username: "fb_911", Role: 1, Status: 1,
		Quota: 30000, AffCode: "aff911"}).Error)
	require.NoError(t, DB.Create(&User{Id: 912, Username: "fb_912", Role: 1, Status: 1,
		Quota: 20000, AffCode: "aff912"}).Error)
	// 基线尚未建立时该用户已注销，账上仍有余额
	require.NoError(t, DB.Delete(&User{}, 912).Error)

	_, err := InitFundBaseline(1)
	require.NoError(t, err)

	rep, err := CheckFundConsistency()
	require.NoError(t, err)
	require.True(t, rep.AllOK,
		"基线要涵盖软删用户，否则建完基线立刻就是假不平：%+v", rep.Items)
}

// 订阅扣费不动 User.Quota，不得计入现金消耗。
//
// SubscriptionFunding 扣的是 UserSubscription.AmountUsed，而订阅售卖收入记在
// subscription 账户。若把订阅消费从现金里减，任何订阅客户产生用量都会触发假不平。
func TestCheckFundConsistency_SubscriptionConsumeExcluded(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.Create(&User{Id: 913, Username: "fb_913", Role: 1, Status: 1,
		Quota: 50000, AffCode: "aff913"}).Error)
	_, err := InitFundBaseline(1)
	require.NoError(t, err)

	// 订阅扣费：写了消费日志，但 users.quota 一分未动
	require.NoError(t, LOG_DB.Create(&Log{
		UserId: 913, CreatedAt: common.GetTimestamp(), Type: LogTypeConsume,
		Quota: 8000, Other: `{"billing_source":"subscription","subscription_id":7}`,
	}).Error)

	rep, err := CheckFundConsistency()
	require.NoError(t, err)
	require.True(t, rep.AllOK, "订阅消费不得计入现金消耗：%+v", rep.Items)
}

// 套餐权益扣的是次数与算力点批次，同样一分钱没动 users.quota。
// 不排除的话，任何走权益的请求都会被算成现金消耗，现金校验必然假不平——
// 与上面订阅那条是同一个问题的同一个形态。
func TestCheckFundConsistency_EntitlementConsumeExcluded(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.Create(&User{Id: 916, Username: "fb_916", Role: 1, Status: 1,
		Quota: 50000, AffCode: "aff916"}).Error)
	_, err := InitFundBaseline(1)
	require.NoError(t, err)

	require.NoError(t, LOG_DB.Create(&Log{
		UserId: 916, CreatedAt: common.GetTimestamp(), Type: LogTypeConsume,
		Quota: 8000, Other: `{"billing_source":"entitlement","entitlement_id":3}`,
	}).Error)

	rep, err := CheckFundConsistency()
	require.NoError(t, err)
	require.True(t, rep.AllOK, "权益消费不得计入现金消耗：%+v", rep.Items)
}

// 硬删用户在基线之后消费过：他的消费日志留在 logs（可能是独立库，删不掉），
// 而余额与流水都随删号消失。冲销流水要让三者重新自洽。
func TestCheckFundConsistency_HardDeletedUserWithConsumption(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.Create(&User{Id: 914, Username: "fb_914", Role: 1, Status: 1,
		Quota: 50000, AffCode: "aff914"}).Error)
	require.NoError(t, DB.Create(&User{Id: 915, Username: "fb_915", Role: 1, Status: 1,
		Quota: 10000, AffCode: "aff915"}).Error)
	_, err := InitFundBaseline(1)
	require.NoError(t, err)

	// 基线之后消费 20000，余额剩 30000
	require.NoError(t, DecreaseUserQuota(914, 20000, true))
	require.NoError(t, LOG_DB.Create(&Log{
		UserId: 914, CreatedAt: common.GetTimestamp(), Type: LogTypeConsume,
		Quota: 20000,
	}).Error)

	rep, err := CheckFundConsistency()
	require.NoError(t, err)
	require.True(t, rep.AllOK, "消费后应账平：%+v", rep.Items)

	// 硬删：余额与用户行消失，消费日志仍在
	require.NoError(t, HardDeleteUserById(914))

	rep, err = CheckFundConsistency()
	require.NoError(t, err)
	require.True(t, rep.AllOK,
		"冲销流水要抵消掉残留余额，使消费日志不再造成假不平：%+v", rep.Items)
}

// 信用自洽校验的完整等式：信用消耗 − 已核销回款 == credit_used。
//
// 扣费侧接入前这条只能验个残缺版本（「未消耗时应为 0」）。现在透支结转把欠款
// 记进了 credit_used、日志记了 credit_consumed，等式才立得住。
func TestCheckFundConsistency_CreditEquation(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.Create(&User{Id: 916, Username: "fb_916", Role: 1, Status: 1,
		Quota: 0, CreditLimit: 100000, AffCode: "aff916"}).Error)
	_, err := InitFundBaseline(1)
	require.NoError(t, err)
	now := common.GetTimestamp()

	// 客户在授信内消费 8000：扣费把 quota 打负，结算结转进 credit_used
	require.NoError(t, DecreaseUserQuota(916, 8000, true))
	settled, err := SettleOverdraftToCredit(916, 1<<62)
	require.NoError(t, err)
	require.Equal(t, int64(8000), settled)
	require.NoError(t, LOG_DB.Create(&Log{
		UserId: 916, CreatedAt: now, Type: LogTypeConsume,
		Quota: 8000, CreditConsumed: 8000,
	}).Error)

	rep, err := CheckFundConsistency()
	require.NoError(t, err)
	require.True(t, rep.AllOK, "授信消费后三本账都应平：%+v", rep.Items)

	// 回款核销 5000
	require.NoError(t, SettleUserCredit(916, 5000))
	_, err = InsertFundEntry(&FundEntry{
		UserId: 916, CreatedAt: now,
		Account: FundAccountCredit, Kind: FundKindARSettle,
		QuotaDelta: -5000, CashFen: 730,
		Source: FundSourceAdminCash, RefType: FundRefAdminOp, RefId: "AR-1",
	})
	require.NoError(t, err)

	rep, err = CheckFundConsistency()
	require.NoError(t, err)
	require.True(t, rep.AllOK, "回款核销后仍应账平：%+v", rep.Items)

	var creditItem *FundCheckItem
	for i := range rep.Items {
		if rep.Items[i].Name == "信用" {
			creditItem = &rep.Items[i]
		}
	}
	require.NotNil(t, creditItem)
	require.Equal(t, int64(3000), creditItem.Actual, "8000 消费 − 5000 回款 = 3000 应收")
}

// 授信消费不得被算进现金消耗：它减的是 credit_used，不是 users.quota。
func TestGetFundConsumeStats_CreditSplit(t *testing.T) {
	truncateTables(t)
	now := common.GetTimestamp()
	require.NoError(t, LOG_DB.Create(&Log{
		UserId: 917, CreatedAt: now, Type: LogTypeConsume,
		Quota: 10000, PointsConsumed: 2000, CreditConsumed: 3000,
	}).Error)

	st, err := getFundConsumeStats(now-60, now+60)
	require.NoError(t, err)
	require.Equal(t, int64(10000), st.TotalQuota)
	require.Equal(t, int64(2000), st.PointsConsumed)
	require.Equal(t, int64(3000), st.CreditConsumed)
	require.Equal(t, int64(5000), st.CashConsumed,
		"现金承担 = 总额 − 积分 − 授信，三者互斥且穷尽资金来源")
}

// 基线必须记录已存在的欠款。
//
// 信用校验是「期初欠款 + 信用消耗 − 回款 == credit_used」。基线若只记现金与积分，
// 任何建基线时已有欠款的部署都会**永久**报「信用账不平」——而这个告警本该只在
// 流水被绕过时响，恒响等于没有。
func TestInitFundBaseline_RecordsExistingCreditDebt(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.Create(&User{Id: 921, Username: "fb_921", Role: 1, Status: 1,
		Quota: 10000, CreditLimit: 100000, CreditUsed: 25000, AffCode: "aff921"}).Error)

	_, err := InitFundBaseline(1)
	require.NoError(t, err)

	var opening FundEntry
	require.NoError(t, DB.Where("account = ? AND kind = ? AND user_id = ?",
		FundAccountCredit, FundKindOpening, 921).First(&opening).Error)
	require.Equal(t, int64(25000), opening.QuotaDelta, "期初欠款要如实记录")

	rep, err := CheckFundConsistency()
	require.NoError(t, err)
	require.True(t, rep.AllOK, "已有欠款的部署建完基线就该是平的：%+v", rep.Items)
}
