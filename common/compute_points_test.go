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

// 汇率变动不影响「模型消耗多少点」——这正是不建价目表的关键收益。
//
// 模型的 quota 消耗只取决于 token 数 × 倍率，与汇率无关；汇率只影响「1 元买多少 quota」。
// 所以汇率波动只改变套餐的「元→点」定价预览，不改变点数的消耗速度。
func TestComputePoints_ModelCostUnaffectedByExchangeRate(t *testing.T) {
	origRate := USDExchangeRateFunc
	t.Cleanup(func() { USDExchangeRateFunc = origRate })

	const modelQuota = 68493 // 某次调用按倍率算出的 quota

	USDExchangeRateFunc = func() float64 { return 7.3 }
	before := QuotaToComputePoints(modelQuota)

	USDExchangeRateFunc = func() float64 { return 8.5 } // 汇率变动
	after := QuotaToComputePoints(modelQuota)

	require.Equal(t, before, after,
		"同一笔 quota 消耗的算力点数不得随汇率变化，否则套餐消耗速度会被汇率牵动")
}
