package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

// 没有配对象存储时，上传入口必须回 503 + capability_unavailable，
// **不是 500**：那不是"服务器坏了"，客户端该做的是退回内联上传（L-2），
// 而 500 会让它当成一次故障去重试。
func TestPostOvFilesWithoutStorageAnswersCapabilityUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/v1/ov/files", PostOvFiles)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/ov/files", strings.NewReader(""))
	request.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	engine.ServeHTTP(recorder, request)

	// 这个用例依赖"测试环境没开媒体存储"（系统设置默认 false）。
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("= %d，期望 503（测试环境没配对象存储）", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), string(types.ErrorCodeCapabilityUnavailable)) {
		t.Fatalf("响应体里没有 %s：%s", types.ErrorCodeCapabilityUnavailable, recorder.Body.String())
	}
}
