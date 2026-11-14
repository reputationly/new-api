package dto

import (
	"encoding/json"
	"time"
)

// 小助手（DSH 客户端）反馈的请求/响应 DTO。
// 接口文档：docs/assistant-feedback-api.md
//
// 与「我的工单」（dto/feedback.go）是两套独立功能：这里只做单向收集，
// 没有回复、状态流转与未读位。

// AssistantFeedbackSubmitRequest 客户端提交一条反馈。
// channel=rating 时必须带 rating；channel=general 时必须带 content。
type AssistantFeedbackSubmitRequest struct {
	Channel       string          `json:"channel"`
	Rating        string          `json:"rating"`
	Category      string          `json:"category"`
	Content       string          `json:"content"`
	Contact       string          `json:"contact"`
	SessionId     string          `json:"session_id"`
	MessageId     string          `json:"message_id"`
	RequestId     string          `json:"request_id"`
	Model         string          `json:"model"`
	ClientVersion string          `json:"client_version"`
	Platform      string          `json:"platform"`
	DeviceInfo    string          `json:"device_info"`
	Context       json.RawMessage `json:"context"`
}

// AssistantFeedbackSubmitResponse 提交成功后的最小回执。
type AssistantFeedbackSubmitResponse struct {
	Id        int       `json:"id"`
	CreatedAt time.Time `json:"created_at"`
}

// AssistantFeedbackItem 管理后台列表/详情项。Username 由后台查询 JOIN 回填。
type AssistantFeedbackItem struct {
	Id            int       `json:"id"`
	UserId        int       `json:"user_id"`
	Username      string    `json:"username"`
	Channel       string    `json:"channel"`
	Rating        string    `json:"rating"`
	Category      string    `json:"category"`
	Content       string    `json:"content"`
	Contact       string    `json:"contact"`
	SessionId     string    `json:"session_id"`
	MessageId     string    `json:"message_id"`
	RequestId     string    `json:"request_id"`
	Model         string    `json:"model"`
	ClientVersion string    `json:"client_version"`
	Platform      string    `json:"platform"`
	DeviceInfo    string    `json:"device_info"`
	HasContext    bool      `json:"has_context"`
	CreatedAt     time.Time `json:"created_at"`
}

// AssistantFeedbackDetail 管理后台详情：在列表项之外附带对话快照。
// Context 只在详情接口返回，列表接口永远不带，避免整页快照穿过网络。
type AssistantFeedbackDetail struct {
	AssistantFeedbackItem
	Context json.RawMessage `json:"context,omitempty"`
}
