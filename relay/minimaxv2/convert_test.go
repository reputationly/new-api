package minimaxv2

import (
	"net/http"
	"strings"
	"testing"
)

func convert(t *testing.T, body string) (map[string]any, *Snapshot) {
	t.Helper()
	out, snap, apiErr := ConvertCreateRequest([]byte(body))
	if apiErr != nil {
		t.Fatalf("unexpected error: %s", apiErr.Message)
	}
	return out, snap
}

func convertErr(t *testing.T, body string) *APIError {
	t.Helper()
	_, _, apiErr := ConvertCreateRequest([]byte(body))
	if apiErr == nil {
		t.Fatalf("expected error, got success")
	}
	return apiErr
}

func metadataOf(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	md, ok := body["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("metadata missing or wrong type: %#v", body["metadata"])
	}
	return md
}

func stringsOf(t *testing.T, v any) []string {
	t.Helper()
	list, ok := v.([]any)
	if !ok {
		t.Fatalf("expected []any, got %#v", v)
	}
	out := make([]string, 0, len(list))
	for _, e := range list {
		s, ok := e.(string)
		if !ok {
			t.Fatalf("expected string element, got %#v", e)
		}
		out = append(out, s)
	}
	return out
}

func TestConvertTextToVideo(t *testing.T) {
	body, snap := convert(t, `{
		"model": "MiniMax-H3",
		"content": [{"type": "text", "text": "a cat on a piano"}],
		"resolution": "768P",
		"duration": 6,
		"ratio": "16:9"
	}`)

	if body["model"] != "MiniMax-H3" || body["prompt"] != "a cat on a piano" {
		t.Fatalf("unexpected top level: %#v", body)
	}
	// 分辨率必须以档位词下发:像素串会让适配器用 AspectRatioFromSize 反推出一个
	// gcd 约分比例(如 26:15)覆盖掉具名比例,引擎解析不了。
	if body["size"] != "768P" {
		t.Fatalf("size = %v, want 768P", body["size"])
	}
	if body["duration"] != 6 {
		t.Fatalf("duration = %v, want 6", body["duration"])
	}
	if _, ok := body["images"]; ok {
		t.Fatalf("t2v must not carry top level images")
	}
	md := metadataOf(t, body)
	if md["task_type"] != taskTypeT2V {
		t.Fatalf("task_type = %v, want t2v", md["task_type"])
	}
	if md["aspect_ratio"] != "16:9" {
		t.Fatalf("aspect_ratio = %v, want 16:9", md["aspect_ratio"])
	}
	if snap.Resolution != "768P" || snap.Ratio != "16:9" || snap.Duration != 6 {
		t.Fatalf("snapshot = %#v", snap)
	}
	if snap.InputImageCount != 0 || snap.InputVideoCount != 0 {
		t.Fatalf("snapshot counts = %#v", snap)
	}
}

func TestConvertFrameRolesToTaskType(t *testing.T) {
	cases := []struct {
		name     string
		content  string
		taskType string
		images   []string
	}{
		{
			name:     "首帧",
			content:  `{"type":"image_url","role":"first_frame","image_url":{"url":"https://x/1.png"}}`,
			taskType: taskTypeI2V,
			images:   []string{"https://x/1.png"},
		},
		{
			name:     "role 缺省视作首帧",
			content:  `{"type":"image_url","image_url":{"url":"https://x/1.png"}}`,
			taskType: taskTypeI2V,
			images:   []string{"https://x/1.png"},
		},
		{
			// 与 i2v 的输入形态完全相同(都是 1 张图),靠张数推不出「这张是尾帧」,
			// 所以 role 必须被翻译成独立的 l2va。
			name:     "只给尾帧",
			content:  `{"type":"image_url","role":"last_frame","image_url":{"url":"https://x/2.png"}}`,
			taskType: taskTypeL2VA,
			images:   []string{"https://x/2.png"},
		},
		{
			name: "首尾帧",
			content: `{"type":"image_url","role":"last_frame","image_url":{"url":"https://x/2.png"}},
				{"type":"image_url","role":"first_frame","image_url":{"url":"https://x/1.png"}}`,
			taskType: taskTypeFLF2V,
			// 顶层 images 的顺序即语义:[0]=首帧、[1]=尾帧,与 content 里的书写顺序无关。
			images: []string{"https://x/1.png", "https://x/2.png"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := convert(t, `{
				"model": "MiniMax-H3",
				"content": [{"type":"text","text":"go"}, `+tc.content+`],
				"resolution": "768P",
				"duration": 5
			}`)
			md := metadataOf(t, body)
			if md["task_type"] != tc.taskType {
				t.Fatalf("task_type = %v, want %s", md["task_type"], tc.taskType)
			}
			got := stringsOf(t, body["images"])
			if strings.Join(got, ",") != strings.Join(tc.images, ",") {
				t.Fatalf("images = %v, want %v", got, tc.images)
			}
			// 帧约束模式画幅跟随首图,比例传了也被引擎静默忽略 —— 别下发。
			if _, ok := md["aspect_ratio"]; ok {
				t.Fatalf("frame task must not send aspect_ratio: %#v", md)
			}
		})
	}
}

func TestConvertReferencesGoToMetadataNotTopLevelImages(t *testing.T) {
	body, snap := convert(t, `{
		"model": "MiniMax-H3",
		"content": [
			{"type":"text","text":"go"},
			{"type":"image_url","role":"reference_image","image_url":{"url":"https://x/a.png"}},
			{"type":"video_url","role":"reference_video","video_url":{"url":"https://x/a.mp4"}},
			{"type":"audio_url","role":"reference_audio","audio_url":{"url":"https://x/a.wav"}}
		],
		"resolution": "480P",
		"duration": 8
	}`)

	// 绝不能把参考图放进顶层 images:doubao 侧对顶层 images 按张数推断 role
	// (1 张 = first_frame),单张参考图会被误判成首帧约束。
	if _, ok := body["images"]; ok {
		t.Fatalf("reference material must not go to top level images")
	}
	md := metadataOf(t, body)
	if md["task_type"] != taskTypeR2VA {
		t.Fatalf("task_type = %v, want r2va", md["task_type"])
	}
	if got := stringsOf(t, md["src_ref_images"]); len(got) != 1 || got[0] != "https://x/a.png" {
		t.Fatalf("src_ref_images = %v", got)
	}
	if got := stringsOf(t, md["reference_videos"]); len(got) != 1 || got[0] != "https://x/a.mp4" {
		t.Fatalf("reference_videos = %v", got)
	}
	if got := stringsOf(t, md["reference_audios"]); len(got) != 1 || got[0] != "https://x/a.wav" {
		t.Fatalf("reference_audios = %v", got)
	}
	// r2va 的 adaptive 就是引擎的默认 16:9;解析成具名值画布才算得出来,
	// 否则 480P 会静默变成 768P。
	if md["aspect_ratio"] != r2vaAdaptiveRatio || snap.Ratio != r2vaAdaptiveRatio {
		t.Fatalf("aspect_ratio = %v, snapshot ratio = %s", md["aspect_ratio"], snap.Ratio)
	}
	if snap.InputImageCount != 1 || snap.InputVideoCount != 1 {
		t.Fatalf("snapshot = %#v", snap)
	}
}

func TestConvertFrameAndReferenceAreMutuallyExclusive(t *testing.T) {
	err := convertErr(t, `{
		"model":"MiniMax-H3",
		"content":[
			{"type":"text","text":"go"},
			{"type":"image_url","role":"first_frame","image_url":{"url":"https://x/1.png"}},
			{"type":"image_url","role":"reference_image","image_url":{"url":"https://x/2.png"}}
		],
		"resolution":"768P","duration":5}`)
	if err.StatusCode != http.StatusBadRequest || !strings.Contains(err.Message, "mutually exclusive") {
		t.Fatalf("unexpected error: %d %s", err.StatusCode, err.Message)
	}
}

func TestConvertRejectsUnsupportedInputs(t *testing.T) {
	base := func(extra, content string) string {
		if content == "" {
			content = `{"type":"text","text":"go"}`
		}
		return `{"model":"MiniMax-H3","content":[` + content + `],"duration":5` + extra + `}`
	}
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			// 自建只到 768P,2K 依赖闭源的 H3-Regenerate-2K。
			name: "2K",
			body: base(`,"resolution":"2K","ratio":"16:9"`, ""),
			want: "2K is not supported",
		},
		{
			// 那是 MiniMax 的文件库,我们只吃 URL / base64。
			name: "mm_file",
			body: base(`,"resolution":"768P"`, `{"type":"text","text":"go"},{"type":"image_url","role":"first_frame","image_url":{"url":"mm_file://12345"}}`),
			want: "mm_file://",
		},
		{
			// 回调是独立基础设施,不在本兼容层范围内;静默丢弃会让调用方白等推送。
			name: "callback_url",
			body: base(`,"resolution":"768P","ratio":"16:9","callback_url":"https://cb"`, ""),
			want: "callback_url is not supported",
		},
		{
			name: "t2v 必须给具名比例",
			body: base(`,"resolution":"768P","ratio":"adaptive"`, ""),
			want: "explicit named ratio",
		},
		{
			name: "t2v 不给比例",
			body: base(`,"resolution":"768P"`, ""),
			want: "explicit named ratio",
		},
		{
			name: "比例不在枚举内",
			body: base(`,"resolution":"768P","ratio":"26:15"`, ""),
			want: "ratio",
		},
		{
			// 帧约束的画幅跟随输入图,网关不解码图片就换算不出 480P 画布 ——
			// 与其静默出 768P 却回显 480P,不如就地说清楚。
			name: "480P 遇上帧约束",
			body: base(`,"resolution":"480P"`, `{"type":"text","text":"go"},{"type":"image_url","role":"first_frame","image_url":{"url":"https://x/1.png"}}`),
			want: "480P is not available",
		},
		{
			name: "缺 resolution",
			body: `{"model":"MiniMax-H3","content":[{"type":"text","text":"go"}],"duration":5,"ratio":"16:9"}`,
			want: "resolution is required",
		},
		{
			name: "缺 duration",
			body: `{"model":"MiniMax-H3","content":[{"type":"text","text":"go"}],"resolution":"768P","ratio":"16:9"}`,
			want: "duration is required",
		},
		{
			name: "duration 越界",
			body: `{"model":"MiniMax-H3","content":[{"type":"text","text":"go"}],"resolution":"768P","ratio":"16:9","duration":16}`,
			want: "between 4 and 15",
		},
		{
			name: "没有 text",
			body: `{"model":"MiniMax-H3","content":[{"type":"image_url","image_url":{"url":"https://x/1.png"}}],"resolution":"768P","duration":5}`,
			want: "exactly one non-empty text",
		},
		{
			name: "两个 text",
			body: base(`,"resolution":"768P","ratio":"16:9"`, `{"type":"text","text":"a"},{"type":"text","text":"b"}`),
			want: "exactly one non-empty text",
		},
		{
			// 引擎侧同样硬校验:独立音频参考必须搭配视觉参考。
			name: "纯音频参考",
			body: base(`,"resolution":"768P"`, `{"type":"text","text":"go"},{"type":"audio_url","audio_url":{"url":"https://x/a.wav"}}`),
			want: "requires at least one reference_image or reference_video",
		},
		{
			name: "两张首帧",
			body: base(`,"resolution":"768P"`, `{"type":"text","text":"go"},{"type":"image_url","role":"first_frame","image_url":{"url":"https://x/1.png"}},{"type":"image_url","role":"first_frame","image_url":{"url":"https://x/2.png"}}`),
			want: "at most one image with role=first_frame",
		},
		{
			name: "role 与 type 不匹配",
			body: base(`,"resolution":"768P"`, `{"type":"video_url","role":"first_frame","video_url":{"url":"https://x/a.mp4"}}`),
			want: "not valid for type=video_url",
		},
		{
			name: "未知 content type",
			body: base(`,"resolution":"768P","ratio":"16:9"`, `{"type":"text","text":"go"},{"type":"file_url"}`),
			want: "is not supported",
		},
		{
			name: "缺 model",
			body: `{"content":[{"type":"text","text":"go"}],"resolution":"768P","ratio":"16:9","duration":5}`,
			want: "model is required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := convertErr(t, tc.body)
			if err.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", err.StatusCode)
			}
			if err.Type != ErrTypeBadRequest {
				t.Fatalf("type = %s, want %s", err.Type, ErrTypeBadRequest)
			}
			if !strings.Contains(err.Message, tc.want) {
				t.Fatalf("message %q does not contain %q", err.Message, tc.want)
			}
		})
	}
}

func TestConvertReferenceCountCaps(t *testing.T) {
	build := func(kind, role, url string, n int) string {
		items := []string{`{"type":"text","text":"go"}`}
		for i := 0; i < n; i++ {
			items = append(items, `{"type":"`+kind+`","role":"`+role+`","`+kind+`":{"url":"`+url+`"}}`)
		}
		return `{"model":"MiniMax-H3","content":[` + strings.Join(items, ",") + `],"resolution":"768P","duration":5}`
	}
	if err := convertErr(t, build("image_url", roleReferenceImage, "https://x/a.png", maxReferenceImages+1)); !strings.Contains(err.Message, "reference_image") {
		t.Fatalf("unexpected: %s", err.Message)
	}
	if err := convertErr(t, build("video_url", roleReferenceVideo, "https://x/a.mp4", maxReferenceVideos+1)); !strings.Contains(err.Message, "reference_video") {
		t.Fatalf("unexpected: %s", err.Message)
	}
	// 恰好压线要通过。
	if _, _, apiErr := ConvertCreateRequest([]byte(build("image_url", roleReferenceImage, "https://x/a.png", maxReferenceImages))); apiErr != nil {
		t.Fatalf("unexpected error at cap: %s", apiErr.Message)
	}
}

func TestConvertAcceptsExtension480PForNonFrameTasks(t *testing.T) {
	body, snap := convert(t, `{
		"model":"MiniMax-H3",
		"content":[{"type":"text","text":"go"}],
		"resolution":"480p","duration":4,"ratio":"9:16"}`)
	// 480P 是我们的扩展档,如实回显,不映射成官方枚举里的别的值。
	if body["size"] != "480P" || snap.Resolution != "480P" {
		t.Fatalf("size = %v, snapshot = %#v", body["size"], snap)
	}
}
