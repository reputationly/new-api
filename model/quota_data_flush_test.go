package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/require"
)

// 优雅退出时「刷数据看板增量」这一步。
//
// 要验证的是：进程退出前调用它，本进程内存里攒着的看板增量确实落库了。不刷的话，
// 最多 DataExportInterval（默认 5 分钟）内的增量随进程消失——单容器时 docker stop
// 也一样丢，但上了 CCE 之后滚动升级/节点排水/缩容都会触发，会持续累积成看板偏少。
//
// 注意这处**不能**像另外三个后台任务那样改成「只在主节点跑」：CacheQuotaData 是进程内
// map，每个副本只能刷自己那份，收敛写入者会让从节点的增量永远落不了库（详见 usedata.go
// 的注释）。所以这里的用例刻意只测「刷下去」这一件事。
func TestFlushQuotaDataCachePersistsAccumulatedDelta(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&QuotaData{}))
	DB.Exec("DELETE FROM quota_data WHERE user_id = ?", 950)
	t.Cleanup(func() { DB.Exec("DELETE FROM quota_data WHERE user_id = ?", 950) })

	origEnabled := common.DataExportEnabled
	common.DataExportEnabled = true
	t.Cleanup(func() { common.DataExportEnabled = origEnabled })

	createdAt := time.Now().Unix()
	LogQuotaData(950, "flush_950", "dash-test-model", 123, createdAt, 7)

	// 只累积在内存里，还没落库
	var before int64
	require.NoError(t, DB.Table("quota_data").Where("user_id = ?", 950).Count(&before).Error)
	require.Zero(t, before, "增量应先只进本进程的内存 map")

	FlushQuotaDataCache()

	var got QuotaData
	require.NoError(t, DB.Table("quota_data").Where("user_id = ?", 950).First(&got).Error)
	require.Equal(t, 123, got.Quota, "退出前应把内存里的看板增量落库")
	require.Equal(t, 1, got.Count)
	require.Equal(t, 7, got.TokenUsed)

	// 刷完缓存已清空，再刷一次不应重复累加（否则看板会越刷越多）
	FlushQuotaDataCache()
	require.NoError(t, DB.Table("quota_data").Where("user_id = ?", 950).First(&got).Error)
	require.Equal(t, 123, got.Quota, "缓存已清空，重复刷不应再加一次")
	require.Equal(t, 1, got.Count)
}

// 关掉数据导出时是空操作：那种部署下 LogQuotaData 的调用点也判这个开关，没有任何东西
// 被累积，更不该因此写库或报错。
func TestFlushQuotaDataCacheIsNoopWhenDataExportDisabled(t *testing.T) {
	origEnabled := common.DataExportEnabled
	common.DataExportEnabled = false
	t.Cleanup(func() { common.DataExportEnabled = origEnabled })

	// 不该 panic，也不该碰数据库
	FlushQuotaDataCache()
}
