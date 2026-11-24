package service

import (
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

// 复用本包 TestMain（内存 SQLite、MaxOpenConns(1)、RedisEnabled=false）。

func seedPointsExpiryUser(t *testing.T, id int, balance int64) {
	t.Helper()
	require.NoError(t, model.DB.Create(&model.User{
		Id: id, Username: fmt.Sprintf("pexp_%d", id), Role: 1, Status: 1,
		Group: "default", PointsBalance: int(balance),
		// users.aff_code 有唯一索引，空串会互相撞（NULL 才允许多行）——同库多用户必须给值
		AffCode: fmt.Sprintf("pa%06d", id),
	}).Error)
	t.Cleanup(func() {
		// 流水必须一起清：points_lots 没有 AUTOINCREMENT，删行后 id 会被复用，
		// 残留的历史作废流水会与新批次的幂等键相撞。
		model.DB.Unscoped().Where("user_id = ?", id).Delete(&model.PointsLot{})
		model.DB.Unscoped().Where("user_id = ?", id).Delete(&model.FundEntry{})
		model.DB.Unscoped().Where("id = ?", id).Delete(&model.User{})
	})
}

func seedDueLot(t *testing.T, userId int, amount, expiresAt int64) {
	t.Helper()
	require.NoError(t, model.DB.Create(&model.PointsLot{
		UserId: userId, Source: model.FundSourceCheckin, RefId: fmt.Sprintf("due:%d", userId),
		Amount: amount, GrantedAt: expiresAt - 86400, ExpiresAt: expiresAt,
		Status: model.PointsLotStatusActive,
	}).Error)
}

// AC13：调度按北京时间 00:05，与主机/入参的时区无关。
func TestNextPointsExpiryDelay_UsesBeijingTime(t *testing.T) {
	utc := time.UTC
	cases := []struct {
		name string
		now  time.Time
		want time.Duration
	}{
		{
			// 2026-01-05 20:00 UTC = 2026-01-06 04:00 北京 → 下一个 00:05 是 01-07
			name: "UTC 晚间跨日",
			now:  time.Date(2026, 1, 5, 20, 0, 0, 0, utc),
			want: 20*time.Hour + 5*time.Minute,
		},
		{
			// 2026-01-05 16:00 UTC = 2026-01-06 00:00 北京 → 5 分钟后
			name: "北京刚过零点",
			now:  time.Date(2026, 1, 5, 16, 0, 0, 0, utc),
			want: 5 * time.Minute,
		},
		{
			// 2026-01-05 16:05 UTC = 2026-01-06 00:05 北京 → 已到点，推到次日
			name: "恰好 00:05",
			now:  time.Date(2026, 1, 5, 16, 5, 0, 0, utc),
			want: 24 * time.Hour,
		},
		{
			// 同一时刻换成北京时区表达，结果必须一致
			name: "北京时区等价入参",
			now:  time.Date(2026, 1, 6, 4, 0, 0, 0, shanghaiLoc),
			want: 20*time.Hour + 5*time.Minute,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, nextPointsExpiryDelay(c.now))
		})
	}
}

func TestRunPointsExpiryOnce_ForfeitsAndWritesLedgerAndLog(t *testing.T) {
	now := time.Now().Unix()
	due := now - 3600

	const idA, idB = 7101, 7102
	seedPointsExpiryUser(t, idA, 400)
	seedPointsExpiryUser(t, idB, 700)
	// A 还有 400 未花，B 只剩 100（期间花了 600）
	seedDueLot(t, idA, 400, due)
	seedDueLot(t, idB, 700, due)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", idB).
		Update("points_balance", 100).Error)

	forfeited, users, err := RunPointsExpiryOnce(now)
	require.NoError(t, err)
	require.GreaterOrEqual(t, forfeited, int64(500)) // A 400 + B 100
	require.GreaterOrEqual(t, users, 2)

	var balA, balB int64
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", idA).
		Select("points_balance").Scan(&balA).Error)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", idB).
		Select("points_balance").Scan(&balB).Error)
	require.Zero(t, balA, "到期的剩余必须归零")
	require.Zero(t, balB)

	// 作废流水与「作废额」一致，且幂等键挂在批次上
	var entries []model.FundEntry
	require.NoError(t, model.DB.Where("user_id IN ? AND kind = ?", []int{idA, idB}, model.FundKindExpire).
		Find(&entries).Error)
	require.Len(t, entries, 2)
	for _, e := range entries {
		require.Negative(t, e.QuotaDelta)
		require.Equal(t, model.FundRefPointsLot, e.RefType)
	}

	// 使用日志按来源合并成一条
	var logCount int64
	require.NoError(t, model.LOG_DB.Model(&model.Log{}).
		Where("user_id IN ? AND content LIKE ?", []int{idA, idB}, "积分过期：%").
		Count(&logCount).Error)
	require.EqualValues(t, 2, logCount)
}

// 决策 8：积分系统总开关关闭时过期任务照跑，否则关开关等于冻结过期。
func TestRunPointsExpiryOnce_RunsEvenWhenPointsDisabled(t *testing.T) {
	ps := operation_setting.GetPointsSetting()
	orig := ps.Enabled
	ps.Enabled = false
	t.Cleanup(func() { ps.Enabled = orig })

	now := time.Now().Unix()
	const id = 7110
	seedPointsExpiryUser(t, id, 300)
	seedDueLot(t, id, 300, now-60)

	forfeited, _, err := RunPointsExpiryOnce(now)
	require.NoError(t, err)
	require.GreaterOrEqual(t, forfeited, int64(300))

	var bal int64
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", id).
		Select("points_balance").Scan(&bal).Error)
	require.Zero(t, bal)
}

// 归档清理只删 expires_at > 0 的旧批次，永不过期批次（legacy 标记）必须留下。
func TestRunPointsExpiryOnce_ArchivesOldExpiredLotsOnly(t *testing.T) {
	now := time.Now().Unix()
	const id = 7120
	seedPointsExpiryUser(t, id, 0)

	old := now - 200*86400
	require.NoError(t, model.DB.Create(&model.PointsLot{
		UserId: id, Source: model.FundSourceCheckin, RefId: fmt.Sprintf("arch:%d", id),
		Amount: 100, Forfeited: 100, ExpiresAt: old,
		Status: model.PointsLotStatusExpired,
	}).Error)
	require.NoError(t, model.DB.Create(&model.PointsLot{
		UserId: id, Source: model.PointsLotSourceLegacy, RefId: fmt.Sprintf("legacy:%d", id),
		Amount: 0, ExpiresAt: 0, Status: model.PointsLotStatusExhausted,
	}).Error)

	_, _, err := RunPointsExpiryOnce(now)
	require.NoError(t, err)

	var lots []model.PointsLot
	require.NoError(t, model.DB.Where("user_id = ?", id).Find(&lots).Error)
	require.Len(t, lots, 1, "旧的已到期批次应被归档删掉")
	require.Equal(t, model.PointsLotSourceLegacy, lots[0].Source, "永不过期批次必须保留")
}

// 一轮跑完后应把全部到期批次清干净（分批循环真的在推进，不是只处理第一批）。
func TestRunPointsExpiryOnce_ProcessesMoreThanOneBatch(t *testing.T) {
	now := time.Now().Unix()
	base := 7200
	for i := 0; i < 5; i++ {
		id := base + i
		seedPointsExpiryUser(t, id, 100)
		seedDueLot(t, id, 100, now-60)
	}

	forfeited, _, err := RunPointsExpiryOnce(now)
	require.NoError(t, err)
	require.GreaterOrEqual(t, forfeited, int64(500))

	var remaining int64
	require.NoError(t, model.DB.Model(&model.PointsLot{}).
		Where("user_id >= ? AND user_id < ? AND status = ?", base, base+5, model.PointsLotStatusActive).
		Count(&remaining).Error)
	require.Zero(t, remaining, "所有到期批次都应被处理")
}

var _ = common.QuotaToPoints
