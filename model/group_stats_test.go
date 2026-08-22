package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func seedAbility(t *testing.T, group, modelName string, channelID int, enabled bool) {
	t.Helper()
	require.NoError(t, DB.Create(&Ability{
		Group:     group,
		Model:     modelName,
		ChannelId: channelID,
		Enabled:   enabled,
	}).Error)
}

func seedChannelWithGroup(t *testing.T, id int, group string) {
	t.Helper()
	require.NoError(t, DB.Create(&Channel{
		Id:    id,
		Name:  "ch",
		Group: group,
	}).Error)
}

func TestGetGroupCoverage(t *testing.T) {
	truncateTables(t)

	// premium：2 个渠道、2 个模型（其中一个模型被两个渠道同时挂载）
	seedAbility(t, "premium", "GLM-5", 1, true)
	seedAbility(t, "premium", "GLM-5", 2, true)
	seedAbility(t, "premium", "wan2.2", 1, true)
	// default：1 个渠道 1 个模型
	seedAbility(t, "default", "gpt-4o", 3, true)
	// 停用的不算：停用渠道对用户等同于不存在，算进去会让不可用的分组显示成健康
	seedAbility(t, "stale", "gpt-4o", 4, false)

	got, err := GetGroupCoverage()
	require.NoError(t, err)

	require.Equal(t, 2, got["premium"].ChannelCount, "同一模型挂两个渠道时渠道数要去重")
	require.Equal(t, 2, got["premium"].ModelCount, "同一模型挂两个渠道时模型数也要去重")
	require.Equal(t, 1, got["default"].ChannelCount)
	require.Equal(t, 1, got["default"].ModelCount)
	require.NotContains(t, got, "stale", "仅有停用 ability 的分组不该算作已挂载")
}

func TestGetGroupModels(t *testing.T) {
	truncateTables(t)

	seedAbility(t, "premium", "wan2.2", 1, true)
	seedAbility(t, "premium", "GLM-5", 1, true)
	seedAbility(t, "premium", "GLM-5", 2, true)
	seedAbility(t, "premium", "disabled-model", 3, false)
	seedAbility(t, "default", "gpt-4o", 4, true)

	got, err := GetGroupModels("premium")
	require.NoError(t, err)

	require.Equal(t, []string{"GLM-5", "wan2.2"}, got,
		"去重且有序；不含其他分组的模型，也不含停用的")
}

func TestGetGroupReferences(t *testing.T) {
	truncateTables(t)

	// aff_code 有唯一约束，两个用户不能都留空
	require.NoError(t, DB.Create(&User{Username: "u1", AffCode: "aff1", Group: "premium"}).Error)
	require.NoError(t, DB.Create(&User{Username: "u2", AffCode: "aff2", Group: "default"}).Error)
	require.NoError(t, DB.Create(&Token{Name: "t1", Key: "k1", Group: "premium"}).Error)
	require.NoError(t, DB.Create(&SubscriptionPlan{Title: "p1", UpgradeGroup: "premium"}).Error)

	seedChannelWithGroup(t, 1, "default,premium")
	seedChannelWithGroup(t, 2, "premium")
	// 前缀相同但不是同一个分组：靠逗号切分而不是 LIKE '%premium%' 才不会误算
	seedChannelWithGroup(t, 3, "premium_special")

	refs, err := GetGroupReferences("premium")
	require.NoError(t, err)

	require.EqualValues(t, 1, refs.Users)
	require.EqualValues(t, 1, refs.Tokens)
	require.EqualValues(t, 1, refs.Plans)
	require.EqualValues(t, 2, refs.Channels, "premium_special 不能被算成 premium")
	require.EqualValues(t, 5, refs.Total())
}

func TestGetGroupReferences_Unreferenced(t *testing.T) {
	truncateTables(t)

	require.NoError(t, DB.Create(&User{Username: "u1", Group: "default"}).Error)
	seedChannelWithGroup(t, 1, "default")

	refs, err := GetGroupReferences("ghost")
	require.NoError(t, err)
	require.EqualValues(t, 0, refs.Total(), "无引用的分组可以直接删")
}

func TestGetGroupsUsedByChannels(t *testing.T) {
	truncateTables(t)

	seedChannelWithGroup(t, 1, "default,premium")
	seedChannelWithGroup(t, 2, " premium , volcano ") // 带空格的手工输入
	seedChannelWithGroup(t, 3, "")

	got, err := GetGroupsUsedByChannels()
	require.NoError(t, err)

	require.ElementsMatch(t, []string{"default", "premium", "volcano"}, got,
		"去重、去空、trim；这份名单减去已配置分组就是失配提示条的内容")
}
