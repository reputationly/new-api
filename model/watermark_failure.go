package model

import (
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
)

type WatermarkFailure struct {
	Id        int    `json:"id"`
	CreatedAt int64  `json:"created_at" gorm:"index"`
	Source    string `json:"source" gorm:"type:varchar(32)"`
	MediaKind string `json:"media_kind" gorm:"type:varchar(16)"`
	GroupName string `json:"group_name" gorm:"column:group_name;type:varchar(64)"`
	UserId    int    `json:"user_id"`
	TaskId    string `json:"task_id" gorm:"type:varchar(64)"`
	ObjectKey string `json:"object_key" gorm:"type:varchar(512)"`
	Error     string `json:"error" gorm:"type:text"`
}

func (WatermarkFailure) TableName() string {
	return "watermark_failures"
}

func RecordWatermarkFailure(source, mediaKind, group string, userID int, taskID, objectKey string, err error) {
	if err == nil {
		return
	}
	message := err.Error()
	if utf8.RuneCountInString(message) > 1000 {
		runes := []rune(message)
		message = string(runes[:1000])
	}
	common.SysError(fmt.Sprintf("watermark failed (source=%s kind=%s group=%s key=%s): %s", source, mediaKind, group, objectKey, message))
	if DB == nil {
		return
	}
	entry := WatermarkFailure{
		CreatedAt: time.Now().Unix(), Source: source, MediaKind: mediaKind,
		GroupName: group, UserId: userID, TaskId: taskID, ObjectKey: objectKey, Error: message,
	}
	if dbErr := DB.Create(&entry).Error; dbErr != nil {
		common.SysError("watermark failure record could not be saved: " + dbErr.Error())
	}
}

func CountWatermarkFailuresSince(ts int64) (int64, error) {
	var count int64
	if DB == nil {
		return 0, nil
	}
	err := DB.Model(&WatermarkFailure{}).Where("created_at >= ?", ts).Count(&count).Error
	return count, err
}

func RecentWatermarkFailures(limit int) ([]WatermarkFailure, error) {
	if limit <= 0 {
		limit = 20
	}
	var entries []WatermarkFailure
	if DB == nil {
		return entries, nil
	}
	err := DB.Order("created_at desc").Limit(limit).Find(&entries).Error
	return entries, err
}

func CleanupWatermarkFailures() error {
	if DB == nil {
		return nil
	}
	return DB.Where("created_at < ?", time.Now().AddDate(0, 0, -30).Unix()).Delete(&WatermarkFailure{}).Error
}
