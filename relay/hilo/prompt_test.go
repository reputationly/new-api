package hilo

import (
	"encoding/json"
	"strings"
	"testing"
)

// **schema 骨架必须是合法 JSON。**
//
// 它整段嵌进提示词给模型照着填。里面少个逗号的话，模型会跟着产出畸形
// JSON —— 而那时的表现是"编译一直失败"，没人会想到是骨架本身坏了。
func TestSkeletonIsValidJSON(t *testing.T) {
	raw := irSkeleton(CompilerInput{DurationSeconds: 6})
	var v map[string]any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatalf("骨架不是合法 JSON：%v", err)
	}
	// 顶层键要和 ContextIR 的 json tag 对得上，否则模型填的字段我们解不出来。
	for _, k := range []string{
		"schema_version", "semantic_plan", "protocol", "subjects",
		"asset_bindings", "reference_relationships", "keyframe_roles",
		"creative_focus", "constraints", "timeline", "audio_plan",
		"generation_description", "intent",
	} {
		if _, ok := v[k]; !ok {
			t.Errorf("骨架缺少顶层键 %q", k)
		}
	}
}

// 骨架能被我们自己的结构体解出来 —— 两边的 json tag 必须一致。
//
// 对不上的话模型照骨架填得再对，我们也拿不到值：字段静默为零值，
// 然后校验器报"缺这个缺那个"，而实际是标签错了。
func TestSkeletonUnmarshalsIntoContextIR(t *testing.T) {
	raw := irSkeleton(CompilerInput{DurationSeconds: 6})
	var ir ContextIR
	if err := json.Unmarshal([]byte(raw), &ir); err != nil {
		t.Fatalf("骨架解不进 ContextIR：%v", err)
	}
	if len(ir.Subjects) == 0 || ir.Subjects[0].SubjectID == "" {
		t.Error("subjects 没解出来 —— json tag 对不上")
	}
	if len(ir.Timeline) == 0 || ir.Timeline[0].ShotID == "" {
		t.Error("timeline 没解出来")
	}
	if len(ir.AssetBindings) == 0 || ir.AssetBindings[0].Role == "" {
		t.Error("asset_bindings 没解出来")
	}
	if ir.Protocol.RewriteLanguage == "" {
		t.Error("protocol 没解出来")
	}
}

// 时长要写进骨架 —— 模型照着填时第一个镜头的 end_seconds 就有了锚点。
func TestSkeletonCarriesDuration(t *testing.T) {
	if !strings.Contains(irSkeleton(CompilerInput{DurationSeconds: 9}), `"end_seconds": 9`) {
		t.Error("骨架里没带上本次的时长")
	}
}

// **素材的角色要给模型，URL 不给。**
//
// 模型看图看不出"这张是尾帧" —— 那是客户端用哪个字段装它决定的，
// 必须显式告诉它。而 URL 写进文字里只会让它把地址抄进描述。
func TestInputGivesRolesNotURLs(t *testing.T) {
	in := CompilerInput{
		UserRequest: "a cat runs", TaskType: TaskL2VA, DurationSeconds: 5,
		Assets: []CompilerAsset{
			{AssetID: "image_1", MediaType: "image", Role: "last_frame", URL: "https://secret.example/x.png"},
		},
	}
	out := BuildCompilerPrompt(in)
	if !strings.Contains(out, "role: last_frame") {
		t.Error("没把角色告诉模型 —— 它推不出这张是尾帧")
	}
	if strings.Contains(out, "secret.example") {
		t.Error("URL 被写进提示词了 —— 模型会把地址抄进描述")
	}
	if !strings.Contains(out, "task.type: l2va") {
		t.Error("没下发 task_type")
	}
}

// 那几条硬约束必须在提示词里。它们是从三份材料逐字对齐出来的。
func TestPromptKeepsTheHardRules(t *testing.T) {
	p := compilerSystemPrompt
	for _, must := range []string{
		// 语言分离 —— 台词被翻译是这一步最难发现的错
		"Understanding language and rewrite language are separate",
		"Preserve the source language",
		// 素材授权边界 —— XINGSHEN2 相对官方 skill 最有价值的补充
		"It can **never** supply observed motion",
		"does **not** authorize that video's camera",
		"are different controls",
		// 时间线
		"no gaps or overlaps, and ends exactly at the requested duration",
		// 不确定性
		"never guess the missing characters",
		// 输出格式
		"exactly one JSON object",
	} {
		if !strings.Contains(p, must) {
			t.Errorf("提示词里缺了「%s」", must)
		}
	}
}
