package relay

import (
	"fmt"
	"math"
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
// client mistake into a 500 the customer cannot act on.
//
// Clients speaking the Ark image protocol send the sizes their own tier table
// documents (Seedream 5.0 Pro: 1776x2368, 2816x1584, 1584x2816, …). Those sit on a
// 16 px grid and their areas straddle our billed bands, so rejecting them outright
// would make this endpoint unusable from an Ark SDK. Instead a size whose area lands
// around a billed band is mapped to the band's size with the closest aspect ratio —
// the delivered canvas keeps the requested framing, the billed tier stays exact.
//
// Keep the table in sync with the engine contract:
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
		sizes:    []string{"2048x2048", "2368x1760", "1760x2368", "2720x1536", "1536x2720", "3136x1344", "1344x3136", "3552x1184", "1184x3552"},
	},
	{
		name:     "16mp",
		lowArea:  16_500_000,
		highArea: 16_900_000,
		sizes:    []string{"4096x4096", "4736x3552", "3552x4736", "5472x3072", "3072x5472", "7104x2368", "2368x7104"},
	},
}

const h3ImagePixelGrid = 32

// imageBandAreaTolerance is how far outside a band's area a requested size may sit
// and still be mapped into it. 15% covers the neighbouring tier tables (a 2k 9:16
// from Seedream is 4.46 MP against our 4.0–4.3 MP band) without letting a request
// for a genuinely different resolution silently become a 4 MP image.
const imageBandAreaTolerance = 0.15

// h3ImageMaxOutputAspect mirrors the engine's 3:1 output bound.
const h3ImageMaxOutputAspect = 3

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

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

// allowedSizeHint 是错误文案里的"可用尺寸"提示，统一一种写法。
func allowedSizeHint() string {
	return " allowed sizes -> " + allowedSizeList()
}

func allowedSizeList() string {
	var out []string
	for _, band := range h3ImageAreaBands {
		out = append(out, fmt.Sprintf("%s: %s", band.name, strings.Join(band.sizes, ", ")))
	}
	return strings.Join(out, " | ")
}

// bandOfArea returns the billed band a requested area belongs to, allowing the
// tolerance above. nil means the area is a different resolution than we sell.
func bandOfArea(area int) *imageAreaBand {
	for i := range h3ImageAreaBands {
		band := &h3ImageAreaBands[i]
		low := int(float64(band.lowArea) * (1 - imageBandAreaTolerance))
		high := int(float64(band.highArea) * (1 + imageBandAreaTolerance))
		if area >= low && area <= high {
			return band
		}
	}
	return nil
}

// closestSizeInBand picks the band size whose aspect ratio is closest to the
// requested one (log distance, so 1:2 and 2:1 are equally far from 1:1); an exact
// match wins, then the nearest area.
func closestSizeInBand(band *imageAreaBand, width, height int) string {
	requestedAspect := math.Log(float64(width) / float64(height))
	requestedArea := width * height
	best := ""
	bestAspect := math.Inf(1)
	bestArea := math.Inf(1)
	for _, candidate := range band.sizes {
		cw, ch, ok := parseWidthHeight(candidate)
		if !ok {
			continue
		}
		aspect := math.Abs(math.Log(float64(cw)/float64(ch)) - requestedAspect)
		area := math.Abs(float64(cw*ch - requestedArea))
		if aspect < bestAspect-1e-9 || (math.Abs(aspect-bestAspect) <= 1e-9 && area < bestArea) {
			best, bestAspect, bestArea = candidate, aspect, area
		}
	}
	return best
}

// normalizeBilledImageSize validates a requested size and, when it is not one of the
// billed sizes, maps it to the band size with the closest aspect ratio.
//
// An empty size is passed through: it means "the engine decides" (aspect_ratio /
// resolution path) and the engine still enforces its own contract.
//
// A size below the smallest billed tier is upgraded to the 2K tier (nearest aspect
// ratio), and "auto" becomes 2K 1:1. Vendor SDKs send their own tier tables — a 1K
// request is the common case — and the vendor itself floors the generated canvas at
// 2048x2048, i.e. bills such a request as 2K. Upgrading keeps that same bargain here
// and keeps the billed canvas equal to the delivered image. Sizes above the largest
// tier are still rejected: delivering 16 MP for a request the client priced higher is
// a different (and costlier) decision, not a rounding one.
func normalizeBilledImageSize(size string) (string, error) {
	trimmed := strings.TrimSpace(strings.ToLower(size))
	if trimmed == "" {
		return "", nil
	}
	if trimmed == "auto" {
		return "", fmt.Errorf("size must be an explicit WIDTHxHEIGHT (auto is not supported);%s", allowedSizeHint())
	}
	width, height, ok := parseWidthHeight(trimmed)
	if !ok {
		return "", fmt.Errorf("invalid size %q, expected WIDTHxHEIGHT;%s", size, allowedSizeHint())
	}
	exact := fmt.Sprintf("%dx%d", width, height)
	for _, band := range h3ImageAreaBands {
		for _, candidate := range band.sizes {
			if candidate == exact {
				return candidate, nil
			}
		}
	}
	// A size the engine already accepts travels untouched: the engine's own gate is
	// "32-aligned sides, area inside a billed band, aspect at most 3:1", not membership
	// of the table above (the table is only the ratio family we advertise).
	if width%h3ImagePixelGrid == 0 && height%h3ImagePixelGrid == 0 &&
		bandOfArea(width*height) != nil && maxInt(width, height) <= h3ImageMaxOutputAspect*minInt(width, height) {
		return exact, nil
	}
	band := bandOfArea(width * height)
	if band == nil {
		// Below the smallest tier is refused, never upgraded: delivering 2K for a 1K
		// request would silently change what the client asked for, and letting it travel
		// costs a GPU slot (measured: ~2 minutes) before the engine answers the same 400.
		if smallest := &h3ImageAreaBands[0]; width*height < smallest.lowArea {
			return "", fmt.Errorf("size %dx%d (%d px) is below the smallest billed tier (2K, %d..%d px); %s",
				width, height, width*height, smallest.lowArea, smallest.highArea, allowedSizeHint())
		}
		return "", fmt.Errorf("unsupported_size: %dx%d (%d px) is outside the billed tiers;%s",
			width, height, width*height, allowedSizeHint())
	}
	mapped := closestSizeInBand(band, width, height)
	if mapped == "" {
		return "", fmt.Errorf("unsupported_size: %dx%d (%d px) is outside the billed tiers; allowed sizes -> %s",
			width, height, width*height, allowedSizeList())
	}
	return mapped, nil
}
