package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/service"
)

// 超分段跳过的**落点**在这里,不在 middleware。
//
// middleware 判出 UpscaleSkipped 只是知道「这次不该超分」;真正让第二段不发生的，
// 是不填 UpscaleModel —— 轮询侧 TryAdvanceAggregatePipeline 的准入是
// `agg.HasUpscale() && agg.Stage != 1`。所以这两个用例锁的是那个准入**读到的值**，
// 而不是判定的输入。变异对照:把 buildTaskAggregateInfo 里的 `&& !exp.UpscaleSkipped`
// 删掉，本文件必红；只测 middleware 那侧的判定则照绿。
func TestBuildTaskAggregateInfoRespectsUpscaleSkip(t *testing.T) {
	cfg := &common.AggregateModel{
		Name:     "h3-2k",
		Type:     "video",
		Generate: common.AggregateGenerate{Model: "minimax-h3"},
		Upscale:  &common.AggregateUpscale{Model: "seedvr2", Target: "2k"},
	}
	newCtx := func() *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		req := httptest.NewRequest(http.MethodPost, "/v1/video/generations", nil)
		req.Header.Set("Authorization", "Bearer sk-caller-key")
		c.Request = req
		return c
	}

	t.Run("客户要 2K:第二段照跑,并留下客户令牌", func(t *testing.T) {
		info := buildTaskAggregateInfo(newCtx(), &middleware.AggregateExpansion{
			PublicName:    "h3-2k",
			Config:        cfg,
			RequestedSize: "2k",
			GenerateSize:  "768P",
		})
		require.True(t, info.HasUpscale(), "客户要的最终档高于生成段,超分段必须跑")
		require.Equal(t, 1, info.Stage)
		require.Equal(t, "seedvr2", info.UpscaleModel)
		require.Equal(t, "2k", info.UpscaleTarget)
		require.Equal(t, "sk-caller-key", info.CallerKey)
		require.Equal(t, "h3-2k", info.PublicModel, "对外回显的仍是聚合模型名")
	})

	t.Run("客户要 768P:第二段不跑,且不留客户令牌", func(t *testing.T) {
		info := buildTaskAggregateInfo(newCtx(), &middleware.AggregateExpansion{
			PublicName:        "h3-2k",
			Config:            cfg,
			RequestedSize:     "768P",
			GenerateSize:      "768P",
			UpscaleSkipped:    true,
			UpscaleSkipReason: "客户要 768P(短边 768)不高于生成段的 768P(短边 768),超分段跳过",
		})
		// HasUpscale 是轮询侧唯一的准入,跳过就必须让它为假。
		require.False(t, info.HasUpscale(),
			"跳过了却还留着 UpscaleModel,轮询就会提交第二段 —— 客户付超分的钱拿回 1080p")
		require.Equal(t, 0, info.Stage)
		require.Empty(t, info.UpscaleTarget)
		require.Empty(t, info.CallerKey, "没有第二段就没人用令牌,存了只是多一处明文凭证")
		require.Equal(t, "h3-2k", info.PublicModel,
			"跳过超分段不改变对外那个模型名 —— 客户调的仍是聚合模型")
	})

	// 跳过超分段与提示词增强是两件独立的事:增强跑在生成段之前,钱已经烧掉了,
	// 记录必须随任务留下 —— 客户报「生成的跟写的不一样」时只有它能解释。
	t.Run("跳过超分段不影响增强记录", func(t *testing.T) {
		info := buildTaskAggregateInfo(newCtx(), &middleware.AggregateExpansion{
			PublicName:     "h3-2k",
			Config:         cfg,
			UpscaleSkipped: true,
			Enhance: &service.EnhanceResult{
				Model:          "qwen3.8-flash-fp8",
				OriginalPrompt: "一只猫",
				EnhancedPrompt: "A cat sleeping on the windowsill.",
			},
		})
		require.False(t, info.HasUpscale())
		require.Equal(t, "qwen3.8-flash-fp8", info.EnhanceModel)
		require.Equal(t, "一只猫", info.OriginalPrompt)
	})
}
