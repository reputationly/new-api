package continuation

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func fakeDeps(probe ContinuationProbe, fetchBytes []byte, frame []byte) *PrepareDeps {
	return &PrepareDeps{
		fetch: func(context.Context, ContinuationPrepareOptions) ([]byte, string, error) {
			if fetchBytes == nil {
				return nil, "", errors.New("boom")
			}
			return fetchBytes, ".mp4", nil
		},
		probe: func(context.Context, string) (ContinuationProbe, error) {
			if probe.Width == 0 && probe.Height == 0 && probe.DurationSeconds == 0 {
				return ContinuationProbe{}, errors.New("unreadable")
			}
			return probe, nil
		},
		lastFrame: func(context.Context, string, ContinuationProbe) ([]byte, string, error) {
			return frame, "image/png", nil
		},
	}
}

func TestPrepareContinuationHappyPath(t *testing.T) {
	deps := fakeDeps(
		ContinuationProbe{DurationSeconds: 3, Width: 1344, Height: 768, FPS: 24},
		[]byte("fake-mp4-bytes"),
		[]byte{0x89, 'P', 'N', 'G'},
	)

	got, err := PrepareContinuation(context.Background(), ContinuationPrepareOptions{
		Source:       "https://example.com/src.mp4",
		TotalSeconds: 10,
	}, deps)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !strings.HasPrefix(got.FirstFrameDataURL, "data:image/png;base64,") {
		t.Fatalf("首帧应是 PNG data URI, got %q", got.FirstFrameDataURL)
	}
	// 源 3 s、总 10 s → 生成段 7 s（引擎最短档 1 s，总时长必须 > 源）
	if got.GenerateSeconds != 7 {
		t.Fatalf("GenerateSeconds = %d, want 7", got.GenerateSeconds)
	}
	if got.Probe.DurationSeconds != 3 {
		t.Fatalf("Probe 未透传: %+v", got.Probe)
	}
}

// 生成段秒数 = 最终总时长 − ceil(源时长)。这条规则决定"用户填的 duration 合不合法"，
// 错了就是花完 GPU 才发现接不上。
func TestContinuationGenerateSeconds(t *testing.T) {
	cases := []struct {
		name   string
		total  int
		source float64
		want   int
		code   string
	}{
		{"源 3 s、总 10 s → 生成 7 s", 10, 3, 7, ""},
		{"源 3.2 s 向上取整到 4 → 生成 6 s", 10, 3.2, 6, ""},
		{"总时长恰好比源多 1 s(允许的最小值)", 4, 3, 1, ""},
		{"总时长等于源时长 → 拒", 3, 3, 0, "duration_below_source"},
		{"总时长小于源 → 拒", 2, 3, 0, "duration_below_source"},
		{"没给总时长 → 拒", 0, 3, 0, "duration_missing"},
		{"小数源时长刚好等于整数", 6, 5.0, 1, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := continuationGenerateSeconds(tc.total, tc.source)
			if tc.code != "" {
				var cerr *ContinuationError
				if !errors.As(err, &cerr) || cerr.Code != tc.code {
					t.Fatalf("err = %v, want code %q", err, tc.code)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got != tc.want {
				t.Fatalf("gen = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestPrepareContinuationRejects(t *testing.T) {
	cases := []struct {
		name   string
		opts   ContinuationPrepareOptions
		deps   *PrepareDeps
		code   string
		errMsg string
	}{
		{
			name: "源为空",
			opts: ContinuationPrepareOptions{TotalSeconds: 5},
			code: "source_missing",
		},
		{
			name: "取不到源片",
			opts: ContinuationPrepareOptions{Source: "https://example.com/x.mp4", TotalSeconds: 5},
			deps: fakeDeps(ContinuationProbe{DurationSeconds: 3}, nil, nil),
			code: "source_unreadable",
		},
		{
			name: "源片过短",
			opts: ContinuationPrepareOptions{Source: "https://example.com/x.mp4", TotalSeconds: 5},
			deps: fakeDeps(ContinuationProbe{DurationSeconds: 1.2, Width: 640, Height: 360}, []byte("x"), []byte("f")),
			code: "source_too_short",
		},
		{
			name: "源片过长",
			opts: ContinuationPrepareOptions{Source: "https://example.com/x.mp4", TotalSeconds: 30},
			deps: fakeDeps(ContinuationProbe{DurationSeconds: 16, Width: 640, Height: 360}, []byte("x"), []byte("f")),
			code: "source_too_long",
		},
		{
			name: "短边不足 256",
			opts: ContinuationPrepareOptions{Source: "https://example.com/x.mp4", TotalSeconds: 10},
			deps: fakeDeps(ContinuationProbe{DurationSeconds: 3, Width: 640, Height: 240}, []byte("x"), []byte("f")),
			code: "source_too_small",
		},
		{
			name: "总时长不足",
			opts: ContinuationPrepareOptions{Source: "https://example.com/x.mp4", TotalSeconds: 3},
			deps: fakeDeps(ContinuationProbe{DurationSeconds: 3, Width: 640, Height: 360}, []byte("x"), []byte("f")),
			code: "duration_below_source",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := PrepareContinuation(context.Background(), tc.opts, tc.deps)
			if err == nil {
				t.Fatalf("want error code %q, got nil", tc.code)
			}
			var cerr *ContinuationError
			if !errors.As(err, &cerr) {
				t.Fatalf("错误类型应能拿到 code, got %T: %v", err, err)
			}
			if cerr.Code != tc.code {
				t.Fatalf("code = %q, want %q (msg=%s)", cerr.Code, tc.code, cerr.Message)
			}
		})
	}
}

// 末帧抽取失败要报 frame_extract_failed，而不是笼统的 500 —— 这条分支在真机上表现为
// "源片能下载、能探测，但解码窗口内没有视频帧"（例如纯音频 mp4）。
func TestPrepareContinuationFrameExtractFails(t *testing.T) {
	deps := fakeDeps(
		ContinuationProbe{DurationSeconds: 3, Width: 640, Height: 360},
		[]byte("x"),
		nil,
	)
	deps.lastFrame = func(context.Context, string, ContinuationProbe) ([]byte, string, error) {
		return nil, "", errors.New("no video frame")
	}
	_, err := PrepareContinuation(context.Background(), ContinuationPrepareOptions{
		Source: "https://example.com/x.mp4", TotalSeconds: 10,
	}, deps)
	var cerr *ContinuationError
	if !errors.As(err, &cerr) || cerr.Code != "frame_extract_failed" {
		t.Fatalf("err = %v, want frame_extract_failed", err)
	}
}
