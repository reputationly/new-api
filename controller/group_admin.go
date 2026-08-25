package controller

import (
	"net/http"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

// 分组管理页的只读支撑接口，设计见 docs/group-management-redesign.md §7.0。
//
// 分组本身仍然存在 option JSON 里，由前端走 PUT /api/option/ 整体保存——这里不
// 承担写入。这几个接口回答的是配置文件里看不出来的东西：这个分组通不通、删了会
// 影响谁、它下面到底有哪些模型可以配折扣。

// pseudoGroupAuto 是「自动分组」这个伪分组名，在 middleware/auth.go、
// middleware/distributor.go、controller/token.go 等处硬编码。它不对应任何渠道，
// 运行时会被替换成 auto 池里的某个真实分组，所以拿真实分组的标准去判它的健康度
// 必然误报——不特判的话页面会永远挂着一个红灯，而那正是它应有的样子。
const pseudoGroupAuto = "auto"

// GroupHealth 分组的健康判定结果。
type GroupHealth struct {
	Name         string  `json:"name"`
	Ratio        float64 `json:"ratio"`
	Selectable   bool    `json:"selectable"`
	Description  string  `json:"description"`
	ChannelCount int     `json:"channel_count"`
	ModelCount   int     `json:"model_count"`
	RuleCount    int     `json:"rule_count"`

	// Status: ok | no_channel | unreachable | virtual
	Status string `json:"status"`
	// StaleRules 配了折扣、但该模型在本分组没有渠道覆盖的规则模式串
	StaleRules []string `json:"stale_rules"`
}

// GetGroupOverview 汇总分组管理页首屏需要的一切。
func GetGroupOverview(c *gin.Context) {
	coverage, err := model.GetGroupCoverage()
	if err != nil {
		common.ApiError(c, err)
		return
	}

	ratios := ratio_setting.GetGroupRatioCopy()
	usable := setting.GetUserUsableGroupsCopy()
	rules := ratio_setting.GetGroupModelRatioCopy()
	autoGroups := setting.GetAutoGroups()

	// 有用户属于该分组时，即便没勾「用户可选」它也是可达的——管理员分配即生效。
	// 不查这个的话，按用户等级定价的分组会被误判成死配置。
	groupsWithUsers, err := model.GetGroupsWithUsers()
	if err != nil {
		common.ApiError(c, err)
		return
	}

	// 分组特殊可用规则也能让一个没勾「用户可选」的分组变得可达
	reachableBySpecialRule := make(map[string]bool)
	for _, rule := range ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.ReadAll() {
		for target := range rule {
			if strings.HasPrefix(target, "-:") {
				continue
			}
			reachableBySpecialRule[strings.TrimPrefix(target, "+:")] = true
		}
	}

	names := make([]string, 0, len(ratios))
	for name := range ratios {
		names = append(names, name)
	}
	sort.Strings(names)

	list := make([]GroupHealth, 0, len(names))
	for _, name := range names {
		cov := coverage[name]
		_, selectable := usable[name]

		item := GroupHealth{
			Name:         name,
			Ratio:        ratios[name],
			Selectable:   selectable,
			Description:  setting.GetGroupDescription(name),
			ChannelCount: cov.ChannelCount,
			ModelCount:   cov.ModelCount,
			RuleCount:    len(rules[name]),
			StaleRules:   staleRulePatterns(name, rules[name]),
		}

		reachable := selectable ||
			groupsWithUsers[name] ||
			reachableBySpecialRule[name] ||
			common.StringsContains(autoGroups, name)

		item.Status = groupStatus(name, cov.ChannelCount, reachable)
		list = append(list, item)
	}

	// 被渠道挂载、但分组配置里没有的名字：那些渠道当前完全不可用
	// （middleware/auth.go 判「分组已被弃用」），而现在没有任何地方能发现
	usedByChannels, err := model.GetGroupsUsedByChannels()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	unconfigured := make([]gin.H, 0)
	for _, name := range usedByChannels {
		if _, ok := ratios[name]; ok {
			continue
		}
		if name == pseudoGroupAuto {
			// 渠道理论上不该挂 auto；真挂了也不是「缺配置」，补建一个 auto 分组只会更乱
			continue
		}
		unconfigured = append(unconfigured, gin.H{
			"name":          name,
			"channel_count": coverage[name].ChannelCount,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"groups":       list,
			"unconfigured": unconfigured,
		},
	})
}

// groupStatus 判定一个分组的健康度。
//
// 抽成独立函数是为了能被测试直接调用——内联在 GetGroupOverview 里的话，
// 要覆盖这四条分支就得起 gin context、造 DB、铺三份配置，代价高到没人会写，
// 于是只能写一份逻辑的副本来「测」，而副本改不动真实现，等于没测。
func groupStatus(name string, channelCount int, reachable bool) string {
	switch {
	case name == pseudoGroupAuto:
		// 伪分组：不对应任何渠道，没渠道正是它应有的样子
		return "virtual"
	case channelCount == 0:
		// 用户一旦选中必然报「无可用渠道」，这是最硬的失配
		return "no_channel"
	case !reachable:
		// 有渠道，但没有任何路径能让用户用上它
		return "unreachable"
	default:
		return "ok"
	}
}

// staleRulePatterns 找出配了折扣、但在本分组匹配不到任何模型的规则。
//
// 这类规则不会算错钱（匹配不到就不生效），但它是一条无声失效的运营配置——
// 管理员以为打了折，实际什么都没发生。
func staleRulePatterns(group string, rules map[string]ratio_setting.ModelRatioRule) []string {
	if len(rules) == 0 {
		return []string{}
	}
	models, err := model.GetGroupModels(group)
	if err != nil {
		return []string{}
	}
	stale := make([]string, 0)
	for pattern := range rules {
		matched := false
		for _, m := range models {
			if ratio_setting.MatchModelPattern(pattern, m) {
				matched = true
				break
			}
		}
		if !matched {
			stale = append(stale, pattern)
		}
	}
	sort.Strings(stale)
	return stale
}

// GetGroupModels 返回某分组下实际有渠道覆盖的模型名，供折扣编辑器的模型下拉使用。
//
// 刻意不返回全站模型：给一个本分组根本没有的模型配折扣是纯废配置，
// 不该让人先配出来、再靠 stale_rules 去发现。
func GetGroupModels(c *gin.Context) {
	group := strings.TrimSpace(c.Query("group"))
	if group == "" {
		common.ApiErrorMsg(c, "缺少 group 参数")
		return
	}
	models, err := model.GetGroupModels(group)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    models,
	})
}

// GetGroupReferences 统计分组被 user / token / channel / subscription_plan 引用的次数。
//
// 删除分组前调它。现在从 JSON 里抹掉一行没有任何检查，用户会撞上
// 「分组已被弃用」而管理员毫不知情。
func GetGroupReferences(c *gin.Context) {
	group := strings.TrimSpace(c.Query("group"))
	if group == "" {
		common.ApiErrorMsg(c, "缺少 group 参数")
		return
	}
	refs, err := model.GetGroupReferences(group)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"users":    refs.Users,
			"tokens":   refs.Tokens,
			"channels": refs.Channels,
			"plans":    refs.Plans,
			"total":    refs.Total(),
		},
	})
}

// ResolveGroupRatioPreview 倍率试算：给定（用户分组，令牌分组，模型），返回完整解析链。
//
// 两层叠加加通配的可解释性必须有工具兜底，否则运营改完价不敢上线——尤其配过
// override 之后，它会吃掉身份折扣（设计 §3.3），只有把每一层摊开才看得见。
func ResolveGroupRatioPreview(c *gin.Context) {
	var req struct {
		UserGroup  string `json:"user_group"`
		UsingGroup string `json:"using_group"`
		ModelName  string `json:"model_name"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if strings.TrimSpace(req.UsingGroup) == "" {
		common.ApiErrorMsg(c, "缺少令牌分组")
		return
	}

	res := ratio_setting.ResolveGroupRatio(req.UserGroup, req.UsingGroup, req.ModelName)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"final":             res.Final,
			"group_ratio":       res.GroupRatio,
			"base":              res.Base,
			"has_special_ratio": res.HasSpecialRatio,
			"special_ratio":     res.SpecialRatio,
			"rule_match":        res.RuleMatch,
			"rule_mode":         res.RuleMode,
			"rule_value":        res.RuleValue,
			// 该组合下用户实际能否用到这个分组，试算结果才有意义
			"usable": service.GroupInUserUsableGroups(req.UserGroup, req.UsingGroup),
		},
	})
}
