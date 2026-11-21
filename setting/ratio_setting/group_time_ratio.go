package ratio_setting

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// 时段折扣（解析链 Layer 4）。
//
// 解决的问题：Layer 0/1/2 描述的是「这条供应链上这个模型多少钱」，但自建 GPUStack
// 的成本本身带时间维度——夜里 GPU 空闲，边际成本远低于白天。没有这一层就只能按
// 白天的成本定一个全天价，既拿不到夜间增量，也没法把负载从峰值挪走。
//
// 索引是**模型**（v1 决定，见 docs/time-price-axis-design.md §3.1）：目录价全网一份，
// 所以时段价也只有一个归属；「某条线路单独便宜」会把成本差异泄漏成价目差异。
//
// 叠加语义是**叠乘**：命中时段时 终值 = 常规价 × 时段系数。不取代 Layer 2，因为取代会把
// 模型折扣与 override 锁价一起吃掉（见 §4.1/§4.8）。系数因此允许 >1（紧缺时段加价）。
// 它排在 Layer 3 之前，用户档折扣仍然会乘上去。
//
// 为什么 windows 和 rules 放在同一个 option key 里：规则只存模板名，保存时必须
// 校验「引用的模板存在」。option 是按 key 逐个 PUT 的，拆成两个 key 就既做不到这个
// 校验，也做不到原子保存——新建模板成功、引用它的规则保存失败，会留下一批悬空引用。

const defaultTimeRatioZone = "Asia/Shanghai"

// maxTimePriceFactor 是时段价系数的上限。
//
// 系数乘在常规价上：<1 空闲时段更便宜，>1 紧缺时段更贵（现网就有 0.5/0.45 = 1.1111 的用例）。
// 允许加价是本次改造的核心之一——不允许的话，「紧缺时段贵一点」这个需求根本表达不出来。
// 上限取 20 只是防手误：把 100 打成 20 与打成 100 的差别不大，但至少挡住「多打一个 0 让整批
// 模型在某个时段涨十倍」这种事故。
const maxTimePriceFactor = 20.0

// globalTimeRatioScope 是「全网生效」的规则作用域键。
//
// v1 的模型：时段价是价目的一部分，全网一份，所以规则按**模型**索引、不再按线路。
// 但存量配置是按线路（"default"）存的，形状不能一夜之间改掉。约定因此是：
// 解析时先查 rules[线路]（存量兼容），查不到再查 rules["*"]（全网）。
// 把规则搬到 "*" 下、删掉按线路的那份，就等于完成摊平；反过来想给某条线路留例外，
// 也仍然可以单独配——但**界面不提供**这个入口（设计见 docs/time-price-axis-design.md §3.1）。
//
// 用保留键而不是改数据结构，是为了让这次切换保持**纯加法**：老数据原样可用，
// 摊平只改数据、不改代码路径，出问题时把 key 搬回去就能回滚。
const globalTimeRatioScope = "*"

// TimeWindow 是一个命名时段模板，被多条规则引用。
//
// Start/End 为 "HH:MM"。Start > End 表示跨午夜（如 22:00→06:00）。
// Days 为空表示每天；否则是 time.Weekday 取值（0=周日）的集合。
// TimeSegment 是时段里的**一个连续区间**：「工作日 09:00-12:00」就是一段。
type TimeSegment struct {
	Start string `json:"start"`
	End   string `json:"end"`
	Days  []int  `json:"days,omitempty"`
}

// TimeWindow 是一个**时段**（官方口径里的「高峰时段」「空闲时段」）。
//
// 一个时段可以含**多个不连续区间**：官方的高峰时段 = 工作日 09-12 + 工作日 14-18，
// 就是两段。所以 Segments 是权威字段，Start/End/Days 只作为单段的历史格式读入时归一。
//
// ExcludeOf 表达**补集**：「本时段 = 除 ExcludeOf 指向的时段以外的所有时间」。
// 这正是「空闲时段」的定义（高峰之外），也避免了让运营手工铺 3 段还容易漏。
// 它只在存储/编辑层存在：解析前会展开成显式 Segments（compileWindows），
// 所以 pickTimeRule / 视图这些热路径拿到的永远是普通多段，没有补集分支。
type TimeWindow struct {
	Label string `json:"label,omitempty"`
	// 历史格式：单段。读入时归一到 Segments；写回不再产生。
	Start string `json:"start,omitempty"`
	End   string `json:"end,omitempty"`
	Days  []int  `json:"days,omitempty"`
	TZ    string `json:"tz,omitempty"`
	// Segments 是该时段的全部区间（不连续时段就放多段）。
	Segments []TimeSegment `json:"segments,omitempty"`
	// ExcludeOf 非空表示「其余时间」：本时段 = 目标时段之外的全部时间。
	ExcludeOf string `json:"exclude_of,omitempty"`
}

// normalizedSegments 返回该时段的显式区间列表（把历史单段格式也算进来）。
func (w TimeWindow) normalizedSegments() []TimeSegment {
	if len(w.Segments) > 0 {
		return w.Segments
	}
	if strings.TrimSpace(w.Start) == "" || strings.TrimSpace(w.End) == "" {
		return nil
	}
	return []TimeSegment{{Start: w.Start, End: w.End, Days: w.Days}}
}

// ActiveAtSegment 判断单个区间在 at 时刻是否生效（跨午夜按起始日判定）。
func segmentActive(seg TimeSegment, loc *time.Location, at time.Time) bool {
	startMin, err := parseClock(seg.Start)
	if err != nil {
		return false
	}
	endMin, err := parseClockAllowEndOfDay(seg.End)
	if err != nil {
		return false
	}
	local := at.In(loc)
	cur := local.Hour()*60 + local.Minute()
	dayOK := func(d time.Weekday) bool {
		if len(seg.Days) == 0 {
			return true
		}
		for _, v := range seg.Days {
			if v == int(d) {
				return true
			}
		}
		return false
	}
	if startMin < endMin {
		return cur >= startMin && cur < endMin && dayOK(local.Weekday())
	}
	// 跨午夜
	if cur >= startMin {
		return dayOK(local.Weekday())
	}
	if cur < endMin {
		return dayOK(local.AddDate(0, 0, -1).Weekday())
	}
	return false
}

// TimeRule 是「某模型在某时段的价目系数」。Window 指向 TimeWindow 的键。
//
// Value 只有一个语义：**乘在常规价上**。小于 1 是空闲时段更便宜，大于 1 是紧缺时段更贵。
// 它不再是「相对 Base 的绝对系数、命中即取代模型定价」——那个语义会把模型折扣与
// override 锁价一起吃掉（design §4.1/§4.8）。
//
// ⚠️ 升级注意：这个字段的**含义变过，而存储形状没变**，所以旧值直接沿用是错的。
// 旧语义下 0.5 表示「终值 = Base × 0.5」；新语义下它表示「终值 = 常规价 × 0.5」，
// 在有模型折扣（如 0.45）的模型上会从 0.5 掉到 0.225 —— 少收 55%，且不报错。
// 因此上线必须按 docs/time-price-axis-design.md §8.1 的次序：
// **先清空时段规则 → 发版 → 按新语义重新配置**（清空后新旧码同价，滚动窗口才安全）。
//
// 没有做「新旧双语义字段」那种 expand/contract：为一个只影响少数规则的迁移，
// 留两套解析、两套校验、两套测试的代价更大。代价就是上面这条纪律必须守住。
type TimeRule struct {
	Window string `json:"window"`
	// Prices 是**分量 → 该时段绝对价目**的稀疏表（¥/1M，按次价是 ¥/次）。
	// 键取自 timeFactorComponents。
	//
	// 为什么存绝对价而不是系数：目录价是对外公布的**价目表**，运营填 1 就是 1。
	// 存系数会让「改了常规价 → 所有时段价自己变」，运营明明没动它却变了。
	// 换算成系数发生在解析时（EffectiveFactors），计费/展示链路拿到的仍是系数，
	// 所以那两层的代码不受影响。
	//
	// 语义：**没出现在表里的分量不随时段变**（保持常规价）。
	Prices map[string]float64 `json:"prices,omitempty"`
	// Factors 是历史格式（相对系数），只在读入时归一，写回不再产生。
	Factors map[string]float64 `json:"factors,omitempty"`
	// Value / OutputFactor 是历史格式（整价系数 / 输出侧系数），只在读入时归一，
	// 归一后一律走 Factors，写回时不再产生这两个字段。
	Value        float64 `json:"value,omitempty"`
	OutputFactor float64 `json:"output_factor,omitempty"`
	Remark       string  `json:"remark,omitempty"` // 运营备注，同 ModelRatioRule.Remark
}

// timeFactorComponents 是允许按时段变价的分量白名单。
//
// 覆盖「按量计费 + 按次计费」的全部价目分量：
//   - input         ModelRatio（输入价，唯一绝对值基准）
//   - output        CompletionRatio（输出 = 输入 × C）
//   - cache         CacheRatio（缓存命中）
//   - create_cache  CreateCacheRatio（缓存创建）
//   - image         ImageRatio（图片输入）
//   - audio_input   AudioRatio（音频输入）
//   - audio_output  AudioCompletionRatio（音频输出 = 音频输入 × r）
//   - per_call      ModelPrice（按次价）
//
// 明确**不**支持的两类：tiered_expr 表达式（它用 hour() 自己表达时间维度，
// 与时段价互斥）与视频矩阵（绝对 ¥/秒、按分辨率分格，另属一块）。
var timeFactorComponents = map[string]struct{}{
	"input":        {},
	"output":       {},
	"cache":        {},
	"create_cache": {},
	"image":        {},
	"audio_input":  {},
	"audio_output": {},
	"per_call":     {},
}

// effectiveFactor 取某分量的时段系数（未配置时 = 1，即不随时段变）。
func effectiveFactor(rule TimeRule, modelName, component string) float64 {
	if f := rule.EffectiveFactors(modelName); f != nil {
		if v, ok := f[component]; ok && v > 0 {
			return v
		}
	}
	return 1
}

// TimeFactorComponents 返回分量白名单（供校验/界面提示）。
func TimeFactorComponents() []string {
	out := make([]string, 0, len(timeFactorComponents))
	for k := range timeFactorComponents {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// componentBasePrice 返回某分量的**常规目录价**（¥）。
//
// 这是 prices → factors 换算的分母，也是面板展示「常规目录价」的同一口径。
func componentBasePrice(modelName, component string) float64 {
	modelRatio, ok, _ := GetModelRatio(modelName)
	if !ok || modelRatio <= 0 {
		if component == "per_call" {
			if price, okPrice := GetModelPrice(modelName, false); okPrice && price > 0 {
				return price
			}
		}
		return 0
	}
	switch component {
	case "input":
		return modelRatio
	case "output":
		return modelRatio * GetCompletionRatio(modelName)
	case "cache":
		if r, okCache := GetCacheRatio(modelName); okCache {
			return modelRatio * r
		}
	case "create_cache":
		if r, okCreate := GetCreateCacheRatio(modelName); okCreate {
			return modelRatio * r
		}
	case "image":
		if r, okImage := GetImageRatio(modelName); okImage {
			return modelRatio * r
		}
	case "audio_input":
		return modelRatio * GetAudioRatio(modelName)
	case "audio_output":
		return modelRatio * GetAudioRatio(modelName) * GetAudioCompletionRatio(modelName)
	case "per_call":
		if price, okPrice := GetModelPrice(modelName, false); okPrice && price > 0 {
			return price
		}
	}
	return 0
}

// EffectiveFactors 返回归一后的**分量系数表**（不含"未配置"的分量）。
//
// 三种输入格式，统一折算成系数（计费与展示只认系数）：
//   - Prices（当前格式）：系数 = 该时段价 ÷ 常规价；
//   - Factors（上一版，相对系数）：原样使用；
//   - Value / OutputFactor（最初版本，整价系数）：展开到全部分量。
func (r TimeRule) EffectiveFactors(modelName string) map[string]float64 {
	if len(r.Prices) > 0 {
		out := make(map[string]float64, len(r.Prices))
		for comp, price := range r.Prices {
			base := componentBasePrice(modelName, comp)
			if base > 0 && price > 0 {
				out[comp] = price / base
			}
		}
		return out
	}
	if len(r.Factors) > 0 {
		out := make(map[string]float64, len(r.Factors))
		for k, v := range r.Factors {
			if v > 0 {
				out[k] = v
			}
		}
		if r.Value > 0 {
			if _, ok := out["input"]; !ok {
				out["input"] = r.Value
			}
		}
		if r.OutputFactor > 0 {
			if _, ok := out["output"]; !ok {
				out["output"] = r.OutputFactor
			}
		}
		return out
	}
	if r.Value <= 0 && r.OutputFactor <= 0 {
		return nil
	}
	out := make(map[string]float64, len(timeFactorComponents))
	for k := range timeFactorComponents {
		out[k] = r.Value
	}
	if r.OutputFactor > 0 {
		out["output"] = r.OutputFactor
	}
	return out
}

// InputFactor 返回输入侧系数（未配置时为 1）。整单倍率乘的就是它。
func (r TimeRule) InputFactor(modelName string) float64 {
	if f := r.EffectiveFactors(modelName); f != nil {
		if v, ok := f["input"]; ok && v > 0 {
			return v
		}
	}
	return 1
}

// GroupTimeRatioConfig: windows 是全局共用的模板表，rules 是
// 使用分组 -> 模型模式串 -> 时段规则列表。模式串语义与 GroupModelRatio 完全一致
// （精确名 / 尾部通配），复用同一套 pickRuleFrom 匹配。
type GroupTimeRatioConfig struct {
	Windows map[string]TimeWindow            `json:"windows"`
	Rules   map[string]map[string][]TimeRule `json:"rules"`
}

var (
	groupTimeRatioMu sync.RWMutex
	groupTimeRatio   = GroupTimeRatioConfig{
		Windows: map[string]TimeWindow{},
		Rules:   map[string]map[string][]TimeRule{},
	}
	// compiledWindows 是 Windows 的**展开版**（补集已在加载时解开）。
	// 解析/展示/校验都读它，所以热路径上不存在"补集"这个分支。
	compiledWindows = map[string]TimeWindow{}
)

func GroupTimeRatio2JSONString() string {
	groupTimeRatioMu.RLock()
	defer groupTimeRatioMu.RUnlock()
	data, err := common.Marshal(groupTimeRatio)
	if err != nil {
		return "{}"
	}
	return string(data)
}

// UpdateGroupTimeRatioByJSONString 整体替换配置。
//
// 与 types.RWMap 的 LoadFromJsonString 一样是**整表原子替换**而不是逐 key merge：
// 删一条规则的表达方式就是新配置里没有它，merge 语义下删不掉。
func UpdateGroupTimeRatioByJSONString(jsonStr string) error {
	cfg, err := parseGroupTimeRatio(jsonStr)
	if err != nil {
		return err
	}
	compiled, err := compileWindows(cfg.Windows)
	if err != nil {
		return err
	}
	groupTimeRatioMu.Lock()
	defer groupTimeRatioMu.Unlock()
	groupTimeRatio = cfg
	compiledWindows = compiled
	return nil
}

func GetGroupTimeRatioCopy() GroupTimeRatioConfig {
	groupTimeRatioMu.RLock()
	defer groupTimeRatioMu.RUnlock()
	out := GroupTimeRatioConfig{
		Windows: make(map[string]TimeWindow, len(groupTimeRatio.Windows)),
		Rules:   make(map[string]map[string][]TimeRule, len(groupTimeRatio.Rules)),
	}
	for k, v := range groupTimeRatio.Windows {
		out.Windows[k] = v
	}
	for g, rules := range groupTimeRatio.Rules {
		copied := make(map[string][]TimeRule, len(rules))
		for pattern, list := range rules {
			copied[pattern] = append([]TimeRule(nil), list...)
		}
		out.Rules[g] = copied
	}
	return out
}

// HasGroupTimeRules 报告某使用分组是否配了任何时段规则。
//
// 导出是给 controller/pricing.go 用的，理由与 HasUserGroupModelRules 完全相同：
// 展开终值表时要决定跳不跳过某个分组，漏掉「只配了时段规则」的分组会让模型广场
// 显示原价而实扣打折价——显示价与实扣不一致里最难发现的那种。
func HasGroupTimeRules(group string) bool {
	groupTimeRatioMu.RLock()
	defer groupTimeRatioMu.RUnlock()
	// 全网规则对每条线路都算「有线规则」：漏掉这一条，只配了 "*" 时会按「没有时段规则」
	// 跳过整个线路，模型广场显示原价而实扣已含时段系数。
	return len(groupTimeRatio.Rules[group]) > 0 || len(groupTimeRatio.Rules[globalTimeRatioScope]) > 0
}

// TimeRulePatterns 解析一份 GroupTimeRatio JSON，返回其中出现的全部模型模式串（跨作用域去重）。
//
// 保存前用它做「时段价 × 计费表达式时间函数」的互斥检查 —— 检查必须针对**将要保存的**内容，
// 而不是当前内存里的旧配置。
func TimeRulePatterns(jsonStr string) ([]string, error) {
	cfg, err := parseGroupTimeRatio(jsonStr)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(cfg.Rules))
	out := make([]string, 0, 8)
	for _, rules := range cfg.Rules {
		for pattern := range rules {
			if seen[pattern] {
				continue
			}
			seen[pattern] = true
			out = append(out, pattern)
		}
	}
	sort.Strings(out)
	return out, nil
}

// HasTimeRulesForModel 报告当前生效的时段价是否会命中该模型（跨全部作用域）。
//
// 反方向互斥用：保存计费表达式时，若它引用了时间函数、而当前时段价已覆盖该模型，就拒绝。
func HasTimeRulesForModel(modelName string) bool {
	if modelName == "" {
		return false
	}
	groupTimeRatioMu.RLock()
	defer groupTimeRatioMu.RUnlock()
	for _, rules := range groupTimeRatio.Rules {
		if _, _, found := pickRuleFrom(rules, modelName); found {
			return true
		}
	}
	return false
}

// matchedTimeRulesLocked 取某 (线路, 模型) 命中的时段规则列表与模板表副本。
//
// 先查按线路存的规则（存量兼容），没有再查全网规则 "*"。必须是「先线路后全网」的
// 整组取代而不是合并：合并会让同一条规则在两层各出现一次，窗口重叠校验根本看不出，
// 而 pickTimeRule 取最小值的方向又会让结果偏向一侧，没人能解释那个价怎么来的。
//
// 调用方必须持读锁。
func matchedTimeRulesLocked(group, modelName string) ([]TimeRule, map[string]TimeWindow) {
	lookup := func(scope string) ([]TimeRule, bool) {
		rules, ok := groupTimeRatio.Rules[scope]
		if !ok {
			return nil, false
		}
		_, matched, found := pickRuleFrom(rules, modelName)
		if !found {
			return nil, false
		}
		return matched, true
	}

	var matched []TimeRule
	if list, ok := lookup(group); ok {
		matched = list
	} else if list, ok := lookup(globalTimeRatioScope); ok {
		matched = list
	}
	if len(matched) == 0 {
		return nil, nil
	}
	windows := make(map[string]TimeWindow, len(compiledWindows))
	for k, v := range compiledWindows {
		windows[k] = v
	}
	return append([]TimeRule(nil), matched...), windows
}

func parseGroupTimeRatio(jsonStr string) (GroupTimeRatioConfig, error) {
	cfg := GroupTimeRatioConfig{}
	if strings.TrimSpace(jsonStr) == "" {
		cfg.Windows = map[string]TimeWindow{}
		cfg.Rules = map[string]map[string][]TimeRule{}
		return cfg, nil
	}
	if err := common.Unmarshal([]byte(jsonStr), &cfg); err != nil {
		return cfg, err
	}
	if cfg.Windows == nil {
		cfg.Windows = map[string]TimeWindow{}
	}
	if cfg.Rules == nil {
		cfg.Rules = map[string]map[string][]TimeRule{}
	}
	return cfg, nil
}

// CheckGroupTimeRatio 校验时段折扣配置。保存前调用。
func CheckGroupTimeRatio(jsonStr string) error {
	cfg, err := parseGroupTimeRatio(jsonStr)
	if err != nil {
		return err
	}

	for key, win := range cfg.Windows {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("time window has an empty key")
		}
		if strings.TrimSpace(win.ExcludeOf) != "" {
			// 补集时段：自身不带区间，只校验引用
			ref, ok := cfg.Windows[win.ExcludeOf]
			if !ok {
				return fmt.Errorf("time window %q excludes unknown window %q", key, win.ExcludeOf)
			}
			if strings.TrimSpace(ref.ExcludeOf) != "" {
				return fmt.Errorf("time window %q excludes %q, which is itself a complement window", key, win.ExcludeOf)
			}
			if len(ref.normalizedSegments()) == 0 {
				return fmt.Errorf("time window %q excludes %q, which has no segments", key, win.ExcludeOf)
			}
			// 补集时段自己**没有**区间（区间是"除参照时段之外"算出来的），
			// 所以这里不能走 validateSegments —— 那会报 "has no time segment"。
			if err := win.validateTZ(key); err != nil {
				return err
			}
			continue
		}
		if err := win.validateSegments(key); err != nil {
			return err
		}
	}

	for group, rules := range cfg.Rules {
		for pattern, list := range rules {
			if strings.TrimSpace(pattern) == "" {
				return fmt.Errorf("group %s has an empty model pattern in time rules", group)
			}
			// 只支持尾部通配，与 CheckGroupModelRatio 同一约定：正则写错不报错，
			// 只会静默算错价。
			if idx := strings.Index(pattern, "*"); idx != -1 && idx != len(pattern)-1 {
				return fmt.Errorf("group %s time rule pattern %q: '*' is only supported as a trailing wildcard", group, pattern)
			}
			if len(list) == 0 {
				return fmt.Errorf("group %s time rule %q has no window", group, pattern)
			}
			for _, rule := range list {
				if _, ok := cfg.Windows[rule.Window]; !ok {
					// 悬空引用必须拦住：规则会静默不生效，而管理员以为打了折。
					return fmt.Errorf("group %s time rule %q references unknown window %q", group, pattern, rule.Window)
				}
				// 系数乘在常规价上，所以 >1 是「紧缺时段加价」，合法且必要（见 maxTimePriceFactor）。
				// 仍不允许 0：免费用 Layer 2 的 override 表达，误打一个 0 会让整批模型在某个时段白送。
				// 旧格式 value（整价系数）若存在，同样要校验区间。
				if rule.Value < 0 || rule.Value > maxTimePriceFactor {
					return fmt.Errorf("group %s time rule %q window %q: value must be in (0, %g], got %g", group, pattern, rule.Window, maxTimePriceFactor, rule.Value)
				}
				if rule.OutputFactor < 0 || rule.OutputFactor > maxTimePriceFactor {
					return fmt.Errorf("group %s time rule %q window %q: output_factor must be in (0, %g], got %g", group, pattern, rule.Window, maxTimePriceFactor, rule.OutputFactor)
				}
				// 分量白名单 + 绝对价校验。价格是运营直接填的 ¥，所以校验的是"价"；
				// "折算系数是否离谱"由价格反推（超过上限说明数量级填错了）。
				for comp, price := range rule.Prices {
					if _, ok := timeFactorComponents[comp]; !ok {
						return fmt.Errorf("group %s time rule %q window %q: unknown price component %q (allowed: %v)", group, pattern, rule.Window, comp, TimeFactorComponents())
					}
					if price <= 0 {
						return fmt.Errorf("group %s time rule %q window %q component %q: price must be > 0, got %g", group, pattern, rule.Window, comp, price)
					}
					// 通配模式串没有单一常规价，无法反推区间，只校验价格本身
					if base := componentBasePrice(pattern, comp); base > 0 && price/base > maxTimePriceFactor {
						return fmt.Errorf("group %s time rule %q window %q component %q: 该时段价 %g 超过常规价 %g 的 %g 倍", group, pattern, rule.Window, comp, price, base, maxTimePriceFactor)
					}
				}
				for comp, factor := range rule.Factors {
					if _, ok := timeFactorComponents[comp]; !ok {
						return fmt.Errorf("group %s time rule %q window %q: unknown price component %q (allowed: %v)", group, pattern, rule.Window, comp, TimeFactorComponents())
					}
					if factor <= 0 || factor > maxTimePriceFactor {
						return fmt.Errorf("group %s time rule %q window %q component %q: factor must be in (0, %g], got %g", group, pattern, rule.Window, comp, maxTimePriceFactor, factor)
					}
				}
				if len(rule.Prices) == 0 && len(rule.Factors) == 0 && rule.Value <= 0 {
					return fmt.Errorf("group %s time rule %q window %q: 至少要配一个分量价目", group, pattern, rule.Window)
				}
			}
			seen := make(map[string]bool, len(list))
			for _, rule := range list {
				if seen[rule.Window] {
					return fmt.Errorf("group %s time rule %q: window %q is configured twice", group, pattern, rule.Window)
				}
				seen[rule.Window] = true
			}
			compiled, cErr := compileWindows(cfg.Windows)
			if cErr != nil {
				return cErr
			}
			if err := checkWindowOverlap(compiled, cfg.Windows, group, pattern, list); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkWindowOverlap 拒绝同一条规则下时间窗互相重叠的配置。
//
// 重叠本身有确定的运行时解（取最小值，见 pickTimeRule），但那是防手改 JSON 的兜底。
// 正常配置路径拦住它，是因为重叠的两档折扣里必有一档永远不生效——那是一条管理员
// 以为配上了、实际从未发生的折扣。
func checkWindowOverlap(windows, raw map[string]TimeWindow, group, pattern string, list []TimeRule) error {
	type span struct {
		day        int // time.Weekday
		start, end int // 分钟，[start, end)
		window     string
	}
	var spans []span
	for _, rule := range list {
		win := windows[rule.Window]
		for _, seg := range win.normalizedSegments() {
			startMin, err := parseClock(seg.Start)
			if err != nil {
				continue // 已被 validateSegments 拦过，这里只为不 panic
			}
			endMin, err := parseClockAllowEndOfDay(seg.End)
			if err != nil {
				continue
			}
			days := seg.Days
			if len(days) == 0 {
				days = []int{0, 1, 2, 3, 4, 5, 6}
			}
			for _, day := range days {
				if startMin < endMin {
					spans = append(spans, span{day, startMin, endMin, rule.Window})
					continue
				}
				// 跨午夜：拆成起始日的 [start, 1440) 与次日的 [0, end)
				spans = append(spans, span{day, startMin, 24 * 60, rule.Window})
				spans = append(spans, span{(day + 1) % 7, 0, endMin, rule.Window})
			}
		}
	}
	for i := 0; i < len(spans); i++ {
		for j := i + 1; j < len(spans); j++ {
			a, b := spans[i], spans[j]
			if a.day != b.day || a.window == b.window {
				continue
			}
			if a.start < b.end && b.start < a.end {
				// 「其余时间」是补集，天然覆盖其它所有时段：一旦同时存在就必然重叠。
				// 这条最容易撞上，所以单独给一句能照着做的提示。
				aIsComp := strings.TrimSpace(raw[a.window].ExcludeOf) != ""
				bIsComp := strings.TrimSpace(raw[b.window].ExcludeOf) != ""
				if aIsComp || bIsComp {
					comp, other := a.window, b.window
					if bIsComp {
						comp, other = b.window, a.window
					}
					return fmt.Errorf("group %s time rule %q: window %q is the complement (rest-of-time) and covers %q; remove the other windows or narrow the complement", group, pattern, comp, other)
				}
				return fmt.Errorf("group %s time rule %q: windows %q and %q overlap", group, pattern, a.window, b.window)
			}
		}
	}
	return nil
}

// validateSegments 校验时段自身的区间列表（旧单段格式也会被归一后校验）。
func (w TimeWindow) validateSegments(key string) error {
	segs := w.normalizedSegments()
	if len(segs) == 0 {
		return fmt.Errorf("time window %q has no time segment", key)
	}
	for _, seg := range segs {
		startMin, err := parseClock(seg.Start)
		if err != nil {
			return fmt.Errorf("time window %q start: %w", key, err)
		}
		endMin, err := parseClock(seg.End)
		if err != nil {
			return fmt.Errorf("time window %q end: %w", key, err)
		}
		if startMin == endMin {
			// 「起止相同」的语义要么是全天、要么是空，两种读法都说得通，
			// 配错了不报错只会静默算错价。要全天就别配时段规则。
			return fmt.Errorf("time window %q: start and end must differ", key)
		}
		for _, d := range seg.Days {
			if d < 0 || d > 6 {
				return fmt.Errorf("time window %q: weekday must be 0-6 (0=Sunday), got %d", key, d)
			}
		}
	}
	// 同一时段内部的两段不能重叠（重叠意味着同一天同一分钟被算了两次）
	if err := detectSegmentOverlap(key, segs); err != nil {
		return err
	}
	return w.validateTZ(key)
}

// validateTZ 校验时区可解析。普通时段与补集时段都要查。
func (w TimeWindow) validateTZ(key string) error {
	if w.TZ != "" {
		if _, err := time.LoadLocation(w.TZ); err != nil {
			return fmt.Errorf("time window %q: unknown timezone %q", key, w.TZ)
		}
	}
	return nil
}

func detectSegmentOverlap(key string, segs []TimeSegment) error {
	type span struct{ day, start, end, idx int }
	var spans []span
	for i, seg := range segs {
		startMin, errS := parseClock(seg.Start)
		endMin, errE := parseClockAllowEndOfDay(seg.End)
		if errS != nil || errE != nil {
			continue
		}
		days := seg.Days
		if len(days) == 0 {
			days = []int{0, 1, 2, 3, 4, 5, 6}
		}
		for _, day := range days {
			if startMin < endMin {
				spans = append(spans, span{day, startMin, endMin, i})
				continue
			}
			spans = append(spans, span{day, startMin, 24 * 60, i})
			spans = append(spans, span{(day + 1) % 7, 0, endMin, i})
		}
	}
	for i := 0; i < len(spans); i++ {
		for j := i + 1; j < len(spans); j++ {
			a, b := spans[i], spans[j]
			if a.day != b.day || a.idx == b.idx {
				continue
			}
			if a.start < b.end && b.start < a.end {
				return fmt.Errorf("time window %q: segment %d and %d overlap", key, a.idx+1, b.idx+1)
			}
		}
	}
	return nil
}

// parseClock 解析 "HH:MM" 为当日分钟数。
// parseClockAllowEndOfDay 与 parseClock 相同，但额外接受 "24:00" 表示"当天结束"。
//
// 只给**编译产物**用（补集展开出来的整天空闲就是 00:00-24:00）。用户输入仍走
// parseClock，24:00 会被拒绝——不给运营留一个"看起来对、其实越界"的写法。
func parseClockAllowEndOfDay(s string) (int, error) {
	if strings.TrimSpace(s) == "24:00" {
		return 24 * 60, nil
	}
	return parseClock(s)
}

func parseClock(s string) (int, error) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	// 必须补零。Atoi 本身接受 "9"，但展示侧多处按定长 "HH:MM" 处理，放行 "9:00"
	// 会让后端与前端对同一个值得出不同结论（"9:00" > "18:00" 按字符比较为真，
	// 前端会把它渲染成跨午夜）。拒绝而不是静默补零——改写用户输入是另一种意外。
	// 与管理端编辑器的 CLOCK_RE 同一约定。
	if len(parts) != 2 || len(parts[0]) != 2 || len(parts[1]) != 2 {
		return 0, fmt.Errorf("expect HH:MM, got %q", s)
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil || h < 0 || h > 23 {
		return 0, fmt.Errorf("invalid hour in %q", s)
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil || m < 0 || m > 59 {
		return 0, fmt.Errorf("invalid minute in %q", s)
	}
	return h*60 + m, nil
}

// effectiveDays 返回窗口生效的星期集合；Days 为空表示每天。
func (w TimeWindow) effectiveDays() []int {
	if len(w.Days) == 0 {
		return []int{0, 1, 2, 3, 4, 5, 6}
	}
	return w.Days
}

func (w TimeWindow) hasDay(day time.Weekday) bool {
	if len(w.Days) == 0 {
		return true
	}
	for _, d := range w.Days {
		if d == int(day) {
			return true
		}
	}
	return false
}

// 时区解析结果缓存。LoadLocation 每次都会读文件，而倍率解析在每个请求的热路径上。
var (
	locCacheMu sync.RWMutex
	locCache   = map[string]*time.Location{}
)

func (w TimeWindow) location() *time.Location {
	name := strings.TrimSpace(w.TZ)
	if name == "" {
		name = defaultTimeRatioZone
	}
	locCacheMu.RLock()
	loc, ok := locCache[name]
	locCacheMu.RUnlock()
	if ok {
		return loc
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		// 容器里常常没有 tzdata。回落到固定 +8，与 service/reconcile_helpers.go
		// 的处理一致——宁可用一个确定的偏移，也不要静默退回 UTC 把夜间折扣错开 8 小时。
		loc = time.FixedZone("CST", 8*3600)
	}
	locCacheMu.Lock()
	locCache[name] = loc
	locCacheMu.Unlock()
	return loc
}

// ActiveAt 报告窗口在 at 时刻是否生效。
//
// 跨午夜窗口（start > end）按**起始日**判定生效日：「周五 22:00→06:00」整段都算
// 周五，周六凌晨 02:00 仍然生效。另一种读法（按当前日判定）会让跨午夜 + 工作日
// 的组合在周六 00:00 突然断档，而配置上完全看不出来。
func (w TimeWindow) ActiveAt(at time.Time) bool {
	loc := w.location()
	for _, seg := range w.normalizedSegments() {
		if segmentActive(seg, loc, at) {
			return true
		}
	}
	return false
}

// NextBoundary 返回 at 之后窗口生效状态下一次翻转的时刻。
//
// 状态只可能在 start / end 这两个钟点上翻转，所以枚举未来 8 天的候选时刻、取第一个
// 与当前状态不同的即可——不需要逐分钟推进，也不需要为跨午夜和 Days 过滤各写一套
// 边界推导（那正是最容易算错、且错了看不出来的地方）。
func (w TimeWindow) NextBoundary(at time.Time) (time.Time, bool) {
	segs := w.normalizedSegments()
	if len(segs) == 0 {
		return time.Time{}, false
	}
	loc := w.location()
	cur := w.ActiveAt(at)
	local := at.In(loc)
	midnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)

	// 多段时把所有段的起止钟点都当候选：状态只可能在某个段的 start/end 翻转。
	candidates := make([]time.Time, 0, 18*len(segs))
	for _, seg := range segs {
		startMin, err := parseClock(seg.Start)
		if err != nil {
			continue
		}
		endMin, err := parseClockAllowEndOfDay(seg.End)
		if err != nil {
			continue
		}
		for d := 0; d <= 8; d++ {
			base := midnight.AddDate(0, 0, d)
			candidates = append(candidates,
				base.Add(time.Duration(startMin)*time.Minute),
				base.Add(time.Duration(endMin)*time.Minute),
			)
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Before(candidates[j]) })
	for _, c := range candidates {
		if !c.After(at) {
			continue
		}
		if w.ActiveAt(c) != cur {
			return c, true
		}
	}
	return time.Time{}, false
}

// TimeWindowView 是下发给前端的单个时段档位。
type TimeWindowView struct {
	Label string  `json:"label"`
	Start string  `json:"start"`
	End   string  `json:"end"`
	Days  []int   `json:"days,omitempty"`
	TZ    string  `json:"tz,omitempty"`
	Value float64 `json:"value"` // 配置值（取代模型折扣的那个系数）
	// Ratio 是该时段的**最终倍率**，与 group_model_ratio 下发的终值同口径，可以直接
	// 比较。前端要显示「空闲时段 5.6 折」必须用它——光有 Value 拼不出来，而让前端自己
	// 乘一遍分组基础倍率，等于把解析逻辑抄一份到前端。
	Ratio float64 `json:"ratio"`
	// 下面三个是「展示口径」，与计费口径分开但同源：
	//   · InputDirectoryRatio  = 线路基准 × 输入系数   → 该时段的**目录价倍率**（不含折扣）
	//   · OutputDirectoryRatio = 线路基准 × 输出系数
	//   · DiscountRatio        = 终值 ÷ 输入目录倍率   → **固定折扣**，与时刻无关
	// 广场的划线价取目录倍率、角标取 DiscountRatio，客户拿「划线 × 角标」就能算出成交价。
	// 只下发 Ratio 的话，划线价只能取「常规价（含折扣）」，于是看着像折扣在动。
	InputDirectoryRatio  float64 `json:"input_directory_ratio"`
	OutputDirectoryRatio float64 `json:"output_directory_ratio"`
	DiscountRatio        float64 `json:"discount_ratio"`
	// Active 标出此刻生效的那一档。与 pickTimeRule 取同一条（重叠时同样取最小值），
	// 否则展示会标出一个计费上并没有用到的档位。
	//
	// 由后端标而不是让前端按 label 反推：label 互为子串时（「深夜」与「深夜加强」）
	// 字符串匹配会把两档都标成生效，而用户看到的折扣与实扣对不上。
	Active bool `json:"active"`
}

// TimeRatioView 是某 (分组, 模型) 的时段折扣展示数据。
//
// **Active / Until 由后端算好**，前端一个时间判断都不做。客户端时钟和时区都不可信——
// 用户把手机时区改成 UTC，前端自己算就会显示错误的「空闲时段中」，而价格是后端算的，
// 于是标签和价格互相矛盾。
type TimeRatioView struct {
	// Active 表示此刻是否处于**有优惠的**时段，只给角标用。要判断「哪一档是当前
	// 计费档」请读 Windows[i].Active——命中 ×1 的原价档时后者为真而这里为假。
	Active bool `json:"active"`
	// BestRatio 是全天最优的**最终倍率**（不是配置倍率）。用绝对值而非相对系数：
	// 用户不该为了知道自己付多少而去心算 0.8 × 0.7。
	BestRatio float64 `json:"best_ratio"`
	// BestLabel 是 BestRatio 所属档位的名字。角标必须用它，不能硬编码「空闲时段」：
	// 管理员完全可能把**更贵**的高峰档配成唯一的时段规则（常规 0.35、高峰 0.5），
	// 那时硬编码会显示「空闲时段 5折」——标签和数字双双指向错的东西。
	BestLabel string `json:"best_label,omitempty"`
	// NormalRatio 是未命中任何时段时的最终倍率，供详情页补出「其余时段」那一行。
	// 只列配了规则的时段，用户看不到全天覆盖：表里两行 5 折，其余时间是 3.5 折
	// 还是 8 折完全看不出来。
	NormalRatio float64 `json:"normal_ratio"`
	// Label 是此刻所处档位的名字（未命中任何时段时为空）。命中 ×1 的原价档时
	// 它有值而 Active 为假——两者回答的是不同的问题。
	Label   string           `json:"label,omitempty"`
	Until   string           `json:"until,omitempty"` // 当前状态的结束时刻，RFC3339
	Windows []TimeWindowView `json:"windows"`         // 全部档位，供详情页渲染分时价格表

	// 以下是**此刻**的展示口径（与 windows[*] 同定义，取此刻生效的那一档；
	// 未命中任何时段时取常规：目录倍率 = 线路基准，折扣 = 常规价终值 ÷ 线路基准）。
	InputDirectoryRatio  float64 `json:"input_directory_ratio"`
	OutputDirectoryRatio float64 `json:"output_directory_ratio"`
	DiscountRatio        float64 `json:"discount_ratio"`
	// TimeFactors 是此刻生效的**分量系数表**（与 TimeRule.factors 同语义）。
	// 供下发端点算出「每个分量要折多少」，前端据此展示各自的目录价与成交价。
	TimeFactors map[string]float64 `json:"factors,omitempty"`
	// EffectiveCompletionRatio 是**此刻生效的补全倍率** = 基础补全倍率 × 输出系数/输入系数。
	// 前端算输出价必须用它，否则「闲时输入 1/输出 7」这种不等比例价目会被算成 1/4。
	EffectiveCompletionRatio float64 `json:"completion_ratio"`
}

// ResolveTimeRatioView 构建模型广场用的时段折扣展示数据。
//
// 与 pickTimeRule 共用同一套窗口判定，不另写一份——展示口径与计费口径一旦分叉，
// 表现就是「广场标着空闲时段中、实扣却是原价」，而两边都不报错。
// normalRatio 由调用方从同一次 ResolveGroupRatioAt 的结果里取
// （res.NormalRatio * res.UserMultiplier()，已是终值口径），不在这里重算——重算就是
// 第二份解析实现，一旦与计费分叉，表现是广场标着「空闲时段 5 折」而实扣是另一个数，两边都不报错。
func ResolveTimeRatioView(usingGroup, modelName string, at time.Time, res RatioResolution) (TimeRatioView, bool) {
	normalRatio := res.NormalRatio * res.UserMultiplier()
	// 展示口径的两个基准：线路基准（含身份覆盖）与「终值 ÷ 线路基准」= 固定折扣。
	// 折扣与时刻无关，两种语义下都是这个数，所以只算一次。
	base := res.Base
	discountRatio := 0.0
	if base > 0 {
		discountRatio = normalRatio / base
	}
	modelCompletionRatio := GetCompletionRatio(modelName)
	groupTimeRatioMu.RLock()
	list, windows := matchedTimeRulesLocked(usingGroup, modelName)
	groupTimeRatioMu.RUnlock()

	if len(list) == 0 {
		return TimeRatioView{}, false
	}

	view := TimeRatioView{
		NormalRatio: normalRatio,
		Windows:     make([]TimeWindowView, 0, len(list)),
	}
	var until time.Time
	chargedIdx := -1
	bestValue := math.Inf(1)
	bestRatio := math.Inf(1)
	chargedValue := math.Inf(1)
	for _, rule := range list {
		win, ok := windows[rule.Window]
		if !ok {
			continue
		}
		// 该时段的最终倍率，口径与 ResolveGroupRatioAt 的 Layer 4 逐位一致：
		// 系数乘在常规价上（normalRatio 已含用户档乘数）。
		// 用**归一后的分量系数**算，不能用 rule.Value：新格式（prices）规则里
		// Value 恒为 0，直接用会让 ratio / 目录倍率全变 0 —— 广场上那个模型
		// 会显示「免费」、划线价 ¥0。
		inFactor := rule.InputFactor(modelName)
		windowRatio := normalRatio * inFactor
		outFactor := effectiveFactor(rule, modelName, "output")
		view.Windows = append(view.Windows, TimeWindowView{
			Label:                win.displayName(rule.Window),
			Start:                win.Start,
			End:                  win.End,
			Days:                 win.Days,
			TZ:                   win.TZ,
			Value:                inFactor,
			Ratio:                windowRatio,
			InputDirectoryRatio:  base * inFactor,
			OutputDirectoryRatio: base * outFactor,
			DiscountRatio:        discountRatio,
		})
		if inFactor < bestValue {
			bestValue = inFactor
			bestRatio = windowRatio
			view.BestLabel = win.displayName(rule.Window)
		}
		// 此刻计费实际取用的档位：判据必须与 pickTimeRule **逐位一致**（命中且取最小
		// 值，不看是否打折）。此前这里多了一个 rule.Value < 1 的门槛，于是一个 ×1 的
		// 档（校验允许，语义是「这个时段不享受模型折扣，按原价」）生效时，详情表
		// 没有任何窗口行标「进行中」，「其余时段」那行反而被标上——用户看到的
		// 「当前档」是模型折扣价，实扣却是原价。
		//
		// 「是否打折」是另一个问题，见下面的 view.Active。
		if win.ActiveAt(at) && inFactor < chargedValue {
			view.Label = win.displayName(rule.Window)
			chargedValue = inFactor
			chargedIdx = len(view.Windows) - 1
		}
		// Until 取所有档位里最近的一次状态翻转：任一档位翻转都会改变这个模型的价，
		// 只看当前命中的那个档，会在「一档刚结束、另一档紧接着开始」时给出错误的时刻。
		if next, ok := win.NextBoundary(at); ok && (until.IsZero() || next.Before(until)) {
			until = next
		}
	}

	if len(view.Windows) == 0 {
		return TimeRatioView{}, false
	}
	if chargedIdx >= 0 {
		view.Windows[chargedIdx].Active = true
		// view.Active 回答的是「此刻有没有优惠」，只给角标用：命中一个 ×1 的档位
		// 并不是优惠，标一个青色「进行中」会和旁边那个没打折的价格自相矛盾。
		// 它与 Windows[i].Active 是两件事，共用一个字段正是上面那个 bug 的成因。
		view.Active = chargedValue < 1
	}
	// bestRatio 恒有限：走到这里说明至少有一个档位进了 Windows
	view.BestRatio = bestRatio

	// 此刻生效的展示口径：命中某档就用该档的系数，未命中则回到常规（系数 1/1）。
	inFactor, outFactor := 1.0, 1.0
	if chargedIdx >= 0 {
		inFactor = list[chargedIdx].InputFactor(modelName)
		outFactor = effectiveFactor(list[chargedIdx], modelName, "output")
		view.TimeFactors = list[chargedIdx].EffectiveFactors(modelName)
	}
	view.InputDirectoryRatio = base * inFactor
	view.OutputDirectoryRatio = base * outFactor
	view.DiscountRatio = discountRatio
	view.EffectiveCompletionRatio = modelCompletionRatio * (outFactor / inFactor)
	if !until.IsZero() {
		view.Until = until.Format(time.RFC3339)
	}
	return view, true
}

func (w TimeWindow) displayName(key string) string {
	if strings.TrimSpace(w.Label) != "" {
		return w.Label
	}
	return key
}

// pickTimeRule 取 (使用分组, 模型) 在 at 时刻生效的时段规则。
//
// 两步：先用 pickRuleFrom 选出最具体的那条模式串（精确 > 前缀通配，同 Layer 2/3），
// 再在该模式串下的窗口列表里挑当前生效的。
//
// 多个窗口同时生效时取 value 最小的：保存时已经拒绝重叠配置，这里是防手改 JSON
// 绕过校验的兜底——结果确定，且方向对用户有利。
func pickTimeRule(usingGroup, modelName string, at time.Time) (string, TimeWindow, TimeRule, bool) {
	groupTimeRatioMu.RLock()
	list, windows := matchedTimeRulesLocked(usingGroup, modelName)
	groupTimeRatioMu.RUnlock()

	if len(list) == 0 {
		return "", TimeWindow{}, TimeRule{}, false
	}

	var (
		bestKey  string
		bestWin  TimeWindow
		bestRule TimeRule
		found    bool
	)
	for _, rule := range list {
		win, ok := windows[rule.Window]
		if !ok || !win.ActiveAt(at) {
			continue
		}
		// 重叠时取「最便宜」的那一档：比的必须是**归一后的输入系数**。
		// 用 rule.Value 会在新格式（prices）规则上恒为 0，多个模式串的窗口重叠时
		// 就退化成"谁先出现谁生效"——计费跟着错，不只是展示。
		if !found || rule.InputFactor(modelName) < bestRule.InputFactor(modelName) {
			bestKey, bestWin, bestRule, found = rule.Window, win, rule, true
		}
	}
	return bestKey, bestWin, bestRule, found
}

// compileWindows 把配置里的时段**展开成显式多段**，补集（ExcludeOf）在这一步解开。
//
// 为什么在加载期展开而不是在解析期判断：补集只是「编辑时的便利写法」，一旦展开，
// pickTimeRule / 视图 / 重叠校验都只面对普通多段，不需要各自再写一套补集分支
// （那正是最容易出现"展示与计费不一致"的地方）。
func compileWindows(windows map[string]TimeWindow) (map[string]TimeWindow, error) {
	out := make(map[string]TimeWindow, len(windows))
	for key, win := range windows {
		if strings.TrimSpace(win.ExcludeOf) == "" {
			copyWin := win
			copyWin.Segments = win.normalizedSegments()
			copyWin.Start, copyWin.End = "", ""
			copyWin.Days = nil
			out[key] = copyWin
			continue
		}
		ref, ok := windows[win.ExcludeOf]
		if !ok {
			return nil, fmt.Errorf("window %q excludes unknown window %q", key, win.ExcludeOf)
		}
		if strings.TrimSpace(ref.ExcludeOf) != "" {
			return nil, fmt.Errorf("window %q excludes %q, which is itself a complement window", key, win.ExcludeOf)
		}
		segs, err := complementSegments(ref.normalizedSegments())
		if err != nil {
			return nil, fmt.Errorf("window %q: %w", key, err)
		}
		out[key] = TimeWindow{
			Label:    win.Label,
			TZ:       win.TZ,
			Segments: segs,
		}
	}
	return out, nil
}

// complementSegments 计算「这些段之外的全部时间」，结果是显式多段。
//
// 做法：把一周展开成 7×1440 分钟表，标记被覆盖的分钟，再取反后合并成区间。
// 跨午夜段（start > end）按起始日覆盖当天尾段与次日前段——与 ActiveAt 的判定一致。
func complementSegments(segs []TimeSegment) ([]TimeSegment, error) {
	if len(segs) == 0 {
		return nil, fmt.Errorf("complement window must reference a window with segments")
	}
	var covered [7][1440]bool
	for _, seg := range segs {
		startMin, err := parseClock(seg.Start)
		if err != nil {
			return nil, err
		}
		endMin, err := parseClockAllowEndOfDay(seg.End)
		if err != nil {
			return nil, err
		}
		days := seg.Days
		if len(days) == 0 {
			days = []int{0, 1, 2, 3, 4, 5, 6}
		}
		for _, d := range days {
			if d < 0 || d > 6 {
				return nil, fmt.Errorf("invalid weekday %d", d)
			}
			if startMin < endMin {
				for m := startMin; m < endMin; m++ {
					covered[d][m] = true
				}
				continue
			}
			// 跨午夜：当天 [start,1440)，次日 [0,end)
			for m := startMin; m < 1440; m++ {
				covered[d][m] = true
			}
			next := (d + 1) % 7
			for m := 0; m < endMin; m++ {
				covered[next][m] = true
			}
		}
	}

	// 取反后按「每天的空闲区间列表」分组，相同列表的星期合并成一条 segment
	type dayFree struct {
		days []int
		mins []int // 扁平化：[s0,e0,s1,e1,...]
	}
	groups := make([]dayFree, 0, 7)
	for d := 0; d < 7; d++ {
		var mins []int
		m := 0
		for m < 1440 {
			if covered[d][m] {
				m++
				continue
			}
			startMin := m
			for m < 1440 && !covered[d][m] {
				m++
			}
			mins = append(mins, startMin, m)
		}
		matched := false
		for i := range groups {
			if equalInts(groups[i].mins, mins) {
				groups[i].days = append(groups[i].days, d)
				matched = true
				break
			}
		}
		if !matched {
			groups = append(groups, dayFree{days: []int{d}, mins: mins})
		}
	}

	out := make([]TimeSegment, 0, 4)
	for _, g := range groups {
		for i := 0; i+1 < len(g.mins); i += 2 {
			s, e := g.mins[i], g.mins[i+1]
			if s == e {
				continue
			}
			out = append(out, TimeSegment{
				Start: formatClock(s),
				End:   formatClockEnd(e),
				Days:  g.days,
			})
		}
	}
	return out, nil
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func formatClock(minute int) string {
	return fmt.Sprintf("%02d:%02d", minute/60, minute%60)
}

// formatClockEnd 允许 1440 → "24:00"（跨到当天结束）。
func formatClockEnd(minute int) string {
	if minute == 1440 {
		return "24:00"
	}
	return formatClock(minute)
}
