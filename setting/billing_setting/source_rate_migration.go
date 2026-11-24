package billing_setting

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
)

func ParseGlobalModelSourceRate(raw string) (float64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return 0, nil
	}
	var rate float64
	if err := common.UnmarshalJsonStr(raw, &rate); err != nil || !validModelSourceRate(rate) {
		return 0, fmt.Errorf("ModelSourceRate 必须为有效的正数")
	}
	return rate, nil
}

func validModelSourceRate(rate float64) bool {
	return rate > 0 && !math.IsInf(rate, 0)
}

// PlanGlobalModelSourceRateMigration preserves the price of live fx expressions
// without treating legacy per-model rate entries as USD declarations for ordinary models.
func PlanGlobalModelSourceRateMigration(rawRate, rawModes, rawExprs string) (scalar string, legacy bool, err error) {
	rawRate = strings.TrimSpace(rawRate)
	if !strings.HasPrefix(rawRate, "{") {
		_, err := ParseGlobalModelSourceRate(rawRate)
		return "", false, err
	}
	var rates map[string]float64
	if err := common.UnmarshalJsonStr(rawRate, &rates); err != nil {
		return "", true, fmt.Errorf("读取旧 ModelSourceRate 失败: %w", err)
	}
	names := make([]string, 0, len(rates))
	for name := range rates {
		names = append(names, name)
	}
	sort.Strings(names)
	var rate float64
	for _, name := range names {
		value := rates[name]
		if !validModelSourceRate(value) {
			return "", true, fmt.Errorf("模型 %s 的旧 ModelSourceRate 无效，请人工核对", name)
		}
		if rate > 0 && rate != value {
			return "", true, fmt.Errorf("模型 %s 的旧 ModelSourceRate 与 %s 不一致，请人工核对", name, names[0])
		}
		rate = value
	}
	var modes, exprs map[string]string
	if strings.TrimSpace(rawModes) != "" {
		if err := common.UnmarshalJsonStr(rawModes, &modes); err != nil {
			return "", true, fmt.Errorf("读取旧计费模式失败: %w", err)
		}
	}
	if strings.TrimSpace(rawExprs) != "" {
		if err := common.UnmarshalJsonStr(rawExprs, &exprs); err != nil {
			return "", true, fmt.Errorf("读取旧计费表达式失败: %w", err)
		}
	}
	for name, expr := range exprs {
		if modes[name] != BillingModeTieredExpr || !billingexpr.UsedVars(strings.TrimSpace(expr))["fx"] {
			continue
		}
		if _, exists := rates[name]; !exists {
			return "", true, fmt.Errorf("模型 %s 的在用表达式引用 fx，但没有旧汇率，请人工核对", name)
		}
	}
	if rate > 0 {
		scalar = strconv.FormatFloat(rate, 'g', -1, 64)
	}
	return scalar, true, nil
}
