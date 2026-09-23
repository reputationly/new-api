package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 套餐价就是实付人民币。后端曾把 currency 强制写成 USD，而支付宝 / 微信直连下单前
// 要求 CNY——两头各自都「对」，接起来却是任何套餐都无法用直连购买。这里从建套餐一路
// 打到下单，锁住接线。

func callAdminPlanHandler(t *testing.T, h gin.HandlerFunc, method, idParam, body string) map[string]any {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(method, "/plans", bytes.NewBufferString(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	if idParam != "" {
		ctx.Params = gin.Params{{Key: "id", Value: idParam}}
	}
	h(ctx)
	var out map[string]any
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &out))
	return out
}

func setupPlanCurrencyTestDB(t *testing.T) {
	t.Helper()
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.SubscriptionPlan{}, &model.SubscriptionPlanEntitlement{},
		&model.SubscriptionOrder{}, &model.TopUp{}))
}

func TestAdminCreateSubscriptionPlan_CurrencyIsCNYAndAlipayAccepts(t *testing.T) {
	setupPlanCurrencyTestDB(t)
	enableAlipayForTest(t)

	out := callAdminPlanHandler(t, AdminCreateSubscriptionPlan, http.MethodPost, "",
		`{"plan":{"title":"专业版","price_amount":99,"currency":"USD","enabled":true}}`)
	require.Equal(t, true, out["success"], out["message"])
	id := int(out["data"].(map[string]any)["id"].(float64))

	plan, err := model.GetSubscriptionPlanById(id)
	require.NoError(t, err)
	require.Equal(t, "CNY", plan.Currency, "前端传什么都应落成 CNY")

	pay := callPayHandler(t, SubscriptionRequestAlipay, 4001, `{"plan_id": `+strconv.Itoa(id)+`}`)
	data, _ := pay["data"].(string)
	msg, _ := pay["message"].(string)
	require.NotContains(t, data+msg, "仅支持 CNY 套餐", "新建的套餐必须能走支付宝直连")
}

func TestAdminUpdateSubscriptionPlan_LegacyUSDPlanBecomesCNY(t *testing.T) {
	setupPlanCurrencyTestDB(t)

	legacy := model.SubscriptionPlan{Title: "旧套餐", PriceAmount: 10, Currency: "USD", Enabled: true}
	require.NoError(t, model.DB.Create(&legacy).Error)

	out := callAdminPlanHandler(t, AdminUpdateSubscriptionPlan, http.MethodPut, strconv.Itoa(legacy.Id),
		`{"plan":{"title":"旧套餐","price_amount":10,"currency":"USD","enabled":true}}`)
	require.Equal(t, true, out["success"], out["message"])

	model.InvalidateSubscriptionPlanCache(legacy.Id)
	plan, err := model.GetSubscriptionPlanById(legacy.Id)
	require.NoError(t, err)
	require.Equal(t, "CNY", plan.Currency, "编辑保存一次后旧套餐也应转成 CNY")
}
