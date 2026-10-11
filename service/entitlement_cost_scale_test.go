package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/require"
)

// 套餐权益成本估算的 UnitQuota 与记账侧同口径：单价(¥) × QuotaPerYuan × 成本比。
//
// 该路径此前是 int64() 截断（旧单位下 ¥0.1 会算成 99999），现在必须四舍五入，
// 且不读 QuotaPerUnit —— 人民币单价 × 常量 1e8 即可。
func TestEstimateModelCostRoundsUnitQuota(t *testing.T) {
	original := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = original })

	pricing := []model.Pricing{
		{ModelName: "rounding-model", QuotaType: 1, ModelPrice: 0.1}, // ¥0.1/次
	}

	for _, qpu := range []float64{7_300_000, 730_000_000} {
		common.QuotaPerUnit = qpu
		item := estimateModelCost(pricing, fixedCost(1), noChannels(), "rounding-model", []int{1})
		require.True(t, item.Resolvable)
		require.Equal(t, int64(10_000_000), item.UnitQuota, "QuotaPerUnit=%v", qpu)
	}
}

// 取整方向：¥0.099999999 × 1e8 = 9,999,999.9 → 10,000,000（不得截断成 9,999,999）。
func TestEstimateModelCostRoundsFractionalUnitQuota(t *testing.T) {
	pricing := []model.Pricing{
		{ModelName: "fractional-model", QuotaType: 1, ModelPrice: 0.099999999},
	}
	item := estimateModelCost(pricing, fixedCost(1), noChannels(), "fractional-model", []int{1})
	require.True(t, item.Resolvable)
	require.Equal(t, int64(10_000_000), item.UnitQuota)
}
