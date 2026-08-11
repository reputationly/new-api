package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"

	"github.com/gin-gonic/gin"
)

func SetVideoRouter(router *gin.Engine) {
	// Video proxy: accepts either session auth (dashboard) or token auth (API clients)
	videoProxyRouter := router.Group("/v1")
	videoProxyRouter.Use(middleware.RouteTag("relay"))
	videoProxyRouter.Use(middleware.TokenOrUserAuth())
	{
		videoProxyRouter.GET("/videos/:task_id/content", controller.VideoProxy)
	}

	videoV1Router := router.Group("/v1")
	videoV1Router.Use(middleware.RouteTag("relay"))
	videoV1Router.Use(middleware.TokenAuth(), middleware.Distribute())
	{
		videoV1Router.POST("/video/generations", controller.RelayTask)
		videoV1Router.GET("/video/generations/:task_id", controller.RelayTaskFetch)
		videoV1Router.POST("/videos/:video_id/remix", controller.RelayTask)
	}
	// openai compatible API video routes
	// docs: https://platform.openai.com/docs/api-reference/videos/create
	{
		videoV1Router.POST("/videos", controller.RelayTask)
		videoV1Router.GET("/videos/:task_id", controller.RelayTaskFetch)
	}

	klingV1Router := router.Group("/kling/v1")
	klingV1Router.Use(middleware.RouteTag("relay"))
	klingV1Router.Use(middleware.KlingRequestConvert(), middleware.TokenAuth(), middleware.Distribute())
	{
		klingV1Router.POST("/videos/text2video", controller.RelayTask)
		klingV1Router.POST("/videos/image2video", controller.RelayTask)
		klingV1Router.GET("/videos/text2video/:task_id", controller.RelayTaskFetch)
		klingV1Router.GET("/videos/image2video/:task_id", controller.RelayTaskFetch)
	}

	// MiniMax v2 官方视频协议兼容层（docs/minimax-h3-playground-design.md §七の二）。
	// 目标：官方 API 用户改 base_url + key + model 就能切过来。model 要改是因为我们按
	// 玩法拆成了 minimax-h3-fl2va / minimax-h3-ref2va 两套部署，见 relay/minimaxv2 包注释。
	// 与上面的 OpenAI 兼容端点是**并存**关系：同一批任务两套协议都能提交与查询。
	// 范围是主流程 + 任务管理，不含 callback_url 回调。
	//
	// ⚠️ 依赖 router/main.go 里 SetRelayRouter 先于 SetVideoRouter 调用：engine 级的
	// BodyStorageCleanup 只对其后注册的路由生效，而提交端点会 ReplaceRequestBody
	// 新建一份 body storage，靠它收尾。顺序反过来会在 64MB 级 base64 请求体上漏临时文件。
	minimaxV2Router := router.Group("/v2")
	minimaxV2Router.Use(middleware.RouteTag("relay"))
	{
		// 提交：官方 content[]+role → 统一任务契约，随后复用 controller.RelayTask。
		minimaxV2Router.POST("/video_generation",
			middleware.MiniMaxV2CreateConvert(), middleware.TokenAuth(), middleware.Distribute(),
			controller.RelayTask)
		// 查询：只读本地任务表，不需要选渠道，故不挂 Distribute，relay_mode 显式指定。
		minimaxV2Router.GET("/query/video_generation/:task_id",
			middleware.MiniMaxV2Envelope(), middleware.TokenAuth(), middleware.MiniMaxV2FetchMode(),
			controller.RelayTaskFetch)
		minimaxV2Router.GET("/query/video_generation",
			middleware.MiniMaxV2Envelope(), middleware.TokenAuth(),
			controller.MiniMaxV2ListTasks)
		minimaxV2Router.DELETE("/video_generation/:task_id",
			middleware.MiniMaxV2Envelope(), middleware.TokenAuth(),
			controller.MiniMaxV2DeleteTask)

		// 官方有、我们做不到的两个端点：如实 501，不假装支持。
		minimaxV2Router.POST("/video_regeneration",
			middleware.MiniMaxV2Envelope(), middleware.TokenAuth(),
			controller.MiniMaxV2NotImplemented("video regeneration is not available on this gateway: it upgrades 768P output to 2K via the closed-source H3-Regenerate-2K model, which the self-hosted deployment does not have"))
		minimaxV2Router.POST("/h3_context_ir",
			middleware.MiniMaxV2Envelope(), middleware.TokenAuth(),
			controller.MiniMaxV2NotImplemented("h3_context_ir is not available on this gateway: structured prompt enrichment is served by MiniMax's own hosted model, which requires a MiniMax API key"))
	}

	// Jimeng official API routes - direct mapping to official API format
	jimengOfficialGroup := router.Group("jimeng")
	jimengOfficialGroup.Use(middleware.RouteTag("relay"))
	jimengOfficialGroup.Use(middleware.JimengRequestConvert(), middleware.TokenAuth(), middleware.Distribute())
	{
		// Maps to: /?Action=CVSync2AsyncSubmitTask&Version=2022-08-31 and /?Action=CVSync2AsyncGetResult&Version=2022-08-31
		jimengOfficialGroup.POST("/", controller.RelayTask)
	}
}
