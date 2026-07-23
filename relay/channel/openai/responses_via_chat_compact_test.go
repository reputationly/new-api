package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
)

func TestOaiChatToResponsesHandler_UsageNormalization(t *testing.T) {
	// DeepSeek reports cache hits under prompt_cache_hit_tokens; the conversion
	// handler must normalize it into prompt_tokens_details.cached_tokens like the
	// native Chat path, for consistent billing.
	chatJSON := `{
		"id": "cmpl-1", "object": "chat.completion", "created": 100, "model": "deepseek-chat",
		"choices": [{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],
		"usage": {"prompt_tokens": 100, "completion_tokens": 5, "total_tokens": 105, "prompt_cache_hit_tokens": 30}
	}`

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(chatJSON))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		UpstreamModelName: "deepseek-chat",
		ChannelType:       constant.ChannelTypeDeepSeek,
	}}

	usage, apiErr := OaiChatToResponsesHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("handler error: %v", apiErr)
	}
	if usage.PromptTokensDetails.CachedTokens != 30 {
		t.Errorf("cached_tokens = %d, want 30 (normalized from prompt_cache_hit_tokens)", usage.PromptTokensDetails.CachedTokens)
	}
}

func TestOaiChatToResponsesHandler_PartialUsage(t *testing.T) {
	// Upstream returns prompt/completion counts but omits total_tokens: the real
	// counts must be kept (total filled from parts), not replaced by an estimate.
	chatJSON := `{
		"id": "cmpl-1", "object": "chat.completion", "created": 100, "model": "gpt-4o",
		"choices": [{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],
		"usage": {"prompt_tokens": 100, "completion_tokens": 50}
	}`

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(chatJSON))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}

	usage, apiErr := OaiChatToResponsesHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("handler error: %v", apiErr)
	}
	if usage.PromptTokens != 100 || usage.CompletionTokens != 50 || usage.TotalTokens != 150 {
		t.Errorf("usage = %d/%d/%d, want 100/50/150 (real counts, not estimated)",
			usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens)
	}
}

func TestOaiChatToResponsesCompactionHandler_UsageNormalization(t *testing.T) {
	chatJSON := `{
		"id": "cmpl-1", "object": "chat.completion", "created": 100, "model": "deepseek-chat",
		"choices": [{"index":0,"message":{"role":"assistant","content":"summary"},"finish_reason":"stop"}],
		"usage": {"prompt_tokens": 200, "completion_tokens": 20, "total_tokens": 220, "prompt_cache_hit_tokens": 50}
	}`

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", nil)
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(chatJSON))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		UpstreamModelName: "deepseek-chat",
		ChannelType:       constant.ChannelTypeDeepSeek,
	}}

	usage, apiErr := OaiChatToResponsesCompactionHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("handler error: %v", apiErr)
	}
	if usage.PromptTokensDetails.CachedTokens != 50 {
		t.Errorf("compact cached_tokens = %d, want 50 (normalized)", usage.PromptTokensDetails.CachedTokens)
	}
}

func TestOaiChatToResponsesCompactionHandler(t *testing.T) {
	// Upstream returns a normal (non-stream) chat completion — the model's
	// summary of the conversation.
	chatJSON := `{
		"id": "cmpl-1",
		"object": "chat.completion",
		"created": 100,
		"model": "gpt-4o",
		"choices": [{"index":0,"message":{"role":"assistant","content":"Summary of the conversation so far."},"finish_reason":"stop"}],
		"usage": {"prompt_tokens": 200, "completion_tokens": 20, "total_tokens": 220}
	}`

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(chatJSON)),
	}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"}}

	usage, apiErr := OaiChatToResponsesCompactionHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("handler error: %v", apiErr)
	}
	if usage == nil || usage.PromptTokens != 200 || usage.CompletionTokens != 20 {
		t.Fatalf("usage = %+v", usage)
	}

	var compaction dto.OpenAIResponsesCompactionResponse
	if err := common.UnmarshalJsonStr(recorder.Body.String(), &compaction); err != nil {
		t.Fatalf("unmarshal compaction response: %v\nbody=%s", err, recorder.Body.String())
	}

	if compaction.Object != "response" {
		t.Errorf("object = %q", compaction.Object)
	}
	if compaction.Usage == nil || compaction.Usage.InputTokens != 200 || compaction.Usage.OutputTokens != 20 {
		t.Errorf("compaction usage = %+v", compaction.Usage)
	}

	// output must be a Responses items array carrying the summary as a message.
	var output []dto.ResponsesOutput
	if err := common.Unmarshal(compaction.Output, &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if len(output) != 1 {
		t.Fatalf("output len = %d, want 1: %s", len(output), string(compaction.Output))
	}
	item := output[0]
	if item.Type != "message" || item.Role != "assistant" {
		t.Errorf("output item = %+v", item)
	}
	if len(item.Content) != 1 || item.Content[0].Type != "output_text" ||
		item.Content[0].Text != "Summary of the conversation so far." {
		t.Errorf("output content = %+v", item.Content)
	}
}

func TestCaptureChatUsageDetails(t *testing.T) {
	dst := &dto.Usage{}
	src := &dto.Usage{
		PromptTokens:     100,
		CompletionTokens: 50,
		TotalTokens:      150,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 10,
			ImageTokens:  20,
			AudioTokens:  5,
			TextTokens:   65,
		},
		CompletionTokenDetails: dto.OutputTokenDetails{
			ReasoningTokens: 8,
			AudioTokens:     3,
			ImageTokens:     4,
		},
	}
	captureChatUsage(dst, src)

	if dst.PromptTokensDetails != src.PromptTokensDetails {
		t.Errorf("input token details = %+v, want %+v", dst.PromptTokensDetails, src.PromptTokensDetails)
	}
	if dst.CompletionTokenDetails != src.CompletionTokenDetails {
		t.Errorf("output token details = %+v, want %+v", dst.CompletionTokenDetails, src.CompletionTokenDetails)
	}

	// a later usage chunk with empty details must not wipe accumulated details
	captureChatUsage(dst, &dto.Usage{PromptTokens: 100, CompletionTokens: 50})
	if dst.PromptTokensDetails.ImageTokens != 20 || dst.CompletionTokenDetails.AudioTokens != 3 {
		t.Errorf("details wiped by empty chunk: in=%+v out=%+v", dst.PromptTokensDetails, dst.CompletionTokenDetails)
	}
}
