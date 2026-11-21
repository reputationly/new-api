package ratio_setting

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 没有配置任何时段规则时，解析结果必须与改造前**逐位一致**：
// Layer 4 整段不参与，TimeFactors 为 nil，Final 就是 NormalRatio。
func TestResolveGroupRatio_NoTimeRulesUnchanged(t *testing.T) {
	seedRatios(t, `{"default":1}`, `{}`, `{"default":{"m":{"mode":"multiply","value":0.5}}}`)
	seedTimeRatio(t, `{}`)

	res := ResolveGroupRatioAt("default", "default", "m", time.Now())
	require.Equal(t, res.NormalRatio, res.Final, "无规则时 Final 就是 NormalRatio")
	require.Zero(t, res.TimeValue, "没命中时段，TimeValue 保持零值")
	require.Nil(t, res.TimeFactors, "没命中时段不下发任何分量系数")

	// 展示侧同理：没有规则时压根没有 view
	_, ok := ResolveTimeRatioView("default", "m", time.Now(), res)
	require.False(t, ok, "没有规则就不该有分时视图")
}
