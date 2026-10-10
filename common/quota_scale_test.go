package common

import "testing"

// QuotaScale 是倍率路径与 QuotaPerUnit 解耦的唯一入口：QPU = RatioQuotaBase 时 scale = 1
// （旧单位下行为不变），QPU = 7300000 时 scale = 14.6（1 quota = ¥0.000001）。
func TestQuotaScale(t *testing.T) {
	original := QuotaPerUnit
	t.Cleanup(func() { QuotaPerUnit = original })

	cases := []struct {
		qpu  float64
		want float64
	}{
		{500000, 1},
		{7300000, 14.6},
	}
	for _, c := range cases {
		QuotaPerUnit = c.qpu
		if got := QuotaScale(); got != c.want {
			t.Fatalf("QuotaScale() with QuotaPerUnit=%v = %v, want %v", c.qpu, got, c.want)
		}
	}
}

// RatioQuotaBase 必须与"倍率 1 = $2 / 1M tokens"自洽：ratio=1 时 1 token 值 $2/1e6，
// 乘上 RatioQuotaBase quota/USD 正好是 1 quota/token（这正是倍率路径能直接把 ratio
// 当 quota/token 用的前提）。
func TestRatioQuotaBaseMatchesTwoDollarsPerMillion(t *testing.T) {
	if want := 1.0; RatioQuotaBase*2.0/1_000_000.0 != want {
		t.Fatalf("RatioQuotaBase*2/1e6 = %v, want %v", RatioQuotaBase*2.0/1_000_000.0, want)
	}
}
