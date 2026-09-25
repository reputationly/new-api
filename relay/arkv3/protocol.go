// Package arkv3 实现火山方舟 v3 视频生成协议（/api/v3/contents/generations/tasks 系列）
// 的对外兼容层：方舟 API 用户改 base_url + key 就能切过来，请求体、响应体、错误信封
// 与官方逐字段一致。
//
// 与 MiniMax v2 兼容层（relay/minimaxv2）最大的差别在上游：那边的上游是自建引擎，
// 官方那套 content[]+role 引擎一个字都不认，必须做真转换；这边的上游**就是方舟**
// （relay/channel/task/doubao），适配器本来就说 Ark 语言。
//
// 即便如此这里仍然做真转换，而不是把 body 原样透传：
//
//   - 任务 ID 必须是我们的 task_xxx。透传上游 ID 会让轮询、计费结算、产物代理
//     （/v1/videos/{id}/content）三条链路全部对不上，而且把上游任务 ID 暴露给了调用方。
//   - 模型名可能被渠道重定向到别处（自建引擎），那边不认 Ark 字段。转成统一任务契约后，
//     落哪个渠道由 Distribute 决定，本层不关心。
//   - content[] 里的媒体引用要经过我们自己的计费判定 —— 有没有视频输入直接决定单价
//     （doubao.EstimateBilling / relaycommon.VideoHasVideoInput），原样透传的话这一维读不到。
//
// 边界：官方有、我们做不到的一律显式拒绝并写明原因，不伪装支持。
//
//   - callback_url：400。回调由上游直接发给调用方，而回调体里的任务 ID 是**上游**的，
//     拿它查我们的接口必然 404。静默丢弃更糟 —— 调用方会一直等一个永远不来的推送。
//   - content[].draft_task：400。样片任务 ID 属于上游命名空间，调用方手里只有我们的
//     task_xxx，两者不通用。
//   - asset:// 素材引用：400。本网关没有方舟那套素材库。
package arkv3

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"

	"github.com/gin-gonic/gin"
)

// ── 请求 ─────────────────────────────────────────────────────────────────────

// CreateRequest 是 POST /api/v3/contents/generations/tasks 的官方请求体。
//
// 可选标量一律用指针（Rule 6 的同一条理由）：官方把 generate_audio 默认成 true、
// watermark 默认成 false，非指针无法区分「没传」与「显式传了 false」——前者要让上游
// 用它自己的默认，后者必须原样发过去。seed 更直接：-1 是合法值（随机），0 也是合法值。
type CreateRequest struct {
	Model   string        `json:"model"`
	Content []ContentItem `json:"content"`

	CallbackURL           string  `json:"callback_url"`
	ReturnLastFrame       *bool   `json:"return_last_frame"`
	ServiceTier           string  `json:"service_tier"`
	ExecutionExpiresAfter *arkInt `json:"execution_expires_after"`
	GenerateAudio         *bool   `json:"generate_audio"`
	Draft                 *bool   `json:"draft"`
	Tools                 []Tool  `json:"tools"`
	SafetyIdentifier      string  `json:"safety_identifier"`
	Priority              *arkInt `json:"priority"`
	Resolution            string  `json:"resolution"`
	Ratio                 string  `json:"ratio"`
	Duration              *arkInt `json:"duration"`
	Frames                *arkInt `json:"frames"`
	Seed                  *arkInt `json:"seed"`
	CameraFixed           *bool   `json:"camera_fixed"`
	Watermark             *bool   `json:"watermark"`
	// 仅 Seedance 2.5。不收的话会被 Unmarshal 静默丢掉，调用方以为生效了。
	OmniReferenceTaskType string `json:"omni_reference_task_type"`
	OutputFormat          string `json:"output_format"`
}

// arkInt 是一个能收下**整值浮点**的整数。
//
// 不能用裸 int：Python 的 json.dumps(5.0) 与 JS 的 JSON.stringify(5.0) 都发出 `5.0`，
// 而 encoding/json 把它解进 int 会失败 —— 失败的还不止这一个字段，**整个请求体的
// Unmarshal 都会中止**，调用方拿到一句「request body is not valid JSON」，而他发的
// 恰恰是合法 JSON。本仓原生端点专门为这件事写过 parseDurationSeconds（它的注释记着
// 现网事故：「现网实测 duration=15.0 与 duration=5.0 出的都是 8.7 秒片子」），新入口
// 不能比它更严 —— 那会让「改 base_url 就能切过来」在最常见的一个参数上失效。
//
// dto.IntValue 解决不了这件事：它只试 int 与字符串两种形态，5.0 两条都不匹配。
//
// 非整值（15.5）显式报错、不截断：截断是把调用方要的值换成另一个，与静默丢弃同类。
type arkInt int

func (v *arkInt) UnmarshalJSON(b []byte) error {
	var num float64
	if err := common.Unmarshal(b, &num); err != nil {
		// 数字被客户端框架包成字符串（"5"）是常见误用，一并收下，与 dto.IntValue 的
		// 宽容口径一致。两种都不匹配就回原始的数字解析错误，它更贴近真实问题。
		var s string
		if strErr := common.Unmarshal(b, &s); strErr != nil {
			return err
		}
		n, convErr := strconv.Atoi(strings.TrimSpace(s))
		if convErr != nil {
			return fmt.Errorf("must be an integer, got %s", string(b))
		}
		*v = arkInt(n)
		return nil
	}
	if num != math.Trunc(num) {
		return fmt.Errorf("must be an integer, got %s", string(b))
	}
	*v = arkInt(num)
	return nil
}

// intPtr 把 *arkInt 转成 *int，供落盘快照与对外响应使用（那两处都用标准 int）。
func (v *arkInt) intPtr() *int {
	if v == nil {
		return nil
	}
	n := int(*v)
	return &n
}

func (v *arkInt) intOrZero() int {
	if v == nil {
		return 0
	}
	return int(*v)
}

// ContentItem 是官方 content[] 的一项。type 决定读哪个媒体字段，role 决定它的语义。
type ContentItem struct {
	Type      string     `json:"type"`
	Text      string     `json:"text"`
	Role      string     `json:"role"`
	ImageURL  *MediaRef  `json:"image_url"`
	VideoURL  *MediaRef  `json:"video_url"`
	AudioURL  *MediaRef  `json:"audio_url"`
	DraftTask *DraftTask `json:"draft_task"`
}

type MediaRef struct {
	URL string `json:"url"`
}

type DraftTask struct {
	ID string `json:"id"`
}

type Tool struct {
	Type string `json:"type"`
}

// ── 响应 ─────────────────────────────────────────────────────────────────────

// CreateResponse：官方提交接口只回一个任务 ID（外加回显 safety_identifier）。
type CreateResponse struct {
	ID               string `json:"id"`
	SafetyIdentifier string `json:"safety_identifier,omitempty"`
}

// ListResponse 是 GET /api/v3/contents/generations/tasks 的响应。
type ListResponse struct {
	Total int    `json:"total"`
	Items []Task `json:"items"`
}

// Task 是查询 / 列表接口里的任务对象。
//
// Status 不加 omitempty：它恒存在，省掉会让按字段存在性判断的客户端走进「字段缺失」分支。
// 其余回显字段都可能真的不适用（frames 与 duration 官方明说只返回一个），用 omitempty。
type Task struct {
	ID        string     `json:"id"`
	Model     string     `json:"model"`
	Status    string     `json:"status"`
	Error     *TaskError `json:"error,omitempty"`
	CreatedAt int64      `json:"created_at"`
	UpdatedAt int64      `json:"updated_at"`

	Content *Content `json:"content,omitempty"`

	Seed            *int   `json:"seed,omitempty"`
	Resolution      string `json:"resolution,omitempty"`
	Ratio           string `json:"ratio,omitempty"`
	Duration        int    `json:"duration,omitempty"`
	Frames          int    `json:"frames,omitempty"`
	FramesPerSecond int    `json:"framespersecond,omitempty"`
	GenerateAudio   *bool  `json:"generate_audio,omitempty"`
	Tools           []Tool `json:"tools,omitempty"`

	SafetyIdentifier      string `json:"safety_identifier,omitempty"`
	Priority              *int   `json:"priority,omitempty"`
	Draft                 *bool  `json:"draft,omitempty"`
	ServiceTier           string `json:"service_tier,omitempty"`
	ExecutionExpiresAfter int    `json:"execution_expires_after,omitempty"`
	OutputFormat          string `json:"output_format,omitempty"`

	Usage *Usage `json:"usage,omitempty"`
}

type Content struct {
	VideoURL     string `json:"video_url,omitempty"`
	LastFrameURL string `json:"last_frame_url,omitempty"`
}

type TaskError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Usage 是上游回执里的 token 用量，调用方用它对账。**只在上游真的回了才透出** ——
// 编一个数出来会让对账对到一个不存在的量上。
type Usage struct {
	CompletionTokens int        `json:"completion_tokens"`
	TotalTokens      int        `json:"total_tokens"`
	ToolUsage        *ToolUsage `json:"tool_usage,omitempty"`
}

type ToolUsage struct {
	WebSearch int `json:"web_search"`
}

// 官方任务状态词。内部的 TaskStatus 在 taskStatusToArk 里映射过来。
const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
	StatusExpired   = "expired"
)

var officialStatuses = map[string]bool{
	StatusQueued:    true,
	StatusRunning:   true,
	StatusSucceeded: true,
	StatusFailed:    true,
	StatusCancelled: true,
	StatusExpired:   true,
}

// 官方 service_tier 枚举。flex（离线推理）本网关不区分排队队列，收下只做回显。
const (
	ServiceTierDefault = "default"
	ServiceTierFlex    = "flex"
)

// 官方 omni_reference_task_type / output_format 枚举（仅 Seedance 2.5）。
var (
	omniReferenceTaskTypes = map[string]bool{"auto": true, "reference": true, "edit": true, "extend": true}
	outputFormats          = map[string]bool{"mp4": true, "mov": true}
)

// ── 错误 ─────────────────────────────────────────────────────────────────────

// ErrorEnvelope 是方舟的错误信封（OpenAI 风格）。HTTP 状态码本身就是真实状态码。
type ErrorEnvelope struct {
	Error ErrorDetail `json:"error"`
}

type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Param   string `json:"param"`
	Type    string `json:"type"`
	// RequestID 不是官方字段，但方舟自己也在响应头里带 x-request-id。放在 body 里
	// 是为了让调用方报障时能直接贴出来 —— 我们的日志按它检索。
	RequestID string `json:"request_id,omitempty"`
}

// 官方 error.code 枚举里我们会产生的那些。
const (
	CodeInvalidParameter     = "InvalidParameter"
	CodeAuthenticationError  = "AuthenticationError"
	CodeAccessDenied         = "AccessDenied"
	CodeResourceNotFound     = "ResourceNotFound"
	CodeQuotaExceeded        = "QuotaExceeded"
	CodeRateLimitExceeded    = "RateLimitExceeded"
	CodeSensitiveContent     = "SensitiveContentDetected"
	CodeInternalServiceError = "InternalServiceError"
	CodeServiceUnavailable   = "ServiceUnavailable"
	CodeNotSupported         = "OperationNotSupported"
)

// 官方 error.type 枚举（与 HTTP 状态语义一一对应）。
const (
	TypeBadRequest          = "BadRequest"
	TypeUnauthorized        = "Unauthorized"
	TypeForbidden           = "Forbidden"
	TypeNotFound            = "NotFound"
	TypeTooManyRequests     = "TooManyRequests"
	TypeInternalServerError = "InternalServerError"
	TypeServiceUnavailable  = "ServiceUnavailable"
)

// APIError 是本兼容层自己产生的错误（转换期校验、任务管理接口）。
type APIError struct {
	StatusCode int
	Code       string
	Type       string
	Message    string
	Param      string
}

func (e *APIError) Error() string { return e.Message }

func newError(status int, code, errType, message string) *APIError {
	return &APIError{StatusCode: status, Code: code, Type: errType, Message: message}
}

func badRequest(message string) *APIError {
	return newError(http.StatusBadRequest, CodeInvalidParameter, TypeBadRequest, message)
}

// NewBadRequest / NewServerError / NewNotSupported 供包外（中间件、控制器）构造错误。
func NewBadRequest(message string) *APIError { return badRequest(message) }

func NewNotFound(message string) *APIError {
	return newError(http.StatusNotFound, CodeResourceNotFound, TypeNotFound, message)
}

func NewServerError(message string) *APIError {
	return newError(http.StatusInternalServerError, CodeInternalServiceError, TypeInternalServerError, message)
}

// NewNotSupported 用于「官方有、本网关做不到」的操作。用 400 + OperationNotSupported
// 而不是 501：方舟 SDK 按 error.code 分支，501 在它的码表里不存在，客户端只会看到一个
// 无法归类的传输层错误。
func NewNotSupported(message string) *APIError {
	return newError(http.StatusBadRequest, CodeNotSupported, TypeBadRequest, message)
}

// errorCodeForStatus 把 HTTP 状态码映射成方舟的 (code, type)。
//
// ⚠️ SensitiveContentDetected（422）只在**下游真的回了 422** 时才会出现，我们自己
// 绝不生成它 —— 本网关没有内容审核环节，编一个不存在的审核结果是欺骗。
func errorCodeForStatus(status int) (code, errType string) {
	switch status {
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge:
		return CodeInvalidParameter, TypeBadRequest
	case http.StatusUnauthorized:
		return CodeAuthenticationError, TypeUnauthorized
	case http.StatusForbidden:
		return CodeAccessDenied, TypeForbidden
	case http.StatusPaymentRequired:
		return CodeQuotaExceeded, TypeForbidden
	case http.StatusNotFound:
		return CodeResourceNotFound, TypeNotFound
	case http.StatusUnprocessableEntity:
		return CodeSensitiveContent, TypeBadRequest
	case http.StatusTooManyRequests:
		return CodeRateLimitExceeded, TypeTooManyRequests
	case http.StatusServiceUnavailable, 529:
		return CodeServiceUnavailable, TypeServiceUnavailable
	}
	if status >= 500 {
		return CodeInternalServiceError, TypeInternalServerError
	}
	return CodeInvalidParameter, TypeBadRequest
}

// BuildErrorBody 组装方舟错误信封。code / errType 为空时按状态码推。
func BuildErrorBody(requestID string, status int, code, errType, message string) []byte {
	if code == "" || errType == "" {
		defaultCode, defaultType := errorCodeForStatus(status)
		if code == "" {
			code = defaultCode
		}
		if errType == "" {
			errType = defaultType
		}
	}
	body, err := common.Marshal(ErrorEnvelope{
		Error: ErrorDetail{
			Code:      code,
			Message:   message,
			Type:      errType,
			RequestID: requestID,
		},
	})
	if err != nil {
		// Marshal 一个纯静态结构不可能失败；真失败了也得给出合法 JSON。
		return []byte(`{"error":{"code":"InternalServiceError","message":"failed to build error body","param":"","type":"InternalServerError"}}`)
	}
	return body
}

// IsErrorEnvelope 判断 body 是否已经是方舟错误信封，供响应改写层避免二次包装。
func IsErrorEnvelope(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	var probe struct {
		Error *struct {
			Code string `json:"code"`
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := common.Unmarshal(body, &probe); err != nil {
		return false
	}
	// 必须同时看 code 与 type：OpenAI 风格的 {"error":{"message":...}} 在本仓到处都是
	// （abortWithOpenAiMessage），只判 error 存在会把它们当成已经转换过而原样放出去。
	return probe.Error != nil && strings.TrimSpace(probe.Error.Code) != "" && strings.TrimSpace(probe.Error.Type) != ""
}

// AbortWithError 以方舟信封结束请求。
func AbortWithError(c *gin.Context, e *APIError) {
	body := BuildErrorBody(c.GetString(common.RequestIdKey), e.StatusCode, e.Code, e.Type, e.Message)
	c.Data(e.StatusCode, "application/json; charset=utf-8", body)
	c.Abort()
}
