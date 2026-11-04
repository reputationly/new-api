package service

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
)

// 音乐聚合的干跑校验。守的是「配错了但跑起来不报错」那几类:
// 指错引擎族(描述在哪个字段会反)、开了超分段、覆盖了增强要读写的字段。

func musicAggregate() *common.AggregateModel {
	return &common.AggregateModel{
		Name:    "music3-enhanced",
		Type:    "music",
		Enabled: true,
		PromptEnhance: &common.AggregatePromptEnhance{
			Model: "enh",
		},
		Generate: common.AggregateGenerate{Model: "my-music"},
	}
}

func TestDryRunAcceptsMusic3Aggregate(t *testing.T) {
	withFakeModels(t, map[string]fakeModel{
		"my-music": {groups: []string{"default"}},
		"enh":      {groups: []string{"default"}},
	})
	withMusicEngineConfig(t, `{"models":{"my-music":{"engine":"minimax-music3"}}}`)

	res := DryRunAggregateModel(musicAggregate(), nil)
	// 合法配置下 type / enhance_template 根本不会产生检查项(它们只在出问题时出现),
	// 所以这里断言的是"没有 error",而不是"有一条 ok"。
	requireNoError(t, res, "type")
	requireNoError(t, res, "enhance_template")
	requireLevel(t, res, "generate", AggregateCheckOK)
	// 音乐没有图片输入,传图那两条检查不该出现(出现了运营会去找一个不存在的开关)
	if ch := checkByKey(res, "enhance_vision"); ch != nil {
		t.Errorf("音乐聚合不该报 enhance_vision: %s", ch.Message)
	}
	if ch := checkByKey(res, "send_input_images"); ch != nil {
		t.Errorf("音乐聚合不该报 send_input_images: %s", ch.Message)
	}
}

// 未声明引擎族:运行时按模型名兜底,所以这是 warn 而不是 error ——
// 出厂配置里的 minimax-music3 在刚部署的站点上通常还没声明过,报 error 会让它一出生就红。
func TestDryRunWarnsMusicWithoutDeclaredEngine(t *testing.T) {
	withFakeModels(t, map[string]fakeModel{
		"my-music": {groups: []string{"default"}},
		"enh":      {groups: []string{"default"}},
	})
	withMusicEngineConfig(t, `{}`)

	res := DryRunAggregateModel(musicAggregate(), nil)
	requireLevel(t, res, "generate", AggregateCheckWarn)
	requireContains(t, checkByKey(res, "generate").Message, "引擎族")
}

// 指到 ACE-Step:字段语义相反(prompt=描述、metadata.lyrics=歌词),接上去增强会写到
// 引擎不读的字段上,钱花了、描述没动、不报错。
func TestDryRunRejectsMusicPointedAtACEState(t *testing.T) {
	withFakeModels(t, map[string]fakeModel{
		"my-music": {groups: []string{"default"}},
		"enh":      {groups: []string{"default"}},
	})
	withMusicEngineConfig(t, `{"models":{"my-music":{"engine":"acestep"}}}`)

	res := DryRunAggregateModel(musicAggregate(), nil)
	requireLevel(t, res, "generate", AggregateCheckError)
	requireContains(t, checkByKey(res, "generate").Message, "ACE-Step")
}

func TestDryRunRejectsMusicUpscale(t *testing.T) {
	withFakeModels(t, map[string]fakeModel{
		"my-music": {groups: []string{"default"}},
		"enh":      {groups: []string{"default"}},
	})
	withMusicEngineConfig(t, `{"models":{"my-music":{"engine":"minimax-music3"}}}`)

	m := musicAggregate()
	m.Upscale = &common.AggregateUpscale{Model: "relayvsr", Target: "2k"}
	res := DryRunAggregateModel(m, nil)
	requireLevel(t, res, "upscale", AggregateCheckError)
}

// overrides 覆盖 prompt/instructions/metadata:增强读写的就是这几个字段,
// 覆盖之后增强与生成段看到的不是同一份内容。
func TestDryRunRejectsMusicOverridesOnEnhanceFields(t *testing.T) {
	withFakeModels(t, map[string]fakeModel{
		"my-music": {groups: []string{"default"}},
		"enh":      {groups: []string{"default"}},
	})
	withMusicEngineConfig(t, `{"models":{"my-music":{"engine":"minimax-music3"}}}`)

	m := musicAggregate()
	m.Generate.Overrides = map[string]any{"prompt": "x"}
	res := DryRunAggregateModel(m, nil)
	requireLevel(t, res, "generate", AggregateCheckError)
	requireContains(t, checkByKey(res, "generate").Message, "overrides")
}

// 只有 text 对音乐有意义:别的模式不会更糟(会回退),但也不会带来任何提升,
// 而运营会以为"我开了更强的模式"。
func TestDryRunWarnsMusicWithNonTextMode(t *testing.T) {
	withFakeModels(t, map[string]fakeModel{
		"my-music": {groups: []string{"default"}},
		"enh":      {groups: []string{"default"}},
	})
	withMusicEngineConfig(t, `{"models":{"my-music":{"engine":"minimax-music3"}}}`)

	m := musicAggregate()
	m.PromptEnhance.Mode = common.EnhanceModeSingleCall
	res := DryRunAggregateModel(m, nil)
	requireLevel(t, res, "enhance_mode", AggregateCheckWarn)
}

// type 的合法取值现在有三个,报错文案要跟着说全 —— 否则运营会照着"image 或 video"
// 去查一个根本不存在的第三个选项。
func TestDryRunTypeMessageMentionsMusic(t *testing.T) {
	withFakeModels(t, map[string]fakeModel{})
	withMusicEngineConfig(t, `{}`)

	m := musicAggregate()
	m.Type = "audio"
	res := DryRunAggregateModel(m, nil)
	requireLevel(t, res, "type", AggregateCheckError)
	requireContains(t, checkByKey(res, "type").Message, "music")
}

func requireContains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Errorf("消息里应包含 %q,实得 %q", want, got)
	}
}

// requireNoError 某个检查项要么不存在(合法配置不产生检查项),要么不是 error。
func requireNoError(t *testing.T, res *AggregateDryRunResult, key string) {
	t.Helper()
	if ch := checkByKey(res, key); ch != nil && ch.Level == AggregateCheckError {
		t.Errorf("检查项 %q 不该报 error: %s", key, ch.Message)
	}
}
