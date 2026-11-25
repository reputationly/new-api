package common

import "testing"

// QuotaPerUnit = 7.3×10⁹（默认）时上界就是 1e9 美元对应的额度。
func TestMaxTokenQuotaAtDefaultQuotaPerUnit(t *testing.T) {
	original := QuotaPerUnit
	QuotaPerUnit = 7.3e9
	t.Cleanup(func() { QuotaPerUnit = original })

	if got, want := MaxTokenQuota(), int64(7300000000000000000); got != want {
		t.Fatalf("MaxTokenQuota() = %d，期望 %d", got, want)
	}
}

func TestMaxTokenQuotaDoesNotChangeWithLegacyQuotaPerUnit(t *testing.T) {
	original := QuotaPerUnit
	t.Cleanup(func() { QuotaPerUnit = original })

	for _, qpu := range []float64{9.3e9, 1e10, 1e12} {
		QuotaPerUnit = qpu
		got := MaxTokenQuota()
		if got != 7_300_000_000_000_000_000 {
			t.Fatalf("QuotaPerUnit=%v 时 MaxTokenQuota() = %d，期望固定上限", qpu, got)
		}
		if got <= 0 {
			t.Fatalf("QuotaPerUnit=%v 时上界不能是非正数，得到 %d", qpu, got)
		}
	}
}
