package operation_setting

import (
	"fmt"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
)

// ---------------------------------------------------------------------------
// Tool call prices (¥/1K calls, admin-configurable)
// DB key: tool_price_setting.prices
//
// Key format:
//   - "tool_name"              → default price for all models
//   - "tool_name:model_prefix*" → override for models matching the prefix
//
// Lookup order: longest prefix match → default → hardcoded fallback → 0
// ---------------------------------------------------------------------------

// 值为人民币（¥/千次）。× usdToCNY 前面的是上游的美元原价。
var defaultToolPrices = map[string]float64{
	"web_search":         10.0 * usdToCNY, // $10    OpenAI web search (all models) / Claude web search
	"web_search_preview": 10.0 * usdToCNY, // $10    OpenAI web search preview (default: reasoning models)
	"file_search":        2.5 * usdToCNY,  // $2.5   OpenAI file search (Responses API)
	"google_search":      14.0 * usdToCNY, // $14    Gemini Grounding with Google Search
}

var defaultToolPriceOverrides = map[string]float64{
	"web_search_preview:gpt-4o*":       25.0 * usdToCNY, // 原 $25，non-reasoning models
	"web_search_preview:gpt-4.1*":      25.0 * usdToCNY,
	"web_search_preview:gpt-4o-mini*":  25.0 * usdToCNY,
	"web_search_preview:gpt-4.1-mini*": 25.0 * usdToCNY,
}

// ToolPriceSetting is managed by config.GlobalConfig.Register.
type ToolPriceSetting struct {
	Prices     map[string]float64 `json:"prices"`
	Currencies map[string]string  `json:"currencies"`
}

var toolPriceSetting = ToolPriceSetting{
	Currencies: map[string]string{},
	Prices: func() map[string]float64 {
		m := make(map[string]float64, len(defaultToolPrices)+len(defaultToolPriceOverrides))
		for k, v := range defaultToolPrices {
			m[k] = v
		}
		for k, v := range defaultToolPriceOverrides {
			m[k] = v
		}
		return m
	}(),
}

func init() {
	config.GlobalConfig.Register("tool_price_setting", &toolPriceSetting)
	RebuildToolPriceIndex()
}

// ---------------------------------------------------------------------------
// Precomputed price index (atomic, lock-free on read path)
// ---------------------------------------------------------------------------

type prefixEntry struct {
	prefix string
	price  toolPrice
}

type toolPrice struct {
	amount float64
	usd    bool
}

type toolPriceIndex struct {
	defaults map[string]toolPrice
	prefixes map[string][]prefixEntry
}

var currentIndex atomic.Pointer[toolPriceIndex]

// RebuildToolPriceIndex rebuilds the lookup index from the current config.
// Called on init and after config updates. Not on the billing hot path.
func RebuildToolPriceIndex() {
	merged := make(map[string]float64, len(defaultToolPrices)+len(defaultToolPriceOverrides)+len(toolPriceSetting.Prices))
	for k, v := range defaultToolPrices {
		merged[k] = v
	}
	for k, v := range defaultToolPriceOverrides {
		merged[k] = v
	}
	for k, v := range toolPriceSetting.Prices {
		merged[k] = v
	}

	idx := &toolPriceIndex{
		defaults: make(map[string]toolPrice),
		prefixes: make(map[string][]prefixEntry),
	}

	for key, amount := range merged {
		price := toolPrice{amount: amount, usd: toolPriceSetting.Currencies[key] == "USD"}
		colonIdx := strings.IndexByte(key, ':')
		if colonIdx < 0 {
			idx.defaults[key] = price
			continue
		}
		toolName := key[:colonIdx]
		modelPart := key[colonIdx+1:]
		prefix := strings.TrimSuffix(modelPart, "*")
		idx.prefixes[toolName] = append(idx.prefixes[toolName], prefixEntry{prefix: prefix, price: price})
	}

	for tool := range idx.prefixes {
		entries := idx.prefixes[tool]
		sort.Slice(entries, func(i, j int) bool {
			return len(entries[i].prefix) > len(entries[j].prefix)
		})
		idx.prefixes[tool] = entries
	}

	currentIndex.Store(idx)
}

// GetToolPriceForModel returns the configured amount in its source currency.
// Lookup: longest prefix match → tool default → 0.
func GetToolPriceForModel(toolName, modelName string) float64 {
	price := resolveToolPrice(toolName, modelName)
	return price.amount
}

func GetToolPriceWithCurrency(toolName, modelName string) (float64, bool) {
	price := resolveToolPrice(toolName, modelName)
	return price.amount, price.usd
}

func ResolveToolPriceCNY(toolName, modelName string, rate float64, hasRate bool) (float64, bool, error) {
	amount, usd := GetToolPriceWithCurrency(toolName, modelName)
	if !usd {
		return amount, false, nil
	}
	if !hasRate {
		return 0, true, fmt.Errorf("工具 %s 使用美元定价: %w", toolName, billing_setting.ErrModelSourceRateUnavailable)
	}
	return amount * rate, true, nil
}

func ParseToolPriceCurrencies(raw string) (map[string]string, error) {
	var currencies map[string]string
	if strings.TrimSpace(raw) != "" {
		if err := common.UnmarshalJsonStr(raw, &currencies); err != nil {
			return nil, fmt.Errorf("工具定价币种不是合法的 JSON: %w", err)
		}
	}
	for tool, currency := range currencies {
		if strings.TrimSpace(tool) == "" || (currency != "CNY" && currency != "USD") {
			return nil, fmt.Errorf("工具 %q 的定价币种无效: %q", tool, currency)
		}
	}
	return currencies, nil
}

func resolveToolPrice(toolName, modelName string) toolPrice {
	idx := currentIndex.Load()
	if idx == nil {
		if v, ok := defaultToolPrices[toolName]; ok {
			return toolPrice{amount: v}
		}
		return fallbackToolPrice(toolName, modelName)
	}

	if entries, ok := idx.prefixes[toolName]; ok && modelName != "" {
		for _, e := range entries {
			if strings.HasPrefix(modelName, e.prefix) {
				return e.price
			}
		}
	}

	if p, ok := idx.defaults[toolName]; ok {
		return p
	}
	return fallbackToolPrice(toolName, modelName)
}

func fallbackToolPrice(toolName, modelName string) toolPrice {
	if toolName == "gemini_audio_input" {
		return toolPrice{amount: GetGeminiInputAudioPricePerMillionTokens(modelName)}
	}
	if qualitySize, ok := strings.CutPrefix(toolName, "image_generation_"); ok {
		quality, size, _ := strings.Cut(qualitySize, "_")
		return toolPrice{amount: GetGPTImage1PriceOnceCall(quality, size)}
	}
	return toolPrice{}
}

func ImageGenerationToolName(quality, size string) string {
	switch quality {
	case "low", "medium", "high":
		switch size {
		case "1024x1024", "1024x1536", "1536x1024":
			return "image_generation_" + quality + "_" + size
		}
	}
	return "image_generation_high_1024x1024"
}

// GetToolPrice is a convenience wrapper when no model name is needed.
func GetToolPrice(toolName string) float64 {
	return GetToolPriceForModel(toolName, "")
}

// ---------------------------------------------------------------------------
// GPT Image 1 per-call pricing (special: depends on quality + size)
// 值为人民币（¥/次）；÷ usdToCNY 就是上游的美元原价。
// ---------------------------------------------------------------------------

const (
	GPTImage1Low1024x1024    = 0.011 * usdToCNY
	GPTImage1Low1024x1536    = 0.016 * usdToCNY
	GPTImage1Low1536x1024    = 0.016 * usdToCNY
	GPTImage1Medium1024x1024 = 0.042 * usdToCNY
	GPTImage1Medium1024x1536 = 0.063 * usdToCNY
	GPTImage1Medium1536x1024 = 0.063 * usdToCNY
	GPTImage1High1024x1024   = 0.167 * usdToCNY
	GPTImage1High1024x1536   = 0.25 * usdToCNY
	GPTImage1High1536x1024   = 0.25 * usdToCNY
)

func GetGPTImage1PriceOnceCall(quality string, size string) float64 {
	prices := map[string]map[string]float64{
		"low": {
			"1024x1024": GPTImage1Low1024x1024,
			"1024x1536": GPTImage1Low1024x1536,
			"1536x1024": GPTImage1Low1536x1024,
		},
		"medium": {
			"1024x1024": GPTImage1Medium1024x1024,
			"1024x1536": GPTImage1Medium1024x1536,
			"1536x1024": GPTImage1Medium1536x1024,
		},
		"high": {
			"1024x1024": GPTImage1High1024x1024,
			"1024x1536": GPTImage1High1024x1536,
			"1536x1024": GPTImage1High1536x1024,
		},
	}

	if qualityMap, exists := prices[quality]; exists {
		if price, exists := qualityMap[size]; exists {
			return price
		}
	}

	return GPTImage1High1024x1024
}

// ---------------------------------------------------------------------------
// Gemini audio input pricing (per-million tokens, model-specific)
// 值为人民币（¥/百万 tokens）；÷ usdToCNY 就是上游的美元原价。
// ---------------------------------------------------------------------------

const (
	Gemini25FlashPreviewInputAudioPrice     = 1.00 * usdToCNY
	Gemini25FlashProductionInputAudioPrice  = 1.00 * usdToCNY
	Gemini25FlashLitePreviewInputAudioPrice = 0.50 * usdToCNY
	Gemini25FlashNativeAudioInputAudioPrice = 3.00 * usdToCNY
	Gemini20FlashInputAudioPrice            = 0.70 * usdToCNY
	GeminiRoboticsER15InputAudioPrice       = 1.00 * usdToCNY
)

func GetGeminiInputAudioPricePerMillionTokens(modelName string) float64 {
	if strings.HasPrefix(modelName, "gemini-2.5-flash-preview-native-audio") {
		return Gemini25FlashNativeAudioInputAudioPrice
	} else if strings.HasPrefix(modelName, "gemini-2.5-flash-preview-lite") {
		return Gemini25FlashLitePreviewInputAudioPrice
	} else if strings.HasPrefix(modelName, "gemini-2.5-flash-preview") {
		return Gemini25FlashPreviewInputAudioPrice
	} else if strings.HasPrefix(modelName, "gemini-2.5-flash") {
		return Gemini25FlashProductionInputAudioPrice
	} else if strings.HasPrefix(modelName, "gemini-2.0-flash") {
		return Gemini20FlashInputAudioPrice
	} else if strings.HasPrefix(modelName, "gemini-robotics-er-1.5") {
		return GeminiRoboticsER15InputAudioPrice
	}
	return 0
}
