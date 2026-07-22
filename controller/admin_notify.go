package controller

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// TestNotification POST /api/option/notification_test
// 向指定渠道发送一条测试通知，用于验证管理员通知 webhook 是否配置正确。
func TestNotification(c *gin.Context) {
	var req struct {
		Channel string `json:"channel"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "参数错误："+err.Error())
		return
	}
	if err := service.SendAdminTestNotification(req.Channel); err != nil {
		common.ApiErrorMsg(c, "发送失败："+err.Error())
		return
	}
	common.ApiSuccess(c, nil)
}
