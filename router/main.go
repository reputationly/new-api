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
	// 健康探针最先挂载，理由同 WellKnownRouter：gin 注册路由时会把当前的全局中间件链
	// 快照进去，早于限流 / gzip 注册，/healthz 才不会被限流拒掉而让 K8s 误判实例不健康。
	SetHealthRouter(router)
	// 站点根目录验证文件必须最先挂载：早于全局限流 / gzip / 移动端重定向中间件注册，
	// 这些路由的中间件链才是干净的。理由详见 wellknown-router.go 的注释。
	SetWellKnownRouter(router)
	SetApiRouter(router)
	SetDashboardRouter(router)
	SetRelayRouter(router)
	SetVideoRouter(router)
	// MiniMax Design 客户端的云侧接口。必须在 FRONTEND_BASE_URL 判断之前:
	// 它服务的是本地 gateway,不能被重定向到外置前端。
	SetHiloRouter(router)
	// `/v1/ov/*`：我们自己的能力目录与平台服务（非模型调用）。
	// 与 SetHiloRouter 同一处位置的理由：必须在 web 兜底之前，否则会落进
	// NoRoute 返回 `Invalid URL`。
	SetOvRouter(router)
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
