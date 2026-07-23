package relay

import (
	"testing"

	appconstant "github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
)

func TestDecideResponsesRoute(t *testing.T) {
	cases := []struct {
		name        string
		apiType     int
		channelType int
		baseURL     string
		relayMode   int
		want        responsesRouteDecision
	}{
		// compact routing is consistent with regular /responses:
		{"deepseek compact convert", appconstant.APITypeDeepSeek, appconstant.ChannelTypeDeepSeek, "", relayconstant.RelayModeResponsesCompact, routeConvertToChat},
		{"ali compact native", appconstant.APITypeAli, appconstant.ChannelTypeAli, "", relayconstant.RelayModeResponsesCompact, routeNativeResponses},
		// Adaptors that natively serve /v1/responses must stay native (no regression).
		{"ali native", appconstant.APITypeAli, appconstant.ChannelTypeAli, "", 0, routeNativeResponses},
		{"xai native", appconstant.APITypeXai, appconstant.ChannelTypeXai, "", 0, routeNativeResponses},
		{"perplexity native", appconstant.APITypePerplexity, appconstant.ChannelTypePerplexity, "", 0, routeNativeResponses},
		{"volcengine native", appconstant.APITypeVolcEngine, appconstant.ChannelTypeVolcEngine, "", 0, routeNativeResponses},
		{"cloudflare native", appconstant.APITypeCloudflare, appconstant.ChannelCloudflare, "", 0, routeNativeResponses},
		// Chat-only OpenAI-wire adaptors (error on native responses) borrow via chat.
		{"deepseek convert", appconstant.APITypeDeepSeek, appconstant.ChannelTypeDeepSeek, "", 0, routeConvertToChat},
		{"minimax convert", appconstant.APITypeMiniMax, appconstant.ChannelTypeMiniMax, "", 0, routeConvertToChat},
		// OpenAI adaptor: official/empty base_url native, custom base_url converts.
		{"openai official native", appconstant.APITypeOpenAI, appconstant.ChannelTypeOpenAI, "", 0, routeNativeResponses},
		{"openai custom base_url convert", appconstant.APITypeOpenAI, appconstant.ChannelTypeOpenAI, "https://api.groq.com/openai/v1", 0, routeConvertToChat},
		// Private-wire, non-native upstream: no compatible endpoint (was an error before too).
		{"gemini no compatible endpoint", appconstant.APITypeGemini, appconstant.ChannelTypeGemini, "", 0, routeNoCompatibleEndpoint},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			adaptor := GetAdaptor(tc.apiType)
			if adaptor == nil {
				t.Fatalf("no adaptor for apiType %d", tc.apiType)
			}
			mode := tc.relayMode
			if mode == 0 {
				mode = relayconstant.RelayModeResponses
			}
			info := &relaycommon.RelayInfo{
				RelayMode: mode,
				ChannelMeta: &relaycommon.ChannelMeta{
					ApiType:        tc.apiType,
					ChannelType:    tc.channelType,
					ChannelBaseUrl: tc.baseURL,
				},
			}
			if got := decideResponsesRoute(info, adaptor); got != tc.want {
				t.Errorf("decideResponsesRoute = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestUpstreamSpeaksOpenAIWireChat(t *testing.T) {
	openAIWire := []int{
		appconstant.APITypeOpenAI,
		appconstant.APITypeOpenRouter,
		appconstant.APITypeXinference,
		appconstant.APITypeAli,
		appconstant.APITypeBaiduV2,
		appconstant.APITypeDeepSeek,
		appconstant.APITypeMiniMax,
		appconstant.APITypeMoonshot,
		appconstant.APITypeSiliconFlow,
		appconstant.APITypeVolcEngine,
		appconstant.APITypeZhipuV4,
		appconstant.APITypeXai,
		appconstant.APITypeMistral,
	}
	for _, apiType := range openAIWire {
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiType: apiType}}
		if !upstreamSpeaksOpenAIWireChat(info) {
			t.Errorf("apiType %d should be OpenAI-wire", apiType)
		}
	}

	// Native-wire upstreams must NOT be treated as OpenAI-wire — they would be
	// mis-parsed by the borrow path.
	nativeWire := []int{
		appconstant.APITypeAws,
		appconstant.APITypeGemini,
		appconstant.APITypeAnthropic,
		appconstant.APITypeBaidu, // v1
		appconstant.APITypeTencent,
		appconstant.APITypePaLM,
		appconstant.APITypeCohere,
		appconstant.APITypeXunfei,
		appconstant.APITypeOllama,
	}
	for _, apiType := range nativeWire {
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiType: apiType}}
		if upstreamSpeaksOpenAIWireChat(info) {
			t.Errorf("apiType %d must NOT be OpenAI-wire (native format)", apiType)
		}
	}
}
