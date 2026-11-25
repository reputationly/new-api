package controller

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

func GetSubscription(c *gin.Context) {
	var remainQuota int
	var usedQuota int
	var err error
	var token *model.Token
	var expiredTime int64
	if common.DisplayTokenStatEnabled {
		tokenId := c.GetInt("token_id")
		token, err = model.GetTokenById(tokenId)
		expiredTime = token.ExpiredTime
		remainQuota = token.RemainQuota
		usedQuota = token.UsedQuota
	} else {
		userId := c.GetInt("id")
		remainQuota, err = model.GetUserQuota(userId, false)
		usedQuota, err = model.GetUserUsedQuota(userId)
	}
	if expiredTime <= 0 {
		expiredTime = 0
	}
	if err != nil {
		openAIError := types.OpenAIError{
			Message: err.Error(),
			Type:    "upstream_error",
		}
		c.JSON(200, gin.H{
			"error": openAIError,
		})
		return
	}
	quota := remainQuota + usedQuota
	var amount float64
	if token != nil && token.UnlimitedQuota {
		amount = 100000000
	} else {
		var ok bool
		amount, ok = displayBillingQuota(c, quota)
		if !ok {
			return
		}
	}
	subscription := OpenAISubscriptionResponse{
		Object:             "billing_subscription",
		HasPaymentMethod:   true,
		SoftLimitUSD:       amount,
		HardLimitUSD:       amount,
		SystemHardLimitUSD: amount,
		AccessUntil:        expiredTime,
	}
	c.JSON(200, subscription)
	return
}

func GetUsage(c *gin.Context) {
	var quota int
	var err error
	var token *model.Token
	if common.DisplayTokenStatEnabled {
		tokenId := c.GetInt("token_id")
		token, err = model.GetTokenById(tokenId)
		quota = token.UsedQuota
	} else {
		userId := c.GetInt("id")
		quota, err = model.GetUserUsedQuota(userId)
	}
	if err != nil {
		openAIError := types.OpenAIError{
			Message: err.Error(),
			Type:    "new_api_error",
		}
		c.JSON(200, gin.H{
			"error": openAIError,
		})
		return
	}
	amount, ok := displayBillingQuota(c, quota)
	if !ok {
		return
	}
	usage := OpenAIUsageResponse{
		Object:     "list",
		TotalUsage: amount * 100,
	}
	c.JSON(200, usage)
	return
}

func displayBillingQuota(c *gin.Context, quota int) (float64, bool) {
	displayType := operation_setting.GetQuotaDisplayType()
	if displayType == operation_setting.QuotaDisplayTypeTokens {
		return float64(quota), true
	}
	amount := float64(quota) / common.QuotaPerYuan
	if displayType == operation_setting.QuotaDisplayTypeCNY {
		return amount, true
	}
	rate, ok := billing_setting.GetModelSourceRate()
	if !ok {
		c.JSON(503, gin.H{"error": types.OpenAIError{Message: "全局模型源汇率未配置", Type: "new_api_error"}})
		return 0, false
	}
	amount /= rate
	if displayType == operation_setting.QuotaDisplayTypeCustom {
		if customRate := operation_setting.GetGeneralSetting().CustomCurrencyExchangeRate; customRate > 0 {
			amount *= customRate
		}
	}
	return amount, true
}
