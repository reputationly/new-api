package relay

import (
	"os"
	"strings"
	"testing"
)

// 本包的源码级断言助手。用于「某次调用是否接进了某个分支」这类没法从行为侧
// 观测的接线检查——分支走没走到，行为上都看不出差别。

func readRelaySource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", name, err)
	}
	return string(b)
}

// sliceBetween 取 start 之后、end 之前的那一段。
// 两个锚点缺任何一个都直接 t.Fatal —— 悄悄退化成「整个文件」会让断言恒真，
// 那正是这类测试最容易变成假测试的地方。
func sliceBetween(t *testing.T, src, start, end string) string {
	t.Helper()
	i := strings.Index(src, start)
	if i < 0 {
		t.Fatalf("找不到起始锚点 %q，这条测试的立论要重写", start)
	}
	rest := src[i:]
	j := strings.Index(rest, end)
	if j < 0 {
		t.Fatalf("找不到结束锚点 %q，这条测试的立论要重写", end)
	}
	return rest[:j]
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
