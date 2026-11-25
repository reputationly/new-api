package model

import (
	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const legacySiteFXBackupKey = "_internal.site_fx_legacy"

func isRetiredFXOption(key string) bool {
	return key == "USDExchangeRate" || key == "QuotaPerUnit"
}

func backupLegacySiteFX(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		values, err := getOptionValues(tx, []string{"USDExchangeRate", "QuotaPerUnit", "Price"})
		if err != nil || len(values) == 0 {
			return err
		}
		raw, err := common.Marshal(values)
		if err != nil {
			return err
		}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&Option{Key: legacySiteFXBackupKey, Value: string(raw)}).Error
	})
}
