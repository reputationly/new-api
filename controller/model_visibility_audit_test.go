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

// 「哪些模型因为渠道停用而不可见」必须能从页面上看出来。
// 判据：一条启用中的 ability 都没有的模型名 = 用户哪儿都看不到它。
func TestGetChannelInvisibleModels(t *testing.T) {
	db := setupModelListControllerTestDB(t)

	require.NoError(t, db.Create(&[]model.Ability{
		// 渠道 9501 启用：模型可见
		{Group: "default", Model: "zz-visible-a", ChannelId: 9501, Enabled: true},
		{Group: "default", Model: "zz-visible-b", ChannelId: 9501, Enabled: true},
		// 渠道 9502 停用：它带来的两个模型对用户不可见
		{Group: "default", Model: "zz-gone-a", ChannelId: 9502, Enabled: false},
		{Group: "default", Model: "zz-gone-b", ChannelId: 9502, Enabled: false},
		// 同一个模型被两条渠道供奉、其中一条停用：仍然可见，不该计入
		{Group: "default", Model: "zz-visible-b", ChannelId: 9502, Enabled: false},
		// 同一个停用模型挂在两个分组上：按模型去重，数字不能翻倍
		{Group: "vip", Model: "zz-gone-a", ChannelId: 9502, Enabled: false},
	}).Error)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/channel/invisible_models", nil)
	GetChannelInvisibleModels(ctx)
	require.Equal(t, http.StatusOK, recorder.Code)

	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			ByChannel           map[string]int `json:"by_channel"`
			InvisibleModels     int            `json:"invisible_models"`
			InvisibleModelNames []string       `json:"invisible_model_names"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	require.True(t, payload.Success)

	// 9501 没有停用的行，不该出现在 map 里。
	// 9502 有 3 条 enabled=false，但其中 zz-visible-b 还被 9501 卖着 —— 它对用户可见，
	// 所以这里数的是"不可见模型数"（2），不是能力行数（3）。
	require.NotContains(t, payload.Data.ByChannel, "9501")
	require.Equal(t, 2, payload.Data.ByChannel["9502"])

	// 真正不可见的只有那两个（zz-visible-b 还有一条启用的）
	require.Equal(t, 2, payload.Data.InvisibleModels)
	require.Equal(t, []string{"zz-gone-a", "zz-gone-b"}, payload.Data.InvisibleModelNames)
}

// 仅大小写不同的名字是"两个看着一样的模型"的根源，且倍率按精确名匹配：
// 只配了一边时另一边静默按默认倍率计费。这里锁住分组与逐变体信息。
func TestGetModelCaseVariants(t *testing.T) {
	db := setupModelListControllerTestDB(t)

	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "default", Model: "zz-case-model", ChannelId: 9601, Enabled: true},
		{Group: "default", Model: "ZZ-Case-Model", ChannelId: 9602, Enabled: false},
		{Group: "default", Model: "ZZ-Case-Model", ChannelId: 9603, Enabled: true},
		// 只有一个写法的模型不该出现在体检结果里
		{Group: "default", Model: "zz-single-model", ChannelId: 9601, Enabled: true},
	}).Error)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/models/case_variants", nil)
	GetModelCaseVariants(ctx)
	require.Equal(t, http.StatusOK, recorder.Code)

	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			Groups      []caseVariantGroup `json:"groups"`
			TotalGroups int                `json:"total_groups"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	require.True(t, payload.Success)
	require.Equal(t, 1, payload.Data.TotalGroups)
	require.Len(t, payload.Data.Groups, 1)

	group := payload.Data.Groups[0]
	require.Equal(t, "zz-case-model", group.NameLower)
	require.Len(t, group.Variants, 2)
	// 变体按名字升序：大写在前
	require.Equal(t, "ZZ-Case-Model", group.Variants[0].Model)
	require.True(t, group.Variants[0].Enabled)
	require.Equal(t, []int{9602, 9603}, group.Variants[0].ChannelIds)
	require.Equal(t, 2, group.Variants[0].AbilityRows)
	require.Equal(t, "zz-case-model", group.Variants[1].Model)
	require.True(t, group.Variants[1].Enabled)
}
