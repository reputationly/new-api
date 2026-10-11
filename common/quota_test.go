package common

import (
	"math"
	"testing"
)

// QuotaPerUnit = 7.3×10⁹（默认）时上界就是 1e9 美元对应的额度。
func TestMaxTokenQuotaAtDefaultQuotaPerUnit(t *testing.T) {
	original := QuotaPerUnit
	QuotaPerUnit = 7.3e9
	t.Cleanup(func() { QuotaPerUnit = original })

	if got, want := MaxTokenQuota(), int64(7300000000000000000); got != want {
		t.Fatalf("MaxTokenQuota() = %d，期望 %d", got, want)
	}
}

// QPU 被运营调大后（例如按汇率 9.3 调美元换算，QPU > 9.22e9）必须饱和到 MaxInt64，
// 而不是像 int(1e9 × QPU) 那样越界成负数（负上界会把所有正额度拒掉）。
func TestMaxTokenQuotaSaturatesWhenQuotaPerUnitIsLarge(t *testing.T) {
	original := QuotaPerUnit
	t.Cleanup(func() { QuotaPerUnit = original })

	for _, qpu := range []float64{9.3e9, 1e10, 1e12} {
		QuotaPerUnit = qpu
		got := MaxTokenQuota()
		if got != math.MaxInt64 {
			t.Fatalf("QuotaPerUnit=%v 时 MaxTokenQuota() = %d，期望 MaxInt64", qpu, got)
		}
		if got <= 0 {
			t.Fatalf("QuotaPerUnit=%v 时上界不能是非正数，得到 %d", qpu, got)
		}
	}
}
