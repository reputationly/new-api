package relay

import (
	"strings"
	"testing"
)

// The bands mirror the engine contract (vllm-omni usage spec h3-image-v1): only the
// 2K and 16MP tiers are sold, and both sides must be 32-aligned so the generated
// canvas equals the delivered image.
func TestValidateBilledImageSize(t *testing.T) {
	accept := []string{
		"2048x2048", "2368x1760", "1760x2368", "2720x1536", "1536x2720", "3552x1184", "1184x3552",
		"4096x4096", "4736x3552", "3552x4736", "5472x3072", "3072x5472", "7104x2368", "2368x7104",
		"", // empty = engine decides (aspect_ratio / resolution path)
	}
	for _, size := range accept {
		if err := validateBilledImageSize(size); err != nil {
			t.Errorf("%q should be accepted, got %v", size, err)
		}
	}

	reject := map[string]string{
		"1024x1024": "below the 2K tier",
		"auto":      "auto is not an explicit size",
		"2048X2048": "", // uppercase X is normalised, must pass
		"2000x2000": "not 32-aligned",
		"3000x3000": "outside both bands",
		"8000x8000": "above the 16MP band",
	}
	for size, why := range reject {
		err := validateBilledImageSize(size)
		if size == "2048X2048" {
			if err != nil {
				t.Errorf("uppercase X must be normalised, got %v", err)
			}
			continue
		}
		if err == nil {
			t.Errorf("%q must be rejected (%s)", size, why)
			continue
		}
		if !strings.Contains(err.Error(), "allowed sizes") {
			t.Errorf("%q error should list allowed sizes, got %v", size, err)
		}
	}
}

func TestIsSizePolicyModel(t *testing.T) {
	for _, model := range []string{"minimax-h3-image", "MiniMax-H3-Image", "minimax-h3-image-pro"} {
		if !isSizePolicyModel(model) {
			t.Errorf("%q should be size-policed", model)
		}
	}
	for _, model := range []string{"qwen-image-pro", "minimax-h3-fl2va", "minimax-h3-ref2va", ""} {
		if isSizePolicyModel(model) {
			t.Errorf("%q must not be size-policed", model)
		}
	}
}
