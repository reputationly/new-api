package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"

	"github.com/gin-gonic/gin"
)

// SetOvRouter 挂载 `/v1/ov/*` —— 非模型能力的新命名空间。
//
// # 与 `/v1`（relay）的分工
//
// `/v1/models`、`/v1/images/*`、`/v1/videos`、`/v1/chat/completions` 是**模型调用**，
// 走 relay：选渠道、计费、日志、重试。`/v1/ov/*` 是**平台服务**：能力目录、文件上传，
// 以及后续的搜索/市场/OAuth。它们没有渠道可选、不计 token，所以：
//
//	中间件      RouteTag("ov") + TokenAuth + KYCRequired + 独立限流桶
//	不挂        Distribute（没有渠道可选）
//
// 鉴权沿用 relay 的令牌。客户端只有一个 base_url 加一个 key，不另立一套凭证。
//
// # 挂载位置
//
// 必须在 `SetWebRouter` 之前（见 router/main.go）：晚了会落到 SPA 兜底，返回
// `Invalid URL`，看起来像路由配错了。
//
// # 为什么限流是独立的一个桶
//
// 能力目录会被客户端轮询，桶的 key 是 `mark + ClientIP`。与 dashboard/user API 共用
// "GA" 的话，同一出口 IP 下的浏览器正常请求会被挤成 429（同 HiloRateLimit 的理由）。
func SetOvRouter(router *gin.Engine) {
	ov := router.Group("/v1/ov")
	ov.Use(middleware.RouteTag("ov"))
	ov.Use(middleware.OvRateLimit())
	// 与 `/v1` 保持一致：令牌 → KYC。KYC 没通过时目录也不该给（给了也调不通）。
	ov.Use(middleware.TokenAuth())
	ov.Use(middleware.KYCRequired())
	{
		// 能力目录。客户端按 version 缓存，见 controller.GetOvCapabilities。
		ov.GET("/capabilities", controller.GetOvCapabilities)

		// 参考素材上传。返回签名 URL，任务的 metadata 直接吃这个 URL。
		//
		// **额外挂一条上传专用限流**（`UP` 桶，与系统里其它上传入口共用一个额度）：
		// 组上的 OvRateLimit 是"每 IP 180 次/180 秒"，对一个会往对象存储写文件的接口
		// 太松 —— 已 KYC 账号可以靠它把存储刷爆（对象还没有 TTL），并发上传还会把
		// 内存打满（见 controller.PostOvFiles 的 32MB 硬顶说明）。
		ov.POST("/files", middleware.UploadRateLimit(), controller.PostOvFiles)
	}
}
