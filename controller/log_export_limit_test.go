package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupExportLimitDB 起一个内存 SQLite，迁移 User 与 Log 表，并把 DB / LOG_DB 指向它。
func setupExportLimitDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	prevDB, prevLogDB := model.DB, model.LOG_DB
	model.DB = db
	model.LOG_DB = db
	// 用户缓存走 Redis 时会读 common.RDB；测试里关掉 Redis，直接回落到数据库。
	prevRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() {
		common.RedisEnabled = prevRedis
		model.DB, model.LOG_DB = prevDB, prevLogDB
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Log{}))
	return db
}

func seedExportLimitUser(t *testing.T, db *gorm.DB, id int, username string) {
	t.Helper()
	require.NoError(t, db.Create(&model.User{
		Id:       id,
		Username: username,
		Password: "hashed-placeholder",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}).Error)
}

func seedExportLimitLogs(t *testing.T, db *gorm.DB, userId int, username string, n int) {
	t.Helper()
	rows := make([]*model.Log, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, &model.Log{
			UserId:    userId,
			Username:  username,
			CreatedAt: 1790092800 + int64(i),
			Type:      model.LogTypeConsume,
			Quota:     1,
		})
	}
	require.NoError(t, db.CreateInBatches(rows, 500).Error)
}

// callExportHandler 以 userId 身份调用导出 handler，返回响应记录。
func callExportHandler(t *testing.T, handler func(*gin.Context), userId int, rawQuery string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/log/export?"+rawQuery, nil)
	ctx.Set("id", userId)
	handler(ctx)
	return rec
}

func setExportLimitForTest(t *testing.T, limit int) {
	t.Helper()
	prev := logExportMaxRows
	logExportMaxRows = limit
	t.Cleanup(func() { logExportMaxRows = prev })
}

func TestExportUserLogs_OverLimitReturnsJSONCode(t *testing.T) {
	db := setupExportLimitDB(t)
	seedExportLimitUser(t, db, 9301, "export_limit_user")
	seedExportLimitLogs(t, db, 9301, "export_limit_user", 6)
	setExportLimitForTest(t, 5)

	rec := callExportHandler(t, ExportUserLogs, 9301, "type=0")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "application/json")

	var resp map[string]any
	require.NoError(t, common.UnmarshalJsonStr(rec.Body.String(), &resp))
	require.Equal(t, false, resp["success"])
	require.Equal(t, logExportLimitCode, resp["code"])
	msg, _ := resp["message"].(string)
	require.Contains(t, msg, "5")
}

func TestExportUserLogs_WithinLimitStreamsCSV(t *testing.T) {
	db := setupExportLimitDB(t)
	seedExportLimitUser(t, db, 9302, "export_within_user")
	seedExportLimitLogs(t, db, 9302, "export_within_user", 6)
	setExportLimitForTest(t, 10)

	rec := callExportHandler(t, ExportUserLogs, 9302, "type=0")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "text/csv")
	// 表头 + 6 行数据，每行以换行结束
	require.Equal(t, 7, strings.Count(rec.Body.String(), "\n"))
}

func TestExportUserLogs_ConcurrentExportRejected(t *testing.T) {
	db := setupExportLimitDB(t)
	seedExportLimitUser(t, db, 9303, "export_busy_user")
	seedExportLimitLogs(t, db, 9303, "export_busy_user", 1)

	// 模拟该用户已有一个导出在进行
	require.True(t, acquireLogExport(9303))
	t.Cleanup(func() { releaseLogExport(9303) })

	rec := callExportHandler(t, ExportUserLogs, 9303, "type=0")
	var resp map[string]any
	require.NoError(t, common.UnmarshalJsonStr(rec.Body.String(), &resp))
	require.Equal(t, false, resp["success"])
	require.Nil(t, resp["code"], "忙碌不是超限，不应带超限的 code")
	msg, _ := resp["message"].(string)
	require.Contains(t, msg, "正在进行")
}

func TestExportAllLogs_OverLimitReturnsJSONCode(t *testing.T) {
	db := setupExportLimitDB(t)
	seedExportLimitUser(t, db, 9304, "export_admin_user")
	seedExportLimitLogs(t, db, 9304, "export_admin_user", 6)
	setExportLimitForTest(t, 5)

	rec := callExportHandler(t, ExportAllLogs, 9304, "type=0")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "application/json")

	var resp map[string]any
	require.NoError(t, common.UnmarshalJsonStr(rec.Body.String(), &resp))
	require.Equal(t, false, resp["success"])
	require.Equal(t, logExportLimitCode, resp["code"])
}
