package service

import (
	"strings"
	"testing"
)

// 模板的产出形状就是 ACE-Step 吃的东西:一条英文 dense caption、逗号分隔、不带 BPM/调性。
// 它被顺手改写 成别的形状不会有任何报错 —— 只是歌变难听,所以用结构断言守住。
func TestAceStepEnhanceTemplateContract(t *testing.T) {
	for _, want := range []string{
		"ACE-Step",
		"one single line of comma-separated description",
		"Never put BPM, musical key, or time signature in the caption",
		"An explicit instrumental request stays instrumental",
		"Never quote, paraphrase, summarise, or translate the lyrics",
		"Write the caption in English",
		"No headings, no labels",
	} {
		if !strings.Contains(defaultAceStepEnhanceTemplate, want) {
			t.Errorf("模板少了这条约束: %q", want)
		}
	}
}

// 引擎族优先,未声明按模型名兜底 —— 与 Music3 同一套判据。
func TestIsAceStepGeneratingModel(t *testing.T) {
	withMusicEngineConfig(t, `{"models":{
		"ace-aliased":{"engine":"ace-step"},
		"ace-upper":{"engine":"ACE_Step"},
		"ace-named":{"engine":"acestep"},
		"music3-in-disguise":{"engine":"minimax-music3"}
	}}`)

	if !IsAceStepGeneratingModel("ace-aliased") {
		t.Error("engine=ace-step 应判为 ACE-Step")
	}
	if !IsAceStepGeneratingModel("ace-upper") {
		t.Error("engine=ACE_Step 也要认(大小写/下划线)")
	}
	if !IsAceStepGeneratingModel("ace-named") {
		t.Error("engine=acestep 应判为 ACE-Step")
	}
	if IsAceStepGeneratingModel("music3-in-disguise") {
		t.Error("声明为 minimax-music3 时不该判成 ACE-Step")
	}
	// 未声明时按名字兜底(出厂配置靠这条)
	if !IsAceStepGeneratingModel("acestep-v15-xl-turbo") {
		t.Error("未声明引擎族时没有按模型名兜底")
	}
	if IsAceStepGeneratingModel("minimax-music3") {
		t.Error("Music3 不该判成 ACE-Step")
	}
}

// **改写的是描述,不是歌词。** caption 模式取顶层 prompt,sample 模式取 sample_query。
func TestAceStepCaptionTarget(t *testing.T) {
	if f, v := AceStepCaptionTarget("jazz trio, brushed drums", "ignored"); f != "prompt" || v != "jazz trio, brushed drums" {
		t.Errorf("caption 模式应改 prompt, got %q %q", f, v)
	}
	if f, v := AceStepCaptionTarget("  ", "a cheerful song about spring"); f != "sample_query" || v != "a cheerful song about spring" {
		t.Errorf("sample 模式应改 sample_query, got %q %q", f, v)
	}
	if f, _ := AceStepCaptionTarget("", ""); f != "" {
		t.Error("没有描述时应返回空字段名(绝不能用歌词顶上)")
	}
}

// 请求事实要按玩法说清:翻唱/重绘不是从零生成,器乐不该描述歌手,段标签顺序要跟着歌词。
func TestBuildAceStepEnhanceContext(t *testing.T) {
	txt := BuildAceStepEnhanceContext("pop", "", "t2m", false, 1200)
	if !strings.Contains(txt, "text-to-music") || !strings.Contains(txt, "instrumental piece") {
		t.Errorf("无歌词时应声明器乐: %s", txt)
	}
	if !strings.Contains(txt, "at most 1200 characters") {
		t.Errorf("要带字数预算: %s", txt)
	}

	cover := BuildAceStepEnhanceContext("rock", "[Verse] a\n[Chorus] b", "cover", true, 800)
	if !strings.Contains(cover, "COVER") {
		t.Errorf("cover 要说清玩法: %s", cover)
	}
	// 段标签是去掉方括号后的名字(Music3 那条同一套解析)。
	if !strings.Contains(cover, "Verse, Chorus") {
		t.Errorf("要给出歌词里的段标签顺序: %s", cover)
	}
	if !strings.Contains(cover, "not one word of them may appear") {
		t.Errorf("有歌词时必须声明不得引用: %s", cover)
	}
	if !strings.Contains(cover, "source audio file is attached") {
		t.Errorf("有参考音频时要说明: %s", cover)
	}

	inst := BuildAceStepEnhanceContext("lofi", "[Intro]\n[Instrumental]\n[Outro]", "t2m", false, 1200)
	if !strings.Contains(inst, "only section tags") {
		t.Errorf("纯标签歌词应声明器乐: %s", inst)
	}

	repaint := BuildAceStepEnhanceContext("pop", "", "repaint", true, 1200)
	if !strings.Contains(repaint, "REPAINT") {
		t.Errorf("repaint 要说清玩法: %s", repaint)
	}
}
