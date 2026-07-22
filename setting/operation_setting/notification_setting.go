package operation_setting

import (
	"github.com/QuantumNous/new-api/setting/config"
)

// NotificationSetting 管理员通知配置：当业务事件被用户提交时，
// 通过企业微信/钉钉群机器人 webhook 主动提醒管理员。
type NotificationSetting struct {
	WeChatWorkWebhookURL string `json:"wechat_work_webhook_url"`
	DingTalkWebhookURL   string `json:"dingtalk_webhook_url"`
	NotifyFeedback       bool   `json:"notify_feedback"`      // 工单
	NotifyEnterprise     bool   `json:"notify_enterprise"`    // 企业认证
	NotifyKYC            bool   `json:"notify_kyc"`           // 实名认证
	NotifyBankTransfer   bool   `json:"notify_bank_transfer"` // 企业转账
	NotifyInvoice        bool   `json:"notify_invoice"`       // 企业开票
}

// 默认配置
var notificationSetting = NotificationSetting{}

func init() {
	// 注册到全局配置管理器
	config.GlobalConfig.Register("notification_setting", &notificationSetting)
}

func GetNotificationSetting() *NotificationSetting {
	return &notificationSetting
}
