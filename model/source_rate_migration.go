package model

import (
	"fmt"

	"github.com/QuantumNous/new-api/setting/billing_setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	modelSourceRateKey       = "ModelSourceRate"
	modelPriceCurrencyKey    = "ModelPriceCurrency"
	legacyModelSourceRateKey = "_internal.model_source_rate_legacy"
)

// migrateGlobalModelSourceRate rewrites the legacy rate atomically before options load.
func migrateGlobalModelSourceRate(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		lock := Option{Key: pricingWriteLockKey}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "key"}},
			DoUpdates: clause.AssignmentColumns([]string{"value"}),
		}).Create(&lock).Error; err != nil {
			return err
		}
		const modeKey, exprKey = "billing_setting.billing_mode", "billing_setting.billing_expr"
		values, err := getOptionValues(tx, []string{modelSourceRateKey, modelPriceCurrencyKey, legacyModelSourceRateKey, modeKey, exprKey})
		if err != nil {
			return err
		}
		scalar, legacy, err := billing_setting.PlanGlobalModelSourceRateMigration(values[modelSourceRateKey], values[modeKey], values[exprKey])
		if err != nil || !legacy {
			return err
		}
		if _, backedUp := values[legacyModelSourceRateKey]; backedUp {
			return fmt.Errorf("旧 ModelSourceRate 已有迁移备份，请确认回滚状态后再迁移")
		}
		if _, configured := values[modelPriceCurrencyKey]; configured {
			return fmt.Errorf("旧 ModelSourceRate 与新版 ModelPriceCurrency 同时存在，请人工核对")
		}
		if err := tx.Create(&Option{Key: legacyModelSourceRateKey, Value: values[modelSourceRateKey]}).Error; err != nil {
			return err
		}
		return tx.Save(&Option{Key: modelSourceRateKey, Value: scalar}).Error
	})
}
