package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/hilo"
)

// IR 模式:模型先吐结构化 JSON,我们确定性地校验、再确定性地渲染成提示词。
//
// # 为什么绕这一圈
//
// 文本改写吐出来的东西**没有任何地方能校验**。镜头时长加起来不等于请求时长、
// 描述里提到一张根本没传的参考图、只给了尾帧却写成"从这一帧开始运动" ——
// 这些在 text 模式下全都不报错,只是出来的视频不对,而且事后没法指认是哪一步
// 错了。改模板像在暗处调参:改完还是不对,但说不出哪里不对。
//
// IR 把这件事拆成两半:**模型只负责创作判断**(谁是主体、镜头怎么分、参考素材
// 保留什么),**结构正确性由代码保证**(时长对得上、引用的镜头号存在、素材角色
// 不越权)。于是错误第一次有了名字 —— TIMELINE_GAP_OR_OVERLAP、
// SUBJECT_APPEARANCE_SHOT_UNKNOWN —— 能被驳回、能被重修、能被计数。
//
// # 降级是三级的,不是两级
//
//	IR 编译成功            → 用渲染出来的提示词
//	IR 失败(修复后仍失败)  → **回落到 text 模式改写**
//	text 也失败            → 用客户的原始提示词
//
// 中间那级是关键。直接从 IR 掉到原始提示词,等于开了 IR 反而比开 text 更差 ——
// 那会让人不敢开它,而不开就永远收不到真实失败样本。

// irMaxRepairRounds 校验失败后的重修轮数。
//
// 1 轮,不是 3 轮:整个增强跑在 enhanceTimeout(20s)里,而且它卡在客户的生成
// 请求前面 —— 每多一轮就是所有人多等一次模型往返。一轮修不好的,多半是这次
// 输入本身让模型为难,再试两轮也是同样的错,不如尽快回落到 text。
const irMaxRepairRounds = 1

// irCompileTimeout IR 编译的**总**预算(两轮共用)的内置默认值。
// 聚合配置里写了 timeout_seconds 就以配置为准。
//
// # 这个数字是量出来的,不是拍的
//
// 同一份编译器提示词、同一个请求,各跑 5 次实测(秒):
//
//	qwen3.8-27b        64.9  69.0  72.5  83.8  92.0    5/5 通过
//	qwen3.8-flash-fp8  38.9  39.5  50.9  75.1  97.4    3/5 通过
//
// 单次编译的实测范围是 **34-102 秒** —— 一整份结构化 IR(镜头、主体、
// 保留分析、声音计划)输出天然就有几千 token,这是方案的固有成本。
//
// 预算要按**编译 + 一轮重修**来留:最坏情况约 200 秒。240 秒留了余量,
// 又不至于让一次彻底卡住的调用把客户吊到天荒地老。
//
// 早先这里是 20 秒(沿用 text 改写那个 enhanceTimeout),后果是 IR 这条路
// **一次都不可能成功** —— 每次超时、每次静默回落 text,看起来像"IR 没什么
// 效果",实际是一次都没跑成。
//
// # 两轮共用一份预算,不是各给一份
//
// 首轮花掉 100 秒时,重修轮只剩 140 秒,够用;首轮若异常地慢,重修就让它
// 超时回落 text。这是**故意**的:重修只在编译本来就顺的时候才划算,
// 不该为了让它一定跑完而把客户的等待无上限地翻倍。
//
// 做成 var 而不是 const:测试要能把它压到毫秒级,去验「IR 超时后 text
// 回落仍然跑得起来」—— 那条路上真正的坑是两级共用一个 ctx,而共用与否
// 在几分钟的预算下根本测不出来。
var irCompileTimeout = 240 * time.Second

// irBudget 这次编译的时间预算:配置优先,没配用内置默认。
func irBudget(cfg *common.AggregatePromptEnhance) time.Duration {
	if cfg != nil && cfg.TimeoutSeconds > 0 {
		return time.Duration(cfg.TimeoutSeconds) * time.Second
	}
	return irCompileTimeout
}

// irResult IR 编译的产物。
type irResult struct {
	Prompt string
	IR     *hilo.ContextIR
	Usage  *dto.Usage
	// Repaired 第一轮校验没过、靠重修才成功。运维看这个判断模板是不是该调了。
	Repaired bool
}

// compileViaIR 跑完整的 IR 编译:出 IR → 校验 → (重修) → 渲染。
//
// 返回 error 表示这条路没走通,调用方应回落到 text 模式。任何一步的失败都不该
// 让客户的生成请求出错。
func compileViaIR(ctx context.Context, authHeader, model string, in EnhanceInput) (*irResult, error) {
	if in.Compiler == nil {
		return nil, fmt.Errorf("缺少请求事实(CompilerInput),无法编译 IR")
	}
	sysPrompt := hilo.BuildCompilerPrompt(*in.Compiler)

	// 素材跟着第一轮发出去。IR 里那些"看着素材才能填"的字段
	// (asset_bindings 的 provides/excludes、reference_relationships 的保留模式)
	// 没有素材就只能编 —— 而编出来的 IR 结构完全合法,校验器抓不到。
	msgs := []map[string]any{
		{"role": "system", "content": sysPrompt},
		{"role": "user", "content": buildUserContent(in.Prompt, in.ImageURLs, in.VideoURLs, in.SendMedia)},
	}

	total := &dto.Usage{}
	var lastErr error

	for round := 0; round <= irMaxRepairRounds; round++ {
		body := map[string]any{
			"model":    model,
			"messages": msgs,
			"stream":   false,
			// 要 JSON 对象。上游不认这个字段时会忽略它(不是报错),所以
			// extractIR 仍然得自己扛住围栏和前后缀 —— 两道防线都要有。
			"response_format": map[string]any{"type": "json_object"},
		}
		applyThinking(body, in.Thinking)
		payload, err := common.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("构造 IR 编译请求失败: %w", err)
		}
		raw, usage, err := callEnhance(ctx, authHeader, payload)
		accumulateUsage(total, usage)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(raw) == "" {
			return nil, fmt.Errorf("IR 编译返回空内容")
		}

		ir, err := extractIR(raw)
		if err != nil {
			lastErr = err
		} else {
			// **请求事实一律盖回去,不采信模型填的那份。**
			//
			// task.type / duration / generate_audio 是从客户端传了哪个字段
			// 推出来的(见 hilo.ResolveFrameRoles),不是创作判断。模型把
			// l2va 写成 i2v,渲染器就会把尾帧当首帧 —— 视频从结尾往后长,
			// 不报错。校验器拦不住它:改完之后那份 IR 依然自洽。
			applyAuthoritativeFacts(ir, in.Compiler)

			report := hilo.ValidateIR(ir)
			if report.Passed() {
				prompt, err := hilo.RenderPrompt(ir)
				if err != nil {
					lastErr = fmt.Errorf("渲染失败: %w", err)
				} else if strings.TrimSpace(prompt) == "" {
					lastErr = fmt.Errorf("渲染出空提示词")
				} else {
					return &irResult{
						Prompt: prompt, IR: ir, Usage: total,
						Repaired: round > 0,
					}, nil
				}
			} else {
				// **必须带着类型传下去,不能在这里拍平成一句中文。**
				//
				// buildRepairInstruction 靠 errors.As 取出 ReportError 才能换成
				// 纯英文的具名代码。拍平之后类型没了,errors.As 失败,递给模型的
				// 就是这段面向运维的中文说明 —— 而最常见的一条问题恰恰是
				// PROMPT_REWRITE_LANGUAGE_VIOLATION:一边要求它"正文不要出现
				// 中文",一边把一整段中文摆在它面前。
				lastErr = &hilo.ReportError{Stage: "context-ir 校验", Report: report}
			}
		}

		if round == irMaxRepairRounds {
			break
		}
		// 重修:把它自己那份原样回放,再把**具名问题**递回去。
		//
		// 只说"格式不对"没有用 —— 模型会整篇重写,连本来对的创作判断一起换掉。
		// 给出 code + path 它才知道要动哪一处。
		msgs = append(msgs,
			map[string]any{"role": "assistant", "content": raw},
			map[string]any{"role": "user", "content": buildRepairInstruction(lastErr)},
		)
	}
	return nil, lastErr
}

// applyAuthoritativeFacts 用我们这边的事实覆盖 IR 里不该由模型决定的部分。
//
// **task 段**:玩法、时长、出不出声都是从客户端传了哪个字段推出来的
// (见 hilo.ResolveFrameRoles),不是创作判断。模型把 l2va 写成 i2v,
// 渲染器就会把尾帧当首帧 —— 视频从结尾往后长,不报错。
//
// **素材清单**:<Picture N> / <Video N> 的标号按它的顺序发。不盖回去的话
// 标号跟着模型的书写顺序走 —— 它先写 image_2 再写 image_1,<Picture 1>
// 就指向第二张图;漏写一个,后面全体前移一位。而素材是**按提交顺序**发给
// 模型和 H3 的,两边一错位,提示词指着的素材和它描述的不是同一个。
func applyAuthoritativeFacts(ir *hilo.ContextIR, in *hilo.CompilerInput) {
	if ir == nil || in == nil {
		return
	}
	ir.Task.Type = string(in.TaskType)
	ir.Task.DurationSeconds = in.DurationSeconds
	ir.Task.GenerateAudio = in.GenerateAudio

	ir.Assets = make([]hilo.IRAsset, 0, len(in.Assets))
	for _, a := range in.Assets {
		ir.Assets = append(ir.Assets, hilo.IRAsset{
			AssetID: a.AssetID, MediaType: a.MediaType, Role: a.Role,
		})
	}
}

// buildRepairInstruction 把校验问题写成一条重修指令。
func buildRepairInstruction(err error) string {
	reason := "unknown error"
	if err != nil {
		reason = err.Error()
	}
	// **递回去的必须是纯英文的具名代码。**
	//
	// 日志那份是中文(运维要读"哪条规则、为什么"),但它不能原样进指令:
	// 最常见的一条问题恰恰是 PROMPT_REWRITE_LANGUAGE_VIOLATION ——
	// 一边要求模型"正文不要出现中文",一边把中文说明摆在它面前,既示范了
	// 错误行为,也会让一部分模型跟着把输出语言切过去。
	var re *hilo.ReportError
	if errors.As(err, &re) {
		if mf := re.ModelFacing(); mf != "" {
			reason = mf
		}
	}
	// **整份跑偏要换一套说法。**
	//
	// 实测思考型模型会从用户消息出发重新推导任务,推到训练先验里最常见的
	// 那个形状去(prompt / negative_prompt / audio_prompt 那种"视频生成
	// 请求"),完全绕开系统提示词里的 schema。它自己的思考记录写着
	// "This is a video generation API?" —— 它是在猜。
	//
	// 这时说「只改被点名的、其余保持逐字不变,那些创作判断已被接受」是
	// **反作用**的:没有任何值得保留的东西,那句话恰恰是在让它守住错的形状。
	if strings.Contains(reason, "IR_SHAPE_UNRECOGNIZED") {
		return "Your previous output was not a Context-IR at all — you emitted a different JSON shape. " +
			"Ignore what you produced last time; none of it can be reused.\n\n" +
			"Re-read the \"Required shape\" block in the system prompt and emit **that** object: " +
			"same keys, same nesting. Return the complete JSON object, nothing else."
	}
	return "Your previous output was rejected by the deterministic validator:\n\n" +
		reason +
		"\n\nFix **only** what the report names. Keep every other field byte-identical — " +
		"the creative decisions in it were accepted. Return the complete corrected JSON object, nothing else."
}

// extractIR 从模型回复里取出 ContextIR。
//
// 模型很少只吐一个干净的 JSON:常见的是包在代码围栏里,或者前面先说一句
// "Here is the IR:"。要 response_format 也只是**大概率**能拿到干净的 ——
// 不是所有上游都认那个字段,不认的会**静默忽略**它。
func extractIR(raw string) (*hilo.ContextIR, error) {
	var lastErr error
	for _, cand := range irCandidates(raw) {
		var ir hilo.ContextIR
		if err := common.Unmarshal([]byte(cand), &ir); err != nil {
			lastErr = err
			continue
		}
		return &ir, nil
	}
	if lastErr != nil {
		return nil, fmt.Errorf("IR 解析失败: %w", lastErr)
	}
	return nil, fmt.Errorf("模型回复里找不到 JSON 对象")
}

// irCandidates 按可能性从高到低给出待解析的候选片段。
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
func irCandidates(raw string) []string {
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

// compileIRWithTimeout 给 IR 编译单独套上它自己的预算。
func compileIRWithTimeout(ctx context.Context, authHeader, model string, in EnhanceInput, budget time.Duration) (*irResult, error) {
	irCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	return compileViaIR(irCtx, authHeader, model, in)
}
