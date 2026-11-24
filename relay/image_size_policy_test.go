package relay

import (
	"strings"
	"testing"
)

// The bands mirror the engine contract (vllm-omni usage spec h3-image-v1): only the
// 2K and 16MP tiers are sold, and both sides must be 32-aligned so the generated
// canvas equals the delivered image.
func TestNormalizeBilledImageSize(t *testing.T) {
	accept := []string{
		"2048x2048", "2368x1760", "1760x2368", "2720x1536", "1536x2720", "3136x1344", "1344x3136", "3552x1184", "1184x3552",
		"4096x4096", "4736x3552", "3552x4736", "5472x3072", "3072x5472", "7104x2368", "2368x7104",
		"", // empty = engine decides (aspect_ratio / resolution path)
	}
	for _, size := range accept {
		got, err := normalizeBilledImageSize(size)
		if err != nil {
			t.Errorf("%q should be accepted, got %v", size, err)
			continue
		}
		if want := strings.ToLower(size); got != want {
			t.Errorf("%q should be returned unchanged, got %q", size, got)
		}
	}

	// Sizes from the Ark/Seedream 5.0 Pro tier table: 16 px grid, areas straddling our
	// bands. They must map onto the band size with the closest aspect ratio instead of
	// being rejected, otherwise an Ark SDK cannot use this endpoint at all.
	mapped := map[string]string{
		"1776x2368": "1760x2368", // 3:4
		"2368x1776": "2368x1760", // 4:3
		"2816x1584": "2720x1536", // 16:9
		"1584x2816": "1536x2720", // 9:16
		"2000x2000": "2048x2048", // 4.0 MP: on the band floor, 16 px grid

		"4096x1024": "3552x1184", // 4:1 exceeds the engine's 3:1 output bound
	}
	for size, want := range mapped {
		got, err := normalizeBilledImageSize(size)
		if err != nil {
			t.Errorf("%q should be mapped into a band, got error %v", size, err)
			continue
		}
		if got != want {
			t.Errorf("%q should map to %q, got %q", size, want, got)
		}
		if w, h, ok := parseWidthHeight(got); !ok || w%h3ImagePixelGrid != 0 || h%h3ImagePixelGrid != 0 {
			t.Errorf("%q mapped to %q which is not %d-aligned", size, got, h3ImagePixelGrid)
		}
	}

	// 32-aligned and inside the 2K band: the engine accepts it, so it must not be
	// rewritten just because it is not one of the listed ratio-family sizes.
	if got, err := normalizeBilledImageSize("2400x1728"); err != nil || got != "2400x1728" {
		t.Errorf("engine-legal 2400x1728 must pass through, got %q err=%v", got, err)
	}

	reject := map[string]string{
		"1024x1024": "below the 2K tier: refused fast, never upgraded (delivery must match the request)",
		"1248x832":  "below the 2K tier",
		"auto":      "auto is not an explicit size",
		"3000x3000": "between the bands, no tier to map into",
		"8000x8000": "above the 16MP band",
	}
	for size, why := range reject {
		_, err := normalizeBilledImageSize(size)
		if err == nil {
			t.Errorf("%q must be rejected (%s)", size, why)
			continue
		}
		if !strings.Contains(err.Error(), "allowed sizes") {
			t.Errorf("%q error should list allowed sizes, got %v", size, err)
		}
	}

	if _, err := normalizeBilledImageSize("2048X2048"); err != nil {
		t.Errorf("uppercase X must be normalised, got %v", err)
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
