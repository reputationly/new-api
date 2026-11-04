package middleware

import (
	"fmt"

	"github.com/gin-gonic/gin"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
)

// 体验区/直连的**声明式后置段**入口（metadata.post_stages）。
//
// 两段式流水线今天由浏览器编排：生成段跑完，前端再发一个 POST 提交超分段。于是
// 关掉页面/切走标签，第二段永远不会被提交 —— 用户付了 2K 拿到 768P，不报错。
//
// 这个中间件把"要不要有第二段、第二段长什么样"从**命令**变成**声明**：客户端在
// 生成段请求里附上 post_stages，服务端落进任务、由巡检推进（见
// service.TryAdvanceAggregatePipeline）。声明一旦落在任务上，关页面也能跑完。
//
// 三件事，顺序不能变：
//  1. **校验**（不合法就 400）。校验必须在提交阶段：推进是后台跑的，那时才发现
//     "模型不存在/分组调不了"，只能静默降级 —— 正是这条改动要消灭的那种失败。
//  2. **剥离**。引擎不认识这个键。漏摘不是报错，是上游静默忽略，以及将来某次
//     schema 收紧时炸。
//  3. **挂到 context**，供 controller 落进 PrivateData.Aggregate。
//
// 灰度：common.PlaygroundPipelineServerSide 关掉时整个中间件是 no-op —— 旧行为
// （浏览器自己提交第二段）逐位不变，出问题可以一条配置切回去。

// ContextKeyPostStages gin context 里放校验通过的后置段声明。
const ContextKeyPostStages = "post_stages"

// groupEnabledModels 该分组下可用的模型集合。抽成变量供测试注入 —— 这个中间件的
// 价值在于"拒得对不对"，而拒的判据不该被一个数据库连接挡住测试。
var groupEnabledModels = model.GetGroupEnabledModels

// TaskPostStages 解析并剥离 metadata.post_stages。
func TaskPostStages() gin.HandlerFunc {
	return func(c *gin.Context) {
		body, err := readJSONBodyMap(c)
		if err != nil {
			// 读不出请求体不是这里该拒的理由：图片/多模态可能有别的形态，
			// 交给后面的既有链路判定。没有 body 自然也没有 post_stages。
			c.Next()
			return
		}
		// metadata 有两种合法形态：对象，或**JSON 编码的字符串**（TaskSubmitReq 的
		// UnmarshalJSON 两种都收，聚合展开也用同一个 helper 读它）。只认 map 的话，
		// 字符串形态下 post_stages 既不会剥离、也不会被校验 —— 声明静默失效，
		// 而 `post_stages` 这个键还会原样发给引擎。
		meta, ok := expansionMetadata(body)
		if !ok {
			c.Next()
			return
		}
		raw, has := meta[common.PostStagesKey]
		if !has {
			c.Next()
			return
		}

		// **剥离与开关无关**：这个键绝不能发给引擎（引擎不认识它，漏摘是静默的）。
		// 开关只决定"服务端要不要接管"，不决定"要不要把脏数据递给上游"。
		delete(meta, common.PostStagesKey)
		if werr := setMetadataPreservingShape(body, meta); werr != nil {
			abortWithTaskError(c, "改写请求体失败: "+werr.Error())
			return
		}
		if werr := writeJSONBodyMap(c, body); werr != nil {
			abortWithTaskError(c, "改写请求体失败: "+werr.Error())
			return
		}

		if !common.PlaygroundPipelineServerSide {
			// 灰度关着：声明被忽略，第二段仍由浏览器自己提交（旧行为逐位不变）。
			common.SysLog("post_stages: 服务端流水线开关未开启，已忽略本次声明（后置段仍由客户端提交）")
			c.Next()
			return
		}

		stages, perr := common.ParsePostStages(raw)
		if perr != nil {
			abortWithTaskError(c, perr.Error())
			return
		}
		if len(stages) == 0 {
			c.Next()
			return
		}
		// 生成段必须是自建引擎（两段式要 NFS 产物作为第二段的输入）。前端有这道闸，
		// 服务端再守一次：直连调用方没有前端。
		if genModel, _ := body["model"].(string); !common.IsPipelineVideoModel(genModel) {
			abortWithTaskError(c, fmt.Sprintf(
				"post_stages 需要自建引擎的生成段（当前 %q 不支持），请改用自建（gpustackplus）模型", genModel))
			return
		}
		// 声明的模型必须对**当前用户分组**可用。前端过滤过不等于服务端安全：
		// 直连 /v1 也能发这个键，而不可用的模型在推进时只会静默失败。
		group := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
		allowed := map[string]bool{}
		for _, name := range groupEnabledModels(group) {
			allowed[name] = true
		}
		for _, st := range stages {
			if !allowed[st.Model] {
				abortWithTaskError(c, fmt.Sprintf(
					"post_stages 里的模型 %q 在分组 %q 下不可用", st.Model, group))
				return
			}
		}
		common.SetContextKey(c, ContextKeyPostStages, stages)
		c.Next()
	}
}

// abortWithTaskError 以任务接口的错误形态返回 400（与 relay 侧同一套形状）。
func abortWithTaskError(c *gin.Context, msg string) {
	c.AbortWithStatusJSON(400, gin.H{
		"error": gin.H{
			"message": msg,
			"type":    "invalid_request_error",
			"code":    "invalid_post_stages",
		},
	})
}

// readJSONBodyMap 读请求体为 map（保留数字原样，避免大整数被 float64 改写）。
//
// 刻意**不用** common.UnmarshalBodyReusable：它按 Content-Type 分派，非 json/form/
// multipart 时返回 nil 却什么都不填 —— 那会让"有没有 post_stages"这个判断落空。
// 这里只关心 JSON 体，读不出来就当作没有（不是这里该拒的理由）。
func readJSONBodyMap(c *gin.Context) (map[string]any, error) {
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return nil, err
	}
	raw, err := storage.Bytes()
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("empty body")
	}
	var body map[string]any
	if err := common.UnmarshalWithNumber(raw, &body); err != nil {
		return nil, err
	}
	return body, nil
}

// setMetadataPreservingShape 按**原来的形态**写回 metadata：原来是什么形态就还什么
// 形态。客户发字符串、我们回对象，上游可能按字符串解析（TaskSubmitReq 两种都收，
// 但引擎/门面不一定），这类"不改语义却换了编码"的差异最难查。
func setMetadataPreservingShape(body map[string]any, meta map[string]any) error {
	switch body["metadata"].(type) {
	case string:
		data, err := common.Marshal(meta)
		if err != nil {
			return err
		}
		body["metadata"] = string(data)
	default:
		body["metadata"] = meta
	}
	return nil
}

func writeJSONBodyMap(c *gin.Context, body map[string]any) error {
	data, err := common.Marshal(body)
	if err != nil {
		return err
	}
	return common.ReplaceRequestBody(c, data)
}

// PostStagesFromContext 取校验通过的后置段声明（没有则为 nil）。
func PostStagesFromContext(c *gin.Context) []common.PostStage {
	v, ok := c.Get(ContextKeyPostStages)
	if !ok {
		return nil
	}
	stages, _ := v.([]common.PostStage)
	return stages
}
