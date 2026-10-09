package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// seedExportCountLogs 写入 n 条消费日志，user_id 与 username 由参数指定。
func seedExportCountLogs(t *testing.T, userId int, username string, n int) {
	t.Helper()
	rows := make([]*Log, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, &Log{
			UserId:    userId,
			Username:  username,
			CreatedAt: 1790092800 + int64(i),
			Type:      LogTypeConsume,
			Quota:     1,
		})
	}
	require.NoError(t, LOG_DB.CreateInBatches(rows, 500).Error)
}

func TestCountUserLogsForExport_CapsAtLimit(t *testing.T) {
	truncateTables(t)
	seedExportCountLogs(t, 9201, "count_user_a", 25)
	seedExportCountLogs(t, 9202, "count_user_b", 5)

	n, err := CountUserLogsForExport(9201, LogTypeUnknown, 0, 0, "", "", "", "", nil, "", 1000)
	require.NoError(t, err)
	require.Equal(t, int64(25), n)

	// limit 生效：最多只数到 limit，不会全表计数
	n, err = CountUserLogsForExport(9201, LogTypeUnknown, 0, 0, "", "", "", "", nil, "", 10)
	require.NoError(t, err)
	require.Equal(t, int64(10), n)

	// 只统计本用户的日志
	n, err = CountUserLogsForExport(9202, LogTypeUnknown, 0, 0, "", "", "", "", nil, "", 1000)
	require.NoError(t, err)
	require.Equal(t, int64(5), n)
}

func TestCountAllLogsForExport_MatchesUsernameFilter(t *testing.T) {
	truncateTables(t)
	seedExportCountLogs(t, 9211, "count_admin_alice", 7)
	seedExportCountLogs(t, 9212, "count_admin_bob", 3)

	n, err := CountAllLogsForExport(LogTypeUnknown, 0, 0, "", "count_admin_alice", "", nil, "", "", "", 1000)
	require.NoError(t, err)
	require.Equal(t, int64(7), n)

	n, err = CountAllLogsForExport(LogTypeUnknown, 0, 0, "", "", "", nil, "", "", "", 1000)
	require.NoError(t, err)
	require.Equal(t, int64(10), n)
}
