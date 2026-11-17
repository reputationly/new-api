package gpustackplus

import (
	"encoding/json"
	"testing"
)

// The payload shape is the vllm-omni usage contract (vendor-compatible), measured
// against MiniMax's own H3-Image API: a 2048x2048 request bills 4096 output image
// tokens and 15 prompt tokens.
func TestEngineUsageToDTO_MapsMeasuredUsage(t *testing.T) {
	raw := json.RawMessage(`{
		"generated_images": 1,
		"total_tokens": 4111,
		"input_tokens": 15,
		"output_tokens": 4096,
		"input_tokens_details": {"text_tokens": 15, "image_tokens": 0},
		"output_tokens_details": {"image_tokens": 4096},
		"usage_source": "engine",
		"usage_version": "h3-image-v1"
	}`)
	usage, ok := engineUsageToDTO(raw)
	if !ok {
		t.Fatal("measured payload should be accepted")
	}
	if usage.PromptTokens != 15 || usage.CompletionTokens != 4096 || usage.TotalTokens != 4111 {
		t.Fatalf("unexpected tokens: %+v", usage)
	}
	if usage.PromptTokensDetails.ImageTokens != 0 || usage.PromptTokensDetails.TextTokens != 15 {
		t.Fatalf("input details lost: %+v", usage.PromptTokensDetails)
	}
	if usage.CompletionTokenDetails.ImageTokens != 4096 {
		t.Fatalf("output image tokens lost: %+v", usage.CompletionTokenDetails)
	}
	if usage.UsageSource != "engine" || usage.UsageSemantic != "openai" {
		t.Fatalf("source/semantic not marked: %q %q", usage.UsageSource, usage.UsageSemantic)
	}
}

func TestEngineUsageToDTO_ReferenceImagesAreInputTokens(t *testing.T) {
	raw := json.RawMessage(`{
		"total_tokens": 9238,
		"input_tokens": 5142,
		"output_tokens": 4096,
		"input_tokens_details": {"text_tokens": 30, "image_tokens": 5112},
		"output_tokens_details": {"image_tokens": 4096}
	}`)
	usage, ok := engineUsageToDTO(raw)
	if !ok {
		t.Fatal("payload with reference images should be accepted")
	}
	if usage.PromptTokensDetails.ImageTokens != 5112 {
		t.Fatalf("vision tokens: %d", usage.PromptTokensDetails.ImageTokens)
	}
	if usage.UsageSource != "engine" {
		t.Fatalf("missing usage_source must default to engine, got %q", usage.UsageSource)
	}
}

func TestEngineUsageToDTO_RejectsPlaceholderAndGarbage(t *testing.T) {
	for name, raw := range map[string]json.RawMessage{
		"absent":    nil,
		"zeroed":    json.RawMessage(`{"total_tokens":0,"input_tokens":0,"output_tokens":0}`),
		"malformed": json.RawMessage(`{"total_tokens":`),
		"not-json":  json.RawMessage(`"nope"`),
	} {
		if _, ok := engineUsageToDTO(raw); ok {
			t.Fatalf("%s: must not be treated as measured usage", name)
		}
	}
}

// 客户端响应必须是官方 image 接口的形状：字段名、嵌套、generated_images 都要对得上，
// 且不能混入计费侧 dto.Usage 的噪声字段（usage_semantic / claude_* / cache_*）。
func TestEngineImageUsage_MatchesVendorShape(t *testing.T) {
	raw := json.RawMessage(`{
		"generated_images": 1,
		"total_tokens": 16399,
		"input_tokens": 15,
		"output_tokens": 16384,
		"input_tokens_details": {"text_tokens": 15, "image_tokens": 0},
		"output_tokens_details": {"image_tokens": 16384},
		"usage_source": "engine",
		"usage_version": "h3-image-v1",
		"canvas_tokens": 16384,
		"reference_latent_tokens": 0,
		"text_token_factor": 1
	}`)
	usage, ok := engineImageUsage(raw)
	if !ok {
		t.Fatal("measured payload should decode")
	}
	if usage.GeneratedImages != 1 || usage.InputTokens != 15 || usage.OutputTokens != 16384 || usage.TotalTokens != 16399 {
		t.Fatalf("unexpected usage: %+v", usage)
	}
	if usage.InputTokensDetails.TextTokens != 15 || usage.InputTokensDetails.ImageTokens != 0 {
		t.Fatalf("input details: %+v", usage.InputTokensDetails)
	}
	if usage.OutputTokensDetails.ImageTokens != 16384 {
		t.Fatalf("output details: %+v", usage.OutputTokensDetails)
	}

	encoded, err := json.Marshal(usage)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, want := range []string{"generated_images", "total_tokens", "input_tokens", "output_tokens",
		"input_tokens_details", "output_tokens_details"} {
		if _, ok := fields[want]; !ok {
			t.Fatalf("response usage missing %q: %s", want, encoded)
		}
	}
	for _, unwanted := range []string{"usage_semantic", "usage_source", "canvas_tokens",
		"reference_latent_tokens", "text_token_factor", "claude_cache_creation_5_m_tokens"} {
		if _, ok := fields[unwanted]; ok {
			t.Fatalf("response usage leaked %q: %s", unwanted, encoded)
		}
	}
}

func TestEngineImageUsage_RejectsEmpty(t *testing.T) {
	for _, raw := range []json.RawMessage{nil, json.RawMessage(`{"total_tokens":0}`), json.RawMessage(`{`)} {
		if _, ok := engineImageUsage(raw); ok {
			t.Fatalf("must not decode: %s", raw)
		}
	}
}
