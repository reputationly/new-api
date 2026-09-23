package relay

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/require"
)

// 这些渠道不支持 /v1/messages。ConvertClaudeRequest 必须返回 error 而不是 panic:
// panic 会绕过 controller.Relay 里按 newAPIError 判断的退款分支,预扣额度不退。
func TestUnsupportedChannelsConvertClaudeRequestReturnsError(t *testing.T) {
	apiTypes := map[string]int{
		"baidu":      constant.APITypeBaidu,
		"cohere":     constant.APITypeCohere,
		"tencent":    constant.APITypeTencent,
		"cloudflare": constant.APITypeCloudflare,
		"dify":       constant.APITypeDify,
		"xunfei":     constant.APITypeXunfei,
		"mokaai":     constant.APITypeMokaAI,
		"zhipu":      constant.APITypeZhipu,
		"jina":       constant.APITypeJina,
		"mistral":    constant.APITypeMistral,
		"palm":       constant.APITypePaLM,
	}
	for name, apiType := range apiTypes {
		t.Run(name, func(t *testing.T) {
			adaptor := GetAdaptor(apiType)
			require.NotNil(t, adaptor)
			require.NotPanics(t, func() {
				converted, err := adaptor.ConvertClaudeRequest(nil, &relaycommon.RelayInfo{}, &dto.ClaudeRequest{})
				require.Error(t, err)
				require.Nil(t, converted)
			})
		})
	}
}
