package model

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 复用本包 TestMain（内存 SQLite、MaxOpenConns(1)、RedisEnabled=false）。

func shanghaiTime(y int, m time.Month, d, hh, mm, ss int) int64 {
	return time.Date(y, m, d, hh, mm, ss, 0, pointsExpiryLoc).Unix()
}

func seedLotUser(t *testing.T, id int, balance int64) {
	t.Helper()
	require.NoError(t, DB.Create(&User{
		Id:            id,
		Username:      fmt.Sprintf("pl_%d", id),
		Role:          1,
		Status:        1,
		Group:         "default",
		PointsBalance: int(balance),
	}).Error)
	t.Cleanup(func() {
		// 批次与流水都要清：本包测试共用一个内存库，残留的 surplus / 负余额批次会
		// 让 §十一 的三项健康检查在别的用例里报红。
		DB.Exec("DELETE FROM points_lots WHERE user_id = ?", id)
		DB.Exec("DELETE FROM fund_entries WHERE user_id = ?", id)
		DB.Unscoped().Where("id = ?", id).Delete(&User{})
	})
}

func readLotBalance(t *testing.T, id int) int64 {
	t.Helper()
	var b int64
	require.NoError(t, DB.Model(&User{}).Where("id = ?", id).Select("points_balance").Scan(&b).Error)
	return b
}

func listLots(t *testing.T, userId int) []PointsLot {
	t.Helper()
	var lots []PointsLot
	require.NoError(t, DB.Where("user_id = ?", userId).Order("id").Find(&lots).Error)
	return lots
}

func sumActiveRemaining(lots []PointsLot) int64 {
	var s int64
	for _, l := range lots {
		if l.Status == PointsLotStatusActive {
			s += l.Amount - l.Attributed
		}
	}
	return s
}

func insertLot(t *testing.T, lot PointsLot) PointsLot {
	t.Helper()
	if lot.Status == "" {
		lot.Status = PointsLotStatusActive
	}
	require.NoError(t, DB.Create(&lot).Error)
	return lot
}

// ---------------------------------------------------------------------------
// allocatePointsLots —— 纯函数
// ---------------------------------------------------------------------------

func TestAllocatePointsLots_DeductsEarliestExpiryFirst(t *testing.T) {
	now := shanghaiTime(2026, 1, 5, 0, 5, 0)
	lots := []PointsLot{
		{Id: 1, Amount: 100, ExpiresAt: shanghaiTime(2026, 3, 1, 23, 59, 59), Status: PointsLotStatusActive},
		{Id: 2, Amount: 100, ExpiresAt: shanghaiTime(2026, 2, 1, 23, 59, 59), Status: PointsLotStatusActive},
		{Id: 3, Amount: 100, ExpiresAt: 0, Status: PointsLotStatusActive},
	}
	// 余额 250 → 净消耗 50，应全部落在 2 月 1 日到期的那笔上
	muts, surplus := allocatePointsLots(lots, 250, now)
	require.Zero(t, surplus)
	require.Len(t, muts, 1)
	require.Equal(t, 2, muts[0].LotId)
	require.EqualValues(t, 50, muts[0].AttributedDelta)
	require.Equal(t, PointsLotStatusActive, muts[0].NewStatus)
}

func TestAllocatePointsLots_FillsAcrossLotsAndExhausts(t *testing.T) {
	now := shanghaiTime(2026, 1, 5, 0, 5, 0)
	lots := []PointsLot{
		{Id: 1, Amount: 100, ExpiresAt: 0, Status: PointsLotStatusActive}, // 永不过期排最后
		{Id: 2, Amount: 100, ExpiresAt: shanghaiTime(2026, 2, 1, 23, 59, 59), Status: PointsLotStatusActive},
		{Id: 3, Amount: 100, ExpiresAt: shanghaiTime(2026, 2, 1, 23, 59, 59), Status: PointsLotStatusActive},
	}
	// 同一到期时刻按 id 升序：先 2 再 3。余额 0 → 净消耗 300。
	muts, surplus := allocatePointsLots(lots, 0, now)
	require.Zero(t, surplus)
	require.Len(t, muts, 3)
	require.Equal(t, 2, muts[0].LotId)
	require.EqualValues(t, 100, muts[0].AttributedDelta)
	require.Equal(t, PointsLotStatusExhausted, muts[0].NewStatus)
	require.Equal(t, 3, muts[1].LotId)
	require.EqualValues(t, 100, muts[1].AttributedDelta)
	require.Equal(t, 1, muts[2].LotId)
	require.EqualValues(t, 100, muts[2].AttributedDelta)
	require.Equal(t, PointsLotStatusExhausted, muts[2].NewStatus)
}

func TestAllocatePointsLots_PartialDeductionKeepsActive(t *testing.T) {
	now := shanghaiTime(2026, 1, 5, 0, 5, 0)
	lots := []PointsLot{{Id: 7, Amount: 100, ExpiresAt: 0, Status: PointsLotStatusActive}}
	muts, _ := allocatePointsLots(lots, 60, now)
	require.Len(t, muts, 1)
	require.EqualValues(t, 40, muts[0].AttributedDelta)
	require.Equal(t, PointsLotStatusActive, muts[0].NewStatus)
}

func TestAllocatePointsLots_NoChangeWhenBalanced(t *testing.T) {
	now := shanghaiTime(2026, 1, 5, 0, 5, 0)
	lots := []PointsLot{{Id: 1, Amount: 100, Attributed: 30, ExpiresAt: 0, Status: PointsLotStatusActive}}
	muts, surplus := allocatePointsLots(lots, 70, now)
	require.Empty(t, muts)
	require.Zero(t, surplus)
}

func TestAllocatePointsLots_BackfillsInReverseExpiryOrder(t *testing.T) {
	now := shanghaiTime(2026, 1, 5, 0, 5, 0)
	lots := []PointsLot{
		{Id: 1, Amount: 100, Attributed: 100, ExpiresAt: shanghaiTime(2026, 2, 1, 23, 59, 59), Status: PointsLotStatusExhausted},
		{Id: 2, Amount: 100, Attributed: 100, ExpiresAt: 0, Status: PointsLotStatusExhausted},
	}
	// 余额比批次多 50 → 回补 50。永不过期的先还（它排在扣减顺序的最后）。
	muts, surplus := allocatePointsLots(lots, 50, now)
	require.Zero(t, surplus)
	require.Len(t, muts, 1)
	require.Equal(t, 2, muts[0].LotId)
	require.EqualValues(t, -50, muts[0].AttributedDelta)
	require.Equal(t, PointsLotStatusActive, muts[0].NewStatus)
}

func TestAllocatePointsLots_BackfillSkipsExpiredLots(t *testing.T) {
	now := shanghaiTime(2026, 1, 5, 0, 5, 0)
	expired := shanghaiTime(2026, 1, 1, 23, 59, 59) // 已到期，尚未被任务作废
	lots := []PointsLot{
		{Id: 1, Amount: 100, Attributed: 100, ExpiresAt: expired, Status: PointsLotStatusExhausted},
	}
	muts, surplus := allocatePointsLots(lots, 50, now)
	require.Empty(t, muts, "已到期的批次不得复活")
	require.EqualValues(t, 50, surplus, "回补不完的部分应作为 surplus 返回")
}

func TestAllocatePointsLots_BackfillRevivesExhausted(t *testing.T) {
	now := shanghaiTime(2026, 1, 5, 0, 5, 0)
	lots := []PointsLot{
		{Id: 1, Amount: 100, Attributed: 40, ExpiresAt: 0, Status: PointsLotStatusActive},
	}
	// 余额比批次多 30：回补到 active 批次，状态保持 active
	muts, surplus := allocatePointsLots(lots, 90, now)
	require.Zero(t, surplus)
	require.Len(t, muts, 1)
	require.EqualValues(t, -30, muts[0].AttributedDelta)
	require.Equal(t, PointsLotStatusActive, muts[0].NewStatus)
}

// ---------------------------------------------------------------------------
// syncPointsLotsTx
// ---------------------------------------------------------------------------

func TestPointsLotsSync_CreatesLegacyFromPreExistingBalance(t *testing.T) {
	const id = 1001
	seedLotUser(t, id, 5000)

	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return syncPointsLotsTx(tx, id, grantNow(0))
	}))

	lots := listLots(t, id)
	require.Len(t, lots, 1)
	require.Equal(t, PointsLotSourceLegacy, lots[0].Source)
	require.EqualValues(t, 5000, lots[0].Amount)
	require.Zero(t, lots[0].ExpiresAt)
	require.Equal(t, PointsLotStatusActive, lots[0].Status)
}

func TestPointsLotsSync_CreatesZeroLegacyMarker(t *testing.T) {
	const id = 1002
	seedLotUser(t, id, 0)

	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return syncPointsLotsTx(tx, id, grantNow(0))
	}))

	lots := listLots(t, id)
	require.Len(t, lots, 1)
	require.Equal(t, PointsLotSourceLegacy, lots[0].Source)
	require.Zero(t, lots[0].Amount)
	require.Equal(t, PointsLotStatusExhausted, lots[0].Status, "0 额存量批次作为「已初始化」标记")
}

func TestPointsLotsSync_SecondSurplusBecomesSurplusLot(t *testing.T) {
	const id = 1003
	seedLotUser(t, id, 0)

	// 首次：建 0 额标记
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return syncPointsLotsTx(tx, id, grantNow(0))
	}))
	// 余额凭空多出 300（模拟某条发放路径漏建批次）
	require.NoError(t, DB.Model(&User{}).Where("id = ?", id).
		Update("points_balance", gorm.Expr("points_balance + ?", 300)).Error)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return syncPointsLotsTx(tx, id, grantNow(0))
	}))

	lots := listLots(t, id)
	require.Len(t, lots, 2)
	require.Equal(t, PointsLotSourceSurplus, lots[1].Source)
	require.EqualValues(t, 300, lots[1].Amount)
	require.Zero(t, lots[1].ExpiresAt, "溢出批次永不过期（§3.4 决策 3 修订）")
	require.Equal(t, PointsLotStatusActive, lots[1].Status)
}

func TestPointsLotsSync_DeletesLotsForMissingUser(t *testing.T) {
	const id = 1004
	seedLotUser(t, id, 100)
	insertLot(t, PointsLot{UserId: id, Source: "checkin", Amount: 100, Status: PointsLotStatusActive})
	require.NoError(t, DB.Unscoped().Where("id = ?", id).Delete(&User{}).Error)

	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return syncPointsLotsTx(tx, id, grantNow(0))
	}))
	require.Empty(t, listLots(t, id), "用户已被硬删除，残留批次应清理")
}

func TestPointsLotsSync_NegativeBalanceDegradesWithoutError(t *testing.T) {
	const id = 1005
	seedLotUser(t, id, 0)
	insertLot(t, PointsLot{UserId: id, Source: "checkin", Amount: 100, Status: PointsLotStatusActive})
	// 存量库里的负余额（曾经允许 override 写负数）
	require.NoError(t, DB.Model(&User{}).Where("id = ?", id).Update("points_balance", -50).Error)

	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return syncPointsLotsTx(tx, id, grantNow(0))
	}), "负余额不得让 sync 失败（否则该用户永久无法获得积分）")

	require.EqualValues(t, -50, readLotBalance(t, id), "sync 不得改动余额")
}

// ---------------------------------------------------------------------------
// 发放
// ---------------------------------------------------------------------------

func TestGrantPoints_WritesBalanceLotAndLedgerTogether(t *testing.T) {
	const id = 1010
	seedLotUser(t, id, 0)
	now := shanghaiTime(2026, 1, 5, 10, 0, 0)

	_, err := GrantPoints(PointsGrant{
		UserId: id, Quota: 6849, Source: FundSourceCheckin, RefId: "1010:2026-01-05", Now: now,
		Fund: FundEntry{Kind: FundKindGift, Source: FundSourceCheckin, RefType: FundRefCheckin, RefId: "1010:2026-01-05"},
	})
	require.NoError(t, err)

	require.EqualValues(t, 6849, readLotBalance(t, id))
	lots := listLots(t, id)
	require.Len(t, lots, 2, "一条 0 额 legacy 标记 + 一条签到批次")
	require.EqualValues(t, 6849, lots[1].Amount)
	require.EqualValues(t, now, lots[1].GrantedAt)

	var entries []FundEntry
	require.NoError(t, DB.Where("user_id = ? AND account = ?", id, FundAccountPoints).Order("id").Find(&entries).Error)
	require.Len(t, entries, 1)
	require.EqualValues(t, 6849, entries[0].QuotaDelta)
	require.Equal(t, FundKindGift, entries[0].Kind)
}

func TestGrantPoints_DuplicateRefRollsBackEverything(t *testing.T) {
	const id = 1011
	seedLotUser(t, id, 0)
	now := shanghaiTime(2026, 1, 5, 10, 0, 0)

	g := PointsGrant{
		UserId: id, Quota: 6849, Source: FundSourceKyc, RefId: "dup", Now: now,
		Fund: FundEntry{Kind: FundKindGift, Source: FundSourceKyc, RefType: FundRefUser, RefId: "dup"},
	}
	_, err := GrantPoints(g)
	require.NoError(t, err)
	balanceAfterFirst := readLotBalance(t, id)
	lotsAfterFirst := listLots(t, id)

	_, err = GrantPoints(g)
	require.ErrorIs(t, err, ErrPointsGrantDuplicate)

	require.EqualValues(t, balanceAfterFirst, readLotBalance(t, id), "重复发放不得再加余额")
	require.Len(t, listLots(t, id), len(lotsAfterFirst), "重复发放不得再加批次")
}

func TestGrantPoints_MergeWeekCombinesSameExpiry(t *testing.T) {
	const id = 1012
	seedLotUser(t, id, 0)
	// 同一周的周一与周三：ExpireAtWeeks 结果相同 → 合并为一笔
	monday := shanghaiTime(2026, 1, 5, 9, 0, 0)
	wednesday := shanghaiTime(2026, 1, 7, 20, 0, 0)
	exp := ExpireAtWeeks(monday, 1)
	require.Equal(t, exp, ExpireAtWeeks(wednesday, 1))

	for i, now := range []int64{monday, wednesday} {
		_, err := GrantPoints(PointsGrant{
			UserId: id, Quota: 1000, Source: FundSourceCheckin, RefId: fmt.Sprintf("1012:d%d", i),
			ExpiresAt: exp, MergeWeek: true, Now: now,
			Fund: FundEntry{
				Kind: FundKindGift, Source: FundSourceCheckin, RefType: FundRefCheckin,
				RefId: fmt.Sprintf("1012:d%d", i),
			},
		})
		require.NoError(t, err)
	}

	lots := listLots(t, id)
	var checkin []PointsLot
	for _, l := range lots {
		if l.Source == FundSourceCheckin {
			checkin = append(checkin, l)
		}
	}
	require.Len(t, checkin, 1, "同周合并为一笔")
	require.EqualValues(t, 2000, checkin[0].Amount)
	require.EqualValues(t, monday, checkin[0].GrantedAt, "GrantedAt 保持首笔")
	require.EqualValues(t, exp, checkin[0].ExpiresAt)
}

func TestGrantPoints_MergeWeekDifferentWeekCreatesNewLot(t *testing.T) {
	const id = 1013
	seedLotUser(t, id, 0)
	monday := shanghaiTime(2026, 1, 5, 9, 0, 0)
	nextMonday := shanghaiTime(2026, 1, 12, 9, 0, 0)
	require.NotEqual(t, ExpireAtWeeks(monday, 1), ExpireAtWeeks(nextMonday, 1))

	for i, now := range []int64{monday, nextMonday} {
		_, err := GrantPoints(PointsGrant{
			UserId: id, Quota: 1000, Source: FundSourceCheckin, RefId: fmt.Sprintf("1013:w%d", i),
			ExpiresAt: ExpireAtWeeks(now, 1), MergeWeek: true, Now: now,
			Fund: FundEntry{
				Kind: FundKindGift, Source: FundSourceCheckin, RefType: FundRefCheckin,
				RefId: fmt.Sprintf("1013:w%d", i),
			},
		})
		require.NoError(t, err)
	}

	var checkin int
	for _, l := range listLots(t, id) {
		if l.Source == FundSourceCheckin {
			checkin++
		}
	}
	require.Equal(t, 2, checkin, "跨周应各建一笔")
}

// ---------------------------------------------------------------------------
// 到期时间
// ---------------------------------------------------------------------------

func TestExpireAtDays(t *testing.T) {
	// 12-01 任意时刻 + 30 天 → 12-31 23:59:59
	got := ExpireAtDays(shanghaiTime(2026, 12, 1, 0, 0, 1), 30)
	require.Equal(t, shanghaiTime(2026, 12, 31, 23, 59, 59), got)

	got = ExpireAtDays(shanghaiTime(2026, 12, 1, 23, 59, 58), 30)
	require.Equal(t, shanghaiTime(2026, 12, 31, 23, 59, 59), got)

	// 跨月 / 跨年
	require.Equal(t, shanghaiTime(2027, 1, 31, 23, 59, 59), ExpireAtDays(shanghaiTime(2026, 12, 31, 12, 0, 0), 31))
	require.Equal(t, shanghaiTime(2027, 3, 1, 23, 59, 59), ExpireAtDays(shanghaiTime(2026, 12, 31, 12, 0, 0), 60))

	require.Zero(t, ExpireAtDays(shanghaiTime(2026, 12, 1, 0, 0, 0), 0))
	require.Zero(t, ExpireAtDays(shanghaiTime(2026, 12, 1, 0, 0, 0), -3))
}

// 七日基线：获得所在周的下一周周日。2026-01-05 是周一。
func TestExpireAtWeeks_SevenDayBaseline(t *testing.T) {
	cases := []struct {
		day  int // 2026-01 的日
		want int64
	}{
		{5, shanghaiTime(2026, 1, 18, 23, 59, 59)},  // 周一 → 13 天
		{6, shanghaiTime(2026, 1, 18, 23, 59, 59)},  // 周二 → 12
		{7, shanghaiTime(2026, 1, 18, 23, 59, 59)},  // 周三 → 11
		{8, shanghaiTime(2026, 1, 18, 23, 59, 59)},  // 周四 → 10
		{9, shanghaiTime(2026, 1, 18, 23, 59, 59)},  // 周五 → 9
		{10, shanghaiTime(2026, 1, 18, 23, 59, 59)}, // 周六 → 8
		{11, shanghaiTime(2026, 1, 18, 23, 59, 59)}, // 周日 → 7
	}
	days := []int{13, 12, 11, 10, 9, 8, 7}
	for i, c := range cases {
		now := shanghaiTime(2026, 1, c.day, 12, 0, 0)
		got := ExpireAtWeeks(now, 1)
		require.Equal(t, c.want, got, "第 %d 天", c.day)
		// 相隔天数必须与基线表一致
		require.Equal(t, int64(days[i])*86400-(12*3600)+86399, got-now, "第 %d 天的间隔天数", c.day)
	}

	// N=2 各加 7 天
	require.Equal(t, shanghaiTime(2026, 1, 25, 23, 59, 59), ExpireAtWeeks(shanghaiTime(2026, 1, 5, 12, 0, 0), 2))
	require.Equal(t, shanghaiTime(2026, 1, 25, 23, 59, 59), ExpireAtWeeks(shanghaiTime(2026, 1, 11, 12, 0, 0), 2))

	require.Zero(t, ExpireAtWeeks(shanghaiTime(2026, 1, 5, 12, 0, 0), 0))
}

func TestExpireAtDate(t *testing.T) {
	got, err := ExpireAtDate("2099-06-15")
	require.NoError(t, err)
	require.Equal(t, shanghaiTime(2099, 6, 15, 23, 59, 59), got)

	_, err = ExpireAtDate("2000-01-01")
	require.Error(t, err, "过去日期应拒绝")

	_, err = ExpireAtDate("2099/06/15")
	require.Error(t, err, "格式错误应拒绝")
}

// ---------------------------------------------------------------------------
// 过期作废
// ---------------------------------------------------------------------------

func TestPointsExpiry_ForfeitsRemainderAndWritesLedger(t *testing.T) {
	const id = 1020
	seedLotUser(t, id, 0)
	exp := shanghaiTime(2026, 1, 6, 23, 59, 59)
	now := shanghaiTime(2026, 1, 7, 0, 5, 0)

	require.NoError(t, DB.Create(&PointsLot{
		UserId: id, Source: FundSourceCheckin, Amount: 1000, ExpiresAt: exp,
		GrantedAt: exp - 86400, Status: PointsLotStatusActive,
	}).Error)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", id).Update("points_balance", 600).Error)

	forfeited, bySource, err := ExpireUserPointsLots(id, now)
	require.NoError(t, err)
	// sync 先把 400 的净消耗归属给该批次，剩 600 全部作废
	require.EqualValues(t, 600, forfeited)
	require.EqualValues(t, 0, readLotBalance(t, id))
	require.Len(t, bySource, 1)
	require.Equal(t, FundSourceCheckin, bySource[0].Source)

	var entries []FundEntry
	require.NoError(t, DB.Where("user_id = ? AND kind = ?", id, FundKindExpire).Find(&entries).Error)
	require.Len(t, entries, 1)
	require.EqualValues(t, -600, entries[0].QuotaDelta)
	require.Equal(t, FundRefPointsLot, entries[0].RefType)

	lots := listLots(t, id)
	var target PointsLot
	for _, l := range lots {
		if l.Source == FundSourceCheckin {
			target = l
		}
	}
	require.Equal(t, PointsLotStatusExpired, target.Status)
	require.EqualValues(t, 600, target.Forfeited)
}

func TestPointsExpiry_RerunIsIdempotent(t *testing.T) {
	const id = 1021
	seedLotUser(t, id, 0)
	exp := shanghaiTime(2026, 1, 6, 23, 59, 59)
	now := shanghaiTime(2026, 1, 7, 0, 5, 0)
	require.NoError(t, DB.Create(&PointsLot{
		UserId: id, Source: FundSourceCheckin, Amount: 500, ExpiresAt: exp,
		GrantedAt: exp - 86400, Status: PointsLotStatusActive,
	}).Error)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", id).Update("points_balance", 500).Error)

	first, _, err := ExpireUserPointsLots(id, now)
	require.NoError(t, err)
	require.EqualValues(t, 500, first)

	second, _, err := ExpireUserPointsLots(id, now)
	require.NoError(t, err)
	require.Zero(t, second, "重跑不得重复作废")
	require.EqualValues(t, 0, readLotBalance(t, id))

	var n int64
	require.NoError(t, DB.Model(&FundEntry{}).Where("user_id = ? AND kind = ?", id, FundKindExpire).Count(&n).Error)
	require.EqualValues(t, 1, n, "重跑不得重复记流水")
}

// 两个执行者并发对同一用户作废：只有一次生效。
// 条件更新保证「作废不依赖锁」——即使连接池让两者真正并行，也不会双倍扣减。
func TestPointsExpiryConcurrent_TwoExecutorsForfeitOnce(t *testing.T) {
	const id = 1022
	seedLotUser(t, id, 0)
	exp := shanghaiTime(2026, 1, 6, 23, 59, 59)
	now := shanghaiTime(2026, 1, 7, 0, 5, 0)
	require.NoError(t, DB.Create(&PointsLot{
		UserId: id, Source: FundSourceCheckin, Amount: 900, ExpiresAt: exp,
		GrantedAt: exp - 86400, Status: PointsLotStatusActive,
	}).Error)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", id).Update("points_balance", 900).Error)

	var wg sync.WaitGroup
	results := make([]int64, 4)
	errs := make([]error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], _, errs[i] = ExpireUserPointsLots(id, now)
		}(i)
	}
	wg.Wait()

	var total int64
	for i := 0; i < 4; i++ {
		require.NoError(t, errs[i])
		total += results[i]
	}
	require.EqualValues(t, 900, total, "四个执行者合计只能作废一次")
	require.EqualValues(t, 0, readLotBalance(t, id), "余额只能被扣一次")

	var n int64
	require.NoError(t, DB.Model(&FundEntry{}).Where("user_id = ? AND kind = ?", id, FundKindExpire).Count(&n).Error)
	require.EqualValues(t, 1, n)
}

func TestPointsExpiry_SkipsLotsThatHaveNotExpired(t *testing.T) {
	const id = 1023
	seedLotUser(t, id, 0)
	future := shanghaiTime(2026, 6, 1, 23, 59, 59)
	require.NoError(t, DB.Create(&PointsLot{
		UserId: id, Source: FundSourceCheckin, Amount: 500, ExpiresAt: future,
		GrantedAt: future - 86400, Status: PointsLotStatusActive,
	}).Error)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", id).Update("points_balance", 500).Error)

	forfeited, _, err := ExpireUserPointsLots(id, shanghaiTime(2026, 1, 7, 0, 5, 0))
	require.NoError(t, err)
	require.Zero(t, forfeited)
	require.EqualValues(t, 500, readLotBalance(t, id))
}

// 作废后退款：原批次已 expired，回补不回原批次，剩余部分进 surplus（永不过期）。
func TestPointsExpiry_RefundAfterExpireGoesToSurplus(t *testing.T) {
	const id = 1024
	seedLotUser(t, id, 0)
	exp := shanghaiTime(2026, 1, 6, 23, 59, 59)
	now := shanghaiTime(2026, 1, 7, 0, 5, 0)
	require.NoError(t, DB.Create(&PointsLot{
		UserId: id, Source: FundSourceCheckin, Amount: 500, ExpiresAt: exp,
		GrantedAt: exp - 86400, Status: PointsLotStatusActive,
	}).Error)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", id).Update("points_balance", 500).Error)

	_, _, err := ExpireUserPointsLots(id, now)
	require.NoError(t, err)

	// 退款 200（余额涨回 200，批次全是 expired/标记，无可回补对象）
	require.NoError(t, IncreaseUserPoints(id, 200, true))
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return syncPointsLotsTx(tx, id, now+60)
	}))

	var surplus *PointsLot
	for _, l := range listLots(t, id) {
		if l.Source == PointsLotSourceSurplus {
			cp := l
			surplus = &cp
		}
	}
	require.NotNil(t, surplus, "跨到期退款应进 surplus")
	require.EqualValues(t, 200, surplus.Amount)
	require.Zero(t, surplus.ExpiresAt)
	require.EqualValues(t, 200, readLotBalance(t, id))
}

// ---------------------------------------------------------------------------
// 归档清理
// ---------------------------------------------------------------------------

func TestPointsLotsCleanup_KeepsNeverExpiring(t *testing.T) {
	const id = 1030
	seedLotUser(t, id, 0)
	old := shanghaiTime(2025, 1, 1, 23, 59, 59)
	insertLot(t, PointsLot{
		UserId: id, Source: FundSourceCheckin, Amount: 100, ExpiresAt: old,
		Status: PointsLotStatusExpired, Forfeited: 100,
	})
	insertLot(t, PointsLot{
		UserId: id, Source: PointsLotSourceLegacy, Amount: 0, ExpiresAt: 0,
		Status: PointsLotStatusExhausted,
	})

	n, err := CleanupArchivedPointsLots(shanghaiTime(2026, 6, 1, 0, 0, 0), 90*86400, 100)
	require.NoError(t, err)
	require.GreaterOrEqual(t, n, 1)

	var remaining []PointsLot
	for _, l := range listLots(t, id) {
		remaining = append(remaining, l)
	}
	require.Len(t, remaining, 1)
	require.Equal(t, PointsLotSourceLegacy, remaining[0].Source, "永不过期的批次永不删除")
}

// 守卫：sync 之后不变式必须成立（合计 == 余额）。
func TestPointsLotsSync_InvariantHoldsAcrossGrantAndConsume(t *testing.T) {
	const id = 1040
	seedLotUser(t, id, 0)
	now := shanghaiTime(2026, 1, 5, 10, 0, 0)

	for i := 0; i < 3; i++ {
		_, err := GrantPoints(PointsGrant{
			UserId: id, Quota: int64(1000 * (i + 1)), Source: FundSourceCheckin,
			RefId: fmt.Sprintf("1040:g%d", i), ExpiresAt: ExpireAtDays(now, 30*(i+1)), Now: now,
			Fund: FundEntry{
				Kind: FundKindGift, Source: FundSourceCheckin, RefType: FundRefCheckin,
				RefId: fmt.Sprintf("1040:g%d", i),
			},
		})
		require.NoError(t, err)
	}
	// 热路径扣减
	ok, err := TryDecreaseUserPoints(id, 2500)
	require.NoError(t, err)
	require.True(t, ok)

	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return syncPointsLotsTx(tx, id, now+10)
	}))

	balance := readLotBalance(t, id)
	require.EqualValues(t, 3500, balance)
	require.Equal(t, balance, sumActiveRemaining(listLots(t, id)), "sync 后不变式必须成立")
}

// 并发：发放与热路径扣减交织，结束后 sync 必须让不变式成立、余额不为负。
// （真实三库并发由 S8 覆盖；这里在 SQLite 单连接下验证逻辑正确性与无死锁。）
func TestPointsLotsConcurrent_GrantsAndDecreaseKeepInvariant(t *testing.T) {
	const id = 1050
	seedLotUser(t, id, 0)
	now := shanghaiTime(2026, 1, 5, 10, 0, 0)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := GrantPoints(PointsGrant{
				UserId: id, Quota: 1000, Source: FundSourceCheckin,
				RefId: fmt.Sprintf("1050:g%d", i), ExpiresAt: ExpireAtDays(now, 10), Now: now,
				Fund: FundEntry{
					Kind: FundKindGift, Source: FundSourceCheckin, RefType: FundRefCheckin,
					RefId: fmt.Sprintf("1050:g%d", i),
				},
			})
			require.NoError(t, err)
		}(i)
	}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 5; k++ {
				_, err := TryDecreaseUserPoints(id, 100)
				require.NoError(t, err)
			}
		}()
	}
	wg.Wait()

	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return syncPointsLotsTx(tx, id, now+10)
	}))

	balance := readLotBalance(t, id)
	require.GreaterOrEqual(t, balance, int64(0), "余额不得为负")
	require.Equal(t, balance, sumActiveRemaining(listLots(t, id)), "sync 后不变式必须成立")
}
