package router

import (
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/relay"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

func SetRelayRouter(router *gin.Engine) {
	router.Use(middleware.CORS())
	router.Use(middleware.DecompressRequestMiddleware())
	router.Use(middleware.BodyStorageCleanup()) // 清理请求体存储
	router.Use(middleware.StatsMiddleware())
	// https://platform.openai.com/docs/api-reference/introduction
	modelsRouter := router.Group("/v1/models")
	modelsRouter.Use(middleware.RouteTag("relay"))
	modelsRouter.Use(middleware.TokenAuth())
	modelsRouter.Use(middleware.KYCRequired())
	{
		modelsRouter.GET("", func(c *gin.Context) {
			switch {
			case c.GetHeader("x-api-key") != "" && c.GetHeader("anthropic-version") != "":
				controller.ListModels(c, constant.ChannelTypeAnthropic)
			case c.GetHeader("x-goog-api-key") != "" || c.Query("key") != "": // 单独的适配
				controller.RetrieveModel(c, constant.ChannelTypeGemini)
			default:
				controller.ListModels(c, constant.ChannelTypeOpenAI)
			}
		})

		modelsRouter.GET("/:model", func(c *gin.Context) {
			switch {
			case c.GetHeader("x-api-key") != "" && c.GetHeader("anthropic-version") != "":
				controller.RetrieveModel(c, constant.ChannelTypeAnthropic)
			default:
				controller.RetrieveModel(c, constant.ChannelTypeOpenAI)
			}
		})
	}

	geminiRouter := router.Group("/v1beta/models")
	geminiRouter.Use(middleware.RouteTag("relay"))
	geminiRouter.Use(middleware.TokenAuth())
	geminiRouter.Use(middleware.KYCRequired())
	{
		geminiRouter.GET("", func(c *gin.Context) {
			controller.ListModels(c, constant.ChannelTypeGemini)
		})
	}

	geminiCompatibleRouter := router.Group("/v1beta/openai/models")
	geminiCompatibleRouter.Use(middleware.RouteTag("relay"))
	geminiCompatibleRouter.Use(middleware.TokenAuth())
	geminiCompatibleRouter.Use(middleware.KYCRequired())
	{
		geminiCompatibleRouter.GET("", func(c *gin.Context) {
			controller.ListModels(c, constant.ChannelTypeOpenAI)
		})
	}

	playgroundRouter := router.Group("/pg")
	playgroundRouter.Use(middleware.RouteTag("relay"))
	playgroundRouter.Use(middleware.SystemPerformanceCheck())
	playgroundRouter.Use(middleware.UserAuth(), middleware.KYCRequired(), middleware.Distribute())
	{
		playgroundRouter.POST("/chat/completions", controller.Playground)
		// 图片路由不在这里，见下方 playgroundImageRouter：它需要 ImageAsyncConvert
		// 跑在 Distribute 之前，而本组的 Distribute 是分组级中间件，插不进去。
		playgroundRouter.POST("/responses", controller.PlaygroundResponses)
		playgroundRouter.POST("/audio/speech", controller.PlaygroundAudioSpeech)
		playgroundRouter.POST("/videos", controller.PlaygroundVideo)
		playgroundRouter.GET("/videos/:task_id", controller.PlaygroundVideoFetch)
	}
	// 体验区图片：与 playgroundRouter 同一套鉴权，但把 Distribute 留到路由级，
	// 好让 ImageAsyncConvert 排在它前面（gin 的分组中间件恒先于路由中间件，
	// 挂在上面那组里是插不进 Distribute 之前的）。
	// 与 /v1 的 imageRouter 是同一套转换 + 分流，只是鉴权从 token 换成 session。
	playgroundImageRouter := router.Group("/pg")
	playgroundImageRouter.Use(middleware.RouteTag("relay"))
	playgroundImageRouter.Use(middleware.SystemPerformanceCheck())
	playgroundImageRouter.Use(middleware.UserAuth(), middleware.KYCRequired())
	playgroundImageRouter.Use(middleware.ImageAsyncConvert(), middleware.Distribute())
	{
		playgroundImageRouter.POST("/images/generations", controller.PlaygroundImage)
		playgroundImageRouter.POST("/images/edits", controller.PlaygroundImage)
		playgroundImageRouter.GET("/images/generations/:task_id", controller.PlaygroundImageFetch)
		playgroundImageRouter.DELETE("/images/generations/:task_id", controller.PlaygroundImageCancel)
	}

	// 图片代理：仅需登录会话鉴权，不经过 Distribute（GET 无模型可分发）
	playgroundUtilRouter := router.Group("/pg")
	playgroundUtilRouter.Use(middleware.UserAuth())
	{
		playgroundUtilRouter.GET("/images/proxy", controller.PlaygroundImageProxy)
		playgroundUtilRouter.GET("/videos/:task_id/content", controller.VideoProxy)
		playgroundUtilRouter.GET("/models", func(c *gin.Context) {
			controller.ListModels(c, constant.ChannelTypeOpenAI)
		})
	}
	relayV1Router := router.Group("/v1")
	relayV1Router.Use(middleware.RouteTag("relay"))
	relayV1Router.Use(middleware.SystemPerformanceCheck())
	relayV1Router.Use(middleware.TokenAuth())
	relayV1Router.Use(middleware.KYCRequired())
	relayV1Router.Use(middleware.ModelRequestRateLimit())
	{
		// WebSocket 路由（统一到 Relay）
		wsRouter := relayV1Router.Group("")
		wsRouter.Use(middleware.Distribute())
		wsRouter.GET("/realtime", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAIRealtime)
		})
	}
	{
		//http router
		httpRouter := relayV1Router.Group("")
		httpRouter.Use(middleware.Distribute())

		// claude related routes
		httpRouter.POST("/messages", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatClaude)
		})

		// chat related routes
		httpRouter.POST("/completions", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAI)
		})
		httpRouter.POST("/chat/completions", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAI)
		})

		// response related routes
		httpRouter.POST("/responses", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAIResponses)
		})
		httpRouter.POST("/responses/compact", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAIResponsesCompaction)
		})

		// image related routes
		// 注意：/images/generations 与 /images/edits 不在这里——它们支持同步/异步
		// 双模，需要转换中间件跑在 Distribute 之前，故独立成 imageRouter 分组（见下）。
		httpRouter.POST("/edits", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAIImage)
		})

		// embedding related routes
		httpRouter.POST("/embeddings", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatEmbedding)
		})

		// audio related routes
		httpRouter.POST("/audio/transcriptions", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAIAudio)
		})
		httpRouter.POST("/audio/translations", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAIAudio)
		})
		httpRouter.POST("/audio/speech", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAIAudio)
		})

		// rerank related routes
		httpRouter.POST("/rerank", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatRerank)
		})

		// gemini relay routes
		httpRouter.POST("/engines/:model/embeddings", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatGemini)
		})
		httpRouter.POST("/models/*path", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatGemini)
		})

		// other relay routes
		httpRouter.POST("/moderations", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAI)
		})

		// not implemented
		httpRouter.POST("/images/variations", controller.RelayNotImplemented)
		httpRouter.GET("/files", controller.RelayNotImplemented)
		httpRouter.POST("/files", controller.RelayNotImplemented)
		httpRouter.DELETE("/files/:id", controller.RelayNotImplemented)
		httpRouter.GET("/files/:id", controller.RelayNotImplemented)
		httpRouter.GET("/files/:id/content", controller.RelayNotImplemented)
		httpRouter.POST("/fine-tunes", controller.RelayNotImplemented)
		httpRouter.GET("/fine-tunes", controller.RelayNotImplemented)
		httpRouter.GET("/fine-tunes/:id", controller.RelayNotImplemented)
		httpRouter.POST("/fine-tunes/:id/cancel", controller.RelayNotImplemented)
		httpRouter.GET("/fine-tunes/:id/events", controller.RelayNotImplemented)
		httpRouter.DELETE("/models/:model", controller.RelayNotImplemented)
	}

	// 图片端点：同一路径支持同步与异步两种模式（docs/image-async-task-design.md）。
	//
	// 为什么要独立分组而不是复用上面的 httpRouter：ImageAsyncConvert 必须在
	// Distribute 之前执行（它改写 body，而 Distribute 从 body 读 model），而 gin 的
	// 分组中间件恒先于路由中间件执行——挂成路由级中间件是来不及的。
	//
	// 分组定义在 SetRelayRouter 内部、engine 级 BodyStorageCleanup（本函数开头
	// router.Use）之后，所以 ImageAsyncConvert 里 ReplaceRequestBody 新建的 body
	// storage 有人收尾。别把它挪到 SetRelayRouter 之外的新文件里再提前调用——
	// router/video-router.go:50 记录过这个陷阱，代价是 64MB 级请求体的临时文件泄漏。
	imageRouter := relayV1Router.Group("")
	imageRouter.Use(middleware.ImageAsyncConvert(), middleware.Distribute())
	{
		imageRouter.POST("/images/generations", imageEntry)
		imageRouter.POST("/images/edits", imageEntry)
		imageRouter.GET("/images/generations/:task_id", controller.RelayTaskFetch)
		imageRouter.DELETE("/images/generations/:task_id", controller.RelayTaskCancel)
	}

	relayMjRouter := router.Group("/mj")
	relayMjRouter.Use(middleware.RouteTag("relay"))
	relayMjRouter.Use(middleware.SystemPerformanceCheck())
	registerMjRouterGroup(relayMjRouter)

	relayMjModeRouter := router.Group("/:mode/mj")
	relayMjModeRouter.Use(middleware.RouteTag("relay"))
	relayMjModeRouter.Use(middleware.SystemPerformanceCheck())
	registerMjRouterGroup(relayMjModeRouter)
	//relayMjRouter.Use()

	relaySunoRouter := router.Group("/suno")
	relaySunoRouter.Use(middleware.RouteTag("relay"))
	relaySunoRouter.Use(middleware.SystemPerformanceCheck())
	relaySunoRouter.Use(middleware.TokenAuth(), middleware.KYCRequired(), middleware.Distribute())
	{
		relaySunoRouter.POST("/submit/:action", controller.RelayTask)
		relaySunoRouter.POST("/fetch", controller.RelayTaskFetch)
		relaySunoRouter.GET("/fetch/:id", controller.RelayTaskFetch)
	}

	relayGeminiRouter := router.Group("/v1beta")
	relayGeminiRouter.Use(middleware.RouteTag("relay"))
	relayGeminiRouter.Use(middleware.SystemPerformanceCheck())
	relayGeminiRouter.Use(middleware.TokenAuth())
	relayGeminiRouter.Use(middleware.KYCRequired())
	relayGeminiRouter.Use(middleware.ModelRequestRateLimit())
	relayGeminiRouter.Use(middleware.Distribute())
	{
		// Gemini API 路径格式: /v1beta/models/{model_name}:{action}
		relayGeminiRouter.POST("/models/*path", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatGemini)
		})
	}
}

// imageEntry 图片端点的同步/异步分流。开关识别与请求体改写已由
// middleware.ImageAsyncConvert 完成，这里只看它留下的标记。
// 不带 async 的请求走的是与改造前完全相同的那一行。
func imageEntry(c *gin.Context) {
	if c.GetBool(middleware.CtxKeyImageAsync) {
		controller.RelayTask(c)
		return
	}
	controller.Relay(c, types.RelayFormatOpenAIImage)
}

func registerMjRouterGroup(relayMjRouter *gin.RouterGroup) {
	relayMjRouter.GET("/image/:id", relay.RelayMidjourneyImage)
	relayMjRouter.Use(middleware.TokenAuth(), middleware.KYCRequired(), middleware.Distribute())
	{
		relayMjRouter.POST("/submit/action", controller.RelayMidjourney)
		relayMjRouter.POST("/submit/shorten", controller.RelayMidjourney)
		relayMjRouter.POST("/submit/modal", controller.RelayMidjourney)
		relayMjRouter.POST("/submit/imagine", controller.RelayMidjourney)
		relayMjRouter.POST("/submit/change", controller.RelayMidjourney)
		relayMjRouter.POST("/submit/simple-change", controller.RelayMidjourney)
		relayMjRouter.POST("/submit/describe", controller.RelayMidjourney)
		relayMjRouter.POST("/submit/blend", controller.RelayMidjourney)
		relayMjRouter.POST("/submit/edits", controller.RelayMidjourney)
		relayMjRouter.POST("/submit/video", controller.RelayMidjourney)
		//relayMjRouter.POST("/notify", controller.RelayMidjourney)
		relayMjRouter.GET("/task/:id/fetch", controller.RelayMidjourney)
		relayMjRouter.GET("/task/:id/image-seed", controller.RelayMidjourney)
		relayMjRouter.POST("/task/list-by-condition", controller.RelayMidjourney)
		relayMjRouter.POST("/insight-face/swap", controller.RelayMidjourney)
		relayMjRouter.POST("/submit/upload-discord-images", controller.RelayMidjourney)
	}
}
