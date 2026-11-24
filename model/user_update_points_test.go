package model

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/require"
)

// §2.6 回归：整行回写不得覆盖积分列（丢失更新）。
//
// User.Update / TransferAffQuotaToQuota 都是「拿调用方手里的结构体整行写回」，
// 若把 points_balance / points_used 一并写回，读取与写回之间的并发扣费/发放会被抹掉。
// 在积分批次方案下，这不只是丢失更新，还是一条绕过 GrantPoints 的余额写入：
// 旧值偏高会在下次归属时变成 surplus（用户凭空多分），旧值偏低会被归属成消耗。

func seedUpdatePointsUser(t *testing.T, id int, points, pointsUsed int) {
	t.Helper()
	require.NoError(t, DB.Create(&User{
		Id:            id,
		Username:      fmt.Sprintf("up_%d", id),
		Role:          1,
		Status:        1,
		Group:         "default",
		PointsBalance: points,
		PointsUsed:    pointsUsed,
	}).Error)
	t.Cleanup(func() { DB.Unscoped().Where("id = ?", id).Delete(&User{}) })
}

func readPointsPair(t *testing.T, id int) (balance, used int) {
	t.Helper()
	var row struct {
		PointsBalance int
		PointsUsed    int
	}
	require.NoError(t, DB.Model(&User{}).Where("id = ?", id).
		Select("points_balance", "points_used").Scan(&row).Error)
	return row.PointsBalance, row.PointsUsed
}

// 读取快照 → 期间发生一次发放 → 用旧快照整行写回。积分必须保持发放后的值。
func TestUserUpdateDoesNotOverwritePointsBalance(t *testing.T) {
	const id = 921
	seedUpdatePointsUser(t, id, 1000, 200)

	var stale User
	require.NoError(t, DB.Where("id = ?", id).First(&stale).Error)
	require.Equal(t, 1000, stale.PointsBalance)

	// 并发语义：读取之后、写回之前，余额被别的路径改了
	require.NoError(t, IncreaseUserPoints(id, 500, true))
	require.NoError(t, AddUserPointsUsed(id, 77))

	// 改一个无关字段再整行写回（Update 的真实用法）
	stale.Remark = "changed"
	require.NoError(t, stale.Update(false))

	balance, used := readPointsPair(t, id)
	require.Equal(t, 1500, balance, "整行回写覆盖了并发发放的积分")
	require.Equal(t, 277, used, "整行回写覆盖了并发的已用积分累加")
	require.Equal(t, "changed", mustUserRemark(t, id), "非积分字段仍应正常写回")
}

// MutateSetting 是用户设置写入的统一入口，走 Update(false)，同样不能回写积分列。
func TestMutateSettingDoesNotOverwritePointsBalance(t *testing.T) {
	const id = 922
	seedUpdatePointsUser(t, id, 700, 0)

	var stale User
	require.NoError(t, DB.Where("id = ?", id).First(&stale).Error)

	require.NoError(t, IncreaseUserPoints(id, 300, true))

	require.NoError(t, stale.MutateSetting(func(s *dto.UserSetting) { s.Language = "zh-CN" }))

	balance, _ := readPointsPair(t, id)
	require.Equal(t, 1000, balance, "MutateSetting 经 Update 整行回写覆盖了积分")
}

// TransferAffQuotaToQuota 用 tx.Save(user) 整行写回。
// 函数内部会先 First 重读整行（因此调用方手里的旧积分不会直接进库），但「重读到 Save」
// 之间没有有效行锁（gorm:query_option 在 v2 下不生效），所以 Omit 覆盖的是这个窗口。
// 这里同时钉住两件事：调用方传进来的旧积分不得落库；邀请额度转移本身仍然生效。
func TestTransferAffQuotaDoesNotOverwritePointsBalance(t *testing.T) {
	const id = 923
	seedUpdatePointsUser(t, id, 1000, 50)

	quota := int(common.QuotaPerUnit)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", id).
		Update("aff_quota", quota*2).Error)

	stale := &User{Id: id, AffQuota: quota * 2, PointsBalance: 424242, PointsUsed: 999}
	require.NoError(t, stale.TransferAffQuotaToQuota(quota))

	balance, used := readPointsPair(t, id)
	require.Equal(t, 1000, balance, "TransferAffQuotaToQuota 把调用方的旧积分写回了库")
	require.Equal(t, 50, used, "TransferAffQuotaToQuota 把调用方的旧已用积分写回了库")

	// 额度转移本身必须照常生效
	var row struct {
		AffQuota int
		Quota    int
	}
	require.NoError(t, DB.Model(&User{}).Where("id = ?", id).
		Select("aff_quota", "quota").Scan(&row).Error)
	require.Equal(t, quota, row.AffQuota)
	require.Equal(t, quota, row.Quota)
}

func mustUserRemark(t *testing.T, id int) string {
	t.Helper()
	var remark string
	require.NoError(t, DB.Model(&User{}).Where("id = ?", id).Select("remark").Scan(&remark).Error)
	return remark
}
