package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/gin-gonic/gin"
)

// /healthz 是优雅退出的"先摘流量"那一步的执行者：K8s 靠 readiness 失败把本 Pod
// 从 ELB 后端摘掉，之后进程才调 Shutdown。它一旦回归成"永远 200"，滚动升级就会
// 在摘流量之前开始关连接，用户看到连接被拒；回归成"永远 503"，Pod 永远不 Ready。
//
// 两个断言必须放在同一个测试函数里：MarkShuttingDown 是不可撤销的全局状态，
// 拆成两个函数的话执行顺序没有保证，"健康时应 200"那条会随机失败。
func TestHealthzContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetHealthRouter(engine)

	probe := func() int {
		t.Helper()
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
		return w.Code
	}

	// 1) 正常运行 → 200
	if got := probe(); got != http.StatusOK {
		t.Fatalf("实例正常时 /healthz 应返回 200，实际 %d", got)
	}

	// 2) 进入关闭流程 → 503
	common.MarkShuttingDown()
	if got := probe(); got != http.StatusServiceUnavailable {
		t.Fatalf("关闭流程中 /healthz 应返回 503，实际 %d", got)
	}

	// 3) 而且必须保持 503：中途回到 200 的话，负载均衡可能又把新流量送进来，
	//    而那时监听器已经关了。
	if got := probe(); got != http.StatusServiceUnavailable {
		t.Fatalf("/healthz 在关闭流程中回到 %d，应为稳定的 503", got)
	}
}
