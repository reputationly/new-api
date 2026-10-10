package relay

import (
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"

	"github.com/stretchr/testify/require"
)

// 提交后按适配器回执调整倍率时，先要「除掉旧倍率恢复基础额度」再乘新倍率。
// 两步都会产生小数：截断会累积成少收（100000 ÷ 0.6 = 166666.66… 截断成 166666，
// 再 × 0.6 = 99999.6 截断成 99999，白白少收 1 quota），所以必须四舍五入。
func TestRecalcQuotaFromRatiosRounds(t *testing.T) {
	info := &relaycommon.RelayInfo{}
	info.PriceData = types.PriceData{
		Quota:       100000,
		OtherRatios: map[string]float64{"video_input": 0.6},
	}

	got := recalcQuotaFromRatios(info, map[string]float64{"video_input": 0.6})
	require.Equal(t, 100000, got, "倍率一除一乘之后必须回到原值")
}
