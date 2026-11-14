package model

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

// 小助手（DSH 客户端）反馈——用户对单条回复的赞/踩，以及「更多 → 意见反馈」的
// 单向留言。与 feedback_topics 的工单系统相互独立：这里没有回复、状态流转和未读位，
// 只负责收集并在管理后台展示。接口文档：docs/assistant-feedback-api.md

const (
	AssistantFeedbackChannelRating  = "rating"  // 对话内对某条回复的赞/踩
	AssistantFeedbackChannelGeneral = "general" // 「更多 → 意见反馈」留言

	AssistantFeedbackRatingLike    = "like"
	AssistantFeedbackRatingDislike = "dislike"

	AssistantFeedbackMaxCategoryLen      = 64
	AssistantFeedbackMaxContentLen       = 5000
	AssistantFeedbackMaxContactLen       = 128
	AssistantFeedbackMaxSessionIdLen     = 128
	AssistantFeedbackMaxMessageIdLen     = 128
	AssistantFeedbackMaxRequestIdLen     = 64
	AssistantFeedbackMaxModelLen         = 128
	AssistantFeedbackMaxClientVersionLen = 64
	AssistantFeedbackMaxPlatformLen      = 32
	AssistantFeedbackMaxDeviceInfoLen    = 512

	// 对话快照上限。MySQL 的 TEXT 上限是 64KB，这里留一点余量；客户端超出时应
	// 丢弃最旧消息/工具输出，并在 context.truncated 里标记。
	AssistantFeedbackMaxContextBytes = 60 * 1024

	// 单用户单日提交上限：公网接口的兜底防刷。按 user_id 计数，多令牌共享同一额度。
	AssistantFeedbackMaxDailyPerUser = 200
)

var (
	ErrAssistantFeedbackInvalidChannel  = errors.New("无效的反馈类型")
	ErrAssistantFeedbackInvalidRating   = errors.New("评价必须是 like 或 dislike")
	ErrAssistantFeedbackRatingRequired  = errors.New("对话评价必须包含 like 或 dislike")
	ErrAssistantFeedbackContentRequired = errors.New("意见反馈内容不能为空")
	ErrAssistantFeedbackDailyLimit      = errors.New("今日反馈次数已达上限，请明天再试")
	ErrAssistantFeedbackContextTooLarge = errors.New("对话快照超过 60KB 上限")
	ErrAssistantFeedbackContextInvalid  = errors.New("对话快照必须是合法的 JSON 对象")
)

// AssistantFeedbackValidationError 客户端可直接修正的输入错误。
// 控制器只把该类型回给客户端，DB 错误统一转成通用文案，
// 避免把表名/SQL 细节暴露给外部客户端。
type AssistantFeedbackValidationError struct {
	Err error
}

func (e *AssistantFeedbackValidationError) Error() string { return e.Err.Error() }

func (e *AssistantFeedbackValidationError) Unwrap() error { return e.Err }

func invalidAssistantFeedback(err error) error {
	return &AssistantFeedbackValidationError{Err: err}
}

// IsAssistantFeedbackValidationError 判断 err 是否是输入校验错误。
func IsAssistantFeedbackValidationError(err error) bool {
	var target *AssistantFeedbackValidationError
	return errors.As(err, &target)
}

func IsValidAssistantFeedbackChannel(channel string) bool {
	return channel == AssistantFeedbackChannelRating || channel == AssistantFeedbackChannelGeneral
}

func IsValidAssistantFeedbackRating(rating string) bool {
	return rating == AssistantFeedbackRatingLike || rating == AssistantFeedbackRatingDislike
}

// AssistantFeedback 一条小助手反馈。UserId 由鉴权结果写入，客户端不可指定。
type AssistantFeedback struct {
	Id            int    `json:"id"             gorm:"primaryKey;autoIncrement"`
	UserId        int    `json:"user_id"        gorm:"not null;index"`
	Channel       string `json:"channel"        gorm:"type:varchar(16);not null;index"`
	Rating        string `json:"rating"         gorm:"type:varchar(16);not null;default:''"`
	Category      string `json:"category"       gorm:"type:varchar(64);not null;default:''"`
	Content       string `json:"content"        gorm:"type:varchar(5000);not null;default:''"`
	Contact       string `json:"contact"        gorm:"type:varchar(128);not null;default:''"`
	SessionId     string `json:"session_id"     gorm:"type:varchar(128);not null;default:'';index"`
	MessageId     string `json:"message_id"     gorm:"type:varchar(128);not null;default:''"`
	RequestId     string `json:"request_id"     gorm:"type:varchar(64);not null;default:'';index"`
	Model         string `json:"model"          gorm:"type:varchar(128);not null;default:''"`
	ClientVersion string `json:"client_version" gorm:"type:varchar(64);not null;default:''"`
	Platform      string `json:"platform"       gorm:"type:varchar(32);not null;default:''"`
	DeviceInfo    string `json:"device_info"    gorm:"type:varchar(512);not null;default:''"`
	// ContextJson 客户端附带的对话快照（system prompt / messages / request params）。
	// 不进列表投影，只在管理员详情接口返回；用 TEXT 以兼容三库并限制在 60KB。
	ContextJson string    `json:"-" gorm:"type:text"`
	CreatedAt   time.Time `json:"created_at" gorm:"index"`
}

// AssistantFeedbackInput 已由控制器从请求体解出的反馈内容（尚未校验/归一化）。
type AssistantFeedbackInput struct {
	Channel       string
	Rating        string
	Category      string
	Content       string
	Contact       string
	SessionId     string
	MessageId     string
	RequestId     string
	Model         string
	ClientVersion string
	Platform      string
	DeviceInfo    string
	Context       []byte
}

// CreateAssistantFeedback 校验并落库一条反馈。失败时返回可直接展示给客户端的错误。
func CreateAssistantFeedback(userId int, input AssistantFeedbackInput) (*AssistantFeedback, error) {
	input.Channel = normalizeAssistantFeedbackText(input.Channel)
	input.Rating = normalizeAssistantFeedbackText(input.Rating)
	input.Category = normalizeAssistantFeedbackText(input.Category)
	input.Content = normalizeAssistantFeedbackText(input.Content)
	input.Contact = normalizeAssistantFeedbackText(input.Contact)
	input.SessionId = normalizeAssistantFeedbackText(input.SessionId)
	input.MessageId = normalizeAssistantFeedbackText(input.MessageId)
	input.RequestId = normalizeAssistantFeedbackText(input.RequestId)
	input.Model = normalizeAssistantFeedbackText(input.Model)
	input.ClientVersion = normalizeAssistantFeedbackText(input.ClientVersion)
	input.Platform = normalizeAssistantFeedbackText(input.Platform)
	input.DeviceInfo = normalizeAssistantFeedbackText(input.DeviceInfo)

	if !IsValidAssistantFeedbackChannel(input.Channel) {
		return nil, invalidAssistantFeedback(ErrAssistantFeedbackInvalidChannel)
	}
	switch input.Channel {
	case AssistantFeedbackChannelRating:
		if input.Rating == "" {
			return nil, invalidAssistantFeedback(ErrAssistantFeedbackRatingRequired)
		}
		if !IsValidAssistantFeedbackRating(input.Rating) {
			return nil, invalidAssistantFeedback(ErrAssistantFeedbackInvalidRating)
		}
	case AssistantFeedbackChannelGeneral:
		// 意见反馈没有评价；客户端误带时直接丢弃，避免后台出现「意见反馈 + 差评」。
		input.Rating = ""
		if input.Content == "" {
			return nil, invalidAssistantFeedback(ErrAssistantFeedbackContentRequired)
		}
	}

	if err := checkAssistantFeedbackLen("分类", input.Category, AssistantFeedbackMaxCategoryLen); err != nil {
		return nil, err
	}
	if err := checkAssistantFeedbackLen("反馈内容", input.Content, AssistantFeedbackMaxContentLen); err != nil {
		return nil, err
	}
	if err := checkAssistantFeedbackLen("联系方式", input.Contact, AssistantFeedbackMaxContactLen); err != nil {
		return nil, err
	}
	if err := checkAssistantFeedbackLen("会话 ID", input.SessionId, AssistantFeedbackMaxSessionIdLen); err != nil {
		return nil, err
	}
	if err := checkAssistantFeedbackLen("消息 ID", input.MessageId, AssistantFeedbackMaxMessageIdLen); err != nil {
		return nil, err
	}
	if err := checkAssistantFeedbackLen("Request ID", input.RequestId, AssistantFeedbackMaxRequestIdLen); err != nil {
		return nil, err
	}
	if err := checkAssistantFeedbackLen("模型", input.Model, AssistantFeedbackMaxModelLen); err != nil {
		return nil, err
	}
	if err := checkAssistantFeedbackLen("客户端版本", input.ClientVersion, AssistantFeedbackMaxClientVersionLen); err != nil {
		return nil, err
	}
	if err := checkAssistantFeedbackLen("平台", input.Platform, AssistantFeedbackMaxPlatformLen); err != nil {
		return nil, err
	}
	if err := checkAssistantFeedbackLen("设备信息", input.DeviceInfo, AssistantFeedbackMaxDeviceInfoLen); err != nil {
		return nil, err
	}
	contextJson, err := normalizeAssistantFeedbackContext(input.Context)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	var dailyCount int64
	if err := DB.Model(&AssistantFeedback{}).
		Where("user_id = ? AND created_at >= ?", userId, startOfDay).
		Count(&dailyCount).Error; err != nil {
		return nil, err
	}
	if dailyCount >= AssistantFeedbackMaxDailyPerUser {
		return nil, invalidAssistantFeedback(ErrAssistantFeedbackDailyLimit)
	}

	feedback := &AssistantFeedback{
		UserId:        userId,
		Channel:       input.Channel,
		Rating:        input.Rating,
		Category:      input.Category,
		Content:       input.Content,
		Contact:       input.Contact,
		SessionId:     input.SessionId,
		MessageId:     input.MessageId,
		RequestId:     input.RequestId,
		Model:         input.Model,
		ClientVersion: input.ClientVersion,
		Platform:      input.Platform,
		DeviceInfo:    input.DeviceInfo,
		ContextJson:   contextJson,
	}
	if err := DB.Create(feedback).Error; err != nil {
		return nil, err
	}
	return feedback, nil
}

// normalizeAssistantFeedbackText 去掉首尾空白与 NUL。
// PostgreSQL 的 text/varchar 不接受 0x00，不清掉会让带 \u0000 的请求稳定报 "提交失败"。
func normalizeAssistantFeedbackText(value string) string {
	value = strings.TrimSpace(value)
	if strings.IndexByte(value, 0) >= 0 {
		value = strings.ReplaceAll(value, "\x00", "")
	}
	return value
}

var (
	assistantFeedbackSkPattern     = regexp.MustCompile(`sk-[A-Za-z0-9_-]{16,}`)
	assistantFeedbackBearerPattern = regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9._\-]{16,}`)
)

// normalizeAssistantFeedbackContext 校验并归一化客户端附带的对话快照。
// 只要求是 JSON 对象；内部 schema 由客户端/文档约定。服务端做一层尽力而为的
// 密钥脱敏，避免 Host 误把 Authorization/api_key 一起塞进快照。
func normalizeAssistantFeedbackContext(raw []byte) (string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || bytes.Equal(trimmed, []byte("{}")) {
		return "", nil
	}
	if len(trimmed) > AssistantFeedbackMaxContextBytes {
		return "", invalidAssistantFeedback(ErrAssistantFeedbackContextTooLarge)
	}
	if trimmed[0] != '{' {
		return "", invalidAssistantFeedback(ErrAssistantFeedbackContextInvalid)
	}
	var probe map[string]any
	if err := common.Unmarshal(trimmed, &probe); err != nil {
		return "", invalidAssistantFeedback(ErrAssistantFeedbackContextInvalid)
	}
	text := string(trimmed)
	text = assistantFeedbackSkPattern.ReplaceAllString(text, "sk-[REDACTED]")
	text = assistantFeedbackBearerPattern.ReplaceAllString(text, "Bearer [REDACTED]")
	// 替换值只包含普通字符，不会破坏 JSON 的引号/转义；不按 key 名做正则替换，
	// 否则遇到含转义引号的值会把 JSON 改坏。
	return text, nil
}

func checkAssistantFeedbackLen(field, value string, max int) error {
	if utf8.RuneCountInString(value) > max {
		return invalidAssistantFeedback(fmt.Errorf("%s长度不能超过 %d 个字符", field, max))
	}
	return nil
}

// ─── 管理后台查询 ─────────────────────────────────────────────────────────────

// AssistantFeedbackAdminFilter 管理后台列表筛选。空字符串/0 表示不过滤。
type AssistantFeedbackAdminFilter struct {
	UserId    int
	Username  string
	Channel   string
	Rating    string
	RequestId string // 精确匹配模型请求 ID
	Keyword   string // 模糊匹配内容 / 分类 / 会话 ID / 消息 ID / Request ID
	Page      int
	PageSize  int
}

// AssistantFeedbackAdminRow 列表/详情行：反馈 + 提交用户名 + 是否带对话快照。
type AssistantFeedbackAdminRow struct {
	AssistantFeedback
	Username       string `gorm:"column:username"`
	HasContextFlag int64  `gorm:"column:has_context"`
}

// assistantFeedbackListColumns 列表投影：刻意不选 context_json。
// 一页最多 100 条 × 60KB 快照会白白穿过网络/GC；详情接口再按 id 单独取。
// 新增列时记得同步这里（漏了是可见的缺字段；SELECT * 漏的是不可见的泄漏）。
const assistantFeedbackListColumns = "assistant_feedbacks.id, assistant_feedbacks.user_id, " +
	"assistant_feedbacks.channel, assistant_feedbacks.rating, assistant_feedbacks.category, " +
	"assistant_feedbacks.content, assistant_feedbacks.contact, assistant_feedbacks.session_id, " +
	"assistant_feedbacks.message_id, assistant_feedbacks.request_id, assistant_feedbacks.model, assistant_feedbacks.client_version, " +
	"assistant_feedbacks.platform, assistant_feedbacks.device_info, assistant_feedbacks.created_at, " +
	"(CASE WHEN assistant_feedbacks.context_json IS NULL OR assistant_feedbacks.context_json = '' THEN 0 ELSE 1 END) AS has_context"

// GetAssistantFeedbackAdminList 全量反馈列表，按创建时间倒序。
func GetAssistantFeedbackAdminList(filter AssistantFeedbackAdminFilter) ([]*AssistantFeedbackAdminRow, int64, error) {
	build := func(db *gorm.DB) *gorm.DB {
		q := db.Model(&AssistantFeedback{}).
			Joins("LEFT JOIN users u1 ON u1.id = assistant_feedbacks.user_id")
		if filter.UserId != 0 {
			q = q.Where("assistant_feedbacks.user_id = ?", filter.UserId)
		}
		if filter.Username != "" {
			q = q.Where("u1.username LIKE ?", "%"+filter.Username+"%")
		}
		if filter.Channel != "" {
			q = q.Where("assistant_feedbacks.channel = ?", filter.Channel)
		}
		if filter.Rating != "" {
			q = q.Where("assistant_feedbacks.rating = ?", filter.Rating)
		}
		if filter.RequestId != "" {
			q = q.Where("assistant_feedbacks.request_id = ?", filter.RequestId)
		}
		if filter.Keyword != "" {
			like := "%" + filter.Keyword + "%"
			q = q.Where(
				"assistant_feedbacks.content LIKE ? OR assistant_feedbacks.category LIKE ? OR assistant_feedbacks.session_id LIKE ? OR assistant_feedbacks.message_id LIKE ? OR assistant_feedbacks.request_id LIKE ?",
				like, like, like, like, like,
			)
		}
		return q
	}

	var total int64
	if err := build(DB).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var rows []*AssistantFeedbackAdminRow
	offset := (filter.Page - 1) * filter.PageSize
	err := build(DB).
		Select(assistantFeedbackListColumns + ", u1.username AS username").
		Order("assistant_feedbacks.created_at DESC, assistant_feedbacks.id DESC").
		Offset(offset).Limit(filter.PageSize).Scan(&rows).Error
	if err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// GetAssistantFeedbackById 管理员详情：单条反馈（含 context_json）与用户名。
func GetAssistantFeedbackById(id int) (*AssistantFeedbackAdminRow, error) {
	var row AssistantFeedbackAdminRow
	err := DB.Model(&AssistantFeedback{}).
		Select("assistant_feedbacks.*, u1.username AS username").
		Joins("LEFT JOIN users u1 ON u1.id = assistant_feedbacks.user_id").
		Where("assistant_feedbacks.id = ?", id).
		Take(&row).Error
	if err != nil {
		return nil, err
	}
	return &row, nil
}
