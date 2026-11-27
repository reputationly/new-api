package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
)

// 尺寸不合法的图片请求必须在提示词改写(LM 调用,实测约 20 秒)之前被拒:否则每次
// 非法请求都要先烧掉一次改写调用才得到 400。这里用一个会记账的替身证明改写没有被
// 调用,而不是只断言错误信息。
func TestImageAggregateRejectsBadSizeBeforeEnhance(t *testing.T) {
	withAggregateConfig(t, `[{
		"name":"img-aggregate","type":"image","enabled":true,
		"generate":{"model":"minimax-h3-image"}
	}]`)
	agg := common.GetAggregateModel("img-aggregate")
	require.NotNil(t, agg)

	origEnhance := enhancePrompt
	t.Cleanup(func() { enhancePrompt = origEnhance })

	calls := 0
	enhancePrompt = func(_ context.Context, _ *common.AggregateModel, _ string, _ service.EnhanceInput) *service.EnhanceResult {
		calls++
		return &service.EnhanceResult{}
	}

	expand := func(body string) (map[string]any, error) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(body))
		err := applyExpansionForTest(c, "img-aggregate", "minimax-h3-image", agg)
		if err != nil {
			return nil, err
		}
		return readBodyMap(t, c), nil
	}

	t.Run("1K 请求:拒绝且不调用改写", func(t *testing.T) {
		calls = 0
		_, err := expand(`{"model":"img-aggregate","prompt":"a cat","size":"1024x1024"}`)
		require.Error(t, err)
		require.Contains(t, err.Error(), "below the smallest billed tier")
		require.Zero(t, calls, "尺寸非法时不应发生提示词改写")
	})

	t.Run("厂商档位表尺寸:放行,且改写看到的是归一后的尺寸", func(t *testing.T) {
		calls = 0
		body, err := expand(`{"model":"img-aggregate","prompt":"a cat","size":"1776x2368"}`)
		require.NoError(t, err)
		require.Equal(t, "1760x2368", body["size"], "16 网格的 3:4 应归一成档内的 1760x2368")
	})
}
