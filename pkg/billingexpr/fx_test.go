package billingexpr

import (
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
