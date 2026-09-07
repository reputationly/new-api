package middleware

import (
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
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
		// 最可能的成因是 multipart:/v1/images/edits 支持 multipart 提交
		// (relay/helper/valid_request.go 的 RelayModeImagesEdits 分支),而这里的
		// 展开只会读-改-写 JSON。报错要把这层说破,否则运营看到的是一句
		// "解析请求体失败",完全指不到"换 JSON 提交或别用聚合模型"这个动作上。
		if ct := c.Request.Header.Get("Content-Type"); strings.Contains(ct, "multipart/form-data") {
			return fmt.Errorf("聚合模型 %s 暂不支持 multipart 提交(Content-Type: %s),请改用 JSON 请求体,或直接调用生成段模型 %s", publicName, ct, realModel)
		}
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
			// 事实一律从**归一化后**的请求读,不从原始 map 读。
			//
			// TaskSubmitReq.UnmarshalJSON 已经把几种合法编码收敛成一种:metadata 可以是
			// JSON 编码的字符串、duration 可以是字符串整数。直接读 map 会在这些形态下
			// 一条事实都取不到 —— 生成段照常按 metadata 里的 task_type 和素材跑,
			// 增强段却什么都不知道,于是改写出一段与素材无关的提示词,且不报错。
			//
			// 从**改写后**的 body 走一遍序列化,而不是用入口的原始字节:Overrides 可能
			// 改掉 duration 一类字段,事实要描述真正发出去的那个请求。
			norm := normalizeTaskRequest(body)
			res := service.EnhancePrompt(c.Request.Context(), agg,
				c.Request.Header.Get("Authorization"),
				prompt, collectInputImages(body, norm), buildTaskContext(body, norm))
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
// 顶层的 image / images / input_reference 覆盖图生图与首尾帧;**参考生视频(r2va)的
// 参考图不在顶层**,它在 metadata.src_ref_images 里 —— 顶层的 image 是"首帧",与参考素材
// 是不同的键,调用方混用会让上游的输入形态判定失准,所以两边必须分开收集。
//
// 漏掉 metadata 那一份的后果不是"少看几张图":参考生视频的全部创作意图就在参考素材里,
// 增强模型只能从文字猜,会凭空臆造出与素材打架的描述(见 SendInputImages 的字段注释),
// 而生成模型是看得见素材的。这正是该开关默认为开要防的事故,只是换了个入参位置。
//
// **只收图片,不收 metadata.reference_videos / reference_audios**:增强请求把每一项都
// 编成 `image_url` part(见 buildEnhanceRequest),塞一段视频进去,轻则被模型忽略、重则
// 整个请求被拒;而调用方传的常常是 base64 data-uri,一段几十 MB 的视频还会把请求撑爆。
// 结果是增强直接降级——比看不见参考视频更糟。要让增强理解视频参考,得先有"抽帧"或
// "在文本里声明素材角色"的机制,那是另一件事,不该借这个口子偷偷做半套。
func collectInputImages(body map[string]any, norm *relaycommon.TaskSubmitReq) []string {
	// **按 task_type 只收生成段真正会用到的那一组**,顺序与 buildTaskContext 的
	// <Picture N> 标号一致。
	//
	// 混着收会同时坏两件事:标号错位(参考图被顶层图往后挤,<Picture 1> 指向了别的素材),
	// 以及把生成段根本不看的素材摆到增强模型面前 —— 它会照着一张不参与生成的图去写。
	// 两者都不报错,只是改写出的提示词描述的是另一个输入。
	if norm != nil {
		switch norm.TaskType() {
		case "r2va", "r2v", "rv2v":
			// 参考族只吃 metadata.src_ref_images;顶层图不在它的输入契约里。
			return norm.RefImages()
		case "i2v", "l2va", "flf2v", "s2v":
			// 帧族只吃顶层条件图,且 images / image / input_reference 是**回落关系**
			// 而不是并集 —— 取并集会让同一张图数两遍,<Picture N> 整体后移。
			return norm.FrameImages()
		}
	}
	// 说不出 task_type(图片聚合、或调用方没显式指定)时尽力收集:顶层在前、参考在后。
	// 这条路径不发标号,所以这里取并集是安全的 —— 宁可多给增强模型看一张,
	// 也别漏掉调用方用非常规别名传的那张。
	out := topLevelImages(body)
	if norm != nil {
		out = append(out, norm.RefImages()...)
	}
	return out
}

// topLevelImages 取顶层的条件图。三个键都收:公共校验把 image 归一进 images,
// 适配器在 images 为空时补 input_reference,这里覆盖调用方可能用的每一种写法。
func topLevelImages(body map[string]any) []string {
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

// normalizeTaskRequest 把改写后的 body 过一遍 TaskSubmitReq 的解码,拿到归一化的
// metadata / duration。解不出来(图片聚合的请求体就不是这个形状)返回 nil,
// 调用方按"说不出事实"处理 —— 中间件不因为解析失败挡住本能跑通的生成。
func normalizeTaskRequest(body map[string]any) *relaycommon.TaskSubmitReq {
	raw, err := common.Marshal(body)
	if err != nil {
		return nil
	}
	var req relaycommon.TaskSubmitReq
	if err := common.Unmarshal(raw, &req); err != nil {
		return nil
	}
	return &req
}

// buildTaskContext 把本次请求的**既成事实**编成一段给增强模型看的说明。
//
// 增强模型看不到调用方的面板:它只拿到一句"一只猫在窗台打盹",分不出这是首帧生视频
// 还是首尾帧,更不知道有几张参考素材、成片多长。猜错同样不报错,只是默默出差档 ——
// 体验区早就踩过,那边的对策是 buildH3OptimizeContext(`web/classic/src/constants/
// h3Prompt.constants.js`),这里是同一件事的后端版。
//
// **措辞与标号逐字抄自前端那份**(`<Picture 1>` / `<Video 1>` / 两位小数的时长,
// 以及每句里的 "Emit the <X> alignment instruction")。不是风格问题:H3 的 guide 就是
// 用这套标号和这几句指代素材与对齐动作的,自造一套等于让模型去对齐一个它没见过的
// 体系;尤其**丢掉肯定句而留下 T2VA 的否定句**最糟 —— 帧类任务从没被要求发出对齐指令,
// 纯文生却明确说别发。两处将来要一起改。
//
// **唯一的有意分歧是参考音频的条数**:前端那份的入参是布尔量(`hasRefAudio`),写死
// "1 reference audio";而平台后端 R2VA 收最多三段(adaptor.go 的 maxR2VARefAudios),
// 这里按实际条数输出。抄成 1 会让后两段在增强模型眼里不存在。
//
// 与模板的分工:模板(system_prompt)说"产出长什么样",这段说"这一次的输入是什么",
// 所以它**无条件拼上**,运营改写过模板也不例外 —— 事实不该被模板覆盖掉。
//
// 判据只用 metadata.task_type 与素材字段这些**平台统一任务契约**里的东西,不去猜模型;
// 说不出任何事实时返回空串,拼上去等于没拼。
func buildTaskContext(body map[string]any, norm *relaycommon.TaskSubmitReq) string {
	if norm == nil {
		return ""
	}
	taskType := norm.TaskType()

	var facts []string
	switch strings.TrimSpace(taskType) {
	case "r2va":
		var parts []string
		if n := len(norm.RefImages()); n > 0 {
			parts = append(parts, fmt.Sprintf("%d reference image(s), labelled %s", n, pictureLabels(n)))
		}
		if n := len(norm.RefVideos()); n > 0 {
			parts = append(parts, fmt.Sprintf("%d reference video(s), labelled %s", n, videoLabels(n)))
		}
		// R2VA 最多收三段参考音频(adaptor.go 的 maxR2VARefAudios),H3 的 guide 按
		// <Audio N> 逐条指代。写死成 1 会让增强模型以为只有一段,后两段在它眼里不存在。
		if n := len(norm.RefAudios()); n > 0 {
			parts = append(parts, fmt.Sprintf(
				"%d reference audio(s), labelled %s (voice-timbre reference)", n, audioLabels(n)))
		}
		assets := "none yet"
		if len(parts) > 0 {
			assets = strings.Join(parts, "; ")
		}
		facts = append(facts,
			fmt.Sprintf("Task: full-reference rewrite. Available reference assets: %s.", assets))
	case "flf2v":
		// 关键帧的两张图在顶层 images 里,顺序即首帧、尾帧。只给一张时**不猜**是首是尾
		// ——猜反了模型会朝着错误的方向收敛,而只陈述"有一张关键帧"至少不会误导。
		if countRefs(body["images"]) >= 2 {
			facts = append(facts,
				"Task type: FL2VA. <Picture 1> is the first frame and <Picture 2> is the last frame. Emit the FL2VA alignment instruction, and prefer a single shot.")
		} else {
			facts = append(facts, "Task type: keyframe-driven. <Picture 1> is a keyframe of the shot.")
		}
	case "i2v":
		facts = append(facts,
			"Task type: I2VA. <Picture 1> is the first frame. Emit the I2VA alignment instruction and develop forward from it.")
	case "l2va":
		// l2va 是"只给尾帧、反推开头"。它与 i2v 的输入形态完全一样(都是一张图),
		// 差别只在语义 —— 不说破,增强模型只会按最常见的 i2v 去写"从这张图往下发展",
		// 方向正好反了。这正是 flf2v 分支"只给一张时不猜是首是尾"要回避的歧义,
		// 而 l2va 这里是**知道**的,不该跟着不猜。
		facts = append(facts,
			"Task type: L2VA. <Picture 1> is the LAST frame, not the first. Emit the L2VA alignment instruction and converge onto it at the end.")
	case "t2v":
		facts = append(facts,
			"Task type: T2VA. There is no reference image. Do NOT emit any alignment instruction; begin directly with integrated_multimodal_description.")
	}

	// 时长只认显式的 duration(字符串整数由 TaskSubmitReq 归一化)。
	//
	// **不跟 seconds 回落**,尽管 gpustackplus 会跟:那条回落只对确实读 Seconds 的渠道
	// 成立(见 VideoSecondsFallback 的说明),而聚合展开跑在**选渠道之前**,这里根本
	// 不知道生成段会落到哪个渠道。对 kling/vidu/jimeng 这类忽略 Seconds 的渠道,
	// 跟了就是告诉增强模型"成片 10 秒",而上游按自己的默认出 5 秒 —— 分镜时间点
	// 全部落在片子之外。计费侧对同一件事是设了渠道闸的(videoBillingSeconds 先判
	// TaskPlatform),我们拿不到那个判据,就只能不说。
	//
	// 代价是 gpustackplus + 只给 seconds 的请求会少一条时长约束。少说一句事实,
	// 比说一句错的安全 —— 这一段的全部意义就是"陈述既成事实"。
	if seconds := float64(norm.Duration); seconds > 0 {
		facts = append(facts, fmt.Sprintf(
			"Effective video duration: %.2f seconds. Every cut timestamp must fall strictly inside it.", seconds))
	}

	if len(facts) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n---\n\nCurrent request:\n\n")
	for _, f := range facts {
		b.WriteString("- ")
		b.WriteString(f)
		b.WriteString("\n")
	}
	return b.String()
}

// countRefs 数一个素材字段里有几项。单个字符串算一项,数组按非空项数,其余算 0。
func countRefs(v any) int {
	switch t := v.(type) {
	case string:
		if strings.TrimSpace(t) != "" {
			return 1
		}
	case []any:
		n := 0
		for _, item := range t {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				n++
			}
		}
		return n
	}
	return 0
}

func pictureLabels(n int) string {
	if n > 1 {
		return fmt.Sprintf("<Picture 1>..<Picture %d>", n)
	}
	return "<Picture 1>"
}

func audioLabels(n int) string {
	if n > 1 {
		return fmt.Sprintf("<Audio 1>..<Audio %d>", n)
	}
	return "<Audio 1>"
}

func videoLabels(n int) string {
	if n > 1 {
		return fmt.Sprintf("<Video 1>..<Video %d>", n)
	}
	return "<Video 1>"
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
