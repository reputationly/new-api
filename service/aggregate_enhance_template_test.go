package service

import (
	"strings"
	"testing"
)

// 模板必须按生成段模型挑，认不出的返回空。
//
// 硬套别的模型的模板会让改写结果带着一堆它不认的字段名 —— 而返回空只是
// 降级成"用原始提示词"，效果打折但不会产出错的东西。
func TestDefaultEnhanceTemplateIsModelSpecific(t *testing.T) {
	for _, m := range []string{"minimax-h3-fl2va", "minimax-h3-ref2va", "MiniMax-Hailuo-03"} {
		if DefaultEnhanceTemplate(m) == "" {
			t.Errorf("%s 应当有内置模板", m)
		}
	}
	for _, m := range []string{"qwen-image", "ace-step", "", "seedream"} {
		if DefaultEnhanceTemplate(m) != "" {
			t.Errorf("%s 不该拿到 H3 的模板", m)
		}
	}
}

// 三条硬约束必须在模板里。
//
// 它们是从三份材料逐字对齐出来的（官方 H3 skill、官方客户端的 vendor 卡、
// 官方 TVC 工作流），三处措辞一致：
//
//	preserve dialogue, lyrics, and visible scene text in their original language
//	对白精准保留原文、语言和说话人
//	对白、旁白、品牌名和画内文字保留确认原文
//
// **翻译对白是这一步最容易犯、也最难发现的错**：视频生成出来口型对得上、
// 语言却换了，而用户要的恰恰是那句话。所以钉住它。
func TestTemplateKeepsDialogueInOriginalLanguage(t *testing.T) {
	tpl := DefaultEnhanceTemplate("minimax-h3-fl2va")
	for _, must := range []string{"保留原文", "不要翻译"} {
		if !strings.Contains(tpl, must) {
			t.Errorf("模板里没有「%s」—— 对白会被翻译，而且没人会发现", must)
		}
	}
}

// 输出结构必须是客户端那套「全局基准 →【镜头N】」。
//
// 官方 skill 的多字段结构（integrated_multimodal_description 等）是给
// 直接调 API 的人填表用的；官方客户端通用路径真正在用的是这套分镜结构，
// 而我们这一步做的正是它做的事：把一句话改写成可执行 Prompt。
func TestTemplateUsesShotStructure(t *testing.T) {
	tpl := DefaultEnhanceTemplate("minimax-h3-fl2va")
	for _, must := range []string{"全局基准", "【镜头1】", "景别", "镜头运动"} {
		if !strings.Contains(tpl, must) {
			t.Errorf("模板里没有「%s」", must)
		}
	}
	// 多字段结构不该混进来 —— 两套拼在一起会让改写模型不知道该输出哪种。
	for _, wrong := range []string{"integrated_multimodal_description", "non_diegetic_music"} {
		if strings.Contains(tpl, wrong) {
			t.Errorf("模板里混进了多字段结构的 %s，和分镜结构冲突", wrong)
		}
	}
}

// **素材的授权边界** —— 这是 XINGSHEN2 相对官方 skill 最有价值的补充。
//
// 图片能提供外观/构图/场景/风格/关键帧，但不能提供运动、剪辑节奏、音乐；
// 视频能提供动作/运镜/剪辑，但只在用户授权时，而且"授权了动作"不等于
// "授权了它的切点和镜头数"。
//
// 不写这条的结果是改写模型把参考视频的运镜和切点一并抄进来，而用户只想
// 要那个动作 —— 产出和意图打架，却完全说得通，很难发现。
func TestTemplateSeparatesReferenceDimensions(t *testing.T) {
	tpl := DefaultEnhanceTemplate("minimax-h3-fl2va")
	for _, must := range []string{"不提供运动", "只在用户明确授权时", "没有授权它的运镜"} {
		if !strings.Contains(tpl, must) {
			t.Errorf("模板里没有素材授权边界的「%s」", must)
		}
	}
}

// 三种节奏是不同的控制，不能互相推断。
func TestTemplateSeparatesRhythmControls(t *testing.T) {
	tpl := DefaultEnhanceTemplate("minimax-h3-fl2va")
	if !strings.Contains(tpl, "三种不同的控制") {
		t.Error("模板没区分镜头节奏/剪辑节奏/表演节奏")
	}
}

// 不确定就保留不确定，不要猜。
func TestTemplateKeepsUncertaintyLocal(t *testing.T) {
	tpl := DefaultEnhanceTemplate("minimax-h3-fl2va")
	for _, must := range []string{"保留不确定", "不要编字符"} {
		if !strings.Contains(tpl, must) {
			t.Errorf("模板里没有「%s」—— OCR 不全时会被猜出错字", must)
		}
	}
}

// 用户的话高于素材观察。
func TestTemplatePutsUserAboveObservation(t *testing.T) {
	tpl := DefaultEnhanceTemplate("minimax-h3-fl2va")
	if !strings.Contains(tpl, "用户的话高于素材观察") {
		t.Error("模板没说用户优先 —— 视觉模型会静默覆盖用户的描述")
	}
}

// 素材相关的规则要在模板里：H3 看得见素材，改写时不该复述外观。
func TestTemplateTellsModelNotToRestateAssets(t *testing.T) {
	tpl := DefaultEnhanceTemplate("minimax-h3-fl2va")
	if !strings.Contains(tpl, "不要再用文字复述") {
		t.Error("模板没说「素材里已明确的外观不要复述」—— 会浪费长度且可能和素材打架")
	}
	if !strings.Contains(tpl, "首帧") || !strings.Contains(tpl, "尾帧") {
		t.Error("模板没讲首尾帧的时间角色 —— 只给一张图时下游会当成首帧")
	}
}
