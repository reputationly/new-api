package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
)

// withAggregateModelConfig 装配一份聚合模型配置,测试结束还原。
func withAggregateModelConfig(t *testing.T, raw string) {
	t.Helper()
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	orig, had := common.OptionMap["AggregateModelConfig"]
	common.OptionMap["AggregateModelConfig"] = raw
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		if had {
			common.OptionMap["AggregateModelConfig"] = orig
		} else {
			delete(common.OptionMap, "AggregateModelConfig")
		}
		common.OptionMapRWMutex.Unlock()
		common.GetAggregateModels() // 缓存按 raw 比对失效,主动刷一次
	})
}

// 聚合模型即使出现在令牌白名单里,也不该出现在 /v1/models。
//
// 这是隐藏这件事唯一需要显式过滤的出口:其余列表(模型广场 / GetEnabledModels /
// 本接口的非白名单路径 / 令牌页模型下拉)全部源自 abilities 表,而聚合模型没有渠道
// ability,天然不在里面;令牌白名单是唯一按模型名逐条存的地方。
//
// **本用例刻意直接注入 ContextKeyTokenModelLimit,而不是走 AddToken/UpdateToken**,
// 因为那条保存路径现在还存不进聚合模型名(validateTokenModelLimits 按 abilities 校验,
// 见 controller/model.go 过滤处的注释)。也就是说本用例现在覆盖的是一个**尚不可达**的
// 状态 —— 这是有意的:
//
//   - 下一步要让定向发放可用,必然把聚合模型并入 validateTokenModelLimits 的 available,
//     届时这个状态立刻变成真实路径,而本用例已经在守着它;
//   - 反过来,如果那时忘了这一层过滤,隐藏会在"刚刚打通白名单"的同一刻失效,
//     那是最难察觉的时机。
//
// 打通保存路径后,应把本用例改走 AddToken/UpdateToken,让校验与过滤落在同一条链路上。
// 同时钉住「同一份白名单里的普通模型必须保留」,避免过滤误伤。
func TestListModelsHidesAggregateModel(t *testing.T) {
	withSelfUseModeDisabled(t)
	withTieredBillingConfig(t, map[string]string{
		"zz-agg-normal-model": "tiered_expr",
		"zz-agg-hidden":       "tiered_expr",
		"zz-agg-public":       "tiered_expr",
	}, map[string]string{
		"zz-agg-normal-model": `tier("base", p * 1 + c * 2)`,
		"zz-agg-hidden":       `tier("base", p * 1 + c * 2)`,
		"zz-agg-public":       `tier("base", p * 1 + c * 2)`,
	})
	withAggregateModelConfig(t, `[
		{"name":"zz-agg-hidden","type":"video","enabled":true},
		{"name":"zz-agg-public","type":"video","enabled":true,"hidden":false}
	]`)

	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.Create(&model.User{
		Id:       2101,
		Username: "agg-token-user",
		Password: "password",
		Group:    "default",
		AffCode:  "agg-aff-2101",
		Status:   common.UserStatusEnabled,
	}).Error)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	ctx.Set("id", 2101)
	common.SetContextKey(ctx, constant.ContextKeyTokenModelLimitEnabled, true)
	common.SetContextKey(ctx, constant.ContextKeyTokenModelLimit, map[string]bool{
		"zz-agg-normal-model": true,
		"zz-agg-hidden":       true,
		"zz-agg-public":       true,
	})

	ListModels(ctx, constant.ChannelTypeOpenAI)

	ids := decodeListModelsResponse(t, recorder)
	require.NotContains(t, ids, "zz-agg-hidden",
		"隐藏的聚合模型即使在令牌白名单里也不该出现在 /v1/models")
	require.Contains(t, ids, "zz-agg-normal-model",
		"同一份白名单里的普通模型不该被连带滤掉")
	require.Contains(t, ids, "zz-agg-public",
		"显式 hidden:false 的聚合模型应正常展示")
}
