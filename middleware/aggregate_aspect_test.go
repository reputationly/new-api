package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/common"
)

// 视频聚合里宽高比的三种来路与优先级:metadata > 顶层 > 从像素串推导。
func TestVideoAspectRatioAlignment(t *testing.T) {
	withAggregateConfig(t, `[{
		"name":"h3-2k","type":"video","enabled":true,
		"generate":{"model":"minimax-h3","overrides":{"size":"768P"}},
		"upscale":{"model":"seedvr2","target_size":"2k"}
	}]`)
	agg := common.GetAggregateModel("h3-2k")
	require.NotNil(t, agg)

	expand := func(t *testing.T, body string) map[string]any {
		t.Helper()
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(body))
		require.NoError(t, applyAggregateExpansion(c, "h3-2k", "minimax-h3", agg))
		return readBodyMap(t, c)
	}
	aspectOf := func(b map[string]any) any {
		md, _ := b["metadata"].(map[string]any)
		if md == nil {
			return nil
		}
		return md["aspect_ratio"]
	}

	t.Run("竖版像素串:从像素串推导 9:16,生成段 size 仍是 768P", func(t *testing.T) {
		b := expand(t, `{"model":"h3-2k","prompt":"a cat","size":"1080x1920"}`)
		require.Equal(t, "9:16", aspectOf(b))
		require.Equal(t, "768P", b["size"], "档位词覆盖照常生效")
	})

	t.Run("像素串推导到最近的具名比例,不下发 26:15 这种非具名值", func(t *testing.T) {
		b := expand(t, `{"model":"h3-2k","prompt":"a cat","size":"832x480"}`)
		require.Equal(t, "16:9", aspectOf(b))
	})

	t.Run("顶层 aspect_ratio 搬进 metadata,顶层键删除", func(t *testing.T) {
		b := expand(t, `{"model":"h3-2k","prompt":"a cat","size":"1080P","aspect_ratio":"21:9"}`)
		require.Equal(t, "21:9", aspectOf(b))
		_, stillTop := b["aspect_ratio"]
		require.False(t, stillTop, "顶层键适配器本来就不认,留着只会误导")
	})

	t.Run("顶层优先于像素串", func(t *testing.T) {
		b := expand(t, `{"model":"h3-2k","prompt":"a cat","size":"1080x1920","aspect_ratio":"21:9"}`)
		require.Equal(t, "21:9", aspectOf(b))
	})

	t.Run("metadata 显式给的优先于顶层与像素串", func(t *testing.T) {
		b := expand(t, `{"model":"h3-2k","prompt":"a cat","size":"1080x1920","aspect_ratio":"21:9","metadata":{"aspect_ratio":"1:1"}}`)
		require.Equal(t, "1:1", aspectOf(b))
	})

	t.Run("metadata 是 JSON 字符串也能补齐,其余字段不丢", func(t *testing.T) {
		b := expand(t, `{"model":"h3-2k","prompt":"a cat","size":"1080x1920","metadata":"{\"task_type\":\"t2v\"}"}`)
		require.Equal(t, "9:16", aspectOf(b))
		md := b["metadata"].(map[string]any)
		require.Equal(t, "t2v", md["task_type"])
	})

	t.Run("没有任何宽高比信息:不补", func(t *testing.T) {
		b := expand(t, `{"model":"h3-2k","prompt":"a cat","size":"1080P"}`)
		require.Nil(t, aspectOf(b))
	})
}

// 没有 size 覆盖时,像素串原样到达适配器,由它自己推导,这里不替它做。
func TestPixelSizeNotDerivedWithoutSizeOverride(t *testing.T) {
	withAggregateConfig(t, `[{
		"name":"h3-plain","type":"video","enabled":true,
		"generate":{"model":"minimax-h3"},
		"upscale":{"model":"seedvr2","target_size":"2k"}
	}]`)
	agg := common.GetAggregateModel("h3-plain")
	require.NotNil(t, agg)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos",
		strings.NewReader(`{"model":"h3-plain","prompt":"a cat","size":"1080x1920"}`))
	require.NoError(t, applyAggregateExpansion(c, "h3-plain", "minimax-h3", agg))

	b := readBodyMap(t, c)
	require.Equal(t, "1080x1920", b["size"], "没有覆盖时像素串照旧透传")
	_, hasMD := b["metadata"]
	require.False(t, hasMD, "没有需要补的东西就不写 metadata")
}

// 图片聚合不走视频的宽高比补齐:顶层键保持原样。
func TestImageAggregateUntouchedByAspectAlignment(t *testing.T) {
	withAggregateConfig(t, `[{
		"name":"img-plain","type":"image","enabled":true,
		"generate":{"model":"qwen-image-pro","overrides":{"size":"1024x1024"}}
	}]`)
	agg := common.GetAggregateModel("img-plain")
	require.NotNil(t, agg)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations",
		strings.NewReader(`{"model":"img-plain","prompt":"a cat","size":"768x1344","aspect_ratio":"9:16"}`))
	require.NoError(t, applyAggregateExpansion(c, "img-plain", "qwen-image-pro", agg))

	b := readBodyMap(t, c)
	require.Equal(t, "9:16", b["aspect_ratio"], "图片聚合的顶层键不属于视频补齐的范围")
	_, hasMD := b["metadata"]
	require.False(t, hasMD)
}
