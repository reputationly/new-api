package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

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
