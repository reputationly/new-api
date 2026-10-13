package relay

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// 强关 WebSocket 之后，**不能**把注册表清空——连接被关掉不等于会话结束。
//
// 真实的时序：handler 在 DoResponse 返回之后还要走 PostWssConsumeQuota 结算
// （relay/websocket.go），注销是那个 handler 自己的 defer、要等它整个返回才跑。
// 如果 CloseAllWebSockets 一上来就把名单清空，ActiveWebSocketCount 立刻归零，
// 收尾就会以为 WS 全处理完了、直接去刷库——结算要么晚于刷库进了再也没人刷的 store，
// 要么写到一半被进程退出打断。被强关的恰好是撑过整段宽限期的长会话，单笔金额最大。
//
// 这个用例用一个"关掉连接后还要结算 300ms 才返回"的 handler 来复现那个中间态。
// 旧实现（清空名单）下，关完之后计数立刻是 0 → 下面的断言会失败。
func TestForceCloseKeepsSessionRegisteredUntilHandlerReturns(t *testing.T) {
	var upgrader websocket.Upgrader
	upgrader.CheckOrigin = func(*http.Request) bool { return true }

	settling := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		untrack := TrackWebSocket(conn)
		defer untrack()
		defer conn.Close()

		// 阻塞到连接被关（对端正常断开或被强制关闭）
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				break
			}
		}
		// 连接已经没了，但 handler 还在干活：模拟 PostWssConsumeQuota 的结算
		close(settling)
		time.Sleep(300 * time.Millisecond)
	}))
	// Cleanup 是 LIFO：先注册 srv.Close（后跑），client.Close 先跑
	t.Cleanup(srv.Close)

	client, _, err := websocket.DefaultDialer.Dial("ws"+srv.URL[len("http"):], nil)
	if err != nil {
		t.Fatalf("拨号失败: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	waitForWSConnCount(t, 1, 3*time.Second)

	if n := CloseAllWebSockets(); n != 1 {
		t.Fatalf("应发起关闭 1 条连接，实际 %d", n)
	}
	select {
	case <-settling:
	case <-time.After(3 * time.Second):
		t.Fatal("连接没被关掉")
	}

	// 关键断言：连接已关，但 handler 还没返回（正在结算）→ 必须仍能观察到它
	if got := ActiveWebSocketCount(); got != 1 {
		t.Fatalf("连接被强关后 handler 仍在结算，注册表里应还能看到它（实际 %d）；"+
			"清空名单会让收尾以为 WS 都结束了、直接去刷库，这笔结算就丢了", got)
	}

	// 而且等待必须一直到结算结束才返回，不能立刻返回 true
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	if !WaitWebSockets(ctx) {
		t.Fatal("应一直等到结算结束")
	}
	if elapsed := time.Since(start); elapsed < 200*time.Millisecond {
		t.Fatalf("应在结算完成之前一直等待，实际只等了 %s", elapsed)
	}
}
