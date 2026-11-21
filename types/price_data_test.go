package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// ScaleComponent 是整条链路唯一的分量换算口：整单只乘 input 系数，其余分量把
// f_component / f_input 折进自己的相对倍率。
//
// 三条语义（都是可被运营直接感知的）：
//   1. 表里没有该分量 → 该分量的**价格不随时段变** → 返回 1/f_input（把整单的缩放抵消掉）
//   2. 表里有该分量且与 input 相等 → 返回 1（整价同比例变，最常见）
//   3. 表里有该分量且不同 → 返回 f_c/f_input（只改这一个分量）
func TestScaleComponent(t *testing.T) {
	// 空表：整单不变
	require.Equal(t, 1.0, ScaleComponent("output", nil))

	// 只配 input（输入 2 → 1）：其它分量价格不变，所以补全倍率要翻倍抵消
	require.InDelta(t, 2.0, ScaleComponent("output", map[string]float64{"input": 0.5}), 1e-12)
	require.InDelta(t, 2.0, ScaleComponent("cache", map[string]float64{"input": 0.5}), 1e-12)

	// 整价同比例（都乘 0.5）：无需折算
	f := map[string]float64{"input": 0.5, "output": 0.5, "cache": 0.5}
	require.Equal(t, 1.0, ScaleComponent("output", f))
	require.Equal(t, 1.0, ScaleComponent("cache", f))

	// 忙时输入 2/输出 8、闲时输入 1/输出 7 → 补全倍率 ×(0.875/0.5)
	require.InDelta(t, 1.75, ScaleComponent("output", map[string]float64{
		"input": 0.5, "output": 0.875,
	}), 1e-12)

	// 只改缓存（0.04 → 0.01）：折进 cacheRatio 的就是 0.25/0.5
	require.InDelta(t, 0.5, ScaleComponent("cache", map[string]float64{
		"input": 0.5, "cache": 0.25,
	}), 1e-12)

	// 按次价显式不变：ModelPrice 折回 1/0.5 = 2（再由整单 ×0.5 抵消）
	require.InDelta(t, 2.0, ScaleComponent("per_call", map[string]float64{
		"input": 0.5, "per_call": 1,
	}), 1e-12)
}
