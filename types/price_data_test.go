package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestScaleCompletionRatio 锁死「输出侧时段系数折进 completion_ratio」这条换算。
//
// 计费公式是 (输入 + 输出 × completionRatio) × ModelRatio × GroupRatio，整单只乘
// GroupRatio（输入侧系数）。输出侧要独立变化，必须把 output/input 乘进 completionRatio。
func TestScaleCompletionRatio(t *testing.T) {
	require.Equal(t, 1.0, ScaleCompletionRatio(0, 0))
	require.Equal(t, 1.0, ScaleCompletionRatio(0.5, 0))
	require.Equal(t, 1.0, ScaleCompletionRatio(0.5, 0.5))
	// 忙时输入 2/输出 8、闲时输入 1/输出 7 → 输入系数 0.5、输出系数 0.875，比值 1.75
	require.InDelta(t, 1.75, ScaleCompletionRatio(0.5, 0.875), 1e-12)
	require.InDelta(t, 0.5, ScaleCompletionRatio(0.8, 0.4), 1e-12)
}

func TestGroupRatioInfoCompletionRatioScale(t *testing.T) {
	require.Equal(t, 1.0, GroupRatioInfo{TimeValue: 0.5, TimeOutputFactor: 0.875}.CompletionRatioScale())
	require.InDelta(t, 1.75, GroupRatioInfo{
		TimeWindow: "night", TimeValue: 0.5, TimeOutputFactor: 0.875,
	}.CompletionRatioScale(), 1e-12)
	require.Equal(t, 1.0, GroupRatioInfo{
		TimeWindow: "night", TimeValue: 0.5, TimeOutputFactor: 0.5,
	}.CompletionRatioScale())
}
