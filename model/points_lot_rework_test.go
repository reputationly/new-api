package model

import (
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 第二轮返工（验收单 R1 / R2 / M1 / M2）的回归测试。

// ---------------------------------------------------------------------------
// R1 软删除用户不得被当作「不存在」
// ---------------------------------------------------------------------------

// 软删可恢复：恢复后这些批次还在、余额还在。若分摊时把软删用户判成不存在，
// sync 会删光其批次、过期任务也不作废——恢复后首次 sync 会把这些本该作废的积分
// 补建成永不过期的 legacy，等于凭空送永久积分。
func TestPointsLotSoftDeletedUser_ExpiryStillForfeits(t *testing.T) {
	const id = 4101
	exp := time.Now().Unix() - 3600
	seedLotUser(t, id, 1000)
	insertLot(t, PointsLot{
		UserId: id, Source: FundSourceCheckin, Amount: 1000, ExpiresAt: exp,
		GrantedAt: exp - 86400, Status: PointsLotStatusActive,
	})

	// 软删除（GORM 写 deleted_at，行仍在）
	require.NoError(t, DB.Delete(&User{}, id).Error)
	var alive int64
	require.NoError(t, DB.Unscoped().Model(&User{}).Where("id = ?", id).Count(&alive).Error)
	require.EqualValues(t, 1, alive, "应当是软删除，行仍在")
	require.NoError(t, DB.Model(&User{}).Where("id = ?", id).Count(&alive).Error)
	require.EqualValues(t, 0, alive, "普通查询看不到软删用户（这正是坑的来源）")

	forfeited, bySource, err := ExpireUserPointsLots(id, time.Now().Unix())
	require.NoError(t, err)
	require.EqualValues(t, 1000, forfeited, "软删用户的到期批次照常作废")
	require.Len(t, bySource, 1)

	// 余额同额减少
	var balance int64
	require.NoError(t, DB.Unscoped().Model(&User{}).Where("id = ?", id).
		Select("points_balance").Scan(&balance).Error)
	require.Zero(t, balance)

	// 批次不得被删除，而是标记作废（sync 还会补一条 legacy 初始化标记，属正常）
	var lots []PointsLot
	require.NoError(t, DB.Where("user_id = ? AND source = ?", id, FundSourceCheckin).Find(&lots).Error)
	require.Len(t, lots, 1, "软删不得删批次")
	require.Equal(t, PointsLotStatusExpired, lots[0].Status)
	require.EqualValues(t, 1000, lots[0].Forfeited)

	// expire 流水
	var entries []FundEntry
	require.NoError(t, DB.Where("user_id = ? AND kind = ?", id, FundKindExpire).Find(&entries).Error)
	require.Len(t, entries, 1)
	require.EqualValues(t, -1000, entries[0].QuotaDelta)
}

// 软删用户的首次 sync 也不得删批次（旧实现会走「用户不存在 → DELETE 批次」分支）。
func TestPointsLotSoftDeletedUser_SyncKeepsLots(t *testing.T) {
	const id = 4102
	seedLotUser(t, id, 500)
	insertLot(t, PointsLot{UserId: id, Source: FundSourceCheckin, Amount: 500, Status: PointsLotStatusActive})
	require.NoError(t, DB.Delete(&User{}, id).Error)

	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return syncPointsLotsTx(tx, id, common.GetTimestamp())
	}))

	var kept []PointsLot
	require.NoError(t, DB.Where("user_id = ? AND source = ?", id, FundSourceCheckin).Find(&kept).Error)
	require.Len(t, kept, 1, "软删用户的批次必须保留")
	require.EqualValues(t, 500, kept[0].Amount)
}

// 软删用户参与对账：余额与流水仍然自洽（GetFundBalanceStats 本就用 Unscoped）。
func TestPointsLotSoftDeletedUser_FundConsistencyStaysGreen(t *testing.T) {
	truncateTables(t)
	const id = 4103
	seedLotUser(t, id, 0)
	now := time.Now().Unix()

	require.NoError(t, DB.Create(&PointsLot{
		UserId: id, Source: FundSourceCheckin, Amount: 800,
		ExpiresAt: now - 60, GrantedAt: now - 86400, Status: PointsLotStatusActive,
	}).Error)
	require.NoError(t, DB.Unscoped().Model(&User{}).Where("id = ?", id).
		Update("points_balance", 800).Error)
	_, err := InitFundBaseline(1)
	require.NoError(t, err)

	require.NoError(t, DB.Delete(&User{}, id).Error)
	_, _, err = ExpireUserPointsLots(id, now)
	require.NoError(t, err)

	rep, err := CheckFundConsistency()
	require.NoError(t, err)
	require.True(t, rep.AllOK, "软删用户作废后对账仍须全绿")
}

// ---------------------------------------------------------------------------
// R2 已到期未作废的批次：不进 active / summary，在 history 里按 expired 展示
// ---------------------------------------------------------------------------

func TestPointsLotDueButNotForfeited_HiddenFromActiveAndSummary(t *testing.T) {
	const id = 4111
	now := shanghaiTime(2026, 6, 1, 0, 5, 0)
	due := now - 60 // 已到期，过期任务还没跑
	seedLotUser(t, id, 400)

	insertLot(t, PointsLot{
		UserId: id, Source: FundSourceCheckin, Amount: 300,
		ExpiresAt: due, GrantedAt: due - 86400, Status: PointsLotStatusActive,
	})
	insertLot(t, PointsLot{
		UserId: id, Source: FundSourceKyc, Amount: 100, ExpiresAt: 0,
		Status: PointsLotStatusActive,
	})

	active, surplus, err := GetActivePointsLotViews(id, now)
	require.NoError(t, err)
	require.Zero(t, surplus)
	require.Len(t, active, 1, "已到期的那笔不得出现在可用列表")
	require.Equal(t, FundSourceKyc, active[0].Source)

	summary, err := GetPointsExpirySummary(id, now, 30*86400, 3)
	require.NoError(t, err)
	require.Nil(t, summary.Expiring, "已到期的不得进临期汇总")
	require.Empty(t, summary.NextExpiries)

	history, err := GetPointsLotHistory(id, now)
	require.NoError(t, err)
	var found *PointsLotView
	for i := range history {
		if history[i].Id != 0 && history[i].Source == FundSourceCheckin {
			found = &history[i]
		}
	}
	require.NotNil(t, found, "已到期未作废的批次应出现在 history")
	require.Equal(t, PointsLotStatusExpired, found.Status, "历史里按已过期展示")
	require.EqualValues(t, 300, found.RemainingQuota, "剩余表示即将作废的量")
	require.Equal(t, due, found.ExpiresAt)
}

// ---------------------------------------------------------------------------
// M1 作废流水幂等键已存在时整笔回滚（不能只扣余额不写流水）
// ---------------------------------------------------------------------------

func TestPointsExpiry_ExistingLedgerEntryRollsBackWholeTx(t *testing.T) {
	const id = 4121
	exp := time.Now().Unix() - 3600
	seedLotUser(t, id, 600)
	lot := insertLot(t, PointsLot{
		UserId: id, Source: FundSourceCheckin, Amount: 600, ExpiresAt: exp,
		GrantedAt: exp - 86400, Status: PointsLotStatusActive,
	})

	// 预先占用该批次的作废幂等键
	_, err := InsertFundEntry(&FundEntry{
		UserId: id, Account: FundAccountPoints, Kind: FundKindExpire,
		QuotaDelta: -600, CashFen: 0, Source: FundSourceCheckin,
		RefType: FundRefPointsLot, RefId: strconv.Itoa(lot.Id),
	})
	require.NoError(t, err)

	_, _, err = ExpireUserPointsLots(id, time.Now().Unix())
	require.Error(t, err, "幂等键已存在时必须报错而不是静默扣余额")
	require.Contains(t, err.Error(), "作废流水已存在")

	// 整笔回滚：余额未动、批次仍 active
	var balance int64
	require.NoError(t, DB.Unscoped().Model(&User{}).Where("id = ?", id).
		Select("points_balance").Scan(&balance).Error)
	require.EqualValues(t, 600, balance, "回滚后余额不得变化")

	var after PointsLot
	require.NoError(t, DB.Where("id = ?", lot.Id).First(&after).Error)
	require.Equal(t, PointsLotStatusActive, after.Status, "回滚后批次仍是 active")
	require.Zero(t, after.Forfeited)

	var count int64
	require.NoError(t, DB.Model(&FundEntry{}).Where("user_id = ? AND kind = ?", id, FundKindExpire).
		Count(&count).Error)
	require.EqualValues(t, 1, count, "仍只有预先占用那一条")
}

// ---------------------------------------------------------------------------
// M2 作废流水的备注用可读日期，不是 Unix 时间戳
// ---------------------------------------------------------------------------

func TestPointsExpiry_RemarkUsesReadableDate(t *testing.T) {
	const id = 4131
	exp := shanghaiTime(2026, 3, 15, 23, 59, 59)
	seedLotUser(t, id, 300)
	insertLot(t, PointsLot{
		UserId: id, Source: FundSourceCheckin, Amount: 300, ExpiresAt: exp,
		GrantedAt: exp - 86400, Status: PointsLotStatusActive,
	})

	_, _, err := ExpireUserPointsLots(id, exp+60)
	require.NoError(t, err)

	var entry FundEntry
	require.NoError(t, DB.Where("user_id = ? AND kind = ?", id, FundKindExpire).First(&entry).Error)
	require.Contains(t, entry.Remark, "03-15", "备注应是可读日期")
	require.NotContains(t, entry.Remark, fmt.Sprintf("%d", exp), "不得出现 Unix 时间戳")
}
