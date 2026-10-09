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

// 展开阶段写入的使用日志标签:有超分段真要跑时,生成段那一条标成「生成段」。
func TestAggregateLabelsMarkGenerateStageOnlyWhenUpscaleRuns(t *testing.T) {
	expandLabels := func(t *testing.T, cfg, body string) map[string]string {
		t.Helper()
		withAggregateConfig(t, cfg)
		agg := common.GetAggregateModel("h3-2k")
		require.NotNil(t, agg)
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(body))
		require.NoError(t, applyAggregateExpansion(c, "h3-2k", "minimax-h3", agg))
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

	t.Run("客户要的档不高于生成段,超分跳过:不标段", func(t *testing.T) {
		labels := expandLabels(t, `[{"name":"h3-2k","type":"video","enabled":true,
			"generate":{"model":"minimax-h3","overrides":{"size":"768P"}},
			"upscale":{"model":"seedvr2","target_size":"2k"}}]`,
			`{"model":"h3-2k","prompt":"a cat","size":"768P"}`)
		require.Equal(t, "h3-2k", labels["aggregate_model"])
		_, hasStage := labels["pipeline_stage"]
		require.False(t, hasStage, "超分段不会跑,就不该标成生成段")
	})

	t.Run("没有超分段的单段聚合:只带聚合名", func(t *testing.T) {
		labels := expandLabels(t, `[{"name":"h3-2k","type":"video","enabled":true,
			"generate":{"model":"minimax-h3","overrides":{"size":"768P"}}}]`,
			`{"model":"h3-2k","prompt":"a cat","size":"2k"}`)
		require.Equal(t, "h3-2k", labels["aggregate_model"])
		_, hasStage := labels["pipeline_stage"]
		require.False(t, hasStage)
	})
}
