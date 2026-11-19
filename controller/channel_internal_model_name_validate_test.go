package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

// 把内部名（模型别名的右侧）写进渠道的「模型」列表是确定性错误：
// AddAbilities / UpdateAbilities 会跳过它，这个模型永远不会生成 ability、
// 也不会出现在任何模型列表里，而保存接口照样返回成功 —— 运营只会看到
// "我加了这个模型，体验区却选不到"。所以保存时必须拦掉。
//
// 这条用例同时锁住第二件事：错误信息里必须带声明方的渠道 id / 名称 / 对外名，
// 只报"这是内部名"等于让运营自己去翻全部渠道的别名。
func TestValidateChannelRejectsInternalModelName(t *testing.T) {
	db := setupModelListControllerTestDB(t)

	alias := `{"zz-public-model":"zz-internal-model"}`
	require.NoError(t, db.Create(&model.Channel{
		Id:         9301,
		Name:       "alias-owner",
		Key:        "k",
		Models:     "zz-public-model",
		Group:      "default",
		Status:     common.ChannelStatusEnabled,
		ModelAlias: &alias,
	}).Error)

	err := validateChannel(&model.Channel{
		Key:    "k",
		Models: "zz-internal-model",
		Group:  "default",
	}, true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "zz-internal-model")
	require.Contains(t, err.Error(), "#9301")
	require.Contains(t, err.Error(), "alias-owner")
	require.Contains(t, err.Error(), "zz-public-model")

	// 混在一堆正常模型中间、且带空格时也要拦（只校验首项的实现会漏掉）
	err = validateChannel(&model.Channel{
		Key:    "k",
		Models: "zz-fine-model, zz-internal-model",
		Group:  "default",
	}, true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "zz-internal-model")

	// 改渠道走同一个函数（isAdd=false），同样会重建 abilities，同样要拦
	err = validateChannel(&model.Channel{
		Key:    "k",
		Models: "zz-internal-model",
		Group:  "default",
	}, false)
	require.Error(t, err)

	// 对外名与无关名字不受影响
	require.NoError(t, validateChannel(&model.Channel{
		Key:    "k",
		Models: "zz-public-model,zz-other-model",
		Group:  "default",
	}, true))
}

// 别名声明已被删掉、而内部名缓存还没同步时，保存依然会建不出 ability。
// 这条兜底提示要与"有声明方"的提示区分开，否则运营会去查一个已经查不到的声明。
func TestValidateChannelRejectsStaleHiddenCache(t *testing.T) {
	db := setupModelListControllerTestDB(t)

	alias := `{"zz-cached-public":"zz-cached-internal"}`
	require.NoError(t, db.Create(&model.Channel{
		Id:         9302,
		Name:       "cache-owner",
		Key:        "k",
		Models:     "zz-cached-public",
		Group:      "default",
		Status:     common.ChannelStatusEnabled,
		ModelAlias: &alias,
	}).Error)
	// 先把缓存建起来，再把别名从库里删掉 —— 模拟"缓存落后库"的窗口
	model.InitModelAliasCache()
	t.Cleanup(func() { model.InitModelAliasCache() })
	require.NoError(t, db.Model(&model.Channel{}).
		Where("id = ?", 9302).
		Update("model_alias", "").Error)

	err := validateChannel(&model.Channel{
		Key:    "k",
		Models: "zz-cached-internal",
		Group:  "default",
	}, true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "缓存")
}

// 反方向同一个坑：别名把「正在被别的渠道售卖的名字」声明成内部名，会让那些渠道的
// ability 在重建时被删掉 —— 出事的渠道页面上毫无痕迹。所以保存别名时也要拦。
func TestValidateChannelRejectsAliasThatHidesLiveModel(t *testing.T) {
	db := setupModelListControllerTestDB(t)

	require.NoError(t, db.Create(&model.Channel{
		Id:     9311,
		Name:   "live-seller",
		Key:    "k",
		Models: "zz-live-model",
		Group:  "default",
		Status: common.ChannelStatusEnabled,
	}).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group: "default", Model: "zz-live-model", ChannelId: 9311, Enabled: true,
	}).Error)

	alias := `{"zz-alias-public":"zz-live-model"}`
	err := validateChannel(&model.Channel{
		Key:        "k",
		Models:     "zz-alias-public",
		Group:      "default",
		ModelAlias: &alias,
	}, true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "#9311")
	require.Contains(t, err.Error(), "live-seller")
	require.Contains(t, err.Error(), "模型重定向")

	// 历史配置（该名字早就是内部名）不因为一次无关编辑被挡死
	require.NoError(t, db.Create(&model.Channel{
		Id:         9312,
		Name:       "historical-alias-owner",
		Key:        "k",
		Models:     "zz-hist-public",
		Group:      "default",
		Status:     common.ChannelStatusEnabled,
		ModelAlias: &alias,
	}).Error)
	require.NoError(t, validateChannel(&model.Channel{
		Key:        "k",
		Models:     "zz-hist-public",
		Group:      "default",
		ModelAlias: &alias,
	}, true))
}
