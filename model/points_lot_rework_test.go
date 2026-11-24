package model

import (
	"fmt"
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
		RefType: FundRefPointsLot, RefId: pointsLotLedgerRef(lot),
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

// 批次 id 会被复用（points_lots 没有 AUTOINCREMENT）：硬删用户或归档清理删掉最大 id 后，
// 新批次可能拿到同一个 id。作废流水的幂等键若只用 id，就会与历史流水相撞，M1 会让这条
// 新批次永远作废不了。这里直接钉住「同一 id、不同发放时刻 → 键不同」。
func TestPointsLotLedgerRef_NotCollideOnReusedId(t *testing.T) {
	a := PointsLot{Id: 7, GrantedAt: 1000}
	b := PointsLot{Id: 7, GrantedAt: 2000}
	require.NotEqual(t, pointsLotLedgerRef(a), pointsLotLedgerRef(b),
		"同一 id 的不同批次实例必须有不同的幂等键")
	require.Equal(t, pointsLotLedgerRef(a), pointsLotLedgerRef(PointsLot{Id: 7, GrantedAt: 1000}),
		"同一条批次重复作废时键必须稳定（幂等）")
}

// 端到端：先作废一条批次并留下历史流水，再让新批次复用它的 id，
// 新批次仍须能正常作废（不能因幂等键相撞而永久失败）。
func TestPointsExpiry_ReusedLotIdStillForfeits(t *testing.T) {
	const id = 4141
	exp1 := shanghaiTime(2026, 3, 1, 23, 59, 59)
	seedLotUser(t, id, 100)
	first := insertLot(t, PointsLot{
		UserId: id, Source: FundSourceCheckin, Amount: 100, ExpiresAt: exp1,
		GrantedAt: exp1 - 86400, Status: PointsLotStatusActive,
	})
	_, _, err := ExpireUserPointsLots(id, exp1+60)
	require.NoError(t, err)

	// 模拟 id 复用：直接插入一条同 id、不同 granted_at 的批次（真实场景是删行后自增回落）
	require.NoError(t, DB.Exec("DELETE FROM points_lots WHERE id = ?", first.Id).Error)
	exp2 := shanghaiTime(2026, 4, 1, 23, 59, 59)
	reused := insertLot(t, PointsLot{
		Id:     first.Id, // 显式指定，复现「自增回落导致 id 复用」
		UserId: id, Source: FundSourceCheckin, Amount: 200, ExpiresAt: exp2,
		GrantedAt: exp2 - 3600, Status: PointsLotStatusActive,
	})
	require.Equal(t, first.Id, reused.Id, "这里就是要复用同一个 id")
	require.NoError(t, DB.Unscoped().Model(&User{}).Where("id = ?", id).
		Update("points_balance", 200).Error)

	forfeited, _, err := ExpireUserPointsLots(id, exp2+60)
	require.NoError(t, err, "复用了 id 的新批次仍须能作废")
	require.EqualValues(t, 200, forfeited)
}

// GrantPoints 对不存在的用户必须整笔失败：sync 对不存在的用户只清批次、不报错，
// 若不检查余额更新命中的行数，会留下孤儿批次与孤儿流水，资金自洽校验从此不平。
func TestGrantPoints_NonexistentUserRollsBack(t *testing.T) {
	const id = 9400
	_, err := GrantPoints(PointsGrant{
		UserId: id, Quota: 1000, Source: FundSourceAdminGift, RefId: "nx",
		Fund: FundEntry{Kind: FundKindGift, Source: FundSourceAdminGift, RefType: FundRefAdminOp, RefId: "nx-9400"},
	})
	require.ErrorIs(t, err, ErrPointsGrantUserNotFound)
	require.Empty(t, listLots(t, id))
	var n int64
	require.NoError(t, DB.Model(&FundEntry{}).Where("user_id = ?", id).Count(&n).Error)
	require.Zero(t, n)
}
