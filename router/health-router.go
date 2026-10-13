package router

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"

	"github.com/gin-gonic/gin"
)

// SetHealthRouter 挂载轻量探针端点 /healthz。
//
// 为什么不复用 /api/status：
//   - 那个接口返回整份状态 JSON（上百个字段，含定价配置、OAuth 配置等），每次探测都序列化一遍；
//   - 它挂在 /api 组下，要过 gzip.Gzip 和 GlobalAPIRateLimit —— 探针每秒都在打，
//     既没必要占限流配额，也不该因为限流被拒而让 K8s 误判实例不健康。
//
// 必须在 SetRouter 的最前面调用（与 SetWellKnownRouter 同理）：gin 在注册路由时会把当前的
// 全局中间件链快照进去，早于限流 / gzip / 移动端重定向注册，这条路由的中间件链才是干净的。
//
// 关闭期间返回 503 而不是 200：K8s 靠 readiness 失败把本 Pod 从 ELB 后端摘掉，这是优雅退出
// 「先摘流量、再等在途请求」那一步的执行者。状态一直保持到进程退出（见 common/shutdown.go）。
//
// 用法建议：
//   - readinessProbe → /healthz（关掉时立刻失败，这正是我们要的）
//   - livenessProbe  → /healthz 或 TCP 3000 都行。不要把 liveness 单独指到 /api/status：
//     那个接口依赖数据库，数据库抖一下就会触发容器重启，而重启解决不了数据库的问题。
func SetHealthRouter(router *gin.Engine) {
	router.GET("/healthz", func(c *gin.Context) {
		if common.IsShuttingDown() {
			c.String(http.StatusServiceUnavailable, "shutting down\n")
			return
		}
		c.String(http.StatusOK, "ok\n")
	})
}
