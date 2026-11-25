package helper

import (
	"fmt"
	"math"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

func modelPriceNotConfiguredError(modelName string, userId int) error {
	if model.IsAdmin(userId) {
		return fmt.Errorf(
			"模型 %s 的价格未配置。请前往「系统设置 → 运营设置」开启自用模式，或在「系统设置 → 分组与模型定价设置」中为该模型配置价格；"+
				"Model %s price not configured. Go to System Settings → Operation Settings to enable self-use mode, or configure the model price in System Settings → Group & Model Pricing.",
			modelName, modelName,
		)
	}
	return fmt.Errorf(
		"模型 %s 的价格尚未由管理员配置，暂时无法使用，请联系站点管理员开启该模型；"+
			"Model %s has not been priced by the administrator yet. Please contact the site administrator to enable this model.",
		modelName, modelName,
	)
}

// https://docs.claude.com/en/docs/build-with-claude/prompt-caching#1-hour-cache-duration
const claudeCacheCreation1hMultiplier = 6 / 3.75

// HandleGroupRatio checks for "auto_group" in the context and updates the group ratio and relayInfo.UsingGroup if present
//
// 这是整条计费链路**唯一**的分组倍率产出点：文本 / 图像 / 音频 / 视频 / MJ / Task /
// pkg/billingexpr 分段计费全部读 PriceData.GroupRatioInfo.GroupRatio 这一个标量。
// 因此模型级折扣在这里接入一次，对所有计费模式都生效。
func HandleGroupRatio(ctx *gin.Context, relayInfo *relaycommon.RelayInfo) types.GroupRatioInfo {
	// check auto group
	//
	// 必须先落定 UsingGroup 再解析：auto 是伪分组名，拿它去查 GroupModelRatio
	// 永远查不到，模型级折扣会对所有 auto 令牌静默失效。
	autoGroup, exists := ctx.Get("auto_group")
	if exists {
		logger.LogDebug(ctx, fmt.Sprintf("final group: %s", autoGroup))
		relayInfo.UsingGroup = autoGroup.(string)
	}

	res := ratio_setting.ResolveGroupRatio(relayInfo.UserGroup, relayInfo.UsingGroup, relayInfo.OriginModelName)

	groupRatioInfo := types.GroupRatioInfo{
		GroupRatio:        res.Final,
		GroupSpecialRatio: -1,
		BaseRatio:         res.Base,
		ModelRuleMatch:    res.RuleMatch,
		ModelRuleMode:     res.RuleMode,
		ModelRuleValue:    res.RuleValue,
		UserRuleMatch:     res.UserRuleMatch,
		UserRuleValue:     res.UserRuleValue,
		TimeWindow:        res.TimeWindow,
		TimeLabel:         res.TimeLabel,
		TimeValue:         res.TimeValue,
		TimeFactors:       res.TimeFactors,
	}
	if res.HasSpecialRatio {
		groupRatioInfo.HasSpecialRatio = true
		groupRatioInfo.GroupSpecialRatio = res.SpecialRatio
	}

	return groupRatioInfo
}

func ModelPriceHelper(c *gin.Context, info *relaycommon.RelayInfo, promptTokens int, meta *types.TokenCountMeta) (types.PriceData, error) {
	defer model.LockPricingRead()()
	upstreamCostFX, _ := billing_setting.GetModelSourceRate()
	currency, factor, err := billing_setting.ResolveModelPriceConversion(info.OriginModelName)
	if err != nil {
		return types.PriceData{}, err
	}
	modelPrice, usePrice := ratio_setting.GetModelPrice(info.OriginModelName, false)

	groupRatioInfo := HandleGroupRatio(c, info)

	// Check if this model uses tiered_expr billing
	if billing_setting.GetBillingMode(info.OriginModelName) == billing_setting.BillingModeTieredExpr {
		return modelPriceHelperTiered(c, info, promptTokens, meta, groupRatioInfo, currency, factor)
	}
	toolPrices, toolRate, err := freezeToolPrices(info.OriginModelName)
	if err != nil {
		return types.PriceData{}, err
	}

	var preConsumedQuota int
	var modelRatio float64
	var completionRatio float64
	var cacheRatio float64
	var imageRatio float64
	var cacheCreationRatio float64
	var cacheCreationRatio5m float64
	var cacheCreationRatio1h float64
	var audioRatio float64
	var audioCompletionRatio float64
	var freeModel bool
	if !usePrice {
		preConsumedTokens := common.Max(promptTokens, common.PreConsumedQuota)
		if meta.MaxTokens != 0 {
			preConsumedTokens += meta.MaxTokens
		}
		var success bool
		var matchName string
		modelRatio, success, matchName = ratio_setting.GetModelRatio(info.OriginModelName)
		if !success {
			return types.PriceData{}, modelPriceNotConfiguredError(matchName, info.UserId)
		}
		modelRatio *= factor
		// 时段价按**分量**折算：整单乘的是 input 系数，其余分量把 f_c/f_input
		// 折进各自的相对倍率（未配置的分量不随时段变，系数为 1）。
		tf := groupRatioInfo.TimeFactors
		completionRatio = ratio_setting.GetCompletionRatio(info.OriginModelName) *
			types.ScaleComponent("output", tf)
		cacheRatio, _ = ratio_setting.GetCacheRatio(info.OriginModelName)
		cacheRatio *= types.ScaleComponent("cache", tf)
		cacheCreationRatio, _ = ratio_setting.GetCreateCacheRatio(info.OriginModelName)
		cacheCreationRatio *= types.ScaleComponent("create_cache", tf)
		cacheCreationRatio5m = cacheCreationRatio
		// 固定1h和5min缓存写入价格的比例
		cacheCreationRatio1h = cacheCreationRatio * claudeCacheCreation1hMultiplier
		imageRatio, _ = ratio_setting.GetImageRatio(info.OriginModelName)
		imageRatio *= types.ScaleComponent("image", tf)
		audioRatio = ratio_setting.GetAudioRatio(info.OriginModelName) *
			types.ScaleComponent("audio_input", tf)
		// 音频输出 = 音频输入 × 该倍率；两端各自的时段系数之差折进来
		audioCompletionRatio = ratio_setting.GetAudioCompletionRatio(info.OriginModelName) *
			types.ScaleComponent("audio_output", tf) /
			types.ScaleComponent("audio_input", tf)
		// common.PreConsumedQuota 名字里带 quota，实际是**预扣 token 数下限**，
		// 所以它不乘 QuotaPerMillionTokens、单位迁移也不改它：再乘一次会把冻结额放大两次。
		// 倍率换算只发生在下面 ratio 这一步（倍率是 ¥/百万 tokens → quota/token = 倍率 × 1000）。
		ratio := modelRatio * groupRatioInfo.GroupRatio * common.QuotaPerMillionTokens
		preConsumedQuota = int(math.Round(float64(preConsumedTokens) * ratio))
	} else {
		modelPrice *= factor
		if meta.ImagePriceRatio != 0 {
			modelPrice = modelPrice * meta.ImagePriceRatio
		}
		// 按次价是分量价目之一：整单只乘 input 系数，per_call 的差额由
		// ScaleComponent（= f_percall / f_input）折进这里，与 output/cache 同一约定。
		preConsumedQuota = int(math.Round(modelPrice * common.QuotaPerYuan * groupRatioInfo.GroupRatio *
			types.ScaleComponent("per_call", groupRatioInfo.TimeFactors)))
	}

	// check if free model pre-consume is disabled
	if !operation_setting.GetQuotaSetting().EnableFreeModelPreConsume {
		// if model price or ratio is 0, do not pre-consume quota
		if groupRatioInfo.GroupRatio == 0 {
			preConsumedQuota = 0
			freeModel = true
		} else if usePrice {
			if modelPrice == 0 {
				preConsumedQuota = 0
				freeModel = true
			}
		} else {
			if modelRatio == 0 {
				preConsumedQuota = 0
				freeModel = true
			}
		}
	}

	priceData := types.PriceData{
		PricingCurrency:      currency,
		ToolPrices:           toolPrices,
		ToolSourceRate:       toolRate,
		FreeModel:            freeModel,
		ModelPrice:           modelPrice,
		ModelRatio:           modelRatio,
		CompletionRatio:      completionRatio,
		GroupRatioInfo:       groupRatioInfo,
		UsePrice:             usePrice,
		CacheRatio:           cacheRatio,
		ImageRatio:           imageRatio,
		AudioRatio:           audioRatio,
		AudioCompletionRatio: audioCompletionRatio,
		CacheCreationRatio:   cacheCreationRatio,
		CacheCreation5mRatio: cacheCreationRatio5m,
		CacheCreation1hRatio: cacheCreationRatio1h,
		QuotaToPreConsume:    preConsumedQuota,
	}
	priceData.UpstreamCostFX = upstreamCostFX
	if currency == billing_setting.CurrencyUSD {
		priceData.ModelSourceRate = factor
	}

	if common.DebugEnabled {
		println(fmt.Sprintf("model_price_helper result: %s", priceData.ToSetting()))
	}
	info.PriceData = priceData
	return priceData, nil
}

// ModelPriceHelperPerCall 按次/按量计费的 PriceHelper (MJ、Task)
func ModelPriceHelperPerCall(c *gin.Context, info *relaycommon.RelayInfo) (types.PriceData, error) {
	defer model.LockPricingRead()()
	if info.TaskBasePriceData != nil {
		data := *info.TaskBasePriceData
		group := HandleGroupRatio(c, info)
		if info.UsingGroup != info.TaskBasePriceGroup {
			data.GroupRatioInfo = group
			if info.TaskBasePriceError == nil {
				data = priceTaskWithGroup(data)
			}
		}
		return data, info.TaskBasePriceError
	}
	currency, factor, err := billing_setting.ResolveModelPriceConversion(info.OriginModelName)
	if err != nil {
		return types.PriceData{}, err
	}
	entry, ok := ratio_setting.GetVideoPricing(info.OriginModelName)
	info.VideoPricingAtSubmit = nil
	if ok {
		info.VideoPricingAtSubmit = &entry
	}
	groupRatioInfo := HandleGroupRatio(c, info)
	priceData := types.PriceData{PricingCurrency: currency, GroupRatioInfo: groupRatioInfo}
	if currency == billing_setting.CurrencyUSD {
		priceData.ModelSourceRate = factor
	}

	modelPrice, success := ratio_setting.GetModelPrice(info.OriginModelName, true)
	usePrice := success
	var modelRatio float64

	if !success {
		defaultPrice, ok := ratio_setting.GetDefaultModelPriceMap()[info.OriginModelName]
		if ok {
			modelPrice = defaultPrice
			usePrice = true
		} else {
			var ratioSuccess bool
			var matchName string
			modelRatio, ratioSuccess, matchName = ratio_setting.GetModelRatio(info.OriginModelName)
			if !ratioSuccess {
				return priceData, modelPriceNotConfiguredError(matchName, info.UserId)
			}
		}
	}

	priceData.ModelPrice = modelPrice * factor
	priceData.ModelRatio = modelRatio * factor
	priceData.UsePrice = usePrice
	return priceTaskWithGroup(priceData), nil
}

func priceTaskWithGroup(priceData types.PriceData) types.PriceData {
	modelPrice, modelRatio := priceData.ModelPrice, priceData.ModelRatio
	groupRatioInfo := priceData.GroupRatioInfo
	var quota int
	freeModel := false

	if priceData.UsePrice {
		// per_call 分量系数与 ModelPriceHelper 的按次分支同一约定：
		// 整单乘的是 input 系数，按次价的差额折进 ScaleComponent。
		quota = int(math.Round(modelPrice * common.QuotaPerYuan * groupRatioInfo.GroupRatio *
			types.ScaleComponent("per_call", groupRatioInfo.TimeFactors)))
		if !operation_setting.GetQuotaSetting().EnableFreeModelPreConsume {
			if groupRatioInfo.GroupRatio == 0 || modelPrice == 0 {
				quota = 0
				freeModel = true
			}
		}
	} else {
		// 按量计费：预扣 25 万 tokens 的输入价（= 25 万 × quota/token）。
		// 这是 token 语义，不是"倍率的一半 × 某个单位"，所以写成显式的 250_000。
		quota = int(math.Round(250_000 * modelRatio * common.QuotaPerMillionTokens * groupRatioInfo.GroupRatio))
		modelPrice = -1
		if !operation_setting.GetQuotaSetting().EnableFreeModelPreConsume {
			if groupRatioInfo.GroupRatio == 0 || modelRatio == 0 {
				quota = 0
				freeModel = true
			}
		}
	}

	priceData.FreeModel = freeModel
	priceData.ModelPrice = modelPrice
	priceData.Quota = quota
	return priceData
}

func HasModelBillingConfig(modelName string) bool {
	if _, ok := ratio_setting.GetModelPrice(modelName, false); ok {
		return true
	}
	if _, ok, _ := ratio_setting.GetModelRatio(modelName); ok {
		return true
	}
	// 视频计费矩阵是第四个定价源（docs/video-billing-matrix-design.md §2.6）。
	// 不登记的话，只靠矩阵定价的模型会从 /v1/models 里消失（controller/model.go:137,184），
	// 且 ModelPriceHelperPerCall 会先于矩阵报 model_price_error。
	if _, ok := ratio_setting.GetVideoPricing(modelName); ok {
		return true
	}
	if billing_setting.GetBillingMode(modelName) != billing_setting.BillingModeTieredExpr {
		return false
	}
	expr, ok := billing_setting.GetBillingExpr(modelName)
	return ok && strings.TrimSpace(expr) != ""
}

func modelPriceHelperTiered(c *gin.Context, info *relaycommon.RelayInfo, promptTokens int, meta *types.TokenCountMeta, groupRatioInfo types.GroupRatioInfo, currency string, factor float64) (types.PriceData, error) {
	toolPrices, toolRate, err := freezeToolPrices(info.OriginModelName)
	if err != nil {
		return types.PriceData{}, err
	}
	exprStr, ok := billing_setting.GetBillingExpr(info.OriginModelName)
	if !ok {
		return types.PriceData{}, fmt.Errorf("model %s is configured as tiered_expr but has no billing expression", info.OriginModelName)
	}

	estimatedCompletionTokens := 0
	if meta.MaxTokens != 0 {
		estimatedCompletionTokens = meta.MaxTokens
	}

	requestInput, err := ResolveIncomingBillingExprRequestInput(c, info)
	if err != nil {
		return types.PriceData{}, err
	}

	// fx：表达式引用 fx 表示系数写的是美元目录价，必须取到全局源汇率。
	// 取不到就**拒绝这次请求**：默认 0 会白送，默认 1 会少收一个汇率倍数，
	// 两种都比报错糟得多（错误至少可以立刻发现）。
	exprFX := float64(0)
	if billingexpr.UsedVars(exprStr)["fx"] {
		rate, ok := billing_setting.GetModelSourceRate()
		if !ok {
			return types.PriceData{}, fmt.Errorf(
				"model %s 的计费表达式引用了 fx，但没有设置全局源汇率", info.OriginModelName)
		}
		exprFX = rate
	}

	rawCost, trace, err := billingexpr.RunExprWithRequest(exprStr, billingexpr.TokenParams{
		P:   float64(promptTokens),
		C:   float64(estimatedCompletionTokens),
		Len: float64(promptTokens),
		FX:  exprFX,
	}, requestInput)
	if err != nil {
		return types.PriceData{}, fmt.Errorf("model %s tiered expr run failed: %w", info.OriginModelName, err)
	}

	// 表达式系数按版本换算成 quota：v2 是 ¥/百万 tokens，v1 直接拒绝（与结算共用同一函数）。
	quotaBeforeGroup, err := billingexpr.QuotaFromExprOutput(rawCost, billingexpr.ExprVersion(exprStr))
	if err != nil {
		return types.PriceData{}, fmt.Errorf("model %s: %w", info.OriginModelName, err)
	}
	quotaAfterGroup := quotaBeforeGroup * groupRatioInfo.GroupRatio
	// 乘完分组倍率也要校验：倍率是管理员可配的任意浮点数，
	// 1e15 这类值会把额度溢出成 MinInt64（预扣与结算两侧都要拦）。
	if err := billingexpr.ValidateFinalQuota(quotaAfterGroup); err != nil {
		return types.PriceData{}, fmt.Errorf("model %s: %w", info.OriginModelName, err)
	}
	preConsumedQuota := billingexpr.QuotaRound(quotaAfterGroup)

	freeModel := false
	if !operation_setting.GetQuotaSetting().EnableFreeModelPreConsume {
		if groupRatioInfo.GroupRatio == 0 {
			preConsumedQuota = 0
			freeModel = true
		}
	}

	exprHash := billingexpr.ExprHashString(exprStr)
	snapshot := &billingexpr.BillingSnapshot{
		BillingMode:               billing_setting.BillingModeTieredExpr,
		ModelName:                 info.OriginModelName,
		ExprString:                exprStr,
		ExprHash:                  exprHash,
		GroupRatio:                groupRatioInfo.GroupRatio,
		EstimatedPromptTokens:     promptTokens,
		EstimatedCompletionTokens: estimatedCompletionTokens,
		EstimatedQuotaBeforeGroup: quotaBeforeGroup,
		EstimatedQuotaAfterGroup:  preConsumedQuota,
		// 预扣额本身也要过一遍范围校验（见上），这里只是把同一份数据留在快照里。
		EstimatedTier: trace.MatchedTier,
		ExprVersion:   billingexpr.ExprVersion(exprStr),
		// 冻结本次使用的 fx：结算按快照重算，运营中途改汇率不影响在途请求。
		ExprFX: exprFX,
	}
	info.TieredBillingSnapshot = snapshot
	info.BillingRequestInput = &requestInput

	priceData := types.PriceData{
		PricingCurrency:   currency,
		ModelSourceRate:   exprFX,
		ToolPrices:        toolPrices,
		ToolSourceRate:    toolRate,
		FreeModel:         freeModel,
		GroupRatioInfo:    groupRatioInfo,
		QuotaToPreConsume: preConsumedQuota,
	}
	if currency == billing_setting.CurrencyUSD {
		priceData.ModelSourceRate = factor
	}

	if common.DebugEnabled {
		println(fmt.Sprintf("model_price_helper_tiered result: model=%s preConsume=%d quotaBeforeGroup=%.2f groupRatio=%.2f tier=%s", info.OriginModelName, preConsumedQuota, quotaBeforeGroup, groupRatioInfo.GroupRatio, trace.MatchedTier))
	}

	info.PriceData = priceData
	return priceData, nil
}

func freezeToolPrices(modelName string) (map[string]float64, float64, error) {
	prices := make(map[string]float64, 4)
	var usedRate float64
	rate, hasRate := billing_setting.GetModelSourceRate()
	names := []string{"web_search_preview", "web_search", "file_search", "gemini_audio_input"}
	for _, quality := range []string{"low", "medium", "high"} {
		for _, size := range []string{"1024x1024", "1024x1536", "1536x1024"} {
			names = append(names, operation_setting.ImageGenerationToolName(quality, size))
		}
	}
	for _, name := range names {
		amount, usd, err := operation_setting.ResolveToolPriceCNY(name, modelName, rate, hasRate)
		if err != nil {
			return nil, 0, err
		}
		if usd {
			usedRate = rate
		}
		prices[name] = amount
	}
	return prices, usedRate, nil
}
