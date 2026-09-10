package service

import (
	"strings"
	"testing"
)

// 产物审核的每轮预算必须真的**每轮**重置。
//
// 这条测试存在的原因：加预算时注释写着「每轮开头重置」、告警写着「下一轮恢复」，
// 而那行重置压根没落进文件。于是它成了进程级累计量——两次最坏情况的单次超时
// 就把产物审核永久关掉，且守卫和告警在同一个 if 里，关得毫无声息，
// 管理端还显示着「产物运行模式 = 拦截」。
func TestOutputModerationBudgetResetsEachCycle(t *testing.T) {
	t.Cleanup(resetOutputModerationBudget)

	resetOutputModerationBudget()
	if !outputModerationAllowed() {
		t.Fatal("重置后应当允许审核")
	}

	// 烧满预算（一次最坏情况的单次超时是 mediaBatchBudget=30s，两次就够）
	outputModerationSpent = outputModerationCycleBudget
	if outputModerationAllowed() {
		t.Fatal("预算用满后本轮应当停止审核")
	}

	// **关键的一步**：下一轮必须恢复。少了重置，这里仍然是 false，
	// 而线上表现是「审核悄悄永久关闭」。
	resetOutputModerationBudget()
	if !outputModerationAllowed() {
		t.Fatal("下一轮预算必须重置——否则产物审核会在第一次超时后永久关闭")
	}
}

// 轮询循环里必须真的调用重置。上面那条测试只证明函数本身对，
// 证明不了它被接进了循环——而漏接正是实际发生过的那次事故。
func TestPollingLoopResetsOutputModerationBudget(t *testing.T) {
	src := readSourceFile(t, "task_polling.go")
	loop := src[strings.Index(src, "func TaskPollingLoop() {"):]
	body := loop[:strings.Index(loop, "\n}\n")]
	if !strings.Contains(body, "resetOutputModerationBudget()") {
		t.Fatal("TaskPollingLoop 里没有调用 resetOutputModerationBudget()——" +
			"预算会变成进程级累计量，产物审核在第一次超时后永久关闭")
	}
}
