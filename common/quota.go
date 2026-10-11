package common

import "math"

func GetTrustQuota() int {
	return int(10 * QuotaPerUnit)
}

// MaxTokenQuota 返回令牌额度（RemainQuota）允许的最大值，语义是「1e9 美元对应的额度」。
// 它只作为明显不合理值的兜底，具体数值不重要，重要的是不溢出。
//
// QuotaPerUnit 是运行时可改的 option（运营会按汇率调整美元换算），所以这里必须先判范围
// 再转换：直接写 int(1000000000 * QuotaPerUnit) 时，只要 QPU > 9.22e9，float64 → int 的
// 越界转换行为就是**实现相关**的 —— x86-64 上得到 MinInt64（**负数**，令牌创建与更新会
// 把所有正额度都拒掉，报错里的 Max 也是负数），ARM64 上则饱和成 MaxInt64（看不出问题）。
// 生产镜像跑在 x86-64 上，所以这曾是真实故障；显式钳制后两种架构行为一致。
func MaxTokenQuota() int64 {
	const dollars = 1_000_000_000
	value := float64(dollars) * QuotaPerUnit
	if value >= float64(math.MaxInt64) {
		return math.MaxInt64
	}
	return int64(value)
}
