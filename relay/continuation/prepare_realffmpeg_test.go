package continuation

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math"
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

	// **必须是真末帧，不是窗口起点那一帧。** 只断言"是合法 PNG"查不出抽错帧 ——
	// 这正是上一版的盲区（`-frames:v 1` 会让 ffmpeg 在 -sseof 定位点只吐一帧，
	// 拿到的是 duration−窗口，早约 1 秒）。这里用帧序号独立取参考帧做像素比对。
	frames := int(math.Round(3 * 24)) // testsrc 3 s / 24 fps
	last := framePixels(t, src, frames-1)
	first := framePixels(t, src, 0)
	mid := framePixels(t, src, frames/2)
	gotPixels := pngPixels(t, png)

	if gotPixels != last {
		t.Fatalf("抽到的不是末帧（第 %d 帧）: got=%s last=%s first=%s mid=%s",
			frames-1, gotPixels[:8], last[:8], first[:8], mid[:8])
	}
	if gotPixels == mid || gotPixels == first {
		t.Fatal("抽到的是中间帧/首帧，不是末帧")
	}
}

// framePixels 用帧序号独立取一帧的像素摘要（与生产代码走不同的过滤路径，避免自证）。
func framePixels(t *testing.T, src string, index int) string {
	t.Helper()
	cmd := exec.Command("ffmpeg", "-v", "error", "-i", src,
		"-vf", fmt.Sprintf("select=eq(n\\,%d)", index),
		"-vsync", "0", "-frames:v", "1",
		"-f", "rawvideo", "-pix_fmt", "rgb24", "pipe:1")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("取第 %d 帧失败: %v", index, err)
	}
	sum := sha256.Sum256(out)
	return hex.EncodeToString(sum[:])
}

// pngPixels 把一张 PNG 解成 rgb24 后取摘要，用来和上面的帧比较。
func pngPixels(t *testing.T, png []byte) string {
	t.Helper()
	tmp := filepath.Join(t.TempDir(), "frame.png")
	if err := os.WriteFile(tmp, png, 0o600); err != nil {
		t.Fatalf("写临时 PNG 失败: %v", err)
	}
	cmd := exec.Command("ffmpeg", "-v", "error", "-i", tmp,
		"-f", "rawvideo", "-pix_fmt", "rgb24", "pipe:1")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("解码 PNG 失败: %v", err)
	}
	sum := sha256.Sum256(out)
	return hex.EncodeToString(sum[:])
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
