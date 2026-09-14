package hilo

import "strings"

// 「哪张图是什么帧」的**单一来源**。
//
// # 为什么要单独抽出来
//
// 这个语义此前散在三处，互不相连：
//
//	VideoRequest.FirstFrame           客户端形状      convert.go
//	metadata.task_type = "flf2v"      平台契约        convert.go
//	IRAssetBinding.Role="first_frame" IR             ir.go（定义了，render 不读）
//
// 于是同一类错犯了三次：convert.go 漏传 task_type 让「仅尾帧」退化成 i2v；
// render.go 按书写顺序取首尾帧、把尾帧当首帧；中间我还自造过一个
// `last_frame_only` 字段，全仓没人读。**我在 convert.go 为这件事写过
// 警告注释和测试，转头在 render.go 又犯了一遍** —— 分开写就会分开想。
//
// 现在判定只在这里做一次，convert 和 render 都从它取。
//
// # 判据：看填了哪个槽，不是看有几张图
//
// l2va（只给尾帧）和 i2v 的输入形态完全相同 —— 都是一张图，区别纯在语义。
// 平台适配器把 l2va **故意排除**在形态推断之外，正因为推不出来。

// FrameRoles 一次请求里各类素材的角色划分。
type FrameRoles struct {
	// First 首帧 URL。空 = 没给。
	First string
	// Last 尾帧 URL。空 = 没给。
	Last string
	// Refs 参考素材。和首尾帧**互斥** —— 它们走不同的落点、不同的 checkpoint。
	Refs []string
	// Mode 客户端层面的玩法。
	Mode ImageMode
	// TaskType 平台的 task_type，下发在 metadata.task_type。
	TaskType TaskType
}

// ResolveFrameRoles 从客户端请求里解析角色。**这是唯一的判定入口。**
func ResolveFrameRoles(r *VideoRequest) FrameRoles {
	if r == nil {
		return FrameRoles{Mode: ModeTextToVideo, TaskType: TaskT2V}
	}
	first := strings.TrimSpace(r.FirstFrame)
	last := strings.TrimSpace(r.LastFrame)

	var refs []string
	for _, u := range r.RefImages {
		if s := strings.TrimSpace(u); s != "" {
			refs = append(refs, s)
		}
	}

	out := FrameRoles{First: first, Last: last, Refs: refs}
	switch {
	case first != "" || last != "":
		// **首尾帧优先。** 同时给了两种时，帧约束的语义更强（它直接决定
		// 画幅），参考图只是风格参考。真实请求里不会同时出现，这里只是
		// 不让它变成一个"看起来随机"的选择。
		out.Mode = ModeFirstLastFrame
		out.Refs = nil
		switch {
		case first != "" && last != "":
			out.TaskType = TaskFLF2V
		case last != "":
			// 只给尾帧。**必须是 l2va，不能退化成 i2v** ——
			// 退化的后果是把尾帧当首帧，视频从结尾往后长，而且不报错。
			out.TaskType = TaskL2VA
		default:
			out.TaskType = TaskI2V
		}
	case len(refs) > 0:
		out.Mode = ModeReference
		out.TaskType = TaskR2VA
	default:
		out.Mode = ModeTextToVideo
		out.TaskType = TaskT2V
	}
	return out
}

// FrameImages 帧约束模式下要放进顶层 images[] 的素材。
//
// **顺序即语义**：[0]=首帧、[1]=尾帧。颠倒了视频会倒着长，而且不报错。
// 只有尾帧时列表里就一张，靠 task_type=l2va 表达它是尾帧 ——
// **不能补一个空串占位**，那会被当成一张读不出的图。
func (f FrameRoles) FrameImages() []string {
	if f.Mode != ModeFirstLastFrame {
		return nil
	}
	var out []string
	if f.First != "" {
		out = append(out, f.First)
	}
	if f.Last != "" {
		out = append(out, f.Last)
	}
	return out
}

// IRBindingRole 某个素材在 IR 里该标成什么角色。
//
// 这是把客户端语义翻成 IR 词汇的地方 —— 渲染器按 `first_frame` /
// `last_frame` 认关键帧（见 frameLabelsByRole），标错就会把尾帧当首帧。
func (f FrameRoles) IRBindingRole(url string) string {
	switch strings.TrimSpace(url) {
	case "":
		return ""
	case f.First:
		return "first_frame"
	case f.Last:
		return "last_frame"
	}
	for _, r := range f.Refs {
		if r == url {
			return "reference"
		}
	}
	return ""
}

// FrameRolesFromTask 从**已归一化**的请求解析角色。
//
// 和 ResolveFrameRoles 是同一条规则的两个入口:那个吃客户端形状
// (VideoRequest.FirstFrame/LastFrame),这个吃平台契约形状(task_type +
// 顶层 images[] + metadata.src_ref_images)。聚合编排在归一化之后才跑,
// 手上只有后者。
//
// **判定逻辑不复制一份** —— 这个文件顶部记着同一类错犯过三次的由来。
// 这里只做形状翻译:顺序即语义([0]=首帧、[1]=尾帧),而"一张图到底是首帧
// 还是尾帧"仍然只由 task_type 说了算 —— l2va 和 i2v 的输入形态完全相同,
// 看图数是看不出来的。
func FrameRolesFromTask(taskType string, frameImages, refImages []string) FrameRoles {
	clean := func(in []string) []string {
		var out []string
		for _, u := range in {
			if s := strings.TrimSpace(u); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	frames, refs := clean(frameImages), clean(refImages)
	out := FrameRoles{TaskType: TaskType(strings.ToLower(strings.TrimSpace(taskType)))}

	switch out.TaskType {
	case TaskFLF2V:
		out.Mode = ModeFirstLastFrame
		if len(frames) > 0 {
			out.First = frames[0]
		}
		if len(frames) > 1 {
			out.Last = frames[1]
		}
	case TaskL2VA:
		// 只给尾帧。顶层就一张图,但它是**尾帧** —— 当成首帧会让视频
		// 从结尾往后长,而且不报错。
		out.Mode = ModeFirstLastFrame
		if len(frames) > 0 {
			out.Last = frames[0]
		}
	case TaskI2V:
		out.Mode = ModeFirstLastFrame
		if len(frames) > 0 {
			out.First = frames[0]
		}
	case TaskR2VA:
		out.Mode = ModeReference
		out.Refs = refs
	default:
		out.Mode = ModeTextToVideo
		if out.TaskType == "" {
			out.TaskType = TaskT2V
		}
	}
	return out
}
