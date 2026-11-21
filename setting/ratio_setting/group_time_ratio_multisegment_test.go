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
