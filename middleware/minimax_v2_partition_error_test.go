package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relay/minimaxv2"
)

// 线上真实响应（任务 tsk_7d17cf773ad5）：判据就是这两个子串，别改成"猜"。
const enginePartitionError = "task_type 'r2va' needs the MiniMax-H3 Ref2VA checkpoint, " +
	"but model 'minimax-h3-fl2va' is deployed from the FL2VA partition (serves t2va/fl2va only). " +
	"Point this task_type at a Ref2VA deployment."

// 分区错误要改写成客户端能照做的动作，并且回显**它自己传的那个模型名** ——
// 内部展开出来的段模型名它不认识，照着改也无从改起。
func TestRewritePartitionError(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v2/video_generation", nil)
	minimaxv2.StoreSnapshot(c, &minimaxv2.Snapshot{Model: "minimax-h3-2k", TaskType: "r2va"})

	got := rewritePartitionError(c, enginePartitionError)
	require.Contains(t, got, `model "minimax-h3-2k" cannot serve this request (task_type=r2va)`)
	require.Contains(t, got, `use model "MiniMax-H3"`)
	require.NotContains(t, got, "partition", "引擎原文不该再出现在给客户的 message 里")
}

// 只有这两类错误改写：其余错误原样透出，别把上游的可用信息也吞掉。
func TestRewritePartitionErrorLeavesOthersUntouched(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v2/video_generation", nil)
	minimaxv2.StoreSnapshot(c, &minimaxv2.Snapshot{Model: "MiniMax-H3", TaskType: "i2v"})

	original := "invalid params: duration must be an integer between 1 and 20 seconds, got 0"
	require.Equal(t, original, rewritePartitionError(c, original))
}

// 拿不到快照时不做改写：拼一句 `model "" cannot serve` 比原文更糟。
func TestRewritePartitionErrorWithoutSnapshot(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v2/video_generation", nil)

	require.Equal(t, enginePartitionError, rewritePartitionError(c, enginePartitionError))
	require.True(t, strings.Contains(rewritePartitionError(c, enginePartitionError), "partition"))
}

// 客户**已经在用** MiniMax-H3 时不能再叫它改用它自己 —— 那是网关的分派配置出了问题
// （AggregateModelConfig 少了 task_models，或生成段指错），改 model 解决不了。
func TestRewritePartitionErrorWhenClientAlreadyUsesRoutingModel(t *testing.T) {
	for _, model := range []string{"MiniMax-H3", "minimax-h3", " MiniMax-H3 "} {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v2/video_generation", nil)
		c.Set(common.RequestIdKey, "req-abc123")
		minimaxv2.StoreSnapshot(c, &minimaxv2.Snapshot{Model: model, TaskType: "r2va"})

		got := rewritePartitionError(c, enginePartitionError)
		require.NotContains(t, got, `use model "MiniMax-H3"`, "不能叫客户改用他自己正在用的名字：%q", model)
		require.Contains(t, got, "gateway configuration problem")
		require.Contains(t, got, `keep request_id "req-abc123"`)
		require.Contains(t, got, "task_type=r2va")
	}
}

// 没有 request_id 时也不能拼一句空的 —— 改成"保留本响应里的 request_id"。
func TestRewritePartitionErrorWithoutRequestID(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v2/video_generation", nil)
	minimaxv2.StoreSnapshot(c, &minimaxv2.Snapshot{Model: "MiniMax-H3", TaskType: "r2va"})

	got := rewritePartitionError(c, enginePartitionError)
	require.Contains(t, got, "keep the request_id from this response")
	require.NotContains(t, got, `""`)
}
