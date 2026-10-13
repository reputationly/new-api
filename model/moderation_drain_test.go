package model

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 审核日志异步队列的排空判定。这里直接操作包内全局量、不建真实记录：
// 要验证的是"什么时候算排空"这个判定本身，而它有两个容易写错的点——
//   - 光看队列长度不够：worker 可能刚把最后一条取走、正在写库，此时 len==0 但没落库；
//   - 不能靠 close(channel) 来收口：RecordModerationLog 会向它发送，关闭后再发直接 panic。
func withModerationQueueState(t *testing.T, q chan *ModerationLog, inFlight int64) {
	t.Helper()
	origQueue := moderationQueue
	origInFlight := moderationInFlight.Load()
	moderationQueue = q
	moderationInFlight.Store(inFlight)
	t.Cleanup(func() {
		moderationQueue = origQueue
		moderationInFlight.Store(origInFlight)
	})
}

func TestDrainModerationLogQueueEmpty(t *testing.T) {
	withModerationQueueState(t, make(chan *ModerationLog, 4), 0)

	start := time.Now()
	require.True(t, DrainModerationLogQueue(time.Second), "空队列应立即判定为已排空")
	require.Less(t, time.Since(start), 200*time.Millisecond, "空队列不该白等")
}

// worker 未启动（队列为 nil）时直接放行：这种部署根本没有异步落库，没有东西可排。
func TestDrainModerationLogQueueNotStarted(t *testing.T) {
	withModerationQueueState(t, nil, 0)
	require.True(t, DrainModerationLogQueue(time.Second), "worker 未启动时应直接放行")
}

// 队列里有积压且没人消费 → 必须等到超时并如实返回 false，绝不能谎报排空后退出。
func TestDrainModerationLogQueueTimesOutWithBacklog(t *testing.T) {
	q := make(chan *ModerationLog, 4)
	q <- &ModerationLog{Id: 1}
	withModerationQueueState(t, q, 0)

	start := time.Now()
	require.False(t, DrainModerationLogQueue(300*time.Millisecond), "有积压时不应判定为已排空")
	require.GreaterOrEqual(t, time.Since(start), 300*time.Millisecond, "应等满超时")

	<-q // 排掉，别影响同包其它测试
}

// 队列空但有在途写库 → 仍要等。这条对应"worker 刚取走最后一条、正在写 DB"的窗口，
// 漏判的话最后一条审核记录会丢。
func TestDrainModerationLogQueueWaitsForInFlightWrite(t *testing.T) {
	withModerationQueueState(t, make(chan *ModerationLog, 4), 1)
	require.False(t, DrainModerationLogQueue(300*time.Millisecond), "有在途写库时不应判定为已排空")
}
