package hilo

import (
	"strings"
	"testing"
)

func auditCodes(r *ValidationReport) map[string]bool {
	m := map[string]bool{}
	for _, p := range r.Problems {
		m[p.Code] = true
	}
	return m
}

// 现有夹具渲染出来的成品必须干净通过 —— 审计若对合法产出误报,
// 每一次编译都会白跑一轮重修再回落。
func TestAuditAcceptsRenderedFixtures(t *testing.T) {
	for name, ir := range map[string]*ContextIR{"sitcom": sitcomIR(), "valid": validIR()} {
		prompt, err := RenderPrompt(ir)
		if err != nil {
			t.Fatalf("%s 渲染失败：%v", name, err)
		}
		if rep := AuditPrompt(ir, prompt); !rep.Passed() {
			t.Errorf("%s 的合法成品被审计判错：%s", name, rep.Reason())
		}
	}
}

// ── 改写语言:对白保留原语言 ────────────────────────────────────────

// **英文正文里混进中文散文要被拦下。**
//
// 这在 IR 上完全看不出来 —— 每个字段都填了、都合法。只有看成品文本
// 才知道改写语言的约定没被遵守。
func TestAuditCatchesChineseProse(t *testing.T) {
	ir := sitcomIR()
	prompt, err := RenderPrompt(ir)
	if err != nil {
		t.Fatal(err)
	}
	tainted := prompt + "\n镜头缓缓推近，她转过身来。"
	if !auditCodes(AuditPrompt(ir, tainted))["PROMPT_REWRITE_LANGUAGE_VIOLATION"] {
		t.Error("正文里的中文散文没被拦下")
	}
}

// **<d> 标签里的台词保留源语言,不算违规。**
//
// 这是协议的一部分,也是这条规则存在的理由:翻译台词是这一步最容易犯、
// 最难发现的错 —— 成品读起来完全正常,只是人物说的不再是用户写的那句话。
func TestAuditAllowsSourceLanguageInsideDialogueTags(t *testing.T) {
	ir := sitcomIR()
	prompt, err := RenderPrompt(ir)
	if err != nil {
		t.Fatal(err)
	}
	for _, tagged := range []string{
		prompt + "\n<d>[Chinese]等我一下。</d>",
		prompt + "\n<l>[Chinese]月亮代表我的心</l>",
		prompt + "\n<d>等我一下。</d>", // 不带语言标注也算
	} {
		if rep := AuditPrompt(ir, tagged); !rep.Passed() {
			t.Errorf("逐字标签里的源语言被误判：%s", rep.Reason())
		}
	}
}

// 标签外的中文仍然要拦 —— 挖掉标签只是审计探针,不能变成整段放行。
func TestAuditStillCatchesChineseOutsideDialogueTags(t *testing.T) {
	ir := sitcomIR()
	prompt, _ := RenderPrompt(ir)
	tainted := prompt + "\n<d>[Chinese]等我一下。</d>\n她笑了一下。"
	if !auditCodes(AuditPrompt(ir, tainted))["PROMPT_REWRITE_LANGUAGE_VIOLATION"] {
		t.Error("逐字标签之外的中文没被拦下")
	}
}

// 改写语言不是英文时这条规则不适用。
func TestAuditSkipsLanguageRuleForNonEnglish(t *testing.T) {
	ir := sitcomIR()
	prompt, _ := RenderPrompt(ir)
	ir.Protocol.RewriteLanguage = "Chinese"
	if auditCodes(AuditPrompt(ir, prompt+"\n她转过身来。"))["PROMPT_REWRITE_LANGUAGE_VIOLATION"] {
		t.Error("改写语言不是英文时不该套用英文规则")
	}
}

// ── 素材 ID 投影 ───────────────────────────────────────────────────

// 正文里的内部素材 ID 必须被换成官方标签。
//
// 模型在 event / retention_description 里写 `image_1` 是常态 —— 它在 IR
// 里就是这么指代素材的。H3 只认 `<Picture 1>`,原样交出去它读不懂那是
// 什么,而且不报错。
func TestProjectReferenceLabelsReplacesInternalIDs(t *testing.T) {
	inv := map[string]string{"image_1": "<Picture 1>", "video_1": "<Video 1>"}
	got := ProjectReferenceLabels("keep the look of image_1 and the motion of video_1", inv)
	want := "keep the look of <Picture 1> and the motion of <Video 1>"
	if got != want {
		t.Errorf("投影结果不对\n实得: %s\n应为: %s", got, want)
	}
}

// **从长到短替换。** 短的先替会让 image_1 啃掉 image_10 的前半截,
// 剩下一个 `<Picture 1>0`。
func TestProjectReferenceLabelsLongestFirst(t *testing.T) {
	inv := map[string]string{"image_1": "<Picture 1>", "image_10": "<Picture 10>"}
	got := ProjectReferenceLabels("image_10 and image_1", inv)
	if got != "<Picture 10> and <Picture 1>" {
		t.Errorf("长 ID 被短 ID 啃掉了：%s", got)
	}
}

// **边界要判。** 不判的话 `my_image_12x` 里会切出一个 image_12,
// 投影时把它换掉,活生生改坏一个本来正常的词。
func TestProjectReferenceLabelsRespectsWordBoundaries(t *testing.T) {
	inv := map[string]string{"image_1": "<Picture 1>"}
	for _, in := range []string{"my_image_1", "image_1x", "image_12"} {
		if got := ProjectReferenceLabels(in, inv); got != in {
			t.Errorf("%q 不该被改动,实得 %q", in, got)
		}
	}
}

// 投影之后还剩的内部 ID,说明它指的素材根本不在本次清单里 ——
// 模型引用了一个没传的素材。
func TestAuditCatchesUnprojectedAssetID(t *testing.T) {
	ir := sitcomIR()
	prompt, _ := RenderPrompt(ir)
	if !auditCodes(AuditPrompt(ir, prompt+"\nmatch the lighting of image_9."))["RAW_ASSET_ID_LEAK"] {
		t.Error("成品里残留的内部素材 ID 没被拦下")
	}
}

// ── 标签 ───────────────────────────────────────────────────────────

// 引用了本次没有的素材标签 —— H3 会对着一个不存在的标签编内容。
func TestAuditCatchesUnexpectedReferenceTag(t *testing.T) {
	ir := sitcomIR()
	prompt, _ := RenderPrompt(ir)
	if !auditCodes(AuditPrompt(ir, prompt+"\nalso match <Picture 7>."))["REFERENCE_TAG_UNEXPECTED"] {
		t.Error("引用不存在的素材标签没被拦下")
	}
}

// 参考生视频下有素材从未被引用 —— 全部创作意图都在参考素材里,
// 漏用一个等于把它扔了。
func TestAuditCatchesMissingReferenceTag(t *testing.T) {
	ir := sitcomIR()
	prompt, _ := RenderPrompt(ir)
	stripped := strings.ReplaceAll(prompt, "<Picture 2>", "the dog")
	if !auditCodes(AuditPrompt(ir, stripped))["REFERENCE_TAG_MISSING"] {
		t.Error("有素材从未被引用却没被拦下")
	}
}

// 自造的尖括号标签要拦:H3 把尖括号当协议读,自造一个 <camera> 它会
// 当成指令去解析,而不是当成文字 —— 成品看起来没问题,行为却变了。
func TestAuditCatchesNonOfficialAngleTag(t *testing.T) {
	ir := sitcomIR()
	prompt, _ := RenderPrompt(ir)
	if !auditCodes(AuditPrompt(ir, prompt+"\n<camera>push in</camera>"))["PROMPT_NONOFFICIAL_ANGLE_TAG"] {
		t.Error("非官方尖括号标签没被拦下")
	}
}

// 官方词汇表里的标签不算违规。
func TestAuditAllowsOfficialAngleTags(t *testing.T) {
	ir := sitcomIR()
	prompt, _ := RenderPrompt(ir)
	ok := prompt + "\n<scenetrans>to the street</scenetrans>\n<cutoff>abrupt</cutoff>"
	if auditCodes(AuditPrompt(ir, ok))["PROMPT_NONOFFICIAL_ANGLE_TAG"] {
		t.Error("官方标签被误判成非官方")
	}
}

// ── 时间戳 ─────────────────────────────────────────────────────────

func TestAuditCatchesTimestampBeyondDuration(t *testing.T) {
	ir := sitcomIR() // 7 秒
	prompt, _ := RenderPrompt(ir)
	if !auditCodes(AuditPrompt(ir, prompt+"\nat 00:12.000 she turns."))["PROMPT_TIMESTAMP_RANGE"] {
		t.Error("超出时长的时间戳没被拦下")
	}
}

// ── 段落 ───────────────────────────────────────────────────────────

func TestAuditCatchesMissingSection(t *testing.T) {
	ir := sitcomIR()
	prompt, _ := RenderPrompt(ir)
	dropped := strings.ReplaceAll(prompt, "\nretention_analysis:", "\nretention_notes:")
	if !auditCodes(AuditPrompt(ir, dropped))["PROMPT_SECTION_MISSING"] {
		t.Error("缺段没被拦下")
	}
}

// 三段式下混进六段式的段落要拦,但 **overall_soundscape /
// non_diegetic_music 两种玩法共有**,不能被当成越界。
func TestAuditSharedSectionsAreNotForbiddenInBaseMode(t *testing.T) {
	ir := validIR()
	ir.Task.Type = string(TaskI2V)
	ir.AssetBindings = []IRAssetBinding{{AssetID: "image_1", Role: "first_frame"}}
	prompt, err := RenderPrompt(ir)
	if err != nil {
		t.Fatalf("三段式渲染失败：%v", err)
	}
	if c := auditCodes(AuditPrompt(ir, prompt)); c["PROMPT_SECTION_UNEXPECTED"] {
		t.Error("两种玩法共有的声音段被误判成越界")
	}
}

// [Shot 1] 是必须的:H3 按它认镜头边界,没有就整段当一个镜头处理。
func TestAuditRequiresShotOne(t *testing.T) {
	ir := sitcomIR()
	prompt, _ := RenderPrompt(ir)
	if !auditCodes(AuditPrompt(ir, strings.ReplaceAll(prompt, "[Shot 1]", "[Scene 1]")))["PROMPT_SHOT_ONE_MISSING"] {
		t.Error("缺 [Shot 1] 没被拦下")
	}
}

// ── 主体覆盖 ───────────────────────────────────────────────────────

// 六段式下每个主体都必须在定义、保留分析、详细描述里各露一次面。
// 少一处,H3 对那个主体的理解就是残缺的 —— 定义里没有它就没有身份锚点。
func TestAuditCatchesSubjectMissingFromRetention(t *testing.T) {
	ir := sitcomIR()
	prompt, _ := RenderPrompt(ir)
	secs := splitSections(prompt, refSections)
	broken := strings.Replace(prompt,
		secs["retention_analysis"],
		strings.ReplaceAll(secs["retention_analysis"], "<Subject 2>", "the dog"), 1)
	if !auditCodes(AuditPrompt(ir, broken))["SUBJECT_RETENTION_MISSING"] {
		t.Error("主体从保留分析里消失却没被拦下")
	}
}

// ── 段落切分 ───────────────────────────────────────────────────────

// 段落标题必须锚在行首:正文里提一句 "summary:" 不是一个段落开始,
// 当成段落会让顺序判定乱掉。
func TestSectionIndexAnchorsToLineStart(t *testing.T) {
	if sectionIndex("he wrote summary: in his notes", "summary") >= 0 {
		t.Error("行中出现的段落名被当成了段落开始")
	}
	if sectionIndex("summary: ok", "summary") != 0 {
		t.Error("行首的段落名没认出来")
	}
	if sectionIndex("a\nsummary: ok", "summary") != 2 {
		t.Error("换行后的段落名没认出来")
	}
}

// **审计要真的挡住渲染。** 不过就不能把成品交出去。
//
// 上面那些用例都是直接调 AuditPrompt,验的是规则本身;这条验的是接线 ——
// 规则写对了但没接进 RenderPrompt,等于一条都没生效。
func TestRenderPromptRefusesWhenAuditFails(t *testing.T) {
	ir := sitcomIR()
	// 让模型在自由文本里留了一句中文散文(没有用逐字标签包起来)。
	ir.Timeline[0].Event = "她转过身来，看向窗外"

	prompt, err := RenderPrompt(ir)
	if err == nil {
		t.Fatalf("审计不过却照样渲染出了成品：\n%s", prompt)
	}
	if !strings.Contains(err.Error(), "PROMPT_REWRITE_LANGUAGE_VIOLATION") {
		t.Errorf("错误里应带上具名问题(重修轮靠它)，实得：%v", err)
	}
}

// 同一条链路上,IR 结构错误仍然在审计之前被拦下 —— 两层查的不是一回事。
func TestRenderPromptStillValidatesIRFirst(t *testing.T) {
	ir := sitcomIR()
	ir.Timeline[len(ir.Timeline)-1].EndSeconds = 99 // 总时长对不上
	_, err := RenderPrompt(ir)
	if err == nil {
		t.Fatal("结构错误的 IR 被渲染了")
	}
	if !strings.Contains(err.Error(), "TOTAL_DURATION_MISMATCH") {
		t.Errorf("应先报结构问题，实得：%v", err)
	}
}

// ── 标号由请求决定,不跟模型书写顺序 ────────────────────────────────

// **模型把 image_2 写在前面,标号不能跟着它走。**
//
// 素材是**按提交顺序**发给模型和 H3 的。标号若按 asset_bindings 的书写
// 顺序发,<Picture 1> 就指向了第二张图 —— 提示词描述的是甲、H3 看到的
// 标签指向乙,完全不报错。
func TestReferenceInventoryFollowsRequestOrder(t *testing.T) {
	ir := sitcomIR()
	// 模型倒着写绑定
	ir.AssetBindings = []IRAssetBinding{
		{AssetID: "image_2", Role: "identity"},
		{AssetID: "image_1", Role: "scene"},
	}
	// 请求侧清单(由 applyAuthoritativeFacts 填)才是权威顺序
	ir.Assets = []IRAsset{
		{AssetID: "image_1", MediaType: "image", Role: "reference"},
		{AssetID: "image_2", MediaType: "image", Role: "reference"},
	}
	inv := BuildReferenceInventory(ir)
	if inv["image_1"] != "<Picture 1>" || inv["image_2"] != "<Picture 2>" {
		t.Errorf("标号跟着模型书写顺序走了：%v", inv)
	}
}

// 模型漏掉一个绑定时,后面的标号不能整体前移。
func TestReferenceInventoryDoesNotShiftOnMissingBinding(t *testing.T) {
	ir := sitcomIR()
	ir.AssetBindings = []IRAssetBinding{{AssetID: "image_2", Role: "identity"}} // 漏了 image_1
	ir.Assets = []IRAsset{
		{AssetID: "image_1", MediaType: "image"},
		{AssetID: "image_2", MediaType: "image"},
	}
	if got := BuildReferenceInventory(ir)["image_2"]; got != "<Picture 2>" {
		t.Errorf("漏一个绑定就让标号前移了：image_2 拿到 %s", got)
	}
}

// 图片与视频各自编号,且都按请求顺序。
func TestReferenceInventoryNumbersPerMediaType(t *testing.T) {
	ir := sitcomIR()
	ir.Assets = []IRAsset{
		{AssetID: "image_1", MediaType: "image"},
		{AssetID: "video_1", MediaType: "video"},
		{AssetID: "image_2", MediaType: "image"},
	}
	inv := BuildReferenceInventory(ir)
	for id, want := range map[string]string{
		"image_1": "<Picture 1>", "image_2": "<Picture 2>", "video_1": "<Video 1>",
	} {
		if inv[id] != want {
			t.Errorf("%s 应为 %s，实得 %s", id, want, inv[id])
		}
	}
}

// 引用一个没提交的素材要在**校验层**被具名拦下,而不是一路走到成品审计
// 才以 RAW_ASSET_ID_LEAK 的形式冒出来 —— 那报的是症状,不是原因。
func TestValidateCatchesUnknownAsset(t *testing.T) {
	ir := sitcomIR()
	ir.Assets = []IRAsset{{AssetID: "image_1", MediaType: "image"}}
	ir.AssetBindings = append(ir.AssetBindings, IRAssetBinding{AssetID: "image_9", Role: "scene"})
	if !auditCodes(ValidateIR(ir))["ASSET_UNKNOWN"] {
		t.Error("引用了没提交的素材却没被拦下")
	}
}

// 没有请求侧清单时不查 —— 直接构造 IR 的单测拿不到那份事实。
func TestValidateSkipsAssetCheckWithoutInventory(t *testing.T) {
	ir := sitcomIR()
	ir.Assets = nil
	if auditCodes(ValidateIR(ir))["ASSET_UNKNOWN"] {
		t.Error("没有请求侧清单时不该查素材归属")
	}
}

// ── 主体 ID 投影 ───────────────────────────────────────────────────

// 主体 ID 也要换成官方标签。
//
// 实测真实产出里出现过 "girl_1 pivots, walks a few steps…" —— IR 里那个
// 主体的 subject_id 正是 girl_1,原样漏进了正文。H3 只认 <Subject 1>,
// 留着它就把"同一个人"这条线索断掉了(六段式各段靠同一标签串联)。
func TestProjectSubjectLabelsReplacesSubjectIDs(t *testing.T) {
	inv := map[string]string{"girl_1": "<Subject 1>"}
	got := ProjectSubjectLabels("girl_1 pivots, then girl_1 smiles", inv)
	if got != "<Subject 1> pivots, then <Subject 1> smiles" {
		t.Errorf("主体 ID 没被投影：%s", got)
	}
}

// 前缀关系的两个 ID 不能互相啃。
//
// 靠的是整词边界判断,不是替换顺序 —— "girl_1" 在 "girl_10" 里后面跟着
// 数字,边界不成立。(这条用例对替换顺序不敏感,它盯的是边界判断。)
func TestProjectSubjectLabelsPrefixIDsDoNotCollide(t *testing.T) {
	inv := map[string]string{"girl_1": "<Subject 1>", "girl_10": "<Subject 2>"}
	got := ProjectSubjectLabels("girl_10 waves at girl_1", inv)
	if got != "<Subject 2> waves at <Subject 1>" {
		t.Errorf("长 ID 被短 ID 啃掉了：%s", got)
	}
}

// 整词才替:cowgirl_1 里不该切出 girl_1。
func TestProjectSubjectLabelsRespectsWordBoundaries(t *testing.T) {
	inv := map[string]string{"girl_1": "<Subject 1>"}
	for _, in := range []string{"cowgirl_1", "girl_1x", "girl_12"} {
		if got := ProjectSubjectLabels(in, inv); got != in {
			t.Errorf("%q 不该被改动，实得 %q", in, got)
		}
	}
}

// 渲染链路上要真的投影 —— 规则写对了但没接进 RenderPrompt 等于没生效。
func TestRenderPromptProjectsSubjectIDs(t *testing.T) {
	ir := sitcomIR()
	ir.Timeline[0].Event = "subject_1 turns toward the window"
	prompt, err := RenderPrompt(ir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prompt, "subject_1") {
		t.Errorf("成品里仍有裸的主体 ID：\n%s", prompt)
	}
	if !strings.Contains(prompt, "<Subject 1> turns toward the window") {
		t.Errorf("主体 ID 没被换成官方标签")
	}
}

// **三段式下不投影主体 ID。**
//
// 那条路的渲染器自己从不发主体标签,官方协议里也没有 <Subject N>。
// 投上去就是凭空造一个标签,而审计只在六段式白名单里放行它 —— 于是同时
// 踩中 SUBJECT_TAG_UNEXPECTED 和 PROMPT_NONOFFICIAL_ANGLE_TAG,一份本来
// 没问题的 IR 必然渲染失败,白烧一轮重修再静默回落 text。
func TestBaseModeDoesNotProjectSubjectIDs(t *testing.T) {
	ir := validIR()
	ir.Task.Type = string(TaskI2V)
	ir.AssetBindings = []IRAssetBinding{{AssetID: "image_1", Role: "first_frame"}}
	// 模型在正文里写了裸的 subject_id —— 实测真实产出就是这个形状。
	ir.Timeline[0].Event = "subject_1 starts the turn"

	prompt, err := RenderPrompt(ir)
	if err != nil {
		t.Fatalf("三段式渲染不该失败：%v", err)
	}
	if strings.Contains(prompt, "<Subject") {
		t.Errorf("三段式成品里不该出现主体标签：\n%s", prompt)
	}
}

// 六段式下照旧投影 —— 那里 <Subject N> 是协议的一部分。
func TestRefModeStillProjectsSubjectIDs(t *testing.T) {
	ir := sitcomIR()
	ir.Timeline[0].Event = "subject_1 turns toward the window"
	prompt, err := RenderPrompt(ir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prompt, "subject_1") {
		t.Error("六段式成品里仍有裸的主体 ID")
	}
	if !strings.Contains(prompt, "<Subject 1> turns toward the window") {
		t.Error("六段式没把主体 ID 投影成官方标签")
	}
}

// ── 帧角色以请求事实为准 ────────────────────────────────────────────

// flf2v 下模型把首尾帧标反,渲染必须按**请求侧**的角色走。
//
// 只读模型那份的后果很具体:提示词会写成 "<Picture 2> … aligns with the
// 0.00-second mark / <Picture 1> … aligns with the N-second mark" ——
// 视频倒着长,而那份 IR 自洽,校验一条都拦不下。
func TestFrameLabelsPreferRequestRoles(t *testing.T) {
	ir := validIR()
	ir.Task.Type = string(TaskFLF2V)
	ir.Assets = []IRAsset{
		{AssetID: "image_1", MediaType: "image", Role: "first_frame"},
		{AssetID: "image_2", MediaType: "image", Role: "last_frame"},
	}
	// 模型标反了
	ir.AssetBindings = []IRAssetBinding{
		{AssetID: "image_1", Role: "last_frame"},
		{AssetID: "image_2", Role: "first_frame"},
	}
	inv := BuildReferenceInventory(ir)
	first, last := frameLabelsByRole(ir, inv)
	if first != "<Picture 1>" || last != "<Picture 2>" {
		t.Errorf("角色没按请求事实取：first=%s last=%s", first, last)
	}
}

// 请求侧没给角色时才回落到模型那份 —— 直接构造 IR 的单测拿不到请求事实。
func TestFrameLabelsFallBackToModelRoles(t *testing.T) {
	ir := validIR()
	ir.Task.Type = string(TaskFLF2V)
	ir.Assets = nil
	ir.AssetBindings = []IRAssetBinding{
		{AssetID: "image_1", Role: "first_frame"},
		{AssetID: "image_2", Role: "last_frame"},
	}
	first, last := frameLabelsByRole(ir, BuildReferenceInventory(ir))
	if first != "<Picture 1>" || last != "<Picture 2>" {
		t.Errorf("没有请求事实时应回落模型角色：first=%s last=%s", first, last)
	}
}

// **冲突要被具名拦下,不能只是悄悄覆盖。**
//
// 模型把首尾帧标反,说明它对这次任务的理解就是反的 —— 后面的镜头描述多半
// 也按反的方向写。覆盖掉角色、留着一段反向的叙事,比直接驳回更糟。
func TestValidateCatchesFrameRoleConflict(t *testing.T) {
	ir := validIR()
	ir.Assets = []IRAsset{{AssetID: "image_1", MediaType: "image", Role: "first_frame"}}
	ir.AssetBindings = []IRAssetBinding{{AssetID: "image_1", Role: "last_frame"}}
	if !auditCodes(ValidateIR(ir))["ASSET_ROLE_CONFLICT"] {
		t.Error("首尾帧标反却没被拦下")
	}
}

// 参考族的角色没有方向性,不该被当成冲突。
func TestValidateIgnoresNonFrameRoleDifference(t *testing.T) {
	ir := validIR()
	ir.Assets = []IRAsset{{AssetID: "image_1", MediaType: "image", Role: "reference"}}
	ir.AssetBindings = []IRAssetBinding{{AssetID: "image_1", Role: "identity"}}
	if auditCodes(ValidateIR(ir))["ASSET_ROLE_CONFLICT"] {
		t.Error("参考族的角色差异不该判成冲突")
	}
}

// **00:00.000 是合法的,不能判错。**
//
// 早先这条规则把 sec<=0 也算违规,理由只对渲染器自己写的文本成立
// (shotText 确实不写 0 秒戳)。但成品里还有大片模型写的自由文本:编译器
// 要求每个动作同步音写清起止,sync_rules 骨架也写着"落在哪个时刻" ——
// 一条 00:00.000 的音效提示完全正常。
//
// 判错的代价是一份过了全部结构规则的 IR 被整个丢掉,白烧一轮重修加一次
// text 回落,约等于实测编译成本的两倍。
func TestAuditAcceptsZeroTimestamp(t *testing.T) {
	ir := sitcomIR()
	prompt, _ := RenderPrompt(ir)
	withCue := prompt + "\nthe door latch clicks at 00:00.000."
	if auditCodes(AuditPrompt(ir, withCue))["PROMPT_TIMESTAMP_RANGE"] {
		t.Error("00:00.000 被判成超范围")
	}
}

// 上界仍然要守住 —— 切点落在成片之外是真的错。
func TestAuditStillRejectsTimestampBeyondDuration(t *testing.T) {
	ir := sitcomIR() // 7 秒
	prompt, _ := RenderPrompt(ir)
	if !auditCodes(AuditPrompt(ir, prompt+"\nat 00:12.000 she turns."))["PROMPT_TIMESTAMP_RANGE"] {
		t.Error("超出时长的时间戳没被拦下")
	}
}
