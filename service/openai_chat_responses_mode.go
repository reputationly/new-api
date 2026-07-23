package service

import (
	"github.com/QuantumNous/new-api/service/openaicompat"
	"github.com/QuantumNous/new-api/setting/model_setting"
)

func ShouldChatCompletionsUseResponsesPolicy(policy model_setting.ChatCompletionsToResponsesPolicy, channelID int, channelType int, model string) bool {
	return openaicompat.ShouldChatCompletionsUseResponsesPolicy(policy, channelID, channelType, model)
}

func ShouldChatCompletionsUseResponsesGlobal(channelID int, channelType int, model string) bool {
	return openaicompat.ShouldChatCompletionsUseResponsesGlobal(channelID, channelType, model)
}

func ShouldResponsesForceConvert(channelID int, channelType int, model string) bool {
	return openaicompat.ShouldResponsesForceConvert(channelID, channelType, model)
}

func ShouldResponsesForceNative(channelID int, channelType int, model string) bool {
	return openaicompat.ShouldResponsesForceNative(channelID, channelType, model)
}
