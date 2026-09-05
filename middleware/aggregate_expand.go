package middleware

import (
	"fmt"

	"github.com/gin-gonic/gin"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
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
}

// expandAggregateModel 若 modelName 是一个启用中的聚合模型,校验访问权限并返回展开后的
// 生成段模型名。返回 ("", nil) 表示不是聚合模型,调用方按原样继续。
//
// 权限判定只做「分组是否被允许」这一件事,其余(令牌白名单、可见性)都由既有链路负责:
// 展开发生在它们之后,而展开后的真实模型还会再过一遍渠道选择,该拒的自然会拒。
func expandAggregateModel(modelName, userGroup string) (string, error) {
	agg := common.GetAggregateModel(modelName)
	if agg == nil {
		return "", nil
	}
	if !groupAllowedForAggregate(agg, userGroup) {
		// 与可见性拦截同口径:对这个用户它就是不存在,不透露隐藏能力的存在。
		return "", fmt.Errorf("model not found")
	}
	if agg.Generate.Model == "" {
		return "", fmt.Errorf("聚合模型 %s 未配置生成段模型", modelName)
	}
	return agg.Generate.Model, nil
}

// groupAllowedForAggregate 判断某分组能否使用该聚合模型。
//
// 未配置 Groups = 不额外限制:此时约束完全来自展开后的生成段模型 —— 该分组下它没有渠道
// 的话,选渠道那一步自然会拒。配置了 Groups 才是显式白名单,用于把定向能力圈给指定集成方。
func groupAllowedForAggregate(agg *common.AggregateModel, userGroup string) bool {
	if agg == nil {
		return false
	}
	if len(agg.Groups) == 0 {
		return true
	}
	return common.StringsContains(agg.Groups, userGroup)
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
	var body map[string]any
	if err := common.Unmarshal(raw, &body); err != nil {
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
	data, err := common.Marshal(body)
	if err != nil {
		return fmt.Errorf("序列化请求体失败: %w", err)
	}
	if err := common.ReplaceRequestBody(c, data); err != nil {
		return fmt.Errorf("改写请求体失败: %w", err)
	}
	common.SetContextKey(c, constant.ContextKeyAggregateExpansion, &AggregateExpansion{
		PublicName: publicName,
		Config:     agg,
	})
	return nil
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
