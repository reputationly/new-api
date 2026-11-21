package ratio_setting

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 存绝对价目的核心承诺：**改了常规目录价，时段价不动**。
//
// 常规 2/8、闲时配 1/7；把常规改成 3/12 后，闲时的输入价仍然是 1，输出仍然是 7。
func TestResolveGroupRatio_PricesAreAbsolute(t *testing.T) {
	seedRatios(t, `{"default":1}`, `{}`, `{}`)
	require.NoError(t, UpdateModelRatioByJSONString(`{"m": 2}`))
	require.NoError(t, UpdateCompletionRatioByJSONString(`{"m": 4}`))
	seedTimeRatio(t, `{
		"windows": {"night": {"label":"闲时","start":"00:00","end":"08:00"}},
		"rules":   {"*": {"m": [{"window":"night","prices":{"input":1,"output":7}}]}}
	}`)

	night := ResolveGroupRatioAt("default", "default", "m", at(t, 0, 2, 0))
	inputPriceBefore := 2 * night.Final
	require.InDelta(t, 1.0, inputPriceBefore, 1e-9, "输入价 = 常规 2 × 系数 0.5")
	// 输出折算：(0.875/0.5)=1.75 → 补全倍率 4 × 1.75 = 7，输出价 = 2×4×0.875 = 7
	outFactorBefore := night.TimeFactors["output"] / night.TimeFactors["input"]
	require.InDelta(t, 7.0, 2*4*night.Final*outFactorBefore, 1e-9)

	// 常规价改成 3 / 12：时段价必须原地不动
	require.NoError(t, UpdateModelRatioByJSONString(`{"m": 3}`))
	require.NoError(t, UpdateCompletionRatioByJSONString(`{"m": 6}`))
	after := ResolveGroupRatioAt("default", "default", "m", at(t, 0, 2, 0))
	require.InDelta(t, 1.0, 3*after.Final, 1e-9, "输入价仍是 1")
	outFactorAfter := after.TimeFactors["output"] / after.TimeFactors["input"]
	require.InDelta(t, 7.0, 3*6*after.Final*outFactorAfter, 1e-9, "输出价仍是 7")

	seedTimeRatio(t, `{}`)
}

// 三种历史格式等价性：prices / factors / value 在同一组常规价下算出的系数必须一致。
func TestTimeRuleFormatsEquivalence(t *testing.T) {
	require.NoError(t, UpdateModelRatioByJSONString(`{"m": 2}`))
	require.NoError(t, UpdateCompletionRatioByJSONString(`{"m": 4}`))

	pricesRule := TimeRule{Prices: map[string]float64{"input": 1, "output": 7}}
	factorsRule := TimeRule{Factors: map[string]float64{"input": 0.5, "output": 0.875}}
	valueRule := TimeRule{Value: 0.5, OutputFactor: 0.875}

	pFactors := pricesRule.EffectiveFactors("m")
	fFactors := factorsRule.EffectiveFactors("m")
	vFactors := valueRule.EffectiveFactors("m")

	require.InDelta(t, 0.5, pFactors["input"], 1e-9)
	require.InDelta(t, 0.875, pFactors["output"], 1e-9)
	require.InDelta(t, 0.5, fFactors["input"], 1e-9)
	require.InDelta(t, 0.875, fFactors["output"], 1e-9)
	// 旧 value 会展开到全部分量（整价统一变），input/output 与上面两种一致
	require.InDelta(t, 0.5, vFactors["input"], 1e-9)
	require.InDelta(t, 0.875, vFactors["output"], 1e-9)
	require.InDelta(t, 0.5, vFactors["cache"], 1e-9, "旧格式连缓存一起变")
}

var _ = time.Now
