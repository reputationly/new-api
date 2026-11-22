package continuation

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
)

// ffprobe 的 JSON 是这个包唯一的外部契约（引擎/门面换 ffprobe 版本时最先变的就是它）。
// 用一份真实的输出结构钉住解析：format.duration 优先、视频流给宽高与帧率、音轨只判有无。
func TestParseContinuationProbe(t *testing.T) {
	raw := []byte(`{
	  "streams": [
	    {"codec_type":"video","width":1344,"height":768,"avg_frame_rate":"24/1","duration":"3.000000"},
	    {"codec_type":"audio","avg_frame_rate":"0/0"}
	  ],
	  "format": {"duration":"3.033333"}
	}`)
	probe, err := parseContinuationProbe(raw)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if probe.DurationSeconds != 3.033333 {
		t.Fatalf("duration = %v, want 3.033333（format 优先）", probe.DurationSeconds)
	}
	if probe.Width != 1344 || probe.Height != 768 {
		t.Fatalf("size = %dx%d", probe.Width, probe.Height)
	}
	if probe.FPS != 24 {
		t.Fatalf("fps = %v, want 24", probe.FPS)
	}
	if !probe.HasAudio {
		t.Fatal("应识别出音轨")
	}
}

// 没有 format.duration 时退回视频流自己的 duration —— 有些封装（如 webm）只给流级时长。
func TestParseContinuationProbeFallsBackToStreamDuration(t *testing.T) {
	raw := []byte(`{"streams":[{"codec_type":"video","width":640,"height":360,"avg_frame_rate":"30000/1001","duration":"2.5"}],"format":{}}`)
	probe, err := parseContinuationProbe(raw)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if probe.DurationSeconds != 2.5 {
		t.Fatalf("duration = %v, want 2.5", probe.DurationSeconds)
	}
	if probe.FPS <= 29.9 || probe.FPS >= 30.1 {
		t.Fatalf("fps = %v, want ≈29.97", probe.FPS)
	}
}

func TestParseContinuationProbeRejectsGarbage(t *testing.T) {
	if _, err := parseContinuationProbe([]byte("not json")); err == nil {
		t.Fatal("非 JSON 必须报错")
	}
}

func TestParseFrameRate(t *testing.T) {
	cases := map[string]float64{
		"24/1":       24,
		"30000/1001": 29.97002997002997,
		"0/0":        0,
		"":           0,
		"60":         60,
		"1/0":        0,
	}
	for raw, want := range cases {
		if got := parseFrameRate(raw); got != want {
			t.Fatalf("parseFrameRate(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestExtHelpers(t *testing.T) {
	if got := extFromSourceURL("https://x.com/a/b/c.MOV?sig=1#f"); got != ".mov" {
		t.Fatalf("extFromSourceURL = %q", got)
	}
	if got := extFromSourceURL("https://x.com/download"); got != "" {
		t.Fatalf("无扩展名应返回空串, got %q", got)
	}
	if got := extFromSourceURL("https://x.com/a.exe"); got != "" {
		t.Fatalf("非视频扩展名应返回空串, got %q", got)
	}
	if got := extFromMime("video/webm"); got != ".webm" {
		t.Fatalf("extFromMime = %q", got)
	}
	if got := extFromMime("application/octet-stream"); got != "" {
		t.Fatalf("未知 MIME 应返回空串, got %q", got)
	}
}

func TestDecodeContinuationDataURI(t *testing.T) {
	payload := []byte{0x00, 0x00, 0x00, 0x20, 'f', 't', 'y', 'p'}
	uri := "data:video/mp4;base64," + base64.StdEncoding.EncodeToString(payload)
	body, ext, err := decodeContinuationDataURI(uri)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if string(body) != string(payload) {
		t.Fatalf("body mismatch")
	}
	if ext != ".mp4" {
		t.Fatalf("ext = %q, want .mp4", ext)
	}

	if _, _, err := decodeContinuationDataURI("data:video/mp4,notbase64"); err == nil {
		t.Fatal("非 base64 的 data URI 必须报错")
	}
	if _, _, err := decodeContinuationDataURI("data:video/mp4;base64,!!!!"); err == nil {
		t.Fatal("非法 base64 必须报错")
	}
}

// 本地路径必须被拒：这是"读服务器任意文件"的口子，只能走 task: 引用（有归属校验）。
func TestFetchContinuationSourceRejectsLocalPath(t *testing.T) {
	_, _, err := fetchContinuationSource(context.Background(), ContinuationPrepareOptions{
		Source: "/etc/passwd",
	})
	if err == nil || !strings.Contains(err.Error(), "只接受") {
		t.Fatalf("本地路径应被拒, got %v", err)
	}
}

func TestFetchContinuationSourceRequiresUserForTaskRef(t *testing.T) {
	_, _, err := fetchContinuationSource(context.Background(), ContinuationPrepareOptions{
		Source: "task:task_abc",
	})
	if err == nil || !strings.Contains(err.Error(), "登录用户") {
		t.Fatalf("task: 引用缺用户应被拒, got %v", err)
	}
}

func TestTruncateForLog(t *testing.T) {
	long := strings.Repeat("x", 500)
	if got := truncateForLog(long, 10); len([]rune(got)) > 11 {
		t.Fatalf("截断后仍然过长: %d", len(got))
	}
	if got := truncateForLog("  hi  ", 10); got != "hi" {
		t.Fatalf("应去掉首尾空白, got %q", got)
	}
}
