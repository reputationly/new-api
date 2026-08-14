package router

import (
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/QuantumNous/new-api/middleware"

	"github.com/gin-gonic/gin"
)

// SetCanvasRouter 挂载内置画布静态应用 /canvas-app/*。
//
// 必须在 FRONTEND_BASE_URL 判断之前调用:即使部署了外置前端,画布也永远由
// Go 单二进制内置伺服,不得重定向到外置前端。
// 使用显式 catch-all 路由(而非引擎级中间件),保证 CanvasStaticAuth 先于
// 静态伺服执行,且不受 gin trailing-slash 内部重定向影响。
func SetCanvasRouter(router *gin.Engine, assets ThemeAssets) {
	canvasFS, err := fs.Sub(assets.CanvasBuildFS, "web/canvas/dist")
	if err != nil {
		panic(err)
	}
	httpFS := http.FS(canvasFS)
	fileServer := http.StripPrefix("/canvas-app", http.FileServer(httpFS))

	// 画布是 Vite 单页应用(react-router createBrowserRouter),路由在客户端。
	// /canvas-app/canvas/<id> 这类深链在磁盘上没有对应文件,必须回落到 index.html
	// 交给前端路由,否则用户在项目页刷新就 404。
	indexPage, err := assets.CanvasBuildFS.ReadFile("web/canvas/dist/index.html")
	if err != nil {
		panic(err)
	}

	handler := func(c *gin.Context) {
		c.Set(middleware.RouteTagKey, "web")
		if !canvasFileExists(httpFS, strings.TrimPrefix(c.Request.URL.Path, "/canvas-app")) {
			// 只有真实静态资源(带扩展名的 js/css/图片等)缺失才算 404;其余一律当作
			// 前端路由。否则拼错的资源路径会返回一份 HTML,浏览器把 HTML 当 JS 解析,
			// 报错信息会非常难查。
			if path.Ext(c.Request.URL.Path) != "" {
				c.Status(http.StatusNotFound)
				return
			}
			c.Data(http.StatusOK, "text/html; charset=utf-8", indexPage)
			return
		}
		fileServer.ServeHTTP(c.Writer, c.Request)
	}

	// 无斜杠的 /canvas-app 显式重定向,避免落入 SPA NoRoute fallback
	redirectRoot := func(c *gin.Context) {
		c.Set(middleware.RouteTagKey, "web")
		c.Redirect(http.StatusMovedPermanently, "/canvas-app/")
	}
	router.GET("/canvas-app", redirectRoot)
	router.HEAD("/canvas-app", redirectRoot)

	canvasGroup := router.Group("/canvas-app")
	canvasGroup.Use(middleware.CanvasStaticAuth())
	canvasGroup.GET("/*filepath", handler)
	canvasGroup.HEAD("/*filepath", handler)
}

// canvasFileExists 判断路径是否可服务:普通文件直接可服务;
// 目录必须包含 index.html(trailingSlash 导出布局),否则视为不存在,
// 同时避免 http.FileServer 渲染目录列表。
func canvasFileExists(httpFS http.FileSystem, path string) bool {
	// http.FS 不接受带尾斜杠的路径(fs.ValidPath),统一去掉再查
	name := strings.TrimSuffix(path, "/")
	if name == "" {
		name = "/"
	}
	f, err := httpFS.Open(name)
	if err != nil {
		return false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return false
	}
	if !info.IsDir() {
		return true
	}
	index, err := httpFS.Open(strings.TrimSuffix(name, "/") + "/index.html")
	if err != nil {
		return false
	}
	_ = index.Close()
	return true
}
