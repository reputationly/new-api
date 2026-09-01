package operation_setting

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 积分渠道白名单：分组合并后，只靠分组白名单已经分不出「这次调用花的是谁的钱」。
// 渠道这层管的是「积分能买什么」，与分组白名单是 AND 关系。

func withPoints(t *testing.T, mutate func(ps *PointsSetting)) {
	t.Helper()
	backup := pointsSetting
	t.Cleanup(func() { pointsSetting = backup })
	mutate(&pointsSetting)
}

// 最要害的一条：这个字段是后加的，空值必须等价于「没有这层过滤」。
// 写成「空 = 全部拒绝」的话，升级那一刻所有持有积分的用户会集体失效。
func TestIsPointsEnabledForChannel_EmptyMeansUnrestricted(t *testing.T) {
	withPoints(t, func(ps *PointsSetting) {
		ps.EnabledChannels = []int{}
	})
	require.True(t, IsPointsEnabledForChannel(1))
	require.True(t, IsPointsEnabledForChannel(9))
	require.True(t, IsPointsEnabledForChannel(0))
}

func TestIsPointsEnabledForChannel_NilAlsoUnrestricted(t *testing.T) {
	withPoints(t, func(ps *PointsSetting) {
		ps.EnabledChannels = nil
	})
	require.True(t, IsPointsEnabledForChannel(9))
}

func TestIsPointsEnabledForChannel_WhitelistFiltersOthers(t *testing.T) {
	withPoints(t, func(ps *PointsSetting) {
		ps.EnabledChannels = []int{1, 18} // 两个自建 gpustack
	})
	require.True(t, IsPointsEnabledForChannel(1), "自建渠道应允许积分抵扣")
	require.True(t, IsPointsEnabledForChannel(18))
	require.False(t, IsPointsEnabledForChannel(9), "外采渠道必须拒绝——这正是本功能的目的")
	require.False(t, IsPointsEnabledForChannel(14))
}

// 渠道白名单不接管总开关：总开关关掉时积分整体停用，与渠道无关。
// 这条约束在 IsPointsEnabledForGroup 里，此处确认两者职责没有串。
func TestIsPointsEnabledForChannel_IgnoresMasterSwitch(t *testing.T) {
	withPoints(t, func(ps *PointsSetting) {
		ps.Enabled = false
		ps.EnabledChannels = []int{1}
	})
	require.True(t, IsPointsEnabledForChannel(1),
		"渠道判定只回答「这个渠道允不允许」，总开关由 IsPointsEnabledForGroup 把守")
	require.False(t, IsPointsEnabledForGroup("default"),
		"总开关关闭时分组判定必须为 false，混扣判定是两者 AND")
}

// 两层 AND 的完整矩阵：任一层不过都不该走混扣
func TestPointsGate_GroupAndChannelAreBothRequired(t *testing.T) {
	withPoints(t, func(ps *PointsSetting) {
		ps.Enabled = true
		ps.EnabledGroups = []string{"default"}
		ps.EnabledChannels = []int{1}
	})

	cases := []struct {
		group   string
		channel int
		want    bool
		desc    string
	}{
		{"default", 1, true, "分组过 + 渠道过"},
		{"default", 9, false, "分组过但渠道是外采"},
		{"premium", 1, false, "渠道过但分组不在白名单"},
		{"premium", 9, false, "两层都不过"},
	}
	for _, c := range cases {
		got := IsPointsEnabledForGroup(c.group) && IsPointsEnabledForChannel(c.channel)
		require.Equal(t, c.want, got, c.desc)
	}
}
