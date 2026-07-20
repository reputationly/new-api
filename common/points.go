package common

import "github.com/shopspring/decimal"

// QuotaPerPoint 表示「1 积分」对应多少内部 quota unit（初始值 1 积分 = 1 分钱人民币）。
//
// 推导：1 USD = QuotaPerUnit(=500000) quota = 7.3 元；1 积分 = 1 分 = 0.01 元
//
//	=> QuotaPerPoint = 500000 / (7.3 * 100) = 500000 / 730 ≈ 684.93
//
// 与 QuotaPerUnit 一样作为 common 包全局默认值，扣费/发放全链路只以 quota unit 记账，
// QuotaPerPoint 仅用于「发放输入」「UI 展示」「结算取整」三个边界的换算，不进高频扣费热路径。
var QuotaPerPoint = QuotaPerUnit / 730.0

// QuotaPerPointFunc 由上层配置包（setting/operation_setting）在 init 时注入，返回
// PointsSetting.QuotaPerPoint 的实时值。common 不能 import operation_setting（会循环），
// 故用依赖倒置：注入后换算读实时配置值，未注入则回退默认 QuotaPerPoint。
var QuotaPerPointFunc func() float64

func getQuotaPerPoint() float64 {
	if QuotaPerPointFunc != nil {
		if v := QuotaPerPointFunc(); v > 0 {
			return v
		}
	}
	return QuotaPerPoint
}

// PointsToQuota 把「积分数」换算为内部 quota unit（发放/输入边界用），向上取整。
// 用 Ceil 而非 Round：保证 QuotaToPoints(PointsToQuota(n)) == n 精确往返——
// round 有约一半概率向下，叠加展示侧 floor 会「发 100 显示 99、1 积分码显示 0」；
// ceil 多给的差额 < 1 quota unit（远小于 1 积分），方向让利用户且不累积。
func PointsToQuota(points int) int {
	if points == 0 {
		return 0
	}
	q := decimal.NewFromInt(int64(points)).Mul(decimal.NewFromFloat(getQuotaPerPoint()))
	return int(q.Ceil().IntPart())
}

// QuotaToPoints 把内部 quota unit 换算为「积分数」（展示/对账边界用），向下取整
// （积分对用户永不显示小数；余额展示保守方向）。
func QuotaToPoints(quota int) int {
	qpp := getQuotaPerPoint()
	if quota == 0 || qpp <= 0 {
		return 0
	}
	p := decimal.NewFromInt(int64(quota)).Div(decimal.NewFromFloat(qpp))
	return int(p.Floor().IntPart())
}

// QuotaToPointsCeil 把内部 quota unit 换算为「积分数」，向上取整。
// 用于消费结算取整：积分抵扣量不足 1 积分按 1 积分计（加速营销积分消耗，
// 见 HybridFunding 结算处），与展示侧 QuotaToPoints 的 floor 方向相反。
func QuotaToPointsCeil(quota int) int {
	qpp := getQuotaPerPoint()
	if quota <= 0 || qpp <= 0 {
		return 0
	}
	p := decimal.NewFromInt(int64(quota)).Div(decimal.NewFromFloat(qpp))
	return int(p.Ceil().IntPart())
}
