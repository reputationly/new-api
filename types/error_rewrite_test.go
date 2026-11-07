package types

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// 上游透传的错误（ErrorTypeOpenAIError）在 ToOpenAIError / ToClaudeError 里是直接
// 返回 RelayError 结构体的，只改 e.Err 的 SetMessage 对响应体**不生效**。
// 改写对外文案必须走 RewriteMessage，否则客户端照样能看到上游真实模型名。
func TestRewriteMessageAffectsUpstreamOpenAIError(t *testing.T) {
	upstream := OpenAIError{
		Message: "No available channel for model no-such-model-xyz",
		Type:    "invalid_request_error",
		Code:    "model_not_found",
	}
	apiErr := WithOpenAIError(upstream, http.StatusNotFound)
	require.Contains(t, apiErr.ToOpenAIError().Message, "no-such-model-xyz")

	apiErr.RewriteMessage("No available channel for model my-glm")

	require.Equal(t, "No available channel for model my-glm", apiErr.ToOpenAIError().Message)
	// Claude 格式出口读的是 e.Err，同样要跟上。
	require.Equal(t, "No available channel for model my-glm", apiErr.ToClaudeError().Message)
	require.Equal(t, "No available channel for model my-glm", apiErr.Error())
}

// 网关自造的错误（ErrorTypeNewAPIError）本来就走 e.Err，改写同样要生效。
func TestRewriteMessageAffectsSelfAuthoredError(t *testing.T) {
	apiErr := NewError(nil, ErrorCodeModelNotFound)
	apiErr.SetMessage("model no-such-model-xyz not found")

	apiErr.RewriteMessage("model my-glm not found")

	require.Equal(t, "model my-glm not found", apiErr.ToOpenAIError().Message)
}

// RewriteMessage 不该动错误码与状态码：改写的是文案，不是分类。
func TestRewriteMessageKeepsCodeAndStatus(t *testing.T) {
	apiErr := WithOpenAIError(OpenAIError{Message: "a", Code: "model_not_found"}, http.StatusNotFound)

	apiErr.RewriteMessage("b")

	require.Equal(t, http.StatusNotFound, apiErr.StatusCode)
	require.Equal(t, ErrorCodeModelNotFound, apiErr.errorCode)
}
