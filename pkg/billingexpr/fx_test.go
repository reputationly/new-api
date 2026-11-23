package billingexpr

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

// fx 是「本模型的源货币汇率」：表达式写美元系数，结算时乘以它。
func TestRunExprWithFX(t *testing.T) {
	expr := `v2:tier("base", p * 1.5 * fx + c * 7.5 * fx)`
	params := TokenParams{P: 1000000, C: 0, Len: 1000000, FX: 6.8}
	cost, trace, err := RunExpr(expr, params)
	require.NoError(t, err)
	// p=1e6 tokens × $1.5/百万 × fx 6.8 = ¥10.2（cost 的单位是 ¥×1e6）
	require.InDelta(t, 10.2, cost/1e6, 1e-9)
	require.Equal(t, "base", trace.MatchedTier)

	// 汇率翻倍，价格翻倍，档位不变
	params.FX = 13.6
	cost2, _, err := RunExpr(expr, params)
	require.NoError(t, err)
	require.InDelta(t, 20.4, cost2/1e6, 1e-9)
}

// 引用 fx 的表达式必须能被识别出来（保存校验与预扣费都靠它决定要不要汇率）。
func TestUsedVarsDetectsFX(t *testing.T) {
	require.True(t, UsedVars(`v2:tier("base", p * 1.5 * fx)`)["fx"])
	require.False(t, UsedVars(`v2:tier("base", p * 1.36)`)["fx"])
}

// 结算必须用快照里冻结的 fx，而不是调用方当前的汇率。
func TestComputeTieredQuotaUsesSnapshotFX(t *testing.T) {
	expr := `v2:tier("base", p * 1 * fx)`
	snap := &BillingSnapshot{
		BillingMode:  "tiered_expr",
		ExprString:   expr,
		ExprHash:     ExprHashString(expr),
		GroupRatio:   1,
		QuotaPerUnit: 1,
		ExprVersion:  2,
		ExprFX:       6.8,
	}
	// 调用方给了一个不同的 fx：应当被快照值覆盖
	res, err := ComputeTieredQuotaWithRequest(snap, TokenParams{P: 1000000, Len: 1000000, FX: 100}, RequestInput{})
	require.NoError(t, err)
	// p=1e6 tokens × ¥1/百万 × fx 6.8 = ¥6.8 → quota = ¥6.8 × 1e9 = 6.8e9
	require.InDelta(t, 6.8e9, res.ActualQuotaBeforeGroup, 1)
}

// 额度换算的硬门槛：负值/非有限值/越界一律拒绝（0 合法，免费模型要用）。
func TestQuotaFromExprOutputRejectsInvalid(t *testing.T) {
	_, err := QuotaFromExprOutput(-1, ExprVersionV2)
	require.Error(t, err)

	_, err = QuotaFromExprOutput(math.NaN(), ExprVersionV2)
	require.Error(t, err)

	_, err = QuotaFromExprOutput(math.Inf(1), ExprVersionV2)
	require.Error(t, err)

	_, err = QuotaFromExprOutput(1e13, ExprVersionV2) // 越界：1e13/1e6*1e9 = 1e16 > 2^53
	require.Error(t, err)

	quota, err := QuotaFromExprOutput(0, ExprVersionV2)
	require.NoError(t, err)
	require.Equal(t, float64(0), quota)
}

// 乘完分组倍率后的最终额度也要校验：分组倍率是任意浮点，1e15 会把 int64 溢出。
func TestValidateFinalQuota(t *testing.T) {
	require.NoError(t, ValidateFinalQuota(0))
	require.NoError(t, ValidateFinalQuota(1e9))

	require.Error(t, ValidateFinalQuota(-1))
	require.Error(t, ValidateFinalQuota(math.NaN()))
	require.Error(t, ValidateFinalQuota(math.Inf(1)))
	require.Error(t, ValidateFinalQuota(1e16)) // > 2^53
}
