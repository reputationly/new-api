package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
)

// 小助手页面可能由外置 iframe 跨域加载；/api 组默认没有 CORS，
// 这里锁住 /api/assistant/feedback 的预检与响应头，避免线上浏览器直接拦掉反馈。
func TestAssistantFeedbackCORS(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)

	req := httptest.NewRequest(http.MethodOptions, "/api/assistant/feedback", nil)
	req.Header.Set("Origin", "https://assistant.example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "authorization,content-type")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204 (body=%s)", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" && got != "https://assistant.example.com" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want * or request origin", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Fatalf("preflight missing Access-Control-Allow-Methods")
	}
	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Fatalf("Access-Control-Allow-Credentials = %q, want empty (token-only endpoint)", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(strings.ToLower(got), "authorization") {
		t.Fatalf("Access-Control-Allow-Headers = %q, want Authorization", got)
	}
}

func TestAssistantFeedbackRequiresAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(sessions.Sessions("session", cookie.NewStore([]byte("test-secret"))))
	SetApiRouter(engine)

	req := httptest.NewRequest(http.MethodPost, "/api/assistant/feedback", strings.NewReader(`{"channel":"general","content":"hi"}`))
	req.Header.Set("Origin", "https://assistant.example.com")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body=%s)", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" && got != "https://assistant.example.com" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want * or request origin", got)
	}
	if got := w.Header().Get("Access-Control-Expose-Headers"); !strings.Contains(strings.ToLower(got), "x-oneapi-request-id") {
		t.Fatalf("Access-Control-Expose-Headers = %q, want X-Oneapi-Request-Id", got)
	}
}
