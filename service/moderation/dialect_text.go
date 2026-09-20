package moderation

import (
	"github.com/QuantumNous/new-api/setting/system_setting"
)

// 文本判定协议（dialect）。见 docs/content-moderation-design.md §4.1.1。
//
// 存在的理由：L1 曾经写死了 Qwen3Guard-Gen 的输出格式（`Safety: X` / `Categories: Y`）。
// 换成任何别的护栏模型时，解析器找不到那两行 → safety 为空 → parseVerdict 判 ActionError
// → 配合默认开着的 FailOpen 就是**静默全量放行**：审核看起来在跑，实际一条都没审。
//
// 所以「换模型」不能是改代码，必须是改配置。dialect 把两件事从 L1 里抽出来：
// 请求参数（max_tokens 各模型差异很大）和输出解析。其余的——分段、节点轮换、
// 冻结、并发闸、严格度、类别处置——全部是 dialect 无关的，留在 l1_text.go 里共用。

// textJudgement dialect 解析出的归一化判定。
//
// 这是抽象的关键：各模型的输出格式天差地别，但**语义都能归约到「多严重 + 什么类型」**
// 这两个正交的量（§8.2 的前两个旋钮）。归约到这里之后，strictness 与类别处置的逻辑
// 就能对所有 dialect 共用一份。
type textJudgement struct {
	// Level 归一化安全等级：LevelSafe / LevelControversial / LevelUnsafe。
	//
	// **空串表示无法识别**，调用方必须判 ActionError 交给 fail 策略，绝不能当成
	// LevelSafe（§6.4）。「模型没说安全」≠「安全」——输出格式对不上多半意味着
	// 部署的不是这个 dialect 对应的模型，那种情况下每一条判定都是不可信的。
	Level string
	// Categories 已映射到本项目的类别常量。
	// 认不出来的上游类别要映射成 CategoryUnknownUpstream 而不是丢弃：
	// 丢了等于把未知风险当成安全放行。
	Categories []string
	// Reason 模型给出的归因理由，可空。
	//
	// 只有部分 dialect 提供（Zhongsen 的 <explanation>）。它进 detail 列给待复核队列用：
	// 复核时最需要回答的是「模型为什么判它违规」，而光有类别标签答不了这个问题。
	// **不回显给用户**——那等于送一个免费的绕过探测器（§9.2.2）。
	Reason string
}

// 归一化安全等级。
const (
	LevelSafe          = "safe"
	LevelControversial = "controversial"
	LevelUnsafe        = "unsafe"
)

// textDialect 一种文本判定协议。
type textDialect interface {
	// Name dialect 标识，与 system_setting.Dialect* 一致。落进 detail 列用于按模型分组统计。
	Name() string

	// MaxTokens 判定调用的 max_tokens。
	//
	// 由 dialect 而不是配置项决定，因为**它在各模型下的语义根本不同**：输出会
	// 自然停止的（qwen3guard）这只是个够不着的上限，不会自己停的（zhongsen）
	// 它就是实际解码量，直接决定时延。差一个量级，且没法从配置页看出来。
	// 两者的取值理由分别见各自 dialect 的 MaxTokens。
	MaxTokens() int

	// DefaultInputLimit 节点没填 input_limit 时的分段长度兜底（rune）。
	// 按模型的上下文窗口给，取值必须保证「一段 + 判定模板」塞得进窗口。
	DefaultInputLimit() int

	// ChatTemplateKwargs 要传给模型 chat template 的变量，可为 nil。
	//
	// 返回 nil 时请求体里**不出现这个字段**（omitempty），所以对不需要它的 dialect
	// 是零影响——qwen3guard 的线上请求因此保持字节级不变。空 map 与 nil 等价，
	// encoding/json 的 omitempty 对「长度为 0 的 map」同样省略（实测确认）。
	//
	// 反过来要注意：**键的值不受 omitempty 影响**。`{"reason_first": false}` 里的
	// false 会照常发出去，正是靠这一点才能显式传一个「假」值；换成带 omitempty 的
	// 结构体字段就会被静默吞掉（见 AGENTS.md Rule 6）。
	//
	// vLLM 会 apply_chat_template(messages, **chat_template_kwargs)，也就是把这里的
	// 键**splat 成模板的顶层变量**。注意这跟「模板里有个叫 chat_template_kwargs 的
	// dict 变量」是两种不同的取法，有些模板两种都认，读模板时要看清它取哪个。
	ChatTemplateKwargs() map[string]any

	// Parse 把模型输出解析成归一化判定。无法识别时返回 Level 为空的结果。
	Parse(content string) textJudgement

	// ProbeText 连通性测试用的无害文本。各 dialect 可以不同（语言、长度）。
	ProbeText() string
}

// textDialects 全部已实现的文本 dialect。
var textDialects = map[string]textDialect{
	system_setting.DialectQwen3Guard:   qwen3GuardDialect{},
	system_setting.DialectZhongsenText: zhongsenTextDialect{},
}

// resolveTextDialect 按名取 dialect。
//
// 认不出来时回落到 qwen3guard 而不是报错：这个函数在判定热路径上，而 dialect 名字
// 已经由 validateModerationEndpoints 在保存时校验过。真要出现未知值（options 表被
// 手改），回落到一个能工作的解析器比让审核整体失败更可取——后者在 fail-open 下
// 同样是静默放行，还多了一层「为什么全是 error」要排查。
func resolveTextDialect(name string) textDialect {
	if d, ok := textDialects[name]; ok {
		return d
	}
	return qwen3GuardDialect{}
}
