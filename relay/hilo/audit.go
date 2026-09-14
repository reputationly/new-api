package hilo

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// 成品审计:校验**渲染出来的那段文本**,而不是 IR。
//
// 移植自 XINGSHEN2 的 audit_h3_prompt。
//
// # 为什么 ValidateIR 之外还要这一层
//
// 两者查的是不同的东西。ValidateIR 查的是结构自洽(时长加得起来、引用的
// 镜头号存在);审计查的是**交出去的文本本身**对不对:
//
//   - 模型在描述里写了 `image_1`,而 H3 只认 `<Picture 1>` —— IR 完全合法,
//     渲染也正常,只是成品里带着一个内部 ID,H3 读不懂那是什么。
//   - 改写语言是英文,但正文里混进了中文散文 —— 这在 IR 上看不出来,
//     每个字段都填了、都合法。
//   - 提示词引用了 `<Picture 3>`,而这次只传了两张图。
//
// 这些全都不报错,只是出来的视频不对。审计是最后一道能指认它们的地方。
//
// # 审计失败会喂回重修轮
//
// RenderPrompt 在审计不过时返回错误,compileViaIR 把具名问题递回模型
// (见 service/aggregate_enhance_ir.go)。这是 IR 方案真正的价值所在 ——
// 错误有名字,就能被驳回、被重修、被计数。

var (
	// referenceTagPattern 官方素材标签。
	referenceTagPattern = regexp.MustCompile(`<(Picture|Video|Audio)\s+(\d+)>`)
	// subjectTagPattern 官方主体标签(仅六段式用)。
	subjectTagPattern = regexp.MustCompile(`<Subject\s+(\d+)>`)
	// angleTagPattern 任何尖括号标签。用来揪出不在官方词汇表里的那些。
	angleTagPattern = regexp.MustCompile(`<([^>]+)>`)
	// timestampPattern mm:ss.mmm。
	timestampPattern = regexp.MustCompile(`(\d{2}):(\d{2})\.(\d{3})`)
	// cjkPattern 中日韩文字。
	cjkPattern = regexp.MustCompile(`[\x{3400}-\x{4dbf}\x{4e00}-\x{9fff}\x{f900}-\x{faff}]`)
	// dialogueTagPattern 官方逐字标签 <d>台词</d> / <l>歌词</l>。
	//
	// **它们里面的内容保留源语言**,是协议的一部分,不是违规。审计时先把
	// 它们整段挖掉再查语言,挖掉的只是探针,交付的提示词里标签照旧。
	dialogueTagPattern = regexp.MustCompile(`(?is)<(?:d|l)(?:\s[^>]*)?>.*?</(?:d|l)>`)
	// internalMediaTerms 内部抽帧术语。漏进成品会让 H3 以为要画一张联系表。
	internalMediaTerms = regexp.MustCompile(`(?i)\b(contact[ -]?sheet|sampled? frames?|frame sampling|thumbnail grid)\b`)
	// rawAssetIDPattern 内部素材 ID。边界另行判断(Go 的 regexp 没有后顾断言)。
	rawAssetIDPattern = regexp.MustCompile(`(?i)(?:image|video|audio)_\d+`)
)

// officialAngleTags 除素材/主体标签外,还允许出现的官方尖括号词汇。
var officialAngleTags = map[string]bool{
	"d": true, "/d": true,
	"l": true, "/l": true,
	"scenetrans": true, "/scenetrans": true,
	"cutoff": true, "/cutoff": true,
}

// promptTimestampEpsilon 时间戳与总时长比较的容差,与 durationEpsilon 同源。
const promptTimestampEpsilon = durationEpsilon

// rawAssetIDSpans 文本里所有**独立成词**的内部素材 ID 位置。
//
// Go 的 regexp 是 RE2,没有后顾断言,`(?<![A-Za-z0-9_])` 写不出来;
// `\b` 也不行 —— 下划线算词字符,`image_1` 前后的 `\b` 判定与源实现不一致。
// 所以边界自己判:前后一个字节不能是字母、数字或下划线。
//
// 不判边界的后果很具体:`my_image_12x` 里会切出一个 `image_12`,投影时把它
// 换成 `<Picture 12>`,活生生改坏一个本来正常的词。
func rawAssetIDSpans(text string) [][]int {
	isWord := func(b byte) bool {
		return b == '_' ||
			(b >= '0' && b <= '9') ||
			(b >= 'a' && b <= 'z') ||
			(b >= 'A' && b <= 'Z')
	}
	var out [][]int
	for _, m := range rawAssetIDPattern.FindAllStringIndex(text, -1) {
		if m[0] > 0 && isWord(text[m[0]-1]) {
			continue
		}
		if m[1] < len(text) && isWord(text[m[1]]) {
			continue
		}
		out = append(out, m)
	}
	return out
}

// ProjectReferenceLabels 把正文里的内部素材 ID 换成官方标签。
//
// 渲染的最后一步。模型在 event / retention_description 这类自由文本里
// 写 `image_1` 是常态 —— 它在 IR 里就是这么标识素材的。**H3 只认
// `<Picture 1>`**,原样交出去它读不懂那是什么,而这不会报错。
//
// 源实现在这里按 ID **从长到短**逐个正则替换,防止 `image_1` 啃掉
// `image_10` 的前半截。我们不需要那一步:这里是**先切词、再整词查表**,
// `image_10` 只会作为一个完整 token 被找出来,压根不存在被短 ID 啃掉的
// 可能。照抄一个排序过来只会让人以为它在防什么(实测去掉排序行为不变)。
func ProjectReferenceLabels(text string, inv map[string]string) string {
	if len(inv) == 0 {
		return text
	}
	lookup := make(map[string]string, len(inv))
	for id, label := range inv {
		lookup[strings.ToLower(id)] = label
	}

	spans := rawAssetIDSpans(text)
	if len(spans) == 0 {
		return text
	}
	var b strings.Builder
	last := 0
	for _, sp := range spans {
		label, ok := lookup[strings.ToLower(text[sp[0]:sp[1]])]
		if !ok {
			continue
		}
		b.WriteString(text[last:sp[0]])
		b.WriteString(label)
		last = sp[1]
	}
	b.WriteString(text[last:])
	return b.String()
}

// ProjectSubjectLabels 把正文里的主体 ID 换成官方主体标签。
//
// 和素材 ID 是同一类漏,但**抓不到同一个模式**:素材 ID 有固定前缀
// (image_/video_/audio_),主体 ID 是模型自己起的名字 —— 实测真实产出里
// 出现过 "girl_1 pivots, walks a few steps…",而 IR 里那个主体的
// subject_id 正是 girl_1。
//
// H3 只认 <Subject 1>。留着 girl_1 的后果是它把这个词当普通名词读,
// 于是"同一个人"这条线索断了 —— 六段式里各段本该靠同一个标签串起来。
// 不报错,只是主体一致性没了。
//
// 主体 ID 是模型自起的任意字符串,girl_1 完全可能是 girl_10 的前缀 ——
// 但**替换顺序无关**:replaceWholeWord 判整词边界,"girl_1" 在 "girl_10"
// 里因为后面跟着数字而不匹配。源实现在素材那边按长度排过序,照搬到这里
// 只会让人以为它在防什么(实测去掉排序行为不变)。真正起作用的是边界判断。
func ProjectSubjectLabels(text string, inv map[string]string) string {
	for id, label := range inv {
		if id != "" {
			text = replaceWholeWord(text, id, label)
		}
	}
	return text
}

// replaceWholeWord 整词替换。边界判据与 rawAssetIDSpans 一致:
// 前后一个字节不能是字母、数字或下划线 —— 否则 "girl_1" 会在
// "cowgirl_1" 里被切出来。
func replaceWholeWord(text, word, repl string) string {
	if word == "" || !strings.Contains(text, word) {
		return text
	}
	isWord := func(b byte) bool {
		return b == '_' || (b >= '0' && b <= '9') ||
			(b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
	}
	var b strings.Builder
	for i := 0; i < len(text); {
		if strings.HasPrefix(text[i:], word) &&
			(i == 0 || !isWord(text[i-1])) &&
			(i+len(word) == len(text) || !isWord(text[i+len(word)])) {
			b.WriteString(repl)
			i += len(word)
			continue
		}
		b.WriteByte(text[i])
		i++
	}
	return b.String()
}

// AuditPrompt 审计渲染出来的成品提示词。
func AuditPrompt(ir *ContextIR, prompt string) *ValidationReport {
	r := &ValidationReport{}
	if ir == nil {
		r.add("PROMPT_IR_MISSING", "$.h3_prompt", "没有 IR,无法审计成品")
		return r
	}
	isRef := strings.EqualFold(ir.Task.Type, string(TaskR2VA))

	auditSections(r, prompt, isRef)
	auditLeaks(r, prompt)
	auditLanguage(r, ir, prompt)
	auditTimestamps(r, ir, prompt)
	auditTags(r, ir, prompt, isRef)
	if isRef {
		auditRefSubjectCoverage(r, ir, prompt)
	}
	return r
}

// auditSections 段落齐不齐、顺序对不对、有没有混进另一种玩法的段。
func auditSections(r *ValidationReport, prompt string, isRef bool) {
	required := SectionsFor(taskTypeOfRef(isRef))
	var positions []int
	for _, s := range required {
		idx := sectionIndex(prompt, s)
		if idx < 0 {
			r.add("PROMPT_SECTION_MISSING", "$.h3_prompt", "缺少段落 %s", s)
			continue
		}
		positions = append(positions, idx)
	}
	if !sortedAsc(positions) {
		r.add("PROMPT_SECTION_ORDER", "$.h3_prompt", "段落顺序不对")
	}

	// 混进另一种玩法的段落。
	//
	// 两层防护都照源实现留着:只取六段式**前四段**,且下面再跳过一次
	// requiredSet。后两段(overall_soundscape / non_diegetic_music)两种
	// 玩法共有,漏掉任一层都会把每一份合法的三段式判错。
	//
	// 实际起作用的是 requiredSet 那道 —— 去掉 [:4] 行为不变。
	var forbidden []string
	if isRef {
		forbidden = baseSections
	} else {
		forbidden = refSections[:4]
	}
	requiredSet := map[string]bool{}
	for _, s := range required {
		requiredSet[s] = true
	}
	for _, s := range forbidden {
		if requiredSet[s] {
			continue
		}
		if sectionIndex(prompt, s) >= 0 {
			r.add("PROMPT_SECTION_UNEXPECTED", "$.h3_prompt", "出现了不该有的段落 %s", s)
		}
	}

	// [Shot 1] 是必须的:H3 按它认镜头边界,没有就整段当一个镜头处理。
	if !strings.Contains(prompt, "[Shot 1]") {
		r.add("PROMPT_SHOT_ONE_MISSING", "$.h3_prompt", "缺少 [Shot 1]")
	}
}

// auditLeaks 内部词汇有没有漏进成品。
func auditLeaks(r *ValidationReport, prompt string) {
	if internalMediaTerms.MatchString(prompt) {
		r.add("INTERNAL_MEDIA_LEAK", "$.h3_prompt", "内部抽帧术语漏进了成品提示词")
	}
	// 投影(ProjectReferenceLabels)之后还剩的内部 ID,说明它指的素材根本
	// 不在本次清单里 —— 模型引用了一个没传的素材。
	var leaked []string
	seen := map[string]bool{}
	for _, sp := range rawAssetIDSpans(prompt) {
		id := prompt[sp[0]:sp[1]]
		if !seen[strings.ToLower(id)] {
			seen[strings.ToLower(id)] = true
			leaked = append(leaked, id)
		}
	}
	if len(leaked) > 0 {
		sort.Strings(leaked)
		r.add("RAW_ASSET_ID_LEAK", "$.h3_prompt",
			"内部素材 ID 必须投影成官方标签,成品里仍有: %s", strings.Join(leaked, ", "))
	}
}

// auditLanguage 改写语言约定。
//
// **这条是「对白保留原文和原语言」真正的执行点。** 正文要用改写语言写,
// 只有 <d>台词</d> / <l>歌词</l> 里的内容保留源语言。翻译台词是这一步
// 最容易犯、也最难发现的错 —— 成品读起来完全正常。
//
// 已知局限:源实现还会把**素材里的可见文字**(perception 采出来的 OCR
// 结果)一并放行。我们没做素材感知,所以画面里本就该保留的中文招牌若被
// 写进正文,这里会误报一次 —— 代价是一轮重修,不是错误的成品。
func auditLanguage(r *ValidationReport, ir *ContextIR, prompt string) {
	if !strings.EqualFold(strings.TrimSpace(ir.Protocol.RewriteLanguage), "English") {
		return
	}
	// 先把逐字标签整段挖掉再查。挖的只是探针,交付的文本不动。
	probe := dialogueTagPattern.ReplaceAllString(prompt, "")
	if cjkPattern.MatchString(probe) {
		r.add("PROMPT_REWRITE_LANGUAGE_VIOLATION", "$.h3_prompt",
			"改写语言为英文时,正文不得出现中文;只有 <d>…</d> / <l>…</l> 里的逐字台词与歌词保留源语言")
	}
}

// auditTimestamps 时间戳必须落在本次时长之内。
func auditTimestamps(r *ValidationReport, ir *ContextIR, prompt string) {
	dur := ir.Task.DurationSeconds
	for _, m := range timestampPattern.FindAllStringSubmatch(prompt, -1) {
		mm, _ := strconv.Atoi(m[1])
		ss, _ := strconv.Atoi(m[2])
		ms, _ := strconv.Atoi(m[3])
		sec := float64(mm)*60 + float64(ss) + float64(ms)/1000
		// **只查上界,0 是合法的。**
		//
		// 早先这里写的是 sec <= 0 也算违规,理由是"首个镜头不带时间戳,
		// 出现 00:00.000 说明多写了一个"。那条理由只对**渲染器自己写的**
		// 文本成立(shotText 确实不写 0 秒戳),但成品里还有一大片**模型写的**
		// 自由文本:编译器提示词要求"每个动作同步音各占一条时间线事件,
		// 写清起止",sync_rules 的骨架也写着"落在哪个动作或哪个时刻" ——
		// 一条 00:00.000 的音效提示完全正常。
		//
		// 判错的代价很重:一份过了全部结构规则的 IR 被整个丢掉,白烧一轮重修
		// 加一次 text 回落,也就是实测 34-102 秒编译成本的两倍 —— 换来的只是
		// 一个字面上的洁癖。
		if sec >= dur+promptTimestampEpsilon {
			r.add("PROMPT_TIMESTAMP_RANGE", "$.h3_prompt",
				"时间戳 %s 超出本次时长(%g 秒)", m[0], dur)
		}
	}
}

// auditTags 素材标签与主体标签:有没有引用不存在的,六段式下有没有漏用。
func auditTags(r *ValidationReport, ir *ContextIR, prompt string, isRef bool) {
	expected := map[string]bool{}
	for _, label := range BuildReferenceInventory(ir) {
		expected[label] = true
	}
	actual := map[string]bool{}
	for _, m := range referenceTagPattern.FindAllStringSubmatch(prompt, -1) {
		actual[fmt.Sprintf("<%s %s>", m[1], m[2])] = true
	}
	if extra := diffKeys(actual, expected); len(extra) > 0 {
		// 引用了没传的素材。H3 会对着一个不存在的标签编内容。
		r.add("REFERENCE_TAG_UNEXPECTED", "$.h3_prompt",
			"引用了本次没有的素材标签: %s", strings.Join(extra, ", "))
	}
	if isRef {
		if missing := diffKeys(expected, actual); len(missing) > 0 {
			// 参考生视频的全部创作意图都在参考素材里,漏用一个等于把它扔了。
			r.add("REFERENCE_TAG_MISSING", "$.h3_prompt",
				"有素材从未被引用: %s", strings.Join(missing, ", "))
		}
	}

	expectedSubj := map[string]bool{}
	if isRef {
		for _, label := range BuildSubjectInventory(ir) {
			expectedSubj[label] = true
		}
	}
	actualSubj := map[string]bool{}
	for _, m := range subjectTagPattern.FindAllStringSubmatch(prompt, -1) {
		actualSubj[fmt.Sprintf("<Subject %s>", m[1])] = true
	}
	if extra := diffKeys(actualSubj, expectedSubj); len(extra) > 0 {
		r.add("SUBJECT_TAG_UNEXPECTED", "$.h3_prompt",
			"引用了不存在的主体标签: %s", strings.Join(extra, ", "))
	}
	if isRef {
		if missing := diffKeys(expectedSubj, actualSubj); len(missing) > 0 {
			r.add("SUBJECT_TAG_MISSING", "$.h3_prompt",
				"有主体从未被引用: %s", strings.Join(missing, ", "))
		}
	}

	// 不在官方词汇表里的尖括号标签。
	//
	// H3 把尖括号当协议读,自造一个 `<camera>` 它会当成指令去解析,
	// 而不是当成文字 —— 成品看起来没问题,行为却变了。
	allowed := map[string]bool{}
	for label := range expected {
		allowed[strings.Trim(label, "<>")] = true
	}
	for label := range expectedSubj {
		allowed[strings.Trim(label, "<>")] = true
	}
	var illegal []string
	seen := map[string]bool{}
	for _, m := range angleTagPattern.FindAllStringSubmatch(prompt, -1) {
		tag := m[1]
		if allowed[tag] || officialAngleTags[strings.ToLower(tag)] || seen[tag] {
			continue
		}
		seen[tag] = true
		illegal = append(illegal, tag)
	}
	if len(illegal) > 0 {
		sort.Strings(illegal)
		r.add("PROMPT_NONOFFICIAL_ANGLE_TAG", "$.h3_prompt",
			"出现了非官方的尖括号标签: <%s>", strings.Join(illegal, ">, <"))
	}
}

// auditRefSubjectCoverage 六段式下每个主体都必须在三个段里各露一次面。
//
// 单独一个函数是因为它要切段:少了任何一处,H3 对那个主体的理解就是残缺的
// —— 定义里没有它就没有身份锚点,保留分析里没有它就不知道该保留什么。
func auditRefSubjectCoverage(r *ValidationReport, ir *ContextIR, prompt string) {
	inv := BuildSubjectInventory(ir)
	if len(inv) == 0 {
		return
	}
	sections := splitSections(prompt, refSections)
	checks := []struct {
		section string
		code    string
		hint    string
	}{
		{"subject_definitions", "SUBJECT_DEFINITION_MISSING", "缺少官方定义"},
		{"retention_analysis", "SUBJECT_RETENTION_MISSING", "没有出现在保留分析里"},
		{"detailed_description", "SUBJECT_DETAIL_USAGE_MISSING", "没有出现在详细描述里"},
	}
	labels := make([]string, 0, len(inv))
	for _, l := range inv {
		labels = append(labels, l)
	}
	sort.Strings(labels)
	for _, label := range labels {
		for _, c := range checks {
			if !strings.Contains(sections[c.section], label) {
				r.add(c.code, "$.h3_prompt."+c.section, "%s %s", label, c.hint)
			}
		}
	}
}

// ── 小工具 ──────────────────────────────────────────────────────────

func taskTypeOfRef(isRef bool) string {
	if isRef {
		return string(TaskR2VA)
	}
	return string(TaskT2V)
}

// sectionIndex 段落标题的位置。**必须锚在行首** —— 正文里提一句
// "summary:" 不是一个段落开始,当成段落会让顺序判定乱掉。
func sectionIndex(prompt, section string) int {
	head := section + ":"
	for i := 0; i+len(head) <= len(prompt); i++ {
		if i > 0 && prompt[i-1] != '\n' {
			continue
		}
		if prompt[i:i+len(head)] == head {
			return i
		}
	}
	return -1
}

// splitSections 把成品切成各段正文。
func splitSections(prompt string, names []string) map[string]string {
	out := map[string]string{}
	for i, name := range names {
		start := sectionIndex(prompt, name)
		if start < 0 {
			continue
		}
		start += len(name) + 1
		end := len(prompt)
		for _, later := range names[i+1:] {
			if idx := sectionIndex(prompt, later); idx > start && idx < end {
				end = idx
				break
			}
		}
		out[name] = prompt[start:end]
	}
	return out
}

func sortedAsc(v []int) bool {
	for i := 1; i < len(v); i++ {
		if v[i] < v[i-1] {
			return false
		}
	}
	return true
}

// diffKeys a 里有、b 里没有的键,排好序。
func diffKeys(a, b map[string]bool) []string {
	var out []string
	for k := range a {
		if !b[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
