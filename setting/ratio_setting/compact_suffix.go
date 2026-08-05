package ratio_setting

import "strings"

const CompactModelSuffix = "-openai-compact"
const CompactWildcardModelKey = "*" + CompactModelSuffix

func WithCompactModelSuffix(modelName string) string {
	if strings.HasSuffix(modelName, CompactModelSuffix) {
		return modelName
	}
	return modelName + CompactModelSuffix
}

// WithCompactModelVariants 返回原模型名 + 各自的 compact 变体，原名在前、变体在后，
// 并按首次出现去重。顺序是有意的：渠道 ModelList 直接用它渲染，原名排在一起更好认。
func WithCompactModelVariants(models []string) []string {
	variants := make([]string, 0, len(models)*2)
	seen := make(map[string]struct{}, len(models)*2)
	for _, model := range models {
		if _, ok := seen[model]; ok {
			continue
		}
		seen[model] = struct{}{}
		variants = append(variants, model)
	}
	for _, model := range models {
		compactModel := WithCompactModelSuffix(model)
		if _, ok := seen[compactModel]; ok {
			continue
		}
		seen[compactModel] = struct{}{}
		variants = append(variants, compactModel)
	}
	return variants
}
