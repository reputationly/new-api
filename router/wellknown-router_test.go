package router

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/gin-gonic/gin"
)

// 微信域名归属验证的真实形态：一个 32 位 hex 命名的 txt，内容是一串校验码。
const (
	wxVerifyName    = "f100cf7e4e69464cc13741f1ec39a31a.txt"
	wxVerifyContent = "11250a988b60085ca63517e842547ed4b5991b0c"
)

func newWellKnownEngine(t *testing.T, dir string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	t.Setenv(wellKnownDirEnv, dir)
	router := gin.New()
	SetWellKnownRouter(router)
	// 复刻真实注册顺序里紧跟其后的那些根级路由，确认不会触发 httprouter 的冲突 panic。
	router.GET("/api/status", func(c *gin.Context) {})
	router.GET("/m", func(c *gin.Context) {})
	router.GET("/audio-presets/*filepath", func(c *gin.Context) {})
	return router
}

func TestWellKnownRouterServesVerificationFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, wxVerifyName), []byte(wxVerifyContent), 0o644); err != nil {
		t.Fatal(err)
	}
	router := newWellKnownEngine(t, dir)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/"+wxVerifyName, nil))

	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, 期望 200", w.Code)
	}
	// 抓取方按整份响应体逐字比对，多一个换行或被注入任何内容都会验证失败。
	if got := w.Body.String(); got != wxVerifyContent {
		t.Errorf("响应体 = %q, 期望 %q", got, wxVerifyContent)
	}
	if got := w.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, 期望 no-store", got)
	}
}

func TestWellKnownRouterSkipsUnsafeNames(t *testing.T) {
	dir := t.TempDir()
	// 无扩展名的文件一律跳过——正是它们可能和 /api、/m 这类根级路由撞车。
	for _, name := range []string{"api", "m", ".hidden", "no-extension"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "nested.dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	// 能构造出来本身就是一半的断言：如果 "api"/"m" 被挂成了根级路由，
	// 下面那几条桩路由注册时 httprouter 就会 panic。
	router := newWellKnownEngine(t, dir)

	// 直接查注册表，不走请求——"/api"、"/m" 有桩路由占位，靠状态码分辨不出是谁在响应。
	stubs := map[string]bool{"/api/status": true, "/m": true, "/audio-presets/*filepath": true}
	for _, route := range router.Routes() {
		if !stubs[route.Path] {
			t.Errorf("不应挂载的路由被注册了: %s", route.Path)
		}
	}
}

// 符号链接的大小必须按链接目标算：entry.Info() 是 lstat，量到的是链接自身那几十字节，
// 而 os.ReadFile 跟随链接，两者不一致就等于大小上限失效。
func TestWellKnownRouterEnforcesSizeThroughSymlink(t *testing.T) {
	dir := t.TempDir()
	oversized := filepath.Join(t.TempDir(), "oversized")
	if err := os.WriteFile(oversized, make([]byte, maxWellKnownFileSize+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(oversized, filepath.Join(dir, "leak.txt")); err != nil {
		t.Skipf("当前环境无法创建符号链接: %v", err)
	}
	router := newWellKnownEngine(t, dir)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/leak.txt", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("超限的符号链接目标被发出了 %d 字节（状态码 %d）", w.Body.Len(), w.Code)
	}
}

// FIFO 必须在 stat 阶段就被挡掉：os.Open 会阻塞到有写入端出现，而这段代码跑在路由
// 注册阶段，卡住就是整个进程起不来。本用例一旦超时未返回即说明这条防线没了。
func TestWellKnownRouterSkipsFifoWithoutBlocking(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe.txt"), 0o600); err != nil {
		t.Skipf("当前环境无法创建 FIFO: %v", err)
	}
	router := newWellKnownEngine(t, dir)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/pipe.txt", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("状态码 = %d, 期望 404", w.Code)
	}
}

func TestWellKnownRouterMissingDirIsNoop(t *testing.T) {
	router := newWellKnownEngine(t, filepath.Join(t.TempDir(), "absent"))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/"+wxVerifyName, nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("状态码 = %d, 期望 404", w.Code)
	}
}
