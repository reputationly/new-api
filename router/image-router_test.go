package router

import (
	"testing"

	"github.com/gin-gonic/gin"
)

// 图片端点要同时提供同步与异步两套模式（docs/image-async-task-design.md）。
// 异步化时把两条 POST 从 httpRouter 移到了独立分组（转换中间件必须跑在 Distribute
// 之前，而 gin 的分组中间件先于路由中间件执行）。这个搬迁最容易出两种事故：
//  1. 忘了从 httpRouter 删掉旧的注册 → gin 在注册期直接 panic；
//  2. 搬迁时漏掉一条 → 某个端点静默 404。
//
// 「能注册完 + 路径齐全」本身就是断言。
func TestSetRelayRouterRegistersImageRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetRelayRouter(engine)

	registered := map[string]bool{}
	for _, r := range engine.Routes() {
		registered[r.Method+" "+r.Path] = true
	}

	want := []string{
		// 同步（原有，不能因为搬迁而丢失）
		"POST /v1/images/generations",
		"POST /v1/images/edits",
		// 异步（新增）
		"GET /v1/images/generations/:task_id",
		"DELETE /v1/images/generations/:task_id",
		// 体验区同步端点不受影响
		"POST /pg/images/generations",
		"POST /pg/images/edits",
	}
	for _, route := range want {
		if !registered[route] {
			t.Errorf("route %s is not registered", route)
		}
	}
}
