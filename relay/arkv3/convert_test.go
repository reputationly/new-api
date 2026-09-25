package arkv3

import (
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/stretchr/testify/require"
)

func convert(t *testing.T, raw string) (map[string]any, *Snapshot) {
	t.Helper()
	body, snap, apiErr := ConvertCreateRequest([]byte(raw))
	require.Nil(t, apiErr, "unexpected error: %v", apiErr)
	return body, snap
}

func convertErr(t *testing.T, raw string) *APIError {
	t.Helper()
	_, _, apiErr := ConvertCreateRequest([]byte(raw))
	require.NotNil(t, apiErr, "expected an error")
	return apiErr
}

func metadataOf(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	md, ok := body["metadata"].(map[string]any)
	require.True(t, ok, "metadata missing")
	return md
}

// 文档首页那个最小请求：纯文生视频。
func TestConvertMinimalTextToVideo(t *testing.T) {
	body, snap := convert(t, `{
		"model": "doubao-seedance-2-0-260128",
		"content": [{"type": "text", "text": "清晨的海边，一架纸飞机迎着风飞行"}],
		"duration": 5,
		"resolution": "720p",
		"ratio": "16:9"
	}`)

	require.Equal(t, "doubao-seedance-2-0-260128", body["model"])
	require.Equal(t, "清晨的海边，一架纸飞机迎着风飞行", body["prompt"])
	// 顶层 duration 是计费维度的唯一来源（relaycommon.videoPerCallSeconds 只认它）。
	require.Equal(t, 5, body["duration"])
	require.NotContains(t, body, "images")

	md := metadataOf(t, body)
	require.Equal(t, taskTypeT2V, md["task_type"])
	require.Equal(t, "720p", md["resolution"])
	require.Equal(t, "16:9", md["ratio"])
	require.Equal(t, 5, md["duration"])

	require.Equal(t, "720p", snap.Resolution)
	require.Equal(t, "16:9", snap.Ratio)
	require.Equal(t, 5, snap.Duration)
}

// 帧约束走顶层 images[]（顺序即语义），参考媒体走 metadata 的三个键。
// 两者混为一谈是本转换最容易写错的地方：把参考图塞进 images 会被下游按张数
// 推断成首帧约束，语义完全不同。
func TestConvertSplitsFramesFromReferences(t *testing.T) {
	t.Run("首帧", func(t *testing.T) {
		body, _ := convert(t, `{
			"model": "m",
			"content": [
				{"type": "text", "text": "推镜"},
				{"type": "image_url", "image_url": {"url": "https://x/first.jpg"}, "role": "first_frame"}
			]
		}`)
		require.Equal(t, []any{"https://x/first.jpg"}, body["images"])
		require.Equal(t, taskTypeI2V, metadataOf(t, body)["task_type"])
	})

	t.Run("首尾帧按顺序", func(t *testing.T) {
		// 尾帧写在前面也要落到 images[1] —— 顺序由 role 决定，不是由书写顺序决定。
		body, _ := convert(t, `{
			"model": "m",
			"content": [
				{"type": "image_url", "image_url": {"url": "https://x/last.jpg"}, "role": "last_frame"},
				{"type": "image_url", "image_url": {"url": "https://x/first.jpg"}, "role": "first_frame"}
			]
		}`)
		require.Equal(t, []any{"https://x/first.jpg", "https://x/last.jpg"}, body["images"])
		require.Equal(t, taskTypeFLF2V, metadataOf(t, body)["task_type"])
	})

	t.Run("只给尾帧要独立成 l2va", func(t *testing.T) {
		// 与 i2v 的输入形态完全相同（都是 1 张图），只有语义不同。丢了 task_type
		// 的话下游按张数推断会当成首帧，生成的视频从错误一端开始且全链路无报错。
		body, _ := convert(t, `{
			"model": "m",
			"content": [{"type": "image_url", "image_url": {"url": "https://x/l.jpg"}, "role": "last_frame"}]
		}`)
		require.Equal(t, []any{"https://x/l.jpg"}, body["images"])
		require.Equal(t, taskTypeL2VA, metadataOf(t, body)["task_type"])
	})

	t.Run("多模态参考不进 images", func(t *testing.T) {
		body, _ := convert(t, `{
			"model": "m",
			"content": [
				{"type": "text", "text": "保持主体"},
				{"type": "image_url", "image_url": {"url": "https://x/a.jpg"}, "role": "reference_image"},
				{"type": "video_url", "video_url": {"url": "https://x/ref.mp4"}, "role": "reference_video"},
				{"type": "audio_url", "audio_url": {"url": "https://x/ref.mp3"}, "role": "reference_audio"}
			]
		}`)
		require.NotContains(t, body, "images")
		md := metadataOf(t, body)
		require.Equal(t, taskTypeR2VA, md["task_type"])
		require.Equal(t, []any{"https://x/a.jpg"}, md["src_ref_images"])
		require.Equal(t, []any{"https://x/ref.mp4"}, md["reference_videos"])
		require.Equal(t, []any{"https://x/ref.mp3"}, md["reference_audios"])
	})
}

// reference_videos 这个键名兼任计费判据：relaycommon.VideoHasVideoInput 只认它，
// 含视频输入与不含是两档单价（doubao.GetVideoInputRatio）。换个键名会静默少收。
func TestConvertVideoReferenceKeyIsTheBillingSignal(t *testing.T) {
	body, _ := convert(t, `{
		"model": "doubao-seedance-2-0-260128",
		"content": [
			{"type": "text", "text": "换场景"},
			{"type": "video_url", "video_url": {"url": "https://x/ref.mp4"}, "role": "reference_video"}
		]
	}`)
	require.True(t, relaycommon.VideoHasVideoInput(metadataOf(t, body)),
		"metadata 必须让 VideoHasVideoInput 判定为含视频输入，否则按不含视频的高价档计费")
}

func TestConvertRejectsUnsupportedFeatures(t *testing.T) {
	t.Run("callback_url 显式拒绝而不是静默丢弃", func(t *testing.T) {
		// 静默丢弃的后果是调用方一直等一个永远不会来的推送。
		e := convertErr(t, `{
			"model": "m", "content": [{"type": "text", "text": "x"}],
			"callback_url": "https://caller.example/hook"
		}`)
		require.Equal(t, CodeNotSupported, e.Code)
		require.Contains(t, e.Message, "callback_url")
	})

	t.Run("draft_task", func(t *testing.T) {
		e := convertErr(t, `{
			"model": "m",
			"content": [{"type": "draft_task", "draft_task": {"id": "cgt-draft"}}]
		}`)
		require.Equal(t, CodeNotSupported, e.Code)
	})

	t.Run("asset:// 素材引用", func(t *testing.T) {
		e := convertErr(t, `{
			"model": "m",
			"content": [
				{"type": "text", "text": "x"},
				{"type": "image_url", "image_url": {"url": "asset://abc123"}, "role": "first_frame"}
			]
		}`)
		require.Equal(t, CodeNotSupported, e.Code)
	})
}

func TestConvertValidatesOfficialBounds(t *testing.T) {
	base := `{"model": "m", "content": [{"type": "text", "text": "x"}]`

	cases := []struct {
		name string
		tail string
		want string
	}{
		{"model 必填", `{"content": [{"type": "text", "text": "x"}]}`, "model is required"},
		{"content 必填", `{"model": "m"}`, "content is required"},
		{"ratio 枚举", base + `, "ratio": "5:4"}`, "ratio"},
		{"service_tier 枚举", base + `, "service_tier": "turbo"}`, "service_tier"},
		{"priority 上界", base + `, "priority": 10}`, "priority"},
		{"execution_expires_after 下界", base + `, "execution_expires_after": 60}`, "execution_expires_after"},
		{"seed 上界", base + `, "seed": 4294967296}`, "seed"},
		{"duration 与 frames 互斥", base + `, "duration": 5, "frames": 121}`, "mutually exclusive"},
		{"帧约束与参考互斥", `{"model": "m", "content": [
			{"type": "image_url", "image_url": {"url": "https://x/a.jpg"}, "role": "first_frame"},
			{"type": "image_url", "image_url": {"url": "https://x/b.jpg"}, "role": "reference_image"}
		]}`, "mutually exclusive"},
		{"音频不能单独输入", `{"model": "m", "content": [
			{"type": "text", "text": "x"},
			{"type": "audio_url", "audio_url": {"url": "https://x/a.mp3"}, "role": "reference_audio"}
		]}`, "reference_audio requires"},
		{"两张不带 role 的图要求显式 role", `{"model": "m", "content": [
			{"type": "image_url", "image_url": {"url": "https://x/a.jpg"}},
			{"type": "image_url", "image_url": {"url": "https://x/b.jpg"}}
		]}`, "first_frame"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Contains(t, convertErr(t, tc.tail).Message, tc.want)
		})
	}

	// 边界内的取值必须放行 —— 校验写反了同样是故障，只是方向相反。
	t.Run("边界内放行", func(t *testing.T) {
		body, snap := convert(t, base+`, "priority": 9, "execution_expires_after": 3600, "seed": -1}`)
		md := metadataOf(t, body)
		require.Equal(t, 9, md["priority"])
		require.Equal(t, 3600, md["execution_expires_after"])
		require.Equal(t, -1, md["seed"])
		require.Equal(t, -1, *snap.Seed)
	})
}

// 显式 false 必须原样下发：官方 generate_audio 默认 true，丢掉这个 false 就等于
// 调用方明确要求静音、拿到的却是带声音的片子（还被按含音频的算力计费）。
func TestConvertPreservesExplicitFalse(t *testing.T) {
	body, snap := convert(t, `{
		"model": "m", "content": [{"type": "text", "text": "x"}],
		"generate_audio": false, "watermark": false, "camera_fixed": false, "return_last_frame": false
	}`)
	md := metadataOf(t, body)
	require.Equal(t, false, md["generate_audio"])
	require.Equal(t, false, md["watermark"])
	require.Equal(t, false, md["camera_fixed"])
	require.Equal(t, false, md["return_last_frame"])
	require.NotNil(t, snap.GenerateAudio)
	require.False(t, *snap.GenerateAudio)

	// 没传的字段则一个都不能出现，否则等于替上游编了一份默认值。
	body, snap = convert(t, `{"model": "m", "content": [{"type": "text", "text": "x"}]}`)
	md = metadataOf(t, body)
	for _, key := range []string{"generate_audio", "watermark", "camera_fixed", "return_last_frame",
		"resolution", "ratio", "duration", "seed", "priority", "service_tier"} {
		require.NotContains(t, md, key, "未传的 %s 不该出现在下发的 metadata 里", key)
	}
	require.NotContains(t, body, "duration")
	require.Nil(t, snap.GenerateAudio)
}

// 整值浮点必须收下。
//
// Python 的 json.dumps(5.0) 与 JS 的 JSON.stringify(5.0) 都发出 5.0，而裸 int 解不进去
// —— 而且**整个请求体的 Unmarshal 都会中止**，一个字段的形态毁掉整条请求。本仓原生端点
// 为这件事专门写过 parseDurationSeconds（注释里记着现网事故），新入口不能比它更严。
func TestConvertAcceptsIntegralFloats(t *testing.T) {
	body, snap := convert(t, `{
		"model": "m", "content": [{"type": "text", "text": "x"}],
		"duration": 5.0, "seed": 42.0, "priority": 7.0, "execution_expires_after": 7200.0
	}`)
	md := metadataOf(t, body)
	require.Equal(t, 5, md["duration"])
	require.Equal(t, 42, md["seed"])
	require.Equal(t, 7, md["priority"])
	require.Equal(t, 7200, md["execution_expires_after"])
	// 顶层 duration 是计费维度的唯一来源，也必须是干净的整数。
	require.Equal(t, 5, body["duration"])
	require.Equal(t, 5, snap.Duration)
	require.Equal(t, 42, *snap.Seed)

	t.Run("数字被包成字符串也收", func(t *testing.T) {
		body, _ := convert(t, `{"model":"m","content":[{"type":"text","text":"x"}],"duration":"8"}`)
		require.Equal(t, 8, body["duration"])
	})

	t.Run("非整值显式报错而不是截断", func(t *testing.T) {
		// 截断是把调用方要的值换成另一个，与静默丢弃同类。
		e := convertErr(t, `{"model":"m","content":[{"type":"text","text":"x"}],"duration":5.5}`)
		require.Contains(t, e.Message, "must be an integer")
	})
}

// 合法 JSON 但字段类型不对，不能报「不是合法 JSON」—— 那会把调用方指向一个不存在的
// 语法问题，而真正该看的是哪个字段填错了。
func TestConvertDistinguishesSyntaxErrorFromTypeError(t *testing.T) {
	syntaxErr := convertErr(t, `{"model": "m", `)
	require.Contains(t, syntaxErr.Message, "not valid JSON")

	typeErr := convertErr(t, `{"model": {"nested": true}, "content": [{"type": "text", "text": "x"}]}`)
	require.Contains(t, typeErr.Message, "could not be decoded")
	require.NotContains(t, typeErr.Message, "not valid JSON")
}

// 分辨率归一成小写：计费矩阵的行名就是小写（relaycommon.VideoResolutionTier），
// 回显与计费口径分叉会出现「账单写 720p、响应写 720P」。
func TestConvertNormalizesResolutionCase(t *testing.T) {
	body, snap := convert(t, `{"model": "m", "content": [{"type": "text", "text": "x"}], "resolution": "1080P"}`)
	require.Equal(t, "1080p", metadataOf(t, body)["resolution"])
	require.Equal(t, "1080p", snap.Resolution)
}

// 「文本可选 + 图片」是官方明确支持的组合，不能因为没写提示词就报错。
func TestConvertAllowsImageOnlyRequest(t *testing.T) {
	body, _ := convert(t, `{
		"model": "m",
		"content": [{"type": "image_url", "image_url": {"url": "https://x/a.jpg"}, "role": "first_frame"}]
	}`)
	require.Equal(t, "", body["prompt"])
	require.Equal(t, []any{"https://x/a.jpg"}, body["images"])
}

// Seedance 2.5 的两个枚举参数：合法值归一化后下发并进快照，非法值就地 400——
// 不收或静默丢弃都会让调用方以为参数生效了。
func TestConvertSeedance25EnumParams(t *testing.T) {
	base := `{"model": "m", "content": [{"type": "text", "text": "x"}]`

	body, snap := convert(t, base+`, "omni_reference_task_type": "Reference", "output_format": "MOV"}`)
	md := metadataOf(t, body)
	require.Equal(t, "reference", md["omni_reference_task_type"])
	require.Equal(t, "mov", md["output_format"])
	require.Equal(t, "reference", snap.OmniReferenceTaskType)
	require.Equal(t, "mov", snap.OutputFormat)

	body, _ = convert(t, base+`}`)
	md = metadataOf(t, body)
	require.NotContains(t, md, "omni_reference_task_type", "没传就不下发，让上游用自己的默认值")
	require.NotContains(t, md, "output_format")

	require.Contains(t, convertErr(t, base+`, "omni_reference_task_type": "remix"}`).Message, "omni_reference_task_type")
	require.Contains(t, convertErr(t, base+`, "output_format": "webm"}`).Message, "output_format")
}
