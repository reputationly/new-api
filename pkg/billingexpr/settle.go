package billingexpr

import (
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
)

// ErrExprV1Unsupported v1 表达式（系数是美元/百万 tokens）在 1 quota = ¥1e-9 之后
// 无法与余额同单位结算：它的输出单位是旧单位（1 quota = ¥1e-6），照原样结算会少收 1000 倍。
var ErrExprV1Unsupported = errors.New(
	"分段表达式 v1（美元口径）已不再支持结算，请在「定价设置 → 表达式」里重新录入为 v2（系数为 ¥/百万 tokens）")

// QuotaFromExprOutput 把表达式输出（系数 × tokens）按版本换算成 quota。
//
//   - v2：系数是 ¥/百万 tokens，人民币金额 × QuotaPerYuan。
//   - v1：系数是美元/百万 tokens，输出单位是旧单位，与当前余额不同单位 → 拒绝结算。
func QuotaFromExprOutput(exprOutput float64, version int) (float64, error) {
	switch version {
	case ExprVersionV2:
		return exprOutput / 1_000_000 * common.QuotaPerYuan, nil
	case ExprVersionV1:
		return 0, ErrExprV1Unsupported
	default:
		return 0, fmt.Errorf("未知的分段表达式版本 %d", version)
	}
}

// quotaConversion converts raw expression output to quota based on the
// expression version. This is the central dispatch point for future versions
// that may use a different conversion formula.
func quotaConversion(exprOutput float64, snap *BillingSnapshot) (float64, error) {
	return QuotaFromExprOutput(exprOutput, snap.ExprVersion)
}

// ComputeTieredQuota runs the Expr from a frozen BillingSnapshot against
// actual token counts and returns the settlement result.
func ComputeTieredQuota(snap *BillingSnapshot, params TokenParams) (TieredResult, error) {
	return ComputeTieredQuotaWithRequest(snap, params, RequestInput{})
}

func ComputeTieredQuotaWithRequest(snap *BillingSnapshot, params TokenParams, request RequestInput) (TieredResult, error) {
	cost, trace, err := RunExprByHashWithRequest(snap.ExprString, snap.ExprHash, params, request)
	if err != nil {
		return TieredResult{}, err
	}

	quotaBeforeGroup, err := quotaConversion(cost, snap)
	if err != nil {
		return TieredResult{}, err
	}
	afterGroup := QuotaRound(quotaBeforeGroup * snap.GroupRatio)
	crossed := trace.MatchedTier != snap.EstimatedTier

	return TieredResult{
		ActualQuotaBeforeGroup: quotaBeforeGroup,
		ActualQuotaAfterGroup:  afterGroup,
		MatchedTier:            trace.MatchedTier,
		CrossedTier:            crossed,
	}, nil
}
