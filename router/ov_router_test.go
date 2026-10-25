package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

// `/v1/ov/*` 必须**注册过**，而且不能落到 web 的 SPA 兜底。
//
// 不注册的话这两条会走 `SetWebRouter` 的 NoRoute → `Invalid URL`，客户端看到的是一句
// 和"这个接口不存在"没区别的 404 —— 而它其实是"权限不对"或"没挂载"。
//
// 这里只断言"路由存在 + 没鉴权时 401"，不碰鉴权逻辑本身（那是 middleware 的测试）。
func TestOvRoutesAreRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetOvRouter(engine)

	registered := map[string]bool{}
	for _, route := range engine.Routes() {
		registered[route.Method+" "+route.Path] = true
	}
	for _, route := range []string{
		"GET /v1/ov/capabilities",
		"POST /v1/ov/files",
	} {
		if !registered[route] {
			t.Errorf("route not registered: %s", route)
		}
	}
}

// 没有令牌时必须是 401，不能是 404 —— 客户端把 404 当"这个部署没这个功能"，
// 于是会去隐藏入口，而真实原因是它没带 key。
func TestOvRoutesRequireToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previousRedis })
	engine := gin.New()
	SetOvRouter(engine)

	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/v1/ov/capabilities"},
		{http.MethodPost, "/v1/ov/files"},
	} {
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, httptest.NewRequest(tc.method, tc.path, nil))
		if recorder.Code != http.StatusUnauthorized {
			t.Errorf("%s %s = %d，期望 401", tc.method, tc.path, recorder.Code)
		}
	}
}

// 限流被拒时要有**带错误码的 JSON 体**。
//
// 三条限流中间件里只有这一条会补体：另外两条是空体 429，客户端分不出"被限流"和
// "服务出错"（见 ovaijisuandesign 的 L-4）。这里钉住这条差异，别被"统一成空体"改回去。
func TestOvRateLimitAnswersWithErrorCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousEnable, previousNum, previousDuration := common.GlobalApiRateLimitEnable, common.GlobalApiRateLimitNum, common.GlobalApiRateLimitDuration
	previousRedis := common.RedisEnabled
	// 内存桶就够；Redis 未连接时走 redis 分支会解引用 nil 客户端。
	common.RedisEnabled = false
	common.GlobalApiRateLimitEnable = true
	common.GlobalApiRateLimitNum = 1
	common.GlobalApiRateLimitDuration = 60
	t.Cleanup(func() {
		common.GlobalApiRateLimitEnable = previousEnable
		common.GlobalApiRateLimitNum = previousNum
		common.GlobalApiRateLimitDuration = previousDuration
		common.RedisEnabled = previousRedis
	})

	engine := gin.New()
	handler := gin.HandlerFunc(func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	engine.GET("/ov-test", middleware.OvRateLimit(), handler)

	// 用一个人造的、本进程其它用例不会用到的出口 IP：内存桶的 key 是 mark + ClientIP，
	// 和别的用例共用 IP 会让这条用例先被别人的请求计数挤成 429。
	const clientAddr = "203.0.113.7:34567"
	firstRequest := httptest.NewRequest(http.MethodGet, "/ov-test", nil)
	firstRequest.RemoteAddr = clientAddr
	first := httptest.NewRecorder()
	engine.ServeHTTP(first, firstRequest)
	if first.Code != http.StatusOK {
		t.Fatalf("第一次请求 %d，期望 200", first.Code)
	}

	secondRequest := httptest.NewRequest(http.MethodGet, "/ov-test", nil)
	secondRequest.RemoteAddr = clientAddr
	second := httptest.NewRecorder()
	engine.ServeHTTP(second, secondRequest)
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("第二次请求 %d，期望 429", second.Code)
	}
	body := second.Body.String()
	if !strings.Contains(body, string(types.ErrorCodeRateLimited)) {
		t.Fatalf("429 体里没有 %s：%s", types.ErrorCodeRateLimited, body)
	}
}
