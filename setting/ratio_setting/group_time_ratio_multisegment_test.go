package ratio_setting

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 官方口径：高峰 = 工作日 09-12 + 14-18（**一个时段两段**），空闲 = 其余全部（补集）。
// 这两条断言锁的就是"两个时段、每个时段可以不连续"这个模型。
func TestComplementSegments_PeakOffPeak(t *testing.T) {
	peak := []TimeSegment{
		{Start: "09:00", End: "12:00", Days: []int{1, 2, 3, 4, 5}},
		{Start: "14:00", End: "18:00", Days: []int{1, 2, 3, 4, 5}},
	}
	segs, err := complementSegments(peak)
	require.NoError(t, err)
	require.NotEmpty(t, segs)

	// 展开后应当能覆盖：凌晨、午休、夜间、周末全天
	// 用 ActiveAt 逐点验证比断言具体段数更贴近语义。
	win := TimeWindow{Label: "空闲时段", TZ: "UTC", Segments: segs}
	// 2026-11-23 是周一
	require.True(t, win.ActiveAt(time.Date(2026, 11, 23, 3, 0, 0, 0, time.UTC)), "周一凌晨是空闲")
	require.False(t, win.ActiveAt(time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)), "周一 10 点是高峰")
	require.True(t, win.ActiveAt(time.Date(2026, 11, 23, 13, 0, 0, 0, time.UTC)), "周一午休是空闲")
	require.False(t, win.ActiveAt(time.Date(2026, 11, 23, 15, 0, 0, 0, time.UTC)), "周一 15 点是高峰")
	require.True(t, win.ActiveAt(time.Date(2026, 11, 23, 20, 0, 0, 0, time.UTC)), "周一夜间是空闲")
	// 2026-11-28 是周六：全天空闲
	require.True(t, win.ActiveAt(time.Date(2026, 11, 28, 10, 0, 0, 0, time.UTC)), "周末 10 点也空闲")
	require.True(t, win.ActiveAt(time.Date(2026, 11, 28, 15, 0, 0, 0, time.UTC)), "周末 15 点也空闲")
}

// 多段时段本身的生效判定（不涉及补集）
func TestTimeWindow_MultiSegmentActiveAt(t *testing.T) {
	win := TimeWindow{Label: "高峰时段", TZ: "UTC", Segments: []TimeSegment{
		{Start: "09:00", End: "12:00", Days: []int{1, 2, 3, 4, 5}},
		{Start: "14:00", End: "18:00", Days: []int{1, 2, 3, 4, 5}},
	}}
	require.True(t, win.ActiveAt(time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)))
	require.False(t, win.ActiveAt(time.Date(2026, 11, 23, 13, 0, 0, 0, time.UTC)))
	require.True(t, win.ActiveAt(time.Date(2026, 11, 23, 14, 0, 0, 0, time.UTC)))
	require.False(t, win.ActiveAt(time.Date(2026, 11, 28, 10, 0, 0, 0, time.UTC)), "周末不在工作日集合里")
}

// 旧单段格式必须继续等于"一段的 Segments"
func TestTimeWindow_LegacySingleSegment(t *testing.T) {
	legacy := TimeWindow{TZ: "UTC", Start: "22:00", End: "06:00", Days: []int{5}}
	multi := TimeWindow{TZ: "UTC", Segments: []TimeSegment{{Start: "22:00", End: "06:00", Days: []int{5}}}}
	for _, at := range []time.Time{
		time.Date(2026, 11, 27, 23, 0, 0, 0, time.UTC), // 周五 23:00
		time.Date(2026, 11, 28, 5, 0, 0, 0, time.UTC),  // 周六 05:00（承接周五）
		time.Date(2026, 11, 28, 6, 0, 0, 0, time.UTC),  // 边界外
	} {
		require.Equal(t, multi.ActiveAt(at), legacy.ActiveAt(at))
	}
}

// 端到端：按官方口径配「高峰/空闲」两个时段，验证价格与折扣。
//
// 配置 = 官方价目表（Flash：高峰 2/8/0.04，空闲 1/4/0.02）+ 我方 5 折。
// 断言：两个时段的成交价与「固定折扣不随时段变」。
func TestOfficialTwoSlotConfig(t *testing.T) {
	seedRatios(t, `{"default":1}`, `{}`, `{"default":{"deepseek-v4-1-flash":{"mode":"multiply","value":0.5}}}`)
	require.NoError(t, UpdateModelRatioByJSONString(`{"deepseek-v4-1-flash": 2}`))
	require.NoError(t, UpdateCompletionRatioByJSONString(`{"deepseek-v4-1-flash": 4}`))
	require.NoError(t, UpdateCacheRatioByJSONString(`{"deepseek-v4-1-flash": 0.02}`))

	// 官方口径：高峰=工作日两段；空闲=其余时间（补集）
	seedTimeRatio(t, `{
		"windows": {
		  "peak": {"label":"高峰时段","tz":"UTC","segments":[
		    {"start":"09:00","end":"12:00","days":[1,2,3,4,5]},
		    {"start":"14:00","end":"18:00","days":[1,2,3,4,5]}]},
		  "offpeak": {"label":"空闲时段","tz":"UTC","exclude_of":"peak"}
		},
		"rules": {"*": {"deepseek-v4-1-flash": [
		  {"window":"peak","prices":{"input":2,"output":8,"cache":0.04}},
		  {"window":"offpeak","prices":{"input":1,"output":4,"cache":0.02}}
		]}}
	}`)

	// 2026-11-23 是周一：10:00 高峰、13:00 午休（空闲）、20:00 夜间（空闲）
	peakAt := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	noonAt := time.Date(2026, 11, 23, 13, 0, 0, 0, time.UTC)

	peakRes := ResolveGroupRatioAt("default", "default", "deepseek-v4-1-flash", peakAt)
	// 高峰：目录价 = 常规价（系数 1），5 折后输入 ¥1
	require.InDelta(t, 0.5, peakRes.Final, 1e-9, "高峰：常规终值 0.5 × 系数 1")
	require.InDelta(t, 1.0, peakRes.TimeFactors["input"], 1e-9)
	require.InDelta(t, 1.0, 2*peakRes.Final, 1e-9, "高峰成交价 ¥1 / 1M 输入")

	offRes := ResolveGroupRatioAt("default", "default", "deepseek-v4-1-flash", noonAt)
	// 空闲：目录价 1 = 常规 2 × 系数 0.5，5 折后输入 ¥0.5
	require.InDelta(t, 0.25, offRes.Final, 1e-9, "空闲：常规终值 0.5 × 系数 0.5")
	require.InDelta(t, 0.5, offRes.TimeFactors["input"], 1e-9)
	require.InDelta(t, 0.5, 2*offRes.Final, 1e-9, "空闲成交价 ¥0.5 / 1M 输入")

	// 展示侧：固定折扣恒为 5 折（不能把时段系数乘进折扣）
	peakView, ok := ResolveTimeRatioView("default", "deepseek-v4-1-flash", peakAt, peakRes)
	require.True(t, ok)
	require.InDelta(t, 0.5, peakView.DiscountRatio, 1e-9)
	require.InDelta(t, 1.0, peakView.InputDirectoryRatio, 1e-9, "高峰目录倍率 1 → ¥2")

	offView, ok2 := ResolveTimeRatioView("default", "deepseek-v4-1-flash", noonAt, offRes)
	require.True(t, ok2)
	require.InDelta(t, 0.5, offView.DiscountRatio, 1e-9, "折扣仍 5 折")
	require.InDelta(t, 0.5, offView.InputDirectoryRatio, 1e-9, "空闲目录倍率 0.5 → ¥1")
	require.InDelta(t, 0.5, offView.TimeFactors["input"], 1e-9)

	seedTimeRatio(t, `{}`)
}
