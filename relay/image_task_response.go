package relay

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// 异步图片任务的查询响应（docs/image-async-task-design.md §2.2、§5.4）。

// imageFetchByIDRespBodyBuilder 构建 GET /v1/images/generations/{task_id} 的响应体。
// 与 videoFetchByIDRespBodyBuilder 同构，区别只在对外形状：图片回 image.generation.job，
// data[] 与同步 ImageResponse.Data 同形，好让调用方复用解析代码。
func imageFetchByIDRespBodyBuilder(c *gin.Context) (respBody []byte, taskResp *dto.TaskError) {
	taskId := c.Param("task_id")
	if taskId == "" {
		taskId = c.GetString("task_id")
	}
	userId := c.GetInt("id")

	// 必须带 user_id 查：否则任何人拿到一个 task_id 就能读他人的生成结果。
	originTask, exist, err := model.GetByTaskId(userId, taskId)
	if err != nil {
		return nil, service.TaskErrorWrapper(err, "get_task_failed", http.StatusInternalServerError)
	}
	if !exist || !IsImageTask(originTask) {
		return nil, service.TaskErrorWrapperLocal(errors.New("task_not_exist"), "task_not_exist", http.StatusBadRequest)
	}

	job := BuildImageJob(c.Request.Context(), originTask)
	respBody, err = common.Marshal(job)
	if err != nil {
		return nil, service.TaskErrorWrapper(err, "marshal_response_failed", http.StatusInternalServerError)
	}
	return respBody, nil
}

// BuildImageJob 把任务行渲染成对外的 job 对象。
// 供查询端点与取消端点共用，两处形状必须一致。
func BuildImageJob(ctx context.Context, task *model.Task) *dto.ImageJob {
	job := &dto.ImageJob{
		ID:        task.TaskID,
		Object:    dto.ImageJobObject,
		Model:     imageJobModelName(task),
		Status:    imageJobStatus(task),
		Progress:  task.Progress,
		CreatedAt: task.SubmitTime,
	}
	if task.FinishTime > 0 {
		job.CompletedAt = task.FinishTime
	}

	switch job.Status {
	case dto.ImageJobStatusCompleted:
		// DB 里存的是 obs://<key> 占位符，签名 URL 有有效期、不能存库，
		// 每次查询实时签发。这套机制与视频共用。
		if url := service.ResolveResultURL(ctx, task.GetResultURL()); url != "" {
			job.Data = []dto.ImageData{{Url: url}}
		}
		// 图片按次计费，没有真实 token 用量；给一个占位值与同步链路保持一致
		// （同步的 gpustackplus DoResponse 也是回 PromptTokens/TotalTokens = 1）。
		job.Usage = &dto.Usage{PromptTokens: 1, TotalTokens: 1}
	case dto.ImageJobStatusFailed, dto.ImageJobStatusCancelled:
		job.Error = &dto.ImageJobError{
			Message: task.FailReason,
			Type:    "upstream_error",
		}
		if job.Status == dto.ImageJobStatusCancelled {
			job.Error.Type = "cancelled"
		}
	}
	return job
}

// imageJobStatus 把内部七态映射到对外状态词表。
//
// CANCELLED 不是任务表里的状态，而是由 PrivateData.Cancelled 在这里渲染出来的
// （设计 §5.5：不为取消新增第八态）。判定必须先于 FAILURE —— 取消的任务终态就是 FAILURE。
func imageJobStatus(task *model.Task) string {
	switch task.Status {
	case model.TaskStatusSuccess:
		return dto.ImageJobStatusCompleted
	case model.TaskStatusFailure:
		if task.PrivateData.Cancelled {
			return dto.ImageJobStatusCancelled
		}
		return dto.ImageJobStatusFailed
	case model.TaskStatusInProgress:
		return dto.ImageJobStatusInProgress
	default:
		// NOT_START / SUBMITTED / QUEUED / UNKNOWN 对调用方是同一件事：还没开始出图。
		return dto.ImageJobStatusQueued
	}
}

// imageJobModelName 回显调用方请求时用的公开模型名，不是重定向后的上游名。
func imageJobModelName(task *model.Task) string {
	if name := strings.TrimSpace(task.Properties.OriginModelName); name != "" {
		return name
	}
	return strings.TrimSpace(task.Properties.UpstreamModelName)
}
