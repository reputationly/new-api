package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// playgroundMediaConfigKeys 与 common/media_model_config.go 的四份体验区模型配置同名。
// 模型一旦出现在其中任一份的 models{} 里，文本体验区也会排除它（即便端点类型是 openai）。
var playgroundMediaConfigKeys = []string{
	"ImageModelSizeConfig",
	"VideoModelConfig",
	"AudioModelConfig",
	"MusicModelConfig",
}

type diagnoseAbility struct {
	ChannelId     int    `json:"channel_id"`
	ChannelName   string `json:"channel_name"`
	Group         string `json:"group"`
	Enabled       bool   `json:"enabled"`
	ChannelStatus int    `json:"channel_status"`
}

type diagnoseVariant struct {
	Model      string `json:"model"`
	Enabled    bool   `json:"enabled"`
	ChannelIds []int  `json:"channel_ids"`
}

// DiagnoseChannelModel 回答一个问题：**为什么这个模型在体验区/模型列表里看不到**。
//
// 背景（真实事故，反复发生）：模型不可见有五六种互相独立的原因，分布在 DB 行、运行时
// 缓存和前端过滤三层，而每一层都是静默失败 —— 保存渠道返回成功、DB 里也确实有那条记录，
// 但用户永远看不到它，页面上没有任何地方能解释为什么。此前只能靠人工对着 abilities、
// channels.model_alias、/api/pricing 一路猜。
//
// 这个接口把那些判据集中在一处（复用各自的判定函数，不另写一套），逐个给出结论，
// 并顺带指出"谁干的"（哪个渠道的别名、哪条配置占用了这个模型名）。
//
// 只给超管用：响应里有渠道 id/名称/分组，属于内部配置，不能下发给普通用户。
func DiagnoseChannelModel(c *gin.Context) {
	name := strings.TrimSpace(c.Query("model"))
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "model 参数不能为空",
		})
		return
	}

	result, err := diagnoseChannelModel(name)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    result,
	})
}

type channelModelDiagnosis struct {
	Model         string                     `json:"model"`
	Visible       bool                       `json:"visible"`
	Blockers      []string                   `json:"blockers"`
	Warnings      []string                   `json:"warnings"`
	Abilities     []diagnoseAbility          `json:"abilities"`
	Groups        []string                   `json:"groups"`
	CaseVariants  []diagnoseVariant          `json:"case_variants"`
	HiddenBy      []model.ModelAliasDeclarer `json:"hidden_by"`
	HiddenCached  bool                       `json:"hidden_cached"`
	EndpointTypes []string                   `json:"endpoint_types"`
	PricingEntry  bool                       `json:"pricing_entry"`
	MediaConfigs  []string                   `json:"media_configs"`
	VisibleGroups string                     `json:"visible_groups"`
	MetaStatus    *int                       `json:"meta_status"`
	HasMeta       bool                       `json:"has_meta"`
}

// diagnoseChannelModel 把上面那个接口的判定逻辑独立出来，便于单测直接调用。
//
// 数据库错误必须**返回**而不是吞掉：吞掉的表现是"查询挂了"和"这个名字没有任何渠道"
// 给出同一句结论，排查方向会被带到完全错误的路上。
func diagnoseChannelModel(name string) (channelModelDiagnosis, error) {
	result := channelModelDiagnosis{
		Model:         name,
		Blockers:      make([]string, 0),
		Warnings:      make([]string, 0),
		Abilities:     make([]diagnoseAbility, 0),
		Groups:        make([]string, 0),
		CaseVariants:  make([]diagnoseVariant, 0),
		HiddenBy:      make([]model.ModelAliasDeclarer, 0),
		EndpointTypes: make([]string, 0),
		MediaConfigs:  make([]string, 0),
	}

	// ① abilities：这个模型名到底有没有落成可路由的记录
	var abilityRows []model.Ability
	if err := model.DB.Where("model = ?", name).Find(&abilityRows).Error; err != nil {
		return result, err
	}
	// MySQL 默认排序规则不区分大小写，`model = ?` 会把 Qwen3.8-Max 也带进来；
	// 两者在本站是两个独立模型，这里再按精确大小写过滤。
	abilityRows = filterAbilitiesByExactName(abilityRows, name)
	channelIds := make([]int, 0, len(abilityRows))
	seenChannel := make(map[int]bool, len(abilityRows))
	for _, a := range abilityRows {
		if !seenChannel[a.ChannelId] {
			seenChannel[a.ChannelId] = true
			channelIds = append(channelIds, a.ChannelId)
		}
	}
	channelById := make(map[int]*model.Channel, len(channelIds))
	if len(channelIds) > 0 {
		if channels, err := model.GetChannelsByIds(channelIds); err == nil {
			for _, ch := range channels {
				channelById[ch.Id] = ch
			}
		}
	}
	sort.Slice(channelIds, func(i, j int) bool { return channelIds[i] < channelIds[j] })
	enabledCount := 0
	groupSet := make(map[string]bool)
	disabledNames := make([]string, 0)
	for _, a := range abilityRows {
		ch := channelById[a.ChannelId]
		row := diagnoseAbility{
			ChannelId:     a.ChannelId,
			Group:         a.Group,
			Enabled:       a.Enabled,
			ChannelStatus: 0,
		}
		if ch != nil {
			row.ChannelName = ch.Name
			row.ChannelStatus = ch.Status
		}
		result.Abilities = append(result.Abilities, row)
		if a.Enabled {
			enabledCount++
			groupSet[a.Group] = true
		} else if ch != nil {
			disabledNames = append(disabledNames, fmt.Sprintf("#%d「%s」", ch.Id, ch.Name))
		}
	}
	sort.Slice(result.Abilities, func(i, j int) bool {
		if result.Abilities[i].ChannelId != result.Abilities[j].ChannelId {
			return result.Abilities[i].ChannelId < result.Abilities[j].ChannelId
		}
		return result.Abilities[i].Group < result.Abilities[j].Group
	})
	for g := range groupSet {
		result.Groups = append(result.Groups, g)
	}
	sort.Strings(result.Groups)

	// ② 内部名：别名右侧的名字既不会生成 ability，也会被所有列表过滤
	result.HiddenBy = append(result.HiddenBy, model.FindModelAliasDeclarers(name)...)
	result.HiddenCached = model.IsHiddenModelName(name)
	if len(result.HiddenBy) > 0 {
		owners := make([]string, 0, len(result.HiddenBy))
		for _, d := range result.HiddenBy {
			owners = append(owners, fmt.Sprintf("渠道 #%d「%s」（对外名 %s）", d.ChannelId, d.ChannelName, d.PublicName))
		}
		result.Blockers = append(result.Blockers,
			fmt.Sprintf("这个名字被 %s 的「模型别名」声明为内部名：既不会生成 ability，也不会出现在任何模型列表。请改用对外名，或把上游真名配进「模型重定向」", strings.Join(owners, "、")))
	} else if result.HiddenCached {
		result.Warnings = append(result.Warnings,
			"别名的内部名缓存里仍认为这是内部名（声明已从库里消失，缓存按周期重建）：最多一个同步周期后自动恢复")
	}

	// ③ 定价条目：模型广场与前端"非文本端点"过滤都以它为准
	var pricingTypes []constant.EndpointType
	for _, p := range model.GetPricing() {
		if p.ModelName == name {
			result.PricingEntry = true
			pricingTypes = p.SupportedEndpointTypes
			break
		}
	}
	if len(pricingTypes) == 0 {
		pricingTypes = model.GetModelSupportEndpointTypes(name)
	}
	nonChatLabels := make([]string, 0)
	for _, et := range pricingTypes {
		result.EndpointTypes = append(result.EndpointTypes, string(et))
		if label, ok := constant.PlaygroundNonChatEndpointLabels[et]; ok {
			nonChatLabels = append(nonChatLabels, fmt.Sprintf("%s（%s）", label, et))
		}
	}
	if len(nonChatLabels) > 0 {
		result.Blockers = append(result.Blockers,
			fmt.Sprintf("端点类型含 %s：文本体验区会整体排除它（渠道类型或模型元数据里的自定义 endpoints 决定的）", strings.Join(nonChatLabels, "、")))
	}
	if !result.PricingEntry && enabledCount > 0 {
		result.Warnings = append(result.Warnings,
			"不在 /api/pricing 里（模型广场看不到它）：通常是模型元数据 status≠1，或渠道刚改动、定价缓存还没刷新（最长 60 秒）")
	}

	// ④ 体验区媒体配置占用
	result.MediaConfigs = append(result.MediaConfigs, mediaConfigsDeclaringModel(name)...)
	if len(result.MediaConfigs) > 0 {
		result.Blockers = append(result.Blockers,
			fmt.Sprintf("已配进 %s 的 models{}：文本体验区会排除它（那是图片/音视频玩法的配置）", strings.Join(result.MediaConfigs, "、")))
	}

	// ⑤ 模型元数据：status 与可见档位
	if meta := model.GetResolvedModelMeta(name); meta != nil {
		result.HasMeta = true
		status := meta.Status
		result.MetaStatus = &status
		result.VisibleGroups = meta.VisibleGroups
		if status != 1 {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("模型元数据 status=%d：模型广场不展示；体验区仍可能可选，但它所在的分组可能被分组下拉隐藏", status))
		}
		if strings.TrimSpace(meta.VisibleGroups) != "" {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("可见性限制 visible_groups=%s：不在此列表的用户档看不到、也调不通", meta.VisibleGroups))
		}
	} else {
		result.Warnings = append(result.Warnings,
			"models 表里没有元数据行：模型广场没有图标/描述/供应商（不影响调用与计费）")
	}

	// ⑥ 仅大小写不同的变体：最常见的是"两个看着一样的模型"，各自有独立的 ability 与倍率
	var sameNameRows []model.Ability
	if err := model.DB.Where("LOWER(model) = ?", strings.ToLower(name)).Find(&sameNameRows).Error; err != nil {
		return result, err
	}
	variantIndex := make(map[string]*diagnoseVariant)
	variantOrder := make([]string, 0)
	for _, a := range sameNameRows {
		v, ok := variantIndex[a.Model]
		if !ok {
			v = &diagnoseVariant{Model: a.Model, ChannelIds: make([]int, 0, 2)}
			variantIndex[a.Model] = v
			variantOrder = append(variantOrder, a.Model)
		}
		v.Enabled = v.Enabled || a.Enabled
		dup := false
		for _, id := range v.ChannelIds {
			if id == a.ChannelId {
				dup = true
				break
			}
		}
		if !dup {
			v.ChannelIds = append(v.ChannelIds, a.ChannelId)
		}
	}
	sort.Strings(variantOrder)
	for _, name := range variantOrder {
		result.CaseVariants = append(result.CaseVariants, *variantIndex[name])
	}
	if len(result.CaseVariants) > 1 {
		spellings := make([]string, 0, len(result.CaseVariants))
		for _, v := range result.CaseVariants {
			spellings = append(spellings, v.Model)
		}
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("站点内存在仅大小写不同的多个名字 %s：它们是彼此独立的模型（各自计价、各占一个下拉项），建议收敛成一个", strings.Join(spellings, " / ")))
	}

	// ⑦ 结论：按文本体验区的口径回答"能不能看到"
	if len(abilityRows) == 0 {
		result.Blockers = append(result.Blockers,
			"没有任何渠道的「模型」列表包含这个名字：ability 未生成，/api/user/models 与体验区都不会返回它")
	} else if enabledCount == 0 {
		result.Blockers = append(result.Blockers,
			fmt.Sprintf("有 %d 条 ability，但对应渠道全部禁用（%s）：abilities.enabled 跟着渠道状态走", len(abilityRows), strings.Join(disabledNames, "、")))
	}
	result.Visible = len(result.Blockers) == 0
	return result, nil
}

// filterAbilitiesByExactName 在内存里按精确大小写再筛一遍（见上面 MySQL 排序规则的说明）。
func filterAbilitiesByExactName(rows []model.Ability, name string) []model.Ability {
	if len(rows) == 0 {
		return rows
	}
	out := rows[:0]
	for _, row := range rows {
		if row.Model == name {
			out = append(out, row)
		}
	}
	return out
}

// mediaConfigsDeclaringModel 返回把该模型配进 models{} 的体验区配置键。
//
// 逐个 key 解析而不是整串 LIKE：配置里模型名可能出现在 capabilities/tabs 等任意位置，
// LIKE 会把"只是在注释或尺寸列表里提到过"也算命中，给出错误的阻断结论。
func mediaConfigsDeclaringModel(name string) []string {
	common.OptionMapRWMutex.RLock()
	raws := make(map[string]string, len(playgroundMediaConfigKeys))
	for _, key := range playgroundMediaConfigKeys {
		raws[key] = common.OptionMap[key]
	}
	common.OptionMapRWMutex.RUnlock()

	declared := make([]string, 0, len(playgroundMediaConfigKeys))
	for _, key := range playgroundMediaConfigKeys {
		raw := strings.TrimSpace(raws[key])
		if raw == "" {
			continue
		}
		var cfg struct {
			Models map[string]json.RawMessage `json:"models"`
		}
		if err := common.UnmarshalJsonStr(raw, &cfg); err != nil {
			continue
		}
		if _, ok := cfg.Models[name]; ok {
			declared = append(declared, key)
		}
	}
	return declared
}
