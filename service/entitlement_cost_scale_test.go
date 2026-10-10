package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/require"
)

// 套餐权益成本估算的 UnitQuota 与记账侧同口径：单价(美元) × QuotaPerUnit × 成本比。
// 此前是 int64() 截断，¥0.1 的按次价在 QPU = 7300000 下会算成 99999。
func TestEstimateModelCostRoundsUnitQuota(t *testing.T) {
	original := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = original })
	common.QuotaPerUnit = 7300000

	pricing := []model.Pricing{
		{ModelName: "rounding-model", QuotaType: 1, ModelPrice: 0.1 / 7.3},
	}

	item := estimateModelCost(pricing, fixedCost(1), noChannels(), "rounding-model", []int{1})
	require.True(t, item.Resolvable)
	require.Equal(t, int64(100000), item.UnitQuota)

	common.QuotaPerUnit = 500000
	item = estimateModelCost(pricing, fixedCost(1), noChannels(), "rounding-model", []int{1})
	require.Equal(t, int64(6849), item.UnitQuota)
}
