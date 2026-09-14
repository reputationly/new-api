package hilo

import (
	"regexp"
	"strings"
	"testing"
)

// 基准取自官方 ref-en.txt 第 7 节的完整示例（咖啡馆 + 萨摩耶那个）。
//
// **拿官方自己的例子当对拍基准**，而不是我编一个：段落名、顺序、
// retention 标记、[Shot N] 前缀、时间戳格式——这些都是 H3 认的协议，
// 我凭印象写必然漂。
func sitcomIR() *ContextIR {
	return &ContextIR{
		Task:     IRTask{Type: string(TaskR2VA), DurationSeconds: 7, GenerateAudio: true},
		Protocol: IRProtocol{RewriteLanguage: "English"},
		Subjects: []IRSubject{
			{SubjectID: "subject_1", Description: "the coffee-shop environment, exposed brick wall, orange tufted sofa",
				SourceAssetIDs: []string{"image_1"}, AppearanceShotIDs: []string{"01", "02", "03"},
				RetentionMode: RetentionFull, RetentionDescription: "the exposed brick wall and orange sofa are retained"},
			{SubjectID: "subject_2", Description: "the fluffy white Samoyed",
				SourceAssetIDs: []string{"image_2"}, AppearanceShotIDs: []string{"01", "02"},
				RetentionMode: RetentionFull, RetentionDescription: "thick white fur and curved tail are retained"},
		},
		AssetBindings: []IRAssetBinding{
			{AssetID: "image_1", Role: "scene"},
			{AssetID: "image_2", Role: "identity"},
		},
		CreativeFocus: IRCreativeFocus{
			PrimaryTarget:    "The target video shows the woman eating a cookie in the coffee shop.",
			Objective:        "she reacts to the dog lunging for her cookie",
			PrimarySubjectID: "subject_1",
		},
		Timeline: []IRShot{
			{ShotID: "01", StartSeconds: 0, EndSeconds: 3, Event: "A medium shot establishes the coffee shop",
				Camera: "medium static", ObservableEndState: "she guards the cookie",
				SubjectRefs: []string{"subject_1", "subject_2"}},
			{ShotID: "02", StartSeconds: 3, EndSeconds: 5, Event: "cuts to a close-up of the man",
				Camera: "close-up", ObservableEndState: "he strokes the dog",
				SubjectRefs: []string{"subject_2"}},
			{ShotID: "03", StartSeconds: 5, EndSeconds: 7, Event: "cuts to a close-up of the woman",
				ObservableEndState: "she raises the cookie", SubjectRefs: []string{"subject_1"}},
		},
		AudioPlan: IRAudioPlan{AmbientSound: "Soft indoor coffee-shop room tone"},
		GenerationDescription: IRGenerationDescription{
			Cinematography: "realistic multi-camera sitcom style", Lighting: "warm indoor lighting"},
	}
}

// 六段式的段落名与顺序，必须和官方一字不差。
//
// 段落名是 H3 认的协议；漏一段或顺序错，H3 读不出对应内容，
// 而它**不会报错** —— 只是那部分指令没生效。
func TestRefPromptSectionsMatchOfficial(t *testing.T) {
	out, err := RenderPrompt(sitcomIR())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"subject_definitions", "summary", "retention_analysis",
		"detailed_description", "overall_soundscape", "non_diegetic_music",
	}
	pos := -1
	for _, sec := range want {
		re := regexp.MustCompile(`(?m)^` + sec + `:`)
		loc := re.FindStringIndex(out)
		if loc == nil {
			t.Fatalf("缺少段落 %s", sec)
		}
		if loc[0] <= pos {
			t.Errorf("段落 %s 顺序不对", sec)
		}
		pos = loc[0]
	}
	// 三字段的段落名不能混进来 —— 两套拼在一起 H3 会读不出哪个是哪个。
	for _, bad := range []string{"integrated_multimodal_description"} {
		if strings.Contains(out, bad+":") {
			t.Errorf("六段式里混进了 %s", bad)
		}
	}
}

// retention_analysis 是「编辑」的表达方式，格式必须对得上官方示例：
//
//	<Subject 1> (appears in [Shot 1], [Shot 2]): fully_preserved - …
func TestRetentionAnalysisFormat(t *testing.T) {
	out, _ := RenderPrompt(sitcomIR())
	re := regexp.MustCompile(`<Subject 1> \(appears in \[Shot 1\], \[Shot 2\], \[Shot 3\]\): fully_preserved - `)
	if !re.MatchString(out) {
		seg := out[strings.Index(out, "retention_analysis:"):]
		t.Errorf("retention 行的格式对不上官方示例：\n%s", seg[:min(300, len(seg))])
	}
}

// 标签编号必须确定性：同一个主体在所有段落里是同一个号。
//
// 让 LLM 自己编号的话，同一个人在 subject_definitions 里叫 <Subject 1>、
// 在 retention_analysis 里叫 <Subject 2> —— 这是最难查的一类错。
func TestLabelsAreStableAcrossSections(t *testing.T) {
	ir := sitcomIR()
	out, _ := RenderPrompt(ir)
	for _, sec := range []string{"subject_definitions", "retention_analysis", "detailed_description"} {
		i := strings.Index(out, sec+":")
		j := len(out)
		for _, later := range refSections {
			if k := strings.Index(out[i+len(sec):], "\n"+later+":"); k > 0 && i+len(sec)+k < j {
				j = i + len(sec) + k
			}
		}
		if !strings.Contains(out[i:j], "<Subject 1>") {
			t.Errorf("%s 段里没有 <Subject 1>", sec)
		}
	}
}

// 第一个镜头不带时间戳，其余带，格式 mm:ss.mmm。
func TestShotTimestamps(t *testing.T) {
	out, _ := RenderPrompt(sitcomIR())
	if strings.Contains(out, "[Shot 1] At ") {
		t.Error("第一个镜头不该带时间戳 —— 它从 0 开始，写出来是噪音")
	}
	if !strings.Contains(out, "[Shot 2] At 00:03.000,") {
		t.Errorf("第二个镜头的时间戳格式不对")
	}
}

// 三字段渲染（非 r2va）。
func TestBasePromptSections(t *testing.T) {
	ir := sitcomIR()
	ir.Task.Type = string(TaskFLF2V)
	out, _ := RenderPrompt(ir)
	for _, sec := range baseSections {
		if !regexp.MustCompile(`(?m)^` + sec + `:`).MatchString(out) {
			t.Errorf("缺少段落 %s", sec)
		}
	}
	// 六段式独有的不能出现。
	for _, bad := range []string{"subject_definitions", "retention_analysis"} {
		if strings.Contains(out, bad+":") {
			t.Errorf("三字段渲染里混进了 %s", bad)
		}
	}
	// 首尾帧要声明时间对齐，且时间戳由代码算。
	if !strings.Contains(out, "0.00-second mark") || !strings.Contains(out, "7.00-second mark") {
		t.Errorf("首尾帧的时间对齐声明不对：%s", out[:min(200, len(out))])
	}
}

// **只给尾帧时不能补首帧对齐** —— 那正是"把尾帧当首帧"的错法。
func TestLastFrameOnlyDoesNotClaimFirstFrame(t *testing.T) {
	ir := sitcomIR()
	ir.Task.Type = string(TaskL2VA)
	out, _ := RenderPrompt(ir)
	if strings.Contains(out, "0.00-second mark") {
		t.Error("仅尾帧却声明了 0 秒对齐 —— 视频会从结尾往后长")
	}
	if !strings.Contains(out, "7.00-second mark") {
		t.Error("仅尾帧要声明尾部对齐")
	}
}

// 关掉声音时渲染成 N/A，不是留空 —— 空段落会让 H3 以为没写完。
func TestSilentRendersNA(t *testing.T) {
	ir := sitcomIR()
	ir.Task.GenerateAudio = false
	// 静音时声音计划也要清空 —— 校验器拦的正是"说要静音却填了内容"
	// 这种自相矛盾，真实的 IR 不会这样。
	ir.AudioPlan = IRAudioPlan{}
	out, err := RenderPrompt(ir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "overall_soundscape:\nN/A") ||
		!strings.Contains(out, "non_diegetic_music:\nN/A") {
		t.Error("静音时两个声音段都该是 N/A")
	}
}

// 模型表达"没有"的各种写法不能被当成内容渲染进去。
func TestAbsentValuesAreNotRendered(t *testing.T) {
	ir := sitcomIR()
	ir.AudioPlan = IRAudioPlan{Voice: "none", Music: "not requested", AmbientSound: "room tone"}
	out, err := RenderPrompt(ir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "voice: none") {
		t.Error("`voice: none` 被渲染进去了 —— H3 会把它当成一句描述")
	}
	if !strings.Contains(out, "non_diegetic_music:\nN/A") {
		t.Error("`not requested` 应当渲染成 N/A")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// **只挂结构类角色的素材，必须明说它不是外观来源。**
//
// 用户说"参考这个视频的动作"，授权的是动作 —— 不说清楚的话 H3 会把
// 视频里那个人的长相也带过来，而产出和意图打架却完全说得通，很难发现。
//
// 这是 XINGSHEN2 相对官方 skill 最有价值的补充，也是移植这一段的理由。
func TestStructuralOnlyReferenceIsNotAnAppearanceSource(t *testing.T) {
	ir := sitcomIR()
	ir.Subjects = []IRSubject{{
		SubjectID: "subject_1", Name: "the dancer", Description: "a young woman",
		SourceAssetIDs: []string{"video_1"},
		RetentionMode:  RetentionPartial, RetentionDescription: "her choreography is followed",
	}}
	ir.AssetBindings = []IRAssetBinding{{AssetID: "video_1", Role: "motion"}}
	// 直调渲染器：本用例验的是「素材控制维度怎么渲染」，
	// 而这些只改一处的 IR 过不了完整校验。
	out := renderRefPrompt(ir, BuildReferenceInventory(ir))

	if !strings.Contains(out, "is not an appearance source") {
		t.Errorf("没声明「不是外观来源」—— 参考视频的长相会被一起抄过来：\n%s",
			out[:min(400, len(out))])
	}
	if !strings.Contains(out, "its motion follows <Video 1>") {
		t.Error("没说清楚它控制的是 motion")
	}
}

// 外观类角色要渲染成人话，并按固定顺序 —— 同一组角色每次产出同样的句子。
func TestAppearanceRolesRenderAsScope(t *testing.T) {
	ir := sitcomIR()
	ir.Subjects = []IRSubject{{
		SubjectID: "subject_1", Name: "the model", Description: "a woman",
		SourceAssetIDs: []string{"image_1"},
		RetentionMode:  RetentionFull, RetentionDescription: "kept",
	}}
	ir.AssetBindings = []IRAssetBinding{
		{AssetID: "image_1", Role: "outfit"},
		{AssetID: "image_1", Role: "identity"},
	}
	// 直调渲染器：本用例验的是「素材控制维度怎么渲染」，
	// 而这些只改一处的 IR 过不了完整校验。
	out := renderRefPrompt(ir, BuildReferenceInventory(ir))
	// identity 在 outfit 之前 —— 固定顺序，不跟着 IR 里的先后走。
	if !strings.Contains(out, "<Picture 1> controls its identity and facial appearance, outfit") {
		t.Errorf("外观维度的渲染或顺序不对：\n%s", out[:min(400, len(out))])
	}
}

// 关键帧角色要说清楚它**不提供**什么。
func TestKeyframeRoleStatesWhatItDoesNotSupply(t *testing.T) {
	ir := sitcomIR()
	ir.Subjects = []IRSubject{{
		SubjectID: "subject_1", Name: "the dancer", Description: "a woman",
		RetentionMode: RetentionFull, RetentionDescription: "kept",
	}}
	ir.AssetBindings = nil
	ir.KeyframeRoles = []IRKeyframeRole{
		{AssetID: "image_1", Role: "action_keyframe", SubjectRefs: []string{"subject_1"}},
	}
	// 直调渲染器：本用例验的是「素材控制维度怎么渲染」，
	// 而这些只改一处的 IR 过不了完整校验。
	out := renderRefPrompt(ir, BuildReferenceInventory(ir))
	if !strings.Contains(out, "anchors its exact action pose, not motion or edit rhythm") {
		t.Errorf("动作关键帧没声明「不提供运动和剪辑节奏」：\n%s", out[:min(400, len(out))])
	}
}

// 字段为空时**不能产出病句**。
//
// 渲染结果原样进提示词，H3 读到 "<Subject 1> is , the cat" 或
// "Primary creative objective: ." 只会当成破碎的描述 —— 不报错，
// 只是那句指令废了。这是 dump 出来才发现的，测试断言里看不出来。
func TestNoMalformedSentencesOnEmptyFields(t *testing.T) {
	ir := sitcomIR()
	ir.CreativeFocus = IRCreativeFocus{} // 焦点整个为空
	for i := range ir.Subjects {
		ir.Subjects[i].Name = "" // 只有 Description
	}
	// **绕过校验直接渲染。** 这个用例要验的是"渲染器遇到空字段不产病句",
	// 而这样的 IR 本来就过不了校验 —— 但校验是可以被降级绕过的一层，
	// 渲染器自己也得稳。
	out := renderRefPrompt(ir, BuildReferenceInventory(ir))

	for _, bad := range []string{" is , ", ": .", "objective: ."} {
		if strings.Contains(out, bad) {
			t.Errorf("产出了病句片段 %q：\n%s", bad, out[:min(400, len(out))])
		}
	}
}

// retention 行尾要有句号 —— 照 XINGSHEN2 的 rstrip('.') + "."。
func TestRetentionLinesEndWithPeriod(t *testing.T) {
	out, _ := RenderPrompt(sitcomIR())
	i := strings.Index(out, "retention_analysis:\n")
	seg := out[i:]
	if j := strings.Index(seg, "\n\n"); j > 0 {
		seg = seg[:j]
	}
	for _, line := range strings.Split(seg, "\n")[1:] {
		if line != "" && !strings.HasSuffix(line, ".") {
			t.Errorf("retention 行没有句号：%q", line)
		}
	}
}

// **镜头引用按 timeline 位置算，不能拿 shot_id 裁零。**
//
// 提示词里其他地方的 [Shot N] 都是 timeline 下标，两者只有在模型恰好
// 输出 "01"/"02" 这种补零连续 ID 时才对得上。模型写 "shot_1" 的话，
// 老写法会渲染出 [Shot shot_1] —— H3 读不懂的引用，而且不报错。
func TestShotCitationsUseTimelinePosition(t *testing.T) {
	ir := sitcomIR()
	// 模型用了另一种 ID 风格，且顺序和 timeline 一致。
	ir.Timeline[0].ShotID = "shot_1"
	ir.Timeline[1].ShotID = "shot_2"
	ir.Timeline[2].ShotID = "shot_3"
	ir.Subjects[0].AppearanceShotIDs = []string{"shot_1", "shot_3"}
	ir.Subjects[1].AppearanceShotIDs = []string{"shot_2"}

	// 直调渲染器：本用例改了 shot_id 的风格，而校验器会因为
	// appearance_shot_ids 对不上而拦下 —— 那是另一条规则的职责。
	out := renderRefPrompt(ir, BuildReferenceInventory(ir))
	if out == "" {
		t.Fatal("渲染结果是空的")
	}
	if strings.Contains(out, "[Shot shot_") {
		t.Errorf("渲染出了 H3 读不懂的引用：\n%s", out[:min(500, len(out))])
	}
	if !strings.Contains(out, "(appears in [Shot 1], [Shot 3])") {
		t.Errorf("镜头引用没按 timeline 位置算：\n%s", out[:min(500, len(out))])
	}
}

// 认不出的 shot_id 丢掉，**不产出错引用**。
func TestUnknownShotIDsAreDroppedNotRendered(t *testing.T) {
	ir := sitcomIR()
	ir.Subjects[0].AppearanceShotIDs = []string{"01", "nope"}
	out := renderRefPrompt(ir, BuildReferenceInventory(ir))
	if strings.Contains(out, "nope") {
		t.Error("认不出的 shot_id 被渲染进去了")
	}
}

// **"no dialogue, only footsteps" 是实实在在的声音指令，不能当成"没有"。**
//
// 我移植时把前缀启发（以 "no " 开头就算没有）套给了所有声音字段，
// 而 XINGSHEN2 原文里它**只用在 music 上**。套错的后果：用户要的环境音
// 被整条删掉，而且因为校验器用同一个判据，整份 IR 还会被判"声音计划为空"
// 直接拒掉 —— 降级成原始提示词。
func TestNegativePhrasingIsNotTreatedAsAbsent(t *testing.T) {
	ir := sitcomIR()
	ir.AudioPlan = IRAudioPlan{AmbientSound: "no dialogue, only footsteps and room tone"}
	out, err := RenderPrompt(ir)
	if err != nil {
		t.Fatalf("这份 IR 有声音内容，不该被拒：%v", err)
	}
	if !strings.Contains(out, "footsteps and room tone") {
		t.Errorf("声音指令被当成「没有」丢掉了：\n%s", out[strings.Index(out, "overall_soundscape"):])
	}
}

// 但 music 那个字段保留前缀启发 —— 照原文。
func TestMusicKeepsThePrefixHeuristic(t *testing.T) {
	ir := sitcomIR()
	ir.AudioPlan = IRAudioPlan{AmbientSound: "room tone", Music: "none; the scene stays quiet"}
	out, _ := RenderPrompt(ir)
	if !strings.Contains(out, "non_diegetic_music:\nN/A") {
		t.Errorf("music 的「none; …」应当渲染成 N/A")
	}
}

// **只有一张图时不能声明首尾帧对齐。**
//
// pics[0] 和 pics[len-1] 会是同一个标签，渲染出来是「这张图既是首帧
// 又是尾帧」，H3 会试图让画面回到起点。
func TestFlfWithSinglePictureDoesNotClaimBothFrames(t *testing.T) {
	ir := sitcomIR()
	ir.Task.Type = string(TaskFLF2V)
	ir.AssetBindings = []IRAssetBinding{{AssetID: "image_1", Role: "identity"}}
	ir.Subjects = ir.Subjects[:1]
	ir.Subjects[0].SourceAssetIDs = []string{"image_1"}
	for i := range ir.Timeline {
		ir.Timeline[i].SubjectRefs = []string{"subject_1"}
	}
	out, err := RenderPrompt(ir)
	// **必须断言 err。** 第一版没断言，而这份 IR 当时过不了校验、out 是
	// 空串 —— "不包含某串"在空串上恒真，测试永远绿。校准时才发现。
	if err != nil {
		t.Fatalf("这份 IR 应当合法：%v", err)
	}
	if strings.Contains(out, "aligns with the 7.00-second mark") {
		t.Errorf("只有一张图却声明了尾帧对齐：\n%s", out[:min(300, len(out))])
	}
	if !strings.Contains(out, "is fully referenced") {
		t.Errorf("单图应当退回 i2v 的说法：\n%s", out[:min(300, len(out))])
	}
}

// **首尾帧按角色认，不是按书写顺序。**
//
// 中间夹一张风格参考图、或把尾帧写在前面，按顺序取就会把尾帧当首帧 ——
// 视频从结尾往后长，而且不报错。这正是 convert.go 警告过的那类错。
func TestFrameAlignmentUsesRolesNotOrder(t *testing.T) {
	ir := sitcomIR()
	ir.Task.Type = string(TaskFLF2V)
	ir.Subjects = ir.Subjects[:1]
	ir.Subjects[0].SourceAssetIDs = []string{"image_1", "image_2", "image_3"}
	// **尾帧写在最前，中间夹一张风格图** —— 按顺序取会全错。
	ir.AssetBindings = []IRAssetBinding{
		{AssetID: "image_1", Role: "last_frame"},
		{AssetID: "image_2", Role: "style"},
		{AssetID: "image_3", Role: "first_frame"},
	}
	for i := range ir.Timeline {
		ir.Timeline[i].SubjectRefs = []string{"subject_1"}
	}
	out, err := RenderPrompt(ir)
	if err != nil {
		t.Fatalf("这份 IR 应当合法：%v", err)
	}
	// image_3 是首帧 → <Picture 3>；image_1 是尾帧 → <Picture 1>。
	if !strings.Contains(out, "<Picture 3> (from [Shot 1]) aligns with the 0.00-second mark") {
		t.Errorf("首帧认错了：\n%s", out[:min(320, len(out))])
	}
	if !strings.Contains(out, "<Picture 1> (from [Shot 3]) aligns with the 7.00-second mark") {
		t.Errorf("尾帧认错了：\n%s", out[:min(320, len(out))])
	}
}

// 没声明角色时退回按顺序 —— 不能因为模型没写角色就整条渲染不出来。
func TestFrameAlignmentFallsBackToOrder(t *testing.T) {
	ir := sitcomIR()
	ir.Task.Type = string(TaskFLF2V)
	ir.Subjects = ir.Subjects[:1]
	ir.Subjects[0].SourceAssetIDs = []string{"image_1", "image_2"}
	ir.AssetBindings = []IRAssetBinding{
		{AssetID: "image_1", Role: "identity"},
		{AssetID: "image_2", Role: "scene"},
	}
	for i := range ir.Timeline {
		ir.Timeline[i].SubjectRefs = []string{"subject_1"}
	}
	out, err := RenderPrompt(ir)
	if err != nil {
		t.Fatalf("应当合法：%v", err)
	}
	if !strings.Contains(out, "<Picture 1> (from [Shot 1]) aligns with the 0.00-second mark") {
		t.Errorf("没有角色时应当退回按顺序：\n%s", out[:min(320, len(out))])
	}
}
