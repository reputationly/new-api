package ratio_setting

import (
	"errors"
	"fmt"
	"strings"
	"time"

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
// 见 user_group_model_ratio.go）与 Layer 4（时段折扣，见 group_time_ratio.go）复用。
// 三层的模式串语义必须逐位一致——各写一份匹配逻辑，一旦分叉就会出现
// 「Layer 2 命中而 Layer 3 不命中」这种没人能解释的价格。
//
// 泛型化是为了让 Layer 4 的 []TimeRule 走同一份匹配：规则值的类型与模式串怎么匹配
// 无关，为它再抄一份 for 循环正是上面那句话要防的事。
func pickRuleFrom[T any](rules map[string]T, modelName string) (string, T, bool) {
	var zero T
	if len(rules) == 0 || modelName == "" {
		return "", zero, false
	}
	bestWeight := -1
	bestPattern := ""
	var best T
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
		return "", zero, false
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

	// NormalRatio 是 Layer 0/1/2 的结果，即**未命中任何时段**时的倍率。
	// 展示层靠它在分时定价表里补出「其余时段」那一行——只列配了规则的时段，
	// 用户看不到全天覆盖，也就说不出自己此刻到底按哪一档付钱。
	NormalRatio float64

	UserRuleMatch string  // Layer 3 命中的模式串，"" = 未命中
	UserRuleValue float64 // Layer 3 配置值（恒为 multiply）

	TimeWindow string  // Layer 4 命中的时段模板键，"" = 未命中
	TimeLabel  string  // 时段模板显示名，如「深夜档」
	TimeValue  float64 // Layer 4 输入侧系数（乘在 NormalRatio 上；<1 空闲、>1 紧缺）
	// TimeOutputFactor 是输出侧系数。分时段价目不一定等比例变化
	// （忙时输入 2/输出 8、闲时输入 1/输出 7），而整单只乘输入侧；
	// 输出侧的差额由 types.GroupRatioInfo.CompletionRatioScale 折回 completion_ratio。
	TimeOutputFactor float64
}

// UserMultiplier 返回 Layer 3 的乘数，未命中时为 1。
//
// 存在的理由：UserRuleValue 未命中时是零值 0，直接拿去乘会把价格算成免费。
// 展示层要算「某个时段的最终倍率」时必须带上这一层，这里把那个陷阱收口。
func (r RatioResolution) UserMultiplier() float64 {
	if r.UserRuleMatch == "" {
		return 1
	}
	return r.UserRuleValue
}

// ResolveGroupRatio 四层解析计费倍率。
//
//	Layer 0  base  = GroupRatio[usingGroup]                   场景倍率
//	Layer 1  base ← GroupGroupRatio[userGroup][usingGroup]    命中即覆盖
//	Layer 2  final ← GroupModelRatio[usingGroup][modelName]   override 覆盖 / multiply 叠乘
//	Layer 4  final ← 常规价 × GroupTimeRatio[usingGroup][modelName] 命中时段则乘在常规价上（叠加）
//	Layer 3  final × UserGroupModelRatio[userGroup][modelName]  恒为叠乘（永远最后）
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
// Layer 4（时段价）描述的是价目的时间维度：算力紧缺时段贵一点、空闲时段便宜一点。
// 它**乘在常规价上**（叠乘），不取代 Layer 2 —— 取代会把模型折扣与 override 锁价
// 一起吃掉，还会逼管理员把「折扣 × 时段」自己心算成一个系数（见
// docs/time-price-axis-design.md §4.1/§4.8）。叠乘之后 <1 恒便宜、>1 恒更贵。
//
// 它排在 Layer 3 **之前**：Layer 3 是售价侧（这批用户打几折），与此刻价目正交，
// 不该被时段系数吃掉。
//
// modelName 传空（无模型上下文的调用点）时 Layer 2/3/4 恒不命中，
// 结果与改造前逐位相同。
func ResolveGroupRatio(userGroup, usingGroup, modelName string) RatioResolution {
	return ResolveGroupRatioAt(userGroup, usingGroup, modelName, time.Now())
}

// ResolveGroupRatioAt 是 ResolveGroupRatio 的可注入时刻版本。
//
// 把时刻显式化而不是让 time.Now() 藏在内部，有两个消费方非它不可：Layer 4 的单测
// （否则只能测「此刻」，跨午夜和工作日这些分支永远测不到），以及模型广场的
// 「另一时段价」展示。
func ResolveGroupRatioAt(userGroup, usingGroup, modelName string, at time.Time) RatioResolution {
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

	// 记在 Layer 4 之前：它是「没有时段规则时会走的那个倍率」。
	res.NormalRatio = res.Final

	// Layer 4：命中生效时段则**乘在常规价上**（叠乘），不再取代模型折扣。
	//
	// 语义是「这一段时间的价目 = 常规价 × 时段系数」。不取代的理由见
	// docs/time-price-axis-design.md §4.1/§4.8：
	//   · 取代会把 Layer 2 的折扣连同 override 锁价一起吃掉（override 2.2 会被算成
	//     Base × 时段值），运营配的数与实扣完全脱钩，而且页面上看不出来；
	//   · 取代还要求管理员把「模型折扣 × 时段折扣」自己心算成一个系数（0.56），
	//     一旦模型折扣变了时段值不会跟随——现网就踩过：0.45 被算出 0.5（§10.1）。
	// 叠乘之后 <1 一定更便宜、>1 一定更贵，成为可测的不变量。
	//
	// Layer 2 的命中痕迹**保留**：它确实生效了，日志里 group_base_ratio × 规则值 ×
	// 时段系数 必须能反算出 group_ratio（service/log_info_generate.go 的不变式）。
	if key, win, rule, ok := pickTimeRule(usingGroup, modelName, at); ok {
		res.TimeWindow = key
		res.TimeLabel = win.Label
		res.TimeValue = rule.Value
		res.TimeOutputFactor = rule.EffectiveOutputFactor()
		res.Final = res.NormalRatio * rule.Value
	}

	res.AfterModelRule = res.Final

	// Layer 3 放在 Layer 4 之后：它描述的是「这批用户打几折」，与走的是常规价还是
	// 空闲时段价无关。放在前面会被 Layer 4 的取代吃掉——企业客户的档位优惠每天夜里
	// 静默消失 8 小时，而日志上只有一个最终倍率，反算不出是哪一层拍的板。
	if pattern, rule, ok := pickUserModelRule(userGroup, modelName); ok {
		res.UserRuleMatch = pattern
		res.UserRuleValue = rule.Value
		res.Final = res.Final * rule.Value
	}

	return res
}
