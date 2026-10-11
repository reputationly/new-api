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

// 渠道测试的日志是真扣费（type 2、写进使用日志），倍率路径必须用新口径：
// quota = tokens × 倍率 × 100（倍率是 ¥/百万 tokens），且与 QuotaPerUnit 无关。
func TestSettleTestQuotaRatioPathUsesHundredPerRatio(t *testing.T) {
	info := &relaycommon.RelayInfo{}
	priceData := types.PriceData{ModelRatio: 1, CompletionRatio: 4}
	usage := &dto.Usage{PromptTokens: 1000}

	for _, qpu := range []float64{500_000, 7_300_000, 730_000_000} {
		setQuotaPerUnit(t, qpu)
		quota, _ := settleTestQuota(info, priceData, usage)
		require.Equal(t, 100_000, quota, "1000 tokens × ¥1/百万 × 100，且不随 QPU 变化")
	}
}

// 按次分支：quota = 人民币价 × QuotaPerYuan，四舍五入（此前 int() 截断会少收 1 quota）。
func TestSettleTestQuotaPerCallUsesQuotaPerYuan(t *testing.T) {
	info := &relaycommon.RelayInfo{}
	usage := &dto.Usage{PromptTokens: 1000}

	for _, qpu := range []float64{7_300_000, 730_000_000} {
		setQuotaPerUnit(t, qpu)
		quota, _ := settleTestQuota(info, types.PriceData{UsePrice: true, ModelPrice: 0.1}, usage)
		require.Equal(t, 10_000_000, quota, "¥0.1 × 10⁸，且不随 QPU 变化")
	}

	quota, _ := settleTestQuota(info, types.PriceData{UsePrice: true, ModelPrice: 0.099999999}, usage)
	require.Equal(t, 10_000_000, quota, "9,999,999.9 必须四舍五入")
}
