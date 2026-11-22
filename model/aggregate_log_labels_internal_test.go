package model

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/constant"
)

// 提示词增强走 /v1/chat/completions(普通同步 chat),请求里没有聚合展开,
// context key 必然为空 —— 只读 context 的话那条日志就没有任何聚合标签。
func TestAggregateLabelsForLogReadsHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Request.Header.Set(constant.AggregateStageHeader, "enhance")
	c.Request.Header.Set(constant.AggregatePublicModelHeader, "minimax-h3-2k")

	labels := aggregateLabelsForLog(c)
	require.Equal(t, "enhance", labels["pipeline_stage"])
	require.Equal(t, "minimax-h3-2k", labels["aggregate_model"])
}

// context key 优先:展开过的请求不能被子任务的头覆盖。
func TestAggregateLabelsForLogPrefersContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
	c.Set(string(constant.ContextKeyAggregateLabels), map[string]string{
		"aggregate_model": "minimax-h3-2k",
		"pipeline_stage":  "generate",
	})
	c.Request.Header.Set(constant.AggregateStageHeader, "upscale")

	labels := aggregateLabelsForLog(c)
	require.Equal(t, "generate", labels["pipeline_stage"])
}

func TestAggregateLabelsForLogPlainRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	require.Nil(t, aggregateLabelsForLog(c))
}
