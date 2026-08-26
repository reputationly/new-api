package controller

import (
	"bytes"
	"strings"
	"testing"
)

// 站外分享只允许下载：落地页不得出现任何内联播放/预览元素，下载入口也不能再挂
// ?download=1 这种「删掉参数就变在线播放」的开关。
func TestSharePageIsDownloadOnly(t *testing.T) {
	cases := []struct {
		name     string
		inWeChat bool
	}{
		{name: "普通浏览器", inWeChat: false},
		{name: "微信内置浏览器", inWeChat: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := sharePageTmpl.Execute(&buf, sharePageData{
				Brand:    "Demo",
				Kind:     "视频",
				Token:    "tok123",
				InWeChat: tc.inWeChat,
			})
			if err != nil {
				t.Fatalf("render share page: %v", err)
			}
			html := buf.String()

			for _, tag := range []string{"<video", "<audio", "<img", "<iframe"} {
				if strings.Contains(html, tag) {
					t.Errorf("落地页出现内联媒体元素 %s：站外只允许下载，不允许在线浏览", tag)
				}
			}
			if !strings.Contains(html, `href="/s/tok123/content"`) {
				t.Errorf("落地页缺少下载入口 /s/<token>/content，实际内容：%s", html)
			}
			if strings.Contains(html, "download=1") {
				t.Errorf("下载入口不该再依赖 ?download=1：该参数已去掉，内容端点无条件按附件下发")
			}
			if tc.inWeChat && !strings.Contains(html, "在浏览器打开") {
				t.Errorf("微信内应引导「在浏览器打开」后再下载，实际内容：%s", html)
			}
		})
	}
}

func TestMediaKindLabel(t *testing.T) {
	cases := map[string]string{
		"a/b/c.mp4":     "视频",
		"a/b/c.mp3":     "音频",
		"a/b/c.png":     "图片",
		"a/b/c.unknown": "文件",
	}
	for key, want := range cases {
		if got := mediaKindLabel(key); got != want {
			t.Errorf("mediaKindLabel(%q) = %q, want %q", key, got, want)
		}
	}
}
