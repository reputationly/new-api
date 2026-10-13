package main

import (
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// 优雅退出的核心承诺：滚动升级时在途请求不被切断。这里用真实监听端口 + 真实 HTTP 请求
// 验证，而不是只测各部件——各部件都测过但拼起来顺序错了，是这类改动最常见的翻车方式。
//
// 注意 common.MarkShuttingDown() 是不可撤销的全局状态，会持续影响整个 main 测试二进制。
// 目前本包只有这一个测试；以后再加涉及 /healthz 或关闭标志的用例，必须放在同一个函数里，
// 或先引入可重置的状态。
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
	requireShuttingDownAlreadySet(t)

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

// requireShuttingDownAlreadySet 记录本包内关闭标志的不可逆性：
// TestShutdownGracefullyDrainsInFlightRequest 已置位，后续用例不能再假设"初始是健康态"。
func requireShuttingDownAlreadySet(t *testing.T) {
	t.Helper()
	if !common.IsShuttingDown() {
		t.Skip("关闭标志尚未置位：本用例依赖它在同一二进制内先被前一个用例置位")
	}
}
