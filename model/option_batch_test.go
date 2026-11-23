package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 批量保存的「提交前校验」：必须在写库之前就失败，而不是 commit 后应用内存时才报错
// （那样会返回 500 但数据已经生效）。
func TestValidateOptionValueForBatch(t *testing.T) {
	valid := map[string]string{
		"ModelRatio":                   `{"m": 1.36}`,
		"ModelPrice":                   `{"m": 0.2}`,
		"billing_setting.billing_mode": `{"m": "tiered_expr"}`,
		"billing_setting.billing_expr": `{"m": "v2:tier(\"base\", p * 1)"}`,
		"ModelSourceRate":              `{"m": 6.8}`,
		"VideoPricingConfig":           `{}`,
	}
	for key, value := range valid {
		require.NoError(t, ValidateOptionValueForBatch(key, value), key)
	}

	// 空表/显式 null 放行（表示清空）
	require.NoError(t, ValidateOptionValueForBatch("ModelSourceRate", "{}"))
	require.NoError(t, ValidateOptionValueForBatch("ModelSourceRate", "null"))
	require.NoError(t, ValidateOptionValueForBatch("billing_setting.billing_expr", ""))

	// 畸形 JSON 拒绝
	require.Error(t, ValidateOptionValueForBatch("ModelRatio", "not-json"))
	require.Error(t, ValidateOptionValueForBatch("billing_setting.billing_expr", "[1,2]"))
	require.Error(t, ValidateOptionValueForBatch("VideoPricingConfig", "not-json"))

	// 白名单外拒绝
	require.Error(t, ValidateOptionValueForBatch("GitHubClientSecret", "x"))
}
