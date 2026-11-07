package common

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newRewriteContext(t *testing.T, publicName, upstreamName string) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	if publicName != "" {
		common.SetContextKey(c, constant.ContextKeyPublicModelName, publicName)
		common.SetContextKey(c, constant.ContextKeyUpstreamModelName, upstreamName)
	}
	return c
}

func TestRewriteResponseModelJSON(t *testing.T) {
	t.Run("把顶层 model 换回对外名", func(t *testing.T) {
		c := newRewriteContext(t, "my-gpt", "gpt-4o")
		got := RewriteResponseModelJSON(c, []byte(`{"id":"x","model":"gpt-4o","choices":[]}`))
		require.JSONEq(t, `{"id":"x","model":"my-gpt","choices":[]}`, string(got))
	})

	// 最要紧的一条：只动顶层的 model 字段，绝不做全局字符串替换 ——
	// 否则回答正文里出现模型名时会被篡改。
	t.Run("不动响应内容里的同名文本", func(t *testing.T) {
		c := newRewriteContext(t, "my-gpt", "gpt-4o")
		body := `{"model":"gpt-4o","choices":[{"message":{"content":"gpt-4o 是 OpenAI 的模型"}}]}`
		got := RewriteResponseModelJSON(c, []byte(body))
		require.JSONEq(t, `{"model":"my-gpt","choices":[{"message":{"content":"gpt-4o 是 OpenAI 的模型"}}]}`, string(got))
	})

	t.Run("没有改名时原样返回", func(t *testing.T) {
		c := newRewriteContext(t, "", "")
		in := []byte(`{"model":"gpt-4o"}`)
		require.Equal(t, in, RewriteResponseModelJSON(c, in))
	})

	t.Run("没有 model 字段时原样返回", func(t *testing.T) {
		c := newRewriteContext(t, "my-gpt", "gpt-4o")
		in := []byte(`{"data":[{"url":"https://example/image.png"}]}`)
		require.Equal(t, in, RewriteResponseModelJSON(c, in))
	})

	t.Run("非 JSON 体原样返回", func(t *testing.T) {
		c := newRewriteContext(t, "my-gpt", "gpt-4o")
		in := []byte{0x00, 0x01, 0x02, 0xff}
		require.Equal(t, in, RewriteResponseModelJSON(c, in))
	})
}

func TestRewriteResponseModelString(t *testing.T) {
	t.Run("改写 SSE chunk", func(t *testing.T) {
		c := newRewriteContext(t, "my-gpt", "gpt-4o")
		got := RewriteResponseModelString(c, `{"model":"gpt-4o","choices":[{"delta":{"content":"hi"}}]}`)
		require.JSONEq(t, `{"model":"my-gpt","choices":[{"delta":{"content":"hi"}}]}`, got)
	})

	// [DONE] 这类非 JSON 的尾部标记必须原样通过，否则整条流会被打断。
	t.Run("非 JSON 行原样通过", func(t *testing.T) {
		c := newRewriteContext(t, "my-gpt", "gpt-4o")
		require.Equal(t, "[DONE]", RewriteResponseModelString(c, "[DONE]"))
		require.Equal(t, "", RewriteResponseModelString(c, ""))
	})
}

func TestScrubLeakyResponseHeaders(t *testing.T) {
	c := newRewriteContext(t, "my-gpt", "gpt-4o")
	header := http.Header{
		"Content-Type":   {"application/json"},
		"X-Model":        {"gpt-4o"},
		"X-Request-Id":   {"req-1"},
		"X-Model-Family": {"models/gpt-4o"},
	}
	ScrubLeakyResponseHeaders(c, header)

	require.Equal(t, "application/json", header.Get("Content-Type"))
	require.Equal(t, "req-1", header.Get("X-Request-Id"))
	require.Empty(t, header.Values("X-Model"))
	require.Empty(t, header.Values("X-Model-Family"))
}

func TestRewriteUpstreamText(t *testing.T) {
	c := newRewriteContext(t, "my-gpt", "gpt-4o")

	require.Equal(t,
		"my-gpt is not a multimodal model",
		RewriteUpstreamText(c, "gpt-4o is not a multimodal model"))

	// 没有改名时不做任何替换。
	plain := newRewriteContext(t, "", "")
	require.Equal(t, "gpt-4o is not a multimodal model",
		RewriteUpstreamText(plain, "gpt-4o is not a multimodal model"))
}

// Python 的 json.dumps 默认会带空格（`"model": "gpt-4o"`），固定模式串匹配不到，
// 所以扫描器必须容忍键和值之间的空白。
func TestRewriteResponseModelJSON_WhitespaceVariants(t *testing.T) {
	c := newRewriteContext(t, "my-gpt", "gpt-4o")

	cases := []string{
		`{"model": "gpt-4o"}`,
		`{"model" : "gpt-4o"}`,
		"{\"model\"\t:\r\n\"gpt-4o\"}",
		`{"response":{"model":"gpt-4o"}}`,
	}
	for _, in := range cases {
		got := RewriteResponseModelJSON(c, []byte(in))
		require.Contains(t, string(got), `"my-gpt"`, "输入: %s", in)
		require.NotContains(t, string(got), `"gpt-4o"`, "输入: %s", in)
	}
}

// 正文里出现的模型名（引号被转义）绝不能被改掉。
func TestRewriteResponseModelJSON_ContentWithEscapedKeyUntouched(t *testing.T) {
	c := newRewriteContext(t, "my-gpt", "gpt-4o")
	body := `{"model":"gpt-4o","choices":[{"message":{"content":"{\"model\":\"gpt-4o\"}"}}]}`
	got := RewriteResponseModelJSON(c, []byte(body))
	require.JSONEq(t, `{"model":"my-gpt","choices":[{"message":{"content":"{\"model\":\"gpt-4o\"}"}}]}`, string(got))
}

// 值不是 `"model"` 键时不能被误判：`"object":"model"` 是很常见的字段。
func TestRewriteResponseModelJSON_ModelAsValueNotKey(t *testing.T) {
	c := newRewriteContext(t, "my-gpt", "gpt-4o")
	body := `{"object":"model","id":"gpt-4o","model":"gpt-4o"}`
	got := RewriteResponseModelJSON(c, []byte(body))
	require.JSONEq(t, `{"object":"model","id":"gpt-4o","model":"my-gpt"}`, string(got))
}

// 上游把模型名规范化了（我们发 gpt-4o，它回 gpt-4o-2024-08-06）：字节模式匹配不到，
// 必须走 JSON 回退把字段改成对外名，否则客户端照样能看见真实模型。
func TestRewriteResponseModelJSON_NormalizedUpstreamName(t *testing.T) {
	c := newRewriteContext(t, "my-gpt", "gpt-4o")
	got := RewriteResponseModelJSON(c, []byte(`{"model":"gpt-4o-2024-08-06","id":"x"}`))
	require.JSONEq(t, `{"model":"my-gpt","id":"x"}`, string(got))
}

// 快路径不重新序列化，所以除 model 值以外逐字节不变（键顺序、浮点格式都不动）。
func TestRewriteResponseModelJSON_PreservesOtherBytes(t *testing.T) {
	c := newRewriteContext(t, "my-gpt", "gpt-4o")
	in := `{"zz":1.500,"aa":"x","model":"gpt-4o","created":1700000000}`
	got := string(RewriteResponseModelJSON(c, []byte(in)))
	require.Equal(t, `{"zz":1.500,"aa":"x","model":"my-gpt","created":1700000000}`, got)
}

func BenchmarkRewriteResponseModelStreamChunk(b *testing.B) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, constant.ContextKeyPublicModelName, "my-gpt-4o")
	common.SetContextKey(c, constant.ContextKeyUpstreamModelName, "gpt-4o")
	chunk := []byte(`{"id":"chatcmpl-123","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o","system_fingerprint":"fp_abc","choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":null}]}`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = RewriteResponseModelJSON(c, chunk)
	}
}

func BenchmarkRewriteResponseModelNoMapping(b *testing.B) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	chunk := []byte(`{"id":"chatcmpl-123","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":"hello"}}]}`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = RewriteResponseModelJSON(c, chunk)
	}
}

// 回退路径（解析 + 重序列化）的代价，用来和快路径对比。
func BenchmarkRewriteResponseModelViaJSONFallback(b *testing.B) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, constant.ContextKeyPublicModelName, "my-gpt-4o")
	body := []byte(`{"id":"chatcmpl-123","object":"chat.completion","created":1700000000,"model":"gpt-4o-2024-08-06","choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":null}]}`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = rewriteModelFieldViaJSON(c, body, "my-gpt-4o")
	}
}

// 同一份体里可能既有顶层 model、又有嵌套的 response.model（转换/聚合类响应）。
// 只替换第一处等于留了个漏，客户端仍能看见上游名。
func TestRewriteResponseModelJSON_ReplacesEveryOccurrence(t *testing.T) {
	c := newRewriteContext(t, "my-gpt", "gpt-4o")
	in := `{"model":"gpt-4o","response":{"model":"gpt-4o","id":"r1"},"echo":{"model":"gpt-4o"}}`
	got := RewriteResponseModelJSON(c, []byte(in))
	require.JSONEq(t, `{"model":"my-gpt","response":{"model":"my-gpt","id":"r1"},"echo":{"model":"my-gpt"}}`, string(got))
	require.NotContains(t, string(got), `"gpt-4o"`)
}

// 写出口也承载纯文本（例如 STT 的 srt/vtt）甚至二进制。手写扫描器不校验语法，
// 所以非 JSON 对象体必须原样放过。
func TestRewriteResponseModelJSON_NonJSONBodyUntouched(t *testing.T) {
	c := newRewriteContext(t, "my-gpt", "gpt-4o")

	plain := "1\n00:00:00,000 --> 00:00:01,000\n\"model\":\"gpt-4o\"\n"
	require.Equal(t, plain, string(RewriteResponseModelJSON(c, []byte(plain))))

	binaryLike := []byte{0x00, 0x01, '"', 'm', 'o', 'd', 'e', 'l', '"', ':', '"', 'g', 'p', 't', '-', '4', 'o', '"'}
	require.Equal(t, binaryLike, RewriteResponseModelJSON(c, binaryLike))
}

// Realtime（WebSocket）的帧同样要回显对外名：session.created / response.done 等事件
// 把 model 放在嵌套对象里，只改顶层会漏。这条锁住 WS 转发路径上的改写前提。
func TestRewriteResponseModelJSON_RealtimeEventPayload(t *testing.T) {
	c := newRewriteContext(t, "my-voice", "gpt-4o-realtime-preview")
	event := `{"type":"session.created","event_id":"e1","session":{"id":"s1","model":"gpt-4o-realtime-preview","voice":"alloy"}}`
	got := RewriteResponseModelJSON(c, []byte(event))
	require.NotContains(t, string(got), `"gpt-4o-realtime-preview"`)
	require.Contains(t, string(got), `"my-voice"`)
}
