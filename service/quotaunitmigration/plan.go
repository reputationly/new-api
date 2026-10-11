package quotaunitmigration

import (
	"fmt"
	"math"
	"strconv"

	"github.com/shopspring/decimal"
)

// plan 描述一次单位迁移的全部差异：倍数、状态 key、清单与方案特有的额外步骤。
//
// V1（1 quota = ¥0.0000146 → ¥0.000001，QPU 500000 → 7300000）与
// V2（¥0.000001 → ¥0.00000001，QPU 7300000 → 730000000，同时把定价配置换成人民币）
// 共用同一套状态机、分批、断点续跑与报告骨架，只有本结构里的字段不同。
type plan struct {
	// title 报告标题里的方案名（V1 = 额度单位迁移，V2 = 额度单位迁移 V2）。
	title string
	// stateKey options 表里记录进度的 key。
	stateKey string
	// baseQPU / targetQPU 迁移前后的 QuotaPerUnit，只用于报告里的人民币对照。
	baseQPU   float64
	targetQPU float64

	// ratio 非 0 时用 ROUND(col × ratio) 缩放（V1：14.6）。
	ratio float64
	// factor 非 0 时是整数倍，SQL 直接写成 col × factor（V2：100，三库精确）。
	factor int64
	// scaling 是 JSON 数字字面量的缩放比（整数比，保留全部有效位）。
	scaling scaling

	smallScopes []numericScope
	smallJSON   []jsonScope
	optionSpecs []optionSpec

	// flag 是命令行参数名，用于报告与启动保护里的提示文案。
	flag string
	// pricing 为真时执行 V2 的定价 JSON 换算（V1 不做）。
	pricing bool
	// requireV1Done 为真时要求 V1 迁移已完成（V2 的前置条件）。
	requireV1Done bool
	// requireRate > 0 时要求 USDExchangeRate 等于它（V2：7.3，否则倍率换算的 14.6 会变）。
	requireRate float64
}

// maxMultiplier 缩放倍数（溢出预检用）。
func (pl *plan) maxMultiplier() float64 {
	if pl.factor > 0 {
		return float64(pl.factor)
	}
	return pl.ratio
}

// sqlScale 生成一列的 SQL 缩放表达式。
//
// V1 用 ROUND(col × 14.6)：n × 14.6 的小数部分只可能是 0/0.2/0.4/0.6/0.8，永远不落在
// .5 上，所以三库的 ROUND 规则差异不影响结果。
// V2 是整数倍 col × 100，三种数据库都是精确整数运算。
func (pl *plan) sqlScale(column string) string {
	if pl.factor > 0 {
		return fmt.Sprintf("%s * %d", column, pl.factor)
	}
	return fmt.Sprintf("ROUND(%s * %s)", column, strconv.FormatFloat(pl.ratio, 'f', -1, 64))
}

// scaleValue 把一个数值缩放到新单位（四舍五入到整数）。
func (pl *plan) scaleValue(v float64) int64 {
	if pl.factor > 0 {
		return int64(math.Round(v * float64(pl.factor)))
	}
	return int64(math.Round(v * pl.ratio))
}

// scaleLabel 报告与提示文案里用的倍数写法。
func (pl *plan) scaleLabel() string {
	if pl.factor > 0 {
		return fmt.Sprintf("%d", pl.factor)
	}
	return strconv.FormatFloat(pl.ratio, 'f', -1, 64)
}

// scaleDescription 报告里「迁移后合计」的算法描述。
func (pl *plan) scaleDescription() string {
	if pl.factor > 0 {
		return fmt.Sprintf("SUM(列 × %d)", pl.factor)
	}
	return fmt.Sprintf("SUM(ROUND(列 × %s))", pl.scaleLabel())
}

// scaleDecimal 缩放 JSON 里的数字字面量：保留全部有效位，不经过 float64。
func (pl *plan) scaleDecimal(value decimal.Decimal) decimal.Decimal {
	return pl.scaling.apply(value)
}

// scaling JSON 数字字面量的缩放比。写成整数比避免浮点误差：
//
//	V1：×14.6 = ×146 ÷10；V2：×100 = ×100 ÷1。
//
// 低层改写函数只读 activeScaling 而不读 plan：plan 经 scope 引用到改写函数，
// 改写函数再引用 plan 会构成 Go 的初始化环。
type scaling struct {
	num int64
	den int64
}

func (sc scaling) apply(value decimal.Decimal) decimal.Decimal {
	return value.Mul(decimal.NewFromInt(sc.num)).Div(decimal.NewFromInt(sc.den)).Round(0)
}

// activeScaling 当前缩放比；默认按 V1（未进入迁移时单测直接调辅助函数也能工作）。
var activeScaling = scaling{num: 146, den: 10}

// planV1 是 2026-10-10 已上线的那次迁移。
//
// 用函数而不是包级变量：Go 的初始化顺序分析会沿函数体追踪变量依赖，
// planV1 会经 smallJSONScopes → 改写函数 → currentPlan() 绕回自己，构成初始化环。
func planV1() *plan {
	return &plan{
		title:     "额度单位迁移",
		stateKey:  "QuotaUnitMigration",
		baseQPU:   500000.0,
		targetQPU: 7300000.0,
		ratio:     14.6,

		flag:        "migrate-quota-unit",
		scaling:     scaling{num: 146, den: 10},
		smallScopes: smallNumericScopes,
		smallJSON:   smallJSONScopes,
		optionSpecs: optionSpecs,
	}
}

// planV2 把单位细化到 ¥0.00000001，并把定价配置从美元口径换成人民币口径。
func planV2() *plan {
	return &plan{
		title:     "额度单位迁移 V2",
		stateKey:  "QuotaUnitMigrationV2",
		baseQPU:   7300000.0,
		targetQPU: 730000000.0,
		factor:    100,

		flag:          "migrate-quota-unit-v2",
		pricing:       true,
		scaling:       scaling{num: 100, den: 1},
		smallScopes:   smallNumericScopes,
		smallJSON:     smallJSONScopesV2,
		optionSpecs:   optionSpecsV2,
		requireV1Done: true,
		requireRate:   7.3,
	}
}

// activePlan 是当前正在跑的方案。迁移程序是「一次进程只跑一种方案」的命令行，
// 用包级变量避免把方案穿透十几个辅助函数（与 testHookAfterLogBatch 同类做法）。
//
// 刻意不给初始值：planV1 会引用到调用 currentPlan() 的辅助函数，带初始值会构成
// 初始化环。nil 时按 V1 语义，便于单测直接调用这些辅助函数。
var activePlan *plan

// currentPlan 返回当前方案（未进入迁移时按 V1）。
func currentPlan() *plan {
	if activePlan == nil {
		return planV1()
	}
	return activePlan
}
