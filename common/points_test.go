package common

import "testing"

// 换算依赖默认 QuotaPerPoint = QuotaPerUnit/730 ≈ 684.93（1 积分 = 1 分钱；common 测试
// 二进制不 import operation_setting，QuotaPerPointFunc 为 nil，走默认值，结果确定）。
// 发放侧 PointsToQuota 用 Ceil：保证 QuotaToPoints(PointsToQuota(n)) == n 精确往返。

func TestPointsQuota_Zero(t *testing.T) {
	if got := PointsToQuota(0); got != 0 {
		t.Fatalf("PointsToQuota(0) = %d, want 0", got)
	}
	if got := QuotaToPoints(0); got != 0 {
		t.Fatalf("QuotaToPoints(0) = %d, want 0", got)
	}
	if got := QuotaToPointsCeil(0); got != 0 {
		t.Fatalf("QuotaToPointsCeil(0) = %d, want 0", got)
	}
}

func TestPointsToQuota_KnownValues(t *testing.T) {
	// 仅当默认基线（QuotaPerUnit=500000）时断言具体值，避免基线变动导致脆断言。
	if QuotaPerUnit != 500*1000.0 {
		t.Skipf("QuotaPerUnit=%v 非默认基线，跳过具体值断言", QuotaPerUnit)
	}
	// 1 积分 = ceil(684.9315...) = 685
	if got := PointsToQuota(1); got != 685 {
		t.Fatalf("PointsToQuota(1) = %d, want 685", got)
	}
	// 100 积分 = ceil(68493.15...) = 68494
	if got := PointsToQuota(100); got != 68494 {
		t.Fatalf("PointsToQuota(100) = %d, want 68494", got)
	}
	// ceil 发放后 floor 展示精确还原
	if got := QuotaToPoints(685); got != 1 {
		t.Fatalf("QuotaToPoints(685) = %d, want 1", got)
	}
	if got := QuotaToPoints(68494); got != 100 {
		t.Fatalf("QuotaToPoints(68494) = %d, want 100", got)
	}
}

// QuotaToPointsCeil：消费结算取整方向——不足 1 积分按 1 积分。
func TestQuotaToPointsCeil(t *testing.T) {
	if QuotaPerUnit != 500*1000.0 {
		t.Skipf("QuotaPerUnit=%v 非默认基线，跳过具体值断言", QuotaPerUnit)
	}
	// 1 quota unit（远不足 1 积分）→ 1 积分
	if got := QuotaToPointsCeil(1); got != 1 {
		t.Fatalf("QuotaToPointsCeil(1) = %d, want 1", got)
	}
	// 1000 unit ≈ 1.46 积分 → 2 积分
	if got := QuotaToPointsCeil(1000); got != 2 {
		t.Fatalf("QuotaToPointsCeil(1000) = %d, want 2", got)
	}
	// 685 unit ≈ 1.0001 积分（略超 1 积分）→ 2 积分（纯 ceil，按规则多烧）
	if got := QuotaToPointsCeil(685); got != 2 {
		t.Fatalf("QuotaToPointsCeil(685) = %d, want 2", got)
	}
	// 负数/0 → 0
	if got := QuotaToPointsCeil(-5); got != 0 {
		t.Fatalf("QuotaToPointsCeil(-5) = %d, want 0", got)
	}
}

// QuotaToPoints 对正 quota 永不返回负数。
func TestQuotaToPoints_NeverNegative(t *testing.T) {
	for _, q := range []int{1, 684, 685, 686, 1000, 684930, 1_000_000_000} {
		if got := QuotaToPoints(q); got < 0 {
			t.Fatalf("QuotaToPoints(%d) = %d, want >= 0", q, got)
		}
	}
}

// 往返恒等：ceil 发放 + floor 展示，QuotaToPoints(PointsToQuota(p)) == p 精确成立，
// 大额 10 亿积分不溢出。
func TestPointsQuota_RoundTrip(t *testing.T) {
	for _, p := range []int{1, 2, 10, 100, 1000, 12345, 1_000_000, 1_000_000_000} {
		q := PointsToQuota(p)
		if q <= 0 {
			t.Fatalf("PointsToQuota(%d) = %d, want > 0", p, q)
		}
		back := QuotaToPoints(q)
		if back != p {
			t.Fatalf("round-trip p=%d: PointsToQuota=%d QuotaToPoints=%d, want exactly %d", p, q, back, p)
		}
	}
}

// PointsToQuota 单调不减：积分越多，换算 quota 不减少。
func TestPointsToQuota_Monotonic(t *testing.T) {
	prev := -1
	for _, p := range []int{0, 1, 2, 5, 10, 50, 100, 1000, 100000} {
		q := PointsToQuota(p)
		if q < prev {
			t.Fatalf("PointsToQuota not monotonic at p=%d: %d < prev %d", p, q, prev)
		}
		prev = q
	}
}
