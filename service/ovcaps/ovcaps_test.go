package ovcaps

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting"
)

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
		"minimax-h3-fl2va": true, "minimax-h3-ref2va": true, "swiftvr": true,
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
		"qwen-image": true, "qwen-image-pro": true, "minimax-h3-fl2va": true, "swiftvr": true,
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
		Enabled:      map[string]bool{"qwen-image": true, "qwen-image-pro": true, "minimax-h3-fl2va": true, "swiftvr": true},
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
		"upscale":{"model":"swiftvr","target_size":"2k"}}]`)
	// 超分段没渠道。
	capabilities := Build(Scope{Enabled: map[string]bool{"minimax-h3-fl2va": true}})
	for _, capability := range capabilities {
		if capability.Model == "h3-2k" {
			t.Fatalf("超分段没渠道时不该报出聚合模型")
		}
	}
	// 补上超分段就该出现。
	capabilities = Build(Scope{Enabled: map[string]bool{"minimax-h3-fl2va": true, "swiftvr": true}})
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
	capabilities := Build(Scope{Enabled: map[string]bool{"minimax-h3-fl2va": true, "swiftvr": true}})
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

// 转写能力由 transport 决定操作：同一个 audio 分组里，`audio_transcriptions` 那条
// 必须报成 transcribe，而不是被工具名带到"语音合成/音乐"上。
//
// 平台现在**没有部署转写模型**（`/v1/models` 里没有），所以这条用例断言的是"配上就
// 能报出来" —— 部署侧补齐模型后不需要再改代码。
func TestBuildReportsTranscriptionFromTransport(t *testing.T) {
	previous := setting.HiloCatalog2JsonString()
	t.Cleanup(func() {
		if err := setting.UpdateHiloCatalogByJsonString(previous); err != nil {
			t.Fatalf("恢复目录失败: %v", err)
		}
	})
	catalog := `{"audio":[{"platform_model":"asr-model","transport":"audio_transcriptions",
		"model":{"id":"asr","name":"ASR","backend":"minimax_tts","model_name":"asr-model",
		"type":"audio","max_refs":0,"params":{},"display_name":"语音识别",
		"description":"","visibility":"","icon_url":"","tool_names":["hub_generate_speech"],"hot":false}}]}`
	if err := setting.UpdateHiloCatalogByJsonString(catalog); err != nil {
		t.Fatalf("写入目录失败: %v", err)
	}
	withAggregateConfig(t, "[]")
	capabilities := Build(Scope{Enabled: map[string]bool{"asr-model": true}})
	if len(capabilities) != 1 {
		t.Fatalf("期望 1 条，实际 %+v", capabilities)
	}
	if capabilities[0].Operation != OperationTranscribe {
		t.Errorf("operation = %s，期望 %s", capabilities[0].Operation, OperationTranscribe)
	}
	if capabilities[0].Transport.Kind != setting.TransportTranscribe {
		t.Errorf("transport = %s", capabilities[0].Transport.Kind)
	}
}
