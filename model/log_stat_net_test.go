package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 消耗额度口径：消费计正、退款计负，统计随类型筛选变化，与导出「费用」列同一口径。
func TestSumUserLogStat_NetOfRefunds(t *testing.T) {
	truncateTables(t)

	const userId = 9501
	const username = "stat_net_user"
	base := int64(1790092800)
	rows := []*Log{
		{UserId: userId, Username: username, CreatedAt: base + 1, Type: LogTypeConsume, Quota: 100},
		{UserId: userId, Username: username, CreatedAt: base + 2, Type: LogTypeConsume, Quota: 50},
		{UserId: userId, Username: username, CreatedAt: base + 3, Type: LogTypeRefund, Quota: 30},
		{UserId: userId, Username: username, CreatedAt: base + 4, Type: LogTypeError, Quota: 0},
	}
	require.NoError(t, LOG_DB.CreateInBatches(rows, 500).Error)

	cases := []struct {
		name    string
		logType int
		want    int
	}{
		{"全部类型：消费 150 - 退款 30", LogTypeUnknown, 120},
		{"只看消费", LogTypeConsume, 150},
		{"只看退款：负数，与导出里退款行的费用一致", LogTypeRefund, -30},
		{"错误日志不计费", LogTypeError, 0},
	}
	for _, c := range cases {
		stat, err := SumUserLogStat(userId, c.logType, base, base+3600, "", "", "", "", nil, "")
		require.NoError(t, err, c.name)
		require.Equal(t, c.want, stat.Quota, c.name)
	}
}
