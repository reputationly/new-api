package ovcaps

import (
	"math"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/setting"
)

func h3EntryWithModes(modes ...string) setting.HiloCatalogEntry {
	return setting.HiloCatalogEntry{
		PlatformModel: "minimax-h3-2k",
		Model: dto.HiloMediaModel{
			ID: "MiniMax-H3-FL", ModelName: "minimax-h3-fl2va", Type: "video",
			Params: map[string]dto.HiloModelParam{
				"image_mode": {Type: "select", Options: modes},
				"duration":   {Type: "select", Options: []string{"4", "5", "10", "15"}},
			},
		},
	}
}

func withH3Engine(t *testing.T) {
	t.Helper()
	withPlaygroundConfig(t, "VideoModelConfig", `{"models":{"minimax-h3-fl2va":{"engine":"minimax-h3"}}}`)
}

// 四种玩法各自的输入契约必须与网关真实校验一致：首尾帧只吃 2 张图、参考族 9/3/3、
// 续写只吃 1 段视频。任何一处放宽，客户端就会照着填然后在提交时 400。
func TestModesOfH3CoversEveryPlaystyle(t *testing.T) {
	withH3Engine(t)
	modes := modesOf(h3EntryWithModes("reference", "first-last-frame", "video-extension", "text-to-video"),
		"minimax-h3-2k", "minimax-h3-fl2va", setting.TransportVideosTask)

	byID := map[string]Mode{}
	for _, m := range modes {
		byID[m.ID] = m
	}
	if len(byID) != 4 {
		t.Fatalf("应报出 4 个玩法，实际 %+v", byID)
	}

	t2v := byID["text-to-video"]
	if t2v.TaskType != "t2v" || t2v.References.Image.Max != 0 {
		t.Fatalf("文生视频不该吃参考图: %+v", t2v)
	}

	flf := byID["first-last-frame"]
	if flf.TaskType != "flf2v" || flf.TaskTypeIfSingleImage != "i2v" {
		t.Fatalf("首尾帧的 task_type 规则不对: %+v", flf)
	}
	if flf.References.Image.Min != 1 || flf.References.Image.Max != 2 {
		t.Fatalf("首尾帧图数应为 1–2: %+v", flf.References.Image)
	}

	ref := byID["reference"]
	if ref.TaskType != "r2va" {
		t.Fatalf("参考族的 task_type 应为 r2va: %+v", ref)
	}
	if ref.References.Image.Max != 9 || ref.References.Video.Max != 3 || ref.References.Audio.Max != 3 {
		t.Fatalf("参考族上限应为 9/3/3: %+v", ref.References)
	}

	ext := byID["video-extension"]
	if ext.TaskType != "continuation" || !ext.RequiresSourceVideo {
		t.Fatalf("续写要标 requiresSourceVideo 且 task_type=continuation: %+v", ext)
	}
	if ext.References.Video.Min != 1 || ext.References.Video.Max != 1 || ext.References.Image.Max != 0 {
		t.Fatalf("续写只吃 1 段视频: %+v", ext.References)
	}
}

// 目录里出现不认识的玩法就跳过，不能编一个我们其实提交不了的 mode。
func TestModesOfSkipsUnknownPlaystyle(t *testing.T) {
	withH3Engine(t)
	modes := modesOf(h3EntryWithModes("reference", "flip-vertical"), "minimax-h3-2k", "minimax-h3-fl2va", setting.TransportVideosTask)
	if len(modes) != 1 || modes[0].ID != "reference" {
		t.Fatalf("未知玩法应被跳过, got %+v", modes)
	}
}

// 没声明 image_mode 的模型保持"单一玩法"（返回 nil）—— 与改造前逐位一致。
func TestModesOfEmptyWithoutImageMode(t *testing.T) {
	entry := h3EntryWithModes()
	if modes := modesOf(entry, "minimax-h3-2k", "minimax-h3-fl2va", setting.TransportVideosTask); modes != nil {
		t.Fatalf("没有 image_mode 参数时不该报玩法, got %+v", modes)
	}
	// 图片传输没有玩法概念
	img := h3EntryWithModes("reference")
	img.Model.Type = "image"
	if modes := modesOf(img, "minimax-h3-image", "minimax-h3-image", setting.TransportImages); modes != nil {
		t.Fatalf("图片能力不该有玩法, got %+v", modes)
	}
}

// 非 H3 引擎族：保守值（首尾帧 1 张、参考族 1 图），不冒用 H3 的 9/3/3。
func TestModesOfNonH3UsesConservativeCaps(t *testing.T) {
	withPlaygroundConfig(t, "VideoModelConfig", `{"models":{"wan2.6-i2v":{"engine":"wan"}}}`)
	entry := setting.HiloCatalogEntry{
		PlatformModel: "wan2.6-i2v",
		Model: dto.HiloMediaModel{
			ModelName: "wan2.6-i2v", Type: "video",
			Params: map[string]dto.HiloModelParam{
				"image_mode": {Type: "select", Options: []string{"first-last-frame", "reference"}},
			},
		},
	}
	modes := modesOf(entry, "wan2.6-i2v", "wan2.6-i2v", setting.TransportVideosTask)
	byID := map[string]Mode{}
	for _, m := range modes {
		byID[m.ID] = m
	}
	if byID["first-last-frame"].References.Image.Max != 1 {
		t.Fatalf("非 H3 首尾帧保守给 1 张: %+v", byID["first-last-frame"])
	}
	if byID["reference"].References.Image.Max != 1 {
		t.Fatalf("非 H3 参考族保守给 1 张: %+v", byID["reference"])
	}
	if _, ok := byID["video-extension"]; ok {
		t.Fatal("非 H3 不该报续写玩法")
	}
}

// 交付阶梯：请求 1 s→1.625 s、5 s→5.167 s、15 s→15.083 s，与回显同精度（3 位小数）。
func TestDeliveryDurationsH3(t *testing.T) {
	withH3Engine(t)
	// 目录档位是 4/5/10/15 → 阶梯就四档，按目录顺序（已排序）逐个换算。
	got := deliveryDurationsOf("minimax-h3-fl2va", h3EntryWithModes("reference"))
	if len(got) != 4 {
		t.Fatalf("目录四个档位应得 4 档, got %v", got)
	}
	if math.Abs(got[0]-4.458) > 1e-9 {
		t.Fatalf("4 s → %v, want 4.458", got[0])
	}
	if math.Abs(got[1]-5.167) > 1e-9 {
		t.Fatalf("5 s → %v, want 5.167", got[1])
	}
	if math.Abs(got[3]-15.083) > 1e-9 {
		t.Fatalf("15 s → %v, want 15.083", got[3])
	}
	// 单调递增，客户端可以二分定位
	for i := 1; i < len(got); i++ {
		if got[i] <= got[i-1] {
			t.Fatalf("阶梯应单调递增: [%d]=%v [%d]=%v", i-1, got[i-1], i, got[i])
		}
	}
}

func TestDeliveryDurationsSkippedForNonH3(t *testing.T) {
	if got := deliveryDurationsOf("wan2.6-i2v", h3EntryWithModes("reference")); got != nil {
		t.Fatalf("非 H3 不该有交付阶梯, got %v", got)
	}
	// H3 但目录没配 duration：不编数字
	withH3Engine(t)
	entry := h3EntryWithModes("reference")
	delete(entry.Model.Params, "duration")
	if got := deliveryDurationsOf("minimax-h3-fl2va", entry); got != nil {
		t.Fatalf("没配 duration 时不该编阶梯, got %v", got)
	}
}

// 出厂目录里的 H3 视频能力应当带上玩法与阶梯（端到端确认接线没漏）。
func TestBuildExposesModesForH3Video(t *testing.T) {
	withAggregateConfig(t, common.DefaultAggregateModelConfig)
	withH3Engine(t)
	caps := Build(Scope{Enabled: map[string]bool{
		"minimax-h3-fl2va": true, "relayvsr": true,
	}})
	var found bool
	for _, c := range caps {
		if c.Operation != OperationGenerateVideo {
			continue
		}
		found = true
		if len(c.Modes) == 0 {
			t.Fatalf("H3 视频能力应带玩法: %+v", c)
		}
		if len(c.DeliveryDurations) == 0 {
			t.Fatalf("H3 视频能力应带交付阶梯: %+v", c)
		}
	}
	if !found {
		t.Fatal("没有找到视频能力")
	}
	// 图片能力不该带玩法（保持向后兼容）
	for _, c := range caps {
		if c.Operation == OperationGenerateImage && len(c.Modes) != 0 {
			t.Fatalf("图片能力不该带玩法: %+v", c)
		}
	}
}

// 线上契约就是这段 JSON：客户端按 `modes[].references` 填素材、按 `deliveryDurations`
// 展示时长。这里对**序列化后**的形状断言，而不是对 Go 结构体 —— 少一个 json tag
// 在结构体上完全看不出来，在线上就是客户端读不到。
func TestCapabilityJSONCarriesModesAndDeliveryDurations(t *testing.T) {
	withAggregateConfig(t, common.DefaultAggregateModelConfig)
	withH3Engine(t)
	caps := Build(Scope{Enabled: map[string]bool{
		"minimax-h3-fl2va": true, "relayvsr": true,
	}})

	var video Capability
	for _, c := range caps {
		if c.Operation == OperationGenerateVideo {
			video = c
			break
		}
	}
	if video.ID == "" {
		t.Fatal("没有视频能力")
	}
	raw, err := common.Marshal(video)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := common.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// 目录里 mode_models 把 reference 派给参考族的 checkpoint，所以帧族这条只报 3 个玩法
	// （参考族那条单独报 reference）。**每个平台模型只报落在自己身上的玩法** ——
	// 帧族报"参考生视频"会让客户端照着填，然后被派到另一个 checkpoint 上失败。
	modes, ok := decoded["modes"].([]any)
	if !ok || len(modes) != 3 {
		t.Fatalf("帧族应有 3 条玩法，实际 %v", decoded["modes"])
	}
	byID := map[string]map[string]any{}
	for _, m := range modes {
		item, _ := m.(map[string]any)
		byID[item["id"].(string)] = item
	}
	ext, ok := byID["video-extension"]
	if !ok {
		t.Fatalf("缺少 video-extension mode: %v", byID)
	}
	if ext["requiresSourceVideo"] != true || ext["taskType"] != "continuation" {
		t.Fatalf("续写 mode 契约不对: %v", ext)
	}
	refs, _ := ext["references"].(map[string]any)
	videoRef, _ := refs["video"].(map[string]any)
	if videoRef["min"] != float64(1) || videoRef["max"] != float64(1) {
		t.Fatalf("续写参考视频应为 1–1: %v", refs)
	}

	// 阶梯与目录档位一一对应（目录 1–20，共 20 档），首档是 1 s → 1.625。
	durations, ok := decoded["deliveryDurations"].([]any)
	if !ok || len(durations) != 20 {
		t.Fatalf("deliveryDurations 应有 20 档（1–20 s），实际 %v", decoded["deliveryDurations"])
	}
	if durations[0] != 1.625 {
		t.Fatalf("1 s 的交付时长应为 1.625，实际 %v", durations[0])
	}
}

// 出厂的三条 H3 聚合（MiniMax-H3 / minimax-h3-2k / minimax-h3-ref-2k）都报**全部**
// 四个玩法。
//
// 这条以前断言的是"参考族只报 reference、帧族报其余三个"—— 那是只有一个
// `generate.model` 的时代：名字决定了它能出哪种片。`generate.task_models` 之后
// 三个名字是等价的，五种 task_type 都能分派到对的 checkpoint，再按名字拆玩法
// 就会把"其实能出"的玩法藏掉。
func TestAggregateNamesReportEveryPlaystyle(t *testing.T) {
	withAggregateConfig(t, common.DefaultAggregateModelConfig)
	withH3Engine(t)
	caps := Build(Scope{Enabled: map[string]bool{
		"minimax-h3-fl2va": true, "minimax-h3-ref2va": true, "relayvsr": true,
	}})

	modesOfModel := map[string][]string{}
	for _, c := range caps {
		if c.Operation != OperationGenerateVideo {
			continue
		}
		for _, m := range c.Modes {
			modesOfModel[c.Model] = append(modesOfModel[c.Model], m.ID)
		}
	}
	for _, name := range []string{"MiniMax-H3", "minimax-h3-2k", "minimax-h3-ref-2k"} {
		got := modesOfModel[name]
		if len(got) != 4 {
			t.Fatalf("%s 应报全部 4 个玩法，实际 %v（全部：%v）", name, got, modesOfModel)
		}
		// reference 必须在内：它是唯一走参考族 checkpoint 的玩法，也是最容易被
		// mode_models 那套旧过滤藏掉的一个。
		if !contains(got, "reference") {
			t.Fatalf("%s 缺 reference 玩法：%v", name, got)
		}
	}
}

// 参考族的 checkpoint 停掉时，聚合仍然可用（帧族还在），但**参考玩法不能再报** ——
// 报了用户切过去必然失败，而失败信息来自渠道层。
func TestAggregateHidesModeWhoseSegmentIsUnavailable(t *testing.T) {
	withAggregateConfig(t, common.DefaultAggregateModelConfig)
	withH3Engine(t)
	caps := Build(Scope{Enabled: map[string]bool{
		// 只留帧族：ref2va 与 relayvsr 都停掉。
		"minimax-h3-fl2va": true,
	}})

	for _, c := range caps {
		if c.Model != "MiniMax-H3" {
			continue
		}
		for _, m := range c.Modes {
			if m.ID == "reference" {
				t.Fatalf("参考族的生成段没有渠道时不该报 reference：%+v", c.Modes)
			}
		}
		if len(c.Modes) != 3 {
			t.Fatalf("其余三个玩法应照报，实际 %+v", c.Modes)
		}
		if contains(c.TaskTypes, "r2va") {
			t.Fatalf("参考段不可用时 taskTypes 不该带 r2va，实际 %v", c.TaskTypes)
		}
	}
}

// 续写玩法在 task_models 里没有自己的 key：它第一步就被改写成一次 i2v。
// 直接拿 mode.TaskType("continuation") 去查会让续写**静默消失**。
func TestAggregateServesContinuationThroughI2V(t *testing.T) {
	agg := &common.AggregateModel{
		Name: "h3-agg", Type: "video",
		Generate: common.AggregateGenerate{
			Model: "fl2va-only",
			TaskModels: map[string]string{
				"t2v": "fl2va-only", "i2v": "fl2va-only", "r2va": "ref2va-only",
			},
		},
	}
	if !aggregateServesMode(agg, "continuation", nil) {
		t.Fatal("续写应通过 i2v 判定为可服务")
	}
	if aggregateServesMode(agg, "continuation", func(name string) bool { return name == "ref2va-only" }) {
		t.Fatal("i2v 映射到的段不可调用时,续写不该报出来")
	}
	// 没配 task_models 的聚合按默认段兜底，仍然认续写。
	plain := &common.AggregateModel{Generate: common.AggregateGenerate{Model: "some-model"}}
	if !aggregateServesMode(plain, "continuation", func(name string) bool { return name == "some-model" }) {
		t.Fatal("没有 task_models 时应回落到默认段")
	}
	if aggregateServesMode(plain, "s2v", nil) {
		t.Fatal("不在五个玩法里的 task_type 不该被判定为可服务")
	}
}
