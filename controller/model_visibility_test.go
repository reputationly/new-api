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
	"gorm.io/gorm"
)

// seedVisibility 通过真实路径铺设可见性：写 Model 行 -> 重建缓存。
// 不直接改缓存内部结构，顺带把 InitModelVisibilityCache 的展开逻辑也覆盖到。
func seedVisibility(t *testing.T, db *gorm.DB, rows []model.Model) {
	t.Helper()
	for i := range rows {
		if rows[i].Status == 0 {
			rows[i].Status = 1
		}
		require.NoError(t, db.Create(&rows[i]).Error)
	}
	model.InitModelVisibilityCache()
	t.Cleanup(func() {
		// 缓存是包级全局的，不清会污染同包后续用例；DB 此时可能已关闭，
		// 所以先删记录再重建，重建失败也已经没有受限项可读
		_ = db.Where("1 = 1").Delete(&model.Model{}).Error
		model.InitModelVisibilityCache()
	})
}

// TestListModelsTokenLimitAppliesVisibility 修复 Codex review 第二条。
//
// 令牌白名单是**保存时的快照**：用户建令牌时模型还是公开的，之后管理员才给它配上
// VisibleGroups。controller/token.go 的保存时校验拦不住这个时序——它只在新建/编辑
// 令牌时生效，而「模型先公开、后来才收紧」恰恰是限制模型的典型顺序。
//
// 症状是「看得到调不了」：/v1/models 列出该模型，实际调用被 distributor 拒绝。
func TestListModelsTokenLimitAppliesVisibility(t *testing.T) {
	withSelfUseModeDisabled(t)
	withTieredBillingConfig(t, map[string]string{
		"zz-vis-open-model":       "tiered_expr",
		"zz-vis-restricted-model": "tiered_expr",
	}, map[string]string{
		"zz-vis-open-model":       `tier("base", p * 1 + c * 2)`,
		"zz-vis-restricted-model": `tier("base", p * 1 + c * 2)`,
	})

	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.Create(&model.User{
		Id:       2001,
		Username: "vis-token-user",
		Password: "password",
		Group:    "default",
		AffCode:  "vis-aff-2001",
		Status:   common.UserStatusEnabled,
	}).Error)
	seedVisibility(t, db, []model.Model{
		{ModelName: "zz-vis-restricted-model", VisibleGroups: "vip", NameRule: model.NameRuleExact},
	})

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	ctx.Set("id", 2001)
	common.SetContextKey(ctx, constant.ContextKeyTokenModelLimitEnabled, true)
	common.SetContextKey(ctx, constant.ContextKeyTokenModelLimit, map[string]bool{
		"zz-vis-open-model":       true,
		"zz-vis-restricted-model": true,
	})

	ListModels(ctx, constant.ChannelTypeOpenAI)

	ids := decodeListModelsResponse(t, recorder)
	require.Contains(t, ids, "zz-vis-open-model")
	require.NotContains(t, ids, "zz-vis-restricted-model",
		"令牌白名单里的存量模型，被限制后不该继续出现在 /v1/models")
}

// TestListModelsTokenLimitVisibleForAllowedGroup 名单内的用户仍能看到——过滤不能
// 变成一刀切。
func TestListModelsTokenLimitVisibleForAllowedGroup(t *testing.T) {
	withSelfUseModeDisabled(t)
	withTieredBillingConfig(t, map[string]string{
		"zz-vis2-restricted-model": "tiered_expr",
	}, map[string]string{
		"zz-vis2-restricted-model": `tier("base", p * 1 + c * 2)`,
	})

	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.Create(&model.User{
		Id:       2002,
		Username: "vis-vip-user",
		Password: "password",
		Group:    "vip",
		AffCode:  "vis-aff-2002",
		Status:   common.UserStatusEnabled,
	}).Error)
	seedVisibility(t, db, []model.Model{
		{ModelName: "zz-vis2-restricted-model", VisibleGroups: "vip", NameRule: model.NameRuleExact},
	})

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	ctx.Set("id", 2002)
	common.SetContextKey(ctx, constant.ContextKeyTokenModelLimitEnabled, true)
	common.SetContextKey(ctx, constant.ContextKeyTokenModelLimit, map[string]bool{
		"zz-vis2-restricted-model": true,
	})

	ListModels(ctx, constant.ChannelTypeOpenAI)

	require.Contains(t, decodeListModelsResponse(t, recorder), "zz-vis2-restricted-model")
}

// TestRetrieveModelAppliesVisibility 修复 Codex review 第一条。
//
// RetrieveModel 查的是编译期内置模型清单 openAIModelsMap，与站点挂载无关。真正的
// 问题不是「泄露存在性」（内置清单在开源代码里公开可查），而是**接口自相矛盾**：
// 同一个模型 GET /v1/models 里没有、GET /v1/models/:model 却返回 200，而客户端
// SDK 常用「先 list 再 retrieve」的模式。
func TestRetrieveModelAppliesVisibility(t *testing.T) {
	const builtinModel = "gpt-4"
	// 断言前提：该模型确实在内置清单里。内置清单若变动，这里要明确失败而不是假绿
	_, inBuiltin := openAIModelsMap[builtinModel]
	require.True(t, inBuiltin, "测试前提失效：%s 已不在内置模型清单中", builtinModel)

	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.Create(&model.User{
		Id:       2003,
		Username: "vis-retrieve-user",
		Password: "password",
		Group:    "default",
		AffCode:  "vis-aff-2003",
		Status:   common.UserStatusEnabled,
	}).Error)
	seedVisibility(t, db, []model.Model{
		{ModelName: builtinModel, VisibleGroups: "vip", NameRule: model.NameRuleExact},
	})

	t.Run("名单外返回 model_not_found", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/models/"+builtinModel, nil)
		ctx.Params = gin.Params{{Key: "model", Value: builtinModel}}
		ctx.Set("id", 2003)

		RetrieveModel(ctx, constant.ChannelTypeOpenAI)

		require.Contains(t, recorder.Body.String(), "model_not_found")
	})

	t.Run("名单内正常返回", func(t *testing.T) {
		require.NoError(t, db.Create(&model.User{
			Id:       2004,
			Username: "vis-retrieve-vip",
			Password: "password",
			Group:    "vip",
			AffCode:  "vis-aff-2004",
			Status:   common.UserStatusEnabled,
		}).Error)

		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/models/"+builtinModel, nil)
		ctx.Params = gin.Params{{Key: "model", Value: builtinModel}}
		ctx.Set("id", 2004)

		RetrieveModel(ctx, constant.ChannelTypeOpenAI)

		require.NotContains(t, recorder.Body.String(), "model_not_found")
		require.Contains(t, recorder.Body.String(), builtinModel)
	})
}

// TestRetrieveModelUnrestrictedUnchanged 无限制时行为与改造前逐位相同——这条保证
// 本次改动可以先上线、后配置。
func TestRetrieveModelUnrestrictedUnchanged(t *testing.T) {
	const builtinModel = "gpt-4"
	_, inBuiltin := openAIModelsMap[builtinModel]
	require.True(t, inBuiltin)

	db := setupModelListControllerTestDB(t)
	seedVisibility(t, db, nil) // 无任何限制

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/models/"+builtinModel, nil)
	ctx.Params = gin.Params{{Key: "model", Value: builtinModel}}
	ctx.Set("id", 2005)

	RetrieveModel(ctx, constant.ChannelTypeOpenAI)

	require.NotContains(t, recorder.Body.String(), "model_not_found")
}

// TestRetrieveModelFailsOpenWhenUserGroupUnavailable 取不到用户分组时必须放行。
//
// 必须在**有限制**的前提下测：无限制时 HasModelVisibilityRestrictions 会直接短路，
// 根本走不到取分组那一步——那样的用例看着在测 fail-open，实际什么都没测到
// （本用例的前一版就是这样，靠变异验证才发现）。
//
// fail-closed 的后果：userGroup 会是空串，而受限模型对空档位一律不可见，
// 一次取分组失败就会让模型接口对该用户整个塌掉。
func TestRetrieveModelFailsOpenWhenUserGroupUnavailable(t *testing.T) {
	const builtinModel = "gpt-4"
	_, inBuiltin := openAIModelsMap[builtinModel]
	require.True(t, inBuiltin)

	db := setupModelListControllerTestDB(t)
	seedVisibility(t, db, []model.Model{
		{ModelName: builtinModel, VisibleGroups: "vip", NameRule: model.NameRuleExact},
	})
	require.True(t, model.HasModelVisibilityRestrictions(),
		"前提：必须存在限制，否则会走短路分支而测不到 fail-open")

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/models/"+builtinModel, nil)
	ctx.Params = gin.Params{{Key: "model", Value: builtinModel}}
	// 指向一个不存在的用户，GetUserGroup 必然失败
	ctx.Set("id", 999999)

	RetrieveModel(ctx, constant.ChannelTypeOpenAI)

	require.NotContains(t, recorder.Body.String(), "model_not_found",
		"取不到用户分组时应放行，而不是把受限模型判为不存在")
}

// TestListModelsTokenLimitFailsOpenWhenUserGroupUnavailable ListModels 白名单分支的
// 同一个守卫：取不到用户身份时不过滤。
//
// 与 RetrieveModel 那条一样，必须在**有限制**的前提下测，否则
// FilterModelsByVisibility 内部会短路，测不到守卫本身。
func TestListModelsTokenLimitFailsOpenWhenUserGroupUnavailable(t *testing.T) {
	withSelfUseModeDisabled(t)
	withTieredBillingConfig(t,
		map[string]string{"zz-vis3-restricted-model": "tiered_expr"},
		map[string]string{"zz-vis3-restricted-model": `tier("base", p * 1 + c * 2)`})

	db := setupModelListControllerTestDB(t)
	seedVisibility(t, db, []model.Model{
		{ModelName: "zz-vis3-restricted-model", VisibleGroups: "vip", NameRule: model.NameRuleExact},
	})
	require.True(t, model.HasModelVisibilityRestrictions(),
		"前提：必须存在限制，否则会走短路分支而测不到守卫")

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	// 指向不存在的用户：GetUserGroup 会返回 ("", nil)——不报错但拿不到身份
	ctx.Set("id", 999999)
	common.SetContextKey(ctx, constant.ContextKeyTokenModelLimitEnabled, true)
	common.SetContextKey(ctx, constant.ContextKeyTokenModelLimit, map[string]bool{
		"zz-vis3-restricted-model": true,
	})

	ListModels(ctx, constant.ChannelTypeOpenAI)

	require.Contains(t, decodeListModelsResponse(t, recorder), "zz-vis3-restricted-model",
		"取不到用户分组时不该把白名单里的受限模型滤光")
}
