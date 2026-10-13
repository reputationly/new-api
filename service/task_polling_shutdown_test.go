package service

import (
	"testing"
	"time"
)

// 进入关闭流程后，任务轮询循环必须**不再开新的一轮**并且退出。
//
// 为什么这条重要：每一轮里的 sweepTimedOutTasks 会把超时的任务 CAS 成失败、**然后**才调
// RefundTaskQuota 退款，而退款走 IncreaseUserQuota(..., false) → addNewRecord。收尾只会等
// 「已登记的关键工作」，所以关闭期间新起的一轮可能晚于刷库（进了一个再也没人刷的 store）
// 甚至晚于进程退出——而那时任务已经是终态，之后再也不会被捞回来退款，用户的钱就白扣了。
//
// 这里把间隔压到 10ms 后调用循环，断言它会自己退出；没有那两行判断的话它会一直循环，
// 用例超时失败。
//
// 这里注入一个"拒绝登记"的 beginPollingRound，而不是去置位 common 那个不可撤销的
// 关闭标志：后者会污染整个 service 测试二进制，以后任何需要真跑一轮的用例都会莫名失败。
// 真实的关闭判定（以及"先加计数、再读标志"的顺序保证）在 common.BeginCritical 里，
// 由 common 包的 TestBeginCriticalContract 覆盖。
func TestTaskPollingLoopStopsWhenShuttingDown(t *testing.T) {
	origInterval := taskPollingInterval
	taskPollingInterval = 10 * time.Millisecond
	t.Cleanup(func() { taskPollingInterval = origInterval })

	origBegin := beginPollingRound
	beginPollingRound = func() (func(), bool) { return func() {}, false }
	t.Cleanup(func() { beginPollingRound = origBegin })

	done := make(chan struct{})
	go func() {
		defer close(done)
		TaskPollingLoop()
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("登记被拒绝（已进入关闭流程）时轮询循环应立刻退出——" +
			"否则它会在收尾期间起新的一轮，把任务 CAS 成失败后才退款，而退款可能晚于刷库甚至晚于进程退出")
	}
}
