package router

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"

	"github.com/gin-gonic/gin"
)

func SetRouter(router *gin.Engine, assets ThemeAssets) {
	// 站点根目录验证文件必须最先挂载：早于全局限流 / gzip / 移动端重定向中间件注册，
	// 这些路由的中间件链才是干净的。理由详见 wellknown-router.go 的注释。
	SetWellKnownRouter(router)
	SetApiRouter(router)
	SetDashboardRouter(router)
	SetRelayRouter(router)
	SetVideoRouter(router)
	// 画布静态应用必须早于 FRONTEND_BASE_URL 分支挂载：
	// 外置前端部署下 /canvas-app/* 仍由 Go 单二进制伺服
	SetCanvasRouter(router, assets)
	// 移动端 H5 同理：/m/* 永远由 Go 单二进制内置伺服
	SetMobileRouter(router, assets)
	// 免登录分享落地页同理：/s/* 由 Go 直出，且必须早于 SetWebRouter 的 SPA fallback
	SetShareRouter(router)
	frontendBaseUrl := os.Getenv("FRONTEND_BASE_URL")
	if common.IsMasterNode && frontendBaseUrl != "" {
		frontendBaseUrl = ""
		common.SysLog("FRONTEND_BASE_URL is ignored on master node")
	}
	if frontendBaseUrl == "" {
		SetWebRouter(router, assets)
	} else {
		frontendBaseUrl = strings.TrimSuffix(frontendBaseUrl, "/")
		router.NoRoute(func(c *gin.Context) {
			c.Set(middleware.RouteTagKey, "web")
			c.Redirect(http.StatusMovedPermanently, fmt.Sprintf("%s%s", frontendBaseUrl, c.Request.RequestURI))
		})
	}
}
