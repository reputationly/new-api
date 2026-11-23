package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestValidatePricingFinalStateTimePrice(t *testing.T) {
	rules := func(pattern string) string {
		return `{"windows":{"am":{"start":"09:00","end":"12:00"}},"rules":{"*":{"` + pattern + `":[{"window":"am","value":1.2}]}}}`
	}
	exprJSON, err := common.Marshal(map[string]string{
		"m1": `v2:tier("base", hour("Asia/Shanghai") >= 9 ? p * 5 : p * 2)`,
		"m2": `v2:tier("base", weekday("Asia/Shanghai") >= 1 ? p * 2 : p * 3)`,
	})
	require.NoError(t, err)
	final := map[string]string{
		"billing_setting.billing_expr": string(exprJSON),
		"billing_setting.billing_mode": `{"m1":"tiered_expr"}`,
	}

	for _, pattern := range []string{"m1", "m*", "m2"} {
		final["GroupTimeRatio"] = rules(pattern)
		require.ErrorContains(t, validatePricingFinalState(final), "留有一条带时间函数的计费表达式")
	}
	final["billing_setting.billing_mode"] = `{"m1":"tiered_expr","m2":"tiered_expr"}`
	require.ErrorContains(t, validatePricingFinalState(final), "留有一条带时间函数的计费表达式")
	final["GroupTimeRatio"] = rules("other")
	require.NoError(t, validatePricingFinalState(final))
	final["GroupTimeRatio"] = rules("m*")
	final["billing_setting.billing_expr"] = `{"m1":"p * 2","m2":"p * 2 + c * 8"}`
	require.NoError(t, validatePricingFinalState(final))
}
