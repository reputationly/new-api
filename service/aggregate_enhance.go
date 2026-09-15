package service

import (
	"bytes"
	"context"
	"fmt"
	"github.com/QuantumNous/new-api/relay/hilo"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

// 聚合模型的提示词增强段:在生成之前,先让一个 LLM 把客户的 prompt 改写得更适合目标模型。
//
// 这条能力体验区早就有(用户点「AI 优化提示词」按钮),但那是**前端**发起的 —— 直连 API
// 的集成方享受不到。聚合模型把它搬到后端,对客户完全透明。
// 与体验区共享同一套配置心智:用哪个 LLM 是运营的事,客户看不到也配不了。
//
// ── 为什么是 self-call(而不是选渠道直连上游)────────────────────────────
//
// 体验区的分段计费之所以是"白来的",是因为它每一段都是一次**独立的 HTTP 请求**,
// 完整走 Distribute → relay,计费/限流/日志全在链路里。这里照做:服务端带着客户的
// Authorization 向本站 /v1/chat/completions 发一次请求。收益不止计费:
//
//   - **计费零重复逻辑**。手写一份算价(倍率 × token × 分组倍率)看着简单,但主链路的
//     PostTextConsumeQuota 还有分层计费、web search、缓存 token、Claude 语义等分支;
//     抄一份出来,将来主逻辑加了新维度这里不会跟上,而且不报错 —— 只是账悄悄算少了。
//   - **不再受限于 OpenAI 兼容渠道**。各家协议的转换是 relay adaptor 的职责,走一遍
//     relay 就自动拿到,Claude / Gemini 渠道上的模型也能用作增强模型。
//
// 代价只有一条:客户令牌若开了模型白名单,增强模型必须一并加进去,否则这一步吃 403
// 并降级(干跑校验会提示)。相比"多一份会漂移的算价逻辑",这个代价划算。
//
// ── 失败一律降级 ────────────────────────────────────────────────────
//
// 增强是锦上添花:为它牺牲一次本来能成功的生成,任何场景下都不划算。
// 所以本文件所有错误路径的最终归宿都是"调用方拿原 prompt 继续"。

// enhanceTimeout 增强调用的超时。这段跑在**扣费与生成之前**,客户在同步等待,
// 不能让一个抽风的 LLM 把整个请求拖死 —— 超时即降级,用原 prompt 照常生成。
const enhanceTimeout = 20 * time.Second

// textBudget text 改写的时间预算:配置优先,没配用内置默认。
//
// **IR 那边的配置值不适用于这里。** 运营为 IR 配的是几分钟级的数字
// (一整份结构化 IR 要跑几十秒),而 text 改写只吐一段提示词,拿几分钟的
// 预算兜一次本该几秒完成的调用,只会让失败的那次把客户吊更久。
//
// 所以配了 timeout_seconds 时这里取**两者的较小值**:既尊重运营显式调小
// 的意图,又不让为 IR 放宽的预算顺带把 text 也放宽。
func textBudget(cfg *common.AggregatePromptEnhance) time.Duration {
	if cfg == nil || cfg.TimeoutSeconds <= 0 {
		return enhanceTimeout
	}
	if d := time.Duration(cfg.TimeoutSeconds) * time.Second; d < enhanceTimeout {
		return d
	}
	return enhanceTimeout
}

// EnhanceInput 一次增强要用到的全部输入。
//
// 做成结构体而不是继续加参数:这里已经有「提示词、图、视频、请求事实、
// IR 编译事实」五类输入,位置参数排到第七个时,调用点看不出哪个是哪个,
// 传串了也照样编译过。
type EnhanceInput struct {
	// Prompt 客户的原始提示词,逐字。
	Prompt string
	// ImageURLs / VideoURLs 本次要给模型看的素材。
	ImageURLs []string
	VideoURLs []string
	// SendMedia 是否真的把素材发出去(对应 send_input_images 开关)。
	SendMedia bool
	// Thinking 让模型开启"思考"。默认关 —— 思考型模型会重新推导任务、
	// 绕开 schema(见 common.AggregatePromptEnhance.Thinking)。
	Thinking bool
	// TaskContext 请求事实的文本描述,拼进 text 模式的系统提示词。
	TaskContext string
	// Compiler IR 模式所需的结构化请求事实。nil = 编译不了 IR,
	// 配了 mode=ir 也只能走 text。
	Compiler *hilo.CompilerInput
}

// EnhanceResult 一次增强的结果与过程记录。
//
// 记录是必须的:增强后的 prompt **不回传给客户**(避免暴露内部编排),那么客户报
// "生成的东西跟我写的不一样"时,服务端这份记录就是唯一能解释清楚的证据。
type EnhanceResult struct {
	OriginalPrompt string
	EnhancedPrompt string
	Model          string
	ElapsedMs      int64
	// Degraded 为 true 表示增强没成功、用的是原始提示词。DegradeReason 记下原因。
	Degraded      bool
	DegradeReason string
	Usage         *dto.Usage

	// Mode 实际走的模式("text" / "ir" / "singlecall")。配的是后两者
	// 而这里是 text,说明那条路失败了、回落了 —— 见 IRFallbackReason。
	Mode string
	// ContentPlan singlecall 产出的生产记录(content_plan)原文。
	// **不回传客户**,只用于排障对照:客户说"生成的跟我写的不一样"时,
	// 这份记录能指出模型当时把哪些内容当成了 must_keep。
	ContentPlan string
	// Uncertainties 模型自己标出的未决点。**不是错误** —— 上游刻意让它
	// 把说不准的地方记下来,而不是编一个确定答案糊过去。
	Uncertainties []string
	// CompilerRevision 用的哪一版编译器提示词(singlecall 才有)。
	CompilerRevision string
	// IR 编译成功时的那份 IR。留着供日志与排查:提示词不对时,能指出
	// 是模型的创作判断有问题,还是渲染这一步错了。
	IR *hilo.ContextIR
	// IRRepaired 第一轮校验没过、靠重修才成功。
	IRRepaired bool
	// IRFallbackReason ir / singlecall 失败并回落到 text 的原因。
	// 空 = 没回落过。
	//
	// **这是整件事里唯一的反馈来源。** IR 层此前零调用方,没有任何真实
	// 失败样本,所以校验规则和编译器模板都只能靠想 —— 也就一直不收敛。
	IRFallbackReason string
}

// u15EditClosingMarker 官方 U1.5 编辑模板的收尾句,与前端
// `web/classic/src/constants/promptOptimize.constants.js` 的 U15_EDIT_CLOSING_MARKER
// **必须逐字一致** —— 判据是字面匹配,两处漂移了这里就静默失效。
//
// 跨语言没法共享一个常量,所以改动时两处一起改;前端那份有更详细的来由说明。
const u15EditClosingMarker = "\nBelow is the Prompt to be rewritten."

// appendTaskContext 把请求事实拼进系统提示词。
//
// **不是简单地接在末尾**:官方 U1.5 编辑模板以「下面紧接着就是要改写的原文」收尾,
// 事实若拼在那句之后,模型很容易把这段英文的 Task type / Effective video duration
// 当成"原文",于是模板里「用原文语言改写」这条规则被读成「用英文改写」;
// 同时原文与说明之间隔了一段无关内容,模板刻意安排的「说明→原文」贴合也被拆开。
//
// 命中这句就插在它**之前**,没有这句的模板(通用版 / H3 / LTX / Music3)照旧追加末尾。
// 与前端 appendOptimizeContext 同一套判据。
func appendTaskContext(systemPrompt, taskContext string) string {
	if taskContext == "" {
		return systemPrompt
	}
	at := strings.LastIndex(systemPrompt, u15EditClosingMarker)
	if at == -1 {
		return systemPrompt + taskContext
	}
	return systemPrompt[:at] + taskContext + "\n" + systemPrompt[at:]
}

// EnhancePrompt 按聚合模型的增强段配置改写 prompt。authHeader 为客户本次请求的
// Authorization,增强调用以同一身份发起,因而计费落在客户账上(分段计费)。
//
// taskContext 是本次请求的既成事实(输入形态、素材标号、时长),由调用方从请求体里编出来。
// 它**拼在系统提示词末尾**,与体验区同一位置(`usePromptOptimize` 的 appendOptimizeContext)
// —— 位置不是随意的:模板讲"产出长什么样",事实讲"这一次的输入是什么",放进 user 消息
// 会让模型把它当成待改写的内容的一部分。空串表示没有可说的事实,不拼。
//
// **永远不返回错误** —— 任何失败都体现为 Degraded=true + 原样返回的 prompt。
// 这是刻意的签名设计:让调用方无法"忘记处理增强失败",因为根本没有失败分支可漏。
func EnhancePrompt(ctx context.Context, agg *common.AggregateModel, authHeader string, in EnhanceInput) *EnhanceResult {
	started := time.Now()
	prompt := in.Prompt
	res := &EnhanceResult{OriginalPrompt: prompt, EnhancedPrompt: prompt, Mode: common.EnhanceModeText}
	degrade := func(format string, args ...any) *EnhanceResult {
		res.Degraded = true
		res.DegradeReason = fmt.Sprintf(format, args...)
		res.ElapsedMs = time.Since(started).Milliseconds()
		common.SysLog(fmt.Sprintf("aggregate enhance degraded (model=%s): %s", res.Model, res.DegradeReason))
		return res
	}

	cfg := agg.PromptEnhance
	if !cfg.IsEnabled() {
		res.ElapsedMs = time.Since(started).Milliseconds()
		return res
	}
	if strings.TrimSpace(prompt) == "" {
		return degrade("原始提示词为空,无可增强")
	}
	res.Model = strings.TrimSpace(cfg.Model)
	if res.Model == "" {
		return degrade("未配置增强模型")
	}
	if strings.TrimSpace(authHeader) == "" {
		// 拿不到客户身份就无法以他的名义调用,也就无从计费 —— 宁可不增强。
		return degrade("缺少调用者身份,无法发起增强调用")
	}
	// 素材发不发由配置定,调用点不必知道 —— 这个开关的后果太重
	// (见 SendInputImages 字段注释),不该散在每个调用点各判一次。
	in.SendMedia = cfg.IsSendInputImages()
	in.Thinking = cfg.IsThinking()

	// ── singlecall 模式 ──────────────────────────────────────
	//
	// 一次调用同时产出 content_plan 与 h3_prompt,程序只做轻量传输检查
	// (见 aggregate_enhance_singlecall.go)。失败**回落 text 改写**,
	// 与 IR 那条同一个理由 —— 直接掉到原始提示词会让开着比不开还差。
	if cfg.EnhanceMode() == common.EnhanceModeSingleCall {
		if in.Compiler == nil {
			res.IRFallbackReason = "缺少请求事实(CompilerInput),无法编译"
		} else if out, err := compileSingleCallWithTimeout(
			ctx, authHeader, res.Model, in, singleCallBudget(cfg)); err != nil {
			res.IRFallbackReason = err.Error()
		} else {
			res.EnhancedPrompt = out.Prompt
			res.ContentPlan = out.Plan
			res.Uncertainties = out.Uncertainties
			res.CompilerRevision = out.Revision
			res.IRRepaired = out.Repaired
			res.Usage = out.Usage
			res.Mode = common.EnhanceModeSingleCall
			res.ElapsedMs = time.Since(started).Milliseconds()
			for _, w := range out.Warnings {
				common.SysLog(fmt.Sprintf("aggregate enhance: singlecall 传输警告 (model=%s): %s", res.Model, w))
			}
			return res
		}
		common.SysLog(fmt.Sprintf(
			"aggregate enhance: singlecall 编译失败,回落 text 改写 (model=%s): %s",
			res.Model, res.IRFallbackReason))
	}

	// ── IR 模式 ──────────────────────────────────────────────
	//
	// 成功就直接返回;失败**不降级,而是回落到 text 改写**(见
	// aggregate_enhance_ir.go 顶部的三级降级说明)。直接掉到原始提示词
	// 会让开 IR 比不开还差,于是没人敢开,于是永远收不到真实失败样本。
	if cfg.EnhanceMode() == common.EnhanceModeIR {
		if in.Compiler == nil {
			res.IRFallbackReason = "缺少请求事实(CompilerInput),无法编译 IR"
		} else if out, err := compileIRWithTimeout(ctx, authHeader, res.Model, in, irBudget(cfg)); err != nil {
			res.IRFallbackReason = err.Error()
		} else {
			res.EnhancedPrompt = out.Prompt
			res.IR = out.IR
			res.IRRepaired = out.Repaired
			res.Usage = out.Usage
			res.Mode = common.EnhanceModeIR
			res.ElapsedMs = time.Since(started).Milliseconds()
			return res
		}
		common.SysLog(fmt.Sprintf(
			"aggregate enhance: IR 编译失败,回落 text 改写 (model=%s): %s",
			res.Model, res.IRFallbackReason))
	}

	// 模板的继承链：配置里写了就用配置的，没写就回落到**按生成段模型挑的
	// 内置默认**。这一级以前不存在 —— 于是「SystemPrompt 空 = 继承」这句
	// 注释描述的是一条断掉的链，出厂配置也就不敢带增强段。
	//
	// 仍然拿不到模板时才降级：那说明这个模型我们没有对应的改写知识，
	// 硬套别的模型的模板会让改写结果带着一堆它不认的字段名。
	systemPrompt := strings.TrimSpace(cfg.SystemPrompt)
	if systemPrompt == "" {
		systemPrompt = DefaultEnhanceTemplate(agg.Generate.Model)
	}
	if systemPrompt == "" {
		return degrade("没有可用的增强模板(system_prompt 为空，且 %s 没有内置默认)",
			agg.Generate.Model)
	}
	// 事实无条件拼在模板之后:运营改写过模板也不例外 —— 模板可以换,
	// "这一次传了几张图、多少秒"不能被换掉。
	body := buildEnhanceRequest(res.Model, appendTaskContext(systemPrompt, in.TaskContext), prompt, in.ImageURLs, in.VideoURLs, in.SendMedia, cfg.IsThinking())
	payload, err := common.Marshal(body)
	if err != nil {
		return degrade("构造增强请求失败: %v", err)
	}

	// text 改写用自己的预算。**不能和 IR 共用一个 ctx** —— 共用的话
	// IR 那 150 秒一旦用满,回落进来的 text 改写会立刻拿着一个已经取消的
	// ctx 失败,三级降级里的中间那级就永远走不到,等于白设计。
	reqCtx, cancel := context.WithTimeout(ctx, textBudget(cfg))
	defer cancel()
	enhanced, usage, err := callEnhance(reqCtx, authHeader, payload)
	if err != nil {
		return degrade("%v", err)
	}
	if strings.TrimSpace(enhanced) == "" {
		return degrade("增强模型返回空内容")
	}

	res.EnhancedPrompt = enhanced
	res.Usage = usage
	res.ElapsedMs = time.Since(started).Milliseconds()
	return res
}

// buildEnhanceRequest 构造一次 OpenAI chat 请求。
//
// 带图时用多模态 content 数组。**图片必须真的发过去**,只在文字里说"用户传了一张图"是
// 不够的:图生图场景下增强模型看不到底图就会凭空臆造(用户传彩色油画、它写出"黑白纪实"),
// 而生成模型是看得见底图的,两边直接打架。这一条是体验区踩出来的经验。
func buildEnhanceRequest(modelName, systemPrompt, prompt string, imageURLs, videoURLs []string, sendImages, thinking bool) map[string]any {
	body := map[string]any{
		"model": modelName,
		"messages": []map[string]any{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": buildUserContent(prompt, imageURLs, videoURLs, sendImages)},
		},
		"stream": false,
	}
	applyThinking(body, thinking)
	return body
}

// maxEnhanceVideoDataURI 内联视频(data URI)的大小上限。
//
// 参考视频常常是 base64 data URI,而**这条路在客户的关键路径上**:增强跑在
// 生成请求发出之前,失败了还要再跑一轮重修 —— 同一段字节要上传两次。
// 一段几十 MB 的视频会把请求撑爆,而表现只是"增强降级 + 白等很久"。
//
// 8 MB 是按"能让模型看清、又不至于拖垮一次同步调用"取的:实测探针用的
// 2 秒 320x240 片段是 4 KB 级,真实的几秒参考片在这个量级之内。
//
// 超限就**跳过这一段视频**,不是整条降级 —— 少看一段素材比让整次增强失败好,
// 而且图片那一半照常送到。
const maxEnhanceVideoDataURI = 8 << 20

// enhanceVideoAllowed 这段视频该不该发给增强模型。
//
// 远程 URL 一律放行(体积由上游自己取,不占我们的请求体);只有内联的
// data URI 才卡大小 —— 那是唯一会把请求体撑大的形态。
func enhanceVideoAllowed(u string) bool {
	u = strings.TrimSpace(u)
	if u == "" {
		return false
	}
	if !strings.HasPrefix(u, "data:") {
		return true
	}
	if len(u) > maxEnhanceVideoDataURI {
		common.SysLog(fmt.Sprintf(
			"aggregate enhance: 跳过一段内联参考视频(%d 字节,超过 %d 上限),"+
				"增强模型看不到它;改用可访问的 URL 传参考视频可避免",
			len(u), maxEnhanceVideoDataURI))
		return false
	}
	return true
}

// applyThinking 显式声明要不要思考。
//
// **默认关**(见 AggregatePromptEnhance.Thinking 的字段注释):思考型模型会
// 从用户消息重新推导任务、绕开系统提示词里的 schema,实测五次里跑偏两次;
// 关掉之后又快又稳,视觉理解不受影响。
//
// 走 chat_template_kwargs 而不是顶层 enable_thinking:实测只有前者真的把
// reasoning 关到 0,后者在这个平台上仍然会思考(reasoning=585)。
//
// 不思考时**只发这一个参数**,不额外声明其它:对非思考模型它是安全的空操作
// (实测 qwen3.8-27b 照常返回、不报错),而参数发得越多,遇上不认识它们的
// 上游就越容易整条请求被拒 —— 那会让增强直接降级,比多思考几秒糟得多。
func applyThinking(body map[string]any, thinking bool) {
	if thinking {
		return // 开启就用上游自己的默认,不额外声明
	}
	body["chat_template_kwargs"] = map[string]any{"enable_thinking": false}
}

// buildUserContent 用户这一轮的 content:纯文本,或带素材的多模态数组。
//
// 单独抽出来是因为 IR 模式要自己拼多轮消息(见 aggregate_enhance_ir.go),
// 素材该怎么编进 content 这件事只能有一份 —— 两处各写一遍,漂移的症状是
// 「某条路径上视频又没发出去」,而那恰恰是不报错的那类错。
func buildUserContent(prompt string, imageURLs, videoURLs []string, sendImages bool) any {
	if !sendImages || (len(imageURLs) == 0 && len(videoURLs) == 0) {
		return prompt
	}
	parts := []map[string]any{{"type": "text", "text": prompt}}
	for _, u := range imageURLs {
		if strings.TrimSpace(u) == "" {
			continue
		}
		parts = append(parts, map[string]any{
			"type":      "image_url",
			"image_url": map[string]any{"url": u},
		})
	}
	// **参考视频也要给模型看。**
	//
	// 以前这里只有图片,于是用户传一段参考视频,增强模型压根不知道它存在,
	// 却被模板要求"看着素材写" —— 它只能编,而且编得通顺、不报错。
	//
	// 视频放在图片之后:<Picture N> 的标号按图片顺序发,视频插在中间会让
	// 标号和 buildTaskContext 对不上。
	for _, u := range videoURLs {
		if !enhanceVideoAllowed(u) {
			continue
		}
		parts = append(parts, map[string]any{
			"type":      "video_url",
			"video_url": map[string]any{"url": u},
		})
	}
	// **过滤之后什么素材都没剩时,退回纯文本。**
	//
	// 空串和超限的内联视频会被上面两个循环跳掉,于是可能只剩下那条 text。
	// 单元素 content 数组和纯字符串在部分上游的处理不一致(见
	// TestBuildEnhanceRequestPlainWhenNoImages),不能因为调用方**传了**
	// 素材就发一个只剩文字的数组 —— 判据要看真正编进去了几项。
	if len(parts) <= 1 {
		return prompt
	}
	return parts
}

// enhanceEndpoint 增强调用的目标地址。做成变量供测试指向本地假服务端。
var enhanceEndpoint = func() string {
	base := strings.TrimRight(strings.TrimSpace(system_setting.ServerAddress), "/")
	if base == "" {
		return ""
	}
	return base + "/v1/chat/completions"
}

// callEnhance 以客户身份向本站发一次 chat 请求,取回首个 choice 的文本与 usage。
// 走完整 relay 链路,所以这次调用会像客户自己调一样被计费、限流、记日志。
func callEnhance(ctx context.Context, authHeader string, payload []byte) (string, *dto.Usage, error) {
	endpoint := enhanceEndpoint()
	if endpoint == "" {
		// ServerAddress 没配就无法自调用。这是站点配置缺失,不是客户的问题,故降级而非报错。
		return "", nil, fmt.Errorf("站点未配置服务器地址(ServerAddress),无法发起增强调用")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", nil, fmt.Errorf("构造增强请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader)

	// GetHttpClient 返回的是由 InitHttpClient 在启动时赋值的包级变量,拿到 nil 会直接
	// panic。生产虽然一定初始化过,但这里的失败语义是"降级",不该有任何路径把它变成
	// 一个 500 —— 增强出问题只该让客户少一次改写,不该让他的生成请求崩掉。
	// 超时由上面的 ctx 兜着,兜底 client 不设 Timeout 也不会挂住。
	client := GetHttpClient()
	if client == nil {
		client = &http.Client{}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("增强调用失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		// 最可能的原因:客户令牌开了模型白名单但没放行增强模型。单独指认它,
		// 否则运营会对着一条笼统的"调用失败"去查渠道,查不出任何问题。
		return "", nil, fmt.Errorf("增强调用被拒(403):请确认客户令牌的模型白名单已包含增强模型")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil, fmt.Errorf("增强调用返回 %d", resp.StatusCode)
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage *dto.Usage `json:"usage"`
	}
	if err := common.DecodeJson(resp.Body, &parsed); err != nil {
		return "", nil, fmt.Errorf("解析增强响应失败: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return "", nil, fmt.Errorf("增强响应没有 choices")
	}
	return strings.TrimSpace(parsed.Choices[0].Message.Content), parsed.Usage, nil
}
