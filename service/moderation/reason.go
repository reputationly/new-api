package moderation

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

// 拒绝文案与落库明细。见 docs/content-moderation-design.md §9.2.2、§10。

// categoryLabels 类别 → 面向用户的中文措辞。
//
// 只到类别，绝不带命中片段：把「命中了哪个词」回显给用户，等于免费送一个绕过探测器——
// 改一个字再试一次就知道是不是这个词触发的（§9.2.2）。
var categoryLabels = map[string]string{
	system_setting.CategoryKeyword:   "敏感词",
	system_setting.CategorySexual:    "色情内容",
	system_setting.CategoryIllegal:   "违法违规内容",
	system_setting.CategoryPolitical: "政治敏感内容",
	system_setting.CategoryJailbreak: "越狱指令",
	system_setting.CategoryViolent:   "暴力内容",
	system_setting.CategorySelfHarm:  "自伤自杀内容",
	system_setting.CategoryUnethical: "违背公序良俗的内容",
	system_setting.CategoryPII:       "个人隐私信息",
	system_setting.CategoryCopyright: "版权风险内容",
	system_setting.CategoryCyber:     "网络攻击内容",
	system_setting.CategoryAdvice:    "违规专业建议",
	system_setting.CategoryMinor:     "危害未成年人的内容",
	system_setting.CategoryTerror:    "暴恐极端内容",
	system_setting.CategoryVulgar:    "低俗不良内容",
}

// ReasonText 把类别列表拼成拒绝原因。类别为空时给一个不暴露任何信息的兜底文案。
func ReasonText(categories []string) string {
	labels := make([]string, 0, len(categories))
	seen := make(map[string]bool, len(categories))
	for _, c := range categories {
		label, ok := categoryLabels[c]
		if !ok {
			// 模型给出未登记的类别时不回显原始英文标识，退化成通用措辞即可。
			continue
		}
		if seen[label] {
			continue
		}
		seen[label] = true
		labels = append(labels, label)
	}
	if len(labels) == 0 {
		return "输入内容不符合内容规范"
	}
	return "输入内容涉及" + strings.Join(labels, "、")
}

// CategoryLabel 单个类别的中文名，供管理端列表展示。
func CategoryLabel(category string) string {
	if label, ok := categoryLabels[category]; ok {
		return label
	}
	return category
}

// wordsColumnLimit Words 列的字符上限，与 gorm 的 varchar(512) 对齐。
// 超长直接截断：命中几十个词的记录，前几个已经足够定位是哪条规则在误杀。
const wordsColumnLimit = 500

// truncateWords 把命中词拼成分隔的列值，超长按整词截断。
// 按字节硬切会切出半个词，那种值既筛不出来也读不懂。
//
// 分隔符用换行而不是逗号：敏感词是运营自由填写的，词条里完全可能带逗号
// （英文短语尤其常见），逗号分隔会把一条规则劈成两个词——列表页显示出一条
// 根本不存在的规则，按词筛选也再也匹配不上，而「是哪一条词拦的」正是这一列
// 存在的全部理由。换行是安全的：setting.SensitiveWordsFromString 就是按换行
// 切出词表的（setting/sensitive.go:29），所以词条里不可能含换行。
func truncateWords(words []string) string {
	out := ""
	for _, w := range words {
		next := w
		if out != "" {
			next = out + model.ModerationWordsSep + w
		}
		if len(next) > wordsColumnLimit {
			break
		}
		out = next
	}
	return out
}

// detailPayload moderation_log.detail 的结构。只放脱敏内容，原文走 ContentEnc（§10）。
// 命中词不在这里——它已经是独立的 words 列，重复存一份只会让两处对不上时无从判断哪个准。
type detailPayload struct {
	Mode string `json:"mode"`
	// Dialect 这条判定是哪个协议（也就是哪个模型）给出的。
	//
	// **切换审核模型时这一列是必需的。** 没有它，切换前后的记录在 provider 上
	// 完全一样（都是 "L1"），于是没法按模型分组算准召——而那正是切换要回答的
	// 唯一问题。事后再想补也来不及：历史记录无从追认是哪个模型判的。
	Dialect string `json:"dialect,omitempty"`
	// Reason 模型给出的归因理由（如 Zhongsen 的 <explanation>）。
	//
	// 给待复核队列用：复核时真正要回答的是「模型为什么判它违规」，光有类别标签
	// 答不了。**只进 detail 列，不回显给用户**（§9.2.2）。
	Reason string `json:"reason,omitempty"`
	Error  string `json:"error,omitempty"`
}

func buildDetail(v *Verdict, mode system_setting.ModerationMode) string {
	p := detailPayload{Mode: string(mode), Dialect: v.Dialect, Reason: v.Reason}
	if v.Action == ActionError {
		p.Error = v.Detail
	}
	b, err := common.Marshal(p)
	if err != nil {
		return ""
	}
	return string(b)
}
