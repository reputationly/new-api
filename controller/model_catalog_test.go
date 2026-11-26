package controller

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/setting"
)

func TestMediaCatalogItemKeepsPlatformModelName(t *testing.T) {
	entry := setting.HiloCatalogEntry{
		PlatformModel: "minimax-h3-2k",
		Model:         dto.HiloMediaModel{ID: "MiniMax-H3-FL", ModelName: "MiniMax-H3-FL", DisplayName: "MiniMax H3 · 帧族", MaxRefs: 2},
	}
	item := mediaCatalogItem(entry)
	if item.PlatformModel != "minimax-h3-2k" {
		t.Fatalf("platform_model = %q, want minimax-h3-2k（提交时必须用平台名）", item.PlatformModel)
	}
	if item.ID != "MiniMax-H3-FL" {
		t.Fatalf("id = %q", item.ID)
	}
	raw, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(raw) == "" {
		t.Fatal("empty payload")
	}
}

func TestTextCatalogItemCarriesModalities(t *testing.T) {
	item := textCatalogItem(setting.HiloTextEntry{
		PlatformModel: "qwen3.8-flash",
		Model:         dto.HiloTextModel{ID: "qwen3.8-flash", Name: "qwen3.8-flash", SupportsImage: true, SupportsVideo: true},
	})
	if !item.SupportsImage || !item.SupportsVideo || item.SupportsAudio {
		t.Fatalf("modalities = image:%v video:%v audio:%v", item.SupportsImage, item.SupportsVideo, item.SupportsAudio)
	}
	if item.PlatformModel != "qwen3.8-flash" || item.DisplayName != "qwen3.8-flash" {
		t.Fatalf("unexpected item: %+v", item)
	}
}

// multimodal 默认值必须同时满足"已下发"和"声明了图片/视频能力"。
func TestResolveCatalogDefaultsGatesMultimodalByVision(t *testing.T) {
	catalog := setting.HiloCatalog{MultimodalModel: "GLM-5"}
	text := []ModelCatalogItem{
		{ID: "GLM-5", PlatformModel: "GLM-5"},
		{ID: "qwen3.8-flash-fp8", PlatformModel: "qwen3.8-flash-fp8"},
	}
	vision := map[string]bool{"qwen3.8-flash-fp8": true}

	if got := resolveCatalogDefaults(catalog, text, vision); got.Multimodal != "" {
		t.Fatalf("不支持图片的模型不应成为 multimodal 默认: %q", got.Multimodal)
	}
	catalog.MultimodalModel = "qwen3.8-flash-fp8"
	if got := resolveCatalogDefaults(catalog, text, vision); got.Multimodal != "qwen3.8-flash-fp8" {
		t.Fatalf("multimodal = %q, want qwen3.8-flash-fp8", got.Multimodal)
	}
}

func TestResolveCatalogDefaultsChatFallback(t *testing.T) {
	text := []ModelCatalogItem{{PlatformModel: "a"}, {PlatformModel: "b"}}
	if got := resolveCatalogDefaults(setting.HiloCatalog{DefaultTextModel: "not-reported"}, text, nil); got.Chat != "a" {
		t.Fatalf("chat 应回退下发列表第一条, got %q", got.Chat)
	}
	if got := resolveCatalogDefaults(setting.HiloCatalog{DefaultTextModel: "b"}, text, nil); got.Chat != "b" {
		t.Fatalf("chat 应取目录配置值, got %q", got.Chat)
	}
	if got := resolveCatalogDefaults(setting.HiloCatalog{}, nil, nil); got.Chat != "" || got.Multimodal != "" {
		t.Fatalf("空目录不应给默认值: %+v", got)
	}
}

// 线上实测口径：qwen3.8-flash-fp8 支持图片+视频，glm-5.3-flash-w4a8 不支持。
func TestVisionTextModels(t *testing.T) {
	vision := visionTextModels([]setting.HiloTextEntry{
		{PlatformModel: "qwen3.8-flash-fp8", Model: dto.HiloTextModel{ID: "qwen3.8-flash-fp8", SupportsImage: true, SupportsVideo: true}},
		{PlatformModel: "glm-5.3-flash-w4a8", Model: dto.HiloTextModel{ID: "glm-5.3-flash-w4a8"}},
	})
	if !vision["qwen3.8-flash-fp8"] {
		t.Fatalf("qwen3.8-flash-fp8 应被认作视觉模型: %v", vision)
	}
	if vision["glm-5.3-flash-w4a8"] {
		t.Fatalf("glm-5.3-flash-w4a8 不该被认作视觉模型: %v", vision)
	}
}

// interfaceType 由目录配置下发；对话条目缺省为 chat-completion，客户端不再自己维护映射。
func TestCatalogItemsCarryInterfaceType(t *testing.T) {
	media := mediaCatalogItem(setting.HiloCatalogEntry{
		PlatformModel: "minimax-h3-image",
		InterfaceType: "maas-image",
		Model:         dto.HiloMediaModel{ID: "MiniMax-H3-Image"},
	})
	if media.InterfaceType != "maas-image" {
		t.Fatalf("media interfaceType = %q", media.InterfaceType)
	}

	text := textCatalogItem(setting.HiloTextEntry{
		PlatformModel: "deepseek-v4-1-flash",
		Model:         dto.HiloTextModel{ID: "deepseek-v4-1-flash"},
	})
	if text.InterfaceType != "chat-completion" {
		t.Fatalf("text default interfaceType = %q, want chat-completion", text.InterfaceType)
	}

	custom := textCatalogItem(setting.HiloTextEntry{
		PlatformModel: "minimax-text-native",
		InterfaceType: "minimax-text-native",
		Model:         dto.HiloTextModel{ID: "minimax-text-native"},
	})
	if custom.InterfaceType != "minimax-text-native" {
		t.Fatalf("explicit text interfaceType = %q", custom.InterfaceType)
	}
}
