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

// 两个用户级开关已被删除：请求体里再怎么带，也不该生效、更不该入库。
// 存量 JSON 里已经开着的值，会在下一次保存时自然消失。
func TestUpdateUserSettingIgnoresRemovedFields(t *testing.T) {
	db := setupExportLimitDB(t)
	seedExportLimitUser(t, db, 9701, "setting_removed_fields")
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", 9701).
		Update("setting", `{"accept_unset_model_ratio_model":true,"record_ip_log":true}`).Error)

	body := `{
		"notify_type": "email",
		"quota_warning_threshold": 100,
		"notification_email": "u@example.com",
		"accept_unset_model_ratio_model": true,
		"record_ip_log": true
	}`

	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/user/setting", strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Set("id", 9701)

	UpdateUserSetting(ctx)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp map[string]any
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, true, resp["success"])

	var user model.User
	require.NoError(t, db.First(&user, "id = ?", 9701).Error)
	require.NotContains(t, user.Setting, "accept_unset_model_ratio_model")
	require.NotContains(t, user.Setting, "record_ip_log")
	// 合法字段照常写入，别把整个设置保存改坏。
	require.Contains(t, user.Setting, "notification_email")
}
