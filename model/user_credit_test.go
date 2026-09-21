package model

import (
	"testing"

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
