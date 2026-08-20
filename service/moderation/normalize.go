package moderation

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// 文本归一化。见 docs/content-moderation-design.md §6.2。
//
// 分工：模型防语义，归一化防编码层面的花招。这一层同时喂给 L0 和 L1，
// 也让 ContentHash 更稳定（同一内容换个零宽字符不该算成新内容）。
//
// 重要：归一化会改变字符偏移量，因此**只用于检测，不能用于替换**。
// service.SensitiveWordReplace 依赖原文偏移写回，绝不能改喂归一化后的文本。

// zeroWidth 需要剥离的零宽/格式类字符。
// 敏感词中间插一个 U+200B 就能穿透词库，而肉眼完全看不出来。
// 必须写成转义而不是字面量：这些字符在源码里同样是隐形的，
// 字面量既没法 review 也容易被编辑器吃掉，U+FEFF 更会直接被 Go 词法器判为非法 BOM。
var zeroWidth = map[rune]bool{
	'\u200b': true, // ZERO WIDTH SPACE
	'\u200c': true, // ZERO WIDTH NON-JOINER
	'\u200d': true, // ZERO WIDTH JOINER
	'\u200e': true, // LEFT-TO-RIGHT MARK
	'\u200f': true, // RIGHT-TO-LEFT MARK
	'\u2060': true, // WORD JOINER
	'\u2061': true, // FUNCTION APPLICATION
	'\u2062': true, // INVISIBLE TIMES
	'\u2063': true, // INVISIBLE SEPARATOR
	'\u2064': true, // INVISIBLE PLUS
	'\ufeff': true, // ZERO WIDTH NO-BREAK SPACE / BOM
	'\u00ad': true, // SOFT HYPHEN
	'\u034f': true, // COMBINING GRAPHEME JOINER
	'\u180e': true, // MONGOLIAN VOWEL SEPARATOR
}

// homoglyphs 同形字映射：长得像拉丁字母的西里尔/希腊字母 → 对应拉丁字母。
//
// NFKC 不做这件事——它管的是兼容性变体（全角→半角），而西里尔 'а' 与拉丁 'a'
// 是两个语义不同的字符，Unicode 不认为它们等价。但在词库匹配上它们是同一个东西：
// 攻击者只要把一个字母换成西里尔的，肉眼一模一样，词库直接失效。
//
// 这里只覆盖真正会混淆的那批，不追求 Unicode confusables 全表——全表有数千项，
// 且包含大量正常文本里的合法字符，全量映射会制造误杀。
var homoglyphs = map[rune]rune{
	// 西里尔小写
	'а': 'a', 'е': 'e', 'о': 'o', 'р': 'p', 'с': 'c', 'у': 'y', 'х': 'x',
	'і': 'i', 'ј': 'j', 'ѕ': 's', 'ԁ': 'd', 'һ': 'h', 'ӏ': 'l',
	'м': 'm', 'т': 't', 'в': 'b', 'к': 'k', 'н': 'h',
	// 西里尔大写
	'А': 'A', 'Е': 'E', 'О': 'O', 'Р': 'P', 'С': 'C', 'У': 'Y', 'Х': 'X',
	'І': 'I', 'Ј': 'J', 'Ѕ': 'S', 'В': 'B', 'К': 'K', 'М': 'M', 'Н': 'H',
	'Т': 'T', 'Ф': 'F',
	// 希腊
	'α': 'a', 'ο': 'o', 'ρ': 'p', 'τ': 't', 'υ': 'u', 'κ': 'k', 'ι': 'i', 'ν': 'v',
	// 亚美尼亚（仅取真正会混淆的）
	'ց': 'g',
	'Α': 'A', 'Β': 'B', 'Ε': 'E', 'Ζ': 'Z', 'Η': 'H', 'Ι': 'I', 'Κ': 'K',
	'Μ': 'M', 'Ν': 'N', 'Ο': 'O', 'Ρ': 'P', 'Τ': 'T', 'Υ': 'Y', 'Χ': 'X',
}

// Normalize 把文本归一化成用于检测的形式。
//
// 顺序有讲究：先 NFKC（把全角、兼容变体收敛），再剥零宽（NFKC 不剥），
// 再同形字（要在小写之前做，映射表区分大小写），最后小写与空白折叠。
func Normalize(text string) string {
	if text == "" {
		return ""
	}

	text = norm.NFKC.String(text)

	var b strings.Builder
	b.Grow(len(text))
	lastWasSpace := false
	for _, r := range text {
		if zeroWidth[r] {
			continue
		}
		if mapped, ok := homoglyphs[r]; ok {
			r = mapped
		}
		// 空白折叠：连续空白（含换行、制表）压成一个空格，
		// 让「换行拆词」和「多个空格拆词」收敛成同一种形式，也让 ContentHash 更稳。
		// 注意折叠本身不足以对付空格拆词，那要靠 NormalizeCompact。
		if unicode.IsSpace(r) {
			if !lastWasSpace {
				b.WriteRune(' ')
				lastWasSpace = true
			}
			continue
		}
		lastWasSpace = false
		b.WriteRune(unicode.ToLower(r))
	}
	return strings.TrimSpace(b.String())
}

// NormalizeCompact 在 Normalize 基础上再去掉所有空白。
//
// 用于 L0 的第二次扫描：折叠后的「敏 感 词」仍然带空格，匹配不上词库里的「敏感词」，
// 去掉空白才能命中。代价是会制造跨词边界的误命中（词库有「赌博」时，
// 「打赌 博士」压成「打赌博士」就会命中），所以两种形式都扫、命中任一即算命中，
// 误杀风险由词库自身的质量承担。
func NormalizeCompact(normalized string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, normalized)
}
