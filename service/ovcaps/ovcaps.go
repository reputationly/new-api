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

	catalog := setting.GetHiloCatalog()
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
				if emitted[platformModel] || !scope.available(platformModel) {
					continue
				}
				if scope.TokenAllowed != nil && !scope.TokenAllowed[platformModel] {
					continue
				}
				capability, ok := fromCatalogEntry(entry, platformModel, group.name)
				if !ok {
					continue
				}
				emitted[platformModel] = true
				out = append(out, capability)
			}
		}
	}

	// 聚合模型。目录里没配到的那些（例如管理员只配了官方 schema 的几条）照样要报 ——
	// 客户端的预设用的就是 `qwen-image-pro-enhanced` 这类聚合名。
	for name, agg := range common.GetAggregateModels() {
		if emitted[name] || !scope.aggregateAllows(agg) {
			continue
		}
		operation, transport := aggregateOperation(agg)
		if operation == "" {
			continue
		}
		emitted[name] = true
		out = append(out, Capability{
			ID:          capabilityID(operation, name),
			Operation:   operation,
			Model:       name,
			DisplayName: Localized{Zh: name, En: name},
			Description: Localized{Zh: agg.Note, En: agg.Note},
			Transport:   Transport{Kind: transport},
			TaskTypes:   taskTypesOf(agg.Generate.Model),
			Limits:      limitsOf(nil, agg.Generate.Model, transport),
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
		out = append(out, Capability{
			ID:          capabilityID(OperationChat, name),
			Operation:   OperationChat,
			Model:       name,
			DisplayName: Localized{Zh: name, En: name},
			Transport:   Transport{Kind: setting.TransportChat},
			Limits:      Limits{},
		})
	}

	sortCapabilities(out)
	markDefaults(out)
	return out
}

// Version 目录内容的指纹。客户端按它判断要不要重新拉。
//
// 内容变了它才变；同一个 key 反复请求得到同一个值 —— 所以哈希的输入必须是
// **稳定排序后**的 JSON（由 [Build] 保证），不能带时间戳。
func Version(capabilities []Capability) string {
	raw, err := common.Marshal(capabilities)
	if err != nil {
		// 序列化不了就不给版本号，客户端每次都重拉 —— 比给一个会误导的稳定值好。
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// fromCatalogEntry 把一条目录项翻成能力。
func fromCatalogEntry(entry setting.HiloCatalogEntry, platformModel, group string) (Capability, bool) {
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
	return Capability{
		ID:          capabilityID(operation, platformModel),
		Operation:   operation,
		Model:       platformModel,
		DisplayName: Localized{Zh: display, En: display},
		Description: Localized{Zh: entry.Model.Description, En: entry.Model.Description},
		Transport:   Transport{Kind: transport},
		TaskTypes:   taskTypesOf(platformModel),
		ParamSchema: paramSchema(entry.Model.Params),
		Limits:      limitsOf(&entry.Model, platformModel, transport),
	}, true
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
func aggregateOperation(agg *common.AggregateModel) (string, string) {
	switch strings.TrimSpace(agg.Type) {
	case "image":
		return OperationGenerateImage, setting.TransportImages
	case "video":
		return OperationGenerateVideo, setting.TransportVideosTask
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
func limitsOf(entry *dto.HiloMediaModel, platformModel, transport string) Limits {
	limits := Limits{}
	if entry != nil {
		limits.MaxReferenceImages = entry.MaxRefs
		if duration, ok := entry.Params["duration"]; ok {
			limits.MaxDurationSeconds = maxNumeric(duration.Options)
		}
	}
	if transport == setting.TransportVideosTask && limits.MaxDurationSeconds == 0 {
		for _, taskType := range taskTypesOf(platformModel) {
			if durations, configured := common.VideoDurationsAllowedForModel(taskType, platformModel); configured {
				if seconds := maxNumeric(durations); seconds > limits.MaxDurationSeconds {
					limits.MaxDurationSeconds = seconds
				}
			}
		}
	}
	// 输入文件上限取运营配的那份；没配就是 0（不额外限制）。
	for _, taskType := range append([]string{""}, taskTypesOf(platformModel)...) {
		if bytes, configured := common.VideoMaxInputBytesForModel(taskType, platformModel); configured && bytes > limits.MaxInputBytes {
			limits.MaxInputBytes = bytes
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
	for _, endpoint := range model.GetModelSupportEndpointTypes(name) {
		switch endpoint {
		case constant.EndpointTypeOpenAI, constant.EndpointTypeOpenAIResponse,
			constant.EndpointTypeOpenAIResponseCompact, constant.EndpointTypeAnthropic,
			constant.EndpointTypeGemini:
			return true
		}
	}
	return false
}

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

// markDefaults 每个操作挑一条默认：优先目录里第一条（管理员排过序），
// 否则名字最短的（`qwen-image-pro-enhanced` 比 `qwen-image-pro-enhanced-2` 更像默认）。
func markDefaults(capabilities []Capability) {
	chosen := map[string]int{}
	for index := range capabilities {
		operation := capabilities[index].Operation
		if _, ok := chosen[operation]; !ok {
			chosen[operation] = index
			continue
		}
		current := chosen[operation]
		if len(capabilities[index].Model) < len(capabilities[current].Model) {
			chosen[operation] = index
		}
	}
	for operation, index := range chosen {
		_ = operation
		capabilities[index].Default = true
	}
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
func FormatVersion(capabilities []Capability) string {
	return fmt.Sprintf("%d capabilities, version %s", len(capabilities), Version(capabilities))
}
