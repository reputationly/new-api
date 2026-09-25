package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 邀请人只该看到被邀请人「给平台带来了多少真钱」。口径与收入对账报表一致：
// 只有 prepay 与 ar_settle 的 cash_fen 计入；赠送、授信开额、性质不明的调整一律不算，
// 余额与消耗字段则根本不下发。

func insertInvitee(t *testing.T, id, inviterId int, username string) {
	t.Helper()
	require.NoError(t, DB.Create(&User{
		Id:        id,
		Username:  username,
		Password:  "x",
		AffCode:   username,
		InviterId: inviterId,
		Quota:     123456,
		UsedQuota: 654321,
	}).Error)
}

func insertFundEntryForTest(t *testing.T, userId int, account, kind string, cashFen int64, refId string) {
	t.Helper()
	inserted, err := InsertFundEntry(&FundEntry{
		UserId:     userId,
		Account:    account,
		Kind:       kind,
		QuotaDelta: 1,
		CashFen:    cashFen,
		Source:     FundSourceAdminCash,
		RefType:    FundRefAdminOp,
		RefId:      refId,
	})
	require.NoError(t, err)
	require.True(t, inserted)
}

func TestGetInviteesByInviter_CashPaidOnlyCountsRevenueKinds(t *testing.T) {
	truncateTables(t)
	const inviter = 9001
	insertInvitee(t, 9101, inviter, "paid")
	insertInvitee(t, 9102, inviter, "gifted")
	insertInvitee(t, 9103, inviter, "nothing")
	insertInvitee(t, 9201, 9999, "other-inviter")

	// paid：在线充值 + 对公转账 + 授信回款都算；同一用户的赠送与授信开额不算
	insertFundEntryForTest(t, 9101, FundAccountCash, FundKindPrepay, 10000, "P-1")
	insertFundEntryForTest(t, 9101, FundAccountCash, FundKindPrepay, 2550, "P-2")
	insertFundEntryForTest(t, 9101, FundAccountCredit, FundKindARSettle, 700, "AR-1")
	insertFundEntryForTest(t, 9101, FundAccountPoints, FundKindGift, 0, "G-1")
	insertFundEntryForTest(t, 9101, FundAccountCredit, FundKindCreditGrant, 0, "CG-1")

	// gifted：只收过赠送与调整。即便某条 adjust 被误填了 cash_fen，也不能算进实付。
	insertFundEntryForTest(t, 9102, FundAccountPoints, FundKindGift, 0, "G-2")
	insertFundEntryForTest(t, 9102, FundAccountCash, FundKindAdjust, 999, "ADJ-1")

	// 别人的下线充了钱，不能串到本邀请人名下
	insertFundEntryForTest(t, 9201, FundAccountCash, FundKindPrepay, 88888, "P-OTHER")

	list, total, _, err := GetInviteesByInviter(inviter, 1, 10)
	require.NoError(t, err)
	require.Equal(t, int64(3), total)

	byName := make(map[string]InviteeInfo, len(list))
	for _, it := range list {
		byName[it.Username] = it
	}
	require.Equal(t, int64(10000+2550+700), byName["paid"].CashPaidFen, "prepay + ar_settle 之和")
	require.Equal(t, int64(0), byName["gifted"].CashPaidFen, "gift / adjust 不计入实付")
	require.Equal(t, int64(0), byName["nothing"].CashPaidFen, "没有流水的用户为 0 而不是缺行")
}
