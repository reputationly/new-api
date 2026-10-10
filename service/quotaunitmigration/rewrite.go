package quotaunitmigration

import (
	"encoding/json"
	"strings"

	"github.com/QuantumNous/new-api/common"

	"github.com/shopspring/decimal"
)

// JSON 列的改写只做「按目标键换值」的外科手术：
//
//   - 顶层解码成 map[string]json.RawMessage，而不是 map[string]interface{}。
//     后者会把每个数字都过一遍 float64，JSON 里超过 2^53 的大整数（例如 9007199254740993）
//     会当场丢精度。RawMessage 保留每个键的原始字节，只有被改写的键才重新序列化。
//   - 嵌套层（aggregate 对象、entitlement_spent 数组）同样逐层取 RawMessage，
//     只重排被改写的那个子树。
//   - 没有任何目标键被改写的行**不写回数据库**：logs.other 有 85 万行，
//     无差别重写一遍是纯粹的浪费，也会把本来完好的原文改成另一种写法。
//
// 改写后顶层键顺序与空白可能与原文不同（common.Marshal 即 json.Marshal，按键名排序
// 并做 HTML 转义），这属于写法差异、含义不变。

// jsonStat 一次 JSON 列扫描的统计。
type jsonStat struct {
	scanned    int64
	rewritten  int64
	nonNumeric int64
	// unparsable 无法解析成 JSON 对象的行 id（空串已在 SQL 侧排除）。
	unparsable []int64
}

// jsonRewriter 在已解码的顶层对象上改写目标键，返回是否有改写。
type jsonRewriter func(fields map[string]json.RawMessage, stat *jsonStat) bool

// scaleRawNumber 把 JSON 数字字面量 ×14.6 四舍五入成整数。
// 用 decimal 解析原始字节：不经过 float64，大整数逐位保留。
// 返回 false 表示这个字面量不是数字（null / 字符串 / 对象 / 数组 / 非法数字），
// 调用方按「未改写」计数。
func scaleRawNumber(raw json.RawMessage) (json.RawMessage, bool) {
	text := strings.TrimSpace(string(raw))
	if text == "" || text == "null" {
		return nil, false
	}
	value, err := decimal.NewFromString(text)
	if err != nil {
		return nil, false
	}
	// ×14.6 = ×146 ÷10：整数部分精确（decimal 的除法精度只约束小数位），
	// 且 14.6 × n 的小数部分永远不落在 .5 上，Round 的舍入方向不影响结果。
	scaled := value.Mul(decimal.NewFromInt(146)).Div(decimal.NewFromInt(10)).Round(0)
	return json.RawMessage(scaled.String()), true
}

// scaleObjectKey 改写对象里某个键的数值，返回是否改写。
func scaleObjectKey(fields map[string]json.RawMessage, key string, stat *jsonStat) bool {
	raw, ok := fields[key]
	if !ok {
		return false
	}
	scaled, ok := scaleRawNumber(raw)
	if !ok {
		stat.nonNumeric++
		return false
	}
	fields[key] = scaled
	return true
}

// scaleNestedObjectKey 改写「对象.键」一步嵌套，只重排那个子树。
func scaleNestedObjectKey(fields map[string]json.RawMessage, objectKey, key string, stat *jsonStat) bool {
	raw, ok := fields[objectKey]
	if !ok {
		return false
	}
	var nested map[string]json.RawMessage
	if err := common.Unmarshal(raw, &nested); err != nil || nested == nil {
		return false
	}
	if !scaleObjectKey(nested, key, stat) {
		return false
	}
	encoded, err := common.Marshal(nested)
	if err != nil {
		return false
	}
	fields[objectKey] = encoded
	return true
}

// scaleArrayAmounts 改写「对象数组[].键」，只重排被改写的元素。
func scaleArrayAmounts(fields map[string]json.RawMessage, arrayKey, key string, stat *jsonStat) bool {
	raw, ok := fields[arrayKey]
	if !ok {
		return false
	}
	var items []json.RawMessage
	if err := common.Unmarshal(raw, &items); err != nil || items == nil {
		return false
	}
	changed := false
	for i, itemRaw := range items {
		var item map[string]json.RawMessage
		if err := common.Unmarshal(itemRaw, &item); err != nil || item == nil {
			continue
		}
		if !scaleObjectKey(item, key, stat) {
			continue
		}
		encoded, err := common.Marshal(item)
		if err != nil {
			continue
		}
		items[i] = encoded
		changed = true
	}
	if !changed {
		return false
	}
	encoded, err := common.Marshal(items)
	if err != nil {
		return false
	}
	fields[arrayKey] = encoded
	return true
}

func rewriteLogOther(fields map[string]json.RawMessage, stat *jsonStat) bool {
	changed := false
	for _, key := range logOtherScaledKeys {
		if scaleObjectKey(fields, key, stat) {
			changed = true
		}
	}
	return changed
}

func rewriteTaskPrivateData(fields map[string]json.RawMessage, stat *jsonStat) bool {
	changed := false
	if scaleObjectKey(fields, "points_consumed", stat) {
		changed = true
	}
	if scaleObjectKey(fields, "credit_consumed", stat) {
		changed = true
	}
	if scaleNestedObjectKey(fields, "aggregate", "stage1_adaptor_quota", stat) {
		changed = true
	}
	if scaleArrayAmounts(fields, "entitlement_spent", "amount", stat) {
		changed = true
	}
	return changed
}

func rewriteUserSetting(fields map[string]json.RawMessage, stat *jsonStat) bool {
	return scaleObjectKey(fields, "quota_warning_threshold", stat)
}
