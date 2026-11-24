package model

import (
	"context"
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const CFETSPair = "USD/CNY"

type ModelSourceFXQuote struct {
	Pair      string  `json:"-" gorm:"primaryKey;size:12"`
	QuoteDate string  `json:"date" gorm:"primaryKey;size:10"`
	Rate      float64 `json:"rate"`
}

type ModelSourceFXStatus struct {
	Pair        string `json:"-" gorm:"primaryKey;size:12"`
	LastAttempt int64  `json:"last_attempt"`
	LastSuccess int64  `json:"last_success"`
	Error       string `json:"error" gorm:"size:160"`
}

type ModelSourceFXChange struct {
	At    int64    `json:"at"`
	Rate  *float64 `json:"rate"`
	Actor string   `json:"actor"`
}

func SaveModelSourceFXResult(ctx context.Context, at time.Time, quotes []ModelSourceFXQuote, failure string) error {
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		status := ModelSourceFXStatus{Pair: CFETSPair}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&status).Error; err != nil {
			return err
		}
		stamp := at.UnixMilli()
		fields := map[string]any{"last_attempt": stamp, "error": failure}
		if failure == "" {
			fields["last_success"] = stamp
		}
		res := tx.Model(&ModelSourceFXStatus{}).Where("pair = ? AND last_attempt < ?", CFETSPair, stamp).Updates(fields)
		if res.Error != nil || res.RowsAffected == 0 || failure != "" {
			return res.Error
		}
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "pair"}, {Name: "quote_date"}},
			DoUpdates: clause.AssignmentColumns([]string{"rate"}),
		}).Create(&quotes).Error
	})
}

func ReadModelSourceFX(ctx context.Context, from, through string) ([]ModelSourceFXQuote, ModelSourceFXStatus, error) {
	db := DB.WithContext(ctx)
	var status ModelSourceFXStatus
	err := db.First(&status, "pair = ?", CFETSPair).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, status, err
	}
	quotes := make([]ModelSourceFXQuote, 0)
	err = db.Where("pair = ? AND quote_date BETWEEN ? AND ?", CFETSPair, from, through).
		Order("quote_date").Find(&quotes).Error
	return quotes, status, err
}

func ReadModelSourceFXRate(ctx context.Context) (string, error) {
	var option Option
	err := DB.WithContext(ctx).First(&option, clause.Eq{Column: "key", Value: modelSourceRateKey}).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	return option.Value, err
}

func ReadLatestModelSourceFXQuote(ctx context.Context, through string) (ModelSourceFXQuote, error) {
	var quote ModelSourceFXQuote
	err := DB.WithContext(ctx).Where("pair = ? AND quote_date <= ?", CFETSPair, through).
		Order("quote_date DESC").Take(&quote).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = nil
	}
	return quote, err
}

func ReadModelSourceFXChanges(ctx context.Context, from time.Time) (*ModelSourceFXChange, []ModelSourceFXChange, error) {
	query := func() *gorm.DB {
		return LOG_DB.WithContext(ctx).Model(&Log{}).
			Select("id", "created_at", "username", "other").
			Where("type = ? AND other LIKE ?", LogTypeManage, `%"key":"ModelSourceRate"%`)
	}
	var prior []Log
	if err := query().Where("created_at < ?", from.Unix()).Order("created_at DESC, id DESC").Limit(1).Find(&prior).Error; err != nil {
		return nil, nil, err
	}
	var logs []Log
	if err := query().Where("created_at >= ?", from.Unix()).Order("created_at, id").Find(&logs).Error; err != nil {
		return nil, nil, err
	}
	parse := func(log Log) *ModelSourceFXChange {
		var payload struct {
			AdminInfo struct {
				Changes []optionAuditChange `json:"changes"`
			} `json:"admin_info"`
		}
		if common.UnmarshalJsonStr(log.Other, &payload) != nil {
			return nil
		}
		for _, change := range payload.AdminInfo.Changes {
			if change.Key != modelSourceRateKey || len(change.Path) > 0 {
				continue
			}
			raw, err := common.Marshal(change.After)
			if err != nil {
				continue
			}
			rate, err := billing_setting.ParseGlobalModelSourceRate(string(raw))
			// An unset rate can appear as an empty JSON string in the audit log.
			if text, ok := change.After.(string); ok {
				rate, err = billing_setting.ParseGlobalModelSourceRate(text)
			}
			if err == nil {
				result := &ModelSourceFXChange{At: log.CreatedAt, Actor: log.Username}
				if rate > 0 {
					result.Rate = &rate
				}
				return result
			}
		}
		return nil
	}
	var baseline *ModelSourceFXChange
	if len(prior) != 0 {
		baseline = parse(prior[0])
	}
	changes := make([]ModelSourceFXChange, 0, len(logs))
	for _, log := range logs {
		if change := parse(log); change != nil {
			changes = append(changes, *change)
		}
	}
	return baseline, changes, nil
}
