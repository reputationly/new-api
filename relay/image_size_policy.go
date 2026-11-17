package relay

import (
	"fmt"
	"strconv"
	"strings"
)

// Image size admission for self-hosted image models whose engine bills by tokens.
//
// The engine (vllm-omni H3-Image) only accepts sizes whose area falls in a billed
// tier and whose sides are multiples of 32 — that is what keeps the generated
// canvas identical to the delivered image, which is what makes token accounting
// unambiguous. It enforces this itself (400 unsupported_size), but letting the
// request travel to a GPU replica first wastes queue time and, worse, turns a
// client mistake into a 500 the customer cannot act on. Rejecting here gives a
// clean 400 plus the list of allowed sizes.
//
// Keep this table in sync with the engine contract:
// vllm-omni `MINIMAX_H3_IMAGE_BILLABLE_AREA_BANDS` (usage spec h3-image-v1).
type imageAreaBand struct {
	name     string
	lowArea  int
	highArea int
	sizes    []string
}

var h3ImageAreaBands = []imageAreaBand{
	{
		name:     "2k",
		lowArea:  4_000_000,
		highArea: 4_300_000,
		sizes:    []string{"2048x2048", "2368x1760", "1760x2368", "2720x1536", "1536x2720", "3552x1184", "1184x3552"},
	},
	{
		name:     "16mp",
		lowArea:  16_500_000,
		highArea: 16_900_000,
		sizes:    []string{"4096x4096", "4736x3552", "3552x4736", "5472x3072", "3072x5472", "7104x2368", "2368x7104"},
	},
}

const h3ImagePixelGrid = 32

// imageSizePolicyModels lists models that only sell the billed tiers. Prefix match
// so a channel can name the model with or without a vendor prefix.
var imageSizePolicyModels = []string{"minimax-h3-image"}

func isSizePolicyModel(model string) bool {
	lower := strings.ToLower(strings.TrimSpace(model))
	for _, want := range imageSizePolicyModels {
		if lower == want || strings.HasPrefix(lower, want+"-") {
			return true
		}
	}
	return false
}

// parseWidthHeight parses "WxH" (case-insensitive x). Returns ok=false when the
// value is empty or not of that shape — callers decide whether that is an error.
func parseWidthHeight(size string) (int, int, bool) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(size)), "x")
	if len(parts) != 2 {
		return 0, 0, false
	}
	w, errW := strconv.Atoi(strings.TrimSpace(parts[0]))
	h, errH := strconv.Atoi(strings.TrimSpace(parts[1]))
	if errW != nil || errH != nil || w <= 0 || h <= 0 {
		return 0, 0, false
	}
	return w, h, true
}

func allowedSizeList() string {
	var out []string
	for _, band := range h3ImageAreaBands {
		out = append(out, fmt.Sprintf("%s: %s", band.name, strings.Join(band.sizes, ", ")))
	}
	return strings.Join(out, " | ")
}

// validateBilledImageSize rejects sizes the engine cannot bill unambiguously.
//
// An empty size is accepted: it means "the engine decides" (aspect_ratio/resolution
// path) and the engine still enforces its own contract. "auto" is rejected because
// this deployment requires an explicit size, so passing it on would only produce a
// confusing downstream 400.
func validateBilledImageSize(size string) error {
	trimmed := strings.TrimSpace(strings.ToLower(size))
	if trimmed == "" {
		return nil
	}
	if trimmed == "auto" {
		return fmt.Errorf("size must be an explicit WIDTHxHEIGHT; allowed sizes -> %s", allowedSizeList())
	}
	width, height, ok := parseWidthHeight(trimmed)
	if !ok {
		return fmt.Errorf("invalid size %q, expected WIDTHxHEIGHT; allowed sizes -> %s", size, allowedSizeList())
	}
	if width%h3ImagePixelGrid != 0 || height%h3ImagePixelGrid != 0 {
		return fmt.Errorf("size %dx%d must have both sides as multiples of %d; allowed sizes -> %s",
			width, height, h3ImagePixelGrid, allowedSizeList())
	}
	area := width * height
	for _, band := range h3ImageAreaBands {
		if area >= band.lowArea && area <= band.highArea {
			return nil
		}
	}
	return fmt.Errorf("unsupported_size: %dx%d (%d px) is outside the billed tiers; allowed sizes -> %s",
		width, height, area, allowedSizeList())
}
