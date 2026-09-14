package router

import (
	"net/http"

	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"

	"github.com/gin-gonic/gin"
)

// SetHiloRouter 挂载 MiniMax Design（内部代号 hilo）本地 gateway 的云侧接口。
//
// # 为什么这些路径长这样
//
// 路径不是我们定的，是**官方本地 gateway 写死往外发的**：把它的
// `CLOUD_GATEWAY_BASE_URL` 指到 new-api，它就按这些路径打过来。
// 抓包实测的结果：
//
//	POST /api/v1/video/minimax-v3/generate     ← 视频在 v1
//	POST /api/v2/image/qwen/generate           ← 图片在 v2
//	GET  /api/v1/models/config                 ← 模型目录
//
// v1/v2 混用是官方的历史包袱，不是笔误。
//
// # 鉴权：分两类，不能一刀切
//
// **目录与配置类（models/config、client_config、user_equity…）不挂鉴权。**
// 官方 gateway 请求这些时带的是它自己云端的 token，对 new-api 没有意义;
// 要求鉴权等于要求用户先在我们这边登录，而"绕开登录"正是这条路的前提。
// 代价是这几条匿名可达，所以它们只读、且单独限流（HiloRateLimit）。
//
// **生成类必须挂 TokenAuth。** 这不是安全洁癖，是分段计费的前提：
//
//	· 增强段以**客户的 Authorization** 自调用 /v1/chat/completions，
//	  那笔账才落在客户头上（service/aggregate_enhance.go）；
//	· 超分段把同一个令牌存进 CallerKey，轮询阶段用它提交第二段
//	  （controller/relay.go 的 buildTaskAggregateInfo）。
//
// 不鉴权的话这两段都没有身份可用，整条分段计费断掉。
//
// 也就是说：客户端要为生成配一个**我们这边的**令牌。它和官方云的 token
// 是两回事，填在 gateway 的环境变量里。
func SetHiloRouter(router *gin.Engine) {
	hilo := router.Group("/api")
	hilo.Use(middleware.RouteTag("hilo"))
	// **这一组是匿名的**（见上面的注释），而 `/v1/models/config` 每次都会
	// `SELECT DISTINCT model FROM abilities`，客户端还在轮询它 —— 不限流的话,
	// 一个公网可达的 new-api 上这就是个免鉴权的数据库压力源。注释里
	// "只监听本地"是**约定，不是强制**，不能拿它当防护。
	//
	// 用 `HiloRateLimit` 而不是 `GlobalAPIRateLimit`:后者的桶 key 是
	// `"GA" + ClientIP`，和整个 dashboard/user API 共用 —— 客户端的轮询会
	// 把同一出口 IP 下浏览器的正常请求挤成 429。
	hilo.Use(middleware.HiloRateLimit())
	{
		// 模型目录。**整条链的总开关** —— 官方 gateway 的模型选择器、
		// 参数表、参数互斥规则全部由它驱动，返回不合 schema 的内容会
		// 让界面上一个模型都没有。见 controller/hilo.go。
		hilo.GET("/v1/models/config", controller.GetHiloModelsConfig)

		// 下面这些官方 gateway 会调，但**对我们没有意义**。
		//
		// 返回固定值而不是留 404：404 只产生 WARN 日志、功能不受影响
		// （实测全 404 时应用照常启动），但每次轮询都刷一条警告。
		// 给一个"额度无限、已登录"的固定答复更干净。
		hilo.GET("/v1/client_config", hiloEmptyObject)
		hilo.GET("/v1/hub/client_config", hiloEmptyObject)
		hilo.GET("/v1/apollo/config", hiloEmptyObject)
		hilo.GET("/v1/home/quick_start_config", hiloEmptyObject)
		hilo.GET("/v1/user/equity", controller.GetHiloUserEquity)
		hilo.GET("/v1/models/concurrency/limits", hiloEmptyObject)
		hilo.GET("/v1/models/concurrency/usage", hiloEmptyObject)

		// 视频生成。官方形状 → 统一任务契约 → 复用 RelayTask。
		//
		// **中间件顺序有讲究**，和 /v2/video_generation 那条一致：
		//   HiloVideoConvert  改写 body 与路径（要早于鉴权，错误才走同一个信封）
		//   TokenAuth         鉴权；白名单里存的是聚合模型名
		//   Distribute        聚合展开 + 选渠道（展开必须晚于白名单、早于选渠道）
		//   RelayTask         既有链路，分段计费/日志/限流全部白拿
		hilo.POST("/v1/video/minimax-v3/generate",
			middleware.HiloVideoConvert(), middleware.TokenAuth(), middleware.Distribute(),
			controller.RelayTask)

		// 其余生成入口**还没实现**，但必须显式占位。
		//
		// 不占位的话这些 POST 会落到 `web-router` 的 `NoRoute` 兜底，
		// 返回一个笼统的 404 `Invalid URL` —— 而上面那份目录正在把客户端
		// 往这些路径上引（`backend` 字段决定它 POST 到哪条）。
		// 404 看起来像"路由配错了"，501 才说得清"这一半还没做"。
		//
		// 路径不是我们定的，是官方 gateway 写死往外发的，抓包实测得来。
		// v1/v2 混用是他们的历史包袱。
		for _, p := range []string{
			"/v2/image/qwen/generate",
			"/v2/image/nano_banana/generate",
			"/v2/image/seedream/generate",
			"/v2/image/enhance/generate",
			"/v2/audio/tts",
			"/v2/audio/music/minimax",
		} {
			hilo.POST(p, controller.RelayNotImplemented)
		}
	}
}

// hiloEmptyObject 回一个空对象。
//
// **不是空响应体** —— 官方 gateway 对这几条会 `JSON.parse`，空体会抛
// 解析错误，那比 404 还吵。
func hiloEmptyObject(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{})
}
