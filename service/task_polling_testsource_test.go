package service

import (
	"os"
	"testing"
)

// readSourceFile 读取本包的源文件。
// 用于「某个调用是否接进了某个循环」这类没法从行为侧观测的接线检查。
func readSourceFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", name, err)
	}
	return string(b)
}
