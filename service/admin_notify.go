package service

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

// AdminNotifyEvent 管理员通知事件类型
type AdminNotifyEvent string

const (
	AdminNotifyFeedback     AdminNotifyEvent = "feedback"      // 工单
	AdminNotifyEnterprise   AdminNotifyEvent = "enterprise"    // 企业认证
	AdminNotifyKYC          AdminNotifyEvent = "kyc"           // 实名认证
	AdminNotifyBankTransfer AdminNotifyEvent = "bank_transfer" // 企业转账
	AdminNotifyInvoice      AdminNotifyEvent = "invoice"       // 企业开票
)

// adminNotifyKeyword 固定包含在文案中的关键词，便于钉钉「自定义关键词」放行。
const adminNotifyKeyword = "管理员通知"

func adminNotifyEventTitle(event AdminNotifyEvent) string {
	switch event {
	case AdminNotifyFeedback:
		return "新工单待处理"
	case AdminNotifyEnterprise:
		return "企业认证待审核"
	case AdminNotifyKYC:
		return "实名认证待审核"
	case AdminNotifyBankTransfer:
		return "企业转账待审核"
	case AdminNotifyInvoice:
		return "开票申请待审核"
	}
	return string(event)
}

func adminNotifyEventEnabled(s *operation_setting.NotificationSetting, event AdminNotifyEvent) bool {
	switch event {
	case AdminNotifyFeedback:
		return s.NotifyFeedback
	case AdminNotifyEnterprise:
		return s.NotifyEnterprise
	case AdminNotifyKYC:
		return s.NotifyKYC
	case AdminNotifyBankTransfer:
		return s.NotifyBankTransfer
	case AdminNotifyInvoice:
		return s.NotifyInvoice
	}
	return false
}

// NotifyAdminEvent 当业务事件被用户提交时提醒管理员。
// 该函数会读取事件开关，未开启或未配置任何 webhook 时直接返回。
// 调用方应以 `go NotifyAdminEvent(...)` 方式调用，避免阻塞主流程。
func NotifyAdminEvent(event AdminNotifyEvent, summary string) {
	s := operation_setting.GetNotificationSetting()
	if !adminNotifyEventEnabled(s, event) {
		return
	}
	if s.WeChatWorkWebhookURL == "" && s.DingTalkWebhookURL == "" {
		return
	}

	title := fmt.Sprintf("【%s】%s", adminNotifyKeyword, adminNotifyEventTitle(event))
	content := fmt.Sprintf("**%s**\n\n%s\n\n时间：%s", title, summary, time.Now().Format("2006-01-02 15:04:05"))

	if s.WeChatWorkWebhookURL != "" {
		if err := sendWeChatWorkWebhook(s.WeChatWorkWebhookURL, content); err != nil {
			common.SysError("发送企业微信管理员通知失败: " + err.Error())
		}
	}
	if s.DingTalkWebhookURL != "" {
		if err := sendDingTalkWebhook(s.DingTalkWebhookURL, title, content); err != nil {
			common.SysError("发送钉钉管理员通知失败: " + err.Error())
		}
	}
}

// SendAdminTestNotification 向指定渠道发送一条测试通知，供后台「发送测试」按钮调用。
// channel 取值：wechat_work / dingtalk。
func SendAdminTestNotification(channel string) error {
	s := operation_setting.GetNotificationSetting()
	title := fmt.Sprintf("【%s】测试通知", adminNotifyKeyword)
	content := fmt.Sprintf("**%s**\n\n这是一条来自 new-api 的测试通知，收到即说明 webhook 配置成功。\n\n时间：%s",
		title, time.Now().Format("2006-01-02 15:04:05"))
	switch channel {
	case "wechat_work":
		if s.WeChatWorkWebhookURL == "" {
			return fmt.Errorf("企业微信 webhook 未配置")
		}
		return sendWeChatWorkWebhook(s.WeChatWorkWebhookURL, content)
	case "dingtalk":
		if s.DingTalkWebhookURL == "" {
			return fmt.Errorf("钉钉 webhook 未配置")
		}
		return sendDingTalkWebhook(s.DingTalkWebhookURL, title, content)
	}
	return fmt.Errorf("未知渠道: %s", channel)
}

// sendWeChatWorkWebhook 企业微信群机器人，markdown 消息。
func sendWeChatWorkWebhook(webhookURL string, content string) error {
	payload := map[string]any{
		"msgtype": "markdown",
		"markdown": map[string]string{
			"content": content,
		},
	}
	return postAdminWebhook(webhookURL, payload)
}

// sendDingTalkWebhook 钉钉群机器人，markdown 消息（不加签，依赖关键词/IP 白名单放行）。
func sendDingTalkWebhook(webhookURL string, title string, content string) error {
	payload := map[string]any{
		"msgtype": "markdown",
		"markdown": map[string]string{
			"title": title,
			"text":  content,
		},
	}
	return postAdminWebhook(webhookURL, payload)
}

// postAdminWebhook 统一的 webhook POST：SSRF 校验 + 发送 + 结果校验。
func postAdminWebhook(webhookURL string, payload any) error {
	body, err := common.Marshal(payload)
	if err != nil {
		return fmt.Errorf("序列化负载失败: %v", err)
	}

	// SSRF 防护
	fs := system_setting.GetFetchSetting()
	if err := common.ValidateURLWithFetchSetting(webhookURL, fs.EnableSSRFProtection, fs.AllowPrivateIp,
		fs.DomainFilterMode, fs.IpFilterMode, fs.DomainList, fs.IpList, fs.AllowedPorts, fs.ApplyIPFilterForDomain); err != nil {
		return fmt.Errorf("请求被拒绝: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, webhookURL, bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := GetHttpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP 状态码: %d", resp.StatusCode)
	}

	// 企业微信/钉钉都以 {"errcode":0,"errmsg":"ok"} 返回逻辑结果
	respBody, _ := io.ReadAll(resp.Body)
	var r struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := common.Unmarshal(respBody, &r); err == nil && r.ErrCode != 0 {
		return fmt.Errorf("webhook 返回错误 errcode=%d, errmsg=%s", r.ErrCode, r.ErrMsg)
	}
	return nil
}
