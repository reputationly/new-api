package service

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// 体验区的服务端流水线推进。
//
// 这些用例守的是"关掉页面也能跑完"这条承诺背后的状态机：
// 生成段完成 → 提交后置段 → 任务留在 InProgress 等子段 → 子段成功才收尾。
// 与聚合那条共用同一套收尾/降级逻辑，所以这里只钉**与聚合不同的部分**：
// 后置段是链、凭证来自内部令牌、Stage 会一路加上去。

func playgroundTask(stages ...common.PostStage) *model.Task {
	t := &model.Task{TaskID: "pg-1", ChannelId: 7, Platform: "24", Action: "t2v", UserId: 9, Group: "default"}
	t.PrivateData.UpstreamTaskID = "up-gen"
	t.PrivateData.Aggregate = &model.TaskAggregateInfo{
		Source:     model.TaskPipelineSourcePlayground,
		Stage:      1,
		PostStages: stages,
	}
	return t
}

func srStage() common.PostStage {
	ratio := 2.0
	return common.PostStage{Kind: common.PostStageKindSR, Model: "relayvsr", SRRatio: &ratio, TargetShortEdge: 1440}
}

func dubStage() common.PostStage {
	return common.PostStage{Kind: common.PostStageKindV2A, Model: "audioX"}
}

// withFakeClaim 用进程内的原子比较并交换冒充库上的 CAS。
// 真实实现见 model.ClaimPipelineStage（单条 UPDATE ... WHERE pipeline_claim_stage < ?）。
func withFakeClaim(t *testing.T) {
	t.Helper()
	orig := claimPipelineStage
	t.Cleanup(func() { claimPipelineStage = orig })
	var claimed int32
	claimPipelineStage = func(taskID int64, stage int) (bool, error) {
		cur := atomic.LoadInt32(&claimed)
		for {
			if int32(stage) <= cur {
				return false, nil
			}
			if atomic.CompareAndSwapInt32(&claimed, cur, int32(stage)) {
				return true, nil
			}
			cur = atomic.LoadInt32(&claimed)
		}
	}
}

// withPipelineToken 让推进逻辑拿到一个假令牌（真实实现要落库）。
func withPipelineToken(t *testing.T) {
	t.Helper()
	orig := pipelineTokenForTask
	t.Cleanup(func() { pipelineTokenForTask = orig })
	pipelineTokenForTask = func(userId int, group string, models []string) (*model.Token, error) {
		return &model.Token{UserId: userId, Group: group, Key: "sk-internal"}, nil
	}
}

// 生成段完成 → 提交超分段 → 任务留在进行中、记住等哪条子任务。
func TestPlaygroundAdvancesToSr(t *testing.T) {
	withPipelineToken(t)
	withFakeClaim(t)
	calls, body := withFakeVideosEndpoint(t, 200, `{"task_id":"pub-2"}`)
	task := playgroundTask(srStage())

	require.True(t, TryAdvanceAggregatePipeline(context.Background(), nil, task, stage1Result(), "/nfs-output/t2v/a.mp4"))

	agg := task.PrivateData.Aggregate
	require.Equal(t, 2, agg.Stage)
	require.Equal(t, "sub-public-1", agg.Stage2TaskID)
	require.Equal(t, "/nfs-output/t2v/a.mp4", agg.Stage1NFSPath, "降级交付要用生成段产物")
	require.EqualValues(t, 1, *calls, "只提交一次")
	// 请求体与体验区浏览器版本逐字段对齐：换人提交不该顺手改语义。
	require.Contains(t, string(*body), `"task_type":"sr"`)
	require.Contains(t, string(*body), `"sr_ratio":2`)
	require.Contains(t, string(*body), `"target_short_edge":1440`)
	require.Contains(t, string(*body), `"model":"relayvsr"`)
	// 父任务保留自己的轮询身份（换成子任务的会让父任务永远不被轮询）。
	require.Equal(t, "up-gen", task.PrivateData.UpstreamTaskID)
	require.Equal(t, 7, task.ChannelId)
}

// 只有一段时，推进过一次之后就再没有下一段 —— 这是重复提交/重复计费的防线。
func TestPlaygroundNeverAdvancesTwice(t *testing.T) {
	withPipelineToken(t)
	withFakeClaim(t)
	calls, _ := withFakeVideosEndpoint(t, 200, `{"task_id":"pub-2"}`)
	task := playgroundTask(srStage())

	require.True(t, TryAdvanceAggregatePipeline(context.Background(), nil, task, stage1Result(), "/nfs/a.mp4"))
	require.False(t, TryAdvanceAggregatePipeline(context.Background(), nil, task, stage1Result(), "/nfs/a.mp4"),
		"已经到链尾，不得再提交")
	require.EqualValues(t, 1, *calls, "后置段只能提交一次")
}

// 两段链：sr 完成后再推进 v2a，且 v2a 的输入是 `task:<sr 子任务>`。
func TestPlaygroundAdvancesToDubWithChildReference(t *testing.T) {
	withPipelineToken(t)
	withFakeClaim(t)
	calls, body := withFakeVideosEndpoint(t, 200, `{"task_id":"pub-3"}`)
	task := playgroundTask(srStage(), dubStage())
	task.PrivateData.Aggregate.Stage = 2
	task.PrivateData.Aggregate.Stage2TaskID = "sub-sr-1"

	require.True(t, TryAdvanceAggregatePipeline(context.Background(), nil, task, nil, ""))

	agg := task.PrivateData.Aggregate
	require.Equal(t, 3, agg.Stage)
	require.EqualValues(t, 1, *calls)
	require.Contains(t, string(*body), `"task_type":"v2a"`)
	// 上一段已成功，用它的任务 id 引用产物；父任务自己的 ResultURL 这时还是空的。
	require.Contains(t, string(*body), `"video":"task:sub-sr-1"`)
	// 生成段的计费依据只在第一次推进时留，这里不该被覆盖。
	require.Equal(t, 0, agg.Stage1BillableTokens)
}

// 提交失败：不改状态、不落计费依据，交给调用方按现有成品收尾（不判失败）。
func TestPlaygroundSubmitFailureKeepsTaskSettleable(t *testing.T) {
	withPipelineToken(t)
	withFakeClaim(t)
	calls, _ := withFakeVideosEndpoint(t, 502, `{"error":{"message":"bad gateway"}}`)
	task := playgroundTask(srStage())

	require.False(t, TryAdvanceAggregatePipeline(context.Background(), nil, task, stage1Result(), "/nfs/a.mp4"))

	agg := task.PrivateData.Aggregate
	require.Equal(t, 1, agg.Stage, "提交失败不得把 Stage 改掉")
	require.Empty(t, agg.Stage2TaskID)
	require.EqualValues(t, 1, *calls)
}

// 没有产物可作输入时直接跳过推进（与聚合同口径），而不是把任务挂死。
func TestPlaygroundSkipsWhenNoProduct(t *testing.T) {
	withPipelineToken(t)
	withFakeClaim(t)
	calls, _ := withFakeVideosEndpoint(t, 200, `{"task_id":"pub-2"}`)
	task := playgroundTask(srStage())

	require.False(t, TryAdvanceAggregatePipeline(context.Background(), nil, task, stage1Result(), "  "))
	require.EqualValues(t, 0, *calls)
}

// 没有声明后置段的任务完全不受影响（老行为）。
func TestPlaygroundNoStagesIsNoop(t *testing.T) {
	calls, _ := withFakeVideosEndpoint(t, 200, `{"task_id":"pub-2"}`)
	task := playgroundTask()
	require.False(t, TryAdvanceAggregatePipeline(context.Background(), nil, task, stage1Result(), "/nfs/a.mp4"))
	require.EqualValues(t, 0, *calls)
}

// 并发审批：两个 goroutine 同时推进同一条任务，只允许提交一次。
//
// 这条守住的是**重复计费**。实现上靠的是 Stage 前进后 NextPostStage 立即返回 false
// （单段链），以及落库时的状态守卫（UpdateWithStatus）；这里钉住前半段。
func TestPlaygroundConcurrentAdvanceSubmitsOnce(t *testing.T) {
	withPipelineToken(t)
	withFakeClaim(t)
	calls, _ := withFakeVideosEndpoint(t, 200, `{"task_id":"pub-2"}`)
	task := playgroundTask(srStage())

	var advanced int32
	done := make(chan struct{})
	for i := 0; i < 2; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			if TryAdvanceAggregatePipeline(context.Background(), nil, task, stage1Result(), "/nfs/a.mp4") {
				atomic.AddInt32(&advanced, 1)
			}
		}()
	}
	<-done
	<-done

	require.EqualValues(t, 1, *calls, "同一条任务的后置段只能被提交一次，否则重复计费")
}
