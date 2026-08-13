package common

import (
	"embed"
	"io/fs"
	"net/http"
	"os"

	"github.com/gin-contrib/static"
)

// Credit: https://github.com/gin-contrib/static/issues/19

type embedFileSystem struct {
	http.FileSystem
}

func (e *embedFileSystem) Exists(prefix string, path string) bool {
	_, err := e.Open(path)
	if err != nil {
		return false
	}
	return true
}

func (e *embedFileSystem) Open(name string) (http.File, error) {
	// 藏起入口 HTML，逼它落到 NoRoute handler —— 那里才会做品牌替换与统计代码注入。
	//
	// **"/index.html" 与 "/" 必须一起藏**：dist 里 index.html 是真实存在的文件，只藏 "/"
	// 的话直接访问 /index.html 会被 static.Serve 原样发出去，绕开 NoRoute，于是
	//   1. 拿到的是构建期默认品牌（运营配的站点名/logo 全不生效）；
	//   2. 缓存头是 middleware.Cache() 给的 max-age=604800 —— 整整一周的强缓存，而这份
	//      HTML 里写死了带内容 hash 的 chunk 名。新部署会删掉上一版的 chunk 文件，
	//      拿着一周前 HTML 的客户端请求过去就是 404 → 白屏，只能靠清缓存自救。
	// 手机端一直是对的（mobile-router.go 显式判了 p == "/" || p == "/index.html"），
	// 桌面端这条一直漏着。
	if name == "/" || name == "/index.html" {
		return nil, os.ErrNotExist
	}
	return e.FileSystem.Open(name)
}

func EmbedFolder(fsEmbed embed.FS, targetPath string) static.ServeFileSystem {
	efs, err := fs.Sub(fsEmbed, targetPath)
	if err != nil {
		panic(err)
	}
	return &embedFileSystem{
		FileSystem: http.FS(efs),
	}
}

// themeAwareFileSystem delegates to the appropriate embedded FS based on
// the current theme (via GetTheme). This enables runtime theme switching
// without restarting the server.
type themeAwareFileSystem struct {
	defaultFS static.ServeFileSystem
	classicFS static.ServeFileSystem
}

func (t *themeAwareFileSystem) Exists(prefix string, path string) bool {
	if GetTheme() == "classic" {
		return t.classicFS.Exists(prefix, path)
	}
	return t.defaultFS.Exists(prefix, path)
}

func (t *themeAwareFileSystem) Open(name string) (http.File, error) {
	if GetTheme() == "classic" {
		return t.classicFS.Open(name)
	}
	return t.defaultFS.Open(name)
}

func NewThemeAwareFS(defaultFS, classicFS static.ServeFileSystem) static.ServeFileSystem {
	return &themeAwareFileSystem{defaultFS: defaultFS, classicFS: classicFS}
}
