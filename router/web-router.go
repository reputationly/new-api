package router

import (
	"embed"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/gin-contrib/gzip"
	"github.com/gin-contrib/static"
	"github.com/gin-gonic/gin"
)

// ThemeAssets holds the embedded frontend assets for both themes.
type ThemeAssets struct {
	DefaultBuildFS   embed.FS
	DefaultIndexPage []byte
	ClassicBuildFS   embed.FS
	ClassicIndexPage []byte
	CanvasBuildFS    embed.FS
	MobileBuildFS    embed.FS
	MobileIndexPage  []byte
}

func SetWebRouter(router *gin.Engine, assets ThemeAssets) {
	defaultFS := common.EmbedFolder(assets.DefaultBuildFS, "web/default/dist")
	classicFS := common.EmbedFolder(assets.ClassicBuildFS, "web/classic/dist")
	themeFS := common.NewThemeAwareFS(defaultFS, classicFS)

	router.Use(gzip.Gzip(gzip.DefaultCompression))
	router.Use(middleware.GlobalWebRateLimit())
	router.Use(middleware.Cache())
	router.Use(static.Serve("/", themeFS))
	router.NoRoute(func(c *gin.Context) {
		c.Set(middleware.RouteTagKey, "web")
		if strings.HasPrefix(c.Request.RequestURI, "/v1") || strings.HasPrefix(c.Request.RequestURI, "/api") || strings.HasPrefix(c.Request.RequestURI, "/pg") || strings.HasPrefix(c.Request.RequestURI, "/assets") {
			controller.RelayNotFound(c)
			return
		}
		// no-store 而不是 no-cache：理由同 mobile-router 的 index 分支——no-cache 允许
		// 存下来（只要求用前回源校验），而微信 X5 这类内核并不老实。这份 HTML 写死了带
		// 内容 hash 的 chunk 名，拿旧的就会 404 到白屏，用户只能清缓存自救。
		c.Header("Cache-Control", "no-store")
		// 下面的浮条按 UA 与 prefer_desktop cookie 决定是否注入，同一地址会有两种产物。
		// 必须 Add 不能 Set：gzip 中间件跑在前面，已经写过 Vary: Accept-Encoding，
		// 用 c.Header 会把它整个覆盖掉，中间层就可能把压缩过的 HTML 发给不收 gzip 的客户端。
		c.Writer.Header().Add("Vary", "User-Agent, Cookie")
		page := assets.DefaultIndexPage
		if common.GetTheme() == "classic" {
			page = assets.ClassicIndexPage
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8",
			withMobileSwitchBar(BrandIndexHTML(page), mobileSwitchBarHref(c)))
	})
}
