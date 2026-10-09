package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// withModerationStub 替换审核钩子,结束时还原并恢复本轮预算。
func withModerationStub(t *testing.T, blocked bool) {
	t.Helper()
	origFn := ModerateTaskOutputFunc
	origSpent := outputModerationSpent
	t.Cleanup(func() {
		ModerateTaskOutputFunc = origFn
		outputModerationSpent = origSpent
	})
	resetOutputModerationBudget()
	ModerateTaskOutputFunc = func(context.Context, *model.Task, string, string) (bool, string) {
		return blocked, "test"
	}
}

// 推进成功 → 生成段成品落盘后挂到聚合信息上,供日志里生成段那一行下载。
func TestAdvanceExposesStage1ProductForLogWhenPersisted(t *testing.T) {
	withFakeVideosEndpoint(t, http.StatusOK, `{"task_id":"pub-2"}`)
	withPersistResult(t, "obs://stage1/x.mp4", true)
	withModerationStub(t, false)
	task := pipelineTask()

	require.True(t, TryAdvanceAggregatePipeline(context.Background(), nil, task,
		&relaycommon.TaskInfo{Status: model.TaskStatusSuccess}, "/nfs/x.mp4"))

	require.Equal(t, "obs://stage1/x.mp4", task.PrivateData.Aggregate.Stage1ResultURL)
}

// 落盘失败 → 任务照常推进,生成段那一行只是不提供下载。
func TestAdvanceLeavesStage1ProductEmptyWhenPersistFails(t *testing.T) {
	withFakeVideosEndpoint(t, http.StatusOK, `{"task_id":"pub-2"}`)
	withPersistResult(t, "", false)
	withModerationStub(t, false)
	task := pipelineTask()

	require.True(t, TryAdvanceAggregatePipeline(context.Background(), nil, task, stage1Result(), "/nfs/x.mp4"))

	require.Empty(t, task.PrivateData.Aggregate.Stage1ResultURL, "落盘失败时不得挂出空的或旧的地址")
	require.EqualValues(t, 2, task.PrivateData.Aggregate.Stage, "落盘失败不影响流水线推进")
}

// 被内容审核拦截的生成段成品不得对外 —— 与主路径同口径。
func TestAdvanceDoesNotExposeBlockedStage1Product(t *testing.T) {
	withFakeVideosEndpoint(t, http.StatusOK, `{"task_id":"pub-2"}`)
	withPersistResult(t, "obs://stage1/x.mp4", true)
	withModerationStub(t, true)
	task := pipelineTask()

	require.True(t, TryAdvanceAggregatePipeline(context.Background(), nil, task, stage1Result(), "/nfs/x.mp4"))

	require.Empty(t, task.PrivateData.Aggregate.Stage1ResultURL, "被审核拦截的生成段成品不得挂出")
}

// 超分提交失败 → 没有推进,就不挂生成段成品:主路径会照常落盘交付这份成品。
func TestAdvanceSkipsStage1ProductWhenSubmitFails(t *testing.T) {
	withFakeVideosEndpoint(t, http.StatusInternalServerError, `{"error":"boom"}`)
	withPersistResult(t, "obs://stage1/x.mp4", true)
	withModerationStub(t, false)
	task := pipelineTask()

	require.False(t, TryAdvanceAggregatePipeline(context.Background(), nil, task, stage1Result(), "/nfs/x.mp4"))

	require.Empty(t, task.PrivateData.Aggregate.Stage1ResultURL)
	require.EqualValues(t, 1, task.PrivateData.Aggregate.Stage, "没有推进就仍在生成段")
}
