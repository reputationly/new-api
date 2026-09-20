package moderation

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

// Zhongsen-Text-8b 的解析。厂商文档《安全标签对照表》共 29 个码：1 个安全 + 28 个风险。

func TestZhongsenCodeTableIsComplete(t *testing.T) {
	// 少一个码的后果不是「这一类漏判」，是**判 ActionError**：Parse 认不出首行就
	// 留 Level 为空，配合默认开着的 FailOpen 就是静默放行。而漏的那一个恰恰
	// 只会在真有人发那类内容时才暴露。
	if len(zhongsenCodes) != 28 {
		t.Fatalf("风险码应为 28 个（29 个标签减去 sec），得到 %d 个", len(zhongsenCodes))
	}

	// 每个码映射到的类别都必须在 dialect 声明的覆盖集合里。
	// 对不上说明配置页的覆盖标注在说谎——那一类实际会被判出来，界面上却标着「不覆盖」。
	covered := system_setting.DialectCoveredCategories(system_setting.DialectZhongsenText)
	for code, cat := range zhongsenCodes {
		if !covered[cat] {
			t.Fatalf("码 %s 映射到 %s，但该类别没有登记在 zhongsen-text 的覆盖集合里", code, cat)
		}
	}
	// 反向：声明覆盖的类别必须真有码能产出它，否则是反过来的谎。
	produced := make(map[string]bool, len(zhongsenCodes))
	for _, cat := range zhongsenCodes {
		produced[cat] = true
	}
	for cat := range covered {
		if !produced[cat] {
			t.Fatalf("覆盖集合声明了 %s，但没有任何码会产出它", cat)
		}
	}
}

// TestZhongsenSafeCodeNotConfusedWithEthicsCode 这个 dialect 最隐蔽的失效方式。
//
// 29 个码里同时有 sec（安全）和 se（伦理违规），两者只差一个字符——安全与违规
// 在这里是一个字符的距离。任何「前缀匹配」「包含匹配」的解析写法都会在这一对上
// 翻车，而翻车的方向是**安全放行被读成违规**，或者反过来。
//
// 这条钉住解析侧必须精确匹配。注意它不依赖 max_tokens 取值：解析拿到什么字符串
// 就得判对什么，上游给的是完整码还是被截断的碎片，都不该让这一对混淆。
func TestZhongsenSafeCodeNotConfusedWithEthicsCode(t *testing.T) {
	d := zhongsenTextDialect{}

	if got := d.Parse("sec").Level; got != LevelSafe {
		t.Fatalf("sec 应判安全，得到 %q", got)
	}

	j := d.Parse("se")
	if j.Level != LevelUnsafe {
		t.Fatalf("se 应判违规，得到 %q", j.Level)
	}
	if len(j.Categories) != 1 || j.Categories[0] != system_setting.CategoryUnethical {
		t.Fatalf("se 应映射到 unethical，得到 %v", j.Categories)
	}
}

// TestZhongsenMaxTokensStaysOnFastPath max_tokens 不能大到让模型写出 <explanation>。
//
// 模型自带模板的 `# Instructions` 段是**无条件**的两条：先给类别 ID，下一行给
// <explanation> 理由。没有 safe/unsafe 分支 —— 判成 sec 的请求同样会接着写那段，
// 而且它**不会自己停**（finish_reason=length），给多少写多少，实测完整一段约
// 108 个 completion token。
//
// 而文本审核在每个请求的同步路径上、预扣费之前，99% 的流量是安全的：给足预算
// 等于给每一个正常请求都加一次百来 token 的解码。A100-40G 单副本、输入 500 字、
// 固定 20 QPS 实测，max_tokens 取 1 是 p50 38ms，取 16 就涨到 239ms，
// 而线上 qwen3guard 是 121ms —— 也就是说这个值一调大，换模型就从净改善变成净劣化。
//
// 这条拦的是「为了拿归因理由把它调上去」这类改动 —— 那是个要显式决策的取舍，
// 不该悄悄变成默认。
//
// 注：这里只钉上界。**下界不设**，因为 29 个码在本模型词表里全部是单 token
// （已实测），max_tokens=1 就能拿到完整类别码；曾经有一条按「一个 token 至少
// 一个字符」推出下界的用例，那个前提对 BPE 不成立，已删。理由见 MaxTokens 注释。
func TestZhongsenMaxTokensStaysOnFastPath(t *testing.T) {
	got := (zhongsenTextDialect{}).MaxTokens()
	if got > 32 {
		t.Fatalf("max_tokens=%d 会让模型把 <explanation> 写出来（约 108 token），"+
			"而它对每个安全请求也照写——这是加在全站同步路径上的时延", got)
	}
}

// TestZhongsenPinsOutputOrder 输出顺序必须是显式契约，不能靠未定义变量。
//
// 模板里 `{% if reason_first %}` 理由在前、`{% else %}` 类别码在前。不传时 Jinja
// 把未定义变量当假值，正好走 else —— 也就是说解析器的正确性挂在一个默认行为上。
// 顺序一反，首行变成英文理由，精确匹配全部落空 → 每次 ActionError →
// FailOpen 默认开着 → 静默全量放行，且没有任何报错。
func TestZhongsenPinsOutputOrder(t *testing.T) {
	kw := (zhongsenTextDialect{}).ChatTemplateKwargs()
	v, ok := kw["reason_first"]
	if !ok {
		t.Fatal("必须显式传 reason_first，不能依赖模板里未定义变量的默认假值")
	}
	if b, isBool := v.(bool); !isBool || b {
		t.Fatalf("reason_first 必须是 false（类别码在首行），得到 %#v", v)
	}
}

// TestChatTemplateKwargsOnTheWire 断言**marshal 之后的请求体**，而不是 dialect 的返回值。
//
// 这两件事都是对线路字节的承诺，代理断言（比如只检查返回值是不是 nil）测不到：
//
//  1. qwen3guard 的请求体里不能出现 chat_template_kwargs —— 它是线上正在跑的那条路，
//     多一个字段就是一次无谓的变更；
//  2. zhongsen 的 reason_first **必须真的带着 false 发出去**。这一条是 Rule 6 那个
//     陷阱的反面：`false` 之所以没被 omitempty 吞掉，靠的是它在 map 的**值**里
//     （omitempty 只作用于 map 本身）。哪天有人把 ChatTemplateKwargs 换成一个带
//     omitempty 的结构体字段，`false` 就会静默消失，输出顺序的保护随之失效，
//     而返回值层面的断言完全看不出来。
//
// 顺带覆盖了 common.Marshal 这一层：将来真换掉 JSON 实现而 omitempty 语义有差异时，
// 这里会红——那正是 Rule 1 要求统一走 common 包装的理由。
func TestChatTemplateKwargsOnTheWire(t *testing.T) {
	marshal := func(t *testing.T, d textDialect) string {
		t.Helper()
		b, err := common.Marshal(chatRequest{
			Model:              "m",
			Messages:           []chatMessage{{Role: "user", Content: "x"}},
			MaxTokens:          d.MaxTokens(),
			Temperature:        0,
			ChatTemplateKwargs: d.ChatTemplateKwargs(),
		})
		if err != nil {
			t.Fatalf("marshal 失败: %v", err)
		}
		return string(b)
	}

	// qwen3guard：字段必须整个不出现。
	//
	// 注意不能只断言「不含 reason_first」——那样返回一个空 map 也会通过，
	// 而要守的是「这个键一个字都不许出现」。
	if got := marshal(t, qwen3GuardDialect{}); strings.Contains(got, "chat_template_kwargs") {
		t.Fatalf("qwen3guard 的请求体不该出现 chat_template_kwargs：%s", got)
	}

	// zhongsen：必须带着 false 出现。
	got := marshal(t, zhongsenTextDialect{})
	if !strings.Contains(got, `"chat_template_kwargs":{"reason_first":false}`) {
		t.Fatalf("zhongsen 必须显式发 reason_first=false，否则首行顺序的保护是空的：%s", got)
	}
}

func TestZhongsenParseEachCode(t *testing.T) {
	d := zhongsenTextDialect{}
	for code, wantCat := range zhongsenCodes {
		j := d.Parse(code + "\n")
		if j.Level != LevelUnsafe {
			t.Fatalf("码 %s 应判违规，得到 %q", code, j.Level)
		}
		if len(j.Categories) != 1 || j.Categories[0] != wantCat {
			t.Fatalf("码 %s 应映射到 %s，得到 %v", code, wantCat, j.Categories)
		}
	}
}

func TestZhongsenParseUnrecognized(t *testing.T) {
	d := zhongsenTextDialect{}

	// 认不出来的首行必须留 Level 为空 → 上层判 ActionError。
	//
	// **刻意不落 CategoryUnknownUpstream**：那个类别的语义是「模型报了个我们没登记
	// 的风险类型」，前提是模型确实在判定；而首行对不上码表说明输出格式本身不对
	// （多半部署的模型和配的 dialect 对不上），整条输出都不可信。
	for _, content := range []string{
		"",
		"   \n  ",
		"I cannot help with that",
		"Safety: Unsafe\nCategories: Violent", // qwen3guard 的输出喂给 zhongsen 解析器
		"unsafe",                              // ZSWS 的首行喂给 zhongsen 解析器
		"secure",                              // sec 的前缀扩展，不能被当成 sec
	} {
		j := d.Parse(content)
		if j.Level != "" {
			t.Fatalf("无法识别的输出 %q 应留 Level 为空，得到 %q", content, j.Level)
		}
		if len(j.Categories) != 0 {
			t.Fatalf("无法识别的输出 %q 不应带类别，得到 %v", content, j.Categories)
		}
	}
}

// TestZhongsenOnlyFirstLineDecides 解析只看首行，绝不对整段输出做包含匹配。
//
// explanation 的英文正文里随时会出现 security / section 这样含 "sec" 的词，
// 包含匹配会把一条违规判定读成安全——这是个会稳定复现的漏放。
func TestZhongsenOnlyFirstLineDecides(t *testing.T) {
	content := "dw\n<explanation>\nThis is a security-sensitive section about sec.\n</explanation>"
	j := zhongsenTextDialect{}.Parse(content)
	if j.Level != LevelUnsafe {
		t.Fatalf("首行是 dw，应判违规，得到 %q", j.Level)
	}
	if len(j.Categories) != 1 || j.Categories[0] != system_setting.CategoryIllegal {
		t.Fatalf("dw 应映射到 illegal，得到 %v", j.Categories)
	}
}

func TestZhongsenExtractExplanation(t *testing.T) {
	d := zhongsenTextDialect{}

	// 正常闭合
	j := d.Parse("dw\n<explanation>\nThe input requests bomb-making instructions.\n</explanation>")
	if j.Reason != "The input requests bomb-making instructions." {
		t.Fatalf("理由提取有误：%q", j.Reason)
	}

	// 闭合标签缺失（被 max_tokens 截断）：**返回空，不要碎片**。
	//
	// 默认 MaxTokens=1 根本走不到这里（整段输出就是一个类别码），这条覆盖的是
	// 把 MaxTokens 调大、但又不够写完整一段理由的中间值：截断点落在 <explanation>
	// 里面，「取到结尾」会把 "The" 这种一两个词的碎片存进 detail。它读不懂又长得
	// **像**一条理由，比空着更有害。
	j = d.Parse("dw\n<explanation>\nThe input requests bomb-making")
	if j.Reason != "" {
		t.Fatalf("理由不完整时应返回空而不是碎片，得到 %q", j.Reason)
	}
	// 判定本身不受影响——理由是附加信息，拿不到不能影响拦不拦。
	if j.Level != LevelUnsafe {
		t.Fatalf("理由被截断不应影响判定，得到 %q", j.Level)
	}

	// 没有 explanation（极速模式）：理由为空，判定照常。
	j = d.Parse("dw")
	if j.Reason != "" {
		t.Fatalf("没有 explanation 时理由应为空，得到 %q", j.Reason)
	}
	if j.Level != LevelUnsafe {
		t.Fatal("没有 explanation 不影响判定")
	}

	// 超长理由按 rune 截断，不切出半个 UTF-8 字符。
	long := "dw\n<explanation>" + strings.Repeat("危", zhongsenReasonLimit+500) + "</explanation>"
	j = d.Parse(long)
	if n := len([]rune(j.Reason)); n != zhongsenReasonLimit {
		t.Fatalf("超长理由应截到 %d 个 rune，得到 %d", zhongsenReasonLimit, n)
	}
	if !strings.HasSuffix(j.Reason, "危") {
		t.Fatal("按 rune 截断不应切出半个字符")
	}
}

// TestZhongsenVerdictThroughSharedLogic dialect 解析出的判定要能走通共用的类别处置。
//
// 钉的是抽象边界真的接上了：strictness × 类别处置那段逻辑写在 l1_text.go 里、
// 对所有 dialect 共用一份，而不是每个 dialect 各自实现一套。
func TestZhongsenVerdictThroughSharedLogic(t *testing.T) {
	m := textModerator{
		dialect:    zhongsenTextDialect{},
		strictness: system_setting.StrictnessStandard,
		policy: &system_setting.ModerationPolicy{
			Name: "标准",
			Categories: map[string]string{
				system_setting.CategoryIllegal: system_setting.CategoryActionBlock,
				system_setting.CategoryCyber:   system_setting.CategoryActionLog,
				system_setting.CategoryAdvice:  system_setting.CategoryActionIgnore,
			},
		},
	}

	if v := m.parseVerdict("sec"); v.Action != ActionPass {
		t.Fatalf("sec 应判 pass，实际 %s", v.Action)
	}
	// dw → illegal → block
	if v := m.parseVerdict("dw"); v.Action != ActionBlock {
		t.Fatalf("illegal 配的是 block，实际 %s", v.Action)
	}
	// ha → cyber → log → 放行但留全量记录
	if v := m.parseVerdict("ha"); v.Action != ActionReview {
		t.Fatalf("cyber 配的是 log，应判 review，实际 %s", v.Action)
	}
	// fin → advice → ignore → 放行，但**类别照留**（与 L2 的 ignore 口径一致）
	v := m.parseVerdict("fin")
	if v.Action != ActionPass {
		t.Fatalf("advice 配的是 ignore，应放行，实际 %s", v.Action)
	}
	if len(v.Categories) != 1 || v.Categories[0] != system_setting.CategoryAdvice {
		t.Fatalf("ignore 时类别必须保留，否则类别命中统计按 dialect 分裂，得到 %v", v.Categories)
	}

	// 无法识别 → ActionError，且 Detail 要带上原始输出与 dialect 名：
	// 这个分支最常见的成因是节点上的模型和配的 dialect 对不上。
	v = m.parseVerdict("Safety: Safe")
	if v.Action != ActionError {
		t.Fatalf("无法识别的输出应判 error，实际 %s", v.Action)
	}
	if !strings.Contains(v.Detail, system_setting.DialectZhongsenText) {
		t.Fatalf("error 的 Detail 应带 dialect 名以便定位配错，得到 %q", v.Detail)
	}

	// 归因理由要落到 Verdict 上（再由 buildDetail 进 detail 列给复核用）。
	v = m.parseVerdict("dw\n<explanation>bomb-making</explanation>")
	if v.Reason != "bomb-making" {
		t.Fatalf("归因理由应落到 Verdict.Reason，得到 %q", v.Reason)
	}
	if v.Dialect != system_setting.DialectZhongsenText {
		t.Fatalf("Verdict.Dialect 应为 zhongsen-text，得到 %q", v.Dialect)
	}
}

// TestZhongsenStrictnessHasNoEffect 这个 dialect 是二分的，严格度对它无效。
//
// 钉住这件事是因为配置页上「严格度」看起来是个全局松紧旋钮。换到 zhongsen 之后
// 它对判定毫无影响——运营调了以为生效其实没有，而那正是这套系统最不能出的错。
func TestZhongsenStrictnessHasNoEffect(t *testing.T) {
	policy := &system_setting.ModerationPolicy{
		Categories: map[string]string{system_setting.CategoryIllegal: system_setting.CategoryActionBlock},
	}
	for _, s := range []string{
		system_setting.StrictnessLoose,
		system_setting.StrictnessStandard,
		system_setting.StrictnessStrict,
	} {
		m := textModerator{dialect: zhongsenTextDialect{}, strictness: s, policy: policy}
		if v := m.parseVerdict("sec"); v.Action != ActionPass {
			t.Fatalf("严格度 %s 下 sec 仍应 pass，实际 %s", s, v.Action)
		}
		if v := m.parseVerdict("dw"); v.Action != ActionBlock {
			t.Fatalf("严格度 %s 下 dw 仍应 block，实际 %s", s, v.Action)
		}
	}
}

// TestSelectableDialectsAreImplemented 可选的 dialect 必须真有解析实现。
//
// 这条守的是一个具体的翻车：ZSWS 的常量、覆盖表、前端下拉框都先就位了，
// 而图片侧的 dialect 分派要到第二步才有。中间那段时间它**可以被选中**，
// 后果是给 ZSWS 发 ShieldGemma 的请求 → 每次 ActionError → FailOpen 默认 true
// → 图片审核静默停摆，而「测试连接」走的还是 ShieldGemma 协议，照样报绿。
//
// 所以 Text/ImageDialects 的语义必须是「已实现」而不是「已认识」。
// 第二步加 imageDialect 分派时，把 implementedImage 一起改。
func TestSelectableDialectsAreImplemented(t *testing.T) {
	for _, name := range system_setting.TextDialects {
		if _, ok := textDialects[name]; !ok {
			t.Fatalf("文本协议 %s 可选但没有解析实现；"+
				"选中它会让每次判定都 ActionError，而 FailOpen 默认开着（静默放行）", name)
		}
	}

	// 图片侧还没有注册表，实现集合先写在这里。
	implementedImage := map[string]bool{
		system_setting.DialectShieldGemma2: true,
	}
	for _, name := range system_setting.ImageDialects {
		if !implementedImage[name] {
			t.Fatalf("图片协议 %s 可选但没有解析实现（media.go 写死 shieldGemmaModerator）；"+
				"选中它会让图片审核静默停摆", name)
		}
	}

	// 反面：常量与覆盖表可以先于实现存在（第二步要用），只是不能进可选列表。
	if system_setting.DialectCoveredCategories(system_setting.DialectZSWS) == nil {
		t.Fatal("ZSWS 的覆盖表应当保留，第二步实现时要用")
	}
	for _, name := range system_setting.ImageDialects {
		if name == system_setting.DialectZSWS {
			t.Fatal("ZSWS 还没有实现，不能出现在可选列表里")
		}
	}
}

// TestResolveTextDialect 未知 dialect 名回落到 qwen3guard 而不是 panic 或 nil。
func TestResolveTextDialect(t *testing.T) {
	if got := resolveTextDialect(system_setting.DialectZhongsenText).Name(); got != system_setting.DialectZhongsenText {
		t.Fatalf("应解析到 zhongsen-text，得到 %s", got)
	}
	if got := resolveTextDialect("").Name(); got != system_setting.DialectQwen3Guard {
		t.Fatalf("空值应回落到 qwen3guard，得到 %s", got)
	}
	if got := resolveTextDialect("nonexistent").Name(); got != system_setting.DialectQwen3Guard {
		t.Fatalf("未知值应回落到 qwen3guard，得到 %s", got)
	}
}

// TestDialectInputLimitsMatchWindows 各 dialect 的分段兜底必须与模型窗口相称。
//
// Zhongsen 的部署参数是 --max-model-len 4096，而内置判定模板本身占约 317 token。
// 照搬 qwen3guard 按 8192 窗口定的 4000 rune 会直接打穿窗口，vLLM 返回 400——
// 而 400 是「不冻结」的（freezeDurationForHTTPStatus），于是每个长输入都会
// 在所有节点上各失败一次，然后 fail-open 静默放行。
func TestDialectInputLimitsMatchWindows(t *testing.T) {
	if got := (zhongsenTextDialect{}).DefaultInputLimit(); got > 2000 {
		t.Fatalf("zhongsen 的分段兜底 %d 对 4096 窗口过大（模板另占约 317 token）", got)
	}
	if got := (qwen3GuardDialect{}).DefaultInputLimit(); got != 4000 {
		t.Fatalf("qwen3guard 的分段兜底应保持 4000 不变（存量行为），得到 %d", got)
	}
}
