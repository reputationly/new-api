package openaicompat

import "github.com/QuantumNous/new-api/setting/model_setting"

func ShouldChatCompletionsUseResponsesPolicy(policy model_setting.ChatCompletionsToResponsesPolicy, channelID int, channelType int, model string) bool {
	if !policy.IsChannelEnabled(channelID, channelType) {
		return false
	}
	return matchAnyRegex(policy.ModelPatterns, model)
}

func ShouldChatCompletionsUseResponsesGlobal(channelID int, channelType int, model string) bool {
	return ShouldChatCompletionsUseResponsesPolicy(
		model_setting.GetGlobalSettings().ChatCompletionsToResponsesPolicy,
		channelID,
		channelType,
		model,
	)
}

func matchResponsesConversion(m model_setting.ResponsesConversionMatch, channelID int, channelType int, model string) bool {
	if !m.MatchesChannel(channelID, channelType) {
		return false
	}
	// Empty model patterns => the override applies to every model in scope.
	if len(m.ModelPatterns) == 0 {
		return true
	}
	return matchAnyRegex(m.ModelPatterns, model)
}

// ShouldResponsesForceConvert reports whether a /v1/responses request should be
// forced onto the Chat conversion path by explicit policy override.
func ShouldResponsesForceConvert(channelID int, channelType int, model string) bool {
	return matchResponsesConversion(
		model_setting.GetGlobalSettings().ResponsesToChatCompletionsPolicy.ForceConvert,
		channelID, channelType, model,
	)
}

// ShouldResponsesForceNative reports whether a /v1/responses request should be
// forced onto the native Responses path by explicit policy override.
func ShouldResponsesForceNative(channelID int, channelType int, model string) bool {
	return matchResponsesConversion(
		model_setting.GetGlobalSettings().ResponsesToChatCompletionsPolicy.ForceNative,
		channelID, channelType, model,
	)
}
