package controller

import (
	"net/http"
	"sort"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// channelDisabledModelRow 是「某渠道下被置为停用的能力行」的扫描目标。
type channelDisabledModelRow struct {
	ChannelId int    `json:"channel_id"`
	Model     string `json:"model"`
}

type channelInvisibleSummary struct {
	// ByChannel 渠道 id -> 该渠道上「当前对用户不可见」的模型数（按模型去重，不是能力行数：
	// 一个渠道挂多个分组会让行数翻倍）。别的渠道还在卖的模型不计入。
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
// 两条查询都在 Go 侧聚合，不依赖任何方言函数（`bool_or` / `FILTER` 这类都是方言）：
// abilities 是「模型 × 渠道」量级的表，但结果集只有去重模型名与停用行，管理页每次加载
// 的代价可以接受。
func GetChannelInvisibleModels(c *gin.Context) {
	// 用户能看到一个模型，当且仅当它**至少有一条启用的 ability**。先取这个集合：
	// 后面两个口径都基于它，避免"某渠道禁用了但别的渠道还在卖"被重复计入。
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

	// 停用的能力行（渠道禁用时整批置 false）。按「渠道 × 模型」去重再计数：
	// 一条渠道可能挂多个分组，直接数行会让数字翻倍，而运营关心的是"多少个模型"。
	var disabledRows []channelDisabledModelRow
	if err := model.DB.Model(&model.Ability{}).
		Select("channel_id, model").
		Where("enabled = ?", false).
		Scan(&disabledRows).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	perChannelModels := make(map[int]map[string]bool, len(disabledRows))
	invisible := make(map[string]bool)
	for _, row := range disabledRows {
		name := row.Model
		if name == "" || visible[name] {
			// 别的渠道还在卖 -> 对用户仍然可见，不该算进任何一边
			continue
		}
		invisible[name] = true
		set, ok := perChannelModels[row.ChannelId]
		if !ok {
			set = make(map[string]bool)
			perChannelModels[row.ChannelId] = set
		}
		set[name] = true
	}

	byChannel := make(map[string]int, len(perChannelModels))
	for channelId, names := range perChannelModels {
		if len(names) > 0 {
			byChannel[strconv.Itoa(channelId)] = len(names)
		}
	}

	invisibleNames := make([]string, 0, len(invisible))
	for name := range invisible {
		invisibleNames = append(invisibleNames, name)
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
