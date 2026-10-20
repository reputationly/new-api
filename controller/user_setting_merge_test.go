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

func callUserHandlerJSON(t *testing.T, handler func(*gin.Context), userId int, method, path, body string) map[string]any {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(method, path, strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Set("id", userId)
	handler(ctx)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp map[string]any
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &resp))
	return resp
}

func callUpdateUserSetting(t *testing.T, userId int, body string) map[string]any {
	t.Helper()
	return callUserHandlerJSON(t, UpdateUserSetting, userId, http.MethodPut, "/api/user/setting", body)
}

func loadUserSetting(t *testing.T, userId int) dto.UserSetting {
	t.Helper()
	var user model.User
	require.NoError(t, model.DB.First(&user, "id = ?", userId).Error)
	return user.GetSetting()
}

func seedRawUserSetting(t *testing.T, userId int, setting string) {
	t.Helper()
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", userId).
		Update("setting", setting).Error)
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

// ---- Webhook 密钥的三态：没传 / 传空 / 传新值 ----
//
// 前端会把已存的密钥回显进表单再原样回传，所以「不传」只发生在直接调 API
// 的客户端；「传空」才是用户主动清空。两者必须区分开，否则密钥要么永远
// 清不掉（上一版的回归），要么每次保存都被抹掉。

func TestUpdateUserSettingKeepsWebhookSecretWhenOmitted(t *testing.T) {
	db := setupExportLimitDB(t)
	seedExportLimitUser(t, db, 9713, "setting_secret_keep")
	seedRawUserSetting(t, 9713,
		`{"notify_type":"webhook","webhook_url":"https://example.com/hook","webhook_secret":"s3cret"}`)

	callUpdateUserSetting(t, 9713,
		`{"notify_type":"webhook","quota_warning_threshold":100,"webhook_url":"https://example.com/hook"}`)

	require.Equal(t, "s3cret", loadUserSetting(t, 9713).WebhookSecret)
}

func TestUpdateUserSettingCanClearWebhookSecret(t *testing.T) {
	db := setupExportLimitDB(t)
	seedExportLimitUser(t, db, 9714, "setting_secret_clear")
	seedRawUserSetting(t, 9714,
		`{"notify_type":"webhook","webhook_url":"https://example.com/hook","webhook_secret":"s3cret"}`)

	callUpdateUserSetting(t, 9714,
		`{"notify_type":"webhook","quota_warning_threshold":100,"webhook_url":"https://example.com/hook","webhook_secret":""}`)

	require.Empty(t, loadUserSetting(t, 9714).WebhookSecret)
}

func TestUpdateUserSettingUpdatesWebhookSecret(t *testing.T) {
	db := setupExportLimitDB(t)
	seedExportLimitUser(t, db, 9715, "setting_secret_update")
	seedRawUserSetting(t, 9715,
		`{"notify_type":"webhook","webhook_url":"https://example.com/hook","webhook_secret":"s3cret"}`)

	callUpdateUserSetting(t, 9715,
		`{"notify_type":"webhook","quota_warning_threshold":100,"webhook_url":"https://example.com/hook","webhook_secret":"new-secret"}`)

	require.Equal(t, "new-secret", loadUserSetting(t, 9715).WebhookSecret)
}

// ---- 另外两个写 users.setting 的入口：只改自己那几个字段 ----

func TestUpdateSelfPreservesNotificationSettings(t *testing.T) {
	db := setupExportLimitDB(t)
	seedExportLimitUser(t, db, 9721, "setting_sidebar_user")
	seedRawUserSetting(t, 9721,
		`{"notify_type":"email","notification_email":"u@example.com","webhook_secret":"s3cret","language":"en"}`)

	callUserHandlerJSON(t, UpdateSelf, 9721, http.MethodPut, "/api/user/self",
		`{"sidebar_modules":"{\"chat\":{\"enabled\":false}}"}`)

	got := loadUserSetting(t, 9721)
	require.Equal(t, `{"chat":{"enabled":false}}`, got.SidebarModules)
	require.Equal(t, "u@example.com", got.NotificationEmail)
	require.Equal(t, "s3cret", got.WebhookSecret)
	require.Equal(t, "en", got.Language)
}

func TestUpdateBillingPreferencePreservesOtherSettings(t *testing.T) {
	db := setupExportLimitDB(t)
	seedExportLimitUser(t, db, 9722, "setting_billing_user")
	seedRawUserSetting(t, 9722,
		`{"notify_type":"email","notification_email":"u@example.com","sidebar_modules":"{\"chat\":{\"enabled\":false}}"}`)

	callUserHandlerJSON(t, UpdateSubscriptionPreference, 9722, http.MethodPut,
		"/api/user/billing-preference", `{"billing_preference":"wallet_first"}`)

	got := loadUserSetting(t, 9722)
	require.Equal(t, "wallet_first", got.BillingPreference)
	require.Equal(t, "u@example.com", got.NotificationEmail)
	require.Equal(t, `{"chat":{"enabled":false}}`, got.SidebarModules)
}
