package common

import (
	"strings"
	"sync"
)

// 聚合(编排)模型配置。超管在系统设置里维护,存 OptionMap 的 "AggregateModelConfig" 键
// (JSON 字符串,与 VideoModelConfig 同一套存取惯例)。
//
// 一个聚合模型对外只是一个模型名,背后是一条固定流水线:
//
//	图片: 提示词增强 → 生成
//	视频: 提示词增强 → 生成 → 超分
//
// 存在的理由是外部 API 调用方享受不到体验区的前端编排 —— 体验区那套 1080P 自动超分
// (useVideoGeneration 两段串联)只在我们自己的前端里跑,集成方直连 API 时拿到的是裸的
// 模型能力。聚合模型把同一条流水线搬到后端,让"一个模型名"就能交付同样的结果。
//
// **刻意只支持这三种固定 stage,不做 DAG / 条件分支**:一旦通用化,计费口径、失败退款、
// 超时预算全都变成开放问题,而它们每一个都要单独想清楚。需要第四种玩法时再加一种 kind,
// 比先造一个工作流引擎再往里塞语义要便宜得多。
// ── 关于本文件里 bool 字段的默认值,一次说清 ────────────────────────────────
//
// 手工编辑配置漏写一个 bool 是常态,所以每个 bool 都要问:**漏写时落到哪一侧更安全**。
// 判据是失败可见性,不是"保持一致":
//
//   - 漏写导致**立刻可见的失败** → plain bool(默认 false)。如下面的 Enabled:
//     漏写则该聚合模型不生效,集成方拿到 404,一秒就发现。而且"对外新增一个能力"
//     本就该是显式动作,默认启用是错的。
//   - 漏写导致**静默劣化** → *bool + accessor(默认 true)。如 Hidden(漏写就把定向
//     能力公开了)、PromptEnhance.Enabled / Upscale.Enabled(漏写就静默少跑一段,
//     产出变差却不报错)、SendInputImages(漏写会让增强模型主动编错)。
//
// 别把这两类"统一"成一种写法 —— 它们要防的是相反方向的事故。
type AggregateModel struct {
	Name string `json:"name"`
	Type string `json:"type"` // image | video
	// Enabled 漏写 = 不启用。见上方关于默认值的说明:失败可见,且对外新增能力应显式。
	Enabled bool     `json:"enabled"`
	Note    string   `json:"note"`
	Groups  []string `json:"groups"` // 空 = 继承 generate 段模型的分组

	// Hidden 不在任何对外列表中出现(模型广场 / /v1/models / 定价同步 …)。
	// 指针 + nil 视为 true:聚合模型的常态就是隐藏(它是给指定集成方的定向能力),
	// 配置里不写这个字段的人要的一定是隐藏,而不是"忘了配所以公开了"。
	Hidden *bool `json:"hidden"`

	PromptEnhance *AggregatePromptEnhance `json:"prompt_enhance"`
	Generate      AggregateGenerate       `json:"generate"`
	Upscale       *AggregateUpscale       `json:"upscale"` // 仅 video;nil = 不超分
}

// AggregatePromptEnhance 提示词增强段。留空的字段一律继承体验区已配好的那份
// (体验区的系统提示词是三级取值:模型级 → tab 级 → 内置默认)。
//
// **模板知识不抄第二份**:它是模型相关的(H3 要带字段名的分段结构、LTX-2.5 要长段视听
// 描述、通用版要一句话镜头描述),抄两份必然漂移,而漂移的症状是"不报错、默默出差档"。
type AggregatePromptEnhance struct {
	// Enabled 漏写 = 开启:配了 prompt_enhance 这一段的人就是想用它,漏写 enabled 而
	// 静默不增强属于"默默出差档"那一类,不报错、只是效果变差。要临时停用请显式写 false
	// (保留整段配置,不必删掉再重配)。
	Enabled *bool  `json:"enabled"`
	Model   string `json:"model"` // 空 = 继承体验区通用设置里的优化模型
	Group   string `json:"group"` // 空 = 继承
	// SystemPrompt 空 = 继承体验区模板(模型级 → tab 级 → 内置默认)。
	SystemPrompt string `json:"system_prompt"`
	// SendInputImages 把本次请求的输入图一并发给增强模型。**漏写 = 开启**,关掉要非常
	// 清楚后果:图生图缺了它,增强模型只能从文字猜,会**主动编错** —— 不是效果打折,是
	// 产出与底图打架(用户传彩色油画、增强模型写出"黑白纪实摄影",而生成模型看得见底图)。
	//
	// 必须是 *bool:plain bool 漏写就是 false,恰好落进上面这个有害分支,而且不报错。
	SendInputImages *bool `json:"send_input_images"`
}

// IsEnabled 提示词增强段是否启用(段不存在 = 不启用;存在但漏写 enabled = 启用)。
func (p *AggregatePromptEnhance) IsEnabled() bool {
	if p == nil {
		return false
	}
	return p.Enabled == nil || *p.Enabled
}

// IsSendInputImages 是否把输入图一并发给增强模型(漏写 = 是,见字段注释)。
func (p *AggregatePromptEnhance) IsSendInputImages() bool {
	if p == nil {
		return false
	}
	return p.SendInputImages == nil || *p.SendInputImages
}

// AggregateGenerate 生成段:真正出图/出视频的那次调用。
type AggregateGenerate struct {
	Model string `json:"model"`
	// Overrides 下发给生成段的参数覆盖(如 size)。开了超分时这里配的是**中间产物**的
	// 尺寸,不是客户最终拿到的尺寸 —— 配置页必须把这层关系显式写出来,否则运营会以为
	// 这里配的就是最终输出。
	Overrides map[string]any `json:"overrides"`
}

// AggregateUpscale 超分段(仅视频)。
type AggregateUpscale struct {
	// Enabled 漏写 = 开启,理由同 AggregatePromptEnhance.Enabled:漏写而静默少跑一段,
	// 客户拿到的是未超分的产物却没有任何报错。
	//
	// 与 AggregateModel.Upscale 为 nil 的区别:nil 是"这个聚合模型没有超分段",
	// Enabled=false 是"有这段配置但临时停用"。两者在配置页上是不同的操作
	// (删除整段 vs 关一个开关),不要合并。
	Enabled *bool  `json:"enabled"`
	Model   string `json:"model"`       // 须具备 sr capability
	Target  string `json:"target_size"` // 如 2k
}

// IsEnabled 超分段是否启用(段不存在 = 不超分;存在但漏写 enabled = 启用)。
func (u *AggregateUpscale) IsEnabled() bool {
	if u == nil {
		return false
	}
	return u.Enabled == nil || *u.Enabled
}

// IsHidden 该聚合模型是否对外隐藏(未显式配置即隐藏,见 Hidden 字段注释)。
func (m *AggregateModel) IsHidden() bool {
	if m == nil {
		return false
	}
	return m.Hidden == nil || *m.Hidden
}

var (
	aggregateConfigLock sync.RWMutex
	aggregateConfigRaw  string
	aggregateModels     map[string]*AggregateModel
)

// GetAggregateModels 返回「模型名 → 配置」,只含 enabled 的项。
//
// 按 raw 字符串做缓存失效:OptionMap 的写入方(系统设置保存 / 多节点周期同步)不会通知
// 这里,拿 raw 比对是唯一无需改动写入方的判据,代价是一次字符串比较。
func GetAggregateModels() map[string]*AggregateModel {
	OptionMapRWMutex.RLock()
	raw := OptionMap["AggregateModelConfig"]
	OptionMapRWMutex.RUnlock()

	aggregateConfigLock.RLock()
	if aggregateModels != nil && aggregateConfigRaw == raw {
		cached := aggregateModels
		aggregateConfigLock.RUnlock()
		return cached
	}
	aggregateConfigLock.RUnlock()

	parsed := parseAggregateModels(raw)

	aggregateConfigLock.Lock()
	aggregateConfigRaw = raw
	aggregateModels = parsed
	aggregateConfigLock.Unlock()
	return parsed
}

// parseAggregateModels 解析配置。解析失败返回空表 —— 配置坏掉时"没有聚合模型"是安全的
// 降级(集成方拿到 404),而返回半份配置会让流水线以缺字段的形态跑起来(比如少了 upscale
// 段的视频聚合模型,静默少跑一段)。
//
// 失败时那个 return **不是冗余**,尽管当前删掉它行为也不变:今天的 JSON 库在数组部分
// 坏掉时不填充任何元素(items 长度为 0),于是走不走下面的循环结果都一样。但
// common/json.go 存在的全部理由就是"将来可换更快的 JSON 库"(见 CLAUDE.md Rule 1),
// 而换库恰恰可能改掉这个行为——届时没有这个 return,半份配置就会生效。
// TestAggregateModelPartiallyBrokenConfigDiscardsAll 固化的是这个**契约**,
// 它在当前库下无法通过变异见红,原因是变异无效而非测试假。
func parseAggregateModels(raw string) map[string]*AggregateModel {
	out := make(map[string]*AggregateModel)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return out
	}
	var items []*AggregateModel
	if err := UnmarshalJsonStr(raw, &items); err != nil {
		SysError("AggregateModelConfig 解析失败,已按「无聚合模型」降级: " + err.Error())
		return out
	}
	for _, it := range items {
		if it == nil {
			continue
		}
		it.Name = strings.TrimSpace(it.Name)
		if it.Name == "" || !it.Enabled {
			continue
		}
		out[it.Name] = it
	}
	return out
}

// GetAggregateModel 取一个 enabled 的聚合模型配置;不存在返回 nil。
func GetAggregateModel(name string) *AggregateModel {
	return GetAggregateModels()[strings.TrimSpace(name)]
}

// IsAggregateModelHidden 该模型名是否为「应从对外列表隐藏」的聚合模型。
//
// 给各列表出口做统一判定用。非聚合模型一律返回 false —— 本机制只隐藏聚合模型,
// 不是通用的模型隐藏开关(那是 model_visibility 的领域,语义也不同:那边管的是
// 「谁能看且能调」,这里是「谁都看不到、但知道名字能调」)。
func IsAggregateModelHidden(name string) bool {
	m := GetAggregateModel(name)
	return m != nil && m.IsHidden()
}

// HasHiddenAggregateModels 全站是否存在任何隐藏的聚合模型。
// 供各过滤点短路:绝大多数站点一个都没配,不该为每个模型做一次 map 查找与切片重建。
func HasHiddenAggregateModels() bool {
	for _, m := range GetAggregateModels() {
		if m.IsHidden() {
			return true
		}
	}
	return false
}

// FilterHiddenAggregateModels 从模型名列表里剔除隐藏的聚合模型,保持原有顺序。
func FilterHiddenAggregateModels(models []string) []string {
	if !HasHiddenAggregateModels() {
		return models
	}
	out := make([]string, 0, len(models))
	for _, name := range models {
		if !IsAggregateModelHidden(name) {
			out = append(out, name)
		}
	}
	return out
}
