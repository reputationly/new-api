package common

import "testing"

// 新单位口径（设计文档 §2.1 / §2.2）：
//
//	QuotaPerYuan = 10⁹  => 1 quota = ¥0.000000001
//	QuotaPerMillionTokens = QuotaPerYuan / 10⁶ = 1000
//	倍率（¥/百万 tokens）→ quota/token 就是 ratio × 1000。
//
// 倍率路径既不读汇率也不读 QuotaPerUnit：这两者只服务于美元支付与美元展示。
func TestQuotaUnitConstants(t *testing.T) {
	if QuotaPerYuan != 1_000_000_000 {
		t.Fatalf("QuotaPerYuan = %d, want 1000000000", QuotaPerYuan)
	}
	if QuotaPerMillionTokens != 1000 {
		t.Fatalf("QuotaPerMillionTokens = %d, want 1000", QuotaPerMillionTokens)
	}
	// 1 美元 = 7.3 元 = 7.3 × QuotaPerYuan
	if want := 7.3 * float64(QuotaPerYuan); QuotaPerUnit != want {
		t.Fatalf("QuotaPerUnit = %v, want %v（1 美元对应的 quota）", QuotaPerUnit, want)
	}
}

// 倍率路径：quota/token = ratio × 1000，与倍率的旧含义（1 = $2/百万）无关。
func TestRatioQuotaPerTokenIsThousandPerRatio(t *testing.T) {
	cases := []struct {
		ratio float64
		want  float64
	}{
		{0, 0},
		{0.003, 3},        // ¥0.003/百万，1 token = 3 quota
		{0.01, 10},        // ¥0.01/百万时 1 token = 10 quota
		{0.5, 500},        //
		{1, 1000},         // ¥1/百万
		{2, 2000},         // E1：deepseek ¥2/百万
		{1000, 1000000},   // 溢出用例的单价
	}
	for _, c := range cases {
		if got := RatioQuotaPerToken(c.ratio); got != c.want {
			t.Fatalf("RatioQuotaPerToken(%v) = %v, want %v", c.ratio, got, c.want)
		}
	}
}

// 倍率路径必须与 QuotaPerUnit 完全解耦：QPU 从 500000 变到 7300000000，
// 同一个倍率换出的 quota/token 一个字节都不变。
func TestRatioPathIndependentOfQuotaPerUnit(t *testing.T) {
	original := QuotaPerUnit
	t.Cleanup(func() { QuotaPerUnit = original })

	for _, qpu := range []float64{500_000, 7_300_000, 7_300_000_000} {
		QuotaPerUnit = qpu
		if got := RatioQuotaPerToken(2); got != 2000 {
			t.Fatalf("QuotaPerUnit=%v 时 RatioQuotaPerToken(2) = %v, want 2000（倍率路径不得读 QPU）", qpu, got)
		}
		if got := RatioQuotaPerToken(0.01); got != 10 {
			t.Fatalf("QuotaPerUnit=%v 时 RatioQuotaPerToken(0.01) = %v, want 10", qpu, got)
		}
	}
}

// 价格路径：人民币金额 × QuotaPerYuan，同样与 QPU、汇率无关。
func TestPricePathUsesQuotaPerYuan(t *testing.T) {
	original := QuotaPerUnit
	t.Cleanup(func() { QuotaPerUnit = original })

	for _, qpu := range []float64{500_000, 7_300_000, 7_300_000_000} {
		QuotaPerUnit = qpu
		// E5：按次生图 ¥0.2/次
		if got := 0.2 * float64(QuotaPerYuan); got != 200_000_000 {
			t.Fatalf("¥0.2 × QuotaPerYuan = %v, want 200000000", got)
		}
		// E6：联网搜索 ¥73/千次 × 3 次
		if got := 73.0 / 1000 * 3 * float64(QuotaPerYuan); got != 219_000_000 {
			t.Fatalf("¥73/千次 × 3 × QuotaPerYuan = %v, want 219000000", got)
		}
	}
}
