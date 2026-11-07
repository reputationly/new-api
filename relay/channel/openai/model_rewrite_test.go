package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 端到端（上游响应 -> 客户端）：渠道配了「对外名 -> 内部名」之后，客户端拿到的
// 响应体里只能有对外名。
//
// 这条测试串起了三层：OpenaiHandler 解析上游 body → IOCopyBytesGracefully 写出 →
// 写出口按 context 里的对外名改写。任何一层被改掉，这里都会红。
func TestOpenaiHandlerRewritesUpstreamModelToPublicName(t *testing.T) {
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	// 这两个键由 ModelMappedHelper 在发生改名时写入（见 relay/helper/model_mapped.go）。
	common.SetContextKey(c, constant.ContextKeyPublicModelName, "my-gpt-4o")
	common.SetContextKey(c, constant.ContextKeyUpstreamModelName, "gpt-4o")

	info := &relaycommon.RelayInfo{
		OriginModelName: "my-gpt-4o",
		RelayFormat:     types.RelayFormatOpenAI,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gpt-4o",
		},
	}

	upstreamBody := `{
		"id": "chatcmpl-1",
		"object": "chat.completion",
		"created": 1700000000,
		"model": "gpt-4o",
		"choices": [{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],
		"usage": {"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
	}`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}

	_, apiErr := OpenaiHandler(c, info, resp)
	require.Nil(t, apiErr)

	var payload map[string]any
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	require.Equal(t, "my-gpt-4o", payload["model"])
	// 注意别用 "gpt-4o\"" 做否定断言：对外名 my-gpt-4o 本身就包含它。
	require.NotContains(t, recorder.Body.String(), `"model":"gpt-4o"`,
		"响应体里不应再出现上游模型名")
}

// 没有改名的渠道必须逐字节原样透传 —— 这是绝大多数站点的实际路径，
// 任何"顺手格式化一下"都会改变客户端看到的响应。
func TestOpenaiHandlerPassthroughWithoutModelMapping(t *testing.T) {
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	info := &relaycommon.RelayInfo{
		OriginModelName: "gpt-4o",
		RelayFormat:     types.RelayFormatOpenAI,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gpt-4o",
		},
	}

	upstreamBody := `{"id":"x","object":"chat.completion","model":"gpt-4o","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2},"zz_unknown":{"a":1}}`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}

	_, apiErr := OpenaiHandler(c, info, resp)
	require.Nil(t, apiErr)
	require.Equal(t, upstreamBody, recorder.Body.String())
}
