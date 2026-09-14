package moderation

import (
	"strings"
	"testing"

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
// 29 个码里同时有 sec（安全）和 se（伦理违规），两者只差一个字符。
// 厂商文档推荐 max_tokens=1 的「极速模式」——如果 "sec" 在模型词表里不是单 token，
// 截断的结果就是 "se"，于是一次**安全放行被读成伦理类违规**。
// 这条用例钉住解析侧不做前缀匹配；MaxTokens 那一侧由下面那条钉。
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

// TestZhongsenMaxTokensCannotTruncateFirstLine max_tokens 不能小到可能截断首行。
//
// 这条不是在测某个具体数字，是在拦一类改动：有人读了厂商文档的「极速模式」
// 把 MaxTokens 调成 1 或 2 来降时延。最长的码是 3 个字符（sec/def/sci/ter/ext/acc/fin/med/law），
// 而 sec→se 这一截就是安全与违规的反转。
func TestZhongsenMaxTokensCannotTruncateFirstLine(t *testing.T) {
	longest := 0
	for code := range zhongsenCodes {
		if len(code) > longest {
			longest = len(code)
		}
	}
	if len(zhongsenSafeCode) > longest {
		longest = len(zhongsenSafeCode)
	}
	// 一个 token 最少一个字符，所以 max_tokens 至少要够最长的码，
	// 再加一个换行/结束符的余量。
	got := (zhongsenTextDialect{}).MaxTokens()
	if got < longest+1 {
		t.Fatalf("max_tokens=%d 可能截断首行（最长码 %d 字符）；"+
			"截断 sec 会得到 se，把安全放行读成伦理违规", got, longest)
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

	// 闭合标签缺失（被 max_tokens 截断）：取到结尾。
	// 半段理由对复核仍然有用，比丢掉强。
	j = d.Parse("dw\n<explanation>\nThe input requests bomb-making")
	if j.Reason != "The input requests bomb-making" {
		t.Fatalf("截断的理由应取到结尾，得到 %q", j.Reason)
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
