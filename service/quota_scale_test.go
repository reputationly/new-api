package service

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 单位口径回归护栏（设计文档 §2.2）：
//   - 倍率路径：quota = tokens × 倍率 × QuotaPerMillionTokens（1000），倍率是 ¥/百万 tokens；
//   - 价格路径：quota = 人民币价 × QuotaPerYuan（10⁹）；
//   - 两条路径都不读 QuotaPerUnit、不读汇率。改 QPU 只影响美元支付与美元展示。
func setQuotaPerUnit(t *testing.T, qpu float64) {
	t.Helper()
	original := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = original })
	common.QuotaPerUnit = qpu
}

func newTextQuotaCtx(t *testing.T) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	return ctx
}

func textRelayInfo(priceData types.PriceData) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatOpenAI,
		OriginModelName: "scale-test-model",
		PriceData:       priceData,
		StartTime:       time.Now(),
	}
}

// E1：deepseek 输入 ¥2/百万、输出 ×4、分组 0.5，prompt 199 / completion 4096。
//
//	(199 + 4096×4) × 2 × 1000 × 0.5 = 16,583,000 quota = ¥0.016583
//
// 同一场景在 V1 单位（1 quota = ¥1e-6）下是 16,583 quota，人民币金额相同。
func TestCalculateTextQuotaSummaryE1Deepseek(t *testing.T) {
	ctx := newTextQuotaCtx(t)
	usage := &dto.Usage{PromptTokens: 199, CompletionTokens: 4096}
	relayInfo := textRelayInfo(types.PriceData{
		ModelRatio:      2, // ¥2/百万 tokens
		CompletionRatio: 4,
		GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: 0.5},
	})

	setQuotaPerUnit(t, 7_300_000_000)
	require.Equal(t, 16_583_000, calculateTextQuotaSummary(ctx, relayInfo, usage).Quota)

	// 记账单位不参与计费：QPU 变，结果不变。
	setQuotaPerUnit(t, 7_300_000)
	require.Equal(t, 16_583_000, calculateTextQuotaSummary(ctx, relayInfo, usage).Quota)
}

// 倍率路径的分场景覆盖：期望值直接写成 quotaTokens × 倍率 × 1000，并在多个 QPU 下重复，
// 钉住「倍率路径与 QuotaPerUnit 无关」。倍率统一取 1（¥1/百万 tokens）。
func TestCalculateTextQuotaSummaryRatioPathThousandPerRatio(t *testing.T) {
	ctx := newTextQuotaCtx(t)

	cases := []struct {
		name      string
		usage     dto.Usage
		priceData types.PriceData
		want      int
	}{
		{
			name:      "纯文本",
			usage:     dto.Usage{PromptTokens: 1000, CompletionTokens: 0},
			priceData: types.PriceData{ModelRatio: 1, CompletionRatio: 4, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1}},
			want:      1_000_000, // 1000 × 1 × 1000
		},
		{
			name:  "补全倍率",
			usage: dto.Usage{PromptTokens: 100, CompletionTokens: 100},
			priceData: types.PriceData{
				ModelRatio: 1, CompletionRatio: 2, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1},
			},
			want: 300_000, // (100 + 100×2) × 1000
		},
		{
			name:  "缓存命中",
			usage: dto.Usage{PromptTokens: 1000, PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 200}},
			priceData: types.PriceData{
				ModelRatio: 1, CompletionRatio: 4, CacheRatio: 0.5,
				GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1},
			},
			want: 900_000, // ((1000-200) + 200×0.5) × 1000
		},
		{
			name:  "图片 token",
			usage: dto.Usage{PromptTokens: 1000, PromptTokensDetails: dto.InputTokenDetails{ImageTokens: 100}},
			priceData: types.PriceData{
				ModelRatio: 1, CompletionRatio: 4, ImageRatio: 2,
				GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1},
			},
			want: 1_100_000, // ((1000-100) + 100×2) × 1000
		},
		{
			name: "Claude 语义 5m/1h 缓存创建",
			usage: dto.Usage{
				PromptTokens:                1000,
				UsageSemantic:               "anthropic",
				ClaudeCacheCreation5mTokens: 100,
				ClaudeCacheCreation1hTokens: 100,
			},
			priceData: types.PriceData{
				ModelRatio: 1, CompletionRatio: 4,
				CacheCreationRatio: 1.25, CacheCreation5mRatio: 1.25, CacheCreation1hRatio: 2,
				GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1},
			},
			want: 1_325_000, // (1000 + 100×1.25 + 100×2) × 1000
		},
		{
			name:  "OtherRatios",
			usage: dto.Usage{PromptTokens: 1000},
			priceData: types.PriceData{
				ModelRatio: 1, CompletionRatio: 4, OtherRatios: map[string]float64{"video_input": 0.5},
				GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1},
			},
			want: 500_000, // 1000 × 0.5 × 1000
		},
		{
			name:  "分组倍率",
			usage: dto.Usage{PromptTokens: 1000},
			priceData: types.PriceData{
				ModelRatio: 1, CompletionRatio: 4, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 0.5},
			},
			want: 500_000, // 1000 × 1 × 1000 × 0.5
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			relayInfo := textRelayInfo(c.priceData)
			for _, qpu := range []float64{500_000, 7_300_000, 7_300_000_000} {
				setQuotaPerUnit(t, qpu)
				require.Equal(t, c.want, calculateTextQuotaSummary(ctx, relayInfo, &c.usage).Quota,
					"QuotaPerUnit=%v 时结果不得变化", qpu)
			}
		})
	}
}

// 最低扣费：倍率非 0、有 token 但算出 0 时扣 1 quota。
// 新单位下最低扣费是 ¥0.000000001（不再是 ¥0.000001）。
func TestCalculateTextQuotaSummaryMinimumOneQuota(t *testing.T) {
	ctx := newTextQuotaCtx(t)
	usage := &dto.Usage{PromptTokens: 1}
	priceData := types.PriceData{
		ModelRatio: 0.0000001, CompletionRatio: 4,
		GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1},
	}

	relayInfo := textRelayInfo(priceData)
	setQuotaPerUnit(t, 7_300_000_000)
	require.Equal(t, 1, calculateTextQuotaSummary(ctx, relayInfo, usage).Quota)

	// 无 token 时不扣费
	empty := &dto.Usage{}
	require.Equal(t, 0, calculateTextQuotaSummary(ctx, relayInfo, empty).Quota)
}

// 价格路径（UsePrice）：人民币按次价 × QuotaPerYuan，不乘倍率系数、不读 QPU。
func TestCalculateTextQuotaSummaryPricePathUsesQuotaPerYuan(t *testing.T) {
	ctx := newTextQuotaCtx(t)
	usage := &dto.Usage{PromptTokens: 1000, CompletionTokens: 100}
	priceData := types.PriceData{
		UsePrice:       true,
		ModelPrice:     0.5, // ¥0.5/次
		GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1},
	}

	relayInfo := textRelayInfo(priceData)
	for _, qpu := range []float64{500_000, 7_300_000, 7_300_000_000} {
		setQuotaPerUnit(t, qpu)
		require.Equal(t, 500_000_000, calculateTextQuotaSummary(ctx, relayInfo, usage).Quota,
			"¥0.5 × 10⁹ = 500,000,000，且不随 QPU 变化")
	}

	// E5：按次生图 ¥0.2/次、分组 1 → 200,000,000
	img := textRelayInfo(types.PriceData{
		UsePrice: true, ModelPrice: 0.2, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1},
	})
	require.Equal(t, 200_000_000, calculateTextQuotaSummary(ctx, img, usage).Quota)
}

// 音频路径（calculateAudioQuota 倍率分支）：WSS / Realtime / 音频预扣与结算都走它。
func TestCalculateAudioQuotaRatioPathThousandPerRatio(t *testing.T) {
	original := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = original })

	info := QuotaInfo{
		InputDetails:  TokenDetails{TextTokens: 1000},
		OutputDetails: TokenDetails{TextTokens: 0},
		ModelName:     "scale-test-model",
		ModelRatio:    1,
		GroupRatio:    1,
	}

	// 该模型没有配补全/音频倍率时，GetCompletionRatio 等返回默认 1，于是
	// quota = 输入文本 token 数 × 倍率 × 1000。
	for _, qpu := range []float64{500_000, 7_300_000, 7_300_000_000} {
		common.QuotaPerUnit = qpu
		require.Equal(t, 1_000_000, calculateAudioQuota(info), "QuotaPerUnit=%v", qpu)
	}
}

// calculateAudioQuota 的 UsePrice 分支必须 × QuotaPerYuan 并四舍五入，不得用 int() 截断。
func TestCalculateAudioQuotaUsePriceUsesQuotaPerYuan(t *testing.T) {
	original := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = original })

	info := QuotaInfo{
		UsePrice:   true,
		ModelPrice: 0.1, // ¥0.1/次
		GroupRatio: 1,
	}
	for _, qpu := range []float64{7_300_000, 7_300_000_000} {
		common.QuotaPerUnit = qpu
		require.Equal(t, 100_000_000, calculateAudioQuota(info), "QuotaPerUnit=%v", qpu)
	}

	// 取整方向：¥0.0099999999 × 1e9 = 9,999,999.9 → 10,000,000
	fractional := QuotaInfo{UsePrice: true, ModelPrice: 0.0099999999, GroupRatio: 1}
	require.Equal(t, 10_000_000, calculateAudioQuota(fractional))
}

// B3：OpenRouter 的「倍率 → 人民币/token」换算与内部记账单位解耦。
//
// 上游报的是美元成本，这里必须乘汇率；倍率是 ¥/百万 tokens，所以 ratio/10⁶ = ¥/token。
// 同一份上游 cost 在切换记账单位后必须换算出同样的缓存创建 token 数。
func TestCalcOpenRouterCacheCreateTokensIgnoresQuotaPerUnit(t *testing.T) {
	original := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = original })

	usage := dto.Usage{
		PromptTokens:     10_000,
		CompletionTokens: 2_000,
		// 与下面这组 token 分布自洽的上游 cost（美元）：3000 普通输入 + 4000 缓存命中(×0.1)
		// + 3000 缓存创建(×1.25) + 2000 输出(×4)，倍率 = ¥14.6/1M tokens（= 旧口径 $2/1M）。
		Cost: 0.0303,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 4_000,
		},
	}
	priceData := types.PriceData{
		ModelRatio:         14.6,
		CompletionRatio:    4,
		CacheRatio:         0.1,
		CacheCreationRatio: 1.25,
	}

	common.QuotaPerUnit = 7_300_000
	atOld := CalcOpenRouterCacheCreateTokens(usage, priceData)

	common.QuotaPerUnit = 7_300_000_000
	atNew := CalcOpenRouterCacheCreateTokens(usage, priceData)

	require.Equal(t, 3000, atOld, "换算出的缓存创建 token 数应与构造样例一致")
	require.Equal(t, atOld, atNew, "缓存创建 token 数是上游口径，不得随内部记账单位变化")
}
