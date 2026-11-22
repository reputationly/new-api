package continuation

// 续写预处理的真实依赖：取源片、ffprobe 探测、ffmpeg 抽末帧。
//
// 三个函数都做成"纯解析 + 薄执行"：命令行的 JSON 解析单独抽成函数（可直接单测），
// 真正 exec 的那层只负责拼参数、带超时、把 stderr 截成可读的错误。

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// continuationCommandTimeout 单条 ffmpeg/ffprobe 的预算。抽末帧只解码尾部窗口，
	// 正常几百毫秒；给 2 分钟是防 4K/60fps 的源片把工作协程卡死。
	continuationCommandTimeout = 2 * time.Minute
)

// SourceFetcher 抓取 task:<id> / http(s) 形态的源片字节，返回内容与扩展名。
//
// 做成注入而不是直接 import nfsinput：nfsinput 依赖 service 包，而 service 的任务
// 完成路径又要调续写（拼接），直接 import 会成环。真正实现由 relay 包在 init 时注入
// （见 relay/continuation_source.go），复用的仍是同一套 SSRF 校验与 task: 归属校验。
type SourceFetcher func(ctx context.Context, userID int, source string) ([]byte, string, error)

var sourceFetcher SourceFetcher

// SetSourceFetcher 由 relay 包在启动时注入；重复注入以最后一次为准（测试友好）。
func SetSourceFetcher(f SourceFetcher) { sourceFetcher = f }

// fetchContinuationSource 按来源形态取字节：task:<id> / http(s) / data:。
//
// **不接受本地路径**：那等于把"读服务器上任意文件"开成 API。NFS 上的产物走 task: 引用
// （归属与状态都校验过）。
func fetchContinuationSource(ctx context.Context, opts ContinuationPrepareOptions) ([]byte, string, error) {
	source := strings.TrimSpace(opts.Source)
	switch {
	case strings.HasPrefix(strings.ToLower(source), "data:"):
		return decodeContinuationDataURI(source)
	case nfsinputLikeRef(source):
		// 归属校验是我们自己的契约，先于抓取器判断：抓取器拿不到用户就无从校验。
		if strings.HasPrefix(source, "task:") && opts.UserID <= 0 {
			return nil, "", fmt.Errorf("引用 task: 产物需要登录用户")
		}
		if sourceFetcher == nil {
			return nil, "", fmt.Errorf("源片抓取器未初始化（task:/URL 形态需要 relay 侧注入）")
		}
		return sourceFetcher(ctx, opts.UserID, source)
	default:
		return nil, "", fmt.Errorf("源视频只接受 http(s) URL、data: 或 task:<id>，收到 %q", truncateForLog(source, 64))
	}
}

// nfsinputLikeRef 判断是否是要交给注入抓取器的形态（任务产物引用或 http(s)）。
func nfsinputLikeRef(source string) bool {
	lower := strings.ToLower(source)
	return strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(source, "task:")
}

// FetchSourceBytes 供 service 层复用：把源片取成字节 + 扩展名。
func FetchSourceBytes(ctx context.Context, opts ContinuationPrepareOptions) ([]byte, string, error) {
	return fetchContinuationSource(ctx, opts)
}

// decodeContinuationDataURI 解 data: URI，返回字节与由 MIME 推出的扩展名。
func decodeContinuationDataURI(raw string) ([]byte, string, error) {
	head, payload, ok := strings.Cut(raw, ",")
	if !ok {
		return nil, "", fmt.Errorf("data URI 缺少逗号分隔的正文")
	}
	if !strings.Contains(head, ";base64") {
		return nil, "", fmt.Errorf("data URI 必须是 base64 编码")
	}
	mime := strings.TrimPrefix(head, "data:")
	if i := strings.Index(mime, ";"); i >= 0 {
		mime = mime[:i]
	}
	// 先按长度粗判，避免为了看大小先解出几十 MB。
	if int64(len(payload))*3/4 > MaxSourceBytes+1024 {
		return nil, "", fmt.Errorf("data URI 超过 %d MB 上限", MaxSourceBytes>>20)
	}
	body, err := base64.StdEncoding.DecodeString(strings.TrimSpace(payload))
	if err != nil {
		return nil, "", fmt.Errorf("data URI 不是合法 base64: %w", err)
	}
	return body, extFromMime(mime), nil
}

func extFromMime(mime string) string {
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "video/mp4":
		return ".mp4"
	case "video/quicktime":
		return ".mov"
	case "video/webm":
		return ".webm"
	case "video/x-matroska":
		return ".mkv"
	default:
		return ""
	}
}

// ffprobeContinuationProbe 用 ffprobe 读时长/尺寸/帧率/有无音轨。
func ffprobeContinuationProbe(ctx context.Context, path string) (ContinuationProbe, error) {
	out, err := runMediaCommand(ctx, "ffprobe",
		"-v", "error",
		"-show_entries", "stream=codec_type,width,height,avg_frame_rate,duration",
		"-show_entries", "format=duration",
		"-of", "json",
		path,
	)
	if err != nil {
		return ContinuationProbe{}, err
	}
	return parseContinuationProbe(out)
}

// parseContinuationProbe 解析 ffprobe 的 JSON 输出。抽成纯函数以便单测（不需要 ffprobe）。
func parseContinuationProbe(raw []byte) (ContinuationProbe, error) {
	var payload struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
			AvgFrame  string `json:"avg_frame_rate"`
			Duration  string `json:"duration"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ContinuationProbe{}, fmt.Errorf("ffprobe 输出无法解析: %w", err)
	}
	probe := ContinuationProbe{}
	if d, err := strconv.ParseFloat(strings.TrimSpace(payload.Format.Duration), 64); err == nil && d > 0 {
		probe.DurationSeconds = d
	}
	for _, s := range payload.Streams {
		switch s.CodecType {
		case "video":
			if probe.Width == 0 {
				probe.Width, probe.Height = s.Width, s.Height
				probe.FPS = parseFrameRate(s.AvgFrame)
			}
			if probe.DurationSeconds <= 0 {
				if d, err := strconv.ParseFloat(strings.TrimSpace(s.Duration), 64); err == nil && d > 0 {
					probe.DurationSeconds = d
				}
			}
		case "audio":
			probe.HasAudio = true
		}
	}
	return probe, nil
}

// parseFrameRate 解析 "24/1" / "30000/1001" 形态的帧率。
func parseFrameRate(raw string) float64 {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "0/0" {
		return 0
	}
	if num, den, ok := strings.Cut(raw, "/"); ok {
		n, err1 := strconv.ParseFloat(num, 64)
		d, err2 := strconv.ParseFloat(den, 64)
		if err1 == nil && err2 == nil && d != 0 {
			return n / d
		}
		return 0
	}
	f, _ := strconv.ParseFloat(raw, 64)
	return f
}

// ffmpegLastFrame 抽源片最后一帧。
//
// 为什么用 `-sseof -窗口 -update 1`：`-sseof -0.1` 常因容器时长与关键帧对齐问题抓不到
// 真正的末帧（实测有时退到倒数第 3 帧）。从尾部窗口开始解码、用 `-update 1` 反复覆盖
// 同一个文件，落盘的就是解出来的最后一帧 —— 窗口只为省解码时间，不影响结果。
//
// **不能加 `-frames:v 1`**：那句会让 ffmpeg 在 `-sseof` 的定位点只吐一帧就停，
// 拿到的其实是"duration − 窗口"那一帧（窗口恒 ≈1s，于是早约 1 秒），
// 而 `-update 1` 的值恰好是"越写越靠后"。两者叠一起会得到一个看起来很合理、
// 实际是中间帧的图——接缝处重复约 1 秒内容，且不报错。
//
// 产物过大/边长过大时再走一次缩放或转 JPEG：末帧会以 base64 进请求体，尺寸必须有上限。
func ffmpegLastFrame(ctx context.Context, path string, probe ContinuationProbe) ([]byte, string, error) {
	dir := filepath.Dir(path)
	window := 1.0
	if probe.FPS > 0 {
		if w := 3 / probe.FPS; w > window {
			window = w
		}
	}
	outPNG := filepath.Join(dir, "last_frame.png")
	if _, err := runMediaCommand(ctx, "ffmpeg",
		"-nostdin", "-y", "-hide_banner", "-loglevel", "error",
		"-sseof", "-"+strconv.FormatFloat(window, 'f', 2, 64),
		"-i", path,
		"-update", "1",
		"-q:v", "2",
		outPNG,
	); err != nil {
		return nil, "", err
	}
	frame, err := os.ReadFile(outPNG)
	if err != nil {
		return nil, "", fmt.Errorf("读取末帧失败: %w", err)
	}
	tooBig := len(frame) > continuationMaxFirstFrameBytes
	tooWide := probe.Width > continuationFrameMaxEdge || probe.Height > continuationFrameMaxEdge
	if !tooBig && !tooWide {
		return frame, "image/png", nil
	}

	// 超限：等比缩到最长边 ≤ continuationFrameMaxEdge，再不行就转 JPEG。
	scaled := filepath.Join(dir, "last_frame_small.png")
	scale := fmt.Sprintf("scale='min(%d,iw)':'min(%d,ih)':force_original_aspect_ratio=decrease",
		continuationFrameMaxEdge, continuationFrameMaxEdge)
	if _, err := runMediaCommand(ctx, "ffmpeg",
		"-nostdin", "-y", "-hide_banner", "-loglevel", "error",
		"-i", outPNG,
		"-vf", scale,
		scaled,
	); err == nil {
		if small, rerr := os.ReadFile(scaled); rerr == nil && len(small) <= continuationMaxFirstFrameBytes {
			return small, "image/png", nil
		}
	}

	outJPEG := filepath.Join(dir, "last_frame.jpg")
	if _, err := runMediaCommand(ctx, "ffmpeg",
		"-nostdin", "-y", "-hide_banner", "-loglevel", "error",
		"-i", outPNG,
		"-vf", scale,
		"-q:v", "3",
		outJPEG,
	); err != nil {
		return nil, "", fmt.Errorf("末帧压缩失败: %w", err)
	}
	jpeg, err := os.ReadFile(outJPEG)
	if err != nil {
		return nil, "", fmt.Errorf("读取压缩后的末帧失败: %w", err)
	}
	if len(jpeg) > continuationMaxFirstFrameBytes {
		return nil, "", fmt.Errorf("末帧压缩后仍有 %.1f MB，超过上限 %d MB",
			float64(len(jpeg))/(1<<20), continuationMaxFirstFrameBytes>>20)
	}
	return jpeg, "image/jpeg", nil
}

// runMediaCommand 执行 ffmpeg / ffprobe，返回 stdout，失败时把 stderr 尾部带上。
//
// 不找 PATH 之外的路径：Docker 镜像里 ffmpeg 是 apt 装的（内容审核在用），
// 找不到就明确报"环境缺 ffmpeg"，而不是让调用方看到一个空字节切片。
func runMediaCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	if _, err := exec.LookPath(name); err != nil {
		return nil, fmt.Errorf("本机缺少 %s（续写需要 ffmpeg/ffprobe）: %w", name, err)
	}
	ctx, cancel := context.WithTimeout(ctx, continuationCommandTimeout)
	defer cancel()

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s 执行失败: %v: %s", name, err, truncateForLog(stderr.String(), 400))
	}
	return stdout.Bytes(), nil
}

func truncateForLog(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
