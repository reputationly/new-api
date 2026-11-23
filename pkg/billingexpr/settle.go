package billingexpr

import (
	"errors"
	"fmt"
	"math"

	"github.com/QuantumNous/new-api/common"
)

// maxSafeQuota 是允许落库的额度上限（quota 以 int 存储）。
// 超过它的值基本可以断定是表达式配错了（比如把「美元价」当「人民币价」写、
// 或者少了 /1e6），继续放行会让 int 溢出成负数。
const maxSafeQuota = float64(1 << 53)

// ErrExprV1Unsupported v1 表达式（系数是美元/百万 tokens）在 1 quota = ¥1e-9 之后
// 无法与余额同单位结算：它的输出单位是旧单位（1 quota = ¥1e-6），照原样结算会少收 1000 倍。
var ErrExprV1Unsupported = errors.New(
	"分段表达式 v1（美元口径）已不再支持结算，请在「定价设置 → 表达式」里重新录入为 v2（系数为 ¥/百万 tokens）")

// QuotaFromExprOutput 把表达式输出（系数 × tokens）按版本换算成 quota。
//
//   - v2：系数是 ¥/百万 tokens，人民币金额 × QuotaPerYuan。
//   - v1：系数是美元/百万 tokens，输出单位是旧单位，与当前余额不同单位 → 拒绝结算。
func QuotaFromExprOutput(exprOutput float64, version int) (float64, error) {
	// 非法金额一律拒绝，绝不能返回给结算层：service/quota.go 的 PostConsumeQuota
	// 对 quota <= 0 走的是 IncreaseUserQuota（退/加额度），一个负的表达式金额
	// 等于凭空给用户充值。0 是合法的（免费模型），所以只拦负数与非有限值。
	if math.IsNaN(exprOutput) || math.IsInf(exprOutput, 0) {
		return 0, fmt.Errorf("计费表达式算出了非法金额（NaN/Inf），请检查表达式与汇率")
	}
	if exprOutput < 0 {
		return 0, fmt.Errorf(
			"计费表达式算出了负金额 %.6f：表达式在这组 token/汇率下为负，拒绝结算", exprOutput)
	}
	switch version {
	case ExprVersionV2:
		quota := exprOutput / 1_000_000 * common.QuotaPerYuan
		if math.IsNaN(quota) || math.IsInf(quota, 0) || quota > maxSafeQuota {
			return 0, fmt.Errorf("计费表达式换算后的额度越界（%.6g），请检查表达式系数", quota)
		}
		return quota, nil
	case ExprVersionV1:
		return 0, ErrExprV1Unsupported
	default:
		return 0, fmt.Errorf("未知的分段表达式版本 %d", version)
	}
}

// ValidateFinalQuota 校验**乘完分组倍率之后**的最终额度。
//
// 只检查乘倍率之前是不够的：分组倍率是管理员可配的任意浮点数，1e15 这种值
// 与额度相乘后会把 int64 溢出成 MinInt64（本地 Go 实测），再往下就是一次
// 巨额退/扣款。所以最终落库前再校验一次。
func ValidateFinalQuota(quota float64) error {
	if math.IsNaN(quota) || math.IsInf(quota, 0) {
		return fmt.Errorf("最终额度非法（NaN/Inf），请检查表达式与分组倍率")
	}
	if quota < 0 {
		return fmt.Errorf("最终额度为负（%.6f），拒绝结算", quota)
	}
	if quota > maxSafeQuota {
		return fmt.Errorf("最终额度越界（%.6g），请检查分组倍率或表达式系数", quota)
	}
	return nil
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
	// fx 以**快照**为准：预扣时冻结的那个值才是这笔请求的成交价依据。
	// 运营在请求在途时改汇率，不能让同一笔请求预扣与结算各算一个价。
	if snap.ExprFX > 0 {
		params.FX = snap.ExprFX
	}
	cost, trace, err := RunExprByHashWithRequest(snap.ExprString, snap.ExprHash, params, request)
	if err != nil {
		return TieredResult{}, err
	}

	quotaBeforeGroup, err := quotaConversion(cost, snap)
	if err != nil {
		return TieredResult{}, err
	}
	afterGroupRaw := quotaBeforeGroup * snap.GroupRatio
	if err := ValidateFinalQuota(afterGroupRaw); err != nil {
		return TieredResult{}, err
	}
	afterGroup := QuotaRound(afterGroupRaw)
	crossed := trace.MatchedTier != snap.EstimatedTier

	return TieredResult{
		ActualQuotaBeforeGroup: quotaBeforeGroup,
		ActualQuotaAfterGroup:  afterGroup,
		MatchedTier:            trace.MatchedTier,
		CrossedTier:            crossed,
	}, nil
}
