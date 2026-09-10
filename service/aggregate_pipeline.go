package service

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

// 聚合模型的视频流水线:生成段完成后自动接一段超分,客户全程只看到一个任务。
//
// ── 挂钩点 ──────────────────────────────────────────────────────────
//
// 在轮询发现生成段 completed 的那一刻拦截,**早于产物落 OBS、早于结算**:
// 此时任务还没进终态,把它改回 InProgress 并换上超分段的上游 id,轮询下一轮就自然
// 跟进第二段。客户侧从头到尾只有一个 task id,状态一直是 in_progress。
//
// 为什么必须早于落盘:落了 OBS 就等于对外交付了中间产物(未超分的那一版),
// 而客户要的是最终的 2K。中间产物只该作为超分段的输入,不该出现在结果里。
//
// ── 为什么是 self-call ─────────────────────────────────────────────
//
// 与增强段同一个理由:走一遍 /v1/videos 就自动拿到渠道路由、预扣费、日志、退款,
// 而手工构造上游提交请求意味着把这些各抄一份 —— 抄出来的那份会与主链路漂移,
// 且不报错。轮询是后台任务、没有客户的请求上下文,所以客户令牌在提交生成段时
// 就存进了任务的 PrivateData(见 model.TaskAggregateInfo.CallerKey)。
//
// ── 失败语义 ───────────────────────────────────────────────────────
//
// 超分段提交失败 → 整单**按已有逻辑正常结束在生成段**:产物照常落盘交付,
// 客户拿到未超分的那一版。这是深思后的选择,不是偷懒:
//   - 生成段已经真实消耗了 GPU 并已计费,判整单失败要退这笔钱,我们白烧一次算力;
//   - 客户按分段计费付的是"生成 + 超分"两笔,超分没跑成就只扣生成那笔,他没吃亏;
//   - 最重要的是:交付一个分辨率低一些的成品,比交付一个错误强得多。
// 这与你定的"各段独立退款"一致 —— 没跑的段不收钱,跑了的段照收。

// persistTaskResult 把成品落 OBS。做成变量供测试构造"落盘成功/失败"两条路径 ——
// 它们的分歧(交付 vs 重试)正是这段收尾逻辑最要紧的地方,不能只覆盖其中一条。
var persistTaskResult = PersistTaskResultToOBS

// lookupTaskByPublicID 回查自调用产生的那条任务。做成变量供测试构造成功路径,
// 免去为一条状态机测试去建表。
var lookupTaskByPublicID = model.GetByOnlyTaskId

// upscaleSubmitTimeout 提交超分段的超时。后台轮询里的一次自调用,不能拖住整个轮询批次。
const upscaleSubmitTimeout = 30 * time.Second

// videosEndpoint 视频任务提交地址。做成变量供测试指向本地假服务端。
var videosEndpoint = func() string {
	base := strings.TrimRight(strings.TrimSpace(system_setting.ServerAddress), "/")
	if base == "" {
		return ""
	}
	return base + "/v1/videos"
}

// TryAdvanceAggregatePipeline 在生成段完成时尝试推进到超分段。
//
// 返回 true 表示已成功提交超分段、任务应保持 InProgress(调用方须跳过落盘与结算);
// 返回 false 表示这条任务没有后续段、或超分提交失败 —— 两种情况都按原有逻辑正常收尾。
//
// nfsPath 为生成段产物在共享 NFS 上的路径,作为超分段的输入。
func TryAdvanceAggregatePipeline(ctx context.Context, adaptor TaskPollingAdaptor, task *model.Task, taskResult *relaycommon.TaskInfo, nfsPath string) bool {
	agg := task.PrivateData.Aggregate
	if !agg.HasUpscale() || agg.Stage != 1 {
		return false
	}
	// 没有产物路径就无从超分。这不该发生(生成段成功必有产物),但真发生时按
	// "没有后续段"处理 —— 交付生成段的成品,好过把一个已完成的任务判失败。
	if strings.TrimSpace(nfsPath) == "" {
		common.SysLog(fmt.Sprintf("aggregate pipeline: task %s 生成段无 nfs_path,跳过超分段", task.TaskID))
		return false
	}
	if strings.TrimSpace(agg.CallerKey) == "" {
		common.SysLog(fmt.Sprintf("aggregate pipeline: task %s 缺少调用者身份,跳过超分段", task.TaskID))
		return false
	}

	reqCtx, cancel := context.WithTimeout(ctx, upscaleSubmitTimeout)
	defer cancel()
	sub, err := submitUpscaleTask(reqCtx, agg, nfsPath)
	if err != nil {
		// 提交失败不改变任务状态:调用方继续走落盘与结算,客户拿到未超分的成品。
		common.SysError(fmt.Sprintf("aggregate pipeline: task %s 超分段提交失败,按生成段成品交付: %v",
			task.TaskID, err))
		return false
	}

	// 把生成段回执的计费依据留下来。父任务代表生成段,
	// 最终结算要按这些值算 —— 那时手上只剩超分段的回执,拿它算会把生成段按超分后的
	// 分辨率计费;而完全不算则会退回预扣锚点,丢掉视频计费矩阵的单价。
	if taskResult != nil {
		if adaptor != nil {
			agg.Stage1AdaptorQuota = adaptor.AdjustBillingOnComplete(task, taskResult)
		}
		agg.Stage1BillableTokens = videoBillableTokens(taskResult)
		agg.Stage1TotalTokens = taskResult.TotalTokens
		agg.Stage1Resolution = videoUpstreamResolution(taskResult)
	}

	agg.Stage = 2
	agg.Stage2TaskID = sub.TaskID
	agg.Stage1NFSPath = nfsPath
	// 令牌用完即擦:第二段已经提交,它不再需要客户凭证,没有理由继续留在库里。
	agg.CallerKey = ""

	// **刻意不改** task 的 Platform / ChannelId / UpstreamTaskID / PrivateData.Key。
	//
	// 让父任务换上子任务的上游身份看似自然(它就能被轮询到第二段),实际走不通:
	// 轮询按上游 task id 建索引(taskM[upstreamID]),父子两条记录会落在同一个 key 上,
	// 而未完成任务按 id 升序遍历、后来者覆盖先前者 —— id 更大的子任务必然胜出,
	// 父任务永远拿不到更新,一直卡到 sweepTimedOutTasks 判超时失败并退款。
	// 同一个渠道下 taskChannelM 还会把这个上游 id 排两次,同一条任务一轮被结算两遍。
	//
	// 正确的做法是父任务保留自己的身份、改为**等待**子任务:
	// 轮询循环把 Stage==2 的父任务分流出去(不进上游轮询),
	// 由 syncAggregatePipelineParents 查子任务的终态再收尾。
	common.SysLog(fmt.Sprintf("aggregate pipeline: task %s 进入超分段(upstream=%s, platform=%s, channel=%d)",
		task.TaskID, task.PrivateData.UpstreamTaskID, task.Platform, task.ChannelId))
	return true
}

// submitUpscaleTask 以客户身份提交一次超分任务,返回**那条子任务**本身。
//
// 返回整条任务而不是散装的 id:父任务要跟上第二段,需要同步的字段不止一个
// (Platform / Action / PrivateData.Key / ChannelId / UpstreamTaskID),
// 散着返回迟早漏掉其中一个,而漏掉的表现是"任务卡住直到超时",不会报错。
//
// 输入用 metadata.video 传生成段产物的 NFS 绝对路径:nfsinput 的物化层原生支持这种
// 形态(见 relay/channel/gpustackplus/nfsinput/taskref.go 的绝对路径分支),
// 因此**不需要把产物读出来再传一遍** —— 那对一个几十 MB 的视频是纯浪费。
func submitUpscaleTask(ctx context.Context, agg *model.TaskAggregateInfo, nfsPath string) (*model.Task, error) {
	endpoint := videosEndpoint()
	if endpoint == "" {
		return nil, fmt.Errorf("站点未配置服务器地址(ServerAddress)")
	}
	body := map[string]any{
		"model": agg.UpscaleModel,
		// 超分不需要提示词,但部分端点要求该字段存在。
		"prompt": "",
		"metadata": map[string]any{
			"task_type": "sr",
			"video":     nfsPath,
		},
	}
	if agg.UpscaleTarget != "" {
		body["size"] = agg.UpscaleTarget
	}
	payload, err := common.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("构造超分请求失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("构造超分请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+agg.CallerKey)

	client := GetHttpClient()
	if client == nil {
		client = &http.Client{}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("超分调用失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("超分提交返回 %d", resp.StatusCode)
	}

	var parsed struct {
		TaskID string `json:"task_id"`
		ID     string `json:"id"`
	}
	if err := common.DecodeJson(resp.Body, &parsed); err != nil {
		return nil, fmt.Errorf("解析超分响应失败: %w", err)
	}
	publicID := parsed.TaskID
	if publicID == "" {
		publicID = parsed.ID
	}
	if publicID == "" {
		return nil, fmt.Errorf("超分响应没有任务 id")
	}

	// 自调用拿到的是**我们自己**的任务 id,而轮询要按上游 id 去问渠道。
	// 回查那条新任务,取出它的上游 id 与承接渠道。
	sub, exists, err := lookupTaskByPublicID(publicID)
	if err != nil || !exists || sub == nil {
		return nil, fmt.Errorf("超分任务 %s 回查失败: %v", publicID, err)
	}
	if sub.PrivateData.UpstreamTaskID == "" {
		// 没有上游 id 就无从轮询,推进等于把任务挂死。
		return nil, fmt.Errorf("超分任务 %s 尚无上游任务 id", publicID)
	}
	return sub, nil
}

// IsAggregatePipelineParent 该任务是否为正在等待超分段的聚合父任务。
// 轮询循环据此把它从上游轮询里分流出去 —— 它的上游任务(生成段)早就完成了,
// 再去问渠道只会一遍遍拿到同一个 completed。
func IsAggregatePipelineParent(task *model.Task) bool {
	agg := task.PrivateData.Aggregate
	return agg != nil && agg.Stage == 2 && agg.Stage2TaskID != ""
}

// AggregateParentStillWaiting 该父任务是否仍在等一条**存在的**子任务。
//
// 给超时清理用:父任务的时钟不能从最初提交那一刻算起 —— 流水线要跑生成 + 超分两段,
// 总耗时天然翻倍,按单段的窗口判必然误杀。误杀的后果尤其难看:父任务被判失败并退款,
// 而超分子任务还在继续跑(白烧一次算力),客户最终拿到一个失败的任务。
//
// 父任务的超时**由子任务代管**:子任务有自己的 submit_time 与同一套超时保护,
// 它失败后 SyncAggregatePipelineParents 会接手降级交付生成段成品。
// 只有连子任务都查不到时(被清理/从未落库),父任务才该走正常的超时失败退款。
func AggregateParentStillWaiting(task *model.Task) bool {
	if !IsAggregatePipelineParent(task) {
		return false
	}
	sub, exists, err := lookupTaskByPublicID(task.PrivateData.Aggregate.Stage2TaskID)
	return err == nil && exists && sub != nil
}

// SyncAggregatePipelineParents 推进所有正在等待超分段的父任务。
//
// 父任务不冒充子任务的上游身份(见 TryAdvanceAggregatePipeline 里的说明),
// 而是每轮来查一次子任务的终态:
//
//   - 子任务成功 → 把它的产物挂到父任务上,按生成段的回执结算,置成功;
//   - 子任务失败 → **降级交付生成段的成品**。生成段已经真实烧掉 GPU 且已计费,
//     不能因为第二段失败就把成品也丢了;客户按分段计费只被扣了生成那笔,没吃亏。
//   - 子任务还在跑 → 什么都不做,下一轮再看。
func SyncAggregatePipelineParents(ctx context.Context, parents []*model.Task) {
	for _, task := range parents {
		agg := task.PrivateData.Aggregate
		sub, exists, err := lookupTaskByPublicID(agg.Stage2TaskID)
		if err != nil || !exists || sub == nil {
			// 查不到子任务:可能刚提交还没落库,也可能被清理了。不改状态,下轮再看;
			// 真的一直查不到,最终由超时清理兜底,不在这里替它做决定。
			continue
		}
		switch sub.Status {
		case model.TaskStatusSuccess:
			finishAggregateParent(ctx, task, sub.PrivateData.ResultURL, "")
		case model.TaskStatusFailure:
			common.SysLog(fmt.Sprintf(
				"aggregate pipeline: task %s 的超分段失败(%s),降级交付生成段成品",
				task.TaskID, sub.FailReason))
			finishAggregateParent(ctx, task, "", agg.Stage1NFSPath)
		default:
			// 仍在排队/生成中
		}
	}
}

// finishAggregateParent 给父任务收尾:落产物、按生成段回执结算、置终态。
//
// resultURL 非空时直接沿用子任务已经落好的产物(超分成品);否则用 nfsPath 落一次盘
// (超分失败的降级路径,交付生成段的原始分辨率成品)。
//
// **拿不到任何产物引用时绝不置成功**。口径与主轮询路径完全一致(task_polling.go 的
// nfs-only 落盘失败分支):成品还在 SFS 上,瞬时的 OBS 抖动不该让它被丢弃 —— 先留在
// InProgress 等下一轮重试,超过上限才判失败并退款。
//
// 反过来做的后果很难收拾:状态一旦进终态,父任务就离开了未完成集合,
// SyncAggregatePipelineParents 再也看不到它,客户拿到一个"成功"但 url 为空、
// 且无法恢复的任务 —— 而生成段的钱照收。
func finishAggregateParent(ctx context.Context, task *model.Task, resultURL, nfsPath string) {
	ref := resultURL
	if ref == "" && nfsPath != "" {
		if persisted, ok := persistTaskResult(ctx, task, nfsPath, ""); ok {
			ref = persisted
		}
	}

	oldStatus := task.Status
	if ref == "" {
		// 没有可交付的产物。与主轮询路径同口径:先重试,超限才判失败退款。
		task.PrivateData.PersistRetryCount++
		if task.PrivateData.PersistRetryCount <= maxPersistRetries {
			common.SysLog(fmt.Sprintf(
				"aggregate pipeline: task %s 暂无可交付产物,保持进行中等待重试(%d/%d)",
				task.TaskID, task.PrivateData.PersistRetryCount, maxPersistRetries))
			task.Status = model.TaskStatusInProgress
			if _, err := task.UpdateWithStatus(oldStatus); err != nil {
				common.SysError(fmt.Sprintf("aggregate pipeline: 更新父任务 %s 失败: %v", task.TaskID, err))
			}
			return
		}
		common.SysError(fmt.Sprintf(
			"aggregate pipeline: task %s 落盘持续失败,判失败并退款", task.TaskID))
		task.Status = model.TaskStatusFailure
		task.Progress = "100%"
		task.FailReason = "成品落盘 OBS 持续失败，无法对外提供访问 URL（请检查媒体存储配置/连通性）"
		if task.FinishTime == 0 {
			task.FinishTime = time.Now().Unix()
		}
		won, err := task.UpdateWithStatus(oldStatus)
		if err != nil || !won {
			return
		}
		if task.Quota != 0 {
			RefundTaskQuota(ctx, task, task.FailReason)
		}
		return
	}

	task.PrivateData.ResultURL = ref
	task.Status = model.TaskStatusSuccess
	task.Progress = "100%"

	// 产物审核（§12.4）。父任务不走 updateVideoSingleTask —— partitionTasksForPolling
	// 刻意把它挡在平台轮询之外 —— 所以那边的挂载点覆盖不到这里，漏了这段的后果是
	// **超分/编排的最终成品从不送审**，而它恰恰是真正交付给用户的那一份。
	// 子任务被审过不能替代：父任务交付的是自己的 ResultURL，时机也不同。
	//
	// 与主路径同口径：转失败 + 清空产物地址 + 不退费，预算也共用同一轮的额度。
	if ModerateTaskOutputFunc != nil && outputModerationAllowed() {
		started := time.Now()
		blocked, reason := ModerateTaskOutputFunc(ctx, task, ref, "")
		outputModerationSpent += time.Since(started)
		if blocked {
			task.Status = model.TaskStatusFailure
			task.FailReason = reason
			task.PrivateData.ResultURL = ""
			task.Data = nil
			common.SysLog(fmt.Sprintf("aggregate pipeline: task %s 产物被内容审核拦截", task.TaskID))
		}
	}
	if task.FinishTime == 0 {
		task.FinishTime = time.Now().Unix()
	}

	won, err := task.UpdateWithStatus(oldStatus)
	if err != nil {
		common.SysError(fmt.Sprintf("aggregate pipeline: 更新父任务 %s 失败: %v", task.TaskID, err))
		return
	}
	if !won {
		// 已被别的进程推进过,结算也归它做,这里不能重复记账。
		return
	}
	// 结算走与普通任务同一条函数:它内部会认出 Stage==2 并改用生成段的回执。
	settleTaskBillingOnComplete(ctx, nil, task, &relaycommon.TaskInfo{})
}
