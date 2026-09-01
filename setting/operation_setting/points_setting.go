package operation_setting

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

// PointsSetting 积分系统配置。积分是独立于 Quota 余额的营销赠送钱包，
// 内部以 quota unit 记账（与 Quota 同单位），1 积分 = 1 分钱（初始值）。
type PointsSetting struct {
	Enabled           bool     `json:"enabled"`             // 积分系统总开关
	RequireKyc        bool     `json:"require_kyc"`         // 未实名用户不得参加积分活动（只卡发放：签到、邀请人赠分；不卡消费）
	QuotaPerPoint     float64  `json:"quota_per_point"`     // 1 积分对应 quota unit，默认 ≈684.93
	EnabledGroups     []string `json:"enabled_groups"`      // 允许积分抵扣的分组白名单（空=所有分组只扣余额）
	KycVerifiedPoints int      `json:"kyc_verified_points"` // 实名通过赠送积分数（本人），0=关闭
	KycInviterPoints  int      `json:"kyc_inviter_points"`  // 被邀请用户实名通过时邀请人赠送积分数，0=关闭
	NewUserPoints     int      `json:"new_user_points"`     // 新用户注册赠送积分数，0=关闭；不受 RequireKyc 约束

	// ChannelRewards 渠道积分奖励：按**邀请人**覆盖 NewUserPoints。
	// 用渠道商自己的邀请链接注册的新人拿这里的数，其他人拿 NewUserPoints。
	ChannelRewards []ChannelPointsReward `json:"channel_rewards"`
}

// ChannelPointsReward 一条渠道奖励规则。
//
// 存邀请人的用户 ID 而不是 aff_code：aff_code 在 controller/user.go:380 为空时会被
// 重新生成，拿它当主键会让规则在某次访问后静默失配；ID 是稳定的。
type ChannelPointsReward struct {
	InviterId int    `json:"inviter_id"` // 渠道商的用户 ID，唯一，权威字段
	Username  string `json:"username"`   // 冗余，仅列表回显；判定一律以 InviterId 为准
	Points    int    `json:"points"`     // 覆盖后的注册赠分；0 = 该渠道不送（彻底覆盖，不回落默认值）
	Remark    string `json:"remark"`     // 渠道名 / 合作方，运营自用
	Enabled   bool   `json:"enabled"`    // 活动结束关掉即可，不必删除配置

	// AffCode 冗余存渠道商的邀请码，配置页据此直接拼出邀请链接给运营复制。
	// 后端不读它——发放判定只认 InviterId。
	//
	// 必须在结构体里声明而不是只躺在前端发来的 JSON 里：OptionMap 当前是「先
	// ExportAllConfigs 写一遍、再被 loadOptionsFromDatabase 用 DB 原文覆盖」，
	// 未声明的字段能活下来纯属这个顺序的副产物。一旦有人调 SaveToDB（现在无调用点）
	// 或在后端做一次读出改回写，这个字段就会被静默抹掉，表现是所有邀请链接列变空。
	AffCode string `json:"aff_code"`
}

// CheckChannelPointsRewards 保存前校验渠道奖励配置。
//
// 一个邀请人只允许一条规则：GetChannelPointsReward 取首个命中就返回，配了两条时
// 生效的是哪条取决于数组顺序——运营看着两行不同的积分数，猜不出发出去的是哪个。
// 与其在查找时做兜底，不如在写入时就堵死。
func CheckChannelPointsRewards(jsonStr string) error {
	if strings.TrimSpace(jsonStr) == "" {
		return nil
	}
	var rules []ChannelPointsReward
	if err := common.Unmarshal([]byte(jsonStr), &rules); err != nil {
		return err
	}
	seen := make(map[int]bool, len(rules))
	for _, r := range rules {
		if r.InviterId <= 0 {
			return errors.New("渠道奖励存在未选择用户的规则")
		}
		if seen[r.InviterId] {
			return fmt.Errorf("用户 %s(ID %d) 配置了多条渠道奖励，每个用户只能配一条",
				r.Username, r.InviterId)
		}
		seen[r.InviterId] = true
		if r.Points < 0 {
			return fmt.Errorf("用户 %s(ID %d) 的奖励积分不能为负", r.Username, r.InviterId)
		}
	}
	return nil
}

// GetChannelPointsReward 查某个邀请人是否配了渠道奖励。
//
// 只认**直接邀请人**，不向上递归：A 邀 B、B 邀 C 时 C 认 B 的规则。多级分佣是另一件事，
// 掺进来会让「这个新人为什么拿到 X 积分」变得无法从一条记录解释。
//
// 命中停用的规则等同未命中——停用就该完全退回默认行为。
func GetChannelPointsReward(inviterId int) (*ChannelPointsReward, bool) {
	if inviterId <= 0 {
		return nil, false
	}
	for i := range pointsSetting.ChannelRewards {
		r := &pointsSetting.ChannelRewards[i]
		if r.Enabled && r.InviterId == inviterId {
			return r, true
		}
	}
	return nil, false
}

// 默认配置：总开关关闭、要求实名（fail-safe 防薅羊毛）、白名单空（采购分组零配置即安全）。
var pointsSetting = PointsSetting{
	Enabled:           false,
	RequireKyc:        true,
	QuotaPerPoint:     common.QuotaPerUnit / 730.0, // ≈684.93，1 积分 = 1 分钱
	EnabledGroups:     []string{},
	KycVerifiedPoints: 0,
	KycInviterPoints:  0,
	NewUserPoints:     0,
	ChannelRewards:    []ChannelPointsReward{},
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
