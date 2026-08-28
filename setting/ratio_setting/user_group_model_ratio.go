package ratio_setting

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/types"
)

// 用户档位折扣（解析链 Layer 3）。设计见 docs/user-tier-pricing-and-topup-package-design.md。
//
// 与 GroupModelRatio（Layer 2）的分工是这次改造的核心：
//
//	Layer 0/1/2  按「使用分组」索引 —— 成本侧：走哪条供应链、那条链上这个模型多少钱
//	Layer 3      按「用户分组」索引 —— 售价侧：这批用户打几折
//
// 两者维度正交，因此乘法叠加。把折扣挤进 Layer 0/1 会让「用户价格」与「走了哪家
// 供应商」绑死，而用户不该、也不能感知自己被路由到了哪家（§3.2）。
//
// userGroupModelRatioMap: 用户分组 -> 模型模式串 -> 规则。模式串语义与
// groupModelRatioMap 完全一致（精确名 / 尾部通配 / "*" 兜底），复用同一套匹配。
var userGroupModelRatioMap = types.NewRWMap[string, map[string]ModelRatioRule]()

func UserGroupModelRatio2JSONString() string {
	return userGroupModelRatioMap.MarshalJSONString()
}

func UpdateUserGroupModelRatioByJSONString(jsonStr string) error {
	return types.LoadFromJsonString(userGroupModelRatioMap, jsonStr)
}

func GetUserGroupModelRatioCopy() map[string]map[string]ModelRatioRule {
	return userGroupModelRatioMap.ReadAll()
}

// HasUserGroupModelRules 报告某用户分组是否配了任何折扣规则。
//
// 导出是给 controller/pricing.go 用的：它遍历分组展开终值表时要决定跳不跳过，
// 只看 GroupModelRatio 会漏掉「仅配了 Layer 3」的情况，结果是模型广场显示价
// 偏高而实扣正确——最难发现的那类不一致（设计文档 §8.0）。
func HasUserGroupModelRules(userGroup string) bool {
	rules, ok := userGroupModelRatioMap.Get(userGroup)
	return ok && len(rules) > 0
}

// CheckUserGroupModelRatio 校验用户档折扣配置。
//
// 与 CheckGroupModelRatio 的唯一区别：**拒绝 override**。Layer 3 的语义就是
// 「打折」，一旦允许绝对值，它会吃掉 Layer 0/1/2 承载的全部成本信息——在成本高的
// 模型上直接亏损，而日志上只看得到一个最终倍率，反算不出是哪一层拍的板。
// 需要绝对定价时用 Layer 1/2 表达，那是成本侧的事。
func CheckUserGroupModelRatio(jsonStr string) error {
	if strings.TrimSpace(jsonStr) == "" {
		return nil
	}
	check := make(map[string]map[string]ModelRatioRule)
	if err := common.Unmarshal([]byte(jsonStr), &check); err != nil {
		return err
	}
	for userGroup, rules := range check {
		for pattern, rule := range rules {
			if strings.TrimSpace(pattern) == "" {
				return fmt.Errorf("user group %s has an empty model pattern", userGroup)
			}
			// 只支持尾部通配，与 CheckGroupModelRatio 同一约定：正则写错不报错，
			// 只会静默算错价。
			if idx := strings.Index(pattern, "*"); idx != -1 && idx != len(pattern)-1 {
				return fmt.Errorf("user group %s model pattern %q: '*' is only supported as a trailing wildcard", userGroup, pattern)
			}
			switch rule.Mode {
			case RatioModeMultiply, "":
			case RatioModeOverride:
				return fmt.Errorf("user group %s model %s: override is not allowed in user tier discounts, use multiply", userGroup, pattern)
			default:
				return fmt.Errorf("user group %s model %s has unknown mode %q", userGroup, pattern, rule.Mode)
			}
			if rule.Value < 0 {
				return errors.New("user group model ratio must be not less than 0: " + userGroup + "." + pattern)
			}
		}
	}
	return nil
}

// GetUserGroupFallbackRatio 返回用户档折扣中**与模型无关**的那一档，即 "*" 兜底
// 规则的值；未配置返回 1。
//
// 它服务于 /api/pricing 的下发策略（设计文档 §8.0）：前端只做
// `group_model_ratio[g]?.[m] ?? group_ratio[g]` 这一步查表，不含任何解析逻辑。
// 把 "*" 兜底乘进 group_ratio，未命中具体模型规则的那些模型走 fallback 时才是
// 对的价；否则 classic 与 mobile 会一起显示偏高的价（实扣正确），是最难发现的
// 那类不一致。
//
// 只取 "*"，不取通配前缀（如 "wan2.2-*"）——后者是模型相关的，必须逐模型展开进
// group_model_ratio 表，混进 group_ratio 会让不匹配的模型也被打折。
func GetUserGroupFallbackRatio(userGroup string) float64 {
	rules, ok := userGroupModelRatioMap.Get(userGroup)
	if !ok {
		return 1
	}
	rule, ok := rules["*"]
	if !ok {
		return 1
	}
	return rule.Value
}

// pickUserModelRule 在用户分组的规则集里取最具体的一条，语义与 pickModelRule 相同。
func pickUserModelRule(userGroup, modelName string) (string, ModelRatioRule, bool) {
	rules, ok := userGroupModelRatioMap.Get(userGroup)
	if !ok {
		return "", ModelRatioRule{}, false
	}
	return pickRuleFrom(rules, modelName)
}
