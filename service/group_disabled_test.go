package service

import (
	"testing"

	"github.com/QuantumNous/new-api/setting"
	"github.com/stretchr/testify/require"
)

// 停用分组对用户不可见、不可用（设计 §10.8）。
//
// 管理端读的是 GroupRatio、不走这两个函数，所以管理员仍能看到并重新启用它们——
// 这正是「停用」区别于「删除」的地方：配置留着，随时能开回来。

func withGroupsForDisableTest(t *testing.T, usable, disabled, autoGroups string) {
	t.Helper()
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(usable))
	require.NoError(t, setting.UpdateGroupEnabledByJSONString(disabled))
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(autoGroups))
	t.Cleanup(func() {
		_ = setting.UpdateUserUsableGroupsByJSONString(`{"default":"默认分组","vip":"vip分组"}`)
		_ = setting.UpdateGroupEnabledByJSONString("")
		_ = setting.UpdateAutoGroupsByJsonString(`["default"]`)
	})
}

func TestGetUserUsableGroups_ExcludesDisabled(t *testing.T) {
	withGroupsForDisableTest(t,
		`{"default":"默认","free":"体验","premium":"高级"}`,
		`["free"]`,
		`["default"]`)

	got := GetUserUsableGroups("default")

	require.Contains(t, got, "default")
	require.Contains(t, got, "premium")
	require.NotContains(t, got, "free", "停用的分组不该出现在用户可用列表里")
}

// TestGetUserAutoGroup_SkipsDisabled auto 池不得选中停用分组——否则活动结束、
// 分组停用之后，auto 令牌仍会把流量打到那批渠道上。
func TestGetUserAutoGroup_SkipsDisabled(t *testing.T) {
	withGroupsForDisableTest(t,
		`{"default":"默认","free":"体验"}`,
		`["free"]`,
		`["default","free"]`)

	got := GetUserAutoGroup("default")

	require.Contains(t, got, "default")
	require.NotContains(t, got, "free")
}

// TestGetUserUsableGroups_NoDisabledConfigUnchanged 未配置停用时行为逐位不变——
// 本功能可以先上线、后配置。
func TestGetUserUsableGroups_NoDisabledConfigUnchanged(t *testing.T) {
	withGroupsForDisableTest(t,
		`{"default":"默认","free":"体验"}`, ``, `["default"]`)

	got := GetUserUsableGroups("default")
	require.Contains(t, got, "default")
	require.Contains(t, got, "free")
}
