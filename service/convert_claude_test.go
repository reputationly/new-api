package service

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/require"
)

func claudeTool(name string) dto.Tool {
	return dto.Tool{
		Name: name,
		InputSchema: map[string]interface{}{
			"type": "object",
		},
	}
}

func claudeConversionRelayInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType: constant.ChannelTypeOpenAI,
		},
	}
}

func TestClaudeToOpenAIRequestToolChoice(t *testing.T) {
	tests := []struct {
		name              string
		choice            any
		wantChoice        any
		wantParallelTools *bool
	}{
		{
			name:       "auto",
			choice:     map[string]any{"type": "auto"},
			wantChoice: "auto",
		},
		{
			name:       "any becomes required",
			choice:     map[string]any{"type": "any"},
			wantChoice: "required",
		},
		{
			name:       "none",
			choice:     map[string]any{"type": "none"},
			wantChoice: "none",
		},
		{
			name: "specific tool",
			choice: map[string]any{
				"type": "tool",
				"name": "Bash",
			},
			wantChoice: map[string]any{
				"type": "function",
				"function": map[string]any{
					"name": "Bash",
				},
			},
		},
		{
			name: "disable parallel tools",
			choice: map[string]any{
				"type":                      "auto",
				"disable_parallel_tool_use": true,
			},
			wantChoice:        "auto",
			wantParallelTools: func() *bool { value := false; return &value }(),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request, err := ClaudeToOpenAIRequest(dto.ClaudeRequest{
				Model:      "glm-5.2",
				Stream:     func() *bool { value := true; return &value }(),
				Tools:      []dto.Tool{claudeTool("Bash")},
				ToolChoice: test.choice,
			}, claudeConversionRelayInfo())

			require.NoError(t, err)
			require.Equal(t, test.wantChoice, request.ToolChoice)
			require.Equal(t, test.wantParallelTools, request.ParallelTooCalls)
		})
	}
}

func TestClaudeToOpenAIRequestDoesNotInferProviderToolStreamCapability(t *testing.T) {
	stream := true
	request, err := ClaudeToOpenAIRequest(dto.ClaudeRequest{
		Model:  "GLM-5.2",
		Stream: &stream,
		Tools:  []dto.Tool{claudeTool("Bash")},
	}, claudeConversionRelayInfo())

	require.NoError(t, err)
	require.Nil(t, request.ToolStream)
}

// 没有 input_schema 的工具(Claude 内置工具只带 type/name,或客户端漏传)不能转成
// "parameters": null——火山方舟对此回 400「tools[0].***.parameters must be valid JSON」。
func TestClaudeToOpenAIRequestToolWithoutInputSchemaKeepsParametersValidJSON(t *testing.T) {
	request, err := ClaudeToOpenAIRequest(dto.ClaudeRequest{
		Model: "doubao-seed-1-6",
		Tools: []any{
			map[string]any{"name": "no_schema"},
			map[string]any{"name": "null_schema", "input_schema": nil},
			map[string]any{"name": "empty_schema", "input_schema": map[string]any{}},
			map[string]any{"name": "get_weather", "input_schema": map[string]any{"type": "object"}},
		},
	}, claudeConversionRelayInfo())
	require.NoError(t, err)

	body, err := common.Marshal(request)
	require.NoError(t, err)
	require.NotContains(t, string(body), `"parameters":null`)

	emptyObjectSchema := map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}
	require.Len(t, request.Tools, 4)
	require.Equal(t, emptyObjectSchema, request.Tools[0].Function.Parameters)
	require.Equal(t, emptyObjectSchema, request.Tools[1].Function.Parameters)
	require.Equal(t, emptyObjectSchema, request.Tools[2].Function.Parameters)
	// 有 schema 的工具原样透传
	require.Equal(t, map[string]interface{}{"type": "object"}, request.Tools[3].Function.Parameters)
}

// Claude 内置工具(web_search / bash 等)只能由 Anthropic 执行或依赖 Claude 内置 schema,
// 转给非 Claude 上游只会变成空壳函数、静默失效,必须明确 400 拒绝且不重试。
func TestClaudeToOpenAIRequestRejectsBuiltinTools(t *testing.T) {
	for _, tool := range []map[string]any{
		{"type": "web_search_20250305", "name": "web_search", "max_uses": 8},
		{"type": "bash_20250124", "name": "bash"},
	} {
		t.Run(tool["type"].(string), func(t *testing.T) {
			_, err := ClaudeToOpenAIRequest(dto.ClaudeRequest{
				Model: "doubao-seed-1-6",
				Tools: []any{map[string]any{"name": "Read", "input_schema": map[string]any{"type": "object"}}, tool},
			}, claudeConversionRelayInfo())
			require.Error(t, err)
			require.Contains(t, err.Error(), tool["type"].(string))

			// claude_handler 会再包一层 NewError,400 与不重试必须穿透保留
			apiErr := types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
			require.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
			require.True(t, types.IsSkipRetryError(apiErr))
		})
	}
}

func TestClaudeToOpenAIRequestAcceptsExplicitCustomToolType(t *testing.T) {
	request, err := ClaudeToOpenAIRequest(dto.ClaudeRequest{
		Model: "doubao-seed-1-6",
		Tools: []any{map[string]any{"type": "custom", "name": "Read", "input_schema": map[string]any{"type": "object"}}},
	}, claudeConversionRelayInfo())
	require.NoError(t, err)
	require.Len(t, request.Tools, 1)
	require.Equal(t, "Read", request.Tools[0].Function.Name)
}

func TestStreamResponseOpenAI2ClaudeUsageWithoutFinishReasonStaysIncomplete(t *testing.T) {
	info := &relaycommon.RelayInfo{
		ClaudeConvertInfo: &relaycommon.ClaudeConvertInfo{
			LastMessagesType: relaycommon.LastMessageTypeThinking,
			Usage:            &dto.Usage{CompletionTokens: 54},
		},
		SendResponseCount: 2,
	}

	responses := StreamResponseOpenAI2Claude(&dto.ChatCompletionsStreamResponse{
		Usage: &dto.Usage{CompletionTokens: 54},
	}, info)

	require.Empty(t, responses)
	require.False(t, info.ClaudeConvertInfo.Done)
	require.Empty(t, info.FinishReason)
}

func TestStreamResponseOpenAI2ClaudeFinishesAfterUsageOnlyChunk(t *testing.T) {
	finishReason := "tool_calls"
	info := &relaycommon.RelayInfo{
		ClaudeConvertInfo: &relaycommon.ClaudeConvertInfo{
			LastMessagesType: relaycommon.LastMessageTypeTools,
		},
		SendResponseCount: 2,
	}

	finishResponses := StreamResponseOpenAI2Claude(&dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{FinishReason: &finishReason},
		},
	}, info)
	require.Empty(t, finishResponses)
	require.False(t, info.ClaudeConvertInfo.Done)
	require.Equal(t, "tool_calls", info.FinishReason)

	usageResponses := StreamResponseOpenAI2Claude(&dto.ChatCompletionsStreamResponse{
		Usage: &dto.Usage{CompletionTokens: 54},
	}, info)

	require.True(t, info.ClaudeConvertInfo.Done)
	require.Len(t, usageResponses, 3)
	require.Equal(t, "content_block_stop", usageResponses[0].Type)
	require.Equal(t, "message_delta", usageResponses[1].Type)
	require.Equal(t, "tool_use", *usageResponses[1].Delta.StopReason)
	require.Equal(t, "message_stop", usageResponses[2].Type)
}

// HasEmittedAnswer 只应在产出实际答复内容（text / tool_use）时置位，
// 仅有 thinking 不算，供上游缺少 finish_reason 时判断能否安全兜底收尾。
func TestStreamResponseOpenAI2ClaudeTracksEmittedAnswer(t *testing.T) {
	reasoning := "thinking only"
	content := "hello"
	toolIndex := 0

	tests := []struct {
		name  string
		delta dto.ChatCompletionsStreamResponseChoiceDelta
		want  bool
	}{
		{
			name:  "thinking does not count",
			delta: dto.ChatCompletionsStreamResponseChoiceDelta{ReasoningContent: &reasoning},
			want:  false,
		},
		{
			name:  "text counts",
			delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: &content},
			want:  true,
		},
		{
			name: "tool call counts",
			delta: dto.ChatCompletionsStreamResponseChoiceDelta{
				ToolCalls: []dto.ToolCallResponse{
					{
						Index:    &toolIndex,
						ID:       "call_bash",
						Type:     "function",
						Function: dto.FunctionResponse{Name: "Bash", Arguments: "{}"},
					},
				},
			},
			want: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{
				ClaudeConvertInfo: &relaycommon.ClaudeConvertInfo{
					LastMessagesType: relaycommon.LastMessageTypeNone,
				},
				SendResponseCount: 2,
			}

			StreamResponseOpenAI2Claude(&dto.ChatCompletionsStreamResponse{
				Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: test.delta}},
			}, info)

			require.Equal(t, test.want, info.ClaudeConvertInfo.HasEmittedAnswer)
			require.Equal(t, test.want, CanFinalizeClaudeStreamWithoutFinishReason(withNormalStreamEnd(info)))
		})
	}
}

func withNormalStreamEnd(info *relaycommon.RelayInfo) *relaycommon.RelayInfo {
	status := relaycommon.NewStreamStatus()
	status.SetEndReason(relaycommon.StreamEndReasonDone, nil)
	info.StreamStatus = status
	return info
}

// 上游把 tool_calls 与 finish_reason 放在同一个 chunk 时（GLM 未开启 tool_stream 时的常见形态），
// 该 chunk 的工具调用内容必须照常转换，不能因为 chunk 自身没带 usage 就被整块丢弃。
func TestStreamResponseOpenAI2ClaudeKeepsToolCallsOnFinishChunk(t *testing.T) {
	info := &relaycommon.RelayInfo{
		ClaudeConvertInfo: &relaycommon.ClaudeConvertInfo{
			LastMessagesType: relaycommon.LastMessageTypeThinking,
		},
		SendResponseCount: 2,
	}

	finishReason := "tool_calls"
	toolIndex := 0
	responses := StreamResponseOpenAI2Claude(&dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{
				Delta: dto.ChatCompletionsStreamResponseChoiceDelta{
					ToolCalls: []dto.ToolCallResponse{
						{
							Index: &toolIndex,
							ID:    "call_bash",
							Type:  "function",
							Function: dto.FunctionResponse{
								Name:      "Bash",
								Arguments: `{"command":"ls -la"}`,
							},
						},
					},
				},
				FinishReason: &finishReason,
			},
		},
	}, info)

	require.Len(t, responses, 3)
	require.Equal(t, "content_block_stop", responses[0].Type)
	require.Equal(t, "tool_use", responses[1].ContentBlock.Type)
	require.Equal(t, "Bash", responses[1].ContentBlock.Name)
	require.Equal(t, "input_json_delta", responses[2].Delta.Type)
	require.Equal(t, "tool_calls", info.FinishReason)
	// 本 chunk 没有 usage，关闭动作延后到 usage-only chunk
	require.False(t, info.ClaudeConvertInfo.Done)

	usageResponses := StreamResponseOpenAI2Claude(&dto.ChatCompletionsStreamResponse{
		Usage: &dto.Usage{CompletionTokens: 54},
	}, info)
	require.True(t, info.ClaudeConvertInfo.Done)
	require.Equal(t, "message_delta", usageResponses[len(usageResponses)-2].Type)
	require.Equal(t, "tool_use", *usageResponses[len(usageResponses)-2].Delta.StopReason)
	require.Equal(t, "message_stop", usageResponses[len(usageResponses)-1].Type)
}

func TestStreamResponseOpenAI2ClaudeConvertsThinkingToolCallFlow(t *testing.T) {
	info := &relaycommon.RelayInfo{
		ClaudeConvertInfo: &relaycommon.ClaudeConvertInfo{
			LastMessagesType: relaycommon.LastMessageTypeNone,
		},
		SendResponseCount: 1,
	}
	reasoning := "I should inspect the workspace."
	reasoningResponses := StreamResponseOpenAI2Claude(&dto.ChatCompletionsStreamResponse{
		Id:    "chatcmpl-test",
		Model: "GLM-5.2",
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{
				Delta: dto.ChatCompletionsStreamResponseChoiceDelta{
					ReasoningContent: &reasoning,
				},
			},
		},
	}, info)
	require.Equal(t, "message_start", reasoningResponses[0].Type)
	require.Equal(t, "thinking", reasoningResponses[1].ContentBlock.Type)

	info.SendResponseCount = 2
	toolIndex := 0
	toolResponses := StreamResponseOpenAI2Claude(&dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{
				Delta: dto.ChatCompletionsStreamResponseChoiceDelta{
					ToolCalls: []dto.ToolCallResponse{
						{
							Index: &toolIndex,
							ID:    "call_bash",
							Type:  "function",
							Function: dto.FunctionResponse{
								Name:      "Bash",
								Arguments: `{"command":"pwd"}`,
							},
						},
					},
				},
			},
		},
	}, info)
	require.Equal(t, "content_block_stop", toolResponses[0].Type)
	require.Equal(t, "tool_use", toolResponses[1].ContentBlock.Type)
	require.Equal(t, "Bash", toolResponses[1].ContentBlock.Name)
	require.Equal(t, "input_json_delta", toolResponses[2].Delta.Type)

	finishReason := "tool_calls"
	finishResponses := StreamResponseOpenAI2Claude(&dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{FinishReason: &finishReason},
		},
		Usage: &dto.Usage{CompletionTokens: 54},
	}, info)
	require.Len(t, finishResponses, 3)
	require.Equal(t, "content_block_stop", finishResponses[0].Type)
	require.Equal(t, "message_delta", finishResponses[1].Type)
	require.Equal(t, "tool_use", *finishResponses[1].Delta.StopReason)
	require.Equal(t, "message_stop", finishResponses[2].Type)
	require.True(t, info.ClaudeConvertInfo.Done)
}
