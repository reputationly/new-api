package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// 小助手（DSH 客户端）反馈控制器。接口文档：docs/assistant-feedback-api.md
//
// 提交接口要求已登录（会话）或带有效 new-api 令牌（HTTP 层由 TokenOrUserAuth 保证），
// user_id 一律取鉴权结果，客户端不能指定；管理侧注册在 adminRoute（AdminAuth）下。

const (
	assistantFeedbackDefaultPageSize = 20
	assistantFeedbackMaxPageSize     = 100

	// 公开写接口的请求体上限。正文最大约 20KB（5000 个 4 字节字符）+ 60KB
	// 对话快照 + 元数据，128KB 留足余量；避免已认证用户用超大 JSON/未知字段
	// 拖垮内存。
	assistantFeedbackMaxBodyBytes = 128 << 10
)

// SubmitAssistantFeedback POST /api/assistant/feedback
func SubmitAssistantFeedback(c *gin.Context) {
	userId := c.GetInt("id")

	// 只接受 JSON：跨站表单只能发 simple request（text/plain / form-urlencoded），
	// 这里直接拒掉，减少 CSRF/协议滥用面。
	if !strings.HasPrefix(strings.ToLower(c.GetHeader("Content-Type")), "application/json") {
		common.ApiErrorMsg(c, "Content-Type 必须是 application/json")
		return
	}

	// Content-Length 先做便宜的拒绝；再包一层 MaxBytesReader 防止长度头撒谎
	// （chunked/超长 body）。两层都在解析 JSON 之前。
	if c.Request.ContentLength > assistantFeedbackMaxBodyBytes {
		assistantFeedbackBodyTooLarge(c)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, assistantFeedbackMaxBodyBytes)

	var req dto.AssistantFeedbackSubmitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			assistantFeedbackBodyTooLarge(c)
			return
		}
		common.ApiErrorMsg(c, "参数错误："+err.Error())
		return
	}

	feedback, err := model.CreateAssistantFeedback(userId, model.AssistantFeedbackInput{
		Channel:       req.Channel,
		Rating:        req.Rating,
		Category:      req.Category,
		Content:       req.Content,
		Contact:       req.Contact,
		SessionId:     req.SessionId,
		MessageId:     req.MessageId,
		RequestId:     req.RequestId,
		Model:         req.Model,
		ClientVersion: req.ClientVersion,
		Platform:      req.Platform,
		DeviceInfo:    req.DeviceInfo,
		Context:       []byte(req.Context),
	})
	if err != nil {
		if model.IsAssistantFeedbackValidationError(err) {
			common.ApiErrorMsg(c, err.Error())
		} else {
			common.ApiErrorMsg(c, "提交失败，请稍后重试")
		}
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"message": "",
		"data": dto.AssistantFeedbackSubmitResponse{
			Id:        feedback.Id,
			CreatedAt: feedback.CreatedAt,
		},
	})
}

func assistantFeedbackBodyTooLarge(c *gin.Context) {
	c.JSON(http.StatusRequestEntityTooLarge, gin.H{"success": false, "message": "请求体过大"})
}

// AdminGetAssistantFeedback GET /api/user/feedback/admin/assistant
func AdminGetAssistantFeedback(c *gin.Context) {
	channel := strings.TrimSpace(c.Query("channel"))
	rating := strings.TrimSpace(c.Query("rating"))
	if channel != "" && !model.IsValidAssistantFeedbackChannel(channel) {
		common.ApiErrorMsg(c, model.ErrAssistantFeedbackInvalidChannel.Error())
		return
	}
	if rating != "" && !model.IsValidAssistantFeedbackRating(rating) {
		common.ApiErrorMsg(c, model.ErrAssistantFeedbackInvalidRating.Error())
		return
	}
	requestId := strings.TrimSpace(c.Query("request_id"))
	if utf8.RuneCountInString(requestId) > model.AssistantFeedbackMaxRequestIdLen {
		common.ApiErrorMsg(c, "Request ID 过长")
		return
	}

	filterUserId, _ := strconv.Atoi(c.DefaultQuery("user_id", "0"))
	page, pageSize := parsePaging(c, assistantFeedbackDefaultPageSize, assistantFeedbackMaxPageSize)

	rows, total, err := model.GetAssistantFeedbackAdminList(model.AssistantFeedbackAdminFilter{
		UserId:    filterUserId,
		Username:  strings.TrimSpace(c.Query("username")),
		Channel:   channel,
		Rating:    rating,
		RequestId: requestId,
		Keyword:   strings.TrimSpace(c.Query("keyword")),
		Page:      page,
		PageSize:  pageSize,
	})
	if err != nil {
		common.ApiErrorMsg(c, "查询失败")
		return
	}

	items := make([]dto.AssistantFeedbackItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, assistantFeedbackToItem(row))
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": items, "total": total})
}

func assistantFeedbackToItem(row *model.AssistantFeedbackAdminRow) dto.AssistantFeedbackItem {
	return dto.AssistantFeedbackItem{
		Id:            row.Id,
		UserId:        row.UserId,
		Username:      row.Username,
		Channel:       row.Channel,
		Rating:        row.Rating,
		Category:      row.Category,
		Content:       row.Content,
		Contact:       row.Contact,
		SessionId:     row.SessionId,
		MessageId:     row.MessageId,
		RequestId:     row.RequestId,
		Model:         row.Model,
		ClientVersion: row.ClientVersion,
		Platform:      row.Platform,
		DeviceInfo:    row.DeviceInfo,
		HasContext:    row.HasContextFlag > 0 || row.ContextJson != "",
		CreatedAt:     row.CreatedAt,
	}
}

// AdminGetAssistantFeedbackDetail GET /api/user/feedback/admin/assistant/:id
// 对话快照只在这里返回；列表接口不投影 context_json。
func AdminGetAssistantFeedbackDetail(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "反馈不存在"})
		return
	}
	row, err := model.GetAssistantFeedbackById(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "反馈不存在"})
		return
	}

	detail := dto.AssistantFeedbackDetail{AssistantFeedbackItem: assistantFeedbackToItem(row)}
	if row.ContextJson != "" {
		detail.Context = json.RawMessage(row.ContextJson)
		// 对话快照属于用户对话内容，管理员查看留痕。
		model.RecordLog(c.GetInt("id"), model.LogTypeManage, fmt.Sprintf("查看小助手反馈上下文 (feedback_id=%d)", id))
	}
	common.ApiSuccess(c, detail)
}
