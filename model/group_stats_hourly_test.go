package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// baseHour 一个整点时间戳，测试里所有日志都相对它偏移，避免跨小时抖动。
func baseHour() int64 {
	const raw = int64(1790000000)
	return raw - raw%3600
}

func insertGroupLog(t *testing.T, row *Log) {
	t.Helper()
	require.NoError(t, LOG_DB.Create(row).Error)
}

func resetGroupStats(t *testing.T) {
	t.Helper()
	truncateTables(t)
	require.NoError(t, DB.Exec("DELETE FROM group_stats_hourly").Error)
	require.NoError(t, DB.Exec("DELETE FROM group_stats_meta").Error)
}

func findGroupStat(t *testing.T, rows []*GroupQuotaStat, group string) *GroupQuotaStat {
	t.Helper()
	for _, row := range rows {
		if row.Group == group {
			return row
		}
	}
	return nil
}

// 口径：调用次数只数消费行；tokens 只来自消费行；金额按类型净额化；
// 余额逐行 max(quota - 积分 - 授信, 0) 且剔除套餐/订阅。
func TestAggregateLogsByGroupHour_QuotaSemantics(t *testing.T) {
	resetGroupStats(t)
	hour := baseHour()

	rows := []*Log{
		// 默认分组：消费 100，积分抵扣 30 → 现金 70
		{
			UserId: 1, Username: "u1", CreatedAt: hour + 10, Type: LogTypeConsume,
			Group: "default", Quota: 100, PointsConsumed: 30,
			PromptTokens: 10, CompletionTokens: 5,
		},
		// 默认分组：退款 20，积分退回 10 → 现金 -10；不算一次调用
		{
			UserId: 1, Username: "u1", CreatedAt: hour + 20, Type: LogTypeRefund,
			Group: "default", Quota: 20, PointsConsumed: 10,
		},
		// vip：消费 200，授信承担 50 → 现金 150
		{UserId: 2, Username: "u2", CreatedAt: hour + 30, Type: LogTypeConsume, Group: "vip", Quota: 200, CreditConsumed: 50},
		// vip：套餐权益消费，不动钱包 → 现金 0
		{
			UserId: 2, Username: "u2", CreatedAt: hour + 40, Type: LogTypeConsume,
			Group: "vip", Quota: 500, Other: `{"billing_source":"entitlement"}`,
		},
		// 错误日志不计费
		{UserId: 1, Username: "u1", CreatedAt: hour + 50, Type: LogTypeError, Group: "default", Quota: 999},
	}
	for _, row := range rows {
		insertGroupLog(t, row)
	}

	got, err := aggregateLogsByGroupHour(hour, hour+3600, "")
	require.NoError(t, err)

	stat := make(map[string]*GroupStatsHourly)
	for _, row := range got {
		stat[row.GroupName] = row
	}

	def := stat["default"]
	require.NotNil(t, def)
	require.Equal(t, int64(1), def.ReqCount, "退款不算调用")
	require.Equal(t, int64(15), def.Tokens, "tokens 只统计消费行")
	require.Equal(t, int64(80), def.TotalQuota, "消费 100 - 退款 20")
	require.Equal(t, int64(60), def.CashQuota, "现金 (100-30) - (20-10)")

	vip := stat["vip"]
	require.NotNil(t, vip)
	require.Equal(t, int64(2), vip.ReqCount)
	require.Equal(t, int64(700), vip.TotalQuota, "总消耗含套餐权益：200 + 500")
	require.Equal(t, int64(150), vip.CashQuota, "套餐消费不算现金，授信也不算现金")
	require.Zero(t, vip.Tokens)
}

// 汇总写入必须幂等：同一小时重算两次，库里仍是同一行、同样的值。
func TestRollGroupStatsRange_Idempotent(t *testing.T) {
	resetGroupStats(t)
	hour := baseHour()

	for i := 0; i < 3; i++ {
		insertGroupLog(t, &Log{
			UserId: 10, Username: "idem", CreatedAt: hour + int64(i),
			Type: LogTypeConsume, Group: "default", Quota: 100,
			PromptTokens: 100, CompletionTokens: 50,
		})
	}

	count, err := rollGroupStatsRange(hour, hour+3600)
	require.NoError(t, err)
	require.Equal(t, 1, count)

	_, err = rollGroupStatsRange(hour, hour+3600)
	require.NoError(t, err)

	var rows []*GroupStatsHourly
	require.NoError(t, DB.Where("hour_ts = ?", hour).Find(&rows).Error)
	require.Len(t, rows, 1, "重算不能产生重复行")
	require.Equal(t, int64(3), rows[0].ReqCount)
	require.Equal(t, int64(450), rows[0].Tokens)
	require.Equal(t, int64(300), rows[0].TotalQuota)
	require.Equal(t, int64(300), rows[0].CashQuota)
}

// 读路径把「头部零头小时 + 整小时（汇总表）+ 尾部零头小时」拼回一个区间，
// 结果必须与直接聚合日志完全一致。
func TestGetGroupQuotaStats_MergesRollupAndHeadTail(t *testing.T) {
	resetGroupStats(t)
	hour := baseHour()

	// 第一小时：两条；第二小时：一条；第三小时：一条（都落在整小时里）
	insertGroupLog(t, &Log{UserId: 20, Username: "merge", CreatedAt: hour + 300, Type: LogTypeConsume, Group: "default", Quota: 100, PromptTokens: 10, CompletionTokens: 10})
	insertGroupLog(t, &Log{UserId: 20, Username: "merge", CreatedAt: hour + 3000, Type: LogTypeConsume, Group: "vip", Quota: 50, PromptTokens: 5, CompletionTokens: 5})
	insertGroupLog(t, &Log{UserId: 20, Username: "merge", CreatedAt: hour + 3600 + 60, Type: LogTypeConsume, Group: "vip", Quota: 200, PromptTokens: 20, CompletionTokens: 20})
	insertGroupLog(t, &Log{UserId: 20, Username: "merge", CreatedAt: hour + 7200 + 900, Type: LogTypeConsume, Group: "default", Quota: 70, PromptTokens: 7, CompletionTokens: 7})

	// 前两小时已物化
	_, err := rollGroupStatsRange(hour, hour+7200)
	require.NoError(t, err)
	require.NoError(t, saveGroupStatsMeta(hour, hour+3600))

	start := hour + 1800
	end := hour + 7200 + 1200
	got, err := GetGroupQuotaStats(start, end, "")
	require.NoError(t, err)

	// 参照值：同区间直接聚合（一个分组可能跨多个小时，必须累加而不是覆盖）
	refRows, err := aggregateLogsByGroupHour(start, end+1, "")
	require.NoError(t, err)
	reference := map[string]*GroupQuotaStat{}
	mergeGroupStats(reference, refRows)

	require.Len(t, got, len(reference), "分组集合必须一致")
	for group, want := range reference {
		item := findGroupStat(t, got, group)
		require.NotNil(t, item, group)
		require.Equal(t, want.Count, item.Count, group+" 调用次数")
		require.Equal(t, want.Tokens, item.Tokens, group+" tokens")
		require.Equal(t, want.TotalQuota, item.TotalQuota, group+" 总消耗")
		require.Equal(t, want.CashQuota, item.CashQuota, group+" 余额消耗")
	}
}

// 未汇总的区间要退回 logs 现算，不能少报（定时任务停机/回填未完成时的安全性）。
func TestGetGroupQuotaStats_FallsBackToLogsWhenNotRolled(t *testing.T) {
	resetGroupStats(t)
	hour := baseHour()

	insertGroupLog(t, &Log{UserId: 30, Username: "fb", CreatedAt: hour + 100, Type: LogTypeConsume, Group: "default", Quota: 42})

	// 完全没有 meta：rolledEnd = 0，整段都应走 logs
	got, err := GetGroupQuotaStats(hour, hour+3600, "")
	require.NoError(t, err)
	item := findGroupStat(t, got, "default")
	require.NotNil(t, item)
	require.Equal(t, int64(42), item.TotalQuota)
	require.Equal(t, int64(42), item.CashQuota)
}

// 带 username 时整段走 logs（汇总表没有用户维度），结果与管理员全量口径一致。
func TestGetGroupQuotaStats_UsernameFilter(t *testing.T) {
	resetGroupStats(t)
	hour := baseHour()

	insertGroupLog(t, &Log{UserId: 40, Username: "alice", CreatedAt: hour + 10, Type: LogTypeConsume, Group: "default", Quota: 100})
	insertGroupLog(t, &Log{UserId: 41, Username: "bob", CreatedAt: hour + 20, Type: LogTypeConsume, Group: "default", Quota: 900})

	got, err := GetGroupQuotaStats(hour, hour+3600, "alice")
	require.NoError(t, err)
	item := findGroupStat(t, got, "default")
	require.NotNil(t, item)
	require.Equal(t, int64(100), item.TotalQuota)
	require.Equal(t, int64(1), item.Count)
}

// 只汇总了第一小时、第二小时还没汇总时，第二小时必须退回 logs 现算。
//
// 这是 rolledEnd 计算错误的回归用例：meta 存的是整点时间戳，曾经被当成「小时序号」
// 又乘了一次 3600，导致 tableEnd 永远等于窗口末尾，未汇总的整小时被静默漏掉。
func TestGetGroupQuotaStats_UnrolledHourFallsBackToLogs(t *testing.T) {
	resetGroupStats(t)
	hour := baseHour()

	insertGroupLog(t, &Log{UserId: 50, Username: "partial", CreatedAt: hour + 10, Type: LogTypeConsume, Group: "default", Quota: 100})
	insertGroupLog(t, &Log{UserId: 50, Username: "partial", CreatedAt: hour + 3600 + 10, Type: LogTypeConsume, Group: "default", Quota: 7})

	// 只物化第一小时
	_, err := rollGroupStatsRange(hour, hour+3600)
	require.NoError(t, err)
	require.NoError(t, saveGroupStatsMeta(hour, hour))

	got, err := GetGroupQuotaStats(hour, hour+7200, "")
	require.NoError(t, err)
	item := findGroupStat(t, got, "default")
	require.NotNil(t, item)
	require.Equal(t, int64(107), item.TotalQuota, "未汇总的第二小时必须从 logs 补上")
	require.Equal(t, int64(2), item.Count)
}

// 回填起点之前的区间：表里没有行，必须退回 logs，而不是显示 0。
//
// 「跨度 ≤ 90 天」只限跨度不限多早，所以查半年/一年前的整月也能通过校验。
func TestGetGroupQuotaStats_RangeBeforeBackfillStart(t *testing.T) {
	resetGroupStats(t)
	hour := baseHour()

	// 早于回填起点的历史数据
	insertGroupLog(t, &Log{UserId: 51, Username: "history", CreatedAt: hour + 10, Type: LogTypeConsume, Group: "default", Quota: 42})
	// 回填覆盖范围内的一小时
	insertGroupLog(t, &Log{UserId: 51, Username: "history", CreatedAt: hour + 3600 + 10, Type: LogTypeConsume, Group: "default", Quota: 8})
	_, err := rollGroupStatsRange(hour+3600, hour+7200)
	require.NoError(t, err)
	require.NoError(t, saveGroupStatsMeta(hour+3600, hour+3600))

	got, err := GetGroupQuotaStats(hour, hour+7200, "")
	require.NoError(t, err)
	item := findGroupStat(t, got, "default")
	require.NotNil(t, item)
	require.Equal(t, int64(50), item.TotalQuota, "回填起点之前的小时要从 logs 补")
}

// group IS NULL 与 group = '' 必须并成同一行：拆成两行都叫 '' 时，PG 的 upsert 会
// 直接报「cannot affect row a second time」，MySQL/SQLite 则后一行覆盖前一行少报。
func TestAggregateLogsByGroupHour_MergesNullAndEmptyGroup(t *testing.T) {
	resetGroupStats(t)
	hour := baseHour()

	insertGroupLog(t, &Log{UserId: 52, Username: "nullg", CreatedAt: hour + 10, Type: LogTypeConsume, Group: "", Quota: 100})
	require.NoError(t, LOG_DB.Exec(
		"INSERT INTO logs (user_id, created_at, type, username, model_name, quota, token_id, `group`, other) VALUES (52, ?, 2, 'nullg', 'm', 7, 0, NULL, '{}')",
		hour+20,
	).Error)

	rows, err := aggregateLogsByGroupHour(hour, hour+3600, "")
	require.NoError(t, err)
	require.Len(t, rows, 1, "NULL 和 '' 不能拆成两行")
	require.Equal(t, "", rows[0].GroupName)
	require.Equal(t, int64(107), rows[0].TotalQuota)
}
