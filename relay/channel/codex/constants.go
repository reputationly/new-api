package codex

import (
	"slices"

	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

var baseModelList = []string{
	"gpt-5.6-sol",
	"gpt-5.6-terra",
	"gpt-5.6-luna",
	"gpt-5.5",
	"gpt-5.4",
	"gpt-5.4-mini",
	"gpt-5.3-codex-spark",
	"codex-auto-review",
}

// codex-auto-review 只有原生形态，不对外暴露它的 -openai-compact 变体。
var ModelList = slices.DeleteFunc(
	ratio_setting.WithCompactModelVariants(baseModelList),
	func(modelName string) bool {
		return modelName == ratio_setting.WithCompactModelSuffix("codex-auto-review")
	},
)

const ChannelName = "codex"
