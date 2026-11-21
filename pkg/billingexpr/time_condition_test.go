package billingexpr

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 「用表达式按时间段改价」的可行性哨兵（DeepSeek 官方口径）：
//   高峰（工作日 09-12、14-18）：输入 ¥2 / 输出 ¥8 / 缓存命中 ¥0.04
//   空闲（其余时间）：       输入 ¥1 / 输出 ¥4 / 缓存命中 ¥0.02
//
// 这里锁三件事：
//   1) 表达式能以 hour()/weekday() 为条件编译并求值；
//   2) 当前时刻命中哪一档，与手动判断一致（matched_tier）；
//   3) 两档价目此刻恰好是 2:1（官方"空闲=高峰一半"）。
const deepseekFlashExpr = `v2:(weekday("UTC") >= 1 && weekday("UTC") <= 5 && ((hour("UTC") >= 9 && hour("UTC") < 12) || (hour("UTC") >= 14 && hour("UTC") < 18))) ? tier("peak", p * 2 + c * 8 + cr * 0.04) : tier("offpeak", p * 1 + c * 4 + cr * 0.02)`

func TestDeepSeekPeakOffPeakExpression(t *testing.T) {
	params := TokenParams{P: 1_000_000, C: 1_000_000, CR: 200_000, Len: 1_200_000}

	cost, trace, err := RunExpr(deepseekFlashExpr, params)
	require.NoError(t, err, "表达式必须能编译并求值")
	require.NotZero(t, cost)

	now := time.Now().UTC()
	h, wd := now.Hour(), int(now.Weekday())
	peak := wd >= 1 && wd <= 5 && ((h >= 9 && h < 12) || (h >= 14 && h < 18))
	if peak {
		require.Equal(t, "peak", trace.MatchedTier)
	} else {
		require.Equal(t, "offpeak", trace.MatchedTier)
	}

	// 用「恒真/恒假」的两个变体分别取出两档成本，验证 2:1
	peakExpr := "v2:hour(\"UTC\") >= 0 ? tier(\"peak\", p * 2 + c * 8 + cr * 0.04) : 0"
	offExpr := "v2:hour(\"UTC\") >= 0 ? tier(\"offpeak\", p * 1 + c * 4 + cr * 0.02) : 0"
	peakCost, _, err1 := RunExpr(peakExpr, params)
	offCost, _, err2 := RunExpr(offExpr, params)
	require.NoError(t, err1)
	require.NoError(t, err2)
	require.Greater(t, peakCost, 0.0)
	require.InDelta(t, 2.0, peakCost/offCost, 1e-9, "空闲价必须是高峰的一半")
}

