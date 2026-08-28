package setting

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func withDisabledGroups(t *testing.T, jsonStr string) {
	t.Helper()
	require.NoError(t, UpdateGroupEnabledByJSONString(jsonStr))
	t.Cleanup(func() { _ = UpdateGroupEnabledByJSONString("") })
}

// TestIsGroupDisabled_DefaultEnabled 未配置时全部启用——这条保证本功能可以先上线
// 后配置，空配置下行为与改造前逐位相同。
func TestIsGroupDisabled_DefaultEnabled(t *testing.T) {
	withDisabledGroups(t, "")

	require.False(t, IsGroupDisabled("default"))
	require.False(t, IsGroupDisabled("free"))
	require.False(t, IsGroupDisabled(""))
}

func TestIsGroupDisabled_Configured(t *testing.T) {
	withDisabledGroups(t, `["free","bailian"]`)

	require.True(t, IsGroupDisabled("free"))
	require.True(t, IsGroupDisabled("bailian"))
	require.False(t, IsGroupDisabled("default"), "未列出的分组必须保持启用")
}

// TestUpdateGroupEnabled_ClearsPrevious 保存是整体替换，不是增量累加。
//
// 增量语义下「重新启用一个分组」就没法表达了——运营在页面上取消停用、保存，
// 结果它还在停用列表里。
func TestUpdateGroupEnabled_ClearsPrevious(t *testing.T) {
	withDisabledGroups(t, `["free","bailian"]`)
	require.True(t, IsGroupDisabled("free"))

	require.NoError(t, UpdateGroupEnabledByJSONString(`["bailian"]`))
	require.False(t, IsGroupDisabled("free"), "从列表里移除即为重新启用")
	require.True(t, IsGroupDisabled("bailian"))
}

func TestUpdateGroupEnabled_Malformed(t *testing.T) {
	withDisabledGroups(t, `["free"]`)

	t.Run("空串视为全部启用", func(t *testing.T) {
		require.NoError(t, UpdateGroupEnabledByJSONString(""))
		require.False(t, IsGroupDisabled("free"))
	})

	t.Run("非法 JSON 返回错误", func(t *testing.T) {
		require.Error(t, UpdateGroupEnabledByJSONString(`[`))
	})

	t.Run("忽略空白项", func(t *testing.T) {
		require.NoError(t, UpdateGroupEnabledByJSONString(`["free","  ",""]`))
		require.True(t, IsGroupDisabled("free"))
		require.False(t, IsGroupDisabled(""))
	})
}

// TestGroupEnabled_RoundTrip 序列化能被自己解析回来——option 的读写走的就是这条链。
func TestGroupEnabled_RoundTrip(t *testing.T) {
	withDisabledGroups(t, `["free"]`)

	dumped := GroupEnabled2JSONString()
	require.NoError(t, UpdateGroupEnabledByJSONString(dumped))
	require.True(t, IsGroupDisabled("free"))
}
