package hilo

import (
	"fmt"
	"math"
	"strings"
)

// Context-IR 的确定性校验。
//
// # 这是引入 IR 的全部理由
//
// 让 LLM 直接写提示词的话，「镜头时长加起来等不等于请求时长」「素材标签
// 引用对不对」「时间线有没有空档」这些**只能靠它记得**。改成先产 IR，
// 这些就是几行代码能查的事。
//
// 移植自 XINGSHEN2/minimax-H3-context-IR 的 validate_context_ir（115 条）。
// **跳过了它有而我们 IR 里没有的那些**：directives（用户指令的结构化登记）、
// perception（独立的素材感知阶段）、isolation（参考隔离策略）—— 我们是
// prompt→prompt 的一次调用，没有这几层。
//
// # 校验失败怎么办：降级，不报错
//
// 和增强段整体策略一致。增强是锦上添花，为它牺牲一次本来能成功的生成
// 不划算。校验失败就退回原始提示词，并把原因记进过程记录。

// Problem 一条校验结果。
type Problem struct {
	// Code 机器可读的代号，和 XINGSHEN2 保持一致，便于对照它的规则。
	Code string
	// Path 出问题的字段路径，形如 `$.timeline[2].start_seconds`。
	Path string
	// Message 人话。会进过程记录，客户报"生成的跟我写的不一样"时是唯一证据。
	Message string
}

func (p Problem) String() string {
	if p.Path == "" {
		return fmt.Sprintf("[%s] %s", p.Code, p.Message)
	}
	return fmt.Sprintf("[%s] %s (%s)", p.Code, p.Message, p.Path)
}

// ValidationReport 一次校验的全部结论。
type ValidationReport struct {
	Problems []Problem
}

func (r *ValidationReport) Passed() bool { return len(r.Problems) == 0 }

func (r *ValidationReport) Reason() string {
	parts := make([]string, 0, len(r.Problems))
	for _, p := range r.Problems {
		parts = append(parts, p.String())
	}
	return strings.Join(parts, "; ")
}

func (r *ValidationReport) add(code, path, format string, args ...any) {
	r.Problems = append(r.Problems, Problem{
		Code: code, Path: path, Message: fmt.Sprintf(format, args...),
	})
}

// 时长比较的容差。浮点相加必然有误差，严格相等会把正确的 IR 判错。
const durationEpsilon = 0.01

var validTaskTypes = map[string]bool{
	string(TaskT2V): true, string(TaskI2V): true, string(TaskFLF2V): true,
	string(TaskL2VA): true, string(TaskR2VA): true,
}

var validRetentionModes = map[string]bool{
	string(RetentionFull): true, string(RetentionPartial): true,
	string(RetentionTransfer): true, string(RetentionWeak): true,
}

// ValidateIR 校验 IR。
func ValidateIR(ir *ContextIR) *ValidationReport {
	r := &ValidationReport{}
	if ir == nil {
		r.add("ROOT_INVALID", "$", "Context-IR 是空的")
		return r
	}

	// **先判"这到底是不是一份 IR"。**
	//
	// 实测 qwen3.8-flash-fp8 会整份跑偏:交来一个语法合法、但结构完全
	// 自造的 JSON(prompt / negative_prompt / audio_prompt 那种"视频生成
	// 请求"),ContextIR 的字段一个都没有。反序列化照样成功,于是每个字段
	// 都是零值,下面的逐项校验会同时报出五六条"缺这个缺那个"。
	//
	// 那组报告是**误导**的:它读起来像"这份 IR 差几块内容",而真相是
	// "这压根不是 IR"。更糟的是重修指令会照着它说「只改被点名的、其余
	// 保持逐字不变」—— 而这里根本没有值得保留的东西,那句话反而是在让
	// 模型守住错的形状。
	//
	// 给它一个自己的名字,重修那一侧才能换一套说法(见 buildRepairInstruction)。
	if !ir.IsRecognizable() {
		r.add("IR_SHAPE_UNRECOGNIZED", "$",
			"返回的 JSON 不是 Context-IR:timeline / subjects / asset_bindings / "+
				"creative_focus / protocol 全部缺失,模型多半自造了一套结构")
		return r
	}

	validateTask(ir, r)
	validateProtocol(ir, r)
	validateSubjects(ir, r)
	validateBindings(ir, r)
	validateAssetInventory(ir, r)
	validateReferenceRelationships(ir, r)
	validateTimeline(ir, r)
	validateAudio(ir, r)
	validateFocusAndConstraints(ir, r)
	return r
}

func validateTask(ir *ContextIR, r *ValidationReport) {
	t := strings.ToLower(strings.TrimSpace(ir.Task.Type))
	if t == "" {
		r.add("TASK_MISSING", "$.task.type", "没有声明 task.type")
	} else if !validTaskTypes[t] {
		r.add("TASK_TYPE_INVALID", "$.task.type",
			"task.type=%q 不是支持的玩法（t2v/i2v/flf2v/l2va/r2va）", ir.Task.Type)
	}
	if ir.Task.DurationSeconds <= 0 {
		r.add("DURATION_INVALID", "$.task.duration_seconds",
			"时长必须大于 0，实际 %g", ir.Task.DurationSeconds)
	}
}

func validateProtocol(ir *ContextIR, r *ValidationReport) {
	// **改写语言必须声明。**
	//
	// 它是"理解语言和改写语言是两件事"这条的载体：正文用改写语言，
	// 只有逐字台词、歌词、画面内文字保留源语言。不声明的话成品审计
	// 就没有判据 —— 而台词被翻译是这一步最难发现的错。
	if strings.TrimSpace(ir.Protocol.RewriteLanguage) == "" {
		r.add("PROTOCOL_MISSING", "$.protocol.rewrite_language", "没有声明改写语言")
	}
}

func validateSubjects(ir *ContextIR, r *ValidationReport) {
	shotIDs := map[string]bool{}
	for _, sh := range ir.Timeline {
		if sh.ShotID != "" {
			shotIDs[sh.ShotID] = true
		}
	}
	seen := map[string]bool{}
	isRef := strings.EqualFold(ir.Task.Type, string(TaskR2VA))
	for i, s := range ir.Subjects {
		path := fmt.Sprintf("$.subjects[%d]", i)
		if strings.TrimSpace(s.SubjectID) == "" {
			r.add("SUBJECT_ID_INVALID", path, "主体缺少 subject_id")
			continue
		}
		if seen[s.SubjectID] {
			r.add("SUBJECT_ID_DUPLICATE", path, "subject_id %q 重复", s.SubjectID)
		}
		seen[s.SubjectID] = true
		if strings.TrimSpace(s.Description) == "" {
			r.add("SUBJECT_DESCRIPTION_MISSING", path+".description",
				"主体 %s 没有描述 —— 渲染出来是一句破碎的定义", s.SubjectID)
		}
		// **appearance_shot_ids 必须指向真实存在的镜头。**
		//
		// 渲染时按 timeline 位置算 [Shot N]，认不出的 ID 会被直接丢掉 ——
		// retention 行就少了"出现在哪"的说明，而没人会发现。
		for _, sid := range s.AppearanceShotIDs {
			if !shotIDs[sid] {
				r.add("SUBJECT_APPEARANCE_SHOT_UNKNOWN", path+".appearance_shot_ids",
					"主体 %s 声称出现在镜头 %q，而 timeline 里没有这个 shot_id",
					s.SubjectID, sid)
			}
		}
		// 六段式必须有保留标记：retention_analysis 整段就是从它渲染的，
		// 缺了那一行就没了，而那正是"编辑"的表达。
		if isRef {
			if s.RetentionMode == "" {
				r.add("SUBJECT_RETENTION_MISSING", path+".retention_mode",
					"主体 %s 没有 retention_mode，retention_analysis 会缺这一行", s.SubjectID)
			} else if !validRetentionModes[string(s.RetentionMode)] {
				r.add("SUBJECT_RETENTION_INVALID", path+".retention_mode",
					"retention_mode=%q 不是官方的四个值之一", s.RetentionMode)
			}
			if s.RetentionMode != "" && strings.TrimSpace(s.RetentionDescription) == "" {
				r.add("SUBJECT_RETENTION_DESCRIPTION_MISSING", path+".retention_description",
					"主体 %s 声明了保留程度却没说保留了什么", s.SubjectID)
			}
		}
	}
}

func validateBindings(ir *ContextIR, r *ValidationReport) {
	for i, b := range ir.AssetBindings {
		path := fmt.Sprintf("$.asset_bindings[%d]", i)
		if strings.TrimSpace(b.AssetID) == "" {
			r.add("BINDING_ASSET_UNKNOWN", path+".asset_id", "绑定没有 asset_id")
		}
		if strings.TrimSpace(b.Role) == "" {
			r.add("BINDING_ROLE_MISSING", path+".role",
				"绑定 %s 没有角色 —— 渲染时说不出这个素材控制什么维度", b.AssetID)
			continue
		}
		// **图片不能提供运动/剪辑/音乐。**
		//
		// 这是素材授权边界里最要紧的一条：图片提供外观、构图、场景、
		// 风格或关键帧，但不提供观察到的运动、剪辑节奏或音乐。
		// 放过去的话改写模型会从一张静态图"推断"出运镜。
		if mediaTypeOf(b.AssetID, ir) == "image" {
			switch b.Role {
			case "motion", "rhythm", "camera", "music":
				r.add("BINDING_ROLE_EXCEEDS_MEDIA", path+".role",
					"图片 %s 被绑成 %s —— 静态图提供不了运动/节奏/运镜/音乐",
					b.AssetID, b.Role)
			}
		}
	}
	// 六段式里主体的外观来源要能对上绑定。
	if strings.EqualFold(ir.Task.Type, string(TaskR2VA)) {
		bound := map[string]bool{}
		for _, b := range ir.AssetBindings {
			bound[b.AssetID] = true
		}
		for i, s := range ir.Subjects {
			for _, src := range s.SourceAssetIDs {
				if !bound[src] {
					r.add("SUBJECT_SOURCE_WITHOUT_BINDING",
						fmt.Sprintf("$.subjects[%d].source_asset_ids", i),
						"主体 %s 声明来源 %s 却没有对应绑定 —— 渲染时说不出它控制什么",
						s.SubjectID, src)
				}
			}
		}
	}
}

func validateReferenceRelationships(ir *ContextIR, r *ValidationReport) {
	for i, rel := range ir.ReferenceRelationships {
		path := fmt.Sprintf("$.reference_relationships[%d]", i)
		if strings.TrimSpace(rel.AssetID) == "" {
			r.add("REFERENCE_RELATIONSHIP_ASSET_INVALID", path+".asset_id",
				"参考关系没有 asset_id")
		}
		if strings.TrimSpace(rel.Definition) == "" {
			r.add("REFERENCE_DESCRIPTION_MISSING", path+".definition",
				"参考素材 %s 没有定义 —— subject_definitions 里会少一行", rel.AssetID)
		}
		if rel.RetentionMode != "" && !validRetentionModes[string(rel.RetentionMode)] {
			r.add("REFERENCE_RETENTION_INVALID", path+".retention_mode",
				"retention_mode=%q 不是官方的四个值之一", rel.RetentionMode)
		}
	}
}

func validateTimeline(ir *ContextIR, r *ValidationReport) {
	if len(ir.Timeline) == 0 {
		r.add("TIMELINE_MISSING", "$.timeline", "没有任何镜头")
		return
	}
	subjects := map[string]bool{}
	for _, s := range ir.Subjects {
		subjects[s.SubjectID] = true
	}

	// **shot_id 不能重复。**
	//
	// buildShotIndex 是"后写覆盖先写"，而 [Shot N] 的引用全从那张表来 ——
	// 模型每场重新编号、或复用 shot_1 的话，主体的出现位置会**静默指向
	// 后面那个镜头**。这正是这一层要消灭的那类错引用：不报错，只是指错。
	seenShotID := map[string]bool{}
	prevEnd := 0.0
	for i, s := range ir.Timeline {
		path := fmt.Sprintf("$.timeline[%d]", i)
		if s.ShotID != "" {
			if seenShotID[s.ShotID] {
				r.add("SHOT_ID_DUPLICATE", path+".shot_id",
					"shot_id %q 重复 —— 引用它的地方会静默指向最后那个镜头", s.ShotID)
			}
			seenShotID[s.ShotID] = true
		}
		if strings.TrimSpace(s.Event) == "" {
			r.add("SHOT_EVENT_MISSING", path+".event",
				"第 %d 个镜头没有事件 —— 渲染出来只剩一个 [Shot N] 空壳", i+1)
		}
		if strings.TrimSpace(s.ObservableEndState) == "" {
			r.add("SHOT_END_STATE_MISSING", path+".observable_end_state",
				"第 %d 个镜头没有可观察的结束状态", i+1)
		}
		if s.EndSeconds <= s.StartSeconds {
			r.add("SHOT_DURATION_INVALID", path,
				"第 %d 个镜头的时间区间无效：%g → %g", i+1, s.StartSeconds, s.EndSeconds)
		}
		// **时间线不能有空档或重叠。**
		//
		// 空档会让 H3 在那段时间里没有指令、自由发挥；重叠则是两条
		// 互相打架的指令。两者都不报错，只是成片和描述对不上。
		if i == 0 {
			if math.Abs(s.StartSeconds) > durationEpsilon {
				r.add("TIMELINE_GAP_OR_OVERLAP", path+".start_seconds",
					"第一个镜头不是从 0 开始，而是 %g", s.StartSeconds)
			}
		} else if math.Abs(s.StartSeconds-prevEnd) > durationEpsilon {
			r.add("TIMELINE_GAP_OR_OVERLAP", path+".start_seconds",
				"第 %d 个镜头从 %g 开始，而上一个在 %g 结束", i+1, s.StartSeconds, prevEnd)
		}
		prevEnd = s.EndSeconds

		for _, ref := range s.SubjectRefs {
			if !subjects[ref] {
				r.add("SHOT_SUBJECT_UNKNOWN", path+".subject_refs",
					"第 %d 个镜头引用了不存在的主体 %s", i+1, ref)
			}
		}
	}

	// **镜头时长加起来必须等于请求时长。**
	//
	// 这条只能在 IR 上查 —— 在一段散文里，"总时长对不对"是查不了的。
	// 对不上的后果：H3 按它自己的理解分配时间，而用户要的节奏没了。
	if ir.Task.DurationSeconds > 0 &&
		math.Abs(prevEnd-ir.Task.DurationSeconds) > durationEpsilon {
		r.add("TOTAL_DURATION_MISMATCH", "$.timeline",
			"镜头总时长 %g 秒，请求的是 %g 秒", prevEnd, ir.Task.DurationSeconds)
	}
}

func validateAudio(ir *ContextIR, r *ValidationReport) {
	if !ir.Task.GenerateAudio {
		// 明确要静音时，声音字段必须是空的 —— 填了会被渲染进去，
		// 而用户要的是没有声音。
		a := ir.AudioPlan
		if !isAbsent(a.Voice) || !isAbsent(a.Music) ||
			!isAbsent(a.SoundEffects) || !isAbsent(a.AmbientSound) {
			r.add("AUDIO_PLAN_NOT_EMPTY_WHEN_SILENT", "$.audio_plan",
				"generate_audio=false 却填了声音内容")
		}
		return
	}
	// **要出声就不能整段空着。**
	//
	// 声音是成片的一部分，不是可选装饰。全空的话 overall_soundscape
	// 会渲染成 N/A —— 客户要了有声视频，拿到的是静音片。
	a := ir.AudioPlan
	if isAbsent(a.Voice) && isAbsent(a.Music) &&
		isAbsent(a.SoundEffects) && isAbsent(a.AmbientSound) && len(a.SyncRules) == 0 {
		r.add("AUDIO_SOUNDSCAPE_EMPTY", "$.audio_plan",
			"generate_audio=true 但声音计划整段是空的")
	}
}

func validateFocusAndConstraints(ir *ContextIR, r *ValidationReport) {
	if strings.TrimSpace(ir.CreativeFocus.Objective) == "" {
		r.add("CREATIVE_FOCUS_MISSING", "$.creative_focus.objective",
			"没有创作目标 —— summary 段会缺焦点声明")
	}
	// **只看真正会被渲染的两个字段。**
	//
	// 六段式的 styleOpening 只取 cinematography 和 lighting（照 XINGSHEN2
	// 原文），materials / performance / continuity 在那条路上根本不出现。
	// 把它们算进"基准非空"的话，一份只填了 materials 的 IR 会通过校验，
	// 然后渲染出一个**没有任何风格锚点**的 detailed_description ——
	// 正是这条校验要防的事。
	if s := ir.GenerationDescription; strings.TrimSpace(s.Cinematography) == "" &&
		strings.TrimSpace(s.Lighting) == "" {
		r.add("GENERATION_DESCRIPTION_MISSING", "$.generation_description",
			"全局基准缺 cinematography 和 lighting —— 渲染出来没有风格锚点"+
				"（materials/performance/continuity 只在三字段模式里用）")
	}
	// **同一项不能既要求保留又允许改动。**
	//
	// 两条会一起渲染进全局约束，H3 收到互相打架的指令，按哪条都说得通。
	preserve := map[string]bool{}
	for _, v := range ir.Constraints.Preserve {
		preserve[strings.ToLower(strings.TrimSpace(v))] = true
	}
	for _, v := range ir.Constraints.AllowChange {
		if preserve[strings.ToLower(strings.TrimSpace(v))] {
			r.add("CONSTRAINT_CONFLICT", "$.constraints",
				"%q 同时出现在 preserve 和 allow_change 里", v)
		}
	}
}

// validateAssetInventory 模型引用的素材必须都在**本次提交的清单**里。
//
// 只在 Assets 非空时查 —— 那是我们填的请求侧事实(见 applyAuthoritativeFacts),
// 没有它就无从判断"这个 ID 是不是真的传过来了"。
//
// 引用一个没传的素材是**可修复**的具体错误,给它一个名字才能驳回重修。
// 不查的话它会一路走到渲染:mediaTypeOf 猜一个类型、标号表里没有它,
// 于是正文里留下一个裸的 `image_9`,最后由成品审计报 RAW_ASSET_ID_LEAK ——
// 报得出来,但指向的是症状而不是原因。
func validateAssetInventory(ir *ContextIR, r *ValidationReport) {
	if len(ir.Assets) == 0 {
		return
	}
	known := make(map[string]bool, len(ir.Assets))
	for _, a := range ir.Assets {
		if a.AssetID != "" {
			known[a.AssetID] = true
		}
	}
	seen := map[string]bool{}
	report := func(path, id string) {
		if id == "" || known[id] || seen[id] {
			return
		}
		seen[id] = true
		r.add("ASSET_UNKNOWN", path, "引用了本次没有提交的素材 %q", id)
	}
	for i, b := range ir.AssetBindings {
		report(fmt.Sprintf("$.asset_bindings[%d].asset_id", i), b.AssetID)
	}
	for i, rr := range ir.ReferenceRelationships {
		report(fmt.Sprintf("$.reference_relationships[%d].asset_id", i), rr.AssetID)
	}
	for i, k := range ir.KeyframeRoles {
		report(fmt.Sprintf("$.keyframe_roles[%d].asset_id", i), k.AssetID)
	}
	for i, sub := range ir.Subjects {
		for _, id := range sub.SourceAssetIDs {
			report(fmt.Sprintf("$.subjects[%d].source_asset_ids", i), id)
		}
	}

	// **帧角色和请求事实冲突要被指认。**
	//
	// 渲染时请求侧的角色优先(见 frameLabelsByRole),所以冲突不会渲染出错的
	// 结果。但模型把首尾帧标反,说明它对这次任务的理解就是反的 —— 后面的
	// 镜头描述多半也按反的方向写。悄悄覆盖掉角色、留着一段反向的叙事,
	// 比直接驳回更糟。
	//
	// 只查首尾帧:参考族的角色没有方向性,标成什么都不改变时间轴。
	roleOf := map[string]string{}
	for _, a := range ir.Assets {
		if a.Role == "first_frame" || a.Role == "last_frame" {
			roleOf[a.AssetID] = a.Role
		}
	}
	if len(roleOf) > 0 {
		for i, b := range ir.AssetBindings {
			want, ok := roleOf[b.AssetID]
			if !ok || b.Role == "" {
				continue
			}
			if (b.Role == "first_frame" || b.Role == "last_frame") && b.Role != want {
				r.add("ASSET_ROLE_CONFLICT",
					fmt.Sprintf("$.asset_bindings[%d].role", i),
					"素材 %q 在请求里是 %s,这里标成了 %s —— 首尾帧标反会让视频倒着长",
					b.AssetID, want, b.Role)
			}
		}
	}
}

// ReportError 带着完整报告的错误。
//
// **两个读者要的是两种文本。** 运维在日志里读的是中文说明(哪条规则、
// 为什么);而递回给模型重修的那份必须是**纯英文的具名代码**——它正在被
// 要求"正文不要出现中文",指令里却嵌着一段中文,既示范了错误行为,也让
// 一部分模型跟着把输出语言切过去。
type ReportError struct {
	// Stage 出在哪一层:context-ir(结构)还是 prompt-audit(成品)。
	Stage  string
	Report *ValidationReport
}

func (e *ReportError) Error() string {
	return e.Stage + "未通过: " + e.Report.Reason()
}

// ModelFacing 递给模型的那份:只有代码和 JSON 路径。
//
// 不带中文说明是刻意的(见类型注释)。代价是像 ASSET_UNKNOWN 这种"具体
// 是哪一个"的信息只能靠模型自己比对 —— 它手上有完整 IR 和素材清单,
// 比对得出来;而把说明翻成英文要给三十来条规则各写一份,两份文案必然漂移。
func (e *ReportError) ModelFacing() string {
	if e == nil || e.Report == nil {
		return ""
	}
	seen := map[string]bool{}
	var out []string
	for _, p := range e.Report.Problems {
		line := p.Code
		if p.Path != "" {
			line += " at " + p.Path
		}
		if seen[line] {
			continue
		}
		seen[line] = true
		out = append(out, "- "+line)
	}
	return strings.Join(out, "\n")
}

// IsRecognizable 这份 JSON 看得出是 Context-IR 吗。
//
// 判据刻意宽松:只要**任何一处**主干还在,就当它是一份有欠缺的 IR,交给
// 逐项校验去报具名问题。全都不在才算整份跑偏 —— 那是另一类错,要另一
// 套说法。
//
// 宁可放过一份残缺的 IR,也不要把它误判成"整份跑偏":前者下游还能逐条
// 指出缺什么,后者会让模型把本来对的部分一起重写掉。
func (ir *ContextIR) IsRecognizable() bool {
	if ir == nil {
		return false
	}
	return len(ir.Timeline) > 0 ||
		len(ir.Subjects) > 0 ||
		len(ir.AssetBindings) > 0 ||
		len(ir.ReferenceRelationships) > 0 ||
		strings.TrimSpace(ir.CreativeFocus.Objective) != "" ||
		strings.TrimSpace(ir.Protocol.RewriteLanguage) != "" ||
		strings.TrimSpace(ir.SemanticPlan.PrimaryFocus) != ""
}
