package relay

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
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
  },
  "ltx2.5": {
    "mode": "per_second",
    "per_second": { "544p": 0.01, "1080p": 0.05, "2k": 0.1 }
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

// token 模式查不到单价时仍要冻结 Mode——它是「上游返回用量计费」的判据。
//
// 图生视频、参考生视频按设计不下发 size（画幅跟随输入图），供应商的 resolution
// 本就是可选参数、ratio=adaptive 时画幅跟随输入、draft 还会强制 480p。不冻的话
// 这些玩法整单退回改造前的路径：使用日志变回「一条消费 + 一条退款」，且按
// ModelRatio 而非矩阵单价收费，与供应商账单对不上。
func TestApplyVideoPricing_TokenModeFreezesWithoutResolution(t *testing.T) {
	cases := map[string]relaycommon.TaskSubmitReq{
		"不下发 size（图生视频）": {},
		"只有宽高比":           {Size: "16:9"},
		"档位未配价":           {Size: "4k"},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			c, info := videoPricingCtx(t, "doubao-seedance-2-0-260128", req)

			// 返回 false 是刻意的：单价未知，预扣必须继续走 EstimateBilling + OtherRatios
			// 那条老锚点，否则预扣会塌成一个不含时长维度的数。
			require.False(t, applyVideoPricing(c, info))

			require.NotNil(t, info.VideoBilling, "必须冻结，否则延迟记账不生效")
			require.Equal(t, ratio_setting.VideoPriceModeToken, info.VideoBilling.Mode)
			require.Zero(t, info.VideoBilling.UnitPrice, "单价留 0，由结算侧按回执补查")
		})
	}
}

// 参考生视频带参考视频时，冻结的 has_video_input 必须为真——结算侧靠它选
// with_video 那一列。判据与豆包适配器拼 video_url 的键完全同源。
func TestApplyVideoPricing_TokenModeFreezesVideoInputWithoutResolution(t *testing.T) {
	c, info := videoPricingCtx(t, "doubao-seedance-2-0-260128", relaycommon.TaskSubmitReq{
		Metadata: map[string]any{"reference_videos": []any{"https://a/v.mp4"}},
	})

	require.False(t, applyVideoPricing(c, info))
	require.NotNil(t, info.VideoBilling)
	require.True(t, info.VideoBilling.HasVideoInput)
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

// ── per_second ────────────────────────────────────────────────────────────
//
// 时长连续的自建模型（minimax-h3 4~15 秒、LTX-2.5 1080p 到 17.7 秒）用这条路。
// 结算口径与 per_call 完全一致：提交时定死终价，轮询期不差额结算。

func TestApplyVideoPricing_PerSecondMultipliesByDuration(t *testing.T) {
	c, info := videoPricingCtx(t, "ltx2.5",
		relaycommon.TaskSubmitReq{Size: "1080P", Duration: 12})

	require.True(t, applyVideoPricing(c, info))
	require.Equal(t, ratio_setting.VideoPriceModePerSecond, info.VideoBilling.Mode)
	require.Equal(t, "1080p", info.VideoBilling.Resolution)
	require.Equal(t, 12, info.VideoBilling.Seconds)
	// UnitPrice 冻的是**每秒单价**，不是总价——日志与对账靠 unit_price × seconds 反算
	require.InDelta(t, 0.05, info.VideoBilling.UnitPrice, 1e-9)

	require.True(t, info.PriceData.UsePrice)
	require.InDelta(t, 0.05*12, info.PriceData.ModelPrice, 1e-9)
	require.Equal(t, int(0.05*12*common.QuotaPerUnit), info.PriceData.Quota)
}

// 同一分辨率下时长翻倍，价格必须跟着翻倍——这正是改造前做不到的：
// 无论 4 秒还是 15 秒都收固定的 model_price。
func TestApplyVideoPricing_PerSecondScalesWithDuration(t *testing.T) {
	quotaFor := func(sec int) int {
		c, info := videoPricingCtx(t, "ltx2.5",
			relaycommon.TaskSubmitReq{Size: "1080p", Duration: sec})
		require.True(t, applyVideoPricing(c, info))
		return info.PriceData.Quota
	}
	require.Equal(t, 2*quotaFor(5), quotaFor(10))
}

// 2K 是 LTX 最贵的档，也是 VideoResolutionTier 的档位正则曾经漏掉的那个。
// 漏掉时返回空行名 → 未命中 → 静默回退固定单价，最贵的档反而收得最少。
func TestApplyVideoPricing_PerSecond2KResolves(t *testing.T) {
	c, info := videoPricingCtx(t, "ltx2.5",
		relaycommon.TaskSubmitReq{Size: "2K", Duration: 10})

	require.True(t, applyVideoPricing(c, info))
	require.Equal(t, "2k", info.VideoBilling.Resolution)
	require.InDelta(t, 0.1*10, info.PriceData.ModelPrice, 1e-9)
}

func TestApplyVideoPricing_PerSecondScalesByGroupRatio(t *testing.T) {
	c, info := videoPricingCtx(t, "ltx2.5",
		relaycommon.TaskSubmitReq{Size: "544p", Duration: 8})
	info.PriceData.GroupRatioInfo.GroupRatio = 0.5

	require.True(t, applyVideoPricing(c, info))
	require.Equal(t, int(0.01*8*common.QuotaPerUnit*0.5), info.PriceData.Quota)
}

// 秒数缺失时不能按 0 秒算出 0 元——那等于白送。必须未命中、回退旧路径。
func TestApplyVideoPricing_PerSecondMissingDurationMisses(t *testing.T) {
	for _, req := range []relaycommon.TaskSubmitReq{
		{Size: "1080p"},               // 没给 duration
		{Size: "1080p", Duration: 0},  // 显式 0
		{Size: "1080p", Seconds: "8"}, // 只给 seconds：上游读的是 duration，会按默认时长出片
		{Size: "720p", Duration: 8},   // 未配的档位不回退到相邻档
		{Size: "16:9", Duration: 8},   // 比例形态不含分辨率信息
	} {
		c, info := videoPricingCtx(t, "ltx2.5", req)
		require.Falsef(t, applyVideoPricing(c, info), "req=%+v", req)
		require.Zerof(t, info.PriceData.Quota, "req=%+v", req)
	}
}

// per_second 必须和 per_call 一样能脱离 legacy 价格独立定价，
// 否则模型没配 model_price 时 RelayTaskSubmit 会以「未配置价格」拒掉。
func TestVideoPerCallPriceable_PerSecond(t *testing.T) {
	c, info := videoPricingCtx(t, "ltx2.5",
		relaycommon.TaskSubmitReq{Size: "1080p", Duration: 6})
	require.True(t, videoPerCallPriceable(c, info))

	c, info = videoPricingCtx(t, "ltx2.5", relaycommon.TaskSubmitReq{Size: "1080p"})
	require.False(t, videoPerCallPriceable(c, info), "秒数缺失时不能放行")
}

// 免费分组仍然免费——FreeModel 只取决于分组倍率，与矩阵是否命中无关。
func TestApplyVideoPricing_PerSecondZeroGroupRatioStaysFree(t *testing.T) {
	c, info := videoPricingCtx(t, "ltx2.5",
		relaycommon.TaskSubmitReq{Size: "1080p", Duration: 10})
	info.PriceData.GroupRatioInfo.GroupRatio = 0

	require.True(t, applyVideoPricing(c, info))
	require.True(t, info.PriceData.FreeModel)
	require.Zero(t, info.PriceData.Quota)
}

// ── D：秒数的渠道分叉 ──────────────────────────────────────────────────
//
// gpustackplus（自建：LTX-2.5 / MiniMax-H3 / wan2.2 系列）在 adaptor.go:513-517
// 明确把 Seconds 当 Duration 的回落，而 kling/vidu/jimeng 完全忽略 Seconds。
// 计费侧只认 Duration 的话，自建模型会「引擎按 10 秒出片、计费拿到 0 秒、
// 矩阵未命中、按固定价收」——时长越长亏越多。
//
// 判据必须是渠道而不是模型名或引擎族：wan2.2 系列同样走 gpustackplus 的回落，
// 但它没有声明 engine（isMiniMaxH3 / isLTX25 都不命中），按引擎族白名单会漏掉它。

func videoPricingCtxOnPlatform(t *testing.T, channelType int, model string, req relaycommon.TaskSubmitReq) (*gin.Context, *relaycommon.RelayInfo) {
	t.Helper()
	c, info := videoPricingCtx(t, model, req)
	c.Set("channel_type", channelType)
	return c, info
}

func TestApplyVideoPricing_PerSecondHonorsSecondsOnSelfHosted(t *testing.T) {
	c, info := videoPricingCtxOnPlatform(t, constant.ChannelTypeGPUStackPlus, "ltx2.5",
		relaycommon.TaskSubmitReq{Size: "1080p", Seconds: "10"})

	require.True(t, applyVideoPricing(c, info))
	require.Equal(t, 10, info.VideoBilling.Seconds)
	require.InDelta(t, 0.05*10, info.PriceData.ModelPrice, 1e-9)
}

// 同一份请求换个渠道就必须落空——kling 上游按自己的默认 5 秒出片，
// 认了 Seconds 就是按 10 秒收、只出 5 秒。
func TestApplyVideoPricing_PerCallIgnoresSecondsOnThirdParty(t *testing.T) {
	c, info := videoPricingCtxOnPlatform(t, constant.ChannelTypeKling, "kling-v2-master",
		relaycommon.TaskSubmitReq{Size: "720p", Seconds: "10"})

	require.False(t, applyVideoPricing(c, info))
	require.Zero(t, info.PriceData.Quota)
}

// Duration 永远优先，与渠道无关
func TestApplyVideoPricing_DurationBeatsSecondsEverywhere(t *testing.T) {
	for _, ch := range []int{constant.ChannelTypeGPUStackPlus, constant.ChannelTypeKling} {
		c, info := videoPricingCtxOnPlatform(t, ch, "ltx2.5",
			relaycommon.TaskSubmitReq{Size: "1080p", Duration: 6, Seconds: "20"})
		require.Truef(t, applyVideoPricing(c, info), "channelType=%d", ch)
		require.Equalf(t, 6, info.VideoBilling.Seconds, "channelType=%d", ch)
	}
}

// 渠道未知时退回保守口径：宁可未命中走旧路径，也不冒多收的风险
func TestApplyVideoPricing_PerSecondUnknownPlatformStaysConservative(t *testing.T) {
	c, info := videoPricingCtx(t, "ltx2.5",
		relaycommon.TaskSubmitReq{Size: "1080p", Seconds: "10"})
	require.False(t, applyVideoPricing(c, info))
}

// videoPerCallPriceable 与 applyVideoPricing 必须用同一套秒数口径，
// 否则会「放行了预扣闸却定不出价」。
func TestVideoPerCallPriceable_SecondsMatchesApply(t *testing.T) {
	c, info := videoPricingCtxOnPlatform(t, constant.ChannelTypeGPUStackPlus, "ltx2.5",
		relaycommon.TaskSubmitReq{Size: "1080p", Seconds: "10"})
	require.True(t, videoPerCallPriceable(c, info))

	c, info = videoPricingCtxOnPlatform(t, constant.ChannelTypeKling, "ltx2.5",
		relaycommon.TaskSubmitReq{Size: "1080p", Seconds: "10"})
	require.False(t, videoPerCallPriceable(c, info))
}
