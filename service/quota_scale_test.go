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

// 单位切换的回归护栏：QuotaPerUnit 换了，但计费路径的换算必须跟着走。
//
// 约定：QuotaPerUnit = 汇率 × 10⁶ 时 1 quota = ¥0.000001，倍率路径的 quota/token
// 变成 ratio × QuotaScale()。QPU = 500000（scale = 1）下结果必须与改造前逐位相同，
// 作为「没有改动旧单位行为」的证据。
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

// 用户 junm1888 反馈的那条日志：prompt 199 / completion 4096、模型倍率 0.13698630137、
// 补全倍率 4、分组倍率 0.5。
//
// 旧单位（QPU = 500000）算出 1135.82… quota，Round 得 1136 = ¥0.0165856，与前端公式
// ¥0.016583 差 ±0.5 quota；新单位（QPU = 7300000）算出 16582.99… quota，Round 得
// 16583 = ¥0.016583，与公式逐位一致。这条用例就是本次改造的目标。
func TestCalculateTextQuotaSummaryReporterSample(t *testing.T) {
	ctx := newTextQuotaCtx(t)
	usage := &dto.Usage{PromptTokens: 199, CompletionTokens: 4096}
	relayInfo := textRelayInfo(types.PriceData{
		ModelRatio:      0.13698630137,
		CompletionRatio: 4,
		GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: 0.5},
	})

	setQuotaPerUnit(t, 500000)
	require.Equal(t, 1136, calculateTextQuotaSummary(ctx, relayInfo, usage).Quota)

	setQuotaPerUnit(t, 7300000)
	require.Equal(t, 16583, calculateTextQuotaSummary(ctx, relayInfo, usage).Quota)
}

// 倍率路径的分场景覆盖：每个场景都取「未取整值恰好是整数」的参数，于是两种单位下的
// 期望值都能精确写出（QPU = 500000 时 = 整数本身；QPU = 7300000 时 = round(整数 × 14.6)），
// 不需要在测试里复刻生产公式，也不会被取整误差掩盖。
func TestCalculateTextQuotaSummaryRatioPathScale(t *testing.T) {
	ctx := newTextQuotaCtx(t)

	cases := []struct {
		name        string
		usage       dto.Usage
		priceData   types.PriceData
		quotaAt500  int
		quotaAt7300 int
	}{
		{
			name:        "纯文本",
			usage:       dto.Usage{PromptTokens: 1000, CompletionTokens: 0},
			priceData:   types.PriceData{ModelRatio: 1, CompletionRatio: 4, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1}},
			quotaAt500:  1000,
			quotaAt7300: 14600,
		},
		{
			name:  "补全倍率",
			usage: dto.Usage{PromptTokens: 100, CompletionTokens: 100},
			priceData: types.PriceData{
				ModelRatio: 1, CompletionRatio: 2, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1},
			},
			quotaAt500:  300,  // 100 + 100×2
			quotaAt7300: 4380, // 300 × 14.6
		},
		{
			name:  "缓存命中",
			usage: dto.Usage{PromptTokens: 1000, PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 200}},
			priceData: types.PriceData{
				ModelRatio: 1, CompletionRatio: 4, CacheRatio: 0.5,
				GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1},
			},
			quotaAt500:  900, // (1000-200) + 200×0.5
			quotaAt7300: 13140,
		},
		{
			name:  "图片 token",
			usage: dto.Usage{PromptTokens: 1000, PromptTokensDetails: dto.InputTokenDetails{ImageTokens: 100}},
			priceData: types.PriceData{
				ModelRatio: 1, CompletionRatio: 4, ImageRatio: 2,
				GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1},
			},
			quotaAt500:  1100, // (1000-100) + 100×2
			quotaAt7300: 16060,
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
			quotaAt500:  1325,  // 1000（Claude 语义下 prompt 已是文本口径）+ 100×1.25 + 100×2
			quotaAt7300: 19345, // 1325 × 14.6
		},
		{
			name:  "OtherRatios",
			usage: dto.Usage{PromptTokens: 1000},
			priceData: types.PriceData{
				ModelRatio: 1, CompletionRatio: 4, OtherRatios: map[string]float64{"video_input": 0.5},
				GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1},
			},
			quotaAt500:  500,
			quotaAt7300: 7300,
		},
		{
			name:  "分组倍率",
			usage: dto.Usage{PromptTokens: 1000},
			priceData: types.PriceData{
				ModelRatio: 1, CompletionRatio: 4, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 0.5},
			},
			quotaAt500:  500,
			quotaAt7300: 7300,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			relayInfo := textRelayInfo(c.priceData)

			setQuotaPerUnit(t, 500000)
			require.Equal(t, c.quotaAt500, calculateTextQuotaSummary(ctx, relayInfo, &c.usage).Quota)

			setQuotaPerUnit(t, 7300000)
			require.Equal(t, c.quotaAt7300, calculateTextQuotaSummary(ctx, relayInfo, &c.usage).Quota)
		})
	}
}

// quota 算出 ≤ 0 且倍率非 0 时兜底为 1：兜底量本身不随单位变化（它就是一个 quota）。
func TestCalculateTextQuotaSummaryMinimumOneQuotaSurvivesScale(t *testing.T) {
	ctx := newTextQuotaCtx(t)
	usage := &dto.Usage{PromptTokens: 1}
	priceData := types.PriceData{
		ModelRatio: 0.0000001, CompletionRatio: 4,
		GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1},
	}

	relayInfo := textRelayInfo(priceData)
	setQuotaPerUnit(t, 500000)
	require.Equal(t, 1, calculateTextQuotaSummary(ctx, relayInfo, usage).Quota)

	setQuotaPerUnit(t, 7300000)
	require.Equal(t, 1, calculateTextQuotaSummary(ctx, relayInfo, usage).Quota)
}

// 价格路径（UsePrice）不得乘 scale：它已经乘过 QuotaPerUnit，再乘就是 14.6 倍。
func TestCalculateTextQuotaSummaryPricePathDoesNotScale(t *testing.T) {
	ctx := newTextQuotaCtx(t)
	usage := &dto.Usage{PromptTokens: 1000, CompletionTokens: 100}
	priceData := types.PriceData{
		UsePrice:       true,
		ModelPrice:     0.5,
		GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1},
	}

	relayInfo := textRelayInfo(priceData)
	setQuotaPerUnit(t, 500000)
	require.Equal(t, 250000, calculateTextQuotaSummary(ctx, relayInfo, usage).Quota)

	setQuotaPerUnit(t, 7300000)
	require.Equal(t, int(0.5*7300000), calculateTextQuotaSummary(ctx, relayInfo, usage).Quota)
}

// 音频路径（calculateAudioQuota 倍率分支）：WSS / Realtime / 音频预扣与结算都走它。
func TestCalculateAudioQuotaRatioPathScale(t *testing.T) {
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
	// quota = 输入文本 token 数 × 倍率 × 分组倍率。
	common.QuotaPerUnit = 500000
	require.Equal(t, 1000, calculateAudioQuota(info))

	common.QuotaPerUnit = 7300000
	require.Equal(t, 14600, calculateAudioQuota(info))
}

// calculateAudioQuota 的 UsePrice 分支此前用 IntPart 截断：¥0.1 的按次价在
// QPU = 7300000 下算出 99999.9999…，截断会少收 1 quota。
func TestCalculateAudioQuotaUsePriceRoundsUp(t *testing.T) {
	original := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = original })
	common.QuotaPerUnit = 7300000

	info := QuotaInfo{
		UsePrice:   true,
		ModelPrice: 0.1 / 7.3, // 运营在界面上填 ¥0.1，落库是美元
		GroupRatio: 1,
	}
	require.Equal(t, 100000, calculateAudioQuota(info))
}

// B3：OpenRouter 的「倍率 → 美元/token」换算必须与内部记账单位解耦，
// 否则同一份上游 cost 在切换单位后会换算出不同的缓存创建 token 数。
func TestCalcOpenRouterCacheCreateTokensIgnoresQuotaPerUnit(t *testing.T) {
	original := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = original })

	usage := dto.Usage{
		PromptTokens:     10_000,
		CompletionTokens: 2_000,
		// 与下面这组 token 分布自洽的上游 cost：3000 普通输入 + 4000 缓存命中(×0.1)
		// + 3000 缓存创建(×1.25) + 2000 输出(×4)，倍率 1 = $2/1M tokens。
		Cost: 0.0303,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 4_000,
		},
	}
	priceData := types.PriceData{
		ModelRatio:         1,
		CompletionRatio:    4,
		CacheRatio:         0.1,
		CacheCreationRatio: 1.25,
	}

	common.QuotaPerUnit = 500000
	at500 := CalcOpenRouterCacheCreateTokens(usage, priceData)

	common.QuotaPerUnit = 7300000
	at7300 := CalcOpenRouterCacheCreateTokens(usage, priceData)

	require.Equal(t, 3000, at500, "换算出的缓存创建 token 数应与构造样例一致")
	require.Equal(t, at500, at7300, "缓存创建 token 数是上游口径，不得随内部记账单位变化")
}
