package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/require"
)

// 优雅退出时"刷批量额度"这一步。要验证的是：进程退出前调用它，内存里攒着的额度变动
// 确实落库了——否则这部分扣费/充值会凭空消失，额度账对不上。
//
// 不在这里重复证明增量 SQL 本身（quota = quota + ?），那是既有代码的行为。
func TestFlushBatchUpdatePersistsAccumulatedDelta(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.Create(&User{Id: 901, Username: "flush_901", Role: 1, Status: 1, Quota: 1000}).Error)

	// 测试环境 TestMain 把批量更新关掉了，这里显式打开——被测的正是"开启批量更新"
	// 这条路径上的退出刷库。
	origEnabled := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = true
	t.Cleanup(func() { common.BatchUpdateEnabled = origEnabled })

	// db=false + 批量更新开启 + 无授信额度 → 只累加在内存里，不碰数据库
	require.NoError(t, DecreaseUserQuota(901, 500, false))

	var before User
	require.NoError(t, DB.Select("quota").Where("id = ?", 901).First(&before).Error)
	require.Equal(t, 1000, before.Quota, "还没刷库，库里应保持原值（说明确实进了内存队列）")

	FlushBatchUpdate()

	var after User
	require.NoError(t, DB.Select("quota").Where("id = ?", 901).First(&after).Error)
	require.Equal(t, 500, after.Quota, "退出前刷库应把内存里累积的扣减落库")
}

// FlushBatchUpdate 在未开启批量更新时必须是空操作：那种部署下每笔变动本来就是
// 直接写库的，没有东西要刷，更不能因此报错。
func TestFlushBatchUpdateIsNoopWhenBatchUpdateDisabled(t *testing.T) {
	origEnabled := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = false
	t.Cleanup(func() { common.BatchUpdateEnabled = origEnabled })

	// 不该 panic，也不该碰数据库（此时 DB 里没有任何用户，若真去刷会打一堆错误日志）
	FlushBatchUpdate()
}

// 复现并锁住退出路径上的丢数据竞态。
//
// 后台那一轮 batchUpdate 是**先把 store 换成空 map、再逐条写库**。如果退出时的
// FlushBatchUpdate 只看"store 空不空"，它会直接返回，调用方以为额度已刷完 → 进程退出、
// 数据库关闭 → 上一轮还没写完的那些增量就丢了。而这恰好发生在退出路径上，
// 正是优雅退出本要防的场景。
//
// 这里手工摆出那个中间态（store 已空、那笔增量还在"在途轮"手上没落库），
// 断言 Flush 必须等它写完才能返回。
func TestFlushBatchUpdateWaitsForRoundInProgress(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.Create(&User{Id: 903, Username: "flush_903", Role: 1, Status: 1, Quota: 1000}).Error)

	origEnabled := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = true
	t.Cleanup(func() { common.BatchUpdateEnabled = origEnabled })

	const delta = -500
	addNewRecord(BatchUpdateTypeUserQuota, 903, delta)

	// 摆中间态：整轮锁被"在途轮"持有，store 已被换成空 map，那笔增量取到手上还没写库。
	batchUpdateMu.Lock()
	roundHeld := true
	defer func() {
		if roundHeld {
			batchUpdateMu.Unlock()
		}
	}()

	batchUpdateLocks[BatchUpdateTypeUserQuota].Lock()
	taken := batchUpdateStores[BatchUpdateTypeUserQuota]
	batchUpdateStores[BatchUpdateTypeUserQuota] = make(map[int]int)
	batchUpdateLocks[BatchUpdateTypeUserQuota].Unlock()
	require.Equal(t, map[int]int{903: delta}, taken, "增量应已被在途轮取走（此时 store 是空的）")

	flushed := make(chan struct{})
	go func() {
		defer close(flushed)
		FlushBatchUpdate()
	}()

	select {
	case <-flushed:
		t.Fatal("在途轮还没写完库，FlushBatchUpdate 就返回了——调用方会以为已刷完并退出进程")
	case <-time.After(300 * time.Millisecond):
		// 预期：被在途轮挡住
	}

	// 在途轮把它手上的增量写完，再释放整轮锁
	require.NoError(t, increaseUserQuota(903, delta))
	batchUpdateMu.Unlock()
	roundHeld = false

	select {
	case <-flushed:
	case <-time.After(2 * time.Second):
		t.Fatal("在途轮结束后 FlushBatchUpdate 应能继续，而不是一直挂着")
	}

	var after User
	require.NoError(t, DB.Select("quota").Where("id = ?", 903).First(&after).Error)
	require.Equal(t, 500, after.Quota, "退出前必须保证所有已入队的增量都落库")
}
