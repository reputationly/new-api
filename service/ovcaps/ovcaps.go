package ovcaps

// 能力目录：把「这个 key 实际能调什么」整理成客户端可以直接用的清单。
//
// # 为什么不是 `GET /v1/models`
//
// `/v1/models` 是 OpenAI 标准接口，只能回答"有哪些模型"，回答不了：
//
//   - 这个模型是文生图还是图生视频（这套部署的语音/音乐/视频全在同一条任务接口上，
//     端点类型给不出这个信息，见 [common.MediaModelKinds] 的注释）；
//   - 它能吃哪些参数、默认值是什么、上限多少（`aspect_ratio` 的枚举、时长档位）；
//   - 聚合模型（`qwen-image-pro-enhanced` / `minimax-h3-2k`）—— 它们没有渠道
//     ability，压根不出现在 `/v1/models` 里，而客户端预设用的正是这几个名字。
//
// 所以能力目录单独一个接口，数据来自三处现成的来源，**这里不新造知识**：
//
//	setting.HiloCatalog         参数表、上限、展示名（管理员可配）
//	common.GetAggregateModels() 聚合流水线（客户端只看到一个模型名）
//	model.GetGroupEnabledModels 这个分组真有渠道的模型
//
// # 只报"这个 key 能调的"
//
// 用户分组 → abilities（有渠道）→ 令牌白名单 → 聚合模型的分组白名单与分段可用性。
// 报一个调不了的模型出去，用户能选中它，点生成才失败 —— 而失败信息来自渠道层，
// 说不清"这个模型没部署"还是"你没这个权限"。

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
)

// 能力操作。
const (
	OperationGenerateImage  = "generate_image"
	OperationGenerateVideo  = "generate_video"
	OperationGenerateSpeech = "generate_speech"
	OperationGenerateMusic  = "generate_music"
	OperationUpscale        = "upscale"
	OperationEditMedia      = "edit_media"
	OperationChat           = "chat"
)

// Localized 双语展示。客户端按界面语言取一个，都缺时回落到 en。
type Localized struct {
	Zh string `json:"zh"`
	En string `json:"en"`
}

// Transport 调用这个能力走哪条协议。kind 的取值见 [setting.ValidTransports]。
type Transport struct {
	Kind string `json:"kind"`
}

// Limits 这个能力的硬上限。0 = 不限制或未知（客户端按 0 处理为"不额外限制"）。
type Limits struct {
	MaxReferenceImages int   `json:"maxReferenceImages"`
	MaxDurationSeconds int   `json:"maxDurationSeconds"`
	MaxInputBytes      int64 `json:"maxInputBytes"`
}

// MediaRefRule 某一类参考素材的数量区间。0/0 = 这个玩法不吃这类素材。
type MediaRefRule struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

// ModeReferences 一个玩法的参考素材规则。
type ModeReferences struct {
	Image MediaRefRule `json:"image"`
	Video MediaRefRule `json:"video"`
	Audio MediaRefRule `json:"audio"`
}

// Mode 一个"玩法"：客户端按它决定填哪些参考、发哪个 task_type。
//
// 为什么要单独列出来：同一个模型名下面的玩法，输入契约完全不同（首尾帧只吃 2 张图、
// 参考族能吃 9 图 + 3 视频 + 3 音频、续写只吃 1 段视频）。只看顶层 `limits` 的
// `maxReferenceImages`，客户端会把 9 张图塞进首尾帧，请求到引擎才 400 —— 错误来自
// 上游，文案与"你选错了玩法"毫无关联。
type Mode struct {
	// ID 玩法标识，也是提交时 metadata.image_mode 的取值。
	ID    string    `json:"id"`
	Label Localized `json:"label"`
	// TaskType 显式提交时 metadata.task_type 的取值（等价写法，二选一）。
	TaskType string `json:"taskType"`
	// TaskTypeIfSingleImage 首尾帧专用：只给 1 张图时其实要走 i2v —— 客户端不必自己
	// 背这条规则，也不必为"一张图算不算首尾帧"去猜。
	TaskTypeIfSingleImage string         `json:"taskTypeIfSingleImage,omitempty"`
	References            ModeReferences `json:"references"`
	// RequiresSourceVideo 续写专用：必须给 metadata.source_video（URL / data: / task:<id>）。
	RequiresSourceVideo bool `json:"requiresSourceVideo,omitempty"`
	// Note 给客户端看的补充说明（如续写的 duration 语义）。
	Note Localized `json:"note,omitempty"`
}

// DeliveryDurations 这个能力**实际能交付**的秒数（不是请求秒数）。
//
// 带帧栅格的引擎族（H3，17n+5 @24fps）请求 1 s 交付 1.625 s，客户端拿它做进度条与
// 时长展示，比"按请求值猜"准确；不带栅格的模型为空（等价于按请求值交付）。
type DeliveryDurations []float64

// Capability 目录里的一条。
type Capability struct {
	// ID 客户端提交任务时用的能力标识：`<operation>:<model>`。
	ID string `json:"id"`
	// Operation 这个能力属于哪一类操作。
	Operation string `json:"operation"`
	// Model 调用时放在 model 字段里的名字。聚合模型报的也是它自己那个名字。
	Model       string    `json:"model"`
	DisplayName Localized `json:"displayName"`
	Description Localized `json:"description"`
	Transport   Transport `json:"transport"`
	// TaskTypes 只有 tasks 类传输有：这个模型支持的 task_type。
	TaskTypes []string `json:"taskTypes,omitempty"`
	// ParamSchema 参数表，JSON Schema 的最小子集（type/properties/enum/default/title）。
	ParamSchema map[string]any `json:"paramSchema,omitempty"`
	Limits      Limits         `json:"limits"`
	// Modes 这个能力的玩法列表（只有视频类、且目录声明了 image_mode 的才有）。
	// 为空表示"单一玩法"：按顶层 limits 与 taskTypes 提交即可。
	Modes []Mode `json:"modes,omitempty"`
	// DeliveryDurations 实际可交付的时长阶梯（见类型注释）。
	DeliveryDurations DeliveryDurations `json:"deliveryDurations,omitempty"`
	// InputModalities 对话模型能接收的**输入**模态（image/video/audio；text 恒支持，不列出）。
	// 名字刻意区别于 OpenAI 请求里的 `modalities`（那是输出模态），避免客户端读错。
	InputModalities []string `json:"inputModalities,omitempty"`
	// Default 是不是这个操作下的默认模型。同一操作最多一条为 true。
	Default bool `json:"default"`
}

// Scope 调用方这个 key 能用到什么。由 controller 从鉴权上下文算出，这里只做判定。
type Scope struct {
	// Enabled 令牌生效分组下**真有渠道**的模型（model.GetGroupEnabledModels）。
	Enabled map[string]bool
	// TokenAllowed 令牌模型白名单；nil = 没开白名单（不限制）。
	TokenAllowed map[string]bool
	// UsingGroups 令牌生效分组，auto 已展开（service.AggregateUsingGroups）。
	UsingGroups []string
}

// allows 这个裸模型这个 key 能不能调（不含令牌白名单 —— 白名单由调用点判，
// 因为聚合模型的白名单里存的是**公开名**，和分段名不是一回事）。
func (s Scope) allows(platformModel string) bool {
	if platformModel == "" || !s.Enabled[platformModel] {
		return false
	}
	if s.TokenAllowed != nil && !s.TokenAllowed[platformModel] {
		return false
	}
	return true
}

// callable 这个模型能不能跑起来 —— 裸模型看有没有渠道，聚合模型看**每一段**。
//
// 聚合模型没有渠道 ability（它不是一个能被路由的模型，见 controller/model.go），
// 所以它**永远不会**出现在 `Enabled` 里。只看 `Enabled` 的话，目录里指向聚合模型的
// 那些条目会被整条滤掉 —— 而客户端的预设用的正是 `qwen-image-pro-enhanced` 这类
// 聚合名，表现是"设置页一个模型都没有"。
func (s Scope) callable(platformModel string) bool {
	if platformModel == "" {
		return false
	}
	if agg := common.GetAggregateModel(platformModel); agg != nil {
		return s.segmentsAvailable(agg)
	}
	return s.Enabled[platformModel]
}

// segmentsAvailable 聚合流水线每一段都能跑。
//
// **提示词增强段不参与判定**：它失败会回落（见 service/aggregate_enhance.go 的
// 注释），缺那一段的后果是"提示词没被改写"，不是"任务跑不完"。按它拦会让一条
// 本来能用的流水线从目录里消失。
func (s Scope) segmentsAvailable(agg *common.AggregateModel) bool {
	if agg == nil {
		return false
	}
	if !s.callable(agg.Generate.Model) {
		return false
	}
	if agg.Upscale != nil && agg.Upscale.IsEnabled() && !s.callable(agg.Upscale.Model) {
		return false
	}
	return true
}

// available 能跑 + 流水线兑现得了它承诺的产物。
func (s Scope) available(platformModel string) bool {
	return s.callable(platformModel) && service.PipelineCannotDeliver(platformModel) == ""
}

// aggregateAllows 这个聚合模型这个 key 能不能调。
//
// 三条缺一不可：分组白名单（与调用侧同一判据）、令牌白名单、**每一段都可用**。
func (s Scope) aggregateAllows(agg *common.AggregateModel) bool {
	if agg == nil || agg.Name == "" {
		return false
	}
	if !agg.AllowsGroups(s.UsingGroups) {
		return false
	}
	if s.TokenAllowed != nil && !s.TokenAllowed[agg.Name] {
		return false
	}
	return s.available(agg.Name)
}

// Build 生成能力目录。顺序稳定（操作 → 默认优先 → 模型名），同样的输入永远同样的输出，
// 版本哈希才有意义。
func Build(scope Scope) []Capability {
	emitted := map[string]bool{}
	out := make([]Capability, 0, 32)
	// 默认项：**按出现顺序**每个操作的第一条。
	// 顺序是：HiloCatalog（管理员排过序）→ 按名字排序的聚合模型 → 按名字排序的对话模型。
	// 它必须在 `sortCapabilities` **之前**定下来 —— 排序只是为了让输出稳定、让
	// `Version` 的哈希可复现，不应该顺带决定"默认用哪个模型"。
	defaults := map[string]string{}
	// 管理员指定的默认对话模型先记下来，等目录发完再决定是否覆盖（见下方）。
	catalog := setting.GetHiloCatalog()
	configuredChatModel := strings.TrimSpace(catalog.DefaultTextModel)
	emit := func(capability Capability) {
		if _, ok := defaults[capability.Operation]; !ok {
			defaults[capability.Operation] = capability.Model
		}
		out = append(out, capability)
	}

	// 输入模态唯一来源：Hilo 目录 text 条目的 supportsImage/Video/Audio。
	modalities := hiloTextModalities(catalog.Text)
	for _, group := range []struct {
		name    string
		entries []setting.HiloCatalogEntry
	}{
		{"image", catalog.Image}, {"video", catalog.Video}, {"audio", catalog.Audio},
	} {
		for _, entry := range group.entries {
			// 一条目录项可能按玩法分派到多个平台模型（H3 的帧族/参考族是两个
			// checkpoint）。**逐个报**：我们自己的客户端按能力 id 选，不需要
			// "一个模型名 + 玩法参数"那层间接。
			for _, platformModel := range entry.PlatformModelsOf() {
				if emitted[platformModel] {
					continue
				}
				// 目录里的条目也可能指向**聚合模型**（出厂目录的 `minimax-h3-2k` 就是）：
				// 这类名字在 abilities 表里没有行，`available()` 只看分段、**不看分组白名单**。
				// 管理员一旦给聚合模型限了分组，别的分组的 key 会在这里看到它、调用时才被拒。
				if agg := common.GetAggregateModel(platformModel); agg != nil {
					if !scope.aggregateAllows(agg) {
						continue
					}
				} else {
					if !scope.available(platformModel) {
						continue
					}
					if scope.TokenAllowed != nil && !scope.TokenAllowed[platformModel] {
						continue
					}
				}
				capability, ok := fromCatalogEntry(scope, entry, platformModel, group.name)
				if !ok {
					continue
				}
				emitted[platformModel] = true
				emit(capability)
			}
		}
	}

	// 聚合模型。目录里没配到的那些（例如管理员只配了官方 schema 的几条）照样要报 ——
	// 客户端的预设用的就是 `qwen-image-pro-enhanced` 这类聚合名。
	// 按名字排序：`GetAggregateModels()` 是 map，直接遍历的话"谁是默认"每次都可能变。
	aggregateNames := make([]string, 0, len(common.GetAggregateModels()))
	for name := range common.GetAggregateModels() {
		aggregateNames = append(aggregateNames, name)
	}
	sort.Strings(aggregateNames)
	for _, name := range aggregateNames {
		agg := common.GetAggregateModels()[name]
		if emitted[name] || !scope.aggregateAllows(agg) {
			continue
		}
		operation, transport := aggregateOperation(agg)
		if operation == "" {
			continue
		}
		emitted[name] = true
		// 目录里没给这条聚合名配条目时，复用**生成段模型**的条目来填参数/玩法/上限：
		// 聚合只是"多一段增强/超分"，可填的东西与生成段完全一样。不这么做的话
		// `minimax-music3-enhanced` 这类名字会以"无参数、无玩法"的裸能力出现 ——
		// 客户端能选中它，却不知道能填什么。
		if entry, group, ok := catalogEntryForAggregate(agg); ok {
			if enriched, ok := fromCatalogEntry(scope, entry, name, group); ok && len(enriched.ParamSchema) > 0 {
				emit(enriched)
				continue
			}
		}
		probeModels := probeModelsOf(scope, agg, name)
		emit(Capability{
			ID:          capabilityID(operation, name),
			Operation:   operation,
			Model:       name,
			DisplayName: Localized{Zh: name, En: name},
			Description: Localized{Zh: agg.Note, En: agg.Note},
			Transport:   Transport{Kind: transport},
			TaskTypes:   taskTypesOf(probeModels...),
			Limits:      limitsOf(nil, probeModels, transport),
		})
	}

	// 对话模型。判据是端点类型 + 不是媒体模型 —— 语音/音乐/视频在这套部署上也是
	// OpenAI 端点，只按端点归类会把 tts 报成对话模型。
	for _, name := range sortedKeys(scope.Enabled) {
		if emitted[name] || !scope.allows(name) || isMediaModel(name) {
			continue
		}
		if !hasChatEndpoint(name) {
			continue
		}
		emitted[name] = true
		emit(Capability{
			ID:              capabilityID(OperationChat, name),
			Operation:       OperationChat,
			Model:           name,
			DisplayName:     Localized{Zh: name, En: name},
			Transport:       Transport{Kind: setting.TransportChat},
			Limits:          Limits{},
			InputModalities: modalities[name],
		})
	}

	sortCapabilities(out)
	// 配置的默认对话模型只有**真的以 chat 条目出现**时才生效：否则静默回退"按顺序第一条"，
	// 保证"chat 恰好一条 default"的客户端契约不被配置错误破坏。
	if configuredChatModel != "" {
		matched := false
		for _, capability := range out {
			if capability.Operation == OperationChat && capability.Model == configuredChatModel {
				matched = true
				break
			}
		}
		if matched {
			defaults[OperationChat] = configuredChatModel
		} else if _, warned := warnedMissingDefaultChatModel.LoadOrStore(configuredChatModel, struct{}{}); !warned {
			// 每把 key 的分组/白名单不同，"看不到默认模型"是合法状态：按模型名只报一次，
			// 否则每次拉目录都会写一条错误日志。
			common.SysError(fmt.Sprintf(
				"通用设置里的默认对话模型 %q 不在能力目录中（未部署、分组不可见或被令牌白名单过滤），已回退按顺序第一条",
				configuredChatModel))
		}
	}
	for index := range out {
		if defaults[out[index].Operation] == out[index].Model {
			out[index].Default = true
		}
	}
	return out
}

// Version 目录内容的指纹。客户端按它判断要不要重新拉。
//
// 内容变了它才变；同一个 key 反复请求得到同一个值 —— 所以哈希的输入必须是
// **稳定排序后**的 JSON（由 [Build] 保证），不能带时间戳。
//
// **必须把 roles 一起哈希**：`multimodal_model` 只影响 defaults、不动任何 capability
// 字段，漏掉它的话管理员改完配置指纹没变，按 version 缓存的客户端就永远不会重拉。
// 所以这里刻意不留"只哈希 capabilities"的重载 —— 那个签名迟早会被误用。
func Version(capabilities []Capability, roles DefaultRoles) string {
	raw, err := common.Marshal(struct {
		Capabilities []Capability `json:"capabilities"`
		Defaults     DefaultRoles `json:"defaults"`
	}{capabilities, roles})
	if err != nil {
		// 序列化不了就不给版本号，客户端每次都重拉 —— 比给一个会误导的稳定值好。
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// fromCatalogEntry 把一条目录项翻成能力。
//
// scope 参与判定：聚合模型按玩法分派之后，`taskTypes` / `limits` / `modes` 要按
// **当前可调用的那些生成段**取并集 —— 见 probeModels。
func fromCatalogEntry(scope Scope, entry setting.HiloCatalogEntry, platformModel, group string) (Capability, bool) {
	operation, ok := catalogOperation(entry)
	if !ok {
		return Capability{}, false
	}
	transport := entry.TransportFor(group)
	if !setting.ValidTransports[transport] {
		return Capability{}, false
	}
	display := entry.Model.DisplayName
	if strings.TrimSpace(display) == "" {
		display = entry.Model.ID
	}
	agg := common.GetAggregateModel(platformModel)
	// `taskTypes` / `limits` / `modes` 按**真正调用的那些模型**取：聚合模型的名字在
	// 体验区配置里通常没有登记，登记的是它的生成段；而按玩法分派之后，一个聚合名下面
	// 可能有两个生成段（H3 的帧族 + 参考族）。
	//
	// 只取 `Generate.Model` 的后果和"按聚合名查"是同一个：参考族那条拿不到 `r2va`，
	// 客户端补不上"参考生视频"。只取**可调用的**那部分，是因为没部署的 checkpoint
	// 报了也没用 —— 用户切过去必然失败，而失败信息来自渠道层。
	probeModels := probeModelsOf(scope, agg, platformModel)
	probe := platformModel
	if len(probeModels) > 0 {
		probe = probeModels[0]
	}
	return Capability{
		ID:                capabilityID(operation, platformModel),
		Operation:         operation,
		Model:             platformModel,
		DisplayName:       Localized{Zh: display, En: display},
		Description:       Localized{Zh: entry.Model.Description, En: entry.Model.Description},
		Transport:         Transport{Kind: transport},
		TaskTypes:         taskTypesOf(probeModels...),
		ParamSchema:       paramSchema(entry.Model.Params),
		Limits:            limitsOf(&entry.Model, probeModels, transport),
		Modes:             modesOfFor(entry, platformModel, probeModels, transport, agg, scope.callable),
		DeliveryDurations: deliveryDurationsOf(probe, entry),
	}, true
}

// probeModelsOf 这个能力该按哪些模型去查玩法/上限。
//
//	裸模型            它自己
//	聚合模型          `Generate.AllModels()` 里**当前可调用**的那些
//
// 一个都不剩下时退回 `Generate.Model`：调用方（catalog 循环 / aggregateAllows）已经
// 判过"这条流水线跑不跑得起来"，走到这里还能剩下零个只会出现在"裸模型也没有渠道"
// 的情形，那时按生成段名查就是最小意外。
func probeModelsOf(scope Scope, agg *common.AggregateModel, platformModel string) []string {
	if agg == nil {
		return []string{platformModel}
	}
	models := make([]string, 0, len(agg.Generate.TaskModels)+1)
	for _, name := range agg.Generate.AllModels() {
		if scope.callable(name) {
			models = append(models, name)
		}
	}
	if len(models) == 0 && strings.TrimSpace(agg.Generate.Model) != "" {
		models = append(models, strings.TrimSpace(agg.Generate.Model))
	}
	return models
}

// catalogOperation 目录项属于哪一类操作。判据是官方工具名 —— 那是目录里
// "这个模型被配来干什么"的既有表达，不另立字段。
func catalogOperation(entry setting.HiloCatalogEntry) (string, bool) {
	for _, tool := range entry.Model.ToolNames {
		switch tool {
		case "hub_generate_image":
			return OperationGenerateImage, true
		case "hub_generate_video":
			return OperationGenerateVideo, true
		// 两套工具名都要认：目录里写的是 `hub_generate_speech`，而客户端的能力表
		// 用的是 `hub_generate_audio_speech`。只认一个的话，语音那条会被下面的
		// type 兜底判成"音乐"—— 而两者的表单和接口完全一样，错得悄无声息。
		case "hub_generate_audio_speech", "hub_generate_speech":
			return OperationGenerateSpeech, true
		case "hub_generate_audio_music", "hub_generate_music":
			return OperationGenerateMusic, true
		}
	}
	// tool_names 没配时按 type 兜底：目录里 type 只有 image/video/audio 三种，
	// 而 audio 分不出语音还是音乐 —— 这时按音乐报（媒体类型都是 audio，
	// 客户端按 operation 只是决定展示哪个表单）。
	switch entry.Model.Type {
	case "image":
		return OperationGenerateImage, true
	case "video":
		return OperationGenerateVideo, true
	case "audio":
		return OperationGenerateMusic, true
	}
	return "", false
}

// aggregateOperation 聚合模型属于哪一类操作；不认识的类型返回空。
//
// 音乐走的是**和视频同一条**传输(videos_task):Music3 在统一任务契约里就是
// POST /v1/videos + metadata.task_type(适配器再把它改写成 tts 发给引擎)。
// 报错成空操作会让音乐聚合在客户端能力目录里整个消失 —— 而它恰恰是客户端预设要用的名字。
func aggregateOperation(agg *common.AggregateModel) (string, string) {
	switch strings.TrimSpace(agg.Type) {
	case "image":
		return OperationGenerateImage, setting.TransportImages
	case "video":
		return OperationGenerateVideo, setting.TransportVideosTask
	case "music":
		return OperationGenerateMusic, setting.TransportVideosTask
	}
	return "", ""
}

// paramSchema 把 hilo 的参数表翻成 JSON Schema 的最小子集。
//
// 客户端已经有这个形状的消费代码（`catalogEntries` 生成的就是它），照抄一份比另立
// 一套结构省事，也不会让两边的默认值语义漂移。
func paramSchema(params map[string]dto.HiloModelParam) map[string]any {
	if len(params) == 0 {
		return nil
	}
	properties := make(map[string]any, len(params))
	for name, p := range params {
		property := map[string]any{"type": "string"}
		if p.Label != "" {
			property["title"] = p.Label
		}
		switch p.Type {
		case "select":
			if len(p.Options) > 0 {
				property["enum"] = p.Options
			}
		case "slider":
			// slider 在能力目录里也当字符串报：客户端的参数表是按字符串枚举消费的，
			// 报 number 会让它走另一条分支。区间放在 min/max 上。
			if p.Min != nil {
				property["minimum"] = *p.Min
			}
			if p.Max != nil {
				property["maximum"] = *p.Max
			}
		}
		if p.Default != "" {
			property["default"] = p.Default
		}
		properties[name] = property
	}
	return map[string]any{"type": "object", "properties": properties}
}

// limitsOf 汇总上限。时长取目录里 duration 档位的最大值 —— 报错成固定值的话，
// 用户选了超出档位的时长，请求发出去才被拒。
// models 是这次要按哪些模型取上限：裸模型就一个，聚合模型是它**当前可调用**的
// 那些生成段（见 probeModelsOf）。逐个模型查再取最大值 —— 走默认段那一个会在
// "参考族的时长档比帧族宽"这类配置上报小，客户端于是把用户能选的档位藏掉。
func limitsOf(entry *dto.HiloMediaModel, models []string, transport string) Limits {
	limits := Limits{}
	if entry != nil {
		limits.MaxReferenceImages = entry.MaxRefs
		if duration, ok := entry.Params["duration"]; ok {
			limits.MaxDurationSeconds = maxNumeric(duration.Options)
		}
	}
	if transport == setting.TransportVideosTask && limits.MaxDurationSeconds == 0 {
		for _, platformModel := range models {
			for _, taskType := range taskTypesOf(platformModel) {
				if durations, configured := common.VideoDurationsAllowedForModel(taskType, platformModel); configured {
					if seconds := maxNumeric(durations); seconds > limits.MaxDurationSeconds {
						limits.MaxDurationSeconds = seconds
					}
				}
			}
		}
	}
	// 输入文件上限取运营配的那份；没配就是 0（不额外限制）。
	for _, platformModel := range models {
		for _, taskType := range append([]string{""}, taskTypesOf(platformModel)...) {
			if bytes, configured := common.VideoMaxInputBytesForModel(taskType, platformModel); configured && bytes > limits.MaxInputBytes {
				limits.MaxInputBytes = bytes
			}
		}
	}
	return limits
}

// taskTypesOf 这个模型在体验区配置里声明过的 task_type 候选集。
//
// 拿不到就返回空 —— **不猜**。"猜一个 t2v" 会让客户端把参考图任务报成文生视频，
// 而请求发出去是被引擎拒，报错指向的是提示词。
func taskTypesOf(platformModels ...string) []string {
	var names []string
	for _, name := range platformModels {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	return common.PlaygroundTaskTypeCandidates(names...)
}

// isMediaModel 这个模型是否被配成了媒体模型（图片/视频/语音/音乐）。
func isMediaModel(name string) bool {
	if len(common.MediaModelKinds(name)) > 0 {
		return true
	}
	if common.GetAggregateModel(name) != nil {
		return true
	}
	catalog := setting.GetHiloCatalog()
	for _, entries := range [][]setting.HiloCatalogEntry{catalog.Image, catalog.Video, catalog.Audio} {
		for _, entry := range entries {
			for _, platformModel := range entry.PlatformModelsOf() {
				if platformModel == name {
					return true
				}
			}
		}
	}
	return false
}

// hasChatEndpoint 这个模型能不能当对话模型用。
//
// 端点类型取自已同步的定价元数据（`model.GetModelSupportEndpointTypes`）。**只有
// 明确是对话类端点才算** —— 反过来"不是媒体端点就当对话模型"会把 embedding、
// rerank、审核模型一起报进对话选择器。
func hasChatEndpoint(name string) bool {
	for _, endpoint := range chatEndpointTypes(name) {
		switch endpoint {
		case constant.EndpointTypeOpenAI, constant.EndpointTypeOpenAIResponse,
			constant.EndpointTypeOpenAIResponseCompact, constant.EndpointTypeAnthropic,
			constant.EndpointTypeGemini:
			return true
		}
	}
	return false
}

// chatEndpointTypes 是对模型端点元数据的读取口，用例可以替换它来固定"谁是对话模型"。
// 生产直接走模型定价元数据（由渠道能力同步而来）。
var chatEndpointTypes = model.GetModelSupportEndpointTypes

// warnedMissingDefaultChatModel 记录已经报过"配置的默认对话模型未下发"的模型名。
var warnedMissingDefaultChatModel sync.Map

func capabilityID(operation, platformModel string) string {
	return operation + ":" + platformModel
}

// sortCapabilities 稳定排序：操作字典序 → 默认优先（由 markDefaults 之后仍稳定）→ 模型名。
func sortCapabilities(capabilities []Capability) {
	sort.SliceStable(capabilities, func(i, j int) bool {
		if capabilities[i].Operation != capabilities[j].Operation {
			return capabilities[i].Operation < capabilities[j].Operation
		}
		return capabilities[i].Model < capabilities[j].Model
	})
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// maxNumeric 一组档位字符串里的最大值；认不出的跳过。
func maxNumeric(values []string) int {
	best := 0
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		number, err := strconv.ParseFloat(trimmed, 64)
		if err != nil {
			// "2K" / "768P" 这类档位词不是秒数，跳过。
			continue
		}
		if int(number) > best {
			best = int(number)
		}
	}
	return best
}

// FormatVersion 给日志/测试用的可读形式。
func FormatVersion(capabilities []Capability, roles DefaultRoles) string {
	return fmt.Sprintf("%d capabilities, version %s", len(capabilities), Version(capabilities, roles))
}

// hiloTextModalities 把 Hilo 目录 text 条目的 supports* 标记翻译成输入模态。
// 这是模态的唯一来源：客户端不再各自硬编码或猜。
func hiloTextModalities(entries []setting.HiloTextEntry) map[string][]string {
	if len(entries) == 0 {
		return nil
	}
	out := make(map[string][]string, len(entries))
	for _, entry := range entries {
		model := strings.TrimSpace(entry.PlatformModel)
		if model == "" {
			continue
		}
		modalities := make([]string, 0, 3)
		if entry.Model.SupportsImage {
			modalities = append(modalities, "image")
		}
		if entry.Model.SupportsVideo {
			modalities = append(modalities, "video")
		}
		if entry.Model.SupportsAudio {
			modalities = append(modalities, "audio")
		}
		if len(modalities) > 0 {
			out[model] = modalities
		}
	}
	return out
}

// DefaultRoles 能力目录里的角色默认值，和 Hilo 目录共用同一份配置。
type DefaultRoles struct {
	// Chat 默认对话模型（等价于 chat 条目里 Default=true 的那条）。
	Chat string `json:"chat,omitempty"`
	// Multimodal 多模态识别模型；目录没配或该模型对这个 key 不可见时为空。
	Multimodal string `json:"multimodal,omitempty"`
}

// ResolveDefaultRoles 从已构建的能力清单里解析角色默认值。
//
// Multimodal 报的是"这个 key 真的能看到、**且真能吃图片/视频**"的模型：只判可见性
// 不够 —— 配错时会把图片发给一个只能读文字的模型，错误从上游回来，看不出是配置问题。
func ResolveDefaultRoles(capabilities []Capability) DefaultRoles {
	roles := DefaultRoles{}
	vision := make(map[string]bool, len(capabilities))
	for _, capability := range capabilities {
		if capability.Operation != OperationChat {
			continue
		}
		if capability.Default && roles.Chat == "" {
			roles.Chat = capability.Model
		}
		for _, modality := range capability.InputModalities {
			if modality == "image" || modality == "video" {
				vision[capability.Model] = true
				break
			}
		}
	}
	if configured := strings.TrimSpace(setting.GetHiloCatalog().MultimodalModel); configured != "" && vision[configured] {
		roles.Multimodal = configured
	}
	return roles
}
