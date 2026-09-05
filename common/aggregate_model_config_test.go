package common

import "testing"

// withAggregateConfig 装配一份聚合模型配置,测试结束还原。
func withAggregateConfig(t *testing.T, raw string) {
	t.Helper()
	OptionMapRWMutex.Lock()
	// 生产里由 InitOptionMap 建表,单测不跑那条初始化路径。
	if OptionMap == nil {
		OptionMap = make(map[string]string)
	}
	orig, had := OptionMap["AggregateModelConfig"]
	OptionMap["AggregateModelConfig"] = raw
	OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		OptionMapRWMutex.Lock()
		if had {
			OptionMap["AggregateModelConfig"] = orig
		} else {
			delete(OptionMap, "AggregateModelConfig")
		}
		OptionMapRWMutex.Unlock()
		// 缓存按 raw 比对失效,还原后主动取一次,避免把测试配置留给后续用例。
		GetAggregateModels()
	})
}

// 不写 hidden 字段 = 隐藏。聚合模型是给指定集成方的定向能力,配置里没写这个字段的人
// 要的一定是隐藏,而不是"忘了配所以公开了"。
func TestAggregateModelHiddenByDefault(t *testing.T) {
	withAggregateConfig(t, `[{"name":"agg-a","type":"video","enabled":true}]`)

	m := GetAggregateModel("agg-a")
	if m == nil {
		t.Fatal("应解析出 agg-a")
	}
	if !m.IsHidden() {
		t.Error("未显式配置 hidden 时应视为隐藏")
	}
	if !IsAggregateModelHidden("agg-a") {
		t.Error("IsAggregateModelHidden 应为 true")
	}
}

// 显式 hidden:false 时公开 —— 聚合能力本身也可以是对外产品。
func TestAggregateModelExplicitlyPublic(t *testing.T) {
	withAggregateConfig(t, `[{"name":"agg-pub","type":"image","enabled":true,"hidden":false}]`)

	if IsAggregateModelHidden("agg-pub") {
		t.Error("显式 hidden:false 不应被隐藏")
	}
	if got := FilterHiddenAggregateModels([]string{"agg-pub", "gpt-4o"}); len(got) != 2 {
		t.Errorf("公开的聚合模型不应被过滤掉,得到 %v", got)
	}
}

// enabled:false 的配置视同不存在:既不参与路由,也不必判隐藏。
func TestAggregateModelDisabledIsAbsent(t *testing.T) {
	withAggregateConfig(t, `[{"name":"agg-off","type":"video","enabled":false}]`)

	if GetAggregateModel("agg-off") != nil {
		t.Error("enabled:false 不应出现在解析结果里")
	}
	if IsAggregateModelHidden("agg-off") {
		t.Error("不存在的聚合模型不该被当成隐藏项")
	}
}

// 配置坏掉时按「没有聚合模型」降级:集成方拿到 404 是安全的失败,
// 而半份配置会让流水线以缺字段的形态跑起来。
func TestAggregateModelBrokenConfigDegrades(t *testing.T) {
	withAggregateConfig(t, `{ not valid json`)

	if n := len(GetAggregateModels()); n != 0 {
		t.Errorf("坏配置应降级为空表,得到 %d 项", n)
	}
}

// **部分**坏掉的配置也必须整份丢弃,不能采用已解析出来的前几项。
//
// 与上一条的区别很关键:完全无法解析时 items 本来就是空的,丢不丢都一样;而数组前段
// 合法、后段坏掉时,JSON 库可能已经把前几项填进 items —— 此时若不整份丢弃,就会拿
// 半份配置把流水线跑起来(比如缺了 upscale 段的视频聚合模型,静默少一段)。
// 宁可整份不生效让集成方拿到 404,那是能被立刻发现的失败。
func TestAggregateModelPartiallyBrokenConfigDiscardsAll(t *testing.T) {
	withAggregateConfig(t, `[{"name":"agg-ok","type":"video","enabled":true},{"name":]`)

	if m := GetAggregateModel("agg-ok"); m != nil {
		t.Error("配置后半段坏掉时,前段已解析的项也不该生效")
	}
	if n := len(GetAggregateModels()); n != 0 {
		t.Errorf("部分坏配置应整份降级为空表,得到 %d 项", n)
	}
}

// 漏写 bool 时必须落到「安全的那一侧」,而安全的方向取决于失败可见性:
// 漏写导致静默劣化的(增强/超分/传图)默认开,漏写导致立刻可见失败的(顶层 Enabled)默认关。
// 这两类方向相反,任何一边写反了都不会报错 —— 只会默默出差档,或默默公开一个能力。
func TestAggregateStageBoolDefaults(t *testing.T) {
	// 只给段的必要字段,三个 bool 全部漏写。
	withAggregateConfig(t, `[{
		"name":"agg-defaults","type":"video","enabled":true,
		"prompt_enhance":{"model":"gpt-4o-mini"},
		"upscale":{"model":"seedvr2-3b","target_size":"2k"}
	}]`)

	m := GetAggregateModel("agg-defaults")
	if m == nil {
		t.Fatal("应解析出 agg-defaults")
	}
	if !m.PromptEnhance.IsEnabled() {
		t.Error("prompt_enhance 段存在但漏写 enabled,应视为启用")
	}
	if !m.PromptEnhance.IsSendInputImages() {
		t.Error("漏写 send_input_images 应视为开启 —— 关掉会让增强模型主动编错,与底图打架")
	}
	if !m.Upscale.IsEnabled() {
		t.Error("upscale 段存在但漏写 enabled,应视为启用")
	}
}

// 显式 false 必须被尊重:段还在(配置页上是「关一个开关」而不是「删掉整段」)。
func TestAggregateStageExplicitFalseRespected(t *testing.T) {
	withAggregateConfig(t, `[{
		"name":"agg-off","type":"video","enabled":true,
		"prompt_enhance":{"model":"m","enabled":false,"send_input_images":false},
		"upscale":{"model":"sr","enabled":false}
	}]`)

	m := GetAggregateModel("agg-off")
	if m == nil {
		t.Fatal("应解析出 agg-off")
	}
	if m.PromptEnhance.IsEnabled() {
		t.Error("显式 enabled:false 应停用增强段")
	}
	if m.PromptEnhance.IsSendInputImages() {
		t.Error("显式 send_input_images:false 应生效")
	}
	if m.Upscale.IsEnabled() {
		t.Error("显式 enabled:false 应停用超分段")
	}
	// 但配置本身还在 —— 与「删掉整段」是两回事。
	if m.Upscale == nil || m.Upscale.Model != "sr" {
		t.Error("停用不等于删除,段配置应保留")
	}
}

// 段不存在时一律不启用,且 nil 接收者不得 panic(配置页允许只配生成段)。
func TestAggregateAbsentStagesAreDisabled(t *testing.T) {
	withAggregateConfig(t, `[{"name":"agg-bare","type":"image","enabled":true,"generate":{"model":"x"}}]`)

	m := GetAggregateModel("agg-bare")
	if m == nil {
		t.Fatal("应解析出 agg-bare")
	}
	if m.PromptEnhance.IsEnabled() || m.PromptEnhance.IsSendInputImages() {
		t.Error("未配置增强段时应为不启用")
	}
	if m.Upscale.IsEnabled() {
		t.Error("未配置超分段时应为不启用")
	}
}

func TestFilterHiddenAggregateModels(t *testing.T) {
	withAggregateConfig(t, `[
		{"name":"agg-hidden","type":"video","enabled":true},
		{"name":"agg-open","type":"video","enabled":true,"hidden":false}
	]`)

	got := FilterHiddenAggregateModels([]string{"gpt-4o", "agg-hidden", "agg-open", "wan2.2"})
	for _, want := range []string{"gpt-4o", "agg-open", "wan2.2"} {
		found := false
		for _, g := range got {
			if g == want {
				found = true
			}
		}
		if !found {
			t.Errorf("%q 不该被过滤,得到 %v", want, got)
		}
	}
	for _, g := range got {
		if g == "agg-hidden" {
			t.Errorf("隐藏的聚合模型应被过滤,得到 %v", got)
		}
	}
	// 顺序保持不变:调用方(如 /v1/models)依赖它。
	if len(got) != 3 || got[0] != "gpt-4o" {
		t.Errorf("应保持原有顺序,得到 %v", got)
	}
}

// 没有任何隐藏聚合模型时走短路,原切片原样返回(绝大多数站点的常态)。
func TestFilterHiddenNoopWhenNoneConfigured(t *testing.T) {
	withAggregateConfig(t, ``)

	in := []string{"gpt-4o", "wan2.2"}
	if got := FilterHiddenAggregateModels(in); len(got) != 2 {
		t.Errorf("无聚合模型时不应改动列表,得到 %v", got)
	}
	if HasHiddenAggregateModels() {
		t.Error("未配置时不应报告存在隐藏聚合模型")
	}
}
