package relay

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// 这组测试覆盖的是「WebSocket 连接在优雅退出时会不会被漏掉」。
//
// 为什么值得单独测：http.Server.Shutdown 不等待 hijacked 连接，所以 WS 完全靠这里的
// 注册表兜。这块逻辑平时只在删 Pod 时执行一次，没有测试就等于没验证过——而两种回归
// 都很难在生产上被发现：漏等会静默切断 realtime 会话，多等会让滚动升级卡到 SIGKILL。

func waitForWSConnCount(t *testing.T, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if got := ActiveWebSocketCount(); got == want {
			return
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("等待 WebSocket 连接数变为 %d 超时，当前 %d", want, ActiveWebSocketCount())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// newTestWSServer 起一个真实的 WebSocket 服务端，handler 里按被测代码的方式登记连接，
// 并保持连接直到对端断开（模拟 realtime 那种长期存活的会话）。
func newTestWSServer(t *testing.T) string {
	t.Helper()
	var upgrader websocket.Upgrader
	// 这个 httptest.Server 的监听地址是 127.0.0.1，同源检查会通过；
	// 仍显式放开，避免不同 Go 版本对 Origin 的处理差异导致偶发失败。
	upgrader.CheckOrigin = func(*http.Request) bool { return true }

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		untrack := TrackWebSocket(conn)
		defer untrack()
		defer conn.Close()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	// Cleanup 是 LIFO：先注册 srv.Close（最后跑），拨号后注册的 client.Close 先跑。
	// 顺序反了的话 httptest.Server.Close 会一直等 hijacked 连接结束，测试直接挂死。
	t.Cleanup(srv.Close)

	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func dialTestWS(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("拨号失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func TestWaitWebSocketsReturnsImmediatelyWhenIdle(t *testing.T) {
	waitForWSConnCount(t, 0, 2*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	start := time.Now()
	if !WaitWebSockets(ctx) {
		t.Fatal("没有 WebSocket 连接时应判定为已结束")
	}
	// 这条断言防的是"每次关闭都白等一个轮询周期"：升级频率高时，那个延迟会累加。
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("没有连接时应立即返回，实际耗时 %s", elapsed)
	}
}

func TestWebSocketRegistryTracksAndReleases(t *testing.T) {
	url := newTestWSServer(t)
	waitForWSConnCount(t, 0, 2*time.Second)

	client := dialTestWS(t, url)
	waitForWSConnCount(t, 1, 2*time.Second)

	// 对端主动断开后，注册表应自动清理（靠 handler 里的 defer untrack）。
	_ = client.Close()
	waitForWSConnCount(t, 0, 2*time.Second)

	// 清理干净之后，等待应当立刻成功——说明退出流程不会为已结束的会话多等。
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if !WaitWebSockets(ctx) {
		t.Fatal("连接已全部结束，WaitWebSockets 应返回 true")
	}
}

func TestWaitWebSocketsTimesOutOnLiveConnection(t *testing.T) {
	url := newTestWSServer(t)
	waitForWSConnCount(t, 0, 2*time.Second)

	client := dialTestWS(t, url)
	waitForWSConnCount(t, 1, 2*time.Second)

	// 对端不会自己结束 → 宽限期用完必须如实返回 false（不能假装成功）。
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	if WaitWebSockets(ctx) {
		t.Fatal("连接仍然存活，WaitWebSockets 不应返回 true")
	}
	if elapsed := time.Since(start); elapsed < 250*time.Millisecond {
		t.Fatalf("应等到宽限期结束才返回，实际只用了 %s", elapsed)
	}

	// 宽限期到了就要能强制收口，否则进程会被 SIGKILL，连刷库都做不完。
	if n := CloseAllWebSockets(); n != 1 {
		t.Fatalf("应强制关闭 1 条连接，实际 %d", n)
	}
	waitForWSConnCount(t, 0, 2*time.Second)

	// 客户端应当收到 CloseGoingAway，而不是被直接掐断——这样客户端知道是服务端
	// 正常关闭、可以重连，而不是网络故障。
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err := client.ReadMessage()
	if err == nil {
		t.Fatal("客户端应读到关闭")
	}
	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) && closeErr.Code != websocket.CloseGoingAway {
		t.Fatalf("关闭码应为 %d(CloseGoingAway)，实际 %d", websocket.CloseGoingAway, closeErr.Code)
	}
}

func TestCloseAllWebSocketsOnEmptyRegistryIsNoop(t *testing.T) {
	waitForWSConnCount(t, 0, 2*time.Second)
	if n := CloseAllWebSockets(); n != 0 {
		t.Fatalf("没有连接时应返回 0，实际 %d", n)
	}
}
