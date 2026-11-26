package operation_setting

import (
	"fmt"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// 已知的输入模态取值。客户端只认这三个，别的值一律丢弃。
// text 恒支持，所以不在取值里出现。
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

// builtinModelModalities 是当前部署的出厂已知对话模型模态，避免管理员不配就没有任何信息。
// 管理员在「通用设置 → 模型输入模态」里写的 JSON 会覆盖同名条目；换部署时改这里或写配置覆盖。
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
		merged[strings.TrimSpace(model)] = normalizeModalities(modalities)
	}
	return merged
}

// parseConfiguredModalities 只解析不改错：调用方已经过 ValidateModelModalities，
// 这里再失败就当作"没有配置"，不让整份能力目录报错。
func parseConfiguredModalities(raw string) map[string][]string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	parsed := map[string][]string{}
	if err := common.UnmarshalJsonStr(trimmed, &parsed); err != nil {
		return nil
	}
	return parsed
}

// ValidateModelModalities 校验「模型输入模态」的 JSON 文本。
// 写错不会报错的话，客户端只会静默丢掉图片/视频能力，所以校验放在保存路径上。
func ValidateModelModalities(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	parsed := map[string][]string{}
	if err := common.UnmarshalJsonStr(trimmed, &parsed); err != nil {
		return fmt.Errorf("模型输入模态必须是「模型名 → 模态数组」的 JSON 对象: %w", err)
	}
	for model, modalities := range parsed {
		if strings.TrimSpace(model) == "" {
			return fmt.Errorf("模型输入模态里存在空的模型名")
		}
		for _, modality := range modalities {
			if !validModality(modality) {
				return fmt.Errorf("模型 %q 的模态 %q 无效，只能是 image / video / audio", model, modality)
			}
		}
	}
	return nil
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
