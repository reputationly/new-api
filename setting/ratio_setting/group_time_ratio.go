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
// 为什么按「使用分组」（线路）索引：与 Layer 0/1/2 同轴（成本侧），与 Layer 3
// （按用户档的售价侧）正交。
//
// 叠加语义是**取代**而不是叠乘（见 ResolveGroupRatioAt）：命中时段时用
// base × 时段值 整条替换 Layer 2 的结果，包括 override。「常规 8 折、空闲时段 5.6 折」
// 是两个能直接比较的绝对价；叠乘则要求人先算 0.8 × 0.7 才知道自己付多少。
// 但它排在 Layer 3 之前，用户档折扣仍然会乘上去。
//
// 为什么 windows 和 rules 放在同一个 option key 里：规则只存模板名，保存时必须
// 校验「引用的模板存在」。option 是按 key 逐个 PUT 的，拆成两个 key 就既做不到这个
// 校验，也做不到原子保存——新建模板成功、引用它的规则保存失败，会留下一批悬空引用。

const defaultTimeRatioZone = "Asia/Shanghai"

// TimeWindow 是一个命名时段模板，被多条规则引用。
//
// Start/End 为 "HH:MM"。Start > End 表示跨午夜（如 22:00→06:00）。
// Days 为空表示每天；否则是 time.Weekday 取值（0=周日）的集合。
type TimeWindow struct {
	Label string `json:"label,omitempty"`
	Start string `json:"start"`
	End   string `json:"end"`
	Days  []int  `json:"days,omitempty"`
	TZ    string `json:"tz,omitempty"`
}

// TimeRule 是「某模型在某时段打几折」。Window 指向 TimeWindow 的键。
type TimeRule struct {
	Window string  `json:"window"`
	Value  float64 `json:"value"`
	Remark string  `json:"remark,omitempty"` // 运营备注，同 ModelRatioRule.Remark
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
	groupTimeRatioMu.Lock()
	defer groupTimeRatioMu.Unlock()
	groupTimeRatio = cfg
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
	return len(groupTimeRatio.Rules[group]) > 0
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
		if err := win.validate(key); err != nil {
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
				// 只打折不加价（本次范围）。不允许 0：免费用 Layer 2 的 override 表达，
				// 时段系数是乘数，误打一个 0 会让整批模型在夜间白送，代价不对称。
				if rule.Value <= 0 || rule.Value > 1 {
					return fmt.Errorf("group %s time rule %q window %q: value must be in (0, 1], got %g", group, pattern, rule.Window, rule.Value)
				}
			}
			if err := checkWindowOverlap(cfg.Windows, group, pattern, list); err != nil {
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
func checkWindowOverlap(windows map[string]TimeWindow, group, pattern string, list []TimeRule) error {
	type span struct {
		day        int // time.Weekday
		start, end int // 分钟，[start, end)
		window     string
	}
	var spans []span
	for _, rule := range list {
		win := windows[rule.Window]
		startMin, _ := parseClock(win.Start)
		endMin, _ := parseClock(win.End)
		for _, day := range win.effectiveDays() {
			if startMin < endMin {
				spans = append(spans, span{day, startMin, endMin, rule.Window})
				continue
			}
			// 跨午夜：拆成起始日的 [start, 1440) 与次日的 [0, end)
			spans = append(spans, span{day, startMin, 24 * 60, rule.Window})
			spans = append(spans, span{(day + 1) % 7, 0, endMin, rule.Window})
		}
	}
	for i := 0; i < len(spans); i++ {
		for j := i + 1; j < len(spans); j++ {
			a, b := spans[i], spans[j]
			if a.day != b.day || a.window == b.window {
				continue
			}
			if a.start < b.end && b.start < a.end {
				return fmt.Errorf("group %s time rule %q: windows %q and %q overlap", group, pattern, a.window, b.window)
			}
		}
	}
	return nil
}

func (w TimeWindow) validate(key string) error {
	startMin, err := parseClock(w.Start)
	if err != nil {
		return fmt.Errorf("time window %q start: %w", key, err)
	}
	endMin, err := parseClock(w.End)
	if err != nil {
		return fmt.Errorf("time window %q end: %w", key, err)
	}
	if startMin == endMin {
		// 允许的话语义要么是「全天」要么是「空」，两种读法都说得通，
		// 配错了不会报错只会静默算错价。要全天就别配时段规则。
		return fmt.Errorf("time window %q: start and end must differ", key)
	}
	for _, d := range w.Days {
		if d < 0 || d > 6 {
			return fmt.Errorf("time window %q: weekday must be 0-6 (0=Sunday), got %d", key, d)
		}
	}
	if w.TZ != "" {
		if _, err := time.LoadLocation(w.TZ); err != nil {
			return fmt.Errorf("time window %q: unknown timezone %q", key, w.TZ)
		}
	}
	return nil
}

// parseClock 解析 "HH:MM" 为当日分钟数。
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
	startMin, err := parseClock(w.Start)
	if err != nil {
		return false
	}
	endMin, err := parseClock(w.End)
	if err != nil {
		return false
	}
	local := at.In(w.location())
	cur := local.Hour()*60 + local.Minute()

	if startMin < endMin {
		return cur >= startMin && cur < endMin && w.hasDay(local.Weekday())
	}
	// 跨午夜
	if cur >= startMin {
		return w.hasDay(local.Weekday()) // 今天是起始日
	}
	if cur < endMin {
		return w.hasDay(local.AddDate(0, 0, -1).Weekday()) // 起始日是昨天
	}
	return false
}

// NextBoundary 返回 at 之后窗口生效状态下一次翻转的时刻。
//
// 状态只可能在 start / end 这两个钟点上翻转，所以枚举未来 8 天的候选时刻、取第一个
// 与当前状态不同的即可——不需要逐分钟推进，也不需要为跨午夜和 Days 过滤各写一套
// 边界推导（那正是最容易算错、且错了看不出来的地方）。
func (w TimeWindow) NextBoundary(at time.Time) (time.Time, bool) {
	startMin, err := parseClock(w.Start)
	if err != nil {
		return time.Time{}, false
	}
	endMin, err := parseClock(w.End)
	if err != nil {
		return time.Time{}, false
	}
	loc := w.location()
	cur := w.ActiveAt(at)
	local := at.In(loc)
	midnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)

	candidates := make([]time.Time, 0, 18)
	for d := 0; d <= 8; d++ {
		base := midnight.AddDate(0, 0, d)
		candidates = append(candidates,
			base.Add(time.Duration(startMin)*time.Minute),
			base.Add(time.Duration(endMin)*time.Minute),
		)
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
}

// ResolveTimeRatioView 构建模型广场用的时段折扣展示数据。
//
// 与 pickTimeRule 共用同一套窗口判定，不另写一份——展示口径与计费口径一旦分叉，
// 表现就是「广场标着空闲时段中、实扣却是原价」，而两边都不报错。
// base / userMul 由调用方从同一次 ResolveGroupRatioAt 的结果里取（res.Base 与
// res.UserMultiplier()），不在这里重算——重算就是第二份解析实现，一旦与计费分叉，
// 表现是广场标着「空闲时段 5.6 折」而实扣是另一个数，两边都不报错。
func ResolveTimeRatioView(usingGroup, modelName string, at time.Time, base, userMul, normalRatio float64) (TimeRatioView, bool) {
	groupTimeRatioMu.RLock()
	rules, hasGroup := groupTimeRatio.Rules[usingGroup]
	var list []TimeRule
	windows := map[string]TimeWindow{}
	if hasGroup {
		if _, matched, found := pickRuleFrom(rules, modelName); found {
			list = append([]TimeRule(nil), matched...)
			for k, v := range groupTimeRatio.Windows {
				windows[k] = v
			}
		}
	}
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
	chargedValue := math.Inf(1)
	for _, rule := range list {
		win, ok := windows[rule.Window]
		if !ok {
			continue
		}
		// 该时段的最终倍率，口径与 ResolveGroupRatioAt 的 Layer 4 逐位一致：
		// 时段规则取代模型折扣（相对 Base），用户档折扣仍在最后叠乘。
		view.Windows = append(view.Windows, TimeWindowView{
			Label: win.displayName(rule.Window),
			Start: win.Start,
			End:   win.End,
			Days:  win.Days,
			TZ:    win.TZ,
			Value: rule.Value,
			Ratio: base * rule.Value * userMul,
		})
		if rule.Value < bestValue {
			bestValue = rule.Value
			view.BestLabel = win.displayName(rule.Window)
		}
		// 此刻计费实际取用的档位：判据必须与 pickTimeRule **逐位一致**（命中且取最小
		// 值，不看是否打折）。此前这里多了一个 rule.Value < 1 的门槛，于是一个 ×1 的
		// 档（校验允许，语义是「这个时段不享受模型折扣，按原价」）生效时，详情表
		// 没有任何窗口行标「进行中」，「其余时段」那行反而被标上——用户看到的
		// 「当前档」是模型折扣价，实扣却是原价。
		//
		// 「是否打折」是另一个问题，见下面的 view.Active。
		if win.ActiveAt(at) && rule.Value < chargedValue {
			view.Label = win.displayName(rule.Window)
			chargedValue = rule.Value
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
	// bestValue 恒有限：走到这里说明至少有一个档位进了 Windows
	view.BestRatio = base * bestValue * userMul
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
	rules, ok := groupTimeRatio.Rules[usingGroup]
	var list []TimeRule
	var windows map[string]TimeWindow
	if ok {
		_, matched, found := pickRuleFrom(rules, modelName)
		if found {
			list = append([]TimeRule(nil), matched...)
			windows = make(map[string]TimeWindow, len(groupTimeRatio.Windows))
			for k, v := range groupTimeRatio.Windows {
				windows[k] = v
			}
		}
	}
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
		if !found || rule.Value < bestRule.Value {
			bestKey, bestWin, bestRule, found = rule.Window, win, rule, true
		}
	}
	return bestKey, bestWin, bestRule, found
}
