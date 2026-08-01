package gpustackplus

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/gin-gonic/gin"
)

func TestApplyErnieImageTurboDefaultsPromptEnhancer(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		applyPE bool
	}{
		{name: "omitted defaults off", payload: `{}`, applyPE: false},
		{name: "explicit false survives", payload: `{"use_prompt_enhancer":false}`, applyPE: false},
		{name: "explicit true", payload: `{"use_prompt_enhancer":true}`, applyPE: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := gin.CreateTestContextOnly(httptest.NewRecorder(), gin.New())
			c.Request = httptest.NewRequest("POST", "/v1/images/generations", nil)
			var request dto.ImageRequest
			if err := json.Unmarshal([]byte(tt.payload), &request); err != nil {
				t.Fatalf("unmarshal image request: %v", err)
			}
			body := map[string]any{}
			applyErnieImageTurboDefaults(
				c,
				request,
				"ernie-image-turbo",
				body,
			)

			if got := body["num_inference_steps"]; got != 8 {
				t.Fatalf("num_inference_steps = %v, want 8", got)
			}
			if got := body["guidance_scale"]; got != 1.0 {
				t.Fatalf("guidance_scale = %v, want 1.0", got)
			}
			extraArgs, ok := body["extra_args"].(map[string]any)
			if !ok {
				t.Fatalf("extra_args = %#v, want map", body["extra_args"])
			}
			if got := extraArgs["apply_pe"]; got != tt.applyPE {
				t.Fatalf("apply_pe = %v, want %v", got, tt.applyPE)
			}
		})
	}
}

func TestApplyErnieImageTurboDefaultsIgnoresOtherModels(t *testing.T) {
	c := gin.CreateTestContextOnly(httptest.NewRecorder(), gin.New())
	c.Request = httptest.NewRequest("POST", "/v1/images/generations", nil)
	body := map[string]any{}
	applyErnieImageTurboDefaults(
		c,
		dto.ImageRequest{Extra: map[string]json.RawMessage{"use_prompt_enhancer": json.RawMessage("true")}},
		"z-image",
		body,
	)
	if len(body) != 0 {
		t.Fatalf("body = %#v, want no ERNIE defaults", body)
	}
}

func TestMappedImageModelKeepsPromptEnhancer(t *testing.T) {
	c := gin.CreateTestContextOnly(httptest.NewRecorder(), gin.New())
	c.Request = httptest.NewRequest("POST", "/v1/images/generations", nil)
	c.Set("model_mapping", `{"poster-model":"ernie-image-turbo"}`)

	var request dto.ImageRequest
	if err := json.Unmarshal(
		[]byte(`{"model":"poster-model","prompt":"test","use_prompt_enhancer":true}`),
		&request,
	); err != nil {
		t.Fatalf("unmarshal image request: %v", err)
	}
	info := &relaycommon.RelayInfo{
		OriginModelName: "poster-model",
		ChannelMeta:     &relaycommon.ChannelMeta{UpstreamModelName: "poster-model"},
	}
	if err := helper.ModelMappedHelper(c, info, &request); err != nil {
		t.Fatalf("map model: %v", err)
	}
	if info.UpstreamModelName != "ernie-image-turbo" {
		t.Fatalf("upstream model = %q, want ernie-image-turbo", info.UpstreamModelName)
	}

	body := map[string]any{}
	modelName := firstNonEmpty(info.UpstreamModelName, request.Model, info.OriginModelName)
	applyErnieImageTurboDefaults(c, request, modelName, body)
	extraArgs, ok := body["extra_args"].(map[string]any)
	if !ok || extraArgs["apply_pe"] != true {
		t.Fatalf("extra_args = %#v, want apply_pe=true", body["extra_args"])
	}
}
