package continuation

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// 真 ffmpeg 的端到端验证：生成一段 3 s 测试视频 → 走 data URI 进来 → 抽末帧。
//
// 默认跳过（CI 的 golang 镜像里没有 ffmpeg，见 Dockerfile 才装），用
// `CONTINUATION_REAL_FFMPEG=1 go test ./relay/continuation/ -run RealFFmpeg -v` 手动跑。
// 这条用例覆盖的是注入测试够不到的东西：ffprobe 的真实 JSON、`-sseof -update 1` 是否
// 真的落到最后一帧、以及 data URI 解码后的扩展名推断。
func TestPrepareContinuationWithRealFFmpeg(t *testing.T) {
	if os.Getenv("CONTINUATION_REAL_FFMPEG") == "" {
		t.Skip("设置 CONTINUATION_REAL_FFMPEG=1 才跑真 ffmpeg")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("本机没有 ffmpeg")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("本机没有 ffprobe")
	}

	dir := t.TempDir()
	src := filepath.Join(dir, "src.mp4")
	// testsrc 的计数器让每帧都不同：末帧抽错会立刻从时长/内容上看出来。
	cmd := exec.Command("ffmpeg", "-y", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=duration=3:size=640x360:rate=24",
		"-pix_fmt", "yuv420p", src)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("生成测试视频失败: %v: %s", err, out)
	}
	body, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("读取测试视频失败: %v", err)
	}

	got, err := PrepareContinuation(context.Background(), ContinuationPrepareOptions{
		Source:       "data:video/mp4;base64," + base64.StdEncoding.EncodeToString(body),
		TotalSeconds: 10,
	}, nil)
	if err != nil {
		t.Fatalf("真 ffmpeg 路径失败: %v", err)
	}
	if got.GenerateSeconds != 7 {
		t.Fatalf("GenerateSeconds = %d, want 7", got.GenerateSeconds)
	}
	if got.Probe.Width != 640 || got.Probe.Height != 360 {
		t.Fatalf("probe size = %dx%d, want 640x360", got.Probe.Width, got.Probe.Height)
	}
	if got.Probe.DurationSeconds < 2.9 || got.Probe.DurationSeconds > 3.1 {
		t.Fatalf("probe duration = %v, want ≈3", got.Probe.DurationSeconds)
	}
	if !strings.HasPrefix(got.FirstFrameDataURL, "data:image/") {
		t.Fatalf("首帧不是 data URI: %q", got.FirstFrameDataURL[:min(40, len(got.FirstFrameDataURL))])
	}
	frame := strings.SplitN(got.FirstFrameDataURL, ",", 2)
	if len(frame) != 2 {
		t.Fatal("data URI 缺少正文")
	}
	png, err := base64.StdEncoding.DecodeString(frame[1])
	if err != nil {
		t.Fatalf("首帧 base64 无法解码: %v", err)
	}
	if len(png) < 1024 {
		t.Fatalf("首帧只有 %d 字节，不像一张图", len(png))
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
