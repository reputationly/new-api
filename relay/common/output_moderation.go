package common

import "github.com/gin-gonic/gin"

// outputModerationBlockedKey 标记本次请求的产物被内容审核拦下了。
//
// 存在的理由是**层级错位**：判定发生在渠道适配器里（只有那里能在写响应之前拿到
// 产物），而计费发生在 ImageHelper 里。适配器返回错误后，标准错误路径会把预扣费
// 退还——而产物拦截的口径是「照常计费，不退款」（产物已生成、算力已花掉，§12.4.5）。
// 靠这个标记让 ImageHelper 认出这一种错误并照常结算。
const outputModerationBlockedKey = "output_moderation_blocked"

// MarkOutputModerationBlocked 由渠道适配器在拦下产物时调用。
func MarkOutputModerationBlocked(c *gin.Context) {
	c.Set(outputModerationBlockedKey, true)
}

// IsOutputModerationBlocked 报告本次请求是否因产物审核被拦。
func IsOutputModerationBlocked(c *gin.Context) bool {
	return c.GetBool(outputModerationBlockedKey)
}
