package model

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 消费 / 错误日志无条件记录客户端 IP:不再读用户设置,也没有任何开关能关掉。
// 请求上下文用 httptest.NewRequest 构造,RemoteAddr 是 192.0.2.1:1234。

func newLogRecordContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c
}

func TestRecordConsumeLogAlwaysRecordsClientIp(t *testing.T) {
	truncateTables(t)
	c := newLogRecordContext()

	RecordConsumeLog(c, 9501, RecordConsumeLogParams{
		ModelName: "ip-test-model",
		Quota:     1,
		Other:     map[string]interface{}{},
	})

	var got Log
	require.NoError(t, LOG_DB.Order("id desc").First(&got).Error)
	require.Equal(t, LogTypeConsume, got.Type)
	require.Equal(t, "192.0.2.1", got.Ip)
}

func TestRecordErrorLogAlwaysRecordsClientIp(t *testing.T) {
	truncateTables(t)
	c := newLogRecordContext()

	RecordErrorLog(c, 9502, 1, "ip-test-model", "ip-test-token", "upstream boom",
		1, 1, false, "default", map[string]interface{}{})

	var got Log
	require.NoError(t, LOG_DB.Order("id desc").First(&got).Error)
	require.Equal(t, LogTypeError, got.Type)
	require.Equal(t, "192.0.2.1", got.Ip)
}
