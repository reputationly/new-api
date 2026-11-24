package service

import (
	"math"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// ToolCallUsage captures all tool call counts from a single request.
type ToolCallUsage struct {
	ModelName              string
	WebSearchCalls         int
	WebSearchToolName      string // "web_search_preview", "web_search", etc.
	FileSearchCalls        int
	ImageGenerationCall    bool
	ImageGenerationQuality string
	ImageGenerationSize    string
}

// ToolCallItem represents a single billed tool usage line.
type ToolCallItem struct {
	Name       string  `json:"name"`
	CallCount  int     `json:"call_count"`
	PricePer1K float64 `json:"price_per_1k"`
	TotalPrice float64 `json:"total_price"`
	Quota      int     `json:"quota"`
}

// ToolCallResult holds the aggregated tool call billing for a request.
type ToolCallResult struct {
	TotalQuota int            `json:"total_quota"`
	Items      []ToolCallItem `json:"items,omitempty"`
}

// ComputeToolCallQuota calculates the total quota for all tool calls in a
// request. Tool prices use the shared currency resolver, which supports
// model-prefix overrides. groupRatio is applied.
func ComputeToolCallQuota(usage ToolCallUsage, groupRatio float64) (ToolCallResult, error) {
	var items []ToolCallItem
	totalQuota := 0
	rate, hasRate := billing_setting.GetModelSourceRate()
	effectivePrice := func(toolName string) (float64, error) {
		price, _, err := operation_setting.ResolveToolPriceCNY(toolName, usage.ModelName, rate, hasRate)
		return price, err
	}

	addItem := func(toolName string, count int) error {
		if count <= 0 {
			return nil
		}
		pricePer1K, err := effectivePrice(toolName)
		if err != nil {
			return err
		}
		if pricePer1K <= 0 {
			return nil
		}
		totalPrice := pricePer1K * float64(count) / 1000
		quota := int(math.Round(totalPrice * common.QuotaPerYuan * groupRatio))
		items = append(items, ToolCallItem{
			Name:       toolName,
			CallCount:  count,
			PricePer1K: pricePer1K,
			TotalPrice: totalPrice,
			Quota:      quota,
		})
		totalQuota += quota
		return nil
	}

	if usage.WebSearchCalls > 0 && usage.WebSearchToolName != "" {
		if err := addItem(usage.WebSearchToolName, usage.WebSearchCalls); err != nil {
			return ToolCallResult{}, err
		}
	}

	if usage.FileSearchCalls > 0 {
		if err := addItem("file_search", usage.FileSearchCalls); err != nil {
			return ToolCallResult{}, err
		}
	}

	if usage.ImageGenerationCall {
		price, err := effectivePrice(operation_setting.ImageGenerationToolName(usage.ImageGenerationQuality, usage.ImageGenerationSize))
		if err != nil {
			return ToolCallResult{}, err
		}
		quota := int(math.Round(price * common.QuotaPerYuan * groupRatio))
		items = append(items, ToolCallItem{
			Name:       "image_generation",
			CallCount:  1,
			PricePer1K: price,
			TotalPrice: price,
			Quota:      quota,
		})
		totalQuota += quota
	}

	return ToolCallResult{
		TotalQuota: totalQuota,
		Items:      items,
	}, nil
}
