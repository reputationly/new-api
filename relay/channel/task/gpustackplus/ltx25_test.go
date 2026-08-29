package gpustackplus

import "testing"

// LTX-2.5 请求整形的回归测试。
//
// 这里锁住的每一条都是「写错了不会报错、只会默默 500 或默默不生效」的那类约定 ——
// 帧数栅格发错引擎直接拒、seconds 留在 body 里就是一条恒失败的路径、
// wan 的 target_video_length 混进来则是排查时的噪声。正因为静默,才必须由测试守住。

// 时长换算:向上吸附到 `≡ 9 (mod 16)` 栅格。
//
// 这条栅格同时扛两个约束,少一个都会在生产上炸:
//   - 8k+1:引擎硬校验,发错直接 500;
//   - T 为偶数:多卡 SP 整除,960×544 的 30×17=510 只含一个因子 2,T 为奇数时
//     4 卡下去噪阶段直接报 "not evenly shardable"。
//
// 用例覆盖奇数秒(落 X.04)与偶数秒(落 X.375)两类,并逐条断言"实际时长不短于承诺" ——
// 就近吸附会让 10 秒落到 233 帧(9.71 s),那是对外承诺的违约。
func TestLTX25DurationToFrames(t *testing.T) {
	cases := []struct {
		durationSec int
		wantFrames  int
	}{
		{4, 105},
		{5, 121},
		{6, 153},
		{7, 169},
		{8, 201},
		{9, 217},
		{10, 249},
		{15, 361},
	}
	for _, tc := range cases {
		body := map[string]any{}
		applyLTX25Request(body, tc.durationSec)

		got, ok := body["num_frames"].(int)
		if !ok || got != tc.wantFrames {
			t.Fatalf("%ds: num_frames = %v, want %d", tc.durationSec, body["num_frames"], tc.wantFrames)
		}
		if (got-1)%8 != 0 {
			t.Fatalf("%ds: num_frames %d 不在 8k+1 栅格上,引擎会 500", tc.durationSec, got)
		}
		// T = (n-1)/8+1 必须是偶数,否则多卡下 token 除不尽 SP
		if tt := (got-1)/8 + 1; tt%2 != 0 {
			t.Fatalf("%ds: num_frames %d 的 T=%d 是奇数,多卡会 500", tc.durationSec, got, tt)
		}
		// 实际时长不得短于对外承诺
		if float64(got)/24.0 < float64(tc.durationSec) {
			t.Fatalf("%ds: %d 帧只有 %.3f s,短于承诺", tc.durationSec, got, float64(got)/24.0)
		}
		if fps, ok := body["fps"].(int); !ok || fps != 24 {
			t.Fatalf("%ds: fps = %v, want 24", tc.durationSec, body["fps"])
		}
	}
}

// seconds 必须被清掉。
//
// 引擎的 seconds 正则是 ^[1-9]\d*$(只收整数秒),而 frames = seconds×24 恒 ≡ 0 (mod 8),
// 永远取不到 8k+1 需要的余数 1 —— 实测 seconds=5 直接 500(got 120)。
// 只要这个键还在 body 里,就存在一条恒失败的路径。
func TestLTX25DropsSecondsAlways(t *testing.T) {
	// 有时长时
	body := map[string]any{"seconds": "5"}
	applyLTX25Request(body, 5)
	if _, ok := body["seconds"]; ok {
		t.Fatal("seconds 未被清除(有时长)")
	}
	// 没时长时同样要清:提前 return 的分支也不能把它漏下
	body = map[string]any{"seconds": "5"}
	applyLTX25Request(body, 0)
	if _, ok := body["seconds"]; ok {
		t.Fatal("seconds 未被清除(无时长)")
	}
	// 调用方自传帧数时同样要清
	body = map[string]any{"seconds": "5", "num_frames": 121}
	applyLTX25Request(body, 5)
	if _, ok := body["seconds"]; ok {
		t.Fatal("seconds 未被清除(自传 num_frames)")
	}
}

// wan / InfiniteTalk 专属字段必须清掉:引擎侧 LTX 分支从不读它们,
// 留着不报错,只会在排查时让人以为时长可控。
func TestLTX25DropsForeignEngineFields(t *testing.T) {
	body := map[string]any{
		"target_video_length": 81, // wan 的 4n+1 @16fps
		"video_duration":      30, // InfiniteTalk 的输出时长上限
	}
	applyLTX25Request(body, 5)
	for _, key := range []string{"target_video_length", "video_duration"} {
		if _, ok := body[key]; ok {
			t.Fatalf("%s 未被清除", key)
		}
	}
}

// 调用方显式给的帧数优先,不被覆盖 —— metadata 是开放透传的,门面只补默认。
// 合法性(8k+1)交由引擎判定,与本渠道对 size 的处理同一策略。
func TestLTX25KeepsCallerFrames(t *testing.T) {
	body := map[string]any{"num_frames": 249}
	applyLTX25Request(body, 5) // 5 秒本会算成 121
	if got := body["num_frames"]; got != 249 {
		t.Fatalf("num_frames = %v, want 249(调用方取值被覆盖了)", got)
	}
	// 没被接管的请求也不该被补上 fps
	if _, ok := body["fps"]; ok {
		t.Fatal("调用方自传帧数时不应再补 fps")
	}
}

// 没给时长就不写帧数:由引擎按 pipeline 默认帧数决定,门面不替它猜。
func TestLTX25NoDurationLeavesFramesUnset(t *testing.T) {
	body := map[string]any{}
	applyLTX25Request(body, 0)
	if _, ok := body["num_frames"]; ok {
		t.Fatal("没给时长却写了 num_frames")
	}
	if _, ok := body["fps"]; ok {
		t.Fatal("没给时长却写了 fps")
	}
}
