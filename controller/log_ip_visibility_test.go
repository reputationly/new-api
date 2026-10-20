package controller

import (
	"bytes"
	"encoding/csv"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 使用日志的 IP 只给管理员看：普通用户的三个出口（列表 / 导出 / 令牌查询）都不带 IP，
// 管理员的列表与导出照常带。

const (
	userSideIp  = "203.0.113.7"
	adminSideIp = "198.51.100.7"
)

// parseCSVBody 去掉 UTF-8 BOM 后把整段 CSV 解析成二维切片。
func parseCSVBody(t *testing.T, body []byte) [][]string {
	t.Helper()
	trimmed := bytes.TrimPrefix(body, []byte{0xEF, 0xBB, 0xBF})
	records, err := csv.NewReader(bytes.NewReader(trimmed)).ReadAll()
	require.NoError(t, err)
	return records
}

func seedLogWithIp(t *testing.T, userId int, username string, ip string) {
	t.Helper()
	require.NoError(t, model.LOG_DB.Create(&model.Log{
		UserId:    userId,
		Username:  username,
		CreatedAt: 1790092800,
		Type:      model.LogTypeConsume,
		Quota:     1,
		Ip:        ip,
	}).Error)
}

// TestExportUserLogsHasNoIpColumn：普通用户导出的表头里没有 IP，正文里也没有任何 IP 字符串，
// 并且表头列数与每行列数严格一致（否则 Excel 打开会串列）。
func TestExportUserLogsHasNoIpColumn(t *testing.T) {
	db := setupExportLimitDB(t)
	seedExportLimitUser(t, db, 9601, "ip_export_user")
	seedLogWithIp(t, 9601, "ip_export_user", userSideIp)

	rec := callExportHandler(t, ExportUserLogs, 9601, "type=0")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "text/csv")

	records := parseCSVBody(t, rec.Body.Bytes())
	require.Len(t, records, 2, "表头 + 1 行数据")

	header := records[0]
	require.NotContains(t, header, "IP")
	for i, row := range records {
		require.Len(t, row, len(header), "第 %d 行的列数与表头不一致", i)
		require.NotContains(t, row, userSideIp)
	}
	require.NotContains(t, rec.Body.String(), userSideIp, "整个响应体里都不该出现 IP")
}

// TestExportAllLogsKeepsIpColumnForAdmins：管理员导出保持 IP 列，且值落在正确的列上。
func TestExportAllLogsKeepsIpColumnForAdmins(t *testing.T) {
	db := setupExportLimitDB(t)
	seedExportLimitUser(t, db, 9602, "ip_export_admin")
	seedLogWithIp(t, 9602, "ip_export_admin", adminSideIp)

	rec := callExportHandler(t, ExportAllLogs, 9602, "type=0")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "text/csv")

	records := parseCSVBody(t, rec.Body.Bytes())
	require.Len(t, records, 2)

	header := records[0]
	ipIndex := -1
	for i, name := range header {
		if name == "IP" {
			ipIndex = i
		}
	}
	require.NotEqual(t, -1, ipIndex, "管理员导出的表头必须有 IP 列")

	for i, row := range records {
		require.Len(t, row, len(header), "第 %d 行的列数与表头不一致", i)
	}
	require.Equal(t, adminSideIp, records[1][ipIndex])
}

func callLogListHandler(t *testing.T, handler func(*gin.Context), userId int, path string) map[string]any {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, path, nil)
	ctx.Set("id", userId)
	handler(ctx)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp map[string]any
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, true, resp["success"])
	return resp
}

// TestGetUserLogsHandlerHidesIp：普通用户列表接口的 items 里连 ip 这个键都没有。
func TestGetUserLogsHandlerHidesIp(t *testing.T) {
	db := setupExportLimitDB(t)
	seedExportLimitUser(t, db, 9603, "ip_list_user")
	seedLogWithIp(t, 9603, "ip_list_user", userSideIp)

	resp := callLogListHandler(t, GetUserLogs, 9603, "/api/log/self?p=1&page_size=10")

	data, ok := resp["data"].(map[string]any)
	require.True(t, ok)
	items, ok := data["items"].([]any)
	require.True(t, ok)
	require.Len(t, items, 1)

	item, ok := items[0].(map[string]any)
	require.True(t, ok)
	_, hasIp := item["ip"]
	require.False(t, hasIp, "普通用户的日志响应里不该有 ip 键")
}

// TestGetAllLogsHandlerKeepsIp：管理员列表接口照常返回 IP。
func TestGetAllLogsHandlerKeepsIp(t *testing.T) {
	db := setupExportLimitDB(t)
	seedExportLimitUser(t, db, 9604, "ip_list_admin")
	seedLogWithIp(t, 9604, "ip_list_admin", adminSideIp)

	resp := callLogListHandler(t, GetAllLogs, 9604, "/api/log/?p=1&page_size=10")

	data, ok := resp["data"].(map[string]any)
	require.True(t, ok)
	items, ok := data["items"].([]any)
	require.True(t, ok)
	require.Len(t, items, 1)

	item, ok := items[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, adminSideIp, item["ip"])
}
