package controller

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// RelayVideoCancel 取消一个排队中的视频任务：DELETE /v1/videos/{task_id}。
//
// 只有 queued 能取消。一旦上游开跑，算力已经在烧、费用照收，假装取消成功却照样出片
// 比明确说做不到更糟 —— 所以运行中与各终态一律回冲突错误，不改状态、不退款。
//
// 顺序是**先抢本地终态（CAS），赢了才通知上游，最后退款** —— 与 RelayTaskCancel
// （异步图片）刻意相反。理由见下面 CAS 那一段：抢在 CAS 之前发上游请求，会在输掉竞态时
// 出现「对调用方说取消失败、却已经把上游记录删掉」。cancelArkV3Task 同此。
func RelayVideoCancel(c *gin.Context) {
	taskID := strings.TrimSpace(c.Param("task_id"))
	userID := c.GetInt("id")
	if taskID == "" {
		respondTaskError(c, &dto.TaskError{
			Code: "invalid_request", Message: "task_id is required", StatusCode: http.StatusBadRequest,
		})
		return
	}

	// 必须带 user_id 查：否则任何人拿到 task_id 就能取消他人的任务。
	task, exist, err := model.GetByTaskId(userID, taskID)
	if err != nil {
		respondTaskError(c, &dto.TaskError{
			Code: "get_task_failed", Message: err.Error(), StatusCode: http.StatusInternalServerError,
		})
		return
	}
	// 挡住 action 能区分出来的那几类：图片（imageGenerate / imageEdit）与 Suno
	// （MUSIC / LYRICS）。拿它们的 task_id 打过来会被当成「不存在」，不额外区分。
	//
	// ⚠️ 这道闸**挡不住同走任务子系统的音频 / 超分任务**：gpustackplus 把所有非图片任务
	// 的 action 一律写成 generate（taskActionOf），TTS、音乐、配音（v2a）、超分（sr）
	// 与视频在这一维上完全同形，我们手里也没有第二个能分辨它们的列。
	// 这不是资金缺口 —— 取消的是调用方自己的任务、退款金额正确、上游也会被正确停下；
	// 而且同样的可达集 GET /v1/videos/{task_id} 本来就有（那边一样不看任务类型）。
	// 真要收窄的话得先给任务记录补一个媒体类型维度，那是独立一件事。
	if !exist || task == nil || !constant.IsVideoTaskAction(task.Action) {
		respondTaskError(c, &dto.TaskError{
			Code: "task_not_exist", Message: "task not found: " + taskID, StatusCode: http.StatusNotFound,
		})
		return
	}

	if task.Status == model.TaskStatusInProgress || task.Status == model.TaskStatusSuccess ||
		task.Status == model.TaskStatusFailure {
		respondTaskError(c, &dto.TaskError{
			Code:       "task_not_cancellable",
			Message:    "task " + taskID + " has already left the queue and can no longer be cancelled",
			StatusCode: http.StatusConflict,
		})
		return
	}

	// 先抢本地终态，**赢了才动上游**。与轮询循环存在竞态：它可能正好把同一条任务推到
	// SUCCESS/FAILURE。CAS 输了说明轮询已经处理完并会自己结算/退款 —— 这里就不能再退
	// 一次，也不该再去碰上游。
	//
	// 顺序与 RelayTaskCancel（异步图片）相反，是刻意的：那边的注释说「先取消上游让算力
	// 尽早停下」，但 cancelUpstreamTask 忽略所有错误、fire-and-forget，两种顺序下「上游
	// 没停」的结果完全一样，差别只有一次 DB 写的毫秒级延迟。而抢在 CAS 之前发请求有真实
	// 代价：CAS 输了时我们对调用方回「取消失败、任务保留、费用照收」，却已经对上游发过
	// 一个破坏性请求 —— 火山的 DELETE 对终态任务是「删记录」而不是「取消」
	// （见 doubao.CancelTask），那条上游记录就没了，事后对账也失去依据。
	oldStatus := task.Status
	task.Status = model.TaskStatusFailure
	task.Progress = "100%"
	task.FinishTime = common.GetTimestamp()
	task.FailReason = "用户取消"
	task.PrivateData.Cancelled = true

	won, err := task.UpdateWithStatus(oldStatus)
	if err != nil {
		respondTaskError(c, &dto.TaskError{
			Code: "cancel_task_failed", Message: err.Error(), StatusCode: http.StatusInternalServerError,
		})
		return
	}
	if !won {
		logger.LogInfo(c, "task "+taskID+" already transitioned before cancel, skip refund")
		respondTaskError(c, &dto.TaskError{
			Code:       "task_not_cancellable",
			Message:    "task " + taskID + " left the queue before the cancellation took effect",
			StatusCode: http.StatusConflict,
		})
		return
	}

	// CAS 赢了 = 我们独占地把这条任务从排队推到了终态，取消这件事已经定下。此刻无论
	// 上游处于什么状态，那条记录我们都不会再用（不再轮询、不交付、已退款），可以放心
	// 通知它停下。
	cancelUpstreamTask(c, task)

	// 全额退款。任务还没开跑，上游没有产生任何用量。RefundTaskQuota 本身不幂等，
	// 靠上面那次 CAS 保证只调用一次。
	if task.Quota != 0 {
		service.RefundTaskQuota(c.Request.Context(), task, "用户取消")
	}

	c.JSON(http.StatusOK, task.ToOpenAIVideo())
}
