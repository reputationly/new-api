package router

import (
	"io/fs"
	"net/http"
	"regexp"
	"strings"

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
	// desktopPreferenceParam 用户显式要求桌面版的开关：?desktop=1。
	desktopPreferenceParam = "desktop"
	// desktopPreferenceCookie 记住这个选择，避免用户每次导航都被弹回移动端。
	desktopPreferenceCookie = "prefer_desktop"
	// desktopPreferenceMaxAge cookie 有效期 30 天。
	desktopPreferenceMaxAge = 30 * 24 * 3600
)

// rememberDesktopPreference 看到 ?desktop=1 就把「要桌面版」记进 cookie。
//
// /m 的登录/注册页没有 OAuth（GitHub / LinuxDO 等），页面上给了「前往电脑端」的出口。
// 但那个出口指向 /login、/register——正是本文件会拦截跳转的路径，不放行就会被立刻
// 弹回 /m，逃生门等于没有。所以必须有一个显式绕过。
func rememberDesktopPreference(c *gin.Context) {
	if c.Query(desktopPreferenceParam) != "1" {
		return
	}
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(desktopPreferenceCookie, "1", desktopPreferenceMaxAge, "/", "", false, false)
}

// prefersDesktop 用户是否已选择桌面版：本次请求带 ?desktop=1，或此前记过 cookie。
func prefersDesktop(c *gin.Context) bool {
	if c.Query(desktopPreferenceParam) == "1" {
		return true
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
