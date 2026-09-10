package relay

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 设置页那条「产物审核的覆盖范围」说明是**手写**的，而它描述的是编译期事实：
// 哪些渠道的同步生图挂了审核，取决于代码挂在哪。两者一旦分叉，管理员会照着
// 一份不实的说明做判断——而且分叉不会有任何报错。
//
// 这条测试把「挂载点集合」钉死。新挂或摘掉一个渠道 → 红，提示同步改前端文案。
func TestOutputModerationMountsMatchDocumentedCoverage(t *testing.T) {
	// 当前有产物审核挂载点的同步生图渠道。改这个集合＝改用户可见的覆盖范围。
	mounted := map[string]bool{
		"gpustackplus": true,
	}

	// 所有自己构造 dto.ImageResponse 并写响应的渠道目录——即"有能力挂载"的全集。
	// 用 dto.ImageResponse 当判据而不是手写清单：新增渠道会自动进入这个集合。
	candidates := map[string]bool{}
	root := filepath.Join("channel")
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") {
			return err
		}
		b, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		src := string(b)
		if !strings.Contains(src, "dto.ImageResponse") {
			return nil
		}
		// channel/<name>/... → name
		rel, _ := filepath.Rel(root, path)
		candidates[strings.Split(rel, string(filepath.Separator))[0]] = true
		return nil
	})
	if err != nil {
		t.Fatalf("扫描渠道目录失败: %v", err)
	}
	if len(candidates) == 0 {
		t.Fatal("一个候选渠道都没扫到，这条测试的立论要重写")
	}

	for name := range candidates {
		b, err := os.ReadFile(filepath.Join(root, name, "adaptor.go"))
		hasMount := err == nil && strings.Contains(string(b), "moderation.ModerateImageOutput(")
		if !hasMount {
			// 也可能挂在 image.go 之类的文件里
			hasMount = dirContains(t, filepath.Join(root, name), "moderation.ModerateImageOutput(")
		}
		if hasMount && !mounted[name] {
			t.Errorf("渠道 %s 新挂了产物审核，但覆盖范围说明里还写着「仅自建渠道」。\n"+
				"请同步更新 web/classic/src/pages/Setting/Operation/SettingsModeration.jsx "+
				"的覆盖范围 Banner，并把 %s 加进本测试的 mounted 集合。", name, name)
		}
		if !hasMount && mounted[name] {
			t.Errorf("渠道 %s 的产物审核挂载点没了，而覆盖范围说明还声称覆盖它——"+
				"管理员会以为这条路在审，实际一张都没审。", name)
		}
	}
}

func dirContains(t *testing.T, dir, needle string) bool {
	t.Helper()
	found := false
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") {
			return err
		}
		b, readErr := os.ReadFile(path)
		if readErr == nil && strings.Contains(string(b), needle) {
			found = true
		}
		return nil
	})
	return found
}
