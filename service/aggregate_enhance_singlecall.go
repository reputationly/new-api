// singlecall 编译：一次调用同时产出生产记录与最终 H3 提示词。
//
// 移植自 XINGSHEN2/minimax-H3-context-IR 的 `singlecall.v20.reference_inheritance`
// （`backend/single_call_compiler.py` + `backend/compact_writer.py`）。
// 两段提示词原文在 service/h3v20/，用 go:embed 引入，**不在 Go 里改措辞** ——
// 改了就没法和上游 diff。
//
// # 和 text 那条路的关系
//
//	singlecall  本文件，**出厂默认**。一次调用直接拿到 h3_prompt，
//	            程序只做轻量传输检查。
//	text        官方客户端 vendor 卡那套：中文「全局基准 → 【镜头N】」五字段。
//	            这是 MiniMax Design 应用自己发给 H3 的格式。编译失败时回落到它。
//
// （曾经还有个 ir：模型吐 canonical IR → 确定性校验 → 确定性渲染 → 独立审计。
// 上游废弃了那套架构，我们也从未在生产启用过，已整体删除 ——
// 见 service/h3v20/README.md。）
//
// 两者的输出格式不同，而且两份官方材料本身就不一致：H3 模型仓的
// `h3-prompt-writing` skill 要英文三节（`integrated_multimodal_description` /
// `overall_soundscape` / `non_diegetic_music`），官方客户端的 vendor 卡要中文
// 五字段。singlecall 走前者。
//
// # 为什么只做轻量检查
//
// v20 的编译器提示词里就写死了这一条：
//
//	This single call produces both; do not introduce an audit stage.
//
// 上游是从"模型出 IR → 程序渲染 → 独立审计"那套退回来的：审计阶段拦得住
// 结构错误，拦不住语义错误，却让每次编译多花一到两轮往返。所以这里的
// transportIssues 只核对**运得出去**（有提示词、素材编号真实存在、镜头时间
// 连续且等于请求时长），不核对写得好不好 —— 对齐上游 `transport_audit` 里
// 那句 `semantic_quality_verified: False`。
//
// # 参考视频与参考音频不发给模型（方案 C）
//
// 增强阶段只发图片。视频与音频只以 <Video N> / <Audio N> 标签进证据，并带
// observable:false 显式声明「你看不见」。理由是实测出来的三条：引擎自己拉远程
// 视频会偶发挂死（重试链 30s→120s→480s，拖出 465 秒 504）；内联成 data URI 能
// 绕过但每次多 12288 个视频 token；带上视频之后开思考就没正文（74–118 秒、空或
// 截断，max_tokens 提到 16384 仍截断）。视频照常随**生成**请求发给 H3，所以
// 标签仍按完整素材清单编号。等引擎侧修好再评估「发给模型看」（优先抽关键帧）。
//
// # 降级
//
// 失败**不掉回原始提示词，而是回落 text 改写**。理由与已删除的 ir 那条
// 完全相同：直接掉到原始提示词会让开着比不开还差，
// 于是没人敢开，于是永远收不到真实失败样本。
package service

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/hilo"
)

//go:embed h3v20/rules.txt
var singleCallRules string

//go:embed h3v20/writing.txt
var singleCallWriting string

// singleCallAppendix 我们自己的合规清单,**唯一一处非逐字移植**。
//
// 上游 v20 的提示词是逐字移植的(见本文件顶部),这一份不是 —— 它是拿 8 条用例
// 与官方 Context-IR 逐条对照之后补的差距清单,由实验阶段用 run_s2.py 的第三个
// 参数注入验证过(实验脚本与规则原文在 scripts/h3vid-pe/):
//
//	首帧/参考图清点      i2v 1689 → 2270 字符(官方 3779),元素级信息补齐
//	non_diegetic_music   从"N/A 3/7"变成逐条谱曲
//	保留标记语义         狗穿平铺夹克从 fully_preserved 改成 attribute_transfer,
//	                     与官方判法一致
//
// **位置固定在 rules.txt 之后、writing.txt 之前**:writing.txt 末尾就是
// "Evidence follows:",证据必须紧贴它;而清单要读起来像"对上面规则的复核",
// 放在规则之后最自然。实验里也是这个位置。
//
// 它和 rules.txt 的关系是"覆盖而非替换":rules.txt 讲怎么写计划,这份只写
// 实测会写错的九条。上游改 v20 时**不需要**同步这份。
//
//go:embed h3v20/appendix.txt
var singleCallAppendix string

// 官方 skill 与两份协议规范。
//
// **必须一起发给模型。** writing.txt 第 2 行写着 "Follow the official H3
// writing guide and shot-planning skill" —— 但普通 chat 端点没有文件系统
// 工具,模型执行不了"去读那份指南"。上游的 direct 运行时正是为此把这几份
// 文件拼进系统提示词,它的注释说得很直白:
//
//	Direct Chat has no filesystem tool. The skill index alone
//	cannot execute its instruction to read the protocol guides.
//
// 漏掉它们的后果是**静默降质**:模型只能凭自己记得的那点 H3 知识写,
// 三节名称、[Shot N] 记法、<d>[Language] 台词标签全都无从谈起 ——
// 而产出看起来仍然像模像样。
//
// **AGENTS.md 刻意不带。** 上游会带,但那份是它的仓库指令,里面写着
// "Produce only Context-IR JSON in the final response" —— 对 singlecall
// 是错的(它要的是 content_plan + h3_prompt),带上去等于给模型两条互相
// 矛盾的输出契约。其中唯一属于协议的那句(改写正文用英文、台词保留原语言)
// 在 prompt-writing 的 SKILL.md 的 Output Rules 里已经有了。
var (
	//go:embed h3v20/skills/prompt-writing.SKILL.md
	skillPromptWriting string
	//go:embed h3v20/skills/shot-planning.SKILL.md
	skillShotPlanning string
	//go:embed h3v20/skills/base-en.txt
	guideBaseEN string
	//go:embed h3v20/skills/ref-en.txt
	guideRefEN string
)

// buildSingleCallSystem 拼系统提示词：官方规范 → v20 规则 → 写作说明 → 证据。
//
// # ref-en.txt 只在参考族发
//
// 与上游有一处**有意的出入**：它无论什么玩法都把两份规范都发出去。
// ref-en.txt 有 23 KB，只讲全参考（r2va）那六节；帧族用不上它，而这段
// 提示词是**每个请求**都要发一遍的。base-en.txt 则一律要发 —— 上游注释
// 点明了理由：参考族也要用基础规范里的台词与运镜规则。
func buildSingleCallSystem(taskType, evidenceJSON string) string {
	parts := []string{skillPromptWriting, guideBaseEN}
	if strings.Contains(strings.ToLower(taskType), "r2v") {
		parts = append(parts, guideRefEN)
	}
	parts = append(parts, skillShotPlanning, singleCallRules, singleCallAppendix, singleCallWriting+evidenceJSON)
	return strings.Join(parts, "\n\n")
}

// singleCallRevision 跟着上游的 COMPILER_REVISION 走，出现在排障记录里。
//
// 上游还有个 v27（「连续动作与镜头衔接」），但只发布了成片、没开源代码，
// 仓库里的 COMPILER_REVISION 仍是 v20。所以 v20 是**能拿到的最新实现**，
// 不是最新版本 —— 下次同步先确认这一点。
const singleCallRevision = "singlecall.v20.reference_inheritance"

// singleCallMaxRepairRounds 传输错误的重修轮数。
//
// 与上游一致：一轮。上游 compile_once 里也只重试一次，理由相同 ——
// 这一段卡在客户的生成请求前面，一轮修不好的多半是这次输入本身让模型为难。
const singleCallMaxRepairRounds = 1

// singleCallTimeout 编译的总预算（两轮共用）的内置默认值。
//
// 比 IR 那条（240 秒）短得多：singlecall 只让模型吐一份紧凑的 content_plan
// 加一段提示词，不用产出整棵 canonical IR。配置里写了 timeout_seconds
// 就以配置为准。
//
// 做成 var 是为了让测试能把它压到毫秒级去验超时回落那条路。
//
// **这段预算是同步跑在客户提交请求里的**,所以它就是客户的最长等待:出厂默认配的是
// 180 秒,最坏路径是「增强用满 180 秒 → 回落 text 再用 20 秒」= 200 秒才轮到生成。
// 上线前要确认前面的负载均衡与客户端超时扛得住这个数。
var singleCallTimeout = 120 * time.Second

// singleCallMaxOutputTokens 输出 token 上限的内置默认。
//
// **这个上限不能省。** 一次完整产物是 content_plan(几千 token)+ 整段
// h3_prompt,实测 8192 够用;而不发这个字段时上游默认上限会把正文截断在
// 半句台词上 —— 返回的 JSON 仍是闭合可解析的,只是 h3_prompt 为空。
// 那种失败**看起来不像截断**:传输检查报的是"h3_prompt must be nonempty",
// 重修一轮还是同一份被截断的输出,最后静默回落 text。
//
// **它是"思考 + 正文"的总量。** 关思考时 8192 全是正文,富余;开档位之后正文
// 只剩 8192 减去思考那一截(实测 low 档 thinking 4000+),图生视频这种长清点
// 最可能撞上。要更保险就配 max_output_tokens=16384。
const singleCallMaxOutputTokens = 8192

// singleCallMaxTokens 本次请求的输出上限:配置优先,没配用内置默认。
func singleCallMaxTokens(configured int) int {
	if configured > 0 {
		return configured
	}
	return singleCallMaxOutputTokens
}

// singleCallMinTimeoutSeconds 干跑校验的下限:低于它就该提醒。
//
// 取内置默认的一半:singlecall 只吐一份紧凑记录加一段提示词,比 IR 那条快
// 得多,但系统提示词里带着官方规范(约 50 KB),首轮往返不会很短。留一半是
// 为了"配得比默认小很多"时才出声,而不是运营稍微调一下就报警。
const singleCallMinTimeoutSeconds = 60

// singleCallResult 一次编译的产物。
type singleCallResult struct {
	// Prompt 最终 H3 提示词，直接发给生成模型。
	Prompt string
	// Plan 生产记录（content_plan），只用于排障对照，不回传客户。
	Plan string
	// Uncertainties 模型自己标出的未决点。**不是错误** —— 上游刻意让它
	// 把说不准的地方记下来而不是编一个确定答案。
	Uncertainties []string
	// Warnings 传输检查的软问题。不拦，但要能看见。
	Warnings []string
	Repaired bool
	Usage    *dto.Usage
	// Revision 用的哪一版编译器提示词。落进任务记录:上游还有个 v27,
	// 迟早要同步,那时对比两条任务的产出**必须**知道各自跑的是哪一版 ——
	// 不记的话只能靠提交时间猜。
	Revision string
}

// compileSingleCallWithTimeout 在预算内编译。
func compileSingleCallWithTimeout(
	ctx context.Context, authHeader, model string, in EnhanceInput, budget time.Duration,
) (*singleCallResult, error) {
	cctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	return compileViaSingleCall(cctx, authHeader, model, in)
}

func compileViaSingleCall(
	ctx context.Context, authHeader, model string, in EnhanceInput,
) (*singleCallResult, error) {
	if in.Compiler == nil {
		return nil, fmt.Errorf("缺少请求事实(CompilerInput),无法编译")
	}
	ev := buildSingleCallEvidence(*in.Compiler, in.SendMedia)
	evJSON, err := common.Marshal(ev)
	if err != nil {
		return nil, fmt.Errorf("序列化证据失败: %w", err)
	}
	// 写作说明的最后一句就是 "Evidence follows:"，所以证据直接跟在后面。
	instruction := buildSingleCallSystem(ev.Task.Type, string(evJSON))

	// **参考视频不发给增强模型。** 发过、也修好了，但代价和稳定性都不划算：
	//
	//   - 引擎自己拉 CDN 视频会偶发挂死（30s→120s→480s 重试链 ⇒ 465 秒 504），
	//     内联成 data URI 能绕过，但一次请求要多 12288 个视频 token；
	//   - 带上视频之后**开思考就没正文**（实测 74–118 秒、正文空或截断，把
	//     max_tokens 提到 16384 仍然截断），只能硬性关思考 —— 等于放弃了
	//     资产清点那点收益。
	//
	// 所以先只把 <Video N> 标签和时长写进证据，让模型知道「有这么一段参考、
	// 它提供的是什么」，画面内容交给生成段的 H3 自己看（视频照常随生成请求发给
	// H3，只是不给增强模型）。等引擎侧那两件修好再回到「发给模型看」这条路。
	//
	// 代价：参考视频的构图/运镜只能靠用户提示词，提示词里写不准的那部分补不了。
	msgs := []map[string]any{
		{"role": "system", "content": instruction},
		// 图片照常发：证据 JSON 里只有 asset_id 和角色，**模型要看见图本身**
		// 才填得对可见特征。
		{"role": "user", "content": buildUserContent(in.Prompt, in.ImageURLs, nil, in.SendMedia)},
	}

	total := &dto.Usage{}
	var lastErrs []string
	var lastRaw string

	for round := 0; round <= singleCallMaxRepairRounds; round++ {
		// **不发 response_format。** qwen3.8(MTP 投机解码)+ json_object 会被引擎
		// 的语法约束放过不去:线上日志(qwen3.8-flash-fp8 副本,2026-11-20)
		//
		//	EngineCore ERROR [backend_xgrammar.py:168] Failed to advance FSM for request … for tokens 0
		//	EngineCore ERROR [scheduler.py:2131] Unexpected: grammar rejected tokens [0] … Terminating request
		//	APIServer  ERROR [serving.py:207] Request … failed with an internal error during generation  → 500
		//
		// 关思考那一档尤其容易撞上(实测一段窗口内三次)。qwen_pe 早前也是同一个
		// 症状,那里的结论是"约一半请求 500",于是同样不发这个字段 —— 取 JSON 靠
		// extractSingleCall 的容错解析(围栏、前后缀、游离花括号都扛)。
		// 少一个字段换来的是"不再有一整类 500",不亏。
		body := map[string]any{
			"model":      model,
			"messages":   msgs,
			"stream":     false,
			"max_tokens": singleCallMaxTokens(in.MaxOutputTokens),
		}
		// 思考档位:配了 reasoning_effort 就照发,否则退回布尔开关。
		// 两者不能同时发 —— 实测同时发时上游按档位走,布尔那个被忽略,
		// 但换个上游就可能反过来,不如只发一个。
		if effort := strings.TrimSpace(in.ReasoningEffort); effort != "" {
			body["reasoning_effort"] = effort
		} else {
			applyThinking(body, in.Thinking)
		}
		payload, err := common.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("构造编译请求失败: %w", err)
		}
		raw, usage, err := callEnhance(ctx, authHeader, payload)
		accumulateUsage(total, usage)
		if err != nil {
			// 失败也要把已累计的用量带出去：这些调用真实计过费，
			// 回落 text 后 res.Usage 不能只记 text 那一次。
			return &singleCallResult{Usage: total}, err
		}
		if strings.TrimSpace(raw) == "" {
			// 空正文 + usage 一起报。只有"空"这一个词的话线上没法判断是
			// 模型什么都没说、还是思考把 max_tokens 吃光(正文被挤掉) ——
			// 两种的修法完全不同(换档位 vs 提 max_output_tokens)。
			return &singleCallResult{Usage: total},
				fmt.Errorf("编译返回空内容%s", usageSnippet(usage, total))
		}
		lastRaw = raw

		out, perr := extractSingleCall(raw)
		if perr == nil {
			// 节名用错(帧族名 ↔ 参考族名)在这里**确定性修掉**,不花一轮重修。
			if fixed, note := normalizeSectionHeaders(out.H3Prompt, ev.Task.Type); note != "" {
				common.SysLog(fmt.Sprintf("aggregate enhance: singlecall %s (model=%s)", note, model))
				out.H3Prompt = fixed
			}
		}
		if perr != nil {
			// **递回模型的必须是英文。** extractSingleCall 的报错是面向
			// 运维的中文,直接塞进重修指令等于一边要求它"English elsewhere"
			// (writing.txt 原话),一边把一段中文摆在它面前 —— IR 那条路上
			// 正是这么让重修轮失效的。中文留给日志。
			common.SysLog(fmt.Sprintf("aggregate enhance: singlecall 解析失败 (model=%s): %v", model, perr))
			lastErrs = []string{"Response must be a JSON object"}
		} else {
			errs, warns := transportIssues(out, ev)
			if len(errs) == 0 {
				return &singleCallResult{
					Revision:      singleCallRevision,
					Prompt:        out.H3Prompt,
					Plan:          out.planJSON(),
					Uncertainties: out.Uncertainties,
					Warnings:      warns,
					Repaired:      round > 0,
					Usage:         total,
				}, nil
			}
			lastErrs = errs
		}

		if round == singleCallMaxRepairRounds {
			break
		}
		// 重修照上游原话：只修传输错误，**不动语义内容**。
		// 不说清这一点的话模型会整篇重写，把本来对的创作判断一起换掉。
		msgs = append(msgs,
			map[string]any{"role": "assistant", "content": lastRaw},
			map[string]any{"role": "user", "content": buildSingleCallRepair(lastErrs)},
		)
	}
	// 附上模型的原始回答片段。没有它,线上只能看到"镜头时间不规范"这种
	// 结论、看不到它到底写成了什么形状 —— 而修法(补提示词例子 / 放宽解析 /
	// 换档位)完全取决于那个形状。400 字够看出结构,又不会把库撑大。
	return &singleCallResult{Usage: total},
		fmt.Errorf("传输检查未通过: %s%s", strings.Join(lastErrs, "; "), rawReplySnippet(lastRaw))
}

// usageSnippet 把本轮与累计的 token 用量压成一段,附在失败原因后面。
//
// 判断"正文被思考挤空"看 completion_tokens 就够:它包含思考 tokens,
// 贴近 max_tokens 上限而正文为空,基本可以断定是预算问题而不是模型行为。
func usageSnippet(round, total *dto.Usage) string {
	if round == nil && total == nil {
		return ""
	}
	part := func(u *dto.Usage) string {
		if u == nil {
			return "?"
		}
		return fmt.Sprintf("prompt=%d completion=%d total=%d", u.PromptTokens, u.CompletionTokens, u.TotalTokens)
	}
	return fmt.Sprintf(" | usage: round{%s} total{%s}", part(round), part(total))
}

// rawReplySnippet 把模型原始回答压成一行,附在失败原因后面便于复现。
func rawReplySnippet(raw string) string {
	s := strings.Join(strings.Fields(strings.TrimSpace(raw)), " ")
	if s == "" {
		return ""
	}
	const maxRunes = 400
	r := []rune(s)
	if len(r) > maxRunes {
		s = string(r[:maxRunes]) + "…"
	}
	return " | raw: " + s
}

// buildSingleCallRepair 重修指令。逐字对齐上游 compile_once 里那句。
func buildSingleCallRepair(errs []string) string {
	body, _ := common.Marshal(errs)
	return "Repair ONLY these transport errors, keeping semantic content unchanged:\n" + string(body)
}

// ── 证据 ────────────────────────────────────────────────────────────

type singleCallEvidence struct {
	UserRequest string                    `json:"user_request"`
	Task        singleCallEvidenceTask    `json:"task"`
	Assets      []singleCallEvidenceAsset `json:"assets"`
}

type singleCallEvidenceTask struct {
	Type            string  `json:"type"`
	DurationSeconds float64 `json:"duration_seconds"`
	// EffectiveDurationSeconds 引擎**真正交付**的时长(帧栅格对齐后)。
	//
	// 官方规范(base-en.txt:34)明确写着 "S.SS is the effective video duration
	// formatted to exactly two decimal places" —— 即尾帧对齐那句
	// "aligns with the S.SS-second mark" 里的数**不是**客户请求的秒数。
	// H3 的出片帧数是 17k+5,请求 5 秒实际交付 124 帧 = 5.17 秒。
	//
	// 只给 duration_seconds 时模型只能写 5.00,而官方写 5.17 —— 差 0.17 秒
	// 看着微不足道,但它正是"尾帧落在成片之外"那种不报错的偏差来源:
	// 提示词让画面在 5.00 秒定住,而最后一帧在 5.17 秒。
	EffectiveDurationSeconds float64 `json:"effective_duration_seconds,omitempty"`
	GenerateAudio            bool    `json:"generate_audio"`
}

type singleCallEvidenceAsset struct {
	AssetID   string `json:"asset_id"`
	MediaType string `json:"media_type"`
	Role      string `json:"role"`
	// OfficialLabel 提示词里该用的官方记号（`<Picture 1>`）。
	//
	// **必须由我们给，不能让模型自己编号。** 标号按素材的**提交顺序**定，
	// 而素材正是按这个顺序发给模型和 H3 的；让模型按书写顺序编，它先写
	// image_2 再写 image_1，<Picture 1> 就指向了第二张图 —— 提示词指着的
	// 素材和它描述的不是同一个，而且不报错。
	OfficialLabel string `json:"official_label"`
	// Observable 这段素材**你看得见吗**。
	//
	// 参考视频与参考音频一律 false —— 增强阶段不把它们发给模型(原因见
	// buildSingleCallSystem / 本文件顶部"不送参考视频"那段)。**必须显式写出来**,
	// 而不是让模型从"素材清单里有 video_1"自己推断:看不见又不说的结果是它照着
	// 常识编一段画面(实测会写成"海滩上奔跑的狗"这种完全无关的内容),而 H3 拿到
	// 的是一份描述着不存在的视频的提示词 —— 不报错,只是成片错位。
	Observable bool `json:"observable"`
	// LabelUnknown 这段音频的官方编号**我们不知道**(引擎按"视频有没有音轨"决定)。
	//
	// 为 true 时 OfficialLabel 为空,提示词里必须按角色措辞引用它,不能写死
	// <Audio N> —— 写错会把音色指到参考视频自己的声轨上,而且不报错。
	LabelUnknown bool `json:"label_unknown,omitempty"`
}

// singleCallEffectiveDuration 请求秒数 → 引擎实际交付秒数,保留两位小数。
//
// 取不到(秒数 <= 0,即默认档由引擎定帧数)时返回 0 —— 证据里这个字段带
// omitempty,于是**不写**,模型只会看到请求秒数。宁可让它按请求秒数写,
// 也不要在这里编一个栅格数出来:对齐公式由 common.H3DeliveredDuration 单一
// 持有,引擎改栅格时只有那里要改。
func singleCallEffectiveDuration(seconds float64) float64 {
	if seconds <= 0 {
		return 0
	}
	_, delivered := common.H3DeliveredDuration(int(math.Round(seconds)))
	if delivered <= 0 {
		return 0
	}
	return math.Round(delivered*100) / 100
}

// buildSingleCallEvidence 把请求事实翻成证据。
//
// mediaVisible 对应 send_input_images:素材真发给模型时为 true。只有**图片**会随它
// 变成可见 —— 参考视频与参考音频在增强阶段一律不发送,所以 observable 恒为 false。
func buildSingleCallEvidence(in hilo.CompilerInput, mediaVisible bool) singleCallEvidence {
	ev := singleCallEvidence{
		UserRequest: strings.TrimSpace(in.UserRequest),
		Task: singleCallEvidenceTask{
			Type:                     string(in.TaskType),
			DurationSeconds:          in.DurationSeconds,
			EffectiveDurationSeconds: singleCallEffectiveDuration(in.DurationSeconds),
			GenerateAudio:            in.GenerateAudio,
		},
		Assets: make([]singleCallEvidenceAsset, 0, len(in.Assets)),
	}
	// 编号规则：按 media_type 分别计数、按提交顺序递增。
	//
	// **必须和 H3 实际收到的顺序一致**（middleware 的 compilerImages /
	// compilerVideos / RefAudios 给出）——标号按证据发，而 <Picture N> 最终指的是
	// **生成段**收到的那一批字节。错开一位就是「看着第二张图读 <Picture 1>」。
	//
	// 注意这跟"增强模型看到什么"是两件事：参考视频与参考音频不再发给增强模型
	// （见 compileViaSingleCall 顶部），但它们在 H3 那边照常存在，所以标号仍按
	// 完整素材清单编号，模型只是通过 observable:false 知道哪几段自己看不见。
	// **有参考视频时,用户音频不编号。**
	//
	// 引擎的编号规则(对照 vllm-omni 的 ordered_references / encoder_processing)
	// 是「先图、再视频、再音频」,但**带音轨的参考视频会先占掉一个 <Audio> 槽位**,
	// 再拿它的 <Video> 号。于是「1 段有声参考视频 + 1 段用户音色参考」得到的是
	// 视频音轨 = <Audio 1>、用户音频 = <Audio 2>;而我们只看得到 URL,拿不准那段
	// 视频有没有音轨。
	//
	// 编错号的后果不是报错,是**音色指到参考视频自己的声轨上**。所以宁可不说数字:
	// 音频素材标 label_unknown,由合规清单要求模型按角色措辞引用(见 appendix 第 11 条)。
	//
	// 没有参考视频时是确定的:引擎按图→视频→音频排,用户音频就是 <Audio 1>。
	names := map[string]string{"image": "Picture", "video": "Video", "audio": "Audio"}
	hasVideo := false
	for _, a := range in.Assets {
		if a.MediaType == "video" {
			hasVideo = true
			break
		}
	}
	counters := map[string]int{}
	for _, a := range in.Assets {
		if strings.TrimSpace(a.AssetID) == "" {
			continue
		}
		label := ""
		labelUnknown := false
		if n, ok := names[a.MediaType]; ok {
			if a.MediaType == "audio" && hasVideo {
				labelUnknown = true
			} else {
				counters[a.MediaType]++
				label = fmt.Sprintf("<%s %d>", n, counters[a.MediaType])
			}
		}
		ev.Assets = append(ev.Assets, singleCallEvidenceAsset{
			AssetID:       a.AssetID,
			MediaType:     a.MediaType,
			Role:          a.Role,
			OfficialLabel: label,
			// 引擎分配的音频号,我们拿不准(见上面 hasVideo 那段)。
			LabelUnknown: labelUnknown,
			// 只有图片会随 send_input_images 发给模型;视频与音频在增强阶段
			// 一律不发送(见 compileViaSingleCall 顶部),所以恒为 false。
			Observable: mediaVisible && a.MediaType == "image",
		})
	}
	return ev
}

// ── 模型输出 ────────────────────────────────────────────────────────

type singleCallOutput struct {
	// ContentPlan 原样收着,**不在这里定形状**。
	//
	// 只声明 bindings / shots 两个字段并不能换来宽容:encoding/json 对
	// **已声明**的字段依然严格,模型把 start_seconds 写成 "0"(字符串)、
	// 把 bindings 写成对象、或者干脆把 content_plan 写成一句话,整份
	// 反序列化就失败 —— 连同一段完全可用的 h3_prompt 一起丢掉,白烧一轮
	// 重修,最后静默回落 text。IR 那条路上就是这么栽的(sync_rules 被写成
	// 对象数组)。
	//
	// 所以顶层只收 RawMessage,形状在 parsePlan 里宽松地解,解不动就当
	// "计划有问题",而不是"整次编译作废"。
	ContentPlan   json.RawMessage  `json:"content_plan"`
	H3Prompt      string           `json:"h3_prompt"`
	Uncertainties hilo.FlexStrings `json:"uncertainties"`

	// plan 是 ContentPlan 宽松解出来的结果。nil = 解不出对象。
	plan *singleCallPlan
}

// planJSON 生产记录原文。
//
// **只存 content_plan 这一棵子树。** 早先这里返回的是整份模型回复,于是
// 名字叫 content_plan 的那一列里躺着 h3_prompt 的副本 —— 而排障时正是
// 靠这两列对照"模型把什么当成了 must_keep、最终写成了什么",两者混在
// 一起就对照不了了。
func (o *singleCallOutput) planJSON() string {
	if len(o.ContentPlan) == 0 {
		return ""
	}
	return string(o.ContentPlan)
}

type singleCallPlan struct {
	Bindings flexBindings     `json:"bindings"`
	Shots    []singleCallShot `json:"shots"`
}

type singleCallBinding struct {
	AssetID string `json:"asset_id"`
}

// flexBindings 容忍 bindings 被写成单个对象而不是数组。
//
// **容忍形态,不容忍语义。** 写成对象时当作只有一条,仍然照常校验它引用的
// asset_id 存不存在;既不是数组也不是对象时记下 Malformed —— 光把列表置空
// 会让整道绑定校验静默变成空操作("没有绑定"当然挑不出毛病"),而上游那条
// `bindings must be an array` 本来就是要报出来的。
type flexBindings struct {
	Items []singleCallBinding
	// Malformed 既不是数组也不是单个对象。
	Malformed bool
}

func (b *flexBindings) UnmarshalJSON(data []byte) error {
	var many []singleCallBinding
	if err := common.Unmarshal(data, &many); err == nil {
		b.Items = many
		return nil
	}
	var one singleCallBinding
	if err := common.Unmarshal(data, &one); err == nil {
		b.Items = []singleCallBinding{one}
		return nil
	}
	b.Malformed = true
	return nil
}

type singleCallShot struct {
	StartSeconds flexSeconds `json:"start_seconds"`
	EndSeconds   flexSeconds `json:"end_seconds"`
}

// flexSeconds 秒数。容忍数字与数字字符串两种写法。
//
// **必须区分"没写"和"写了 0"。** 用裸 float64 的话,缺字段的零值会把一份
// 残缺的计划伪装成合法的"从 0 开始" —— 而镜头时间对不上是静默失败:
// H3 照样生成,只是最后一段被拉长或截断。
//
// 解不动时保持 Set=false,等同于"没写",由 transportIssues 报出具名问题
// 并触发一轮重修。
type flexSeconds struct {
	Value float64
	Set   bool
}

func (f *flexSeconds) UnmarshalJSON(data []byte) error {
	raw := strings.TrimSpace(string(data))
	if raw == "" || raw == "null" {
		return nil
	}
	v, err := strconv.ParseFloat(strings.Trim(raw, `"`), 64)
	if err != nil {
		return nil
	}
	f.Value, f.Set = v, true
	return nil
}

// parsePlan 宽松地解 content_plan。返回 nil 表示它根本不是一个对象。
func parsePlan(raw json.RawMessage) *singleCallPlan {
	if len(raw) == 0 {
		return nil
	}
	var probe map[string]json.RawMessage
	if err := common.Unmarshal(raw, &probe); err != nil {
		return nil
	}
	plan := &singleCallPlan{}
	// 逐字段解,单个字段解不动不影响其余 —— 自定义 UnmarshalJSON 已经
	// 把形态问题吞成"空值",所以这里不会因为一处漂移丢掉整棵树。
	_ = common.Unmarshal(raw, plan)
	return plan
}

// extractSingleCall 从模型回复里取出 JSON 对象。
//
// 走 jsonCandidates：它处理围栏、前后缀，以及
// 模型偶发在开头多吐一个游离花括号的情况 —— 那不是"多包一层"，整串净
// 深度是 1，掐头去尾会把真正的收尾括号削掉。
func extractSingleCall(raw string) (*singleCallOutput, error) {
	var lastErr error
	for _, cand := range jsonCandidates(raw) {
		out := &singleCallOutput{}
		if err := common.Unmarshal([]byte(cand), out); err != nil {
			lastErr = err
			continue
		}
		out.plan = parsePlan(out.ContentPlan)
		return out, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("回复里没有 JSON 对象")
	}
	return nil, fmt.Errorf("解析编译结果失败: %w", lastErr)
}

// ── 传输检查 ────────────────────────────────────────────────────────

// officialLabelPattern 提示词里引用的素材记号，如 `<Picture 2>`。
//
// 必须含 **Audio**:参考族会把参考音频写成 <Audio 1>(音色参考),而参考视频
// 自带的音轨官方也算一个 <Audio>。漏掉这一类,提示词里指向不存在音轨的
// <Audio 2> —— 那正是"参考了不存在的素材"这种不报错的错 —— 就查不出来。
var officialLabelPattern = regexp.MustCompile(`<(Picture|Video|Audio)\s+(\d+)>`)

// shotTimeTolerance 时间比对容差。与上游的 0.002 一致。
const shotTimeTolerance = 0.002

// transportIssues 传输检查：**只核对运得出去，不核对写得好不好**。
//
// 逐条对应上游 single_call_compiler.transport_issues，有一处出入：
// 上游对节名缺失只给警告（"inspect wording, no automatic rewrite"），
// 我们判为**错误**并触发重修 —— 线上证明了照搬是错的，见 missingSections。
//
// warns 这一路目前**没有任何来源**。保留返回值而不是删掉：上游那份检查里
// 警告是个正经分类（形态可疑但不影响交付），下一条这种检查随时会回来；
// 而改签名会牵动调用点与测试，不值得为一次空置做。
func transportIssues(out *singleCallOutput, ev singleCallEvidence) (errs []string, warns []string) {
	if strings.TrimSpace(out.H3Prompt) == "" {
		errs = append(errs, "h3_prompt must be nonempty")
	}
	if out.plan == nil {
		return append(errs, "content_plan must be an object"), warns
	}

	ids := make(map[string]bool, len(ev.Assets))
	for _, a := range ev.Assets {
		ids[a.AssetID] = true
	}
	if out.plan.Bindings.Malformed {
		errs = append(errs, "bindings must be an array")
	}
	for _, b := range out.plan.Bindings.Items {
		if !ids[b.AssetID] {
			errs = append(errs, "Binding references an unknown asset_id: "+b.AssetID)
		}
	}

	errs = append(errs, shotTimingIssues(out.plan.Shots, ev.Task.DurationSeconds)...)
	errs = append(errs, labelIssues(out.H3Prompt, ev.Assets)...)
	errs = append(errs, dialogueIssues(ev.UserRequest, out.H3Prompt)...)
	errs = append(errs, missingSections(out.H3Prompt, ev.Task.Type)...)
	errs = append(errs, duplicateSections(out.H3Prompt, ev.Task.Type)...)
	errs = append(errs, missingAssetMentions(out.H3Prompt, ev.Assets, ev.Task.Type)...)
	return errs, warns
}

// shotTimingIssues 镜头时间必须是数字、从 0 起连续递增、正好覆盖到请求时长。
//
// 这是整套检查里唯一真正"硬"的一条,因为它的失败是**静默的**:镜头加起来
// 比请求短,H3 照样生成,只是最后一段被拉长或截断,没有任何地方报错。
func shotTimingIssues(shots []singleCallShot, duration float64) []string {
	if len(shots) == 0 {
		return []string{"shots must be a nonempty array"}
	}
	last := 0.0
	for _, s := range shots {
		if !s.StartSeconds.Set || !s.EndSeconds.Set {
			return []string{"Shot times must be numeric, contiguous, increasing and within task.duration_seconds"}
		}
		a, b := s.StartSeconds.Value, s.EndSeconds.Value
		if math.IsNaN(a) || math.IsInf(a, 0) || math.IsNaN(b) || math.IsInf(b, 0) ||
			math.Abs(a-last) >= shotTimeTolerance || b <= a || b > duration+shotTimeTolerance {
			return []string{"Shot times must be numeric, contiguous, increasing and within task.duration_seconds"}
		}
		last = b
	}
	if math.Abs(last-duration) > shotTimeTolerance {
		// 这句话会原样进重修指令：必须点名字段，否则模型分不清
		// 「target duration」指 duration_seconds 还是 effective_duration_seconds，
		// 按后者写就会再被判一次失败。
		return []string{"Shot plan must end at exact task.duration_seconds " +
			strconv.FormatFloat(duration, 'g', -1, 64)}
	}
	return nil
}

// audioLabelUnknown 有音频素材的官方编号我们不知道(通常是"参考视频带没带音轨")。
func audioLabelUnknown(assets []singleCallEvidenceAsset) bool {
	for _, a := range assets {
		if a.LabelUnknown {
			return true
		}
	}
	return false
}

// labelIssues 提示词不能引用不存在的素材。
//
// `<Picture 3>` 而实际只传了两张图 —— H3 拿不到第三张，表现是那一段描述
// 落空,而不是报错。
func labelIssues(prompt string, assets []singleCallEvidenceAsset) []string {
	if strings.TrimSpace(prompt) == "" {
		return nil
	}
	counts := map[string]int{}
	for _, a := range assets {
		switch a.MediaType {
		case "image":
			counts["Picture"]++
		case "video":
			counts["Video"]++
			// 参考视频**可能**自带音轨,引擎会给它一个 <Audio> 槽位。我们拿不到
			// "这段视频有没有音轨",所以这里多算一位 —— 多一位不会让合法提示词被判错,
			// 少一位会把照官方写法写的 <Audio 1> 判成不存在的素材、白烧一轮重修。
			counts["Audio"]++
		case "audio":
			// 编号未知时不计数:合法范围取决于视频有没有音轨(见 buildSingleCallEvidence 的 hasVideo)。
			if !a.LabelUnknown {
				counts["Audio"]++
			}
		}
	}
	// 一个音轨都没有时不查 <Audio N>:那种情况下"提示词里出现 <Audio 1>"多半
	// 是用户自己在文本里说了"音色参考音频1",而不是我们漏发了素材 ——
	// 每一条都判错会把一份本来能用的提示词送去重修,严重的还整次回落 text。
	countsAudio := counts["Audio"]
	// 用户音频编号未知(有参考视频)时也跳过:合法编号可能是 1 也可能是 2,
	// 判哪一边都会误伤。这类素材的编号问题由"不写数字"这条规则规避。
	var out []string
	seen := map[string]bool{}
	for _, m := range officialLabelPattern.FindAllStringSubmatch(prompt, -1) {
		kind := m[1]
		n, err := strconv.Atoi(m[2])
		if kind == "Audio" && (countsAudio == 0 || audioLabelUnknown(assets)) {
			continue
		}
		if err != nil || n < 1 || n > counts[kind] {
			if !seen[kind] {
				out = append(out, "Prompt references nonexistent "+kind)
				seen[kind] = true
			}
		}
	}
	return out
}

// 官方节名。t2v 那几种玩法用三节（base-en.txt），全参考（r2va）用六节
// （ref-en.txt）。
//
// **与上游有两处有意的出入。**
//
// 一、按玩法选名单。上游无论什么玩法都拿六节去比，于是每个 t2v 请求都会
// 得到一条必然为真的警告 —— 警告一旦恒真就等于没有。
//
// 二、**缺节是错误，不是警告。** 上游的处置是 "inspect wording, no
// automatic rewrite"，我照搬了，线上立刻证明它对我们不成立：
//
//	14:27:44  缺 subject_definitions, summary, retention_analysis, detailed_description
//	14:44:32  缺 detailed_description, overall_soundscape
//
// 第二条那次，detailed_description 是**分镜正文所在的那一节，台词住在
// 里面**。H3 拿到一份没有任何台词、没有声音计划的提示词，只能自己编 ——
// 用户要的是六句中文口播，成片说的是英文。而画面、时长、分辨率全都正常，
// 没有任何地方报错。
//
// 上游那句话针对的是"措辞可能不理想"；而且上游没有重修轮，我们有 ——
// 让模型补一节正是重修该干的事。
var (
	baseSections = []string{"integrated_multimodal_description", "overall_soundscape", "non_diegetic_music"}
	refSections  = []string{"subject_definitions", "summary", "retention_analysis",
		"detailed_description", "overall_soundscape", "non_diegetic_music"}
)

// missingSections 缺哪几个官方必需节。返回非空即**错误**（触发重修）。
//
// # 判据必须宽
//
// 这条是硬错误，且只有一轮重修 —— 误判一次的代价是整次编译作废、静默回落
// text。而节名的排版形态很多，规范自己就用了好几种：
//
//	integrated_multimodal_description: ...        （base-en.txt:39 正文）
//	- **overall_soundscape**: Summarizes ...      （base-en.txt:47 说明）
//	`detailed_description`                        （ref-en.txt:7）
//	### 4.6 overall_soundscape                    （base-en.txt:152）
//
// 模型照着这些示例写，装饰字符（**、反引号、#、-）随时可能带上。早先一版
// 要求 name 后面紧跟冒号，实测五种形态里误判四种。
//
// 所以只问一件事：**这个节名在提示词里出现过吗**。出现了就当它在 —— 漏判
// 的代价（少报一次缺节）远小于误判（整次编译作废）。
func missingSections(prompt, taskType string) []string {
	want := requiredSections(taskType)
	// 归一掉装饰字符与下划线，"**Overall_Soundscape**" 与 "overall soundscape"
	// 都落到同一个形态。
	norm := normalizeSectionText(prompt)
	var missing []string
	for _, s := range want {
		if !strings.Contains(norm, normalizeSectionText(s)) {
			missing = append(missing, s)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return []string{"Required official sections are missing: " + strings.Join(missing, ", ") +
		". " + sectionRepairHint(taskType) +
		" Emit every section, in the order given by the H3 writing guide."}
}

// ── 素材必须被点名 ──────────────────────────────────────────────────

// unlabelledAudioPhrase 编号未知的音频在提示词里的固定措辞(appendix 第 11 条)。
// 判据用**措辞**而不是数字:数字我们不知道,措辞是规则里定死的。
const unlabelledAudioPhrase = "user-supplied audio reference"

// missingAssetMentions 证据里的素材在提示词里一次都没出现。
//
// **这是"静默丢掉参考"的那类错。** 参考族的提示词靠标签把素材和 H3 收到的字节
// 绑起来:一句都不提 <Video 1>,H3 就只剩"参考生成"这个模式,不知道该拿那段视频
// 干什么 —— 成片照样出,只是看着不像"用了参考"。
//
// 实测:参考视频不发给增强模型(见 compileViaSingleCall 顶部)之后,模型曾整段
// 不提 <Video 1>(subject_definitions 里只写了 <Audio 1>),提示词侧的可见性规则
// 修的就是这个,这道检查兜住同样的问题。
//
// 只对**参考族**要求视频/音频必须出现。图片不在这里要求 —— 它可能只作为构图锚点
// 被提到一次、或写在 <Subject N> 的定义里,强行要求反而误判。
func missingAssetMentions(prompt string, assets []singleCallEvidenceAsset, taskType string) []string {
	if strings.TrimSpace(prompt) == "" || !strings.Contains(strings.ToLower(taskType), "r2v") {
		return nil
	}
	var missing []string
	for _, a := range assets {
		if a.MediaType != "video" && a.MediaType != "audio" {
			continue
		}
		// 编号未知的音频没有数字可查,但**必须按角色措辞出现过**(appendix 第 11 条把
		// 措辞定死了)。整段不提它,H3 就只能自己编一条嗓子 —— 那正是这个函数要防的。
		if a.LabelUnknown {
			if !strings.Contains(strings.ToLower(prompt), unlabelledAudioPhrase) {
				missing = append(missing, unlabelledAudioPhrase)
			}
			continue
		}
		if a.OfficialLabel == "" {
			continue
		}
		if !strings.Contains(prompt, a.OfficialLabel) {
			missing = append(missing, a.OfficialLabel)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return []string{"Every reference asset must be named at least once (definition and retention line); missing: " +
		strings.Join(missing, ", ")}
}

// ── 重复节 ──────────────────────────────────────────────────────────

// duplicateSections 同一节被写了两遍。**这是错误。**
//
// 实测 r2va 三次里有一次把 summary 整段原样写了两遍:节名一个不缺,missingSections
// 查不出来(它只问"在不在"),而 H3 拿到的是一份自相矛盾的提示词 —— 两份 summary
// 讲同一件事时看不出害处,但只要其中一份写偏了(实测另一份把参考视频当成了唯一
// 素材),模型就只能二选一,而不报错。
//
// 只认**行首**的节名形态(允许 **、#、-、反引号这类装饰和任意分隔符),不认正文里
// 顺口提到的 "the overall soundscape of the video":后者在官方输出里很常见,
// 按出现次数硬数会把官方自己判错。
func duplicateSections(prompt, taskType string) []string {
	if strings.TrimSpace(prompt) == "" {
		return nil
	}
	var dup []string
	for _, s := range requiredSections(taskType) {
		if n := sectionHeaderCount(prompt, s); n > 1 {
			dup = append(dup, fmt.Sprintf("%s ×%d", s, n))
		}
	}
	if len(dup) == 0 {
		return nil
	}
	return []string{"Each required section must be emitted exactly once; these are repeated: " +
		strings.Join(dup, ", ") + ". Merge each into a single section."}
}

// ── 节名归一 ────────────────────────────────────────────────────────

// normalizeSectionHeaders 把"用错名字"的那一节改成官方名字。
//
// 实测 r2va 的正文节名会写成 integrated_multimodal_description(帧族那个名字),
// 而参考族要求 detailed_description —— 两者**指的是同一件事**(分镜正文与台词都
// 住在里面),差别只是名字。四份样本里三份犯这个错。
//
// 这不是"放过错误":漏了正文那一节是真错误(台词会丢),而**改个名字**是确定性
// 的结构修复,比让模型重修一轮更快也更稳 —— 重修花掉的是一次真实的 relay 调用,
// 而且第二轮仍可能写错(实测就会)。open-h3-ir 的结论同向:结构该由代码定,
// 散文交给模型。
//
// 反方向(帧族写了 detailed_description)一并修,理由相同。
func normalizeSectionHeaders(prompt, taskType string) (string, string) {
	if strings.TrimSpace(prompt) == "" {
		return prompt, ""
	}
	if strings.Contains(strings.ToLower(taskType), "r2v") {
		if sectionHeaderCount(prompt, "detailed_description") == 0 &&
			sectionHeaderCount(prompt, "integrated_multimodal_description") > 0 {
			return renameSectionHeader(prompt, "integrated_multimodal_description", "detailed_description"),
				"正文节名 integrated_multimodal_description → detailed_description(r2va 要求后者)"
		}
		return prompt, ""
	}
	if sectionHeaderCount(prompt, "integrated_multimodal_description") == 0 &&
		sectionHeaderCount(prompt, "detailed_description") > 0 {
		return renameSectionHeader(prompt, "detailed_description", "integrated_multimodal_description"),
			"正文节名 detailed_description → integrated_multimodal_description(帧族要求后者)"
	}
	return prompt, ""
}

// renameSectionHeader 只改**行首那个节名**,正文里提到的同名字样不动。
func renameSectionHeader(prompt, from, to string) string {
	parts := strings.Split(from, "_")
	for i, p := range parts {
		parts[i] = regexp.QuoteMeta(p)
	}
	needle := "(?im)^([ \t>*#_\\-\x60]*)" + strings.Join(parts, "[\\s_\\-]*") + "([ \t*_\\-]*:)"
	re, err := regexp.Compile(needle)
	if err != nil {
		return prompt
	}
	return re.ReplaceAllString(prompt, "${1}"+to+"${2}")
}

// sectionHeaderCount 数这一节作为**标题**出现了几次。
//
// 节名里的下划线在模型笔下可能是空格或连字符(与 normalizeSectionText 同一套
// 容错),所以针里用 [\s_-]* 连接各段。
func sectionHeaderCount(prompt, name string) int {
	parts := strings.Split(name, "_")
	for i, p := range parts {
		parts[i] = regexp.QuoteMeta(p)
	}
	needle := "(?im)^[ \t>*#_\\-\x60]*" + strings.Join(parts, "[\\s_\\-]*") + "[ \t*_\\-]*:"
	re, err := regexp.Compile(needle)
	if err != nil {
		return 0
	}
	return len(re.FindAllStringIndex(prompt, -1))
}

// requiredSections 这次玩法要求哪几节。
func requiredSections(taskType string) []string {
	if strings.Contains(strings.ToLower(taskType), "r2v") {
		return refSections
	}
	return baseSections
}

// sectionRepairHint 递给模型的修复提示。**必须按玩法给** —— 帧族没有
// detailed_description 那一节（它的正文是 integrated_multimodal_description），
// 照着说会让模型凭空加一节不该有的，而 baseSections 不会拒绝多出来的东西，
// 于是一份格式错的提示词就发给 H3 了。
func sectionRepairHint(taskType string) string {
	if strings.Contains(strings.ToLower(taskType), "r2v") {
		return "detailed_description carries the shot body and the <d>…</d> dialogue; " +
			"overall_soundscape carries the sound plan."
	}
	return "integrated_multimodal_description carries the shot body and the <d>…</d> dialogue; " +
		"overall_soundscape carries the sound plan."
}

// normalizeSectionText 归一化到"小写 + 字母数字，其余一律当分隔符"。
//
// **其余一律当分隔符，不能只挑几个。** 早先只把 `_` / 空格 / `-` 换成空格、
// 其它非字母数字**删掉**，于是节名里夹一个别的分隔符就会把词粘回去、
// 匹配不上，实测六种里误判五种：
//
//	integrated\tmultimodal\tdescription   → integratedmultimodaldescription
//	### Overall\nSoundscape               → overallsoundscape
//	integrated＿multimodal＿description     → 同上（全角下划线）
//	integrated—multimodal—description      → 同上（长破折号）
//	integrated\u00a0multimodal…            → 同上（不换行空格）
//
// 而 needle（节名）自身只含字母、数字和下划线，所以"把一切非字母数字变成
// 空格"这个方向是**单向安全**的:它只会让更多形态匹配上,不会让本来能匹配的
// 匹配不上。
//
// 代价是正文里一句 "the overall soundscape of the video" 也会被算作节存在 ——
// 那属于漏判(少报一次缺节),而本函数的取舍就是宁可漏判不可误判:
// 误判一次是整次编译作废、静默回落 text。
func normalizeSectionText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := true // 前导空白也压掉
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			space = false
			continue
		}
		// **连续分隔符必须压成一个。** 不压的话 `integrated_multimodal_\ndescription`
		// 会归一成 "integrated multimodal  description"(两个空格),而 needle 里
		// 词间只有一个空格 —— 换行折断的长节名就匹配不上了。
		if !space {
			b.WriteByte(' ')
			space = true
		}
	}
	return b.String()
}

// singleCallBudget 本次编译的时间预算：配置优先，没配用内置默认。
//
// 与 text 那条的 enhanceTimeout 分开：两者的耗时不是一个量级（singlecall
// 要吐一份 content_plan 加一整段提示词，还带着约 70 KB 的系统提示词），
// 共用一个数会让其中一个要么被吊死、要么白等。
func singleCallBudget(cfg *common.AggregatePromptEnhance) time.Duration {
	if cfg != nil && cfg.TimeoutSeconds > 0 {
		return time.Duration(cfg.TimeoutSeconds) * time.Second
	}
	return singleCallTimeout
}

// spokenDialoguePattern 用户提示词里**真有说话语境**的台词。
//
// # 为什么不能只看引号
//
// 中文提示词里引号最常见的用途是风格词和强调词:
//
//	生成一段"赛博朋克"风格的城市夜景
//	背景是"虚化"的树林
//
// 只按「成对引号 + 中文」判,这些全会被当成台词。而这道检查会触发一轮
// **完整的重修往返**(默认预算 120 秒,且就卡在客户点生成之后),误报的
// 代价不是"白等一次",是让这条检查对一大类最常见的中文输入实际不可用 ——
// 要么把"赛博朋克"包进 <d>[Chinese] …</d> 塞进最终提示词,要么重修还是
// 不过、静默回落 text。
//
// 所以判据收窄成:引号**前面**得有一个说话动词。宁可漏,不可错 ——
// 漏掉的后果是回到改造前(没有这道检查),误报的后果是把正常请求搞坏。
var spokenDialoguePattern = regexp.MustCompile(
	`(?:说|讲|喊|叫|问|答|回答|念|唱|吟|道|says?|said|shouts?|asks?|replies|sings?)` +
		`\s*[:：]?\s*` +
		"[「『\"\u201c\u2018]([^「」『』\"\u201c\u201d\u2018\u2019]{2,60})[」』\"\u201d\u2019]")

// cjkRun 至少两个连续中日韩字符 —— 用来判断引号里装的是不是非英文台词。
var cjkRun = regexp.MustCompile(`[\p{Han}\p{Hiragana}\p{Katakana}\p{Hangul}]{2,}`)

// dialogueIssues 用户写明的台词必须**逐字**出现在提示词里。
//
// # 为什么必须程序来查
//
// 移植来的 writing.txt 写着 "Preserve original-language exact dialogue and
// visible text, English elsewhere",官方 skill 更要求台词包进
// <d>[Chinese] …</d> 且 "do not translate or rewrite them"。但这些都只是
// **指令** —— 而正文改用英文之后,把「你好」顺手写成 "Hello" 是模型最自然的
// 动作,尤其是在一段全英文描述的中间。
//
// 这是这一步最难发现的错:成片口型对得上、时长对得上、读起来完全正常,
// 只有语言换了 —— 而用户要的恰恰是那句话。text 模式下正文本来就是中文,
// 撞不上这个问题;换成英文输出之后它才成为真实风险。
//
// 这是**确定性传输检查**,不是上游禁止的那种"独立审计阶段"(那指的是再叫
// 一次模型)。
func dialogueIssues(userRequest, prompt string) []string {
	if strings.TrimSpace(prompt) == "" {
		return nil
	}
	var missing []string
	seen := map[string]bool{}
	for _, m := range spokenDialoguePattern.FindAllStringSubmatch(userRequest, -1) {
		line := strings.TrimSpace(m[1])
		// 只管非英文台词。英文台词被改写成另一句英文这里查不出来,
		// 那是语义问题,不在传输检查的职责内。
		if line == "" || seen[line] || !cjkRun.MatchString(line) {
			continue
		}
		seen[line] = true
		if !strings.Contains(prompt, line) {
			missing = append(missing, line)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return []string{"Verbatim dialogue must be preserved in its original language " +
		"inside <d>[Language] ...</d>; missing: " + strings.Join(missing, " / ")}
}

// ── 从已删除的 IR 实现里搬过来的工具 ──────────────────────────────
//
// 这几个函数原先住在已删除的 service/aggregate_enhance_ir.go。IR 那套架构
// (上游 v20 已废弃它,见 service/h3v20/README.md),但它们记录的是**模型
// 行为的实测事实**,与哪套架构无关 —— 尤其 jsonCandidates 里那个游离花括号,
// 是真实采样打回来的。

// accumulateUsage 把多轮调用的 usage 累加起来。
//
// **必须累加,不能取最后一轮**:重修轮跑掉的 token 客户已经被计过费了
// (每一轮都是一次真实的 relay 调用),只报最后一轮会让聚合日志里的用量
// 小于实际扣费,对账时对不上。
func accumulateUsage(dst *dto.Usage, src *dto.Usage) {
	if dst == nil || src == nil {
		return
	}
	dst.PromptTokens += src.PromptTokens
	dst.CompletionTokens += src.CompletionTokens
	dst.TotalTokens += src.TotalTokens
}

// jsonCandidates 按可能性从高到低给出待解析的候选片段。
//
// # 第二个候选是实测打回来的
//
// qwen3.8-27b 会**偶发**地在开头多吐一个游离的花括号:
//
//	{{"schema_version":"0.1.0", … "]}}
//
// 五次采样中了两次。看起来像"多包了一层",其实不是 —— 整串花括号的净深度
// 是 **1**,只有开头那一个是多余的,结尾并没有对应地多出来一个。所以
// 「掐头去尾各削一个字符」修不好它,反而会把真正的收尾括号削掉。
//
// 更麻烦的是 extractJSONObject 按配平找结尾,这种输入它永远配不平、直接
// 返回空 —— 于是连解析都到不了,报出来的是"找不到 JSON 对象",指不到真正
// 的原因。必须在这一层就把这个候选喂进去。
//
// 偶发比稳定出错更难查:同一份配置大多数时候好用,偶尔悄悄回落 text 改写,
// 而回落是不报错的。
func jsonCandidates(raw string) []string {
	var out []string
	add := func(s string) {
		if s != "" {
			out = append(out, s)
		}
	}
	add(extractJSONObject(raw))

	// 掐掉一个游离的前导花括号再试一次。
	t := strings.TrimSpace(stripCodeFence(raw))
	if strings.HasPrefix(t, "{{") {
		add(extractJSONObject(t[1:]))
	}
	return out
}

// stripCodeFence 去掉代码围栏,取中间那段。
func stripCodeFence(raw string) string {
	t := strings.TrimSpace(raw)
	if !strings.HasPrefix(t, "```") {
		return t
	}
	if i := strings.IndexByte(t, '\n'); i >= 0 {
		t = t[i+1:]
	}
	if i := strings.LastIndex(t, "```"); i >= 0 {
		t = t[:i]
	}
	return strings.TrimSpace(t)
}

// extractJSONObject 取出第一个完整的顶层 JSON 对象。
//
// 按花括号配平找结尾,而不是取最后一个 `}`:模型在 JSON 之后还说了话时
// (「…以上就是 IR。如需调整请告知。」)最后一个 `}` 可能落在正文里,截出来
// 的片段解析失败,一次本来成功的编译就白跑了。
//
// 字符串内的花括号不算数 —— 提示词正文里出现 `}` 完全正常,不跳过引号会
// 在第一个带花括号的描述那里提前收尾。
func extractJSONObject(raw string) string {
	s := strings.TrimSpace(raw)
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return ""
	}
	depth := 0
	inStr := false
	escaped := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inStr {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return "" // 花括号没配平:截断的输出,解析也不会成功
}
