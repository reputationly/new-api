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
	qpc := getQuotaPerComputePoint()
	half := int(qpc / 2) // 半个算力点：严格小于 1 点对应的 quota

	require.Equal(t, 0, QuotaToComputePoints(half), "展示：不足 1 点显示 0")
	require.Equal(t, 1, QuotaToComputePointsCeil(half), "结算：不足 1 点按 1 点计")

	// 注意：qpc 是小数（≈684.93），不存在一个整数 quota 精确等于「恰好 1 点」——
	// 684 严格不足 1 点，685 已经略超 1 点，二者的 floor/ceil 结果自然不同
	// （684: floor=0/ceil=1；685: floor=1/ceil=2），这是取整语义的必然结果，
	// 不是可以用单一断言去钉的「边界」。
	full := ComputePointsToQuota(2) // 2 点对应的权威 quota（略超，因为 ceil 发放）
	require.Equal(t, 2, QuotaToComputePoints(full), "floor 应恰好还原发放的点数")
	require.GreaterOrEqual(t, QuotaToComputePointsCeil(full), 2,
		"ceil 不得把已发放的点数向下抹")
}

// 换算率可配：改面值后换算结果同步变化，而不是读到编译期常量。
func TestComputePoints_RespectsConfiguredRate(t *testing.T) {
	orig := QuotaPerComputePointFunc
	t.Cleanup(func() { QuotaPerComputePointFunc = orig })

	// 1 元 = 1000 点（面值缩小 10 倍）
	QuotaPerComputePointFunc = func() float64 { return QuotaPerUnit / 7300.0 }

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
	origRate := USDExchangeRateFunc
	origQpc := QuotaPerComputePointFunc
	t.Cleanup(func() {
		USDExchangeRateFunc = origRate
		QuotaPerComputePointFunc = origQpc
	})
	USDExchangeRateFunc = func() float64 { return 7.3 }
	QuotaPerComputePointFunc = func() float64 { return QuotaPerUnit / 730.0 }

	require.Equal(t, 100, YuanToComputePoints(1), "默认面值下 1 元 = 100 点")
	require.Equal(t, 29900, YuanToComputePoints(299), "¥299 套餐 ≈ 29900 点")
	require.Equal(t, 0, YuanToComputePoints(0))
	require.Equal(t, 0, YuanToComputePoints(-5), "负数不产生点数")
}

// 算力点面值固定为「1 点 = 1 分钱」，即换算率 = QuotaPerUnit / (汇率 × 100)。
//
// 只要 §10.1 的不变量 QuotaPerUnit = 汇率 × 10⁶ 成立，它就恒为 10⁴：同一笔人民币消耗
// 换出同样的点数，改汇率不改变「模型消耗多少点」——这正是不建价目表的关键收益。
//
// 与旧实现的差异：旧实现把换算率在包初始化时冻结成 QuotaPerUnit/730，等价于把汇率 7.3
// 写死进常量；一旦只改汇率（不改 QPU），1 点就不再值 1 分钱。新实现跟随当前汇率，
// 面值恒定，代价是「只改汇率不改 QPU」这个 §10.1 明令禁止的配置会改变点数消耗速度。
func TestComputePoints_RateAndQuotaPerUnitInvariant(t *testing.T) {
	origRate := USDExchangeRateFunc
	origQPU := QuotaPerUnit
	t.Cleanup(func() {
		USDExchangeRateFunc = origRate
		QuotaPerUnit = origQPU
	})

	for _, c := range []struct{ rate, qpu float64 }{
		{7.3, 7_300_000},
		{8.5, 8_500_000},
		{7, 7_000_000},
	} {
		USDExchangeRateFunc = func() float64 { return c.rate }
		QuotaPerUnit = c.qpu // §10.1：改汇率必须同步改 QPU

		require.Equal(t, 10000.0, DefaultQuotaPerComputePoint(),
			"汇率 %v 下换算率应为 10⁴", c.rate)
		require.Equal(t, 100, YuanToComputePoints(1),
			"汇率 %v 下 1 元仍应是 100 点", c.rate)
		require.Equal(t, 100, QuotaToComputePoints(1_000_000),
			"汇率 %v 下 ¥1 对应的 quota 消耗仍应是 100 点（1 quota = ¥10⁻⁶）", c.rate)
	}
}
