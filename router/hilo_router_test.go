package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// 生成入口必须**说得清"还没做"**，而不是笼统的 404。
//
// 不显式注册的话，这些 POST 会落到 `web-router` 的 `NoRoute` 兜底
// （`/api` 前缀 → `controller.RelayNotFound` → `Invalid URL`）。
// 而 `GET /api/v1/models/config` 下发的目录正在把客户端往这些路径上引 ——
// `backend` 字段决定它 POST 到哪一条。
//
// 两种失败对排查的价值差很多：404 `Invalid URL` 看起来像**路由配错了**，
// 会让人去翻路由表；501 `API not implemented` 才说得清是这一半还没实现。
//
// 路径不是我们定的，是官方 gateway 写死往外发的（抓包实测）。所以这个
// 测试同时也钉住了那份清单 —— 官方升级换了路径，这里要跟着改。
func TestHiloGenerateRoutesAnswerNotImplemented(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	SetHiloRouter(r)

	paths := []string{
		"/api/v2/image/qwen/generate",
		"/api/v2/image/nano_banana/generate",
		"/api/v2/image/seedream/generate",
		"/api/v2/image/enhance/generate",
		"/api/v2/audio/tts",
		"/api/v2/audio/music/minimax",
	}
	for _, p := range paths {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, p, nil))
		if w.Code != http.StatusNotImplemented {
			t.Errorf("POST %s = %d，期望 501（未实现要说清楚，别落到 404 兜底）", p, w.Code)
		}
	}
}

// 视频那条**已经接上真实转发**，不该再是 501。
//
// 它和上面那批的区别就是"做了没有"，所以这两个用例必须分开写：
// 合成一个的话，哪天把 501 换成实现、忘了改测试，测试会替这个遗漏背书。
func TestHiloVideoRouteIsWiredNotStubbed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	SetHiloRouter(r)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/video/minimax-v3/generate",
		strings.NewReader(`{"model":"MiniMax-H3","prompt":"a cat"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code == http.StatusNotImplemented {
		t.Error("视频转发已经实现了，不该还回 501")
	}
}
