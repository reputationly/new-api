package service

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

//go:embed acestep/ace_caption_v1.txt
var defaultAceStepEnhanceTemplate string

// ACE-Step(V1.5)的提示词增强:把用户的曲风简述写成一条 dense caption。
//
// # 它和 MiniMax-Music3 那条的区别(字段是反的)
//
//	Music3   顶层 prompt = 歌词,   metadata.instructions = 曲风描述
//	ACE-Step 顶层 prompt = 曲风描述, metadata.lyrics      = 歌词
//
// 所以这一条改的是 prompt 位(sample 模式下是 metadata.sample_query),歌词只作只读
// 上下文。写错字段等于把用户的词拿去优化,引擎再把它当曲风描述去编曲 —— 不报错,
// 只是歌毁了。
//
// # 提示词的出处
//
// 规则来自官方 ace-step-skills 仓(github.com/ace-step/ace-step-skills)的
// skills/acestep-songwriting/SKILL.md 里 Caption 是最重要的输入 那一节,以及
// skills/acestep/api-reference.md 的参数表。**没有逐字照抄**:那份文档是写给 agent
// 走完整创作流程用的(还要配歌词创作与 MV 工作流),这里只保留 caption 该怎么写,
// 并把 BPM/调性/拍号不要写进 caption 翻成硬规则 —— 官方原文就是这条,列在它
// Caption 写作原则的第 8 点。
//
// # 为什么不让引擎自己增强(use_format)
//
// 引擎有 use_format(LM 改 caption/歌词)与 thinking。**本文件不依赖它们**:网关侧
// 改写的好处是产物落进任务记录(可对比、可回归),预算由我们自己的超时管,而且与
// 视频/图片那条增强共用同一套计费与降级路径。两条都开也不打架 —— 引擎是在我们给的
// caption 上再加工;要不要开 use_format 由运营在 metadata 里决定。

// IsAceStepGeneratingModel 这个生成段模型是不是 ACE-Step。
//
// 判据与 IsMusic3GeneratingModel 一致:优先运营声明的引擎族,未声明时按模型名兜底
// (出厂配置在刚部署的站点上通常还没在「音乐模型配置」里声明过)。
func IsAceStepGeneratingModel(name string) bool {
	if family := common.MusicEngineFamilyForModel(name); family != "" {
		return isAceStepEngineFamily(family)
	}
	m := strings.ToLower(strings.TrimSpace(name))
	return strings.Contains(m, "acestep") || strings.Contains(m, "ace-step")
}

// isAceStepEngineFamily 引擎族标识是否指 ACE-Step。
//
// 运营可能写 acestep / ace-step / ACE_Step,三种都当同一个引擎族 —— 判错的代价是
// 增强写进引擎不读的字段,而且不报错。
func isAceStepEngineFamily(family string) bool {
	s := strings.ToLower(strings.TrimSpace(family))
	s = strings.NewReplacer("-", "", "_", "", " ", "").Replace(s)
	return s == "acestep"
}

// AceStepCaptionTarget 这条请求里**该被改写的那段描述**在哪个字段。
//
// 两种形态:
//   - caption 模式(官方推荐):描述在顶层 prompt;
//   - sample 模式:prompt 通常为空,描述在 metadata.sample_query,由引擎在服务端自己
//     写歌词与 caption。
//
// 返回 (字段名, 原文)。字段名为空表示这段请求没有可改写的描述 —— 那时**绝不能用
// 歌词顶上**:那会把用户唱出来的词交给优化。
func AceStepCaptionTarget(prompt, sampleQuery string) (field, value string) {
	if strings.TrimSpace(prompt) != "" {
		return "prompt", prompt
	}
	if strings.TrimSpace(sampleQuery) != "" {
		return "sample_query", sampleQuery
	}
	return "", ""
}

// BuildAceStepEnhanceContext 这次改写的请求事实(拼在系统提示词末尾)。
//
// 全是**既成事实**:引擎任务类型、有没有源音频、歌词是不是纯器乐、歌词里的段标签顺序、
// 字数预算。模型看不到请求体,不说它就不知道这是一次翻唱/重绘而不是从零生成。
func BuildAceStepEnhanceContext(caption, lyrics, taskType string, hasSourceAudio bool, budget int) string {
	var facts []string
	facts = append(facts, "Task: ACE-Step music caption rewrite. Write the caption in English.")

	switch strings.ToLower(strings.TrimSpace(taskType)) {
	case "cover":
		facts = append(facts, "The task is a COVER: a source song is supplied and the target keeps its melody "+
			"structure. Describe the target's style - instrumentation, vocal treatment, production - not the source's.")
	case "repaint":
		facts = append(facts, "The task is a REPAINT: one region of a source audio is regenerated. Describe the "+
			"style that region must land in, consistent with the surrounding material.")
	default:
		facts = append(facts, "The task is text-to-music: the caption is the only guide for style.")
	}
	if hasSourceAudio {
		facts = append(facts, "A source audio file is attached to this request.")
	}

	if strings.TrimSpace(lyrics) == "" {
		facts = append(facts, "No lyrics were supplied: treat this as an instrumental piece and describe no singer.")
	} else if Music3LyricsInstrumental(lyrics) {
		facts = append(facts, "The lyrics contain only section tags and no sung words: treat this as instrumental, "+
			"and keep the section order below.")
	} else {
		facts = append(facts, "The lyrics are for reference only; not one word of them may appear in the caption.")
	}
	if tags := music3SectionTags(lyrics); len(tags) > 0 {
		facts = append(facts, fmt.Sprintf(
			"Section tags present, in order: %s. Your structure hints and energy description must follow exactly this order.",
			strings.Join(tags, ", ")))
	}

	facts = append(facts, fmt.Sprintf("Caption budget: at most %d characters, including every comma and space. "+
		"The current caption is %d characters and is the user's own wording - improve it, do not merely reflow it.",
		budget, len([]rune(strings.TrimSpace(caption)))))
	return strings.Join(facts, "\n")
}

// AceStepCaptionBudget 这次改写的字数上限:模型配置优先,没配给一个保守值。
//
// ACE-Step 的 caption 与歌词共用 MusicModelConfig 的 maxChars 闸(适配器会就地 400)。
// 增强后超限的代价是**整条请求被拒**,而那时 LLM 已经计费过 —— 所以先算出来让模型
// 从源头控制长度,写回后再核一次。
func AceStepCaptionBudget(generateModel string) int {
	if maxChars, configured := common.MusicMaxCharsForModel("t2m", generateModel); configured && maxChars > 0 {
		return maxChars
	}
	return aceStepDefaultCaptionBudget
}

// aceStepDefaultCaptionBudget 没配置时的保守上限。
//
// 官方文档没给 caption 的硬字数,只说具体优于笼统。一句 300-500 字符的 dense caption
// 足够覆盖全部维度,再长只会稀释;取 1200 留余量又不至于放开不管。
const aceStepDefaultCaptionBudget = 1200
