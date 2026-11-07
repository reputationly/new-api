package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// withAliasCache 直接注入别名缓存。
//
// 与 model_visibility_test.go 的 withVisibilityCache 同构：判定函数的语义（含 fail-open）
// 才是这些测试要锁的东西，不必为每个用例建库。
func withAliasCache(t *testing.T, hidden map[string]struct{}, internalToPublic map[string]string) {
	t.Helper()
	modelAliasLock.Lock()
	prevHidden := modelAliasHidden
	prevMapping := modelAliasInternalToPublic
	modelAliasHidden = hidden
	modelAliasInternalToPublic = internalToPublic
	modelAliasLock.Unlock()
	t.Cleanup(func() {
		modelAliasLock.Lock()
		modelAliasHidden = prevHidden
		modelAliasInternalToPublic = prevMapping
		modelAliasLock.Unlock()
	})
}

func TestParseModelAlias(t *testing.T) {
	t.Run("正常解析并 trim", func(t *testing.T) {
		parsed, err := ParseModelAlias(`{" my-gpt ":" gpt-4o "}`)
		require.NoError(t, err)
		require.Equal(t, map[string]string{"my-gpt": "gpt-4o"}, parsed)
	})

	// 自映射（左=右）对链式求值没有意义，留着只会让每次请求多绕一圈。
	t.Run("丢弃自映射与空值", func(t *testing.T) {
		parsed, err := ParseModelAlias(`{"a":"a","b":"  ","c":"upstream"}`)
		require.NoError(t, err)
		require.Equal(t, map[string]string{"c": "upstream"}, parsed)
	})

	t.Run("空配置返回 nil", func(t *testing.T) {
		parsed, err := ParseModelAlias("")
		require.NoError(t, err)
		require.Nil(t, parsed)

		parsed, err = ParseModelAlias("{}")
		require.NoError(t, err)
		require.Nil(t, parsed)
	})

	// 配置坏了不能猜：请求路径上要像 model_mapping 那样直接报错。
	t.Run("非法 JSON 返回错误", func(t *testing.T) {
		_, err := ParseModelAlias(`{"a":`)
		require.Error(t, err)
	})
}

func TestHiddenModelNameChecks(t *testing.T) {
	withAliasCache(t,
		map[string]struct{}{"gpt-4o": {}},
		map[string]string{"gpt-4o": "my-gpt"},
	)

	require.True(t, IsHiddenModelName("gpt-4o"))
	require.False(t, IsHiddenModelName("my-gpt"))
	require.False(t, IsHiddenModelName(""))
	require.True(t, HasHiddenModelNames())

	require.Equal(t, []string{"my-gpt", "other"},
		FilterHiddenModelNames([]string{"my-gpt", "gpt-4o", "other"}))

	publicName, ok := ResolveHiddenModelName("gpt-4o")
	require.True(t, ok)
	require.Equal(t, "my-gpt", publicName)

	// 没有开"内部名仍可调用"的内部名不在映射表里：调用方应按"模型不存在"处理。
	_, ok = ResolveHiddenModelName("my-gpt")
	require.False(t, ok)
}

// 一条别名都没配时所有过滤函数都必须是空操作 —— 这是本功能能安全上线的依据：
// 未使用该功能的站点，行为与改造前逐位相同。
func TestNoAliasIsNoop(t *testing.T) {
	withAliasCache(t, map[string]struct{}{}, map[string]string{})

	models := []string{"gpt-4o", "claude"}
	require.Equal(t, models, FilterHiddenModelNames(models))
	require.False(t, IsHiddenModelName("gpt-4o"))
	require.False(t, HasHiddenModelNames())
}

func TestInitModelAliasCache(t *testing.T) {
	require.NoError(t, DB.Exec("DELETE FROM abilities").Error)
	require.NoError(t, DB.Where("id in ?", []int{9101, 9102, 9103}).Delete(&Channel{}).Error)

	hidden := `{"my-gpt":"gpt-4o"}`
	require.NoError(t, DB.Create(&Channel{Id: 9101, Name: "alias-a", Key: "k", Models: "my-gpt", Group: "default", Status: 1, ModelAlias: &hidden}).Error)

	InitModelAliasCache()
	require.True(t, IsHiddenModelName("gpt-4o"))
	// 没开 keepInternal 时内部名不可调用，因此不进入"内部名 -> 对外名"的兼容表。
	_, ok := ResolveHiddenModelName("gpt-4o")
	require.False(t, ok)

	// 打开 keepInternal 后，内部名被调用时应改写成对外名。
	keepInternal := `{"model_alias_keep_internal":true}`
	require.NoError(t, DB.Model(&Channel{}).Where("id = ?", 9101).
		Updates(map[string]any{"settings": keepInternal}).Error)
	InitModelAliasCache()
	publicName, ok := ResolveHiddenModelName("gpt-4o")
	require.True(t, ok)
	require.Equal(t, "my-gpt", publicName)

	// 同一个内部名被映射到两个对外名：仍然隐藏，但停止兼容转发（不猜该回显谁）。
	otherHidden := `{"other-gpt":"gpt-4o"}`
	require.NoError(t, DB.Create(&Channel{Id: 9102, Name: "alias-b", Key: "k", Models: "other-gpt", Group: "default", Status: 1, ModelAlias: &otherHidden}).Error)
	InitModelAliasCache()
	require.True(t, IsHiddenModelName("gpt-4o"))
	_, ok = ResolveHiddenModelName("gpt-4o")
	require.False(t, ok)

	t.Cleanup(func() {
		DB.Where("id in ?", []int{9101, 9102, 9103}).Delete(&Channel{})
		DB.Exec("DELETE FROM abilities")
		InitModelAliasCache()
	})
}

// 内部名即使被误留在渠道「模型」列表里，也不能生成 ability —— 否则模型广场、
// /v1/models、体验区与令牌的模型选择会重新把它摆出来，并且可以按名调用。
func TestAddAbilitiesSkipsHiddenModelNames(t *testing.T) {
	require.NoError(t, DB.Exec("DELETE FROM abilities").Error)
	withAliasCache(t,
		map[string]struct{}{"gpt-4o": {}},
		map[string]string{},
	)

	channel := &Channel{
		Id:     9103,
		Models: "my-gpt,gpt-4o",
		Group:  "default",
		Status: 1,
	}
	require.NoError(t, channel.AddAbilities(nil))

	var abilityModels []string
	require.NoError(t, DB.Table("abilities").Where("channel_id = ?", channel.Id).Pluck("model", &abilityModels).Error)
	require.Equal(t, []string{"my-gpt"}, abilityModels)

	t.Cleanup(func() { DB.Exec("DELETE FROM abilities") })
}
