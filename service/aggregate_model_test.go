package service

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
)

// checkByKey 取某个检查项;不存在返回 nil。
func checkByKey(res *AggregateDryRunResult, key string) *AggregateCheck {
	for i := range res.Checks {
		if res.Checks[i].Key == key {
			return &res.Checks[i]
		}
	}
	return nil
}

func requireLevel(t *testing.T, res *AggregateDryRunResult, key, want string) {
	t.Helper()
	ch := checkByKey(res, key)
	if ch == nil {
		t.Fatalf("缺少检查项 %q,实得 %+v", key, res.Checks)
	}
	if ch.Level != want {
		t.Errorf("检查项 %q 级别应为 %s,实得 %s(%s)", key, want, ch.Level, ch.Message)
	}
}

func boolPtr(b bool) *bool { return &b }

// fakeModel 一个站点上"存在"的模型。
type fakeModel struct {
	groups []string
	caps   []string
}

// withFakeModels 把模型现状换成给定的表,并声明定价缓存可用。
// 没有这个接缝,所有"模型存在"分支(超分能力、分组继承、分组不一致)都测不到 ——
// 而它们恰恰是干跑校验最有价值的部分。
func withFakeModels(t *testing.T, table map[string]fakeModel) {
	t.Helper()
	origFacts, origLoaded := currentModelFacts, pricingLoaded
	t.Cleanup(func() { currentModelFacts, pricingLoaded = origFacts, origLoaded })

	currentModelFacts = func(name string) (bool, []string, []string) {
		m, ok := table[strings.TrimSpace(name)]
		if !ok {
			return false, nil, nil
		}
		return true, m.groups, m.caps
	}
	pricingLoaded = func() bool { return true }
}

// 留空分组时继承生成段模型的可用分组,并把继承结果显式回报 ——
// 配置页要显示继承值而不是一片空白。
func TestDryRunInheritsGroupsFromGenerate(t *testing.T) {
	withFakeModels(t, map[string]fakeModel{
		"gen-model": {groups: []string{"default", "vip"}},
	})

	res := DryRunAggregateModel(&common.AggregateModel{
		Name: "v-agg", Type: "video",
		Generate: common.AggregateGenerate{Model: "gen-model"},
	}, map[string]int{"v-agg": 1})

	if !res.Inherited {
		t.Error("未指定分组时应标记为继承")
	}
	if len(res.Groups) != 2 || res.Groups[0] != "default" {
		t.Errorf("应继承生成段分组,实得 %v", res.Groups)
	}
	requireLevel(t, res, "groups", AggregateCheckOK)
	if !res.Passed {
		t.Errorf("合法配置应通过,实得 %+v", res.Checks)
	}
}

// 聚合模型开放了生成段模型没有渠道的分组 —— 该分组的客户能调进来却必然失败。
func TestDryRunDetectsGroupMismatch(t *testing.T) {
	withFakeModels(t, map[string]fakeModel{
		"gen-model": {groups: []string{"default"}},
	})

	res := DryRunAggregateModel(&common.AggregateModel{
		Name: "v-agg", Type: "video",
		Groups:   []string{"default", "enterprise"},
		Generate: common.AggregateGenerate{Model: "gen-model"},
	}, map[string]int{"v-agg": 1})

	requireLevel(t, res, "groups", AggregateCheckError)
	if !strings.Contains(checkByKey(res, "groups").Message, "enterprise") {
		t.Errorf("应指出具体是哪个分组不可用,实得 %q", checkByKey(res, "groups").Message)
	}
}

// 拿一个没有「视频超分」能力的模型当超分段 —— 这是配置页上最容易点错的一项,
// 而运行时的表现只是超分段失败,看不出是配错了模型。
func TestDryRunRejectsUpscaleModelWithoutCapability(t *testing.T) {
	withFakeModels(t, map[string]fakeModel{
		"gen-model": {groups: []string{"default"}},
		"not-sr":    {groups: []string{"default"}, caps: []string{"图生视频"}},
	})

	res := DryRunAggregateModel(&common.AggregateModel{
		Name: "v-agg", Type: "video",
		Generate: common.AggregateGenerate{Model: "gen-model"},
		Upscale:  &common.AggregateUpscale{Model: "not-sr", Target: "2k"},
	}, map[string]int{"v-agg": 1})

	requireLevel(t, res, "upscale", AggregateCheckError)
	if !strings.Contains(checkByKey(res, "upscale").Message, "视频超分") {
		t.Errorf("应点名缺少的能力,实得 %q", checkByKey(res, "upscale").Message)
	}
}

// 能力标签是中文「视频超分」。写成 "sr" 会让任何模型都通不过,
// 且是"看起来在工作"的那种错误 —— 这里正向钉住它。
func TestDryRunAcceptsUpscaleModelWithCapability(t *testing.T) {
	withFakeModels(t, map[string]fakeModel{
		"gen-model": {groups: []string{"default"}},
		// **字面量,不是 srCapability 常量**:用常量构造输入等于让测试自指 ——
		// 常量被改成 "sr" 时输入也跟着变,断言照样通过,而线上会对所有模型报
		// 「没有超分能力」。这里写死中文标签,才能真正钉住常量的值。
		"seedvr2": {groups: []string{"default"}, caps: []string{"视频超分"}},
	})

	res := DryRunAggregateModel(&common.AggregateModel{
		Name: "v-agg", Type: "video",
		Generate: common.AggregateGenerate{Model: "gen-model"},
		Upscale:  &common.AggregateUpscale{Model: "seedvr2", Target: "2k"},
	}, map[string]int{"v-agg": 1})

	requireLevel(t, res, "upscale", AggregateCheckOK)
	if !res.Passed {
		t.Errorf("合法的超分配置应通过,实得 %+v", res.Checks)
	}
	// 分段计费:三段各自计费,运营配置时就该看到会产生哪几笔。
	if len(res.Billable) != 2 {
		t.Errorf("应报告 2 笔计费(生成 + 超分),实得 %v", res.Billable)
	}
}

// 超分模型在某些分组下没有渠道:生成段仍会计费,超分段却会失败 ——
// 分段计费下这是客户"付了钱没拿到成品"的场景,必须警告。
func TestDryRunWarnsUpscaleUnreachableInSomeGroups(t *testing.T) {
	withFakeModels(t, map[string]fakeModel{
		"gen-model": {groups: []string{"default", "vip"}},
		"seedvr2":   {groups: []string{"default"}, caps: []string{"视频超分"}},
	})

	res := DryRunAggregateModel(&common.AggregateModel{
		Name: "v-agg", Type: "video",
		Generate: common.AggregateGenerate{Model: "gen-model"},
		Upscale:  &common.AggregateUpscale{Model: "seedvr2", Target: "2k"},
	}, map[string]int{"v-agg": 1})

	requireLevel(t, res, "upscale_groups", AggregateCheckWarn)
	if !strings.Contains(checkByKey(res, "upscale_groups").Message, "vip") {
		t.Errorf("应点名不可达的分组,实得 %q", checkByKey(res, "upscale_groups").Message)
	}
}

// 与真实模型重名:同一个名字既指向渠道又指向流水线,路由会有歧义。
func TestDryRunRejectsNameCollisionWithRealModel(t *testing.T) {
	withFakeModels(t, map[string]fakeModel{
		"wan2.2": {groups: []string{"default"}},
	})

	res := DryRunAggregateModel(&common.AggregateModel{
		Name: "wan2.2", Type: "video",
		Generate: common.AggregateGenerate{Model: "wan2.2"},
	}, map[string]int{"wan2.2": 1})

	requireLevel(t, res, "name", AggregateCheckError)
	if !strings.Contains(checkByKey(res, "name").Message, "重名") {
		t.Errorf("应指出与真实模型重名,实得 %q", checkByKey(res, "name").Message)
	}
}

// 定价缓存不可用时,「查不到模型」只能是 warn。
// 报 error 会在 DB 抖动时给运营一屏红色误报,让人去改一份本来没问题的配置。
func TestDryRunDegradesWhenPricingUnavailable(t *testing.T) {
	origFacts, origLoaded := currentModelFacts, pricingLoaded
	t.Cleanup(func() { currentModelFacts, pricingLoaded = origFacts, origLoaded })
	currentModelFacts = func(string) (bool, []string, []string) { return false, nil, nil }
	pricingLoaded = func() bool { return false }

	res := DryRunAggregateModel(&common.AggregateModel{
		Name: "v-agg", Type: "video",
		Generate: common.AggregateGenerate{Model: "gen-model"},
	}, map[string]int{"v-agg": 1})

	requireLevel(t, res, "generate", AggregateCheckWarn)
	if !strings.Contains(checkByKey(res, "generate").Message, "定价缓存为空") {
		t.Errorf("应说明是无法校验而非配置有误,实得 %q", checkByKey(res, "generate").Message)
	}
	if !res.Passed {
		t.Error("缓存不可用不该把一份可能正确的配置判为不通过")
	}
}

// 缓存不可用 + **显式配置了分组**:分组校验同样不能报 error。
//
// 上一条 TestDryRunDegradesWhenPricingUnavailable 走的是"留空继承"分支,
// 覆盖不到这里 —— 分组校验的 genGroups 来自同一次查询,缓存为空时是 nil,
// 照常判定会把每个显式配了分组的模型都判成"所有分组都不可用"并整体不通过。
func TestDryRunGroupCheckDegradesWhenPricingUnavailable(t *testing.T) {
	origFacts, origLoaded := currentModelFacts, pricingLoaded
	t.Cleanup(func() { currentModelFacts, pricingLoaded = origFacts, origLoaded })
	currentModelFacts = func(string) (bool, []string, []string) { return false, nil, nil }
	pricingLoaded = func() bool { return false }

	res := DryRunAggregateModel(&common.AggregateModel{
		Name: "v-agg", Type: "video",
		Groups:   []string{"default", "vip"},
		Generate: common.AggregateGenerate{Model: "gen-model"},
	}, map[string]int{"v-agg": 1})

	requireLevel(t, res, "groups", AggregateCheckWarn)
	if !res.Passed {
		t.Errorf("缓存不可用时不该把显式配了分组的配置判为不通过,实得 %+v", res.Checks)
	}
}

// 分组名两侧的多余空格不该变成一条"该分组无可用渠道"的错误。
// 配置仍是手工编辑的 JSON,而空格在渲染后的消息里看不见 —— 运营会照着错误的方向
// 去查渠道配置,查不出任何问题。
func TestDryRunTrimsGroupNames(t *testing.T) {
	withFakeModels(t, map[string]fakeModel{
		"gen-model": {groups: []string{"default", "vip"}},
	})

	res := DryRunAggregateModel(&common.AggregateModel{
		Name: "v-agg", Type: "video",
		Groups:   []string{" default", "vip ", "  "},
		Generate: common.AggregateGenerate{Model: "gen-model"},
	}, map[string]int{"v-agg": 1})

	requireLevel(t, res, "groups", AggregateCheckOK)
	if !res.Passed {
		t.Errorf("仅多了空格的分组名不该导致校验失败,实得 %+v", res.Checks)
	}
	// 纯空白项应被丢弃,不该混进最终生效的分组里。
	if len(res.Groups) != 2 {
		t.Errorf("应去掉空白项,实得 %v", res.Groups)
	}
}

// 空配置:必填项缺失必须报错,而不是"看起来通过了"。
// 这些是 DryRun 不依赖定价缓存也能给出的结论,故不需要 DB。
func TestDryRunRejectsEmptyConfig(t *testing.T) {
	res := DryRunAggregateModel(&common.AggregateModel{}, map[string]int{"": 1})

	requireLevel(t, res, "name", AggregateCheckError)
	requireLevel(t, res, "type", AggregateCheckError)
	requireLevel(t, res, "generate", AggregateCheckError)
	if res.Passed {
		t.Error("缺必填项的配置不该通过")
	}
}

// 同一份配置里两个同名聚合模型 —— 后写的会覆盖前一个,静默丢配置。
func TestDryRunDetectsDuplicateName(t *testing.T) {
	res := DryRunAggregateModel(
		&common.AggregateModel{Name: "dup", Type: "video", Generate: common.AggregateGenerate{Model: "x"}},
		map[string]int{"dup": 2},
	)
	requireLevel(t, res, "name", AggregateCheckError)
	if !strings.Contains(checkByKey(res, "name").Message, "同名") {
		t.Errorf("应指出同名冲突,实得 %q", checkByKey(res, "name").Message)
	}
}

// 图片类型误配超分段:必须报错。图片流水线只有增强,配了超分要么被静默忽略、
// 要么在执行期炸,两种都不该让它保存。
func TestDryRunRejectsUpscaleOnImageType(t *testing.T) {
	res := DryRunAggregateModel(&common.AggregateModel{
		Name:     "img-agg",
		Type:     "image",
		Generate: common.AggregateGenerate{Model: "some-image-model"},
		Upscale:  &common.AggregateUpscale{Model: "sr-model", Target: "2k"},
	}, map[string]int{"img-agg": 1})

	requireLevel(t, res, "upscale", AggregateCheckError)
	if res.Passed {
		t.Error("图片类型配了超分段不该通过")
	}
}

// 启用超分却没填模型。
func TestDryRunRejectsUpscaleWithoutModel(t *testing.T) {
	res := DryRunAggregateModel(&common.AggregateModel{
		Name:     "v-agg",
		Type:     "video",
		Generate: common.AggregateGenerate{Model: "gen"},
		Upscale:  &common.AggregateUpscale{Target: "2k"},
	}, map[string]int{"v-agg": 1})

	requireLevel(t, res, "upscale", AggregateCheckError)
}

// 关掉「把输入图发给增强模型」必须给出警告 —— 后果隐蔽(增强模型看不到底图会臆造描述,
// 产出与底图打架),不警告的话运营根本不知道自己关掉了什么。
func TestDryRunWarnsWhenInputImagesDisabled(t *testing.T) {
	res := DryRunAggregateModel(&common.AggregateModel{
		Name:     "v-agg",
		Type:     "video",
		Generate: common.AggregateGenerate{Model: "gen"},
		PromptEnhance: &common.AggregatePromptEnhance{
			SendInputImages: boolPtr(false),
		},
	}, map[string]int{"v-agg": 1})

	requireLevel(t, res, "send_input_images", AggregateCheckWarn)
}

// 默认(漏写 send_input_images)不该产生该警告 —— 默认就是开启的。
func TestDryRunNoWarnOnDefaultInputImages(t *testing.T) {
	res := DryRunAggregateModel(&common.AggregateModel{
		Name:          "v-agg",
		Type:          "video",
		Generate:      common.AggregateGenerate{Model: "gen"},
		PromptEnhance: &common.AggregatePromptEnhance{Model: ""},
	}, map[string]int{"v-agg": 1})

	if ch := checkByKey(res, "send_input_images"); ch != nil {
		t.Errorf("默认开启时不该警告,实得 %q", ch.Message)
	}
}

// 停用的超分段给警告而非错误:配置合法,只是客户拿到的是原始分辨率,
// 运营需要知道这件事,但不该被拦着不让保存。
func TestDryRunWarnsOnDisabledUpscale(t *testing.T) {
	res := DryRunAggregateModel(&common.AggregateModel{
		Name:     "v-agg",
		Type:     "video",
		Generate: common.AggregateGenerate{Model: "gen"},
		Upscale:  &common.AggregateUpscale{Enabled: boolPtr(false), Model: "sr", Target: "2k"},
	}, map[string]int{"v-agg": 1})

	requireLevel(t, res, "upscale", AggregateCheckWarn)
}

// 整份配置的查重要跨条目生效。
func TestDryRunConfigCrossEntryDuplicate(t *testing.T) {
	results := DryRunAggregateConfig([]*common.AggregateModel{
		{Name: "same", Type: "video", Generate: common.AggregateGenerate{Model: "a"}},
		{Name: "same", Type: "video", Generate: common.AggregateGenerate{Model: "b"}},
	})
	if len(results) != 2 {
		t.Fatalf("应返回两条结果,实得 %d", len(results))
	}
	for _, r := range results {
		requireLevel(t, r, "name", AggregateCheckError)
	}
}
