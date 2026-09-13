package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting"
)

// 蒜狸客户端的目录要能用聚合模型，**包括隐藏的那些**。
//
// `Hidden` 管的是"不出现在对外的模型列表里"（模型广场 / /v1/models），
// 而客户端取目录不是对外列表 —— 是我们自己的应用按管理员配置拿可用模型。
// 过滤掉的话，定向发放的编排能力（H3 生成 → SwiftVR 超分）永远用不上,
// 而那恰恰是画质追平官方的唯一途径。
//
// 反过来的风险（聚合模型漏进模型广场）由 aggregate_model_hidden_test.go
// 守着，两边是相反方向的约束，不要合并。
func TestHiloCatalogSeesHiddenAggregateModels(t *testing.T) {
	// availableForHilo 第一步就查 abilities 表。没有这个夹具的话
	// `model.DB` 是 nil，GORM 里直接空指针 panic ——
	// **全包跑能过纯属巧合**：字母序更靠前的 aggregate_model_hidden_test.go
	// 会把 DB 指到一个已关闭的 SQLite 句柄上，于是查询静默返回空。
	setupModelListControllerTestDB(t)
	withAggregateModelConfig(t, `[
      {"name":"minimax-h3-2k","type":"video","enabled":true,
       "generate":{"model":"minimax-h3-fl2va"},
       "upscale":{"model":"swiftvr","target_size":"2k"}}
    ]`)

	m := common.GetAggregateModel("minimax-h3-2k")
	if m == nil {
		t.Fatal("聚合配置没生效")
	}
	if !m.IsHidden() {
		t.Fatal("这条应当是隐藏的（Hidden 默认 true），否则本用例没在测想测的东西")
	}

	if !availableForHilo()["minimax-h3-2k"] {
		t.Error("隐藏的聚合模型没进蒜狸目录的可用集合 —— 2K 编排会静默消失")
	}
}

// 停用的聚合模型不能进目录。
//
// 停用意味着那条流水线不跑了，还报出去的话客户端上那个模型点了就失败,
// 而失败信息来自渠道层，说不清"编排被关了"。
func TestHiloCatalogSkipsDisabledAggregate(t *testing.T) {
	setupModelListControllerTestDB(t)
	withAggregateModelConfig(t, `[
      {"name":"minimax-h3-2k","type":"video","enabled":false,
       "generate":{"model":"minimax-h3-fl2va"},
       "upscale":{"model":"swiftvr","target_size":"2k"}}
    ]`)

	if availableForHilo()["minimax-h3-2k"] {
		t.Error("停用的聚合模型不该出现在目录的可用集合里")
	}
}

// 老部署的场景：库里存着**没有 overrides 的旧版**聚合配置，而目录（代码）
// 已经升级成只报 2K。这种组合下 2K 兑现不了，不能报出去。
//
// 出厂目录跟着二进制升级，出厂的聚合配置只是种子、会被库里的值盖掉 ——
// 两者不同步是真实可达的状态，不是假设。
func TestHiloSkipsPipelineThatCannotDeliver(t *testing.T) {
	setupModelListControllerTestDB(t)
	withAggregateModelConfig(t, `[
      {"name":"minimax-h3-2k","type":"video","enabled":true,
       "generate":{"model":"minimax-h3-fl2va"},
       "upscale":{"model":"swiftvr","target_size":"2k"}}
    ]`)

	if why := pipelineCannotDeliver("minimax-h3-2k"); why == "" {
		t.Error("缺 generate.overrides.size 的流水线应当被判为兑现不了")
	}
}

// 补上 overrides 之后立刻恢复 —— 这就是用运行时守卫而不是迁移的意义：
// 配置修好的那一刻自动生效，不需要谁再跑一次什么。
func TestHiloAcceptsPipelineWithSizeOverride(t *testing.T) {
	setupModelListControllerTestDB(t)
	withAggregateModelConfig(t, `[
      {"name":"minimax-h3-2k","type":"video","enabled":true,
       "generate":{"model":"minimax-h3-fl2va","overrides":{"size":"768P"}},
       "upscale":{"model":"swiftvr","target_size":"2k"}}
    ]`)

	if why := pipelineCannotDeliver("minimax-h3-2k"); why != "" {
		t.Errorf("补上 overrides 之后不该再被拦：%s", why)
	}
}

// 裸模型（不是聚合）不受这条守卫影响。
func TestHiloGuardIgnoresPlainModels(t *testing.T) {
	setupModelListControllerTestDB(t)
	withAggregateModelConfig(t, `[]`)
	if why := pipelineCannotDeliver("qwen-image"); why != "" {
		t.Errorf("裸模型不该被这条守卫拦：%s", why)
	}
}

// 目录项依赖的**全部**平台模型都要查，不只是 PlatformModel。
//
// H3 那条同时支持 reference 和 first-last-frame，而这两种在我们平台上是
// 两个 checkpoint、两条流水线。只查一条的话，另一条停掉时目录照样报出
// 这个模型 —— 用户切到那个玩法才失败，失败信息还来自渠道层。
func TestHiloEntryDependsOnAllModeModels(t *testing.T) {
	e := setting.HiloCatalogEntry{
		PlatformModel: "minimax-h3-2k",
		ModeModels: map[string]string{
			"reference":        "minimax-h3-ref-2k",
			"first-last-frame": "minimax-h3-2k",
		},
	}
	got := e.PlatformModelsOf()
	if len(got) != 2 {
		t.Fatalf("应当依赖两个平台模型，实际 %v", got)
	}
	has := map[string]bool{}
	for _, m := range got {
		has[m] = true
	}
	if !has["minimax-h3-2k"] || !has["minimax-h3-ref-2k"] {
		t.Errorf("依赖集合不对：%v", got)
	}
}

// 没配 ModeModels 时退化成只依赖 PlatformModel，不能返回空。
func TestHiloEntryWithoutModeModels(t *testing.T) {
	e := setting.HiloCatalogEntry{PlatformModel: "qwen-image"}
	got := e.PlatformModelsOf()
	if len(got) != 1 || got[0] != "qwen-image" {
		t.Errorf("应当只依赖 qwen-image，实际 %v", got)
	}
}
