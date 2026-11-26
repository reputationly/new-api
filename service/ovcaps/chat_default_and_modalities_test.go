package ovcaps

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting"
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

type textEntry struct {
	PlatformModel string `json:"platform_model"`
	Model         struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		SupportsImage bool   `json:"supportsImage,omitempty"`
		SupportsVideo bool   `json:"supportsVideo,omitempty"`
		SupportsAudio bool   `json:"supportsAudio,omitempty"`
	} `json:"model"`
}

func entry(platformModel string, image, video bool) textEntry {
	var e textEntry
	e.PlatformModel = platformModel
	e.Model.ID = platformModel
	e.Model.Name = platformModel
	e.Model.SupportsImage = image
	e.Model.SupportsVideo = video
	return e
}

// withHiloCatalog 用一份 text-only 目录替换当前 Hilo 配置，并在用例结束后还原。
func withHiloCatalog(t *testing.T, entries []textEntry, defaultText, multimodal string) {
	t.Helper()
	payload := map[string]any{"text": entries}
	if defaultText != "" {
		payload["default_text_model"] = defaultText
	}
	if multimodal != "" {
		payload["multimodal_model"] = multimodal
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal catalog: %v", err)
	}
	previous := setting.HiloCatalog2JsonString()
	if err := setting.UpdateHiloCatalogByJsonString(string(raw)); err != nil {
		t.Fatalf("update catalog: %v", err)
	}
	t.Cleanup(func() { _ = setting.UpdateHiloCatalogByJsonString(previous) })
}

func chatDefaults(capabilities []Capability) []string {
	models := []string{}
	for _, capability := range capabilities {
		if capability.Operation == OperationChat && capability.Default {
			models = append(models, capability.Model)
		}
	}
	return models
}

func TestChatDefaultFollowsHiloDefaultTextModel(t *testing.T) {
	withChatModels(t, "deepseek-v4-1-flash-1017", "DeepSeek-V4-Pro-0813")
	withHiloCatalog(t, []textEntry{
		entry("DeepSeek-V4-Pro-0813", false, false),
		entry("deepseek-v4-1-flash-1017", true, false),
	}, "deepseek-v4-1-flash-1017", "")

	capabilities := Build(Scope{Enabled: map[string]bool{
		"deepseek-v4-1-flash-1017": true,
		"DeepSeek-V4-Pro-0813":     true,
	}})
	if got := chatDefaults(capabilities); len(got) != 1 || got[0] != "deepseek-v4-1-flash-1017" {
		t.Fatalf("chat defaults = %v, want exactly [deepseek-v4-1-flash-1017]", got)
	}
	if roles := ResolveDefaultRoles(capabilities); roles.Chat != "deepseek-v4-1-flash-1017" {
		t.Fatalf("defaults.chat = %q", roles.Chat)
	}
}

func TestChatDefaultFallsBackToFirstReported(t *testing.T) {
	withChatModels(t, "GLM-5", "qwen3.8-flash")
	withHiloCatalog(t, []textEntry{entry("GLM-5", false, false), entry("qwen3.8-flash", true, true)}, "", "")

	got := chatDefaults(Build(Scope{Enabled: map[string]bool{"GLM-5": true, "qwen3.8-flash": true}}))
	if len(got) != 1 {
		t.Fatalf("chat defaults = %v, want exactly 1", got)
	}
}

// 目录里配的默认模型不在 text 段（或没部署）时，必须回退第一条。
func TestConfiguredDefaultFallsBackWhenModelMissing(t *testing.T) {
	withChatModels(t, "GLM-5", "qwen3.8-flash")
	withHiloCatalog(t, []textEntry{entry("GLM-5", false, false), entry("qwen3.8-flash", true, true)}, "not-in-catalog", "")

	got := chatDefaults(Build(Scope{Enabled: map[string]bool{"GLM-5": true, "qwen3.8-flash": true}}))
	if len(got) != 1 || got[0] != "GLM-5" {
		t.Fatalf("chat defaults = %v, want exactly [GLM-5]", got)
	}
}

func TestConfiguredDefaultIgnoredWhenTokenFiltered(t *testing.T) {
	withChatModels(t, "GLM-5", "qwen3.8-flash")
	withHiloCatalog(t, []textEntry{entry("GLM-5", false, false), entry("qwen3.8-flash", true, true)}, "qwen3.8-flash", "")

	capabilities := Build(Scope{
		Enabled:      map[string]bool{"GLM-5": true, "qwen3.8-flash": true},
		TokenAllowed: map[string]bool{"GLM-5": true},
	})
	if got := chatDefaults(capabilities); len(got) != 1 || got[0] != "GLM-5" {
		t.Fatalf("chat defaults = %v, want exactly [GLM-5]", got)
	}
	if roles := ResolveDefaultRoles(capabilities); roles.Multimodal != "" {
		t.Fatalf("不可见的模态模型不应出现在 defaults 里: %q", roles.Multimodal)
	}
}

func TestChatInputModalitiesComeFromHiloSupports(t *testing.T) {
	withChatModels(t, "qwen3.8-flash", "GLM-5")
	withHiloCatalog(t, []textEntry{
		entry("qwen3.8-flash", true, true),
		entry("GLM-5", false, false),
	}, "", "qwen3.8-flash")

	capabilities := Build(Scope{Enabled: map[string]bool{"qwen3.8-flash": true, "GLM-5": true}})
	byModel := map[string]Capability{}
	for _, capability := range capabilities {
		if capability.Operation == OperationChat {
			byModel[capability.Model] = capability
		}
	}
	if got := byModel["qwen3.8-flash"].InputModalities; len(got) != 2 || got[0] != "image" || got[1] != "video" {
		t.Fatalf("qwen3.8-flash inputModalities = %v, want [image video]", got)
	}
	if got := byModel["GLM-5"].InputModalities; len(got) != 0 {
		t.Fatalf("GLM-5 inputModalities = %v, want empty", got)
	}
	encoded, err := common.Marshal(byModel["GLM-5"])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), "inputModalities") {
		t.Fatalf("空模态不应出现在 JSON 里: %s", encoded)
	}
	if roles := ResolveDefaultRoles(capabilities); roles.Multimodal != "qwen3.8-flash" {
		t.Fatalf("defaults.multimodal = %q", roles.Multimodal)
	}
}

// 没列进 Hilo text 的对话模型不再有模态信息（路线 B 的取舍）。
func TestChatModelOutsideHiloTextHasNoModalities(t *testing.T) {
	withChatModels(t, "GLM-5", "Qwen3.8-Flash")
	withHiloCatalog(t, []textEntry{entry("GLM-5", false, false)}, "", "")

	capabilities := Build(Scope{Enabled: map[string]bool{"GLM-5": true, "Qwen3.8-Flash": true}})
	for _, capability := range capabilities {
		if capability.Operation == OperationChat && capability.Model == "Qwen3.8-Flash" && len(capability.InputModalities) != 0 {
			t.Fatalf("未列入 text 的模型不应有模态: %v", capability.InputModalities)
		}
	}
}

// 改 multimodal_model 不动任何 capability 字段，version 必须跟着变 ——
// 客户端只按 version 判断要不要重拉，漏掉 defaults 就等于这次配置永远不生效。
func TestVersionFollowsDefaults(t *testing.T) {
	withChatModels(t, "GLM-5", "qwen3.8-flash")
	entries := []textEntry{entry("GLM-5", false, false), entry("qwen3.8-flash", true, true)}
	scope := Scope{Enabled: map[string]bool{"GLM-5": true, "qwen3.8-flash": true}}

	withHiloCatalog(t, entries, "", "qwen3.8-flash")
	c1 := Build(scope)
	r1 := ResolveDefaultRoles(c1)

	withHiloCatalog(t, entries, "", "GLM-5") // GLM-5 不支持图片/视频 → multimodal 留空
	c2 := Build(scope)
	r2 := ResolveDefaultRoles(c2)

	if r1.Multimodal == r2.Multimodal {
		t.Fatalf("用例无效：defaults 没变（%q / %q）", r1.Multimodal, r2.Multimodal)
	}
	if Version(c1, r1) == Version(c2, r2) {
		t.Fatal("defaults 变了但 version 没变：按 version 缓存的客户端永远不会重拉")
	}
}

// multimodal_model 指到一个不支持图片/视频的模型时，不能下发（否则客户端会把
// 图片发给只能读文字的模型，错误从上游回来、看不出是配置问题）。
func TestMultimodalRejectedWhenModelHasNoVision(t *testing.T) {
	withChatModels(t, "GLM-5", "qwen3.8-flash")
	withHiloCatalog(t, []textEntry{entry("GLM-5", false, false), entry("qwen3.8-flash", true, true)}, "", "GLM-5")

	roles := ResolveDefaultRoles(Build(Scope{Enabled: map[string]bool{"GLM-5": true, "qwen3.8-flash": true}}))
	if roles.Multimodal != "" {
		t.Fatalf("不支持图片/视频的 GLM-5 不应成为默认多模态模型: %q", roles.Multimodal)
	}
}
