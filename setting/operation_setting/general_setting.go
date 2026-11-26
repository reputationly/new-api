package operation_setting

import "github.com/QuantumNous/new-api/setting/config"

// 额度展示类型
const (
	QuotaDisplayTypeUSD    = "USD"
	QuotaDisplayTypeCNY    = "CNY"
	QuotaDisplayTypeTokens = "TOKENS"
	QuotaDisplayTypeCustom = "CUSTOM"
)

type GeneralSetting struct {
	DocsLink string `json:"docs_link"`
	// 蒜狸小助手下载页地址。留空时顶栏不显示该入口。
	AssistantLink       string `json:"assistant_link"`
	PingIntervalEnabled bool   `json:"ping_interval_enabled"`
	PingIntervalSeconds int    `json:"ping_interval_seconds"`
	// 当前站点额度展示类型：USD / CNY / TOKENS
	QuotaDisplayType string `json:"quota_display_type"`
	// 自定义货币符号，用于 CUSTOM 展示类型
	CustomCurrencySymbol string `json:"custom_currency_symbol"`
	// 自定义货币与美元汇率（1 USD = X Custom）
	CustomCurrencyExchangeRate float64 `json:"custom_currency_exchange_rate"`
	// 默认对话模型。留空时沿用"按出现顺序第一条"的旧规则；指定后能力目录把这条标为 chat 默认项。
	// 客户端（蒜狸小助手 / BeefTV）据此直接选中它，不再各自硬编码。
	DefaultChatModel string `json:"default_chat_model"`
	// 模型的输入模态，JSON 对象：{"qwen3.8-flash":["image","video"]}。
	// 能力目录把模态原样透传给对话模型条目，客户端据此决定能不能接图片/视频/音频。
	// 留空时用出厂已知表（见 model_modalities.go）。
	ModelModalities string `json:"model_modalities"`
}

// 默认配置
var generalSetting = GeneralSetting{
	DocsLink:                   "https://docs.newapi.pro",
	AssistantLink:              "",
	PingIntervalEnabled:        false,
	PingIntervalSeconds:        60,
	QuotaDisplayType:           QuotaDisplayTypeCNY,
	CustomCurrencySymbol:       "¤",
	CustomCurrencyExchangeRate: 1.0,
	DefaultChatModel:           "",
	ModelModalities:            "",
}

func init() {
	// 注册到全局配置管理器
	config.GlobalConfig.Register("general_setting", &generalSetting)
}

func GetGeneralSetting() *GeneralSetting {
	return &generalSetting
}

// IsCurrencyDisplay 是否以货币形式展示（美元或人民币）
func IsCurrencyDisplay() bool {
	return generalSetting.QuotaDisplayType != QuotaDisplayTypeTokens
}

// IsCNYDisplay 是否以人民币展示
func IsCNYDisplay() bool {
	return generalSetting.QuotaDisplayType == QuotaDisplayTypeCNY
}

// GetQuotaDisplayType 返回额度展示类型
func GetQuotaDisplayType() string {
	return generalSetting.QuotaDisplayType
}

// GetCurrencySymbol 返回当前展示类型对应符号
func GetCurrencySymbol() string {
	switch generalSetting.QuotaDisplayType {
	case QuotaDisplayTypeUSD:
		return "$"
	case QuotaDisplayTypeCNY:
		return "¥"
	case QuotaDisplayTypeCustom:
		if generalSetting.CustomCurrencySymbol != "" {
			return generalSetting.CustomCurrencySymbol
		}
		return "¤"
	default:
		return ""
	}
}
