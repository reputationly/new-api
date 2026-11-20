package controller

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"

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

// seedExprOnly 只写表达式、不写模式：模拟「表达式已带 hour()，但模式还没切到 tiered_expr」。
func seedExprOnly(t *testing.T, model, expr string) {
	t.Helper()
	cfg := config.GlobalConfig.Get("billing_setting")
	require.NotNil(t, cfg)
	exprs := map[string]string{model: expr}
	raw, err := common.Marshal(exprs)
	require.NoError(t, err)
	config.UpdateConfigFromMap(cfg, map[string]string{"billing_expr": string(raw)})
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

	// 表达式里有 hour() 但模式还不是 tiered_expr：同样禁止配时段价
	// （否则「先配时段价、后切模式」就能绕过互斥）
	seedExprOnly(t, "m3", `v2:tier("base", hour("Asia/Shanghai") >= 9 ? p * 5 : p * 2)`)
	require.ErrorContains(t, checkTimePriceVsExpr(rules("m3")), "已经写在计费表达式里")

	// 反方向：表达式引用时间函数，而该模型已有段价 → 拒绝
	require.NoError(t, ratio_setting.UpdateGroupTimeRatioByJSONString(rules("m2")))
	require.ErrorContains(t, checkExprVsTimePrice(`{"m2":"v2:tier(\"base\", weekday(\"Asia/Shanghai\") >= 1 ? p * 2 : p * 3)"}`), "已配时段价")
	require.NoError(t, checkExprVsTimePrice(`{"m2":"v2:tier(\"base\", p * 2 + c * 8)"}`))

	// 第三条防线：把已有段价的模型切成 tiered_expr，而表达式里带时间函数 → 拒绝
	require.NoError(t, ratio_setting.UpdateGroupTimeRatioByJSONString(rules("m4")))
	seedExprOnly(t, "m4", `v2:tier("base", hour("Asia/Shanghai") >= 9 ? p * 5 : p * 2)`)
	require.ErrorContains(t, checkBillingModeVsTimePrice(`{"m4":"tiered_expr"}`), "请先清掉其中一处")
	require.NoError(t, checkBillingModeVsTimePrice(`{"m5":"tiered_expr"}`), "没配时段价的模型照常放行")
	_, _ = ratio_setting.TimeRulePatterns(rules("m2"))
}
