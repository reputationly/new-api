package continuation

// 续写（video-extension）的本地预处理段。
//
// 续写不是一条 task_type 能表达的：引擎没有"看一段视频接着往下拍"的能力，我们只能
// 把**源片最后一帧**当成首帧走 i2v，再把生成段拼回源片。这一步就是"取出那一帧"，
// 同时把能提前判死的错全判掉 —— 后面的生成段一旦提交就烧 GPU，届时才发现"源片只有
// 1 秒""分辨率 240px"就只能退钱，而用户已经等了半分钟。
//
// 分三段，每段都可注入替身（prepareDeps）：取源片 → 探测 → 抽末帧。
// 真实实现的一头是 nfsinput（task: 引用 / SSRF 校验过的 URL），另一头是 ffprobe / ffmpeg
// （Docker 镜像里已有，内容审核在用，见 Dockerfile 注释）。

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// 续写的输入契约（与 MiniMax Design 的官方 inputContract 对齐）。
const (
	// continuationMinSourceSeconds 源片下限（官方 video-extension 的 min_duration_ms=2000）。
	continuationMinSourceSeconds = 2.0
	// continuationMaxSourceSeconds 源片上限（官方 max_duration_ms=15000）。
	continuationMaxSourceSeconds = 15.0
	// continuationMinSourceEdgePx 源片短边下限（引擎 fl2va 关键帧的 256px 下限）。
	continuationMinSourceEdgePx = 256
	// MaxSourceBytes 源片字节上限（官方 max_bytes=52428800）。
	//
	// 导出是给抓取层用的：抓取时就把它当 cap，超限在**下载途中**就被截断，
	// 而不是整块读进内存再比一遍（那样一个配大了上限的源片会先把内存吃掉）。
	MaxSourceBytes = 50 << 20
	// continuationMaxFirstFrameBytes 首帧 PNG 的字节上限：超了就先缩，再超就转 JPEG。
	// 首帧会以 data URI 进请求体，几 MB 的 base64 会把内存和日志一起撑爆。
	continuationMaxFirstFrameBytes = 6 << 20
	// continuationFrameMaxEdge 首帧的最长边上限（引擎会按输出画布裁切，给太多没意义）。
	continuationFrameMaxEdge = 2048
)

// ContinuationError 续写预处理的失败，带稳定 code 供上游映射错误响应。
type ContinuationError struct {
	Code    string
	Message string
	// UserMessage 面向用户的说法；空则用 Message。
	UserMessage string
}

func (e *ContinuationError) Error() string { return e.Message }

func continuationErr(code, format string, args ...any) *ContinuationError {
	return &ContinuationError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// ContinuationProbe 源片的探测结果。
type ContinuationProbe struct {
	DurationSeconds float64
	Width           int
	Height          int
	FPS             float64
	HasAudio        bool
}

// ContinuationPrepareOptions 一次续写预处理的输入。
type ContinuationPrepareOptions struct {
	// Source 源片：`task:<id>`（本站任务产物）、http(s) URL、或 data: base64。
	Source string
	// UserID 引用 task: 时的归属校验主体。
	UserID int
	// TotalSeconds 客户要的**最终总时长**（官方语义）。0 = 未指定。
	TotalSeconds int
}

// ContinuationPrepareResult 预处理产物。
type ContinuationPrepareResult struct {
	// FirstFrameDataURL 源片末帧的 PNG/JPEG data URI，直接可作为 i2v 的 image 输入。
	FirstFrameDataURL string
	Probe             ContinuationProbe
	// GenerateSeconds 生成段应请求的秒数 = 最终总时长 − 源片时长（向上取整到整秒）。
	GenerateSeconds int
}

// PrepareDeps 三段依赖，测试与调用方注入替身用。
type PrepareDeps struct {
	// fetch 取源片字节。默认按 source 形态分派到 nfsinput。
	fetch func(ctx context.Context, opts ContinuationPrepareOptions) ([]byte, string, error)
	// probe 探测源片（ffprobe）。默认 ffprobeProbe。
	probe func(ctx context.Context, path string) (ContinuationProbe, error)
	// lastFrame 抽取最后一帧（ffmpeg），返回图片字节与其 MIME。
	lastFrame func(ctx context.Context, path string, probe ContinuationProbe) ([]byte, string, error)
}

func defaultPrepareDeps() PrepareDeps {
	return PrepareDeps{fetch: fetchContinuationSource, probe: ffprobeContinuationProbe, lastFrame: ffmpegLastFrame}
}

// PrepareContinuation 取源片 → 校验 → 抽末帧，产出 i2v 的首帧与生成段时长。
//
// 校验顺序刻意是"先便宜的、先本地能判的"：形状 → 字节 → 探测 → 时长/像素 → 末帧。
// 末帧最贵（要解码整段），放最后。
func PrepareContinuation(ctx context.Context, opts ContinuationPrepareOptions, deps *PrepareDeps) (*ContinuationPrepareResult, error) {
	d := defaultPrepareDeps()
	if deps != nil {
		if deps.fetch != nil {
			d.fetch = deps.fetch
		}
		if deps.probe != nil {
			d.probe = deps.probe
		}
		if deps.lastFrame != nil {
			d.lastFrame = deps.lastFrame
		}
	}

	source := strings.TrimSpace(opts.Source)
	if source == "" {
		return nil, continuationErr("source_missing", "续写需要源视频:请在 metadata.source_video 里给出 URL 或 task:<id>")
	}

	bytes, ext, err := d.fetch(ctx, opts)
	if err != nil {
		return nil, continuationErr("source_unreadable", "源视频读取失败: %v", err)
	}
	if len(bytes) == 0 {
		return nil, continuationErr("source_unreadable", "源视频是空文件")
	}
	if len(bytes) > MaxSourceBytes {
		return nil, continuationErr("source_too_large", "源视频 %.1f MB 超过上限 %d MB", float64(len(bytes))/(1<<20), MaxSourceBytes>>20)
	}

	// 探测与抽帧都需要一个本地文件：mp4 的 moov 可能在尾部，管道不可 seek。
	dir, err := os.MkdirTemp("", "h3-cont-")
	if err != nil {
		return nil, continuationErr("local_media_failed", "创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(dir)

	if ext == "" || !strings.HasPrefix(ext, ".") {
		ext = ".mp4"
	}
	path := filepath.Join(dir, "source"+ext)
	if err := os.WriteFile(path, bytes, 0o600); err != nil {
		return nil, continuationErr("local_media_failed", "写入临时源视频失败: %v", err)
	}

	probe, err := d.probe(ctx, path)
	if err != nil {
		return nil, continuationErr("source_unreadable", "源视频无法解析: %v", err)
	}
	if probe.DurationSeconds <= 0 {
		// ffprobe 拿不到时长时退回纯 Go 解析（只支持 mp4 系，但不需要外部程序）。
		if f, ferr := os.Open(path); ferr == nil {
			if sec, derr := common.GetVideoDuration(f); derr == nil && sec > 0 {
				probe.DurationSeconds = sec
			}
			_ = f.Close()
		}
	}
	if probe.DurationSeconds <= 0 {
		return nil, continuationErr("source_unreadable", "读不出源视频时长，无法续写")
	}
	if probe.DurationSeconds < continuationMinSourceSeconds-0.05 {
		return nil, continuationErr("source_too_short", "源视频 %.2f s 短于下限 %.0f s", probe.DurationSeconds, continuationMinSourceSeconds)
	}
	if probe.DurationSeconds > continuationMaxSourceSeconds+0.05 {
		return nil, continuationErr("source_too_long", "源视频 %.2f s 超过上限 %.0f s", probe.DurationSeconds, continuationMaxSourceSeconds)
	}
	if probe.Width > 0 && probe.Height > 0 {
		if probe.Width < continuationMinSourceEdgePx || probe.Height < continuationMinSourceEdgePx {
			return nil, continuationErr("source_too_small", "源视频 %dx%d 小于最小短边 %dpx", probe.Width, probe.Height, continuationMinSourceEdgePx)
		}
	}

	genSeconds, err := continuationGenerateSeconds(opts.TotalSeconds, probe.DurationSeconds)
	if err != nil {
		return nil, err
	}

	frame, mime, err := d.lastFrame(ctx, path, probe)
	if err != nil {
		return nil, continuationErr("frame_extract_failed", "抽取源视频末帧失败: %v", err)
	}
	if len(frame) == 0 {
		return nil, continuationErr("frame_extract_failed", "抽取到的末帧是空文件")
	}

	return &ContinuationPrepareResult{
		FirstFrameDataURL: "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(frame),
		Probe:             probe,
		GenerateSeconds:   genSeconds,
	}, nil
}

// continuationGenerateSeconds 由"最终总时长 − 源片时长"算出生成段秒数。
//
// 与官方的差异写在这里，而不是散在调用方：官方续写是模型内部接续，能做到 +1 s；
// 我们是"抽末帧 + 重新生成一段"，生成段受引擎最短档约束（请求 1 s → 实际 1.625 s），
// 所以可用区间是 [ceil(源)+1, 20]，且总时长必须严格大于源片。
func continuationGenerateSeconds(totalSeconds int, sourceSeconds float64) (int, error) {
	if totalSeconds <= 0 {
		return 0, continuationErr("duration_missing", "续写需要 duration=最终总时长(源视频 %.2f s)", sourceSeconds)
	}
	ceilSource := int(sourceSeconds)
	if float64(ceilSource) < sourceSeconds {
		ceilSource++
	}
	if totalSeconds < ceilSource+1 {
		return 0, continuationErr("duration_below_source",
			"最终总时长 %d s 必须大于源视频(%.2f s),至少 %d s", totalSeconds, sourceSeconds, ceilSource+1)
	}
	gen := totalSeconds - ceilSource
	if gen < 1 {
		gen = 1
	}
	return gen, nil
}
