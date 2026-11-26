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
//
// 字段名统一 camelCase：这个接口面向客户端，与 `/v1/ov/capabilities`、
// `/api/v1/models/config` 的既有风格一致（不要照抄 HiloCatalogEntry 内部的
// snake_case json tag，那是配置文件的写法，不是对外契约）。
type ModelCatalogItem struct {
	ID            string `json:"id"`
	PlatformModel string `json:"platformModel"`
	DisplayName   string `json:"displayName"`
	Description   string `json:"description,omitempty"`
	MaxRefs       int    `json:"maxRefs,omitempty"`
	// InterfaceType 客户端生成时应使用的接口类型。媒体条目取目录配置；
	// 对话条目缺省 chat-completion。客户端据此选协议，不再各自维护映射表。
	InterfaceType string                        `json:"interfaceType,omitempty"`
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
		InterfaceType: strings.TrimSpace(entry.InterfaceType),
		Params:        entry.Model.Params,
	}
}

func textCatalogItem(entry setting.HiloTextEntry) ModelCatalogItem {
	display := strings.TrimSpace(entry.Model.Name)
	if display == "" {
		display = entry.Model.ID
	}
	interfaceType := strings.TrimSpace(entry.InterfaceType)
	if interfaceType == "" {
		interfaceType = "chat-completion"
	}
	return ModelCatalogItem{
		ID:            entry.Model.ID,
		PlatformModel: entry.PlatformModel,
		DisplayName:   display,
		InterfaceType: interfaceType,
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

	for _, entry := range catalog.Text {
		if why := unavailable(enabled, entry.PlatformModel); why != "" {
			continue
		}
		out.Text = append(out.Text, textCatalogItem(entry))
	}
	out.Defaults = resolveCatalogDefaults(catalog, out.Text, visionTextModels(catalog.Text))

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

// visionTextModels 目录 text 段里声明了图片或视频能力的平台模型。
// 只看 `text` 段：多模态默认值是给对话模型用的，媒体条目的"能吃图"是另一回事。
func visionTextModels(entries []setting.HiloTextEntry) map[string]bool {
	out := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if entry.Model.SupportsImage || entry.Model.SupportsVideo {
			out[strings.TrimSpace(entry.PlatformModel)] = true
		}
	}
	return out
}

// resolveCatalogDefaults 算角色默认值。text 是**真的下发出去**的对话条目
// （顺序即目录顺序），vision 是其中声明了图片/视频能力的平台模型。
//
// 两者的取舍不同：chat 是客户端选择器的默认项，空着界面就不好用，所以回退第一条；
// multimodal 是角色挂载，配错不如留空让客户端自己回退 —— 但"配了却不支持图片/视频"
// 必须当作没配，否则客户端会把图片发给只能读文字的模型。
func resolveCatalogDefaults(catalog setting.HiloCatalog, text []ModelCatalogItem, vision map[string]bool) ModelCatalogDefaults {
	reported := make(map[string]bool, len(text))
	for _, item := range text {
		if model := strings.TrimSpace(item.PlatformModel); model != "" {
			reported[model] = true
		}
	}
	var defaults ModelCatalogDefaults
	if chat := strings.TrimSpace(catalog.DefaultTextModel); chat != "" && reported[chat] {
		defaults.Chat = chat
	} else if len(text) > 0 {
		defaults.Chat = text[0].PlatformModel
	}
	if multimodal := strings.TrimSpace(catalog.MultimodalModel); multimodal != "" && reported[multimodal] && vision[multimodal] {
		defaults.Multimodal = multimodal
	}
	return defaults
}
