package controller

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

func TestDirectTopupQuotaUsesCNYFaceValue(t *testing.T) {
	settings := operation_setting.GetGeneralSetting()
	originalType, originalFX := settings.QuotaDisplayType, billing_setting.ModelSourceRate2JSONString()
	t.Cleanup(func() {
		settings.QuotaDisplayType = originalType
		require.NoError(t, billing_setting.UpdateModelSourceRateByJSONString(originalFX))
	})
	settings.QuotaDisplayType = operation_setting.QuotaDisplayTypeCNY
	for _, rate := range []string{"6.8", "7.3", ""} {
		require.NoError(t, billing_setting.UpdateModelSourceRateByJSONString(rate))
		got, ok := directTopupQuota(100)
		require.True(t, ok)
		require.Equal(t, int64(100_000_000_000), got)
	}
}

func TestUpdateOptionRejectsQuotaDisplayChanges(t *testing.T) {
	for _, key := range []string{"general_setting.quota_display_type", "DisplayInCurrencyEnabled"} {
		t.Run(key, func(t *testing.T) {
			c, rec := newAuthenticatedContext(t, http.MethodPut, "/api/option/", map[string]any{"key": key, "value": "USD"}, 1)
			UpdateOption(c)
			res := decodeAPIResponse(t, rec)
			require.False(t, res.Success)
			require.Contains(t, res.Message, "固定为人民币")
			require.Error(t, model.UpdateOption(key, "USD"))
			require.Error(t, model.ValidateOptionValueForBatch(key, "USD"))
		})
	}
}

func TestUpdateOptionRejectsLegacySiteFX(t *testing.T) {
	for _, key := range []string{"USDExchangeRate", "QuotaPerUnit"} {
		t.Run(key, func(t *testing.T) {
			c, rec := newAuthenticatedContext(t, http.MethodPut, "/api/option/", map[string]any{"key": key, "value": "10"}, 1)
			UpdateOption(c)
			res := decodeAPIResponse(t, rec)
			require.False(t, res.Success)
			require.Contains(t, res.Message, "全局模型源汇率")
			require.Error(t, model.UpdateOption(key, "10"))
			require.Error(t, model.ValidateOptionValueForBatch(key, "10"))
		})
	}
}
