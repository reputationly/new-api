// Package minimaxv2 实现 MiniMax v2 官方视频协议(platform.minimax.io 的
// /v2/video_generation 系列)的兼容层:官方 API 用户改 base_url + key + model 就能切过来,
// 请求体、响应体、错误信封与官方逐字段一致。
//
// **model 要改**:官方那边 model 只有 MiniMax-H3 一个值、一个名字覆盖全部玩法,而我们
// 按玩法拆成两套部署 —— minimax-h3-fl2va(纯文本 / 首帧 / 尾帧 / 首尾帧)与
// minimax-h3-ref2va(多模态参考)。这是刻意的产品选择(2026-08-11 决策):
// 用一个对外名 + 渠道重定向做不到按 task_type 分流,ModelMappedHelper 是静态的
// name→name 映射,而 GPUStack 门面是按 model 选实例的 —— 硬凑只会让其中一类玩法
// 在引擎侧失败。本层不校验模型名(原样透传),选错由引擎拒绝。
//
// 边界(设计见 docs/minimax-h3-playground-design.md §七の二):
//
//   - 范围是**主流程 + 任务管理**,不含 callback_url 回调(那是独立基础设施)。
//     callback_url 传了会**显式 400**,不静默丢弃——静默丢弃的后果是调用方在那儿
//     等一个永远不会来的推送。
//   - 与 Kling / 即梦那两个兼容层的做法不同:它们把原始 body 整个塞进 metadata 就完事,
//     因为它们的上游**就是**对应厂商、适配器认得那些字段。我们上游是自建引擎
//     (gpustackplus 门面 + vllm-omni),必须做真转换,见 convert.go。
//   - 官方协议里我们做不到的那几项一律显式拒绝并写明原因,不伪装支持;特别是
//     **不伪造 422 敏感内容**——我们没有审核环节,编一个不存在的审核结果是欺骗。
package minimaxv2

import (
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"

	"github.com/gin-gonic/gin"
)

// ── 请求 ─────────────────────────────────────────────────────────────────────

// CreateRequest 是 POST /v2/video_generation 的官方请求体。
//
// Duration 用指针:官方把它定义为必填,而非指针无法区分「没传」与「传了 0」——
// 后者要报 4–15 越界,前者要报缺字段,两种错误对调用方的指引完全不同。
type CreateRequest struct {
	Model       string        `json:"model"`
	Content     []ContentItem `json:"content"`
	Resolution  string        `json:"resolution"`
	Duration    *int          `json:"duration"`
	Ratio       string        `json:"ratio"`
	CallbackURL string        `json:"callback_url"`
}

// ContentItem 是官方 content[] 的一项。type 决定读哪个媒体字段,role 决定它的语义。
type ContentItem struct {
	Type     string    `json:"type"`
	Text     string    `json:"text"`
	Role     string    `json:"role"`
	ImageURL *MediaRef `json:"image_url"`
	VideoURL *MediaRef `json:"video_url"`
	AudioURL *MediaRef `json:"audio_url"`
}

type MediaRef struct {
	URL string `json:"url"`
}

// ── 响应 ─────────────────────────────────────────────────────────────────────

// CreateResponse:官方提交接口只回一个 task_id(不是 OpenAI 那种完整 video 对象)。
type CreateResponse struct {
	TaskID string `json:"task_id"`
}

type QueryResponse struct {
	Task Task `json:"task"`
}

type ListResponse struct {
	Items []Task `json:"items"`
	Total int    `json:"total"`
}

type DeleteResponse struct {
	TaskID string `json:"task_id"`
	Action string `json:"action"`
	Status string `json:"status"`
}

// Task 是官方查询/列表接口里的任务对象。
//
// Ratio / TaskType / Modality 不加 omitempty:官方 schema 里它们恒存在
// (ratio 明确写了「不适用于该任务类型时可以是空串」),省掉会让按字段存在性判断的
// 客户端走进「字段缺失」分支。
type Task struct {
	ID         string     `json:"id"`
	Model      string     `json:"model"`
	Status     string     `json:"status"`
	Error      *TaskError `json:"error,omitempty"`
	CreatedAt  int64      `json:"created_at"`
	UpdatedAt  int64      `json:"updated_at"`
	Content    *Content   `json:"content,omitempty"`
	Resolution string     `json:"resolution,omitempty"`
	Duration   int        `json:"duration,omitempty"`
	Usage      *Usage     `json:"usage,omitempty"`
	Ratio      string     `json:"ratio"`
	TaskType   string     `json:"task_type"`
	Modality   string     `json:"modality"`
}

type Content struct {
	URL string `json:"url"`
}

type TaskError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Usage 报的是**用量**(秒数、张数),不是钱。四个字段全部由提交时的请求推出,
// 与 ModelPrice / VideoPricing 配没配无关,也不读 Task.Quota(那是钱)。
type Usage struct {
	TotalSeconds    int `json:"total_seconds"`
	InputSeconds    int `json:"input_seconds"`
	OutputSeconds   int `json:"output_seconds"`
	InputImageCount int `json:"input_image_count"`
}

// 官方任务状态词。我们内部的 TaskStatus 在 taskStatusToV2 里映射过来。
const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

// 官方 task_type / modality 枚举。注意**这与我们内部的 task_type(t2v/i2v/r2va…)
// 不是一回事**:官方这个字段区分的是「生成 / 提示词增强 / 2K 重生成」三种任务门类,
// 而我们只提供生成,故恒为 generation + video。
const (
	TaskTypeGeneration   = "generation"
	TaskTypeContextIR    = "h3_context_ir"
	TaskTypeRegeneration = "regeneration"

	ModalityVideo = "video"
)

var officialTaskTypes = map[string]bool{
	TaskTypeGeneration:   true,
	TaskTypeContextIR:    true,
	TaskTypeRegeneration: true,
}

var officialStatuses = map[string]bool{
	StatusQueued:    true,
	StatusRunning:   true,
	StatusSucceeded: true,
	StatusFailed:    true,
	StatusCancelled: true,
}

// ── 错误 ─────────────────────────────────────────────────────────────────────

// ErrorEnvelope 是官方的 OpenAI 风格错误信封。HTTP 状态码本身就是真实状态码
// (不是那种恒 200 再在 body 里放错误码的形态)。
type ErrorEnvelope struct {
	Type      string      `json:"type"`
	Error     ErrorDetail `json:"error"`
	RequestID string      `json:"request_id,omitempty"`
}

type ErrorDetail struct {
	Type     string `json:"type"`
	Message  string `json:"message"`
	HTTPCode string `json:"http_code"`
}

// 官方 error.type 枚举。
const (
	ErrTypeBadRequest         = "bad_request_error"
	ErrTypeAuthorized         = "authorized_error"
	ErrTypeInsufficientQuota  = "insufficient_balance_error"
	ErrTypeUnprocessable      = "unprocessable_entity_error"
	ErrTypeRateLimit          = "rate_limit_error"
	ErrTypeOverloaded         = "overloaded_error"
	ErrTypeServer             = "server_error"
	ErrTypeNotImplemented     = "not_implemented_error"
	errTypeEnvelopeDiscrimant = "error"
)

// APIError 是本兼容层自己产生的错误(转换期校验、任务管理接口)。
type APIError struct {
	StatusCode int
	Type       string
	Message    string
}

func (e *APIError) Error() string { return e.Message }

func newError(status int, errType, message string) *APIError {
	return &APIError{StatusCode: status, Type: errType, Message: message}
}

func badRequest(message string) *APIError {
	return newError(http.StatusBadRequest, ErrTypeBadRequest, message)
}

func notImplemented(message string) *APIError {
	return newError(http.StatusNotImplemented, ErrTypeNotImplemented, message)
}

// 供包外（中间件、控制器）构造错误。
func NewBadRequest(message string) *APIError { return badRequest(message) }

func NewServerError(message string) *APIError {
	return newError(http.StatusInternalServerError, ErrTypeServer, message)
}

func NewNotImplemented(message string) *APIError { return notImplemented(message) }

// ErrorTypeForStatus 把 HTTP 状态码映射成官方 error.type。
//
// ⚠️ 422(敏感内容)只可能出现在**下游真的回了 422** 的情况下——我们自己绝不生成它,
// 因为我们没有审核环节。见包注释。
func ErrorTypeForStatus(status int) string {
	switch status {
	case http.StatusBadRequest, http.StatusNotFound, http.StatusRequestEntityTooLarge:
		return ErrTypeBadRequest
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrTypeAuthorized
	case http.StatusPaymentRequired:
		return ErrTypeInsufficientQuota
	case http.StatusUnprocessableEntity:
		return ErrTypeUnprocessable
	case http.StatusTooManyRequests:
		return ErrTypeRateLimit
	case http.StatusNotImplemented:
		return ErrTypeNotImplemented
	case 529:
		return ErrTypeOverloaded
	}
	if status >= 500 {
		return ErrTypeServer
	}
	return ErrTypeBadRequest
}

// BuildErrorBody 组装官方错误信封。
func BuildErrorBody(requestID string, status int, errType, message string) []byte {
	if errType == "" {
		errType = ErrorTypeForStatus(status)
	}
	body, err := common.Marshal(ErrorEnvelope{
		Type: errTypeEnvelopeDiscrimant,
		Error: ErrorDetail{
			Type:     errType,
			Message:  message,
			HTTPCode: strconv.Itoa(status),
		},
		RequestID: requestID,
	})
	if err != nil {
		// Marshal 一个纯静态结构不可能失败;真失败了也得给出合法 JSON。
		return []byte(`{"type":"error","error":{"type":"server_error","message":"failed to build error body","http_code":"500"}}`)
	}
	return body
}

// IsErrorEnvelope 判断 body 是否已经是官方错误信封,供响应改写层避免二次包装。
func IsErrorEnvelope(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	var probe struct {
		Type  string `json:"type"`
		Error *struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := common.Unmarshal(body, &probe); err != nil {
		return false
	}
	return probe.Type == errTypeEnvelopeDiscrimant && probe.Error != nil
}

// AbortWithError 以官方信封结束请求。
func AbortWithError(c *gin.Context, e *APIError) {
	c.Data(e.StatusCode, "application/json; charset=utf-8",
		BuildErrorBody(c.GetString(common.RequestIdKey), e.StatusCode, e.Type, e.Message))
	c.Abort()
}
