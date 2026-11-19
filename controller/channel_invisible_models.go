package controller

import (
	"net/http"
	"sort"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// channelAbilityStat 是「按渠道聚合的 ability 行数」的扫描目标。
//
// SUM(CASE WHEN enabled THEN 1 ELSE 0 END) 三种数据库都支持（SQLite/MySQL 靠布尔→整数的
// 隐式转换，PostgreSQL 直接吃布尔条件），比 bool_or / FILTER 这类方言安全得多。
type channelAbilityStat struct {
	ChannelId  int   `json:"channel_id"`
	Total      int64 `json:"total"`
	EnabledCnt int64 `json:"enabled_cnt"`
}

type channelInvisibleSummary struct {
	// ByChannel 渠道 id -> 该渠道下 enabled=false 的 ability 行数。
	// 渠道被禁用时能力行会被整体置为 false（见 model.UpdateAbilityStatus），所以这个数字
	// 就是"这条渠道让多少个模型对用户不可见"。
	ByChannel map[string]int `json:"by_channel"`
	// InvisibleModels 全站有多少个模型名**一条启用中的 ability 都没有** —— 用户在
	// 模型广场、体验区、令牌白名单里都看不到它们。
	InvisibleModels int `json:"invisible_models"`
	// InvisibleModelNames 上一项的明细，便于直接在页面上列出来。
	InvisibleModelNames []string `json:"invisible_model_names"`
}

// GetChannelInvisibleModels 汇总「哪些模型因为渠道停用而对用户不可见」。
//
// 解决的问题不是数据错误，而是**可见性语义没有出口**：渠道被禁用时，模型会从广场、
// 体验区、令牌白名单里一起消失，但"渠道页只显示一个禁用红点"，没人能把它和"我今天
// 少了一批模型"联系起来。真实场景是一次盘点里发现二十多条渠道长期 status=2，背后是
// 几十个模型悄无声息地不可用。
//
// 三条查询都在 Go 侧聚合，不依赖任何方言函数：abilities 是「模型 × 渠道」量级的表，
// 但结果集只有"每渠道一行"和"去重模型名"，管理页每次加载的代价可以接受。
func GetChannelInvisibleModels(c *gin.Context) {
	var stats []channelAbilityStat
	if err := model.DB.Model(&model.Ability{}).
		Select("channel_id, COUNT(*) AS total, SUM(CASE WHEN enabled THEN 1 ELSE 0 END) AS enabled_cnt").
		Group("channel_id").
		Scan(&stats).Error; err != nil {
		common.ApiError(c, err)
		return
	}

	byChannel := make(map[string]int, len(stats))
	for _, stat := range stats {
		if invisible := stat.Total - stat.EnabledCnt; invisible > 0 {
			byChannel[strconv.Itoa(stat.ChannelId)] = int(invisible)
		}
	}

	// 有启用 ability 的模型（= 用户能看到的那批）
	var enabledModels []string
	if err := model.DB.Model(&model.Ability{}).
		Where("enabled = ?", true).
		Distinct("model").
		Pluck("model", &enabledModels).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	visible := make(map[string]bool, len(enabledModels))
	for _, name := range enabledModels {
		visible[name] = true
	}

	var allModels []string
	if err := model.DB.Model(&model.Ability{}).
		Distinct("model").
		Pluck("model", &allModels).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	invisibleNames := make([]string, 0)
	for _, name := range allModels {
		if !visible[name] {
			invisibleNames = append(invisibleNames, name)
		}
	}
	sort.Strings(invisibleNames)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": channelInvisibleSummary{
			ByChannel:           byChannel,
			InvisibleModels:     len(invisibleNames),
			InvisibleModelNames: invisibleNames,
		},
	})
}
