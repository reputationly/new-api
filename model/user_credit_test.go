package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

// 信用账户的两个写操作都是条件更新，守的是两条不变量：
//   - credit_limit >= credit_used（否则客户瞬间超限停服，应收口径错乱）
//   - credit_used >= 0（应收做成负数会让风控指标失真且无法从流水反推）
//
// 这两条在并发下只能靠 WHERE 兜住——调用方的前置检查会被并发窗口绕过。

func seedCreditUser(t *testing.T, id int, limit, used, settled int64) {
	t.Helper()
	require.NoError(t, DB.Create(&User{
		Id: id, Username: "cr_" + string(rune('a'+id%26)) + string(rune('0'+id%10)),
		Role: 1, Status: 1,
		CreditLimit: limit, CreditUsed: used, CreditSettled: settled,
	}).Error)
}

func TestSetUserCreditLimit(t *testing.T) {
	truncateTables(t)
	seedCreditUser(t, 601, 0, 0, 0)

	require.NoError(t, SetUserCreditLimit(601, 100000))
	limit, used, _, err := GetUserCreditState(601)
	require.NoError(t, err)
	require.Equal(t, int64(100000), limit)
	require.Equal(t, int64(0), used)
}

// 调低上限到已用未结之下必须被拒。运营侧已有一道检查，这里守的是并发窗口：
// 一个管理员正在调低上限、同时客户的消费把 credit_used 推高。
func TestSetUserCreditLimit_RejectsBelowUsed(t *testing.T) {
	truncateTables(t)
	seedCreditUser(t, 602, 100000, 80000, 0)

	err := SetUserCreditLimit(602, 50000)
	require.ErrorIs(t, err, ErrCreditLimitBelowUsed)

	limit, used, _, err := GetUserCreditState(602)
	require.NoError(t, err)
	require.Equal(t, int64(100000), limit, "被拒后上限不得变动")
	require.Equal(t, int64(80000), used)

	// 恰好等于已用未结是允许的：额度用尽但不违反不变量
	require.NoError(t, SetUserCreditLimit(602, 80000))
}

func TestSettleUserCredit(t *testing.T) {
	truncateTables(t)
	seedCreditUser(t, 603, 100000, 80000, 0)

	require.NoError(t, SettleUserCredit(603, 30000))

	limit, used, settled, err := GetUserCreditState(603)
	require.NoError(t, err)
	require.Equal(t, int64(100000), limit, "核销不改变授信上限")
	require.Equal(t, int64(50000), used, "已用未结相应减少，额度随之恢复")
	require.Equal(t, int64(30000), settled, "累计已核销相应增加")
}

// 核销额度超过欠款就是记错账：宁可拒绝让运营重填，也不要把应收做成负数。
func TestSettleUserCredit_RejectsOverSettle(t *testing.T) {
	truncateTables(t)
	seedCreditUser(t, 604, 100000, 20000, 0)

	err := SettleUserCredit(604, 30000)
	require.ErrorIs(t, err, ErrCreditSettleExceeds)

	_, used, settled, err := GetUserCreditState(604)
	require.NoError(t, err)
	require.Equal(t, int64(20000), used, "被拒后已用未结不得变动")
	require.Equal(t, int64(0), settled, "被拒后累计核销不得增加")
}

// 全额核销后应收归零，额度完全恢复。
func TestSettleUserCredit_FullSettle(t *testing.T) {
	truncateTables(t)
	seedCreditUser(t, 605, 100000, 20000, 5000)

	require.NoError(t, SettleUserCredit(605, 20000))

	_, used, settled, err := GetUserCreditState(605)
	require.NoError(t, err)
	require.Equal(t, int64(0), used)
	require.Equal(t, int64(25000), settled, "累计核销应在原有基础上累加")
}

func TestSettleUserCredit_RejectsNonPositive(t *testing.T) {
	truncateTables(t)
	seedCreditUser(t, 606, 100000, 20000, 0)

	require.Error(t, SettleUserCredit(606, 0))
	require.Error(t, SettleUserCredit(606, -1))

	_, used, _, err := GetUserCreditState(606)
	require.NoError(t, err)
	require.Equal(t, int64(20000), used)
}

// 透支结转：扣费侧照常把 quota 扣成负数，结算后由本函数挪进 CreditUsed 并归零。
// 这是「授信接入扣费链路」的唯一一步，扣费代码一行未改。
func TestSettleOverdraftToCredit(t *testing.T) {
	truncateTables(t)
	seedCreditUser(t, 621, 100000, 0, 0)

	// 扣费把余额打到 -3000（现有 DecreaseUserQuota 本就是无条件递减）
	require.NoError(t, DecreaseUserQuota(621, 3000, true))
	_, used, _, err := GetUserCreditState(621)
	require.NoError(t, err)
	require.Equal(t, int64(0), used, "结转前欠款尚未产生")

	settled, err := SettleOverdraftToCredit(621, 1<<62)
	require.NoError(t, err)
	require.Equal(t, int64(3000), settled, "返回值用于写 Log.CreditConsumed")

	var u User
	require.NoError(t, DB.Select("quota", "credit_used").Where("id = ?", 621).First(&u).Error)
	require.Equal(t, 0, u.Quota, "结转后余额归零，quota 恢复「预付余额」语义")
	require.Equal(t, int64(3000), u.CreditUsed, "欠款进独立科目，不与预付余额混在一个数字里")
}

// 余额为正时不结转：绝大多数请求走这条路径，必须是无副作用的快速返回。
func TestSettleOverdraftToCredit_NoOpWhenPositive(t *testing.T) {
	truncateTables(t)
	seedCreditUser(t, 622, 100000, 0, 0)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", 622).Update("quota", 5000).Error)

	settled, err := SettleOverdraftToCredit(622, 1<<62)
	require.NoError(t, err)
	require.Equal(t, int64(0), settled)

	var u User
	require.NoError(t, DB.Select("quota", "credit_used").Where("id = ?", 622).First(&u).Error)
	require.Equal(t, 5000, u.Quota, "正余额不得被动到")
	require.Equal(t, int64(0), u.CreditUsed)
}

// 多次透支累加欠款：每次结算各结转一次，欠款按笔累积。
func TestSettleOverdraftToCredit_Accumulates(t *testing.T) {
	truncateTables(t)
	seedCreditUser(t, 623, 100000, 0, 0)

	require.NoError(t, DecreaseUserQuota(623, 2000, true))
	s1, err := SettleOverdraftToCredit(623, 1<<62)
	require.NoError(t, err)
	require.Equal(t, int64(2000), s1)

	require.NoError(t, DecreaseUserQuota(623, 1500, true))
	s2, err := SettleOverdraftToCredit(623, 1<<62)
	require.NoError(t, err)
	require.Equal(t, int64(1500), s2, "第二笔只结转新产生的透支")

	_, used, _, err := GetUserCreditState(623)
	require.NoError(t, err)
	require.Equal(t, int64(3500), used)
}

// 结转 → 回款核销的完整闭环：核销后欠款归零、授信额度恢复。
func TestSettleOverdraftToCredit_ThenSettleCredit(t *testing.T) {
	truncateTables(t)
	seedCreditUser(t, 624, 100000, 0, 0)

	require.NoError(t, DecreaseUserQuota(624, 8000, true))
	_, err := SettleOverdraftToCredit(624, 1<<62)
	require.NoError(t, err)

	require.NoError(t, SettleUserCredit(624, 8000))

	limit, used, settled, err := GetUserCreditState(624)
	require.NoError(t, err)
	require.Equal(t, int64(0), used, "回款后欠款归零")
	require.Equal(t, int64(8000), settled)
	require.Equal(t, int64(100000), limit-used, "授信额度完全恢复")
}

// 并发归因：结转必须以「本次请求的消费额」封顶。
//
// 同一客户两个请求都透支、都还没结算时，先结算的那个若不封顶就会把两笔透支一起
// 扫进自己的 CreditConsumed，日志里出现「消费 1000 却记了 2000 授信」，
// 于是 CashConsumed = Quota − Points − Credit 变成负数。
func TestSettleOverdraftToCredit_CapsAtRequestAmount(t *testing.T) {
	truncateTables(t)
	seedCreditUser(t, 625, 100000, 0, 0)

	// 两个请求各预扣 1000，都还没结算 → 余额 -2000
	require.NoError(t, DecreaseUserQuota(625, 1000, true))
	require.NoError(t, DecreaseUserQuota(625, 1000, true))

	// 请求 A 结算：只认领自己那 1000
	settledA, err := SettleOverdraftToCredit(625, 1000)
	require.NoError(t, err)
	require.Equal(t, int64(1000), settledA, "不得把并发请求的透支一起扫走")

	var u User
	require.NoError(t, DB.Select("quota", "credit_used").Where("id = ?", 625).First(&u).Error)
	require.Equal(t, -1000, u.Quota, "余下的透支留给并发请求结算时处理")
	require.Equal(t, int64(1000), u.CreditUsed)

	// 请求 B 结算：认领剩下的 1000
	settledB, err := SettleOverdraftToCredit(625, 1000)
	require.NoError(t, err)
	require.Equal(t, int64(1000), settledB)

	require.NoError(t, DB.Select("quota", "credit_used").Where("id = ?", 625).First(&u).Error)
	require.Equal(t, 0, u.Quota, "两笔都结转后余额归零")
	require.Equal(t, int64(2000), u.CreditUsed, "欠款总额与实际消费一致")
}

// 退款冲销欠款，不是退成现金。
//
// 这是套利通道的堵口：0 余额的授信客户走信用消费后任务失败，若全额退进钱包，
// 他就凭空得到一笔可用真钱而欠款一分不减。
func TestReduceUserCreditUsed(t *testing.T) {
	truncateTables(t)
	seedCreditUser(t, 626, 100000, 3000, 0)

	require.NoError(t, ReduceUserCreditUsed(626, 1200))

	limit, used, settled, err := GetUserCreditState(626)
	require.NoError(t, err)
	require.Equal(t, int64(1800), used)
	require.Equal(t, int64(0), settled,
		"冲销不是回款：不得累加 CreditSettled，否则「累计已回款」虚高、对账看到不存在的回款")
	require.Equal(t, int64(100000), limit)
}

// 欠款不足以冲销时静默跳过，不把应收做成负数，也不中断退款主流程。
func TestReduceUserCreditUsed_InsufficientIsNoOp(t *testing.T) {
	truncateTables(t)
	seedCreditUser(t, 627, 100000, 500, 0)

	require.NoError(t, ReduceUserCreditUsed(627, 2000), "退款主流程不该因此中断")

	_, used, _, err := GetUserCreditState(627)
	require.NoError(t, err)
	require.Equal(t, int64(500), used, "宁可少冲一笔也不要把应收做成负数")
}

// 授信感知的条件扣减：允许透支，但透支后的负余额不得超过可用授信。
// 这是授信上限在扣费侧的唯一执行点——写在 WHERE 里，并发下也越不过去。
func TestTryDecreaseUserQuotaWithinCredit(t *testing.T) {
	truncateTables(t)
	seedCreditUser(t, 631, 5000, 0, 0) // 授信 5000
	require.NoError(t, DB.Model(&User{}).Where("id = ?", 631).Update("quota", 1000).Error)

	// 余额 1000 + 授信 5000 = 可用 6000
	ok, err := TryDecreaseUserQuotaWithinCredit(631, 4000)
	require.NoError(t, err)
	require.True(t, ok, "在可用授信内应放行")

	var u User
	require.NoError(t, DB.Select("quota").Where("id = ?", 631).First(&u).Error)
	require.Equal(t, -3000, u.Quota, "允许透支，由结算时结转进 credit_used")

	// 还剩 2000 可用（授信 5000 − 已透支 3000）
	ok, err = TryDecreaseUserQuotaWithinCredit(631, 2500)
	require.NoError(t, err)
	require.False(t, ok, "超出可用授信必须拒绝，否则授信上限形同虚设")

	require.NoError(t, DB.Select("quota").Where("id = ?", 631).First(&u).Error)
	require.Equal(t, -3000, u.Quota, "被拒时余额不得变动")

	// 恰好用满允许
	ok, err = TryDecreaseUserQuotaWithinCredit(631, 2000)
	require.NoError(t, err)
	require.True(t, ok)
}

// 未开授信的用户行为与原来一致：余额必须充足，绝不透支。
func TestTryDecreaseUserQuotaWithinCredit_NoCreditMeansNoOverdraft(t *testing.T) {
	truncateTables(t)
	seedCreditUser(t, 632, 0, 0, 0) // credit_limit = 0
	require.NoError(t, DB.Model(&User{}).Where("id = ?", 632).Update("quota", 1000).Error)

	ok, err := TryDecreaseUserQuotaWithinCredit(632, 1500)
	require.NoError(t, err)
	require.False(t, ok, "未开授信不得透支")

	ok, err = TryDecreaseUserQuotaWithinCredit(632, 1000)
	require.NoError(t, err)
	require.True(t, ok, "余额充足照常扣")

	var u User
	require.NoError(t, DB.Select("quota").Where("id = ?", 632).First(&u).Error)
	require.Equal(t, 0, u.Quota)
}

// 已用授信会压缩可用透支空间：上限是「剩余授信」而非「授信总额」。
func TestTryDecreaseUserQuotaWithinCredit_RespectsUsedCredit(t *testing.T) {
	truncateTables(t)
	seedCreditUser(t, 633, 5000, 4000, 0) // 授信 5000，已用 4000 → 仅剩 1000

	ok, err := TryDecreaseUserQuotaWithinCredit(633, 1500)
	require.NoError(t, err)
	require.False(t, ok, "已用授信必须计入，否则客户能突破上限")

	ok, err = TryDecreaseUserQuotaWithinCredit(633, 1000)
	require.NoError(t, err)
	require.True(t, ok)
}

// 未开授信的用户不得凭空产生欠款。
//
// 结算补扣是无条件的（服务已交付、允许欠费），所以任何用户的 quota 都可能被打成负数。
// 但那是预估不准造成的系统性透支，不是授信——记成应收账款没有依据，还会让
// 「授信敞口」= limit − used 变成负数。保持为负 quota 即改动前的既有语义。
func TestSettleOverdraftToCredit_SkipsUsersWithoutCreditLine(t *testing.T) {
	truncateTables(t)
	seedCreditUser(t, 641, 0, 0, 0) // credit_limit = 0，从未授信
	require.NoError(t, DecreaseUserQuota(641, 500, true))

	settled, err := SettleOverdraftToCredit(641, 500)
	require.NoError(t, err)
	require.Equal(t, int64(0), settled, "未授信用户不得结转")

	var u User
	require.NoError(t, DB.Select("quota", "credit_used").Where("id = ?", 641).First(&u).Error)
	require.Equal(t, -500, u.Quota, "负余额保持原样，下次充值自然填平")
	require.Equal(t, int64(0), u.CreditUsed, "从没授信给他，不该有应收")
}

// 无授信用户的结算超支仍要让现金账自洽：那笔超支已计入现金消耗，
// 「期初 − 消耗 == 负余额」等式照样成立，不结转反而更简单一致。
func TestCheckFundConsistency_OverdraftWithoutCreditLineStaysBalanced(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.Create(&User{Id: 642, Username: "cr_642", Role: 1, Status: 1,
		Quota: 1000, AffCode: "aff642"}).Error)
	_, err := InitFundBaseline(1)
	require.NoError(t, err)

	// 预扣 1000 后结算补扣 500：实际用量超过预估，余额被打到 -500
	require.NoError(t, DecreaseUserQuota(642, 1500, true))
	settled, err := SettleOverdraftToCredit(642, 1500)
	require.NoError(t, err)
	require.Equal(t, int64(0), settled)
	require.NoError(t, LOG_DB.Create(&Log{
		UserId: 642, CreatedAt: common.GetTimestamp(), Type: LogTypeConsume,
		Quota: 1500,
	}).Error)

	rep, err := CheckFundConsistency()
	require.NoError(t, err)
	require.True(t, rep.AllOK,
		"未结转的负余额不应造成账不平——那笔超支已在现金消耗里：%+v", rep.Items)
}
