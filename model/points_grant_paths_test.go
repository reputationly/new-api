package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

// AC5：注册在 SetMaxOpenConns(1)（本包 TestMain 的配置）下不阻塞，
// 且侧边栏初始化、日志、邀请计数这些「提交后」步骤仍然执行。
func TestInsert_RegisterUnderSingleConnKeepsPostCommitSteps(t *testing.T) {
	truncateTables(t)

	origQuota := common.QuotaForNewUser
	common.QuotaForNewUser = 68493
	t.Cleanup(func() { common.QuotaForNewUser = origQuota })

	ps := operation_setting.GetPointsSetting()
	origEnabled, origNew := ps.Enabled, ps.NewUserPoints
	ps.Enabled, ps.NewUserPoints = true, 10
	t.Cleanup(func() { ps.Enabled, ps.NewUserPoints = origEnabled, origNew })

	inviter := &User{Username: "inviter_ac5", Password: "pass12345", Role: 1, Status: 1}
	require.NoError(t, inviter.Insert(0))

	u := &User{Username: "invitee_ac5", Password: "pass12345", DisplayName: "t"}
	require.NoError(t, u.Insert(inviter.Id))

	// 邀请计数（提交后执行）
	var inviterAfter User
	require.NoError(t, DB.Where("id = ?", inviter.Id).First(&inviterAfter).Error)
	require.Equal(t, 1, inviterAfter.AffCount)

	// 侧边栏初始化（走 createdUser.Update，本改动后必须在事务外，否则单连接下自锁）
	var persisted User
	require.NoError(t, DB.Where("id = ?", u.Id).First(&persisted).Error)
	require.NotEmpty(t, persisted.Setting)

	// 日志（LOG_DB 可能是独立库，提交后写）
	var logCount int64
	require.NoError(t, LOG_DB.Model(&Log{}).Where("user_id = ?", u.Id).Count(&logCount).Error)
	require.Greater(t, logCount, int64(0))

	// 批次与余额一致
	require.EqualValues(t, 68493+common.PointsToQuota(10), persisted.PointsBalance)
	require.EqualValues(t, persisted.PointsBalance, sumActiveRemaining(listLots(t, u.Id)))
}
