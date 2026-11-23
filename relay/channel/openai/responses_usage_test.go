package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 入口级回归：直达 /v1/responses 的非流式响应里，缓存写入必须从
// input_tokens_details 搬进计费读取的 PromptTokensDetails。
// 曾经只搬 cached_tokens，写入 token 按普通输入价结算。
func TestOaiResponsesHandlerCopiesCacheWriteFromInputDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)

	body := `{"id":"resp_1","object":"response","status":"completed","output":[],
		"usage":{"input_tokens":100,"output_tokens":10,"total_tokens":110,
		"input_tokens_details":{"cached_tokens":60,"cache_write_tokens":40,"creation_tokens":40}}}`
	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{},
	}

	usage, apiErr := OaiResponsesHandler(c, &relaycommon.RelayInfo{}, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	require.Equal(t, 60, usage.PromptTokensDetails.CachedTokens)
	require.Equal(t, 40, usage.PromptTokensDetails.CacheWriteTokens)
	require.Equal(t, 40, usage.PromptTokensDetails.CacheWriteTokensTotal())
}

// 入口级回归：/responses/compact 同样不能丢缓存写入。
func TestOaiResponsesCompactionHandlerCopiesCacheWriteFromInputDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/responses/compact", nil)

	body := `{"id":"resp_c","object":"response.compaction","status":"completed","output":[],
		"usage":{"input_tokens":80,"output_tokens":0,"total_tokens":80,
		"input_tokens_details":{"cached_tokens":50,"cache_write_tokens":30}}}`
	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{},
	}

	usage, apiErr := OaiResponsesCompactionHandler(c, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	require.Equal(t, 50, usage.PromptTokensDetails.CachedTokens)
	require.Equal(t, 30, usage.PromptTokensDetails.CacheWriteTokens)
}

// 入口级回归：Responses→chat 的 SSE 入口同样要带出缓存写入。
func TestOaiResponsesToChatStreamHandlerKeepsCacheWrite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)

	body := "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"object\":\"response\"," +
		"\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":100,\"output_tokens\":10," +
		"\"total_tokens\":110,\"input_tokens_details\":{\"cached_tokens\":20,\"cache_write_tokens\":40}}}}\n\n" +
		"data: [DONE]\n\n"
	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{},
	}

	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	info.UpstreamModelName = "gpt-5.6-terra"

	usage, apiErr := OaiResponsesToChatStreamHandler(c, info, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	require.Equal(t, 20, usage.PromptTokensDetails.CachedTokens)
	require.Equal(t, 40, usage.PromptTokensDetails.CacheWriteTokens)
}
