package relay

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

func aggTask(publicModel string) *model.Task {
	t := &model.Task{}
	if publicModel != "" {
		t.PrivateData.Aggregate = &model.TaskAggregateInfo{PublicModel: publicModel}
	}
	return t
}

// 客户调的是聚合模型名,响应就该回显它 —— 任务里记的是展开后的生成段模型(计费按它走),
// 直接回显那个名字既不符合"请求什么返回什么",也把内部编排暴露了。
func TestAggregateModelEchoRewritesModel(t *testing.T) {
	out := applyAggregateModelEcho(aggTask("h3-2k"),
		[]byte(`{"id":"v1","model":"minimax-h3","status":"completed"}`))

	var got map[string]any
	require.NoError(t, common.Unmarshal(out, &got))
	require.Equal(t, "h3-2k", got["model"])
	// 其余字段不得丢失。
	require.Equal(t, "v1", got["id"])
	require.Equal(t, "completed", got["status"])
}

// 非聚合任务原样返回,一个字节都不该动。
func TestAggregateModelEchoLeavesNormalTaskUntouched(t *testing.T) {
	raw := []byte(`{"id":"v1","model":"minimax-h3"}`)
	require.Equal(t, raw, applyAggregateModelEcho(aggTask(""), raw))
	require.Equal(t, raw, applyAggregateModelEcho(nil, raw))
}

// 响应本身没有 model 字段时不凭空加 —— 不同端点响应形状不同,
// 注入未预期的字段可能让客户端的严格解析失败。
func TestAggregateModelEchoDoesNotInjectField(t *testing.T) {
	raw := []byte(`{"id":"v1","status":"queued"}`)
	require.Equal(t, raw, applyAggregateModelEcho(aggTask("h3-2k"), raw))
}

// 坏 JSON / 空响应一律原样返回:回显是锦上添花,不该让一个本来正常的响应损坏。
func TestAggregateModelEchoDegradesOnBadInput(t *testing.T) {
	for _, raw := range [][]byte{[]byte(`not json`), []byte(``), nil} {
		require.Equal(t, raw, applyAggregateModelEcho(aggTask("h3-2k"), raw))
	}
}
