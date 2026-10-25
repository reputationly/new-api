package watermark

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/setting/system_setting"
)

func TestKindFromExt(t *testing.T) {
	for _, ext := range []string{"png", ".jpg", "JPEG", "webp", "gif", "bmp"} {
		if kind, ok := KindFromExt(ext); !ok || kind != system_setting.MediaKindImage {
			t.Errorf("KindFromExt(%q) = %q, %v; want image, true", ext, kind, ok)
		}
	}
	for _, ext := range []string{"mp4", ".mov", "WEBM", "mkv"} {
		if kind, ok := KindFromExt(ext); !ok || kind != system_setting.MediaKindVideo {
			t.Errorf("KindFromExt(%q) = %q, %v; want video, true", ext, kind, ok)
		}
	}
	for _, ext := range []string{"", "mp3", "txt"} {
		if kind, ok := KindFromExt(ext); ok {
			t.Errorf("KindFromExt(%q) = %q, true; want unsupported", ext, kind)
		}
	}
}

func TestApplyFormats(t *testing.T) {
	if ready, reason := Available(); !ready {
		t.Skip(reason)
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	dir := t.TempDir()
	template := system_setting.WatermarkTemplate{
		Name: "test", Text: "AI生成", Position: "bottom_right",
		FontScale: 0.08, Opacity: 0.6, Color: "#FFFFFF",
	}
	tests := []struct {
		name      string
		input     string
		output    string
		format    string
		kind      system_setting.MediaKind
		generate  []string
		wantAlpha bool
		wantAudio bool
	}{
		{
			name: "png alpha", input: "input.png", output: "output.png", format: "png",
			kind: system_setting.MediaKindImage, wantAlpha: true,
			generate: []string{"-f", "lavfi", "-i", "color=c=red@0.3:s=320x180:d=1,format=rgba", "-frames:v", "1", "-pix_fmt", "rgba"},
		},
		{
			name: "jpg", input: "input.jpg", output: "output.jpg", format: "jpg",
			kind:     system_setting.MediaKindImage,
			generate: []string{"-f", "lavfi", "-i", "color=c=blue:s=320x180:d=1", "-frames:v", "1"},
		},
		{
			name: "webp", input: "input.webp", output: "output.webp", format: "webp",
			kind:     system_setting.MediaKindImage,
			generate: []string{"-f", "lavfi", "-i", "color=c=green:s=320x180:d=1", "-frames:v", "1"},
		},
		{
			name: "mp4 audio", input: "input-audio.mp4", output: "output-audio.mp4", format: "mp4",
			kind: system_setting.MediaKindVideo, wantAudio: true,
			generate: []string{
				"-f", "lavfi", "-i", "testsrc2=s=320x180:r=10:d=1",
				"-f", "lavfi", "-i", "sine=frequency=1000:duration=1",
				"-shortest", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac",
			},
		},
		{
			name: "mp4 no audio", input: "input-silent.mp4", output: "output-silent.mp4", format: "mp4",
			kind:     system_setting.MediaKindVideo,
			generate: []string{"-f", "lavfi", "-i", "testsrc2=s=320x180:r=10:d=1", "-c:v", "libx264", "-pix_fmt", "yuv420p"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := filepath.Join(dir, test.input)
			output := filepath.Join(dir, test.output)
			args := append([]string{"-nostdin", "-y", "-hide_banner", "-loglevel", "error"}, test.generate...)
			args = append(args, input)
			if out, err := exec.Command(ffmpeg, args...).CombinedOutput(); err != nil {
				t.Fatalf("generate input: %v: %s", err, out)
			}
			if test.wantAlpha {
				inputProbe := ffprobe(t, input, "-select_streams", "v:0", "-show_entries", "stream=pix_fmt", "-of", "default=noprint_wrappers=1")
				if !strings.Contains(pixelFormat(inputProbe), "a") {
					t.Fatalf("generated PNG input has no alpha: %s", inputProbe)
				}
			}
			if err := Apply(context.Background(), input, output, test.kind, test.format, template); err != nil {
				t.Fatalf("Apply(): %v", err)
			}
			inputBytes, err := os.ReadFile(input)
			if err != nil {
				t.Fatal(err)
			}
			outputBytes, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Equal(inputBytes, outputBytes) {
				t.Fatal("watermarked output bytes equal input")
			}
			probe := ffprobe(t, output, "-select_streams", "v:0", "-show_entries", "stream=width,height,pix_fmt", "-of", "default=noprint_wrappers=1")
			if !strings.Contains(probe, "width=320") || !strings.Contains(probe, "height=180") {
				t.Fatalf("output dimensions changed: %s", probe)
			}
			if test.wantAlpha && !strings.Contains(pixelFormat(probe), "a") {
				t.Fatalf("PNG output lost alpha: %s", probe)
			}
			streamTypes := ffprobe(t, output, "-show_entries", "stream=codec_type", "-of", "csv=p=0")
			hasAudio := strings.Contains(streamTypes, "audio")
			if hasAudio != test.wantAudio {
				t.Fatalf("audio stream present = %v, want %v; streams: %s", hasAudio, test.wantAudio, streamTypes)
			}
		})
	}

	input := filepath.Join(dir, "unsupported.png")
	if out, err := exec.Command(ffmpeg, "-nostdin", "-y", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=c=blue:s=320x180:d=1", "-frames:v", "1", input).CombinedOutput(); err != nil {
		t.Fatalf("generate unsupported-format input: %v: %s", err, out)
	}
	if err := Apply(context.Background(), input, filepath.Join(dir, "unsupported.gif"),
		system_setting.MediaKindImage, "gif", template); err == nil {
		t.Fatal("unsupported image format succeeded")
	}
}

func pixelFormat(probe string) string {
	for _, line := range strings.Split(probe, "\n") {
		if strings.HasPrefix(line, "pix_fmt=") {
			return strings.TrimPrefix(line, "pix_fmt=")
		}
	}
	return ""
}

func ffprobe(t *testing.T, input string, args ...string) string {
	t.Helper()
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe is not installed")
	}
	cmdArgs := append([]string{"-v", "error"}, args...)
	cmdArgs = append(cmdArgs, input)
	out, err := exec.Command(ffprobe, cmdArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe: %v: %s", err, out)
	}
	return string(out)
}
