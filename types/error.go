package types

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

type OpenAIError struct {
	Message  string          `json:"message"`
	Type     string          `json:"type"`
	Param    string          `json:"param"`
	Code     any             `json:"code"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

type ClaudeError struct {
	Type    string `json:"type,omitempty"`
	Message string `json:"message,omitempty"`
}

type ErrorType string

const (
	ErrorTypeNewAPIError     ErrorType = "new_api_error"
	ErrorTypeOpenAIError     ErrorType = "openai_error"
	ErrorTypeClaudeError     ErrorType = "claude_error"
	ErrorTypeMidjourneyError ErrorType = "midjourney_error"
	ErrorTypeGeminiError     ErrorType = "gemini_error"
	ErrorTypeRerankError     ErrorType = "rerank_error"
	ErrorTypeUpstreamError   ErrorType = "upstream_error"
)

type ErrorCode string

const (
	ErrorCodeInvalidRequest         ErrorCode = "invalid_request"
	ErrorCodeSensitiveWordsDetected ErrorCode = "sensitive_words_detected"
	// ErrorCodeModerationUnavailable 审核服务不可用导致的 fail-close 拒绝。
	//
	// 必须与 sensitive_words_detected 分开（§9.2.3）：一个是「你的内容违规」，
	// 一个是「我们的审核挂了」。混用会让用户为一次服务故障去申诉自己没犯的错，
	// 也让运营在记录里分不清违规量突增和审核宕机。
	ErrorCodeModerationUnavailable ErrorCode = "moderation_unavailable"
	ErrorCodeViolationFeeGrokCSAM  ErrorCode = "violation_fee.grok.csam"

	// new api error
	ErrorCodeCountTokenFailed   ErrorCode = "count_token_failed"
	ErrorCodeModelPriceError    ErrorCode = "model_price_error"
	ErrorCodeInvalidApiType     ErrorCode = "invalid_api_type"
	ErrorCodeJsonMarshalFailed  ErrorCode = "json_marshal_failed"
	ErrorCodeDoRequestFailed    ErrorCode = "do_request_failed"
	ErrorCodeGetChannelFailed   ErrorCode = "get_channel_failed"
	ErrorCodeGenRelayInfoFailed ErrorCode = "gen_relay_info_failed"

	// channel error
	ErrorCodeChannelNoAvailableKey        ErrorCode = "channel:no_available_key"
	ErrorCodeChannelParamOverrideInvalid  ErrorCode = "channel:param_override_invalid"
	ErrorCodeChannelHeaderOverrideInvalid ErrorCode = "channel:header_override_invalid"
	ErrorCodeChannelModelMappedError      ErrorCode = "channel:model_mapped_error"
	ErrorCodeChannelAwsClientError        ErrorCode = "channel:aws_client_error"
	ErrorCodeChannelInvalidKey            ErrorCode = "channel:invalid_key"
	ErrorCodeChannelResponseTimeExceeded  ErrorCode = "channel:response_time_exceeded"

	// client request error
	ErrorCodeReadRequestBodyFailed ErrorCode = "read_request_body_failed"
	ErrorCodeConvertRequestFailed  ErrorCode = "convert_request_failed"
	ErrorCodeAccessDenied          ErrorCode = "access_denied"

	// request error
	ErrorCodeBadRequestBody ErrorCode = "bad_request_body"
	// ErrorCodeEndpointNotSupported 客户端把模型打到了本渠道不提供的端点上。
	// 消息由网关自己拼(该走哪个端点 + 文档链接),不含上游地址或密钥,故免打码 ——
	// 见 skipsMasking。
	ErrorCodeEndpointNotSupported ErrorCode = "endpoint_not_supported"

	// response error
	ErrorCodeReadResponseBodyFailed ErrorCode = "read_response_body_failed"
	ErrorCodeBadResponseStatusCode  ErrorCode = "bad_response_status_code"
	ErrorCodeBadResponse            ErrorCode = "bad_response"
	ErrorCodeBadResponseBody        ErrorCode = "bad_response_body"
	ErrorCodeEmptyResponse          ErrorCode = "empty_response"
	ErrorCodeAwsInvokeError         ErrorCode = "aws_invoke_error"
	ErrorCodeModelNotFound          ErrorCode = "model_not_found"
	ErrorCodePromptBlocked          ErrorCode = "prompt_blocked"

	// sql error
	ErrorCodeQueryDataError  ErrorCode = "query_data_error"
	ErrorCodeUpdateDataError ErrorCode = "update_data_error"

	// quota error
	ErrorCodeInsufficientUserQuota      ErrorCode = "insufficient_user_quota"
	ErrorCodePreConsumeTokenQuotaFailed ErrorCode = "pre_consume_token_quota_failed"

	// kyc error
	ErrorCodeKYCRequired ErrorCode = "kyc_required"
)

type NewAPIError struct {
	Err            error
	RelayError     any
	skipRetry      bool
	recordErrorLog *bool
	errorType      ErrorType
	errorCode      ErrorCode
	StatusCode     int
	Metadata       json.RawMessage
}

// Unwrap enables errors.Is / errors.As to work with NewAPIError by exposing the underlying error.
func (e *NewAPIError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *NewAPIError) GetErrorCode() ErrorCode {
	if e == nil {
		return ""
	}
	return e.errorCode
}

func (e *NewAPIError) GetErrorType() ErrorType {
	if e == nil {
		return ""
	}
	return e.errorType
}

func (e *NewAPIError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		// fallback message when underlying error is missing
		return string(e.errorCode)
	}
	return e.Err.Error()
}

func (e *NewAPIError) ErrorWithStatusCode() string {
	if e == nil {
		return ""
	}
	msg := e.Error()
	if e.StatusCode == 0 {
		return msg
	}
	if msg == "" {
		return fmt.Sprintf("status_code=%d", e.StatusCode)
	}
	return fmt.Sprintf("status_code=%d, %s", e.StatusCode, msg)
}

// IsSelfAuthoredGuidance 标记「这条错误的消息完全由网关自己构造」——
// 是给用户看的用法指引(该走哪个端点、公开文档链接),不含上游地址、内部模型路径或密钥。
//
// 这类消息必须原样送达客户端。网关在错误出口上串了两道防泄露改写,两道都会把指引毁掉:
//   - common.MaskSensitiveInfo:URL 被压成 https://***.com/***/***;
//   - service.ReplaceUpstreamModelPath:把「空格 + 以 / 开头 + 含 2 个以上 /」的串
//     当成上游模型路径整段换成模型名。它的终止符只认 ASCII,中文标点不算,
//     一吞就吞到下一个 ASCII 空格,能把半句话连同文档链接一起吃掉。
//
// ⚠️ 判据必须同时看 errorType,不能只看 errorCode:WithOpenAIError 会把**上游响应体里的
// error.code 原样抄进 errorCode**(见该函数),上游回一个 {"code":"endpoint_not_supported",
// "message":"...http://10.0.3.7:8000/v1/..."} 就能骗过豁免,让含内网地址的消息免打码
// 直达客户端 —— 正是打码要防的那种泄露。ErrorTypeNewAPIError 只由网关侧的构造器
// (NewError / NewErrorWithStatusCode)设置,上游构造的错误是 ErrorTypeOpenAIError 等。
//
// ⚠️ 往豁免里加错误码前先确认:该错误码的消息在**任何路径下**都不会带上上游 URL、key
// 或客户端不该看到的内部信息。拿不准就别加,脱敏是安全默认值。
func (e *NewAPIError) IsSelfAuthoredGuidance() bool {
	if e == nil {
		return false
	}
	return e.errorType == ErrorTypeNewAPIError && e.errorCode == ErrorCodeEndpointNotSupported
}

// skipsMasking 标记「这条错误的消息不该过 common.MaskSensitiveInfo」。
// ErrorCodeCountTokenFailed 是早于本机制的特例:它只免打码,不免路径改写。
func (e *NewAPIError) skipsMasking() bool {
	if e == nil {
		return false
	}
	return e.errorCode == ErrorCodeCountTokenFailed || e.IsSelfAuthoredGuidance()
}

func (e *NewAPIError) MaskSensitiveError() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return string(e.errorCode)
	}
	errStr := e.Err.Error()
	if e.skipsMasking() {
		return errStr
	}
	return common.MaskSensitiveInfo(errStr)
}

func (e *NewAPIError) MaskSensitiveErrorWithStatusCode() string {
	if e == nil {
		return ""
	}
	msg := e.MaskSensitiveError()
	if e.StatusCode == 0 {
		return msg
	}
	if msg == "" {
		return fmt.Sprintf("status_code=%d", e.StatusCode)
	}
	return fmt.Sprintf("status_code=%d, %s", e.StatusCode, msg)
}

func (e *NewAPIError) SetMessage(message string) {
	e.Err = errors.New(message)
}

func (e *NewAPIError) ToOpenAIError() OpenAIError {
	var result OpenAIError
	switch e.errorType {
	case ErrorTypeOpenAIError:
		if openAIError, ok := e.RelayError.(OpenAIError); ok {
			result = openAIError
		}
	case ErrorTypeClaudeError:
		if claudeError, ok := e.RelayError.(ClaudeError); ok {
			result = OpenAIError{
				Message: e.Error(),
				Type:    claudeError.Type,
				Param:   "",
				Code:    e.errorCode,
			}
		}
	default:
		result = OpenAIError{
			Message: e.Error(),
			Type:    string(e.errorType),
			Param:   "",
			Code:    e.errorCode,
		}
	}
	if !e.skipsMasking() {
		result.Message = common.MaskSensitiveInfo(result.Message)
	}
	if result.Message == "" {
		result.Message = string(e.errorType)
	}
	return result
}

func (e *NewAPIError) ToClaudeError() ClaudeError {
	var result ClaudeError
	switch e.errorType {
	case ErrorTypeOpenAIError:
		if openAIError, ok := e.RelayError.(OpenAIError); ok {
			result = ClaudeError{
				Message: e.Error(),
				Type:    fmt.Sprintf("%v", openAIError.Code),
			}
		}
	case ErrorTypeClaudeError:
		if claudeError, ok := e.RelayError.(ClaudeError); ok {
			result = claudeError
		}
	default:
		result = ClaudeError{
			Message: e.Error(),
			Type:    string(e.errorType),
		}
	}
	if !e.skipsMasking() {
		result.Message = common.MaskSensitiveInfo(result.Message)
	}
	if result.Message == "" {
		result.Message = string(e.errorType)
	}
	return result
}

type NewAPIErrorOptions func(*NewAPIError)

func NewError(err error, errorCode ErrorCode, ops ...NewAPIErrorOptions) *NewAPIError {
	var newErr *NewAPIError
	// 保留深层传递的 new err
	if errors.As(err, &newErr) {
		for _, op := range ops {
			op(newErr)
		}
		return newErr
	}
	e := &NewAPIError{
		Err:        err,
		RelayError: nil,
		errorType:  ErrorTypeNewAPIError,
		StatusCode: http.StatusInternalServerError,
		errorCode:  errorCode,
	}
	for _, op := range ops {
		op(e)
	}
	return e
}

func NewOpenAIError(err error, errorCode ErrorCode, statusCode int, ops ...NewAPIErrorOptions) *NewAPIError {
	var newErr *NewAPIError
	// 保留深层传递的 new err
	if errors.As(err, &newErr) {
		if newErr.RelayError == nil {
			openaiError := OpenAIError{
				Message: newErr.Error(),
				Type:    string(errorCode),
				Code:    errorCode,
			}
			newErr.RelayError = openaiError
		}
		for _, op := range ops {
			op(newErr)
		}
		return newErr
	}
	openaiError := OpenAIError{
		Message: err.Error(),
		Type:    string(errorCode),
		Code:    errorCode,
	}
	return WithOpenAIError(openaiError, statusCode, ops...)
}

func InitOpenAIError(errorCode ErrorCode, statusCode int, ops ...NewAPIErrorOptions) *NewAPIError {
	openaiError := OpenAIError{
		Type: string(errorCode),
		Code: errorCode,
	}
	return WithOpenAIError(openaiError, statusCode, ops...)
}

func NewErrorWithStatusCode(err error, errorCode ErrorCode, statusCode int, ops ...NewAPIErrorOptions) *NewAPIError {
	e := &NewAPIError{
		Err: err,
		RelayError: OpenAIError{
			Message: err.Error(),
			Type:    string(errorCode),
		},
		errorType:  ErrorTypeNewAPIError,
		StatusCode: statusCode,
		errorCode:  errorCode,
	}
	for _, op := range ops {
		op(e)
	}

	return e
}

func WithOpenAIError(openAIError OpenAIError, statusCode int, ops ...NewAPIErrorOptions) *NewAPIError {
	code, ok := openAIError.Code.(string)
	if !ok {
		if openAIError.Code != nil {
			code = fmt.Sprintf("%v", openAIError.Code)
		} else {
			code = "unknown_error"
		}
	}
	if openAIError.Type == "" {
		openAIError.Type = "upstream_error"
	}
	e := &NewAPIError{
		RelayError: openAIError,
		errorType:  ErrorTypeOpenAIError,
		StatusCode: statusCode,
		Err:        errors.New(openAIError.Message),
		errorCode:  ErrorCode(code),
	}
	// OpenRouter
	if len(openAIError.Metadata) > 0 {
		openAIError.Message = fmt.Sprintf("%s (%s)", openAIError.Message, openAIError.Metadata)
		e.Metadata = openAIError.Metadata
		e.RelayError = openAIError
		e.Err = errors.New(openAIError.Message)
	}
	for _, op := range ops {
		op(e)
	}
	return e
}

func WithClaudeError(claudeError ClaudeError, statusCode int, ops ...NewAPIErrorOptions) *NewAPIError {
	if claudeError.Type == "" {
		claudeError.Type = "upstream_error"
	}
	e := &NewAPIError{
		RelayError: claudeError,
		errorType:  ErrorTypeClaudeError,
		StatusCode: statusCode,
		Err:        errors.New(claudeError.Message),
		errorCode:  ErrorCode(claudeError.Type),
	}
	for _, op := range ops {
		op(e)
	}
	return e
}

func IsChannelError(err *NewAPIError) bool {
	if err == nil {
		return false
	}
	return strings.HasPrefix(string(err.errorCode), "channel:")
}

func IsSkipRetryError(err *NewAPIError) bool {
	if err == nil {
		return false
	}

	return err.skipRetry
}

func ErrOptionWithSkipRetry() NewAPIErrorOptions {
	return func(e *NewAPIError) {
		e.skipRetry = true
	}
}

func ErrOptionWithNoRecordErrorLog() NewAPIErrorOptions {
	return func(e *NewAPIError) {
		e.recordErrorLog = common.GetPointer(false)
	}
}

func ErrOptionWithStatusCode(statusCode int) NewAPIErrorOptions {
	return func(e *NewAPIError) {
		e.StatusCode = statusCode
	}
}

func ErrOptionWithHideErrMsg(replaceStr string) NewAPIErrorOptions {
	return func(e *NewAPIError) {
		if common.DebugEnabled {
			fmt.Printf("ErrOptionWithHideErrMsg: %s, origin error: %s", replaceStr, e.Err)
		}
		e.Err = errors.New(replaceStr)
	}
}

func IsRecordErrorLog(e *NewAPIError) bool {
	if e == nil {
		return false
	}
	if e.recordErrorLog == nil {
		// default to true if not set
		return true
	}
	return *e.recordErrorLog
}
