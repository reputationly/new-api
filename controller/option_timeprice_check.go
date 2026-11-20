package controller

import (
	"fmt"
	"sort"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

// 时段价与「计费表达式里的时间函数」互斥（docs/time-price-axis-design.md §4.3）。
//
// 两者都是「按时刻改价」，同时存在等于同一笔单乘两次时间系数。更麻烦的是表达式里的
// 时间分支既不进 time_rule 日志、也不进模型广场的分时表，用户看到的价与实扣对不上，
// 而对账侧反推不出是哪一层拍的板。所以保存任一侧时，都要拒绝另一侧已存在的情况。

// checkTimePriceVsExpr 在保存 GroupTimeRatio 时调用：新规则覆盖到的模型里，
// 若有 tiered_expr 且表达式引用了时间函数，直接拒绝。
func checkTimePriceVsExpr(timeRatioJSON string) error {
	patterns, err := ratio_setting.TimeRulePatterns(timeRatioJSON)
	if err != nil || len(patterns) == 0 {
		// 语法错由 CheckGroupTimeRatio 报；空规则不可能冲突
		return nil
	}
	// 只看「表达式里有没有时间函数」，**不看当前模式**：
	// 模式随时可以被切到 tiered_expr，而切模式那一步如果是事后发生的，
	// 双重时间轴就会在那时候才出现——所以过滤当前模式等于给未来留了个洞。
	// 表达式存着时间函数 = 这颗雷已经埋下，禁止再配时段价。
	exprs := billing_setting.GetBillingExprCopy()
	models := make([]string, 0, len(exprs))
	for model := range exprs {
		models = append(models, model)
	}
	sort.Strings(models)
	for _, model := range models {
		if !billingexpr.UsesTimeFunctions(exprs[model]) {
			continue
		}
		for _, pattern := range patterns {
			if ratio_setting.MatchModelPattern(pattern, model) {
				return fmt.Errorf("模型 %s 的分时价已经写在计费表达式里（hour/weekday 等时间函数），不能再配时段价：请把分时价留在其中一处", model)
			}
		}
	}
	return nil
}

// checkBillingModeVsTimePrice 是第三条防线：把模型的计费模式切成 tiered_expr 时，
// 若它的表达式里带时间函数、而该模型已经配了时段价，同样拒绝。
//
// 没有这道检查时存在一个真实的时序缺口：先给「模式还不是 tiered 但表达式已含 hour()」
// 的模型配时段价（那时 checkTimePriceVsExpr 因为模式判断会放行），随后再把模式切到
// tiered_expr —— 双重时间轴就在切模式那一刻悄悄生效了。
func checkBillingModeVsTimePrice(modeJSON string) error {
	if modeJSON == "" {
		return nil
	}
	var modes map[string]string
	if err := common.UnmarshalJsonStr(modeJSON, &modes); err != nil {
		return nil
	}
	exprs := billing_setting.GetBillingExprCopy()
	models := make([]string, 0, len(modes))
	for model := range modes {
		models = append(models, model)
	}
	sort.Strings(models)
	for _, model := range models {
		if modes[model] != billing_setting.BillingModeTieredExpr {
			continue
		}
		if !billingexpr.UsesTimeFunctions(exprs[model]) {
			continue
		}
		if ratio_setting.HasTimeRulesForModel(model) {
			return fmt.Errorf("模型 %s 已配时段价，而它的计费表达式里引用了时间函数（hour/weekday 等）：请先清掉其中一处再切换计费模式", model)
		}
	}
	return nil
}

// checkExprVsTimePrice 是反方向：保存计费表达式时，若其中引用了时间函数、
// 而该模型已经配了时段价，同样拒绝。
func checkExprVsTimePrice(exprJSON string) error {
	if exprJSON == "" {
		return nil
	}
	var exprs map[string]string
	if err := common.UnmarshalJsonStr(exprJSON, &exprs); err != nil {
		// 语法错交给 billing_setting 自己的校验，这里只关心时间函数
		return nil
	}
	models := make([]string, 0, len(exprs))
	for model := range exprs {
		models = append(models, model)
	}
	sort.Strings(models)
	for _, model := range models {
		if !billingexpr.UsesTimeFunctions(exprs[model]) {
			continue
		}
		if ratio_setting.HasTimeRulesForModel(model) {
			return fmt.Errorf("模型 %s 已配时段价，计费表达式里不能再引用时间函数（hour/weekday 等）：请把分时价留在其中一处", model)
		}
	}
	return nil
}
