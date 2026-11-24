package model

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 守卫测试（docs/points-expiry-design.md §十四、AC20）。
//
// 积分余额的每一次写入都必须经过批次机制，否则余额与批次脱节：旧值偏高会在下次
// sync 表现为 surplus（用户凭空多分），旧值偏低会被归属成一笔消耗（用户少分）。
// 这类 bug 用 grep 式的代码审查最容易漏，所以固化成测试：新增一处写入就必须来改
// 白名单，改的时候自然会看到这里的理由。
//
// 扫描范围是仓库里的非测试 .go 文件（跳过 web/ 前端与依赖目录）。

// 允许直接按列名写 points_balance 的文件。
var pointsBalanceColumnWriteAllow = map[string]string{
	"model/points_lot.go": "批次机制本体：加锁空操作 / 发放 / override 调低 / 过期作废",
	"model/user_points.go": "IncreaseUserPoints / DecreaseUserPoints / TryDecreaseUserPoints，" +
		"只保留给退款·回补与管理员减少（发放必须走 GrantPoints）",
	"model/user.go": "User.Update 的 Omit：显式声明「不写这一列」，且是本次修复的一部分",
}

// 允许调用 IncreaseUserPoints 的文件。
var increaseUserPointsAllow = map[string]string{
	"service/funding_hybrid.go": "混扣退款 giveBack",
	"service/task_billing.go":   "异步任务退款",
	"model/checkin.go":          "额度模式的旧路径（isPoints=false 时才走到）",
}

// 通过变量拼列名、文本扫描看不到字面量的写法，只允许签到那处历史遗留分支。
var columnVariableAllow = map[string]string{
	"model/checkin.go": "userCheckinWithTransaction 的额度模式分支；积分模式已提前 return",
}

func repoRootForGuard(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	return filepath.Dir(wd)
}

func scanNonTestGoFiles(t *testing.T, root string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "node_modules" || name == "dist" || name == "web" ||
				name == "electron" || name == "logs" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		lines := strings.Split(string(data), "\n")
		kept := make([]string, 0, len(lines))
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "//") {
				continue
			}
			kept = append(kept, trimmed)
		}
		out[rel] = kept
		return nil
	})
	require.NoError(t, err)
	return out
}

// TestPointsGuard_NoDirectBalanceWriteOutsideWhitelist 断言没有任何新代码直接改余额。
func TestPointsGuard_NoDirectBalanceWriteOutsideWhitelist(t *testing.T) {
	root := repoRootForGuard(t)
	files := scanNonTestGoFiles(t, root)

	got := map[string]bool{}
	for rel, lines := range files {
		for _, line := range lines {
			if !strings.Contains(line, "points_balance") {
				continue
			}
			// 写操作的三种形态：Update("points_balance", ...)、
			// gorm.Expr("points_balance + ?")、以及 Omit 的显式声明。
			if strings.Contains(line, "Update(") || strings.Contains(line, "gorm.Expr(") ||
				strings.Contains(line, "Omit(") {
				got[rel] = true
			}
		}
	}

	gotList := sortedKeys(got)
	wantList := sortedMapKeys(pointsBalanceColumnWriteAllow)
	require.Equal(t, wantList, gotList,
		"直接写 points_balance 的文件集合变了。新增写入必须走 GrantPoints / 批次机制，"+
			"确实需要时请连同理由一起更新本测试的白名单。")
}

// TestPointsGuard_NoIncreaseUserPointsOutsideWhitelist 断言发放不会绕过批次。
func TestPointsGuard_NoIncreaseUserPointsOutsideWhitelist(t *testing.T) {
	root := repoRootForGuard(t)
	files := scanNonTestGoFiles(t, root)

	got := map[string]bool{}
	for rel, lines := range files {
		for _, line := range lines {
			if !strings.Contains(line, "IncreaseUserPoints(") {
				continue
			}
			if strings.HasPrefix(line, "func ") {
				continue // 定义本身
			}
			got[rel] = true
		}
	}

	gotList := sortedKeys(got)
	wantList := sortedMapKeys(increaseUserPointsAllow)
	require.Equal(t, wantList, gotList,
		"IncreaseUserPoints 只允许用于退款 / 回补；发放必须走 GrantPoints，否则余额没有批次")
}

// TestPointsGuard_ColumnVariableWritesAreKnown 防止用变量拼列名绕过上面的字面量扫描。
func TestPointsGuard_ColumnVariableWritesAreKnown(t *testing.T) {
	root := repoRootForGuard(t)
	files := scanNonTestGoFiles(t, root)

	got := map[string]bool{}
	for rel, lines := range files {
		for _, line := range lines {
			// 形如 col = "points_balance"
			if strings.Contains(line, "= \"points_balance\"") {
				got[rel] = true
			}
		}
	}

	gotList := sortedKeys(got)
	wantList := sortedMapKeys(columnVariableAllow)
	require.Equal(t, wantList, gotList,
		"通过变量拼列名写积分余额会绕过字面量扫描；除已知的签到额度模式分支外不应再出现")
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedMapKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
