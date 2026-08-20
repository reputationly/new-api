package moderation

import (
	"context"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

// L0：进程内关键词层。见 docs/content-moderation-design.md §6.1。
//
// 这一层复用现网既有的 setting.SensitiveWords + AC 自动机，行为保持不变，
// 只是多了归一化预处理和 moderation_log 落库。
//
// 依赖方向：service/moderation → service（用 AcSearch）。service 不能反向依赖本包，
// 否则成环——这也是类别→中文的映射放在本包而不是 service 的原因。

type keywordModerator struct{}

func (keywordModerator) Name() string { return "L0" }

// wordScanLimit 收集命中词时实际扫描的最大 rune 数。
//
// 32K 字符足够回答「命中了哪几条规则」——这是 words 列存在的唯一目的，
// 而它本身也只有 varchar(512)，收再多也存不下。
const wordScanLimit = 32 * 1024

// searchWords 判定是否命中，并收集命中的词条，但收集有上界。
//
// 分两步是因为两个问题的代价差了几个数量级：
//
//	「有没有命中」用 stopImmediately，扫到第一个就停，对超长文本也廉价；
//	「命中了哪几条」必须收集模式，而收集模式会为**每一次出现**都分配一个字符串——
//	一个 2 字符的词在 1MB 请求里能重复出现几十万次，单个请求就能撑爆内存。
//	改造前的 service.CheckSensitiveText 用的是 stopImmediately=true，没有这个面，
//	是本次改造顺手引入的。
//
// 所以：定性扫全文（不削弱检测，末尾的命中照样拦），收集只扫前 wordScanLimit 个 rune。
// 命中点在 32K 之后时收集不到，退回定性阶段拿到的那一个词——总归有词可归因。
func searchWords(text string, dict []string) (bool, []string) {
	hit, first := service.AcSearch(text, dict, true)
	if !hit {
		return false, nil
	}
	head := text
	if runes := []rune(text); len(runes) > wordScanLimit {
		head = string(runes[:wordScanLimit])
	}
	if _, all := service.AcSearch(head, dict, false); len(all) > 0 {
		return true, all
	}
	return true, first
}

func (keywordModerator) ModerateText(_ context.Context, normalized string) (*Verdict, error) {
	dict, origin := normalizedDict(setting.SensitiveWords)
	if len(dict) == 0 {
		return nil, nil
	}

	// 两种形式都扫：折叠形式保留词间空格（贴近原文），压缩形式对付「敏 感 词」这类空格拆词。
	hit, words := searchWords(normalized, dict)
	compact := NormalizeCompact(normalized)
	if compact != normalized {
		if hit2, words2 := searchWords(compact, dict); hit2 {
			hit = true
			words = append(words, words2...)
		}
	}
	if !hit {
		return &Verdict{Action: ActionPass, Provider: "L0"}, nil
	}

	return &Verdict{
		Action: ActionBlock,
		// L0 没有类别概念，统一记为 keyword。这也意味着 §8.2 的「类别处置」对它不适用：
		// blocking 下关键词命中就是拒，想放宽只能改词库。
		Categories: []string{system_setting.CategoryKeyword},
		Provider:   "L0",
		Words:      originalWords(dedupWords(words), origin),
	}, nil
}

// originalWords 把 AC 命中的归一化词条映射回运营在配置页里实际填的那一条。
//
// 不做这步的话，配置里写的 "Gambling" 会记成 "gambling"、写的「赌 博」会记成「赌博」，
// 拿着记录回配置页里搜不到——而「是哪一条词拦的、要不要删」正是看这条记录的全部目的。
func originalWords(words []string, origin map[string]string) []string {
	out := make([]string, 0, len(words))
	for _, w := range words {
		if o, ok := origin[w]; ok {
			out = append(out, o)
			continue
		}
		out = append(out, w)
	}
	return out
}

// normalizedDict 把词库过一遍与待检文本相同的归一化。
//
// 不做这步会出现一种很难排查的失效：运营从别处粘贴词条时带进全角字符或同形字，
// 词条本身看着完全正常，但归一化后的文本永远匹配不上它——词库里躺着一条死规则。
// 第二个返回值是「归一化形态 → 配置原文」的映射，用于把命中回填成运营看得懂的那一条。
var (
	dictCacheMu     sync.RWMutex
	dictCacheKey    string
	dictCacheVal    []string
	dictCacheOrigin map[string]string
)

func normalizedDict(words []string) ([]string, map[string]string) {
	if len(words) == 0 {
		return nil, nil
	}
	key := strings.Join(words, "\n")

	dictCacheMu.RLock()
	if dictCacheKey == key {
		v, o := dictCacheVal, dictCacheOrigin
		dictCacheMu.RUnlock()
		return v, o
	}
	dictCacheMu.RUnlock()

	out := make([]string, 0, len(words))
	origin := make(map[string]string, len(words))
	for _, w := range words {
		// 词条本身也去空白：词库里的「敏 感 词」应当与压缩后的文本对齐。
		n := NormalizeCompact(Normalize(w))
		// 两条不同的配置归一化到同一形态时，先出现的那条胜出——与去重的取舍一致，
		// 反正它们对文本的匹配行为完全相同，回填哪条都不影响「删哪条能解开」的判断。
		if n == "" || origin[n] != "" {
			continue
		}
		origin[n] = w
		out = append(out, n)
	}

	dictCacheMu.Lock()
	dictCacheKey = key
	dictCacheVal = out
	dictCacheOrigin = origin
	dictCacheMu.Unlock()
	return out, origin
}

func dedupWords(words []string) []string {
	if len(words) <= 1 {
		return words
	}
	seen := make(map[string]bool, len(words))
	out := make([]string, 0, len(words))
	for _, w := range words {
		if seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}
	return out
}
