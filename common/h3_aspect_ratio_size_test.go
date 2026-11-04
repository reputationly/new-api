package common

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 像素尺寸 → H3 的 aspect_ratio。
//
// 线上实测的真 bug：21:9 与 9:21 这两档在体验区选得到、画布也算得出，但请求发不出去 ——
// 官方 768P 尺寸表按 32 对齐取整，21:9 的真值几何是 1536×658，取整成 1536×672 后
// 精确约分是 16:7：既不在 H3 的 11 个具名值里，偏离 21/9 又有 2.04%，超出引擎吸附容差
// → 引擎 400 `aspect_ratio must be one of … got '16:7'`。
// 9:16 那档（768×1344 → 4:7，偏 1.59%）恰好落在容差内，所以只有这两档炸。

// 官方 768P 尺寸表：11 个比例逐个都必须映射到自己。
// 这条表是回归网——任何一档被吸附错了，客户选 A 会拿到 B。
func TestH3AspectRatioForSizeOfficial768PTable(t *testing.T) {
	table := map[string]string{
		"1536x672": "21:9",
		"1344x768": "16:9",
		"1152x768": "3:2",
		"1024x768": "4:3",
		"960x768":  "5:4",
		"768x768":  "1:1",
		"768x960":  "4:5",
		"768x1024": "3:4",
		"768x1152": "2:3",
		"768x1344": "9:16",
		"672x1536": "9:21",
	}
	for size, want := range table {
		t.Run(size, func(t *testing.T) {
			require.Equal(t, want, H3AspectRatioForSize(size))
		})
	}
}

// 精确约分本身就是具名值的，必须原样返回（不能因为"最近"就漂到别处）。
func TestH3AspectRatioForSizeKeepsExactNamed(t *testing.T) {
	require.Equal(t, "4:3", H3AspectRatioForSize("1024x768"))
	require.Equal(t, "16:9", H3AspectRatioForSize("1280x720"), "未取整的 16:9 也是 16:9")
	require.Equal(t, "1:1", H3AspectRatioForSize("512x512"))
}

// 容差之外**不吸附**：宁可让引擎按原样拒，也不把离谱的画幅悄悄改成某个具名比例
// 发出去 —— 那样出片画幅与客户要的不一致且不报错。
func TestH3AspectRatioForSizeDoesNotSnapFarOff(t *testing.T) {
	require.Equal(t, "5:2", H3AspectRatioForSize("1600x640"), "2.5:1 离 21:9(2.33) 超 3%")
	require.Equal(t, "10:1", H3AspectRatioForSize("1600x160"))
}

// 非法/非像素串返回空串，调用方据此不下发 aspect_ratio。
func TestH3AspectRatioForSizeInvalid(t *testing.T) {
	for _, size := range []string{"", "768P", "abc", "0x100", "100x0"} {
		require.Equal(t, "", H3AspectRatioForSize(size), size)
	}
}
