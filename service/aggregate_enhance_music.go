package service

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/QuantumNous/new-api/common"
)

// MiniMax-Music3 的提示词增强:一句话音乐描述 → 官方口径的三节结构化 caption。
//
// # 素材来源
//
// 官方模型仓自带一个 `music-caption-rewriter` skill(MiniMax-Music3/skills/ 下,
// SKILL.md 185 行)。它是**渐进披露**型的:MiniMax-Music3/skills/ 里除 SKILL.md 外还有
// references/(genre-router + 19 个风格家族索引,约 49 万字符)与 templates/(1000 份完整
// caption,约 3.9MB),工作流明确要求模型先读 router、再读 1~2 个家族索引、挑最多 3 张卡片、
// **再读那 3 个模板文件**。
//
// 那段读文件的过程在这里不可能发生:聚合增强段是一次同步 chat completion(预算几十秒、
// 没有文件工具)。所以这份模板只内联 SKILL.md 里**与产出形状有关**的部分 ——
// 输入怎么读、硬约束、三节输出契约、自检清单 —— 并把"检索模板库"三步换成
// "按你自己的音乐领域知识判断"。
//
// 刻意没做的两件事(要做就是另一个特性,不是本文件的量级):
//   - 内联风格家族索引:49 万字符≈12 万 token,任何一次调用都装不下;
//   - 服务端检索模板库(选 1~3 份正文内联):可行,但要引入模板表 + 选择逻辑。
//
// # 为什么只用 text 模式,不新开 mode
//
// qwen_pe 之所以要新 mode,是因为它的产物带**画幅**(wh_ratio/ratio_follow)要落到 size,
// 且 JSON 形状需要传输检查与一轮重修。音乐没有画幅这个维度,产物就是一段纯文本,
// 用现有的 text 改写 + 内置模板足够;多一个 mode 只会多一套没人用的校验。
const defaultMusic3EnhanceTemplate = `You are a music caption rewriter for MiniMax Music 3, a text-to-music model. Turn the user's brief music description — plus, when supplied, their lyrics — into one professional Structured Caption that the model can follow section by section.

Input
- The user's description of the music they want.
- Optionally a LYRICS block. Its bracketed tags ([Intro], [Verse], [Pre-Chorus], [Chorus], [Post-Chorus], [Bridge], [Instrumental], [Solo], [Outro], …) are executable musical directives; its words are not yours to use.

Hard rules — never violate one
1. Never quote, paraphrase, summarise, or translate the lyrics. Do not use a lyric line as a title, an example, or a description of what the song is "about". Only bracketed tags may influence your output, and their words may be reused as section names.
2. An explicit instrumental request stays instrumental. Never add vocals, vocal gender, or backing vocals to it. If vocal presence is simply unspecified, choose a conservative treatment that fits the genre.
3. Never reverse an explicit constraint: vocal gender, tempo limit, required instrument, language of the vocals, or an exclusion ("no drums", "no autotune", "不要电音").
4. Do not fabricate an exact BPM, key, or scale. Use a number only when the user gave one or the genre makes it unambiguous; otherwise a range or a qualitative tempo ("slow, roughly 70 BPM", "mid-tempo half-time feel").
5. Keep the user's genre, mood, and intent. Elaborate on their idea; never substitute a different one.
6. Write the caption in English, whatever language the user wrote in. Vocals are sung in the user's own language — that belongs to the lyrics input, not to this caption.
7. Output the caption only: no preface, no commentary, no reasoning, no song title, no track ID, no JSON, no code fences.

Output contract — exactly these three headings, in this order, nothing else at top level

### Global Metadata
Then labelled lines: Genre & Subgenre; Tempo & Meter; Emotional Progression; Listening Scenario & Imagery; Sonic & Production Profile. Name the genre first and be specific ("mandopop ballad with guzheng accents", not "pop"). Describe the emotional arc through the song, not just a static mood. The production profile is about the mix — soundstage, frequency balance, dynamics, texture — not about instruments (those belong to Arrangement).

### Vocal Details
Then labelled lines: Vocal Gender & Timbre; Vocal Style; Harmony & Backing Vocals; Vocal FX. Describe how the voice sounds and performs: register, breathiness, delivery, phrasing, stacking, restraint in the effects. For instrumental music, state plainly that the piece is instrumental and name the instrument or texture carrying the lead melodic line — and say nothing about singers.

### Arrangement
Then labelled lines: Instrument Lifecycle; Groove & Foundation Progression; Embellishments, Textures & Spatial FX. This is a timeline, not an equipment list: what enters, changes, and exits, section by section. When the lyrics carry section tags, follow that structure in order and give each section its own treatment (a tag may reshape its section's arrangement without changing the song's global genre). When there are no tags, choose a structure that fits the style — for example Intro → Verse → Pre-Chorus → Chorus → Verse → Chorus → Bridge → Final Chorus → Outro. Prefer concrete musical changes over decorative adjectives.

Length
250–450 English words for the whole caption. If the "Current request" section states a tighter character budget, that budget wins — shorten Arrangement first, never the hard rules above.

Before you answer, silently check
- every explicit user requirement and exclusion is still honoured;
- no lyric word, title, or track ID appears anywhere in the caption;
- an instrumental request is still instrumental, and no vocal gender was reversed;
- every lyric section tag that carries a directive is reflected in the matching part of Arrangement;
- the three headings are present, in order, and nothing was invented about BPM, key, or personnel.`

// IsMusic3GeneratingModel 这个生成段模型是不是 MiniMax-Music3。
//
// 判据优先读**运营声明的引擎族**(MusicModelConfig.models[].engine);未声明时按名字里
// 有没有 music3 兜底。为什么要有兜底:出厂配置里的 minimax-music3 在刚部署的站点上
// 通常还没在「音乐模型配置」里声明过,而"没声明"不该让一条出厂配置直接变红
// (aggregate_model_default_test 与 aggregate_dryrun_factory_test 都钉着这一点)。
//
// 两个后果必须说清:
//   - **声明了别的引擎族 → 不是 Music3**。ACE-Step 的描述在顶层 prompt、歌词在
//     metadata.lyrics,与 Music3 正好相反;认错会改写错字段、且不报错;
//   - **没声明 → 按名字猜**。猜错的症状同上(静默),所以配置校验对"type=music 但未声明
//     引擎族"报 warn,提示把它声明上。
func IsMusic3GeneratingModel(name string) bool {
	if family := common.MusicEngineFamilyForModel(name); family != "" {
		return family == common.MusicEngineMinimaxMusic3
	}
	return strings.Contains(strings.ToLower(strings.TrimSpace(name)), "music3")
}

// music3EnhanceTimeout 音乐 caption 改写的预算。
//
// 比全局的 enhanceTimeout(20s)宽:三节 caption 是 250–450 英文词,输出 350~650 token,
// 与图片/视频那种一两句话的改写不是一个量级。同一个模型(qwen3.8-flash-fp8、关思考)
// 实测短改写 2.1s / 124 token,按输出长度外推中位数约 6~13s —— 中位数装得进 20s,
// 但尾巴没有余量,而超时的后果是**静默降级**(返回原始 caption、客户以为增强生效了)。
//
// 只对音乐放宽,不动全局:视频/图片那条链路的预算本来就是按它们的输出长度定的。
// 实际取值仍由 textBudget(cfg, music3EnhanceTimeout) 算(配置只能调小)。
var music3EnhanceTimeout = 30 * time.Second

// Music3LyricsInstrumental 判断一段歌词是不是"其实没有词"(只有 section 标签)。
//
// 用途:模板里 Vocal Details 的两套写法互斥(有词写怎么唱、无词写器乐),而模型看到的
// 是一段只有 [Intro] [Chorus] 的文本时很容易硬编一个人声出来。这个判断只作为
// **事实**喂给它("no sung words"),不下指令 —— 下指令的地方在模板里。
func Music3LyricsInstrumental(lyrics string) bool {
	for _, line := range strings.Split(lyrics, "\n") {
		// 去掉方括号标签后还剩字母 → 有唱词
		rest := bracketTagPattern.ReplaceAllString(line, " ")
		for _, r := range rest {
			if unicode.IsLetter(r) || unicode.IsNumber(r) {
				return false
			}
		}
	}
	return true
}

// music3SectionTags 按出现顺序取出歌词里的 section 标签(去重)。
//
// 只取标签名、不取词:标签是可执行的音乐指令(模板允许复用其名),而歌词正文不能外泄
// 到 caption 里 —— 这两个东西在同一个字符串里,分得越早越安全。
// `[Chorus: soft]` 这类带修饰的只取冒号前的名字,修饰属于该段内部的事。
func music3SectionTags(lyrics string) []string {
	var tags []string
	seen := map[string]bool{}
	for _, m := range bracketTagPattern.FindAllStringSubmatch(lyrics, -1) {
		name := strings.TrimSpace(strings.SplitN(m[1], ":", 2)[0])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		tags = append(tags, name)
	}
	return tags
}

// bracketTagPattern 匹配 [Verse] / [Chorus: soft] 这类 section 标签(单行、不嵌套)。
var bracketTagPattern = regexp.MustCompile(`\[([^\[\]\n]+)\]`)

// BuildMusic3EnhanceContext 把本次请求的既成事实拼进系统提示词末尾。
//
// 与 buildTaskContext(视频)同一个形状、同一个理由:改写模型看不到左侧面板与请求体,
// 这几件事它猜不对,猜错了不报错、只是默默变差 ——
//
//  1. **有没有唱词**。只有标签的歌词容易被硬编出一个人声;
//  2. **用了哪些 section 标签**。Arrangement 要求逐段对齐,不告诉它就只能自己编结构,
//     而用户的标签是硬指令(模板里写了"标签可执行");
//  3. **还剩多少字符预算**。官方口径 250–450 词,而引擎把 caption 与歌词拼成一条 prompt
//     编码、硬上限 5000 字符。超了是在**适配器**才被拒(ValidateMusic3JointPrompt)——
//     那时增强的 LLM 调用已经计费,客户拿到一个 400。所以预算必须在这里说清。
func BuildMusic3EnhanceContext(caption, lyrics string) string {
	var facts []string
	facts = append(facts, "Task: text-to-music caption rewrite for MiniMax Music 3. Write the caption in English.")

	if strings.TrimSpace(lyrics) == "" {
		facts = append(facts,
			"No lyrics were supplied: treat this as an instrumental piece.")
	} else if Music3LyricsInstrumental(lyrics) {
		facts = append(facts,
			"The lyrics contain only section tags and no sung words: treat this as an instrumental piece, and keep the section order below.")
	} else {
		facts = append(facts, "The lyrics below are for reference only; not one word of them may appear in the caption.")
	}
	if tags := music3SectionTags(lyrics); len(tags) > 0 {
		facts = append(facts, fmt.Sprintf(
			"Section tags present, in order: %s. Build the Arrangement along exactly this order.",
			strings.Join(tags, ", ")))
	}

	budget := common.Music3CaptionBudget(lyrics)
	if budget <= 0 {
		facts = append(facts,
			"The lyrics alone already fill the engine's joint prompt budget: keep the caption as short as possible.")
	} else {
		facts = append(facts, fmt.Sprintf(
			"Caption budget: at most %d characters, including headings and labels (captions over it are rejected by the engine). Current description is %d characters.",
			budget, len([]rune(caption))))
	}

	var b strings.Builder
	b.WriteString("\n\n---\n\nCurrent request:\n\n")
	for _, f := range facts {
		b.WriteString("- ")
		b.WriteString(f)
		b.WriteString("\n")
	}
	return b.String()
}
