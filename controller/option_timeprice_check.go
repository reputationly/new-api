package controller

import (
	"fmt"
	"sort"

	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

// 时段价与「计费表达式里的时间函数」互斥（docs/time-price-axis-design.md §4.3）。
//
// 两者都是「按时刻改价」，同时存在等于同一笔单乘两次时间系数。更麻烦的是表达式里的
// 时间分支既不进 time_rule 日志、也不进模型广场的分时表，用户看到的价与实扣对不上，
// 而对账侧反推不出是哪一层拍的板。所以保存任一侧时，都要拒绝另一侧已存在的情况。

func checkTimePriceVsExprs(timeRatioJSON string, exprs map[string]string) error {
	patterns, err := ratio_setting.TimeRulePatterns(timeRatioJSON)
	if err != nil {
		return err
	}
	// **不看当前模式**（2026-11-20 复核后定的口径）：表达式里存着 hour() 就说明
	// "这颗雷已经埋下"——它现在可能因为模式不是 tiered_expr 而没有生效，但把模式切回来
	// 的那一刻它立刻生效，而那一天不会有人回头检查「这个模型配没配时段价」。
	// 运营要配时段价，先删掉那条旧表达式即可，错误提示里就是这么说的。
	//
	// 三个保存入口（时段价 / 表达式 / 计费模式）口径一致：都不看当前模式。
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
				return fmt.Errorf("模型 %s 留有一条带时间函数的计费表达式（hour/weekday 等），不能再配时段价：请先删掉那条表达式，或把分时价改在表达式里写", model)
			}
		}
	}
	return nil
}
