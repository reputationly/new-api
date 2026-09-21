package model

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

// 签到发放层的两道对称守卫（UserCheckin）：
//   - 配置 points + 积分系统关 → 拒绝（既有行为，§11）
//   - 配置 quota  + 积分系统开 → 拒绝（本次新增）
//
// 第二道守的是「赠送不得进现金池」：签到发 User.Quota 会让资金对账的「真实入账」
// 口径虚高（docs/revenue-reconciliation-design.md §6.1）。配置层已在
// controller/option.go 拦住「改回 quota」，这里覆盖存量配置与绕过 API 改库的情况。
//
// 两道门都选择拒绝而非静默降级——宁可暴露误配置，也不要在管理员没意识到的情况下
// 发出真金白银的 quota。

// setCheckinReward 配置签到奖励模式并在用例结束后还原（CheckinSetting 是全局单例指针）。
func setCheckinReward(t *testing.T, rewardType string, minQuota, maxQuota, minPoints, maxPoints int) {
	t.Helper()
	cs := operation_setting.GetCheckinSetting()
	prev := *cs
	cs.Enabled = true
	cs.RewardType = rewardType
	cs.MinQuota, cs.MaxQuota = minQuota, maxQuota
	cs.MinPoints, cs.MaxPoints = minPoints, maxPoints
	t.Cleanup(func() { *cs = prev })
}

// 余额读取复用 redemption_points_test.go 的 redeemBalances（同 package）。

// 积分系统已启用时，额度模式的签到必须被拒绝，且不得吞掉当日签到机会
// （不写 checkins 行，管理员改好配置后用户当天仍可签）。
func TestUserCheckin_RejectsQuotaModeWhenPointsEnabled(t *testing.T) {
	truncateTables(t)
	enablePointsSetting(t)
	setCheckinReward(t, CheckinRewardQuota, 1000, 10000, 0, 0)
	require.NoError(t, DB.Create(&User{Id: 401, Username: "ck_401", Role: 1, Status: 1}).Error)

	_, err := UserCheckin(401)
	require.Error(t, err, "积分系统启用时额度模式签到必须被拒绝")

	p, q := redeemBalances(t, 401)
	require.Equal(t, 0, q, "被拒绝时不得发放任何 quota")
	require.Equal(t, 0, p, "被拒绝时也不应发积分")

	checked, err := HasCheckedInToday(401)
	require.NoError(t, err)
	require.False(t, checked, "拒绝不应写签到记录，否则白吞当日签到机会")
}

// 守卫只针对额度模式：积分模式在积分系统启用时照常发放，不被误伤。
func TestUserCheckin_PointsModeUnaffected(t *testing.T) {
	truncateTables(t)
	enablePointsSetting(t)
	setCheckinReward(t, CheckinRewardPoints, 0, 0, 5, 5)
	// 本用例只验守卫不误伤积分模式，绕开实名门（与既有 checkin 用例同做法）
	ps := operation_setting.GetPointsSetting()
	prevRequireKyc := ps.RequireKyc
	ps.RequireKyc = false
	t.Cleanup(func() { ps.RequireKyc = prevRequireKyc })
	require.NoError(t, DB.Create(&User{Id: 402, Username: "ck_402", Role: 1, Status: 1}).Error)

	_, err := UserCheckin(402)
	require.NoError(t, err)

	p, q := redeemBalances(t, 402)
	require.Greater(t, p, 0, "积分模式应发到 points_balance")
	require.Equal(t, 0, q, "积分模式不得动用钱包 quota")
}

// 积分系统未启用的部署不受影响：整个赠送体系不存在，额度模式照常发放，
// 否则签到功能会无奖励可发。这条是向后兼容的回归保护。
func TestUserCheckin_QuotaModeStillWorksWhenPointsDisabled(t *testing.T) {
	truncateTables(t)
	ps := operation_setting.GetPointsSetting()
	prevEnabled := ps.Enabled
	ps.Enabled = false
	t.Cleanup(func() { ps.Enabled = prevEnabled })
	setCheckinReward(t, CheckinRewardQuota, 1000, 1000, 0, 0)
	require.NoError(t, DB.Create(&User{Id: 403, Username: "ck_403", Role: 1, Status: 1}).Error)

	_, err := UserCheckin(403)
	require.NoError(t, err)

	p, q := redeemBalances(t, 403)
	require.Equal(t, 1000, q, "积分未启用时额度模式应照常发放")
	require.Equal(t, 0, p)
}
