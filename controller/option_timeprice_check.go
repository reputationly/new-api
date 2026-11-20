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
	modes := billing_setting.GetBillingModeCopy()
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
		for _, pattern := range patterns {
			if ratio_setting.MatchModelPattern(pattern, model) {
				return fmt.Errorf("模型 %s 的分时价已经写在计费表达式里（hour/weekday 等时间函数），不能再配时段价：请把分时价留在其中一处", model)
			}
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
