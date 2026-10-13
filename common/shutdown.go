package common

import "sync/atomic"

// shuttingDown 标记进程已进入优雅退出流程。
//
// 为什么需要这个标志位，而不是直接调 http.Server.Shutdown：
//
// Shutdown 保证的是「不再接新连接 + 等在途请求结束」，它**不负责让负载均衡把本实例摘掉**。
// ELB/Service 摘除后端有秒级延迟，这段窗口里新请求仍会被转发进来，而那时监听器已经关了，
// 用户看到的是连接被拒。所以顺序必须是：先置位这个标志让 readiness 探针失败 → 等一会儿
// 让 LB 摘掉本实例 → 再 Shutdown。
//
// 置位后不可撤销：探针要一直返回失败，直到进程退出。若中途变回成功，LB 可能又把流量送进来。
var shuttingDown atomic.Bool

// MarkShuttingDown 置位关闭标志。幂等，可在多个收尾路径重复调用。
func MarkShuttingDown() {
	shuttingDown.Store(true)
}

// IsShuttingDown 报告进程是否已进入关闭流程，供 /healthz 之类的探针端点读取。
func IsShuttingDown() bool {
	return shuttingDown.Load()
}
