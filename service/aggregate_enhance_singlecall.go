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
	parts = append(parts, skillShotPlanning, singleCallRules, singleCallWriting+evidenceJSON)
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
var singleCallTimeout = 120 * time.Second

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
	ev := buildSingleCallEvidence(*in.Compiler)
	evJSON, err := common.Marshal(ev)
	if err != nil {
		return nil, fmt.Errorf("序列化证据失败: %w", err)
	}
	// 写作说明的最后一句就是 "Evidence follows:"，所以证据直接跟在后面。
	instruction := buildSingleCallSystem(ev.Task.Type, string(evJSON))

	msgs := []map[string]any{
		{"role": "system", "content": instruction},
		// 素材跟着第一轮发出去。证据 JSON 里只有 asset_id 和角色，
		// **模型要看见素材本身**才填得对绑定里的可见特征。
		{"role": "user", "content": buildUserContent(in.Prompt, in.ImageURLs, in.VideoURLs, in.SendMedia)},
	}

	total := &dto.Usage{}
	var lastErrs []string
	var lastRaw string

	for round := 0; round <= singleCallMaxRepairRounds; round++ {
		body := map[string]any{
			"model":    model,
			"messages": msgs,
			"stream":   false,
			// 要 JSON 对象。上游不认这个字段时会忽略它(不是报错)，所以
			// extractSingleCall 仍然得自己扛住围栏和前后缀。
			"response_format": map[string]any{"type": "json_object"},
		}
		applyThinking(body, in.Thinking)
		payload, err := common.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("构造编译请求失败: %w", err)
		}
		raw, usage, err := callEnhance(ctx, authHeader, payload)
		accumulateUsage(total, usage)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(raw) == "" {
			return nil, fmt.Errorf("编译返回空内容")
		}
		lastRaw = raw

		out, perr := extractSingleCall(raw)
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
	return nil, fmt.Errorf("传输检查未通过: %s", strings.Join(lastErrs, "; "))
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
	GenerateAudio   bool    `json:"generate_audio"`
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
}

func buildSingleCallEvidence(in hilo.CompilerInput) singleCallEvidence {
	ev := singleCallEvidence{
		UserRequest: strings.TrimSpace(in.UserRequest),
		Task: singleCallEvidenceTask{
			Type:            string(in.TaskType),
			DurationSeconds: in.DurationSeconds,
			GenerateAudio:   in.GenerateAudio,
		},
		Assets: make([]singleCallEvidenceAsset, 0, len(in.Assets)),
	}
	// 编号规则：按 media_type 分别计数、按提交顺序递增。
	//
	// **必须和素材实际发出去的顺序一致**（ImageURLs / VideoURLs，由
	// middleware 的 compilerImages/compilerVideos 给出）——标号按证据发，
	// 而模型看到的是那两个列表，错开一位就是「看着第二张图读 <Picture 1>」。
	names := map[string]string{"image": "Picture", "video": "Video", "audio": "Audio"}
	counters := map[string]int{}
	for _, a := range in.Assets {
		if strings.TrimSpace(a.AssetID) == "" {
			continue
		}
		label := ""
		if n, ok := names[a.MediaType]; ok {
			counters[a.MediaType]++
			label = fmt.Sprintf("<%s %d>", n, counters[a.MediaType])
		}
		ev.Assets = append(ev.Assets, singleCallEvidenceAsset{
			AssetID:       a.AssetID,
			MediaType:     a.MediaType,
			Role:          a.Role,
			OfficialLabel: label,
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
var officialLabelPattern = regexp.MustCompile(`<(Picture|Video)\s+(\d+)>`)

// shotTimeTolerance 时间比对容差。与上游的 0.002 一致。
const shotTimeTolerance = 0.002

// transportIssues 传输检查：**只核对运得出去，不核对写得好不好**。
//
// 逐条对应上游 single_call_compiler.transport_issues。errors 会触发一轮
// 重修，warnings 只记录 —— 上游对节名缺失的处置就是
// "inspect wording, no automatic rewrite"，我们照办：自动重写一份自己
// 都没把握的提示词，比留一条警告糟得多。
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
	warns = append(warns, sectionWarnings(out.H3Prompt, ev.Task.Type)...)
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
			return []string{"Shot times must be numeric, contiguous, increasing and within target duration"}
		}
		a, b := s.StartSeconds.Value, s.EndSeconds.Value
		if math.IsNaN(a) || math.IsInf(a, 0) || math.IsNaN(b) || math.IsInf(b, 0) ||
			math.Abs(a-last) >= shotTimeTolerance || b <= a || b > duration+shotTimeTolerance {
			return []string{"Shot times must be numeric, contiguous, increasing and within target duration"}
		}
		last = b
	}
	if math.Abs(last-duration) > shotTimeTolerance {
		return []string{"Shot plan must end at exact target duration " +
			strconv.FormatFloat(duration, 'g', -1, 64)}
	}
	return nil
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
		}
	}
	var out []string
	seen := map[string]bool{}
	for _, m := range officialLabelPattern.FindAllStringSubmatch(prompt, -1) {
		kind := m[1]
		n, err := strconv.Atoi(m[2])
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
// **这里与上游有一处有意的出入**：上游无论什么玩法都拿六节去比，于是每个
// t2v 请求都会得到一条必然为真的警告。警告一旦恒真就等于没有 —— 真出问题
// 时没人会多看一眼。所以按玩法选名单。
var (
	baseSections = []string{"integrated_multimodal_description", "overall_soundscape", "non_diegetic_music"}
	refSections  = []string{"subject_definitions", "summary", "retention_analysis",
		"detailed_description", "overall_soundscape", "non_diegetic_music"}
)

func sectionWarnings(prompt, taskType string) []string {
	want := baseSections
	if strings.Contains(strings.ToLower(taskType), "r2v") {
		want = refSections
	}
	var missing []string
	for _, s := range want {
		if !strings.Contains(prompt, s) {
			missing = append(missing, s)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return []string{"Some official section labels are absent (" +
		strings.Join(missing, ", ") + "); inspect wording, no automatic rewrite"}
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
