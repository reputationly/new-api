package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 诊断接口的每个闸都要能独立命中 —— 这些原因彼此独立，用户看到的症状却完全相同
// （"我在体验区选不到这个模型"），所以任何一条漏报都会把排查重新推回人肉猜测。
func TestDiagnoseChannelModelBlocksDisabledChannel(t *testing.T) {
	db := setupModelListControllerTestDB(t)

	require.NoError(t, db.Create(&model.Channel{
		Id:     9401,
		Name:   "disabled-owner",
		Key:    "k",
		Models: "zz-diag-disabled",
		Group:  "default",
		Status: common.ChannelStatusManuallyDisabled,
	}).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group: "default", Model: "zz-diag-disabled", ChannelId: 9401, Enabled: false,
	}).Error)

	result, err := diagnoseChannelModel("zz-diag-disabled")
	require.NoError(t, err)
	require.False(t, result.Visible)
	require.Len(t, result.Abilities, 1)
	require.Equal(t, "disabled-owner", result.Abilities[0].ChannelName)
	require.Equal(t, common.ChannelStatusManuallyDisabled, result.Abilities[0].ChannelStatus)
	require.Contains(t, joinStrings(result.Blockers), "全部禁用")
}

func TestDiagnoseChannelModelReportsMissingAbility(t *testing.T) {
	setupModelListControllerTestDB(t)

	// 渠道「模型」字段里有、abilities 里没有：这正是内部名被跳过、或渠道没保存成功的形态
	result, err := diagnoseChannelModel("zz-diag-ghost")
	require.NoError(t, err)
	require.False(t, result.Visible)
	require.Empty(t, result.Abilities)
	require.Contains(t, joinStrings(result.Blockers), "没有任何渠道")
}

func TestDiagnoseChannelModelNamesTheAliasOwner(t *testing.T) {
	db := setupModelListControllerTestDB(t)

	alias := `{"zz-diag-public":"zz-diag-internal"}`
	require.NoError(t, db.Create(&model.Channel{
		Id:         9402,
		Name:       "alias-owner-diag",
		Key:        "k",
		Models:     "zz-diag-public",
		Group:      "default",
		Status:     common.ChannelStatusEnabled,
		ModelAlias: &alias,
	}).Error)
	model.InitModelAliasCache()
	t.Cleanup(func() { model.InitModelAliasCache() })

	result, err := diagnoseChannelModel("zz-diag-internal")
	require.NoError(t, err)
	require.False(t, result.Visible)
	require.Len(t, result.HiddenBy, 1)
	require.Equal(t, 9402, result.HiddenBy[0].ChannelId)
	// 结论里必须点名渠道与对外名，否则运营不知道该改哪一条
	blockers := joinStrings(result.Blockers)
	require.Contains(t, blockers, "内部名")
	require.Contains(t, blockers, "alias-owner-diag")
	require.Contains(t, blockers, "zz-diag-public")
}

// 仅大小写不同的并存名字是"两个看起来一样的模型"的根源：必须报出来，且标明谁启用着。
func TestDiagnoseChannelModelReportsCaseVariants(t *testing.T) {
	db := setupModelListControllerTestDB(t)

	require.NoError(t, db.Create(&model.Channel{
		Id:     9403,
		Name:   "lower-owner",
		Key:    "k",
		Models: "zz-diag-case",
		Group:  "default",
		Status: common.ChannelStatusEnabled,
	}).Error)
	require.NoError(t, db.Create(&model.Channel{
		Id:     9404,
		Name:   "upper-owner",
		Key:    "k",
		Models: "ZZ-Diag-Case",
		Group:  "default",
		Status: common.ChannelStatusEnabled,
	}).Error)
	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "default", Model: "zz-diag-case", ChannelId: 9403, Enabled: true},
		{Group: "default", Model: "ZZ-Diag-Case", ChannelId: 9404, Enabled: true},
	}).Error)

	result, err := diagnoseChannelModel("zz-diag-case")
	require.NoError(t, err)
	require.True(t, result.Visible)
	require.Len(t, result.CaseVariants, 2)
	require.Contains(t, joinStrings(result.Warnings), "仅大小写不同")
}

func joinStrings(items []string) string {
	out := ""
	for _, item := range items {
		out += item + "\n"
	}
	return out
}

// 接口层：缺参数要 400（而不是把空名当模型名去查一堆空结论），正常名要 200 且带上
// data.model —— 前端用它回显"你诊断的是哪个名字"。
func TestDiagnoseChannelModelHandler(t *testing.T) {
	setupModelListControllerTestDB(t)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/channel/diagnose?model=zz-diag-ghost", nil)
	DiagnoseChannelModel(ctx)
	require.Equal(t, http.StatusOK, recorder.Code)

	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			Model    string   `json:"model"`
			Visible  bool     `json:"visible"`
			Blockers []string `json:"blockers"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	require.True(t, payload.Success)
	require.Equal(t, "zz-diag-ghost", payload.Data.Model)
	require.False(t, payload.Data.Visible)
	require.NotEmpty(t, payload.Data.Blockers)

	empty := httptest.NewRecorder()
	emptyCtx, _ := gin.CreateTestContext(empty)
	emptyCtx.Request = httptest.NewRequest(http.MethodGet, "/api/channel/diagnose", nil)
	DiagnoseChannelModel(emptyCtx)
	require.Equal(t, http.StatusBadRequest, empty.Code)
}
