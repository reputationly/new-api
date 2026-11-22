package continuation

// 续写的拼接段：源片 + 生成段 → 一段完整视频。
//
// 引擎没有"接着一段已有视频继续生成"的能力，所以续写是"源片末帧当首帧生成一段"再
// 拼回去。拼接要解决的只有三件事，每一件不做都会在客户端表现为"接缝处炸了"：
//
//  1. **画布一致**：源片任意分辨率，生成段固定 768P 画布（如 1344x768）。不统一，
//     concat 直接失败或产生黑边/变形。这里统一按**生成段**的画布缩放并居中裁切
//     （`force_original_aspect_ratio=increase` + `crop`），保证不出现黑边。
//  2. **帧率一致**：统一到 24fps（引擎出片帧率）。帧率不同 concat 会得到时长错乱的片子。
//  3. **音轨对齐**：源片无音轨而生成段有（H3 默认 generate_audio=true）时，concat
//     的 `-c copy` 会因流数量不一致失败。给无音轨的一侧补等长静音轨。
//
// 音频策略（本期的选择）：两段音轨都保留，接缝处不做淡化 —— 淡化会改变总时长，
// 而总时长是客户买的东西。接缝突兀是已知代价，记在 docs/video-multimodal-plan.md。

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// ConcatDeps 可注入的命令执行与探测，测试用替身。
type ConcatDeps struct {
	probe func(ctx context.Context, path string) (ContinuationProbe, error)
	run   func(ctx context.Context, name string, args ...string) ([]byte, error)
}

func defaultConcatDeps() ConcatDeps {
	return ConcatDeps{probe: ffprobeContinuationProbe, run: runMediaCommand}
}

// ConcatResult 拼接结果。
type ConcatResult struct {
	OutPath string
	// Canvas 交付画布（取自生成段；探不出来时回退 1344x768）。
	Width  int
	Height int
	FPS    int
	// SourceHasAudio / GenerateHasAudio 记录两侧音轨情况，排障用（"为什么接缝没声音"）。
	SourceHasAudio   bool
	GenerateHasAudio bool
}

// Concat 把源片与生成段拼成一段，写到 outPath（必须与两者同一文件系统，通常是 NFS）。
//
// outPath 已存在且非空时直接返回 —— 轮询重试会重复进入这里，拼接是纯函数式操作
// （同样的输入必然得到同样的输出），不做幂等保护只会白烧 CPU。
func Concat(ctx context.Context, sourcePath, generatePath, outPath string, deps *ConcatDeps) (*ConcatResult, error) {
	d := defaultConcatDeps()
	if deps != nil {
		if deps.probe != nil {
			d.probe = deps.probe
		}
		if deps.run != nil {
			d.run = deps.run
		}
	}
	if strings.TrimSpace(sourcePath) == "" || strings.TrimSpace(generatePath) == "" || strings.TrimSpace(outPath) == "" {
		return nil, fmt.Errorf("拼接缺少输入:source=%q generate=%q out=%q", sourcePath, generatePath, outPath)
	}
	for _, p := range []string{sourcePath, generatePath} {
		if fi, err := os.Stat(p); err != nil || fi.Size() == 0 {
			return nil, fmt.Errorf("拼接输入不可读: %s", p)
		}
	}
	if fi, err := os.Stat(outPath); err == nil && fi.Size() > 0 {
		return &ConcatResult{OutPath: outPath}, nil
	}

	// 画布以**生成段**为准：它是交付档位（768P），源片按它缩放。
	genProbe, err := d.probe(ctx, generatePath)
	if err != nil {
		return nil, fmt.Errorf("探测生成段失败: %w", err)
	}
	srcProbe, err := d.probe(ctx, sourcePath)
	if err != nil {
		return nil, fmt.Errorf("探测源片失败: %w", err)
	}
	width, height, fps := genProbe.Width, genProbe.Height, common.H3FPS
	if width <= 0 || height <= 0 {
		width, height = 1344, 768
	}
	if genProbe.FPS > 0 {
		fps = int(genProbe.FPS + 0.5)
	}

	// 临时文件放**任务唯一的子目录**：上游 nfs_path 可能是多任务共享目录，
	// 固定文件名会让并发续写互相覆盖（而且失败时还会互相删掉对方的中间产物）。
	baseDir := filepath.Dir(outPath)
	workDir, err := os.MkdirTemp(baseDir, ".concat-")
	if err != nil {
		return nil, fmt.Errorf("创建拼接临时目录失败: %w", err)
	}
	defer os.RemoveAll(workDir)
	dir := workDir
	srcPrepared := filepath.Join(dir, "source_normalized.mp4")
	genPrepared := filepath.Join(dir, "generate_normalized.mp4")
	if err := normalizeForConcat(ctx, d, sourcePath, srcPrepared, width, height, fps, srcProbe.HasAudio); err != nil {
		return nil, fmt.Errorf("源片规范化失败: %w", err)
	}
	if err := normalizeForConcat(ctx, d, generatePath, genPrepared, width, height, fps, genProbe.HasAudio); err != nil {
		return nil, fmt.Errorf("生成段规范化失败: %w", err)
	}

	listPath := filepath.Join(dir, "concat_list.txt")
	list := "file '" + escapeConcatPath(srcPrepared) + "'\nfile '" + escapeConcatPath(genPrepared) + "'\n"
	if err := os.WriteFile(listPath, []byte(list), 0o600); err != nil {
		return nil, fmt.Errorf("写 concat 清单失败: %w", err)
	}
	defer os.Remove(listPath)
	defer os.Remove(srcPrepared)
	defer os.Remove(genPrepared)

	// 先试流拷贝（最快）；两段编码参数一致时必然成功。失败再回退重编码 ——
	// 不同 ffmpeg 版本/不同源片的 profile 差异会让 copy 失败，此时重编码是唯一出路。
	if _, err := d.run(ctx, "ffmpeg",
		"-nostdin", "-y", "-hide_banner", "-loglevel", "error",
		"-f", "concat", "-safe", "0", "-i", listPath,
		"-c", "copy", outPath,
	); err != nil {
		if _, rerr := d.run(ctx, "ffmpeg",
			"-nostdin", "-y", "-hide_banner", "-loglevel", "error",
			"-f", "concat", "-safe", "0", "-i", listPath,
			"-c:v", "libx264", "-preset", "veryfast", "-crf", "20", "-pix_fmt", "yuv420p",
			"-c:a", "aac", "-b:a", "128k",
			outPath,
		); rerr != nil {
			return nil, fmt.Errorf("拼接失败（copy 与重编码都不行）: %v / %v", err, rerr)
		}
	}
	if fi, err := os.Stat(outPath); err != nil || fi.Size() == 0 {
		return nil, fmt.Errorf("拼接没有产出可读文件: %s", outPath)
	}

	return &ConcatResult{
		OutPath:          outPath,
		Width:            width,
		Height:           height,
		FPS:              fps,
		SourceHasAudio:   srcProbe.HasAudio,
		GenerateHasAudio: genProbe.HasAudio,
	}, nil
}

// normalizeForConcat 把一侧输入规范到统一画布/帧率/编码；无音轨时补等长静音。
func normalizeForConcat(ctx context.Context, d ConcatDeps, in, out string, width, height, fps int, hasAudio bool) error {
	filter := fmt.Sprintf(
		"scale=%d:%d:force_original_aspect_ratio=increase,crop=%d:%d,fps=%d,setsar=1",
		width, height, width, height, fps)
	args := []string{"-nostdin", "-y", "-hide_banner", "-loglevel", "error", "-i", in}
	if !hasAudio {
		// 用 anullsrc 生成等长静音：`-shortest` 让它跟随视频长度，流数量与有音轨的一侧一致。
		args = append(args,
			"-f", "lavfi",
			"-i", "anullsrc=channel_layout=stereo:sample_rate=44100",
		)
	}
	args = append(args, "-vf", filter, "-c:v", "libx264", "-preset", "veryfast", "-crf", "20", "-pix_fmt", "yuv420p")
	if hasAudio {
		args = append(args, "-c:a", "aac", "-b:a", "128k", "-ar", "44100", "-ac", "2")
	} else {
		args = append(args, "-c:a", "aac", "-b:a", "128k", "-shortest")
	}
	args = append(args, "-movflags", "+faststart", out)
	_, err := d.run(ctx, "ffmpeg", args...)
	return err
}

// escapeConcatPath concat 清单里单引号需要转义（' → '\”），否则带引号的路径会把清单写坏。
func escapeConcatPath(p string) string {
	return strings.ReplaceAll(p, "'", `'\''`)
}
