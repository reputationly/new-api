package openaicompat

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/model_setting"
)

func TestResponsesConversionOverrides(t *testing.T) {
	settings := model_setting.GetGlobalSettings()
	orig := settings.ResponsesToChatCompletionsPolicy
	defer func() { settings.ResponsesToChatCompletionsPolicy = orig }()

	// Default: both overrides empty => no forcing.
	settings.ResponsesToChatCompletionsPolicy = model_setting.ResponsesToChatCompletionsPolicy{}
	if ShouldResponsesForceConvert(1, 1, "gpt-4o") {
		t.Error("default should not force convert")
	}
	if ShouldResponsesForceNative(1, 1, "gpt-4o") {
		t.Error("default should not force native")
	}

	// ForceConvert by channel type, all models.
	settings.ResponsesToChatCompletionsPolicy = model_setting.ResponsesToChatCompletionsPolicy{
		ForceConvert: model_setting.ResponsesConversionMatch{
			Enabled:      true,
			ChannelTypes: []int{1},
		},
	}
	if !ShouldResponsesForceConvert(99, 1, "any-model") {
		t.Error("expected force convert for channel type 1")
	}
	if ShouldResponsesForceConvert(99, 2, "any-model") {
		t.Error("channel type 2 should not match")
	}

	// ForceNative by channel id + model pattern.
	settings.ResponsesToChatCompletionsPolicy = model_setting.ResponsesToChatCompletionsPolicy{
		ForceNative: model_setting.ResponsesConversionMatch{
			Enabled:       true,
			ChannelIDs:    []int{7},
			ModelPatterns: []string{"^gpt-5"},
		},
	}
	if !ShouldResponsesForceNative(7, 1, "gpt-5-mini") {
		t.Error("expected force native for channel 7 + gpt-5*")
	}
	if ShouldResponsesForceNative(7, 1, "gpt-4o") {
		t.Error("model gpt-4o should not match pattern ^gpt-5")
	}
	if ShouldResponsesForceNative(8, 1, "gpt-5-mini") {
		t.Error("channel 8 should not match")
	}
}
