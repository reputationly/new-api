package gpustackplus

import (
	"io"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// 超分（sr）的空提示词兜底。
//
// 线上实测：直连调 relayvsr 只做超分时，`prompt` 必须非空，否则引擎回
// `prompt is required` —— 而我们的 `ValidateBasicTaskRequest` **已经豁免了 sr 的空
// prompt**（它知道超分的输出由源视频决定、文本没有意义）。两边口径不一致的后果是：
// 调用方为了过网关校验，只能自己编一个没意义的 prompt 传进来。
//
// 兜底一个 label 与 svs 同一手法（引擎 input 需非空时由网关补，调用方不必知道）。

// 一段极小的 mp4 头（base64 data-uri）。用 base64 而不是 URL：URL 会过 SSRF 校验
// （测试机上任何域名都解析到保留地址，必被拒），与本次要验的"空 prompt 兜底"无关。
const srTestVideoB64 = "data:video/mp4;base64,AAAAIGZ0eXBpc29tAAACAGlzb21pc28yAABhbW9v"

func buildSRBody(t *testing.T, prompt string) map[string]any {
	t.Helper()
	// 超分要先物化源视频到共享盘，物化根默认是生产的 /nfs-output（只读）。
	setNFSRoot(t, t.TempDir())
	setVideoConfig(t, `{"models":{"relayvsr":{"engine":"","pipeline":true}}}`)
	c := newTestGinContext()
	req := relaycommon.TaskSubmitReq{
		Model:  "relayvsr",
		Prompt: prompt,
		Metadata: map[string]any{
			"task_type": "sr",
			"video":     srTestVideoB64,
			"sr_ratio":  2,
		},
	}
	c.Set("task_request", req)
	info := &relaycommon.RelayInfo{
		UserId:        1,
		TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: "task_sr"},
		ChannelMeta:   &relaycommon.ChannelMeta{UpstreamModelName: "relayvsr"},
	}
	info.OriginModelName = "relayvsr"
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

func TestSRPromptDefaultsWhenEmpty(t *testing.T) {
	for _, prompt := range []string{"", "   "} {
		body := buildSRBody(t, prompt)
		got, _ := body["prompt"].(string)
		if strings.TrimSpace(got) == "" {
			t.Fatalf("prompt 为空时没兜底，引擎会回 `prompt is required`（入参 %q）", prompt)
		}
	}
}

// 调用方自己给了提示词就原样用，不要覆盖。
func TestSRPromptKeepsCallerValue(t *testing.T) {
	body := buildSRBody(t, "把这段画面放大到 2K")
	if got := body["prompt"]; got != "把这段画面放大到 2K" {
		t.Fatalf("prompt = %v，调用方给的值被改写了", got)
	}
}
