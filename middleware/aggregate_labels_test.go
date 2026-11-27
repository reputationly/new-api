package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
)

// 展开阶段写入的使用日志标签:**生成段一律标段名**。
//
// 早先只在"超分真要跑"时才标,于是图片/音乐与单段视频的日志只有一个模型名,
// 客户与运营对着"增强段/生成段/超分段"几行账单时,第一行没有段名只能猜。
func TestAggregateLabelsAlwaysMarkGenerateStage(t *testing.T) {
	expandLabels := func(t *testing.T, cfg, body string) map[string]string {
		t.Helper()
		withAggregateConfig(t, cfg)
		agg := common.GetAggregateModel("h3-2k")
		require.NotNil(t, agg)
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(body))
		require.NoError(t, applyExpansionForTest(c, "h3-2k", "minimax-h3", agg))
		v, ok := common.GetContextKey(c, constant.ContextKeyAggregateLabels)
		require.True(t, ok, "展开后必须写入日志标签")
		labels, ok := v.(map[string]string)
		require.True(t, ok)
		return labels
	}

	t.Run("超分要跑:标成生成段,带聚合名", func(t *testing.T) {
		labels := expandLabels(t, `[{"name":"h3-2k","type":"video","enabled":true,
			"generate":{"model":"minimax-h3","overrides":{"size":"768P"}},
			"upscale":{"model":"seedvr2","target_size":"2k"}}]`,
			`{"model":"h3-2k","prompt":"a cat","size":"2k"}`)
		require.Equal(t, "h3-2k", labels["aggregate_model"])
		require.Equal(t, "generate", labels["pipeline_stage"])
	})

	t.Run("客户要的档不高于生成段,超分跳过:仍然标生成段", func(t *testing.T) {
		labels := expandLabels(t, `[{"name":"h3-2k","type":"video","enabled":true,
			"generate":{"model":"minimax-h3","overrides":{"size":"768P"}},
			"upscale":{"model":"seedvr2","target_size":"2k"}}]`,
			`{"model":"h3-2k","prompt":"a cat","size":"768P"}`)
		require.Equal(t, "h3-2k", labels["aggregate_model"])
		require.Equal(t, "generate", labels["pipeline_stage"], "超分跳过也还是生成段")
	})

	t.Run("没有超分段的单段聚合:仍带生成段", func(t *testing.T) {
		labels := expandLabels(t, `[{"name":"h3-2k","type":"video","enabled":true,
			"generate":{"model":"minimax-h3","overrides":{"size":"768P"}}}]`,
			`{"model":"h3-2k","prompt":"a cat","size":"2k"}`)
		require.Equal(t, "h3-2k", labels["aggregate_model"])
		require.Equal(t, "generate", labels["pipeline_stage"], "单段聚合同样要标生成段")
	})
}

// 按玩法分派到非默认生成段时，日志要能看出"这次没走 generate.model"：
// 使用日志那一行的 model 是最终段模型，单看它分不出"本来就该是它"还是"被分派过来的"。
func TestAggregateLabelsRecordRoutedGenerateModel(t *testing.T) {
	withAggregateConfig(t, taskModelsConfig)
	agg := common.GetAggregateModel("h3-both")
	require.NotNil(t, agg)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos",
		strings.NewReader(`{"model":"h3-both","prompt":"a cat","metadata":{"task_type":"r2va","src_ref_images":["a.png"]}}`))
	selected, err := applyAggregateExpansion(c, "h3-both", agg.Generate.Model, agg)
	require.NoError(t, err)
	require.Equal(t, "minimax-h3-ref2va", selected)

	v, ok := common.GetContextKey(c, constant.ContextKeyAggregateLabels)
	require.True(t, ok)
	labels := v.(map[string]string)
	require.Equal(t, "minimax-h3-ref2va", labels["generate_model"])

	// 走默认段时不写这个键（那条日志的 model 已经说明了）。
	rec2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(rec2)
	c2.Request = httptest.NewRequest(http.MethodPost, "/v1/videos",
		strings.NewReader(`{"model":"h3-both","prompt":"a cat","metadata":{"task_type":"t2v"}}`))
	_, err = applyAggregateExpansion(c2, "h3-both", agg.Generate.Model, agg)
	require.NoError(t, err)
	v2, _ := common.GetContextKey(c2, constant.ContextKeyAggregateLabels)
	require.NotContains(t, v2.(map[string]string), "generate_model")
}
