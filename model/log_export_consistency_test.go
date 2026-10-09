package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 客户场景的一致性：列表总数 = 导出行数，导出 quota 合计 = 页面消耗额度。
// 数据按线上真实口径构造：窗口内 2077 条消费（quota 合计 2501047）+ 1 条错误（quota 0），
// 窗口外另有 2 条管理日志（quota 0）。
func TestExportListAndStatAgree_CustomerScenario(t *testing.T) {
	truncateTables(t)

	const (
		userId       = 9401
		username     = "consistency_user"
		windowStart  = int64(1790092800) // 2026-09-23 00:00:00 +08:00
		windowEnd    = int64(1790697599) // 2026-09-29 23:59:59 +08:00
		consumeCount = 2077
		totalQuota   = 2501047
	)

	rows := make([]*Log, 0, consumeCount+3)
	for i := 0; i < consumeCount; i++ {
		quota := totalQuota / consumeCount
		if i < totalQuota%consumeCount {
			quota++
		}
		rows = append(rows, &Log{
			UserId:    userId,
			Username:  username,
			CreatedAt: windowStart + 3600 + int64(i),
			Type:      LogTypeConsume,
			Quota:     quota,
		})
	}
	rows = append(rows,
		&Log{UserId: userId, Username: username, CreatedAt: windowStart + 3600, Type: LogTypeError},
		&Log{UserId: userId, Username: username, CreatedAt: windowStart - 86400, Type: LogTypeManage},
		&Log{UserId: userId, Username: username, CreatedAt: windowStart - 2*86400, Type: LogTypeManage},
	)
	require.NoError(t, LOG_DB.CreateInBatches(rows, 500).Error)

	// 页面"共 N 条"
	_, listTotal, err := GetUserLogs(userId, LogTypeUnknown, windowStart, windowEnd, "", "", 0, 10, "", "", nil, "")
	require.NoError(t, err)
	require.Equal(t, int64(2078), listTotal)

	// 导出：行数与 quota 合计
	exportRows, exportQuota := 0, 0
	err = ExportUserLogs(userId, LogTypeUnknown, windowStart, windowEnd, "", "", "", "", nil, "", 1000,
		func(batch []*Log) error {
			exportRows += len(batch)
			for _, l := range batch {
				exportQuota += l.Quota
			}
			return nil
		})
	require.NoError(t, err)
	require.Equal(t, int(listTotal), exportRows, "导出行数应等于页面总数")
	require.Equal(t, totalQuota, exportQuota, "导出 quota 合计应等于页面消耗额度")

	// 页面"消耗额度"：统计只算消费
	stat, err := SumUserLogStat(userId, LogTypeConsume, windowStart, windowEnd, "", "", "", "", nil, "")
	require.NoError(t, err)
	require.Equal(t, totalQuota, stat.Quota)
}
