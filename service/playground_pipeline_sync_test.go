package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// 巡检侧（SyncAggregatePipelineParents）推进下一段。
//
// 这条路径与 task_polling 的平台分支不同：聚合父任务被 partitionTasksForPolling 挡在
// 平台轮询之外，**没有 switch 之后那个统一的 UpdateWithStatus 尾巴**。所以"推进成功后
// 必须自己落库"这件事只在 Sync 里成立不了就没人管 —— 曾经就是这样：
//
//	改内存态 → continue → 下一轮从库里读回 Stage=2/旧子任务 id → 再次推进 →
//	被 claim 的 CAS 拦住（返回 false）→ 按"提交失败"降级交付 →
//	而刚提交的那一段照常跑、照常计费，产物被丢掉。
//
// 这组用例同时钉住另一件事：写入必须带上**抢段后的** PipelineClaimStage。
// UpdateWithStatus 是 Select("*") 整行写回，结构体里那个字段若是旧值，
// 会把刚抢到的号覆盖回去，下一轮又能抢一次 —— 重复提交、重复计费。

func srSucceededChild() *model.Task {
	sub := &model.Task{TaskID: "sub-sr"}
	sub.Status = model.TaskStatusSuccess
	sub.PrivateData.ResultURL = "obs://sr-product"
	return sub
}

func stubLookupForSync(t *testing.T) {
	t.Helper()
	orig := lookupTaskByPublicID
	t.Cleanup(func() { lookupTaskByPublicID = orig })
	lookupTaskByPublicID = func(publicID string) (*model.Task, bool, error) {
		if publicID == "sub-sr" {
			return srSucceededChild(), true, nil
		}
		// 刚提交的 v2a 子任务
		sub := &model.Task{TaskID: publicID}
		sub.PrivateData.UpstreamTaskID = "up-v2a"
		return sub, true, nil
	}
}

// 两段链：sr 成功 → 巡检推进 v2a → 且**落库**（Stage/子任务 id/抢段号三样都对）。
func TestPlaygroundSyncAdvancesAndPersists(t *testing.T) {
	withPipelineToken(t)
	calls, body := withFakeVideosEndpoint(t, 200, `{"task_id":"sub-v2a"}`)
	// **顺序要紧**：withFakeVideosEndpoint 自己会覆盖 lookupTaskByPublicID，
	// 必须让它先设、我们再覆盖回去，否则同步读到的子任务不是"成功的 sr 段"。
	stubLookupForSync(t)

	task := playgroundTask(srStage(), dubStage())
	task.Status = model.TaskStatusInProgress
	task.Progress = "70%"
	task.PrivateData.Aggregate.Stage = 2
	task.PrivateData.Aggregate.Stage2TaskID = "sub-sr"
	task.PipelineClaimStage = 2 // 第 2 段已抢过
	require.NoError(t, model.DB.Create(task).Error)

	SyncAggregatePipelineParents(context.Background(), []*model.Task{task})

	require.EqualValues(t, 1, *calls, "sr 成功后应提交 v2a，且只提交一次")
	require.Contains(t, string(*body), `"task_type":"v2a"`)
	require.Contains(t, string(*body), `"video":"task:sub-sr"`, "v2a 的输入是 sr 段的产物")

	var got model.Task
	require.NoError(t, model.DB.First(&got, task.ID).Error)
	require.Equal(t, 3, got.PrivateData.Aggregate.Stage, "推进必须落库，否则下一轮又推进一次")
	require.Equal(t, "sub-v2a", got.PrivateData.Aggregate.Stage2TaskID)
	require.Equal(t, 3, got.PipelineClaimStage,
		"写入必须带上抢段后的号：整行写回旧值会把 claim 覆盖回去，下一轮再抢一次 = 重复计费")
	require.Equal(t, model.TaskStatusInProgress, string(got.Status), "父任务仍在进行中，等待 v2a")
}

// 单段链：sr 成功后没有下一段（不许再提交），父任务正常收尾。
func TestPlaygroundSyncFinishesWhenChainEnds(t *testing.T) {
	withPipelineToken(t)
	calls, _ := withFakeVideosEndpoint(t, 200, `{"task_id":"should-not-happen"}`)
	stubLookupForSync(t)

	task := playgroundTask(srStage())
	task.Status = model.TaskStatusInProgress
	task.Progress = "90%"
	task.PrivateData.Aggregate.Stage = 2
	task.PrivateData.Aggregate.Stage2TaskID = "sub-sr"
	task.PipelineClaimStage = 2
	require.NoError(t, model.DB.Create(task).Error)

	SyncAggregatePipelineParents(context.Background(), []*model.Task{task})

	require.EqualValues(t, 0, *calls, "链尾不该再提交任何后置段")
}

// 兜底口径不变：子段失败 → 交付已有成品，不整单判失败。
//
// 这里断言的是"没有判失败、进入了落盘重试"，而不是"已经是 SUCCESS"：
// 落盘要媒体存储配置，测试环境没有，第一次失败会按既有逻辑留在进行中重试
// （maxPersistRetries 次后才判失败并退款）。
func TestPlaygroundSyncDegradesOnChildFailure(t *testing.T) {
	orig := lookupTaskByPublicID
	t.Cleanup(func() { lookupTaskByPublicID = orig })
	lookupTaskByPublicID = func(string) (*model.Task, bool, error) {
		sub := &model.Task{TaskID: "sub-sr"}
		sub.Status = model.TaskStatusFailure
		sub.FailReason = "upscale boom"
		return sub, true, nil
	}

	task := playgroundTask(srStage())
	task.Status = model.TaskStatusInProgress
	task.PrivateData.Aggregate.Stage = 2
	task.PrivateData.Aggregate.Stage2TaskID = "sub-sr"
	task.PrivateData.Aggregate.Stage1NFSPath = "/nfs/gen.mp4"
	require.NoError(t, model.DB.Create(task).Error)

	SyncAggregatePipelineParents(context.Background(), []*model.Task{task})

	var got model.Task
	require.NoError(t, model.DB.First(&got, task.ID).Error)
	require.NotEqual(t, model.TaskStatusFailure, got.Status,
		"子段失败不能把整单判失败（生成段的钱已经烧了、成品不能丢）")
	require.Equal(t, 1, got.PrivateData.PersistRetryCount, "应当进入落盘重试而不是丢弃成品")
}

var _ = common.PostStageKindV2A
