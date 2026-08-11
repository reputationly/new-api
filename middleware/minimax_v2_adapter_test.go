package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"

	"github.com/gin-gonic/gin"
)

func init() { gin.SetMode(gin.TestMode) }

func runMiniMaxV2(t *testing.T, handlers []gin.HandlerFunc, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	router := gin.New()
	router.Handle(method, "/v2/video_generation", handlers...)

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestMiniMaxV2CreateConvertRewritesRequestAndResponse(t *testing.T) {
	var seenBody string
	var seenPath string
	handler := func(c *gin.Context) {
		seenPath = c.Request.URL.Path
		// 下游一律经 GetBodyStorage 读 body:必须看到转换后的统一契约形态,
		// 而不是官方 content[] 形态。
		storage, err := common.GetBodyStorage(c)
		if err != nil {
			t.Fatalf("body storage: %s", err)
		}
		raw, err := storage.Bytes()
		if err != nil {
			t.Fatalf("body bytes: %s", err)
		}
		seenBody = string(raw)
		ov := dto.NewOpenAIVideo()
		ov.ID = "task_abc"
		ov.TaskID = "task_abc"
		c.JSON(http.StatusOK, ov)
	}

	w := runMiniMaxV2(t, []gin.HandlerFunc{MiniMaxV2CreateConvert(), handler},
		http.MethodPost, "/v2/video_generation", `{
			"model":"MiniMax-H3",
			"content":[{"type":"text","text":"a cat"}],
			"resolution":"768P","ratio":"16:9","duration":6}`)

	if seenPath != "/v1/video/generations" {
		t.Fatalf("path = %s, want /v1/video/generations", seenPath)
	}
	if !strings.Contains(seenBody, `"task_type":"t2v"`) || strings.Contains(seenBody, `"content"`) {
		t.Fatalf("downstream body not converted: %s", seenBody)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	// 官方提交接口只回一个 task_id,不是 OpenAI 那种完整 video 对象。
	if got := strings.TrimSpace(w.Body.String()); got != `{"task_id":"task_abc"}` {
		t.Fatalf("body = %s", got)
	}
}

func TestMiniMaxV2CreateConvertRejectsBeforeHandler(t *testing.T) {
	called := false
	handler := func(c *gin.Context) { called = true }

	w := runMiniMaxV2(t, []gin.HandlerFunc{MiniMaxV2CreateConvert(), handler},
		http.MethodPost, "/v2/video_generation", `{
			"model":"MiniMax-H3",
			"content":[{"type":"text","text":"a cat"}],
			"resolution":"2K","ratio":"16:9","duration":6}`)

	if called {
		t.Fatalf("handler must not run for a rejected request")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"type":"error"`) ||
		!strings.Contains(body, `"bad_request_error"`) ||
		!strings.Contains(body, "2K is not supported") {
		t.Fatalf("body = %s", body)
	}
}

func TestMiniMaxV2WrapsDownstreamErrorsInOfficialEnvelope(t *testing.T) {
	// 下游(controller / adaptor)的错误是本仓自己的 dto.TaskError 形态,官方 SDK 解不了。
	handler := func(c *gin.Context) {
		c.JSON(http.StatusTooManyRequests, &dto.TaskError{Code: "backpressure", Message: "上游队列已满"})
	}
	w := runMiniMaxV2(t, []gin.HandlerFunc{MiniMaxV2CreateConvert(), handler},
		http.MethodPost, "/v2/video_generation", `{
			"model":"MiniMax-H3",
			"content":[{"type":"text","text":"a cat"}],
			"resolution":"768P","ratio":"16:9","duration":6}`)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"rate_limit_error"`) || !strings.Contains(body, "上游队列已满") ||
		!strings.Contains(body, `"http_code":"429"`) {
		t.Fatalf("body = %s", body)
	}
}

func TestMiniMaxV2EnvelopePassesSuccessThrough(t *testing.T) {
	handler := func(c *gin.Context) {
		c.Data(http.StatusOK, "application/json", []byte(`{"task":{"id":"task_1"}}`))
	}
	w := runMiniMaxV2(t, []gin.HandlerFunc{MiniMaxV2Envelope(), handler},
		http.MethodPost, "/v2/video_generation", "")
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"task":{"id":"task_1"}}` {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
}

func TestMiniMaxV2EnvelopeDoesNotDoubleWriteOnPanic(t *testing.T) {
	// panic 时不能提交缓冲内容:Recovery 会往真 writer 再写一份,两段 body 拼起来
	// 就是非法 JSON。这里只断言响应仍是单个可解析的 JSON 对象。
	router := gin.New()
	router.Use(gin.CustomRecovery(func(c *gin.Context, err any) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "boom"}})
	}))
	router.POST("/v2/video_generation", MiniMaxV2Envelope(), func(c *gin.Context) {
		panic("handler exploded")
	})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v2/video_generation", nil))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	var probe map[string]any
	if err := common.Unmarshal(w.Body.Bytes(), &probe); err != nil {
		t.Fatalf("response is not a single JSON object: %s (%s)", w.Body.String(), err)
	}
}

func TestMiniMaxV2EnvelopeConvertsOpenAIStyleError(t *testing.T) {
	// 鉴权失败走的是 abortWithOpenAiMessage 的 {"error":{"message"}} 形态。
	handler := func(c *gin.Context) {
		abortWithOpenAiMessage(c, http.StatusUnauthorized, "无效的令牌")
	}
	w := runMiniMaxV2(t, []gin.HandlerFunc{MiniMaxV2Envelope(), handler},
		http.MethodPost, "/v2/video_generation", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"authorized_error"`) || !strings.Contains(body, "无效的令牌") {
		t.Fatalf("body = %s", body)
	}
}
