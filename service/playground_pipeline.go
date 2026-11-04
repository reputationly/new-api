package service

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// 体验区的服务端流水线推进。
//
// 与聚合那条（TryAdvanceAggregatePipeline）共用同一套状态机、同样的收尾与降级口径，
// 唯一的结构差异是**用谁的凭证提交后置段**：
//   - 聚合：客户有真实 API 令牌，`CallerKey` 就存在任务里，直接自调用；
//   - 体验区：/pg 用的是现场造的内存临时令牌（未入库、Id=0），没有可自调的凭证，
//     于是给用户懒建一个**内部令牌**（service/pipeline_token.go），只放行后置段模型。
//
// 两条路都走"以用户身份调本站 /v1/videos"：计费、限流、日志、渠道重试全部复用主链路，
// 不必手写第二份算价与路由逻辑（那份必然与主链路漂移）。因此后置段是**分段计费**，
// 与体验区今天（浏览器提交第二段）的账单口径逐位一致 —— 这次改的只是"谁来提交"。

// claimPipelineStage 抢"提交这一段"的权利（模型层的比较并交换）。
// 抽出成变量：推进逻辑的并发正确性要在不依赖数据库的情况下可测。
var claimPipelineStage = model.ClaimPipelineStage

// pipelineTokenForTask 抽出成变量供测试注入：真实实现要落库建令牌，
// 而推进逻辑（状态机、幂等、计费依据留存）本身不需要数据库。
var pipelineTokenForTask = GetPipelineToken

// advancePlaygroundStage 把体验区任务推进到下一后置段。
//
// 返回 true 表示已提交、任务应保持 InProgress（调用方跳过落盘与结算）；
// 返回 false 表示没有下一段、或提交失败 —— 两种情况都按原有逻辑正常收尾
// （交付已有的成品，好过把一个已经烧掉 GPU 的成功任务判失败）。
func advancePlaygroundStage(ctx context.Context, adaptor TaskPollingAdaptor, task *model.Task, taskResult *relaycommon.TaskInfo, nfsPath string) bool {
	agg := task.PrivateData.Aggregate
	stage, ok := agg.NextPostStage()
	if !ok {
		return false
	}
	// 第一段的输入是生成段的 NFS 产物；之后的段用 `task:<上一段任务 id>` 引用 ——
	// 上一段此时已经完成、产物已落库，而父任务自己的 ResultURL 还没写（它正等着
	// 这一段），拿它做引用会解析到一个空产物。
	videoRef := strings.TrimSpace(nfsPath)
	if agg.Stage >= 2 {
		if strings.TrimSpace(agg.Stage2TaskID) == "" {
			common.SysLog(fmt.Sprintf("playground pipeline: task %s Stage=%d 但缺少上一段任务 id,停止推进", task.TaskID, agg.Stage))
			return false
		}
		videoRef = "task:" + agg.Stage2TaskID
	}
	if videoRef == "" {
		common.SysLog(fmt.Sprintf("playground pipeline: task %s 没有可用的输入产物,跳过 %s 段", task.TaskID, stage.Kind))
		return false
	}

	// **先抢再提交**：读 Stage → 提交 → 回写之间是窗口，两个推进者同时进来就会
	// 重复提交、重复计费。抢不到直接返回，不重试（重试就是重复计费本身）。
	claimed, cerr := claimPipelineStage(task.ID, agg.Stage+1)
	if cerr != nil {
		common.SysError(fmt.Sprintf("playground pipeline: task %s 抢占后置段失败: %v", task.TaskID, cerr))
		return false
	}
	if !claimed {
		common.SysLog(fmt.Sprintf("playground pipeline: task %s 的第 %d 段已被其它推进者抢下,跳过", task.TaskID, agg.Stage+1))
		return false
	}
	// **内存态也要跟上。** 调用方随后会用 task.UpdateWithStatus 整行写回（Select("*")），
	// 而结构体里的 PipelineClaimStage 还是入库时读到的旧值 —— 不更新的话那次写回会把
	// 刚抢到的号覆盖回去，下一轮又能抢一次，等于**重复提交、重复计费**。
	task.PipelineClaimStage = agg.Stage + 1

	token, err := pipelineTokenForTask(task.UserId, task.Group, common.PostStageModels(agg.PostStages))
	if err != nil {
		common.SysError(fmt.Sprintf("playground pipeline: task %s 取内部令牌失败,按现有成品交付: %v", task.TaskID, err))
		return false
	}

	reqCtx, cancel := context.WithTimeout(ctx, upscaleSubmitTimeout)
	defer cancel()
	sub, err := submitStageTask(reqCtx, token.Key, task.TaskID, videoRef, stage)
	if err != nil {
		common.SysError(fmt.Sprintf("playground pipeline: task %s 的 %s 段提交失败,按现有成品交付: %v",
			task.TaskID, stage.Kind, err))
		// 提交失败**不改变任务状态**：调用方继续走落盘与结算，客户拿到上一段的成品。
		return false
	}

	// 生成段的计费依据只在第一次推进时留：父任务代表的是生成段，它的价钱在生成段
	// 完成那一刻就该定死（见 task_polling 里 agg.Stage>=2 的结算分支）。
	if agg.Stage == 1 && taskResult != nil {
		if adaptor != nil {
			agg.Stage1AdaptorQuota = adaptor.AdjustBillingOnComplete(task, taskResult)
		}
		agg.Stage1BillableTokens = videoBillableTokens(taskResult)
		agg.Stage1TotalTokens = taskResult.TotalTokens
		agg.Stage1Resolution = videoUpstreamResolution(taskResult)
		agg.Stage1NFSPath = nfsPath
		// 生成段成品落盘，供日志里生成段那一行下载（与聚合同口径）。
		agg.Stage1ResultURL = persistStage1Product(ctx, task, nfsPath)
	}

	agg.Stage2TaskID = sub.TaskID
	agg.Stage++
	// 对外呈现在这里一起设，不交给调用方：这个函数是唯一知道"确实推进了"的地方，
	// 漏改一处的表现是静默的（进度倒退，或界面上挂着已经消失的队列）。
	markUpscaleStage(task, upscaleProgress(""))

	common.SysLog(fmt.Sprintf("playground pipeline: task %s 进入第 %d 段(%s, model=%s, upstream=%s)",
		task.TaskID, agg.Stage, stage.Kind, stage.Model, sub.TaskID))
	return true
}

// submitStageTask 以给定令牌身份提交一个后置段任务，返回**那条子任务**本身。
//
// 请求体与体验区浏览器版本逐字段对齐（task_type / video / sr_ratio / resize_mode /
// target_short_edge）：服务端只是换了个提交者，不该顺手改请求语义。
// 不发顶层 size：超分段的计费行由 target_short_edge 归档（后端 videoResolution 的
// 第二级），而顶层 size 是"客户以为要什么"，两者在这里没有差别时就不多发一个字段。
func submitStageTask(ctx context.Context, callerKey, parentTaskID, videoRef string, stage common.PostStage) (*model.Task, error) {
	endpoint := videosEndpoint()
	if endpoint == "" {
		return nil, fmt.Errorf("站点未配置服务器地址(ServerAddress)")
	}
	meta := map[string]any{
		"task_type": stage.Kind,
		// 与父任务同口径：外部引用用 task:<id>，第一段的 NFS 路径原样透传。
		"video": videoRef,
	}
	if stage.SRRatio != nil && *stage.SRRatio > 0 {
		meta["sr_ratio"] = *stage.SRRatio
	}
	if mode := strings.TrimSpace(stage.ResizeMode); mode != "" {
		meta["resize_mode"] = mode
	}
	if stage.TargetShortEdge > 0 {
		meta["target_short_edge"] = stage.TargetShortEdge
	}
	body := map[string]any{
		"model": stage.Model,
		// 超分/配音都不需要提示词，但部分端点要求该字段存在。
		"prompt":   "",
		"metadata": meta,
	}
	payload, err := common.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("构造 %s 请求失败: %w", stage.Kind, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("构造 %s 请求失败: %w", stage.Kind, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+callerKey)
	// 归属头：后置段的提交/结算日志据此对回父任务。只影响展示归类，不参与计费。
	req.Header.Set(AggregateParentTaskHeader, parentTaskID)
	// 段标签按 kind 给：配音段的提交/结算日志标成 upscale 会让对账与排障对不上
	// （"我买的是超分怎么多了条配音"反过来也一样）。
	stageLabel := LogStageUpscale
	if stage.Kind == common.PostStageKindV2A {
		stageLabel = LogStageDub
	}
	req.Header.Set(AggregateStageHeader, stageLabel)

	client := GetHttpClient()
	if client == nil {
		client = &http.Client{}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s 调用失败: %w", stage.Kind, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// 必须带上响应体与关键请求参数：这是自调用，出问题往往是我们拼的
		// model/metadata 与站点配置对不上，而那几项在别处都看不到。
		return nil, fmt.Errorf("%s 提交返回 %d（model=%s video=%s）: %s",
			stage.Kind, resp.StatusCode, stage.Model, videoRef, readErrorBody(resp.Body))
	}

	var parsed struct {
		TaskID string `json:"task_id"`
		ID     string `json:"id"`
	}
	if err := common.DecodeJson(resp.Body, &parsed); err != nil {
		return nil, fmt.Errorf("解析 %s 响应失败: %w", stage.Kind, err)
	}
	publicID := parsed.TaskID
	if publicID == "" {
		publicID = parsed.ID
	}
	if publicID == "" {
		return nil, fmt.Errorf("%s 响应没有任务 id", stage.Kind)
	}

	sub, exists, err := lookupTaskByPublicID(publicID)
	if err != nil || !exists || sub == nil {
		return nil, fmt.Errorf("%s 任务 %s 回查失败: %v", stage.Kind, publicID, err)
	}
	if sub.PrivateData.UpstreamTaskID == "" {
		return nil, fmt.Errorf("%s 任务 %s 尚无上游任务 id", stage.Kind, publicID)
	}
	return sub, nil
}
