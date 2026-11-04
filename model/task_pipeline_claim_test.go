package model

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// ClaimPipelineStage 的并发正确性。
//
// 这是流水线"不重复提交、不重复计费"的**唯一**跨进程防线：读 Stage → 提交子任务 →
// 回写 Stage2TaskID 之间有一个窗口，两个推进者同时进来就会提交两遍、扣两笔钱。
// 用一条 `UPDATE ... WHERE pipeline_claim_stage < ?` 做比较并交换，只会有一个成功。
func TestClaimPipelineStageOnlyOneWins(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&Task{}))
	task := &Task{TaskID: "claim-1", Platform: "24", Action: "t2v", Status: TaskStatusInProgress}
	require.NoError(t, task.Insert())

	var wins int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := ClaimPipelineStage(task.ID, 2)
			require.NoError(t, err)
			if ok {
				atomic.AddInt32(&wins, 1)
			}
		}()
	}
	wg.Wait()
	require.EqualValues(t, 1, wins, "同一段只能被一个推进者抢下")
}

// 后面的段可以继续被抢，前面的段抢不回来（防止重复推进同一段）。
func TestClaimPipelineStageOrdering(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&Task{}))
	task := &Task{TaskID: "claim-2", Platform: "24", Action: "t2v", Status: TaskStatusInProgress}
	require.NoError(t, task.Insert())

	ok, err := ClaimPipelineStage(task.ID, 2)
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = ClaimPipelineStage(task.ID, 2)
	require.NoError(t, err)
	require.False(t, ok, "同一段不得被抢两次")

	ok, err = ClaimPipelineStage(task.ID, 1)
	require.NoError(t, err)
	require.False(t, ok, "不得回退抢前面的段")

	ok, err = ClaimPipelineStage(task.ID, 3)
	require.NoError(t, err)
	require.True(t, ok, "下一段应当可以继续抢")
}

// 非法入参直接拒绝，不要去碰库（任务 id 为 0 说明这条任务还没落库）。
func TestClaimPipelineStageRejectsInvalid(t *testing.T) {
	ok, err := ClaimPipelineStage(0, 2)
	require.NoError(t, err)
	require.False(t, ok)

	ok, err = ClaimPipelineStage(1, 0)
	require.NoError(t, err)
	require.False(t, ok)
}
