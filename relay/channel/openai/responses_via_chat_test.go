package openai

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

func TestOpenAIAdaptor_SupportsNativeResponses(t *testing.T) {
	a := &Adaptor{}

	cases := []struct {
		name        string
		channelType int
		baseURL     string
		want        bool
	}{
		{"official openai empty base url", constant.ChannelTypeOpenAI, "", true},
		{"official openai explicit base url", constant.ChannelTypeOpenAI, "https://api.openai.com/v1", true},
		{"azure", constant.ChannelTypeAzure, "https://x.openai.azure.com", true},
		{"third-party custom base url", constant.ChannelTypeOpenAI, "https://api.groq.com/openai/v1", false},
		{"self-hosted custom base url", constant.ChannelTypeOpenAI, "http://localhost:8000/v1", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: tc.channelType, ChannelBaseUrl: tc.baseURL}}
			if got := a.SupportsNativeResponses(info); got != tc.want {
				t.Errorf("SupportsNativeResponses = %v, want %v", got, tc.want)
			}
		})
	}

	// Chat is always supported for OpenAI-compatible upstreams.
	if !a.SupportsNativeChat(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI}}) {
		t.Error("SupportsNativeChat should be true")
	}
}
