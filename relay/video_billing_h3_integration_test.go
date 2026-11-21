package relay

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// H3 的按秒计价必须用**交付秒数**,不是请求秒数：
// 请求 1 s 的 H3 任务引擎实际出 39 帧 = 1.625 s，按 1 s 收就是白送 0.625 s 的 GPU。
//
// 这条用例把三个环节连起来验：引擎族识别（配置声明）→ 栅格换算（common）→
// per_second 计价（applyVideoPricing），任何一环退回请求值都会让它变红。
func TestApplyVideoPricing_H3PerSecondUsesDeliveredSeconds(t *testing.T) {
	const cfg = `{
	  "minimax-h3-fl2va": { "mode": "per_second", "per_second": { "768p": 0.10 } }
	}`
	require.NoError(t, ratio_setting.UpdateVideoPricingByJSONString(cfg))
	t.Cleanup(func() { _ = ratio_setting.UpdateVideoPricingByJSONString("") })

	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	previous, had := common.OptionMap["VideoModelConfig"]
	common.OptionMap["VideoModelConfig"] = `{"models":{"minimax-h3-fl2va":{"engine":"minimax-h3"}}}`
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		if had {
			common.OptionMap["VideoModelConfig"] = previous
		} else {
			delete(common.OptionMap, "VideoModelConfig")
		}
		common.OptionMapRWMutex.Unlock()
	})

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("task_request", relaycommon.TaskSubmitReq{Size: "768P", Duration: 1})

	info := &relaycommon.RelayInfo{OriginModelName: "minimax-h3-fl2va"}
	info.TaskRelayInfo = &relaycommon.TaskRelayInfo{}
	info.PriceData = types.PriceData{GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1}}

	require.True(t, applyVideoPricing(c, info), "per_second 应命中矩阵")
	require.NotNil(t, info.VideoBilling)
	require.Equal(t, 1, info.VideoBilling.Seconds, "请求秒数原样冻结")
	require.InDelta(t, 1.625, info.VideoBilling.DeliveredSeconds, 1e-9, "交付秒数按 39 帧换算")
	require.InDelta(t, 0.1625, info.PriceData.ModelPrice, 1e-9, "总价 = 每秒单价 × 交付秒数")
	require.True(t, info.PriceData.UsePrice)
}

// 非 H3 模型必须一个字节都不变：交付秒数为 0，总价仍按请求秒数。
func TestApplyVideoPricing_NonH3PerSecondKeepsRequestedSeconds(t *testing.T) {
	const cfg = `{
	  "ltx2.5": { "mode": "per_second", "per_second": { "1080p": 0.05 } }
	}`
	require.NoError(t, ratio_setting.UpdateVideoPricingByJSONString(cfg))
	t.Cleanup(func() { _ = ratio_setting.UpdateVideoPricingByJSONString("") })

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("task_request", relaycommon.TaskSubmitReq{Size: "1080p", Duration: 5})

	info := &relaycommon.RelayInfo{OriginModelName: "ltx2.5"}
	info.TaskRelayInfo = &relaycommon.TaskRelayInfo{}
	info.PriceData = types.PriceData{GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1}}

	require.True(t, applyVideoPricing(c, info))
	require.Zero(t, info.VideoBilling.DeliveredSeconds)
	require.InDelta(t, 0.25, info.PriceData.ModelPrice, 1e-9, "5 × 0.05")
}
