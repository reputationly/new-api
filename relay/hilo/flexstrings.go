package hilo

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// FlexStrings 一个能吃下多种写法的字符串列表。
//
// # 为什么需要它
//
// 模型对"列表里放什么"的理解会漂。实测 qwen3.8-flash-fp8 把 sync_rules
// 写成了对象数组:
//
//	"sync_rules": [{"type":"lip_sync","subject_id":"subject_1",
//	                "shot_id":"02","instruction":"Sync mouth movements…"}]
//
// 而我们声明的是 []string。Go 的严格反序列化在这里是**整份失败** ——
// 一个字段的形态差异,把一次本来完全可用的编译(其余字段全对)整个打掉,
// 报出来的还是一句 Go 类型错误,重修那侧完全看不懂。
//
// 源实现是 Python,拿 `_strings()` 统一收口,天然容忍这些写法。移植过来就
// 必须补上这一层,否则我们比源实现脆。
//
// # 容忍不等于放任
//
// 这里只做**形态**归一,不碰语义:该有几条、内容对不对,仍然由 ValidateIR
// 按具名规则查。容忍的是"怎么写",不是"写了什么"。
type FlexStrings []string

// 这些键名按顺序优先取:模型把一条规则写成对象时,真正的正文通常在它们里面。
var flexTextKeys = []string{
	"instruction", "rule", "text", "description", "value", "content", "note", "name",
}

func (f *FlexStrings) UnmarshalJSON(data []byte) error {
	var v any
	if err := common.Unmarshal(data, &v); err != nil {
		return err
	}
	out, ok := flexToStrings(v)
	if !ok {
		return fmt.Errorf("无法读成字符串列表: %s", truncateForError(string(data)))
	}
	*f = out
	return nil
}

func (f FlexStrings) MarshalJSON() ([]byte, error) {
	if f == nil {
		return []byte("[]"), nil
	}
	return common.Marshal([]string(f))
}

func flexToStrings(v any) ([]string, bool) {
	switch t := v.(type) {
	case nil:
		return nil, true
	case string:
		// 单个字符串 = 只有一条。模型偷懒时常这么写。
		if s := strings.TrimSpace(t); s != "" {
			return []string{s}, true
		}
		return nil, true
	case []any:
		var out []string
		for _, it := range t {
			if s := flexElem(it); s != "" {
				out = append(out, s)
			}
		}
		return out, true
	case map[string]any:
		// 整个字段被写成一个对象:当成只有一条。
		if s := flexElem(t); s != "" {
			return []string{s}, true
		}
		return nil, true
	}
	return nil, false
}

// flexElem 把一个元素读成一行文本。
func flexElem(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case float64:
		return strconv.FormatFloat(t, 'g', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	case map[string]any:
		return flexObject(t)
	}
	return ""
}

// flexObject 把一个对象读成一行文本。
//
// 先找正文键(instruction / rule / text …):模型写
// `{"type":"lip_sync","instruction":"Sync mouth…"}` 时,真正要说的是那句
// instruction,其余是它自己加的元数据。
//
// 找不到就退成 `key: value` 的拼接,并**按键名排序** —— Go 的 map 遍历顺序
// 是随机的,不排序会让同一份 IR 每次渲染出不同的文本,而那种不确定性最难查。
func flexObject(m map[string]any) string {
	for _, k := range flexTextKeys {
		if s, ok := m[k].(string); ok {
			if s = strings.TrimSpace(s); s != "" {
				return s
			}
		}
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		if s := flexElem(m[k]); s != "" {
			parts = append(parts, k+": "+s)
		}
	}
	return strings.Join(parts, ", ")
}

func truncateForError(s string) string {
	if len(s) <= 120 {
		return s
	}
	return s[:120] + "…"
}
