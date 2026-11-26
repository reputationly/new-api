package ovcaps

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// withChatModels 把给定模型声明为对话端点，并恢复读取口。
func withChatModels(t *testing.T, models ...string) {
	t.Helper()
	previous := chatEndpointTypes
	chatEndpointTypes = func(name string) []constant.EndpointType {
		for _, candidate := range models {
			if candidate == name {
				return []constant.EndpointType{constant.EndpointTypeOpenAI}
			}
		}
		return nil
	}
	t.Cleanup(func() { chatEndpointTypes = previous })
}

func withGeneralSetting(t *testing.T, mutate func(*operation_setting.GeneralSetting)) {
	t.Helper()
	previous := *operation_setting.GetGeneralSetting()
	mutate(operation_setting.GetGeneralSetting())
	t.Cleanup(func() { *operation_setting.GetGeneralSetting() = previous })
}

func TestChatDefaultFollowsConfiguredModel(t *testing.T) {
	withChatModels(t, "deepseek-v4-1-flash-1017", "DeepSeek-V4-Pro-0813")
	withGeneralSetting(t, func(s *operation_setting.GeneralSetting) { s.DefaultChatModel = "deepseek-v4-1-flash-1017" })

	capabilities := Build(Scope{Enabled: map[string]bool{
		"deepseek-v4-1-flash-1017": true,
		"DeepSeek-V4-Pro-0813":     true,
	}})
	defaults := map[string]string{}
	for _, capability := range capabilities {
		if capability.Operation != OperationChat || !capability.Default {
			continue
		}
		defaults[capability.Operation] = capability.Model
	}
	if defaults[OperationChat] != "deepseek-v4-1-flash-1017" {
		t.Fatalf("chat default = %q, want deepseek-v4-1-flash-1017", defaults[OperationChat])
	}
}

func TestChatDefaultFallsBackToFirstEmittedModel(t *testing.T) {
	withChatModels(t, "GLM-5", "qwen3.8-flash")
	withGeneralSetting(t, func(s *operation_setting.GeneralSetting) { s.DefaultChatModel = "" })

	capabilities := Build(Scope{Enabled: map[string]bool{"GLM-5": true, "qwen3.8-flash": true}})
	marked := 0
	for _, capability := range capabilities {
		if capability.Operation == OperationChat && capability.Default {
			marked++
		}
	}
	if marked != 1 {
		t.Fatalf("chat defaults = %d, want exactly 1", marked)
	}
}

func TestChatCapabilityCarriesModalities(t *testing.T) {
	withChatModels(t, "qwen3.8-flash")
	// 出厂表：qwen3.8-flash 支持图片与视频；管理员配置覆盖时以配置为准。
	withGeneralSetting(t, func(s *operation_setting.GeneralSetting) {
		s.ModelModalities = `{"qwen3.8-flash":["image","VIDEO","bogus"]}`
	})

	capabilities := Build(Scope{Enabled: map[string]bool{"qwen3.8-flash": true}})
	for _, capability := range capabilities {
		if capability.Operation != OperationChat {
			continue
		}
		if len(capability.Modalities) != 2 || capability.Modalities[0] != "image" || capability.Modalities[1] != "video" {
			t.Fatalf("%s modalities = %v, want [image video]", capability.Model, capability.Modalities)
		}
		return
	}
	t.Fatal("chat capability missing")
}
