package router

import (
	"testing"

	"github.com/gin-gonic/gin"
)

// /api/channel、/api/group、/api/user 各自被拆成了两个中间件不同的分组
// （管理员可读的共享接口 vs 仅超管的管理接口）。gin 的路由树对同前缀下静态段与
// 通配段混用会在注册期直接 panic，这里兜住启动即崩，并锁住拆分后没有漏注册路由。
// 注意：本测试只覆盖“路由存在”，不覆盖挂的是 AdminAuth 还是 RootAuth。
func TestSetApiRouterRegistersSplitGroups(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)

	registered := map[string]bool{}
	for _, r := range engine.Routes() {
		registered[r.Method+" "+r.Path] = true
	}

	for _, route := range []string{
		"GET /api/channel/",
		"POST /api/channel/",
		"GET /api/channel/search",
		"PUT /api/channel/",
		"DELETE /api/channel/:id",
		"GET /api/group/",
		"GET /api/group/overview",
		"GET /api/group/models",
		"GET /api/group/references",
		"POST /api/group/resolve",
		"GET /api/user/",
		"GET /api/user/search",
		"GET /api/user/:id",
		"GET /api/user/topup",
		"POST /api/user/manage",
		"DELETE /api/user/:id",
		"DELETE /api/user/:id/2fa",
		"GET /api/user/2fa/stats",
		"GET /api/user/kyc/admin",
		"GET /api/playground_admin/options",
		"PUT /api/playground_admin/option",
		"GET /api/subscription/admin/plans",
	} {
		if !registered[route] {
			t.Errorf("route not registered: %s", route)
		}
	}
}
