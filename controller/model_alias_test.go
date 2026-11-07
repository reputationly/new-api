package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func withSelfUseModeEnabled(t *testing.T) {
	t.Helper()
	original := operation_setting.SelfUseModeEnabled
	operation_setting.SelfUseModeEnabled = true
	t.Cleanup(func() { operation_setting.SelfUseModeEnabled = original })
}

// 内部名（模型别名的右侧）不得出现在任何用户可达的模型名出口。
//
// 这是本功能的核心承诺：运营把上游真实模型名 a 藏起来、只对外卖 b 之后，
// 客户端从 /v1/models 与 /v1/models/{model} 里都拿不到 a。
func TestInternalModelNameHiddenFromModelEndpoints(t *testing.T) {
	withSelfUseModeEnabled(t)
	db := setupModelListControllerTestDB(t)

	alias := `{"zz-public-model":"zz-internal-model"}`
	require.NoError(t, db.Create(&model.Channel{
		Id:         9201,
		Name:       "alias-channel",
		Key:        "k",
		Models:     "zz-public-model",
		Group:      "default",
		Status:     common.ChannelStatusEnabled,
		ModelAlias: &alias,
	}).Error)
	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "default", Model: "zz-public-model", ChannelId: 9201, Enabled: true},
		// 故意留一条内部名的残留 ability：纵深防御必须把它挡住，
		// 否则"渠道保存异常/直接改库"就会让内部名重新出现在对外列表里。
		{Group: "default", Model: "zz-internal-model", ChannelId: 9201, Enabled: true},
	}).Error)
	model.InitModelAliasCache()
	t.Cleanup(func() { model.InitModelAliasCache() })

	// ① 分组模型列表（/v1/models 的主数据源）
	models := model.GetGroupEnabledModels("default")
	require.Contains(t, models, "zz-public-model")
	require.NotContains(t, models, "zz-internal-model")

	// ② /v1/models 的令牌白名单分支：白名单是保存时的快照，里面可能还留着内部名
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	common.SetContextKey(ctx, constant.ContextKeyTokenModelLimitEnabled, true)
	common.SetContextKey(ctx, constant.ContextKeyTokenModelLimit, map[string]bool{
		"zz-public-model":   true,
		"zz-internal-model": true,
	})
	ListModels(ctx, constant.ChannelTypeOpenAI)

	ids := decodeListModelsResponse(t, recorder)
	require.Contains(t, ids, "zz-public-model")
	require.NotContains(t, ids, "zz-internal-model")
}

// GET /v1/models/{model} 查的是编译期的内置模型清单，与站点挂载了什么无关。
// 不在这里拦，一个被隐藏的内置名（例如 gpt-4o）依然能拿到 200 —— 隐藏就形同虚设。
func TestRetrieveModelRejectsInternalName(t *testing.T) {
	withSelfUseModeEnabled(t)
	db := setupModelListControllerTestDB(t)

	alias := `{"my-gpt-4o":"gpt-4o"}`
	require.NoError(t, db.Create(&model.Channel{
		Id:         9202,
		Name:       "alias-channel-retrieve",
		Key:        "k",
		Models:     "my-gpt-4o",
		Group:      "default",
		Status:     common.ChannelStatusEnabled,
		ModelAlias: &alias,
	}).Error)
	model.InitModelAliasCache()
	t.Cleanup(func() { model.InitModelAliasCache() })

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/models/gpt-4o", nil)
	ctx.Params = gin.Params{{Key: "model", Value: "gpt-4o"}}
	ctx.Set("id", 0)

	RetrieveModel(ctx, constant.ChannelTypeOpenAI)

	require.Contains(t, recorder.Body.String(), "model_not_found",
		"内部名必须与「模型不存在」返回同一种结果，不能暴露它存在但被隐藏了")
}

// 保存渠道时校验 model_alias：坏 JSON 一旦落库，缓存侧会静默降级成"没有别名"，
// 请求侧则每次调用都报错，两者都不容易从页面看出来。
func TestValidateChannelRejectsBrokenModelAlias(t *testing.T) {
	broken := `{"my-gpt":`
	err := validateChannel(&model.Channel{Key: "k", ModelAlias: &broken}, true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "模型重定向（隐藏内部名）")

	valid := `{"my-gpt":"gpt-4o"}`
	require.NoError(t, validateChannel(&model.Channel{Key: "k", ModelAlias: &valid}, true))

	// 未配置时不校验
	require.NoError(t, validateChannel(&model.Channel{Key: "k"}, true))
}
