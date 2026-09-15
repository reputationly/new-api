package service

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// **README 的表格必须覆盖每一个被 go:embed 的文件。**
//
// 这个目录存在的唯一理由就是「能和上游 diff」——出处记漏了，下次同步时
// 就不知道该拿哪个提交去比，而那正是当初照着已废弃的 render_h3_prompt
// 移植 3000 行的原因。
//
// 文档类改动测试挡不住，所以这条做成机械检查：加了 embed 却没往 README
// 记一行，这里会红。本次改动里就漏过一次（补了四个 skill 文件，README
// 只写着两个）。
func TestEmbeddedFilesAreDocumented(t *testing.T) {
	src, err := os.ReadFile("aggregate_enhance_singlecall.go")
	if err != nil {
		t.Fatal(err)
	}
	readme, err := os.ReadFile("h3v20/README.md")
	if err != nil {
		t.Fatal(err)
	}

	embeds := regexp.MustCompile(`//go:embed\s+(h3v20/\S+)`).FindAllStringSubmatch(string(src), -1)
	if len(embeds) < 6 {
		t.Fatalf("只找到 %d 处 go:embed，正则可能失配了", len(embeds))
	}
	for _, m := range embeds {
		rel := strings.TrimPrefix(m[1], "h3v20/")
		// 文件得真的在
		if _, err := os.Stat(filepath.Join("h3v20", rel)); err != nil {
			t.Errorf("embed 的文件不存在: %s", rel)
			continue
		}
		// README 里得有它的出处
		if !strings.Contains(string(readme), "`"+rel+"`") {
			t.Errorf("README 没记 %s 的上游出处 —— 下次同步时不知道拿哪个提交去比", rel)
		}
	}
}
