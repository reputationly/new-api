package openaicompat

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"

	"github.com/stretchr/testify/require"
)

// 入口级回归：真正的 Responses→chat 转换（非流式）不能丢缓存写入与 TTL 明细。
// 曾经只搬 cached_tokens，写入 token 会按普通输入价结算。
func TestResponsesResponseToChatCompletionsKeepsCacheWrite(t *testing.T) {
	raw := `{
		"id": "resp_1",
		"object": "response",
		"status": "completed",
		"output": [],
		"usage": {
			"input_tokens": 100,
			"output_tokens": 10,
			"total_tokens": 110,
			"input_tokens_details": {
				"cached_tokens": 20,
				"cache_write_tokens": 40,
				"creation_tokens": 40,
				"eca_cache_creation_details": {"ephemeral_5m_input_tokens": 30, "ephemeral_1h_input_tokens": 10}
			}
		}
	}`

	var resp dto.OpenAIResponsesResponse
	require.NoError(t, common.UnmarshalJsonStr(raw, &resp))

	_, usage, err := ResponsesResponseToChatCompletionsResponse(&resp, "chatcmpl-x")
	require.NoError(t, err)
	require.NotNil(t, usage)
	require.Equal(t, 20, usage.PromptTokensDetails.CachedTokens)
	require.Equal(t, 40, usage.PromptTokensDetails.CacheWriteTokensTotal())

	shortTTL, longTTL := usage.PromptTokensDetails.CacheWriteSplit()
	require.Equal(t, 30, shortTTL)
	require.Equal(t, 10, longTTL)
}
