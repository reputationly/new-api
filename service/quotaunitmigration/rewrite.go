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

// scaleRawNumber 按当前方案的倍数（V1 ×14.6、V2 ×100）把 JSON 数字字面量四舍五入成整数。
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
	// 缩放倍数由方案决定（V1 ×14.6 = ×146÷10，V2 ×100），整数部分始终精确。
	scaled := activeScaling.apply(value)
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

// rewriteTaskPrivateDataV2 在 V1 的 quota 键改写之上，把任务冻结的定价字段
// （billing_context.model_price / model_ratio、video_billing.unit_price）从美元换成
// 人民币，并补上 price_unit = cny 标记。
//
// 迁移范围包含**已完成任务**（评审 §12.2 R3），不区分任务状态。理由是两个真实消费者：
//  1. 视频矩阵任务在轮询阶段按冻结的 video_billing.unit_price 做差额结算（真的在算钱，
//     service/task_billing.go 的 RecalculateTaskQuotaByVideoMatrix ×QuotaPerYuan）；
//  2. 退款/差额日志通过 taskBillingOther 把 model_price / model_ratio 写进 other，
//     前端渲染的就是这份日志。
//
// （Task.PrivateData 本身是 json:"-"，不对外暴露，所以这里不是为了接口展示，而是为了
// 让库里与日志里只剩人民币一种口径。）
func rewriteTaskPrivateDataV2(fields map[string]json.RawMessage, stat *jsonStat) bool {
	raw, ok := fields["billing_context"]
	if !ok {
		return rewriteTaskPrivateData(fields, stat)
	}
	var bc map[string]json.RawMessage
	if err := common.Unmarshal(raw, &bc); err != nil || bc == nil {
		return rewriteTaskPrivateData(fields, stat)
	}
	// 行级幂等：带 price_unit 的行整行已迁过（标记与缩放同在一个事务里写），直接跳过，
	// 否则续跑/重跑会把三个价格字段再乘一遍。
	if _, migrated := bc["price_unit"]; migrated {
		return false
	}

	changed := rewriteTaskPrivateData(fields, stat)
	bcChanged := false
	if scalePricingField(bc, "model_price", pricingScalePriceNum, pricingScalePriceDen) {
		bcChanged = true
	}
	if scalePricingField(bc, "model_ratio", pricingScaleRatioNum, pricingScaleRatioDen) {
		bcChanged = true
	}
	if videoRaw, ok := bc["video_billing"]; ok {
		var vb map[string]json.RawMessage
		if err := common.Unmarshal(videoRaw, &vb); err == nil && vb != nil {
			if scalePricingField(vb, "unit_price", pricingScalePriceNum, pricingScalePriceDen) {
				if encoded, err := common.Marshal(vb); err == nil {
					bc["video_billing"] = encoded
					bcChanged = true
				}
			}
		}
	}
	if _, exists := bc["price_unit"]; !exists {
		bc["price_unit"] = json.RawMessage(`"` + common.PriceUnitCNY + `"`)
		bcChanged = true
	}
	if bcChanged {
		if encoded, err := common.Marshal(bc); err == nil {
			fields["billing_context"] = encoded
			changed = true
		}
	}
	return changed
}

// scalePricingField 把一个定价字段按整数比换算成人民币（保留 6 位小数）。
// 跳过 0 与负值（`-1` 是「按量计费」哨兵，不是价格）。
func scalePricingField(fields map[string]json.RawMessage, key string, num, den int64) bool {
	raw, ok := fields[key]
	if !ok {
		return false
	}
	value, ok := parsePricingNumber(raw)
	if !ok {
		return false
	}
	fields[key] = json.RawMessage(scalePricing(value, num, den).String())
	return true
}

// parsePricingNumber 解析定价字段的数字字面量；非数字、0、负数一律算「不换算」。
func parsePricingNumber(raw json.RawMessage) (decimal.Decimal, bool) {
	text := strings.TrimSpace(string(raw))
	if text == "" || text == "null" {
		return decimal.Zero, false
	}
	value, err := decimal.NewFromString(text)
	if err != nil || value.IsZero() || value.IsNegative() {
		return decimal.Zero, false
	}
	return value, true
}

func rewriteUserSetting(fields map[string]json.RawMessage, stat *jsonStat) bool {
	return scaleObjectKey(fields, "quota_warning_threshold", stat)
}
