package model

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMigrateGlobalModelSourceRate(t *testing.T) {
	const exprKey = "billing_setting.billing_expr"
	const expr = `{"fx":"v2:p * 2 * fx"}`
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{"uniform", `{"fx":6.8,"ordinary":6.8}`},
		{"different", `{"fx":6.8,"ordinary":7}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { _ = sqlDB.Close() })
			require.NoError(t, db.AutoMigrate(&Option{}))
			require.NoError(t, db.Create(&[]Option{
				{Key: modelSourceRateKey, Value: tc.raw},
				{Key: "billing_setting.billing_mode", Value: `{"fx":"tiered_expr"}`},
				{Key: exprKey, Value: expr},
				{Key: "ModelRatio", Value: `{"ordinary":13.6}`},
			}).Error)

			if tc.name == "different" {
				require.ErrorContains(t, migrateGlobalModelSourceRate(db), "不一致")
			} else {
				require.NoError(t, migrateGlobalModelSourceRate(db))
				require.NoError(t, migrateGlobalModelSourceRate(db))
			}
			options, err := getOptionValues(db, []string{modelSourceRateKey, legacyModelSourceRateKey, modelPriceCurrencyKey, exprKey, "ModelRatio"})
			require.NoError(t, err)
			require.Equal(t, expr, options[exprKey])
			require.Equal(t, `{"ordinary":13.6}`, options["ModelRatio"])
			if tc.name == "different" {
				require.Equal(t, tc.raw, options[modelSourceRateKey])
				require.NotContains(t, options, legacyModelSourceRateKey)
				require.NotContains(t, options, modelPriceCurrencyKey)
				return
			}
			require.Equal(t, "6.8", options[modelSourceRateKey])
			require.Equal(t, tc.raw, options[legacyModelSourceRateKey])
			require.NotContains(t, options, modelPriceCurrencyKey)
		})
	}
}
