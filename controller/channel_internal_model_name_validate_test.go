package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
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

// 校验不能把「正在编辑的渠道自己」当成冲突方：这些操作在改动前一次保存就能完成，
// 若被自己拦住，运营只能拆成两次保存，中间那段时间模型处于不可用状态。
func TestValidateChannelDoesNotConflictWithItself(t *testing.T) {
	db := setupModelListControllerTestDB(t)

	// 场景 1：渠道原本直接卖 zz-self-y，一次保存改成卖 zz-self-x 且配别名 x -> y
	require.NoError(t, db.Create(&model.Channel{
		Id: 9701, Name: "self-rewrite", Key: "k", Models: "zz-self-y",
		Group: "default", Status: common.ChannelStatusEnabled,
	}).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group: "default", Model: "zz-self-y", ChannelId: 9701, Enabled: true,
	}).Error)
	aliasAfter := `{"zz-self-x":"zz-self-y"}`
	require.NoError(t, validateChannel(&model.Channel{
		Id: 9701, Key: "k", Models: "zz-self-x", Group: "default",
		ModelAlias: &aliasAfter,
	}, false), "库里的旧模型名与旧能力行都属于自己，不该被当成冲突")

	// 场景 2：渠道原有别名 zz-pub -> zz-int，一次保存撤掉别名改卖 zz-int
	aliasBefore := `{"zz-pub":"zz-int"}`
	require.NoError(t, db.Create(&model.Channel{
		Id: 9702, Name: "self-unhide", Key: "k", Models: "zz-pub",
		Group: "default", Status: common.ChannelStatusEnabled, ModelAlias: &aliasBefore,
	}).Error)
	model.InitModelAliasCache()
	t.Cleanup(func() { model.InitModelAliasCache() })
	// 注意必须是**显式清空**（空串指针），而不是"不提交"：不提交时库里的旧别名仍然生效，
	// 那时把 zz-int 写进模型列表依旧是错配置（见下一条用例）。
	emptyAlias := ""
	require.NoError(t, validateChannel(&model.Channel{
		Id: 9702, Key: "k", Models: "zz-int", Group: "default", ModelAlias: &emptyAlias,
	}, false), "撤掉别名后自己不再声明这个内部名，应该放行")
}

// 历史配置不该挡住无关编辑（改 key、改优先级），但新加一个内部名必须拦。
func TestValidateChannelAllowsHistoricalInternalNameButRejectsNew(t *testing.T) {
	db := setupModelListControllerTestDB(t)

	aliasOwner := `{"zz-hist-pub":"zz-hist-int","zz-hist-pub-2":"zz-hist-int-2"}`
	require.NoError(t, db.Create(&model.Channel{
		Id: 9703, Name: "historical-owner", Key: "k", Models: "zz-hist-pub,zz-hist-pub-2",
		Group: "default", Status: common.ChannelStatusEnabled, ModelAlias: &aliasOwner,
	}).Error)
	// 出事渠道：models 里留着别人声明的内部名
	require.NoError(t, db.Create(&model.Channel{
		Id: 9704, Name: "victim", Key: "k", Models: "zz-hist-int,zz-ok-model",
		Group: "default", Status: common.ChannelStatusEnabled,
	}).Error)
	model.InitModelAliasCache()
	t.Cleanup(func() { model.InitModelAliasCache() })

	// 只改 key：模型列表原样提交，历史遗留的内部名应被放行
	require.NoError(t, validateChannel(&model.Channel{
		Id: 9704, Key: "k2", Models: "zz-hist-int,zz-ok-model", Group: "default",
	}, false))

	// 但新加一个内部名必须拦
	err := validateChannel(&model.Channel{
		Id: 9704, Key: "k2", Models: "zz-hist-int,zz-ok-model,zz-hist-int-2", Group: "default",
	}, false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "zz-hist-int-2")
}

// 本次保存的别名把「自己正在卖的模型」声明成内部名：这是自相矛盾的自引用配置，
// 无论是新建还是更新都要拦。
func TestValidateChannelRejectsSelfReferentialAlias(t *testing.T) {
	setupModelListControllerTestDB(t)

	alias := `{"zz-sr-x":"zz-sr-y"}`
	err := validateChannel(&model.Channel{
		Key: "k", Models: "zz-sr-x,zz-sr-y", Group: "default", ModelAlias: &alias,
	}, true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "保存生效")
}

// 只加别名、不动模型列表：渠道卖 y 又声明 X -> y，保存后 y 不再生成 ability。
// 上一版把「自家渠道」整体排除后漏掉了这条 —— 自引用的错配置永远要拦。
func TestValidateChannelRejectsAliasHidingOwnListedModel(t *testing.T) {
	db := setupModelListControllerTestDB(t)

	require.NoError(t, db.Create(&model.Channel{
		Id: 9711, Name: "alias-only", Key: "k", Models: "zz-own-y",
		Group: "default", Status: common.ChannelStatusEnabled,
	}).Error)
	aliasAdded := `{"zz-own-x":"zz-own-y"}`
	err := validateChannel(&model.Channel{
		Id: 9711, Key: "k", Models: "zz-own-y", Group: "default",
		ModelAlias: &aliasAdded,
	}, false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "zz-own-y")
}

// 请求没带别名字段时，库里的旧别名仍然生效（GORM Updates 只写非零字段）。
// 这时往模型列表里加一个「自己旧别名的内部名」，保存后同样不生成 ability。
func TestValidateChannelUsesStoredAliasWhenFieldAbsent(t *testing.T) {
	db := setupModelListControllerTestDB(t)

	alias := `{"zz-old-x":"zz-old-y"}`
	require.NoError(t, db.Create(&model.Channel{
		Id: 9712, Name: "stored-alias", Key: "k", Models: "zz-old-x",
		Group: "default", Status: common.ChannelStatusEnabled, ModelAlias: &alias,
	}).Error)
	model.InitModelAliasCache()
	t.Cleanup(func() { model.InitModelAliasCache() })

	// ModelAlias 为 nil = 本次不改别名 -> 生效的仍是 X -> y
	err := validateChannel(&model.Channel{
		Id: 9712, Key: "k", Models: "zz-old-x,zz-old-y", Group: "default",
	}, false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "zz-old-y")

	// 只改 key、模型列表原样 -> 放行
	require.NoError(t, validateChannel(&model.Channel{
		Id: 9712, Key: "k2", Models: "zz-old-x", Group: "default",
	}, false))
}

// 按标签只改别名：标签下卖 y 的成员，保存后 ability 会变空，必须拦住。
func TestValidateModelAliasConsistencyForTagAliasOnly(t *testing.T) {
	db := setupModelListControllerTestDB(t)

	require.NoError(t, db.Create(&model.Channel{
		Id: 9721, Name: "tag-a", Key: "k", Models: "zz-tag-y", Tag: ptrString("tag1"),
		Group: "default", Status: common.ChannelStatusEnabled,
	}).Error)
	require.NoError(t, db.Create(&model.Channel{
		Id: 9722, Name: "tag-b", Key: "k", Models: "zz-tag-z", Tag: ptrString("tag1"),
		Group: "default", Status: common.ChannelStatusEnabled,
	}).Error)
	model.InitModelAliasCache()
	t.Cleanup(func() { model.InitModelAliasCache() })

	tagChannels, err := model.GetChannelsByTag("tag1", false, false)
	require.NoError(t, err)
	require.NotEmpty(t, tagChannels)

	// 只提交别名（模型列表不提交 -> 各成员沿用自己库里的值）
	aliasMap, err := model.ParseModelAlias(`{"zz-tag-x":"zz-tag-y"}`)
	require.NoError(t, err)
	targets := buildPolicyTargetsFromChannels(tagChannels, nil, false, aliasMap, true)
	require.Error(t, validateModelAliasConsistency(targets), "tag-a 卖 zz-tag-y，别名又把 y 声明为内部名")

	// 同一套别名但对 tag-b 这种不卖 y 的成员是合法配置（用单成员验证）
	singleTargets := buildPolicyTargetsFromChannels(tagChannels[1:], nil, false, aliasMap, true)
	require.NoError(t, validateModelAliasConsistency(singleTargets))
}

// 自动同步遇到坏名字时要剔除并继续，而不是整条渠道失败。
func TestDropUnsafeUpstreamModels(t *testing.T) {
	db := setupModelListControllerTestDB(t)

	alias := `{"zz-up-pub":"zz-up-internal"}`
	require.NoError(t, db.Create(&model.Channel{
		Id: 9731, Name: "alias-owner-up", Key: "k", Models: "zz-up-pub",
		Group: "default", Status: common.ChannelStatusEnabled, ModelAlias: &alias,
	}).Error)
	require.NoError(t, db.Create(&model.Channel{
		Id: 9732, Name: "sync-target", Key: "k", Models: "zz-keep",
		Group: "default", Status: common.ChannelStatusEnabled,
	}).Error)
	model.InitModelAliasCache()
	t.Cleanup(func() { model.InitModelAliasCache() })

	channel, err := model.GetChannelById(9732, false)
	require.NoError(t, err)

	safe, dropped, reasons := dropUnsafeUpstreamModels(channel, []string{"zz-keep", "zz-up-internal", "zz-new"})
	require.Equal(t, []string{"zz-keep", "zz-new"}, safe)
	require.Equal(t, []string{"zz-up-internal"}, dropped)
	require.Contains(t, reasons["zz-up-internal"], "内部名")
}

func ptrString(v string) *string { return &v }

// 缓存落后于库时的窗口（多节点部署或直接改库才有）：别名已从库里删掉，本节点缓存还认为
// 某名字是内部名。这时**渠道原有的模型**绝不能被剔除 —— 上游自动同步会拿这个列表覆盖
// 渠道，少一项就等于丢模型；同一窗口里的无关编辑也不该被拒。
func TestStaleCacheDoesNotDropExistingModels(t *testing.T) {
	db := setupModelListControllerTestDB(t)

	alias := `{"zz-stale-pub":"zz-stale-int","zz-stale-pub-2":"zz-stale-int-2"}`
	require.NoError(t, db.Create(&model.Channel{
		Id: 9741, Name: "stale-owner", Key: "k", Models: "zz-stale-pub,zz-stale-pub-2",
		Group: "default", Status: common.ChannelStatusEnabled, ModelAlias: &alias,
	}).Error)
	model.InitModelAliasCache()
	t.Cleanup(func() { model.InitModelAliasCache() })
	// 别名从库里消失，但本节点缓存仍是旧的
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 9741).Update("model_alias", "").Error)

	channel := &model.Channel{Id: 9742, Name: "stale-target", Models: "zz-stale-int", Group: "default"}

	// 只提交原有模型：无违规
	targets := buildPolicyTargetsFromChannels([]*model.Channel{channel}, []string{"zz-stale-int"}, true, nil, false)
	require.Empty(t, collectUnsafeModelNames(targets[0], policyExcludeSet(targets)))

	// 自动同步合并后：原有模型保留，只剔除新加的隐藏名
	safe, dropped, _ := dropUnsafeUpstreamModels(channel, []string{"zz-stale-int", "zz-stale-int-2"})
	require.Equal(t, []string{"zz-stale-int"}, safe, "原有模型必须保留在渠道列表里")
	require.Equal(t, []string{"zz-stale-int-2"}, dropped)
}

// 按标签编辑时 `models: ""` 按"不改模型列表"处理（EditChannelByTag 就是这么做的）。
// 若校验把它当成"清空"，传空串 + 新别名就能绕过检查，该标签下渠道的模型照样静默消失。
func TestEditTagChannelsTreatsEmptyModelsAsUnchanged(t *testing.T) {
	db := setupModelListControllerTestDB(t)

	require.NoError(t, db.Create(&model.Channel{
		Id: 9751, Name: "tag-empty-owner", Key: "k", Models: "zz-tag-empty-y",
		Tag: ptrString("tag-empty"), Group: "default", Status: common.ChannelStatusEnabled,
	}).Error)
	model.InitModelAliasCache()
	t.Cleanup(func() { model.InitModelAliasCache() })

	body := `{"tag":"tag-empty","models":"","model_alias":"{\"zz-tag-empty-x\":\"zz-tag-empty-y\"}"}`
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/channel/tag", strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")

	EditTagChannels(ctx)

	require.Contains(t, recorder.Body.String(), "zz-tag-empty-y",
		"空串不该被当成清空模型列表：别名会把 zz-tag-empty-y 变成内部名")
	require.Contains(t, recorder.Body.String(), `"success":false`)
}
