package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/stretchr/testify/require"
)

// 渠道积分奖励：按邀请人覆盖新用户注册赠分。
//
// 覆盖语义是**彻底覆盖**，包括覆盖成 0（该渠道不送）——这一条最容易在实现里被写成
// 「0 就回落默认值」，那样运营配了 0 却照发默认积分，且日志上看不出配置没生效。

func withPointsSetting(t *testing.T, mutate func(ps *operation_setting.PointsSetting)) {
	t.Helper()
	ps := operation_setting.GetPointsSetting()
	backup := *ps
	t.Cleanup(func() { *ps = backup })
	mutate(ps)
}

func TestNewUserPointsGrant_NoInviter_UsesDefault(t *testing.T) {
	withPointsSetting(t, func(ps *operation_setting.PointsSetting) {
		ps.Enabled = true
		ps.NewUserPoints = 100
		ps.ChannelRewards = []operation_setting.ChannelPointsReward{
			{InviterId: 7, Points: 500, Enabled: true, Remark: "渠道A"},
		}
	})

	points, quota, channel := NewUserPointsGrant(0)
	require.Equal(t, 100, points)
	require.Greater(t, quota, 0)
	require.Empty(t, channel, "无邀请人不该带渠道标记")
}

func TestNewUserPointsGrant_ChannelOverrides(t *testing.T) {
	withPointsSetting(t, func(ps *operation_setting.PointsSetting) {
		ps.Enabled = true
		ps.NewUserPoints = 100
		ps.ChannelRewards = []operation_setting.ChannelPointsReward{
			{InviterId: 7, Points: 500, Enabled: true, Remark: "渠道A"},
		}
	})

	points, _, channel := NewUserPointsGrant(7)
	require.Equal(t, 500, points)
	require.Equal(t, "渠道A", channel)
}

func TestNewUserPointsGrant_UnmatchedInviterUsesDefault(t *testing.T) {
	withPointsSetting(t, func(ps *operation_setting.PointsSetting) {
		ps.Enabled = true
		ps.NewUserPoints = 100
		ps.ChannelRewards = []operation_setting.ChannelPointsReward{
			{InviterId: 7, Points: 500, Enabled: true},
		}
	})

	points, _, channel := NewUserPointsGrant(8)
	require.Equal(t, 100, points, "不在配置里的邀请人走默认值")
	require.Empty(t, channel)
}

// 决策 ①：points=0 是「该渠道不送」，不是「回落默认值」
func TestNewUserPointsGrant_ChannelZeroMeansNoGrant(t *testing.T) {
	withPointsSetting(t, func(ps *operation_setting.PointsSetting) {
		ps.Enabled = true
		ps.NewUserPoints = 100
		ps.ChannelRewards = []operation_setting.ChannelPointsReward{
			{InviterId: 7, Points: 0, Enabled: true, Remark: "只拉人不送分"},
		}
	})

	points, quota, channel := NewUserPointsGrant(7)
	require.Zero(t, points, "覆盖成 0 必须是不送，而不是回落到 NewUserPoints")
	require.Zero(t, quota)
	require.Empty(t, channel)
}

func TestNewUserPointsGrant_DisabledRuleFallsBack(t *testing.T) {
	withPointsSetting(t, func(ps *operation_setting.PointsSetting) {
		ps.Enabled = true
		ps.NewUserPoints = 100
		ps.ChannelRewards = []operation_setting.ChannelPointsReward{
			{InviterId: 7, Points: 500, Enabled: false, Remark: "已结束"},
		}
	})

	points, _, channel := NewUserPointsGrant(7)
	require.Equal(t, 100, points, "停用的规则等同未配置，完全退回默认行为")
	require.Empty(t, channel)
}

// 积分总开关关掉时，渠道奖励也必须一起停——否则关了总开关还在发分
func TestNewUserPointsGrant_MasterSwitchOffBlocksChannel(t *testing.T) {
	withPointsSetting(t, func(ps *operation_setting.PointsSetting) {
		ps.Enabled = false
		ps.NewUserPoints = 100
		ps.ChannelRewards = []operation_setting.ChannelPointsReward{
			{InviterId: 7, Points: 500, Enabled: true},
		}
	})

	points, quota, _ := NewUserPointsGrant(7)
	require.Zero(t, points)
	require.Zero(t, quota)
}

// 备注为空时回落到用户名，日志里不能出现「（渠道：）」这种空括号
func TestNewUserPointsGrant_ChannelLabelFallsBackToUsername(t *testing.T) {
	withPointsSetting(t, func(ps *operation_setting.PointsSetting) {
		ps.Enabled = true
		ps.NewUserPoints = 100
		ps.ChannelRewards = []operation_setting.ChannelPointsReward{
			{InviterId: 7, Points: 500, Enabled: true, Username: "reseller_a"},
		}
	})

	_, _, channel := NewUserPointsGrant(7)
	require.Equal(t, "reseller_a", channel)
}

func TestNewUserPointsLog(t *testing.T) {
	require.Equal(t, "新用户注册赠送 100 积分", newUserPointsLog(100, ""))
	require.Equal(t, "新用户注册赠送 500 积分（渠道：渠道A）", newUserPointsLog(500, "渠道A"))
}

func TestCheckChannelPointsRewards(t *testing.T) {
	// 同一邀请人两条规则：生效哪条取决于数组顺序，运营无法预期，必须在写入时拒绝
	err := operation_setting.CheckChannelPointsRewards(
		`[{"inviter_id":7,"username":"a","points":500,"enabled":true},
		  {"inviter_id":7,"username":"a","points":300,"enabled":true}]`)
	require.Error(t, err)
	require.Contains(t, err.Error(), "只能配一条")

	require.Error(t, operation_setting.CheckChannelPointsRewards(
		`[{"inviter_id":0,"points":500}]`), "未选择用户的规则应被拒绝")

	require.Error(t, operation_setting.CheckChannelPointsRewards(
		`[{"inviter_id":7,"points":-1}]`), "负积分应被拒绝")

	require.NoError(t, operation_setting.CheckChannelPointsRewards(""))
	require.NoError(t, operation_setting.CheckChannelPointsRewards(
		`[{"inviter_id":7,"points":500,"enabled":true},
		  {"inviter_id":8,"points":0,"enabled":true}]`))
}

// aff_code 必须能穿过后端的 JSON 往返：配置页靠它拼邀请链接，字段没声明的话
// 只要有一次「反序列化→再序列化」就会被抹掉，表现是邀请链接列集体变空。
func TestChannelPointsReward_AffCodeSurvivesRoundTrip(t *testing.T) {
	raw := `[{"inviter_id":7,"username":"a","points":500,"enabled":true,"aff_code":"Xy9z"}]`

	var rules []operation_setting.ChannelPointsReward
	require.NoError(t, common.Unmarshal([]byte(raw), &rules))
	require.Len(t, rules, 1)
	require.Equal(t, "Xy9z", rules[0].AffCode)

	out, err := common.Marshal(rules)
	require.NoError(t, err)
	require.Contains(t, string(out), "Xy9z")
}
