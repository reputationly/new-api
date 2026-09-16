package router

import (
	"testing"

	"github.com/gin-gonic/gin"
)

// 视频路由要同时提供四套协议：本仓原生（/v1/video/generations）、OpenAI 兼容
// （/v1/videos）、MiniMax v2 官方兼容（/v2/...）、火山方舟 v3 官方兼容
// （/api/v3/contents/generations/tasks）。加新的一组时最容易出的事故是与既有路径
// 冲突——gin 在注册期就会 panic，所以「能注册完」本身就是断言。
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
		"DELETE /v1/videos/:task_id",
		"GET /v1/videos/:task_id/content",
		// 火山方舟 v3 官方兼容
		"POST /api/v3/contents/generations/tasks",
		"GET /api/v3/contents/generations/tasks",
		"GET /api/v3/contents/generations/tasks/:task_id",
		"DELETE /api/v3/contents/generations/tasks/:task_id",
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

// 方舟兼容层把路由挂在 /api/v3/... 下，而 /api 这个前缀底下已经有两组别人的路由：
// SetApiRouter（/api/user/:id 这类参数路由）与 SetHiloRouter（/api/v1、/api/v2）。
// gin 的 httprouter 在同一层混用静态段与参数段时会 panic，而且是**注册期**panic
// —— 也就是说冲突了的话服务根本起不来，单测里 SetVideoRouter 自己注册是看不出来的。
// 这里按真实顺序把三组一起注册，「能注册完」就是断言。
func TestVideoRouterCoexistsWithOtherApiGroups(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()

	SetApiRouter(engine)
	SetVideoRouter(engine)
	SetHiloRouter(engine)

	registered := map[string]bool{}
	for _, r := range engine.Routes() {
		registered[r.Method+" "+r.Path] = true
	}
	for _, route := range []string{
		"POST /api/v3/contents/generations/tasks",
		"GET /api/v3/contents/generations/tasks/:task_id",
	} {
		if !registered[route] {
			t.Errorf("route %s is not registered after the other /api groups", route)
		}
	}
}
