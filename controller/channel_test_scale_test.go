package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"

	"github.com/stretchr/testify/require"
)

func setQuotaPerUnit(t *testing.T, qpu float64) {
	t.Helper()
	original := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = original })
	common.QuotaPerUnit = qpu
}

// 渠道测试的日志是真扣费（type 2、写进使用日志），倍率路径必须跟着单位走。
func TestSettleTestQuotaRatioPathScalesWithQuotaPerUnit(t *testing.T) {
	info := &relaycommon.RelayInfo{}
	priceData := types.PriceData{ModelRatio: 1, CompletionRatio: 4}
	usage := &dto.Usage{PromptTokens: 1000}

	setQuotaPerUnit(t, 500000)
	quota, _ := settleTestQuota(info, priceData, usage)
	require.Equal(t, 1000, quota)

	setQuotaPerUnit(t, 7300000)
	quota, _ = settleTestQuota(info, priceData, usage)
	require.Equal(t, 14600, quota)
}

// 按次分支此前是 int(price × QPU)：¥0.1 的按次价在 QPU = 7300000 下落在
// 99999.9999…，截断会少收 1 quota。
func TestSettleTestQuotaPerCallRoundsUp(t *testing.T) {
	info := &relaycommon.RelayInfo{}
	priceData := types.PriceData{UsePrice: true, ModelPrice: 0.1 / 7.3}
	usage := &dto.Usage{PromptTokens: 1000}

	setQuotaPerUnit(t, 7300000)
	quota, _ := settleTestQuota(info, priceData, usage)
	require.Equal(t, 100000, quota)
}
