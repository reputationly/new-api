package dto

import (
	"encoding/json"
)

type TaskError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	Data       any    `json:"data"`
	StatusCode int    `json:"-"`
	LocalError bool   `json:"-"`
	Error      error  `json:"-"`
}

type TaskData interface {
	SunoDataResponse | []SunoDataResponse | string | any
}

const TaskSuccessCode = "success"

type TaskResponse[T TaskData] struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Data    T      `json:"data"`
}

func (t *TaskResponse[T]) IsSuccess() bool {
	return t.Code == TaskSuccessCode
}

type TaskDto struct {
	ID         int64  `json:"id"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
	TaskID     string `json:"task_id"`
	Platform   string `json:"platform"`
	UserId     int    `json:"user_id"`
	Group      string `json:"group"`
	ChannelId  int    `json:"channel_id"`
	Quota      int    `json:"quota"`
	Action     string `json:"action"`
	Status     string `json:"status"`
	FailReason string `json:"fail_reason"`
	ResultURL  string `json:"result_url,omitempty"` // 任务结果 URL（视频地址等）
	SubmitTime int64  `json:"submit_time"`
	StartTime  int64  `json:"start_time"`
	FinishTime int64  `json:"finish_time"`
	Progress   string `json:"progress"`
	// PipelineStage 流水线当前在第几段：1=生成段，2=增强段(sr)，3=配音段(v2a)。
	// 0/缺省 = 这条任务没有后置段（或已全部完成）。
	//
	// 为什么要下发：后置段改由**服务端**推进之后，客户端不再知道"已经切到第二段"这个
	// 事实（它以前靠本地消息状态判断）。让前端按进度区间反推是把展示逻辑挂在别人的
	// 进度算法上 —— 那边一调 clamp 就跟着错。
	PipelineStage int             `json:"pipeline_stage,omitempty"`
	Properties    any             `json:"properties"`
	Username      string          `json:"username,omitempty"`
	Data          json.RawMessage `json:"data"`
}

type FetchReq struct {
	IDs []string `json:"ids"`
}
