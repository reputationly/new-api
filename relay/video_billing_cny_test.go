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

// 视频计费矩阵的人民币口径（设计文档 §2.3 E7 / §3.1）：
//
//	per_call：quota = 单价(¥/次) × QuotaPerYuan × 分组
//	per_second：quota = 单价(¥/秒) × 秒数 × QuotaPerYuan × 分组
//
// 两条路径都不读 QuotaPerUnit、不读汇率。
const videoCnyCfg = `{
  "e7-percall": { "mode": "per_call",   "per_call":   { "720p": { "5": 0.73 } } },
  "e7-persec":  { "mode": "per_second", "per_second": { "720p": 0.73 } }
}`

func videoCNYCtx(t *testing.T, model string, req relaycommon.TaskSubmitReq, groupRatio float64) (*gin.Context, *relaycommon.RelayInfo) {
	t.Helper()
	require.NoError(t, ratio_setting.UpdateVideoPricingByJSONString(videoCnyCfg))
	t.Cleanup(func() { _ = ratio_setting.UpdateVideoPricingByJSONString("") })

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("task_request", req)

	info := &relaycommon.RelayInfo{OriginModelName: model}
	info.TaskRelayInfo = &relaycommon.TaskRelayInfo{}
	info.PriceData = types.PriceData{
		GroupRatioInfo: types.GroupRatioInfo{GroupRatio: groupRatio},
	}
	return c, info
}

// E7：视频按秒 ¥0.73/秒、5 秒、分组 0.8 → 0.73 × 5 × 10⁸ × 0.8 = 292,000,000 quota。
func TestApplyVideoPricingPerSecondE7(t *testing.T) {
	c, info := videoCNYCtx(t, "e7-persec",
		relaycommon.TaskSubmitReq{Size: "720p", Duration: 5}, 0.8)

	require.True(t, applyVideoPricing(c, info))
	require.Equal(t, ratio_setting.VideoPriceModePerSecond, info.VideoBilling.Mode)
	require.InDelta(t, 0.73, info.VideoBilling.UnitPrice, 1e-9, "冻结的是每秒单价")
	require.InDelta(t, 0.73*5, info.PriceData.ModelPrice, 1e-9)
	require.Equal(t, 292_000_000, info.PriceData.Quota)
	require.InDelta(t, 2.92, float64(info.PriceData.Quota)/float64(common.QuotaPerYuan), 1e-9)
}

// 视频按次 ¥0.73/次、分组 0.8 → 0.73 × 10⁸ × 0.8 = 58,400,000 quota。
func TestApplyVideoPricingPerCallCNY(t *testing.T) {
	c, info := videoCNYCtx(t, "e7-percall",
		relaycommon.TaskSubmitReq{Size: "720p", Duration: 5}, 0.8)

	require.True(t, applyVideoPricing(c, info))
	require.Equal(t, ratio_setting.VideoPriceModePerCall, info.VideoBilling.Mode)
	require.InDelta(t, 0.73, info.PriceData.ModelPrice, 1e-9)
	require.Equal(t, 58_400_000, info.PriceData.Quota)
}

// 记账单位与汇率都不参与：改 QuotaPerUnit 不影响矩阵定价。
func TestApplyVideoPricingIgnoresQuotaPerUnit(t *testing.T) {
	original := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = original })

	for _, qpu := range []float64{7_300_000, 730_000_000} {
		common.QuotaPerUnit = qpu
		c, info := videoCNYCtx(t, "e7-persec",
			relaycommon.TaskSubmitReq{Size: "720p", Duration: 5}, 0.8)
		require.True(t, applyVideoPricing(c, info))
		require.Equal(t, 292_000_000, info.PriceData.Quota, "QuotaPerUnit=%v", qpu)
	}
}
