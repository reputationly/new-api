package common

import (
	"math"
	"testing"
)

// H3 的帧栅格是 17n+5 @24fps。这张表与引擎 vllm-omni 的
// `time_request._align_frame_count` 逐位对应 —— 网关与引擎算的必须是同一个数,
// 否则回显/计费/校验三处会各错各的。
func TestH3DeliveredDuration(t *testing.T) {
	cases := []struct {
		requested int
		frames    int
		seconds   float64
	}{
		{1, 39, 1.625},
		{2, 56, 2.3333333333333335},
		{3, 73, 3.0416666666666665},
		{4, 107, 4.458333333333333},
		{5, 124, 5.166666666666667},
		{8, 192, 8},
		{10, 243, 10.125},
		{12, 294, 12.25},
		{15, 362, 15.083333333333334},
		{18, 447, 18.625},
		{20, 481, 20.041666666666668},
	}
	for _, tc := range cases {
		frames, seconds := H3DeliveredDuration(tc.requested)
		if frames != tc.frames {
			t.Fatalf("requested %d: frames = %d, want %d", tc.requested, frames, tc.frames)
		}
		if math.Abs(seconds-tc.seconds) > 1e-9 {
			t.Fatalf("requested %d: seconds = %v, want %v", tc.requested, seconds, tc.seconds)
		}
		// 交付永远不短于请求(栅格只向上吸附)
		if seconds < float64(tc.requested) {
			t.Fatalf("requested %d: 交付 %v 短于请求,栅格算反了", tc.requested, seconds)
		}
	}
}

func TestH3DeliveredDurationWithoutRequest(t *testing.T) {
	// 没指定时长时不能编一个数:默认帧数由引擎决定。
	for _, v := range []int{0, -1} {
		frames, seconds := H3DeliveredDuration(v)
		if frames != 0 || seconds != 0 {
			t.Fatalf("requested %d: got (%d, %v), want (0, 0)", v, frames, seconds)
		}
	}
}

func TestH3AlignFrameCount(t *testing.T) {
	cases := map[int]int{
		0:   1,
		1:   5,
		24:  39,
		39:  39,
		40:  56,
		107: 107,
		480: 481,
		481: 481,
	}
	for in, want := range cases {
		if got := H3AlignFrameCount(in); got != want {
			t.Fatalf("align(%d) = %d, want %d", in, got, want)
		}
	}
}

// IsH3VideoModel 判的是运营配置里声明的 engine,不是模型名 substring。
func TestIsH3VideoModel(t *testing.T) {
	// 与其它 common 单测共用 setOpt:单测里 OptionMap 可能是 nil(进程没初始化配置)。
	setOpt("", `{"models":{"h3-public":{"engine":"minimax-h3"},"h3-upstream":{"engine":"minimax-h3"},"wan":{"engine":"wan"}}}`)
	defer setOpt("", "")

	if !IsH3VideoModel("h3-public") {
		t.Fatal("h3-public 应判为 H3")
	}
	if !IsH3VideoModel("nope", "h3-upstream") {
		t.Fatal("候选名里任一命中即应判为 H3")
	}
	if IsH3VideoModel("wan") {
		t.Fatal("wan 不该判为 H3")
	}
	if IsH3VideoModel("minimax-h3-fl2va") {
		t.Fatal("未在配置里声明 engine 的名字不该按 substring 命中")
	}
}
