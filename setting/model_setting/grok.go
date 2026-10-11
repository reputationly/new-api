package model_setting

import "github.com/QuantumNous/new-api/setting/config"

// GrokSettings defines Grok model configuration.
type GrokSettings struct {
	ViolationDeductionEnabled bool    `json:"violation_deduction_enabled"`
	ViolationDeductionAmount  float64 `json:"violation_deduction_amount"`
}

var defaultGrokSettings = GrokSettings{
	ViolationDeductionEnabled: true,
	// 人民币（¥/次）；原为上游的 $0.05（0.05 × 7.3 = 0.365）。
	ViolationDeductionAmount: 0.365,
}

var grokSettings = defaultGrokSettings

func init() {
	config.GlobalConfig.Register("grok", &grokSettings)
}

func GetGrokSettings() *GrokSettings {
	return &grokSettings
}
