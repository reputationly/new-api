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

const videoMatrixCfg = `{
  "doubao-seedance-2-0-260128": {
    "mode": "token",
    "token": {
      "720p":  { "with_video": 4.6027, "without_video": 7.5616 },
      "1080p": { "with_video": 5.0959, "without_video": 8.3836 }
    }
  },
  "kling-v2-master": {
    "mode": "per_call",
    "per_call": { "720p": { "5": 0.2, "10": 0.4 } }
  }
}`

func videoPricingCtx(t *testing.T, model string, req relaycommon.TaskSubmitReq) (*gin.Context, *relaycommon.RelayInfo) {
	t.Helper()
	require.NoError(t, ratio_setting.UpdateVideoPricingByJSONString(videoMatrixCfg))
	t.Cleanup(func() { _ = ratio_setting.UpdateVideoPricingByJSONString("") })

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("task_request", req)

	info := &relaycommon.RelayInfo{OriginModelName: model}
	info.TaskRelayInfo = &relaycommon.TaskRelayInfo{}
	info.PriceData = types.PriceData{
		GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1},
	}
	return c, info
}

func TestApplyVideoPricing_TokenModeFreezesUnitPrice(t *testing.T) {
	c, info := videoPricingCtx(t, "doubao-seedance-2-0-260128",
		relaycommon.TaskSubmitReq{Size: "1920x1080"})

	require.True(t, applyVideoPricing(c, info))
	require.NotNil(t, info.VideoBilling)
	require.Equal(t, ratio_setting.VideoPriceModeToken, info.VideoBilling.Mode)
	require.Equal(t, "1080p", info.VideoBilling.Resolution)
	require.False(t, info.VideoBilling.HasVideoInput)
	require.InDelta(t, 8.3836, info.VideoBilling.UnitPrice, 1e-9)
}

// UsePrice 若为 true，controller 会把 PerCallBilling 也置为 true，
// 轮询期的 settleTaskBillingOnComplete 第 0 步就会跳过全部差额结算——
// 结果是 720p 与 1080p 收一样的钱。这条断言守住那道门。
func TestApplyVideoPricing_TokenModeForcesUsePriceOff(t *testing.T) {
	c, info := videoPricingCtx(t, "doubao-seedance-2-0-260128",
		relaycommon.TaskSubmitReq{Size: "720p"})
	info.PriceData.UsePrice = true // 模型配的是「固定价格」

	require.True(t, applyVideoPricing(c, info))
	require.False(t, info.PriceData.UsePrice)
}

func TestApplyVideoPricing_HasVideoInputPicksCheaperColumn(t *testing.T) {
	c, info := videoPricingCtx(t, "doubao-seedance-2-0-260128", relaycommon.TaskSubmitReq{
		Size:     "720p",
		Metadata: map[string]any{"reference_videos": []any{"https://a/v.mp4"}},
	})

	require.True(t, applyVideoPricing(c, info))
	require.True(t, info.VideoBilling.HasVideoInput)
	require.InDelta(t, 4.6027, info.VideoBilling.UnitPrice, 1e-9)
}

func TestApplyVideoPricing_PerCallSetsFinalQuota(t *testing.T) {
	c, info := videoPricingCtx(t, "kling-v2-master",
		relaycommon.TaskSubmitReq{Size: "720p", Duration: 10})

	require.True(t, applyVideoPricing(c, info))
	require.True(t, info.PriceData.UsePrice)
	require.InDelta(t, 0.4, info.PriceData.ModelPrice, 1e-9)
	require.Equal(t, int(0.4*common.QuotaPerUnit), info.PriceData.Quota)
}

func TestApplyVideoPricing_PerCallScalesByGroupRatio(t *testing.T) {
	c, info := videoPricingCtx(t, "kling-v2-master",
		relaycommon.TaskSubmitReq{Size: "720p", Duration: 5})
	info.PriceData.GroupRatioInfo.GroupRatio = 0.5

	require.True(t, applyVideoPricing(c, info))
	require.Equal(t, int(0.2*common.QuotaPerUnit*0.5), info.PriceData.Quota)
}

// 客户端只给 seconds 时，kling 等按次渠道上游会用自己的默认值（5 秒），
// 按 seconds 查表就会「按 10 秒收费、只出 5 秒」。必须未命中而不是照 10 秒收。
func TestApplyVideoPricing_PerCallIgnoresSecondsField(t *testing.T) {
	c, info := videoPricingCtx(t, "kling-v2-master",
		relaycommon.TaskSubmitReq{Size: "720p", Seconds: "10"})

	require.False(t, applyVideoPricing(c, info))
	require.Nil(t, info.VideoBilling)
	require.False(t, info.PriceData.UsePrice)
}

// seconds 与 duration 都给时，以上游真正读的 duration 为准。
func TestApplyVideoPricing_PerCallPrefersDurationOverSeconds(t *testing.T) {
	c, info := videoPricingCtx(t, "kling-v2-master",
		relaycommon.TaskSubmitReq{Size: "720p", Seconds: "10", Duration: 5})

	require.True(t, applyVideoPricing(c, info))
	require.Equal(t, 5, info.VideoBilling.Seconds)
	require.InDelta(t, 0.2, info.PriceData.ModelPrice, 1e-9)
}

// ModelPriceHelperPerCall 会在旧的 ModelPrice/ModelRatio 为 0 时置 FreeModel=true，
// 而 relay_task.go 拿它当预扣闸。矩阵命中即单价 > 0，必须把这个标志推翻——
// 不推翻的话 per_call 会「跳过预扣 + 结算早退」变成永久免费，
// token 会「跳过预扣但照常补扣」造成透支。
func TestApplyVideoPricing_RecomputesFreeModel(t *testing.T) {
	for _, mode := range []struct {
		name  string
		model string
		req   relaycommon.TaskSubmitReq
	}{
		{"token", "doubao-seedance-2-0-260128", relaycommon.TaskSubmitReq{Size: "720p"}},
		{"per_call", "kling-v2-master", relaycommon.TaskSubmitReq{Size: "720p", Duration: 5}},
	} {
		t.Run(mode.name, func(t *testing.T) {
			c, info := videoPricingCtx(t, mode.model, mode.req)
			info.PriceData.FreeModel = true // 旧价格为 0 时 ModelPriceHelperPerCall 的结论

			require.True(t, applyVideoPricing(c, info))
			require.False(t, info.PriceData.FreeModel, "矩阵有正价，这一单不该被当成免费")
		})
	}
}

// 分组倍率为 0 是真免费，矩阵不该把它翻成收费。
func TestApplyVideoPricing_ZeroGroupRatioStaysFree(t *testing.T) {
	c, info := videoPricingCtx(t, "doubao-seedance-2-0-260128",
		relaycommon.TaskSubmitReq{Size: "720p"})
	info.PriceData.GroupRatioInfo.GroupRatio = 0

	require.True(t, applyVideoPricing(c, info))
	require.True(t, info.PriceData.FreeModel)
}

// 按次矩阵的格子里就是终价，不该逼运营再补一个假的 legacy ModelPrice；
// token 模式则仍需 ModelRatio 当预扣锚点，不能放行。
func TestVideoPerCallPriceable(t *testing.T) {
	cases := []struct {
		name  string
		model string
		req   relaycommon.TaskSubmitReq
		want  bool
	}{
		{"按次命中", "kling-v2-master", relaycommon.TaskSubmitReq{Size: "720p", Duration: 5}, true},
		{"按次缺该秒数", "kling-v2-master", relaycommon.TaskSubmitReq{Size: "720p", Duration: 7}, false},
		{"按次缺 duration", "kling-v2-master", relaycommon.TaskSubmitReq{Size: "720p"}, false},
		{"token 模式不放行", "doubao-seedance-2-0-260128", relaycommon.TaskSubmitReq{Size: "720p"}, false},
		{"未配矩阵", "sora-2", relaycommon.TaskSubmitReq{Size: "720p", Duration: 5}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, info := videoPricingCtx(t, tc.model, tc.req)
			require.Equal(t, tc.want, videoPerCallPriceable(c, info))
		})
	}
}

func TestVideoPerCallPriceable_NilSafe(t *testing.T) {
	c, _ := videoPricingCtx(t, "kling-v2-master", relaycommon.TaskSubmitReq{})
	require.False(t, videoPerCallPriceable(c, nil))
}

// 未命中一律返回 false，调用方据此走改造前的 EstimateBilling 路径——
// 改动因此是惰性的：没配的模型行为一个字节不变。
func TestApplyVideoPricing_Misses(t *testing.T) {
	cases := []struct {
		name  string
		model string
		req   relaycommon.TaskSubmitReq
	}{
		{"模型未配置", "sora-2", relaycommon.TaskSubmitReq{Size: "720p"}},
		{"分辨率未配置", "doubao-seedance-2-0-260128", relaycommon.TaskSubmitReq{Size: "4k"}},
		{"取不到分辨率", "doubao-seedance-2-0-260128", relaycommon.TaskSubmitReq{Size: "16:9"}},
		{"按次表缺该秒数", "kling-v2-master", relaycommon.TaskSubmitReq{Size: "720p", Duration: 7}},
		{"按次缺 duration", "kling-v2-master", relaycommon.TaskSubmitReq{Size: "720p"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, info := videoPricingCtx(t, tc.model, tc.req)
			require.False(t, applyVideoPricing(c, info))
			require.Nil(t, info.VideoBilling)
		})
	}
}

func TestApplyVideoPricing_NoTaskRequestIsSafe(t *testing.T) {
	require.NoError(t, ratio_setting.UpdateVideoPricingByJSONString(videoMatrixCfg))
	t.Cleanup(func() { _ = ratio_setting.UpdateVideoPricingByJSONString("") })

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{OriginModelName: "doubao-seedance-2-0-260128"}
	info.TaskRelayInfo = &relaycommon.TaskRelayInfo{}

	require.False(t, applyVideoPricing(c, info))
	require.False(t, applyVideoPricing(c, nil))
}
