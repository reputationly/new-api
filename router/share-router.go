package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"

	"github.com/gin-gonic/gin"
)

// SetShareRouter 挂载免登录分享落地页 /s/*。
//
// 必须在 SetWebRouter 之前注册：后者的 NoRoute 会把所有非 /v1 /api /pg /assets
// 的路径吞进 SPA 的 index.html，/s/xxx 会变成一个白页而不是落地页。
// 与画布、移动端同理，也要早于 FRONTEND_BASE_URL 分支——外置前端部署下分享链接
// 仍由 Go 单二进制伺服，否则会被 301 到前端站点而丢掉 token 语义。
//
// 全部公开访问：分享链接的意义就是收到的人不需要有账号。鉴权在签发侧
// （controller.CreateTaskShareLink 按 user_id 查库），此处只认 token 签名。
func SetShareRouter(router *gin.Engine) {
	shareRouter := router.Group("/s")
	shareRouter.Use(middleware.RouteTag("web"))
	// token 是 HMAC 签名的，爆破不现实，但公开端点仍要限流兜住扫描流量。
	shareRouter.Use(middleware.DownloadRateLimit())
	{
		shareRouter.GET("/:token", controller.ShareLandingPage)
		shareRouter.GET("/:token/content", controller.ShareContent)
	}
}
