package middleware

import (
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service"
)

// 聚合(编排)模型的入口展开。
//
// 聚合模型没有渠道 ability —— 它不是一个能被路由的模型,而是"一个名字 = 一条流水线"。
// 所以在选渠道之前必须把它**展开**成第一段(生成段)的真实模型:改写请求里的 model,
// 后续的渠道选择、计费、日志就全部落在真实模型上,不需要为它们各开一条分支。
//
// 展开点的位置很讲究,必须夹在两件事中间:
//
//   - **在令牌白名单校验之后**:白名单里存的是聚合模型名(集成方拿到的就是那个名字),
//     展开早了会拿生成段模型去比白名单,配得对的令牌反而被拒。
//   - **在选渠道之前**:选渠道要用真实模型名,否则找不到任何 ability。
//
// 分段计费正是这样落地的:展开之后每一段都是一次普通调用,各自预扣、各自记账、各自
// 出现在日志里,不需要为聚合模型单独定价,也不需要给内部段开"免计费"的口子。
// 代价是客户账单上会出现他没直接调过的模型名 —— 这是选分段计费时就接受的取舍。

// AggregateExpansion 一次请求的聚合展开结果,挂在 gin.Context 上供后续阶段读取。
type AggregateExpansion struct {
	// PublicName 客户实际调用的聚合模型名。展开后 model 字段已被改写,
	// 只有这里还留着"客户以为自己在调什么",排障时是第一手信息。
	PublicName string
	Config     *common.AggregateModel
	// Enhance 提示词增强的过程记录(含增强前后的文本、是否降级)。
	// 增强后的 prompt **不回传给客户**,所以客户报"生成的跟我写的不一样"时,
	// 这份记录是唯一能解释清楚的证据。未启用增强时为 nil。
	Enhance *service.EnhanceResult
}

// expandAggregateModel 若 modelName 是一个启用中的聚合模型,校验访问权限并返回展开后的
// 生成段模型名**与其配置**。返回 ("", nil, nil) 表示不是聚合模型,调用方按原样继续。
//
// 权限判定只做「分组是否被允许」这一件事,其余(令牌白名单、可见性)都由既有链路负责:
// 展开发生在它们之后,而展开后的真实模型还会再过一遍渠道选择,该拒的自然会拒。
func expandAggregateModel(modelName, usingGroup, userGroup string) (string, *common.AggregateModel, error) {
	agg := common.GetAggregateModel(modelName)
	if agg == nil {
		return "", nil, nil
	}
	if !groupAllowedForAggregate(agg, usingGroup, userGroup) {
		// 与可见性拦截同口径:对这个用户它就是不存在,不透露隐藏能力的存在。
		return "", nil, fmt.Errorf("model not found")
	}
	if agg.Generate.Model == "" {
		return "", nil, fmt.Errorf("聚合模型 %s 未配置生成段模型", modelName)
	}
	// **把解析出的配置一并返回**,让调用方不必再查一次:配置随时可能被重新保存
	// (管理员保存 / 多节点 option 同步),两次查找之间被改掉的话,第二次会拿到 nil,
	// 而下游立刻解引用它 —— 一个本该干净失败的请求变成 panic。
	return agg.Generate.Model, agg, nil
}

// expandAutoGroups 把 "auto" 展开成用户实际的自动分组集合。做成变量供测试构造 ——
// 真实实现要读运营配置与用户可用分组,在单测里搭不起来,而这条展开正是本判定最容易
// 写错、且写错时表现为"存得进白名单却调不通"的地方,不能没有覆盖。
var expandAutoGroups = service.GetUserAutoGroup

// groupAllowedForAggregate 判断某分组能否使用该聚合模型。
//
// 未配置 Groups = 不额外限制:此时约束完全来自展开后的生成段模型 —— 该分组下它没有渠道
// 的话,选渠道那一步自然会拒。配置了 Groups 才是显式白名单,用于把定向能力圈给指定集成方。
//
// **"auto" 必须先展开再比对**。它是一个合法的 token.Group,但不是任何真实分组的名字,
// 拿字面量去比白名单永远不中。而令牌保存侧(validateTokenModelLimits)对 auto 的处理
// 是展开成 GetUserAutoGroup(user.Group) 再比 —— 两边不一致就会出现:auto 令牌能把
// 聚合模型名存进白名单,每次调用却 404。那正是这套判定要消除的分裂,只是换了个形式。
// 同一函数下方几十行处的渠道选择也是这么展开 auto 的(见 usingGroup == "auto" 分支)。
func groupAllowedForAggregate(agg *common.AggregateModel, usingGroup, userGroup string) bool {
	if agg == nil {
		return false
	}
	if len(agg.Groups) == 0 {
		return true
	}
	candidates := []string{usingGroup}
	if usingGroup == "auto" {
		candidates = expandAutoGroups(userGroup)
	}
	for _, g := range candidates {
		if common.StringsContains(agg.Groups, g) {
			return true
		}
	}
	return false
}

// applyAggregateExpansion 就地改写请求:model 字段换成生成段模型,并把展开结果挂到 context。
//
// **body 也必须改写**,不能只改 modelRequest:适配器构造上游请求时读的是 body 里的 model
// (图片生成尤其明显),只改内存里那份会让上游收到一个它不认识的聚合模型名。
func applyAggregateExpansion(c *gin.Context, publicName, realModel string, agg *common.AggregateModel) error {
	// **刻意不用 UnmarshalBodyReusable**:那个函数按 Content-Type 分派,遇到非
	// json/form/multipart 的类型会走 `else { skip }` 分支——**返回 nil 却什么都不填**。
	// 客户端没带 Content-Type 时,body 会是 nil:轻则这里改写落空(聚合模型名原样发给
	// 上游,上游报未知模型),重则往 nil map 写入直接 panic。而"把 model 换个值"本来
	// 就是纯 JSON 操作,不该看 Content-Type 脸色。直接取字节自己解析,行为与请求头无关。
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return fmt.Errorf("读取请求体失败: %w", err)
	}
	raw, err := storage.Bytes()
	if err != nil {
		return fmt.Errorf("读取请求体失败: %w", err)
	}
	// **UnmarshalWithNumber,不是 Unmarshal**:这里是对客户**整个请求体**做
	// 读-改-写,普通 Unmarshal 会把所有 JSON 数字变成 float64,超过 2^53 的整数
	// (seed、纳秒时间戳、id 形态的 metadata)在 Marshal 回去时被静默改值或写成
	// 指数形式,上游按整数解析直接拒。common/json.go 里这个函数的注释写的就是
	// 本场景("需要原样保留未改写字段再 Marshal 回去"),relay/task_media_rewrite.go
	// 的同类改写也用它。
	var body map[string]any
	if err := common.UnmarshalWithNumber(raw, &body); err != nil {
		return fmt.Errorf("解析请求体失败: %w", err)
	}
	// 解析出 nil(body 为空、或内容是 JSON null)时必须报错,不能就地补一个空 map ——
	// 那样改写完只剩 {"model":...},prompt / size / 输入图全被丢掉,而请求还会照常发出去。
	if body == nil {
		return fmt.Errorf("请求体为空,无法展开聚合模型 %s", publicName)
	}
	// 先套 Overrides,再定 model —— 顺序不能反,见下面对 model 的保护。
	//
	// Overrides 是聚合模型存在的意义所在,不是可选装饰:客户传的尺寸语义是「我要的**最终**
	// 尺寸」,而生成段收到的必须是「**中间**尺寸」。以 2K 视频为例,客户传 size=2k,若原样
	// 透传给 H3 就正好是那个会 OOM / 被钳位的请求(H3 面积上限 768×1344),必须由这里改写
	// 成生成段吃得下的档位,最终的 2K 交给超分段产出。
	for k, v := range agg.Generate.Overrides {
		body[k] = v
	}
	// model 由展开结果决定,不允许被 Overrides 顶掉:运营在 overrides 里手滑写一个 model
	// 会让请求发去一个既非聚合模型、也非配置的生成段模型的地方,且不报错。
	body["model"] = realModel

	exp := &AggregateExpansion{PublicName: publicName, Config: agg}

	// 提示词增强跑在这里 —— 生成段请求发出**之前**,否则改写就没有意义了。
	//
	// 它是一次同步的 LLM 调用,会给请求加上几秒延迟;这是这个功能的固有成本,不是缺陷
	// (体验区那条路是用户点按钮等,这里换成我们替他等)。EnhancePrompt 永远不返回错误,
	// 任何失败都体现为"用原始提示词继续",所以这里没有失败分支可漏。
	if agg.PromptEnhance.IsEnabled() {
		prompt, _ := body["prompt"].(string)
		if strings.TrimSpace(prompt) != "" {
			// 带客户的 Authorization 原样发起 —— 增强以客户身份走一遍 relay,
			// 于是计费/限流/日志与他自己调一次 chat 完全一致(分段计费)。
			res := service.EnhancePrompt(c.Request.Context(), agg,
				c.Request.Header.Get("Authorization"),
				prompt, collectInputImages(body))
			exp.Enhance = res
			// 这个判断当前是**冗余**的:EnhanceResult 的契约保证降级时
			// EnhancedPrompt 就等于原 prompt,所以写不写回结果一样(去掉它做变异
			// 测试也不会见红)。留着是为了让"降级不改客户的提示词"这条意图在调用点
			// 就能读到,而不必翻到 service 层去确认契约;万一哪天那个契约变了,
			// 这里也不会跟着出错。
			if !res.Degraded {
				body["prompt"] = res.EnhancedPrompt
			}
		}
	}

	data, err := common.Marshal(body)
	if err != nil {
		return fmt.Errorf("序列化请求体失败: %w", err)
	}
	if err := common.ReplaceRequestBody(c, data); err != nil {
		return fmt.Errorf("改写请求体失败: %w", err)
	}
	common.SetContextKey(c, constant.ContextKeyAggregateExpansion, exp)
	return nil
}

// collectInputImages 从请求体里收集输入图,喂给增强模型看。
//
// 只认这几个顶层字段:它们覆盖了图生图 / 首尾帧 / 参考生视频的入参形态。收不到也无妨
// —— 纯文生场景本来就没有图,增强照常按文字工作。
func collectInputImages(body map[string]any) []string {
	var out []string
	appendVal := func(v any) {
		switch t := v.(type) {
		case string:
			if strings.TrimSpace(t) != "" {
				out = append(out, t)
			}
		case []any:
			for _, item := range t {
				if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
					out = append(out, s)
				}
			}
		}
	}
	for _, key := range []string{"image", "images", "input_reference"} {
		if v, ok := body[key]; ok {
			appendVal(v)
		}
	}
	return out
}

// GetAggregateExpansion 取本次请求的聚合展开结果;非聚合请求返回 nil。
func GetAggregateExpansion(c *gin.Context) *AggregateExpansion {
	v, ok := common.GetContextKey(c, constant.ContextKeyAggregateExpansion)
	if !ok {
		return nil
	}
	exp, _ := v.(*AggregateExpansion)
	return exp
}
