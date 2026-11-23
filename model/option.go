package model

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/performance_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

type Option struct {
	Key   string `json:"key" gorm:"primaryKey"`
	Value string `json:"value"`
}

const pricingWriteLockKey = "_internal.pricing_write_lock"

var pricingWriteMu sync.Mutex

func AllOption() ([]*Option, error) {
	var options []*Option
	var err error
	err = DB.Find(&options).Error
	return options, err
}

func InitOptionMap() {
	common.OptionMapRWMutex.Lock()
	common.OptionMap = make(map[string]string)

	// 添加原有的系统配置
	common.OptionMap["FileUploadPermission"] = strconv.Itoa(common.FileUploadPermission)
	common.OptionMap["FileDownloadPermission"] = strconv.Itoa(common.FileDownloadPermission)
	common.OptionMap["ImageUploadPermission"] = strconv.Itoa(common.ImageUploadPermission)
	common.OptionMap["ImageDownloadPermission"] = strconv.Itoa(common.ImageDownloadPermission)
	common.OptionMap["PasswordLoginEnabled"] = strconv.FormatBool(common.PasswordLoginEnabled)
	common.OptionMap["PasswordRegisterEnabled"] = strconv.FormatBool(common.PasswordRegisterEnabled)
	common.OptionMap["EmailVerificationEnabled"] = strconv.FormatBool(common.EmailVerificationEnabled)
	common.OptionMap["GitHubOAuthEnabled"] = strconv.FormatBool(common.GitHubOAuthEnabled)
	common.OptionMap["LinuxDOOAuthEnabled"] = strconv.FormatBool(common.LinuxDOOAuthEnabled)
	common.OptionMap["TelegramOAuthEnabled"] = strconv.FormatBool(common.TelegramOAuthEnabled)
	common.OptionMap["WeChatAuthEnabled"] = strconv.FormatBool(common.WeChatAuthEnabled)
	common.OptionMap["TurnstileCheckEnabled"] = strconv.FormatBool(common.TurnstileCheckEnabled)
	common.OptionMap["RegisterEnabled"] = strconv.FormatBool(common.RegisterEnabled)
	common.OptionMap["AutomaticDisableChannelEnabled"] = strconv.FormatBool(common.AutomaticDisableChannelEnabled)
	common.OptionMap["AutomaticEnableChannelEnabled"] = strconv.FormatBool(common.AutomaticEnableChannelEnabled)
	common.OptionMap["LogConsumeEnabled"] = strconv.FormatBool(common.LogConsumeEnabled)
	common.OptionMap["DisplayInCurrencyEnabled"] = strconv.FormatBool(common.DisplayInCurrencyEnabled)
	common.OptionMap["DisplayTokenStatEnabled"] = strconv.FormatBool(common.DisplayTokenStatEnabled)
	common.OptionMap["DrawingEnabled"] = strconv.FormatBool(common.DrawingEnabled)
	common.OptionMap["TaskEnabled"] = strconv.FormatBool(common.TaskEnabled)
	common.OptionMap["DataExportEnabled"] = strconv.FormatBool(common.DataExportEnabled)
	common.OptionMap["ChannelDisableThreshold"] = strconv.FormatFloat(common.ChannelDisableThreshold, 'f', -1, 64)
	common.OptionMap["EmailDomainRestrictionEnabled"] = strconv.FormatBool(common.EmailDomainRestrictionEnabled)
	common.OptionMap["EmailAliasRestrictionEnabled"] = strconv.FormatBool(common.EmailAliasRestrictionEnabled)
	common.OptionMap["EmailDomainWhitelist"] = strings.Join(common.EmailDomainWhitelist, ",")
	common.OptionMap["SMTPServer"] = ""
	common.OptionMap["SMTPFrom"] = ""
	common.OptionMap["SMTPPort"] = strconv.Itoa(common.SMTPPort)
	common.OptionMap["SMTPAccount"] = ""
	common.OptionMap["SMTPToken"] = ""
	common.OptionMap["SMTPSSLEnabled"] = strconv.FormatBool(common.SMTPSSLEnabled)
	common.OptionMap["SMTPForceAuthLogin"] = strconv.FormatBool(common.SMTPForceAuthLogin)
	common.OptionMap["Notice"] = ""
	common.OptionMap["About"] = ""
	common.OptionMap["HomePageContent"] = ""
	common.OptionMap["Footer"] = common.Footer
	common.OptionMap["SystemName"] = common.SystemName
	common.OptionMap["Logo"] = common.Logo
	common.OptionMap["ServerAddress"] = ""
	common.OptionMap["WorkerUrl"] = system_setting.WorkerUrl
	common.OptionMap["WorkerValidKey"] = system_setting.WorkerValidKey
	common.OptionMap["WorkerAllowHttpImageRequestEnabled"] = strconv.FormatBool(system_setting.WorkerAllowHttpImageRequestEnabled)
	common.OptionMap["PayAddress"] = ""
	common.OptionMap["CustomCallbackAddress"] = ""
	common.OptionMap["EpayId"] = ""
	common.OptionMap["EpayKey"] = ""
	common.OptionMap["Price"] = strconv.FormatFloat(operation_setting.Price, 'f', -1, 64)
	common.OptionMap["USDExchangeRate"] = strconv.FormatFloat(operation_setting.USDExchangeRate, 'f', -1, 64)
	common.OptionMap["MinTopUp"] = strconv.Itoa(operation_setting.MinTopUp)
	common.OptionMap["StripeMinTopUp"] = strconv.Itoa(setting.StripeMinTopUp)
	common.OptionMap["StripeApiSecret"] = setting.StripeApiSecret
	common.OptionMap["StripeWebhookSecret"] = setting.StripeWebhookSecret
	common.OptionMap["StripePriceId"] = setting.StripePriceId
	common.OptionMap["StripeUnitPrice"] = strconv.FormatFloat(setting.StripeUnitPrice, 'f', -1, 64)
	common.OptionMap["StripePromotionCodesEnabled"] = strconv.FormatBool(setting.StripePromotionCodesEnabled)
	common.OptionMap["CreemApiKey"] = setting.CreemApiKey
	common.OptionMap["CreemProducts"] = setting.CreemProducts
	common.OptionMap["CreemTestMode"] = strconv.FormatBool(setting.CreemTestMode)
	common.OptionMap["CreemWebhookSecret"] = setting.CreemWebhookSecret
	common.OptionMap["WaffoEnabled"] = strconv.FormatBool(setting.WaffoEnabled)
	common.OptionMap["WaffoApiKey"] = setting.WaffoApiKey
	common.OptionMap["WaffoPrivateKey"] = setting.WaffoPrivateKey
	common.OptionMap["WaffoPublicCert"] = setting.WaffoPublicCert
	common.OptionMap["WaffoSandboxPublicCert"] = setting.WaffoSandboxPublicCert
	common.OptionMap["WaffoSandboxApiKey"] = setting.WaffoSandboxApiKey
	common.OptionMap["WaffoSandboxPrivateKey"] = setting.WaffoSandboxPrivateKey
	common.OptionMap["WaffoSandbox"] = strconv.FormatBool(setting.WaffoSandbox)
	common.OptionMap["WaffoMerchantId"] = setting.WaffoMerchantId
	common.OptionMap["WaffoNotifyUrl"] = setting.WaffoNotifyUrl
	common.OptionMap["WaffoReturnUrl"] = setting.WaffoReturnUrl
	common.OptionMap["WaffoSubscriptionReturnUrl"] = setting.WaffoSubscriptionReturnUrl
	common.OptionMap["WaffoCurrency"] = setting.WaffoCurrency
	common.OptionMap["WaffoUnitPrice"] = strconv.FormatFloat(setting.WaffoUnitPrice, 'f', -1, 64)
	common.OptionMap["WaffoMinTopUp"] = strconv.Itoa(setting.WaffoMinTopUp)
	common.OptionMap["WaffoPayMethods"] = setting.WaffoPayMethods2JsonString()
	common.OptionMap["WaffoPancakeEnabled"] = strconv.FormatBool(setting.WaffoPancakeEnabled)
	common.OptionMap["WaffoPancakeSandbox"] = strconv.FormatBool(setting.WaffoPancakeSandbox)
	common.OptionMap["WaffoPancakeMerchantID"] = setting.WaffoPancakeMerchantID
	common.OptionMap["WaffoPancakePrivateKey"] = setting.WaffoPancakePrivateKey
	common.OptionMap["WaffoPancakeWebhookPublicKey"] = setting.WaffoPancakeWebhookPublicKey
	common.OptionMap["WaffoPancakeWebhookTestKey"] = setting.WaffoPancakeWebhookTestKey
	common.OptionMap["WaffoPancakeStoreID"] = setting.WaffoPancakeStoreID
	common.OptionMap["WaffoPancakeProductID"] = setting.WaffoPancakeProductID
	common.OptionMap["WaffoPancakeReturnURL"] = setting.WaffoPancakeReturnURL
	common.OptionMap["WaffoPancakeCurrency"] = setting.WaffoPancakeCurrency
	common.OptionMap["WaffoPancakeUnitPrice"] = strconv.FormatFloat(setting.WaffoPancakeUnitPrice, 'f', -1, 64)
	common.OptionMap["WaffoPancakeMinTopUp"] = strconv.Itoa(setting.WaffoPancakeMinTopUp)
	common.OptionMap["AlipayAppId"] = setting.AlipayAppId
	common.OptionMap["AlipayPrivateKey"] = setting.AlipayPrivateKey
	common.OptionMap["AlipayPublicKey"] = setting.AlipayPublicKey
	common.OptionMap["AlipayMinTopUp"] = strconv.Itoa(setting.AlipayMinTopUp)
	common.OptionMap["AlipaySandbox"] = strconv.FormatBool(setting.AlipaySandbox)
	common.OptionMap["WxpayAppId"] = setting.WxpayAppId
	common.OptionMap["WxpayMchId"] = setting.WxpayMchId
	common.OptionMap["WxpayPrivateKey"] = setting.WxpayPrivateKey
	common.OptionMap["WxpayApiV3Key"] = setting.WxpayApiV3Key
	common.OptionMap["WxpayCertSerial"] = setting.WxpayCertSerial
	common.OptionMap["WxpayPublicKey"] = setting.WxpayPublicKey
	common.OptionMap["WxpayPublicKeyId"] = setting.WxpayPublicKeyId
	common.OptionMap["WxpayMinTopUp"] = strconv.Itoa(setting.WxpayMinTopUp)
	common.OptionMap["TopupGroupRatio"] = common.TopupGroupRatio2JSONString()
	common.OptionMap["Chats"] = setting.Chats2JsonString()
	// MiniMax Design 客户端的模型目录。管理员改这里，客户端重启后生效。
	common.OptionMap["HiloCatalog"] = setting.HiloCatalog2JsonString()
	common.OptionMap["AutoGroups"] = setting.AutoGroups2JsonString()
	common.OptionMap["DefaultUseAutoGroup"] = strconv.FormatBool(setting.DefaultUseAutoGroup)
	common.OptionMap["PayMethods"] = operation_setting.PayMethods2JsonString()
	common.OptionMap["GitHubClientId"] = ""
	common.OptionMap["GitHubClientSecret"] = ""
	common.OptionMap["TelegramBotToken"] = ""
	common.OptionMap["TelegramBotName"] = ""
	common.OptionMap["WeChatServerAddress"] = ""
	common.OptionMap["WeChatServerToken"] = ""
	common.OptionMap["WeChatAccountQRCodeImageURL"] = ""
	common.OptionMap["TurnstileSiteKey"] = ""
	common.OptionMap["TurnstileSecretKey"] = ""
	common.OptionMap["QuotaForNewUser"] = strconv.Itoa(common.QuotaForNewUser)
	common.OptionMap["QuotaForInviter"] = strconv.Itoa(common.QuotaForInviter)
	common.OptionMap["QuotaForInvitee"] = strconv.Itoa(common.QuotaForInvitee)
	common.OptionMap["QuotaRemindThreshold"] = strconv.Itoa(common.QuotaRemindThreshold)
	common.OptionMap["QuotaRemindEnabled"] = strconv.FormatBool(common.QuotaRemindEnabled)
	common.OptionMap["PlaygroundPipelineServerSide"] = strconv.FormatBool(common.PlaygroundPipelineServerSide)
	common.OptionMap["PreConsumedQuota"] = strconv.Itoa(common.PreConsumedQuota)
	common.OptionMap["ModelRequestRateLimitCount"] = strconv.Itoa(setting.ModelRequestRateLimitCount)
	common.OptionMap["ModelRequestRateLimitDurationMinutes"] = strconv.Itoa(setting.ModelRequestRateLimitDurationMinutes)
	common.OptionMap["ModelRequestRateLimitSuccessCount"] = strconv.Itoa(setting.ModelRequestRateLimitSuccessCount)
	common.OptionMap["ModelRequestRateLimitGroup"] = setting.ModelRequestRateLimitGroup2JSONString()
	common.OptionMap["ModelRatio"] = ratio_setting.ModelRatio2JSONString()
	common.OptionMap["ModelSourceRate"] = billing_setting.ModelSourceRate2JSONString()
	common.OptionMap["ModelPrice"] = ratio_setting.ModelPrice2JSONString()
	common.OptionMap["CacheRatio"] = ratio_setting.CacheRatio2JSONString()
	common.OptionMap["CreateCacheRatio"] = ratio_setting.CreateCacheRatio2JSONString()
	common.OptionMap["GroupRatio"] = ratio_setting.GroupRatio2JSONString()
	common.OptionMap["GroupGroupRatio"] = ratio_setting.GroupGroupRatio2JSONString()
	common.OptionMap["GroupModelRatio"] = ratio_setting.GroupModelRatio2JSONString()
	common.OptionMap["UserGroupModelRatio"] = ratio_setting.UserGroupModelRatio2JSONString()
	common.OptionMap["GroupTimeRatio"] = ratio_setting.GroupTimeRatio2JSONString()
	common.OptionMap["UserUsableGroups"] = setting.UserUsableGroups2JSONString()
	common.OptionMap["GroupEnabled"] = setting.GroupEnabled2JSONString()
	common.OptionMap["GroupDescription"] = setting.GroupDescriptions2JSONString()
	common.OptionMap["CompletionRatio"] = ratio_setting.CompletionRatio2JSONString()
	common.OptionMap["ImageRatio"] = ratio_setting.ImageRatio2JSONString()
	common.OptionMap["AudioRatio"] = ratio_setting.AudioRatio2JSONString()
	common.OptionMap["AudioCompletionRatio"] = ratio_setting.AudioCompletionRatio2JSONString()
	common.OptionMap["VideoPricingConfig"] = ratio_setting.VideoPricing2JSONString()
	// 聚合(编排)模型的出厂配置:两条视频流水线(帧族 / 参考族),都不带提示词增强段。
	// 为什么是这两条、增强段为什么出厂关闭,见 common.DefaultAggregateModelConfig 的注释。
	// 管理员保存过之后以库里的为准。
	common.OptionMap["AggregateModelConfig"] = common.DefaultAggregateModelConfig
	common.OptionMap["TopUpLink"] = common.TopUpLink
	//common.OptionMap["ChatLink"] = common.ChatLink
	//common.OptionMap["ChatLink2"] = common.ChatLink2
	common.OptionMap["QuotaPerUnit"] = strconv.FormatFloat(common.QuotaPerUnit, 'f', -1, 64)
	common.OptionMap["RetryTimes"] = strconv.Itoa(common.RetryTimes)
	common.OptionMap["KYCEnabled"] = strconv.FormatBool(common.KYCEnabled)
	common.OptionMap["KYCMaxSubmitCount"] = strconv.Itoa(common.KYCMaxSubmitCount)
	common.OptionMap["EnterpriseMaxSubmitCount"] = strconv.Itoa(common.EnterpriseMaxSubmitCount)
	common.OptionMap["ReconcileUploadMaxFileBytes"] = strconv.FormatInt(common.ReconcileUploadMaxFileBytes, 10)
	common.OptionMap["ReconcileUploadMaxLogRangeDays"] = strconv.Itoa(common.ReconcileUploadMaxLogRangeDays)
	common.OptionMap["ReconcileDriftOkPct"] = strconv.FormatFloat(common.ReconcileDriftOkPct, 'f', -1, 64)
	common.OptionMap["ReconcileDriftWarnPct"] = strconv.FormatFloat(common.ReconcileDriftWarnPct, 'f', -1, 64)
	common.OptionMap["ReconcileMaxBuckets"] = strconv.Itoa(common.ReconcileMaxBuckets)
	common.OptionMap["DataExportInterval"] = strconv.Itoa(common.DataExportInterval)
	common.OptionMap["DataExportDefaultTime"] = common.DataExportDefaultTime
	common.OptionMap["DefaultCollapseSidebar"] = strconv.FormatBool(common.DefaultCollapseSidebar)
	common.OptionMap["MjNotifyEnabled"] = strconv.FormatBool(setting.MjNotifyEnabled)
	common.OptionMap["MjAccountFilterEnabled"] = strconv.FormatBool(setting.MjAccountFilterEnabled)
	common.OptionMap["MjModeClearEnabled"] = strconv.FormatBool(setting.MjModeClearEnabled)
	common.OptionMap["MjForwardUrlEnabled"] = strconv.FormatBool(setting.MjForwardUrlEnabled)
	common.OptionMap["MjActionCheckSuccessEnabled"] = strconv.FormatBool(setting.MjActionCheckSuccessEnabled)
	common.OptionMap["CheckSensitiveEnabled"] = strconv.FormatBool(setting.CheckSensitiveEnabled)
	common.OptionMap["DemoSiteEnabled"] = strconv.FormatBool(operation_setting.DemoSiteEnabled)
	common.OptionMap["SelfUseModeEnabled"] = strconv.FormatBool(operation_setting.SelfUseModeEnabled)
	common.OptionMap["ModelRequestRateLimitEnabled"] = strconv.FormatBool(setting.ModelRequestRateLimitEnabled)
	common.OptionMap["CheckSensitiveOnPromptEnabled"] = strconv.FormatBool(setting.CheckSensitiveOnPromptEnabled)
	common.OptionMap["StopOnSensitiveEnabled"] = strconv.FormatBool(setting.StopOnSensitiveEnabled)
	common.OptionMap["SensitiveWords"] = setting.SensitiveWordsToString()
	common.OptionMap["SensitiveRefusalText"] = setting.SensitiveRefusalText
	common.OptionMap["StreamCacheQueueLength"] = strconv.Itoa(setting.StreamCacheQueueLength)
	common.OptionMap["AutomaticDisableKeywords"] = operation_setting.AutomaticDisableKeywordsToString()
	common.OptionMap["AutomaticDisableStatusCodes"] = operation_setting.AutomaticDisableStatusCodesToString()
	common.OptionMap["AutomaticRetryStatusCodes"] = operation_setting.AutomaticRetryStatusCodesToString()

	// 自动添加所有注册的模型配置
	modelConfigs := config.GlobalConfig.ExportAllConfigs()
	for k, v := range modelConfigs {
		common.OptionMap[k] = v
	}

	common.OptionMapRWMutex.Unlock()
	loadOptionsFromDatabase()
}

func loadOptionsFromDatabase() {
	pricingWriteMu.Lock()
	defer pricingWriteMu.Unlock()
	options, _ := AllOption()
	for _, option := range options {
		if option.Key == pricingWriteLockKey {
			continue
		}
		err := updateOptionMap(option.Key, option.Value)
		if err != nil {
			common.SysLog("failed to update option map: " + err.Error())
		}
	}
}

func SyncOptions(frequency int) {
	for {
		time.Sleep(time.Duration(frequency) * time.Second)
		common.SysLog("syncing options from database")
		loadOptionsFromDatabase()
	}
}

// OptionValuesFromDatabase 返回一份「以数据库为准」的配置快照。
//
// # 为什么设置页不能读本副本内存里的那份
//
// 多副本部署下（现网是 1 master + 3 slave 挂在同一个 Service 后面），保存只更新了
// 处理这次 PUT 的**那一个**副本的内存；其余副本要等 SyncOptions 的下一拍
// （`SYNC_FREQUENCY`，默认 60 秒）才会从库里刷到新值。而 `GET /api/option/`
// 返回的正是内存里的值，于是：保存成功 → 刷新设置页 → 请求落到别的副本 →
// 显示旧值。表现就是「保存完过一会儿刷新又还原了」，而库里的值其实一直是对的。
//
// 这个接口是 root 专用、低频（打开设置页才读一次），代价只是多一条
// `SELECT key, value FROM options`。
//
// **只覆盖内存里已经有的键，不引入库里的孤儿行。** `common.OptionMap` 的键集
// 是这一版代码认的配置面；库里可能还留着旧版本写过、这个版本已经不认的键，
// 把它们塞进响应只会让设置页多出几个没人认识的字段。
func OptionValuesFromDatabase() map[string]string {
	values := make(map[string]string)
	common.OptionMapRWMutex.RLock()
	for k, v := range common.OptionMap {
		values[k] = common.Interface2String(v)
	}
	common.OptionMapRWMutex.RUnlock()

	options, err := AllOption()
	if err != nil {
		// 读不到库就退回内存那份，至少设置页还能打开；下一次刷新会重试。
		common.SysLog("failed to load option values from database: " + err.Error())
		return values
	}
	for _, option := range options {
		if _, known := values[option.Key]; known {
			values[option.Key] = option.Value
		}
	}
	return values
}

func UpdateOption(key string, value string) error {
	// Save to database first
	option := Option{
		Key: key,
	}
	// https://gorm.io/docs/update.html#Save-All-Fields
	if err := DB.FirstOrCreate(&option, Option{Key: key}).Error; err != nil {
		return err
	}
	option.Value = value
	// Save is a combination function.
	// If save value does not contain primary key, it will execute Create,
	// otherwise it will execute Update (with all fields).
	//
	// **写库失败必须报出去，不能只是更新内存。** 以前这里两次写库的错误都丢了：
	// 库里没写进去，内存却换成了新值，接口照样回 success。管理员看到「保存成功」，
	// 而 60 秒后 SyncOptions 从库里重新加载，那份旧值又把内存盖回去 ——
	// 表现就是「保存完过一会儿刷新又还原了」，且日志里一个字都没有。
	// 报错之后最后一道防线还在：updateOptionMap 不会被调用，内存和库保持一致。
	if err := DB.Save(&option).Error; err != nil {
		return err
	}
	// Update OptionMap
	return updateOptionMap(key, value)
}

// UpdateOptions 在一个事务里写入多个 option，然后逐个应用到内存。
//
// 为什么需要：计费编辑器一次保存会同时改「计费模式 / 表达式 / 源货币汇率 / 六个价格表」，
// 这些值之间有依赖（表达式引用 fx 就必须有汇率；模式切到 tiered_expr 就必须有表达式）。
// 逐个 PUT 时任何一步失败都会留下半套配置，而"模式=表达式计费但没有表达式"会让整个
// 模型之后所有请求直接报错。这里保证要么全写、要么全不写。
func UpdateOptions(options map[string]string, stateKeys []string, validate func(map[string]string) error) error {
	if len(options) == 0 {
		return nil
	}
	keys := make([]string, 0, len(options))
	for key := range options {
		keys = append(keys, key)
	}
	sort.Strings(keys) // 固定顺序，便于排查与测试
	// **提交前**把每个 key 都解析一遍：真正应用发生在 commit 之后，若那时才报错，
	// 库里已经写进去了（返回 500 但数据生效，是最难排查的一类）。这里先失败先返回。
	values := make(map[string]string, len(options))
	for _, key := range keys {
		if err := ValidateOptionValueForBatch(key, options[key]); err != nil {
			return err
		}
		value := options[key]
		switch key {
		case "billing_setting.billing_mode", "billing_setting.billing_expr":
			if strings.TrimSpace(value) == "" {
				value = "{}"
			}
		}
		values[key] = value
	}

	pricingWriteMu.Lock()
	defer pricingWriteMu.Unlock()
	var final map[string]string
	err := DB.Transaction(func(tx *gorm.DB) error {
		// A write to one shared row comes first: it takes the row lock on
		// PostgreSQL/MySQL and the writer lock before any read on SQLite.
		lock := Option{Key: pricingWriteLockKey}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "key"}},
			DoUpdates: clause.AssignmentColumns([]string{"value"}),
		}).Create(&lock).Error; err != nil {
			return err
		}
		var err error
		final, err = getOptionValues(tx, stateKeys)
		if err != nil {
			return err
		}
		for key, value := range values {
			final[key] = value
		}
		if err := validate(final); err != nil {
			return err
		}
		for _, key := range keys {
			option := Option{Key: key}
			if err := tx.FirstOrCreate(&option, Option{Key: key}).Error; err != nil {
				return err
			}
			option.Value = values[key]
			if err := tx.Save(&option).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	// 落库成功后逐个应用到内存；复用 updateOptionMap（它只做内存，不写库）。
	for _, key := range stateKeys {
		if _, submitted := values[key]; !submitted {
			if value, exists := final[key]; exists {
				if (key == "billing_setting.billing_mode" || key == "billing_setting.billing_expr") && strings.TrimSpace(value) == "" {
					value = "{}"
				}
				values[key] = value
				keys = append(keys, key)
			}
		}
	}
	for _, key := range keys {
		if applyErr := updateOptionMap(key, values[key]); applyErr != nil {
			common.SysLog("failed to apply option to memory: " + key + ": " + applyErr.Error())
			return applyErr
		}
	}
	return nil
}

// ValidateOptionValueForBatch 只解析/校验批量保存允许的 key，不写库也不动内存。
func ValidateOptionValueForBatch(key, value string) error {
	trimmed := strings.TrimSpace(value)
	switch key {
	case "ModelPrice", "ModelRatio", "CompletionRatio", "CacheRatio",
		"CreateCacheRatio", "ImageRatio", "AudioRatio", "AudioCompletionRatio",
		"ModelSourceRate":
		if trimmed == "" || trimmed == "null" {
			return nil
		}
		parsed := map[string]float64{}
		if err := common.UnmarshalJsonStr(value, &parsed); err != nil {
			return fmt.Errorf("%s 不是合法的 JSON 数值表: %w", key, err)
		}
		return nil
	case "billing_setting.billing_mode", "billing_setting.billing_expr":
		if trimmed == "" || trimmed == "null" {
			return nil
		}
		parsed := map[string]string{}
		if err := common.UnmarshalJsonStr(value, &parsed); err != nil {
			return fmt.Errorf("%s 不是合法的 JSON 字符串表: %w", key, err)
		}
		return nil
	case "VideoPricingConfig":
		return ratio_setting.CheckVideoPricingByJSONString(value)
	case "GroupTimeRatio":
		return ratio_setting.CheckGroupTimeRatio(value)
	default:
		return fmt.Errorf("该接口不支持批量写入：%s", key)
	}
}

func getOptionValues(tx *gorm.DB, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	if len(keys) == 0 {
		return out, nil
	}
	rows := make([]Option, 0, len(keys))
	if err := tx.Find(&rows, keys).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.Key] = row.Value
	}
	return out, nil
}

func updateOptionMap(key string, value string) (err error) {
	common.OptionMapRWMutex.Lock()
	defer common.OptionMapRWMutex.Unlock()
	common.OptionMap[key] = value

	// 检查是否是模型配置 - 使用更规范的方式处理
	if handleConfigUpdate(key, value) {
		return nil // 已由配置系统处理
	}

	// 处理传统配置项...
	if strings.HasSuffix(key, "Permission") {
		intValue, _ := strconv.Atoi(value)
		switch key {
		case "FileUploadPermission":
			common.FileUploadPermission = intValue
		case "FileDownloadPermission":
			common.FileDownloadPermission = intValue
		case "ImageUploadPermission":
			common.ImageUploadPermission = intValue
		case "ImageDownloadPermission":
			common.ImageDownloadPermission = intValue
		}
	}
	if strings.HasSuffix(key, "Enabled") || key == "DefaultCollapseSidebar" || key == "DefaultUseAutoGroup" || key == "SMTPForceAuthLogin" {
		boolValue := value == "true"
		switch key {
		case "PasswordRegisterEnabled":
			common.PasswordRegisterEnabled = boolValue
		case "PasswordLoginEnabled":
			common.PasswordLoginEnabled = boolValue
		case "EmailVerificationEnabled":
			common.EmailVerificationEnabled = boolValue
		case "GitHubOAuthEnabled":
			common.GitHubOAuthEnabled = boolValue
		case "LinuxDOOAuthEnabled":
			common.LinuxDOOAuthEnabled = boolValue
		case "WeChatAuthEnabled":
			common.WeChatAuthEnabled = boolValue
		case "TelegramOAuthEnabled":
			common.TelegramOAuthEnabled = boolValue
		case "TurnstileCheckEnabled":
			common.TurnstileCheckEnabled = boolValue
		case "RegisterEnabled":
			common.RegisterEnabled = boolValue
		case "EmailDomainRestrictionEnabled":
			common.EmailDomainRestrictionEnabled = boolValue
		case "EmailAliasRestrictionEnabled":
			common.EmailAliasRestrictionEnabled = boolValue
		case "AutomaticDisableChannelEnabled":
			common.AutomaticDisableChannelEnabled = boolValue
		case "AutomaticEnableChannelEnabled":
			common.AutomaticEnableChannelEnabled = boolValue
		case "QuotaRemindEnabled":
			common.QuotaRemindEnabled = boolValue
		case "PlaygroundPipelineServerSide":
			common.PlaygroundPipelineServerSide = boolValue
		case "LogConsumeEnabled":
			common.LogConsumeEnabled = boolValue
		case "DisplayInCurrencyEnabled":
			// 兼容旧字段：同步到新配置 general_setting.quota_display_type（运行时生效）
			// true -> USD, false -> TOKENS
			newVal := "USD"
			if !boolValue {
				newVal = "TOKENS"
			}
			if cfg := config.GlobalConfig.Get("general_setting"); cfg != nil {
				_ = config.UpdateConfigFromMap(cfg, map[string]string{"quota_display_type": newVal})
			}
		case "DisplayTokenStatEnabled":
			common.DisplayTokenStatEnabled = boolValue
		case "DrawingEnabled":
			common.DrawingEnabled = boolValue
		case "TaskEnabled":
			common.TaskEnabled = boolValue
		case "DataExportEnabled":
			common.DataExportEnabled = boolValue
		case "DefaultCollapseSidebar":
			common.DefaultCollapseSidebar = boolValue
		case "MjNotifyEnabled":
			setting.MjNotifyEnabled = boolValue
		case "MjAccountFilterEnabled":
			setting.MjAccountFilterEnabled = boolValue
		case "MjModeClearEnabled":
			setting.MjModeClearEnabled = boolValue
		case "MjForwardUrlEnabled":
			setting.MjForwardUrlEnabled = boolValue
		case "MjActionCheckSuccessEnabled":
			setting.MjActionCheckSuccessEnabled = boolValue
		case "CheckSensitiveEnabled":
			setting.CheckSensitiveEnabled = boolValue
		case "DemoSiteEnabled":
			operation_setting.DemoSiteEnabled = boolValue
		case "SelfUseModeEnabled":
			operation_setting.SelfUseModeEnabled = boolValue
		case "CheckSensitiveOnPromptEnabled":
			setting.CheckSensitiveOnPromptEnabled = boolValue
		case "ModelRequestRateLimitEnabled":
			setting.ModelRequestRateLimitEnabled = boolValue
		case "StopOnSensitiveEnabled":
			setting.StopOnSensitiveEnabled = boolValue
		case "SMTPSSLEnabled":
			common.SMTPSSLEnabled = boolValue
		case "SMTPForceAuthLogin":
			common.SMTPForceAuthLogin = boolValue
		case "WorkerAllowHttpImageRequestEnabled":
			system_setting.WorkerAllowHttpImageRequestEnabled = boolValue
		case "DefaultUseAutoGroup":
			setting.DefaultUseAutoGroup = boolValue
		case "KYCEnabled":
			common.KYCEnabled = boolValue
		}
	}
	switch key {
	case "EmailDomainWhitelist":
		common.EmailDomainWhitelist = strings.Split(value, ",")
	case "SMTPServer":
		common.SMTPServer = value
	case "SMTPPort":
		intValue, _ := strconv.Atoi(value)
		common.SMTPPort = intValue
	case "SMTPAccount":
		common.SMTPAccount = value
	case "SMTPFrom":
		common.SMTPFrom = value
	case "SMTPToken":
		common.SMTPToken = value
	case "ServerAddress":
		system_setting.ServerAddress = value
	case "WorkerUrl":
		system_setting.WorkerUrl = value
	case "WorkerValidKey":
		system_setting.WorkerValidKey = value
	case "PayAddress":
		operation_setting.PayAddress = value
	case "Chats":
		err = setting.UpdateChatsByJsonString(value)
	case "HiloCatalog":
		err = setting.UpdateHiloCatalogByJsonString(value)
	case "AutoGroups":
		err = setting.UpdateAutoGroupsByJsonString(value)
	case "CustomCallbackAddress":
		operation_setting.CustomCallbackAddress = value
	case "EpayId":
		operation_setting.EpayId = value
	case "EpayKey":
		operation_setting.EpayKey = value
	case "Price":
		operation_setting.Price, _ = strconv.ParseFloat(value, 64)
	case "USDExchangeRate":
		operation_setting.USDExchangeRate, _ = strconv.ParseFloat(value, 64)
	case "MinTopUp":
		operation_setting.MinTopUp, _ = strconv.Atoi(value)
	case "StripeApiSecret":
		setting.StripeApiSecret = value
	case "StripeWebhookSecret":
		setting.StripeWebhookSecret = value
	case "StripePriceId":
		setting.StripePriceId = value
	case "StripeUnitPrice":
		setting.StripeUnitPrice, _ = strconv.ParseFloat(value, 64)
	case "StripeMinTopUp":
		setting.StripeMinTopUp, _ = strconv.Atoi(value)
	case "StripePromotionCodesEnabled":
		setting.StripePromotionCodesEnabled = value == "true"
	case "CreemApiKey":
		setting.CreemApiKey = value
	case "CreemProducts":
		setting.CreemProducts = value
	case "CreemTestMode":
		setting.CreemTestMode = value == "true"
	case "CreemWebhookSecret":
		setting.CreemWebhookSecret = value
	case "WaffoEnabled":
		setting.WaffoEnabled = value == "true"
	case "WaffoApiKey":
		setting.WaffoApiKey = value
	case "WaffoPrivateKey":
		setting.WaffoPrivateKey = value
	case "WaffoPublicCert":
		setting.WaffoPublicCert = value
	case "WaffoSandboxPublicCert":
		setting.WaffoSandboxPublicCert = value
	case "WaffoSandboxApiKey":
		setting.WaffoSandboxApiKey = value
	case "WaffoSandboxPrivateKey":
		setting.WaffoSandboxPrivateKey = value
	case "WaffoSandbox":
		setting.WaffoSandbox = value == "true"
	case "WaffoMerchantId":
		setting.WaffoMerchantId = value
	case "WaffoNotifyUrl":
		setting.WaffoNotifyUrl = value
	case "WaffoReturnUrl":
		setting.WaffoReturnUrl = value
	case "WaffoSubscriptionReturnUrl":
		setting.WaffoSubscriptionReturnUrl = value
	case "WaffoCurrency":
		setting.WaffoCurrency = value
	case "WaffoUnitPrice":
		setting.WaffoUnitPrice, _ = strconv.ParseFloat(value, 64)
	case "WaffoMinTopUp":
		setting.WaffoMinTopUp, _ = strconv.Atoi(value)
	case "WaffoPancakeEnabled":
		setting.WaffoPancakeEnabled = value == "true"
	case "WaffoPancakeSandbox":
		setting.WaffoPancakeSandbox = value == "true"
	case "WaffoPancakeMerchantID":
		setting.WaffoPancakeMerchantID = value
	case "WaffoPancakePrivateKey":
		setting.WaffoPancakePrivateKey = value
	case "WaffoPancakeWebhookPublicKey":
		setting.WaffoPancakeWebhookPublicKey = value
	case "WaffoPancakeWebhookTestKey":
		setting.WaffoPancakeWebhookTestKey = value
	case "WaffoPancakeStoreID":
		setting.WaffoPancakeStoreID = value
	case "WaffoPancakeProductID":
		setting.WaffoPancakeProductID = value
	case "WaffoPancakeReturnURL":
		setting.WaffoPancakeReturnURL = value
	case "WaffoPancakeCurrency":
		setting.WaffoPancakeCurrency = value
	case "WaffoPancakeUnitPrice":
		setting.WaffoPancakeUnitPrice, _ = strconv.ParseFloat(value, 64)
	case "WaffoPancakeMinTopUp":
		setting.WaffoPancakeMinTopUp, _ = strconv.Atoi(value)
	case "AlipayAppId":
		setting.AlipayAppId = value
		if AlipayClientResetHook != nil {
			AlipayClientResetHook()
		}
	case "AlipayPrivateKey":
		setting.AlipayPrivateKey = value
		if AlipayClientResetHook != nil {
			AlipayClientResetHook()
		}
	case "AlipayPublicKey":
		setting.AlipayPublicKey = value
		if AlipayClientResetHook != nil {
			AlipayClientResetHook()
		}
	case "AlipayMinTopUp":
		setting.AlipayMinTopUp, _ = strconv.Atoi(value)
	case "AlipaySandbox":
		setting.AlipaySandbox, _ = strconv.ParseBool(value)
		if AlipayClientResetHook != nil {
			AlipayClientResetHook()
		}
	case "WxpayAppId":
		setting.WxpayAppId = value
		if WxpayClientResetHook != nil {
			WxpayClientResetHook()
		}
	case "WxpayMchId":
		setting.WxpayMchId = value
		if WxpayClientResetHook != nil {
			WxpayClientResetHook()
		}
	case "WxpayPrivateKey":
		setting.WxpayPrivateKey = value
		if WxpayClientResetHook != nil {
			WxpayClientResetHook()
		}
	case "WxpayApiV3Key":
		setting.WxpayApiV3Key = value
		if WxpayClientResetHook != nil {
			WxpayClientResetHook()
		}
	case "WxpayCertSerial":
		setting.WxpayCertSerial = value
		if WxpayClientResetHook != nil {
			WxpayClientResetHook()
		}
	case "WxpayPublicKey":
		setting.WxpayPublicKey = value
		if WxpayClientResetHook != nil {
			WxpayClientResetHook()
		}
	case "WxpayPublicKeyId":
		setting.WxpayPublicKeyId = value
		if WxpayClientResetHook != nil {
			WxpayClientResetHook()
		}
	case "WxpayMinTopUp":
		setting.WxpayMinTopUp, _ = strconv.Atoi(value)
	case "TopupGroupRatio":
		err = common.UpdateTopupGroupRatioByJSONString(value)
	case "GitHubClientId":
		common.GitHubClientId = value
	case "GitHubClientSecret":
		common.GitHubClientSecret = value
	case "LinuxDOClientId":
		common.LinuxDOClientId = value
	case "LinuxDOClientSecret":
		common.LinuxDOClientSecret = value
	case "LinuxDOMinimumTrustLevel":
		common.LinuxDOMinimumTrustLevel, _ = strconv.Atoi(value)
	case "Footer":
		common.Footer = value
	case "SystemName":
		common.SystemName = value
	case "Logo":
		common.Logo = value
	case "WeChatServerAddress":
		common.WeChatServerAddress = value
	case "WeChatServerToken":
		common.WeChatServerToken = value
	case "WeChatAccountQRCodeImageURL":
		common.WeChatAccountQRCodeImageURL = value
	case "TelegramBotToken":
		common.TelegramBotToken = value
	case "TelegramBotName":
		common.TelegramBotName = value
	case "TurnstileSiteKey":
		common.TurnstileSiteKey = value
	case "TurnstileSecretKey":
		common.TurnstileSecretKey = value
	case "QuotaForNewUser":
		common.QuotaForNewUser, _ = strconv.Atoi(value)
	case "QuotaForInviter":
		common.QuotaForInviter, _ = strconv.Atoi(value)
	case "QuotaForInvitee":
		common.QuotaForInvitee, _ = strconv.Atoi(value)
	case "QuotaRemindThreshold":
		common.QuotaRemindThreshold, _ = strconv.Atoi(value)
	case "PreConsumedQuota":
		common.PreConsumedQuota, _ = strconv.Atoi(value)
	case "ModelRequestRateLimitCount":
		setting.ModelRequestRateLimitCount, _ = strconv.Atoi(value)
	case "ModelRequestRateLimitDurationMinutes":
		setting.ModelRequestRateLimitDurationMinutes, _ = strconv.Atoi(value)
	case "ModelRequestRateLimitSuccessCount":
		setting.ModelRequestRateLimitSuccessCount, _ = strconv.Atoi(value)
	case "ModelRequestRateLimitGroup":
		err = setting.UpdateModelRequestRateLimitGroupByJSONString(value)
	case "KYCMaxSubmitCount":
		common.KYCMaxSubmitCount, _ = strconv.Atoi(value)
	case "EnterpriseMaxSubmitCount":
		common.EnterpriseMaxSubmitCount, _ = strconv.Atoi(value)
	case "ReconcileUploadMaxFileBytes":
		common.ReconcileUploadMaxFileBytes, _ = strconv.ParseInt(value, 10, 64)
	case "ReconcileUploadMaxLogRangeDays":
		common.ReconcileUploadMaxLogRangeDays, _ = strconv.Atoi(value)
	case "ReconcileDriftOkPct":
		common.ReconcileDriftOkPct, _ = strconv.ParseFloat(value, 64)
	case "ReconcileDriftWarnPct":
		common.ReconcileDriftWarnPct, _ = strconv.ParseFloat(value, 64)
	case "ReconcileMaxBuckets":
		common.ReconcileMaxBuckets, _ = strconv.Atoi(value)
	case "RetryTimes":
		common.RetryTimes, _ = strconv.Atoi(value)
	case "DataExportInterval":
		common.DataExportInterval, _ = strconv.Atoi(value)
	case "DataExportDefaultTime":
		common.DataExportDefaultTime = value
	case "ModelSourceRate":
		err = billing_setting.UpdateModelSourceRateByJSONString(value)
	case "ModelRatio":
		err = ratio_setting.UpdateModelRatioByJSONString(value)
	case "GroupRatio":
		err = ratio_setting.UpdateGroupRatioByJSONString(value)
	case "GroupGroupRatio":
		err = ratio_setting.UpdateGroupGroupRatioByJSONString(value)
	case "GroupModelRatio":
		err = ratio_setting.UpdateGroupModelRatioByJSONString(value)
	case "UserGroupModelRatio":
		err = ratio_setting.UpdateUserGroupModelRatioByJSONString(value)
	case "GroupTimeRatio":
		err = ratio_setting.UpdateGroupTimeRatioByJSONString(value)
	case "UserUsableGroups":
		err = setting.UpdateUserUsableGroupsByJSONString(value)
	case "GroupEnabled":
		err = setting.UpdateGroupEnabledByJSONString(value)
	case "GroupDescription":
		err = setting.UpdateGroupDescriptionsByJSONString(value)
	case "CompletionRatio":
		err = ratio_setting.UpdateCompletionRatioByJSONString(value)
	case "ModelPrice":
		err = ratio_setting.UpdateModelPriceByJSONString(value)
	case "CacheRatio":
		err = ratio_setting.UpdateCacheRatioByJSONString(value)
	case "CreateCacheRatio":
		err = ratio_setting.UpdateCreateCacheRatioByJSONString(value)
	case "ImageRatio":
		err = ratio_setting.UpdateImageRatioByJSONString(value)
	case "AudioRatio":
		err = ratio_setting.UpdateAudioRatioByJSONString(value)
	case "AudioCompletionRatio":
		err = ratio_setting.UpdateAudioCompletionRatioByJSONString(value)
	case "VideoPricingConfig":
		err = ratio_setting.UpdateVideoPricingByJSONString(value)
	case "TopUpLink":
		common.TopUpLink = value
	//case "ChatLink":
	//	common.ChatLink = value
	//case "ChatLink2":
	//	common.ChatLink2 = value
	case "ChannelDisableThreshold":
		common.ChannelDisableThreshold, _ = strconv.ParseFloat(value, 64)
	case "QuotaPerUnit":
		common.QuotaPerUnit, _ = strconv.ParseFloat(value, 64)
	case "SensitiveWords":
		setting.SensitiveWordsFromString(value)
	case "SensitiveRefusalText":
		setting.SensitiveRefusalText = value
	case "AutomaticDisableKeywords":
		operation_setting.AutomaticDisableKeywordsFromString(value)
	case "AutomaticDisableStatusCodes":
		err = operation_setting.AutomaticDisableStatusCodesFromString(value)
	case "AutomaticRetryStatusCodes":
		err = operation_setting.AutomaticRetryStatusCodesFromString(value)
	case "StreamCacheQueueLength":
		setting.StreamCacheQueueLength, _ = strconv.Atoi(value)
	case "PayMethods":
		err = operation_setting.UpdatePayMethodsByJsonString(value)
	case "WaffoPayMethods":
		// WaffoPayMethods is read directly from OptionMap via setting.GetWaffoPayMethods().
		// The value is already stored in OptionMap at the top of this function (line: common.OptionMap[key] = value).
		// No additional in-memory variable to update.
	}
	return err
}

// handleConfigUpdate 处理分层配置更新，返回是否已处理
func handleConfigUpdate(key, value string) bool {
	parts := strings.SplitN(key, ".", 2)
	if len(parts) != 2 {
		return false // 不是分层配置
	}

	configName := parts[0]
	configKey := parts[1]

	// 获取配置对象
	cfg := config.GlobalConfig.Get(configName)
	if cfg == nil {
		return false // 未注册的配置
	}

	// 更新配置
	configMap := map[string]string{
		configKey: value,
	}
	config.UpdateConfigFromMap(cfg, configMap)

	// 特定配置的后处理
	if configName == "performance_setting" {
		performance_setting.UpdateAndSync()
	} else if configName == "tool_price_setting" {
		operation_setting.RebuildToolPriceIndex()
	} else if configName == "billing_setting" {
		InvalidatePricingCache()
		ratio_setting.InvalidateExposedDataCache()
	} else if configName == "theme" {
		system_setting.UpdateAndSyncTheme()
	}

	return true // 已处理
}
