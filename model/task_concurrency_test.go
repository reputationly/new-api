package model

import (
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupTaskCountTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	common.UsingSQLite = true
	common.UsingMySQL = false
	common.UsingPostgreSQL = false
	common.RedisEnabled = false

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	prevDB := DB
	DB = db
	require.NoError(t, db.AutoMigrate(&Task{}))

	t.Cleanup(func() {
		DB = prevDB
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

// 体验区视频并发闸的计数依据。前端那道闸切 tab / 开新标签页 / 刷新都会归零，
// 只有这个查询是准的，所以三条口径都要钉住：只数自己的、只数在途的、只数视频的。
func TestCountUserUnfinishedVideoTasks(t *testing.T) {
	db := setupTaskCountTestDB(t)

	seed := func(userId int, status TaskStatus, action string) {
		require.NoError(t, db.Create(&Task{
			UserId:   userId,
			Status:   status,
			Action:   action,
			Platform: constant.TaskPlatform("1"),
		}).Error)
	}

	// 用户 1：三个在途视频任务，三种在途状态各一个。
	seed(1, TaskStatusSubmitted, constant.TaskActionGenerate)
	seed(1, TaskStatusQueued, constant.TaskActionTextGenerate)
	seed(1, TaskStatusInProgress, constant.TaskActionFirstTailGenerate)
	// 用户 1 的干扰项，都不该被数进来。
	seed(1, TaskStatusSuccess, constant.TaskActionGenerate)  // 已完成
	seed(1, TaskStatusFailure, constant.TaskActionGenerate)  // 已失败
	seed(1, TaskStatusInProgress, constant.SunoActionMusic)  // Suno 音乐，非视频
	seed(1, TaskStatusInProgress, constant.SunoActionLyrics) // Suno 歌词，非视频
	// 用户 2 的在途任务不能算到用户 1 头上。
	seed(2, TaskStatusInProgress, constant.TaskActionGenerate)

	n, err := CountUserUnfinishedVideoTasks(1)
	require.NoError(t, err)
	require.Equal(t, int64(3), n, "只应数用户 1 的在途视频任务")

	n, err = CountUserUnfinishedVideoTasks(2)
	require.NoError(t, err)
	require.Equal(t, int64(1), n)

	n, err = CountUserUnfinishedVideoTasks(999)
	require.NoError(t, err)
	require.Equal(t, int64(0), n, "没有任务的用户应为 0，而不是数到别人的")
}

// 其余两种视频 action 也必须计入，否则「参考生视频」「续写」可以绕过并发闸。
func TestCountUserUnfinishedVideoTasksCoversAllVideoActions(t *testing.T) {
	db := setupTaskCountTestDB(t)

	actions := []string{
		constant.TaskActionGenerate,
		constant.TaskActionTextGenerate,
		constant.TaskActionFirstTailGenerate,
		constant.TaskActionReferenceGenerate,
		constant.TaskActionRemix,
	}
	for _, a := range actions {
		require.NoError(t, db.Create(&Task{
			UserId: 7,
			Status: TaskStatusInProgress,
			Action: a,
		}).Error)
	}

	n, err := CountUserUnfinishedVideoTasks(7)
	require.NoError(t, err)
	require.Equal(t, int64(len(actions)), n, "五种视频 action 都要计入并发闸")
}
