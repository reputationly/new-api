package controller

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/require"
)

// seedTieredExprModel 把某模型设成 tiered_expr 并写入表达式。
// billing_setting 没有导出 setter，测试里走它自己的配置总线（与生产保存同一条路径）。
func seedTieredExprModel(t *testing.T, model, expr string) {
	t.Helper()
	cfg := config.GlobalConfig.Get("billing_setting")
	require.NotNil(t, cfg)
	config.UpdateConfigFromMap(cfg, map[string]string{
		"billing_mode": fmt.Sprintf(`{"%s":"tiered_expr"}`, model),
		"billing_expr": fmt.Sprintf(`{"%s":%s}`, model, strconv.Quote(expr)),
	})
	t.Cleanup(func() {
		config.UpdateConfigFromMap(cfg, map[string]string{
			"billing_mode": "{}",
			"billing_expr": "{}",
		})
	})
}

// TestCheckTimePriceVsExpr 时段价覆盖到「表达式里自带分时」的模型时必须拒绝。
func TestCheckTimePriceVsExpr(t *testing.T) {
	seedTieredExprModel(t, "m1", `v2:tier("base", hour("Asia/Shanghai") >= 9 ? p * 5 : p * 2)`)

	rules := func(pattern string) string {
		return `{"windows":{"am":{"start":"09:00","end":"12:00"}},"rules":{"*":{"` + pattern + `":[{"window":"am","value":1.2}]}}}`
	}

	// 命中该模型 → 拒绝
	require.ErrorContains(t, checkTimePriceVsExpr(rules("m1")), "已经写在计费表达式里")
	// 通配命中 → 也拒绝
	require.ErrorContains(t, checkTimePriceVsExpr(rules("m*")), "已经写在计费表达式里")
	// 不覆盖它 → 放行
	require.NoError(t, checkTimePriceVsExpr(rules("other")))

	// 反方向：表达式引用时间函数，而该模型已有段价 → 拒绝
	require.NoError(t, ratio_setting.UpdateGroupTimeRatioByJSONString(rules("m2")))
	require.ErrorContains(t, checkExprVsTimePrice(`{"m2":"v2:tier(\"base\", weekday(\"Asia/Shanghai\") >= 1 ? p * 2 : p * 3)"}`), "已配时段价")
	require.NoError(t, checkExprVsTimePrice(`{"m2":"v2:tier(\"base\", p * 2 + c * 8)"}`))
	_, _ = ratio_setting.TimeRulePatterns(rules("m2"))
}
