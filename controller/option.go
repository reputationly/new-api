package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/service/mediastore"
	"github.com/QuantumNous/new-api/service/moderation"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/console_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
)

var completionRatioMetaOptionKeys = []string{
	"ModelPrice",
	"ModelRatio",
	"CompletionRatio",
	"CacheRatio",
	"CreateCacheRatio",
	"ImageRatio",
	"AudioRatio",
	"AudioCompletionRatio",
}

func isVisiblePublicKeyOption(key string) bool {
	switch key {
	case "WaffoPancakeWebhookPublicKey", "WaffoPancakeWebhookTestKey",
		"AlipayPublicKey", "WxpayPublicKey":
		return true
	default:
		return false
	}
}

func collectModelNamesFromOptionValue(raw string, modelNames map[string]struct{}) {
	if strings.TrimSpace(raw) == "" {
		return
	}

	var parsed map[string]any
	if err := common.UnmarshalJsonStr(raw, &parsed); err != nil {
		return
	}

	for modelName := range parsed {
		modelNames[modelName] = struct{}{}
	}
}

func buildCompletionRatioMetaValue(optionValues map[string]string) string {
	modelNames := make(map[string]struct{})
	for _, key := range completionRatioMetaOptionKeys {
		collectModelNamesFromOptionValue(optionValues[key], modelNames)
	}

	meta := make(map[string]ratio_setting.CompletionRatioInfo, len(modelNames))
	for modelName := range modelNames {
		meta[modelName] = ratio_setting.GetCompletionRatioInfo(modelName)
	}

	jsonBytes, err := common.Marshal(meta)
	if err != nil {
		return "{}"
	}
	return string(jsonBytes)
}

func GetOptions(c *gin.Context) {
	var options []*model.Option
	// **值以数据库为准，不读本副本的内存。**
	//
	// 多副本部署下（1 master + 3 slave 同一个 Service），保存只更新了处理那次
	// PUT 的副本的内存，其余副本最多滞后 `SYNC_FREQUENCY`（默认 60 秒）才从库里
	// 刷到新值。设置页读的是内存里的那份，于是「保存成功 → 刷新 → 请求落到
	// 别的副本 → 显示旧值」，看起来就是保存被还原了，而库里一直是对的。
	// 来龙去脉见 model.OptionValuesFromDatabase 的注释。
	values := model.OptionValuesFromDatabase()
	for k, value := range values {
		isSensitiveKey := strings.HasSuffix(k, "Token") ||
			strings.HasSuffix(k, "Secret") ||
			strings.HasSuffix(k, "Key") ||
			strings.HasSuffix(k, "secret") ||
			strings.HasSuffix(k, "api_key") ||
			// media_storage.* 凭证：json tag 为下划线小写（access_key_id / secret_access_key），
			// 不命中上面的大写后缀，需显式排除，避免 GET /api/option/ 泄露 OBS AK/SK。
			strings.HasSuffix(k, "access_key_id") ||
			strings.HasSuffix(k, "secret_access_key")
		if isSensitiveKey && !isVisiblePublicKeyOption(k) {
			continue
		}
		// moderation.endpoints 是个 JSON 数组，凭证藏在数组元素的 api_key 里，
		// 命不中上面按后缀做的过滤。整条不返回又不行——同一个键里还装着
		// base_url / model / enabled 这些配置页要渲染的字段，只能逐条抹。
		if k == system_setting.ModerationEndpointsOptionKey {
			value = system_setting.RedactModerationEndpoints(value)
		}
		options = append(options, &model.Option{
			Key:   k,
			Value: value,
		})
	}
	options = append(options, &model.Option{
		Key:   "CompletionRatioMeta",
		Value: buildCompletionRatioMetaValue(values),
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    options,
	})
	return
}

type OptionUpdateRequest struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

// pricingBatchKeys 批量保存允许的 key：只开放相关计费配置，
// 避免这个接口变成「任意 option 批量写入」的后门（其它 key 有各自的校验逻辑）。
var pricingBatchKeys = map[string]bool{
	"ModelPrice":                    true,
	"ModelRatio":                    true,
	"CompletionRatio":               true,
	"CacheRatio":                    true,
	"CreateCacheRatio":              true,
	"ImageRatio":                    true,
	"AudioRatio":                    true,
	"AudioCompletionRatio":          true,
	"billing_setting.billing_mode":  true,
	"billing_setting.billing_expr":  true,
	"ModelSourceRate":               true,
	"ModelPriceCurrency":            true,
	"VideoPricingConfig":            true,
	"GroupTimeRatio":                true,
	"tool_price_setting.prices":     true,
	"tool_price_setting.currencies": true,
}

var groupBatchKeys = map[string]bool{
	"GroupRatio": true, "UserUsableGroups": true, "GroupDescription": true, "GroupEnabled": true,
	"GroupGroupRatio": true, "GroupModelRatio": true, "UserGroupModelRatio": true, "GroupTimeRatio": true,
	"group_ratio_setting.group_special_usable_group": true, "AutoGroups": true, "DefaultUseAutoGroup": true,
	"TopupGroupRatio": true, "ModelRequestRateLimitGroup": true, "points_setting.enabled_groups": true,
}

type optionBatchRequest struct {
	Options map[string]string `json:"options"`
}

var pricingStateKeys = []string{
	"billing_setting.billing_mode", "billing_setting.billing_expr", "ModelSourceRate", "ModelPriceCurrency", "GroupTimeRatio",
	"tool_price_setting.currencies",
}

func parsePricingStringMap(raw string) (map[string]string, error) {
	values := make(map[string]string)
	if strings.TrimSpace(raw) != "" {
		if err := common.UnmarshalJsonStr(raw, &values); err != nil {
			return nil, err
		}
	}
	return values, nil
}

func validatePricingFinalState(final map[string]string) error {
	modes, err := parsePricingStringMap(final["billing_setting.billing_mode"])
	if err != nil {
		return fmt.Errorf("计费模式配置不是合法的 JSON：%w", err)
	}
	exprJSON := final["billing_setting.billing_expr"]
	exprs, err := parsePricingStringMap(exprJSON)
	if err != nil {
		return fmt.Errorf("计费表达式配置不是合法的 JSON：%w", err)
	}
	var missing []string
	for name, mode := range modes {
		if mode == billing_setting.BillingModeTieredExpr && strings.TrimSpace(exprs[name]) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("模型 %s 使用分段计费，但没有设置计费表达式", strings.Join(missing, "、"))
	}
	rate, err := billing_setting.ParseGlobalModelSourceRate(final["ModelSourceRate"])
	if err != nil {
		return fmt.Errorf("汇率配置不是合法的 JSON：%w", err)
	}
	if err := billing_setting.ValidateBillingExprJSONWithRate(exprJSON, rate); err != nil {
		return err
	}
	currencies, err := billing_setting.ParseModelPriceCurrencies(final["ModelPriceCurrency"])
	if err != nil {
		return err
	}
	for name, currency := range currencies {
		if currency != billing_setting.CurrencyUSD {
			continue
		}
		if rate <= 0 {
			return fmt.Errorf("模型 %s 使用美元定价，请先设置全局源汇率", name)
		}
		expr, _, _ := strings.Cut(strings.TrimSpace(exprs[name]), "|||")
		if modes[name] == billing_setting.BillingModeTieredExpr && !billingexpr.UsedVars(strings.TrimSpace(expr))["fx"] {
			return fmt.Errorf("模型 %s 使用美元分段计费，计价表达式必须引用 fx", name)
		}
	}
	toolCurrencies, err := operation_setting.ParseToolPriceCurrencies(final["tool_price_setting.currencies"])
	if err != nil {
		return err
	}
	for tool, currency := range toolCurrencies {
		if currency == billing_setting.CurrencyUSD && rate <= 0 {
			return fmt.Errorf("工具 %s 使用美元定价，请先设置全局源汇率", tool)
		}
	}
	return checkTimePriceVsExprs(final["GroupTimeRatio"], exprs)
}

func validatePricingChange(before, after map[string]string) error {
	if before["ModelPriceCurrency"] == after["ModelPriceCurrency"] {
		return nil
	}
	from, err := billing_setting.ParseModelPriceCurrencies(before["ModelPriceCurrency"])
	if err != nil {
		return err
	}
	to, err := billing_setting.ParseModelPriceCurrencies(after["ModelPriceCurrency"])
	if err != nil {
		return err
	}
	var changed []string
	for name, currency := range from {
		if currency == billing_setting.CurrencyUSD && to[name] != billing_setting.CurrencyUSD {
			changed = append(changed, name)
		}
	}
	for name, currency := range to {
		if currency == billing_setting.CurrencyUSD && from[name] != billing_setting.CurrencyUSD {
			changed = append(changed, name)
		}
	}
	sort.Strings(changed)
	return ratio_setting.CheckTimePricesOnCurrencyChange(before["GroupTimeRatio"], after["GroupTimeRatio"], changed)
}

func savePricingOptions(c *gin.Context, options map[string]string, action, summary string) {
	audit := model.OptionAudit{
		ActorID: c.GetInt("id"), Username: c.GetString("username"), Role: c.GetInt("role"),
		IP: c.ClientIP(), RequestID: c.GetString(common.RequestIdKey), Action: action, Summary: summary,
	}
	if err := model.UpdateOptionsWithAudit(options, pricingStateKeys, validatePricingFinalState, validatePricingChange, &audit); err != nil {
		var validationErr *model.OptionValidationError
		if errors.As(err, &validationErr) {
			common.ApiError(c, validationErr)
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "保存失败：" + err.Error()})
		return
	}
	model.InvalidatePricingCache()
	ratio_setting.InvalidateExposedDataCache()
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}

func UpdatePricingOptions(c *gin.Context) {
	updateScopedOptions(c, pricingBatchKeys, "model_pricing_update", "修改模型定价")
}

func UpdateGroupOptions(c *gin.Context) {
	updateScopedOptions(c, groupBatchKeys, "group_config_update", "修改分组配置")
}

func updateScopedOptions(c *gin.Context, allowed map[string]bool, action, summary string) {
	var req optionBatchRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil || len(req.Options) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "无效的参数"})
		return
	}
	for key := range req.Options {
		if !allowed[key] {
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"message": "该接口不支持批量写入：" + key,
			})
			return
		}
	}
	if _, hasRate := req.Options["ModelSourceRate"]; hasRate && len(req.Options) != 1 {
		common.ApiError(c, fmt.Errorf("全局源汇率必须独立保存"))
		return
	}

	savePricingOptions(c, req.Options, action, summary)
}

func scopedOptions(values map[string]string, allowed map[string]bool) []*model.Option {
	options := make([]*model.Option, 0, len(allowed))
	for key := range allowed {
		if value, ok := values[key]; ok {
			options = append(options, &model.Option{Key: key, Value: value})
		}
	}
	return options
}

func GetPricingOptions(c *gin.Context) {
	names, err := model.GetEnabledModelNames()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	values := model.OptionValuesFromDatabase()
	options := scopedOptions(values, pricingBatchKeys)
	options = append(options, &model.Option{Key: "CompletionRatioMeta", Value: buildCompletionRatioMetaValue(values)})
	c.JSON(http.StatusOK, gin.H{"success": true, "data": options, "enabled_models": names})
}

func GetGroupOptions(c *gin.Context) {
	values := model.OptionValuesFromDatabase()
	options := scopedOptions(values, groupBatchKeys)
	options = append(options, &model.Option{Key: "points_setting.enabled", Value: values["points_setting.enabled"]})
	common.ApiSuccess(c, options)
}

func UpdateOption(c *gin.Context) {
	var option OptionUpdateRequest
	err := common.DecodeJson(c.Request.Body, &option)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "无效的参数",
		})
		return
	}
	pricingOption := pricingBatchKeys[option.Key]
	if model.IsFixedQuotaDisplayOption(option.Key) {
		common.ApiErrorMsg(c, "额度展示固定为人民币")
		return
	}
	if option.Key == "USDExchangeRate" || option.Key == "QuotaPerUnit" {
		common.ApiErrorMsg(c, "此配置已停用，请使用全局模型源汇率")
		return
	}
	if pricingOption && option.Value == nil {
		option.Value = "null"
	}
	switch option.Value.(type) {
	case bool:
		option.Value = common.Interface2String(option.Value.(bool))
	case float64:
		option.Value = common.Interface2String(option.Value.(float64))
	case int:
		option.Value = common.Interface2String(option.Value.(int))
	default:
		option.Value = fmt.Sprintf("%v", option.Value)
	}
	// 计费相关的 key 单键 PUT 也必须走同一套「最终状态」校验：
	// 否则旧接口仍能把仍被 fx 表达式引用的汇率删掉（本地复现：删完请求 400）。
	if pricingOption {
		savePricingOptions(c, map[string]string{option.Key: option.Value.(string)}, "model_pricing_update", "修改模型定价")
		return
	}
	if groupBatchKeys[option.Key] {
		savePricingOptions(c, map[string]string{option.Key: option.Value.(string)}, "group_config_update", "修改分组配置")
		return
	}
	switch option.Key {
	case "GitHubOAuthEnabled":
		if option.Value == "true" && common.GitHubClientId == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无法启用 GitHub OAuth，请先填入 GitHub Client Id 以及 GitHub Client Secret！",
			})
			return
		}
	case "discord.enabled":
		if option.Value == "true" && system_setting.GetDiscordSettings().ClientId == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无法启用 Discord OAuth，请先填入 Discord Client Id 以及 Discord Client Secret！",
			})
			return
		}
	case "oidc.enabled":
		if option.Value == "true" && system_setting.GetOIDCSettings().ClientId == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无法启用 OIDC 登录，请先填入 OIDC Client Id 以及 OIDC Client Secret！",
			})
			return
		}
	case "checkin_setting.reward_type":
		// 签到奖励发额度会把营销赠送混进 User.Quota（现金池），令「真实入账」口径虚高。
		// 赠送一律走积分池，现金池只留真金白银。
		//
		// 只在积分系统启用时拦截：未启用积分系统时整个赠送体系不存在，此时拒绝 quota
		// 会让签到功能无奖励可发、直接不可配置。
		//
		// 拦的是「把奖励类型设成 quota」这个动作本身，不是「存量为 quota 时的任何保存」——
		// 分层配置按 key 逐个更新，运营改 min_quota 等其它字段不会撞上这道门。
		if option.Value == model.CheckinRewardQuota && operation_setting.GetPointsSetting().Enabled {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "签到奖励已统一为积分：发放额度会使赠送混入现金池，导致入账对账口径失真",
			})
			return
		}
	case "LinuxDOOAuthEnabled":
		if option.Value == "true" && common.LinuxDOClientId == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无法启用 LinuxDO OAuth，请先填入 LinuxDO Client Id 以及 LinuxDO Client Secret！",
			})
			return
		}
	case "EmailDomainRestrictionEnabled":
		if option.Value == "true" && len(common.EmailDomainWhitelist) == 0 {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无法启用邮箱域名限制，请先填入限制的邮箱域名！",
			})
			return
		}
	case "WeChatAuthEnabled":
		if option.Value == "true" && common.WeChatServerAddress == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无法启用微信登录，请先填入微信登录相关配置信息！",
			})
			return
		}
	case "TurnstileCheckEnabled":
		if option.Value == "true" && common.TurnstileSiteKey == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无法启用 Turnstile 校验，请先填入 Turnstile 校验相关配置信息！",
			})

			return
		}
	case "TelegramOAuthEnabled":
		if option.Value == "true" && common.TelegramBotToken == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无法启用 Telegram OAuth，请先填入 Telegram Bot Token！",
			})
			return
		}
	case "theme.frontend":
		if option.Value != "default" && option.Value != "classic" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无效的主题值，可选值：default（新版前端）、classic（经典前端）",
			})
			return
		}
	case "points_setting.new_user_expire_days",
		"points_setting.kyc_verified_expire_days",
		"points_setting.kyc_inviter_expire_days",
		"checkin_setting.points_expire_weeks":
		// 积分有效期的范围校验。0 一律表示「永不过期」，不是「继承默认」——所以下限是 0
		// 而不是 1。上限防误填把批次推到遥远的未来（天数 10 年 / 周数 52 周）。
		//
		// 没有这道校验时，输入框里填个负数或 99999 会原样落库，进而在发放时把
		// expires_at 算成一个荒诞值：负数当天就全作废，过大则永远等不到清理。
		raw, _ := option.Value.(string)
		n, convErr := strconv.Atoi(strings.TrimSpace(raw))
		upper := operation_setting.MaxPointsExpireDays
		unit := "天"
		if option.Key == "checkin_setting.points_expire_weeks" {
			upper = operation_setting.MaxPointsExpireWeeks
			unit = "周"
		}
		if convErr != nil || n < 0 || n > upper {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": fmt.Sprintf("积分有效期需是 0~%d 之间的整数（%s，0 = 永不过期）", upper, unit),
			})
			return
		}
	case "points_setting.channel_rewards":
		err = operation_setting.CheckChannelPointsRewards(option.Value.(string))
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	case "AutomaticDisableStatusCodes":
		_, err = operation_setting.ParseHTTPStatusCodeRanges(option.Value.(string))
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	case "AutomaticRetryStatusCodes":
		_, err = operation_setting.ParseHTTPStatusCodeRanges(option.Value.(string))
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	case "console_setting.api_info":
		err = console_setting.ValidateConsoleSettings(option.Value.(string), "ApiInfo")
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	case "console_setting.announcements":
		err = console_setting.ValidateConsoleSettings(option.Value.(string), "Announcements")
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	case "console_setting.faq":
		err = console_setting.ValidateConsoleSettings(option.Value.(string), "FAQ")
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	case "console_setting.uptime_kuma_groups":
		err = console_setting.ValidateConsoleSettings(option.Value.(string), "UptimeKumaGroups")
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	case "media_storage.access_key_id", "media_storage.secret_access_key",
		"user_asset_storage.access_key_id", "user_asset_storage.secret_access_key",
		"moderation_storage.access_key_id", "moderation_storage.secret_access_key":
		// OBS 凭证加密后入库（GET 已过滤不回显）。空值表示「保持不变」，直接返回不覆盖。
		plain := option.Value.(string)
		if plain == "" {
			c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
			return
		}
		enc, encErr := common.EncryptOBSSecret(plain)
		if encErr != nil {
			common.ApiError(c, encErr)
			return
		}
		option.Value = enc
	case "moderation.policies":
		// 类别名、动作、严格度写错都不会报错，只会让判定静默按别的规则走
		// （未登记类别一律按 block 处置）。校验放在这条唯一的写入路径上。
		if err := system_setting.ValidateModerationPoliciesJSON(option.Value.(string)); err != nil {
			common.ApiError(c, err)
			return
		}
	case "moderation.group_policies":
		if err := system_setting.ValidateGroupPoliciesJSON(option.Value.(string)); err != nil {
			common.ApiError(c, err)
			return
		}
	case "moderation.default_policy":
		if err := system_setting.ValidateDefaultPolicyName(option.Value.(string)); err != nil {
			common.ApiError(c, err)
			return
		}
	case "watermark.templates":
		if err := system_setting.ValidateWatermarkTemplatesJSON(option.Value.(string)); err != nil {
			common.ApiError(c, err)
			return
		}
	case "watermark.default_binding":
		if err := system_setting.ValidateWatermarkDefaultBindingJSON(option.Value.(string)); err != nil {
			common.ApiError(c, err)
			return
		}
	case "watermark.group_bindings":
		if err := system_setting.ValidateWatermarkGroupBindingsJSON(option.Value.(string)); err != nil {
			common.ApiError(c, err)
			return
		}
	case system_setting.ModerationEndpointsOptionKey:
		// 审核节点凭证加密入库（GET 已抹掉不回显）。空 api_key 表示「保持不变」，
		// 由 EncryptModerationEndpoints 按 name 从已存配置里取回原密文。
		enc, encErr := system_setting.EncryptModerationEndpoints(option.Value.(string))
		if encErr != nil {
			common.ApiError(c, encErr)
			return
		}
		option.Value = enc
		// 熔断结论是基于旧配置得出的，配置一换就作废。不清的话会出现最难解释的那种
		// 状态：管理员改对了地址或密钥，测试连接是绿的，生产流量却还要熬满一个冻结
		// 窗口（401 那档 10 分钟）才恢复。
		moderation.ClearAllFreezes()
	case "media_storage.enabled":
		// 启用媒体存储前用当前已保存的配置跑一次连通性校验（PutObject+DeleteObject）。
		if option.Value == "true" {
			if hcErr := mediastore.Healthcheck(context.Background()); hcErr != nil {
				c.JSON(http.StatusOK, gin.H{
					"success": false,
					"message": "OBS 连接校验失败，请检查 Endpoint / Bucket / AK/SK：" + hcErr.Error(),
				})
				return
			}
		}
	case "user_asset_storage.enabled":
		// 启用用户素材独立桶前同样跑连通性校验。
		if option.Value == "true" {
			if hcErr := mediastore.UserAssetHealthcheck(context.Background()); hcErr != nil {
				c.JSON(http.StatusOK, gin.H{
					"success": false,
					"message": "用户素材 OBS 连接校验失败，请检查 Endpoint / Bucket / AK/SK：" + hcErr.Error(),
				})
				return
			}
		}
	case "moderation_storage.enabled":
		// 启用审核取证独立桶前同样跑连通性校验。
		//
		// 这一条比另外两个更要紧：取证桶配错了不会有任何日常报错——判定照常、
		// 拦截照常，只是留存悄悄失败，等到有人要复核违规图时才发现一张都没存下来。
		if option.Value == "true" {
			if hcErr := mediastore.ModerationHealthcheck(context.Background()); hcErr != nil {
				c.JSON(http.StatusOK, gin.H{
					"success": false,
					"message": "审核取证 OBS 连接校验失败，请检查 Endpoint / Bucket / AK/SK：" + hcErr.Error(),
				})
				return
			}
		}
	}
	err = model.UpdateOption(option.Key, option.Value.(string))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	// 图片/视频模型配置里声明的「能力」会作为标签在模型广场展示；
	// 视频计费矩阵本身也直接进定价页。保存后失效定价缓存，使 /api/pricing 下次重建即最新。
	switch option.Key {
	case "ImageModelSizeConfig", "VideoModelConfig", "AudioModelConfig", "MusicModelConfig",
		"VideoPricingConfig":
		model.InvalidatePricingCache()
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}
