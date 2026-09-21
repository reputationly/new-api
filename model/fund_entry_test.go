package model

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 用于故意回滚事务的哨兵错误。
var errTestRollback = errors.New("rollback for test")

// fund_entries 是收入对账的唯一事实来源，(ref_type, ref_id) 复合唯一索引是它的幂等键。
// 支付补单、审批重试、赠品补发都会重复触发入账点，重复记一条营收就虚高一笔，
// 所以这条索引必须在三库上都真的生效。

func countFundEntries(t *testing.T, refType, refId string) int64 {
	t.Helper()
	var n int64
	require.NoError(t, DB.Model(&FundEntry{}).
		Where("ref_type = ? AND ref_id = ?", refType, refId).Count(&n).Error)
	return n
}

func TestInsertFundEntry_Idempotent(t *testing.T) {
	truncateTables(t)
	entry := &FundEntry{
		UserId:     501,
		Account:    FundAccountCash,
		Kind:       FundKindPrepay,
		QuotaDelta: 68493,
		CashFen:    100,
		Source:     FundSourceOnlinePay,
		RefType:    FundRefTopUp,
		RefId:      "TRADE-IDEMPOTENT-001",
	}

	inserted, err := InsertFundEntry(entry)
	require.NoError(t, err)
	require.True(t, inserted, "首次写入应成功")

	// 同一业务单号再来一次（支付回调重投 / 管理员补单）
	dup := *entry
	dup.Id = 0
	inserted, err = InsertFundEntry(&dup)
	require.NoError(t, err, "重复写入不应报错，静默跳过即可")
	require.False(t, inserted, "重复写入不应产生新行")

	require.Equal(t, int64(1), countFundEntries(t, FundRefTopUp, "TRADE-IDEMPOTENT-001"),
		"同一 (ref_type, ref_id) 只能有一条流水，否则营收翻倍")
}

// 幂等键只在「同一业务对象」内生效：不同单号、或同单号不同业务类型都应各自记账。
func TestInsertFundEntry_DifferentRefsCoexist(t *testing.T) {
	truncateTables(t)
	base := FundEntry{
		UserId:     502,
		Account:    FundAccountCash,
		Kind:       FundKindPrepay,
		QuotaDelta: 1000,
		CashFen:    100,
		Source:     FundSourceOnlinePay,
		RefType:    FundRefTopUp,
		RefId:      "TRADE-A",
	}
	a := base
	inserted, err := InsertFundEntry(&a)
	require.NoError(t, err)
	require.True(t, inserted)

	b := base
	b.Id, b.RefId = 0, "TRADE-B"
	inserted, err = InsertFundEntry(&b)
	require.NoError(t, err)
	require.True(t, inserted, "不同单号应各自记账")

	// 同单号但不同业务类型：topups 与 subscription_orders 的 trade_no 取自不同序列，
	// 理论上可能撞号，复合键必须把类型算进去。
	c := base
	c.Id, c.RefType = 0, FundRefSubscriptionOrder
	inserted, err = InsertFundEntry(&c)
	require.NoError(t, err)
	require.True(t, inserted, "ref_type 不同应视为不同业务对象")
}

// ref 为空会让多条记录互撞唯一索引（空串在三库中都不等价于 NULL），
// 必须在入口挡住，而不是等到写库时报一个难以归因的约束冲突。
func TestInsertFundEntry_RejectsEmptyRef(t *testing.T) {
	truncateTables(t)
	_, err := InsertFundEntry(&FundEntry{
		UserId:  503,
		Account: FundAccountCash,
		Kind:    FundKindPrepay,
		RefType: FundRefTopUp,
		RefId:   "",
	})
	require.ErrorIs(t, err, ErrFundEntryRefEmpty)

	_, err = InsertFundEntry(&FundEntry{
		UserId:  503,
		Account: FundAccountCash,
		Kind:    FundKindPrepay,
		RefType: "",
		RefId:   "X",
	})
	require.ErrorIs(t, err, ErrFundEntryRefEmpty)
}

// 管理员操作没有天然业务单号，生成的幂等键必须互不相同，否则第二笔会被静默吞掉。
func TestNewAdminOpRefId_Unique(t *testing.T) {
	seen := make(map[string]struct{}, 100)
	for i := 0; i < 100; i++ {
		id := NewAdminOpRefId()
		require.NotEmpty(t, id)
		_, dup := seen[id]
		require.False(t, dup, "管理员操作单号重复会导致后一笔流水被静默丢弃：%s", id)
		seen[id] = struct{}{}
	}
}

// 事务内写入：余额变更与流水必须同生共死，否则自洽校验会长期不平、淹掉真信号。
func TestInsertFundEntryTx_RollbackDiscardsEntry(t *testing.T) {
	truncateTables(t)
	err := DB.Transaction(func(tx *gorm.DB) error {
		inserted, err := insertFundEntryTx(tx, &FundEntry{
			UserId:     504,
			Account:    FundAccountPoints,
			Kind:       FundKindGift,
			QuotaDelta: 68493,
			Source:     FundSourceAdminGift,
			RefType:    FundRefAdminOp,
			RefId:      "AD-ROLLBACK-001",
		})
		require.NoError(t, err)
		require.True(t, inserted)
		return errTestRollback
	})
	require.ErrorIs(t, err, errTestRollback)
	require.Equal(t, int64(0), countFundEntries(t, FundRefAdminOp, "AD-ROLLBACK-001"),
		"事务回滚后流水不应残留")
}
