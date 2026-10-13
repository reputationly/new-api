package model

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 审核日志异步队列：「什么时候算排空」的判定，以及记账时机。
//
// 刻意**不替换包级变量 moderationQueue**。worker goroutine 就是对着那个变量求值并 range 的，
// 测试侧改写它会与 worker 的读取构成数据竞争（`go test -race` 稳定报 DATA RACE），
// 而且会连带把后面的用例搞坏。
//
// 需要让 worker 停在"已取走、未落库"的中间态时，用占住测试库唯一连接的办法：测试库是
// SetMaxOpenConns(1)，一个事务就能把 worker 卡在 DB.Create 上。这样测的是真实代码路径，
// 没有替身，也不需要往生产代码里插测试钩子。

// blockDBWrites 占住测试库唯一的连接，使 worker 的落库停在中途；返回释放函数（可重复调用）。
func blockDBWrites(t *testing.T) func() {
	t.Helper()
	tx := DB.Begin()
	require.NoError(t, tx.Error, "开启事务以占住连接失败")
	released := false
	return func() {
		if released {
			return
		}
		released = true
		_ = tx.Rollback()
	}
}

func setPendingForTest(t *testing.T, v int64) {
	t.Helper()
	orig := moderationPending.Load()
	moderationPending.Store(v)
	t.Cleanup(func() { moderationPending.Store(orig) })
}

// 没有待落库记录 → 立即判定排空，且不该白等：关闭流程每次都要跑，白等会累加。
func TestDrainReturnsTrueWhenNothingPending(t *testing.T) {
	InitModerationLogWorker()
	require.NotNil(t, moderationQueue, "队列应已初始化")
	setPendingForTest(t, 0)

	start := time.Now()
	require.True(t, DrainModerationLogQueue(time.Second), "没有待落库记录时应判定为已排空")
	require.Less(t, time.Since(start), 200*time.Millisecond, "没有待落库记录时不该白等")
}

// 有记录没落库 → 等到超时并如实返回 false，绝不能谎报排空后退出。
func TestDrainWaitsWhileRecordsPending(t *testing.T) {
	InitModerationLogWorker()
	setPendingForTest(t, 1)

	start := time.Now()
	require.False(t, DrainModerationLogQueue(300*time.Millisecond), "有未落库记录时不应判定为已排空")
	require.GreaterOrEqual(t, time.Since(start), 300*time.Millisecond, "应等满超时")
}

// worker 未启动（队列为 nil）时直接放行：这种部署根本没有异步落库，没有东西可排。
func TestDrainModerationLogQueueNotStarted(t *testing.T) {
	origQueue := moderationQueue
	moderationQueue = nil
	t.Cleanup(func() { moderationQueue = origQueue })
	setPendingForTest(t, 0)

	require.True(t, DrainModerationLogQueue(time.Second), "worker 未启动时应直接放行")
}

// 锁住修补的那个空窗：记录已被 worker 从队列取走、但还没写完库。
//
// 旧实现在 worker 取出之后才自增计数（`for entry := range q` 是先取出、再执行循环体），
// 这个瞬间 len(queue)==0 且计数为 0，排空判定会谎报已排空并返回 true —— 丢掉最后一条，
// 而且只在退出路径上发生，平时完全看不出来。
// 新实现从入队那一刻起计数就是 1，所以这里必须判成"还没排空"。
func TestDrainWaitsWhileWorkerIsMidWrite(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&ModerationLog{}))
	InitModerationLogWorker()

	release := blockDBWrites(t)
	defer release()

	before := moderationPending.Load()
	const requestId = "drain-window-1"
	RecordModerationLog(&ModerationLog{RequestId: requestId})

	require.Equal(t, before+1, moderationPending.Load(),
		"入队即记账：记录在路上那一刻计数就该 +1（旧实现此刻是 0，正是丢数据的原因）")

	// 等 worker 把它取走（队列变空）——此时它正卡在写库那一步
	deadline := time.Now().Add(3 * time.Second)
	for len(moderationQueue) > 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	require.Zero(t, len(moderationQueue), "worker 应已把记录从队列取走")

	// 关键断言：队列空了，但绝不能判定为已排空
	require.Equal(t, before+1, moderationPending.Load(), "取走后、落库前，计数必须仍为 1")
	require.False(t, DrainModerationLogQueue(200*time.Millisecond),
		"worker 已取走但尚未落库，此时判定「已排空」就会丢掉最后一条审核记录")

	release()
	require.True(t, DrainModerationLogQueue(3*time.Second), "落库完成后应判定为已排空")

	DB.Where("request_id = ?", requestId).Delete(&ModerationLog{})
}

// 队列满被丢弃时必须立刻还账：没有 worker 会替被丢弃的记录减数，漏了这一步排空会永远
// 等不到 0，每次退出都白等满预算。
//
// 同样靠占住数据库连接让 worker 停住，从而能把队列真正填满（否则它一直在消费）。
func TestRecordModerationLogDoesNotLeakCountOnDrop(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&ModerationLog{}))
	InitModerationLogWorker()

	release := blockDBWrites(t)
	defer release()

	size := cap(moderationQueue)
	require.Positive(t, size, "队列容量应为正数")
	// 多喂两条：worker 手上最多留一条，因此必定至少有一条入队失败
	for i := 0; i < size+2; i++ {
		RecordModerationLog(&ModerationLog{RequestId: "drain-drop"})
	}
	require.Len(t, moderationQueue, size, "队列应已被填满")

	full := moderationPending.Load()
	for i := 0; i < 20; i++ {
		RecordModerationLog(&ModerationLog{RequestId: "drain-drop"})
	}
	require.Equal(t, full, moderationPending.Load(),
		"被丢弃的记录必须在入队失败时立刻还账，否则排空永远等不到 0")

	// 自己把队列排空：不释放 DB 锁，免得 worker 真去写 size 条记录
	for len(moderationQueue) > 0 {
		<-moderationQueue
		moderationPending.Add(-1)
	}
	release()

	require.True(t, DrainModerationLogQueue(5*time.Second), "剩余那一条落库后应判定为已排空")
	DB.Where("request_id = ?", "drain-drop").Delete(&ModerationLog{})
}
