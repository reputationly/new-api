package model

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 展示层与对账健康检查。复用本包 TestMain（内存 SQLite、MaxOpenConns(1)）。

func seedLotViewUser(t *testing.T, id int, balance int64) {
	t.Helper()
	require.NoError(t, DB.Create(&User{
		Id: id, Username: fmt.Sprintf("plv_%d", id), Role: 1, Status: 1,
		Group: "default", PointsBalance: int(balance),
		// users.aff_code 有唯一索引，空串会互相撞
		AffCode: fmt.Sprintf("pv%06d", id),
	}).Error)
	t.Cleanup(func() {
		DB.Exec("DELETE FROM points_lots WHERE user_id = ?", id)
		DB.Exec("DELETE FROM fund_entries WHERE user_id = ?", id)
		DB.Unscoped().Where("id = ?", id).Delete(&User{})
	})
}

// 展示接口必须复用写库路径的同一个纯函数：这里验的是「只读模拟 == 真实归属」。
func TestGetActivePointsLotViews_MatchesRealAllocation(t *testing.T) {
	const id = 3101
	now := shanghaiTime(2026, 6, 1, 0, 5, 0)
	seedLotViewUser(t, id, 260)

	expSoon := shanghaiTime(2026, 6, 20, 23, 59, 59)
	expLate := shanghaiTime(2026, 9, 1, 23, 59, 59)
	insertLot(t, PointsLot{UserId: id, Source: FundSourceCheckin, Amount: 100, ExpiresAt: expSoon})
	insertLot(t, PointsLot{UserId: id, Source: FundSourceKyc, Amount: 100, ExpiresAt: expLate})
	insertLot(t, PointsLot{UserId: id, Source: PointsLotSourceLegacy, Amount: 100, ExpiresAt: 0})
	// 内部初始化标记：amount=0，不得出现在用户视图里
	insertLot(t, PointsLot{
		UserId: id, Source: PointsLotSourceLegacy, Amount: 0, ExpiresAt: 0,
		Status: PointsLotStatusExhausted,
	})

	views, surplus, err := GetActivePointsLotViews(id, now)
	require.NoError(t, err)
	require.Zero(t, surplus)

	// 余额 260，批次合计 300 → 净消耗 40，落在最快到期的那笔上
	require.Len(t, views, 3, "0 额标记必须被过滤掉")
	require.Equal(t, expSoon, views[0].ExpiresAt, "最快到期的排第一")
	require.EqualValues(t, 60, views[0].RemainingQuota)
	require.Equal(t, expLate, views[1].ExpiresAt)
	require.EqualValues(t, 100, views[1].RemainingQuota)
	require.Zero(t, views[2].ExpiresAt, "永不过期的排最后")

	var total int64
	for _, v := range views {
		total += v.RemainingQuota
	}
	require.EqualValues(t, 260, total, "展示的剩余合计必须等于余额")
}

func TestGetActivePointsLotViews_SurplusAppearsAsVirtualRow(t *testing.T) {
	const id = 3102
	now := shanghaiTime(2026, 6, 1, 0, 5, 0)
	seedLotViewUser(t, id, 500) // 余额比批次多 200

	insertLot(t, PointsLot{UserId: id, Source: FundSourceCheckin, Amount: 300, ExpiresAt: expLateTs()})

	views, surplus, err := GetActivePointsLotViews(id, now)
	require.NoError(t, err)
	require.EqualValues(t, 200, surplus)
	require.Len(t, views, 2)
	require.Equal(t, 0, views[1].Id, "未建批次的 surplus 以 id=0 的虚拟行出现")
	require.Equal(t, PointsLotSourceSurplus, views[1].Source)
	require.EqualValues(t, 200, views[1].RemainingQuota)
	require.Zero(t, views[1].ExpiresAt)
}

func TestGetPointsExpirySummary_WindowAndTopN(t *testing.T) {
	const id = 3103
	now := shanghaiTime(2026, 6, 1, 0, 5, 0)
	// 余额与批次合计相等（700），否则只读模拟会先把净消耗摊给最快到期的那笔
	seedLotViewUser(t, id, 700)

	// 三笔 30 天内到期 + 一笔 100 天后到期（窗口外）
	days := []int{3, 5, 10}
	for i, d := range days {
		insertLot(t, PointsLot{
			UserId: id, Source: FundSourceCheckin, Amount: int64(100 * (i + 1)),
			ExpiresAt: ExpireAtDays(now, d),
		})
	}
	insertLot(t, PointsLot{
		UserId: id, Source: FundSourceCheckin, Amount: 100, ExpiresAt: ExpireAtDays(now, 100),
	})

	summary, err := GetPointsExpirySummary(id, now, 30, 3)
	require.NoError(t, err)
	require.NotNil(t, summary.Expiring)
	require.EqualValues(t, 600, summary.Expiring.Quota, "窗口内三笔合计 100+200+300")
	require.Equal(t, ExpireAtDays(now, 3), summary.Expiring.ExpiresAt, "最早到期时刻")
	require.Len(t, summary.NextExpiries, 3)
	require.Equal(t, ExpireAtDays(now, 3), summary.NextExpiries[0].ExpiresAt)
	require.EqualValues(t, 100, summary.NextExpiries[0].Quota)
	require.Equal(t, ExpireAtDays(now, 10), summary.NextExpiries[2].ExpiresAt)
}

func TestGetPointsExpirySummary_EmptyWhenNothingExpiring(t *testing.T) {
	const id = 3104
	now := shanghaiTime(2026, 6, 1, 0, 5, 0)
	seedLotViewUser(t, id, 100)
	insertLot(t, PointsLot{UserId: id, Source: FundSourceCheckin, Amount: 100, ExpiresAt: 0})

	summary, err := GetPointsExpirySummary(id, now, 30, 3)
	require.NoError(t, err)
	require.Nil(t, summary.Expiring)
	require.Empty(t, summary.NextExpiries)
}

func TestGetPointsLotHistory_ExcludesActiveAndMarker(t *testing.T) {
	const id = 3105
	now := shanghaiTime(2026, 6, 1, 0, 5, 0)
	seedLotViewUser(t, id, 0)

	insertLot(t, PointsLot{
		UserId: id, Source: FundSourceCheckin, Amount: 100, Attributed: 100,
		ExpiresAt: expLateTs(), Status: PointsLotStatusExhausted, UpdatedAt: 10,
	})
	insertLot(t, PointsLot{
		UserId: id, Source: FundSourceCheckin, Amount: 100, Forfeited: 100,
		ExpiresAt: now - 86400, Status: PointsLotStatusExpired, UpdatedAt: 20,
	})
	insertLot(t, PointsLot{
		UserId: id, Source: PointsLotSourceLegacy, Amount: 0,
		ExpiresAt: 0, Status: PointsLotStatusExhausted, UpdatedAt: 30,
	})

	views, err := GetPointsLotHistory(id, now)
	require.NoError(t, err)
	require.Len(t, views, 2, "0 额标记不进历史列表")
	// 按 updated_at 降序：expired（updated_at=20）排在 exhausted（10）前面
	require.Equal(t, PointsLotStatusExpired, views[0].Status)
	require.Equal(t, PointsLotStatusExhausted, views[1].Status)
	require.Zero(t, views[0].RemainingQuota)
}

// ---------------------------------------------------------------------------
// §十一 三项健康检查
// ---------------------------------------------------------------------------

func fundCheckItem(t *testing.T, name string) FundCheckItem {
	t.Helper()
	rep, err := CheckFundConsistency()
	require.NoError(t, err)
	require.True(t, rep.HasBaseline, "本测试需要基线才能产出校验项")
	for _, it := range rep.Items {
		if it.Name == name {
			return it
		}
	}
	t.Fatalf("校验项 %q 不存在", name)
	return FundCheckItem{}
}

func TestCheckFundConsistency_PointsHealthChecks(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.Create(&User{
		Id: 3201, Username: "fchk_3201", Role: 1, Status: 1, AffCode: "fc3201",
	}).Error)
	_, err := InitFundBaseline(1)
	require.NoError(t, err)

	// 基线后三项均为 0
	for _, name := range []string{"积分过期任务", "积分余额溢出", "积分负余额"} {
		it := fundCheckItem(t, name)
		require.Zero(t, it.Actual, "%s 正常时应为 0", name)
		require.True(t, it.OK)
	}

	now := time.Now().Unix()

	// 1) 过期任务停摆：已到期超过 24 小时仍是 active
	require.NoError(t, DB.Create(&PointsLot{
		UserId: 3201, Source: FundSourceCheckin, Amount: 100,
		ExpiresAt: now - 2*86400, Status: PointsLotStatusActive,
	}).Error)
	it := fundCheckItem(t, "积分过期任务")
	require.NotZero(t, it.Actual)
	require.False(t, it.OK)

	// 2) 余额溢出：近 24 小时新建的 surplus
	require.NoError(t, DB.Create(&PointsLot{
		UserId: 3201, Source: PointsLotSourceSurplus, Amount: 77,
		GrantedAt: now - 60, ExpiresAt: 0, Status: PointsLotStatusActive,
	}).Error)
	it = fundCheckItem(t, "积分余额溢出")
	require.EqualValues(t, 77, it.Actual)
	require.False(t, it.OK)

	// 3) 负余额
	require.NoError(t, DB.Model(&User{}).Where("id = ?", 3201).
		Update("points_balance", -5).Error)
	it = fundCheckItem(t, "积分负余额")
	require.EqualValues(t, 1, it.Actual)
	require.False(t, it.OK)

	rep, err := CheckFundConsistency()
	require.NoError(t, err)
	require.False(t, rep.AllOK, "三项任一报红都应让 AllOK 为 false")
}

func TestGetSurplusPointsLots_ListsForReview(t *testing.T) {
	const id = 3106
	seedLotViewUser(t, id, 0)
	insertLot(t, PointsLot{
		UserId: id, Source: PointsLotSourceSurplus, Amount: 123,
		GrantedAt: shanghaiTime(2026, 6, 1, 0, 5, 0), Status: PointsLotStatusActive,
	})

	views, total, err := GetSurplusPointsLots(0, 10)
	require.NoError(t, err)
	require.GreaterOrEqual(t, total, int64(1))

	var found bool
	for _, v := range views {
		if v.UserId == id {
			found = true
			require.EqualValues(t, 123, v.AmountQuota)
			require.Equal(t, fmt.Sprintf("plv_%d", id), v.Username)
		}
	}
	require.True(t, found, "溢出批次应能按用户查到")
}

func expLateTs() int64 { return shanghaiTime(2026, 12, 1, 23, 59, 59) }

// 「30 天内到期」必须按**自然日**算：到期时刻都在当天 23:59:59，第 30 天到期的批次
// 比 now+30×86400 晚几个小时。按秒数比会把它漏掉——实测表现为充值卡的临期标签
// 完全不出现（运营会以为「没有积分要过期」）。
func TestGetPointsExpirySummary_IncludesLastDayOfWindow(t *testing.T) {
	const id = 3107
	now := shanghaiTime(2026, 6, 1, 0, 5, 0)
	seedLotViewUser(t, id, 300)

	// 第 30 天到期：必须计入
	insertLot(t, PointsLot{
		UserId: id, Source: FundSourceCheckin, Amount: 100,
		ExpiresAt: ExpireAtDays(now, 30),
	})
	// 第 31 天到期：必须排除
	insertLot(t, PointsLot{
		UserId: id, Source: FundSourceCheckin, Amount: 200,
		ExpiresAt: ExpireAtDays(now, 31),
	})

	summary, err := GetPointsExpirySummary(id, now, 30, 3)
	require.NoError(t, err)
	require.NotNil(t, summary.Expiring, "第 30 天到期的批次必须进窗口")
	require.EqualValues(t, 100, summary.Expiring.Quota, "第 31 天到期的不应计入")
	require.Equal(t, ExpireAtDays(now, 30), summary.Expiring.ExpiresAt)
}
