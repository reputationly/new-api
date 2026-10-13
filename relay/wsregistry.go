package relay

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/gorilla/websocket"
)

// WebSocket 连接注册表。
//
// 为什么必须单独记账：WebSocket 握手成功后连接被 hijack，而 http.Server.Shutdown
// **不会等待 hijacked 连接**——它只等在正常 HTTP 生命周期里的请求。不自己记账的话，
// 滚动升级时 realtime 语音会话会被直接切断，而且 Shutdown 会立刻返回，日志上看
// 「优雅退出成功」，实际上该等的根本没等。
//
// 只登记**服务端**（网关面向客户端）那条连接，也就是 controller.Relay 里 Upgrade 出来的
// 那条。info.TargetWs（网关连上游的那条）由调用方自己 defer Close，不走这里。
var (
	wsRegistryMu sync.Mutex
	wsRegistry   = make(map[*websocket.Conn]struct{})
)

// TrackWebSocket 登记一条已升级的连接，返回注销函数，调用方应当 defer 它。
// conn 为 nil 时返回空操作，便于调用方无脑 defer。
func TrackWebSocket(conn *websocket.Conn) func() {
	if conn == nil {
		return func() {}
	}
	wsRegistryMu.Lock()
	wsRegistry[conn] = struct{}{}
	wsRegistryMu.Unlock()
	return func() {
		wsRegistryMu.Lock()
		delete(wsRegistry, conn)
		wsRegistryMu.Unlock()
	}
}

// ActiveWebSocketCount 当前尚未结束的服务端 WebSocket 连接数。
func ActiveWebSocketCount() int {
	wsRegistryMu.Lock()
	defer wsRegistryMu.Unlock()
	return len(wsRegistry)
}

// WaitWebSockets 等待所有 WebSocket 连接自然结束，直到 ctx 到期。
// 返回 true 表示连接已全部结束，可以安全退出。
//
// 用轮询而不是 sync.WaitGroup：连接是陆续结束的、且没有"全部到齐"的收敛点，
// WaitGroup 得配一个不断变化的 Add/Done 计数，反而更容易出错。轮询 200ms 的代价可以忽略
// （只在关闭流程里跑）。
func WaitWebSockets(ctx context.Context) bool {
	if ActiveWebSocketCount() == 0 {
		return true
	}
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ActiveWebSocketCount() == 0
		case <-ticker.C:
			if ActiveWebSocketCount() == 0 {
				return true
			}
		}
	}
}

// CloseAllWebSockets 强制关闭所有存活连接，返回关闭的条数。
//
// 只在宽限期用完时调用：不能让滚动升级为了几个长期存活的 realtime 会话无限等下去，
// 否则 Pod 会被 SIGKILL，连刷库都做不完。先发一帧 CloseGoingAway 让对端知道这是
// 服务端正常关闭（客户端可以据此重连），再关底层连接。
func CloseAllWebSockets() int {
	wsRegistryMu.Lock()
	conns := make([]*websocket.Conn, 0, len(wsRegistry))
	for conn := range wsRegistry {
		conns = append(conns, conn)
	}
	// 先清空注册表再关连接：关闭过程会触发各请求的 defer 注销，重复 delete 无害，
	// 但清空能保证 ActiveWebSocketCount 立刻归零，不会被"关了一半"的中间态骗到。
	wsRegistry = make(map[*websocket.Conn]struct{})
	wsRegistryMu.Unlock()

	for _, conn := range conns {
		// WriteControl 是文档明确允许与其它写操作并发调用的，不需要额外加锁。
		_ = conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseGoingAway, "server shutting down"),
			time.Now().Add(time.Second),
		)
		_ = conn.Close()
	}
	if len(conns) > 0 {
		common.SysLog(fmt.Sprintf("graceful shutdown: force-closed %d websocket connection(s)", len(conns)))
	}
	return len(conns)
}
