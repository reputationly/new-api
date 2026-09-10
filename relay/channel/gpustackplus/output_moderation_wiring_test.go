package gpustackplus

import (
	"os"
	"strings"
	"testing"
)

// D-2 挂载点的接线检查。
//
// 「审核在写响应之前」这件事没法从行为侧观测——一旦顺序反了，
// IOCopyBytesGracefully 已经把字节送上线，再判违规也收不回来，
// 而单测里看到的仍然是「审核跑了、返回了 Blocked」，一切正常。
// 所以直接对源码断言两者的先后。
func TestOutputModerationRunsBeforeResponseWrite(t *testing.T) {
	b, err := os.ReadFile("adaptor.go")
	if err != nil {
		t.Fatalf("读取 adaptor.go 失败: %v", err)
	}
	src := string(b)

	modIdx := strings.Index(src, "moderation.ModerateImageOutput(")
	if modIdx < 0 {
		t.Fatal("DoResponse 里没有调用 moderation.ModerateImageOutput——同步生图产物完全没送审")
	}
	// 图片响应的那次写。DoResponse 里只有这一处把 OpenAI 图片响应写给客户端。
	writeIdx := strings.Index(src, "service.IOCopyBytesGracefully(c, resp, jsonBytes)")
	if writeIdx < 0 {
		t.Fatal("找不到图片响应的写入点，这条测试的立论要重写")
	}
	if modIdx > writeIdx {
		t.Fatal("产物审核排在写响应之后——字节已经在线上了，拦截不可能生效")
	}

	// 拦截时必须打计费标记，否则 controller/relay.go 的 defer 会把预扣退掉，
	// 「不退款、正常计费」这个决定被框架默认行为悄悄推翻。
	if !strings.Contains(src, "MarkOutputModerationBlocked(c)") {
		t.Fatal("拦截分支没有调用 MarkOutputModerationBlocked——预扣费会被自动退还")
	}
}
