package hilo

import (
	"strings"
	"testing"
)

// 一份合法的 IR，各用例在它上面改一处。
func validIR() *ContextIR {
	return &ContextIR{
		Task:     IRTask{Type: string(TaskR2VA), DurationSeconds: 6, GenerateAudio: true},
		Protocol: IRProtocol{RewriteLanguage: "English"},
		Subjects: []IRSubject{{
			SubjectID: "subject_1", Name: "the dancer", Description: "a young woman",
			SourceAssetIDs: []string{"image_1"}, AppearanceShotIDs: []string{"01"},
			RetentionMode: RetentionFull, RetentionDescription: "her identity is retained",
		}},
		AssetBindings: []IRAssetBinding{{AssetID: "image_1", Role: "identity"}},
		CreativeFocus: IRCreativeFocus{Objective: "she completes the turn", PrimarySubjectID: "subject_1"},
		Timeline: []IRShot{
			{ShotID: "01", StartSeconds: 0, EndSeconds: 3, Event: "she starts the turn",
				ObservableEndState: "mid-turn", SubjectRefs: []string{"subject_1"}},
			{ShotID: "02", StartSeconds: 3, EndSeconds: 6, Event: "she lands",
				ObservableEndState: "standing still"},
		},
		AudioPlan:             IRAudioPlan{AmbientSound: "studio room tone"},
		GenerationDescription: IRGenerationDescription{Cinematography: "handheld", Lighting: "soft"},
	}
}

func codes(r *ValidationReport) map[string]bool {
	m := map[string]bool{}
	for _, p := range r.Problems {
		m[p.Code] = true
	}
	return m
}

func TestValidIRPasses(t *testing.T) {
	if r := ValidateIR(validIR()); !r.Passed() {
		t.Fatalf("合法的 IR 被判失败：%s", r.Reason())
	}
}

// **时长加总必须等于请求时长。**
//
// 这条只能在 IR 上查 —— 一段散文里"总时长对不对"根本查不了。
// 对不上的后果：H3 按自己的理解分配时间，用户要的节奏没了，而且不报错。
func TestTotalDurationMismatch(t *testing.T) {
	ir := validIR()
	ir.Timeline[1].EndSeconds = 5 // 总共 5 秒，请求是 6 秒
	if !codes(ValidateIR(ir))["TOTAL_DURATION_MISMATCH"] {
		t.Error("总时长对不上却没被拦")
	}
}

// 时间线不能有空档或重叠。
func TestTimelineGapAndOverlap(t *testing.T) {
	gap := validIR()
	gap.Timeline[1].StartSeconds = 4 // 上一个 3 秒结束，这个 4 秒开始
	gap.Timeline[1].EndSeconds = 6
	if !codes(ValidateIR(gap))["TIMELINE_GAP_OR_OVERLAP"] {
		t.Error("时间线有空档却没被拦 —— 那段时间 H3 会自由发挥")
	}

	overlap := validIR()
	overlap.Timeline[1].StartSeconds = 2 // 和上一个重叠
	if !codes(ValidateIR(overlap))["TIMELINE_GAP_OR_OVERLAP"] {
		t.Error("时间线重叠却没被拦 —— 两条互相打架的指令")
	}

	notZero := validIR()
	notZero.Timeline[0].StartSeconds = 1
	if !codes(ValidateIR(notZero))["TIMELINE_GAP_OR_OVERLAP"] {
		t.Error("第一个镜头不从 0 开始却没被拦")
	}
}

// **图片不能被绑成运动/节奏/运镜/音乐。**
//
// 素材授权边界里最要紧的一条。放过去的话改写模型会从一张静态图
// "推断"出运镜，而那是它编的。
func TestImageCannotSupplyMotion(t *testing.T) {
	for _, role := range []string{"motion", "rhythm", "camera", "music"} {
		ir := validIR()
		ir.AssetBindings = []IRAssetBinding{{AssetID: "image_1", Role: role}}
		if !codes(ValidateIR(ir))["BINDING_ROLE_EXCEEDS_MEDIA"] {
			t.Errorf("图片被绑成 %s 却没被拦", role)
		}
	}
	// 视频可以 —— 用户授权时它确实能提供这些。
	ir := validIR()
	ir.AssetBindings = []IRAssetBinding{{AssetID: "video_1", Role: "motion"}}
	ir.Subjects[0].SourceAssetIDs = []string{"video_1"}
	if codes(ValidateIR(ir))["BINDING_ROLE_EXCEEDS_MEDIA"] {
		t.Error("视频提供 motion 是正当的，不该被拦")
	}
}

// 六段式必须有保留标记 —— retention_analysis 整段从它渲染。
func TestRefModeRequiresRetention(t *testing.T) {
	ir := validIR()
	ir.Subjects[0].RetentionMode = ""
	if !codes(ValidateIR(ir))["SUBJECT_RETENTION_MISSING"] {
		t.Error("六段式缺 retention_mode 却没被拦")
	}

	bad := validIR()
	bad.Subjects[0].RetentionMode = "kept" // 不是官方四个值之一
	if !codes(ValidateIR(bad))["SUBJECT_RETENTION_INVALID"] {
		t.Error("非法的 retention_mode 没被拦 —— 它会原样进提示词")
	}

	// 三字段模式不要求。
	base := validIR()
	base.Task.Type = string(TaskI2V)
	base.Subjects[0].RetentionMode = ""
	if codes(ValidateIR(base))["SUBJECT_RETENTION_MISSING"] {
		t.Error("三字段模式不该要求 retention_mode")
	}
}

// 主体声明的来源必须有对应绑定，否则渲染时说不出它控制什么维度。
func TestSubjectSourceNeedsBinding(t *testing.T) {
	ir := validIR()
	ir.Subjects[0].SourceAssetIDs = []string{"image_9"}
	if !codes(ValidateIR(ir))["SUBJECT_SOURCE_WITHOUT_BINDING"] {
		t.Error("来源没有对应绑定却没被拦")
	}
}

// 要出声就不能整段空着；要静音就不能填内容。
func TestAudioPlanConsistency(t *testing.T) {
	empty := validIR()
	empty.AudioPlan = IRAudioPlan{}
	if !codes(ValidateIR(empty))["AUDIO_SOUNDSCAPE_EMPTY"] {
		t.Error("要出声却整段空着 —— 客户会拿到静音片")
	}

	silent := validIR()
	silent.Task.GenerateAudio = false
	if !codes(ValidateIR(silent))["AUDIO_PLAN_NOT_EMPTY_WHEN_SILENT"] {
		t.Error("要静音却填了声音内容")
	}
}

// 同一项不能既要求保留又允许改动 —— 渲染进去是互相打架的指令。
func TestConstraintConflict(t *testing.T) {
	ir := validIR()
	ir.Constraints = IRConstraints{
		Preserve:    []string{"the red dress"},
		AllowChange: []string{"The Red Dress"}, // 大小写不同也算同一项
	}
	if !codes(ValidateIR(ir))["CONSTRAINT_CONFLICT"] {
		t.Error("保留和允许改动冲突却没被拦")
	}
}

// 镜头引用了不存在的主体。
func TestShotSubjectUnknown(t *testing.T) {
	ir := validIR()
	ir.Timeline[0].SubjectRefs = []string{"subject_9"}
	if !codes(ValidateIR(ir))["SHOT_SUBJECT_UNKNOWN"] {
		t.Error("引用了不存在的主体却没被拦")
	}
}

// 报告要能读 —— 它会进过程记录，是排障时唯一的证据。
func TestReasonIsReadable(t *testing.T) {
	ir := validIR()
	ir.Timeline[1].EndSeconds = 5
	reason := ValidateIR(ir).Reason()
	if !strings.Contains(reason, "TOTAL_DURATION_MISMATCH") ||
		!strings.Contains(reason, "$.timeline") {
		t.Errorf("报告里缺代号或路径：%s", reason)
	}
}

// **shot_id 重复要拦。**
//
// buildShotIndex 是后写覆盖先写，[Shot N] 的引用全从那张表来 ——
// 模型每场重新编号或复用 shot_1 的话，主体的出现位置会静默指向
// 后面那个镜头。不报错，只是指错。
func TestDuplicateShotIDRejected(t *testing.T) {
	ir := validIR()
	ir.Timeline[1].ShotID = "01" // 和第一个撞了
	if !codes(ValidateIR(ir))["SHOT_ID_DUPLICATE"] {
		t.Error("重复的 shot_id 没被拦 —— 引用会静默指向最后那个镜头")
	}
}

// ── 整份跑偏 ───────────────────────────────────────────────────────

// 模型交来一个语法合法、但结构完全自造的 JSON。
//
// 实测 qwen3.8-flash-fp8 会这样:它从用户消息重新推导任务,推到训练先验
// 里最常见的那个形状(prompt / negative_prompt / audio_prompt 的"视频生成
// 请求"),绕开了系统提示词里的 schema。反序列化照样成功,于是每个字段
// 都是零值。
//
// 这时报一串"缺这个缺那个"是**误导**的 —— 读起来像"这份 IR 差几块内容",
// 而真相是"这压根不是 IR"。要给它自己的名字,重修那侧才能换说法。
func TestUnrecognizedShapeGetsItsOwnCode(t *testing.T) {
	ir := &ContextIR{} // 反序列化一个完全无关的 JSON 就长这样
	c := codes(ValidateIR(ir))
	if !c["IR_SHAPE_UNRECOGNIZED"] {
		t.Fatal("整份跑偏没有被单独指认")
	}
	if len(c) != 1 {
		t.Errorf("整份跑偏时不该再报一串逐项问题，实得 %v", c)
	}
}

// **一份有欠缺的 IR 不能被误判成"整份跑偏"。**
//
// 后者会让模型把本来对的部分一起重写掉。只要主干还剩任何一处,就当它是
// 有欠缺的 IR,交给逐项校验去报具名问题。
func TestPartialIRIsNotTreatedAsUnrecognized(t *testing.T) {
	for name, mut := range map[string]func(*ContextIR){
		"只剩时间线":  func(ir *ContextIR) { ir.Timeline = []IRShot{{ShotID: "01"}} },
		"只剩主体":   func(ir *ContextIR) { ir.Subjects = []IRSubject{{SubjectID: "subject_1"}} },
		"只剩素材绑定": func(ir *ContextIR) { ir.AssetBindings = []IRAssetBinding{{AssetID: "image_1"}} },
		"只剩创作目标": func(ir *ContextIR) { ir.CreativeFocus.Objective = "she turns" },
		"只剩改写语言": func(ir *ContextIR) { ir.Protocol.RewriteLanguage = "English" },
	} {
		ir := &ContextIR{}
		mut(ir)
		if codes(ValidateIR(ir))["IR_SHAPE_UNRECOGNIZED"] {
			t.Errorf("%s 的 IR 只是残缺，不该判成整份跑偏", name)
		}
	}
}

// 合法的 IR 当然认得出来。
func TestValidIRIsRecognizable(t *testing.T) {
	if !validIR().IsRecognizable() {
		t.Error("合法 IR 被判成不认识")
	}
}
