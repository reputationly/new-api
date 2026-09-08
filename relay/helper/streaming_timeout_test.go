package helper

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
)

// runScannerWithTimeout 直接打真实入口 StreamScannerHandler(不复现它的算法),
// 返回它是否 panic。刻意不用 setupStreamTest —— 那个 helper 会把
// StreamingTimeout 强设成 30,正好盖住本用例要验的东西。
func runScannerWithTimeout(t *testing.T, streamingTimeout int) (panicked any) {
	t.Helper()

	old := constant.StreamingTimeout
	constant.StreamingTimeout = streamingTimeout
	t.Cleanup(func() { constant.StreamingTimeout = old })

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	resp := &http.Response{Body: io.NopCloser(strings.NewReader("data: {\"x\":1}\n\ndata: [DONE]\n\n"))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	defer func() { panicked = recover() }()
	StreamScannerHandler(c, resp, info, func(data string, sr *StreamResult) {})
	return nil
}

// constant.StreamingTimeout 拿到非正值有两条路:显式配 STREAMING_TIMEOUT=0
// (GetEnvOrDefault 只在环境变量**为空**时才回落默认值),或没走 common 初始化
// (包级 var 零值)。没有兜底的话 time.NewTicker 直接 panic —— 被 gin.CustomRecovery
// 兜成 500「系统异常」,不崩进程,但所有流式请求全废且报错指错方向。
//
// 这也是 `go test ./...` 偶发 panic 的根因:stream_scanner_test.go 的
// setupStreamTest 用 t.Cleanup 把全局变量恢复成零值 0,而那些用例是 t.Parallel 的,
// 一个用例收尾时把它清零,正在并行跑的另一个用例就撞上 NewTicker(0)。
func TestStreamScannerHandlerSurvivesNonPositiveTimeout(t *testing.T) {
	for _, timeout := range []int{0, -1} {
		if p := runScannerWithTimeout(t, timeout); p != nil {
			t.Fatalf("StreamingTimeout=%d: StreamScannerHandler panicked: %v", timeout, p)
		}
	}
}

// 配了正值就走配的值,兜底不能把它盖掉。
func TestStreamScannerHandlerHonoursConfiguredTimeout(t *testing.T) {
	if p := runScannerWithTimeout(t, 30); p != nil {
		t.Fatalf("StreamingTimeout=30: StreamScannerHandler panicked: %v", p)
	}
}
