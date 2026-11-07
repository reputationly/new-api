package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 非流式响应出口：改名之后 body 与响应头里都不再出现上游模型名。
func TestIOCopyBytesGracefullyRewritesMappedModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	common.SetContextKey(c, constant.ContextKeyPublicModelName, "my-gpt-4o")
	common.SetContextKey(c, constant.ContextKeyUpstreamModelName, "gpt-4o")

	upstream := &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": {"application/json"},
			"X-Upstream":   {"gpt-4o"},
		},
	}
	IOCopyBytesGracefully(c, upstream, []byte(`{"model":"gpt-4o","choices":[]}`))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"model":"my-gpt-4o"`)
	require.NotContains(t, recorder.Body.String(), `"model":"gpt-4o"`)
	require.Empty(t, recorder.Header().Values("X-Upstream"),
		"回显上游模型名的响应头必须被丢掉")
}

// 没配改名的渠道必须逐字节原样透传：这是绝大多数站点的实际路径。
func TestIOCopyBytesGracefullyPassthroughWithoutMapping(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	body := []byte(`{"model":"gpt-4o","unknown_field":{"keep":"byte for byte"}}`)
	IOCopyBytesGracefully(c, &http.Response{StatusCode: http.StatusOK, Header: http.Header{}}, body)

	require.Equal(t, string(body), recorder.Body.String())
}
