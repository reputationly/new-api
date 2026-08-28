package controller

import (
	"fmt"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay"
	"github.com/QuantumNous/new-api/relay/channel/ai360"
	"github.com/QuantumNous/new-api/relay/channel/lingyiwanwu"
	"github.com/QuantumNous/new-api/relay/channel/minimax"
	"github.com/QuantumNous/new-api/relay/channel/moonshot"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
)

// https://platform.openai.com/docs/api-reference/models/list

var openAIModels []dto.OpenAIModels
var openAIModelsMap map[string]dto.OpenAIModels
var channelId2Models map[int][]string

func init() {
	// https://platform.openai.com/docs/models/model-endpoint-compatibility
	for i := 0; i < constant.APITypeDummy; i++ {
		if i == constant.APITypeAIProxyLibrary {
			continue
		}
		adaptor := relay.GetAdaptor(i)
		channelName := adaptor.GetChannelName()
		modelNames := adaptor.GetModelList()
		for _, modelName := range modelNames {
			openAIModels = append(openAIModels, dto.OpenAIModels{
				Id:      modelName,
				Object:  "model",
				Created: 1626777600,
				OwnedBy: channelName,
			})
		}
	}
	for _, modelName := range ai360.ModelList {
		openAIModels = append(openAIModels, dto.OpenAIModels{
			Id:      modelName,
			Object:  "model",
			Created: 1626777600,
			OwnedBy: ai360.ChannelName,
		})
	}
	for _, modelName := range moonshot.ModelList {
		openAIModels = append(openAIModels, dto.OpenAIModels{
			Id:      modelName,
			Object:  "model",
			Created: 1626777600,
			OwnedBy: moonshot.ChannelName,
		})
	}
	for _, modelName := range lingyiwanwu.ModelList {
		openAIModels = append(openAIModels, dto.OpenAIModels{
			Id:      modelName,
			Object:  "model",
			Created: 1626777600,
			OwnedBy: lingyiwanwu.ChannelName,
		})
	}
	for _, modelName := range minimax.ModelList {
		openAIModels = append(openAIModels, dto.OpenAIModels{
			Id:      modelName,
			Object:  "model",
			Created: 1626777600,
			OwnedBy: minimax.ChannelName,
		})
	}
	for modelName, _ := range constant.MidjourneyModel2Action {
		openAIModels = append(openAIModels, dto.OpenAIModels{
			Id:      modelName,
			Object:  "model",
			Created: 1626777600,
			OwnedBy: "midjourney",
		})
	}
	openAIModelsMap = make(map[string]dto.OpenAIModels)
	for _, aiModel := range openAIModels {
		openAIModelsMap[aiModel.Id] = aiModel
	}
	channelId2Models = make(map[int][]string)
	for i := 1; i <= constant.ChannelTypeDummy; i++ {
		apiType, success := common.ChannelType2APIType(i)
		if !success || apiType == constant.APITypeAIProxyLibrary {
			continue
		}
		meta := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType: i,
		}}
		adaptor := relay.GetAdaptor(apiType)
		adaptor.Init(meta)
		channelId2Models[i] = adaptor.GetModelList()
	}
	openAIModels = lo.UniqBy(openAIModels, func(m dto.OpenAIModels) string {
		return m.Id
	})
}

func ListModels(c *gin.Context, modelType int) {
	userOpenAiModels := make([]dto.OpenAIModels, 0)

	acceptUnsetRatioModel := operation_setting.SelfUseModeEnabled
	if !acceptUnsetRatioModel {
		userId := c.GetInt("id")
		if userId > 0 {
			userSettings, _ := model.GetUserSetting(userId, false)
			if userSettings.AcceptUnsetRatioModel {
				acceptUnsetRatioModel = true
			}
		}
	}

	modelLimitEnable := common.GetContextKeyBool(c, constant.ContextKeyTokenModelLimitEnabled)
	if modelLimitEnable {
		s, ok := common.GetContextKey(c, constant.ContextKeyTokenModelLimit)
		var tokenModelLimit map[string]bool
		if ok {
			tokenModelLimit = s.(map[string]bool)
		} else {
			tokenModelLimit = map[string]bool{}
		}
		// 可见性裁剪（§6bis）：令牌白名单是**保存时的快照**，模型可能在那之后才被
		// 限制。controller/token.go 的保存时校验拦不住这个时序——它只在新建/编辑
		// 令牌时生效，而「模型先公开、后来才收紧」恰恰是限制模型的典型顺序。
		allowModels := make([]string, 0, len(tokenModelLimit))
		for m := range tokenModelLimit {
			allowModels = append(allowModels, m)
		}
		// 空串 = 拿不到调用者身份（见 callerUserGroup），此时不过滤，
		// 否则会把白名单里的受限模型全部滤光
		if userGroup := callerUserGroup(c); userGroup != "" {
			allowModels = model.FilterModelsByVisibility(allowModels, userGroup)
		}

		for _, allowModel := range allowModels {
			if !acceptUnsetRatioModel {
				if !helper.HasModelBillingConfig(allowModel) {
					continue
				}
			}
			if oaiModel, ok := openAIModelsMap[allowModel]; ok {
				oaiModel.SupportedEndpointTypes = model.GetModelSupportEndpointTypes(allowModel)
				userOpenAiModels = append(userOpenAiModels, oaiModel)
			} else {
				userOpenAiModels = append(userOpenAiModels, dto.OpenAIModels{
					Id:                     allowModel,
					Object:                 "model",
					Created:                1626777600,
					OwnedBy:                "custom",
					SupportedEndpointTypes: model.GetModelSupportEndpointTypes(allowModel),
				})
			}
		}
	} else {
		userId := c.GetInt("id")
		userGroup, err := model.GetUserGroup(userId, false)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "get user group failed",
			})
			return
		}
		group := userGroup
		tokenGroup := common.GetContextKeyString(c, constant.ContextKeyTokenGroup)
		if tokenGroup != "" {
			group = tokenGroup
		}
		var models []string
		if tokenGroup == "auto" {
			for _, autoGroup := range service.GetUserAutoGroup(userGroup) {
				groupModels := model.GetGroupEnabledModels(autoGroup)
				for _, g := range groupModels {
					if !common.StringsContains(models, g) {
						models = append(models, g)
					}
				}
			}
		} else {
			models = model.GetGroupEnabledModels(group)
		}
		// 可见性裁剪（§6bis）：/v1/models 是 OpenAI 标准接口，任何客户端都会调它
		// 列出可用模型。判据用 userGroup 而非 group——可见性绑用户身份，与令牌
		// 指向哪个路由分组无关。
		models = model.FilterModelsByVisibility(models, userGroup)
		for _, modelName := range models {
			if !acceptUnsetRatioModel {
				if !helper.HasModelBillingConfig(modelName) {
					continue
				}
			}
			if oaiModel, ok := openAIModelsMap[modelName]; ok {
				oaiModel.SupportedEndpointTypes = model.GetModelSupportEndpointTypes(modelName)
				userOpenAiModels = append(userOpenAiModels, oaiModel)
			} else {
				userOpenAiModels = append(userOpenAiModels, dto.OpenAIModels{
					Id:                     modelName,
					Object:                 "model",
					Created:                1626777600,
					OwnedBy:                "custom",
					SupportedEndpointTypes: model.GetModelSupportEndpointTypes(modelName),
				})
			}
		}
	}

	switch modelType {
	case constant.ChannelTypeAnthropic:
		useranthropicModels := make([]dto.AnthropicModel, len(userOpenAiModels))
		for i, model := range userOpenAiModels {
			useranthropicModels[i] = dto.AnthropicModel{
				ID:          model.Id,
				CreatedAt:   time.Unix(int64(model.Created), 0).UTC().Format(time.RFC3339),
				DisplayName: model.Id,
				Type:        "model",
			}
		}
		c.JSON(200, gin.H{
			"data":     useranthropicModels,
			"first_id": useranthropicModels[0].ID,
			"has_more": false,
			"last_id":  useranthropicModels[len(useranthropicModels)-1].ID,
		})
	case constant.ChannelTypeGemini:
		userGeminiModels := make([]dto.GeminiModel, len(userOpenAiModels))
		for i, model := range userOpenAiModels {
			userGeminiModels[i] = dto.GeminiModel{
				Name:        model.Id,
				DisplayName: model.Id,
			}
		}
		c.JSON(200, gin.H{
			"models":        userGeminiModels,
			"nextPageToken": nil,
		})
	default:
		c.JSON(200, gin.H{
			"success": true,
			"data":    userOpenAiModels,
			"object":  "list",
		})
	}
}

func ChannelListModels(c *gin.Context) {
	c.JSON(200, gin.H{
		"success": true,
		"data":    openAIModels,
	})
}

func DashboardListModels(c *gin.Context) {
	c.JSON(200, gin.H{
		"success": true,
		"data":    channelId2Models,
	})
}

func EnabledListModels(c *gin.Context) {
	c.JSON(200, gin.H{
		"success": true,
		"data":    model.GetEnabledModels(),
	})
}

// isModelVisibleToCaller 按调用者的用户档判定模型可见性。
//
// 取不到用户身份时**放行**：可见性是营销与商务层面的限制，不是安全边界
// （model.IsModelVisibleForGroup 的 fail-open 语义）。fail-closed 的后果是
// userGroup 为空串，而受限模型对空档位一律不可见——一次取分组失败就会让模型接口
// 对该调用者整个塌掉。
//
// ⚠️ 只判 err 不够：model.GetUserGroup 用的是 `Find(&group)` 而非 `First`，
// 记录不存在时返回 ("", nil) 而不报错。所以必须显式判空串，否则这个守卫形同虚设。
//
// 与 filterPricingByVisibility 的差异是有意的：那里是公开的模型广场，空档位意味着
// 未登录访客，受限模型本就不该露出；这里已经过 TokenAuth，空档位是异常而非匿名。
func isModelVisibleToCaller(c *gin.Context, modelName string) bool {
	if !model.HasModelVisibilityRestrictions() {
		return true
	}
	userGroup := callerUserGroup(c)
	if userGroup == "" {
		return true
	}
	return model.IsModelVisibleForGroup(modelName, userGroup)
}

// callerUserGroup 取调用者的用户档；拿不到有效身份时返回空串，调用方据此跳过过滤。
//
// 三道守卫缺一不可：
//   - id <= 0：没有用户上下文。**必须先判**——GetUserGroup 会走
//     getUserGroupCache -> RedisHGetObj，在 RedisEnabled 为真而客户端未初始化时
//     直接 panic，而不是返回错误。
//   - err != nil：DB/缓存异常。
//   - 空串：GetUserGroup 用的是 `Find(&group)` 而非 `First`，记录不存在时返回
//     ("", nil) 并不报错，所以只判 err 这个守卫形同虚设。
func callerUserGroup(c *gin.Context) string {
	userId := c.GetInt("id")
	if userId <= 0 {
		return ""
	}
	userGroup, err := model.GetUserGroup(userId, false)
	if err != nil {
		return ""
	}
	return userGroup
}

func RetrieveModel(c *gin.Context, modelType int) {
	modelId := c.Param("model")
	aiModel, exists := openAIModelsMap[modelId]
	// 可见性裁剪（§6bis）：与 ListModels 保持同一口径。二者不一致时，客户端常用的
	// 「先 list 再 retrieve」会拿到自相矛盾的结果——list 里没有、retrieve 却返回 200。
	//
	// 只补可见性这一维：本函数对分组隔离同样不生效（openAIModelsMap 是编译期的内置
	// 模型清单，与站点挂载了什么无关），那是既有缺陷，不在本次范围内。
	if exists && !isModelVisibleToCaller(c, modelId) {
		exists = false
	}
	if exists {
		switch modelType {
		case constant.ChannelTypeAnthropic:
			c.JSON(200, dto.AnthropicModel{
				ID:          aiModel.Id,
				CreatedAt:   time.Unix(int64(aiModel.Created), 0).UTC().Format(time.RFC3339),
				DisplayName: aiModel.Id,
				Type:        "model",
			})
		default:
			c.JSON(200, aiModel)
		}
	} else {
		openAIError := types.OpenAIError{
			Message: fmt.Sprintf("The model '%s' does not exist", modelId),
			Type:    "invalid_request_error",
			Param:   "model",
			Code:    "model_not_found",
		}
		c.JSON(200, gin.H{
			"error": openAIError,
		})
	}
}
