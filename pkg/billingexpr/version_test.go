package billingexpr_test

import (
	"errors"
	"math"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
)

// 分段表达式版本口径（设计文档 §12.2 R2 / B4）：
//
//	v2：系数是 ¥/百万 tokens，quota = output / 10⁶ × QuotaPerYuan（= output × 100）；
//	v1：系数是美元/百万 tokens，输出单位与当前余额不同单位 → **拒绝结算**。
//
// 无前缀按 v1 处理（兼容历史数据），新录入必须带 v2。
func TestParseExprVersion(t *testing.T) {
	cases := []struct {
		expr        string
		wantVersion int
		wantBody    string
	}{
		{`v2:tier("a", p)`, billingexpr.ExprVersionV2, `tier("a", p)`},
		{`v1:tier("a", p)`, billingexpr.ExprVersionV1, `tier("a", p)`},
		{`tier("a", p)`, billingexpr.ExprVersionV1, `tier("a", p)`}, // 无前缀 = v1
	}
	for _, c := range cases {
		version, body := billingexpr.ParseExprVersion(c.expr)
		if version != c.wantVersion || body != c.wantBody {
			t.Fatalf("ParseExprVersion(%q) = (%d, %q), want (%d, %q)",
				c.expr, version, body, c.wantVersion, c.wantBody)
		}
	}
}

// v2 的换算：¥/百万 tokens × tokens → quota。
func TestQuotaFromExprOutputV2(t *testing.T) {
	cases := []struct {
		output float64
		want   float64
	}{
		{0, 0},
		{1, 100},                    // ¥1/百万 的系数 × 1
		{3000, 300_000},             // 1000 tokens × ¥3/百万 = ¥0.003
		{46_000_000, 4_600_000_000}, // 1e6 tokens × ¥46/百万
	}
	for _, c := range cases {
		got, err := billingexpr.QuotaFromExprOutput(c.output, billingexpr.ExprVersionV2)
		if err != nil {
			t.Fatalf("v2 换算不该报错：%v", err)
		}
		if math.Abs(got-c.want) > 1e-6 {
			t.Fatalf("QuotaFromExprOutput(%v, v2) = %v, want %v", c.output, got, c.want)
		}
	}
	// 与常量自洽
	got, _ := billingexpr.QuotaFromExprOutput(1_000_000, billingexpr.ExprVersionV2)
	if got != float64(common.QuotaPerYuan) {
		t.Fatalf("1e6 系数 = ¥1 → %v quota, want %d", got, common.QuotaPerYuan)
	}
}

// v1 表达式（含无前缀）一律拒绝结算，返回可读错误。
func TestQuotaFromExprOutputV1Rejected(t *testing.T) {
	for _, version := range []int{billingexpr.ExprVersionV1, billingexpr.DefaultExprVersion} {
		_, err := billingexpr.QuotaFromExprOutput(1000, version)
		if !errors.Is(err, billingexpr.ErrExprV1Unsupported) {
			t.Fatalf("version=%d：err = %v, want ErrExprV1Unsupported", version, err)
		}
	}
}

// 未知版本也要明确报错，不能静默按某个口径算钱。
func TestQuotaFromExprOutputUnknownVersion(t *testing.T) {
	if _, err := billingexpr.QuotaFromExprOutput(1, 99); err == nil {
		t.Fatal("未知版本必须返回错误")
	}
}

// 端到端：v2 快照正常结算，v1 快照被 TryTieredSettle 的路径拒绝（这里直接验证
// ComputeTieredQuota 返回可读错误）。
func TestComputeTieredQuotaRejectsV1Snapshot(t *testing.T) {
	snap := &billingexpr.BillingSnapshot{
		BillingMode:   "tiered_expr",
		ExprString:    `tier("base", p * 1.5)`,
		ExprHash:      billingexpr.ExprHashString(`tier("base", p * 1.5)`),
		GroupRatio:    1.0,
		QuotaPerUnit:  500_000,
		ExprVersion:   billingexpr.ExprVersionV1,
		EstimatedTier: "base",
	}
	_, err := billingexpr.ComputeTieredQuota(snap, billingexpr.TokenParams{P: 1000})
	if !errors.Is(err, billingexpr.ErrExprV1Unsupported) {
		t.Fatalf("v1 快照结算应被拒绝，err = %v", err)
	}
}

// ExprVersion 按表达式前缀返回版本（已编译过与未编译过两条路径）。
func TestExprVersionFromString(t *testing.T) {
	if v := billingexpr.ExprVersion(`v2:tier("a", p)`); v != billingexpr.ExprVersionV2 {
		t.Fatalf("v2 前缀：ExprVersion = %d", v)
	}
	if v := billingexpr.ExprVersion(`tier("a", p)`); v != billingexpr.ExprVersionV1 {
		t.Fatalf("无前缀：ExprVersion = %d", v)
	}
	if v := billingexpr.ExprVersion(""); v != billingexpr.DefaultExprVersion {
		t.Fatalf("空串：ExprVersion = %d", v)
	}
}

// v2 快照的端到端结算：p × 3（¥/百万）× 1000 tokens = ¥0.003 = 300,000 quota。
func TestComputeTieredQuotaV2Success(t *testing.T) {
	const expr = `v2:tier("base", p * 3)`
	snap := &billingexpr.BillingSnapshot{
		BillingMode:   "tiered_expr",
		ExprString:    expr,
		ExprHash:      billingexpr.ExprHashString(expr),
		GroupRatio:    1.0,
		QuotaPerUnit:  500_000, // v2 快照不读它
		ExprVersion:   billingexpr.ExprVersionV2,
		EstimatedTier: "base",
	}
	result, err := billingexpr.ComputeTieredQuota(snap, billingexpr.TokenParams{P: 1000})
	if err != nil {
		t.Fatalf("v2 结算不该报错：%v", err)
	}
	if result.ActualQuotaAfterGroup != 300_000 {
		t.Fatalf("quota = %d, want 300000", result.ActualQuotaAfterGroup)
	}
	if result.MatchedTier != "base" {
		t.Fatalf("tier = %q, want base", result.MatchedTier)
	}
}
