package dto

// 异步图片任务的对外响应对象（见 docs/image-async-task-design.md §2）。
//
// 形状取自 OpenAI 的长任务约定（id + object + status + created_at），但 data 字段
// 刻意与同步的 ImageResponse.Data 同形：调用方从同步切异步，只需把「读 resp.data」
// 改成「轮询到 completed 后读 resp.data」，解析代码零改动。
//
// 状态词表与 dto.VideoStatus* 共用同一组字符串常量（queued/in_progress/completed/
// failed），避免同一个网关对外吐两套状态词。取消额外多一个 cancelled——任务表里
// 不存在 CANCELLED 状态，它由 FailReason 在响应层渲染而来（设计 §5.5）。

const (
	ImageJobObject = "image.generation.job"

	ImageJobStatusQueued     = VideoStatusQueued
	ImageJobStatusInProgress = VideoStatusInProgress
	ImageJobStatusCompleted  = VideoStatusCompleted
	ImageJobStatusFailed     = VideoStatusFailed
	ImageJobStatusCancelled  = "cancelled"
)

type ImageJob struct {
	ID          string         `json:"id"`
	Object      string         `json:"object"`
	Model       string         `json:"model,omitempty"`
	Status      string         `json:"status"`
	Progress    string         `json:"progress,omitempty"`
	CreatedAt   int64          `json:"created_at"`
	CompletedAt int64          `json:"completed_at,omitempty"`
	Data        []ImageData    `json:"data,omitempty"`
	Usage       *Usage         `json:"usage,omitempty"`
	Error       *ImageJobError `json:"error,omitempty"`
	// QueueAhead / EstimatedStartSeconds 排队回显：还要等几次生成、大约多少秒。
	// 只有自建 GPUStack 门面能给（它知道任务落在哪个实例的队列上），其余渠道恒省略。
	// 指针 + omitempty：0 是「下一个就是我」，缺省是「说不准」，两者在前端是不同的话；
	// 用值类型会把「说不准」折成 0，等于承诺马上开始。
	QueueAhead            *int `json:"queue_ahead,omitempty"`
	EstimatedStartSeconds *int `json:"estimated_start_seconds,omitempty"`
}

type ImageJobError struct {
	Message string `json:"message"`
	Type    string `json:"type,omitempty"`
	Code    string `json:"code,omitempty"`
}

// NewImageJob 构造提交时返回的 202 响应体。
func NewImageJob(taskID, model string, createdAt int64) *ImageJob {
	return &ImageJob{
		ID:        taskID,
		Object:    ImageJobObject,
		Model:     model,
		Status:    ImageJobStatusQueued,
		CreatedAt: createdAt,
	}
}
