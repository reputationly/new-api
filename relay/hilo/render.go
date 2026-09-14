package hilo

import (
	"fmt"
	"strings"
)

// Context-IR → H3 提示词。**纯字符串拼接，没有 LLM 参与。**
//
// 这是引入 IR 的全部意义：段落名、顺序、时间戳格式由代码保证，不可能错。
// LLM 只负责填结构化的字段，「必须有哪几段」「顺序对不对」这类事它不用记。
//
// 移植自 XINGSHEN2/minimax-H3-context-IR 的 _render_base_prompt /
// _render_ref_prompt。

// 两种渲染的段落名。**固定英文字面量，不能翻译或改写** ——
// 它们是 H3 认的协议，而且成品审计按 `^段落名:` 逐个匹配。
var (
	baseSections = []string{
		"integrated_multimodal_description",
		"overall_soundscape",
		"non_diegetic_music",
	}
	refSections = []string{
		"subject_definitions",
		"summary",
		"retention_analysis",
		"detailed_description",
		"overall_soundscape",
		"non_diegetic_music",
	}
)

// SectionsFor 这个任务类型该有哪几段。
func SectionsFor(taskType string) []string {
	if strings.EqualFold(taskType, string(TaskR2VA)) {
		return refSections
	}
	return baseSections
}

// RenderPrompt 把 IR 渲染成 H3 提示词。
//
// 分叉只有一处：r2va 走六段式，其余走三字段。**六段式那条是目录里的
// 默认玩法**（image_mode 默认 reference），不是边角情况。
func RenderPrompt(ir *ContextIR) (string, error) {
	if ir == nil {
		return "", fmt.Errorf("ir is nil")
	}
	// **校验不过就不渲染。** 照 XINGSHEN2 的 render_h3_prompt 开头那两行。
	//
	// 渲染一份有问题的 IR 会产出"看起来正常"的提示词 —— 时长对不上、
	// 素材引用错、声音段空着，这些在成品文本里都看不出来。宁可在这里
	// 停下，让调用方降级用原始提示词。
	if rep := ValidateIR(ir); !rep.Passed() {
		return "", &ReportError{Stage: "context-ir 校验", Report: rep}
	}
	inv := BuildReferenceInventory(ir)
	isRef := strings.EqualFold(ir.Task.Type, string(TaskR2VA))
	var prompt string
	if isRef {
		prompt = renderRefPrompt(ir, inv)
	} else {
		prompt = renderBasePrompt(ir, inv)
	}

	// 投影:把正文里残留的内部素材 ID(`image_1`)换成官方标签
	// (`<Picture 1>`)。模型在自由文本里就是用内部 ID 指代素材的,
	// **H3 只认官方标签**,原样交出去它读不懂那是什么,而且不报错。
	prompt = ProjectReferenceLabels(prompt, inv)

	// 主体 ID 同理,但**只在六段式下投影**。
	//
	// 实测真实产出里出现过 "girl_1 pivots, walks a few steps…" —— 那是 IR 里的
	// subject_id 原样漏进了正文,而六段式各段本该靠同一个 <Subject N> 串起
	// "同一个人",留着内部 ID 就断了。
	//
	// **三段式下不能投影**:那条路的渲染器自己从不发主体标签
	// (shotText 传的 subjInv 是 nil),官方协议里也没有 <Subject N>。投上去
	// 就是凭空造一个标签,而成品审计只在六段式白名单里放行它 ——
	// 于是同时踩中 SUBJECT_TAG_UNEXPECTED 和 PROMPT_NONOFFICIAL_ANGLE_TAG,
	// 一份本来没问题的 IR 必然渲染失败,白烧一轮重修再静默回落 text。
	//
	// 源实现同样只投影素材 ID;主体清单它也只在 ref2va 下才建。
	if isRef {
		prompt = ProjectSubjectLabels(prompt, BuildSubjectInventory(ir))
	}

	// **成品审计。** 校验的是交出去的这段文本,不是 IR ——
	// 引用了没传的素材、英文正文里混进中文散文、时间戳超出时长,
	// 这些在 IR 上全都看不出来(每个字段都填了、都合法)。
	//
	// 不过就返回错误:调用方(compileViaIR)会把具名问题递回模型重修。
	if rep := AuditPrompt(ir, prompt); !rep.Passed() {
		return "", &ReportError{Stage: "成品审计", Report: rep}
	}
	return prompt, nil
}

// BuildReferenceInventory 按**最终条件顺序**给素材分配 H3 标签，按媒体类型
// 各自编号。
//
// 标签一旦分配就在所有段落里保持同一含义（官方 ref-en.txt 的要求）,
// 所以编号必须是确定性的 —— 让 LLM 自己编号的话，同一张图在不同段里
// 可能叫不同的名字，而那是最难查的一类错。
func BuildReferenceInventory(ir *ContextIR) map[string]string {
	names := map[string]string{"image": "Picture", "video": "Video", "audio": "Audio"}
	counters := map[string]int{}
	inv := map[string]string{}
	for _, a := range conditionAssets(ir) {
		n := names[a.MediaType]
		if n == "" {
			continue
		}
		counters[a.MediaType]++
		inv[a.AssetID] = fmt.Sprintf("<%s %d>", n, counters[a.MediaType])
	}
	return inv
}

// conditionAsset 参与条件的素材。
type conditionAsset struct {
	AssetID   string
	MediaType string // image | video | audio
}

// conditionAssets 哪些素材参与条件、按什么顺序。
//
//   - t2v：没有素材。
//   - r2va：**全部**素材都是条件（参考生视频的定义）。
//   - 其余：只有被 asset_bindings 引用到的才算 —— 传了但没绑定的素材
//     不该占一个标签号，否则编号会和实际引用对不上。
func conditionAssets(ir *ContextIR) []conditionAsset {
	if strings.EqualFold(ir.Task.Type, string(TaskT2V)) {
		return nil
	}
	// **有请求侧清单时一律按它编号。**
	//
	// 下面那条按模型书写顺序收集的路径会让标号跟着模型走:它先写 image_2
	// 再写 image_1,<Picture 1> 就指向了第二张图;漏写一个,后面全体前移
	// 一位。而**素材是按提交顺序发给模型和 H3 的**,两边一错位,提示词指着
	// 的素材和它描述的不是同一个 —— 不报错,只是出来的视频不对。
	//
	// 保留回落路径是给没有请求上下文的调用方(测试夹具、直接构造 IR 的
	// 单元测试)用的;生产路径上 Assets 一定非空。
	if len(ir.Assets) > 0 {
		out := make([]conditionAsset, 0, len(ir.Assets))
		for _, a := range ir.Assets {
			if a.AssetID == "" {
				continue
			}
			media := a.MediaType
			if media == "" {
				media = mediaTypeOf(a.AssetID, ir)
			}
			out = append(out, conditionAsset{AssetID: a.AssetID, MediaType: media})
		}
		return out
	}
	seen := map[string]bool{}
	var out []conditionAsset
	add := func(id, media string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, conditionAsset{AssetID: id, MediaType: media})
	}
	for _, b := range ir.AssetBindings {
		add(b.AssetID, mediaTypeOf(b.AssetID, ir))
	}
	for _, r := range ir.ReferenceRelationships {
		add(r.AssetID, mediaTypeOf(r.AssetID, ir))
	}
	for _, k := range ir.KeyframeRoles {
		add(k.AssetID, mediaTypeOf(k.AssetID, ir))
	}
	return out
}

// mediaTypeOf 从 asset_id 的前缀推媒体类型。
//
// 我们的 asset_id 是自己生成的（见 BuildAssetIDs），形如 `image_1`；
// 推不出来时按 image 处理 —— 大多数素材是图片，猜错也只影响标签名。
func mediaTypeOf(assetID string, _ *ContextIR) string {
	switch {
	case strings.HasPrefix(assetID, "video"):
		return "video"
	case strings.HasPrefix(assetID, "audio"):
		return "audio"
	default:
		return "image"
	}
}

// renderBasePrompt 三字段渲染（t2v / i2v / flf2v / l2va）。
func renderBasePrompt(ir *ContextIR, inv map[string]string) string {
	var instruction string
	var pics []string
	for _, a := range conditionAssets(ir) {
		if a.MediaType == "image" {
			if label, ok := inv[a.AssetID]; ok {
				pics = append(pics, label)
			}
		}
	}
	// **首/尾帧优先按角色认，认不出才退回顺序。**
	//
	// IR 里 first_frame / last_frame 是明确声明的角色，而 pics 的顺序来自
	// asset_bindings 的书写次序 —— 两者只有在"恰好只有两张图、且按首尾顺序
	// 写"时才一致。夹一张风格参考图进去，或者把尾帧写在前面，时间对齐就
	// 落到错的 <Picture N> 上：视频从结尾往后长，而且不报错。
	// 这正是 convert.go 里警告过的那类错。
	firstLabel, lastLabel := frameLabelsByRole(ir, inv)
	if firstLabel == "" && len(pics) > 0 {
		firstLabel = pics[0]
	}
	if lastLabel == "" && len(pics) > 0 {
		lastLabel = pics[len(pics)-1]
	}
	lastShot := len(ir.Timeline)
	dur := ir.Task.DurationSeconds

	// 关键帧和目标视频的时间对齐关系。**这段必须由代码生成** ——
	// 时间戳是算出来的，让 LLM 写它必然出现和 duration 对不上的值。
	if len(pics) > 0 {
		switch strings.ToLower(ir.Task.Type) {
		case string(TaskI2V):
			instruction = fmt.Sprintf(
				"For the target video, at 0.00 seconds into the target video, %s (from [Shot 1]) is fully referenced.",
				firstLabel)
		case string(TaskFLF2V):
			// **必须有两张图才能声明首尾对齐。**
			//
			// 只有一张时 pics[0] 和 pics[len-1] 是同一个标签，渲染出来是
			// 「这张图既是首帧又是尾帧」—— H3 会试图让画面回到起点。
			// 退回成 i2v 的说法：它确实只有一张图。
			if len(pics) < 2 {
				instruction = fmt.Sprintf(
					"For the target video, at 0.00 seconds into the target video, %s (from [Shot 1]) is fully referenced.",
					firstLabel)
				break
			}
			instruction = fmt.Sprintf(
				"How the reference pictures align with the target video — "+
					"%s (from [Shot 1]) aligns with the 0.00-second mark of the target video; "+
					"%s (from [Shot %d]) aligns with the %.2f-second mark of the target video.",
				firstLabel, lastLabel, lastShot, dur)
		case string(TaskL2VA):
			// 只给尾帧：只声明尾部对齐。**不能顺手补一个首帧对齐** ——
			// 那正是"把尾帧当首帧"的错法。
			instruction = fmt.Sprintf(
				"How the reference pictures align with the target video — "+
					"%s (from [Shot %d]) aligns with the %.2f-second mark of the target video.",
				lastLabel, lastShot, dur)
		}
	}

	opening := globalBaseline(ir)
	var shots []string
	for i, s := range ir.Timeline {
		shots = append(shots, shotText(s, i+1, nil))
	}
	focus := ir.CreativeFocus
	focusText := "Primary visual focus: " + focus.Objective
	if reqs := dedupe(focus.PresentationRequirements); len(reqs) > 0 {
		focusText += ". Presentation requirements: " + strings.Join(reqs, "; ")
	}

	desc := joinNonEmpty(". ", focusText, constraintText(ir), opening, strings.Join(shots, " "))
	soundscape, music := soundSections(ir)

	core := strings.Join([]string{
		"integrated_multimodal_description: " + desc,
		"overall_soundscape: " + soundscape,
		"non_diegetic_music: " + music,
	}, "\n\n")
	if instruction != "" {
		return instruction + "\n\n" + core + "\n"
	}
	return core + "\n"
}

// renderRefPrompt 六段式渲染（r2va）。
//
// 段落顺序固定：subject_definitions → summary → retention_analysis →
// detailed_description → overall_soundscape → non_diegetic_music。
// **retention_analysis 是"编辑"的表达方式** —— 四个保留标记从"原样保留"
// 到"只借个风格"覆盖全谱，所以 H3 不需要单独的 v2v。
func renderRefPrompt(ir *ContextIR, inv map[string]string) string {
	subjInv := BuildSubjectInventory(ir)
	shotIndex := buildShotIndex(ir)

	// —— subject_definitions ——
	//
	// 每个主体一行，并**把"这个素材控制它的哪些维度"写成人话**。
	// 这是防止参考素材越权的关键：只写 "(source: <Picture 1>)" 的话，
	// H3 不知道那张图是给外观、给构图、还是给运动的 —— 而图片本来就
	// 不该提供运动。
	bindingsBySubject := map[string][]IRAssetBinding{}
	for _, s := range ir.Subjects {
		for _, b := range ir.AssetBindings {
			for _, src := range s.SourceAssetIDs {
				if b.AssetID == src {
					bindingsBySubject[s.SubjectID] = append(bindingsBySubject[s.SubjectID], b)
				}
			}
		}
	}
	kfBySubject := map[string][]IRKeyframeRole{}
	for _, k := range ir.KeyframeRoles {
		for _, sid := range k.SubjectRefs {
			kfBySubject[sid] = append(kfBySubject[sid], k)
		}
	}

	var defs []string
	for _, s := range ir.Subjects {
		label, ok := subjInv[s.SubjectID]
		if !ok {
			continue
		}
		clauses := subjectSourceClauses(s, bindingsBySubject[s.SubjectID], kfBySubject[s.SubjectID], inv)
		// Name 和 Description 拼成 "X is <名>, <描述>"。**任一为空时不能
		// 产出 "X is , 描述" 这种病句** —— 它会原样进提示词，而 H3 读到的
		// 是一句破碎的定义。
		var head []string
		if n := trimPeriod(s.Name); n != "" {
			head = append(head, n)
		}
		if d := trimPeriod(s.Description); d != "" {
			head = append(head, d)
		}
		line := label
		if len(head) > 0 {
			line += " is " + strings.Join(head, ", ")
		}
		if len(clauses) > 0 {
			line += "; " + strings.Join(clauses, "; ")
		}
		defs = append(defs, line+".")
	}
	for _, r := range ir.ReferenceRelationships {
		label, ok := inv[r.AssetID]
		if !ok || r.Definition == "" {
			continue
		}
		defs = append(defs, label+": "+r.Definition)
	}

	// —— summary ——
	//
	// 结构照代码：`[任务类型] 编辑开场 主体焦点。风格。Audio generation: x.`
	summary := buildSummary(ir, inv, subjInv)

	// —— retention_analysis ——
	// 每个参考标签一行，保持 subject_definitions 里确立的含义。
	var retention []string
	for _, s := range ir.Subjects {
		label, ok := subjInv[s.SubjectID]
		if !ok || s.RetentionMode == "" {
			continue
		}
		// **镜头编号按 timeline 位置算，不能拿 shot_id 裁个零就用。**
		//
		// 提示词里其他地方的 [Shot N] 都是 timeline 的下标（见 shotText），
		// 两者只有在模型恰好输出 "01"/"02"/"03" 这种补零连续 ID 时才对得上。
		// 模型写 "shot_1" 或 "S01" 的话，这里会渲染出 [Shot shot_1] ——
		// 一条 H3 读不懂的引用，而且不报错。
		where := ""
		if bracketed := shotCitations(s.AppearanceShotIDs, shotIndex); len(bracketed) > 0 {
			where = " (appears in " + strings.Join(bracketed, ", ") + ")"
		}
		retention = append(retention,
			fmt.Sprintf("%s%s: %s - %s.", label, where, s.RetentionMode,
				trimPeriod(s.RetentionDescription)))
	}
	for _, r := range ir.ReferenceRelationships {
		label, ok := inv[r.AssetID]
		if !ok || r.RetentionMode == "" {
			continue
		}
		retention = append(retention,
			fmt.Sprintf("%s: %s - %s.", label, r.RetentionMode,
				trimPeriod(r.RetentionDescription)))
	}

	// —— detailed_description ——
	//
	// 顺序照代码：全局约束 → 风格开场 → 各镜头，**每项一行**。
	var details []string
	if gc := constraintText(ir); gc != "" {
		details = append(details, gc)
	}
	if so := styleOpening(ir); so != "" {
		details = append(details, so)
	}
	for i, sh := range ir.Timeline {
		details = append(details, shotText(sh, i+1, subjInv))
	}

	soundscape, music := soundSections(ir)

	// **段落名后是换行，不是空格**；各条目之间也是换行。
	// 照 XINGSHEN2 的 `"subject_definitions:\n" + "\n".join(subjects)`。
	// 我一开始写成了空格 + 拼接 —— 成品审计按 `(?m)^段落名:` 匹配，
	// 空格版能过审计，但条目全挤在一行，H3 读起来是另一回事。
	return strings.Join([]string{
		"subject_definitions:\n" + strings.Join(defs, "\n"),
		"summary:\n" + summary,
		"retention_analysis:\n" + strings.Join(retention, "\n"),
		"detailed_description:\n" + strings.Join(details, "\n"),
		"overall_soundscape:\n" + soundscape,
		"non_diegetic_music:\n" + music,
	}, "\n\n") + "\n"
}

// BuildSubjectInventory 给主体分配 `<Subject N>` 标签。
//
// 和素材标签同理：编号必须确定性，否则同一个人在不同段里可能叫不同的名字。
func BuildSubjectInventory(ir *ContextIR) map[string]string {
	inv := map[string]string{}
	n := 0
	for _, s := range ir.Subjects {
		if s.SubjectID == "" {
			continue
		}
		n++
		inv[s.SubjectID] = fmt.Sprintf("<Subject %d>", n)
	}
	return inv
}

// shotText 渲染一个镜头。
//
// 第一个镜头不带时间戳（它从 0 开始，写出来是噪音）；其余带上起始时刻。
func shotText(s IRShot, num int, subjInv map[string]string) string {
	var opening string
	if len(subjInv) > 0 {
		labels := labelsOf(s.SubjectRefs, subjInv)
		if len(labels) == 1 {
			opening = labels[0] + " is visible. "
		} else if len(labels) > 1 {
			opening = strings.Join(labels, ", ") + " are visible. "
		}
	}
	pieces := []string{opening + s.Event}
	for _, kv := range []struct{ k, v string }{
		{"camera", s.Camera}, {"lighting", s.Lighting}, {"transition", s.Transition},
	} {
		if kv.v != "" {
			pieces = append(pieces, kv.k+": "+kv.v)
		}
	}
	pieces = append(pieces, "observable end state: "+s.ObservableEndState)

	prefix := fmt.Sprintf("[Shot %d]", num)
	if num != 1 {
		prefix += " At " + formatTimestamp(s.StartSeconds) + ","
	}
	return prefix + " " + strings.Join(pieces, "; ")
}

// formatTimestamp H3 的时间戳格式 mm:ss.mmm。
func formatTimestamp(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	total := int(sec*1000 + 0.5)
	ms := total % 1000
	totalSec := total / 1000
	return fmt.Sprintf("%02d:%02d.%03d", totalSec/60, totalSec%60, ms)
}

// globalBaseline 全局基准：整片共享的摄影/光线/材质/表演/连续性。
func globalBaseline(ir *ContextIR) string {
	g := ir.GenerationDescription
	var parts []string
	for _, kv := range []struct{ k, v string }{
		{"cinematography", g.Cinematography},
		{"lighting", g.Lighting},
		{"materials", g.Materials},
		{"performance", g.Performance},
		{"continuity", g.Continuity},
	} {
		if strings.TrimSpace(kv.v) != "" {
			parts = append(parts, kv.k+": "+kv.v)
		}
	}
	return strings.Join(parts, "; ")
}

// constraintText 一段权威的全局约束。
//
// **只渲染可执行的边界**：策略来源、模式和事件登记留在 IR 里供审计,
// 不进提示词 —— 策略事件已经由它引用的那些镜头表达过了，再展开一遍
// 是重复。
func constraintText(ir *ContextIR) string {
	c := ir.Constraints
	var parts []string
	if v := dedupe(c.Preserve); len(v) > 0 {
		parts = append(parts, "Must preserve: "+strings.Join(v, ", "))
	}
	if v := dedupe(c.AllowChange); len(v) > 0 {
		parts = append(parts, "May change only as requested: "+strings.Join(v, ", "))
	}
	if v := dedupe(c.Prohibit); len(v) > 0 {
		parts = append(parts, "Must not introduce: "+strings.Join(v, ", "))
	}
	if len(parts) == 0 {
		return ""
	}
	return "Global constraints: " + strings.Join(parts, "; ")
}

// soundSections 渲染两个声音段。
//
// **关掉声音时渲染成 "N/A" 而不是留空** —— 空段落会让 H3 以为这一段
// 没写完，而 N/A 是明确的"本片无此项"。
func soundSections(ir *ContextIR) (string, string) {
	if !ir.Task.GenerateAudio {
		return "N/A", "N/A"
	}
	a := ir.AudioPlan
	var parts []string
	for _, kv := range []struct {
		k string
		v string
	}{
		{"voice", a.Voice},
		{"sound_effects", a.SoundEffects},
		{"ambient_sound", a.AmbientSound},
		{"sync_rules", strings.Join(a.SyncRules, "; ")},
	} {
		if isAbsent(kv.v) {
			continue
		}
		parts = append(parts, kv.k+": "+kv.v)
	}
	soundscape := strings.Join(parts, "; ")
	if soundscape == "" {
		soundscape = "N/A"
	}
	music := strings.TrimSpace(a.Music)
	if isAbsentMusic(music) {
		music = "N/A"
	}
	return soundscape, music
}

// isAbsent 模型表达"整项没有"的写法 —— **只认完全匹配**。
//
// 它可能写 "none"、"not requested"、"[]"，这些不该被当成内容渲染进去：
// 提示词里出现 `voice: none` 会被 H3 当成一句描述。
//
// **不能加前缀启发**（如"以 no 开头就算没有"）：
// "no dialogue, only footsteps and room tone" 是一条**实实在在的声音指令**,
// 丢掉它等于把用户要的环境音也删了。XINGSHEN2 的原文里前缀判断
// 只用在 music 一个字段上，见 isAbsentMusic。
func isAbsent(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "none", "no", "false", "not requested", "[]", "n/a":
		return true
	}
	return false
}

// isAbsentMusic 配乐字段专用，额外认两种前缀。
//
// 照 XINGSHEN2 原文：`music.lower().startswith(("none;", "no "))`。
// 配乐和其余几项不同 —— 模型常写 "none; the scene stays quiet" 来表达
// "不要配乐"，那确实是"没有"，而不是一条配乐指令。
func isAbsentMusic(v string) bool {
	if isAbsent(v) {
		return true
	}
	t := strings.ToLower(strings.TrimSpace(v))
	return strings.HasPrefix(t, "none;") || strings.HasPrefix(t, "no ")
}

func labelsOf(ids []string, inv map[string]string) []string {
	var out []string
	for _, id := range ids {
		if l, ok := inv[id]; ok {
			out = append(out, l)
		}
	}
	return out
}

// dedupe 去重并保持顺序。模型爱在 preserve/prohibit 里重复同一条。
func dedupe(items []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, it := range items {
		t := strings.TrimSpace(it)
		if t == "" {
			continue
		}
		key := strings.ToLower(t)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, t)
	}
	return out
}

func joinNonEmpty(sep string, parts ...string) string {
	var out []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}

// 绑定角色的两类。**这个区分是参考素材不越权的核心。**
//
//	外观类：这个素材决定"长什么样"
//	结构类：这个素材决定"怎么动、怎么拍、什么风格"
//
// 一个素材只挂结构类角色时，必须明说「它不是外观来源」—— 否则 H3 会
// 连它的外观一起抄过来，而用户只想要那个运动。
var (
	appearanceRoles = map[string]bool{"identity": true, "outfit": true, "product": true, "scene": true}
	structuralRoles = map[string]bool{"motion": true, "camera": true, "rhythm": true, "style": true}
)

var appearanceRoleLabel = map[string]string{
	"identity": "identity and facial appearance",
	"outfit":   "outfit",
	"product":  "product appearance",
	"scene":    "environment appearance",
}

// subjectSourceClauses 把主体的素材来源渲染成说清楚控制维度的从句。
func subjectSourceClauses(s IRSubject, bindings []IRAssetBinding, kfs []IRKeyframeRole, inv map[string]string) []string {
	// 按素材聚合角色：同一张图可能既给身份又给服装。
	byAsset := map[string]*assetControl{}
	var assetOrder []string
	for _, b := range bindings {
		if _, ok := inv[b.AssetID]; !ok {
			continue
		}
		c := byAsset[b.AssetID]
		if c == nil {
			c = &assetControl{roles: map[string]bool{}}
			byAsset[b.AssetID] = c
			assetOrder = append(assetOrder, b.AssetID)
		}
		if b.Role != "" && !c.roles[b.Role] {
			c.roles[b.Role] = true
			c.order = append(c.order, b.Role)
		}
	}

	var clauses []string
	for _, assetID := range assetOrder {
		label := inv[assetID]
		c := byAsset[assetID]
		var appear, structural []string
		for _, r := range c.order {
			switch {
			case appearanceRoles[r]:
				appear = append(appear, r)
			case structuralRoles[r]:
				structural = append(structural, r)
			}
		}
		switch {
		case len(appear) == 0 && len(structural) > 0:
			// **只有结构类角色：必须明说它不是外观来源。**
			// 不说的话 H3 会把参考视频里那个人的长相也带进来。
			clauses = append(clauses, fmt.Sprintf(
				"its %s follows %s; %s is not an appearance source",
				strings.Join(structural, ", "), label, label))
		case len(appear) > 0:
			// 按固定顺序渲染，让同一组角色每次产出同样的句子。
			var scope []string
			for _, r := range []string{"identity", "outfit", "product", "scene"} {
				if c.roles[r] {
					scope = append(scope, appearanceRoleLabel[r])
				}
			}
			cl := fmt.Sprintf("%s controls its %s", label, strings.Join(scope, ", "))
			if len(structural) > 0 {
				cl += fmt.Sprintf(" and its %s also follows that reference", strings.Join(structural, ", "))
			}
			clauses = append(clauses, cl)
		default:
			clauses = append(clauses, "its scoped reference guidance comes from "+label)
		}
	}

	// 关键帧角色：每种角色说清楚它锚定什么、**不提供**什么。
	for _, k := range kfs {
		label, ok := inv[k.AssetID]
		if !ok {
			continue
		}
		switch k.Role {
		case "action_keyframe":
			clauses = append(clauses, label+" anchors its exact action pose, not motion or edit rhythm")
		case "product_detail":
			clauses = append(clauses, label+" anchors its exact product surface and close-detail appearance")
		case "appearance_source":
			if !hasAppearanceBinding(byAsset[k.AssetID]) {
				detail := ""
				if len(k.Controls) > 0 {
					n := len(k.Controls)
					if n > 6 {
						n = 6
					}
					detail = " (" + strings.Join(k.Controls[:n], ", ") + ")"
				}
				clauses = append(clauses, label+" controls its appearance"+detail)
			}
		case "scene_anchor":
			if c := byAsset[k.AssetID]; c == nil || !c.roles["scene"] {
				clauses = append(clauses, label+" anchors its environment appearance without supplying motion")
			}
		case "composition_anchor":
			clauses = append(clauses, label+" anchors its composition without supplying motion or editing")
		case "style_reference":
			if c := byAsset[k.AssetID]; c == nil || !c.roles["style"] {
				clauses = append(clauses, label+" supplies only its authorized visual style")
			}
		}
	}
	return clauses
}

// assetControl 一个素材在某个主体身上聚合起来的角色集合。
// 同一张图可能既给身份又给服装。
type assetControl struct {
	roles map[string]bool
	order []string
}

func hasAppearanceBinding(c *assetControl) bool {
	if c == nil {
		return false
	}
	for r := range c.roles {
		if appearanceRoles[r] && r != "scene" {
			return true
		}
	}
	return false
}

// trimPeriod 去掉句尾的点。渲染时我们自己加，模型写的那个会变成两个。
func trimPeriod(s string) string {
	return strings.TrimSuffix(strings.TrimSpace(s), ".")
}

// buildSummary 六段式的 summary。
//
// 结构照 XINGSHEN2 的代码：
//
//	[任务类型] 编辑开场 主体焦点。风格。Audio generation: x.
//
// **编辑开场**（`The target video is an edited version of <Video 1>.`）
// 是视频编辑场景的声明 —— H3 不需要单独的 v2v，靠这句加上 retention 标记
// 就表达了"这是对某个源视频的编辑"。
func buildSummary(ir *ContextIR, inv, subjInv map[string]string) string {
	var b strings.Builder

	// 任务类型前缀。
	if types := summaryTaskTypes(ir); len(types) > 0 {
		b.WriteString("[" + strings.Join(types, " + ") + "] ")
	}

	// 源视频编辑的开场声明。
	for _, r := range ir.ReferenceRelationships {
		if r.Relationship == "source_video_edit" {
			if label, ok := inv[r.AssetID]; ok {
				b.WriteString("The target video is an edited version of " + label + ". ")
			}
			break
		}
	}

	// 创作焦点。多主体并列时说"共享焦点"，**不要挑一个当主角** ——
	// 用户并列呈现几个人/产品时，降级任何一个都是改需求。
	// Objective 为空时整句退化成"没有焦点声明"，而不是 "…creative objective: ."
	objective := trimPeriod(ir.CreativeFocus.Objective)
	pr := ir.SemanticPlan.SubjectPriority
	var joint []string
	seen := map[string]bool{}
	for _, sid := range pr.SubjectIDs {
		if l, ok := subjInv[sid]; ok && !seen[l] {
			seen[l] = true
			joint = append(joint, l)
		}
	}
	if objective != "" {
		switch {
		case pr.Mode == "co_equal" && len(joint) > 1:
			b.WriteString(strings.Join(joint, ", ") + " share the creative focus: " + objective + ".")
		default:
			if l, ok := subjInv[ir.CreativeFocus.PrimarySubjectID]; ok && l != "" {
				b.WriteString(l + " is the primary creative focus: " + objective + ".")
			} else {
				b.WriteString("Primary creative objective: " + objective + ".")
			}
		}
	}

	b.WriteString(fmt.Sprintf(" Audio generation: %t.", ir.Task.GenerateAudio))
	return b.String()
}

// summaryTaskTypes summary 前缀里的任务类型标签。
//
// 官方示例是 `[reference generation + audio reference]` —— 它说明这次
// 用到了哪几类参考。从 reference_relationships 推，而不是让模型自己写:
// 写错了 H3 会按另一种玩法理解。
func summaryTaskTypes(ir *ContextIR) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, r := range ir.ReferenceRelationships {
		switch r.Relationship {
		case "source_video_edit":
			add("video editing")
		case "video_continuation":
			add("video continuation")
		case "keyframe_completion":
			add("keyframe completion")
		case "audio_reuse", "audio_reference":
			add("audio reference")
		case "reference_generation":
			add("reference generation")
		}
	}
	return out
}

// styleOpening detailed_description 的开场：风格 + 摄影 + 光线。
func styleOpening(ir *ContextIR) string {
	g := ir.GenerationDescription
	var parts []string
	for _, v := range []string{g.Cinematography, g.Lighting} {
		if t := trimPeriod(v); t != "" {
			parts = append(parts, t)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ". ") + "."
}

// buildShotIndex shot_id → 它在 timeline 里的位置（从 1 起）。
//
// 提示词里所有 [Shot N] 都必须用这个位置，而不是 shot_id 本身 ——
// 模型给的 ID 可能是 "01"、"shot_1"、"S01"，甚至顺序和 timeline 不一致。
func buildShotIndex(ir *ContextIR) map[string]int {
	idx := map[string]int{}
	for i, s := range ir.Timeline {
		if s.ShotID != "" {
			idx[s.ShotID] = i + 1
		}
	}
	return idx
}

// shotCitations 把 shot_id 列表渲染成 `[Shot 1], [Shot 3]`。
//
// **认不出的 ID 直接丢掉**，不产出 `[Shot shot_1]` 这种 H3 读不懂的引用。
// 丢掉会让 retention 行少一个"出现在哪"的说明（信息少了一点），
// 而产出错引用是给 H3 一条错指令 —— 后者更糟。校验器那边会把
// 认不出的 ID 报出来。
func shotCitations(shotIDs []string, idx map[string]int) []string {
	var out []string
	for _, id := range shotIDs {
		if n, ok := idx[id]; ok {
			out = append(out, fmt.Sprintf("[Shot %d]", n))
		}
	}
	return out
}

// frameLabelsByRole 按 first_frame / last_frame 角色取关键帧标签。
//
// **角色是明确声明，顺序是书面次序** —— 前者才是语义。两者在"只有两张图
// 且按首尾顺序写"时恰好一致，但夹一张风格参考图、或把尾帧写在前面，
// 按顺序取就会把尾帧当成首帧。
//
// 认不出角色时返回空串，调用方退回按顺序取 —— 保持老行为，不因为
// 模型没写角色就整条渲染不出来。
func frameLabelsByRole(ir *ContextIR, inv map[string]string) (first, last string) {
	pick := func(assetID, role string) {
		label, ok := inv[assetID]
		if !ok {
			return
		}
		switch role {
		case "first_frame":
			if first == "" {
				first = label
			}
		case "last_frame":
			if last == "" {
				last = label
			}
		}
	}
	// **请求侧的角色优先,模型写的只是回落。**
	//
	// ir.Assets 是我们填的(applyAuthoritativeFacts),角色来自客户端把图放进
	// 了哪个字段 —— 那是事实,不是创作判断。ir.AssetBindings / ir.KeyframeRoles
	// 是模型写的。
	//
	// 只读模型那两份的后果很具体:flf2v 两张帧,模型把 first_frame 标在
	// image_2、last_frame 标在 image_1,渲染出来就是 "<Picture 2> … aligns
	// with the 0.00-second mark / <Picture 1> … aligns with the N-second
	// mark" —— **视频倒着长**,而那份 IR 自洽,校验一条都拦不下。
	//
	// 素材顺序那一半此前已经修过(BuildReferenceInventory 按 ir.Assets 编号),
	// 角色这一半漏了:清单填进去了,却没有任何地方读它。
	for _, a := range ir.Assets {
		pick(a.AssetID, a.Role)
	}
	if first != "" || last != "" {
		return first, last
	}
	// 请求侧没给角色(直接构造 IR 的调用方、老夹具)时才看模型那份。
	for _, b := range ir.AssetBindings {
		pick(b.AssetID, b.Role)
	}
	for _, k := range ir.KeyframeRoles {
		pick(k.AssetID, k.Role)
	}
	return first, last
}
