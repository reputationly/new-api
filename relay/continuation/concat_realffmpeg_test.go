package continuation

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// 真 ffmpeg 的拼接验证：源片 640x360@30(带音轨) + 生成段 1344x768@24 → 输出必须是
// 1344x768@24、时长 ≈ 两者之和、且能解码。
//
// 默认跳过；`CONTINUATION_REAL_FFMPEG=1 go test ./relay/continuation/ -run RealFFmpeg -v`。
// 它覆盖的是注入替身测不到的东西：scale/crop 表达式是否真的被 ffmpeg 接受、
// 两段不同分辨率能不能 concat、静音轨补齐后流数量是否一致。
func TestConcatWithRealFFmpeg(t *testing.T) {
	if os.Getenv("CONTINUATION_REAL_FFMPEG") == "" {
		t.Skip("设置 CONTINUATION_REAL_FFMPEG=1 才跑真 ffmpeg")
	}
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skip("本机没有 " + bin)
		}
	}

	dir := t.TempDir()
	src := filepath.Join(dir, "src.mp4")
	gen := filepath.Join(dir, "gen.mp4")
	out := filepath.Join(dir, "joined.mp4")

	// 源片：640x360 / 30fps / 2s，带正弦音轨（模拟真实视频）。
	run(t, "ffmpeg", "-y", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=duration=2:size=640x360:rate=30",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2",
		"-shortest", "-pix_fmt", "yuv420p", src)
	// 生成段：1344x768 / 24fps / 1.625s，带音轨（H3 默认 generate_audio=true）。
	run(t, "ffmpeg", "-y", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=duration=1.625:size=1344x768:rate=24",
		"-f", "lavfi", "-i", "sine=frequency=880:duration=1.625",
		"-shortest", "-pix_fmt", "yuv420p", gen)

	res, err := Concat(context.Background(), src, gen, out, nil)
	if err != nil {
		t.Fatalf("真 ffmpeg 拼接失败: %v", err)
	}
	if res.Width != 1344 || res.Height != 768 {
		t.Fatalf("画布 = %dx%d, want 1344x768", res.Width, res.Height)
	}

	probe, err := ffprobeContinuationProbe(context.Background(), out)
	if err != nil {
		t.Fatalf("探测产物失败: %v", err)
	}
	if probe.Width != 1344 || probe.Height != 768 {
		t.Fatalf("产物尺寸 = %dx%d", probe.Width, probe.Height)
	}
	// avg_frame_rate = 帧数/总时长，音轨比视频长一点点（AAC 帧对齐）就会把它拉低，
	// 实测 23.87 而视频流本身仍是 24fps（r_frame_rate=24/1）。这里只断言"在 24 附近"。
	if probe.FPS < 23.5 || probe.FPS > 24.5 {
		t.Fatalf("产物帧率 = %v, want ≈24", probe.FPS)
	}
	// 2 + 1.625 = 3.625s；concat 与重编码都有毫秒级偏差，给 ±0.25s。
	if probe.DurationSeconds < 3.3 || probe.DurationSeconds > 3.9 {
		t.Fatalf("产物时长 = %v, want ≈3.625", probe.DurationSeconds)
	}
	if !probe.HasAudio {
		t.Fatal("产物应保留音轨")
	}
	if fi, err := os.Stat(out); err != nil || fi.Size() == 0 {
		t.Fatal("产物文件不可读")
	}
}

// 源片无音轨时也要能拼：补静音轨后流数量一致。
func TestConcatWithRealFFmpegSilentSource(t *testing.T) {
	if os.Getenv("CONTINUATION_REAL_FFMPEG") == "" {
		t.Skip("设置 CONTINUATION_REAL_FFMPEG=1 才跑真 ffmpeg")
	}
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skip("本机没有 " + bin)
		}
	}

	dir := t.TempDir()
	src := filepath.Join(dir, "src.mp4")
	gen := filepath.Join(dir, "gen.mp4")
	out := filepath.Join(dir, "joined.mp4")
	run(t, "ffmpeg", "-y", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=duration=2:size=640x360:rate=24", "-pix_fmt", "yuv420p", src)
	run(t, "ffmpeg", "-y", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=duration=1.625:size=1344x768:rate=24",
		"-f", "lavfi", "-i", "sine=frequency=880:duration=1.625",
		"-shortest", "-pix_fmt", "yuv420p", gen)

	if _, err := Concat(context.Background(), src, gen, out, nil); err != nil {
		t.Fatalf("无音轨源片拼接失败: %v", err)
	}
	probe, err := ffprobeContinuationProbe(context.Background(), out)
	if err != nil {
		t.Fatalf("探测失败: %v", err)
	}
	if !probe.HasAudio {
		t.Fatal("补静音后产物应有音轨")
	}
	if probe.DurationSeconds < 3.3 || probe.DurationSeconds > 3.9 {
		t.Fatalf("产物时长 = %v, want ≈3.625", probe.DurationSeconds)
	}
}

func run(t *testing.T, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s 失败: %v: %s", name, err, strings.TrimSpace(string(out)))
	}
}
