package operation_setting

import "github.com/QuantumNous/new-api/setting/config"

// CheckinSetting 签到功能配置
type CheckinSetting struct {
	Enabled    bool   `json:"enabled"`     // 是否启用签到功能
	MinQuota   int    `json:"min_quota"`   // 签到最小额度奖励
	MaxQuota   int    `json:"max_quota"`   // 签到最大额度奖励
	RewardType string `json:"reward_type"` // 奖励类型 "quota"(默认,向后兼容) | "points"
	MinPoints  int    `json:"min_points"`  // RewardType=points 时生效，单位:积分
	MaxPoints  int    `json:"max_points"`  // RewardType=points 时生效，单位:积分
}

// 默认配置
var checkinSetting = CheckinSetting{
	Enabled:    false,   // 默认关闭
	MinQuota:   1000,    // 默认最小额度 1000 (约 0.002 USD)
	MaxQuota:   10000,   // 默认最大额度 10000 (约 0.02 USD)
	RewardType: "quota", // 默认发额度，向后兼容
	MinPoints:  0,
	MaxPoints:  0,
}

func init() {
	// 注册到全局配置管理器
	config.GlobalConfig.Register("checkin_setting", &checkinSetting)
}

// GetCheckinSetting 获取签到配置
func GetCheckinSetting() *CheckinSetting {
	return &checkinSetting
}

// IsCheckinEnabled 是否启用签到功能
func IsCheckinEnabled() bool {
	return checkinSetting.Enabled
}

// GetCheckinQuotaRange 获取签到额度范围
func GetCheckinQuotaRange() (min, max int) {
	return checkinSetting.MinQuota, checkinSetting.MaxQuota
}
