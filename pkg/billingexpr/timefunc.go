package billingexpr

import "strings"

// requestRuleSeparator 是「计费表达式 + 请求规则」的拼接分隔符。
// 现阶段只有表达式本身会用到它；分开编译是为了兼容 expr.md 里描述的拼接格式。
const requestRuleSeparator = "|||"

// timeFuncNames 是「会让价格随时刻变化」的内置函数白名单。
//
// 用白名单而不是「凡是函数都算」：表达式里还有 tier / max / min / param / header / has，
// 它们与时间无关，误判会把正常的阶梯价拦下来。
var timeFuncNames = map[string]bool{
	"hour":    true,
	"minute":  true,
	"weekday": true,
	"month":   true,
	"day":     true,
}

// UsesTimeFunctions 报告表达式是否引用了时间函数。
//
// 用途：站点同时有「时段价」这一层。若某模型的计费表达式里也写 hour(...) 直接分时，
// 同一笔单就会乘两次时间系数；而表达式里的时间分支既不进 time_rule 日志、也不进
// 模型广场的分时表，用户看到的价与实扣对不上。所以保存时两侧互斥。
//
// 注意：必须逐段编译。整串含 "|||" 时不是合法表达式，UsedVars 会编译失败并
// **静默返回 nil**，检测恒为 false、互斥形同虚设（实测过）。
func UsesTimeFunctions(exprStr string) bool {
	if strings.TrimSpace(exprStr) == "" {
		return false
	}
	for _, part := range strings.Split(exprStr, requestRuleSeparator) {
		for name := range UsedVars(strings.TrimSpace(part)) {
			if timeFuncNames[name] {
				return true
			}
		}
	}
	return false
}
