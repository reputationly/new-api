package router

import (
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// B6 / B7（设计文档 §7.1 的「删除项」）：上游倍率同步与「暴露倍率」接口都已下线，
// 路由表里不得再出现它们。断言路由表而不是发请求：API 组挂着限流等全局中间件，
// 路由表的判定与此无关且可复现。
func TestRemovedRatioRoutesAreNotRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)

	registered := map[string]bool{}
	for _, r := range engine.Routes() {
		registered[r.Method+" "+r.Path] = true
	}

	// 前置：路由确实注册进来了（否则下面的「不存在」是空证据）
	require.True(t, registered["GET /api/pricing"], "前置：/api/pricing 应在路由表里")

	for _, path := range []string{
		"/api/ratio_config",
		"/api/ratio_sync/channels",
		"/api/ratio_sync/fetch",
		"/api/ratio_sync/models",
	} {
		for _, method := range []string{"GET", "POST", "PUT"} {
			require.Falsef(t, registered[method+" "+path], "%s %s 必须已删除", method, path)
		}
	}
}

// 兜底断言：任何残留的 ratio_sync / ratio_config 路由（含前缀或参数形态）都不该存在。
func TestNoRatioSyncOrConfigRoutePrefix(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)

	for _, r := range engine.Routes() {
		require.Falsef(t, strings.Contains(r.Path, "ratio_sync"),
			"残留路由：%s %s", r.Method, r.Path)
		require.Falsef(t, strings.Contains(r.Path, "ratio_config"),
			"残留路由：%s %s", r.Method, r.Path)
	}
}
