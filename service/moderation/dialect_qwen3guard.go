package moderation

import (
	"strings"

	"github.com/QuantumNous/new-api/setting/system_setting"
)

// qwen3guard dialect：Qwen3Guard-Gen。见 docs/content-moderation-design.md §4.1。
//
// 本文件是从 l1_qwen3guard.go 平移出来的，行为与接 dialect 之前**完全一致**——
// 它是存量部署在跑的那条路，任何改动都是回归风险，所以这次只搬不改。

// safetyLine / categoryLine 解析模型输出。
//
// Qwen3Guard-Gen 的官方 chat template 已经内置了判定 prompt（实测一次调用 prompt_tokens
// 约 308，而输入只有几个字），所以这里**不需要自己拼模板**，把待审文本作为 user message
// 直接发过去即可。输出形如：
//
//	Safety: Unsafe
//	Categories: Violent
//
// 大小写与空白都按模型实际输出容错，别指望它永远规整。
var (
	safetyLine   = "safety:"
	categoryLine = "categories:"
)

// categoryAliases 模型返回的类别名 → 本项目的类别常量（§4.1.1）。
//
// 官方类别是长名（"Non-violent Illegal Acts"），但实测返回的是短名（"Violent"），
// 两种都要认。认不出来的类别**不能丢弃**——那等于把未知风险当成安全，
// 所以统一落到 CategoryUnknownUpstream，由 CategoryAction 按「未登记即 block」处置。
var categoryAliases = map[string]string{
	"sexual content or sexual acts":       system_setting.CategorySexual,
	"sexual":                              system_setting.CategorySexual,
	"non-violent illegal acts":            system_setting.CategoryIllegal,
	"illegal":                             system_setting.CategoryIllegal,
	"politically sensitive topics":        system_setting.CategoryPolitical,
	"political":                           system_setting.CategoryPolitical,
	"jailbreak":                           system_setting.CategoryJailbreak,
	"violent":                             system_setting.CategoryViolent,
	"violence":                            system_setting.CategoryViolent,
	"suicide & self-harm":                 system_setting.CategorySelfHarm,
	"suicide and self-harm":               system_setting.CategorySelfHarm,
	"self-harm":                           system_setting.CategorySelfHarm,
	"unethical acts":                      system_setting.CategoryUnethical,
	"unethical":                           system_setting.CategoryUnethical,
	"personally identifiable information": system_setting.CategoryPII,
	"pii":                                 system_setting.CategoryPII,
	"copyright violation":                 system_setting.CategoryCopyright,
	"copyright":                           system_setting.CategoryCopyright,
}

type qwen3GuardDialect struct{}

func (qwen3GuardDialect) Name() string { return system_setting.DialectQwen3Guard }

// MaxTokens 64 足够：实测判定输出只有 8–9 个 token（"Safety: Unsafe\nCategories: Violent"）。
func (qwen3GuardDialect) MaxTokens() int { return 64 }

// DefaultInputLimit 节点没填时给一个对中文也打不穿 8192 窗口的保守值（§6.3 四）。
func (qwen3GuardDialect) DefaultInputLimit() int { return 4000 }

func (qwen3GuardDialect) ProbeText() string { return "今天天气怎么样" }

// ChatTemplateKwargs 不需要：Qwen3Guard-Gen 的官方模板没有可调变量，判定 prompt
// 是写死在里面的。返回 nil 让请求体里不出现这个字段,保持线上请求字节级不变。
func (qwen3GuardDialect) ChatTemplateKwargs() map[string]any { return nil }

// Parse 从模型输出里提取安全等级与类别。
func (qwen3GuardDialect) Parse(content string) textJudgement {
	var j textJudgement
	var safety string
	for _, line := range strings.Split(content, "\n") {
		lower := strings.ToLower(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(lower, safetyLine):
			safety = strings.TrimSpace(strings.TrimPrefix(lower, safetyLine))
		case strings.HasPrefix(lower, categoryLine):
			raw := strings.TrimSpace(strings.TrimPrefix(lower, categoryLine))
			if raw == "" || raw == "none" {
				continue
			}
			for _, c := range strings.Split(raw, ",") {
				c = strings.TrimSpace(c)
				if c == "" {
					continue
				}
				if mapped, ok := categoryAliases[c]; ok {
					j.Categories = append(j.Categories, mapped)
					continue
				}
				// 认不出来的类别不丢弃：丢了就等于把未知风险当安全放行。
				j.Categories = append(j.Categories, system_setting.CategoryUnknownUpstream)
			}
		}
	}

	// 只认这三个等级。别的（包括空串）一律留 Level 为空，由调用方判 ActionError——
	// 「模型没说安全」≠「安全」（§6.4）。
	switch safety {
	case LevelSafe, LevelControversial, LevelUnsafe:
		j.Level = safety
	}
	return j
}
