package controller

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// 语音适配器在写二进制时会把 Content-Type 设成 audio/wav、Content-Length 设成音频字节数。
// 我们把这些字节截进缓冲区后改写 JSON 时，**头部会残留**——不清就是"audio/wav 头 + 空 body"
// （线上实测客户端拿到 200 但 0 字节）。这条用例钉住清头这一步。
func TestRespondAfterBinaryCaptureClearsStaleHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	// 模拟下游（语音适配器）已经设过的实体头。
	c.Writer.Header().Set("Content-Type", "audio/wav")
	c.Writer.Header().Set("Content-Length", "12345")

	audio := []byte{0x52, 0x49, 0x46, 0x46} // "RIFF"
	resetResponseHeaders(c)
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"audio": hex.EncodeToString(audio)}})

	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type 应改回 JSON，实际 %q", got)
	}
	if got := rec.Header().Get("Content-Length"); got != "" {
		t.Fatalf("Content-Length 应被清掉（旧值 %q 会让客户端等一段永远不来的音频）", got)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是 JSON: %v (%q)", err, rec.Body.String())
	}
	data, _ := body["data"].(map[string]any)
	if data["audio"] != "52494646" {
		t.Fatalf("hex 内容不对: %v", data["audio"])
	}
}
