package controller

import (
	"encoding/csv"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/stretchr/testify/require"
)

// sumCostColumn 解析导出的 CSV，返回「费用(CNY)」列的合计与数据行数（不含表头）。
func sumCostColumn(t *testing.T, body string) (float64, int) {
	t.Helper()
	body = strings.TrimPrefix(body, "\xEF\xBB\xBF")
	records, err := csv.NewReader(strings.NewReader(body)).ReadAll()
	require.NoError(t, err)
	require.NotEmpty(t, records)

	costIdx := -1
	for i, h := range records[0] {
		if h == "费用(CNY)" {
			costIdx = i
		}
	}
	require.GreaterOrEqual(t, costIdx, 0, "表头里应有「费用(CNY)」列")

	var sum float64
	for _, row := range records[1:] {
		v, err := strconv.ParseFloat(row[costIdx], 64)
		require.NoError(t, err)
		sum += v
	}
	return sum, len(records) - 1
}

// 表格的「费用(CNY)」合计必须等于页面消耗额度，且行数等于同一筛选条件下的数据行数。
// 数据包含消费、退款、错误三类，覆盖「全部」「消费」「退款」三种类型筛选。
func TestExportCostSumMatchesStat(t *testing.T) {
	db := setupExportLimitDB(t)
	seedExportLimitUser(t, db, 9601, "stat_export_user")

	base := int64(1790092800)
	rows := []*model.Log{
		{UserId: 9601, Username: "stat_export_user", CreatedAt: base + 1, Type: model.LogTypeConsume, Quota: 1204, PromptTokens: 100, CompletionTokens: 10},
		{UserId: 9601, Username: "stat_export_user", CreatedAt: base + 2, Type: model.LogTypeConsume, Quota: 77, PromptTokens: 50, CompletionTokens: 5},
		{UserId: 9601, Username: "stat_export_user", CreatedAt: base + 3, Type: model.LogTypeRefund, Quota: 300},
		{UserId: 9601, Username: "stat_export_user", CreatedAt: base + 4, Type: model.LogTypeError, Quota: 0},
	}
	require.NoError(t, db.CreateInBatches(rows, 500).Error)

	cases := []struct {
		typ       string
		wantQuota int
		wantRows  int
	}{
		{"0", 1204 + 77 - 300, 4},
		{"2", 1204 + 77, 2},
		{"6", -300, 1},
	}
	for _, c := range cases {
		query := "type=" + c.typ +
			"&start_timestamp=" + strconv.FormatInt(base, 10) +
			"&end_timestamp=" + strconv.FormatInt(base+3600, 10)

		// 页面消耗额度
		statRec := callExportHandler(t, GetLogsSelfStat, 9601, query)
		var statResp map[string]any
		require.NoError(t, common.UnmarshalJsonStr(statRec.Body.String(), &statResp))
		require.Equal(t, true, statResp["success"])
		data, _ := statResp["data"].(map[string]any)
		quota, _ := data["quota"].(float64)
		require.Equal(t, c.wantQuota, int(quota), "type=%s 页面消耗额度", c.typ)

		// 导出表格的费用合计与行数
		exportRec := callExportHandler(t, ExportUserLogs, 9601, query)
		costSum, rowCount := sumCostColumn(t, exportRec.Body.String())
		require.Equal(t, c.wantRows, rowCount, "type=%s 导出行数", c.typ)
		expectCost := float64(c.wantQuota) / common.QuotaPerUnit * operation_setting.USDExchangeRate
		require.InDelta(t, expectCost, costSum, 1e-5, "type=%s 表格费用合计应等于页面消耗额度", c.typ)
	}
}
