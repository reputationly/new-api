package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/setting"

	"github.com/gin-gonic/gin"
)

// 第一方模型目录：与「蒜狸小助手」共用**同一份 Hilo 目录配置**，但补齐
// `/api/v1/models/config` 故意不下发的 `platform_model`。
//
// 为什么不能直接用 `/api/v1/models/config`：那份响应只带客户端 id（视频是
// `MiniMax-H3-FL`），而直接调平台的客户端必须用平台模型名（`minimax-h3-2k`）。
// 见 GetHiloCatalogDefault 的注释：官方那份响应里没有 platform_model，
// 反推只能靠 model_name 猜，H3 恰好猜不对。

// ModelCatalogItem 目录里的一条，媒体与对话共用（对话没有 params/max_refs）。
type ModelCatalogItem struct {
	ID            string                        `json:"id"`
	PlatformModel string                        `json:"platform_model"`
	DisplayName   string                        `json:"display_name"`
	Description   string                        `json:"description,omitempty"`
	MaxRefs       int                           `json:"max_refs,omitempty"`
	Params        map[string]dto.HiloModelParam `json:"params,omitempty"`
	SupportsImage bool                          `json:"supportsImage,omitempty"`
	SupportsVideo bool                          `json:"supportsVideo,omitempty"`
	SupportsAudio bool                          `json:"supportsAudio,omitempty"`
}

// ModelCatalog 第一方目录响应。version 是内容哈希，客户端据此判断要不要重拉。
type ModelCatalog struct {
	Version string             `json:"version"`
	Image   []ModelCatalogItem `json:"image"`
	Video   []ModelCatalogItem `json:"video"`
	Audio   []ModelCatalogItem `json:"audio"`
	Text    []ModelCatalogItem `json:"text"`
	// Defaults 角色默认值，与 `/api/v1/models/config` 的 defaultTextModelId /
	// defaultMultimodalModelId 同源。
	Defaults ModelCatalogDefaults `json:"defaults"`
}

// ModelCatalogDefaults 角色默认值（平台模型名）。
type ModelCatalogDefaults struct {
	Chat       string `json:"chat,omitempty"`
	Multimodal string `json:"multimodal,omitempty"`
}

func mediaCatalogItem(entry setting.HiloCatalogEntry) ModelCatalogItem {
	display := strings.TrimSpace(entry.Model.DisplayName)
	if display == "" {
		display = entry.Model.ID
	}
	return ModelCatalogItem{
		ID:            entry.Model.ID,
		PlatformModel: entry.PlatformModel,
		DisplayName:   display,
		Description:   entry.Model.Description,
		MaxRefs:       entry.Model.MaxRefs,
		Params:        entry.Model.Params,
	}
}

func textCatalogItem(entry setting.HiloTextEntry) ModelCatalogItem {
	display := strings.TrimSpace(entry.Model.Name)
	if display == "" {
		display = entry.Model.ID
	}
	return ModelCatalogItem{
		ID:            entry.Model.ID,
		PlatformModel: entry.PlatformModel,
		DisplayName:   display,
		SupportsImage: entry.Model.SupportsImage,
		SupportsVideo: entry.Model.SupportsVideo,
		SupportsAudio: entry.Model.SupportsAudio,
	}
}

// GetModelCatalog 处理 `GET /api/v1/models/catalog`：Hilo 目录 + 平台模型名。
//
// 与 `/api/v1/models/config` 一样只报"平台上真有渠道"的模型，避免客户端选中
// 之后调用才失败。
func GetModelCatalog(c *gin.Context) {
	enabled := availableForHilo()
	catalog := setting.GetHiloCatalog()

	out := ModelCatalog{
		Image: make([]ModelCatalogItem, 0, len(catalog.Image)),
		Video: make([]ModelCatalogItem, 0, len(catalog.Video)),
		Audio: make([]ModelCatalogItem, 0, len(catalog.Audio)),
		Text:  make([]ModelCatalogItem, 0, len(catalog.Text)),
	}
	collect := func(entries []setting.HiloCatalogEntry, dst *[]ModelCatalogItem) {
		for _, entry := range entries {
			if why := unavailable(enabled, entry.PlatformModel); why != "" {
				continue
			}
			*dst = append(*dst, mediaCatalogItem(entry))
		}
	}
	collect(catalog.Image, &out.Image)
	collect(catalog.Video, &out.Video)
	collect(catalog.Audio, &out.Audio)

	reported := make(map[string]bool, len(catalog.Text))
	for _, entry := range catalog.Text {
		if why := unavailable(enabled, entry.PlatformModel); why != "" {
			continue
		}
		out.Text = append(out.Text, textCatalogItem(entry))
		reported[strings.TrimSpace(entry.PlatformModel)] = true
	}
	// 角色默认值只报"真的下发出去"的模型：配错或没部署时留空，由客户端回退。
	if chat := strings.TrimSpace(catalog.DefaultTextModel); chat != "" && reported[chat] {
		out.Defaults.Chat = chat
	} else if len(out.Text) > 0 {
		out.Defaults.Chat = out.Text[0].PlatformModel
	}
	if multimodal := strings.TrimSpace(catalog.MultimodalModel); multimodal != "" && reported[multimodal] {
		out.Defaults.Multimodal = multimodal
	}

	payload := struct {
		Image    []ModelCatalogItem   `json:"image"`
		Video    []ModelCatalogItem   `json:"video"`
		Audio    []ModelCatalogItem   `json:"audio"`
		Text     []ModelCatalogItem   `json:"text"`
		Defaults ModelCatalogDefaults `json:"defaults"`
	}{out.Image, out.Video, out.Audio, out.Text, out.Defaults}
	raw, err := common.Marshal(payload)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": "目录序列化失败"})
		return
	}
	sum := sha256.Sum256(raw)
	out.Version = hex.EncodeToString(sum[:])

	c.Header("Cache-Control", "no-cache")
	c.JSON(http.StatusOK, out)
}
