package router

import (
	"bytes"
	"html"
	"io/fs"
	"net/http"
	"regexp"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"

	"github.com/gin-gonic/gin"
)

// 手机 UA 特征。iPad 归桌面（屏幕足够跑完整控制台）。
var mobileUARegex = regexp.MustCompile(`(?i)iphone|android.+mobile|windows phone`)

// mobileRedirectPaths 手机 UA 访问这些桌面路径时，跳到移动端的对应页。
//
// 只列 web/mobile 确实存在的路由（见 web/mobile/src/router.jsx），否则会把用户送进
// SPA fallback 变成白页。找回密码等 /m 没有的页面刻意不列，让它继续落桌面版。
//
// 登录/注册必须在册：邀请链接是 /register?aff=xxx 这种深链，只认根路径的话手机用户
// 点进来就落桌面版，aff 也就走不到 /m 的注册页。
var mobileRedirectPaths = map[string]string{
	"/":         "/m/",
	"/login":    "/m/login",
	"/register": "/m/register",
}

const (
	// desktopPreferenceParam 桌面版偏好开关：?desktop=1 要桌面版，?desktop=0 撤销。
	desktopPreferenceParam = "desktop"
	// desktopPreferenceCookie 记住这个选择，避免用户每次导航都被弹回移动端。
	desktopPreferenceCookie = "prefer_desktop"
	// desktopPreferenceMaxAge cookie 有效期，与登录会话取同一个常量（见
	// common.SessionMaxAgeSeconds）——偏好不该比登录态活得久。
	//
	// 不用会话级 cookie：各家内置浏览器对「会话结束」的定义差别很大，微信的 WebView
	// 常驻进程里 session cookie 可能跟着活很久，也可能每开一次链接就重置——前者等于没缩短，
	// 后者会在 OAuth 跳第三方再跳回来的中途把偏好丢掉，用户被弹回 /m，登录做到一半断掉。
	// 定死一个确定的上限，语义也直白：这段登录会话内用桌面版。
	desktopPreferenceMaxAge = common.SessionMaxAgeSeconds
)

// rememberDesktopPreference 按 ?desktop= 记录或撤销「要桌面版」的选择。
//
// desktop=1 种 cookie：/m 的登录/注册页没有 OAuth（GitHub / LinuxDO 等），页面上给了
// 「前往电脑端」的出口。但那个出口指向 /login、/register——正是本文件会拦截跳转的路径，
// 不放行就会被立刻弹回 /m，逃生门等于没有。所以必须有一个显式绕过。
//
// desktop=0 删 cookie：反向的逃生门，同样不可缺。cookie 一存 30 天，而微信这类内置
// 浏览器的 cookie 用户自己清不掉——只要误点过一次「前往电脑端」，或者有人把带
// desktop=1 的地址转发出去，收到的人就会被锁在桌面版一个月且毫无察觉。
func rememberDesktopPreference(c *gin.Context) {
	switch c.Query(desktopPreferenceParam) {
	case "1":
		c.SetSameSite(http.SameSiteLaxMode)
		c.SetCookie(desktopPreferenceCookie, "1", desktopPreferenceMaxAge, "/", "", false, false)
	case "0":
		c.SetSameSite(http.SameSiteLaxMode)
		c.SetCookie(desktopPreferenceCookie, "", -1, "/", "", false, false)
	}
}

// prefersDesktop 用户是否已选择桌面版：本次请求带 ?desktop=1，或此前记过 cookie。
//
// desktop=0 必须当次就生效：删 cookie 只写进响应头，本次请求的请求头里旧 cookie 还在，
// 只读 cookie 的话这一跳仍会落桌面版，用户得再手动刷一次才回得去移动端。
func prefersDesktop(c *gin.Context) bool {
	switch c.Query(desktopPreferenceParam) {
	case "1":
		return true
	case "0":
		return false
	}
	v, err := c.Cookie(desktopPreferenceCookie)
	return err == nil && v == "1"
}

// mobileRedirectTarget 返回该请求应跳转的移动端地址；不需要跳转时返回 ""。
// query 必须原样带上——邀请码就挂在 ?aff= 上，丢了邀请关系就记不上。
func mobileRedirectTarget(c *gin.Context) string {
	if c.Request.Method != http.MethodGet {
		return ""
	}
	if !mobileUARegex.MatchString(c.Request.UserAgent()) {
		return ""
	}
	if prefersDesktop(c) {
		return ""
	}
	target, ok := mobileRedirectPaths[c.Request.URL.Path]
	if !ok {
		return ""
	}
	if raw := c.Request.URL.RawQuery; raw != "" {
		target += "?" + raw
	}
	return target
}

// mobileSwitchBarHref 手机 UA 却停在桌面版时，回移动端的地址；不需要时返回 ""。
//
// 只在用户「显式选过桌面版」时给。手机上主动打开 /console 这类 /m 没有的深链是正常
// 用法，不该被浮条打扰；而选过桌面版的那批人正是被 30 天 cookie 粘住、且没有出口的人。
func mobileSwitchBarHref(c *gin.Context) string {
	if c.Request.Method != http.MethodGet {
		return ""
	}
	if !mobileUARegex.MatchString(c.Request.UserAgent()) {
		return ""
	}
	if !prefersDesktop(c) {
		return ""
	}
	// 有对应移动端页面就回对应页，否则回移动端首页。
	target, ok := mobileRedirectPaths[c.Request.URL.Path]
	if !ok {
		target = "/m/"
	}
	// query 要带上，理由同 mobileRedirectTarget：邀请码挂在 ?aff= 上，从
	// /register?aff=xxx 点回手机版要是把它丢了，邀请关系就断在这一跳。
	// 用 Set 覆盖而不是直接拼 &desktop=0：用户多半是点「前往电脑端」过来的，
	// 地址上已经有个 desktop=1，拼接会留下两个 desktop 参数，取值就看谁先被读到。
	q := c.Request.URL.Query()
	q.Set(desktopPreferenceParam, "0")
	return target + "?" + q.Encode()
}

var bodyCloseTag = []byte("</body>")

// withMobileSwitchBar 往桌面版 index.html 里注入「返回手机版」浮条。
//
// 做在服务端而不是各前端里：判定依据（UA 与 prefer_desktop cookie）本来就只有服务端
// 拿得到，且 default / classic 两套主题一次覆盖，不必在两个框架里各写一遍。样式全部
// 内联，不引前端资源，也就不会被主题的样式重置影响。
//
// href 由本文件的常量表拼出，不含用户输入；仍走一次转义，避免日后改成可配置时漏掉。
func withMobileSwitchBar(page []byte, href string) []byte {
	if href == "" {
		return page
	}
	bar := []byte(`<a href="` + html.EscapeString(href) + `" ` +
		`style="position:fixed;left:12px;bottom:calc(12px + env(safe-area-inset-bottom,0px));` +
		`z-index:2147483647;display:inline-flex;align-items:center;gap:4px;padding:8px 14px;` +
		`border-radius:999px;background:rgba(17,17,17,.88);color:#fff;text-decoration:none;` +
		`font-size:13px;line-height:1;box-shadow:0 4px 12px rgba(0,0,0,.18);` +
		`font-family:-apple-system,BlinkMacSystemFont,'PingFang SC','Microsoft YaHei',sans-serif">` +
		`返回手机版</a>`)
	if !bytes.Contains(page, bodyCloseTag) {
		return append(page, bar...)
	}
	return bytes.Replace(page, bodyCloseTag, append(bar, bodyCloseTag...), 1)
}

// SetMobileRouter 挂载移动端 H5 静态应用 /m/*。
//
// 与画布一致，必须在 FRONTEND_BASE_URL 判断之前调用：即使部署了外置前端，
// 移动端 H5 也永远由 Go 单二进制内置伺服。
// 静态资源公开访问（登录在应用内完成），未知深链回落到 index.html（SPA 语义）。
func SetMobileRouter(router *gin.Engine, assets ThemeAssets) {
	// 手机 UA 访问 mobileRedirectPaths 里的桌面路径时跳转移动端，并原样保留 query。
	// 其余深链（/console 等）不拦截，手机上仍可显式使用桌面版。
	// 注：作为全局中间件注册，先于 SetWebRouter 的 static.Serve 生效。
	router.Use(func(c *gin.Context) {
		rememberDesktopPreference(c)
		if target := mobileRedirectTarget(c); target != "" {
			// 同一地址对不同 UA / cookie 给出不同结果，必须禁掉缓存并声明 Vary：
			// 302 走在 middleware.Cache() 之前，不显式声明的话，中间的反向代理或
			// 浏览器可能把这一跳缓存下来，桌面用户也会被带去 /m。
			//
			// 这里 Abort 得早，gzip 中间件（注册在 SetWebRouter 里）还没跑过，Vary 是空的，
			// Set 也不会覆盖谁；仍用 Add 与 web-router 保持一致，免得日后中间件顺序一变就出坑。
			c.Header("Cache-Control", "no-store")
			c.Writer.Header().Add("Vary", "User-Agent, Cookie")
			c.Redirect(http.StatusFound, target)
			c.Abort()
			return
		}
		c.Next()
	})

	mobileFS, err := fs.Sub(assets.MobileBuildFS, "web/mobile/dist")
	if err != nil {
		panic(err)
	}
	httpFS := http.FS(mobileFS)
	fileServer := http.StripPrefix("/m", http.FileServer(httpFS))

	handler := func(c *gin.Context) {
		c.Set(middleware.RouteTagKey, "web")
		p := strings.TrimPrefix(c.Request.URL.Path, "/m")
		if p == "/manifest.webmanifest" {
			manifest, err := MobileWebManifest()
			if err != nil {
				c.Status(http.StatusInternalServerError)
				return
			}
			c.Header("Cache-Control", "no-cache")
			c.Data(http.StatusOK, "application/manifest+json; charset=utf-8", manifest)
			return
		}
		// index 与 SPA fallback 都走内存字节并做品牌替换，
		// 避免首屏闪现构建期的默认标题/图标
		if p == "/" || p == "/index.html" || !canvasFileExists(httpFS, p) {
			c.Header("Cache-Control", "no-cache")
			c.Data(http.StatusOK, "text/html; charset=utf-8", BrandIndexHTML(assets.MobileIndexPage))
			return
		}
		if strings.HasPrefix(p, "/assets/") {
			// Vite 产物文件名带内容 hash，可长缓存
			c.Header("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			c.Header("Cache-Control", "no-cache")
		}
		fileServer.ServeHTTP(c.Writer, c.Request)
	}

	// 无斜杠的 /m 显式重定向，避免落入 SPA NoRoute fallback。同样要保留 query。
	redirectRoot := func(c *gin.Context) {
		c.Set(middleware.RouteTagKey, "web")
		target := "/m/"
		if raw := c.Request.URL.RawQuery; raw != "" {
			target += "?" + raw
		}
		c.Redirect(http.StatusMovedPermanently, target)
	}
	router.GET("/m", redirectRoot)
	router.HEAD("/m", redirectRoot)

	mobileGroup := router.Group("/m")
	mobileGroup.GET("/*filepath", handler)
	mobileGroup.HEAD("/*filepath", handler)

	// 体验区预置音色与示例素材只打包在 classic 前端里（/audio-presets、
	// /playground-samples 为根绝对路径引用）。显式路由从 classic 产物伺服，
	// 保证移动端及 theme=default 下均可用。
	classicFS, err := fs.Sub(assets.ClassicBuildFS, "web/classic/dist")
	if err != nil {
		panic(err)
	}
	classicFileServer := http.FileServer(http.FS(classicFS))
	serveClassicAsset := func(c *gin.Context) {
		c.Set(middleware.RouteTagKey, "web")
		classicFileServer.ServeHTTP(c.Writer, c.Request)
	}
	router.GET("/audio-presets/*filepath", serveClassicAsset)
	router.HEAD("/audio-presets/*filepath", serveClassicAsset)
	router.GET("/playground-samples/*filepath", serveClassicAsset)
	router.HEAD("/playground-samples/*filepath", serveClassicAsset)
}
