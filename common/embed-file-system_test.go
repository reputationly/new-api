package common

import (
	"embed"
	"testing"
)

// testdata 里必须真有一份 index.html —— 否则 Open("/index.html") 本来就报错，
// 这个测试无论有没有修复都会通过，等于什么都没测。
//
//go:embed testdata
var testEmbedFS embed.FS

// 入口 HTML 必须"不存在"，才能落到 NoRoute handler 去做品牌替换与 no-store。
// "/" 一直是对的，"/index.html" 曾经漏掉：dist 里它是真实文件，static.Serve 会原样
// 发出去，绕开 NoRoute —— 结果是构建期默认品牌 + middleware.Cache() 的一周强缓存，
// 而那份 HTML 写死了带 hash 的 chunk 名，新部署后必然 404 白屏。
func TestEmbedFileSystemHidesIndexHTML(t *testing.T) {
	fs := EmbedFolder(testEmbedFS, "testdata")

	// 前置断言：确认这份 FS 里 index.html 确实存在，否则下面的断言是空转。
	if _, err := testEmbedFS.Open("testdata/index.html"); err != nil {
		t.Fatalf("testdata/index.html 应当存在，否则本测试无效: %v", err)
	}

	for _, name := range []string{"/", "/index.html"} {
		if _, err := fs.Open(name); err == nil {
			t.Errorf("Open(%q) = nil error, want ErrNotExist so it falls through to NoRoute", name)
		}
		if fs.Exists("/", name) {
			t.Errorf("Exists(%q) = true, want false so static.Serve passes it through", name)
		}
	}

	// 其余文件照常伺服，别把整个静态目录一起藏了。
	if !fs.Exists("/", "/app.css") {
		t.Error("Exists(/app.css) = false, want true")
	}
}
