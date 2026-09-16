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

func runArkV3(t *testing.T, handlers []gin.HandlerFunc, method, route, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	router := gin.New()
	router.Handle(method, route, handlers...)

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

const arkCreatePath = "/api/v3/contents/generations/tasks"

func TestArkV3CreateConvertRewritesRequestAndResponse(t *testing.T) {
	var seenBody, seenPath string
	handler := func(c *gin.Context) {
		seenPath = c.Request.URL.Path
		// 下游一律经 GetBodyStorage 读 body：必须看到转换后的统一契约形态，
		// 而不是官方 content[] 形态。只 c.Set 不换 storage 的话这里会读到旧的。
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

	w := runArkV3(t, []gin.HandlerFunc{ArkV3CreateConvert(), handler},
		http.MethodPost, arkCreatePath, arkCreatePath, `{
			"model":"doubao-seedance-2-0-260128",
			"content":[{"type":"text","text":"a cat"}],
			"resolution":"720p","ratio":"16:9","duration":5,
			"safety_identifier":"sha256-x"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	// 提交端点必须复用既有的统一契约链路，所以路径要被改写。
	if seenPath != "/v1/video/generations" {
		t.Errorf("downstream path = %q, want /v1/video/generations", seenPath)
	}
	if !strings.Contains(seenBody, `"prompt"`) || strings.Contains(seenBody, `"content"`) {
		t.Errorf("downstream body was not converted: %s", seenBody)
	}
	// 方舟提交接口只回一个 id（加 safety_identifier 回显），不是 OpenAI 的 video 对象。
	got := w.Body.String()
	if !strings.Contains(got, `"id":"task_abc"`) || !strings.Contains(got, `"safety_identifier":"sha256-x"`) {
		t.Errorf("response was not rewritten to the ark shape: %s", got)
	}
	if strings.Contains(got, `"object"`) {
		t.Errorf("OpenAI video fields leaked into the ark response: %s", got)
	}
}

// 转换期的校验错误要走方舟信封，而且 HTTP 状态码是真实状态码。
func TestArkV3CreateConvertRejectsInvalidRequestInArkShape(t *testing.T) {
	handler := func(c *gin.Context) { t.Fatal("handler must not be reached") }

	w := runArkV3(t, []gin.HandlerFunc{ArkV3CreateConvert(), handler},
		http.MethodPost, arkCreatePath, arkCreatePath, `{"content":[{"type":"text","text":"x"}]}`)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{`"code":"InvalidParameter"`, `"type":"BadRequest"`, "model is required"} {
		if !strings.Contains(body, want) {
			t.Errorf("error body %s missing %s", body, want)
		}
	}
}

// 鉴权失败发生在转换之后的中间件里，响应也必须是方舟信封 —— 方舟 SDK 解不了
// 本仓自己的 {"error":{"message":...}} 形态，401 上会变成一个无法归类的错误。
func TestArkV3EnvelopeRewritesDownstreamErrors(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		body       string
		wantCode   string
		wantMsgSub string
	}{
		{"OpenAI 风格错误", http.StatusUnauthorized,
			`{"error":{"message":"无效的令牌","type":"new_api_error"}}`, "AuthenticationError", "无效的令牌"},
		{"TaskError 形态", http.StatusBadRequest,
			`{"code":"invalid_request","message":"model not available"}`, "InvalidParameter", "model not available"},
		{"限流", http.StatusTooManyRequests,
			`{"error":{"message":"rate limited"}}`, "RateLimitExceeded", "rate limited"},
		{"上游 5xx", http.StatusBadGateway,
			`{"error":{"message":"upstream down"}}`, "InternalServiceError", "upstream down"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handler := func(c *gin.Context) {
				c.Data(tc.status, "application/json", []byte(tc.body))
				c.Abort()
			}
			w := runArkV3(t, []gin.HandlerFunc{ArkV3Envelope(), handler},
				http.MethodGet, arkCreatePath, arkCreatePath, "")

			if w.Code != tc.status {
				t.Fatalf("status = %d, want %d", w.Code, tc.status)
			}
			got := w.Body.String()
			if !strings.Contains(got, `"code":"`+tc.wantCode+`"`) {
				t.Errorf("body %s missing code %s", got, tc.wantCode)
			}
			// 上游的错误信息不能在包装过程中被丢掉。
			if !strings.Contains(got, tc.wantMsgSub) {
				t.Errorf("body %s lost the upstream message %q", got, tc.wantMsgSub)
			}
		})
	}
}

// 已经是方舟信封的错误不能被二次包装（本兼容层自己 abort 时就是这种情况）。
func TestArkV3EnvelopeDoesNotDoubleWrap(t *testing.T) {
	inner := `{"error":{"code":"OperationNotSupported","message":"nope","param":"","type":"BadRequest"}}`
	handler := func(c *gin.Context) {
		c.Data(http.StatusBadRequest, "application/json", []byte(inner))
		c.Abort()
	}
	w := runArkV3(t, []gin.HandlerFunc{ArkV3Envelope(), handler},
		http.MethodGet, arkCreatePath, arkCreatePath, "")

	if got := strings.TrimSpace(w.Body.String()); got != inner {
		t.Errorf("envelope was re-wrapped:\n got  %s\n want %s", got, inner)
	}
}

// 成功响应原样透出（查询 / 列表的 body 本来就已经是官方形态）。
func TestArkV3EnvelopePassesSuccessThrough(t *testing.T) {
	payload := `{"total":1,"items":[{"id":"task_abc","status":"succeeded"}]}`
	handler := func(c *gin.Context) { c.Data(http.StatusOK, "application/json", []byte(payload)) }
	w := runArkV3(t, []gin.HandlerFunc{ArkV3Envelope(), handler},
		http.MethodGet, arkCreatePath, arkCreatePath, "")

	if got := strings.TrimSpace(w.Body.String()); got != payload {
		t.Errorf("success body was altered:\n got  %s\n want %s", got, payload)
	}
}

// 官方 DELETE「操作成功时不返回业务响应体」。缓冲 writer 不能往 204 里塞任何东西
// —— 带 body 的 204 是非法响应，严格的 HTTP 客户端会直接报错。
func TestArkV3EnvelopeKeeps204Empty(t *testing.T) {
	handler := func(c *gin.Context) { c.Status(http.StatusNoContent) }
	w := runArkV3(t, []gin.HandlerFunc{ArkV3Envelope(), handler},
		http.MethodDelete, arkCreatePath, arkCreatePath, "")

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Errorf("204 carried a body: %s", w.Body.String())
	}
}

// 处理链什么都没写就结束（理论上不该发生）：给一个合法的方舟错误，而不是空 200
// —— 调用方拿着空响应只能去猜。
func TestArkV3EnvelopeTurnsEmpty200IntoAnError(t *testing.T) {
	handler := func(c *gin.Context) {}
	w := runArkV3(t, []gin.HandlerFunc{ArkV3Envelope(), handler},
		http.MethodGet, arkCreatePath, arkCreatePath, "")

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"code":"InternalServiceError"`) {
		t.Errorf("unexpected body: %s", w.Body.String())
	}
}
