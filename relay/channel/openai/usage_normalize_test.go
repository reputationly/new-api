package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 两份 details 都在且内容相同时，绝不能累加成两倍。
func TestMergeInputTokenDetailsDoesNotAccumulate(t *testing.T) {
	dst := dto.InputTokenDetails{
		CachedTokens:     100,
		CacheWriteTokens: 40,
		ImageTokens:      7,
	}
	src := dto.InputTokenDetails{
		CachedTokens:     100,
		CacheWriteTokens: 40,
		ImageTokens:      7,
		TextTokens:       90,
		EcaCacheCreationDetails: &dto.EcaCacheCreationDetails{
			Ephemeral5mInputTokens: 40,
		},
	}

	mergeInputTokenDetails(&dst, &src)

	require.Equal(t, 100, dst.CachedTokens)
	require.Equal(t, 40, dst.CacheWriteTokens)
	require.Equal(t, 7, dst.ImageTokens)
	// 主来源为零的字段才回退
	require.Equal(t, 90, dst.TextTokens)
	require.NotNil(t, dst.EcaCacheCreationDetails)
}

// 主来源全空时整体回退到 input_tokens_details（Responses 协议的形态）。
func TestMergeInputTokenDetailsFallsBackWhenPrimaryEmpty(t *testing.T) {
	var dst dto.InputTokenDetails
	src := dto.InputTokenDetails{
		CachedTokens:     60,
		CacheWriteTokens: 40,
		CreationTokens:   40,
		TextTokens:       100,
	}

	mergeInputTokenDetails(&dst, &src)

	require.Equal(t, 60, dst.CachedTokens)
	require.Equal(t, 40, dst.CacheWriteTokens)
	require.Equal(t, 40, dst.CreationTokens)
	require.Equal(t, 100, dst.TextTokens)
	require.Equal(t, 40, dst.CacheWriteTokensTotal())
}

// 直达 /v1/responses 的非流式入口：缓存写入必须从 input_tokens_details 搬进计费字段。
// 回归点：曾只搬 cached_tokens，写入 token 按普通输入价结算。
func TestOaiResponsesHandlerCopiesCacheWriteFromInputDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)

	body := `{"id":"resp_1","object":"response","status":"completed","output":[],
		"usage":{"input_tokens":100,"output_tokens":10,"total_tokens":110,
		"input_tokens_details":{"cached_tokens":60,"cache_write_tokens":40}}}`
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
