package controller

import (
	"testing"
	"time"
)

// 进入关闭流程后，Midjourney 任务轮询循环必须不再开新的一轮并退出。
//
// 与任务轮询同理：每一轮里 `task.UpdateWithStatus(preStatus)` 先把任务 CAS 成终态，
// **然后**才 `IncreaseUserQuota(task.UserId, task.Quota, false)` 退款（走 addNewRecord）。
// 收尾只等「已登记的关键工作」，所以关闭期间新起的一轮可能晚于刷库、甚至晚于进程退出——
// 而那时任务已是终态，不会再被捞回来退款，用户的钱白扣。
//
// 注入"拒绝登记"的 beginMidjourneyRound，不碰全局关闭标志（理由同 service 的同名用例）。
func TestUpdateMidjourneyTaskBulkStopsWhenShuttingDown(t *testing.T) {
	origInterval := midjourneyTaskInterval
	midjourneyTaskInterval = 10 * time.Millisecond
	t.Cleanup(func() { midjourneyTaskInterval = origInterval })

	origBegin := beginMidjourneyRound
	beginMidjourneyRound = func() (func(), bool) { return func() {}, false }
	t.Cleanup(func() { beginMidjourneyRound = origBegin })

	done := make(chan struct{})
	go func() {
		defer close(done)
		UpdateMidjourneyTaskBulk()
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("登记被拒绝（已进入关闭流程）时 Midjourney 轮询循环应立刻退出——" +
			"否则它会在收尾期间起新的一轮，把任务 CAS 成终态后才退款，而退款可能晚于刷库甚至晚于进程退出")
	}
}
