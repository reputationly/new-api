package ratio_setting

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/types"
)

// 分组内按模型的折扣。设计见 docs/group-management-redesign.md。
//
// 解决的问题：GroupRatio 是整组一个标量，对所有模型一视同仁。但两条供应链
// （自建 GPUStack 的 default、并行科技中转的 premium）的成本结构是**逐模型**
// 不同的——不是「premium 整体贵 1.5 倍」，而是「premium 的 GLM-5 贵、
// premium 的 wan2.2 反而便宜」。靠改 ModelRatio 解决不了：那是全局的，
// 一改就把另一条供应链的价也改了。
// 模式常量定义在 types（见 types/price_data.go）——它随 GroupRatioInfo 一路流到
// 日志与前端，是共享词汇表，不该有第二份定义。
const (
	RatioModeMultiply = types.RatioModeMultiply
	RatioModeOverride = types.RatioModeOverride
)

type ModelRatioRule struct {
	Mode   string  `json:"mode"`
	Value  float64 `json:"value"`
	Remark string  `json:"remark,omitempty"` // 运营备注：半年后没人记得 premium 的 GLM-5 为什么是 2.2
}

// UnmarshalJSON 兼容裸数字写法：{"GLM-5": 0.5} 等价于
// {"GLM-5": {"mode": "multiply", "value": 0.5}}。手工编辑 JSON 的人少踩一个坑。
func (r *ModelRatioRule) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	if !strings.HasPrefix(trimmed, "{") {
		var v float64
		if err := common.Unmarshal(data, &v); err != nil {
			return err
		}
		r.Mode = RatioModeMultiply
		r.Value = v
		return nil
	}
	// 别名类型断开 UnmarshalJSON 的递归
	type rawRule ModelRatioRule
	var raw rawRule
	if err := common.Unmarshal(data, &raw); err != nil {
		return err
	}
	*r = ModelRatioRule(raw)
	if r.Mode == "" {
		r.Mode = RatioModeMultiply
	}
	return nil
}

// groupModelRatioMap: 使用分组 -> 模型模式串 -> 规则。
// 模式串为精确模型名或前缀通配（如 "wan2.2-*"）。
var groupModelRatioMap = types.NewRWMap[string, map[string]ModelRatioRule]()

func GroupModelRatio2JSONString() string {
	return groupModelRatioMap.MarshalJSONString()
}

func UpdateGroupModelRatioByJSONString(jsonStr string) error {
	return types.LoadFromJsonString(groupModelRatioMap, jsonStr)
}

func GetGroupModelRatioCopy() map[string]map[string]ModelRatioRule {
	return groupModelRatioMap.ReadAll()
}

func CheckGroupModelRatio(jsonStr string) error {
	if strings.TrimSpace(jsonStr) == "" {
		return nil
	}
	check := make(map[string]map[string]ModelRatioRule)
	if err := common.Unmarshal([]byte(jsonStr), &check); err != nil {
		return err
	}
	for group, rules := range check {
		for pattern, rule := range rules {
			if strings.TrimSpace(pattern) == "" {
				return fmt.Errorf("group %s has an empty model pattern", group)
			}
			// 只支持前缀通配，不引入正则：正则写错不报错，只会静默算错价。
			// 与 setting/system_setting/moderation.go 的 ModelFilter 同一约定。
			if idx := strings.Index(pattern, "*"); idx != -1 && idx != len(pattern)-1 {
				return fmt.Errorf("group %s model pattern %q: '*' is only supported as a trailing wildcard", group, pattern)
			}
			switch rule.Mode {
			case RatioModeMultiply, RatioModeOverride, "":
			default:
				return fmt.Errorf("group %s model %s has unknown mode %q", group, pattern, rule.Mode)
			}
			if rule.Value < 0 {
				return errors.New("group model ratio must be not less than 0: " + group + "." + pattern)
			}
		}
	}
	return nil
}

// MatchModelPattern 报告模式串是否匹配模型名。
//
// 导出是给管理端用的：分组管理页要标出「配了折扣但匹配不到任何模型」的失效规则，
// 那里必须复用同一份匹配规则——另写一份判定，两边一旦分叉，页面就会把生效的规则
// 报成失效、或者反过来。
func MatchModelPattern(pattern, modelName string) bool {
	_, ok := matchModelPattern(pattern, modelName)
	return ok
}

// matchModelPattern 报告模式串是否匹配模型名，并返回特异性权重。
// 只支持尾部 '*' 通配。权重：精确 2 > 前缀通配 1；不匹配返回 0, false。
func matchModelPattern(pattern, modelName string) (int, bool) {
	if pattern == modelName {
		return 2, true
	}
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		if strings.HasPrefix(modelName, prefix) {
			return 1, true
		}
	}
	return 0, false
}

// pickModelRule 在 group 的规则集里取最具体的一条。
// 精确 > 前缀通配；同为通配时模式串长者优先（"wan2.2-t2v-*" 胜过 "wan2.2-*"）。
func pickModelRule(group, modelName string) (string, ModelRatioRule, bool) {
	rules, ok := groupModelRatioMap.Get(group)
	if !ok {
		return "", ModelRatioRule{}, false
	}
	return pickRuleFrom(rules, modelName)
}

// pickRuleFrom 是 pickModelRule 的规则集无关版本，供 Layer 3（用户档折扣，
// 见 user_group_model_ratio.go）复用。两层的模式串语义必须逐位一致——各写一份
// 匹配逻辑，一旦分叉就会出现「Layer 2 命中而 Layer 3 不命中」这种没人能解释的价格。
func pickRuleFrom(rules map[string]ModelRatioRule, modelName string) (string, ModelRatioRule, bool) {
	if len(rules) == 0 || modelName == "" {
		return "", ModelRatioRule{}, false
	}
	bestWeight := -1
	bestPattern := ""
	var best ModelRatioRule
	for pattern, rule := range rules {
		weight, matched := matchModelPattern(pattern, modelName)
		if !matched {
			continue
		}
		// 同权重比模式串长度：更长的前缀更具体
		if weight > bestWeight || (weight == bestWeight && len(pattern) > len(bestPattern)) {
			bestWeight = weight
			bestPattern = pattern
			best = rule
		}
	}
	if bestWeight < 0 {
		return "", ModelRatioRule{}, false
	}
	return bestPattern, best, true
}

// RatioResolution 是一次分组倍率解析的完整过程，不只是结果。
// 日志可解释性、模型广场展示、管理端试算器都要靠这几个中间值——
// 只回一个 Final 的话，运营拿到账单反算不出这个数是怎么来的。
type RatioResolution struct {
	Final float64 // 最终倍率，计费只读这个

	GroupRatio      float64 // Layer 0：分组基础倍率原值
	SpecialRatio    float64 // Layer 1：命中的身份折扣值
	HasSpecialRatio bool    // Layer 1 是否命中
	Base            float64 // Layer 0/1 之后的基准

	RuleMatch string  // Layer 2 命中的模式串，"" = 未命中
	RuleMode  string  // Layer 2 模式
	RuleValue float64 // Layer 2 配置值

	AfterModelRule float64 // Layer 2 之后、套用用户档折扣之前的值

	UserRuleMatch string  // Layer 3 命中的模式串，"" = 未命中
	UserRuleValue float64 // Layer 3 配置值（恒为 multiply）
}

// ResolveGroupRatio 四层解析计费倍率。
//
//	Layer 0  base  = GroupRatio[usingGroup]                   场景倍率
//	Layer 1  base ← GroupGroupRatio[userGroup][usingGroup]    命中即覆盖
//	Layer 2  final ← GroupModelRatio[usingGroup][modelName]   override 覆盖 / multiply 叠乘
//	Layer 3  final × UserGroupModelRatio[userGroup][modelName] 恒为叠乘
//
// 为什么分层、而不是把各类规则拍平成一个规则集「取最具体的一条」：
// 设 GroupGroupRatio{vip: {premium: 0.7}}（vip 全线 7 折）与
// GroupModelRatio{premium: {GLM-5: ×0.5}}（GLM-5 半价）。拍平后模型精确匹配
// 胜过分组级，只会命中后者 → 1.5 × 0.5 = 0.75，**vip 身份被静默丢掉，
// vip 反而比预期贵**。分层则 0.7 × 0.5 = 0.35，身份折扣与促销折扣正交叠加。
//
// Layer 3 与 Layer 0/1/2 的分工是本次改造的核心：前三层按「使用分组」索引，
// 描述的是成本（走哪条供应链、那条链上这个模型多少钱）；Layer 3 按「用户分组」
// 索引，描述的是售价（这批用户打几折）。两个维度正交，所以 Layer 3 **一律叠乘**，
// 包括 Layer 2 命中 override 时——override 说的是「这条链这个模型的成本就是这个
// 价」，用户的身份折扣是另一回事，不该被它吃掉。
//
// modelName 传空（无模型上下文的调用点）时 Layer 2/3 恒不命中，
// 结果与改造前逐位相同。
func ResolveGroupRatio(userGroup, usingGroup, modelName string) RatioResolution {
	res := RatioResolution{}

	res.GroupRatio = GetGroupRatio(usingGroup)
	res.Base = res.GroupRatio

	if special, ok := GetGroupGroupRatio(userGroup, usingGroup); ok {
		res.HasSpecialRatio = true
		res.SpecialRatio = special
		res.Base = special
	}

	res.Final = res.Base

	if pattern, rule, ok := pickModelRule(usingGroup, modelName); ok {
		res.RuleMatch = pattern
		res.RuleMode = rule.Mode
		res.RuleValue = rule.Value
		if rule.Mode == RatioModeOverride {
			res.Final = rule.Value
		} else {
			res.Final = res.Base * rule.Value
		}
	}

	res.AfterModelRule = res.Final

	if pattern, rule, ok := pickUserModelRule(userGroup, modelName); ok {
		res.UserRuleMatch = pattern
		res.UserRuleValue = rule.Value
		res.Final = res.Final * rule.Value
	}

	return res
}
