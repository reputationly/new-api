package ovcaps

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting"
)

// 体验区模型配置（taskType 的来源）。测试里自己给，别依赖首启时的默认值。
func withPlaygroundConfig(t *testing.T, key, raw string) {
	t.Helper()
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	previous, existed := common.OptionMap[key]
	common.OptionMap[key] = raw
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		if existed {
			common.OptionMap[key] = previous
			return
		}
		delete(common.OptionMap, key)
	})
}

// 每个用例都要能自己决定聚合配置 —— 它是 OptionMap 里的一个键，默认值只在首启时写入。
func withAggregateConfig(t *testing.T, raw string) {
	t.Helper()
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	previous, existed := common.OptionMap["AggregateModelConfig"]
	common.OptionMap["AggregateModelConfig"] = raw
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		if existed {
			common.OptionMap["AggregateModelConfig"] = previous
			return
		}
		delete(common.OptionMap, "AggregateModelConfig")
	})
}

// 出厂目录 + 出厂聚合配置，全部有渠道时应该每个模态都有条目。
func TestBuildReportsEveryModality(t *testing.T) {
	withAggregateConfig(t, common.DefaultAggregateModelConfig)
	// 只给**裸模型**：聚合模型没有渠道 ability，永远不在这个集合里
	// （`qwen-image-pro-enhanced` 这类名字靠分段可用性判定，见 Scope.callable）。
	enabled := map[string]bool{
		"qwen-image": true, "qwen-image-edit": true, "qwen-image-pro": true,
		"minimax-h3-fl2va": true, "minimax-h3-ref2va": true, "seedvr2": true,
		"minimax-music3": true, "indextts-2.5": true,
	}
	capabilities := Build(Scope{Enabled: enabled})

	byOperation := map[string][]Capability{}
	for _, capability := range capabilities {
		byOperation[capability.Operation] = append(byOperation[capability.Operation], capability)
	}
	for _, operation := range []string{OperationGenerateImage, OperationGenerateVideo, OperationGenerateSpeech, OperationGenerateMusic} {
		if len(byOperation[operation]) == 0 {
			t.Fatalf("缺少 %s 的能力，实际：%+v", operation, byOperation)
		}
	}

	// 聚合模型必须报出来 —— 客户端预设用的就是它，而它不在 /v1/models 里。
	image := modelsOf(byOperation[OperationGenerateImage])
	for _, want := range []string{"qwen-image-pro-enhanced", "qwen-image", "qwen-image-edit"} {
		if !image[want] {
			t.Errorf("图片能力缺少 %s，实际 %v", want, image)
		}
	}
	// 视频：帧族与参考族是两个 checkpoint，各报一条。
	video := modelsOf(byOperation[OperationGenerateVideo])
	for _, want := range []string{"minimax-h3-2k", "minimax-h3-ref-2k"} {
		if !video[want] {
			t.Errorf("视频能力缺少 %s，实际 %v", want, video)
		}
	}
}

// 每个操作最多一条 Default，而且默认值必须真的在列表里。
func TestBuildMarksOneDefaultPerOperation(t *testing.T) {
	withAggregateConfig(t, common.DefaultAggregateModelConfig)
	capabilities := Build(Scope{Enabled: map[string]bool{
		"qwen-image": true, "qwen-image-pro": true, "minimax-h3-fl2va": true, "seedvr2": true,
	}})
	defaults := map[string]int{}
	for _, capability := range capabilities {
		if capability.Default {
			defaults[capability.Operation]++
		}
	}
	for operation, count := range defaults {
		if count != 1 {
			t.Errorf("%s 有 %d 条 default，期望 1", operation, count)
		}
	}
}

// 令牌白名单只放开一条时，别的都不该出现 —— 报出去用户也调不通。
func TestBuildHonoursTokenAllowList(t *testing.T) {
	withAggregateConfig(t, common.DefaultAggregateModelConfig)
	capabilities := Build(Scope{
		Enabled:      map[string]bool{"qwen-image": true, "qwen-image-pro": true, "minimax-h3-fl2va": true, "seedvr2": true},
		TokenAllowed: map[string]bool{"qwen-image-pro-enhanced": true},
	})
	if len(capabilities) != 1 {
		t.Fatalf("期望只报 1 条，实际 %d 条：%+v", len(capabilities), capabilities)
	}
	if capabilities[0].Model != "qwen-image-pro-enhanced" {
		t.Errorf("报出的是 %s", capabilities[0].Model)
	}
}

// 聚合模型要求**每一段**都可用：生成段没渠道时它不该出现。
func TestBuildDropsAggregateWhenSegmentMissing(t *testing.T) {
	withAggregateConfig(t, `[{"name":"h3-2k","type":"video","enabled":true,
		"generate":{"model":"minimax-h3-fl2va","overrides":{"size":"768P"}},
		"upscale":{"model":"seedvr2","target_size":"2k"}}]`)
	// 超分段没渠道。
	capabilities := Build(Scope{Enabled: map[string]bool{"minimax-h3-fl2va": true}})
	for _, capability := range capabilities {
		if capability.Model == "h3-2k" {
			t.Fatalf("超分段没渠道时不该报出聚合模型")
		}
	}
	// 补上超分段就该出现。
	capabilities = Build(Scope{Enabled: map[string]bool{"minimax-h3-fl2va": true, "seedvr2": true}})
	found := false
	for _, capability := range capabilities {
		if capability.Model == "h3-2k" {
			found = true
			if capability.Transport.Kind != "videos_task" {
				t.Errorf("视频聚合的 transport 是 %s", capability.Transport.Kind)
			}
		}
	}
	if !found {
		t.Fatalf("两段都有渠道时应该报出聚合模型")
	}
}

// 聚合模型的分组白名单与调用侧同一判据：不在白名单里的分组看不到它。
func TestBuildHonoursAggregateGroups(t *testing.T) {
	withAggregateConfig(t, `[{"name":"vip-only","type":"image","enabled":true,"groups":["vip"],
		"generate":{"model":"qwen-image-pro"}}]`)
	enabled := map[string]bool{"qwen-image-pro": true}

	if found := modelOf(Build(Scope{Enabled: enabled, UsingGroups: []string{"default"}}), "vip-only"); found {
		t.Fatalf("default 分组不该看到 vip 专用的聚合模型")
	}
	if !modelOf(Build(Scope{Enabled: enabled, UsingGroups: []string{"vip"}}), "vip-only") {
		t.Fatalf("vip 分组应该看到它")
	}
}

// 版本号必须只跟内容有关：同样的输入两次一样，内容变了就变。
func TestVersionIsStableAndContentSensitive(t *testing.T) {
	withAggregateConfig(t, common.DefaultAggregateModelConfig)
	enabled := map[string]bool{"qwen-image": true, "indextts-2.5": true}
	first := Version(Build(Scope{Enabled: enabled}))
	second := Version(Build(Scope{Enabled: enabled}))
	if first == "" || first != second {
		t.Fatalf("同样输入版本不一致：%q / %q", first, second)
	}
	more := map[string]bool{"qwen-image": true, "indextts-2.5": true, "minimax-music3": true}
	if Version(Build(Scope{Enabled: more})) == first {
		t.Fatalf("多了一条能力，版本没变")
	}
}

// 参数表要能把目录里的枚举和默认值带出去 —— 客户端的选择器全靠它。
func TestParamSchemaCarriesEnumsAndDefaults(t *testing.T) {
	withAggregateConfig(t, common.DefaultAggregateModelConfig)
	// 目录里视频那条的 platform_model 是聚合名 `minimax-h3-2k`，它要靠"两段都有渠道"
	// 才可调：所以这里给的是两个裸模型。
	capabilities := Build(Scope{Enabled: map[string]bool{"minimax-h3-fl2va": true, "seedvr2": true}})
	var video *Capability
	for index := range capabilities {
		if capabilities[index].Operation == OperationGenerateVideo {
			video = &capabilities[index]
			break
		}
	}
	if video == nil {
		t.Fatalf("没有视频能力：%+v", capabilities)
	}
	if video.Transport.Kind != "videos_task" {
		t.Errorf("transport 是 %s", video.Transport.Kind)
	}
	properties, ok := video.ParamSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("paramSchema 没有 properties：%+v", video.ParamSchema)
	}
	duration, ok := properties["duration"].(map[string]any)
	if !ok {
		t.Fatalf("没有 duration 参数：%+v", properties)
	}
	options, ok := duration["enum"].([]string)
	if !ok || len(options) < 2 {
		t.Fatalf("duration 的枚举不对：%+v", duration)
	}
	if duration["default"] != "5" {
		t.Errorf("duration 默认值是 %v", duration["default"])
	}
	// 时长上限取档位最大值，不是写死的 10。
	if video.Limits.MaxDurationSeconds != 15 {
		t.Errorf("maxDurationSeconds = %d，期望 15（目录档位最大值）", video.Limits.MaxDurationSeconds)
	}
	if video.Limits.MaxReferenceImages != 9 {
		t.Errorf("maxReferenceImages = %d，期望 9", video.Limits.MaxReferenceImages)
	}
}

func modelsOf(capabilities []Capability) map[string]bool {
	out := map[string]bool{}
	for _, capability := range capabilities {
		out[capability.Model] = true
	}
	return out
}

func modelOf(capabilities []Capability, name string) bool {
	for _, capability := range capabilities {
		if capability.Model == name {
			return true
		}
	}
	return false
}

// 目录里指向**聚合模型**的条目，必须和聚合模型那一段用同一套判据：
// 分组白名单、令牌白名单、每一段都可用。只看分段会漏掉分组这一条 ——
// 管理员给聚合模型限了分组，别的分组照样能看到它。
func TestBuildHonoursGroupsForCatalogAggregates(t *testing.T) {
	previous := setting.HiloCatalog2JsonString()
	t.Cleanup(func() {
		if err := setting.UpdateHiloCatalogByJsonString(previous); err != nil {
			t.Fatalf("恢复目录失败: %v", err)
		}
	})
	// 出厂目录里 `minimax-h3-2k` 就是聚合模型；这里把它收窄到 vip 分组。
	withAggregateConfig(t, `[{"name":"h3-frames","type":"video","enabled":true,"groups":["vip"],
		"generate":{"model":"minimax-h3-fl2va","overrides":{"size":"768P"}},
		"upscale":{"enabled":true,"model":"seedvr2","target_size":"2k","delivered_size":"1080P"}}]`)
	catalog := `{"video":[{"platform_model":"h3-frames","transport":"videos_task",
		"model":{"id":"h3","name":"H3","backend":"minimax","model_name":"h3-frames","type":"video",
		"max_refs":0,"params":{},"display_name":"H3","description":"","visibility":"","icon_url":"",
		"tool_names":["hub_generate_video"],"hot":false}}]}`
	if err := setting.UpdateHiloCatalogByJsonString(catalog); err != nil {
		t.Fatalf("写入目录失败: %v", err)
	}

	enabled := map[string]bool{"minimax-h3-fl2va": true, "seedvr2": true}
	if modelOf(Build(Scope{Enabled: enabled, UsingGroups: []string{"default"}}), "h3-frames") {
		t.Fatalf("default 分组不该看到 vip 专用的聚合模型")
	}
	if !modelOf(Build(Scope{Enabled: enabled, UsingGroups: []string{"vip"}}), "h3-frames") {
		t.Fatalf("vip 分组应该看到它")
	}
	// 令牌白名单同样要生效：白名单里没有它就不能报。
	if modelOf(Build(Scope{Enabled: enabled, UsingGroups: []string{"vip"}, TokenAllowed: map[string]bool{"other": true}}), "h3-frames") {
		t.Fatalf("令牌白名单没放开时不该报出来")
	}
}

// 聚合模型的 `taskTypes` / `limits` 要按它的**生成段**查，不能按聚合名查 ——
// 体验区配置里登记的是生成段，按聚合名查多半什么都拿不到。
// 后果很具体：参考族拿不到 `r2va`，客户端就补不上"参考生视频"。
func TestBuildReadsTaskTypesThroughGenerateSegment(t *testing.T) {
	withAggregateConfig(t, common.DefaultAggregateModelConfig)
	// `taskType` 来自体验区配置，按**模型名**登记。这里只给生成段登记：
	// 聚合名（minimax-h3-ref-2k）在体验区里通常查不到东西，实现必须回落到生成段。
	withPlaygroundConfig(t, "VideoModelConfig", `{"models": {
		"minimax-h3-fl2va": {"tabs": {"t2v": {}, "i2v": {}}},
		"minimax-h3-ref2va": {"tabs": {"r2va": {}}}
	}}`)
	enabled := map[string]bool{
		"minimax-h3-fl2va": true, "minimax-h3-ref2va": true, "seedvr2": true,
	}
	capabilities := Build(Scope{Enabled: enabled, UsingGroups: []string{"default"}})
	var frames, reference *Capability
	for index := range capabilities {
		switch capabilities[index].Model {
		case "minimax-h3-2k":
			frames = &capabilities[index]
		case "minimax-h3-ref-2k":
			reference = &capabilities[index]
		}
	}
	if frames == nil || reference == nil {
		t.Fatalf("两条视频聚合模型都要报出来，实际 %+v", capabilities)
	}
	if !contains(reference.TaskTypes, "r2va") {
		t.Errorf("参考族要带 r2va，实际 %v", reference.TaskTypes)
	}
	if contains(frames.TaskTypes, "r2va") {
		t.Errorf("帧族不该带 r2va，实际 %v", frames.TaskTypes)
	}
}

// 默认项按**出现顺序**取第一条（管理员排过序），不是"名字最短"。
func TestBuildDefaultsFollowCatalogOrder(t *testing.T) {
	previous := setting.HiloCatalog2JsonString()
	t.Cleanup(func() {
		if err := setting.UpdateHiloCatalogByJsonString(previous); err != nil {
			t.Fatalf("恢复目录失败: %v", err)
		}
	})
	withAggregateConfig(t, "[]")
	entry := func(id, platformModel string) string {
		return `{"platform_model":"` + platformModel + `","transport":"images",
		"model":{"id":"` + id + `","name":"` + id + `","backend":"openai","model_name":"` + platformModel + `",
		"type":"image","max_refs":0,"params":{},"display_name":"` + id + `","description":"",
		"visibility":"","icon_url":"","tool_names":["hub_generate_image"],"hot":false}}`
	}
	// 故意让名字更长的排在前面：如果按"名字最短"选，默认会变成 `a`。
	catalog := `{"image":[` + entry("b-model", "b-model") + `,` + entry("a", "a") + `]}`
	if err := setting.UpdateHiloCatalogByJsonString(catalog); err != nil {
		t.Fatalf("写入目录失败: %v", err)
	}
	capabilities := Build(Scope{Enabled: map[string]bool{"a": true, "b-model": true}})
	var defaults []string
	for _, capability := range capabilities {
		if capability.Default {
			defaults = append(defaults, capability.Model)
		}
	}
	if len(defaults) != 1 || defaults[0] != "b-model" {
		t.Fatalf("默认项应取目录里第一条 b-model，实际 %v", defaults)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
