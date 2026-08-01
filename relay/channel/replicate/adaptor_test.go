package replicate

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
)

// convertImageInput 跑一次 ConvertImageRequest 并取出上游的 input 载荷。
func convertImageInput(t *testing.T, payload string) map[string]any {
	t.Helper()
	c := gin.CreateTestContextOnly(httptest.NewRecorder(), gin.New())
	c.Request = httptest.NewRequest("POST", "/v1/images/generations", nil)

	var request dto.ImageRequest
	if err := json.Unmarshal([]byte(payload), &request); err != nil {
		t.Fatalf("unmarshal image request: %v", err)
	}

	// UpstreamModelName 挂在内嵌的 *ChannelMeta 上,不初始化会空指针。
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	body, err := (&Adaptor{}).ConvertImageRequest(c, info, request)
	if err != nil {
		t.Fatalf("convert image request: %v", err)
	}
	wrapper, ok := body.(map[string]any)
	if !ok {
		t.Fatalf("body = %#v, want map", body)
	}
	input, ok := wrapper["input"].(map[string]any)
	if !ok {
		t.Fatalf("input = %#v, want map", wrapper["input"])
	}
	return input
}

// 体验区的渠道控制字段不能当成 Replicate 的模型 input 转发上去,否则上游报未知输入。
func TestConvertImageRequestDropsNewAPIControlExtras(t *testing.T) {
	input := convertImageInput(t, `{
		"model": "black-forest-labs/flux-1.1-pro",
		"prompt": "a poster",
		"use_prompt_enhancer": true,
		"bot_task": "think_recaption"
	}`)

	for _, key := range []string{"use_prompt_enhancer", "bot_task"} {
		if _, leaked := input[key]; leaked {
			t.Fatalf("input[%q] = %#v, want dropped", key, input[key])
		}
	}
	if input["prompt"] != "a poster" {
		t.Fatalf("prompt = %#v, want passthrough", input["prompt"])
	}
}

// 其余未知字段仍要全量透传——Replicate 的模型 input 是任意的,这是本适配器有意的能力。
func TestConvertImageRequestKeepsOtherExtras(t *testing.T) {
	input := convertImageInput(t, `{
		"model": "black-forest-labs/flux-1.1-pro",
		"prompt": "a poster",
		"system_prompt": "be terse",
		"guidance": 3.5
	}`)

	if input["system_prompt"] != "be terse" {
		t.Fatalf("system_prompt = %#v, want passthrough", input["system_prompt"])
	}
	if input["guidance"] != 3.5 {
		t.Fatalf("guidance = %#v, want passthrough", input["guidance"])
	}
}
