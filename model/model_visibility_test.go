package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func withVisibilityCache(t *testing.T, cache map[string]map[string]bool) {
	t.Helper()
	modelVisibilityLock.Lock()
	prev := modelVisibilityCache
	modelVisibilityCache = cache
	modelVisibilityLock.Unlock()
	t.Cleanup(func() {
		modelVisibilityLock.Lock()
		modelVisibilityCache = prev
		modelVisibilityLock.Unlock()
	})
}

// TestIsModelVisibleForGroup_DefaultAllow 未配置限制的模型对所有人可见。
//
// 这是「默认允许、显式限制」的直接体现，也是本功能能安全上线的依据：全站一条限制
// 都没配时，所有过滤点都是空操作，行为与改造前逐位相同。
func TestIsModelVisibleForGroup_DefaultAllow(t *testing.T) {
	withVisibilityCache(t, map[string]map[string]bool{})

	require.True(t, IsModelVisibleForGroup("GLM-5", "default"))
	require.True(t, IsModelVisibleForGroup("GLM-5", ""))
	require.False(t, HasModelVisibilityRestrictions())
}

func TestIsModelVisibleForGroup_Restricted(t *testing.T) {
	withVisibilityCache(t, map[string]map[string]bool{
		"secret-model": {"vip": true, "geostar": true},
	})

	t.Run("名单内可见", func(t *testing.T) {
		require.True(t, IsModelVisibleForGroup("secret-model", "vip"))
		require.True(t, IsModelVisibleForGroup("secret-model", "geostar"))
	})

	t.Run("名单外不可见", func(t *testing.T) {
		require.False(t, IsModelVisibleForGroup("secret-model", "default"))
	})

	t.Run("未登录（空档位）不可见", func(t *testing.T) {
		require.False(t, IsModelVisibleForGroup("secret-model", ""),
			"模型广场是公开页面，受限模型不该露给匿名访客")
	})

	t.Run("其他模型不受影响", func(t *testing.T) {
		require.True(t, IsModelVisibleForGroup("GLM-5", "default"))
	})
}

// TestIsModelVisibleForGroup_EmptyAllowListHidesFromAll 配了限制但一个档都没勾 =
// 谁都看不到。这是「下架但保留配置」的表达方式，不能被当成「没配置」处理。
func TestIsModelVisibleForGroup_EmptyAllowListHidesFromAll(t *testing.T) {
	withVisibilityCache(t, map[string]map[string]bool{
		"retired-model": {},
	})

	require.False(t, IsModelVisibleForGroup("retired-model", "vip"))
	require.False(t, IsModelVisibleForGroup("retired-model", "default"))
	require.True(t, HasModelVisibilityRestrictions())
}

func TestFilterModelsByVisibility(t *testing.T) {
	withVisibilityCache(t, map[string]map[string]bool{
		"secret-model": {"vip": true},
	})

	in := []string{"GLM-5", "secret-model", "Kimi-K3"}

	require.Equal(t, []string{"GLM-5", "Kimi-K3"}, FilterModelsByVisibility(in, "default"))
	require.Equal(t, []string{"GLM-5", "secret-model", "Kimi-K3"}, FilterModelsByVisibility(in, "vip"),
		"顺序必须保持——模型列表的排序是展示语义的一部分")
}

// TestFilterModelsByVisibility_NoRestrictionsReturnsSameSlice 无限制时直接返回原切片，
// 不做无谓的重建：绝大多数站点一条限制都没配，这是最热的那条路径。
func TestFilterModelsByVisibility_NoRestrictionsReturnsSameSlice(t *testing.T) {
	withVisibilityCache(t, map[string]map[string]bool{})

	in := []string{"GLM-5", "Kimi-K3"}
	require.Equal(t, in, FilterModelsByVisibility(in, "default"))
}

func TestParseVisibleGroups(t *testing.T) {
	t.Run("空串表示不限制", func(t *testing.T) {
		require.Nil(t, parseVisibleGroups(""))
		require.Nil(t, parseVisibleGroups("   "))
	})

	t.Run("逗号分隔并去空格", func(t *testing.T) {
		got := parseVisibleGroups(" vip , geostar ")
		require.Equal(t, map[string]bool{"vip": true, "geostar": true}, got)
	})

	t.Run("忽略空项但仍算已配置", func(t *testing.T) {
		got := parseVisibleGroups(",,")
		require.NotNil(t, got, "配了值就算已配置，即便解析后为空——语义是「谁都看不到」")
		require.Empty(t, got)
	})
}

// TestModelUpdateSelectIncludesVisibleGroups 钉住一个静默失效点。
//
// Model.Update() 用 Select 白名单强制更新零值字段（清空可见范围 = 放开权限，是正常
// 操作，非零字段更新做不到）。新增字段若漏进这个白名单，表现是**保存成功但没存上**：
// 接口返回 200、页面显示已保存、刷新后配置消失，没有任何错误可查。
func TestModelUpdateSelectIncludesVisibleGroups(t *testing.T) {
	require.Contains(t, modelUpdateSelectFields(), "visible_groups",
		"VisibleGroups 必须在 Update 的 Select 白名单里，否则保存会被静默忽略")
}
