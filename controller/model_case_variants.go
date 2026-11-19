package controller

import (
	"net/http"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

type caseVariantEntry struct {
	Model       string `json:"model"`
	ChannelIds  []int  `json:"channel_ids"`
	Enabled     bool   `json:"enabled"`
	AbilityRows int    `json:"ability_rows"`
	// 计价配置是**按名字精确匹配**的：两个仅大小写不同的名字各占一个键。只配了一边时，
	// 另一边在非自用模式下会直接报「价格未配置」拒绝请求，自用模式下回退到默认倍率
	// （37.5）—— 两种都不是运营想要的。
	//
	// 三条都要看：倍率、固定价、分段计费表达式。只看倍率会把配了固定价或表达式的模型
	// 误标成"没配价"。
	RatioConfigured      bool `json:"ratio_configured"`
	PriceConfigured      bool `json:"price_configured"`
	TieredExprConfigured bool `json:"tiered_expr_configured"`
}

type caseVariantGroup struct {
	NameLower string             `json:"name_lower"`
	Variants  []caseVariantEntry `json:"variants"`
}

// GetModelCaseVariants 列出「仅大小写不同」的模型名分组。
//
// 这类名字在系统里是**两个独立模型**：各自有 ability、各自占模型广场与体验区下拉里的
// 一项、各自按精确名匹配计价键。运营侧的观感是"下拉里有两个一模一样的模型"，而计价侧
// 的后果更实际 —— 只配了一个的大小写，另一个在非自用模式下会直接报「价格未配置」拒绝
// 请求（见 relay/helper/price.go 的 modelPriceNotConfiguredError），自用模式下则回退到
// 默认倍率 37.5，两种都不是运营预期的行为。
//
// 真实案例：站点里同时存在 kimi-k3 / Kimi-K3、qwen3.8-flash / Qwen3.8-Flash，
// 而前者被某条渠道的别名间接隐藏过，排查时极易误判成"同一个模型的两种写法"。
//
// 只读，不自动合并：改名会牵动计费键与令牌白名单，必须人工确认。
func GetModelCaseVariants(c *gin.Context) {
	var rows []model.Ability
	if err := model.DB.Select("model", "channel_id", "enabled").Find(&rows).Error; err != nil {
		common.ApiError(c, err)
		return
	}

	// 先按精确名聚合，再按小写名分组：两级都放在 Go 里做，避免依赖任何数据库的
	// collation / LOWER 排序行为（那正是这条规则在别的项目里翻过车的地方）。
	entryIndex := make(map[string]*caseVariantEntry)
	entryOrder := make([]string, 0)
	for _, row := range rows {
		name := strings.TrimSpace(row.Model)
		if name == "" {
			continue
		}
		entry, ok := entryIndex[name]
		if !ok {
			entry = &caseVariantEntry{Model: name, ChannelIds: make([]int, 0, 2)}
			entryIndex[name] = entry
			entryOrder = append(entryOrder, name)
		}
		entry.AbilityRows++
		entry.Enabled = entry.Enabled || row.Enabled
		seen := false
		for _, id := range entry.ChannelIds {
			if id == row.ChannelId {
				seen = true
				break
			}
		}
		if !seen {
			entry.ChannelIds = append(entry.ChannelIds, row.ChannelId)
		}
	}

	ratioCopy := ratio_setting.GetModelRatioCopy()
	priceCopy := ratio_setting.GetModelPriceCopy()

	groups := make(map[string][]caseVariantEntry)
	for _, name := range entryOrder {
		entry := entryIndex[name]
		sort.Ints(entry.ChannelIds)
		_, entry.RatioConfigured = ratioCopy[name]
		_, entry.PriceConfigured = priceCopy[name]
		entry.TieredExprConfigured = billing_setting.GetBillingMode(name) == "tiered_expr"
		key := strings.ToLower(name)
		groups[key] = append(groups[key], *entry)
	}

	result := make([]caseVariantGroup, 0)
	for key, variants := range groups {
		if len(variants) < 2 {
			continue
		}
		sort.Slice(variants, func(i, j int) bool { return variants[i].Model < variants[j].Model })
		result = append(result, caseVariantGroup{NameLower: key, Variants: variants})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].NameLower < result[j].NameLower })

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    gin.H{"groups": result, "total_groups": len(result)},
	})
}
