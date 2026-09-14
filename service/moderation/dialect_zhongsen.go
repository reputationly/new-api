package moderation

import (
	"strings"

	"github.com/QuantumNous/new-api/setting/system_setting"
)

// zhongsen-text dialect：众森卫士 Zhongsen-Text-8b（基于 Qwen3 微调的归因驱动安全模型）。
//
// 输出形状与 Qwen3Guard 完全不同——首行是一个缩写码，随后可选一段包在
// <explanation> 里的审计理由：
//
//	dw
//	<explanation>
//	The input text requests information on how to make a high-explosive bomb...
//	</explanation>
//
// 与 qwen3guard 的两个结构性差异，直接决定了上层能用哪些旋钮：
//
//  1. **没有 Controversial 这一档**。判定是二分的（sec vs 28 个风险码），
//     所以 strictness 对这个 dialect 完全无效，松紧只能靠类别处置表调。
//  2. **能给归因理由，但默认不取**。<explanation> 对每个请求（包括判成 sec 的）
//     都会写，约 108 个 completion token，而这条路在全站同步路径上——
//     所以 MaxTokens 压在 16，理由拿不到。取舍的完整说明见 MaxTokens。
//
// 本文件的所有判断都对着模型自带的 chat template 核过
// （tokenizer_config.json 里的 chat_template 字段），不是照文档写的。

// zhongsenCodes 首词缩写码 → 本项目类别。取自厂商文档《安全标签对照表》（S-Eval 口径）。
//
// 映射到哪一类不是随手选的，几处刻意的取舍：
//
//   - acc / mc / ha / ps 走 CategoryCyber 而不是 illegal：落进 illegal（默认 block）
//     会把「这段 SQL 注入怎么修」这类 coding 常态提问一起拦掉。
//   - fin / med / law 走 CategoryAdvice（默认 ignore）：这一类的边界极模糊，
//     命中的绝大多数是正常咨询。
//   - ter 走 CategoryTerror 而不是 violent：暴恐是备案红线，而 violent 默认只 log。
//   - cm / ma / md 走 CategoryMinor：ma（教唆虐待、剥削未成年人）是高危红线，
//     压进 unethical（默认 log）等于只记录不拦。
//   - ph / mh 走 CategorySelfHarm 是**有偏差的**：原类别是「身心健康」，涵盖
//     「危害身体健康的危险活动」和「宣扬焦虑、重度负面心理暗示」，比自伤自杀宽。
//     没有更贴的类别，按类别配处置时要知道这一点。
//   - cs（商业机密）走 CategoryPII 同样有偏差：PII 的中文措辞是「个人隐私信息」，
//     而商业机密不是个人信息。合并是因为两者的处置诉求一致（默认都不拦）。
var zhongsenCodes = map[string]string{
	// 违法犯罪
	"pc": system_setting.CategorySexual,  // 色情及违禁品交易、宣传与制作
	"dc": system_setting.CategoryIllegal, // 毒品买卖、运输及吸食、制造教程
	"dw": system_setting.CategoryIllegal, // 危险管制枪支、炸弹、军刀
	"pi": system_setting.CategoryIllegal, // 盗窃、抢劫、侵犯他人财产
	"ec": system_setting.CategoryIllegal, // 洗钱、走私、偷税漏税
	// 仇恨谩骂
	"ac":  system_setting.CategoryUnethical, // 脏话、粗俗侮辱性词汇
	"def": system_setting.CategoryUnethical, // 针对在世特定自然人的造谣诽谤
	"ti":  system_setting.CategoryViolent,   // 暴力威胁、人身恐吓、言语霸凌
	"cy":  system_setting.CategoryUnethical, // 网络暴力、羞辱
	// 身心健康
	"ph": system_setting.CategorySelfHarm, // 危害身体健康的危险活动
	"mh": system_setting.CategorySelfHarm, // 宣扬焦虑、重度负面心理暗示
	// 伦理道德
	"se":  system_setting.CategoryUnethical, // 违背公序良俗、社会公德、家庭伦理
	"sci": system_setting.CategoryUnethical, // 违背科研伦理、基因编辑、人体实验
	// 隐私泄露
	"pp": system_setting.CategoryPII, // 非法获取、公开他人身份证、手机号、行踪
	"cs": system_setting.CategoryPII, // 泄露未公开的商业机密、内幕
	// 网络安全
	"acc": system_setting.CategoryCyber, // 越权访问、破解密码、绕过身份鉴权
	"mc":  system_setting.CategoryCyber, // 钓鱼代码、木马病毒、勒索软件
	"ha":  system_setting.CategoryCyber, // DDoS、SQL 注入、漏洞渗透
	"ps":  system_setting.CategoryCyber, // 对工控、民生电网等物理基础设施的攻击
	// 暴恐极端
	"ter": system_setting.CategoryTerror,    // 极端恐怖主义、宗教极端活动
	"sd":  system_setting.CategoryPolitical, // 散布虚假谣言、煽动社会骚乱、危害国家安全
	"ext": system_setting.CategoryPolitical, // 宣扬极端法西斯、种族优越主义、排外思潮
	// 违规建议
	"fin": system_setting.CategoryAdvice, // 违规提供证券投资、财务诊断建议
	"med": system_setting.CategoryAdvice, // 非法提供诊断、处方、网售管制药品渠道
	"law": system_setting.CategoryAdvice, // 提供规避法律制裁的非法诉讼建议
	// 未成年保护
	"cm": system_setting.CategoryMinor, // 诱导未成年人不良嗜好
	"ma": system_setting.CategoryMinor, // 宣扬、教唆虐待、剥削未成年人（高危）
	"md": system_setting.CategoryMinor, // 诱导未成年人旷课、打架、离家出走
}

// zhongsenSafeCode 判定安全的缩写码。
const zhongsenSafeCode = "sec"

// zhongsenReasonLimit Reason 落库的字符上限。
//
// detail 列的设计前提是「体量恒定」（model/moderation_log.go:84）。实测 explanation
// 约 108 token、几百字符，给 1000 字符足够放完整一段，又不会让某次异常长的输出
// 把这一列撑成变长大字段。
const zhongsenReasonLimit = 1000

type zhongsenTextDialect struct{}

func (zhongsenTextDialect) Name() string { return system_setting.DialectZhongsenText }

// MaxTokens 16。两个方向都有约束，这个值是夹出来的。
//
// **下界：绝不能按厂商文档的「极速模式」设成 1。** 29 个码里同时有 sec（安全）和
// se（伦理违规），而 max_tokens=1 只保证拿到一个 token——如果 "sec" 在这个模型的
// 词表里不是单 token，截断的结果就是 "se"，于是一次**安全放行被读成伦理类违规**。
// 安全与违规在这里只差一个字符，是这个 dialect 最隐蔽的失效方式。
//
// **上界：不能大到让它把 <explanation> 写出来。** 读模型自带的 chat template
// 可以确认（tokenizer_config.json 里的 chat_template，`# Instructions` 段）：
//
//   - Identify the single most relevant category ID for the input text.
//   - On the next line, provide a concise justification ... <explanation> ...
//
// 这两条指令是**无条件**的，没有 safe/unsafe 分支——判成 sec 的请求同样会接着
// 写那段理由（实测约 108 个 completion token）。而文本审核在每个请求的同步路径上、
// 预扣费之前，99% 的流量都是安全的：给足预算等于**给每一个正常请求都加上一次
// 百来 token 的解码**。对比 qwen3guard 实测输出只有 8–9 token、P99 415ms，
// 那是五倍以上的时延回归。
//
// 代价是拿不到 <explanation>，Verdict.Reason 对这个 dialect 恒为空。想要归因理由
// 就把这个值调到 256 左右——那是一个「用全站时延换复核信息」的取舍，不该是默认。
// 调之前先确认 reason_first 仍是 false（见 ChatTemplateKwargs），否则首行会变成
// 理由，而调小这个值就直接把判定截没了。
func (zhongsenTextDialect) MaxTokens() int { return 16 }

// ChatTemplateKwargs 显式钉住输出顺序。
//
// 模板里那段是 `{% if reason_first %}` 理由在前、`{% else %}` 类别码在前。
// 我们不传时 Jinja 把未定义变量当假值，走的正是 else 分支——也就是说现在的解析
// （firstNonEmptyLine 精确匹配码表）是**靠一个未定义变量的默认行为**成立的。
//
// 显式传 false 把它变成契约：万一模板把默认翻过去，首行就成了英文理由，精确匹配
// 全部落空 → 每次判定都 ActionError → FailOpen 默认开着 → 静默全量放行。
// 这种失效没有任何报错，而挡住它的代价只是多发一个字段。
//
// vLLM 是 apply_chat_template(messages, **chat_template_kwargs)，所以这里的键
// 到模板里是顶层变量，正好对上 `{% if reason_first %}`。
func (zhongsenTextDialect) ChatTemplateKwargs() map[string]any {
	return map[string]any{"reason_first": false}
}

// DefaultInputLimit 1500 rune。
//
// 比 qwen3guard 的 4000 小得多，因为厂商给的部署参数是 --max-model-len 4096，
// 而内置判定模板本身就占约 317 token。4000 个汉字必然打穿窗口，vLLM 直接返回 400。
// 1500 rune 在 4096 窗口下留足了模板与输出的余量；GPUStack 上把 max-model-len
// 开得更大时，按节点的 input_limit 覆盖它即可。
func (zhongsenTextDialect) DefaultInputLimit() int { return 1500 }

func (zhongsenTextDialect) ProbeText() string { return "今天天气怎么样" }

// Parse 解析首词缩写码与审计理由。
func (zhongsenTextDialect) Parse(content string) textJudgement {
	var j textJudgement
	j.Reason = extractExplanation(content)

	code := strings.ToLower(firstNonEmptyLine(content))
	switch {
	case code == "":
		// 空输出。Level 留空 → ActionError。
	case code == zhongsenSafeCode:
		j.Level = LevelSafe
	default:
		if cat, ok := zhongsenCodes[code]; ok {
			j.Level = LevelUnsafe
			j.Categories = []string{cat}
		}
		// 认不出来的码：Level 留空 → ActionError。
		//
		// **这里刻意不走 CategoryUnknownUpstream。** 那个类别的语义是「模型报了个
		// 我们没登记的风险类型」，前提是模型确实在做判定；而首行对不上码表说明的是
		// 输出格式本身不对——多半部署的不是这个 dialect 对应的模型，或者被别的
		// max_tokens 截断了。那种情况下整条输出都不可信，判 error 交给 fail 策略，
		// 而不是当成一次「未知类别的违规」。
	}
	return j
}

// firstNonEmptyLine 取首个非空行（已 TrimSpace）。
//
// 必须只看首行且**精确匹配**，不能对整段输出做包含匹配：explanation 的英文正文里
// 随时会出现 "sec"（security / section）这样的子串，包含匹配会把一条违规判定读成安全。
func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

// extractExplanation 取出 <explanation> 里的审计理由。
//
// **闭合标签缺失时返回空，而不是取到结尾。**
//
// 早先这里是「取到结尾」，理由写的是「半段理由对复核仍然有用」。那个判断在
// max_tokens=16 的默认下是错的：那个预算刚够首行的类别码，输出必然止于
// `<explanation>` 刚开头几个字，于是这一列存进去的是 "The" / "输入" 这种
// 一两个词的碎片。复核看到它既读不懂也不能据它判断，而它长得**像**一条理由——
// 比空着更有害。要完整理由就得把 MaxTokens 调上去，那时闭合标签自然在。
func extractExplanation(content string) string {
	const open = "<explanation>"
	const close = "</explanation>"
	start := strings.Index(content, open)
	if start < 0 {
		return ""
	}
	body := content[start+len(open):]
	end := strings.Index(body, close)
	if end < 0 {
		return ""
	}
	body = strings.TrimSpace(body[:end])
	if len(body) > zhongsenReasonLimit {
		// 按 rune 截，避免切出半个 UTF-8 字符（理由可能是中文）。
		r := []rune(body)
		if len(r) > zhongsenReasonLimit {
			r = r[:zhongsenReasonLimit]
		}
		body = string(r)
	}
	return body
}
