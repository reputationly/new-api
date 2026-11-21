package controller

import (
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/service"
)

// 异步任务(视频/音乐)的聚合记录必须与同步生图**同一份实现**。
//
// 这两处曾经各有一份手抄的转换代码:2026-11-21 加 enhance_fell_back 时只改了
// middleware 那份,而异步走的恰恰是 controller 这份 —— 于是出现回落了、
// fallback_reason 有值、fell_back 却是 false。这条测试钉住“委托”这件事。
func TestBuildTaskAggregateInfoDelegates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)

	exp := &middleware.AggregateExpansion{
		PublicName: "h3-ref-2k",
		Enhance: &service.EnhanceResult{
			Model:          "qwen3.8-flash-fp8",
			OriginalPrompt: "p",
			EnhancedPrompt: "p2",
			Mode:           "text",
			FallbackReason: "传输检查未通过: Shot times must be numeric",
		},
	}
	info := buildTaskAggregateInfo(c, exp)
	require.NotNil(t, info)
	require.Equal(t, "h3-ref-2k", info.PublicModel)
	require.Equal(t, "p2", info.EnhancedPrompt)
	require.True(t, info.EnhanceFellBack, "回落位必须来自同一实现")
	require.False(t, info.EnhanceDegraded)
}
