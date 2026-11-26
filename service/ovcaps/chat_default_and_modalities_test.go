package ovcaps

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
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
		if len(capability.InputModalities) != 2 || capability.InputModalities[0] != "image" || capability.InputModalities[1] != "video" {
			t.Fatalf("%s modalities = %v, want [image video]", capability.Model, capability.InputModalities)
		}
		return
	}
	t.Fatal("chat capability missing")
}

// 配置了默认对话模型但它不在本次目录里（未部署 / 分组不可见 / 白名单过滤 / 写错名字）时，
// 必须回退"按顺序第一条"，保证 chat 恰好一条 default。
func TestConfiguredDefaultFallsBackWhenModelMissing(t *testing.T) {
	withChatModels(t, "GLM-5", "qwen3.8-flash")
	withGeneralSetting(t, func(s *operation_setting.GeneralSetting) { s.DefaultChatModel = "not-deployed-model" })

	capabilities := Build(Scope{Enabled: map[string]bool{"GLM-5": true, "qwen3.8-flash": true}})
	defaults := []string{}
	for _, capability := range capabilities {
		if capability.Operation == OperationChat && capability.Default {
			defaults = append(defaults, capability.Model)
		}
	}
	if len(defaults) != 1 {
		t.Fatalf("chat defaults = %v, want exactly 1", defaults)
	}
	if defaults[0] != "GLM-5" {
		t.Fatalf("fallback default = %q, want first emitted chat model GLM-5", defaults[0])
	}
}

func TestConfiguredDefaultIgnoredWhenTokenFiltered(t *testing.T) {
	withChatModels(t, "GLM-5", "qwen3.8-flash")
	withGeneralSetting(t, func(s *operation_setting.GeneralSetting) { s.DefaultChatModel = "qwen3.8-flash" })

	// 令牌白名单只放行 GLM-5：配置项对这把 key 不可见，仍要恰好一条 default。
	capabilities := Build(Scope{
		Enabled:      map[string]bool{"GLM-5": true, "qwen3.8-flash": true},
		TokenAllowed: map[string]bool{"GLM-5": true},
	})
	defaults := []string{}
	for _, capability := range capabilities {
		if capability.Operation == OperationChat && capability.Default {
			defaults = append(defaults, capability.Model)
		}
	}
	if len(defaults) != 1 || defaults[0] != "GLM-5" {
		t.Fatalf("chat defaults = %v, want exactly [GLM-5]", defaults)
	}
}

// 没有模态的对话模型不能凭空带出 inputModalities 字段。
func TestChatInputModalitiesOmittedWhenEmpty(t *testing.T) {
	withChatModels(t, "GLM-5")
	withGeneralSetting(t, func(s *operation_setting.GeneralSetting) { s.ModelModalities = "" })

	capabilities := Build(Scope{Enabled: map[string]bool{"GLM-5": true}})
	for _, capability := range capabilities {
		if capability.Operation != OperationChat {
			continue
		}
		if len(capability.InputModalities) != 0 {
			t.Fatalf("%s inputModalities = %v, want empty", capability.Model, capability.InputModalities)
		}
		encoded, err := common.Marshal(capability)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if strings.Contains(string(encoded), "inputModalities") {
			t.Fatalf("空模态不应出现在 JSON 里: %s", encoded)
		}
		return
	}
	t.Fatal("chat capability missing")
}
