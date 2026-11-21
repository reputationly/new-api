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

// 回归哨兵：**新格式（prices）规则**在展示视图里必须算出非零倍率。
//
// 曾经的 bug：视图用 rule.Value 算 ratio / input_directory_ratio，而 prices 格式里
// Value 恒为 0 —— 结果广场上配了分时价的模型显示「免费」、划线价 ¥0（游客与登录用户都受影响）。
// 计费侧没受影响（那条路径用的是归一后的系数），所以这是个纯展示 bug，但更显眼。
func TestResolveTimeRatioView_PricesFormatNonZero(t *testing.T) {
	seedRatios(t, `{"default":1}`, `{}`, `{}`)
	require.NoError(t, UpdateModelRatioByJSONString(`{"m": 2}`))
	require.NoError(t, UpdateCompletionRatioByJSONString(`{"m": 4}`))
	seedTimeRatio(t, `{
		"windows": {"night": {"label":"空闲·夜间","start":"00:00","end":"08:00"}},
		"rules":   {"*": {"m": [{"window":"night","prices":{"input":1,"output":4}}]}}
	}`)

	at2 := at(t, 0, 2, 0)
	res := ResolveGroupRatioAt("default", "default", "m", at2)
	view, ok := ResolveTimeRatioView("default", "m", at2, res)
	require.True(t, ok)
	require.Len(t, view.Windows, 1)
	in := view.Windows[0]
	// 2 → 1 与 8 → 4 都是 0.5 倍
	require.InDelta(t, 0.5, in.Value, 1e-9)
	require.InDelta(t, 0.5, in.Ratio, 1e-9, "倍率不能是 0（否则前端显示免费）")
	require.InDelta(t, 0.5, in.InputDirectoryRatio, 1e-9)
	require.InDelta(t, 0.5, in.OutputDirectoryRatio, 1e-9)
	require.InDelta(t, 1.0, in.DiscountRatio, 1e-9)
	require.InDelta(t, 0.5, view.BestRatio, 1e-9)
	require.InDelta(t, 0.5, view.InputDirectoryRatio, 1e-9, "此刻目录倍率同样不能是 0")
	require.True(t, view.Active, "0.5 < 1，确实是优惠时段")

	// 时段区间必须经 segments 下发：编译后的窗口 Start/End/Days 已清空，
	// 只回填旧字段的话广场会把「00:00-08:00」渲染成「每天 -」。
	require.Len(t, in.Segments, 1)
	require.Equal(t, "00:00", in.Segments[0].Start)
	require.Equal(t, "08:00", in.Segments[0].End)
	require.Equal(t, "00:00", in.Start, "单段窗口回填旧字段")
	require.Equal(t, "08:00", in.End)

	seedTimeRatio(t, `{}`)
}
