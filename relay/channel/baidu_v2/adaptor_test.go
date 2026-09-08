package baidu_v2

import (
	"net/http/httptest"
	"testing"

	channelconstant "github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

// /v1/messages 不参与 Path2RelayMode，RelayMode 恒为 RelayModeUnknown(0)，
// 必须靠 RelayFormat 分派，否则会退化成 "unsupported relay mode: 0"。
func TestGetRequestURLClaudeFormat(t *testing.T) {
	t.Parallel()

	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		RelayMode:   relayconstant.RelayModeUnknown,
		RelayFormat: types.RelayFormatClaude,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl: "https://qianfan.baidubce.com",
		},
	}

	got, err := adaptor.GetRequestURL(info)
	if err != nil {
		t.Fatalf("GetRequestURL returned error: %v", err)
	}

	want := "https://qianfan.baidubce.com/v2/chat/completions"
	if got != want {
		t.Fatalf("GetRequestURL() = %q, want %q", got, want)
	}
}

func TestGetRequestURLOpenAIFormat(t *testing.T) {
	t.Parallel()

	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		RelayMode:   relayconstant.RelayModeRerank,
		RelayFormat: types.RelayFormatOpenAI,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl: "https://qianfan.baidubce.com",
		},
	}

	got, err := adaptor.GetRequestURL(info)
	if err != nil {
		t.Fatalf("GetRequestURL returned error: %v", err)
	}

	want := "https://qianfan.baidubce.com/v2/rerank"
	if got != want {
		t.Fatalf("GetRequestURL() = %q, want %q", got, want)
	}
}

// 非 Claude 格式下的未知 mode 仍应报错，别让 Claude 分支把兜底吃掉。
func TestGetRequestURLUnknownModeStillErrors(t *testing.T) {
	t.Parallel()

	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		RelayMode:   relayconstant.RelayModeUnknown,
		RelayFormat: types.RelayFormatOpenAI,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl: "https://qianfan.baidubce.com",
		},
	}

	if _, err := adaptor.GetRequestURL(info); err == nil {
		t.Fatal("GetRequestURL() returned nil error for unknown relay mode, want error")
	}
}

// -search 后缀的剥离和 web_search 注入原先只写在 ConvertOpenAIRequest 里，
// Claude 路径走 ConvertClaudeRequest，会绕过它把后缀原样发给千帆。
func TestConvertClaudeRequestAppliesSearchSuffix(t *testing.T) {
	t.Parallel()

	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatClaude,
		OriginModelName: "ernie-4.0-8k-search",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl:    "https://qianfan.baidubce.com",
			UpstreamModelName: "ernie-4.0-8k-search",
		},
	}
	maxTokens := uint(1024)
	helloText := "你好"
	req := &dto.ClaudeRequest{
		Model:     "ernie-4.0-8k-search",
		MaxTokens: &maxTokens,
		Messages: []dto.ClaudeMessage{
			{Role: "user", Content: []dto.ClaudeMediaMessage{{Type: "text", Text: &helloText}}},
		},
	}

	got, err := adaptor.ConvertClaudeRequest(gin.CreateTestContextOnly(httptest.NewRecorder(), gin.New()), info, req)
	if err != nil {
		t.Fatalf("ConvertClaudeRequest returned error: %v", err)
	}

	// 必须是类型化的 *dto.GeneralOpenAIRequest：返回 map 会让
	// GuessRelayFormatFromRequest 认不出格式，转换链停在 Claude
	payload, ok := got.(*dto.GeneralOpenAIRequest)
	if !ok {
		t.Fatalf("ConvertClaudeRequest returned %T, want *dto.GeneralOpenAIRequest", got)
	}
	if payload.Model != "ernie-4.0-8k" {
		t.Fatalf("model = %q, want %q", payload.Model, "ernie-4.0-8k")
	}
	if len(payload.WebSearch) == 0 {
		t.Fatal("web_search missing, search-enabled model would silently lose web search")
	}
	if info.UpstreamModelName != "ernie-4.0-8k" {
		t.Fatalf("info.UpstreamModelName = %q, want %q", info.UpstreamModelName, "ernie-4.0-8k")
	}
}

func TestConvertOpenAIRequestAppliesSearchSuffix(t *testing.T) {
	t.Parallel()

	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeChatCompletions,
		RelayFormat:     types.RelayFormatOpenAI,
		OriginModelName: "ernie-4.0-8k-search",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl:    "https://qianfan.baidubce.com",
			UpstreamModelName: "ernie-4.0-8k-search",
		},
	}
	request := &dto.GeneralOpenAIRequest{
		Model:    "ernie-4.0-8k-search",
		Messages: []dto.Message{{Role: "user", Content: "你好"}},
	}

	got, err := adaptor.ConvertOpenAIRequest(gin.CreateTestContextOnly(httptest.NewRecorder(), gin.New()), info, request)
	if err != nil {
		t.Fatalf("ConvertOpenAIRequest returned error: %v", err)
	}

	payload, ok := got.(*dto.GeneralOpenAIRequest)
	if !ok {
		t.Fatalf("ConvertOpenAIRequest returned %T, want *dto.GeneralOpenAIRequest", got)
	}
	if payload.Model != "ernie-4.0-8k" {
		t.Fatalf("model = %q, want %q", payload.Model, "ernie-4.0-8k")
	}
	if len(payload.WebSearch) == 0 {
		t.Fatal("web_search missing")
	}
}

// 没有 -search 后缀时不该注入 web_search，也不该改模型名。
func TestConvertOpenAIRequestWithoutSearchSuffix(t *testing.T) {
	t.Parallel()

	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		RelayMode:   relayconstant.RelayModeChatCompletions,
		RelayFormat: types.RelayFormatOpenAI,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl:    "https://qianfan.baidubce.com",
			UpstreamModelName: "ernie-4.0-8k",
		},
	}
	request := &dto.GeneralOpenAIRequest{
		Model:    "ernie-4.0-8k",
		Messages: []dto.Message{{Role: "user", Content: "你好"}},
	}

	got, err := adaptor.ConvertOpenAIRequest(gin.CreateTestContextOnly(httptest.NewRecorder(), gin.New()), info, request)
	if err != nil {
		t.Fatalf("ConvertOpenAIRequest returned error: %v", err)
	}
	payload, ok := got.(*dto.GeneralOpenAIRequest)
	if !ok {
		t.Fatalf("ConvertOpenAIRequest returned %T, want *dto.GeneralOpenAIRequest", got)
	}
	if len(payload.WebSearch) != 0 {
		t.Fatal("ConvertOpenAIRequest injected web_search for a model without -search suffix")
	}
}

// 千帆流式默认不返回 usage，必须显式带 stream_options.include_usage。
// openai.Adaptor 会为非 OpenAI/Azure 渠道清空这个字段，本 adaptor 要补回来，
// 否则流式 Claude 请求全部落到本地估算计费。
func TestConvertClaudeRequestKeepsStreamOptions(t *testing.T) {
	t.Parallel()

	adaptor := &Adaptor{}
	stream := true
	info := &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatClaude,
		OriginModelName: "ernie-4.0-8k",
		IsStream:        true,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:          channelconstant.ChannelTypeBaiduV2,
			ChannelBaseUrl:       "https://qianfan.baidubce.com",
			UpstreamModelName:    "ernie-4.0-8k",
			SupportStreamOptions: true,
		},
	}
	maxTokens := uint(1024)
	helloText := "你好"
	req := &dto.ClaudeRequest{
		Model:     "ernie-4.0-8k",
		MaxTokens: &maxTokens,
		Stream:    &stream,
		Messages: []dto.ClaudeMessage{
			{Role: "user", Content: []dto.ClaudeMediaMessage{{Type: "text", Text: &helloText}}},
		},
	}

	got, err := adaptor.ConvertClaudeRequest(gin.CreateTestContextOnly(httptest.NewRecorder(), gin.New()), info, req)
	if err != nil {
		t.Fatalf("ConvertClaudeRequest returned error: %v", err)
	}

	payload, ok := got.(*dto.GeneralOpenAIRequest)
	if !ok {
		t.Fatalf("ConvertClaudeRequest returned %T, want *dto.GeneralOpenAIRequest", got)
	}
	if payload.StreamOptions == nil {
		t.Fatal("stream_options dropped, upstream would omit usage and billing falls back to estimation")
	}
	if !payload.StreamOptions.IncludeUsage {
		t.Fatal("stream_options.include_usage = false, want true")
	}
}
