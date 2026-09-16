package relay

import (
	"testing"

	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/require"
)

// 各渠道适配器的 ConvertToOpenAIVideo 只看得到 Status（取消复用的是 FAILURE 终态），
// 所以对外的 cancelled 状态由这一刀统一补上。
func TestApplyCancelledEcho(t *testing.T) {
	cancelled := &model.Task{TaskID: "task_x", Status: model.TaskStatusFailure}
	cancelled.PrivateData.Cancelled = true

	t.Run("改写状态并保留错误原因", func(t *testing.T) {
		out := applyCancelledEcho(cancelled,
			[]byte(`{"id":"task_x","status":"failed","error":{"message":"用户取消"}}`))
		require.JSONEq(t, `{"id":"task_x","status":"cancelled","error":{"message":"用户取消"}}`, string(out))
	})

	t.Run("没被取消的任务原样返回", func(t *testing.T) {
		normal := &model.Task{TaskID: "task_y", Status: model.TaskStatusFailure}
		in := `{"id":"task_y","status":"failed"}`
		require.Equal(t, in, string(applyCancelledEcho(normal, []byte(in))))
	})

	t.Run("不凭空注入 status 字段", func(t *testing.T) {
		// 不同端点的响应形状不同，凭空注入可能让客户端的严格解析失败
		// （与 applyAggregateModelEcho 同理）。
		in := `{"id":"task_x"}`
		require.Equal(t, in, string(applyCancelledEcho(cancelled, []byte(in))))
	})

	t.Run("实时拉取不复活已取消的任务", func(t *testing.T) {
		// Gemini/Vertex 的实时拉取会无条件覆盖 task.Status 并落库，而它们没实现
		// channel.TaskCanceller，取消后上游还在跑。不守这一道的话，一次普通的 GET
		// 就能把已退款的任务拉回未完成态、重新进入轮询与结算。
		//
		// 守卫在函数最前面，所以这条路径完全不碰 DB —— 去掉它，下面这行会走到
		// model.GetChannelById 上（本包测试没有 DB）。
		require.Nil(t, tryRealtimeFetch(cancelled, true))
	})

	t.Run("非 JSON 与空值不炸", func(t *testing.T) {
		require.Equal(t, "not json", string(applyCancelledEcho(cancelled, []byte("not json"))))
		require.Nil(t, applyCancelledEcho(cancelled, nil))
		require.Nil(t, applyCancelledEcho(nil, nil))
	})
}
