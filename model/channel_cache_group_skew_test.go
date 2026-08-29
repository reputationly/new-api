package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/require"
)

// TestInitChannelCache_GroupOnlyInChannels 渠道的分组在 abilities 里没有对应记录时，
// 缓存重建必须照常完成，而不是 panic。
//
// 两张表并不总是同步：abilities 只在保存渠道时由 UpdateAbilities() 重建，直接改库
// （运维 SQL、数据迁移）之后就会出现「channels.group 已经变了、abilities 还是旧的」
// 这种偏斜。本次分组合并就是这么改的。
//
// 而 InitChannelCache 的外层 map 的 key 集合恰恰是从 abilities 收集的，遍历的却是
// channels——偏斜时那个 group 对应 nil map，往里写就 panic。它跑在启动路径和定时
// 同步里，崩的是整个进程，不是一次请求。
func TestInitChannelCache_GroupOnlyInChannels(t *testing.T) {
	truncateTables(t)

	prev := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() { common.MemoryCacheEnabled = prev })

	// abilities 里只有 default，channels 上却挂着 default,brandnew —— 正是偏斜状态
	seedAbility(t, "default", "GLM-5", 1, true)
	require.NoError(t, DB.Create(&Channel{
		Id:     1,
		Name:   "ch",
		Group:  "default,brandnew",
		Models: "GLM-5",
		Status: common.ChannelStatusEnabled,
	}).Error)

	require.NotPanics(t, func() { InitChannelCache() })

	// 不止是「没崩」：偏斜的那个分组也必须被正确建进缓存，否则用它的令牌会拿到
	// 「无可用渠道」，症状同样难查
	got, err := GetRandomSatisfiedChannel("brandnew", "GLM-5", 0)
	require.NoError(t, err)
	require.NotNil(t, got, "channels 上挂了 brandnew，该分组必须能选到渠道")
	require.Equal(t, 1, got.Id)
}
