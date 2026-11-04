package common

import (
	"math"
)

// H3AspectRatioTolerance 把「像素串约分出来的比例」吸附到具名比例时允许的相对误差。
//
// 3% 不是拍的（见 relay/channel/task/gpustackplus/minimax_h3.go 的同名说明）：
// 官方 768P 尺寸表按 32 对齐取整，21:9 的真值几何算出来是 1536×658，取整成 1536×672
// 后约分是 16:7，对 21/9 偏 2.04%；9:21 同量级。容差小于它，这两档就永远吸附不上。
// 上限则由具名表里最挨近的两档决定：4:3 与 5:4 相距 6.7%，其一半 3.35% > 3%，
// 所以 3% 不会把相邻档判混。
const H3AspectRatioTolerance = 0.03

// H3AspectRatioForSize 给一个像素尺寸挑一个 **H3 认的** aspect_ratio。
//
// 为什么需要它（线上实测的真 bug）：`size` 走的是「精确约分」—— 1536×672 约分成 16:7，
// 而 16:7 既不在 H3 的 11 个具名值里，偏离 21:9 又有 2.04%，超出引擎自己的吸附容差，
// 于是引擎直接 400：`aspect_ratio must be one of … got '16:7'`。结果就是
// **21:9 / 9:21 这两档在体验区选得到、算得出画布，却发不出请求**；9:16 那档
// （768×1344 → 4:7，偏 1.59%）恰好落在引擎容差内，所以只有这两档炸。
//
// 吸附规则：
//   - 精确约分本身就是具名值 → 原样返回（16:9、4:3、5:4… 一个字节都不变）；
//   - 否则取相对误差最小的具名值，**且只在容差内**吸附；
//   - 容差外返回精确约分 —— 让引擎按原样拒。悄悄换一个比例发出去，出片画幅与客户
//     要的不一致且不报错，比直接失败更糟。
//
// 只影响 aspect_ratio 这个**校验/吸附字段**：画布仍由 size 决定（实测发
// 1344×768 + 7:4，引擎吸附到 16:9 后照旧出 1344×768）。
func H3AspectRatioForSize(size string) string {
	exact := AspectRatioFromSize(size)
	if exact == "" {
		return ""
	}
	if _, ok := h3NamedAspectRatioValues[exact]; ok {
		return exact
	}
	w, h, ok := DimsFromSize(size)
	if !ok || w <= 0 || h <= 0 {
		return exact
	}
	ratio := float64(w) / float64(h)
	best, bestErr := "", math.Inf(1)
	for name, v := range h3NamedAspectRatioValues {
		e := math.Abs(ratio-v) / v
		if e < bestErr || (e == bestErr && name < best) {
			best, bestErr = name, e
		}
	}
	if best == "" || bestErr > H3AspectRatioTolerance {
		return exact
	}
	return best
}
