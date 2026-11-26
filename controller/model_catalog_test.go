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
