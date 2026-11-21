package continuation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type recordedCall struct {
	name string
	args []string
}

func concatFakes(t *testing.T, srcHasAudio, genHasAudio bool, copyFails bool) (*ConcatDeps, *[]recordedCall) {
	t.Helper()
	calls := &[]recordedCall{}
	deps := &ConcatDeps{
		probe: func(_ context.Context, path string) (ContinuationProbe, error) {
			if strings.Contains(filepath.Base(path), "generate") {
				return ContinuationProbe{Width: 1344, Height: 768, FPS: 24, HasAudio: genHasAudio, DurationSeconds: 4.458}, nil
			}
			return ContinuationProbe{Width: 640, Height: 360, FPS: 30, HasAudio: srcHasAudio, DurationSeconds: 3}, nil
		},
		run: func(_ context.Context, name string, args ...string) ([]byte, error) {
			*calls = append(*calls, recordedCall{name: name, args: args})
			joined := strings.Join(args, " ")
			if strings.Contains(joined, "-c copy") {
				if copyFails {
					return nil, errors.New("copy failed")
				}
				// 模拟产出：concat 的输出路径是最后一个参数。
				_ = os.WriteFile(args[len(args)-1], []byte("mp4"), 0o600)
				return nil, nil
			}
			if out := args[len(args)-1]; !strings.HasPrefix(out, "-") {
				_ = os.WriteFile(out, []byte("mp4"), 0o600)
			}
			return nil, nil
		},
	}
	return deps, calls
}

func concatInputs(t *testing.T) (dir, src, gen, out string) {
	t.Helper()
	dir = t.TempDir()
	src = filepath.Join(dir, "source.mp4")
	gen = filepath.Join(dir, "generate.mp4")
	out = filepath.Join(dir, "merged.mp4")
	if err := os.WriteFile(src, []byte("src"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gen, []byte("gen"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, src, gen, out
}

// 规范化参数是拼接质量的全部：画布按生成段、帧率统一 24、无音轨补静音。
func TestConcatNormalizesBothSides(t *testing.T) {
	_, src, gen, out := concatInputs(t)
	deps, calls := concatFakes(t, false /*源无音轨*/, true /*生成段有*/, false)

	res, err := Concat(context.Background(), src, gen, out, deps)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res.Width != 1344 || res.Height != 768 || res.FPS != 24 {
		t.Fatalf("画布应取生成段 1344x768@24, got %+v", res)
	}
	if res.SourceHasAudio || !res.GenerateHasAudio {
		t.Fatalf("音轨判定不对: %+v", res)
	}

	var srcNorm, genNorm, concatCall string
	for _, c := range *calls {
		joined := strings.Join(c.args, " ")
		switch {
		case strings.Contains(joined, "source_normalized.mp4"):
			srcNorm = joined
		case strings.Contains(joined, "generate_normalized.mp4"):
			genNorm = joined
		case strings.Contains(joined, "-f concat"):
			concatCall = joined
		}
	}
	if srcNorm == "" || genNorm == "" || concatCall == "" {
		t.Fatalf("缺少预期调用: src=%q gen=%q concat=%q", srcNorm, genNorm, concatCall)
	}
	if !strings.Contains(srcNorm, "scale=1344:768:force_original_aspect_ratio=increase") ||
		!strings.Contains(srcNorm, "crop=1344:768") {
		t.Fatalf("源片应按生成段画布缩放+裁切: %s", srcNorm)
	}
	if !strings.Contains(srcNorm, "fps=24") {
		t.Fatalf("帧率应统一到 24: %s", srcNorm)
	}
	if !strings.Contains(srcNorm, "anullsrc") || !strings.Contains(srcNorm, "-shortest") {
		t.Fatalf("无音轨的一侧应补等长静音: %s", srcNorm)
	}
	if strings.Contains(genNorm, "anullsrc") {
		t.Fatalf("有音轨的一侧不该补静音: %s", genNorm)
	}
	if !strings.Contains(concatCall, "-c copy") {
		t.Fatalf("应优先流拷贝: %s", concatCall)
	}
}

// copy 失败要回退重编码，而不是直接判失败（不同源片的 profile 差异很常见）。
func TestConcatFallsBackToReencode(t *testing.T) {
	_, src, gen, out := concatInputs(t)
	deps, calls := concatFakes(t, true, true, true /*copy 失败*/)

	if _, err := Concat(context.Background(), src, gen, out, deps); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	var reencoded bool
	for _, c := range *calls {
		joined := strings.Join(c.args, " ")
		if strings.Contains(joined, "-f concat") && strings.Contains(joined, "libx264") {
			reencoded = true
		}
	}
	if !reencoded {
		t.Fatal("copy 失败后应回退重编码")
	}
	if fi, err := os.Stat(out); err != nil || fi.Size() == 0 {
		t.Fatal("回退路径没有产出文件")
	}
}

// 轮询重试会重复进入：已有非空产物直接复用，不重复烧 CPU。
func TestConcatIsIdempotentOnExistingOutput(t *testing.T) {
	_, src, gen, out := concatInputs(t)
	if err := os.WriteFile(out, []byte("already"), 0o600); err != nil {
		t.Fatal(err)
	}
	deps, calls := concatFakes(t, true, true, false)

	res, err := Concat(context.Background(), src, gen, out, deps)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res.OutPath != out {
		t.Fatalf("OutPath = %q", res.OutPath)
	}
	if len(*calls) != 0 {
		t.Fatalf("已有产物时不该再跑 ffmpeg, 实际调用 %d 次", len(*calls))
	}
}

func TestConcatRejectsBadInputs(t *testing.T) {
	_, src, gen, out := concatInputs(t)
	deps, _ := concatFakes(t, true, true, false)

	if _, err := Concat(context.Background(), "", gen, out, deps); err == nil {
		t.Fatal("缺源片应报错")
	}
	if _, err := Concat(context.Background(), src, filepath.Join(filepath.Dir(src), "missing.mp4"), out, deps); err == nil {
		t.Fatal("输入不存在应报错")
	}
}

func TestEscapeConcatPath(t *testing.T) {
	if got := escapeConcatPath("/a b/it's.mp4"); !strings.Contains(got, `'\''`) {
		t.Fatalf("单引号未转义: %s", got)
	}
}
