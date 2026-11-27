package common

import (
	"testing"
)

func TestAggregateGenerateModelFor(t *testing.T) {
	gen := AggregateGenerate{
		Model: "frames-checkpoint",
		TaskModels: map[string]string{
			"t2v":   "frames-checkpoint",
			"i2v":   " frames-checkpoint ",
			"flf2v": "frames-checkpoint",
			"l2va":  "frames-checkpoint",
			"r2va":  "ref-checkpoint",
		},
	}

	cases := []struct {
		taskType string
		want     string
	}{
		{"r2va", "ref-checkpoint"},
		{"R2VA", "ref-checkpoint"},
		{"r2v", "ref-checkpoint"},             // 参考族别名：语义就是 r2va
		{"rv2v", "ref-checkpoint"},            // 同上
		{"continuation", "frames-checkpoint"}, // 续写最终是一次 i2v
		{"i2v", "frames-checkpoint"},          // key 与 value 两侧的空白都要 trim
		{"t2v", "frames-checkpoint"},
		{"s2v", "frames-checkpoint"}, // 不在五个玩法里：回落 model，不猜
		{"", "frames-checkpoint"},    // 说不出玩法：回落 model
	}
	for _, tc := range cases {
		if got := gen.ModelFor(tc.taskType); got != tc.want {
			t.Errorf("ModelFor(%q) = %q, want %q", tc.taskType, got, tc.want)
		}
	}

	// 没配 task_models 时一律用 model ——「与没有这个字段时完全一致」。
	plain := AggregateGenerate{Model: "only"}
	for _, tt := range []string{"r2va", "t2v", "", "continuation"} {
		if got := plain.ModelFor(tt); got != "only" {
			t.Errorf("未配 task_models 时 ModelFor(%q) = %q, want %q", tt, got, "only")
		}
	}

	// 配了 task_models 但某个玩法没覆盖 → 回落 model（并由此产生 dry-run 的 warn）。
	partial := AggregateGenerate{Model: "def", TaskModels: map[string]string{"r2va": "ref"}}
	if got := partial.ModelFor("t2v"); got != "def" {
		t.Errorf("未覆盖的玩法应回落默认段，实得 %q", got)
	}

	// 值写成空串等于没配，不能把空模型名发给上游。
	blank := AggregateGenerate{Model: "def", TaskModels: map[string]string{"r2va": "  "}}
	if got := blank.ModelFor("r2va"); got != "def" {
		t.Errorf("空 value 应回落默认段，实得 %q", got)
	}
}

func TestAggregateGenerateAllModels(t *testing.T) {
	gen := AggregateGenerate{
		Model: "frames",
		TaskModels: map[string]string{
			"t2v":  "frames",
			"i2v":  " frames ",
			"r2va": "ref",
			"l2va": "ref",
		},
	}
	got := gen.AllModels()
	want := []string{"frames", "ref"} // model 在前，其余按 key 排序去重
	if len(got) != len(want) {
		t.Fatalf("AllModels() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("AllModels() = %v, want %v（顺序也必须稳定，能力目录的版本哈希依赖它）", got, want)
		}
	}

	// 空配置只回 model；model 为空时返回空切片（不 panic、不含空串）。
	if got := (AggregateGenerate{Model: "only"}).AllModels(); len(got) != 1 || got[0] != "only" {
		t.Errorf("未配 task_models 时 AllModels() = %v", got)
	}
	if got := (AggregateGenerate{}).AllModels(); len(got) != 0 {
		t.Errorf("空配置应返回空，实得 %v", got)
	}
}

func TestNormalizeTaskTypeKeys(t *testing.T) {
	want := []string{"t2v", "i2v", "l2va", "flf2v", "r2va"}
	got := FacadeTaskTypeKeys()
	if len(got) != len(want) {
		t.Fatalf("FacadeTaskTypeKeys() = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("FacadeTaskTypeKeys() = %v, want %v", got, want)
		}
		if !IsFacadeTaskTypeKey(want[i]) {
			t.Errorf("IsFacadeTaskTypeKey(%q) 应为真", want[i])
		}
	}
	if IsFacadeTaskTypeKey("continuation") || IsFacadeTaskTypeKey("s2v") {
		t.Error("续写与数字人不是门面玩法，不能进 task_models 的 key 白名单")
	}
	// 别名也不是合法的 **key**：运行时的查找键是归一后的 r2va，写 r2v 永远不命中。
	if IsFacadeTaskTypeKey("r2v") || IsFacadeTaskTypeKey("rv2v") {
		t.Error("参考族别名可以出现在请求里，但不能出现在 task_models 的 key 上")
	}
	if !IsFacadeTaskTypeKey(" R2VA ") {
		t.Error("大小写与空白应被容忍")
	}

	// 路由口径：参考族三种写法 + 续写都归一到五个玩法之一。
	for raw, want := range map[string]string{
		"r2va": "r2va", "R2V": "r2va", " rv2v ": "r2va",
		"continuation": "i2v", "Continuation": "i2v",
		"t2v": "t2v", "FLF2V": "flf2v", "l2va": "l2va",
	} {
		if got, ok := NormalizeRoutedTaskType(raw); !ok || got != want {
			t.Errorf("NormalizeRoutedTaskType(%q) = (%q,%v), want %q", raw, got, ok, want)
		}
	}
	if _, ok := NormalizeRoutedTaskType("s2v"); ok {
		t.Error("s2v 不该被归一成门面玩法")
	}

	// 编译口径**不认续写**：首帧在那时还不存在，按 i2v 编出来的分镜描述的是
	// 一张不存在的图。这条分歧是刻意的，别为了"统一"把它抹掉。
	if _, ok := NormalizeCompilerTaskType("continuation"); ok {
		t.Error("编译口径不该认续写")
	}
	if got, ok := NormalizeCompilerTaskType("r2v"); !ok || got != "r2va" {
		t.Errorf("编译口径仍要认 r2v 别名，实得 (%q,%v)", got, ok)
	}
}
