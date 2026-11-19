package controller

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
)

type channelModelEndpointType struct {
	EndpointTypes []string `json:"endpoint_types"`
	// PlaygroundBlocked 直接给出"会不会从文本体验区消失"的结论，不让前端自己再推一遍：
	// 判据来自 constant.PlaygroundNonChatEndpointLabels，与诊断接口同源。
	PlaygroundBlocked bool `json:"playground_blocked"`
}

// GetChannelModelEndpointTypes 返回「这批模型挂在某类渠道上会被判成什么端点」。
//
// 用途只有一处：渠道编辑页在保存前告诉运营"这些模型不会出现在文本体验区"。
//
// 为什么必须由后端算：端点类型由 common.GetEndpointTypesByChannelType 决定 —— 它同时
// 看渠道类型和模型名（GPUStackPlus 按名字区分图片/视频、dall-e/gpt-image/flux/imagen
// 前缀判图像、tts/speech- 判语音）。在 JS 里复刻这套规则必然漂移，而漂移的表现是
// "页面没警告、模型却选不到" —— 正是本功能要消灭的那类静默失败。
//
// 只读、只给管理员（channelRoute 统一 RootAuth）。
func GetChannelModelEndpointTypes(c *gin.Context) {
	channelType, err := strconv.Atoi(strings.TrimSpace(c.Query("type")))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "type 参数必须是渠道类型数字",
		})
		return
	}
	rawModels := strings.TrimSpace(c.Query("models"))
	result := make(map[string]channelModelEndpointType)
	if rawModels == "" {
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{"models": result}})
		return
	}
	for _, raw := range strings.Split(rawModels, ",") {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		endpointTypes := common.GetEndpointTypesByChannelType(channelType, name)
		names := make([]string, 0, len(endpointTypes))
		blocked := false
		for _, et := range endpointTypes {
			names = append(names, string(et))
			if constant.IsPlaygroundNonChatEndpoint(et) {
				blocked = true
			}
		}
		result[name] = channelModelEndpointType{
			EndpointTypes:     names,
			PlaygroundBlocked: blocked,
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{"models": result}})
}
