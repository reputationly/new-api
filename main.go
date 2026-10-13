package main

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/oauth"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	"github.com/QuantumNous/new-api/relay"
	"github.com/QuantumNous/new-api/relay/channel/gpustackplus/nfsinput"
	"github.com/QuantumNous/new-api/router"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/mediastore"
	"github.com/QuantumNous/new-api/service/moderation"
	"github.com/QuantumNous/new-api/service/quotaunitmigration"
	_ "github.com/QuantumNous/new-api/setting/performance_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"

	_ "net/http/pprof"
)

//go:embed web/classic/dist
var classicBuildFS embed.FS

//go:embed web/classic/dist/index.html
var classicIndexPage []byte

//go:embed web/mobile/dist
var mobileBuildFS embed.FS

//go:embed web/mobile/dist/index.html
var mobileIndexPage []byte

func main() {
	startTime := time.Now()

	err := InitResources()
	if err != nil {
		common.FatalLog("failed to initialize resources: " + err.Error())
		return
	}

	// 额度单位迁移模式：环境与数据库已就绪，只跑迁移并退出——不起 HTTP 服务、
	// 不起定时任务、不碰渠道缓存，因此也不受 NFS / ffmpeg 等运行时依赖影响。
	if *common.QuotaUnitMigrationMode != "" {
		if err := quotaunitmigration.Run(*common.QuotaUnitMigrationMode, os.Stdout); err != nil {
			common.FatalLog("quota unit migration failed: " + err.Error())
		}
		return
	}

	if *common.QuotaUnitMigrationV2Mode != "" {
		if err := quotaunitmigration.RunV2(*common.QuotaUnitMigrationV2Mode, os.Stdout); err != nil {
			common.FatalLog("quota unit migration V2 failed: " + err.Error())
		}
		return
	}

	// 启动保护（设计文档 §4 B9）：未完成 V2 迁移而库里已有数据时，配置仍是美元口径而代码已按
	// 人民币与更细的 quota 单位计算，每条请求会按错误倍数扣费（倍率路径约 68.5 倍、按次/
	// 视频/工具路径约 137 倍），因此整个进程拒绝启动，只允许用上面的迁移参数跑完再起。
	if err := quotaunitmigration.EnsurePriceUnitReady(); err != nil {
		common.FatalLog(err.Error())
		return
	}

	common.SysLog("New API " + common.Version + " started")

	// Surface KYC key (mis)configuration warnings at startup rather than first use.
	common.InitKYCKeys()
	// Surface OBS media-store key (mis)configuration warnings at startup too.
	common.InitOBSKeys()
	// 内容审核原文加密密钥。缺失只警告不致命——审核照跑，只是 block 记录不留原文。
	common.InitModerationKey()

	// LightX2V NFS 输入盘启动探测(§6):启用媒体存储 + NFS 落盘搬运时,<NFSRoot>/inputs
	// 必须可读写,且与 gpustack lightx2v_output_root 指向同一 SFS(同一绝对挂载路径),
	// 否则 gpustackplus 的输入物化会落到错误的文件系统。不满足则拒绝启动。
	if mediastore.Enabled() && system_setting.GetMediaStorageSettings().IngestNFSPath {
		if err := nfsinput.ProbeNFSInputs(); err != nil {
			common.FatalLog("LightX2V NFS 输入盘探测失败(NFS 未挂载/不可写/与 gpustack output_root 不一致): " + err.Error())
		}
		common.SysLog("LightX2V NFS 输入盘 OK: " + mediastore.NFSRoot() + "/inputs")
	}

	// 内容审核的视频抽帧依赖外部 ffmpeg。这里主动探一次而不是等第一个视频请求：
	// 缺了它视频会被静默跳过（不拒绝请求），启动时不说，就要等到有人问
	// 「为什么违规视频没拦住」才发现——那时已经漏了一整段时间。
	if ok, missing := moderation.FFmpegAvailable(); ok {
		common.SysLog("内容审核: ffmpeg 可用，视频抽帧已就绪")
	} else {
		common.SysLog("内容审核: 缺少 " + missing + "，视频将不经审核（图片审核不受影响）")
	}

	if os.Getenv("GIN_MODE") != "debug" {
		gin.SetMode(gin.ReleaseMode)
	}
	if common.DebugEnabled {
		common.SysLog("running in debug mode")
	}

	defer func() {
		err := model.CloseDB()
		if err != nil {
			common.FatalLog("failed to close database: " + err.Error())
		}
	}()

	if common.RedisEnabled {
		// for compatibility with old versions
		common.MemoryCacheEnabled = true
	}
	if common.MemoryCacheEnabled {
		common.SysLog("memory cache enabled")
		common.SysLog(fmt.Sprintf("sync frequency: %d seconds", common.SyncFrequency))

		// Add panic recovery and retry for InitChannelCache
		func() {
			defer func() {
				if r := recover(); r != nil {
					common.SysLog(fmt.Sprintf("InitChannelCache panic: %v, retrying once", r))
					// Retry once
					_, _, fixErr := model.FixAbility()
					if fixErr != nil {
						common.FatalLog(fmt.Sprintf("InitChannelCache failed: %s", fixErr.Error()))
					}
				}
			}()
			model.InitChannelCache()
		}()

		go model.SyncChannelCache(common.SyncFrequency)
	}

	// 渠道成本缓存：记账与选路都依赖它，因此与内存缓存开关无关，无条件初始化 + 轮询
	model.InitChannelModelCostCache()
	go model.SyncChannelModelCostCache(common.SyncFrequency)

	// 模型可见性缓存：权限判定，任何部署形态都必须生效，同样与内存缓存开关无关
	model.InitModelVisibilityCache()
	go model.SyncModelVisibilityCache(common.SyncFrequency)

	// 热更新配置
	go model.SyncOptions(common.SyncFrequency)

	// 数据看板
	go model.UpdateQuotaData()

	if os.Getenv("CHANNEL_UPDATE_FREQUENCY") != "" && common.IsMasterNode {
		// 只在主节点上跑：这个任务会遍历**全部**渠道去上游拉余额并写回库，多副本下每个
		// 副本各跑一遍，等于把上游账单接口的调用量乘以副本数（结果本身是"最后写入者生效"，
		// 不会写坏数据）。现网没有设这个变量，但设了就会触发，所以在这里一并收口。
		frequency, err := strconv.Atoi(os.Getenv("CHANNEL_UPDATE_FREQUENCY"))
		if err != nil {
			common.FatalLog("failed to parse CHANNEL_UPDATE_FREQUENCY: " + err.Error())
		}
		go controller.AutomaticallyUpdateChannels(frequency)
	}

	go controller.AutomaticallyTestChannels()

	// Codex credential auto-refresh check every 10 minutes, refresh when expires within 1 day
	service.StartCodexCredentialAutoRefreshTask()

	// Subscription quota reset task (daily/weekly/monthly/custom)
	service.StartSubscriptionQuotaResetTask()

	// Direct payment (Alipay/WeChat Pay) expired order cleanup
	service.StartDirectPaymentExpiryScan()

	// Media storage (OBS) bucket usage snapshot + threshold alert cron
	service.StartMediaStorageStatsTask()

	// 内容审核日志：异步落库 worker + 分档清理。
	model.InitModerationLogWorker()
	if common.IsMasterNode {
		// 只在主节点清理：多节点同时跑 DELETE 除了互相锁等待没有任何好处。
		gopool.Go(func() {
			for {
				// 先删取证对象再删记录，且**取证没清完就不删记录**：object_key 只存在于
				// 记录里，记录一删，剩下的对象就永久变成桶里一堆无人认领的违规内容。
				// 连续跳过太多轮时会强制收口并告警，见 ShouldCleanupLogsAfterEvidence。
				drained := moderation.CleanupEvidence()
				if moderation.ShouldCleanupLogsAfterEvidence(drained) {
					if err := model.CleanupModerationLogs(); err != nil {
						common.SysError("moderation_log 清理失败: " + err.Error())
					}
				}
				time.Sleep(6 * time.Hour)
			}
		})
	}

	// 产物审核钩子（§12.4 挂载点 D-1）。用注入而不是直接调用：
	// service/moderation 已经依赖 service，反向 import 会成环。
	service.ModerateTaskOutputFunc = func(ctx context.Context, task *model.Task, resultURL, upstreamURL string) (bool, string) {
		res := moderation.ModerateTaskOutput(ctx, task, resultURL, upstreamURL)
		return res.Blocked, res.Reason
	}

	// Wire task polling adaptor factory (breaks service -> relay import cycle)
	service.GetTaskAdaptorFunc = func(platform constant.TaskPlatform) service.TaskPollingAdaptor {
		a := relay.GetTaskAdaptor(platform)
		if a == nil {
			return nil
		}
		return a
	}

	// Channel upstream model update check task
	controller.StartChannelUpstreamModelUpdateTask()

	if common.IsMasterNode && constant.UpdateTask {
		gopool.Go(func() {
			controller.UpdateMidjourneyTaskBulk()
		})
		gopool.Go(func() {
			controller.UpdateTaskBulk()
		})
	}
	if os.Getenv("BATCH_UPDATE_ENABLED") == "true" {
		common.BatchUpdateEnabled = true
		common.SysLog("batch update enabled with interval " + strconv.Itoa(common.BatchUpdateInterval) + "s")
		model.InitBatchUpdater()
	}

	var pprofServer *http.Server
	if os.Getenv("ENABLE_PPROF") == "true" {
		// Handler 留 nil 走 DefaultServeMux —— net/http/pprof 在 init 里注册的就是它。
		// 之所以要拿住这个 *http.Server，是为了优雅退出时能把它一并关掉。
		pprofServer = &http.Server{Addr: "0.0.0.0:8005"}
		gopool.Go(func() {
			if err := pprofServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Println("pprof server exited:", err)
			}
		})
		go common.Monitor()
		common.SysLog("pprof enabled")
	}

	err = common.StartPyroScope()
	if err != nil {
		common.SysError(fmt.Sprintf("start pyroscope error : %v", err))
	}

	// Initialize HTTP server
	server := gin.New()
	configureTrustedProxies(server)
	server.Use(gin.CustomRecovery(func(c *gin.Context, err any) {
		common.SysLog(fmt.Sprintf("panic detected: %v", err))
		common.SysLog(fmt.Sprintf("stacktrace from panic: %s", string(debug.Stack())))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"message": "系统异常,请在「我的工单」中反馈本次异常。",
				"type":    "new_api_panic",
			},
		})
	}))
	// This will cause SSE not to work!!!
	//server.Use(gzip.Gzip(gzip.DefaultCompression))
	server.Use(middleware.RequestId())
	server.Use(middleware.PoweredBy())
	server.Use(middleware.I18n())
	middleware.SetUpLogger(server)
	// Initialize session store
	store := cookie.NewStore([]byte(common.SessionSecret))
	store.Options(sessions.Options{
		Path:     "/",
		MaxAge:   common.SessionMaxAgeSeconds,
		HttpOnly: true,
		Secure:   false,
		SameSite: http.SameSiteStrictMode,
	})
	server.Use(sessions.Sessions("session", store))

	InjectUmamiAnalytics()
	InjectGoogleAnalytics()

	// 设置路由
	router.SetRouter(server, router.ThemeAssets{
		ClassicBuildFS:   classicBuildFS,
		ClassicIndexPage: classicIndexPage,
		MobileBuildFS:    mobileBuildFS,
		MobileIndexPage:  mobileIndexPage,
	})
	var port = os.Getenv("PORT")
	if port == "" {
		port = strconv.Itoa(*common.Port)
	}

	// Log startup success message
	common.LogStartupSuccess(startTime, port)

	// ---------- 优雅退出 ----------
	//
	// 这里原来是 server.Run(":"+port)，等价于 http.ListenAndServe，完全不处理信号：K8s 删 Pod
	// 时先发 SIGTERM，Go 进程按默认行为立即终止，于是
	//   1) 在途请求（SSE / streaming / 长耗时同步请求）被当场切断；
	//   2) 内存里批量累积的额度变动（BATCH_UPDATE_ENABLED=true 时 5s 一刷）丢最近一个周期，
	//      额度账就对不上了；
	//   3) 审核日志异步队列里尚未落库的记录一起丢。
	// 单容器时这三件事只在人工升级时发生一次；上了 CCE 之后滚动升级、节点排水、缩容、
	// Pod 漂移都会触发，频率高得多，所以必须补上。
	httpServer := &http.Server{
		Addr:    ":" + port,
		Handler: server,
		// 刻意不设 ReadTimeout / WriteTimeout / IdleTimeout：本服务是 AI 网关，单个
		// streaming 请求可能持续几分钟，任何写超时都会把正常的长请求打断。
		// 原先的 server.Run 也没设，这里保持一致。
	}

	// ListenAndServe 在 Shutdown 开始关监听器的那一刻就返回，早于等待在途请求 / 刷库 / 排空队列。
	// 所以必须用这个 channel 把 main 挂住，否则进程会在收尾做完之前就退出，前面全白做。
	shutdownDone := make(chan struct{})
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		defer close(shutdownDone)
		defer func() {
			// 收尾里任何一处 panic 都不能让 main 永远卡在等待上：那会一直挂到 SIGKILL，
			// 反倒连刷库的机会都没有。
			if r := recover(); r != nil {
				common.SysLog(fmt.Sprintf("graceful shutdown panic: %v", r))
			}
		}()
		sig := <-sigCh
		// 收到信号后不再接管：如果运维发现收尾卡住，第二次 SIGTERM/SIGINT 会走默认动作
		// 立即结束进程。这比干等到 SIGKILL 可控。
		signal.Stop(sigCh)
		common.SysLog("received " + sig.String() + ", starting graceful shutdown")
		shutdownGracefully(httpServer, pprofServer, shutdownTimeout())
	}()

	err = httpServer.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		common.FatalLog("failed to start HTTP server: " + err.Error())
	}

	// 等收尾真正做完（正常关闭时 Shutdown 会关掉监听器，让上面那行返回 ErrServerClosed）。
	<-shutdownDone
	common.SysLog("graceful shutdown: completed, exiting")
}

const (
	// shutdownDefaultTimeoutSeconds 收尾总预算的默认值（秒）。应当小于 Pod 的
	// terminationGracePeriodSeconds，给最后那几秒留出余量。可用 SHUTDOWN_TIMEOUT 覆盖。
	shutdownDefaultTimeoutSeconds = 300
)

// configureTrustedProxies 按 TRUSTED_PROXY_CIDR 配置 gin 的代理信任，**两个分支都必须显式设置**。
//
// 不能写成"不配置就不调 SetTrustedProxies"：gin.New() 的默认是
// trustedProxies = {"0.0.0.0/0", "::/0"}、trustedCIDRs = 对应网段（谁都信），而 ClientIP()
// 判断"要不要采信 X-Forwarded-For"时只看 trustedCIDRs，**没有"这份列表是否被配置过"的守卫**。
// 所以不调用 ≠ 不信任代理，而是**信任所有代理**：ClientIP() 会直接采信 XFF 最左边那个值，
// 客户端自己伪造这个头就能绕过按 IP 的限流，并把伪造的 IP 写进日志。
// 未配置时必须显式设 nil，把可信列表清空（SetTrustedProxies 会把 trustedProxies 置 nil，
// 而 isTrustedProxy 对 nil 返回 false）。
//
// 抽成独立函数是为了可测：这个默认值一旦回退成"谁都信"，运行时没有任何症状，
// 只有第三方伪造 XFF 时才看得出来。见 main_test.go 的 TestTrustedProxyDefaultsToNobody。
func configureTrustedProxies(server *gin.Engine) {
	cidr := os.Getenv("TRUSTED_PROXY_CIDR")
	if cidr == "" {
		// 直连部署（前面没有反向代理）走这里。显式清空 = 不采信任何 XFF。
		_ = server.SetTrustedProxies(nil)
		common.SysLog("TRUSTED_PROXY_CIDR 未配置，已关闭代理信任：客户端 IP 取直接对端地址")
		return
	}
	if err := server.SetTrustedProxies(strings.Split(cidr, ",")); err != nil {
		// 注意失败时的状态不是"没配"：gin 保留的是**出错前已解析成功的那部分**，
		// 所以"只有后面某一项写错"时前面几项仍然生效。排查时别假设它整份失效了。
		common.SysError("TRUSTED_PROXY_CIDR 解析失败（" + cidr + "），已解析成功的部分仍会生效，" +
			"客户端 IP 可能被错误解析（影响按 IP 的限流）: " + err.Error())
	}
}

// 这些时长用 var 而不是 const，纯粹是为了可测：关闭流程的测试要把它压到 0，
// 否则每个用例都得先白等 2 秒。生产代码里没人改它们。
var (
	// shutdownDrainDelay 置位 readiness 之后、真正 Shutdown 之前等的一小会儿，
	// 让 ELB/Service 把本实例从后端摘掉。K8s 侧还有 preStop sleep 作为主要手段，
	// 这里是兜底（也覆盖 docker stop 这类没有 preStop 的场景）。
	shutdownDrainDelay = 2 * time.Second

	// shutdownFinalReserve 必须留给「落盘」这类不能省的动作：刷额度/看板增量 + 排空审核日志。
	// 即使前面的等待全部超时，也要保证这段时间可用——否则「优雅退出」退成了「退出，
	// 但把要保的东西丢了」。
	shutdownFinalReserve = 10 * time.Second

	// shutdownSettleReserve 留给"强关 WebSocket 之后的结算"和"等在途的关键后台任务"。
	//
	// 为什么必须独立留一段，而不是复用 finalizing：撑到宽限期的正是那些不会自己结束的
	// 长 realtime 会话，等它们自然结束的那一步会把预算一路用到 finalizing。此时再用
	// finalizing 去等结算，截止时间已经过去了，等待立刻返回 0——**这处丢失恰好发生在
	// 它唯一会发生的场景里**。所以阶段 2/3 的"自然等待"只能用前一段预算（preFinalizing），
	// 强关之后的等待改用这一段（settling）。
	shutdownSettleReserve = 15 * time.Second
)

func shutdownTimeout() time.Duration {
	return time.Duration(common.GetEnvOrDefault("SHUTDOWN_TIMEOUT", shutdownDefaultTimeoutSeconds)) * time.Second
}

// shutdownGracefully 按顺序收尾，返回时进程已无在途工作，main 的 defer 随后关数据库。
//
// 顺序不能随意调换，每一步都对应一类会丢东西的状态：
//  1. 置位 readiness 失败 —— 先让负载均衡别再往这里送新请求；
//  2. Shutdown —— 等在途的普通 HTTP 请求（含 SSE / streaming）结束；
//  3. 等 / 关 WebSocket —— hijacked 连接 Shutdown 不管，必须自己等（见 relay/wsregistry.go）；
//     3.5 等关键后台任务 —— 失败请求的退款跑在 gopool 里，Shutdown 同样不等；不等就会丢钱；
//  4. 刷内存里累积的两处状态 —— 额度变动 + 数据看板增量。到这里已无新生产者，刷下去的就是全部；
//  5. 排空审核日志队列；
//  6. 关 pprof。
//
// 刷库刻意放在 WebSocket 之后而不是之前：realtime 会话结束时也会结算额度
// （service.PostWssConsumeQuota），早刷会漏掉它们。
func shutdownGracefully(httpServer, pprofServer *http.Server, total time.Duration) {
	deadline := time.Now().Add(total)
	// 三段预算，从后往前预留（见 shutdownFinalReserve / shutdownSettleReserve 的注释）：
	//   [阶段 2/3 的自然等待] ...preFinalizing | [强关后结算 + 关键任务] ...settling | [刷库] ...deadline
	settling := deadline.Add(-shutdownFinalReserve)
	preFinalizing := settling.Add(-shutdownSettleReserve)

	// 1) 摘流量
	common.MarkShuttingDown()
	common.SysLog("graceful shutdown: readiness flipped to failing (drain delay " + shutdownDrainDelay.String() + ")")
	time.Sleep(shutdownDrainDelay)

	// 2) 等在途的普通请求。deadline 若已过，WithDeadline 返回的 context 立即就绪，
	//    Shutdown 会马上返回错误，走下面的 Close 分支——不需要额外判空。
	shutdownCtx, cancelShutdown := context.WithDeadline(context.Background(), preFinalizing)
	shutdownErr := httpServer.Shutdown(shutdownCtx)
	cancelShutdown()
	if shutdownErr != nil {
		common.SysLog("graceful shutdown: in-flight requests did not finish before the deadline, force closing: " + shutdownErr.Error())
		// 掐掉剩余连接，否则进程退不出去（main 还等在 shutdownDone 上）。
		_ = httpServer.Close()
	} else {
		common.SysLog("graceful shutdown: in-flight requests drained")
	}

	// 3) WebSocket
	if n := relay.ActiveWebSocketCount(); n > 0 {
		common.SysLog(fmt.Sprintf("graceful shutdown: waiting for %d websocket connection(s)", n))
		wsCtx, cancelWS := context.WithDeadline(context.Background(), preFinalizing)
		drained := relay.WaitWebSockets(wsCtx)
		cancelWS()
		if !drained {
			// 自然等待的预算用完了：不能让几个长期存活的会话把升级无限拖住，强制关掉。
			relay.CloseAllWebSockets()

			// 关掉连接**不等于会话结束**：各 handler 还要走 PostWssConsumeQuota 结算，
			// 注销是它们自己的 defer、要等 handler 整个返回才跑。不再等一次的话，结算会
			// 和下面的刷库并发——要么晚于刷库进了再也没人刷的 store，要么写到一半被进程
			// 退出打断。被强关的恰好是撑过整段宽限期的长会话，单笔金额最大。
			// 这里必须用 settling（独占的一段预算）：自然等待已经把 preFinalizing 用光了，
			// 再用同一个截止时间等于不等。
			if n := relay.ActiveWebSocketCount(); n > 0 {
				common.SysLog(fmt.Sprintf("graceful shutdown: waiting for %d websocket session(s) to settle", n))
				settleCtx, cancelSettle := context.WithDeadline(context.Background(), settling)
				settled := relay.WaitWebSockets(settleCtx)
				cancelSettle()
				if !settled {
					common.SysLog(fmt.Sprintf(
						"graceful shutdown: %d websocket session(s) still not settled; "+
							"their billing may be lost", relay.ActiveWebSocketCount()))
				}
			}
		}
	}

	// 3.5) 等「退出前必须做完」的后台任务——主要是失败请求的退款。
	//
	// 这些任务跑在 gopool 里，**Shutdown 不会等它们**（处理函数一返回，Shutdown 就认为
	// 那个请求结束了）。不等的话有两种丢法：开了批量更新时退款进了 store 却晚于下一步的
	// 刷库（再也没人刷）；直写数据库时写到一半被进程退出打断。两者都等于用户被白扣。
	// 等完之后每一笔退款要么已经落库、要么已经进了随后会被刷下去的 store。
	if n := common.CriticalTaskCount(); n > 0 {
		common.SysLog(fmt.Sprintf("graceful shutdown: waiting for %d critical background task(s)", n))
		critCtx, cancelCrit := context.WithDeadline(context.Background(), settling)
		waited := common.WaitCriticalTasks(critCtx)
		cancelCrit()
		if !waited {
			// 只能如实记下来：这些退款可能已经落在 store 外，或写到一半被进程退出打断。
			common.SysLog(fmt.Sprintf(
				"graceful shutdown: %d critical background task(s) did not finish in time; "+
					"some refunds / dashboard deltas may be lost", common.CriticalTaskCount()))
		}
	}

	// 4) 刷内存里累积的两处状态：额度变动 + 数据看板增量（此时已无新生产者）
	model.FlushBatchUpdate()
	model.FlushQuotaDataCache()

	// 5) 排空审核日志队列
	drainBudget := time.Until(deadline)
	if drainBudget < time.Second {
		// 前面用光了预算也要给它一点时间：这是唯一一次落库机会。
		drainBudget = time.Second
	}
	if model.DrainModerationLogQueue(drainBudget) {
		common.SysLog("graceful shutdown: moderation log queue drained")
	}

	// 6) pprof（debug 端点，与流量无关，一并关掉更干净）
	if pprofServer != nil {
		_ = pprofServer.Close()
	}
}

func InjectUmamiAnalytics() {
	analyticsInjectBuilder := &strings.Builder{}
	if os.Getenv("UMAMI_WEBSITE_ID") != "" {
		umamiSiteID := os.Getenv("UMAMI_WEBSITE_ID")
		umamiScriptURL := os.Getenv("UMAMI_SCRIPT_URL")
		if umamiScriptURL == "" {
			umamiScriptURL = "https://analytics.umami.is/script.js"
		}
		analyticsInjectBuilder.WriteString("<script defer src=\"")
		analyticsInjectBuilder.WriteString(umamiScriptURL)
		analyticsInjectBuilder.WriteString("\" data-website-id=\"")
		analyticsInjectBuilder.WriteString(umamiSiteID)
		analyticsInjectBuilder.WriteString("\"></script>")
	}
	analyticsInjectBuilder.WriteString("<!--Umami QuantumNous-->\n")
	analyticsInject := []byte(analyticsInjectBuilder.String())
	placeholder := []byte("<!--umami-->\n")
	classicIndexPage = bytes.ReplaceAll(classicIndexPage, placeholder, analyticsInject)
}

func InjectGoogleAnalytics() {
	analyticsInjectBuilder := &strings.Builder{}
	if os.Getenv("GOOGLE_ANALYTICS_ID") != "" {
		gaID := os.Getenv("GOOGLE_ANALYTICS_ID")
		// Google Analytics 4 (gtag.js)
		analyticsInjectBuilder.WriteString("<script async src=\"https://www.googletagmanager.com/gtag/js?id=")
		analyticsInjectBuilder.WriteString(gaID)
		analyticsInjectBuilder.WriteString("\"></script>")
		analyticsInjectBuilder.WriteString("<script>")
		analyticsInjectBuilder.WriteString("window.dataLayer = window.dataLayer || [];")
		analyticsInjectBuilder.WriteString("function gtag(){dataLayer.push(arguments);}")
		analyticsInjectBuilder.WriteString("gtag('js', new Date());")
		analyticsInjectBuilder.WriteString("gtag('config', '")
		analyticsInjectBuilder.WriteString(gaID)
		analyticsInjectBuilder.WriteString("');")
		analyticsInjectBuilder.WriteString("</script>")
	}
	analyticsInjectBuilder.WriteString("<!--Google Analytics QuantumNous-->\n")
	analyticsInject := []byte(analyticsInjectBuilder.String())
	placeholder := []byte("<!--Google Analytics-->\n")
	classicIndexPage = bytes.ReplaceAll(classicIndexPage, placeholder, analyticsInject)
}

func InitResources() error {
	// Initialize resources here if needed
	// This is a placeholder function for future resource initialization
	// Load order: .env.local overrides .env (godotenv.Load does not override already-set vars)
	godotenv.Load(".env.local", ".env")

	var err error

	// 加载环境变量
	common.InitEnv()

	logger.SetupLogger()

	// Initialize model settings
	ratio_setting.InitRatioSettings()

	service.InitHttpClient()

	service.InitTokenEncoders()

	// Initialize SQL Database
	err = model.InitDB()
	if err != nil {
		common.FatalLog("failed to initialize database: " + err.Error())
		return err
	}

	model.CheckSetup()

	// Initialize options, should after model.InitDB()
	model.InitOptionMap()

	// Surface payment-sandbox state once DB-loaded options are in effect
	controller.LogAlipaySandboxStatus()

	// 清理旧的磁盘缓存文件
	common.CleanupOldCacheFiles()

	// 初始化模型
	model.GetPricing()

	// Initialize SQL Database
	err = model.InitLogDB()
	if err != nil {
		return err
	}

	// Initialize Redis
	err = common.InitRedisClient()
	if err != nil {
		return err
	}

	perfmetrics.Init()

	// 启动系统监控
	common.StartSystemMonitor()

	// Initialize i18n
	err = i18n.Init()
	if err != nil {
		common.SysError("failed to initialize i18n: " + err.Error())
		// Don't return error, i18n is not critical
	} else {
		common.SysLog("i18n initialized with languages: " + strings.Join(i18n.SupportedLanguages(), ", "))
	}
	// Register user language loader for lazy loading
	i18n.SetUserLangLoader(model.GetUserLanguage)

	// Load custom OAuth providers from database
	err = oauth.LoadCustomProviders()
	if err != nil {
		common.SysError("failed to load custom OAuth providers: " + err.Error())
		// Don't return error, custom OAuth is not critical
	}

	return nil
}
