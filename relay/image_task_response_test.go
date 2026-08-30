package relay

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
)

func imageTask(status model.TaskStatus) *model.Task {
	t := &model.Task{
		TaskID:     "task_abc",
		Status:     status,
		Progress:   "45%",
		SubmitTime: 1756512000,
	}
	t.Properties.OriginModelName = "z-image"
	t.Properties.UpstreamModelName = "z-image-upstream"
	return t
}

// 内部七态到对外四态的映射。错一档的后果是客户端要么提前停止轮询（把进行中读成终态），
// 要么永远轮下去（把终态读成进行中）。
func TestImageJobStatusMapping(t *testing.T) {
	cases := []struct {
		name   string
		status model.TaskStatus
		want   string
	}{
		{"未开始", model.TaskStatusNotStart, dto.ImageJobStatusQueued},
		{"已提交", model.TaskStatusSubmitted, dto.ImageJobStatusQueued},
		{"排队中", model.TaskStatusQueued, dto.ImageJobStatusQueued},
		{"未知状态也当排队", model.TaskStatusUnknown, dto.ImageJobStatusQueued},
		{"进行中", model.TaskStatusInProgress, dto.ImageJobStatusInProgress},
		{"成功", model.TaskStatusSuccess, dto.ImageJobStatusCompleted},
		{"失败", model.TaskStatusFailure, dto.ImageJobStatusFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := imageJobStatus(imageTask(tc.status)); got != tc.want {
				t.Errorf("status %s → %q, want %q", tc.status, got, tc.want)
			}
		})
	}
}

// 取消的任务在表里就是 FAILURE，靠 PrivateData.Cancelled 渲染成 cancelled
// （设计 §5.5：不为取消新增第八态）。判定必须先于 FAILURE 分支。
func TestImageJobStatusCancelledOverridesFailure(t *testing.T) {
	task := imageTask(model.TaskStatusFailure)
	task.PrivateData.Cancelled = true

	if got := imageJobStatus(task); got != dto.ImageJobStatusCancelled {
		t.Errorf("cancelled task → %q, want %q", got, dto.ImageJobStatusCancelled)
	}

	job := BuildImageJob(context.Background(), task)
	if job.Error == nil || job.Error.Type != "cancelled" {
		t.Errorf("cancelled job error = %+v, want type cancelled", job.Error)
	}
}

func TestBuildImageJobCompleted(t *testing.T) {
	task := imageTask(model.TaskStatusSuccess)
	task.FinishTime = 1756512042
	task.PrivateData.ResultURL = "https://obs.example.com/signed.png"

	job := BuildImageJob(context.Background(), task)

	if job.Object != dto.ImageJobObject {
		t.Errorf("object = %q, want %q", job.Object, dto.ImageJobObject)
	}
	if job.ID != "task_abc" {
		t.Errorf("id = %q, want task_abc", job.ID)
	}
	// 回显的必须是调用方请求时用的公开名，不是重定向后的上游名 ——
	// 否则客户端会看到一个它从没请求过的模型。
	if job.Model != "z-image" {
		t.Errorf("model = %q, want z-image (public name, not upstream)", job.Model)
	}
	if job.CompletedAt != 1756512042 {
		t.Errorf("completed_at = %d, want 1756512042", job.CompletedAt)
	}
	// data[] 与同步 ImageResponse.Data 同形是对外契约的一部分：调用方从同步切异步时
	// 解析代码要能零改动复用。
	if len(job.Data) != 1 || job.Data[0].Url != "https://obs.example.com/signed.png" {
		t.Errorf("data = %+v, want one item with the result url", job.Data)
	}
	if job.Error != nil {
		t.Errorf("completed job should not carry an error, got %+v", job.Error)
	}
}

func TestBuildImageJobFailedCarriesReason(t *testing.T) {
	task := imageTask(model.TaskStatusFailure)
	task.FailReason = "engine OOM"

	job := BuildImageJob(context.Background(), task)

	if job.Status != dto.ImageJobStatusFailed {
		t.Errorf("status = %q, want failed", job.Status)
	}
	if job.Error == nil || job.Error.Message != "engine OOM" {
		t.Errorf("error = %+v, want message 'engine OOM'", job.Error)
	}
	// 失败的任务不该带 data —— 客户端见到非空 data 会以为出图成功了。
	if len(job.Data) != 0 {
		t.Errorf("failed job should not carry data, got %+v", job.Data)
	}
}

// 进行中的任务不能带 data：即便 ResultURL 因为某些原因已经有值
// （比如落盘重试路径中途写过），也不能让客户端提前把它当成成品拿走。
func TestBuildImageJobInProgressHasNoData(t *testing.T) {
	task := imageTask(model.TaskStatusInProgress)
	task.PrivateData.ResultURL = "https://obs.example.com/partial.png"

	job := BuildImageJob(context.Background(), task)

	if job.Status != dto.ImageJobStatusInProgress {
		t.Errorf("status = %q, want in_progress", job.Status)
	}
	if len(job.Data) != 0 {
		t.Errorf("in-progress job leaked data: %+v", job.Data)
	}
	if job.Progress != "45%" {
		t.Errorf("progress = %q, want 45%%", job.Progress)
	}
}

// 非图片任务在图片端点下必须视同不存在（code review P2）。
//
// task_id 在**所有**任务类型间共用同一个 task_xxxx 格式，图片与视频还共用同一个
// platform 列，唯一的区分依据就是 api_protocol。不守这道门的话：
//   - GET  /v1/images/generations/{视频id} 会回一个 image.generation.job 外壳，
//     data[].url 后面是段视频；
//   - DELETE 同一个 id 会把正在跑的视频任务标成失败并退款。
//
// 这与 MiniMax v2 兼容层的 minimaxv2.IsV2Task 是同一条规矩。
func TestIsImageTaskGuard(t *testing.T) {
	cases := []struct {
		name     string
		protocol string
		want     bool
	}{
		{"异步图片任务", model.TaskAPIProtocolImage, true},
		{"视频任务（api_protocol 为空）", "", false},
		{"MiniMax v2 任务", model.TaskAPIProtocolMiniMaxV2, false},
		{"未知协议", "something_else", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task := imageTask(model.TaskStatusSuccess)
			task.APIProtocol = tc.protocol
			if got := IsImageTask(task); got != tc.want {
				t.Errorf("IsImageTask(api_protocol=%q) = %v, want %v", tc.protocol, got, tc.want)
			}
		})
	}
}

func TestIsImageTaskNilSafe(t *testing.T) {
	if IsImageTask(nil) {
		t.Error("IsImageTask(nil) must be false")
	}
}
