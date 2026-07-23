package relay

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	appconstant "github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/channel"
	openaichannel "github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

// openAIWireChatApiTypes are the upstream ApiTypes whose Chat Completions
// responses are OpenAI-compatible on the wire (their DoResponse is handled by
// the openai package's OaiStreamHandler/OpenaiHandler). The responses→chat
// borrow path parses the upstream response as OpenAI Chat SSE/JSON, so it is
// only valid for these ApiTypes. Providers with native wire formats (AWS
// Bedrock, Gemini, Anthropic, Baidu v1, Tencent, PaLM, Cohere, Xunfei, Ollama,
// …) must NOT be auto-converted — they route to a clear error instead of
// producing garbage. Such upstreams are normally reached through an
// intermediate new-api relay that already normalizes them to OpenAI wire.
var openAIWireChatApiTypes = map[int]bool{
	appconstant.APITypeOpenAI:      true, // also OpenRouter/Xinference/unknown fallback (openai.Adaptor)
	appconstant.APITypeOpenRouter:  true,
	appconstant.APITypeXinference:  true,
	appconstant.APITypeAli:         true,
	appconstant.APITypeBaiduV2:     true,
	appconstant.APITypeDeepSeek:    true,
	appconstant.APITypeMiniMax:     true,
	appconstant.APITypeMoonshot:    true,
	appconstant.APITypePerplexity:  true,
	appconstant.APITypeSiliconFlow: true,
	appconstant.APITypeVolcEngine:  true,
	appconstant.APITypeZhipuV4:     true,
	appconstant.APITypeXai:         true,
	appconstant.APITypeMistral:     true,
	appconstant.APITypeSubmodel:    true,
}

// upstreamSpeaksOpenAIWireChat reports whether the borrow path can parse this
// upstream's Chat Completions response as OpenAI wire format.
func upstreamSpeaksOpenAIWireChat(info *relaycommon.RelayInfo) bool {
	return openAIWireChatApiTypes[info.ApiType]
}

type responsesRouteDecision int

const (
	routeNativeResponses responsesRouteDecision = iota
	routeConvertToChat
	routeNoCompatibleEndpoint
)

// decideResponsesRoute implements §4.1.1: protocol match preferred, conversion
// only as a fill-in, with explicit policy overrides taking precedence. It
// applies to both /v1/responses and /v1/responses/compact — the same capability
// logic decides native passthrough vs. borrow-via-chat for each.
func decideResponsesRoute(info *relaycommon.RelayInfo, adaptor channel.Adaptor) responsesRouteDecision {
	if info.RelayMode != relayconstant.RelayModeResponses &&
		info.RelayMode != relayconstant.RelayModeResponsesCompact {
		return routeNativeResponses
	}
	// Raw pass-through bypasses conversion entirely.
	if model_setting.GetGlobalSettings().PassThroughRequestEnabled || info.ChannelSetting.PassThroughBodyEnabled {
		return routeNativeResponses
	}

	channelID := info.ChannelId
	channelType := info.ChannelType
	model := info.OriginModelName

	// 1) Explicit policy overrides pin behavior for specific channels/models.
	//    ForceConvert is a trusted operator override and bypasses the wire-format
	//    guard (it must only target OpenAI-wire upstreams — see docs).
	if service.ShouldResponsesForceNative(channelID, channelType, model) {
		return routeNativeResponses
	}
	if service.ShouldResponsesForceConvert(channelID, channelType, model) {
		return routeConvertToChat
	}

	// 2) Default: protocol match preferred — never convert if the upstream
	//    natively supports Responses (covers the "supports both" case too).
	if channel.SupportsNativeResponses(adaptor, info) {
		return routeNativeResponses
	}
	// 3) Auto-convert only when the upstream speaks OpenAI-wire Chat, so the
	//    borrow path can parse its response. Native-wire upstreams (AWS, Gemini,
	//    Anthropic, …) fall through to a clear error rather than garbage.
	if channel.SupportsNativeChat(adaptor, info) && upstreamSpeaksOpenAIWireChat(info) {
		return routeConvertToChat
	}
	return routeNoCompatibleEndpoint
}

// convertResponsesToChatAndSend converts a Responses request into a Chat
// Completions request (G1), sends it upstream via the standard Chat path, and
// returns the validated upstream *http.Response. It restores info.RelayMode /
// RequestURLPath before returning (via the caller-visible saved values) so
// downstream billing sees the original mode.
func convertResponsesToChatAndSend(c *gin.Context, info *relaycommon.RelayInfo, adaptor channel.Adaptor, chatReq *dto.GeneralOpenAIRequest) (*http.Response, *types.NewAPIError) {
	info.AppendRequestConversion(types.RelayFormatOpenAI)
	applySystemPromptIfNeeded(c, info, chatReq)

	info.RelayMode = relayconstant.RelayModeChatCompletions
	info.RequestURLPath = "/v1/chat/completions"
	// Some adaptors pick their Chat endpoint URL from RelayFormat (e.g. moonshot
	// special-base channels). Present the borrowed call as an OpenAI Chat request
	// during the request phase only; restore before the response handlers run so
	// they keep Responses semantics (no [DONE] sentinel for Responses clients).
	savedRelayFormat := info.RelayFormat
	info.RelayFormat = types.RelayFormatOpenAI
	defer func() { info.RelayFormat = savedRelayFormat }()

	convertedRequest, err := adaptor.ConvertOpenAIRequest(c, info, chatReq)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	relaycommon.AppendRequestConversionFromRequest(info, convertedRequest)

	jsonData, err := common.Marshal(convertedRequest)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}

	jsonData, err = relaycommon.RemoveDisabledFields(jsonData, info.ChannelOtherSettings, info.ChannelSetting.PassThroughBodyEnabled)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}

	if len(info.ParamOverride) > 0 {
		jsonData, err = relaycommon.ApplyParamOverrideWithRelayInfo(jsonData, info)
		if err != nil {
			return nil, newAPIErrorFromParamOverride(err)
		}
	}

	var requestBody io.Reader = bytes.NewBuffer(jsonData)

	resp, err := adaptor.DoRequest(c, info, requestBody)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	}
	if resp == nil {
		return nil, types.NewOpenAIError(nil, types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}

	httpResp := resp.(*http.Response)
	info.IsStream = info.IsStream || strings.HasPrefix(httpResp.Header.Get("Content-Type"), "text/event-stream")
	if httpResp.StatusCode != http.StatusOK {
		newApiErr := service.RelayErrorHandler(c.Request.Context(), httpResp, false)
		service.ResetStatusCode(newApiErr, c.GetString("status_code_mapping"))
		return nil, newApiErr
	}
	return httpResp, nil
}

// responsesViaChatCompletions serves a Responses API request from a Chat-only
// upstream: it converts the Responses request into a Chat Completions request
// (G1), sends it upstream via the standard Chat path, then converts the Chat
// response back into Responses form (G2/G3). It is the mirror of
// chatCompletionsViaResponses.
func responsesViaChatCompletions(c *gin.Context, info *relaycommon.RelayInfo, adaptor channel.Adaptor, request *dto.OpenAIResponsesRequest) (*dto.Usage, *types.NewAPIError) {
	chatReq, toolCtx, err := service.ResponsesRequestToChatCompletionsRequest(request)
	if err != nil {
		return nil, types.NewErrorWithStatusCode(err, types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	info.ResponsesToolContext = toolCtx

	// Request upstream usage only when the channel supports stream_options;
	// otherwise omit it (like the native Chat path) and fall back to the
	// handlers' estimated usage. Sending stream_options to a channel that does
	// not advertise support can make the upstream reject the request.
	if info.IsStream && info.SupportStreamOptions {
		if chatReq.StreamOptions == nil {
			chatReq.StreamOptions = &dto.StreamOptions{}
		}
		chatReq.StreamOptions.IncludeUsage = true
	}

	savedRelayMode := info.RelayMode
	savedRequestURLPath := info.RequestURLPath
	defer func() {
		info.RelayMode = savedRelayMode
		info.RequestURLPath = savedRequestURLPath
	}()

	httpResp, newApiErr := convertResponsesToChatAndSend(c, info, adaptor, chatReq)
	if newApiErr != nil {
		return nil, newApiErr
	}

	statusCodeMappingStr := c.GetString("status_code_mapping")
	if info.IsStream {
		usage, newApiErr := openaichannel.OaiChatToResponsesStreamHandler(c, info, httpResp)
		if newApiErr != nil {
			service.ResetStatusCode(newApiErr, statusCodeMappingStr)
			return nil, newApiErr
		}
		return usage, nil
	}

	usage, newApiErr := openaichannel.OaiChatToResponsesHandler(c, info, httpResp)
	if newApiErr != nil {
		service.ResetStatusCode(newApiErr, statusCodeMappingStr)
		return nil, newApiErr
	}
	return usage, nil
}

// responsesCompactViaChatCompletions serves a /v1/responses/compact request from
// a Chat-only upstream. The compact request already carries Codex's client-side
// summarization prompt, so a plain non-streaming Chat call produces the
// compacted window, which we return as Responses output items (G2) wrapped in a
// compaction response — no proprietary encrypted compaction item is fabricated.
func responsesCompactViaChatCompletions(c *gin.Context, info *relaycommon.RelayInfo, adaptor channel.Adaptor, request *dto.OpenAIResponsesRequest) (*dto.Usage, *types.NewAPIError) {
	// Compaction sends the full window in `input`; its previous_response_id is
	// redundant (and unusable on a Chat upstream), so drop it before conversion
	// to avoid the stateful-request guard.
	request.PreviousResponseID = ""
	chatReq, toolCtx, err := service.ResponsesRequestToChatCompletionsRequest(request)
	if err != nil {
		return nil, types.NewErrorWithStatusCode(err, types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	info.ResponsesToolContext = toolCtx
	// Compaction is always non-streaming.
	info.IsStream = false
	chatReq.Stream = common.GetPointer(false)
	chatReq.StreamOptions = nil

	savedRelayMode := info.RelayMode
	savedRequestURLPath := info.RequestURLPath
	defer func() {
		info.RelayMode = savedRelayMode
		info.RequestURLPath = savedRequestURLPath
	}()

	httpResp, newApiErr := convertResponsesToChatAndSend(c, info, adaptor, chatReq)
	if newApiErr != nil {
		return nil, newApiErr
	}

	usage, newApiErr := openaichannel.OaiChatToResponsesCompactionHandler(c, info, httpResp)
	if newApiErr != nil {
		service.ResetStatusCode(newApiErr, c.GetString("status_code_mapping"))
		return nil, newApiErr
	}
	return usage, nil
}
