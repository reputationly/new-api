package watermark

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/setting/system_setting"
)

type MediaKind = system_setting.MediaKind

var (
	availabilityOnce sync.Once
	ffmpegPath       string
	ffprobePath      string
	fontPath         string
	available        bool
	availabilityErr  string
	imageSlots       = make(chan struct{}, 8)
	videoSlots       = make(chan struct{}, 2)
)

func Available() (bool, string) {
	availabilityOnce.Do(func() {
		var err error
		ffmpegPath, err = exec.LookPath("ffmpeg")
		if err != nil {
			availabilityErr = "ffmpeg 未安装"
			return
		}
		ffprobePath, err = exec.LookPath("ffprobe")
		if err != nil {
			availabilityErr = "ffprobe 未安装"
			return
		}
		fontPath = os.Getenv("WATERMARK_FONT_FILE")
		if fontPath == "" {
			fontPath = "/usr/share/fonts/truetype/wqy/wqy-microhei.ttc"
		}
		if info, statErr := os.Stat(fontPath); statErr != nil || info.IsDir() {
			availabilityErr = "水印字体文件不存在"
			return
		}
		available = true
	})
	return available, availabilityErr
}

func KindFromExt(ext string) (MediaKind, bool) {
	switch strings.TrimPrefix(strings.ToLower(ext), ".") {
	case "png", "jpg", "jpeg", "webp", "gif", "bmp":
		return system_setting.MediaKindImage, true
	case "mp4", "mov", "webm", "mkv":
		return system_setting.MediaKindVideo, true
	default:
		return "", false
	}
}

func Apply(ctx context.Context, inPath, outPath string, kind MediaKind, outFormat string, tpl system_setting.WatermarkTemplate) error {
	ready, reason := Available()
	if !ready {
		return fmt.Errorf("watermark unavailable: %s", reason)
	}
	var slots chan struct{}
	timeout := 30 * time.Second
	switch kind {
	case system_setting.MediaKindImage:
		slots = imageSlots
	case system_setting.MediaKindVideo:
		slots = videoSlots
		timeout = 300 * time.Second
	default:
		return fmt.Errorf("unsupported media kind %q", kind)
	}
	select {
	case slots <- struct{}{}:
		defer func() { <-slots }()
	case <-ctx.Done():
		return ctx.Err()
	}

	width, height, err := probeDimensions(ctx, inPath)
	if err != nil {
		return err
	}
	shortSide := min(width, height)
	fontSize := max(1, int(float64(shortSide)*tpl.FontScale+0.5))
	margin := max(1, int(float64(shortSide)*0.03+0.5))
	borderWidth := max(1, fontSize/16)
	textFile, err := os.CreateTemp("", "watermark-text-*")
	if err != nil {
		return err
	}
	defer func() {
		_ = textFile.Close()
		_ = os.Remove(textFile.Name())
	}()
	if _, err = textFile.WriteString(tpl.Text); err != nil {
		return err
	}
	if err = textFile.Close(); err != nil {
		return err
	}

	x, y := watermarkPosition(tpl.Position, margin)
	fontColor := strings.TrimPrefix(tpl.Color, "#")
	opacity := strconv.FormatFloat(tpl.Opacity, 'f', -1, 64)
	borderOpacity := strconv.FormatFloat(tpl.Opacity*0.6, 'f', -1, 64)
	filter := fmt.Sprintf(
		"drawtext=fontfile='%s':textfile='%s':fontsize=%d:fontcolor=0x%s@%s:borderw=%d:bordercolor=black@%s:x=%s:y=%s",
		escapeDrawtextPath(fontPath), escapeDrawtextPath(textFile.Name()), fontSize, fontColor,
		opacity, borderWidth, borderOpacity, x, y,
	)

	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	args := []string{"-nostdin", "-y", "-hide_banner", "-loglevel", "error", "-protocol_whitelist", "file", "-i", inPath, "-vf", filter}
	if kind == system_setting.MediaKindImage {
		args = append(args, "-frames:v", "1")
		switch strings.TrimPrefix(strings.ToLower(outFormat), ".") {
		case "png":
			args = append(args, "-c:v", "png")
		case "jpg", "jpeg":
			args = append(args, "-q:v", "2")
		case "webp":
			args = append(args, "-c:v", "libwebp", "-quality", "90")
		default:
			return fmt.Errorf("unsupported image output format %q", outFormat)
		}
	} else {
		if format := strings.TrimPrefix(strings.ToLower(outFormat), "."); format != "mp4" && format != "mov" {
			return fmt.Errorf("unsupported video output format %q", outFormat)
		}
		args = append(args, "-map", "0:v:0", "-map", "0:a?", "-c:v", "libx264", "-preset", "veryfast", "-crf", "20",
			"-pix_fmt", "yuv420p", "-c:a", "copy", "-movflags", "+faststart", "-threads", "2")
	}
	args = append(args, outPath)
	cmd := exec.CommandContext(cmdCtx, ffmpegPath, args...)
	var stderr tailBuffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if cmdCtx.Err() != nil {
			return fmt.Errorf("ffmpeg %s: %w: %s", cmdCtx.Err(), err, stderr.String())
		}
		return fmt.Errorf("ffmpeg failed: %w: %s", err, stderr.String())
	}
	return nil
}

func probeDimensions(ctx context.Context, input string) (int, int, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, ffprobePath, "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=width,height", "-of", "csv=s=x:p=0", input)
	out, err := cmd.Output()
	if err != nil {
		return 0, 0, fmt.Errorf("ffprobe failed: %w", err)
	}
	var width, height int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%dx%d", &width, &height); err != nil || width <= 0 || height <= 0 {
		return 0, 0, fmt.Errorf("ffprobe returned invalid dimensions %q", strings.TrimSpace(string(out)))
	}
	return width, height, nil
}

func watermarkPosition(position string, margin int) (string, string) {
	m := strconv.Itoa(margin)
	switch position {
	case "top_left":
		return m, m
	case "top_right":
		return "w-text_w-" + m, m
	case "bottom_left":
		return m, "h-text_h-" + m
	case "center":
		return "(w-text_w)/2", "(h-text_h)/2"
	default:
		return "w-text_w-" + m, "h-text_h-" + m
	}
}

func escapeDrawtextPath(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, ":", `\:`)
	return strings.ReplaceAll(value, "'", `\'`)
}

type tailBuffer struct {
	buf []byte
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	const max = 4096
	b.buf = append(b.buf, p...)
	if len(b.buf) > max {
		b.buf = b.buf[len(b.buf)-max:]
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	return strings.TrimSpace(string(b.buf))
}
