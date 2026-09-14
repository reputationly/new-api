package common

import (
	"strings"
	"testing"
)

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
// hasBuiltinEnhanceTemplate 和 service.DefaultEnhanceTemplate 的判据保持一致。
//
// **不能直接 import service** —— 那会造成 common → service 的反向依赖
// （service 已经 import common）。这里只复制"哪些模型有内置模板"这一个
// 事实，而不是复制模板本身；service 那边加了新模型要记得同步这里，
// 不同步的症状是这个测试放过一个会静默降级的配置。
func hasBuiltinEnhanceTemplate(generateModel string) bool {
	m := strings.ToLower(strings.TrimSpace(generateModel))
	return strings.Contains(m, "h3") || strings.Contains(m, "hailuo")
}

func TestDefaultAggregateShipsNoEnhanceStageUntilTemplatesInherit(t *testing.T) {
	items, err := ParseAggregateModelList(DefaultAggregateModelConfig)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.PromptEnhance == nil {
			continue
		}
		if !it.PromptEnhance.IsEnabled() {
			continue
		}
		// **空 system_prompt 现在是合法的**：它表示"继承内置默认"
		// （service/aggregate_enhance_template.go，按生成段模型挑）。
		// 这一级以前不存在，空模板只能 degrade，所以那时空 = 静默不增强。
		//
		// 但"能继承"是有前提的：生成段模型必须有对应的内置模板。没有的话
		// 还是会降级 —— 名字叫 enhanced、实际用原始提示词，且不报错。
		// 所以判据从"有没有写模板"改成"拿不拿得到模板"。
		if it.PromptEnhance.SystemPrompt == "" && !hasBuiltinEnhanceTemplate(it.Generate.Model) {
			t.Errorf("%s 启用了增强段，既没写 system_prompt，%s 也没有内置默认模板 —— 会静默降级成不增强",
				it.Name, it.Generate.Model)
		}
		if it.PromptEnhance.Model == "" {
			t.Errorf("%s 启用了增强段却没配增强模型 —— 同样会静默降级", it.Name)
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

// 有超分段的聚合必须把生成段**钉在中间尺寸**，而且键名必须是 size。
//
// 没有这条 override 的话，客户传的 size=2K（也就是这个聚合模型名承诺的东西）
// 会原样到达生成段 —— 而自建部署的 H3 上限是 768P，请求原地失败。
//
// # 键名踩过坑
//
// 一开始写的是 `resolution`。applyAggregateExpansion 改写的是 Distribute 里
// 那份**统一任务契约**的 body（prompt/model/images/size/…），H3 也从
// body["size"] 取档位词（h3ApplyCanvas → h3ShortEdgeFromSizeToken）。
// `resolution` 在这条路上没人读 —— 覆盖静默落空，不报错。
//
// `resolution` 是官方形状那条 /v2/video_generation 上的字段名，但那条路的
// MiniMaxV2CreateConvert 跑在 Distribute 之前，自己就把它转成 size 了。
func TestDefaultAggregatePinsGenerateStageWithSizeKey(t *testing.T) {
	items, err := ParseAggregateModelList(DefaultAggregateModelConfig)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.Upscale == nil || !it.Upscale.IsEnabled() {
			// 这一支由 TestDefaultAggregateVideoEntriesHaveUpscale 守着:
			// 视频聚合必须有启用的超分段。这里不重复表达，但也不能因此
			// 让本用例在"超分段都没了"时变成空转 —— 那个组合正是
			// pipelineCannotDeliver 要防的静默降级。
			continue
		}
		ov := it.Generate.Overrides
		if ov == nil {
			t.Errorf("%s 有超分段却没有 generate.overrides —— 客户传的最终尺寸会直接打到生成段", it.Name)
			continue
		}
		if _, ok := ov["size"]; !ok {
			keys := make([]string, 0, len(ov))
			for k := range ov {
				keys = append(keys, k)
			}
			t.Errorf("%s 的 overrides 里没有 size（有的是 %v）—— 生成段读的是 body[\"size\"]，别的键没人读",
				it.Name, keys)
		}
		if _, wrong := ov["resolution"]; wrong {
			t.Errorf("%s 的 overrides 用了 resolution —— 这条路上没人读它，覆盖会静默落空", it.Name)
		}
	}
}
