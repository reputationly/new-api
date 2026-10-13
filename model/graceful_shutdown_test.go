package model

import (
	"testing"

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
