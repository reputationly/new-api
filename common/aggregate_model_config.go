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

	// Mode 增强怎么做。**出厂配置用 singlecall;字段留空则是 text。**
	//
	//	singlecall  一次调用同时产出生产记录(content_plan)与最终提示词。
	//	            输出 H3 官方格式(英文三节);程序只做轻量传输检查。
	//	            见 service/aggregate_enhance_singlecall.go。
	//	text        一次改写:模板 + 原提示词 → 模型直接吐出改写后的提示词。
	//	            输出官方客户端那套中文格式(全局基准 →【镜头N】)。
	//	            **只有这个模式会用 system_prompt。**
	//	qwen_pe     Qwen-Image-2.1(图片):官方 PE 系统提示词 + JSON 协议,产出
	//	            改写提示词与画幅;失败直接用原始提示词(不回落 text)。
	//	            见 service/aggregate_enhance_qwenpe.go。
	//
	// singlecall 换来的是**错误可指认**:文本改写吐出来的东西没有任何地方
	// 能校验,镜头时长加起来不等于请求时长、参考图被描述成一张根本没传的图、
	// 用户写明的台词被翻译掉 —— 这些在 text 模式下全都不报错,只是出来的
	// 视频不对。
	//
	// 失败**回落 text**,不是直接用原始提示词 —— 直接掉到原文会让开着比
	// 不开还差,于是没人敢开,于是永远收不到真实失败样本。
	//
	// 曾经还有个 "ir" 模式(模型吐 canonical IR → 程序确定性渲染 → 独立
	// 审计)。上游 2026-09-10 就废弃了那套架构,我们 09-14 才照着它移植,
	// 且从未在生产启用过 —— 已整体删除,原委记在 service/h3v20/README.md。
	//
	// 空 = text:老配置不能因为新增了一个字段就改变行为。**出厂默认写在
	// DefaultAggregateModelConfig 里,不靠这里的零值。**
	Mode string `json:"mode"`

	// TimeoutSeconds 增强段的时间预算(秒)。0 = **按模式**取内置默认:
	//
	//	singlecall  120 秒(service.singleCallTimeout)
	//	text        走 enhanceTimeout,不看这个字段
	//
	// **这段时间直接加在客户提交请求之前**,所以它是个需要按实际模型调的
	// 参数,而不是一个可以拍脑袋定死的常量 —— 慢的那一半跟模型的"思考"
	// 长度强相关,换个模型就是另一条分布。
	//
	// 配小了的症状是**静默的**:每次超时、每次回落 text 改写,看起来像
	// "增强没什么效果",实际是一次都没跑成。干跑校验会对配得过小的 singlecall 预算提醒。
	TimeoutSeconds int `json:"timeout_seconds"`

	// Thinking 让增强模型开启"思考"。**漏写 = 关闭。**
	//
	// # 为什么默认关
	//
	// 思考型模型会从用户消息出发**重新推导任务是什么**,而不是照系统提示词
	// 里的 schema 写。实测 qwen3.8-flash-fp8 五次里有两次整份跑偏,交来一份
	// 自造的"视频生成请求"(prompt / negative_prompt / audio_prompt),
	// Context-IR 的字段一个都没有 —— 它自己的思考记录写着
	// "This is a video generation API?",它是在猜。
	//
	// 关掉之后(各 5 次实测):
	//
	//	开 thinking   38.9-97.4 秒   3/5 通过
	//	关 thinking   16.3-21.2 秒   4/5 通过(那 1 次是我们字段类型太死,已修)
	//
	// **又快又稳**,没有任何一项变差。视觉理解不受影响 —— 图片和视频的编码
	// 和思考无关,关掉后两个模型对计数/方位/形状/运动方向/数量变化/颜色顺序
	// 的判读都照旧正确。
	//
	// 对非思考模型(如 qwen3.8-27b)这个参数是安全的空操作:实测照常返回,
	// 不报错。
	//
	// 留这个开关是因为将来可能换上一个确实需要思考才写得好 IR 的模型;
	// 但那要靠实测数字说话,不是默认。
	Thinking *bool `json:"thinking"`
}

// IsThinking 增强模型是否开启思考(漏写 = 关闭,见字段注释)。
func (p *AggregatePromptEnhance) IsThinking() bool {
	return p != nil && p.Thinking != nil && *p.Thinking
}

// 增强模式取值。
const (
	EnhanceModeText = "text"
	// EnhanceModeSingleCall 一次调用直接拿到 H3 提示词(移植自上游 v20)。
	// 见 service/aggregate_enhance_singlecall.go。
	EnhanceModeSingleCall = "singlecall"
	// EnhanceModeQwenPE Qwen-Image-2.1 官方 PE 协议:官方系统提示词 → JSON
	// (rewritten_prompt / wh_ratio / ratio_follow) → 传输检查 → 定画幅。
	// 见 service/aggregate_enhance_qwenpe.go。
	EnhanceModeQwenPE = "qwen_pe"
)

// EnhanceMode 归一化后的增强模式(空/未知 = text)。
//
// 未知值回落到 text 而不是报错:配置是运营手写的,拼错一个模式名不该让
// 整条生成链路挂掉 —— 退回到老行为是安全的那一侧。
func (p *AggregatePromptEnhance) EnhanceMode() string {
	if p == nil {
		return EnhanceModeText
	}
	switch strings.ToLower(strings.TrimSpace(p.Mode)) {
	case EnhanceModeSingleCall:
		return EnhanceModeSingleCall
	case EnhanceModeQwenPE:
		return EnhanceModeQwenPE
	}
	return EnhanceModeText
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

// RawAggregateModelConfig 返回**未经解析与过滤**的原始配置串。
//
// 给干跑校验用。GetAggregateModels 是过滤视图:配置坏掉时返回空表、enabled:false 的
// 条目被丢弃 —— 拿它做校验就分不出「没有配置」和「配置坏了」,后者会被报成"没什么可校验的,
// 一切正常",正是干跑校验要消除的那种假绿灯。
func RawAggregateModelConfig() string {
	OptionMapRWMutex.RLock()
	defer OptionMapRWMutex.RUnlock()
	return OptionMap["AggregateModelConfig"]
}

// ParseAggregateModelList 解析配置串为**完整列表**(保留 enabled:false 的条目),
// 解析失败返回错误而不是静默降级。与 GetAggregateModels 的分工:那个服务于运行时
// (只要能用的),这个服务于校验(要看全部,并且要知道解析有没有失败)。
func ParseAggregateModelList(raw string) ([]*AggregateModel, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var items []*AggregateModel
	if err := UnmarshalJsonStr(raw, &items); err != nil {
		return nil, err
	}
	return items, nil
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
