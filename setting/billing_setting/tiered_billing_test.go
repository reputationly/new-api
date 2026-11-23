package billing_setting

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 保存路径的编译校验：坏表达式必须带模型名报错，好表达式与存量 v1 放行。
func TestValidateBillingExprJSON(t *testing.T) {
	cases := []struct {
		name    string
		json    string
		wantErr string
	}{
		{
			name: "合法 v2 表达式通过",
			json: `{"gpt-5.6-luna":"v2:len <= 272000 ? tier(\"≤272K\", p * 1.36 + c * 8.16) : tier(\">272K\", p * 2.72 + c * 12.24)"}`,
		},
		{
			name:    "语法错误带模型名",
			json:    `{"gpt-x":"v2:tier(\"base\", p * )"}`,
			wantErr: "模型 gpt-x 的计费表达式无法编译",
		},
		{
			name:    "双前缀也能被拦住",
			json:    `{"gpt-y":"v2:v2:tier(\"base\", p * 1)"}`,
			wantErr: "模型 gpt-y 的计费表达式无法编译",
		},
		{
			name: "存量 v1 跳过校验",
			json: `{"legacy":"tier(\"base\", p * 2)"}`,
		},
		{
			name: "空值放行",
			json: ``,
		},
		{
			name: "结构非法交给别的路径",
			json: `not-json`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateBillingExprJSON(tc.json)
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.True(t, strings.Contains(err.Error(), tc.wantErr), "err = %v", err)
		})
	}
}

// 只校验 v2：同一张映射里 v1 与 v2 混存时，不能因为 v1 存在就误拦。
func TestValidateBillingExprJSONMixedVersions(t *testing.T) {
	jsonStr := `{
		"legacy-model": "tier(\"base\", p * 2)",
		"new-model": "v2:tier(\"base\", p * 1.36 + img_o * 204)"
	}`
	require.NoError(t, ValidateBillingExprJSON(jsonStr))
}

// 引用 fx 的表达式必须有源货币汇率；没有就保存时报错，而不是等到请求时炸。
func TestValidateBillingExprJSON_RequiresFXRate(t *testing.T) {
	expr := `{"gpt-fx":"v2:tier(\"base\", p * 1.5 * fx + c * 7.5 * fx)"}`
	t.Cleanup(func() { _ = UpdateModelSourceRateByJSONString("") })

	require.NoError(t, UpdateModelSourceRateByJSONString("{}"))
	err := ValidateBillingExprJSON(expr)
	require.Error(t, err)
	require.Contains(t, err.Error(), "没有设置「源货币汇率」")

	require.NoError(t, UpdateModelSourceRateByJSONString(`{"gpt-fx":6.8}`))
	require.NoError(t, ValidateBillingExprJSON(expr))
}

// 不引用 fx 的表达式不受汇率影响（老写法继续可用）。
func TestValidateBillingExprJSON_NoFXNoRateOK(t *testing.T) {
	t.Cleanup(func() { _ = UpdateModelSourceRateByJSONString("") })
	require.NoError(t, UpdateModelSourceRateByJSONString("{}"))
	require.NoError(t, ValidateBillingExprJSON(`{"m":"v2:tier(\"base\", p * 1.36)"}`))
}

// 用**真实汇率**校验：`p * (2 - fx)` 在 fx=1 下恒正，到 6.8 就是负数，
// 负额度在结算时会走 IncreaseUserQuota（凭空加额度），必须在保存阶段拦住。
func TestValidateBillingExprJSON_NegativeAtRealRate(t *testing.T) {
	t.Cleanup(func() { _ = UpdateModelSourceRateByJSONString("") })
	require.NoError(t, UpdateModelSourceRateByJSONString(`{"neg":6.8}`))

	err := ValidateBillingExprJSON(`{"neg":"v2:tier(\"base\", p * (2 - fx))"}`)
	require.Error(t, err)
	require.Contains(t, err.Error(), "非法金额")
}

// 批量保存用「这次要写的汇率」校验，而不是内存里的旧值：
// 同一个请求里把汇率删掉、表达式却还引用 fx，必须报错。
func TestValidateBillingExprJSONWithRates_FinalState(t *testing.T) {
	exprJSON := `{"m":"v2:tier(\"base\", p * 1.5 * fx)"}`

	err := ValidateBillingExprJSONWithRates(exprJSON, map[string]float64{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "没有设置「源货币汇率」")

	require.NoError(t, ValidateBillingExprJSONWithRates(exprJSON, map[string]float64{"m": 6.8}))
}
