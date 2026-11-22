package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 增强段的自调用**没有父任务**(它跑在生成段任务落库之前),标签仍然要能生成 ——
// 否则使用日志里那条 qwen 调用永远标不上所属流水线,只能靠时间戳猜。
func TestPipelineLogLabelsEnhanceWithoutParent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Request.Header.Set(AggregateStageHeader, LogStageEnhance)
	c.Request.Header.Set(AggregatePublicModelHeader, "minimax-h3-2k")

	labels := PipelineLogLabels(c, "")
	require.Equal(t, LogStageEnhance, labels[LogKeyPipelineStage])
	require.Equal(t, "minimax-h3-2k", labels[LogKeyAggregateModel])
	require.NotContains(t, labels, LogKeyAggregateTaskID)
}

// 超分段仍然要求父任务:没有父任务无法归因,宁可不标。
func TestPipelineLogLabelsUpscaleNeedsParent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
	c.Request.Header.Set(AggregateStageHeader, LogStageUpscale)
	require.Nil(t, PipelineLogLabels(c, ""))

	c.Request.Header.Set(AggregateParentTaskHeader, "task_parent")
	labels := PipelineLogLabels(c, "")
	require.Equal(t, LogStageUpscale, labels[LogKeyPipelineStage])
	require.Equal(t, "task_parent", labels[LogKeyAggregateTaskID])
}
