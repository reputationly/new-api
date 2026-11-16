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
