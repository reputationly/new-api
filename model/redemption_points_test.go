package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

// Redeem 按奖励类型分支：points 码充 points_balance、quota 不动；历史码（RewardType 空）
// 按额度充 quota；返回值需带 rewardType 供接口层/前端区分（codex review P3）。
// 积分码兑换受总开关约束（§8.5 禁兑），测试需显式开启并还原。

func enablePointsSetting(t *testing.T) {
	t.Helper()
	ps := operation_setting.GetPointsSetting()
	ps.Enabled = true
	t.Cleanup(func() { ps.Enabled = false })
}

func seedRedemption(t *testing.T, key, rewardType string, quota int) {
	t.Helper()
	require.NoError(t, DB.Create(&Redemption{
		UserId:     1,
		Key:        key,
		Status:     common.RedemptionCodeStatusEnabled,
		Name:       "test",
		Quota:      quota,
		RewardType: rewardType,
	}).Error)
}

func redeemBalances(t *testing.T, id int) (points, quota int) {
	t.Helper()
	var u User
	require.NoError(t, DB.Select("points_balance", "quota").Where("id = ?", id).First(&u).Error)
	return u.PointsBalance, u.Quota
}

func TestRedeem_PointsCode(t *testing.T) {
	truncateTables(t)
	enablePointsSetting(t)
	require.NoError(t, DB.Create(&User{Id: 301, Username: "rd_301", Role: 1, Status: 1}).Error)
	seedRedemption(t, "pointskey000000000000000000000001", RedemptionRewardPoints, 6849)

	quota, rewardType, err := Redeem("pointskey000000000000000000000001", 301)
	require.NoError(t, err)
	require.Equal(t, 6849, quota, "data 维持面值 quota unit")
	require.Equal(t, RedemptionRewardPoints, rewardType)

	p, q := redeemBalances(t, 301)
	require.Equal(t, 6849, p, "积分码充入 points_balance")
	require.Equal(t, 0, q, "钱包 quota 不应被动用")
}

func TestRedeem_QuotaCode_LegacyEmptyType(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.Create(&User{Id: 302, Username: "rd_302", Role: 1, Status: 1}).Error)
	// 历史码 RewardType 为空 → 按额度兑换（向后兼容）
	seedRedemption(t, "legacykey00000000000000000000002", "", 5000)

	quota, rewardType, err := Redeem("legacykey00000000000000000000002", 302)
	require.NoError(t, err)
	require.Equal(t, 5000, quota)
	require.NotEqual(t, RedemptionRewardPoints, rewardType)

	p, q := redeemBalances(t, 302)
	require.Equal(t, 0, p)
	require.Equal(t, 5000, q)
}

func TestRedeem_UsedCodeRejected(t *testing.T) {
	truncateTables(t)
	enablePointsSetting(t)
	require.NoError(t, DB.Create(&User{Id: 303, Username: "rd_303", Role: 1, Status: 1}).Error)
	seedRedemption(t, "usedkey0000000000000000000000003", RedemptionRewardPoints, 1000)

	_, _, err := Redeem("usedkey0000000000000000000000003", 303)
	require.NoError(t, err)

	// 二次兑换同一码必须失败，余额不再变化
	_, _, err = Redeem("usedkey0000000000000000000000003", 303)
	require.Error(t, err)
	p, _ := redeemBalances(t, 303)
	require.Equal(t, 1000, p)
}

// 总开关关闭禁兑积分码（§8.5）：报错、余额不动、码保持未使用；重开开关后同一码可兑。
func TestRedeem_PointsCodeBlockedWhenDisabled(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.Create(&User{Id: 304, Username: "rd_304", Role: 1, Status: 1}).Error)
	seedRedemption(t, "gatedkey000000000000000000000004", RedemptionRewardPoints, 2000)

	// 默认 Enabled=false → 禁兑
	_, _, err := Redeem("gatedkey000000000000000000000004", 304)
	require.Error(t, err)
	p, q := redeemBalances(t, 304)
	require.Equal(t, 0, p, "关闭态不得充入积分")
	require.Equal(t, 0, q)
	var r Redemption
	require.NoError(t, DB.Where("`key` = ?", "gatedkey000000000000000000000004").First(&r).Error)
	require.Equal(t, common.RedemptionCodeStatusEnabled, r.Status, "码必须保持未使用")

	// 额度码不受总开关影响
	seedRedemption(t, "quotakey000000000000000000000005", RedemptionRewardQuota, 3000)
	_, _, err = Redeem("quotakey000000000000000000000005", 304)
	require.NoError(t, err)

	// 重开开关后原积分码可正常兑换
	enablePointsSetting(t)
	quota, rewardType, err := Redeem("gatedkey000000000000000000000004", 304)
	require.NoError(t, err)
	require.Equal(t, 2000, quota)
	require.Equal(t, RedemptionRewardPoints, rewardType)
	p, _ = redeemBalances(t, 304)
	require.Equal(t, 2000, p)
}

// 签到积分模式受总开关约束（§11 停止发放）：关闭时报错不发放；开启时正常发积分。
func TestUserCheckin_PointsModeGatedByPointsSwitch(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.Create(&User{Id: 305, Username: "rd_305", Role: 1, Status: 1}).Error)

	cs := operation_setting.GetCheckinSetting()
	origEnabled, origType, origMin, origMax := cs.Enabled, cs.RewardType, cs.MinPoints, cs.MaxPoints
	cs.Enabled, cs.RewardType, cs.MinPoints, cs.MaxPoints = true, CheckinRewardPoints, 10, 10
	t.Cleanup(func() {
		cs.Enabled, cs.RewardType, cs.MinPoints, cs.MaxPoints = origEnabled, origType, origMin, origMax
	})

	// 积分总开关关闭 → 签到被拒，不发放、不记签到
	_, err := UserCheckin(305)
	require.Error(t, err)
	p, _ := redeemBalances(t, 305)
	require.Equal(t, 0, p)
	checked, err := HasCheckedInToday(305)
	require.NoError(t, err)
	require.False(t, checked, "被拒的签到不得占用当日签到记录")

	// 开启后正常发放：10 积分 = ceil(10*684.93...) = 6850 quota unit
	ps := operation_setting.GetPointsSetting()
	ps.Enabled = true
	ps.RequireKyc = false // 本用例只验开关门，绕开实名门
	t.Cleanup(func() { ps.Enabled = false; ps.RequireKyc = true })

	checkin, err := UserCheckin(305)
	require.NoError(t, err)
	require.Equal(t, CheckinRewardPoints, checkin.RewardType)
	require.Equal(t, 6850, checkin.QuotaAwarded)
	p, _ = redeemBalances(t, 305)
	require.Equal(t, 6850, p, "积分模式签到应充入 points_balance")
}

// 积分模式零奖励拒绝（codex 第八轮）：min/max_points 默认 0 时切积分模式，
// 零奖励签到不得写记录吞掉当日机会——报错、不发放、当天仍可签。
func TestUserCheckin_ZeroPointsRewardRejected(t *testing.T) {
	truncateTables(t)
	enablePointsSetting(t)
	require.NoError(t, DB.Create(&User{Id: 306, Username: "rd_306", Role: 1, Status: 1}).Error)

	cs := operation_setting.GetCheckinSetting()
	origEnabled, origType, origMin, origMax := cs.Enabled, cs.RewardType, cs.MinPoints, cs.MaxPoints
	cs.Enabled, cs.RewardType, cs.MinPoints, cs.MaxPoints = true, CheckinRewardPoints, 0, 0
	t.Cleanup(func() {
		cs.Enabled, cs.RewardType, cs.MinPoints, cs.MaxPoints = origEnabled, origType, origMin, origMax
	})
	ps := operation_setting.GetPointsSetting()
	ps.RequireKyc = false
	t.Cleanup(func() { ps.RequireKyc = true })

	_, err := UserCheckin(306)
	require.Error(t, err)
	p, _ := redeemBalances(t, 306)
	require.Equal(t, 0, p)
	checked, err := HasCheckedInToday(306)
	require.NoError(t, err)
	require.False(t, checked, "零奖励签到不得占用当日签到记录")

	// 管理员修好配置后，当天仍可正常签到
	cs.MinPoints, cs.MaxPoints = 5, 5
	checkin, err := UserCheckin(306)
	require.NoError(t, err)
	require.Equal(t, CheckinRewardPoints, checkin.RewardType)
	p, _ = redeemBalances(t, 306)
	require.Equal(t, checkin.QuotaAwarded, p)
}
