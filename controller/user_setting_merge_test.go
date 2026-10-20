package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 保存通知配置只能改通知相关的字段，不能把同一个 JSON 列里别人存的
// sidebar_modules / billing_preference / language 一起冲掉。

func callUpdateUserSetting(t *testing.T, userId int, body string) map[string]any {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/user/setting", strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Set("id", userId)
	UpdateUserSetting(ctx)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp map[string]any
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &resp))
	return resp
}

func loadUserSetting(t *testing.T, userId int) dto.UserSetting {
	t.Helper()
	var user model.User
	require.NoError(t, model.DB.First(&user, "id = ?", userId).Error)
	return user.GetSetting()
}

func TestUpdateUserSettingPreservesUnrelatedKeys(t *testing.T) {
	db := setupExportLimitDB(t)
	seedExportLimitUser(t, db, 9711, "setting_merge_user")
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", 9711).Update("setting", `{
		"notify_type": "webhook",
		"webhook_url": "https://example.com/hook",
		"webhook_secret": "s3cret",
		"notification_email": "old@example.com",
		"sidebar_modules": "{\"chat\":{\"enabled\":false}}",
		"billing_preference": "wallet_first",
		"language": "en"
	}`).Error)

	resp := callUpdateUserSetting(t, 9711,
		`{"notify_type":"email","quota_warning_threshold":100,"notification_email":"new@example.com"}`)
	require.Equal(t, true, resp["success"])

	got := loadUserSetting(t, 9711)

	// 本接口不认识的三个字段必须原样保留。
	require.Equal(t, `{"chat":{"enabled":false}}`, got.SidebarModules)
	require.Equal(t, "wallet_first", got.BillingPreference)
	require.Equal(t, "en", got.Language)

	// 换成邮件通知后，webhook 那套配置不能被顺手清掉。
	require.Equal(t, "https://example.com/hook", got.WebhookUrl)
	require.Equal(t, "s3cret", got.WebhookSecret)

	// 本次提交的字段生效。
	require.Equal(t, dto.NotifyTypeEmail, got.NotifyType)
	require.Equal(t, 100.0, got.QuotaWarningThreshold)
	require.Equal(t, "new@example.com", got.NotificationEmail)
}

// 邮箱留空是「改用账号绑定的邮箱」，不是「保留上一次填的地址」。
func TestUpdateUserSettingCanClearNotificationEmail(t *testing.T) {
	db := setupExportLimitDB(t)
	seedExportLimitUser(t, db, 9712, "setting_clear_email")
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", 9712).
		Update("setting", `{"notify_type":"email","notification_email":"old@example.com"}`).Error)

	callUpdateUserSetting(t, 9712,
		`{"notify_type":"email","quota_warning_threshold":100,"notification_email":""}`)

	require.Empty(t, loadUserSetting(t, 9712).NotificationEmail)
}
