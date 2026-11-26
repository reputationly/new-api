package operation_setting

import (
	"encoding/json"
	"sort"
	"strings"
)

// 已知的输入模态取值。客户端只认这三个，别的值一律丢弃。
const (
	ModalityImage = "image"
	ModalityVideo = "video"
	ModalityAudio = "audio"
)

func validModality(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case ModalityImage, ModalityVideo, ModalityAudio:
		return true
	}
	return false
}

// builtinModelModalities 是出厂已知的对话模型模态，避免管理员不配就没有任何信息。
// 管理员在「通用设置 → 模型模态能力」里写的 JSON 会覆盖同名条目。
var builtinModelModalities = map[string][]string{
	// 实测：图片、视频可用；音频被后端拒绝（At most 0 audio(s)）。
	"qwen3.8-flash": {ModalityImage, ModalityVideo},
	"glm-4v-flash":  {ModalityImage},
}

// GetModelModalities 合并出厂表与管理员配置，并把结果规范化（小写、去重、稳定排序）。
func GetModelModalities() map[string][]string {
	merged := make(map[string][]string, len(builtinModelModalities))
	for model, modalities := range builtinModelModalities {
		merged[strings.TrimSpace(model)] = normalizeModalities(modalities)
	}
	for model, modalities := range parseConfiguredModalities(GetGeneralSetting().ModelModalities) {
		merged[model] = normalizeModalities(modalities)
	}
	return merged
}

// 配置写坏了只忽略这一段，不让整份能力目录报错。
func parseConfiguredModalities(raw string) map[string][]string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	parsed := map[string][]string{}
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return nil
	}
	return parsed
}

func normalizeModalities(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		normalized := strings.ToLower(strings.TrimSpace(value))
		if !validModality(normalized) || seen[normalized] {
			continue
		}
		seen[normalized] = true
		out = append(out, normalized)
	}
	sort.Strings(out)
	return out
}
