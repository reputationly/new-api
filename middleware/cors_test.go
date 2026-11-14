package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/gin-gonic/gin"
)

// 浏览器端要读 X-Oneapi-Request-Id 做反馈关联，CORS 必须把它放进 Expose-Headers。
func TestCORSExposesRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for name, mw := range map[string]gin.HandlerFunc{
		"legacy": CORS(),
		"token":  CORSForTokenAPI(),
	} {
		t.Run(name, func(t *testing.T) {
			r := gin.New()
			r.Use(mw)
			r.POST("/x", func(c *gin.Context) { c.Status(http.StatusNoContent) })

			req := httptest.NewRequest(http.MethodPost, "/x", nil)
			req.Header.Set("Origin", "https://assistant.example.com")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want 204", w.Code)
			}
			got := w.Header().Get("Access-Control-Expose-Headers")
			if !strings.Contains(strings.ToLower(got), strings.ToLower(common.RequestIdKey)) {
				t.Fatalf("Access-Control-Expose-Headers = %q, want %s", got, common.RequestIdKey)
			}
		})
	}
}
