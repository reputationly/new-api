package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/samber/lo"
)

// TaskPollingAdaptor 定义轮询所需的最小适配器接口，避免 service -> relay 的循环依赖
type TaskPollingAdaptor interface {
	Init(info *relaycommon.RelayInfo)
	FetchTask(baseURL string, key string, body map[string]any, proxy string) (*http.Response, error)
	ParseTaskResult(body []byte) (*relaycommon.TaskInfo, error)
	// AdjustBillingOnComplete 在任务到达终态（成功/失败）时由轮询循环调用。
	// 返回正数触发差额结算（补扣/退还），返回 0 保持预扣费金额不变。
	AdjustBillingOnComplete(task *model.Task, taskResult *relaycommon.TaskInfo) int
}

// GetTaskAdaptorFunc 由 main 包注入，用于获取指定平台的任务适配器。
// 打破 service -> relay -> relay/channel -> service 的循环依赖。
var GetTaskAdaptorFunc func(platform constant.TaskPlatform) TaskPollingAdaptor

// sweepTimedOutTasks 在主轮询之前独立清理超时任务。
// 每次最多处理 100 条，剩余的下个周期继续处理。
// 使用 per-task CAS (UpdateWithStatus) 防止覆盖被正常轮询已推进的任务。
func sweepTimedOutTasks(ctx context.Context) {
	if constant.TaskTimeoutMinutes <= 0 {
		return
	}
	cutoff := time.Now().Unix() - int64(constant.TaskTimeoutMinutes)*60
	tasks := model.GetTimedOutUnfinishedTasks(cutoff, 100)
	if len(tasks) == 0 {
		return
	}

	const legacyTaskCutoff int64 = 1740182400 // 2026-02-22 00:00:00 UTC
	reason := fmt.Sprintf("任务超时（%d分钟）", constant.TaskTimeoutMinutes)
	legacyReason := "任务超时（旧系统遗留任务，不进行退款，请联系管理员）"
	now := time.Now().Unix()
	timedOutCount := 0

	for _, task := range tasks {
		// 聚合流水线的父任务:只要它等的子任务还在,就不算超时。理由见
		// AggregateParentStillWaiting —— 按单段的窗口去判一条跑两段的任务必然误杀,
		// 而误杀会在超分还在跑的时候就把父任务判失败并退款。
		if AggregateParentStillWaiting(task) {
			continue
		}
		isLegacy := task.SubmitTime > 0 && task.SubmitTime < legacyTaskCutoff

		oldStatus := task.Status
		task.Status = model.TaskStatusFailure
		task.Progress = "100%"
		task.FinishTime = now
		if isLegacy {
			task.FailReason = legacyReason
		} else {
			task.FailReason = reason
		}

		won, err := task.UpdateWithStatus(oldStatus)
		if err != nil {
			logger.LogError(ctx, fmt.Sprintf("sweepTimedOutTasks CAS update error for task %s: %v", task.TaskID, err))
			continue
		}
		if !won {
			logger.LogInfo(ctx, fmt.Sprintf("sweepTimedOutTasks: task %s already transitioned, skip", task.TaskID))
			continue
		}
		timedOutCount++
		if !isLegacy && task.Quota != 0 {
			RefundTaskQuota(ctx, task, reason)
		}
	}

	if timedOutCount > 0 {
		logger.LogInfo(ctx, fmt.Sprintf("sweepTimedOutTasks: timed out %d tasks", timedOutCount))
	}
}

// partitionTasksForPolling 把未完成任务分成两拨:按平台分组去问上游的,
// 和等待超分段的聚合父任务。
//
// 父任务**不能**进上游轮询。两个理由:它自己的上游任务(生成段)早已完成,再去问渠道
// 只会一轮轮拿到同一个 completed;而它又不能改挂子任务的上游 id —— 轮询按上游 id
// 建索引(下面的 taskM[upstreamID]),父子两条记录会撞在同一个 key 上,而未完成任务
// 按 id 升序遍历、后来者覆盖先前者,id 更大的子任务必然胜出,父任务于是永远拿不到
// 更新,一直卡到超时被判失败并退款。详见 TryAdvanceAggregatePipeline 里的说明。
//
// 抽成独立函数是为了让这条分流可被测试钉住:它是整条流水线能不能收尾的关键,
// 而 TaskPollingLoop 本身是个无限循环,测不了。
func partitionTasksForPolling(tasks []*model.Task) (map[constant.TaskPlatform][]*model.Task, []*model.Task) {
	byPlatform := make(map[constant.TaskPlatform][]*model.Task)
	var aggregateParents []*model.Task
	for _, t := range tasks {
		if IsAggregatePipelineParent(t) {
			aggregateParents = append(aggregateParents, t)
			continue
		}
		byPlatform[t.Platform] = append(byPlatform[t.Platform], t)
	}
	return byPlatform, aggregateParents
}

// TaskPollingLoop 主轮询循环，每 15 秒检查一次未完成的任务
func TaskPollingLoop() {
	for {
		time.Sleep(time.Duration(15) * time.Second)
		common.SysLog("任务进度轮询开始")
		ctx := context.TODO()
		resetOutputModerationBudget()
		sweepTimedOutTasks(ctx)
		allTasks := model.GetAllUnFinishSyncTasks(constant.TaskQueryLimit)
		platformTask, aggregateParents := partitionTasksForPolling(allTasks)
		if len(aggregateParents) > 0 {
			SyncAggregatePipelineParents(ctx, aggregateParents)
		}
		for platform, tasks := range platformTask {
			if len(tasks) == 0 {
				continue
			}
			taskChannelM := make(map[int][]string)
			taskM := make(map[string]*model.Task)
			nullTaskIds := make([]int64, 0)
			for _, task := range tasks {
				upstreamID := task.GetUpstreamTaskID()
				if upstreamID == "" {
					// 统计失败的未完成任务
					nullTaskIds = append(nullTaskIds, task.ID)
					continue
				}
				taskM[upstreamID] = task
				taskChannelM[task.ChannelId] = append(taskChannelM[task.ChannelId], upstreamID)
			}
			if len(nullTaskIds) > 0 {
				err := model.TaskBulkUpdateByID(nullTaskIds, map[string]any{
					"status":   "FAILURE",
					"progress": "100%",
				})
				if err != nil {
					logger.LogError(ctx, fmt.Sprintf("Fix null task_id task error: %v", err))
				} else {
					logger.LogInfo(ctx, fmt.Sprintf("Fix null task_id task success: %v", nullTaskIds))
				}
			}
			if len(taskChannelM) == 0 {
				continue
			}

			DispatchPlatformUpdate(platform, taskChannelM, taskM)
		}
		common.SysLog("任务进度轮询完成")
	}
}

// DispatchPlatformUpdate 按平台分发轮询更新
func DispatchPlatformUpdate(platform constant.TaskPlatform, taskChannelM map[int][]string, taskM map[string]*model.Task) {
	switch platform {
	case constant.TaskPlatformMidjourney:
		// MJ 轮询由其自身处理，这里预留入口
	case constant.TaskPlatformSuno:
		_ = UpdateSunoTasks(context.Background(), taskChannelM, taskM)
	default:
		if err := UpdateVideoTasks(context.Background(), platform, taskChannelM, taskM); err != nil {
			common.SysLog(fmt.Sprintf("UpdateVideoTasks fail: %s", err))
		}
	}
}

// UpdateSunoTasks 按渠道更新所有 Suno 任务
func UpdateSunoTasks(ctx context.Context, taskChannelM map[int][]string, taskM map[string]*model.Task) error {
	for channelId, taskIds := range taskChannelM {
		err := updateSunoTasks(ctx, channelId, taskIds, taskM)
		if err != nil {
			logger.LogError(ctx, fmt.Sprintf("渠道 #%d 更新异步任务失败: %s", channelId, err.Error()))
		}
	}
	return nil
}

func updateSunoTasks(ctx context.Context, channelId int, taskIds []string, taskM map[string]*model.Task) error {
	logger.LogInfo(ctx, fmt.Sprintf("渠道 #%d 未完成的任务有: %d", channelId, len(taskIds)))
	if len(taskIds) == 0 {
		return nil
	}
	ch, err := model.CacheGetChannel(channelId)
	if err != nil {
		common.SysLog(fmt.Sprintf("CacheGetChannel: %v", err))
		// Collect DB primary key IDs for bulk update (taskIds are upstream IDs, not task_id column values)
		var failedIDs []int64
		for _, upstreamID := range taskIds {
			if t, ok := taskM[upstreamID]; ok {
				failedIDs = append(failedIDs, t.ID)
			}
		}
		err = model.TaskBulkUpdateByID(failedIDs, map[string]any{
			"fail_reason": fmt.Sprintf("获取渠道信息失败，请联系管理员，渠道ID：%d", channelId),
			"status":      "FAILURE",
			"progress":    "100%",
		})
		if err != nil {
			common.SysLog(fmt.Sprintf("UpdateSunoTask error: %v", err))
		}
		return err
	}
	adaptor := GetTaskAdaptorFunc(constant.TaskPlatformSuno)
	if adaptor == nil {
		return errors.New("adaptor not found")
	}
	proxy := ch.GetSetting().Proxy
	resp, err := adaptor.FetchTask(*ch.BaseURL, ch.Key, map[string]any{
		"ids": taskIds,
	}, proxy)
	if err != nil {
		common.SysLog(fmt.Sprintf("Get Task Do req error: %v", err))
		return err
	}
	if resp.StatusCode != http.StatusOK {
		logger.LogError(ctx, fmt.Sprintf("Get Task status code: %d", resp.StatusCode))
		return fmt.Errorf("Get Task status code: %d", resp.StatusCode)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		common.SysLog(fmt.Sprintf("Get Suno Task parse body error: %v", err))
		return err
	}
	var responseItems dto.TaskResponse[[]dto.SunoDataResponse]
	err = common.Unmarshal(responseBody, &responseItems)
	if err != nil {
		logger.LogError(ctx, fmt.Sprintf("Get Suno Task parse body error2: %v, body: %s", err, string(responseBody)))
		return err
	}
	if !responseItems.IsSuccess() {
		common.SysLog(fmt.Sprintf("渠道 #%d 未完成的任务有: %d, 成功获取到任务数: %s", channelId, len(taskIds), string(responseBody)))
		return err
	}

	for _, responseItem := range responseItems.Data {
		task := taskM[responseItem.TaskID]
		if !taskNeedsUpdate(task, responseItem) {
			continue
		}

		task.Status = lo.If(model.TaskStatus(responseItem.Status) != "", model.TaskStatus(responseItem.Status)).Else(task.Status)
		task.FailReason = lo.If(responseItem.FailReason != "", responseItem.FailReason).Else(task.FailReason)
		task.SubmitTime = lo.If(responseItem.SubmitTime != 0, responseItem.SubmitTime).Else(task.SubmitTime)
		task.StartTime = lo.If(responseItem.StartTime != 0, responseItem.StartTime).Else(task.StartTime)
		task.FinishTime = lo.If(responseItem.FinishTime != 0, responseItem.FinishTime).Else(task.FinishTime)
		if responseItem.FailReason != "" || task.Status == model.TaskStatusFailure {
			logger.LogInfo(ctx, task.TaskID+" 构建失败，"+task.FailReason)
			task.Progress = "100%"
			RefundTaskQuota(ctx, task, task.FailReason)
		}
		if responseItem.Status == model.TaskStatusSuccess {
			task.Progress = "100%"
		}
		task.Data = responseItem.Data

		err = task.Update()
		if err != nil {
			common.SysLog("UpdateSunoTask task error: " + err.Error())
		}
	}
	return nil
}

// taskNeedsUpdate 检查 Suno 任务是否需要更新
func taskNeedsUpdate(oldTask *model.Task, newTask dto.SunoDataResponse) bool {
	if oldTask.SubmitTime != newTask.SubmitTime {
		return true
	}
	if oldTask.StartTime != newTask.StartTime {
		return true
	}
	if oldTask.FinishTime != newTask.FinishTime {
		return true
	}
	if string(oldTask.Status) != newTask.Status {
		return true
	}
	if oldTask.FailReason != newTask.FailReason {
		return true
	}

	if (oldTask.Status == model.TaskStatusFailure || oldTask.Status == model.TaskStatusSuccess) && oldTask.Progress != "100%" {
		return true
	}

	oldData, _ := common.Marshal(oldTask.Data)
	newData, _ := common.Marshal(newTask.Data)

	sort.Slice(oldData, func(i, j int) bool {
		return oldData[i] < oldData[j]
	})
	sort.Slice(newData, func(i, j int) bool {
		return newData[i] < newData[j]
	})

	if string(oldData) != string(newData) {
		return true
	}
	return false
}

// UpdateVideoTasks 按渠道更新所有视频任务
func UpdateVideoTasks(ctx context.Context, platform constant.TaskPlatform, taskChannelM map[int][]string, taskM map[string]*model.Task) error {
	for channelId, taskIds := range taskChannelM {
		if err := updateVideoTasks(ctx, platform, channelId, taskIds, taskM); err != nil {
			logger.LogError(ctx, fmt.Sprintf("Channel #%d failed to update video async tasks: %s", channelId, err.Error()))
		}
	}
	return nil
}

func updateVideoTasks(ctx context.Context, platform constant.TaskPlatform, channelId int, taskIds []string, taskM map[string]*model.Task) error {
	logger.LogInfo(ctx, fmt.Sprintf("Channel #%d pending video tasks: %d", channelId, len(taskIds)))
	if len(taskIds) == 0 {
		return nil
	}
	cacheGetChannel, err := model.CacheGetChannel(channelId)
	if err != nil {
		// Collect DB primary key IDs for bulk update (taskIds are upstream IDs, not task_id column values)
		var failedIDs []int64
		for _, upstreamID := range taskIds {
			if t, ok := taskM[upstreamID]; ok {
				failedIDs = append(failedIDs, t.ID)
			}
		}
		errUpdate := model.TaskBulkUpdateByID(failedIDs, map[string]any{
			"fail_reason": fmt.Sprintf("Failed to get channel info, channel ID: %d", channelId),
			"status":      "FAILURE",
			"progress":    "100%",
		})
		if errUpdate != nil {
			common.SysLog(fmt.Sprintf("UpdateVideoTask error: %v", errUpdate))
		}
		return fmt.Errorf("CacheGetChannel failed: %w", err)
	}
	adaptor := GetTaskAdaptorFunc(platform)
	if adaptor == nil {
		return fmt.Errorf("video adaptor not found")
	}
	info := &relaycommon.RelayInfo{}
	info.ChannelMeta = &relaycommon.ChannelMeta{
		ChannelBaseUrl: cacheGetChannel.GetBaseURL(),
	}
	info.ApiKey = cacheGetChannel.Key
	adaptor.Init(info)
	for _, taskId := range taskIds {
		if err := updateVideoSingleTask(ctx, adaptor, cacheGetChannel, taskId, taskM); err != nil {
			logger.LogError(ctx, fmt.Sprintf("Failed to update video task %s: %s", taskId, err.Error()))
		}
		// sleep 1 second between each task to avoid hitting rate limits of upstream platforms
		time.Sleep(1 * time.Second)
	}
	return nil
}

// outputModerationCycleBudget 单轮轮询里产物审核可以占用的总时长。
//
// 整个轮询是**一个 goroutine 串到底**的：平台 → 渠道 → 任务三层 for 循环，
// 任务之间还有 1s 固定 sleep。产物审核塞在这条路上，花掉的每一秒都直接推迟
// 后面所有任务的状态更新——包括跟审核毫无关系的那些。
//
// 单次审核的上界是 mediaBatchBudget（30s）。判定服务挂掉又恰好是「连不上但不
// 立即拒绝」那种挂法时，一轮里完成 20 个任务就是 10 分钟的停摆，全站任务状态
// 集体卡住。这与「审核自己的故障不该影响用户调用」直接冲突。
//
// 所以按**轮**封顶而不是按任务：超预算后本轮剩下的任务跳过审核（漏审，会出声），
// 下一轮预算重置后照常审。60s 相当于允许两次最坏情况的单次超时，正常情况下
// （单张图约 160ms）够审几百个任务，根本碰不到这个上界。
const outputModerationCycleBudget = 60 * time.Second

// outputModerationSpent 本轮已花掉的审核时长。只在轮询这一个 goroutine 里读写，
// 不需要加锁。
//
// 走下面两个函数而不是直接读写：**忘记重置这件事已经发生过一次**——加预算时
// 注释和告警都写着「每轮重置」「下一轮恢复」，而重置那行压根没落进文件，于是它
// 变成进程级累计量，两次最坏超时就把产物审核永久关掉，且因为告警与守卫在同一个
// if 里，关得悄无声息。收成一对函数是为了让这个不变量能被测试钉住。
var outputModerationSpent time.Duration

// resetOutputModerationBudget 每轮轮询开头调用。
func resetOutputModerationBudget() {
	outputModerationSpent = 0
}

// outputModerationAllowed 本轮预算是否还够再审一个。
func outputModerationAllowed() bool {
	return outputModerationSpent < outputModerationCycleBudget
}

// maxPersistRetries 上游已完成但成品落 OBS 失败时的最大重试轮数。
// 轮询间隔 15s，20 轮 ≈ 5 分钟：足够扛过 OBS 瞬时抖动/管理员开启存储，超限判失败退款。
const maxPersistRetries = 20

func updateVideoSingleTask(ctx context.Context, adaptor TaskPollingAdaptor, ch *model.Channel, taskId string, taskM map[string]*model.Task) error {
	baseURL := constant.ChannelBaseURLs[ch.Type]
	if ch.GetBaseURL() != "" {
		baseURL = ch.GetBaseURL()
	}
	proxy := ch.GetSetting().Proxy

	task := taskM[taskId]
	if task == nil {
		logger.LogError(ctx, fmt.Sprintf("Task %s not found in taskM", taskId))
		return fmt.Errorf("task %s not found", taskId)
	}
	key := ch.Key

	privateData := task.PrivateData
	if privateData.Key != "" {
		key = privateData.Key
	}
	resp, err := adaptor.FetchTask(baseURL, key, map[string]any{
		"task_id": task.GetUpstreamTaskID(),
		"action":  task.Action,
	}, proxy)
	if err != nil {
		return fmt.Errorf("fetchTask failed for task %s: %w", taskId, err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("readAll failed for task %s: %w", taskId, err)
	}

	logger.LogDebug(ctx, fmt.Sprintf("updateVideoSingleTask response: %s", string(responseBody)))

	snap := task.Snapshot()

	taskResult := &relaycommon.TaskInfo{}
	// try parse as New API response format
	var responseItems dto.TaskResponse[model.Task]
	if err = common.Unmarshal(responseBody, &responseItems); err == nil && responseItems.IsSuccess() {
		logger.LogDebug(ctx, fmt.Sprintf("updateVideoSingleTask parsed as new api response format: %+v", responseItems))
		t := responseItems.Data
		taskResult.TaskID = t.TaskID
		taskResult.Status = string(t.Status)
		taskResult.Url = t.GetResultURL()
		taskResult.Progress = t.Progress
		taskResult.Reason = t.FailReason
		task.Data = t.Data
	} else if taskResult, err = adaptor.ParseTaskResult(responseBody); err != nil {
		return fmt.Errorf("parseTaskResult failed for task %s: %w", taskId, err)
	}

	task.Data = redactVideoResponseBody(responseBody)

	logger.LogDebug(ctx, fmt.Sprintf("updateVideoSingleTask taskResult: %+v", taskResult))

	now := time.Now().Unix()
	if taskResult.Status == "" {
		//taskResult = relaycommon.FailTaskInfo("upstream returned empty status")
		errorResult := &dto.GeneralErrorResponse{}
		if err = common.Unmarshal(responseBody, &errorResult); err == nil {
			openaiError := errorResult.TryToOpenAIError()
			if openaiError != nil {
				// 返回规范的 OpenAI 错误格式，提取错误信息，判断错误是否为任务失败
				if openaiError.Code == "429" {
					// 429 错误通常表示请求过多或速率限制，暂时不认为是任务失败，保持原状态等待下一轮轮询
					return nil
				}

				// 其他错误认为是任务失败，记录错误信息并更新任务状态
				taskResult = relaycommon.FailTaskInfo("upstream returned error")
			} else {
				// unknown error format, log original response
				logger.LogError(ctx, fmt.Sprintf("Task %s returned empty status with unrecognized error format, response: %s", taskId, string(responseBody)))
				taskResult = relaycommon.FailTaskInfo("upstream returned unrecognized message")
			}
		}
	}

	shouldRefund := false
	shouldSettle := false
	deferredPersist := false
	// pipelineAdvanced 本轮把聚合任务推进到了超分段。与 deferredPersist 同样的作用:
	// 阻止下面用上游进度(生成段的 100%)覆盖我们刚设的 InProgress —— 否则客户会看到
	// 一个"进度 100% 却仍在进行中"的任务。
	pipelineAdvanced := false
	quota := task.Quota

	task.Status = model.TaskStatus(taskResult.Status)
	// 排队回显每轮覆盖（含覆盖回 nil）：上一轮「前面还有 3 个」不能在这一轮说不准时
	// 留在页面上，那会让队伍看起来卡住不动。adaptor 已保证终态回 nil。
	task.Properties.QueueAhead = taskResult.QueueAhead
	task.Properties.EstimatedStartSeconds = taskResult.EstimatedStartSeconds
	switch taskResult.Status {
	case model.TaskStatusSubmitted:
		task.Progress = taskcommon.ProgressSubmitted
	case model.TaskStatusQueued:
		task.Progress = taskcommon.ProgressQueued
	case model.TaskStatusInProgress:
		task.Progress = taskcommon.ProgressInProgress
		if task.StartTime == 0 {
			task.StartTime = now
		}
	case model.TaskStatusSuccess:
		// 聚合流水线:生成段完成时先看还有没有后续段。必须**早于**下面的落盘与结算 ——
		// 一旦落了 OBS 就等于把中间产物(未超分的那一版)交付出去了,而客户要的是最终
		// 分辨率;那一版只该作为超分段的输入。
		//
		// 提交成功则任务留在 InProgress、上游 id 换成第二段的,轮询下一轮自然跟进,
		// 客户侧全程只有一个 task id。提交失败返回 false,继续走下面的正常收尾 ——
		// 交付生成段成品,好过把一个已经烧掉 GPU 的成功任务判失败。
		//
		// **不能在这里 return**:switch 之后才是把改动写回库的地方。提前返回会让
		// Stage=2 与新的上游 id 都不落库,下一轮轮询仍按旧 id 查到生成段 completed,
		// 于是**再提交一次超分**,循环重复提交并重复计费。
		if pipelineAdvanced = TryAdvanceAggregatePipeline(ctx, adaptor, task, taskResult, taskResult.NFSPath); pipelineAdvanced {
			task.Status = model.TaskStatusInProgress
			task.Progress = taskcommon.ProgressInProgress
			task.FinishTime = 0
			break
		}
		task.Progress = taskcommon.ProgressComplete
		if task.FinishTime == 0 {
			task.FinishTime = now
		}
		if ref, ok := PersistTaskResultToOBS(ctx, task, taskResult.NFSPath, taskResult.Url); ok {
			// 落盘成功：DB 只存内部占位符 obs://<key>，签名 URL 在序列化时实时生成（§5.2/§5.4）
			task.PrivateData.ResultURL = ref
			shouldSettle = true
		} else if taskResult.NFSPath != "" {
			// 自建成品只在 SFS 上（nfs_path），落 OBS 失败又无上游 URL → 对外没有可用 URL。
			// 成品已渲染完、还在 SFS 上，瞬时 OBS 抖动/存储未开启不应立刻丢弃：先不落终态，
			// 留在 InProgress 等下一轮轮询（上游状态仍是 completed，自然重试落盘）。
			// 超过重试上限（约 maxPersistRetries × 15s 轮询间隔）才判失败并退款。
			task.PrivateData.PersistRetryCount++
			if task.PrivateData.PersistRetryCount <= maxPersistRetries {
				task.Status = model.TaskStatusInProgress
				task.Progress = taskcommon.ProgressInProgress
				task.FinishTime = 0
				deferredPersist = true
				logger.LogWarn(ctx, fmt.Sprintf("Task %s: result persist to OBS failed, will retry next poll (%d/%d)",
					task.TaskID, task.PrivateData.PersistRetryCount, maxPersistRetries))
			} else {
				task.Status = model.TaskStatusFailure
				task.FailReason = "成品落盘 OBS 持续失败，无法对外提供访问 URL（请检查媒体存储配置/连通性）"
				logger.LogError(ctx, fmt.Sprintf("Task %s: nfs-only result persist to OBS failed after %d retries, marking failure",
					task.TaskID, maxPersistRetries))
				if quota != 0 {
					shouldRefund = true
				}
			}
		} else if strings.HasPrefix(taskResult.Url, "data:") {
			// data: URI (e.g. Vertex base64 encoded video) — keep in Data, not in ResultURL
			task.PrivateData.ResultURL = taskcommon.BuildProxyURL(task.TaskID)
			shouldSettle = true
		} else if taskResult.Url != "" {
			// Direct upstream URL (e.g. Kling, Ali, Doubao, etc.)
			task.PrivateData.ResultURL = taskResult.Url
			shouldSettle = true
		} else {
			// No URL from adaptor — construct proxy URL using public task ID
			task.PrivateData.ResultURL = taskcommon.BuildProxyURL(task.TaskID)
			shouldSettle = true
		}
		// 产物审核（挂载点 D-1，§12.4）。放在成功分支之后统一做：
		// 三种可送审形态（obs:// / 上游直链 / data:）都要审，分头挂会漏。
		//
		// 第二个地址传的是**上游原始 URL**，不能只传 ResultURL：上面 data: 那一支
		// 会把 ResultURL 改写成 BuildProxyURL（代理 URL 要本站鉴权，判定节点拉不到），
		// 只看 ResultURL 的话 Vertex 这类 base64 产物会 100% 被当成「拿不到地址」跳过。
		if task.Status == model.TaskStatusSuccess && ModerateTaskOutputFunc != nil && outputModerationAllowed() {
			started := time.Now()
			blocked, reason := ModerateTaskOutputFunc(ctx, task, task.PrivateData.ResultURL, taskResult.Url)
			outputModerationSpent += time.Since(started)
			if outputModerationSpent >= outputModerationCycleBudget {
				// 本轮预算刚被这次调用耗尽 → 本轮后续任务会跳过审核。必须出声：
				// 漏审和「审过了都没问题」在管理端长得一模一样。
				logger.LogWarn(ctx, fmt.Sprintf(
					"moderation: 本轮产物审核已用满 %s 预算，本轮后续完成的任务将跳过审核（下一轮恢复）",
					outputModerationCycleBudget))
			}
			if blocked {
				task.Status = model.TaskStatusFailure
				task.FailReason = reason
				// **结算照做**，不能因为拦截就跳过。
				//
				// 「上游返回用量计费」的任务（IsDeferredUsageBilling）在提交时刻意不写
				// 使用日志（controller/relay.go），完成时这一次结算是它**唯一**会留下的
				// 记账记录。跳过结算 + 不退费 = 余额扣了、使用日志里查无此单，用户在
				// 控制台既看不到也申诉不了。不退费是「按实际消耗收费」，不是「悄悄扣钱」。
				// **必须把产物地址一并清掉**，光改状态不够。
				//
				// 下游多个序列化口子不看 status：relay.TaskModel2Dto 无条件返回
				// ResolveResultURL(task.GetResultURL())，controller/task_download.go 的
				// 下载端点也只认 UserAuth 不认状态，几个渠道适配器的 ConvertToOpenAIVideo
				// 还会从 task.Data 里翻出上游地址。不清的话结果是「任务显示失败，
				// 违规产物照样能取走」——比不审更糟，因为记录里写着已拦截。
				task.PrivateData.ResultURL = ""
				task.Data = nil
				// 不退费（业务侧决定）。产物已经生成、上游那笔算力已经花掉，
				// 这里不做退款；用户看到的是任务失败 + fail_reason。
				logger.LogWarn(ctx, fmt.Sprintf("Task %s: output blocked by content moderation", task.TaskID))
			}
		}
	case model.TaskStatusFailure:
		logger.LogJson(ctx, fmt.Sprintf("Task %s failed", taskId), task)
		task.Status = model.TaskStatusFailure
		task.Progress = taskcommon.ProgressComplete
		if task.FinishTime == 0 {
			task.FinishTime = now
		}
		task.FailReason = taskResult.Reason
		logger.LogInfo(ctx, fmt.Sprintf("Task %s failed: %s", task.TaskID, task.FailReason))
		taskResult.Progress = taskcommon.ProgressComplete
		if quota != 0 {
			shouldRefund = true
		}
	default:
		return fmt.Errorf("unknown task status %s for task %s", taskResult.Status, task.TaskID)
	}
	if taskResult.Progress != "" && !deferredPersist && !pipelineAdvanced {
		task.Progress = taskResult.Progress
	}

	isDone := task.Status == model.TaskStatusSuccess || task.Status == model.TaskStatusFailure
	if isDone && snap.Status != task.Status {
		won, err := task.UpdateWithStatus(snap.Status)
		if err != nil {
			logger.LogError(ctx, fmt.Sprintf("UpdateWithStatus failed for task %s: %s", task.TaskID, err.Error()))
			shouldRefund = false
			shouldSettle = false
		} else if !won {
			logger.LogWarn(ctx, fmt.Sprintf("Task %s already transitioned by another process, skip billing", task.TaskID))
			shouldRefund = false
			shouldSettle = false
		}
	} else if !snap.Equal(task.Snapshot()) {
		if _, err := task.UpdateWithStatus(snap.Status); err != nil {
			logger.LogError(ctx, fmt.Sprintf("Failed to update task %s: %s", task.TaskID, err.Error()))
		}
	} else {
		// No changes, skip update
		logger.LogDebug(ctx, fmt.Sprintf("No update needed for task %s", task.TaskID))
	}

	if shouldSettle {
		settleTaskBillingOnComplete(ctx, adaptor, task, taskResult)
	}
	if shouldRefund {
		RefundTaskQuota(ctx, task, task.FailReason)
	}

	return nil
}

func redactVideoResponseBody(body []byte) []byte {
	var m map[string]any
	if err := common.Unmarshal(body, &m); err != nil {
		return body
	}
	resp, _ := m["response"].(map[string]any)
	if resp != nil {
		delete(resp, "bytesBase64Encoded")
		if v, ok := resp["video"].(string); ok {
			resp["video"] = truncateBase64(v)
		}
		if vs, ok := resp["videos"].([]any); ok {
			for i := range vs {
				if vm, ok := vs[i].(map[string]any); ok {
					delete(vm, "bytesBase64Encoded")
				}
			}
		}
	}
	b, err := common.Marshal(m)
	if err != nil {
		return body
	}
	return b
}

func truncateBase64(s string) string {
	const maxKeep = 256
	if len(s) <= maxKeep {
		return s
	}
	return s[:maxKeep] + "..."
}

// settleTaskBillingOnComplete 任务完成时的统一计费调整。
// 优先级：1. adaptor.AdjustBillingOnComplete 返回正数 → 使用 adaptor 计算的额度
//
//  2. taskResult.TotalTokens > 0 → 按 token 重算
//  3. 都不满足 → 保持预扣额度不变
func settleTaskBillingOnComplete(ctx context.Context, adaptor TaskPollingAdaptor, task *model.Task, taskResult *relaycommon.TaskInfo) {
	// 0. 按次计费的任务不做差额结算
	if bc := task.PrivateData.BillingContext; bc != nil && bc.PerCallBilling {
		logger.LogInfo(ctx, fmt.Sprintf("任务 %s 按次计费，跳过差额结算", task.TaskID))
		warnVideoMatrixSkipped(ctx, task, "任务被判定为按次计费")
		DeferredBillingFallback(ctx, task, "任务被判定为按次计费")
		return
	}

	// 0.5 聚合流水线的父任务:进入超分段后,最终这份 taskResult 来自**超分段**的上游回执,
	// 而 BillingContext 冻结的是**生成段**的定价上下文。照常按用量重算,就会拿生成段的
	// 模型去查超分后的分辨率(如 2K)的价 —— 生成段被按 2K 档收一次,超分子任务自己又按
	// SR 模型收一次,同一档分辨率付了两遍。图生视频那类"提交时单价为 0、分辨率由回执给出"
	// 的玩法尤其明显。
	//
	// 父任务代表的是生成段,它的价钱在生成段完成时就该定死,不能被第二段的回执改写。
	// 超分那笔由它自己的子任务独立结算(分段计费本来就是这个语义)。
	// **必须排在按次计费之后**:per_call / per_second 的模型价在提交时就定死了,
	// 那条分支会直接返回并保住冻结价。排在它前面的话,下面的 token 重算会把一个
	// 按次计费的父任务改成按 token 收费 —— 只要该模型恰好也配了 ratio,价钱就被悄悄换掉。
	if agg := task.PrivateData.Aggregate; agg != nil && agg.Stage == 2 {
		// 用**推进时留下的生成段回执**结算,而不是手上这份超分段的 taskResult。
		//
		// 两种偷懒都不行:拿超分回执算 → 生成段被按超分后的分辨率计费(2K 档付两遍);
		// 什么都不算直接回退预扣 → 预扣只是粗略锚点,视频计费矩阵的单价只在结算侧
		// 才查得出来,等于按不含分辨率/时长维度的 ModelRatio 收费。
		logger.LogInfo(ctx, fmt.Sprintf("任务 %s 为聚合流水线父任务，按生成段回执结算", task.TaskID))
		if agg.Stage1AdaptorQuota > 0 {
			RecalculateTaskQuota(ctx, task, agg.Stage1AdaptorQuota, "聚合流水线：生成段adaptor计费")
			warnVideoMatrixSkipped(ctx, task, "生成段 adaptor 抢先给出了额度")
			return
		}
		if RecalculateTaskQuotaByVideoMatrix(ctx, task, agg.Stage1BillableTokens, agg.Stage1Resolution) {
			return
		}
		if RecalculateTaskQuotaByTokens(ctx, task, agg.Stage1TotalTokens) {
			warnVideoMatrixSkipped(ctx, task, "聚合流水线：落到生成段的 token 重算")
			return
		}
		warnVideoMatrixSkipped(ctx, task, "聚合流水线：生成段回执未带可计费用量")
		DeferredBillingFallback(ctx, task, "聚合流水线：生成段回执未带可计费用量")
		return
	}
	// 1. 优先让 adaptor 决定最终额度
	if actualQuota := adaptor.AdjustBillingOnComplete(task, taskResult); actualQuota > 0 {
		RecalculateTaskQuota(ctx, task, actualQuota, "adaptor计费调整")
		warnVideoMatrixSkipped(ctx, task, "adaptor 抢先给出了额度")
		return
	}
	// 1.5 视频计费矩阵（运营可配）优先于通用的 token 重算：矩阵单价已含分辨率与
	//     视频输入两维，且不依赖 ModelRatio（模型可能配的是固定价格）。
	if RecalculateTaskQuotaByVideoMatrix(ctx, task, videoBillableTokens(taskResult), videoUpstreamResolution(taskResult)) {
		return
	}
	// 2. 回退到 token 重算
	if RecalculateTaskQuotaByTokens(ctx, task, taskResult.TotalTokens) {
		warnVideoMatrixSkipped(ctx, task, "落到了通用 token 重算")
		return
	}
	// 3. 无调整，保持预扣额度。延迟记账的任务到这里还一分钱没记过，必须兜底补记。
	warnVideoMatrixSkipped(ctx, task, "上游未返回可计费的 token 用量")
	DeferredBillingFallback(ctx, task, "未能按用量结算")
}

// videoBillableTokens 视频矩阵结算用的 token 数。
//
// total_tokens 可能被中转商吞掉（我们已见识过它们自加请求体上限这类改动）。
// doubao 的 usage 结构里没有 prompt_tokens（adaptor.go:95-98），视频任务也没有
// 输入侧 token，所以 completion_tokens 就是全部用量，兜底是等价而非近似。
//
// 不兜的话矩阵拿到 0 直接放弃，任务按预扣的 ModelRatio/2 收费——一个与配置单价
// 毫无关系的数，且全程无提示。
func videoBillableTokens(taskResult *relaycommon.TaskInfo) int {
	if taskResult == nil {
		return 0
	}
	if taskResult.TotalTokens > 0 {
		return taskResult.TotalTokens
	}
	return taskResult.CompletionTokens
}

// videoUpstreamResolution 上游回执里的实际出片档位，提交时定不出分辨率的任务
// （图生视频 / 参考生视频）靠它补查矩阵单价。
func videoUpstreamResolution(taskResult *relaycommon.TaskInfo) string {
	if taskResult == nil {
		return ""
	}
	return taskResult.Resolution
}

// warnVideoMatrixSkipped 冻结了视频计费矩阵、结算却没走矩阵分支时喊一声。
//
// 从「矩阵已配置」到「矩阵真的结算」中间有十来道守卫（见
// docs/video-billing-matrix-design.md §2.4），任何一道关上，矩阵都会**静默**失效、
// 任务按预扣额度收费。这条 WARN 是最后一道网：将来再多出一道门，线上会喊，
// 而不是等对账差额被人发现。
func warnVideoMatrixSkipped(ctx context.Context, task *model.Task, reason string) {
	bc := task.PrivateData.BillingContext
	if bc == nil || bc.VideoBilling == nil || bc.VideoBilling.Mode != ratio_setting.VideoPriceModeToken {
		return
	}
	// 单价为 0 = 提交时定不出分辨率（图生视频 / 参考生视频不下发 size），本该由上游
	// 回执补查。走到这里说明回执也没给出可用档位，与「矩阵配了却没生效」是两回事，
	// 分开说才知道该去查哪一头。
	if bc.VideoBilling.UnitPrice <= 0 {
		logger.LogWarn(ctx, fmt.Sprintf(
			"任务 %s 提交时未定出视频档位、上游回执也未给出分辨率，结算未走矩阵：%s。请核对对账差额。",
			task.TaskID, reason))
		return
	}
	logger.LogWarn(ctx, fmt.Sprintf(
		"任务 %s 已冻结视频计费矩阵(%s/%s，单价 %g)，但结算未走矩阵：%s。本单按预扣额度 %d 收费，请核对对账差额。",
		task.TaskID, bc.VideoBilling.Resolution, videoInputLabel(bc.VideoBilling.HasVideoInput),
		bc.VideoBilling.UnitPrice, reason, task.Quota))
}

// ModerateTaskOutputFunc 产物审核钩子。由 main.go 注入 moderation.ModerateTaskOutput。
//
// 用函数变量而不是直接调用：service/moderation 已经依赖 service
// （keyword.go 用 service.AcSearch、l1 用 service.GetHttpClient），
// 反向 import 会成环。这与 GetTaskAdaptorFunc 打破 service → relay 环是同一个手法。
//
// 未注入时为 nil，调用点会跳过——产物审核是可选能力，没接上不该影响任务交付。
var ModerateTaskOutputFunc func(ctx context.Context, task *model.Task, resultURL, upstreamURL string) (blocked bool, reason string)
