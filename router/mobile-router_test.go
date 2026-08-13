package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/gin-gonic/gin"
)

const iphoneUA = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15"
const desktopUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36"

func targetFor(method, url, ua string) string {
	c := gin.CreateTestContextOnly(httptest.NewRecorder(), gin.New())
	c.Request = httptest.NewRequest(method, url, nil)
	c.Request.Header.Set("User-Agent", ua)
	return mobileRedirectTarget(c)
}

// /m 的登录/注册页没有 OAuth,页面上的「前往电脑端」出口必须真的能出去——
// 不放行就会被立刻弹回 /m,逃生门等于没有。
func TestDesktopPreferenceBypassesRedirect(t *testing.T) {
	if got := targetFor("GET", "/register?desktop=1&aff=mwg1", iphoneUA); got != "" {
		t.Fatalf("target = %q, want no redirect for ?desktop=1", got)
	}
	if got := targetFor("GET", "/login?desktop=1", iphoneUA); got != "" {
		t.Fatalf("target = %q, want no redirect for ?desktop=1", got)
	}
	// 没带开关时仍要跳,否则等于把移动端跳转整个关掉。
	if got := targetFor("GET", "/register?aff=mwg1", iphoneUA); got == "" {
		t.Fatal("target = \"\", want redirect when desktop flag absent")
	}
}

// 选择过桌面版后要记住,否则用户每点一个链接都被弹回移动端。
func TestDesktopPreferenceCookieBypassesRedirect(t *testing.T) {
	c := gin.CreateTestContextOnly(httptest.NewRecorder(), gin.New())
	c.Request = httptest.NewRequest("GET", "/register?aff=mwg1", nil)
	c.Request.Header.Set("User-Agent", iphoneUA)
	c.Request.AddCookie(&http.Cookie{Name: desktopPreferenceCookie, Value: "1"})

	if got := mobileRedirectTarget(c); got != "" {
		t.Fatalf("target = %q, want no redirect when preference cookie set", got)
	}
}

// cookie 一存 30 天且微信内置浏览器清不掉,没有撤销入口就等于把人锁死一个月。
func TestDesktopPreferenceCanBeRevoked(t *testing.T) {
	// ?desktop=0 必须当次就把用户送回移动端:删 cookie 只写响应头,请求头里旧的还在,
	// 只读 cookie 的话这一跳仍会落桌面版,用户得再刷一次。
	c := gin.CreateTestContextOnly(httptest.NewRecorder(), gin.New())
	c.Request = httptest.NewRequest("GET", "/login?desktop=0", nil)
	c.Request.Header.Set("User-Agent", iphoneUA)
	c.Request.AddCookie(&http.Cookie{Name: desktopPreferenceCookie, Value: "1"})

	if got := mobileRedirectTarget(c); got != "/m/login?desktop=0" {
		t.Fatalf("target = %q, want redirect to mobile despite the cookie", got)
	}

	// 同时要真的把 cookie 删掉,否则下一次导航又被粘回桌面版。
	rec := httptest.NewRecorder()
	c2 := gin.CreateTestContextOnly(rec, gin.New())
	c2.Request = httptest.NewRequest("GET", "/login?desktop=0", nil)
	rememberDesktopPreference(c2)

	var found *http.Cookie
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == desktopPreferenceCookie {
			found = ck
		}
	}
	if found == nil || found.MaxAge >= 0 || found.Value != "" {
		t.Fatalf("cookie = %#v, want an expiring %s", found, desktopPreferenceCookie)
	}
}

// 被 cookie 粘在桌面版的手机用户必须看得到出口,否则 30 天里毫无察觉。
func TestMobileSwitchBarHref(t *testing.T) {
	withCookie := func(method, url, ua string, cookie bool) string {
		c := gin.CreateTestContextOnly(httptest.NewRecorder(), gin.New())
		c.Request = httptest.NewRequest(method, url, nil)
		c.Request.Header.Set("User-Agent", ua)
		if cookie {
			c.Request.AddCookie(&http.Cookie{Name: desktopPreferenceCookie, Value: "1"})
		}
		return mobileSwitchBarHref(c)
	}

	// 有对应移动端页面就回对应页,没有的深链回移动端首页。
	if got := withCookie("GET", "/login", iphoneUA, true); got != "/m/login?desktop=0" {
		t.Fatalf("href = %q, want /m/login?desktop=0", got)
	}
	if got := withCookie("GET", "/console", iphoneUA, true); got != "/m/?desktop=0" {
		t.Fatalf("href = %q, want /m/?desktop=0", got)
	}
	// query 要带回去,理由同 mobileRedirectTarget:邀请码挂在 ?aff= 上,
	// 从 /register?aff=xxx 点回手机版丢了它,邀请关系就断在这一跳。
	if got := withCookie("GET", "/register?aff=mwg1", iphoneUA, true); got != "/m/register?aff=mwg1&desktop=0" {
		t.Fatalf("href = %q, want the aff kept", got)
	}
	// 用户多半是点「前往电脑端」过来的,地址上已经有个 desktop=1:必须覆盖掉,
	// 直接拼 &desktop=0 会留下两个 desktop 参数,取值就看谁先被读到。
	if got := withCookie("GET", "/register?aff=mwg1&desktop=1", iphoneUA, true); got != "/m/register?aff=mwg1&desktop=0" {
		t.Fatalf("href = %q, want desktop=1 overwritten, not appended", got)
	}
	// 桌面用户看不到;没选过桌面版的手机用户(主动开深链)也不该被浮条打扰。
	if got := withCookie("GET", "/login", desktopUA, true); got != "" {
		t.Fatalf("href = %q, want no bar for desktop UA", got)
	}
	if got := withCookie("GET", "/console", iphoneUA, false); got != "" {
		t.Fatalf("href = %q, want no bar when preference was never set", got)
	}
	// 已经在撤销的路上,不必再挂一个出口。
	if got := withCookie("GET", "/console?desktop=0", iphoneUA, true); got != "" {
		t.Fatalf("href = %q, want no bar while revoking", got)
	}
}

// 浮条让同一地址按 UA/cookie 有了两种产物,所以桌面版 HTML 要声明 Vary。但 gzip 中间件
// 跑在前面、已经写过 Vary: Accept-Encoding——用 c.Header(Set 语义)会把它整个覆盖掉,
// 中间层就可能把压缩过的 HTML 发给不收 gzip 的客户端。必须 Add。
func TestDesktopHTMLVaryKeepsAcceptEncoding(t *testing.T) {
	r := gin.New()
	SetWebRouter(r, ThemeAssets{DefaultIndexPage: []byte("<html><body></body></html>")})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/login", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("User-Agent", iphoneUA)
	r.ServeHTTP(rec, req)

	// 没压缩的话这条用例根本没走到会被覆盖的场景,等于白测。
	if enc := rec.Header().Get("Content-Encoding"); enc != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", enc)
	}
	vary := strings.Join(rec.Header().Values("Vary"), ", ")
	for _, want := range []string{"Accept-Encoding", "User-Agent", "Cookie"} {
		if !strings.Contains(vary, want) {
			t.Fatalf("Vary = %q, want it to keep %s", vary, want)
		}
	}
}

func TestWithMobileSwitchBar(t *testing.T) {
	page := []byte("<html><body><div id=\"root\"></div></body></html>")

	got := string(withMobileSwitchBar(page, "/m/login?desktop=0"))
	if !strings.Contains(got, `href="/m/login?desktop=0"`) {
		t.Fatalf("page = %q, want the bar injected", got)
	}
	// 必须落在 </body> 之前,注在标签之后浏览器会把它挪出 body 或直接丢弃。
	if strings.Index(got, "返回手机版") > strings.Index(got, "</body>") {
		t.Fatalf("page = %q, want the bar before </body>", got)
	}
	// 不需要出口时原样返回,桌面用户拿到的字节不能有任何变化。
	if got := withMobileSwitchBar(page, ""); string(got) != string(page) {
		t.Fatalf("page = %q, want it untouched", got)
	}
}

// ?desktop=1 要写进 cookie,否则偏好只对当次请求生效。
func TestRememberDesktopPreferenceSetsCookie(t *testing.T) {
	rec := httptest.NewRecorder()
	c := gin.CreateTestContextOnly(rec, gin.New())
	c.Request = httptest.NewRequest("GET", "/register?desktop=1", nil)
	rememberDesktopPreference(c)

	cookies := rec.Result().Cookies()
	var found *http.Cookie
	for _, ck := range cookies {
		if ck.Name == desktopPreferenceCookie {
			found = ck
		}
	}
	if found == nil || found.Value != "1" {
		t.Fatalf("cookies = %#v, want %s=1", cookies, desktopPreferenceCookie)
	}
	// 偏好不该比登录态活得久:误点一次「前往电脑端」的代价必须是小时级,不是按周算。
	if found.MaxAge <= 0 || found.MaxAge > common.SessionMaxAgeSeconds {
		t.Fatalf(
			"MaxAge = %d, want a positive value no longer than the login session (%ds)",
			found.MaxAge, common.SessionMaxAgeSeconds,
		)
	}

	// 没有开关时不该乱写 cookie。
	rec2 := httptest.NewRecorder()
	c2 := gin.CreateTestContextOnly(rec2, gin.New())
	c2.Request = httptest.NewRequest("GET", "/register?aff=mwg1", nil)
	rememberDesktopPreference(c2)
	if len(rec2.Result().Cookies()) != 0 {
		t.Fatalf("cookies = %#v, want none", rec2.Result().Cookies())
	}
}

func TestMobileRedirectPreservesQuery(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		// 邀请链接是这条改动的动机:query 丢了邀请关系就记不上。
		{name: "register deep link keeps aff", url: "/register?aff=mwg1", want: "/m/register?aff=mwg1"},
		{name: "login deep link keeps aff", url: "/login?aff=mwg1", want: "/m/login?aff=mwg1"},
		{name: "root keeps aff", url: "/?aff=mwg1", want: "/m/?aff=mwg1"},
		{name: "root without query", url: "/", want: "/m/"},
		{name: "multiple params kept verbatim", url: "/register?aff=a&x=1", want: "/m/register?aff=a&x=1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := targetFor("GET", tt.url, iphoneUA); got != tt.want {
				t.Fatalf("target = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMobileRedirectSkipped(t *testing.T) {
	tests := []struct {
		name   string
		method string
		url    string
		ua     string
	}{
		{name: "desktop UA never redirects", method: "GET", url: "/register?aff=mwg1", ua: desktopUA},
		// /m 没有找回密码页,跳过去只会白页,必须继续落桌面版。
		{name: "unlisted path falls through", method: "GET", url: "/reset?aff=mwg1", ua: iphoneUA},
		{name: "console deep link falls through", method: "GET", url: "/console", ua: iphoneUA},
		{name: "non-GET falls through", method: "POST", url: "/register", ua: iphoneUA},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := targetFor(tt.method, tt.url, tt.ua); got != "" {
				t.Fatalf("target = %q, want no redirect", got)
			}
		})
	}
}

// 跳转目标必须是 /m 下真实存在的路由,否则用户会落进 SPA fallback。
func TestMobileRedirectTargetsAreMobileRoutes(t *testing.T) {
	for path, target := range mobileRedirectPaths {
		if len(target) < 3 || target[:3] != "/m/" {
			t.Fatalf("path %q → %q, want a /m/ target", path, target)
		}
	}
}
