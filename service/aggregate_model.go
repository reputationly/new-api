package service

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// 聚合模型的「干跑校验」(dry-run):不真的生成,只把配置放到当前站点的模型/分组/能力
// 现状里对一遍。
//
// 为什么需要它,以及为什么它不与体验区重叠 —— 体验区能验证的是**效果**(模板写得好不好、
// 出来的片子行不行),验证不了**配置**:三段模型名能不能路由、分组继承出来是谁、超分模型
// 到底有没有超分能力、图片类型误配了超分段。这些恰恰是这套配置最容易错的地方,而且
// **全都不报错** —— 错了只会在真实调用时失败,或者更糟:静默少跑一段、默默出差档。
//
// 所以这里只做零成本的静态校验(不消耗 GPU、不计费、秒回),效果验证交给体验区。

// AggregateCheckLevel 单项校验结论。
const (
	AggregateCheckOK    = "ok"
	AggregateCheckWarn  = "warn"
	AggregateCheckError = "error"
)

// srCapability 视频超分能力标签。
//
// **是中文字面量,不是 "sr"**:能力标签在 VideoModelConfig 里就是以中文存的
// (web/classic/src/constants/videoPlayground.constants.js 的 VIDEO_SR_CAPABILITY,
// sr 只是体验区 tab 的键名)。写成 "sr" 会让校验对任何模型都报「不支持超分」,
// 而且是那种"看起来在工作"的错误。
const srCapability = "视频超分"

// trimNonEmpty 逐项去空白并丢掉空串。运营手工编辑 JSON 时的多余空格不该变成校验失败。
func trimNonEmpty(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// AggregateCheck 一条校验结果。
type AggregateCheck struct {
	Key     string `json:"key"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

// AggregateDryRunResult 一个聚合模型的完整校验结论。
type AggregateDryRunResult struct {
	Name string `json:"name"`
	// Groups 该聚合模型最终生效的可用分组(已把"留空继承"解析成具体值),
	// 让运营看到继承结果而不是一片空白。
	Groups    []string         `json:"groups"`
	Inherited bool             `json:"groups_inherited"`
	Checks    []AggregateCheck `json:"checks"`
	// Billable 本流水线会产生几笔计费。分段计费下客户账单会出现这些模型名,
	// 运营配置时就该知道,而不是等客户来问。
	Billable []string `json:"billable_models"`
	Passed   bool     `json:"passed"`
}

// modelFacts 从定价缓存取一个模型的现状(是否可路由、可用分组、能力标签)。
// GetPricing 的数据源是 abilities,所以"在里面"等价于"当前有启用的渠道能接它"。
func modelFacts(name string) (exists bool, groups []string, caps []string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return false, nil, nil
	}
	for _, p := range model.GetPricing() {
		if p.ModelName == name {
			return true, p.EnableGroup, p.CapabilityTags
		}
	}
	return false, nil, nil
}

// 测试接缝:model.GetPricing 是包级函数,没有真实 DB 时无法构造"模型存在"的场景,
// 而恰恰是存在时的那些校验(超分能力、分组继承、分组不一致)最有价值。
// 手法与 relay/task_media_offload.go 的 defaultUploader 一致。
var (
	currentModelFacts = modelFacts
	pricingLoaded     = func() bool { return len(model.GetPricing()) > 0 }
)

// unknownLevel 决定「查不到这个模型」该报成什么级别。
//
// 定价缓存为空时(DB 不可达 / abilities 查询失败 / 站点确实一个模型都没配),
// **查不到 ≠ 不存在** —— 此时把每一段都判成"没有可用渠道"会给运营一屏红色误报,
// 让人去改一份本来没问题的配置。降级为 warn 并说明"无法校验",比自信地报错有用。
func unknownLevel() string {
	if pricingLoaded() {
		return AggregateCheckError
	}
	return AggregateCheckWarn
}

// unknownSuffix 缓存不可用时追加的说明,避免运营把"没校验成"当成"配错了"。
func unknownSuffix() string {
	if pricingLoaded() {
		return ""
	}
	return "(注意:当前定价缓存为空,无法确认模型是否真的不可用,请稍后重试)"
}

// DryRunAggregateModel 校验单个聚合模型配置。peers 为同一份配置里的其它模型名,用于查重。
func DryRunAggregateModel(m *common.AggregateModel, peers map[string]int) *AggregateDryRunResult {
	res := &AggregateDryRunResult{Name: strings.TrimSpace(m.Name)}
	add := func(key, level, format string, args ...any) {
		res.Checks = append(res.Checks, AggregateCheck{
			Key: key, Level: level, Message: fmt.Sprintf(format, args...),
		})
	}

	// —— 1. 基本形态 ——
	if res.Name == "" {
		add("name", AggregateCheckError, "模型名不能为空")
	} else if peers[res.Name] > 1 {
		add("name", AggregateCheckError, "配置里存在同名聚合模型 %q", res.Name)
	} else if exists, _, _ := currentModelFacts(res.Name); exists {
		// 与真实模型重名会造成路由歧义:同一个名字既指向渠道又指向流水线。
		add("name", AggregateCheckError,
			"模型名 %q 与已有的真实模型重名,会造成路由歧义,请换一个名字", res.Name)
	} else {
		add("name", AggregateCheckOK, "模型名可用")
	}

	switch m.Type {
	case "image", "video":
	default:
		add("type", AggregateCheckError, "type 必须是 image 或 video,当前为 %q", m.Type)
	}

	// —— 2. 生成段 ——
	genExists, genGroups, _ := currentModelFacts(m.Generate.Model)
	if strings.TrimSpace(m.Generate.Model) == "" {
		add("generate", AggregateCheckError, "必须指定生成段模型")
	} else if !genExists {
		add("generate", unknownLevel(),
			"生成段模型 %q 当前没有可用渠道(不在启用的 abilities 里),调用会失败%s",
			m.Generate.Model, unknownSuffix())
	} else {
		add("generate", AggregateCheckOK, "生成段模型 %q 可路由", m.Generate.Model)
		res.Billable = append(res.Billable, m.Generate.Model)
	}

	// —— 3. 分组:留空继承生成段 ——
	// 逐项 trim,与本函数对其它运营输入(Name / 三个 Model / Target)的处理一致。
	// 配置目前仍是手工编辑的 JSON,`["default "]` 这样的多余空格会让比对失配,
	// 报出一条 error 级别的"该分组无可用渠道" —— 而空格在渲染后的消息里看不见,
	// 运营只会照着错误的方向去查渠道配置。
	res.Groups = trimNonEmpty(m.Groups)
	if len(res.Groups) == 0 {
		res.Groups = genGroups
		res.Inherited = true
		if len(res.Groups) > 0 {
			add("groups", AggregateCheckOK, "未指定分组,继承生成段模型的可用分组:%s",
				strings.Join(res.Groups, ", "))
		}
	} else if !pricingLoaded() {
		// 缓存为空时 genGroups 必然是 nil,此时"每个分组都不可用"不是一个发现,
		// 而是"我们什么都不知道"。照常判定会把每个显式配了分组的模型都标红,
		// 正是 unknownLevel() 要避免的那种误报 —— 只不过这条判定不经过它,
		// 所以必须在这里单独挡一次(见 TestDryRunGroupCheckDegradesWhenPricingUnavailable)。
		add("groups", AggregateCheckWarn,
			"当前定价缓存为空,无法校验分组与生成段模型的一致性,请稍后重试")
	} else {
		// 聚合模型开放的分组必须是生成段模型也能用的,否则客户能调进来、内部段却没渠道。
		var missing []string
		for _, g := range res.Groups {
			if !common.StringsContains(genGroups, g) {
				missing = append(missing, g)
			}
		}
		if len(missing) > 0 {
			add("groups", AggregateCheckError,
				"分组 %s 下生成段模型 %q 无可用渠道,这些分组的客户调用会失败",
				strings.Join(missing, ", "), m.Generate.Model)
		} else {
			add("groups", AggregateCheckOK, "分组配置与生成段模型一致")
		}
	}

	// —— 4. 提示词增强段 ——
	if m.PromptEnhance.IsEnabled() {
		enhModel := strings.TrimSpace(m.PromptEnhance.Model)
		// 模板必填。后端**读不到**体验区那份内置默认模板 —— 它是前端 JS 常量
		// (promptOptimize.constants.js),运营在 options 里的改写后端能读,内置默认读不到。
		// 与其把那几份模板抄一份到 Go(抄两份必然漂移,且漂移不报错、只是默默出差档),
		// 不如要求聚合模型显式写一份:配漏了在这里就报出来,而不是上线后静默降级。
		if strings.TrimSpace(m.PromptEnhance.SystemPrompt) == "" {
			add("enhance_template", AggregateCheckError,
				"启用了提示词增强但未配置模板(system_prompt):运行时会降级为使用原始提示词,"+
					"等于增强没生效")
		}
		if enhModel == "" {
			add("prompt_enhance", AggregateCheckError, "启用了提示词增强但未指定增强模型")
		} else if exists, _, _ := currentModelFacts(enhModel); !exists {
			add("prompt_enhance", unknownLevel(),
				"增强模型 %q 当前没有可用渠道,增强会失败(将降级为使用原始提示词)%s",
				enhModel, unknownSuffix())
		} else {
			add("prompt_enhance", AggregateCheckOK, "增强模型 %q 可路由", enhModel)
			res.Billable = append(res.Billable, enhModel)
			// 增强以客户身份自调用一次 /v1/chat/completions,会过令牌的模型白名单。
			// 客户令牌若开了白名单却没放行这个模型,增强每次都吃 403 并降级 ——
			// 不报错、只是增强静默失效,是最难察觉的那类失败。
			add("enhance_token_whitelist", AggregateCheckWarn,
				"增强以客户身份调用 %q:若集成方的令牌开启了模型白名单,需把该模型一并加入,否则增强会被拒并降级",
				enhModel)
		}
		if !m.PromptEnhance.IsSendInputImages() {
			// 不是错误,但后果隐蔽到必须警告一次。
			add("send_input_images", AggregateCheckWarn,
				"已关闭「把输入图发给增强模型」:图生图场景下增强模型看不到底图,"+
					"会凭文字臆造描述并与底图打架(产出对着干,不是效果打折)")
		} else if enhModel != "" {
			// 开着传图就必须配一个**支持视觉**的模型。
			//
			// 这里只能提示、不能校验:全站没有任何一处声明过"某个 LLM 支不支持视觉"
			// —— CapabilityTags 是媒体玩法能力(图生视频那类),Tags 是运营手写的自由
			// 文本,都不表达多模态。硬猜模型名(带 vision/4o 就算)只会在改名换代时误判。
			//
			// 配错的表现极不显眼:多数纯文本模型收到 image_url 要么报错、要么直接忽略
			// 图片照常回一段文字 —— 后者会让增强"看起来在工作",实际退化成没有底图的
			// 纯文字臆造,正是上面那条警告描述的坏结果。
			add("enhance_vision", AggregateCheckWarn,
				"增强模型 %q 会收到输入图,请确认它**支持视觉输入**;"+
					"纯文本模型可能直接忽略图片并照常返回文字,增强会静默退化成凭空臆造",
				enhModel)
		}
	}

	// —— 5. 超分段 ——
	switch {
	case m.Upscale == nil:
		if m.Type == "video" {
			add("upscale", AggregateCheckOK, "未配置超分段,产物为生成段原始分辨率")
		}
	case m.Type == "image":
		add("upscale", AggregateCheckError, "图片类型不支持超分段,请删除该段")
	case !m.Upscale.IsEnabled():
		add("upscale", AggregateCheckWarn, "超分段已停用,客户拿到的是生成段原始分辨率")
	default:
		srModel := strings.TrimSpace(m.Upscale.Model)
		exists, srGroups, caps := currentModelFacts(srModel)
		switch {
		case srModel == "":
			add("upscale", AggregateCheckError, "启用超分段但未指定超分模型")
		case !exists:
			add("upscale", unknownLevel(),
				"超分模型 %q 当前没有可用渠道,超分段会失败%s", srModel, unknownSuffix())
		case !common.StringsContains(caps, srCapability):
			add("upscale", AggregateCheckError,
				"模型 %q 没有「%s」能力,不能用作超分段", srModel, srCapability)
		default:
			add("upscale", AggregateCheckOK, "超分模型 %q 可路由且具备超分能力", srModel)
			res.Billable = append(res.Billable, srModel)
			// 内部段由我们代为调用,但仍要落到某个有渠道的分组上。
			if len(res.Groups) > 0 {
				var unreachable []string
				for _, g := range res.Groups {
					if !common.StringsContains(srGroups, g) {
						unreachable = append(unreachable, g)
					}
				}
				if len(unreachable) > 0 {
					add("upscale_groups", AggregateCheckWarn,
						"分组 %s 下超分模型 %q 无可用渠道,这些分组的超分段会失败(生成段仍会计费)",
						strings.Join(unreachable, ", "), srModel)
				}
			}
		}
	}

	// && 短路:Upscale 为 nil 时 IsEnabled() 已返回 false,不会解引用 Target。
	if m.Upscale.IsEnabled() && strings.TrimSpace(m.Upscale.Target) == "" {
		add("upscale_target", AggregateCheckWarn, "未指定超分目标尺寸")
	}

	res.Passed = true
	for _, ch := range res.Checks {
		if ch.Level == AggregateCheckError {
			res.Passed = false
			break
		}
	}
	return res
}

// DryRunAggregateConfig 校验整份配置(逐个模型),并做跨条目查重。
func DryRunAggregateConfig(models []*common.AggregateModel) []*AggregateDryRunResult {
	peers := make(map[string]int, len(models))
	for _, m := range models {
		if m != nil {
			peers[strings.TrimSpace(m.Name)]++
		}
	}
	out := make([]*AggregateDryRunResult, 0, len(models))
	for _, m := range models {
		if m == nil {
			continue
		}
		out = append(out, DryRunAggregateModel(m, peers))
	}
	return out
}
