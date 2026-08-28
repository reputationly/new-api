package controller

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

func filterPricingByUsableGroups(pricing []model.Pricing, usableGroup map[string]string) []model.Pricing {
	if len(pricing) == 0 {
		return pricing
	}
	if len(usableGroup) == 0 {
		return []model.Pricing{}
	}

	filtered := make([]model.Pricing, 0, len(pricing))
	for _, item := range pricing {
		if common.StringsContains(item.EnableGroup, "all") {
			filtered = append(filtered, item)
			continue
		}
		for _, group := range item.EnableGroup {
			if _, ok := usableGroup[group]; ok {
				filtered = append(filtered, item)
				break
			}
		}
	}
	return filtered
}

// resolveGroupModelRatio 展开「分组内按模型折扣」，供前端逐模型查表。
//
// 为什么必须由后端展开成具体模型名、而不是把 wan2.2-* 这类通配规则直接下发：
// 模型广场有 classic / default / mobile 三个实现，各写一遍通配匹配就是三份
// 可能算错的价。这里给的值也已经是**最终倍率**（Layer 0/1/2 都算完），
// 前端只做 groupModelRatio[g]?.[m] ?? groupRatio[g] 这一步查表。
//
// 稀疏：只回命中了模型规则的组合。未配置时返回空 map，前端一路走原来的分支。
func resolveGroupModelRatio(userGroup string, groupRatio map[string]float64, pricing []model.Pricing) map[string]map[string]float64 {
	result := make(map[string]map[string]float64)
	allRules := ratio_setting.GetGroupModelRatioCopy()
	// Layer 3（用户档折扣）按 userGroup 索引、与使用分组无关，所以是每次调用一个
	// 定值。它必须参与「跳不跳过」的判断：只看 GroupModelRatio 会漏掉「仅配了
	// 用户档折扣」的情况，结果是模型广场显示价偏高、实扣正确——最难发现的那类
	// 不一致（docs/user-tier-pricing-and-topup-package-design.md §8.0）。
	hasUserRules := ratio_setting.HasUserGroupModelRules(userGroup)
	if len(allRules) == 0 && !hasUserRules {
		return result
	}
	for g := range groupRatio {
		// 两层规则都没有的分组才跳过，避免在模型数三位数时白跑一遍全表
		if len(allRules[g]) == 0 && !hasUserRules {
			continue
		}
		for _, item := range pricing {
			res := ratio_setting.ResolveGroupRatio(userGroup, g, item.ModelName)
			// 保持稀疏：只回「靠 group_ratio 算不出来」的组合。
			// Layer 3 的 "*" 兜底已折进 group_ratio（见 GetPricing），若把它也算作
			// 命中，配一条 "*" 就会把三位数的模型全表展开——响应体积暴涨，且每一项
			// 都与 fallback 值相同。故只认非 "*" 的用户档规则。
			hitModelRule := res.RuleMatch != ""
			hitSpecificUserRule := res.UserRuleMatch != "" && res.UserRuleMatch != "*"
			if !hitModelRule && !hitSpecificUserRule {
				continue
			}
			if result[g] == nil {
				result[g] = make(map[string]float64)
			}
			result[g][item.ModelName] = res.Final
		}
	}
	return result
}

func GetPricing(c *gin.Context) {
	pricing := model.GetPricing()
	userId, exists := c.Get("id")
	usableGroup := map[string]string{}
	groupRatio := map[string]float64{}
	for s, f := range ratio_setting.GetGroupRatioCopy() {
		groupRatio[s] = f
	}
	var group string
	if exists {
		user, err := model.GetUserCache(userId.(int))
		if err == nil {
			// 计费主体的分组：企业子账号用主账号的令牌调用，折扣按主账号算，
			// 展示必须同口径，否则子账号看到的价与实扣不符（§6ter.2）
			group = model.GetBillingUserGroup(user)
			// Layer 3 的 "*" 兜底与模型无关，折进 group_ratio，让前端 fallback
			// 分支（未命中 group_model_ratio 的模型）也拿到正确的价。逐模型的
			// Layer 3 规则由 resolveGroupModelRatio 展开，不走这里。
			userFallback := ratio_setting.GetUserGroupFallbackRatio(group)
			for g := range groupRatio {
				ratio, ok := ratio_setting.GetGroupGroupRatio(group, g)
				if ok {
					groupRatio[g] = ratio
				}
				if userFallback != 1 {
					groupRatio[g] *= userFallback
				}
			}
		}
	}

	usableGroup = service.GetUserUsableGroups(group)
	pricing = filterPricingByUsableGroups(pricing, usableGroup)
	// check groupRatio contains usableGroup
	for group := range ratio_setting.GetGroupRatioCopy() {
		if _, ok := usableGroup[group]; !ok {
			delete(groupRatio, group)
		}
	}

	groupModelRatio := resolveGroupModelRatio(group, groupRatio, pricing)

	// 积分展示：只回传「用户可见 ∩ 白名单」的分组，供模型广场追加积分单价（§8bis.2）
	pointsSetting := operation_setting.GetPointsSetting()
	pointsEnabledGroups := make([]string, 0)
	if pointsSetting.Enabled {
		for _, g := range pointsSetting.EnabledGroups {
			if _, ok := usableGroup[g]; ok {
				pointsEnabledGroups = append(pointsEnabledGroups, g)
			}
		}
	}

	c.JSON(200, gin.H{
		"success":               true,
		"data":                  pricing,
		"vendors":               model.GetVendors(),
		"group_ratio":           groupRatio,
		"group_model_ratio":     groupModelRatio,
		"usable_group":          usableGroup,
		"supported_endpoint":    model.GetSupportedEndpointMap(),
		"auto_groups":           service.GetUserAutoGroup(group),
		"points_enabled":        pointsSetting.Enabled,
		"quota_per_point":       pointsSetting.QuotaPerPoint,
		"points_enabled_groups": pointsEnabledGroups,
		"pricing_version":       "a42d372ccf0b5dd13ecf71203521f9d2",
	})
}

// GetAllPricing 返回**未按调用者分组过滤**的模型全集，仅管理员可用。
//
// 与 GetPricing 的分工：那份是模型广场用的——问的是「我这个用户能买什么」，
// 按 GetUserUsableGroups 裁剪天经地义。而运营配置页（体验区管理）问的是
// 「站点上有哪些模型可以配给用户用」，跟配置者本人在哪个分组无关：管理员若在
// 某个专用分组里，用那份接口会把挂在其他分组上的模型整个藏掉，下拉直接空。
//
// 只回 pricing 数组：group_ratio / usable_group / 积分那几项都是按调用者裁剪的
// 用户视角数据，配置页不需要，也不该在这里给出一份没裁剪的。
func GetAllPricing(c *gin.Context) {
	c.JSON(200, gin.H{
		"success": true,
		"data":    model.GetPricing(),
	})
}

func ResetModelRatio(c *gin.Context) {
	defaultStr := ratio_setting.DefaultModelRatio2JSONString()
	err := model.UpdateOption("ModelRatio", defaultStr)
	if err != nil {
		c.JSON(200, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	err = ratio_setting.UpdateModelRatioByJSONString(defaultStr)
	if err != nil {
		c.JSON(200, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(200, gin.H{
		"success": true,
		"message": "重置模型倍率成功",
	})
}
