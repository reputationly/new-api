package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 老用户在 users.setting 里存的 accept_unset_model_ratio_model 不再生效：
// 即使它在令牌白名单里，未定价模型也不能出现在 /v1/models。
func TestListModelsIgnoresLegacyAcceptUnsetSetting(t *testing.T) {
	withSelfUseModeDisabled(t)
	db := setupModelListControllerTestDB(t)

	const unpriced = "zz-legacy-unpriced-model"
	require.NoError(t, db.Create(&model.User{
		Id:       1051,
		Username: "legacy-unset-user",
		Password: "password",
		Group:    "default",
		Status:   common.UserStatusEnabled,
		// 存量 JSON：老版本写入的开关。新代码的 dto.UserSetting 里已无此字段。
		Setting: `{"accept_unset_model_ratio_model":true}`,
	}).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group: "default", Model: unpriced, ChannelId: 1, Enabled: true,
	}).Error)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	ctx.Set("id", 1051)
	common.SetContextKey(ctx, constant.ContextKeyTokenModelLimitEnabled, true)
	common.SetContextKey(ctx, constant.ContextKeyTokenModelLimit, map[string]bool{unpriced: true})

	ListModels(ctx, constant.ChannelTypeOpenAI)

	ids := decodeListModelsResponse(t, recorder)
	require.NotContains(t, ids, unpriced)
}
