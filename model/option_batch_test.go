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
		"ModelSourceRate":              "6.8",
		"VideoPricingConfig":           `{}`,
	}
	for key, value := range valid {
		require.NoError(t, ValidateOptionValueForBatch(key, value), key)
	}

	// 空表/显式 null 放行（表示清空）
	require.Error(t, ValidateOptionValueForBatch("ModelSourceRate", "{}"))
	require.NoError(t, ValidateOptionValueForBatch("ModelSourceRate", "null"))
	require.NoError(t, ValidateOptionValueForBatch("billing_setting.billing_expr", ""))

	// 畸形 JSON 拒绝
	require.Error(t, ValidateOptionValueForBatch("ModelRatio", "not-json"))
	require.Error(t, ValidateOptionValueForBatch("billing_setting.billing_expr", "[1,2]"))
	require.Error(t, ValidateOptionValueForBatch("VideoPricingConfig", "not-json"))

	// 白名单外拒绝
	require.Error(t, ValidateOptionValueForBatch("GitHubClientSecret", "x"))
}

func TestGroupTimeAuditRecordsChangedModelPath(t *testing.T) {
	const key = "GroupTimeRatio"
	before := map[string]string{key: `{"rules":{"default":{"m":[{"window":"am","value":1}],"other":[{"window":"am","value":1}]}}}`}
	after := map[string]string{key: `{"rules":{"default":{"m":[{"window":"am","value":0}],"other":[{"window":"am","value":1}]}}}`}
	log, err := (OptionAudit{}).log(before, after)
	require.NoError(t, err)
	require.NotNil(t, log)
	require.Contains(t, log.Other, `"path":["rules","default","m"],"before":[{"value":1,"window":"am"}],"after":[{"value":0,"window":"am"}]`)
	require.Contains(t, log.Other, `"total_changes":1`)
}

func TestPricingOptionApplyOrder(t *testing.T) {
	const mode, expr = "billing_setting.billing_mode", "billing_setting.billing_expr"
	for _, keys := range [][]string{
		{mode, expr, "ModelSourceRate", "GroupTimeRatio"},
		{expr, mode, "ModelSourceRate", "GroupTimeRatio"},
	} {
		require.Equal(t, []string{expr, "ModelSourceRate", "GroupTimeRatio", mode}, pricingOptionApplyOrder(keys))
	}
	require.Equal(t, []string{expr, "ModelSourceRate"}, pricingOptionApplyOrder([]string{expr, "ModelSourceRate"}))
}
