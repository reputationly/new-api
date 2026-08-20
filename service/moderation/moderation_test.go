package moderation

import (
	"context"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

func withWords(t *testing.T, words []string) {
	t.Helper()
	old := setting.SensitiveWords
	setting.SensitiveWords = words
	t.Cleanup(func() { setting.SensitiveWords = old })
}

func withMode(t *testing.T, mode system_setting.ModerationMode) {
	t.Helper()
	s := system_setting.GetModerationSettings()
	old := s.Mode
	s.Mode = mode
	t.Cleanup(func() { s.Mode = old })
}

func TestNormalizeEvasions(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"零宽穿插", "赌​博"},
		{"全角", "赌博"}, // NFKC 已在别处覆盖，这里保证正常文本不被破坏
		{"空格拆词", "赌 博"},
		{"换行拆词", "赌\n博"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			n := Normalize(c.in)
			if NormalizeCompact(n) != "赌博" {
				t.Errorf("compact(%q) = %q, want 赌博", c.in, NormalizeCompact(n))
			}
		})
	}
}

func TestNormalizeHomoglyph(t *testing.T) {
	// 西里尔 а / е 冒充拉丁字母
	if got := Normalize("gаmblе"); got != "gamble" {
		t.Errorf("Normalize = %q, want gamble", got)
	}
}

func TestKeywordBlocksInBlockingAndOffMode(t *testing.T) {
	withWords(t, []string{"赌博"})

	// mode=off 时 L0 仍然拦截：这是现网既有行为，不能被「审核默认关闭」连带关掉。
	for _, mode := range []system_setting.ModerationMode{
		system_setting.ModerationModeOff,
		system_setting.ModerationModeBlocking,
	} {
		withMode(t, mode)
		r := Moderate(context.Background(), &Request{Texts: []string{"我想赌 博"}})
		if !r.Blocked {
			t.Fatalf("mode=%s: 应当拦截，实际 action=%s", mode, r.Action)
		}
		if r.Provider != "L0" {
			t.Errorf("mode=%s: provider=%s, want L0", mode, r.Provider)
		}
		if r.Reason != "输入内容涉及敏感词" {
			t.Errorf("mode=%s: reason=%q", mode, r.Reason)
		}
	}
}

func TestObserveDoesNotBlock(t *testing.T) {
	withWords(t, []string{"赌博"})
	withMode(t, system_setting.ModerationModeObserve)

	r := Moderate(context.Background(), &Request{Texts: []string{"我想赌博"}})
	if r.Blocked {
		t.Fatal("observe 模式不应拦截")
	}
	// 判定本身照算，只是不拒——否则 observe 量不出误杀率。
	if r.Action != ActionBlock {
		t.Errorf("action=%s, want block", r.Action)
	}
	if r.Reason != "" {
		t.Errorf("不拦时不应产出拒绝文案，got %q", r.Reason)
	}
}

func TestCleanTextPasses(t *testing.T) {
	withWords(t, []string{"赌博"})
	withMode(t, system_setting.ModerationModeBlocking)

	r := Moderate(context.Background(), &Request{Texts: []string{"今天天气不错"}})
	if r.Blocked || r.Action != ActionPass {
		t.Fatalf("正常文本被拦: action=%s", r.Action)
	}
}

func TestKeywordDisabledSkipsL0(t *testing.T) {
	withWords(t, []string{"赌博"})
	withMode(t, system_setting.ModerationModeOff)

	s := system_setting.GetModerationSettings()
	old := s.KeywordEnabled
	s.KeywordEnabled = false
	t.Cleanup(func() { s.KeywordEnabled = old })

	if r := Moderate(context.Background(), &Request{Texts: []string{"我想赌博"}}); r.Blocked {
		t.Fatal("KeywordEnabled=false 时不应拦截")
	}
}

func TestHitWordsKeepConfiguredForm(t *testing.T) {
	// 命中词要回填成配置页里那一条原文，而不是归一化后的形态。
	// 否则运营拿着 words 回配置页搜「gambling」「赌博」都搜不到，也就答不上是哪条规则拦的。
	withWords(t, []string{"Gambling", "赌 博"})

	cases := []struct {
		text string
		want string
	}{
		{"I like GAMBLING a lot", "Gambling"},
		{"我想赌博", "赌 博"},
	}
	for _, c := range cases {
		v, err := keywordModerator{}.ModerateText(context.Background(), Normalize(c.text))
		if err != nil {
			t.Fatalf("ModerateText(%q): %v", c.text, err)
		}
		if v.Action != ActionBlock {
			t.Fatalf("%q 应当命中，实际 action=%s", c.text, v.Action)
		}
		if len(v.Words) != 1 || v.Words[0] != c.want {
			t.Errorf("%q: words=%v, want [%s]", c.text, v.Words, c.want)
		}
	}
}

func TestReasonTextNeverLeaksWords(t *testing.T) {
	// 未登记的类别不回显原始标识，退化成通用措辞。
	if got := ReasonText([]string{"some_unknown_category"}); got != "输入内容不符合内容规范" {
		t.Errorf("ReasonText = %q", got)
	}
	if got := ReasonText([]string{system_setting.CategorySexual, system_setting.CategoryPolitical}); got != "输入内容涉及色情内容、政治敏感内容" {
		t.Errorf("ReasonText = %q", got)
	}
}

// TestBlockWithoutKeyKeepsPreviewButNoCiphertext 密钥缺失时的降级行为。
//
// 放在这个包而不是 model 包：密钥读取走 sync.Once，一个进程只能验一种配置状态，
// 而 model 的测试进程为了验留存分档必须配上密钥。这里不设 MODERATION_ENCRYPT_KEY，
// 正好是「没配密钥」那一支的宿主。
//
// 关键是不能写进一段解不开的东西：那看起来是「存了」，事后才发现取不出来，
// 而那时原文已经没有第二份了。
func TestBlockWithoutKeyKeepsPreviewButNoCiphertext(t *testing.T) {
	if common.ModerationKeyReady() {
		t.Skip("本用例要求测试进程未配置 MODERATION_ENCRYPT_KEY")
	}

	m := &model.ModerationLog{Action: model.ModerationActionBlock, Enforced: true}
	m.SetModerationContent("违规内容", "违规内容")

	if m.ContentEnc != "" {
		t.Errorf("无密钥时 ContentEnc 必须留空，实际 %q", m.ContentEnc)
	}
	if m.Preview == "" {
		t.Error("无密钥不影响预览，运营至少还能看到前 160 字符")
	}
	if m.ContentHash == "" {
		t.Error("hash 不依赖密钥，任何情况下都该有")
	}
}

// TestActiveHonorsBothSwitches 送审闸门必须同时认两个开关。
//
// 上一版这里用的是 legacy 的 setting.ShouldCheckPromptSensitive() 当总闸，
// 结果运营把 mode 调成 blocking 之后同步链路一条记录都不产生，而任务链路照跑。
// 一个被显式配置的管控措施静默失效，是这套系统最不能出的错。
func TestActiveHonorsBothSwitches(t *testing.T) {
	s := system_setting.GetModerationSettings()
	oldMode, oldKeyword := s.Mode, s.KeywordEnabled
	oldLegacy := setting.CheckSensitiveEnabled
	t.Cleanup(func() {
		s.Mode, s.KeywordEnabled = oldMode, oldKeyword
		setting.CheckSensitiveEnabled = oldLegacy
	})

	cases := []struct {
		name    string
		mode    system_setting.ModerationMode
		keyword bool
		legacy  bool
		want    bool
	}{
		{"两个都关", system_setting.ModerationModeOff, true, false, false},
		{"legacy 关但 mode=blocking：必须审", system_setting.ModerationModeBlocking, true, false, true},
		{"legacy 关但 mode=observe：必须审", system_setting.ModerationModeObserve, true, false, true},
		{"legacy 开、mode=off：走既有关键词行为", system_setting.ModerationModeOff, true, true, true},
		{"关键词开关关且 mode=off", system_setting.ModerationModeOff, false, true, false},
	}
	for _, c := range cases {
		s.Mode, s.KeywordEnabled = c.mode, c.keyword
		setting.CheckSensitiveEnabled = c.legacy
		if got := Active("default", "gpt-4"); got != c.want {
			t.Errorf("%s: Active = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestKeywordScanIsBoundedOnHugeInput 超大输入下命中收集必须有上界。
//
// 收集模式会为**每一次出现**分配一个字符串。一个 2 字符的词在大请求里能重复
// 出现几十万次，单个请求就能把内存撑爆。改造前用的是 stopImmediately=true，
// 没有这个面，是本次改造顺手引入的。
func TestKeywordScanIsBoundedOnHugeInput(t *testing.T) {
	withWords(t, []string{"赌博"})

	// 远超 wordScanLimit 的输入，且命中词高频重复。
	huge := strings.Repeat("赌博", wordScanLimit)

	v, err := keywordModerator{}.ModerateText(context.Background(), Normalize(huge))
	if err != nil {
		t.Fatalf("ModerateText: %v", err)
	}
	if v.Action != ActionBlock {
		t.Fatalf("超长文本里的命中不该被漏掉，实际 action=%s", v.Action)
	}
	// 去重后只有一个词条；关键是收集阶段不能按出现次数无上界地分配。
	if len(v.Words) != 1 || v.Words[0] != "赌博" {
		t.Errorf("Words = %v, want [赌博]", v.Words)
	}
}

// TestKeywordHitBeyondScanLimitStillBlocks 命中点在收集窗口之外时不能漏判。
//
// 收集窗口是为了限制内存，不是为了限制检测范围。末尾的命中照样要拦，
// 并且至少要能归因到一个词条。
func TestKeywordHitBeyondScanLimitStillBlocks(t *testing.T) {
	withWords(t, []string{"赌博"})

	text := strings.Repeat("正", wordScanLimit+1000) + "赌博"

	v, err := keywordModerator{}.ModerateText(context.Background(), Normalize(text))
	if err != nil {
		t.Fatalf("ModerateText: %v", err)
	}
	if v.Action != ActionBlock {
		t.Fatalf("收集窗口之外的命中被漏判了，action=%s", v.Action)
	}
	if len(v.Words) != 1 || v.Words[0] != "赌博" {
		t.Errorf("Words = %v, want [赌博]（退回定性阶段拿到的词）", v.Words)
	}
}

// TestSearchWordsCollectionDoesNotGrowWithInput 收集阶段的分配量不随输入规模增长。
//
// 这条直接测 searchWords 而不是 ModerateText：后者的 dedupWords 会把结果收敛成
// 一两个词，最终 Words 看起来永远正常，而真正的问题发生在去重之前——
// 收集模式为每一次出现分配一个字符串，一个 2 字符的词在大请求里重复几十万次
// 就是几十万次分配。只看最终结果的断言对这个问题是瞎的。
func TestSearchWordsCollectionDoesNotGrowWithInput(t *testing.T) {
	dict := []string{"赌博"}
	small := strings.Repeat("赌博", wordScanLimit)
	large := strings.Repeat("赌博", wordScanLimit*4)

	_, wSmall := searchWords(small, dict)
	_, wLarge := searchWords(large, dict)

	if len(wSmall) == 0 || len(wLarge) == 0 {
		t.Fatal("两组输入都该命中")
	}
	if len(wLarge) != len(wSmall) {
		t.Errorf("收集量随输入增长（%d → %d）：收集阶段没有上界，"+
			"短词在大请求里重复出现就能把内存撑爆", len(wSmall), len(wLarge))
	}
	if len(wSmall) > wordScanLimit {
		t.Errorf("单次收集 %d 条，超过扫描窗口 %d", len(wSmall), wordScanLimit)
	}
}
