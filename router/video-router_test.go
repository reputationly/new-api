package router

import (
	"testing"

	"github.com/gin-gonic/gin"
)

// 视频路由要同时提供三套协议：本仓原生（/v1/video/generations）、OpenAI 兼容
// （/v1/videos）、MiniMax v2 官方兼容（/v2/...）。加 v2 那组时最容易出的事故是
// 与既有路径冲突——gin 在注册期就会 panic，所以「能注册完」本身就是断言。
func TestSetVideoRouterRegistersAllProtocols(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetVideoRouter(engine)

	registered := map[string]bool{}
	for _, r := range engine.Routes() {
		registered[r.Method+" "+r.Path] = true
	}

	want := []string{
		// 原生
		"POST /v1/video/generations",
		"GET /v1/video/generations/:task_id",
		"POST /v1/videos/:video_id/remix",
		// OpenAI 兼容
		"POST /v1/videos",
		"GET /v1/videos/:task_id",
		"GET /v1/videos/:task_id/content",
		// MiniMax v2 官方兼容
		"POST /v2/video_generation",
		"GET /v2/query/video_generation",
		"GET /v2/query/video_generation/:task_id",
		"DELETE /v2/video_generation/:task_id",
		"POST /v2/video_regeneration",
		"POST /v2/h3_context_ir",
	}
	for _, route := range want {
		if !registered[route] {
			t.Errorf("route %s is not registered", route)
		}
	}
}
