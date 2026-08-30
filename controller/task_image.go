package controller

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// RelayTaskCancel 取消一个异步图片任务：DELETE /v1/images/generations/{task_id}
// （设计见 docs/image-async-task-design.md §2.3、§5.5）。
//
// 顺序是刻意的：先向上游取消（让 GPU 尽早停下），再抢本地终态，最后退款。
// 反过来先抢终态的话，若上游取消失败，任务在我们这边已经是「已取消 + 已退款」，
// 而 GPU 还在算并且会产出一个没人认领的结果 —— 白烧且对不上账。
func RelayTaskCancel(c *gin.Context) {
	taskID := strings.TrimSpace(c.Param("task_id"))
	userID := c.GetInt("id")
	if taskID == "" {
		c.JSON(http.StatusBadRequest, imageJobErrorBody("task_id is required", "invalid_request_error"))
		return
	}

	// 必须带 user_id 查：否则任何人拿到 task_id 就能取消他人的任务。
	task, exist, err := model.GetByTaskId(userID, taskID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, imageJobErrorBody(err.Error(), "internal_error"))
		return
	}
	// 非图片任务在本端点下就是不存在（relay.IsImageTask 的注释里有完整理由）。
	// 放行的话，用户拿自己的视频 task_id 打过来就能把那条正在跑的视频任务标成失败并退款
	// —— 一次端点用错的代价太大。与「不存在」共用同一个响应，不额外区分。
	if !exist || !relay.IsImageTask(task) {
		c.JSON(http.StatusNotFound, imageJobErrorBody("task not found: "+taskID, "invalid_request_error"))
		return
	}

	// 幂等：已经是终态就直接回显当前状态，不重复取消、不重复退款。
	if isTerminalTaskStatus(task.Status) {
		c.JSON(http.StatusOK, relay.BuildImageJob(c.Request.Context(), task))
		return
	}

	cancelUpstreamTask(c, task)

	// 抢本地终态。与轮询循环存在竞态：它可能正好把同一条任务推到 SUCCESS/FAILURE。
	// CAS 输了说明轮询已经处理完并会自己结算/退款 —— 这里就不能再退一次，
	// 重新读一遍任务回显它的真实状态即可。
	oldStatus := task.Status
	task.Status = model.TaskStatusFailure
	task.Progress = "100%"
	task.FinishTime = common.GetTimestamp()
	task.FailReason = "用户取消"
	task.PrivateData.Cancelled = true

	won, err := task.UpdateWithStatus(oldStatus)
	if err != nil {
		c.JSON(http.StatusInternalServerError, imageJobErrorBody("cancel task failed: "+err.Error(), "internal_error"))
		return
	}
	if !won {
		logger.LogInfo(c, "task "+taskID+" already transitioned before cancel, skip refund")
		if latest, ok, lerr := model.GetByTaskId(userID, taskID); lerr == nil && ok {
			task = latest
		}
		c.JSON(http.StatusOK, relay.BuildImageJob(c.Request.Context(), task))
		return
	}

	// 全额退款。QUEUED 与 IN_PROGRESS 一律全额退（设计 §5.6）：后者 GPU 确实算了一半，
	// 但图片单价低，分段计费的实现与对账复杂度不划算，且与「超时失败全额退」口径一致。
	// RefundTaskQuota 本身不幂等，靠上面这次 CAS 保证只调用一次。
	if task.Quota != 0 {
		service.RefundTaskQuota(c.Request.Context(), task, "用户取消")
	}

	c.JSON(http.StatusOK, relay.BuildImageJob(c.Request.Context(), task))
}

// cancelUpstreamTask 尽力通知上游停止计算。失败只记日志不阻断：用户的取消意图应当
// 被满足（本地标记 + 退款照做），上游没停最多是白烧一次 GPU，产物由上游的清理任务兜底。
func cancelUpstreamTask(c *gin.Context, task *model.Task) {
	upstreamID := task.GetUpstreamTaskID()
	if upstreamID == "" {
		return
	}
	adaptor := relay.GetTaskAdaptor(task.Platform)
	if adaptor == nil {
		return
	}
	canceller, ok := adaptor.(channel.TaskCanceller)
	if !ok {
		// 上游没有 cancel 能力。不是错误——见 channel.TaskCanceller 的注释。
		return
	}
	ch, err := model.GetChannelById(task.ChannelId, true)
	if err != nil {
		logger.LogWarn(c, "cancel task "+task.TaskID+": get channel failed: "+err.Error())
		return
	}
	baseURL := constant.ChannelBaseURLs[ch.Type]
	if ch.GetBaseURL() != "" {
		baseURL = ch.GetBaseURL()
	}
	key := ch.Key
	if task.PrivateData.Key != "" {
		key = task.PrivateData.Key
	}
	if err := canceller.CancelTask(c.Request.Context(), baseURL, key, upstreamID, ch.GetSetting().Proxy); err != nil {
		logger.LogWarn(c, "cancel upstream task "+upstreamID+" failed: "+err.Error())
	}
}

func isTerminalTaskStatus(status model.TaskStatus) bool {
	return status == model.TaskStatusSuccess || status == model.TaskStatusFailure
}

func imageJobErrorBody(message, errType string) gin.H {
	return gin.H{"error": &dto.ImageJobError{Message: message, Type: errType}}
}
