package common

import "github.com/shopspring/decimal"

// 算力点（compute point）—— 套餐的计费单位。
//
// 与积分同构：内部全程以 quota unit 记账，只有一个全局换算率，换算只发生在
// 「发放输入」「UI 展示」「结算取整」三个边界，**不进扣费热路径**。
//
// 刻意**不建独立价目表**。模型消耗多少算力点，直接由现有倍率体系算出的 quota 换算
// 得到。这么做不只是省一张表的维护：
//
//  1. 零配置、不会漏配。新模型上架自动有算力点价格；独立价目表的失败模式是
//     「被套餐覆盖却没配单价 → 静默降级扣客户余额」，这个模式在此处根本不存在。
//  2. 单价天然恒定。模型的 quota 消耗只取决于 token 数 × 模型倍率 × 分组倍率，
//     **与汇率无关**——汇率只影响「1 元买多少 quota」。不调倍率，算力点单价就不动。
//  3. 不会与真实成本脱节。人工价目表若忘了跟随模型调价，那条会一直亏钱且无人发现。
//
// 代价是调模型倍率会同步改变套餐内的消耗速度。若某模型需要在套餐内稳定报价，
// 用套餐权益的消耗折扣系数锁定，而不是回头去建价目表。
//
// ⚠️ 命名红线：对外一律称「算力点」。「积分」已被营销赠送积分（User.PointsBalance）
// 占用，两个词在任何界面上都不可混用、不可互相简称——积分是签到/赠送来的，
// 算力点是套餐给的，来源、有效期、适用范围三者皆不同。

// QuotaPerComputePoint 表示「1 算力点」对应多少内部 quota unit。
//
// 默认 1 元 = 100 算力点（即 1 算力点 = 1 分钱），与积分同口径，运营心智统一。
// 推导：1 USD = QuotaPerUnit(=500000) quota = 7.3 元；1 算力点 = 0.01 元
//
//	=> QuotaPerComputePoint = 500000 / (7.3 * 100) = 500000 / 730 ≈ 684.93
var QuotaPerComputePoint = QuotaPerUnit / 730.0

// QuotaPerComputePointFunc 由上层配置包在 init 时注入，返回实时配置值。
// common 不能 import operation_setting（会循环），故用依赖倒置——与积分同构。
var QuotaPerComputePointFunc func() float64

func getQuotaPerComputePoint() float64 {
	if QuotaPerComputePointFunc != nil {
		if v := QuotaPerComputePointFunc(); v > 0 {
			return v
		}
	}
	return QuotaPerComputePoint
}

// ComputePointsToQuota 把「算力点数」换算为内部 quota unit（发放/输入边界用），向上取整。
//
// 用 Ceil 而非 Round：保证 QuotaToComputePoints(ComputePointsToQuota(n)) == n 精确往返。
// round 有约一半概率向下，叠加展示侧 floor 会出现「套餐配 50000 点、页面显示 49999」。
// ceil 多给的差额 < 1 quota unit，方向让利用户且不累积。
func ComputePointsToQuota(points int) int {
	if points == 0 {
		return 0
	}
	q := decimal.NewFromInt(int64(points)).Mul(decimal.NewFromFloat(getQuotaPerComputePoint()))
	return int(q.Ceil().IntPart())
}

// QuotaToComputePoints 把内部 quota unit 换算为「算力点数」（展示/对账边界用），向下取整。
// 算力点对用户永不显示小数；余额展示取保守方向。
func QuotaToComputePoints(quota int) int {
	qpc := getQuotaPerComputePoint()
	if quota == 0 || qpc <= 0 {
		return 0
	}
	p := decimal.NewFromInt(int64(quota)).Div(decimal.NewFromFloat(qpc))
	return int(p.Floor().IntPart())
}

// QuotaToComputePointsCeil 向上取整版本，用于消费结算：不足 1 算力点按 1 点计。
//
// 与展示侧的 floor 方向相反，理由同积分：套餐内的算力点是已售出的服务额度，
// 按点取整能让「剩余点数」这个数字始终是用户真正还能用的量，不会出现
// 「显示还剩 1 点却什么都生成不了」。
func QuotaToComputePointsCeil(quota int) int {
	qpc := getQuotaPerComputePoint()
	if quota <= 0 || qpc <= 0 {
		return 0
	}
	p := decimal.NewFromInt(int64(quota)).Div(decimal.NewFromFloat(qpc))
	return int(p.Ceil().IntPart())
}

// YuanToComputePoints 把「元」换算成算力点数，供运营配置套餐时的换算预览使用。
// 默认换算率下 1 元 = 100 点。
func YuanToComputePoints(yuan float64) int {
	qpc := getQuotaPerComputePoint()
	if yuan <= 0 || qpc <= 0 {
		return 0
	}
	// 1 元对应的 quota = QuotaPerUnit / 汇率；此处复用 quota 口径避免二次引入汇率常量
	quotaPerYuan := decimal.NewFromFloat(QuotaPerUnit).Div(decimal.NewFromFloat(getUSDExchangeRate()))
	q := decimal.NewFromFloat(yuan).Mul(quotaPerYuan)
	p := q.Div(decimal.NewFromFloat(qpc))
	return int(p.Floor().IntPart())
}

// USDExchangeRateFunc 由上层配置包注入实时汇率（operation_setting.Price）。
// 同样是依赖倒置：common 不能 import operation_setting。
var USDExchangeRateFunc func() float64

func getUSDExchangeRate() float64 {
	if USDExchangeRateFunc != nil {
		if v := USDExchangeRateFunc(); v > 0 {
			return v
		}
	}
	return 7.3
}
