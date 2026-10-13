package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 未配置 TRUSTED_PROXY_CIDR 时，**绝不能采信 X-Forwarded-For**。
//
// 这条防的是一个没有运行时症状的回归：gin.New() 的默认可信网段是 0.0.0.0/0（谁都信），
// 一旦有人把"未配置就显式设 nil"改回"未配置就不调用 SetTrustedProxies"，客户端自己
// 加一个 X-Forwarded-For 头就能伪装成任意 IP——按 IP 的限流被绕过、日志里的来源 IP 失真，
// 而且测试之外不会有任何报错。
func TestTrustedProxyDefaultsToNobody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("未配置时忽略 X-Forwarded-For", func(t *testing.T) {
		probe := newClientIPProbe(t, "")

		require.Equal(t, "203.0.113.9", probe("203.0.113.9:1234", "1.2.3.4"),
			"未配置代理时，客户端伪造的 X-Forwarded-For 不得被采信")
	})

	t.Run("配置了代理时按其网段采信", func(t *testing.T) {
		probe := newClientIPProbe(t, "192.0.2.0/24")

		// 对端在可信网段内 → 采信 XFF 最左边那个（即真实客户端）
		require.Equal(t, "1.2.3.4", probe("192.0.2.10:1234", "1.2.3.4"),
			"对端是可信代理时，应取 XFF 里的真实客户端 IP")
		// 对端不在可信网段内 → 仍然不采信
		require.Equal(t, "203.0.113.9", probe("203.0.113.9:1234", "1.2.3.4"),
			"对端不在可信网段时，不得采信它带来的 X-Forwarded-For")
	})
}

// newClientIPProbe 按给定配置建一个只暴露 /probe 的引擎，返回"发一个请求、取回它看到的
// ClientIP"的函数。每个用例建自己的引擎：gin 对同一路径重复注册会直接 panic。
func newClientIPProbe(t *testing.T, cidr string) func(remoteAddr, forwardedFor string) string {
	t.Helper()
	t.Setenv("TRUSTED_PROXY_CIDR", cidr)

	engine := gin.New()
	configureTrustedProxies(engine)

	var seen string
	engine.GET("/probe", func(c *gin.Context) { seen = c.ClientIP() })

	return func(remoteAddr, forwardedFor string) string {
		seen = ""
		req := httptest.NewRequest(http.MethodGet, "/probe", nil)
		req.RemoteAddr = remoteAddr
		if forwardedFor != "" {
			req.Header.Set("X-Forwarded-For", forwardedFor)
		}
		engine.ServeHTTP(httptest.NewRecorder(), req)
		return seen
	}
}

// 优雅退出的核心承诺：滚动升级时在途请求不被切断。这里用真实监听端口 + 真实 HTTP 请求
// 验证，而不是只测各部件——各部件都测过但拼起来顺序错了，是这类改动最常见的翻车方式。
//
// 注意 common.MarkShuttingDown() 是不可撤销的全局状态，会持续影响整个 main 测试二进制。
// 本包的用例都只调用 shutdownGracefully（它自己会置位），没有哪个断言"初始是健康态"，
// 所以互不依赖、可以单独跑——**不要**给它们加"要求标志已置位"的 skip 守卫，
// 那会让单独执行时静默跳过、假装配过。
func TestShutdownGracefullyDrainsInFlightRequest(t *testing.T) {
	origDelay := shutdownDrainDelay
	shutdownDrainDelay = 0
	t.Cleanup(func() { shutdownDrainDelay = origDelay })

	requestStarted := make(chan struct{})
	requestFinished := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/slow", func(w http.ResponseWriter, _ *http.Request) {
		close(requestStarted)
		// 模拟一个正在输出的 streaming 请求。Shutdown 必须等它结束，而不是当场切断。
		time.Sleep(400 * time.Millisecond)
		close(requestFinished)
		_, _ = w.Write([]byte("done"))
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	server := &http.Server{Handler: mux}
	go func() { _ = server.Serve(listener) }()

	requestErr := make(chan error, 1)
	go func() {
		resp, err := http.Get("http://" + listener.Addr().String() + "/slow")
		if err != nil {
			requestErr <- err
			return
		}
		_ = resp.Body.Close()
		requestErr <- nil
	}()

	select {
	case <-requestStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("请求没能打到服务端")
	}

	// 收尾：给足预算
	shutdownGracefully(server, nil, 30*time.Second)

	// 1) 在途请求必须已经正常跑完（而不是被切断）
	select {
	case <-requestFinished:
	default:
		t.Fatal("在途请求被切断了——优雅退出没生效")
	}
	if err := <-requestErr; err != nil {
		t.Fatalf("在途请求应当正常完成，实际报错: %v", err)
	}

	// 2) 关闭标志必须已置位（readiness 靠它摘流量，且不能回到健康态）
	if !common.IsShuttingDown() {
		t.Fatal("收尾后关闭标志应为已置位，否则负载均衡还会把新流量送进来")
	}

	// 3) 监听端口必须已关闭，不再接受新连接
	if _, err := net.DialTimeout("tcp", listener.Addr().String(), 500*time.Millisecond); err == nil {
		t.Fatal("收尾后监听端口应已关闭")
	}
}

// 收尾预算被前面的等待耗尽时，也不能卡住不返回——进程还在等 shutdownDone，
// 这里挂住的话 Pod 会被 SIGKILL，反倒连刷库的机会都没有。
func TestShutdownGracefullyReturnsOnExhaustedBudget(t *testing.T) {
	origDelay, origReserve := shutdownDrainDelay, shutdownFinalReserve
	shutdownDrainDelay, shutdownFinalReserve = 0, 0
	t.Cleanup(func() {
		shutdownDrainDelay, shutdownFinalReserve = origDelay, origReserve
	})

	// 起一个永远不返回的 handler，逼出"预算耗尽 → 强制关闭"这条路径
	release := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/hang", func(w http.ResponseWriter, _ *http.Request) {
		<-release
		_, _ = w.Write([]byte("late"))
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	t.Cleanup(func() { close(release) })
	server := &http.Server{Handler: mux}
	go func() { _ = server.Serve(listener) }()

	go func() {
		resp, err := http.Get("http://" + listener.Addr().String() + "/hang")
		if err == nil {
			_ = resp.Body.Close()
		}
	}()
	// 等请求真正打进来，否则 Shutdown 时它是 idle 的，走不到"等超时"这条路径
	time.Sleep(200 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		defer close(done)
		// total 给 1 秒，handler 永不返回 → 必须靠 Close 收口并按时返回
		shutdownGracefully(server, nil, time.Second)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("预算耗尽时 shutdownGracefully 也必须返回，否则进程会挂到 SIGKILL")
	}
}

// waitCriticalIdle 等到关键任务计数归零，作为用例的基线。
//
// 为什么需要：前一个用例的 GoCritical 任务是在 t.Cleanup 里被放行的，它的还账是异步的，
// 可能在本用例取基线之后才发生——于是 "期望回到基线" 这类断言会一直等不到而超时
// （TestGoCriticalReleasesCountOnPanic 就这么失败过一次，单独跑却通过）。
// 先对齐到 0 再取基线，用例之间就没有隐含顺序依赖。
func waitCriticalIdle(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if common.CriticalTaskCount() == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("关键任务计数没有归零（当前 %d）：同包其它用例可能没放行它登记的任务",
		common.CriticalTaskCount())
}

// newIdleServer 起一个没有任何请求的监听端口，供收尾用例使用（收尾会关它的监听器）。
func newIdleServer(t *testing.T) *http.Server {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	server := &http.Server{Handler: http.NewServeMux()}
	go func() { _ = server.Serve(listener) }()
	return server
}

// 收尾必须等「退出前必须做完」的后台任务——失败请求的退款就跑在 gopool 里，
// **http.Server.Shutdown 不会等它**（处理函数一返回，Shutdown 就认为请求结束了）。
// 不等的话退款要么晚于刷库进了再也没人刷的 store，要么在直写数据库时被进程退出打断，
// 两种都是用户被白扣。
//
// 这里用一个真实登记进去、被卡住不放的任务来验证"确实会等"，而不是只测等函数本身。
func TestShutdownWaitsForCriticalBackgroundTasks(t *testing.T) {
	waitCriticalIdle(t)

	origDelay, origReserve := shutdownDrainDelay, shutdownFinalReserve
	shutdownDrainDelay, shutdownFinalReserve = 0, 0
	t.Cleanup(func() {
		shutdownDrainDelay, shutdownFinalReserve = origDelay, origReserve
	})

	started := make(chan struct{})
	release := make(chan struct{})
	// 模拟一笔正在退款的 goroutine：登记进 critical，然后卡住（真实里是卡在数据库上）
	common.GoCritical(func() {
		close(started)
		<-release
	})
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("关键后台任务没能启动")
	}
	require.Equal(t, 1, common.CriticalTaskCount(), "退款任务应已被登记")

	server := newIdleServer(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		shutdownGracefully(server, nil, 30*time.Second)
	}()

	// 退款还没结束 → 收尾不该返回
	select {
	case <-done:
		t.Fatal("退款的 goroutine 还没结束，收尾就返回了——这笔退款会丢")
	case <-time.After(400 * time.Millisecond):
	}

	close(release)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("退款结束后收尾应能继续，而不是一直挂着")
	}
	require.Zero(t, common.CriticalTaskCount(), "收尾返回时计数应已归零")
}

// 预算被前面的等待耗尽时，等关键任务这一步也不能把进程挂住：如实记下来然后继续。
func TestShutdownReturnsEvenIfCriticalTasksHang(t *testing.T) {
	waitCriticalIdle(t)

	origDelay, origReserve := shutdownDrainDelay, shutdownFinalReserve
	shutdownDrainDelay, shutdownFinalReserve = 0, 0
	t.Cleanup(func() {
		shutdownDrainDelay, shutdownFinalReserve = origDelay, origReserve
	})

	release := make(chan struct{})
	started := make(chan struct{})
	common.GoCritical(func() {
		close(started)
		<-release // 永不主动结束，直到用例收尾
	})
	<-started
	t.Cleanup(func() { close(release) })

	server := newIdleServer(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		shutdownGracefully(server, nil, time.Second)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("关键任务卡住时收尾也必须返回，否则 Pod 会挂到 SIGKILL")
	}
}

// GoCritical 的任务 panic 时也必须把计数还回去（gopool 会接住 panic），
// 否则收尾会一直白等到预算用尽。
func TestGoCriticalReleasesCountOnPanic(t *testing.T) {
	waitCriticalIdle(t)

	before := common.CriticalTaskCount()
	common.GoCritical(func() { panic("boom") })

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if common.CriticalTaskCount() == before {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("panic 的任务没有还回计数：期望 %d，实际 %d", before, common.CriticalTaskCount())
}
