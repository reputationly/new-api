package service

import (
	"bytes"
	"context"
	"fmt"
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
func EnhancePrompt(ctx context.Context, agg *common.AggregateModel, authHeader, prompt string, imageURLs []string, taskContext string) *EnhanceResult {
	started := time.Now()
	res := &EnhanceResult{OriginalPrompt: prompt, EnhancedPrompt: prompt}
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
	if strings.TrimSpace(cfg.SystemPrompt) == "" {
		return degrade("未配置增强模板(system_prompt)")
	}
	res.Model = strings.TrimSpace(cfg.Model)
	if res.Model == "" {
		return degrade("未配置增强模型")
	}
	if strings.TrimSpace(authHeader) == "" {
		// 拿不到客户身份就无法以他的名义调用,也就无从计费 —— 宁可不增强。
		return degrade("缺少调用者身份,无法发起增强调用")
	}

	// 事实无条件拼在模板之后:运营改写过模板也不例外 —— 模板可以换,
	// "这一次传了几张图、多少秒"不能被换掉。
	body := buildEnhanceRequest(res.Model, appendTaskContext(cfg.SystemPrompt, taskContext), prompt, imageURLs, cfg.IsSendInputImages())
	payload, err := common.Marshal(body)
	if err != nil {
		return degrade("构造增强请求失败: %v", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, enhanceTimeout)
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
func buildEnhanceRequest(modelName, systemPrompt, prompt string, imageURLs []string, sendImages bool) map[string]any {
	var userContent any = prompt
	if sendImages && len(imageURLs) > 0 {
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
		userContent = parts
	}
	return map[string]any{
		"model": modelName,
		"messages": []map[string]any{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userContent},
		},
		"stream": false,
	}
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
