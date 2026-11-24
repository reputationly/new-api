package router

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAdminPricingGroupAndLogAudit(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.User{}, &model.Ability{}, &model.Log{}))
	prevDB, prevLogDB, prevOptions := model.DB, model.LOG_DB, common.OptionMap
	prevRatio, prevDescription := ratio_setting.ModelRatio2JSONString(), setting.GroupDescriptions2JSONString()
	prevTool := config.GlobalConfig.ExportAllConfigs()["tool_price_setting.prices"]
	model.DB, model.LOG_DB = db, db
	common.OptionMap = map[string]string{
		"ModelRatio": prevRatio, "GroupDescription": prevDescription, "tool_price_setting.prices": prevTool,
		"points_setting.enabled": "true", "NotAPricingSecret": "fixture-only",
	}
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(prevRatio))
		require.NoError(t, setting.UpdateGroupDescriptionsByJSONString(prevDescription))
		require.NoError(t, model.UpdateOption("tool_price_setting.prices", prevTool))
		model.DB, model.LOG_DB, common.OptionMap = prevDB, prevLogDB, prevOptions
		require.NoError(t, sqlDB.Close())
	})
	for i, role := range []int{common.RoleCommonUser, common.RoleAdminUser, common.RoleRootUser} {
		user := model.User{Id: i + 1, Username: fmt.Sprintf("actor-%d", i+1), Role: role, Status: common.UserStatusEnabled, AffCode: strconv.Itoa(i + 1)}
		user.SetAccessToken(fmt.Sprintf("fixture-token-%d", i+1))
		require.NoError(t, db.Create(&user).Error)
	}
	require.NoError(t, db.Create(&model.Ability{Model: "priced-model", Enabled: true}).Error)
	require.NoError(t, db.Create(&model.Option{Key: "ModelRatio", Value: `{"priced-model":0,"old-model":1}`}).Error)

	engine := gin.New()
	engine.Use(sessions.Sessions("pricing-test", cookie.NewStore([]byte("test-only-cookie"))))
	SetApiRouter(engine)
	request := func(id int, method, path, body string, success bool) string {
		t.Helper()
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("New-Api-User", strconv.Itoa(id))
		req.Header.Set("Authorization", fmt.Sprintf("Bearer fixture-token-%d", id))
		req.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(w, req)
		var result struct{ Success bool }
		require.NoError(t, common.Unmarshal(w.Body.Bytes(), &result), "%s %s: %s", method, path, w.Body.String())
		require.Equal(t, success, result.Success, "%s %s: %s", method, path, w.Body.String())
		return w.Body.String()
	}
	const pricing, groups = "/api/model_pricing/options", "/api/group/options"
	const change = `{"options":{"ModelRatio":"{\"priced-model\":2,\"new-model\":0}"}}`
	for _, path := range []string{pricing, groups} {
		request(1, http.MethodGet, path, "", false)
		request(1, http.MethodPost, path, change, false)
	}
	for _, id := range []int{2, 3} {
		body := request(id, http.MethodGet, pricing, "", true)
		require.Contains(t, body, "CompletionRatioMeta")
		require.Contains(t, body, `"enabled_models":["priced-model"]`)
		require.NotContains(t, body, "NotAPricingSecret")
		require.NotContains(t, body, "GroupDescription")
		body = request(id, http.MethodGet, groups, "", true)
		require.Contains(t, body, "points_setting.enabled")
		require.NotContains(t, body, "ModelRatio")
	}
	request(2, http.MethodGet, "/api/option/", "", false)
	request(2, http.MethodPut, "/api/option/", `{"key":"GroupDescription","value":"{}"}`, false)
	request(2, http.MethodPost, pricing, `{"options":{"GroupRatio":"{}"}}`, false)
	request(2, http.MethodPost, groups, `{"options":{"points_setting.enabled":"false"}}`, false)
	request(2, http.MethodPost, groups, `{"options":{"GroupDescription":"{\"test\":\"group\"}","GroupRatio":"{\"test\":-1}"}}`, false)
	request(2, http.MethodPost, pricing, `{"options":{"billing_setting.billing_mode":"{\"priced-model\":\"tiered_expr\"}"}}`, false)
	var count int64
	require.NoError(t, db.Model(&model.Log{}).Count(&count).Error)
	require.Zero(t, count)
	request(2, http.MethodPost, pricing, change, true)
	request(2, http.MethodPost, pricing, `{"options":{"ModelRatio":"{ \"new-model\": 0.0, \"priced-model\": 2 }"}}`, true)
	var initial model.Log
	require.NoError(t, db.First(&initial).Error)
	require.NoError(t, db.Model(&model.Log{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.Equal(t, "actor-2", initial.Username)
	require.Equal(t, model.LogTypeManage, initial.Type)
	require.Positive(t, initial.CreatedAt)
	require.Contains(t, initial.Other, `"admin_role":10`)
	require.Contains(t, initial.Other, `"path":["new-model"],"before":null,"after":0,"before_exists":false,"after_exists":true`)
	require.Contains(t, initial.Other, `"path":["old-model"],"before":1,"after":null,"before_exists":true,"after_exists":false`)
	require.Contains(t, initial.Other, `"path":["priced-model"],"before":0,"after":2,"before_exists":true,"after_exists":true`)
	request(2, http.MethodPost, groups, `{"options":{"GroupDescription":"{\"test\":\"group\"}"}}`, true)
	request(3, http.MethodPost, pricing, `{"options":{"tool_price_setting.prices":"{\"web_search\":0}"}}`, true)
	request(3, http.MethodPut, "/api/option/", `{"key":"GroupDescription","value":"{\"test\":\"root\"}"}`, true)
	request(3, http.MethodPost, "/api/option/pricing_batch", `{"options":{"ModelRatio":"{\"legacy-model\":1}"}}`, true)
	request(2, http.MethodPost, "/api/model_pricing/reset_model_ratio", "", true)
	request(3, http.MethodPost, pricing, change, true)
	request(3, http.MethodPost, "/api/option/rest_model_ratio", "", true)
	require.NoError(t, db.Model(&model.Log{}).Count(&count).Error)
	require.EqualValues(t, 8, count)

	require.NoError(t, db.Create(&model.Log{CreatedAt: 1, Type: model.LogTypeConsume}).Error)
	require.NoError(t, db.Create(&model.Log{CreatedAt: 1, Type: model.LogTypeManage}).Error)
	require.NoError(t, db.Create(&model.Log{CreatedAt: 2, Type: model.LogTypeError}).Error)
	const deletePath = "/api/log/?target_timestamp=2"
	request(2, http.MethodDelete, deletePath, "", false)
	require.Contains(t, request(3, http.MethodDelete, deletePath, "", true), `"data":1`)
	require.NoError(t, db.Model(&model.Log{}).Where("type = ?", model.LogTypeManage).Count(&count).Error)
	require.EqualValues(t, 10, count)
	var deletion model.Log
	require.NoError(t, db.Last(&deletion).Error)
	require.Equal(t, "actor-3", deletion.Username)
	require.Contains(t, deletion.Other, `"target_timestamp":2`)
	require.Contains(t, deletion.Other, `"deleted_count":1`)
	require.NoError(t, db.Model(&model.Log{}).Where("created_at = ?", 2).Count(&count).Error)
	require.EqualValues(t, 1, count)

	request(2, http.MethodPost, pricing, `{"options":{"ModelSourceRate":"6.8"}}`, true)
	request(3, http.MethodPost, pricing, `{"options":{"ModelSourceRate":"7"}}`, true)
	var sourceRateLog model.Log
	require.NoError(t, db.Last(&sourceRateLog).Error)
	require.Contains(t, sourceRateLog.Other, `"key":"ModelSourceRate"`)
	require.Contains(t, sourceRateLog.Other, `"before":6.8,"after":7`)
	require.Contains(t, sourceRateLog.Other, `"total_changes":1`)
	request(2, http.MethodPost, pricing, `{"options":{"billing_setting.billing_mode":"{\"priced-model\":\"tiered_expr\"}","billing_setting.billing_expr":"{\"priced-model\":\"v2:p * 2 * fx\"}","ModelPriceCurrency":"{\"priced-model\":\"USD\"}"}}`, true)
	request(2, http.MethodPost, pricing, `{"options":{"billing_setting.billing_expr":"{\"priced-model\":\"v2:p * 3 * fx\"}"}}`, true)
	var exprLog model.Log
	require.NoError(t, db.Last(&exprLog).Error)
	require.Contains(t, exprLog.Other, `"key":"billing_setting.billing_expr"`)
	require.Contains(t, exprLog.Other, `"before":"v2:p * 2 * fx","after":"v2:p * 3 * fx"`)

	var stored model.Option
	require.NoError(t, db.Migrator().DropTable(&model.Log{}))
	request(2, http.MethodPost, groups, `{"options":{"GroupDescription":"{}"}}`, false)
	require.NoError(t, db.First(&stored, "key = ?", "GroupDescription").Error)
	require.JSONEq(t, `{"test":"root"}`, stored.Value)
}
