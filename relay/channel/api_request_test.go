package channel

import (
	"net/http"
	"net/http/httptest"
	"testing"

	appconstant "github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestProcessHeaderOverride_ChannelTestSkipsPassthroughRules(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Request.Header.Set("X-Trace-Id", "trace-123")

	info := &relaycommon.RelayInfo{
		IsChannelTest: true,
		ChannelMeta: &relaycommon.ChannelMeta{
			HeadersOverride: map[string]any{
				"*": "",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	require.Empty(t, headers)
}

func TestProcessHeaderOverride_ChannelTestSkipsClientHeaderPlaceholder(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Request.Header.Set("X-Trace-Id", "trace-123")

	info := &relaycommon.RelayInfo{
		IsChannelTest: true,
		ChannelMeta: &relaycommon.ChannelMeta{
			HeadersOverride: map[string]any{
				"X-Upstream-Trace": "{client_header:X-Trace-Id}",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	_, ok := headers["x-upstream-trace"]
	require.False(t, ok)
}

func TestProcessHeaderOverride_NonTestKeepsClientHeaderPlaceholder(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Request.Header.Set("X-Trace-Id", "trace-123")

	info := &relaycommon.RelayInfo{
		IsChannelTest: false,
		ChannelMeta: &relaycommon.ChannelMeta{
			HeadersOverride: map[string]any{
				"X-Upstream-Trace": "{client_header:X-Trace-Id}",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	require.Equal(t, "trace-123", headers["x-upstream-trace"])
}

func TestProcessHeaderOverride_RuntimeOverrideIsFinalHeaderMap(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	info := &relaycommon.RelayInfo{
		IsChannelTest:             false,
		UseRuntimeHeadersOverride: true,
		RuntimeHeadersOverride: map[string]any{
			"x-static":  "runtime-value",
			"x-runtime": "runtime-only",
		},
		ChannelMeta: &relaycommon.ChannelMeta{
			HeadersOverride: map[string]any{
				"X-Static": "legacy-value",
				"X-Legacy": "legacy-only",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	require.Equal(t, "runtime-value", headers["x-static"])
	require.Equal(t, "runtime-only", headers["x-runtime"])
	_, exists := headers["x-legacy"]
	require.False(t, exists)
}

func TestProcessHeaderOverride_PassthroughSkipsAcceptEncoding(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Request.Header.Set("X-Trace-Id", "trace-123")
	ctx.Request.Header.Set("Accept-Encoding", "gzip")

	info := &relaycommon.RelayInfo{
		IsChannelTest: false,
		ChannelMeta: &relaycommon.ChannelMeta{
			HeadersOverride: map[string]any{
				"*": "",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	require.Equal(t, "trace-123", headers["x-trace-id"])

	_, hasAcceptEncoding := headers["accept-encoding"]
	require.False(t, hasAcceptEncoding)
}

func TestProcessHeaderOverride_PassHeadersTemplateSetsRuntimeHeaders(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	ctx.Request.Header.Set("Originator", "Codex CLI")
	ctx.Request.Header.Set("Session_id", "sess-123")

	info := &relaycommon.RelayInfo{
		IsChannelTest: false,
		RequestHeaders: map[string]string{
			"Originator": "Codex CLI",
			"Session_id": "sess-123",
		},
		ChannelMeta: &relaycommon.ChannelMeta{
			ParamOverride: map[string]any{
				"operations": []any{
					map[string]any{
						"mode":  "pass_headers",
						"value": []any{"Originator", "Session_id", "X-Codex-Beta-Features"},
					},
				},
			},
			HeadersOverride: map[string]any{
				"X-Static": "legacy-value",
			},
		},
	}

	_, err := relaycommon.ApplyParamOverrideWithRelayInfo([]byte(`{"model":"gpt-4.1"}`), info)
	require.NoError(t, err)
	require.True(t, info.UseRuntimeHeadersOverride)
	require.Equal(t, "Codex CLI", info.RuntimeHeadersOverride["originator"])
	require.Equal(t, "sess-123", info.RuntimeHeadersOverride["session_id"])
	_, exists := info.RuntimeHeadersOverride["x-codex-beta-features"]
	require.False(t, exists)
	require.Equal(t, "legacy-value", info.RuntimeHeadersOverride["x-static"])

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	require.Equal(t, "Codex CLI", headers["originator"])
	require.Equal(t, "sess-123", headers["session_id"])
	_, exists = headers["x-codex-beta-features"]
	require.False(t, exists)

	upstreamReq := httptest.NewRequest(http.MethodPost, "https://example.com/v1/responses", nil)
	applyHeaderOverrideToRequest(upstreamReq, headers)
	require.Equal(t, "Codex CLI", upstreamReq.Header.Get("Originator"))
	require.Equal(t, "sess-123", upstreamReq.Header.Get("Session_id"))
	require.Empty(t, upstreamReq.Header.Get("X-Codex-Beta-Features"))
}

// 渠道 Base URL 带路径前缀时，直连地址必须去掉那个前缀：实例的推理端口上只有
// 裸 /v1/...。不去掉的话每个亲和请求都会 404，而 404 又在外层自动重试范围里
// （AutomaticRetryStatusCodeRanges 401-407），等于白烧一次换渠道重试。
func TestGPUStackAffinityRewriteURLStripsBasePath(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		fullURL string
		baseURL string
		target  string
		want    string
	}{
		{
			name:    "裸 host:port（当前部署）",
			fullURL: "http://10.0.0.238:80/v1/chat/completions",
			baseURL: "http://10.0.0.238:80",
			target:  "http://10.0.0.7:40006",
			want:    "http://10.0.0.7:40006/v1/chat/completions",
		},
		{
			name:    "Base URL 带路径前缀",
			fullURL: "http://gw/gpustack/v1/chat/completions",
			baseURL: "http://gw/gpustack",
			target:  "http://10.0.0.7:40006",
			want:    "http://10.0.0.7:40006/v1/chat/completions",
		},
		{
			name:    "前缀带尾斜杠",
			fullURL: "http://gw/gpustack/v1/chat/completions",
			baseURL: "http://gw/gpustack/",
			target:  "http://10.0.0.7:40006",
			want:    "http://10.0.0.7:40006/v1/chat/completions",
		},
		{
			name:    "查询串保留",
			fullURL: "http://gw/v1/chat/completions?a=1",
			baseURL: "http://gw",
			target:  "http://10.0.0.7:40006",
			want:    "http://10.0.0.7:40006/v1/chat/completions?a=1",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Set(string(appconstant.ContextKeyGPUStackInstanceBaseURL), tc.target)
			require.Equal(t, tc.want, gpustackAffinityRewriteURL(tc.fullURL, tc.baseURL, ctx))
		})
	}
}

// 不做亲和时必须返回空串，调用方据此走原本的网关地址。
func TestGPUStackAffinityRewriteURLDeclinesWithoutTarget(t *testing.T) {
	t.Parallel()
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.Empty(t, gpustackAffinityRewriteURL(
		"http://gw/v1/chat/completions", "http://gw", ctx))
	require.Empty(t, gpustackAffinityRewriteURL(
		"http://gw/v1/chat/completions", "http://gw", nil))
}

// 404 必须触发回退：它是路径或实例不对的信号，且落在外层自动重试范围内。
// 4xx 的其余部分与 500 不回退——那是请求本身或引擎内部的问题。
func TestGPUStackAffinityShouldFallbackClassification(t *testing.T) {
	t.Parallel()
	for _, code := range []int{404, 502, 503, 504} {
		require.True(t, gpustackAffinityShouldFallback(&http.Response{StatusCode: code}, nil),
			"HTTP %d 应回退", code)
	}
	for _, code := range []int{200, 400, 401, 429, 500} {
		require.False(t, gpustackAffinityShouldFallback(&http.Response{StatusCode: code}, nil),
			"HTTP %d 不该回退", code)
	}
	require.True(t, gpustackAffinityShouldFallback(nil, http.ErrHandlerTimeout), "连接层失败应回退")
	require.True(t, gpustackAffinityShouldFallback(nil, nil), "无响应应回退")
}
