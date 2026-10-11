package common

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 精确往返是这套换算的核心契约：配多少点，用户就该看到多少点。
//
// 发放用 ceil、展示用 floor，两者方向相反正是为了这个——若发放改用 round，
// 约一半概率向下，叠加展示侧 floor 就会出现「套餐配 50000 点、页面显示 49999」。
func TestComputePointsRoundTrip(t *testing.T) {
	for _, n := range []int{1, 7, 100, 999, 1000, 15000, 50000, 200000, 1_000_000} {
		q := ComputePointsToQuota(n)
		require.Equal(t, n, QuotaToComputePoints(q),
			"精确往返失败：配 %d 点换算成 quota 再换回来不是 %d", n, n)
	}
}

func TestComputePointsToQuota_Zero(t *testing.T) {
	require.Equal(t, 0, ComputePointsToQuota(0))
	require.Equal(t, 0, QuotaToComputePoints(0))
}

// 展示向下取整、结算向上取整：方向相反且都不可颠倒。
//
// 展示侧若用 ceil，用户会看到自己没有的点数；结算侧若用 floor，不足 1 点的消耗
// 会被抹成 0，无限次小额调用可以白嫖。
func TestComputePointsRounding(t *testing.T) {
	qpc := getQuotaPerComputePoint() // 1e6：1 算力点 = 1 分钱
	half := int(qpc / 2)             // 半个算力点：严格小于 1 点对应的 quota

	require.Equal(t, 0, QuotaToComputePoints(half), "展示：不足 1 点显示 0")
	require.Equal(t, 1, QuotaToComputePointsCeil(half), "结算：不足 1 点按 1 点计")

	// 换算率是整数 1e6 时存在精确边界：qpc 恰好是「1 点」的权威 quota。
	full := ComputePointsToQuota(2) // 2 点对应的权威 quota
	require.Equal(t, 2, QuotaToComputePoints(full), "floor 应恰好还原发放的点数")
	require.GreaterOrEqual(t, QuotaToComputePointsCeil(full), 2,
		"ceil 不得把已发放的点数向下抹")
	require.Equal(t, 1, QuotaToComputePoints(int(qpc)), "1e6 quota = ¥0.01 = 恰好 1 点")
}

// 换算率可配：改面值后换算结果同步变化，而不是读到编译期常量。
func TestComputePoints_RespectsConfiguredRate(t *testing.T) {
	orig := QuotaPerComputePointFunc
	t.Cleanup(func() { QuotaPerComputePointFunc = orig })

	// 1 元 = 1000 点（面值缩小 10 倍）
	QuotaPerComputePointFunc = func() float64 { return float64(QuotaPerYuan) / 1000.0 }

	q := ComputePointsToQuota(1000)
	require.Equal(t, 1000, QuotaToComputePoints(q), "改面值后往返仍须精确")

	// 同样的 quota，在更小的面值下换出更多点数
	fixed := ComputePointsToQuota(100)
	QuotaPerComputePointFunc = orig
	require.Less(t, QuotaToComputePoints(fixed), 100,
		"面值放大后，同一笔 quota 换出的点数应减少")
}

// 元 → 点的预览：运营配套餐时看到的就是这个数。
func TestYuanToComputePoints(t *testing.T) {
	origQpc := QuotaPerComputePointFunc
	t.Cleanup(func() { QuotaPerComputePointFunc = origQpc })

	QuotaPerComputePointFunc = nil // 自动：QuotaPerYuan / 100 = 1e6

	require.Equal(t, 100, YuanToComputePoints(1), "默认面值下 1 元 = 100 点")
	require.Equal(t, 29900, YuanToComputePoints(299), "¥299 套餐 ≈ 29900 点")
	require.Equal(t, 0, YuanToComputePoints(0))
	require.Equal(t, 0, YuanToComputePoints(-5), "负数不产生点数")
}

// 算力点面值固定为「1 点 = 1 分钱」，即换算率 = QuotaPerYuan / 100 = 10⁶。
//
// 这条不变量只依赖常量：改汇率不改它，改 QuotaPerUnit 也不改它——这正是
// 「不建独立价目表」的关键收益（模型消耗多少点只取决于 token × 倍率 × 分组）。
func TestComputePoints_RateIsFixedOneCentPerPoint(t *testing.T) {
	origQPU := QuotaPerUnit
	origQpc := QuotaPerComputePointFunc
	t.Cleanup(func() {
		QuotaPerUnit = origQPU
		QuotaPerComputePointFunc = origQpc
	})
	QuotaPerComputePointFunc = nil

	for _, qpu := range []float64{500_000, 7_300_000, 730_000_000} {
		QuotaPerUnit = qpu
		require.Equal(t, 1_000_000.0, DefaultQuotaPerComputePoint(),
			"QuotaPerUnit=%v 时换算率仍应是 10⁶（1 点 = 1 分钱）", qpu)
		require.Equal(t, 100, YuanToComputePoints(1),
			"QuotaPerUnit=%v 时 1 元仍应是 100 点", qpu)
		require.Equal(t, 1, QuotaToComputePoints(1_000_000),
			"QuotaPerUnit=%v 时 10⁶ quota = ¥0.01 仍应是 1 点（1 quota = ¥10⁻⁸）", qpu)
	}
}
