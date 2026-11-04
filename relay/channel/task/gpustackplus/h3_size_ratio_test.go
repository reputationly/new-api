package gpustackplus

import (
	"io"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// 线上实测的真 bug：H3 的 21:9 / 9:21 两档，画布算得出、请求发不出。
//
// 官方 768P 尺寸表按 32 对齐取整：21:9 的真值几何是 1536×658，取整成 1536×672 之后
// 精确约分是 **16:7** —— 既不在 H3 的 11 个具名比例里，偏离 21/9 又有 2.04%，超出引擎
// 自己的吸附容差，于是引擎 400：`aspect_ratio must be one of … got '16:7'`。
// 9:16 那档（768×1344 → 4:7，偏 1.59%）恰好落在容差内，所以只有这两档炸。
//
// 修法是在 size 这一处（不是 h3NormalizeAspectRatio —— 它对已存在的 aspect_ratio
// 直接早退）把 H3 的比例**吸附到具名值**；画布不受影响，仍由 size 决定。

func buildH3VideoBody(t *testing.T, size string) map[string]any {
	t.Helper()
	setVideoConfig(t, h3Config)
	c := newTestGinContext()
	req := relaycommon.TaskSubmitReq{
		Model:    "video-h3",
		Prompt:   "a cat",
		Size:     size,
		Metadata: map[string]any{"task_type": "t2v"},
	}
	c.Set("task_request", req)
	info := &relaycommon.RelayInfo{
		UserId:        1,
		TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: "task_ratio"},
		ChannelMeta:   &relaycommon.ChannelMeta{UpstreamModelName: "video-h3"},
	}
	info.OriginModelName = "video-h3"
	reader, err := (&TaskAdaptor{}).BuildRequestBody(c, info)
	if err != nil {
		t.Fatalf("BuildRequestBody: %s", err)
	}
	raw, _ := io.ReadAll(reader)
	var body map[string]any
	if err := common.Unmarshal(raw, &body); err != nil {
		t.Fatalf("body is not valid json: %s", err)
	}
	return body
}

// 11 个官方 768P 画布逐个过：aspect_ratio 必须是自己那个具名值，size 一个字都不许改。
func TestH3SizeSnapsToNamedAspectRatio(t *testing.T) {
	cases := map[string]string{
		"1536x672": "21:9",
		"1344x768": "16:9",
		"1152x768": "3:2",
		"1024x768": "4:3",
		"960x768":  "5:4",
		"768x768":  "1:1",
		"768x960":  "4:5",
		"768x1024": "3:4",
		"768x1152": "2:3",
		"768x1344": "9:16",
		"672x1536": "9:21",
	}
	for size, wantRatio := range cases {
		t.Run(size, func(t *testing.T) {
			body := buildH3VideoBody(t, size)
			if got := body["aspect_ratio"]; got != wantRatio {
				t.Fatalf("aspect_ratio = %v, want %s（got 16:7 那类精确约分会被引擎白名单拒）", got, wantRatio)
			}
			// 画布必须是客户选的尺寸本身：aspect_ratio 只参与校验/吸附。
			if got := body["size"]; got != size {
				t.Fatalf("size = %v, want %s（不得被比例吸附改写）", got, size)
			}
		})
	}
}

// 非 H3 引擎保持精确约分：它们可能真的按比例算画布，吸附会改变出片尺寸。
func TestNonH3SizeKeepsExactRatio(t *testing.T) {
	setVideoConfig(t, `{"models":{"wan-video":{"engine":"wan","tabs":{"text2video":{}}}}}`)
	c := newTestGinContext()
	req := relaycommon.TaskSubmitReq{
		Model:    "wan-video",
		Prompt:   "a cat",
		Size:     "1536x672",
		Metadata: map[string]any{"task_type": "t2v"},
	}
	c.Set("task_request", req)
	info := &relaycommon.RelayInfo{
		UserId:        1,
		TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: "task_ratio"},
		ChannelMeta:   &relaycommon.ChannelMeta{UpstreamModelName: "wan-video"},
	}
	info.OriginModelName = "wan-video"
	reader, err := (&TaskAdaptor{}).BuildRequestBody(c, info)
	if err != nil {
		t.Fatalf("BuildRequestBody: %s", err)
	}
	raw, _ := io.ReadAll(reader)
	var body map[string]any
	if err := common.Unmarshal(raw, &body); err != nil {
		t.Fatalf("body is not valid json: %s", err)
	}
	if got := body["aspect_ratio"]; got != "16:7" {
		t.Fatalf("aspect_ratio = %v, want 16:7（非 H3 不该被吸附成 21:9）", got)
	}
}
