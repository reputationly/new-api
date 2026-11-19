package controller

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

type channelModelEndpointType struct {
	EndpointTypes []string `json:"endpoint_types"`
	// PlaygroundBlocked 直接给出"会不会从文本体验区消失"的结论，不让前端自己再推一遍：
	// 判据来自 constant.PlaygroundNonChatEndpointLabels，与诊断接口同源。
	PlaygroundBlocked bool `json:"playground_blocked"`
}

type channelEndpointTypesRequest struct {
	Type   int      `json:"type"`
	Models []string `json:"models"`
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
	// POST 而不是 GET：模型列表动辄上百个名字，拼进 URL 会超长，而被网关截断的表现是
	// "预警静默消失"——正是这类提示最不该有的失败方式。
	var req channelEndpointTypesRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": `请求体必须是 {"type": 渠道类型数字, "models": ["模型名", ...]}`,
		})
		return
	}

	names := make([]string, 0, len(req.Models))
	seen := make(map[string]bool, len(req.Models))
	for _, raw := range req.Models {
		name := strings.TrimSpace(raw)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}

	result := make(map[string]channelModelEndpointType, len(names))
	if len(names) == 0 {
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{"models": result}})
		return
	}

	overrides := lookupEndpointOverrides(names)
	for _, name := range names {
		endpointTypes := common.GetEndpointTypesByChannelType(req.Type, name)
		// 元数据里的自定义 endpoints 会**整体替换**默认端点（与 model/pricing.go 同语义）。
		// 只按精确名匹配：name_rule 的前缀/后缀规则在这里不展开，那种情况交给
		// /api/channel/diagnose（它用完整展开规则，结论更权威）。
		if custom, ok := overrides[name]; ok {
			endpointTypes = custom
		}
		typeNames := make([]string, 0, len(endpointTypes))
		blocked := false
		for _, et := range endpointTypes {
			typeNames = append(typeNames, string(et))
			if constant.IsPlaygroundNonChatEndpoint(et) {
				blocked = true
			}
		}
		result[name] = channelModelEndpointType{
			EndpointTypes:     typeNames,
			PlaygroundBlocked: blocked,
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{"models": result}})
}

// lookupEndpointOverrides 取这批模型在 models 表里自定义的 endpoints（精确名匹配）。
// 解析失败的行直接跳过：坏配置不该让整页预警挂掉。
func lookupEndpointOverrides(names []string) map[string][]constant.EndpointType {
	overrides := make(map[string][]constant.EndpointType)
	var metas []model.Model
	if err := model.DB.Select("model_name", "endpoints").
		Where("model_name IN ?", names).
		Find(&metas).Error; err != nil {
		common.SysLog("failed to load model endpoints for warning: " + err.Error())
		return overrides
	}
	for _, meta := range metas {
		raw := strings.TrimSpace(meta.Endpoints)
		if raw == "" {
			continue
		}
		parsed := make(map[string]any)
		if err := common.UnmarshalJsonStr(raw, &parsed); err != nil {
			continue
		}
		types := make([]constant.EndpointType, 0, len(parsed))
		for key := range parsed {
			types = append(types, constant.EndpointType(key))
		}
		if len(types) > 0 {
			overrides[meta.ModelName] = types
		}
	}
	return overrides
}
