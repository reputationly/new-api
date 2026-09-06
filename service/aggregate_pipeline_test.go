package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/model"
)

// pipelineTask 造一条处于生成段、配了超分的聚合任务。
func pipelineTask() *model.Task {
	t := &model.Task{TaskID: "pub-1", ChannelId: 7}
	t.PrivateData.UpstreamTaskID = "up-stage1"
	t.PrivateData.Aggregate = &model.TaskAggregateInfo{
		PublicModel:   "h3-2k",
		Stage:         1,
		UpscaleModel:  "seedvr2",
		UpscaleTarget: "2k",
		CallerKey:     "sk-customer",
	}
	return t
}

// withFakeVideosEndpoint 把超分提交指向本地假服务端,并让回查返回一条子任务。
// 返回提交次数计数器与最后一次收到的请求体。
func withFakeVideosEndpoint(t *testing.T, status int, respBody string) (*int32, *[]byte) {
	t.Helper()
	var calls int32
	var lastBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		lastBody = buf
		w.WriteHeader(status)
		_, _ = w.Write([]byte(respBody))
	}))
	t.Cleanup(srv.Close)

	origEP, origLookup := videosEndpoint, lookupTaskByPublicID
	t.Cleanup(func() { videosEndpoint, lookupTaskByPublicID = origEP, origLookup })
	videosEndpoint = func() string { return srv.URL }
	lookupTaskByPublicID = func(id string) (*model.Task, bool, error) {
		sub := &model.Task{ChannelId: 42}
		sub.PrivateData.UpstreamTaskID = "up-stage2"
		return sub, true, nil
	}
	return &calls, &lastBody
}

// 生成段完成 → 提交超分 → 任务改挂第二段,状态留在进行中。
func TestPipelineAdvancesToUpscale(t *testing.T) {
	calls, body := withFakeVideosEndpoint(t, http.StatusOK, `{"task_id":"pub-2"}`)
	task := pipelineTask()

	require.True(t, TryAdvanceAggregatePipeline(context.Background(), task, "/nfs-output/t2v/x.mp4"))

	agg := task.PrivateData.Aggregate
	require.Equal(t, 2, agg.Stage)
	require.Equal(t, "up-stage2", task.PrivateData.UpstreamTaskID, "轮询要按第二段的上游 id 去问渠道")
	require.Equal(t, 42, task.ChannelId, "承接第二段的可能是另一个渠道")
	require.Equal(t, "up-stage1", agg.Stage1TaskID, "第一段的上游 id 要留住,否则排障时找不回")
	require.Empty(t, agg.CallerKey, "令牌用完即擦,没有理由继续留在库里")
	require.EqualValues(t, 1, *calls)
	// 输入用 NFS 路径直传,不把几十 MB 的产物读出来再发一遍。
	require.Contains(t, string(*body), "/nfs-output/t2v/x.mp4")
	require.Contains(t, string(*body), "\"task_type\":\"sr\"")
}

// **已在第二段的任务绝不能再次推进** —— 这是重复提交与重复计费的防线。
//
// 曾经踩过:推进后用 return 提前返回,跳过了 switch 之后的落库,于是 Stage=2 没存下,
// 下一轮轮询仍按旧上游 id 查到生成段 completed,又提交一次超分,循环往复。
func TestPipelineNeverAdvancesTwice(t *testing.T) {
	calls, _ := withFakeVideosEndpoint(t, http.StatusOK, `{"task_id":"pub-2"}`)
	task := pipelineTask()

	require.True(t, TryAdvanceAggregatePipeline(context.Background(), task, "/nfs/x.mp4"))
	// 模拟下一轮轮询又拿到一次"生成段 completed"
	require.False(t, TryAdvanceAggregatePipeline(context.Background(), task, "/nfs/x.mp4"),
		"已进入超分段的任务不得再次提交")
	require.EqualValues(t, 1, *calls, "超分只能被提交一次,否则重复计费")
}

// 单独钉住 Stage 判定本身。
//
// 上一条用的是"推进两次"的路径,但那条路第二次其实是被**擦掉令牌**的副作用挡住的
// (CallerKey 已清空 → 缺少调用者身份),Stage 判定并没有真正被考验到。这里构造一个
// Stage=2 但令牌仍在的任务 —— 只有 Stage 判定能拦住它。
// 将来若有人为了重试而保留令牌,这条会立刻发现 Stage 防线是否还在。
func TestPipelineStageGuardStandsAlone(t *testing.T) {
	calls, _ := withFakeVideosEndpoint(t, http.StatusOK, `{"task_id":"pub-3"}`)
	task := pipelineTask()
	task.PrivateData.Aggregate.Stage = 2 // 已在超分段
	// 令牌仍在:唯一能拦住它的只有 Stage 判定。

	require.False(t, TryAdvanceAggregatePipeline(context.Background(), task, "/nfs/x.mp4"),
		"已在超分段的任务必须被 Stage 判定拦住,不能依赖擦令牌那个副作用")
	require.EqualValues(t, 0, *calls, "不该发出任何提交请求")
}

// 没有超分段的聚合任务按普通任务收尾。
func TestPipelineSkipsWhenNoUpscale(t *testing.T) {
	calls, _ := withFakeVideosEndpoint(t, http.StatusOK, `{"task_id":"pub-2"}`)
	task := pipelineTask()
	task.PrivateData.Aggregate.UpscaleModel = ""

	require.False(t, TryAdvanceAggregatePipeline(context.Background(), task, "/nfs/x.mp4"))
	require.EqualValues(t, 0, *calls)
}

// 非聚合任务不受影响(Aggregate 为 nil,不得 panic)。
func TestPipelineIgnoresNonAggregateTask(t *testing.T) {
	task := &model.Task{TaskID: "plain"}
	require.False(t, TryAdvanceAggregatePipeline(context.Background(), task, "/nfs/x.mp4"))
}

// 超分提交失败 → 不推进,调用方继续正常收尾,客户拿到未超分的成品。
// 生成段已经烧掉 GPU 且已计费,判整单失败要退这笔钱,我们白亏一次算力;
// 而交付一个分辨率低些的成品,比交付一个错误强得多。
func TestPipelineDeliversStage1WhenUpscaleSubmitFails(t *testing.T) {
	_, _ = withFakeVideosEndpoint(t, http.StatusInternalServerError, `{"error":"boom"}`)
	task := pipelineTask()

	require.False(t, TryAdvanceAggregatePipeline(context.Background(), task, "/nfs/x.mp4"))
	require.Equal(t, 1, task.PrivateData.Aggregate.Stage, "提交失败不该改变流水线状态")
	require.Equal(t, "up-stage1", task.PrivateData.UpstreamTaskID, "上游 id 不该被改动")
}

// 缺产物路径 / 缺调用者身份都不推进,而不是把一个已完成的任务判失败。
func TestPipelineSkipsOnMissingInputs(t *testing.T) {
	t.Run("无 nfs 路径", func(t *testing.T) {
		calls, _ := withFakeVideosEndpoint(t, http.StatusOK, `{"task_id":"pub-2"}`)
		require.False(t, TryAdvanceAggregatePipeline(context.Background(), pipelineTask(), "  "))
		require.EqualValues(t, 0, *calls)
	})
	t.Run("无调用者身份", func(t *testing.T) {
		calls, _ := withFakeVideosEndpoint(t, http.StatusOK, `{"task_id":"pub-2"}`)
		task := pipelineTask()
		task.PrivateData.Aggregate.CallerKey = ""
		require.False(t, TryAdvanceAggregatePipeline(context.Background(), task, "/nfs/x.mp4"))
		require.EqualValues(t, 0, *calls)
	})
}

// 提交超分要带客户身份 —— 第二段的计费同样落在客户账上(分段计费)。
func TestPipelineSubmitsAsCaller(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"task_id":"pub-2"}`))
	}))
	t.Cleanup(srv.Close)
	origEP, origLookup := videosEndpoint, lookupTaskByPublicID
	t.Cleanup(func() { videosEndpoint, lookupTaskByPublicID = origEP, origLookup })
	videosEndpoint = func() string { return srv.URL }
	lookupTaskByPublicID = func(string) (*model.Task, bool, error) {
		sub := &model.Task{}
		sub.PrivateData.UpstreamTaskID = "up-stage2"
		return sub, true, nil
	}

	TryAdvanceAggregatePipeline(context.Background(), pipelineTask(), "/nfs/x.mp4")

	require.Equal(t, "Bearer sk-customer", gotAuth)
}

// 回查不到子任务时不推进:拿不到第二段的上游 id,轮询就无从跟进,
// 硬推进等于把任务挂死在一个查不到的 id 上。
func TestPipelineSkipsWhenSubTaskLookupFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"task_id":"pub-2"}`))
	}))
	t.Cleanup(srv.Close)
	origEP, origLookup := videosEndpoint, lookupTaskByPublicID
	t.Cleanup(func() { videosEndpoint, lookupTaskByPublicID = origEP, origLookup })
	videosEndpoint = func() string { return srv.URL }
	lookupTaskByPublicID = func(string) (*model.Task, bool, error) { return nil, false, nil }

	task := pipelineTask()
	require.False(t, TryAdvanceAggregatePipeline(context.Background(), task, "/nfs/x.mp4"))
	require.Equal(t, 1, task.PrivateData.Aggregate.Stage)
}
