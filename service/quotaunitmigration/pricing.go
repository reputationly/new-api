package quotaunitmigration

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// 定价字段的换算倍数，用整数比避免浮点误差：
//
//	倍率：旧「1 = $2/百万 tokens」→ 新「¥/百万 tokens」，× 2 × 7.3 = × 14.6 = × 146/10
//	价格：美元价 → 人民币价，× 7.3 = × 73/10
//
// 结果保留 6 位小数（¥0.000001/百万 tokens 的精度，比任何实际报价都细）：
// 编辑器存的 ¥2 是 0.13698630137，× 14.6 = 1.999999999… → 6 位后正好还原成 2。
const (
	pricingScaleRatioNum = 146
	pricingScaleRatioDen = 10
	pricingScalePriceNum = 73
	pricingScalePriceDen = 10
	pricingScaleDecimals = 6
)

const (
	optModelRatio         = "ModelRatio"
	optModelPrice         = "ModelPrice"
	optVideoPricing       = "VideoPricingConfig"
	optToolPrices         = "tool_price_setting.prices"
	optViolationDeduction = "grok.violation_deduction_amount"
	optBillingExpr        = "billing_setting.billing_expr"
)

// scalePricing 按整数比把美元口径的定价换算成人民币，保留 6 位小数。
func scalePricing(value decimal.Decimal, num, den int64) decimal.Decimal {
	return value.Mul(decimal.NewFromInt(num)).Div(decimal.NewFromInt(den)).Round(pricingScaleDecimals)
}

// v2PricingStage 承载 V2 的定价换算：dry-run 只读并产出报告，apply 在小表事务里写回。
type v2PricingStage struct {
	rows     [][]string // [位置, 旧值, 新值, 换算前人民币, 换算后人民币, 差值]
	unparsed []string   // 差值 > 1e-6 的条目，需要人工确认
	v1Exprs  []string   // 仍是 v1 口径的分段表达式（只报告，不改写）
	skipped  []string   // 库里没有的配置项
}

func newV2PricingStage() *v2PricingStage { return &v2PricingStage{} }

// inspectOnly dry-run：只读库并按改写后的口径产出报告，不写任何数据。
func (s *v2PricingStage) inspectOnly() error { return s.convert(nil) }

// runInTx apply：在调用方的事务里读 → 换算 → 写回。
func (s *v2PricingStage) runInTx(tx *gorm.DB) error { return s.convert(tx) }

func (s *v2PricingStage) convert(tx *gorm.DB) error {
	db := tx
	if db == nil {
		db = model.DB
	}
	if err := s.convertNumberMap(db, tx, optModelRatio, pricingScaleRatioNum, pricingScaleRatioDen, false); err != nil {
		return err
	}
	if err := s.convertNumberMap(db, tx, optModelPrice, pricingScalePriceNum, pricingScalePriceDen, true); err != nil {
		return err
	}
	if err := s.convertVideoPricing(db, tx); err != nil {
		return err
	}
	if err := s.convertToolPrices(db, tx); err != nil {
		return err
	}
	if err := s.convertViolationDeduction(db, tx); err != nil {
		return err
	}
	return s.inspectBillingExpr(db)
}

// convertNumberMap 处理 {模型: 数值} 形式的定价表（ModelRatio / ModelPrice）。
//
// allowSentinel 为真时保留负值原样（`-1` 是「按量计费」哨兵，不是价格）。
func (s *v2PricingStage) convertNumberMap(db *gorm.DB, tx *gorm.DB, key string, num, den int64, allowSentinel bool) error {
	raw, found, err := loadOption(db, key)
	if err != nil {
		return err
	}
	if !found {
		s.skipped = append(s.skipped, key)
		return nil
	}
	source := map[string]float64{}
	if err := common.UnmarshalJsonStr(raw, &source); err != nil {
		return fmt.Errorf("options.%s 解析失败，拒绝执行（定价表写错会让所有模型价格失真）: %w", key, err)
	}
	converted := make(map[string]float64, len(source))
	for _, name := range sortedFloatKeys(source) {
		old := source[name]
		if allowSentinel && old < 0 {
			// -1 是「按量计费」哨兵，不是价格，原样保留。
			converted[name] = old
			s.addSentinelRow(key+"."+name, old)
			continue
		}
		beforeCNY := decimal.NewFromFloat(old).Mul(decimal.NewFromInt(num)).Div(decimal.NewFromInt(den))
		after := beforeCNY.Round(pricingScaleDecimals)
		converted[name] = after.InexactFloat64()
		s.addRow(key+"."+name, old, beforeCNY, after)
	}
	if tx == nil {
		return nil
	}
	encoded, err := common.Marshal(converted)
	if err != nil {
		return err
	}
	if err := upsertOption(tx, key, string(encoded)); err != nil {
		return err
	}
	return verifyReadBack(tx, key, converted, allowSentinel)
}

// convertVideoPricing 改写视频矩阵里所有价格格子，结构与分辨率键原样保留。
func (s *v2PricingStage) convertVideoPricing(db *gorm.DB, tx *gorm.DB) error {
	raw, found, err := loadOption(db, optVideoPricing)
	if err != nil {
		return err
	}
	if !found {
		s.skipped = append(s.skipped, optVideoPricing)
		return nil
	}
	entries := map[string]json.RawMessage{}
	if err := common.UnmarshalJsonStr(raw, &entries); err != nil {
		return fmt.Errorf("options.%s 解析失败，拒绝执行: %w", optVideoPricing, err)
	}
	out := make(map[string]json.RawMessage, len(entries))
	for _, modelName := range sortedRawKeys(entries) {
		fields := map[string]json.RawMessage{}
		if err := common.Unmarshal(entries[modelName], &fields); err != nil {
			return fmt.Errorf("options.%s.%s 解析失败，拒绝执行: %w", optVideoPricing, modelName, err)
		}
		for _, priceKey := range []string{"token", "per_call", "per_second"} {
			block, ok := fields[priceKey]
			if !ok {
				continue
			}
			rewritten, changed, err := s.convertVideoPriceBlock(modelName, priceKey, block)
			if err != nil {
				return err
			}
			if changed {
				fields[priceKey] = rewritten
			}
		}
		encoded, err := common.Marshal(fields)
		if err != nil {
			return err
		}
		out[modelName] = encoded
	}
	if tx == nil {
		return nil
	}
	encoded, err := common.Marshal(out)
	if err != nil {
		return err
	}
	if err := upsertOption(tx, optVideoPricing, string(encoded)); err != nil {
		return err
	}
	return verifyReadBack(tx, optVideoPricing, out, false)
}

// convertVideoPriceBlock 改写一个价格区块：token / per_call 是两层 map，per_second 是一层。
func (s *v2PricingStage) convertVideoPriceBlock(modelName, priceKey string, block json.RawMessage) (json.RawMessage, bool, error) {
	if priceKey == "per_second" {
		row := map[string]float64{}
		if err := common.Unmarshal(block, &row); err != nil {
			return nil, false, fmt.Errorf("options.%s.%s.per_second 解析失败: %w", optVideoPricing, modelName, err)
		}
		converted := make(map[string]float64, len(row))
		for _, res := range sortedFloatKeys(row) {
			converted[res] = s.recordAndScale(fmt.Sprintf("%s.%s.per_second.%s", optVideoPricing, modelName, res), row[res])
		}
		encoded, err := common.Marshal(converted)
		if err != nil {
			return nil, false, err
		}
		return encoded, true, nil
	}

	matrix := map[string]map[string]float64{}
	if err := common.Unmarshal(block, &matrix); err != nil {
		return nil, false, fmt.Errorf("options.%s.%s.%s 解析失败: %w", optVideoPricing, modelName, priceKey, err)
	}
	converted := make(map[string]map[string]float64, len(matrix))
	for _, res := range sortedMatrixKeys(matrix) {
		cells := make(map[string]float64, len(matrix[res]))
		for _, cell := range sortedFloatKeys(matrix[res]) {
			cells[cell] = s.recordAndScale(
				fmt.Sprintf("%s.%s.%s.%s.%s", optVideoPricing, modelName, priceKey, res, cell), matrix[res][cell])
		}
		converted[res] = cells
	}
	encoded, err := common.Marshal(converted)
	if err != nil {
		return nil, false, err
	}
	return encoded, true, nil
}

// convertToolPrices / convertViolationDeduction：库里没有这两个 key 时跳过
// （代码默认值本身已经是人民币）。
func (s *v2PricingStage) convertToolPrices(db *gorm.DB, tx *gorm.DB) error {
	return s.convertNumberMap(db, tx, optToolPrices, pricingScalePriceNum, pricingScalePriceDen, false)
}

func (s *v2PricingStage) convertViolationDeduction(db *gorm.DB, tx *gorm.DB) error {
	raw, found, err := loadOption(db, optViolationDeduction)
	if err != nil {
		return err
	}
	if !found {
		s.skipped = append(s.skipped, optViolationDeduction)
		return nil
	}
	old, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return fmt.Errorf("options.%s 不是数字，拒绝执行: %q", optViolationDeduction, raw)
	}
	after := scalePricing(decimal.NewFromFloat(old), pricingScalePriceNum, pricingScalePriceDen)
	s.addRow(optViolationDeduction, old,
		decimal.NewFromFloat(old).Mul(decimal.NewFromInt(pricingScalePriceNum)).Div(decimal.NewFromInt(pricingScalePriceDen)), after)
	if tx == nil {
		return nil
	}
	if after.IsNegative() {
		return fmt.Errorf("options.%s 换算后为负（%s），拒绝执行", optViolationDeduction, after.String())
	}
	if err := upsertOption(tx, optViolationDeduction, after.String()); err != nil {
		return err
	}
	if readBack, found, err := loadOption(tx, optViolationDeduction); err != nil {
		return err
	} else if !found || readBack != after.String() {
		return fmt.Errorf("读回校验失败：options.%s 写入 %s，读回 %q", optViolationDeduction, after.String(), readBack)
	}
	return nil
}

// checkV1Expressions 是 apply 之前的只读前置检查：库里若还有 v1（美元口径）分段表达式，
// 直接拒绝执行迁移。
//
// 迁移完成后这些表达式在预扣阶段就会被拒绝（评审 R2），对应模型会整体不可用——不能等到
// 线上报错才发现。dry-run 仍然只报告，由运维决定是重录为 v2 还是先清掉这些表达式。
func (s *v2PricingStage) checkV1Expressions() error {
	if err := s.inspectBillingExpr(model.DB); err != nil {
		return err
	}
	if len(s.v1Exprs) == 0 {
		return nil
	}
	listed := s.v1Exprs
	if len(listed) > 10 {
		listed = append(listed[:10:10], "…")
	}
	return fmt.Errorf("发现 %d 条 v1（美元口径）分段表达式，拒绝执行迁移：\n  %s\n"+
		"迁移后它们在预扣阶段会被拒绝（ErrExprV1Unsupported），对应模型会整体不可用。\n"+
		"请先在「定价设置 → 表达式」里按 ¥/百万 tokens（系数 ×14.6）重录为 v2，"+
		"或在窗口内先清掉这些表达式，再跑 apply",
		len(s.v1Exprs), strings.Join(listed, "\n  "))
}

// verifyReadBack 在同一事务里读回刚写入的 options 值并与期望值比对。
//
// 定价表写坏（键丢失、值算错、混入负值）不会让迁移报错，只会让模型价格整体失真，
// 所以写完必须读回校验，任何不一致都让整个事务回滚。
func verifyReadBack(tx *gorm.DB, key string, expected any, allowSentinel bool) error {
	raw, found, err := loadOption(tx, key)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("读回校验失败：options.%s 写入后读不到", key)
	}
	actual := map[string]any{}
	if err := common.UnmarshalJsonStr(raw, &actual); err != nil {
		return fmt.Errorf("读回校验失败：options.%s 写入后无法解析: %w", key, err)
	}
	want := map[string]any{}
	encoded, err := common.Marshal(expected)
	if err != nil {
		return err
	}
	if err := common.Unmarshal(encoded, &want); err != nil {
		return err
	}
	if len(actual) != len(want) {
		return fmt.Errorf("读回校验失败：options.%s 键数是 %d，写入时是 %d", key, len(actual), len(want))
	}
	if !reflect.DeepEqual(actual, want) {
		return fmt.Errorf("读回校验失败：options.%s 读回的值与写入值不一致（期望 %d 个键与换算后的值）", key, len(want))
	}
	if bad, ok := findNegative(actual, allowSentinel); ok {
		return fmt.Errorf("读回校验失败：options.%s 出现负值 %v（只有按次价的 -1 哨兵允许为负）", key, bad)
	}
	return nil
}

// findNegative 在嵌套结构里找第一个负的数值叶子；allowSentinel 为真时忽略 -1 哨兵。
func findNegative(value any, allowSentinel bool) (any, bool) {
	switch v := value.(type) {
	case map[string]any:
		for _, item := range v {
			if bad, ok := findNegative(item, allowSentinel); ok {
				return bad, true
			}
		}
	case []any:
		for _, item := range v {
			if bad, ok := findNegative(item, allowSentinel); ok {
				return bad, true
			}
		}
	case float64:
		if v < 0 && !(allowSentinel && v == -1) {
			return v, true
		}
	}
	return nil, false
}

// inspectBillingExpr 只报告、不改写分段表达式（D3）：v1 的结算会被拒绝，
// 需要管理员在编辑器里重录为 v2。
func (s *v2PricingStage) inspectBillingExpr(db *gorm.DB) error {
	raw, found, err := loadOption(db, optBillingExpr)
	if err != nil {
		return err
	}
	if !found || strings.TrimSpace(raw) == "" || strings.TrimSpace(raw) == "{}" {
		return nil
	}
	exprs := map[string]string{}
	if err := common.UnmarshalJsonStr(raw, &exprs); err != nil {
		return fmt.Errorf("options.%s 解析失败: %w", optBillingExpr, err)
	}
	s.v1Exprs = nil
	names := make([]string, 0, len(exprs))
	for name := range exprs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		expr := strings.TrimSpace(exprs[name])
		if strings.HasPrefix(expr, "v2:") {
			continue
		}
		s.v1Exprs = append(s.v1Exprs, fmt.Sprintf("%s = %s", name, expr))
	}
	return nil
}

// recordAndScale 记录一行报告并返回换算后的值。
func (s *v2PricingStage) recordAndScale(label string, old float64) float64 {
	beforeCNY := decimal.NewFromFloat(old).Mul(decimal.NewFromInt(pricingScalePriceNum)).Div(decimal.NewFromInt(pricingScalePriceDen))
	after := beforeCNY.Round(pricingScaleDecimals)
	s.addRow(label, old, beforeCNY, after)
	return after.InexactFloat64()
}

// addSentinelRow 记录一行「哨兵值不换算」。
func (s *v2PricingStage) addSentinelRow(label string, old float64) {
	s.rows = append(s.rows, []string{label, formatPricing(old), formatPricing(old), "—", "—", "—（哨兵，不换算）"})
}

// addRow 往报告里加一行：旧值、新值（= 换算后人民币）、换算前后人民币与差值。
func (s *v2PricingStage) addRow(label string, old float64, beforeCNY, after decimal.Decimal) {
	diff := after.Sub(beforeCNY)
	row := []string{
		label,
		formatPricing(old),
		after.String(),
		beforeCNY.StringFixed(pricingScaleDecimals),
		after.StringFixed(pricingScaleDecimals),
		diff.StringFixed(pricingScaleDecimals),
	}
	s.rows = append(s.rows, row)
	if diff.Abs().GreaterThan(decimal.NewFromFloat(1e-6)) {
		s.unparsed = append(s.unparsed, fmt.Sprintf("%s：差 %s", label, diff.StringFixed(pricingScaleDecimals)))
	}
}

func formatPricing(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

func sortedFloatKeys(m map[string]float64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedMatrixKeys(m map[string]map[string]float64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedRawKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// writePricingReport 打印定价换算报告。
func (s *v2PricingStage) writePricingReport(rep *report) {
	rep.blank()
	rep.line("[定价配置] 美元口径 → 人民币口径（倍率 ×14.6、价格 ×7.3，保留 6 位小数）")
	rep.table([]string{"位置", "旧值", "新值", "换算前人民币", "换算后人民币", "差值"}, s.rows)
	if len(s.unparsed) > 0 {
		rep.line("⚠️ 以下条目换算前后差值超过 1e-6，需要人工确认：")
		for _, line := range s.unparsed {
			rep.line("  - %s", line)
		}
	}
	if len(s.skipped) > 0 {
		rep.line("库里没有、跳过不改写（代码默认值已是人民币）：%s", strings.Join(s.skipped, ", "))
	}
	if len(s.v1Exprs) > 0 {
		rep.line("⚠️ 仍是 v1 口径的分段表达式共 %d 条，未改写（结算会拒绝，请在编辑器里重录为 v2）：", len(s.v1Exprs))
		for _, line := range s.v1Exprs {
			rep.line("  - %s", line)
		}
	} else {
		rep.line("分段表达式：没有 v1 口径的条目（或库中没有配置）")
	}
}
