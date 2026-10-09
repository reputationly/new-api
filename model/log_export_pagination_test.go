package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 回归：用户导出超过一批（1000 条）时必须拿到全部行。
// 旧实现里 perBatch（formatUserLogs）会把 batch 的 Id 改写成 1..N，而游标取自回调之后，
// 第一批结束后游标变成 1000，下一次查询 id < 1000 为空，导出静默只剩第一批。
func TestExportUserLogs_PaginatesPastFirstBatch(t *testing.T) {
	truncateTables(t)

	const userId = 9001
	const total = 2500
	rows := make([]*Log, 0, total)
	for i := 0; i < total; i++ {
		rows = append(rows, &Log{
			UserId:    userId,
			Username:  "export_batch_user",
			CreatedAt: 1790092800 + int64(i),
			Type:      LogTypeConsume,
			Quota:     1,
		})
	}
	require.NoError(t, LOG_DB.CreateInBatches(rows, 500).Error)

	// 行的唯一标识用 CreatedAt：formatUserLogs 会改写 Id，不能用 Id 去重。
	seen := make(map[int64]struct{}, total)
	err := ExportUserLogs(userId, LogTypeUnknown, 0, 0, "", "", "", "", nil, "", 1000,
		func(batch []*Log) error {
			for _, l := range batch {
				seen[l.CreatedAt] = struct{}{}
			}
			return nil
		})
	require.NoError(t, err)
	require.Equal(t, total, len(seen), "用户导出应包含全部日志")
}
