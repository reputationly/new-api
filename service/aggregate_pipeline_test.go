package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// stage1Result 生成段的上游回执:带着**生成段自己的**分辨率与用量。
// 父任务的最终价钱要按它算,不能按超分段的回执算。
func stage1Result() *relaycommon.TaskInfo {
	return &relaycommon.TaskInfo{TotalTokens: 720, Status: model.TaskStatusSuccess}
}

// pipelineTask 造一条处于生成段、配了超分的聚合任务。
func pipelineTask() *model.Task {
	t := &model.Task{TaskID: "pub-1", ChannelId: 7, Platform: "24", Action: "t2v"}
	t.PrivateData.UpstreamTaskID = "up-stage1"
	// 生成段是 Gemini/Vertex 那类会写 PrivateData.Key 的渠道。推进到第二段时它
	// **不该被改动** —— 父任务保留自己的轮询身份(见 TryAdvanceAggregatePipeline),
	// Key 一并保留。
	t.PrivateData.Key = "stage1-channel-key"
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
		sub := &model.Task{TaskID: "sub-public-1", ChannelId: 42, Platform: "59", Action: "sr"}
		sub.PrivateData.UpstreamTaskID = "up-stage2"
		return sub, true, nil
	}
	return &calls, &lastBody
}

// 生成段完成 → 提交超分 → 任务改挂第二段,状态留在进行中。
func TestPipelineAdvancesToUpscale(t *testing.T) {
	calls, body := withFakeVideosEndpoint(t, http.StatusOK, `{"task_id":"pub-2"}`)
	task := pipelineTask()

	require.True(t, TryAdvanceAggregatePipeline(context.Background(), nil, task, stage1Result(), "/nfs-output/t2v/x.mp4"))

	agg := task.PrivateData.Aggregate
	require.Equal(t, 2, agg.Stage)
	require.Equal(t, "sub-public-1", agg.Stage2TaskID, "要记住等哪条子任务")
	require.Equal(t, "/nfs-output/t2v/x.mp4", agg.Stage1NFSPath, "超分失败时要能降级交付它")
	require.Empty(t, agg.CallerKey, "令牌用完即擦,没有理由继续留在库里")
	// **父任务保留自己的轮询身份**:换成子任务的会在 taskM[upstreamID] 上撞 key,
	// 后来者(id 更大的子任务)覆盖父任务,父任务永远不被轮询、卡到超时退款。
	require.Equal(t, "up-stage1", task.PrivateData.UpstreamTaskID, "不得改挂子任务的上游 id")
	require.Equal(t, 7, task.ChannelId, "不得改挂子任务的渠道")
	require.EqualValues(t, "24", task.Platform, "不得改挂子任务的平台")
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

	require.True(t, TryAdvanceAggregatePipeline(context.Background(), nil, task, stage1Result(), "/nfs/x.mp4"))
	// 模拟下一轮轮询又拿到一次"生成段 completed"
	require.False(t, TryAdvanceAggregatePipeline(context.Background(), nil, task, stage1Result(), "/nfs/x.mp4"),
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

	require.False(t, TryAdvanceAggregatePipeline(context.Background(), nil, task, stage1Result(), "/nfs/x.mp4"),
		"已在超分段的任务必须被 Stage 判定拦住,不能依赖擦令牌那个副作用")
	require.EqualValues(t, 0, *calls, "不该发出任何提交请求")
}

// 没有超分段的聚合任务按普通任务收尾。
func TestPipelineSkipsWhenNoUpscale(t *testing.T) {
	calls, _ := withFakeVideosEndpoint(t, http.StatusOK, `{"task_id":"pub-2"}`)
	task := pipelineTask()
	task.PrivateData.Aggregate.UpscaleModel = ""

	require.False(t, TryAdvanceAggregatePipeline(context.Background(), nil, task, stage1Result(), "/nfs/x.mp4"))
	require.EqualValues(t, 0, *calls)
}

// 非聚合任务不受影响(Aggregate 为 nil,不得 panic)。
func TestPipelineIgnoresNonAggregateTask(t *testing.T) {
	task := &model.Task{TaskID: "plain"}
	require.False(t, TryAdvanceAggregatePipeline(context.Background(), nil, task, stage1Result(), "/nfs/x.mp4"))
}

// 超分提交失败 → 不推进,调用方继续正常收尾,客户拿到未超分的成品。
// 生成段已经烧掉 GPU 且已计费,判整单失败要退这笔钱,我们白亏一次算力;
// 而交付一个分辨率低些的成品,比交付一个错误强得多。
func TestPipelineDeliversStage1WhenUpscaleSubmitFails(t *testing.T) {
	_, _ = withFakeVideosEndpoint(t, http.StatusInternalServerError, `{"error":"boom"}`)
	task := pipelineTask()

	require.False(t, TryAdvanceAggregatePipeline(context.Background(), nil, task, stage1Result(), "/nfs/x.mp4"))
	require.Equal(t, 1, task.PrivateData.Aggregate.Stage, "提交失败不该改变流水线状态")
	require.Equal(t, "up-stage1", task.PrivateData.UpstreamTaskID, "上游 id 不该被改动")
}

// 缺产物路径 / 缺调用者身份都不推进,而不是把一个已完成的任务判失败。
func TestPipelineSkipsOnMissingInputs(t *testing.T) {
	t.Run("无 nfs 路径", func(t *testing.T) {
		calls, _ := withFakeVideosEndpoint(t, http.StatusOK, `{"task_id":"pub-2"}`)
		require.False(t, TryAdvanceAggregatePipeline(context.Background(), nil, pipelineTask(), stage1Result(), "  "))
		require.EqualValues(t, 0, *calls)
	})
	t.Run("无调用者身份", func(t *testing.T) {
		calls, _ := withFakeVideosEndpoint(t, http.StatusOK, `{"task_id":"pub-2"}`)
		task := pipelineTask()
		task.PrivateData.Aggregate.CallerKey = ""
		require.False(t, TryAdvanceAggregatePipeline(context.Background(), nil, task, stage1Result(), "/nfs/x.mp4"))
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
		sub := &model.Task{TaskID: "sub-public-1", Platform: "59"}
		sub.PrivateData.UpstreamTaskID = "up-stage2"
		return sub, true, nil
	}

	TryAdvanceAggregatePipeline(context.Background(), nil, pipelineTask(), stage1Result(), "/nfs/x.mp4")

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
	require.False(t, TryAdvanceAggregatePipeline(context.Background(), nil, task, stage1Result(), "/nfs/x.mp4"))
	require.Equal(t, 1, task.PrivateData.Aggregate.Stage)
}

// 子任务还没拿到上游 id 时不推进:没有上游 id 就无从轮询,推进等于把任务挂死。
func TestPipelineSkipsWhenSubTaskHasNoUpstreamID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"task_id":"pub-2"}`))
	}))
	t.Cleanup(srv.Close)
	origEP, origLookup := videosEndpoint, lookupTaskByPublicID
	t.Cleanup(func() { videosEndpoint, lookupTaskByPublicID = origEP, origLookup })
	videosEndpoint = func() string { return srv.URL }
	lookupTaskByPublicID = func(string) (*model.Task, bool, error) {
		return &model.Task{TaskID: "sub-public-1", ChannelId: 42}, true, nil // 无 UpstreamTaskID
	}

	task := pipelineTask()
	require.False(t, TryAdvanceAggregatePipeline(context.Background(), nil, task, stage1Result(), "/nfs/x.mp4"))
	require.Equal(t, 1, task.PrivateData.Aggregate.Stage)
	require.Equal(t, "up-stage1", task.PrivateData.UpstreamTaskID)
}

// fakeSettleAdaptor 只用来观察 AdjustBillingOnComplete 有没有被调用。
type fakeSettleAdaptor struct{ adjustCalled bool }

func (f *fakeSettleAdaptor) Init(*relaycommon.RelayInfo) {}
func (f *fakeSettleAdaptor) FetchTask(string, string, map[string]any, string) (*http.Response, error) {
	return nil, nil
}
func (f *fakeSettleAdaptor) ParseTaskResult([]byte) (*relaycommon.TaskInfo, error) { return nil, nil }
func (f *fakeSettleAdaptor) AdjustBillingOnComplete(*model.Task, *relaycommon.TaskInfo) int {
	f.adjustCalled = true
	return 123
}

// 聚合父任务在超分段完成时,**不得按超分段的用量重算价钱**。
//
// 走到这一步时 taskResult 是超分段的回执(token 数、分辨率都是超分后的),而任务冻结的
// BillingContext 描述的是生成段。照常重算 = 拿生成段的模型去查超分后分辨率的价,
// 生成段被按 2K 档收一次、超分子任务自己再按 SR 模型收一次 —— 同一档付了两遍。
// 父任务代表生成段,价钱在生成段完成时就该定死。
func TestSettleSkipsUsageRecalcForPipelineParent(t *testing.T) {
	// 结算路径会往共享的测试库写日志/额度。不清理的话残留会让后面按
	// countLogs==0 断言的用例(task_billing_test.go)莫名其妙地失败 ——
	// 而且只在"一起跑"时复现,单独跑永远是绿的,最难查的那种测试间干扰。
	truncate(t)
	fake := &fakeSettleAdaptor{}
	task := pipelineTask()
	task.PrivateData.Aggregate.Stage = 2 // 已在超分段,最终回执来自超分

	settleTaskBillingOnComplete(context.Background(), fake, task,
		&relaycommon.TaskInfo{TotalTokens: 99999})

	require.False(t, fake.adjustCalled,
		"父任务不该采用超分段的用量,连 adaptor 的计费调整都不该走到")
}

// 推进时必须把**生成段回执**的计费依据留下来。
//
// 父任务代表生成段,但它到达成功态时手上只剩超分段的回执。两种偷懒都错:拿超分回执算 →
// 生成段被按超分后的分辨率计费;什么都不算直接回退预扣 → 预扣只是粗略锚点,视频计费
// 矩阵的单价只在结算侧才查得出来,等于按不含分辨率/时长维度的 ModelRatio 收费。
func TestPipelineRecordsStage1BillingBasis(t *testing.T) {
	withFakeVideosEndpoint(t, http.StatusOK, `{"task_id":"pub-2"}`)
	task := pipelineTask()
	fake := &fakeSettleAdaptor{}

	require.True(t, TryAdvanceAggregatePipeline(context.Background(), fake, task,
		&relaycommon.TaskInfo{TotalTokens: 720, Status: model.TaskStatusSuccess}, "/nfs/x.mp4"))

	agg := task.PrivateData.Aggregate
	require.Equal(t, 720, agg.Stage1TotalTokens, "生成段的 token 用量要留住")
	require.Equal(t, 720, agg.Stage1BillableTokens)
	require.Equal(t, 123, agg.Stage1AdaptorQuota, "adaptor 给出的额度也要留住(它优先级最高)")
}

// 结算时应当采用留下来的生成段依据,而不是回退到预扣锚点。
func TestSettleUsesRecordedStage1Basis(t *testing.T) {
	truncate(t)
	task := pipelineTask()
	task.PrivateData.Aggregate.Stage = 2
	task.PrivateData.Aggregate.Stage1AdaptorQuota = 4321

	// 传一份"超分段"的回执:它带着完全不同的用量,不该被采用。
	settleTaskBillingOnComplete(context.Background(), &fakeSettleAdaptor{}, task,
		&relaycommon.TaskInfo{TotalTokens: 99999})

	require.Equal(t, 4321, task.Quota,
		"应按推进时留下的生成段额度结算,而不是超分回执、也不是预扣锚点")
}

// 非流水线任务照常走原有结算路径 —— 上面的跳过只针对聚合父任务,不能误伤普通任务。
func TestSettleUnaffectedForNormalTask(t *testing.T) {
	truncate(t)
	fake := &fakeSettleAdaptor{}
	task := &model.Task{TaskID: "plain"}

	settleTaskBillingOnComplete(context.Background(), fake, task,
		&relaycommon.TaskInfo{TotalTokens: 100})

	require.True(t, fake.adjustCalled, "普通任务的结算路径不该被改变")
}

// withSubTaskStatus 让回查返回一条指定状态的子任务。
func withSubTaskStatus(t *testing.T, status model.TaskStatus, resultURL string) {
	t.Helper()
	orig := lookupTaskByPublicID
	t.Cleanup(func() { lookupTaskByPublicID = orig })
	lookupTaskByPublicID = func(string) (*model.Task, bool, error) {
		sub := &model.Task{TaskID: "sub-public-1", Status: status, FailReason: "boom"}
		sub.PrivateData.ResultURL = resultURL
		return sub, true, nil
	}
}

// 等待中的父任务要能被识别出来并从上游轮询里分流 —— 它的生成段早就完成了,
// 再送去问渠道只会一轮轮拿到同一个 completed。
func TestIsAggregatePipelineParent(t *testing.T) {
	waiting := pipelineTask()
	waiting.PrivateData.Aggregate.Stage = 2
	waiting.PrivateData.Aggregate.Stage2TaskID = "sub-public-1"
	require.True(t, IsAggregatePipelineParent(waiting))

	// 还在生成段的、以及普通任务,都要照常走上游轮询。
	require.False(t, IsAggregatePipelineParent(pipelineTask()))
	require.False(t, IsAggregatePipelineParent(&model.Task{TaskID: "plain"}))
}

// 子任务成功 → 父任务挂上超分产物并收尾。
func TestSyncParentAdoptsUpscaledResult(t *testing.T) {
	truncate(t)
	withSubTaskStatus(t, model.TaskStatusSuccess, "obs://upscaled/x.mp4")
	task := pipelineTask()
	task.PrivateData.Aggregate.Stage = 2
	task.PrivateData.Aggregate.Stage2TaskID = "sub-public-1"

	SyncAggregatePipelineParents(context.Background(), []*model.Task{task})

	require.Equal(t, "obs://upscaled/x.mp4", task.PrivateData.ResultURL,
		"父任务交付的应是超分后的成品")
	require.EqualValues(t, model.TaskStatusSuccess, task.Status)
	require.Equal(t, "100%", task.Progress)
}

// withPersistResult 控制落盘成败。
func withPersistResult(t *testing.T, ref string, ok bool) {
	t.Helper()
	orig := persistTaskResult
	t.Cleanup(func() { persistTaskResult = orig })
	persistTaskResult = func(context.Context, *model.Task, string, string) (string, bool) {
		return ref, ok
	}
}

// 子任务失败 → **降级交付生成段成品**,而不是把整单判失败。
// 生成段已经真实烧掉 GPU 且已计费,不该因为第二段失败连成品也丢掉;
// 客户按分段计费只被扣了生成那笔,没吃亏。
func TestSyncParentFallsBackToStage1OnUpscaleFailure(t *testing.T) {
	truncate(t)
	withSubTaskStatus(t, model.TaskStatusFailure, "")
	withPersistResult(t, "obs://stage1/x.mp4", true)
	task := pipelineTask()
	task.PrivateData.Aggregate.Stage = 2
	task.PrivateData.Aggregate.Stage2TaskID = "sub-public-1"
	task.PrivateData.Aggregate.Stage1NFSPath = "/nfs-output/t2v/x.mp4"

	SyncAggregatePipelineParents(context.Background(), []*model.Task{task})

	require.EqualValues(t, model.TaskStatusSuccess, task.Status,
		"超分失败不该把已经产出成品的整单判失败")
	require.Equal(t, "obs://stage1/x.mp4", task.PrivateData.ResultURL,
		"降级时交付的是生成段的成品")
}

// **拿不到产物引用时绝不置成功。**
//
// 状态一旦进终态,父任务就离开未完成集合,再也不会被 SyncAggregatePipelineParents
// 看到 —— 客户拿到一个"成功"但 url 为空、无法恢复的任务,而生成段的钱照收。
// 与主轮询路径同口径:先留在进行中重试。
func TestSyncParentRetriesWhenNoArtifact(t *testing.T) {
	truncate(t)
	withSubTaskStatus(t, model.TaskStatusFailure, "")
	withPersistResult(t, "", false) // 落盘失败
	task := pipelineTask()
	task.Status = model.TaskStatusInProgress
	task.PrivateData.Aggregate.Stage = 2
	task.PrivateData.Aggregate.Stage2TaskID = "sub-public-1"
	task.PrivateData.Aggregate.Stage1NFSPath = "/nfs-output/t2v/x.mp4"

	SyncAggregatePipelineParents(context.Background(), []*model.Task{task})

	require.EqualValues(t, model.TaskStatusInProgress, task.Status,
		"拿不到产物时必须留在进行中等重试,不能置成功")
	require.Empty(t, task.PrivateData.ResultURL)
	require.Equal(t, 1, task.PrivateData.PersistRetryCount, "重试次数要计数")
}

// 重试超过上限 → 判失败并退款,而不是无限期挂着。
func TestSyncParentFailsAndRefundsAfterRetryLimit(t *testing.T) {
	truncate(t)
	withSubTaskStatus(t, model.TaskStatusFailure, "")
	withPersistResult(t, "", false)
	task := pipelineTask()
	task.Status = model.TaskStatusInProgress
	task.PrivateData.PersistRetryCount = maxPersistRetries // 已到上限
	task.PrivateData.Aggregate.Stage = 2
	task.PrivateData.Aggregate.Stage2TaskID = "sub-public-1"
	task.PrivateData.Aggregate.Stage1NFSPath = "/nfs-output/t2v/x.mp4"

	SyncAggregatePipelineParents(context.Background(), []*model.Task{task})

	require.EqualValues(t, model.TaskStatusFailure, task.Status)
	require.NotEmpty(t, task.FailReason, "失败要留下可行动的原因")
}

// 判失败时必须**把钱退回去**。
//
// 只断言状态是不够的:客户没拿到任何产物,却被扣了生成段的钱,这是最不能接受的结局。
func TestSyncParentRefundsWhenGivingUp(t *testing.T) {
	truncate(t)
	const uid, initQuota, taskQuota = 60, 10000, 3000
	seedUser(t, uid, initQuota)
	withSubTaskStatus(t, model.TaskStatusFailure, "")
	withPersistResult(t, "", false)

	task := pipelineTask()
	task.UserId = uid
	task.Quota = taskQuota
	task.Status = model.TaskStatusInProgress
	task.PrivateData.BillingSource = BillingSourceWallet
	task.PrivateData.PersistRetryCount = maxPersistRetries
	task.PrivateData.Aggregate.Stage = 2
	task.PrivateData.Aggregate.Stage2TaskID = "sub-public-1"
	task.PrivateData.Aggregate.Stage1NFSPath = "/nfs-output/t2v/x.mp4"
	// **必须真的入库**:收尾走 UpdateWithStatus 做 CAS,库里没有这行就影响 0 行、
	// won=false 而提前返回,退款根本不会发生 —— 那样测试只是在验证内存字段。
	require.NoError(t, task.Insert())

	SyncAggregatePipelineParents(context.Background(), []*model.Task{task})

	require.EqualValues(t, model.TaskStatusFailure, task.Status)
	require.Equal(t, initQuota+taskQuota, getUserQuota(t, uid),
		"彻底失败必须退还预扣 —— 客户什么都没拿到,不能收钱")
}

// 子任务还在跑 → 父任务**不进终态**,下一轮再看。
//
// 注意它不再是"原样不动":进度会跟着子任务走(见
// TestSyncParentAdvancesProgressWhileUpscaling)。这条钉的是**状态**和
// 产物 —— 超分没完成之前不能落终态,那会让父任务离开未完成集合,
// SyncAggregatePipelineParents 再也看不到它。
func TestSyncParentWaitsWhileSubTaskRunning(t *testing.T) {
	withSubTaskStatus(t, model.TaskStatusInProgress, "")
	task := pipelineTask()
	task.PrivateData.Aggregate.Stage = 2
	task.PrivateData.Aggregate.Stage2TaskID = "sub-public-1"
	task.Status = model.TaskStatusInProgress

	SyncAggregatePipelineParents(context.Background(), []*model.Task{task})

	require.EqualValues(t, model.TaskStatusInProgress, task.Status, "还没轮到收尾")
	require.Empty(t, task.PrivateData.ResultURL)
}

// 查不到子任务时不替它做决定:不改状态,下轮再看,最终由超时清理兜底。
func TestSyncParentLeavesTaskAloneWhenSubTaskMissing(t *testing.T) {
	orig := lookupTaskByPublicID
	t.Cleanup(func() { lookupTaskByPublicID = orig })
	lookupTaskByPublicID = func(string) (*model.Task, bool, error) { return nil, false, nil }

	task := pipelineTask()
	task.PrivateData.Aggregate.Stage = 2
	task.PrivateData.Aggregate.Stage2TaskID = "sub-public-1"
	task.Status = model.TaskStatusInProgress

	SyncAggregatePipelineParents(context.Background(), []*model.Task{task})

	require.EqualValues(t, model.TaskStatusInProgress, task.Status)
}

// 按次计费的聚合父任务必须保住冻结价。
//
// per_call / per_second 模式的价钱在提交时就定死了,轮询期的差额结算本该整段跳过。
// 聚合父任务的分支若排在那道检查**之前**,就会落到 token 重算 —— 只要该模型恰好
// 也配了 ratio,冻结价就被悄悄换成按 token 算的金额。
func TestSettleHonorsPerCallBillingForPipelineParent(t *testing.T) {
	truncate(t)
	task := pipelineTask()
	task.PrivateData.Aggregate.Stage = 2
	// 让 stage2 分支里**必定会改价**的那条路具备触发条件(adaptor 额度优先级最高),
	// 这样"有没有被按次计费拦住"才有可观测差异 —— 否则测试会因为测试环境没有该模型的
	// 倍率配置而两种实现都不改价,变成一条假绿灯。
	task.PrivateData.Aggregate.Stage1AdaptorQuota = 999
	task.PrivateData.BillingContext = &model.TaskBillingContext{
		PerCallBilling:  true,
		OriginModelName: "gpt-4",
	}
	const frozen = 8888
	task.Quota = frozen

	settleTaskBillingOnComplete(context.Background(), &fakeSettleAdaptor{}, task,
		&relaycommon.TaskInfo{})

	require.Equal(t, frozen, task.Quota,
		"按次计费的父任务不该被 token 重算改价")
}

// 等待超分段的父任务必须被分流出上游轮询。
//
// 这是整条流水线能否收尾的关键:父任务与子任务共用同一个上游 id 时会在 taskM 里撞 key,
// 后来者(id 更大的子任务)覆盖父任务,父任务永远拿不到更新、卡到超时退款。
// 分流让父任务根本不进那张表。
func TestPartitionDivertsPipelineParents(t *testing.T) {
	waiting := pipelineTask()
	waiting.PrivateData.Aggregate.Stage = 2
	waiting.PrivateData.Aggregate.Stage2TaskID = "sub-public-1"

	generating := pipelineTask() // 还在生成段,照常走上游轮询
	plain := &model.Task{TaskID: "plain", Platform: "24"}

	byPlatform, parents := partitionTasksForPolling([]*model.Task{waiting, generating, plain})

	require.Len(t, parents, 1)
	require.Equal(t, waiting, parents[0], "等待中的父任务应被分流")
	// 另外两条照常按平台分组。
	require.Len(t, byPlatform["24"], 2)
	for _, got := range byPlatform["24"] {
		require.NotEqual(t, waiting, got, "父任务不得出现在上游轮询集合里")
	}
}

// 父任务的超时时钟不能从最初提交那刻算起 —— 流水线跑两段,总耗时天然翻倍。
// 按单段窗口判必然误杀:父任务被判失败退款,而超分子任务还在跑(白烧算力),
// 客户最终拿到一个失败的任务。
func TestAggregateParentNotTimedOutWhileSubTaskAlive(t *testing.T) {
	withSubTaskStatus(t, model.TaskStatusInProgress, "")
	task := pipelineTask()
	task.PrivateData.Aggregate.Stage = 2
	task.PrivateData.Aggregate.Stage2TaskID = "sub-public-1"

	require.True(t, AggregateParentStillWaiting(task),
		"子任务还在跑时,父任务不该被超时清理判死")
}

// 连子任务都查不到时,父任务该走正常的超时失败退款,而不是无限期挂着。
func TestAggregateParentTimesOutWhenSubTaskGone(t *testing.T) {
	orig := lookupTaskByPublicID
	t.Cleanup(func() { lookupTaskByPublicID = orig })
	lookupTaskByPublicID = func(string) (*model.Task, bool, error) { return nil, false, nil }

	task := pipelineTask()
	task.PrivateData.Aggregate.Stage = 2
	task.PrivateData.Aggregate.Stage2TaskID = "sub-public-1"

	require.False(t, AggregateParentStillWaiting(task),
		"子任务没了就不该继续豁免超时,否则任务永远挂着")
}

// 还在生成段的聚合任务、以及普通任务,都照常受超时清理管辖。
func TestNormalTasksStillSubjectToTimeout(t *testing.T) {
	require.False(t, AggregateParentStillWaiting(pipelineTask()), "生成段的任务不豁免")
	require.False(t, AggregateParentStillWaiting(&model.Task{TaskID: "plain"}), "普通任务不豁免")
}

// 超时清理必须真的放过等待中的父任务 —— 这条接线一旦掉了,
// 父任务会在超分还在跑的时候被判失败并退款,而超分继续烧算力。
func TestSweepTimedOutSparesWaitingPipelineParent(t *testing.T) {
	truncate(t)
	origTimeout := constant.TaskTimeoutMinutes
	t.Cleanup(func() { constant.TaskTimeoutMinutes = origTimeout })
	constant.TaskTimeoutMinutes = 1

	const uid, initQuota, taskQuota = 61, 10000, 3000
	seedUser(t, uid, initQuota)
	withSubTaskStatus(t, model.TaskStatusInProgress, "") // 子任务还在跑

	task := pipelineTask()
	task.UserId = uid
	task.Quota = taskQuota
	task.Status = model.TaskStatusInProgress
	task.Progress = "50%"
	task.SubmitTime = time.Now().Add(-2 * time.Hour).Unix() // 早就"超时"了
	task.PrivateData.BillingSource = BillingSourceWallet
	task.PrivateData.Aggregate.Stage = 2
	task.PrivateData.Aggregate.Stage2TaskID = "sub-public-1"
	require.NoError(t, task.Insert())

	sweepTimedOutTasks(context.Background())

	var after model.Task
	require.NoError(t, model.DB.Where("id = ?", task.ID).First(&after).Error)
	require.EqualValues(t, model.TaskStatusInProgress, after.Status,
		"等待超分段的父任务不该被超时清理判失败")
	require.Equal(t, initQuota, getUserQuota(t, uid), "更不该退款")
}

// 普通任务照常被超时清理管辖 —— 上面的豁免不能误伤。
func TestSweepTimedOutStillFailsNormalTask(t *testing.T) {
	truncate(t)
	origTimeout := constant.TaskTimeoutMinutes
	t.Cleanup(func() { constant.TaskTimeoutMinutes = origTimeout })
	constant.TaskTimeoutMinutes = 1

	const uid = 62
	seedUser(t, uid, 10000)
	task := &model.Task{
		TaskID:     "plain-timeout",
		UserId:     uid,
		Status:     model.TaskStatusInProgress,
		Progress:   "50%",
		SubmitTime: time.Now().Add(-2 * time.Hour).Unix(),
	}
	require.NoError(t, task.Insert())

	sweepTimedOutTasks(context.Background())

	var after model.Task
	require.NoError(t, model.DB.Where("id = ?", task.ID).First(&after).Error)
	require.EqualValues(t, model.TaskStatusFailure, after.Status,
		"普通任务的超时清理不该被这次改动影响")
}

// ── 超分段的对外呈现 ──────────────────────────────────────────────

// **进度不能倒退。** 生成段跑到 30%，流水线一推进却被拨回 30% 并冻在那里
// 直到跳 100%。客户端普遍把进度当单调递增用，倒退会被渲染成"重新开始了"。
func TestAdvanceDoesNotRewindProgress(t *testing.T) {
	calls, _ := withFakeVideosEndpoint(t, 200, `{"task_id":"pub-2"}`)
	_ = calls
	task := pipelineTask()
	task.Progress = "30%"

	require.True(t, TryAdvanceAggregatePipeline(
		context.Background(), nil, task, stage1Result(), "/nfs-output/t2v/x.mp4"))

	require.NotEqual(t, "30%", task.Progress, "进度被拨回生成段的值")
	require.Equal(t, "60%", task.Progress, "进超分段应该明显推进一步")
}

// **队列长度只属于生成段。** 进超分段后生成段的队列已经不存在，而父任务从
// 这一刻起被挡在平台轮询之外，那个值再也不会被刷新 —— 不清的话界面上会一直
// 显示一个已经消失的队列。
func TestAdvanceClearsQueueDisplay(t *testing.T) {
	calls, _ := withFakeVideosEndpoint(t, 200, `{"task_id":"pub-2"}`)
	_ = calls
	task := pipelineTask()
	n, sec := 3, 42
	task.Properties.QueueAhead = &n
	task.Properties.EstimatedStartSeconds = &sec

	require.True(t, TryAdvanceAggregatePipeline(
		context.Background(), nil, task, stage1Result(), "/nfs-output/t2v/x.mp4"))

	require.Nil(t, task.Properties.QueueAhead, "生成段的队列长度留在了超分段")
	require.Nil(t, task.Properties.EstimatedStartSeconds, "生成段的预计开始时间留在了超分段")
}

// 子任务进度映射进 [60,95]，且必须单调、解不出来时回起点而不是 0。
func TestUpscaleProgressMapping(t *testing.T) {
	require.Equal(t, "60%", upscaleProgress("0%"))
	require.Equal(t, "70%", upscaleProgress("30%"))
	require.Equal(t, "95%", upscaleProgress("100%"))

	// 解不出来时回起点。回 0 会让进度倒退回生成段之前。
	for _, bad := range []string{"", "garbage", "abc%", "-10%", "200%"} {
		require.Equal(t, "60%", upscaleProgress(bad), "坏值 %q 应该回起点", bad)
	}

	// 单调
	prev := 0
	for pct := 0; pct <= 100; pct += 10 {
		cur, err := strconv.Atoi(strings.TrimSuffix(
			upscaleProgress(strconv.Itoa(pct)+"%"), "%"))
		require.NoError(t, err)
		require.GreaterOrEqual(t, cur, prev, "映射不单调")
		prev = cur
	}

	// 上限留 5% 给收尾：到了 100% 却还没拿到 url 比停在 95% 更让人困惑
	require.NotEqual(t, "100%", upscaleProgress("100%"))
}

// **超分段进行中，进度要跟着子任务走。**
//
// 原先这一支是 `default:` 什么都不做 —— 进度冻在进入超分段那一刻的值上，
// 直到突然跳 100%。整个超分段对外完全没有进度。
func TestSyncParentAdvancesProgressWhileUpscaling(t *testing.T) {
	truncate(t)
	orig := lookupTaskByPublicID
	t.Cleanup(func() { lookupTaskByPublicID = orig })
	subProgress := "0%"
	lookupTaskByPublicID = func(string) (*model.Task, bool, error) {
		return &model.Task{
			TaskID: "sub-public-1", Status: model.TaskStatusInProgress, Progress: subProgress,
		}, true, nil
	}

	task := pipelineTask()
	task.Status = model.TaskStatusInProgress
	task.Progress = "60%"
	task.PrivateData.Aggregate.Stage = 2
	task.PrivateData.Aggregate.Stage2TaskID = "sub-public-1"
	// **必须真的入库。** 进度是靠 UpdateWithStatus 写出去的，库里没有这行
	// 就影响 0 行 —— 而断言只读内存字段的话，把那次写库整个删掉测试照样
	// 全绿（本文件 TestSyncParentRefundsWhenGivingUp 已经记过这个坑）。
	require.NoError(t, task.Insert())

	seen := []string{}
	for _, p := range []string{"0%", "30%", "60%", "100%"} {
		subProgress = p
		SyncAggregatePipelineParents(context.Background(), []*model.Task{task})
		seen = append(seen, task.Progress)
		require.EqualValues(t, model.TaskStatusInProgress, task.Status,
			"超分还在跑，父任务不该离开进行中")
	}

	require.Equal(t, []string{"60%", "70%", "81%", "95%"}, seen,
		"超分段的进度没跟着子任务走（原先是冻住不动直到跳 100%%）")

	// **回读库里那一行。** 只断言内存字段的话，写库删掉也不红。
	var after model.Task
	require.NoError(t, model.DB.Where("id = ?", task.ID).First(&after).Error)
	require.Equal(t, "95%", after.Progress,
		"进度只改了内存，没写进库 —— 客户端查到的还是旧值")
}

// 超分段不显示队列长度：它快，显示一个转瞬即逝的排队数反而让人以为卡住了。
func TestSyncParentKeepsQueueDisplayCleared(t *testing.T) {
	truncate(t)
	orig := lookupTaskByPublicID
	t.Cleanup(func() { lookupTaskByPublicID = orig })
	lookupTaskByPublicID = func(string) (*model.Task, bool, error) {
		return &model.Task{TaskID: "sub-public-1", Status: model.TaskStatusInProgress, Progress: "30%"}, true, nil
	}

	task := pipelineTask()
	task.Status = model.TaskStatusInProgress
	task.Progress = "60%"
	n := 5
	task.Properties.QueueAhead = &n // 生成段留下的残值
	task.PrivateData.Aggregate.Stage = 2
	task.PrivateData.Aggregate.Stage2TaskID = "sub-public-1"
	require.NoError(t, task.Insert())

	SyncAggregatePipelineParents(context.Background(), []*model.Task{task})

	require.Nil(t, task.Properties.QueueAhead, "超分段不该显示队列长度")

	var after model.Task
	require.NoError(t, model.DB.Where("id = ?", task.ID).First(&after).Error)
	require.Nil(t, after.Properties.QueueAhead, "库里那一行仍留着生成段的队列长度")
}

// **生成段的进度是细粒度的，进超分段不能把它按回起点。**
//
// gpustackplus 的 scaleProgress 把门面的 0-100 压进 [30,95]（adaptor.go），
// task_polling 每轮抄进父任务。线上实测门面确实返回 progress（"progress":
// 42.5），所以生成段跑完那一刻父任务通常已经在 60-95% 之间 —— 写死 60%
// 就是一次倒退，正是这套改动要消灭的东西。
func TestAdvanceKeepsHigherGenerateProgress(t *testing.T) {
	for _, at := range []string{"93%", "80%", "61%"} {
		calls, _ := withFakeVideosEndpoint(t, 200, `{"task_id":"pub-2"}`)
		_ = calls
		task := pipelineTask()
		task.Progress = at

		require.True(t, TryAdvanceAggregatePipeline(
			context.Background(), nil, task, stage1Result(), "/nfs-output/t2v/x.mp4"))

		require.Equal(t, at, task.Progress,
			"生成段已经到 %s，进超分段却被按回去了", at)
	}

	// 低于起点时才抬到 60%
	calls, _ := withFakeVideosEndpoint(t, 200, `{"task_id":"pub-2"}`)
	_ = calls
	low := pipelineTask()
	low.Progress = "30%"
	require.True(t, TryAdvanceAggregatePipeline(
		context.Background(), nil, low, stage1Result(), "/nfs-output/t2v/x.mp4"))
	require.Equal(t, "60%", low.Progress)
}

// **超分段里子任务自己会回落，父任务不能跟着跌。**
//
// 门面的 queued/assigned（含等重派）映射成 ProgressQueued 20%，落盘重试把
// 95% 打回 ProgressInProgress 30%。跟着走就是 93% → 67%/70% 的可见跌落，
// 而这是「等待分支从空实现改成推进进度」新引入的。
func TestSyncParentNeverRewindsOnSubTaskDip(t *testing.T) {
	truncate(t)
	orig := lookupTaskByPublicID
	t.Cleanup(func() { lookupTaskByPublicID = orig })
	subProgress := "0%"
	subStatus := model.TaskStatus(model.TaskStatusInProgress)
	lookupTaskByPublicID = func(string) (*model.Task, bool, error) {
		return &model.Task{TaskID: "sub-public-1", Status: subStatus, Progress: subProgress}, true, nil
	}

	task := pipelineTask()
	task.Status = model.TaskStatusInProgress
	task.Progress = "60%"
	task.PrivateData.Aggregate.Stage = 2
	task.PrivateData.Aggregate.Stage2TaskID = "sub-public-1"
	require.NoError(t, task.Insert())

	// 真实序列：跑到 95% → 门面把它挪回排队（等重派）→ 落盘重试打回 30% → 再跑上来
	seq := []struct {
		progress string
		status   model.TaskStatus
	}{
		{"50%", model.TaskStatus(model.TaskStatusInProgress)},
		{"95%", model.TaskStatus(model.TaskStatusInProgress)},
		{"20%", model.TaskStatus(model.TaskStatusQueued)},     // 等重派
		{"30%", model.TaskStatus(model.TaskStatusInProgress)}, // 落盘重试
		{"95%", model.TaskStatus(model.TaskStatusInProgress)},
	}
	prev := 0
	for _, step := range seq {
		subProgress, subStatus = step.progress, step.status
		SyncAggregatePipelineParents(context.Background(), []*model.Task{task})
		cur, err := strconv.Atoi(strings.TrimSuffix(task.Progress, "%"))
		require.NoError(t, err)
		require.GreaterOrEqual(t, cur, prev,
			"子任务回落到 %s 时父任务跟着跌了：%d%% → %d%%", step.progress, prev, cur)
		prev = cur
	}
	// 子任务 95%% → 父任务 60 + 35×0.95 = 93%%；上限 95%% 只有子任务到 100%% 才达到
	require.Equal(t, "93%", task.Progress)
}

// 超过上限的值是**唯一允许下调**的情形：进度 100% 却仍在进行中，
// 比停在 95% 更让人困惑。
func TestUpscaleProgressCapsAtCeiling(t *testing.T) {
	require.Equal(t, "95%", clampUpscaleProgress("100%", "60%"))
	require.Equal(t, "95%", clampUpscaleProgress("99%", "60%"))
	require.Equal(t, "93%", clampUpscaleProgress("93%", "60%"))
}
