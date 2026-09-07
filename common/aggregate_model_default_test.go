package common

import "testing"

// 出厂配置必须能被自己的解析器吃下去。看着显然,但它是**代码里的字符串字面量**,
// 编译器不校验 JSON 结构 —— 手滑少个逗号要到运行时才发现,那时的表现是
// 「聚合模型全部消失、集成方拿到 404」,日志里只有一行解析失败。
func TestDefaultAggregateModelConfigParses(t *testing.T) {
	items, err := ParseAggregateModelList(DefaultAggregateModelConfig)
	if err != nil {
		t.Fatalf("出厂配置解析失败: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("出厂配置解析出 0 条")
	}
	for _, it := range items {
		if it.Name == "" {
			t.Error("有条目缺 name")
		}
		if it.Generate.Model == "" {
			t.Errorf("%s 缺生成段模型", it.Name)
		}
		if it.Type != "image" && it.Type != "video" {
			t.Errorf("%s 的 type 非法: %q", it.Name, it.Type)
		}
	}
}

// 出厂条目不能带增强段:模板继承尚未实现,空模板会让 EnhancePrompt 静默降级 ——
// 配置上写着 enhanced、实际用原始提示词生成、且不报错。这条守的就是
// 「出厂不发一个看着开了实际没开的功能」。
//
// 等模板继承实现后,这条测试应改成「带增强的条目必须有 system_prompt」。
func TestDefaultAggregateShipsNoEnhanceStageUntilTemplatesInherit(t *testing.T) {
	items, err := ParseAggregateModelList(DefaultAggregateModelConfig)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.PromptEnhance == nil {
			continue
		}
		if it.PromptEnhance.IsEnabled() && it.PromptEnhance.SystemPrompt == "" {
			t.Errorf("%s 启用了增强段却没有 system_prompt —— 会静默降级成不增强", it.Name)
		}
	}
}

// 每一条视频聚合都必须有启用的超分段。没有超分的视频聚合等于"绕一圈还是裸模型",
// 而集成方是冲着"一个模型名拿到 2K"来的。
func TestDefaultAggregateVideoEntriesHaveUpscale(t *testing.T) {
	items, err := ParseAggregateModelList(DefaultAggregateModelConfig)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, it := range items {
		if it.Type != "video" {
			continue
		}
		seen++
		// nil 要先拦下再 continue:IsEnabled 有 nil 接收者保护,下面取 .Model 没有。
		// t.Errorf 不中断,于是这个测试要抓的那个回归(缺超分段)反而会变成
		// 空指针 panic,把整个测试二进制打断、连带掩盖本包其余失败。
		if it.Upscale == nil || !it.Upscale.IsEnabled() {
			t.Errorf("%s 是视频聚合却没有启用的超分段", it.Name)
			continue
		}
		if it.Upscale.Model == "" || it.Upscale.Target == "" {
			t.Errorf("%s 的超分段缺 model 或 target_size", it.Name)
		}
	}
	if seen == 0 {
		t.Fatal("出厂配置里没有视频聚合;那是这份默认值存在的唯一理由")
	}
}
