package operation_setting

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

// PointsSetting 积分系统配置。积分是独立于 Quota 余额的营销赠送钱包，
// 内部以 quota unit 记账（与 Quota 同单位），1 积分 = 1 分钱（初始值）。
type PointsSetting struct {
	Enabled           bool     `json:"enabled"`             // 积分系统总开关
	RequireKyc        bool     `json:"require_kyc"`         // 未实名用户不得参加积分（发放+使用双卡）
	QuotaPerPoint     float64  `json:"quota_per_point"`     // 1 积分对应 quota unit，默认 ≈684.93
	EnabledGroups     []string `json:"enabled_groups"`      // 允许积分抵扣的分组白名单（空=所有分组只扣余额）
	KycVerifiedPoints int      `json:"kyc_verified_points"` // 实名通过赠送积分数（本人），0=关闭
	KycInviterPoints  int      `json:"kyc_inviter_points"`  // 被邀请用户实名通过时邀请人赠送积分数，0=关闭
}

// 默认配置：总开关关闭、要求实名（fail-safe 防薅羊毛）、白名单空（采购分组零配置即安全）。
var pointsSetting = PointsSetting{
	Enabled:           false,
	RequireKyc:        true,
	QuotaPerPoint:     common.QuotaPerUnit / 730.0, // ≈684.93，1 积分 = 1 分钱
	EnabledGroups:     []string{},
	KycVerifiedPoints: 0,
	KycInviterPoints:  0,
}

func init() {
	// 注册到全局配置管理器（option 表持久化，key 形如 points_setting.enabled）
	config.GlobalConfig.Register("points_setting", &pointsSetting)
	// 依赖倒置：把实时 QuotaPerPoint 注入 common 换算层（common 不能 import 本包）
	common.QuotaPerPointFunc = func() float64 { return pointsSetting.QuotaPerPoint }
}

// GetPointsSetting 获取积分配置
func GetPointsSetting() *PointsSetting {
	return &pointsSetting
}

// IsPointsEnabledForGroup 判断某分组是否允许积分抵扣（白名单）。
// 总开关关闭或分组不在白名单 → false（采购分组 fail-safe 只扣余额）。
func IsPointsEnabledForGroup(group string) bool {
	if !pointsSetting.Enabled {
		return false
	}
	for _, g := range pointsSetting.EnabledGroups {
		if g == group {
			return true
		}
	}
	return false
}
