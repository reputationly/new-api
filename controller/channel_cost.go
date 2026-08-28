package controller

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// 渠道成本管理接口。设计见
// docs/user-tier-pricing-and-topup-package-design.md §5.1 与 §5.5。

type channelCostItem struct {
	ModelName string  `json:"model_name"`
	CostRatio float64 `json:"cost_ratio"`
	Remark    string  `json:"remark"`
}

type channelCostSaveRequest struct {
	Costs []channelCostItem `json:"costs"`
}

// GetChannelCosts GET /api/channel/:id/cost
//
// 同时回该渠道**实际挂载的模型**清单，让编辑页能标出「挂了但没配成本」的模型——
// 成本必填（§5.5）不是靠保存时拦，而是靠这个差集在页面上一直可见：成本参与选路后，
// 未配的渠道会静默排到末位，没有报错也没有日志，只有流量为零。
func GetChannelCosts(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorMsg(c, "无效的渠道 id")
		return
	}

	channel, err := model.GetChannelById(id, false)
	if err != nil {
		common.ApiErrorMsg(c, "渠道不存在")
		return
	}

	costs, err := model.GetChannelModelCosts(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	mounted := make([]string, 0)
	for _, m := range strings.Split(channel.Models, ",") {
		if m = strings.TrimSpace(m); m != "" {
			mounted = append(mounted, m)
		}
	}

	configured := make(map[string]bool, len(costs))
	for _, cost := range costs {
		configured[cost.ModelName] = true
	}
	missing := make([]string, 0)
	for _, m := range mounted {
		if !configured[m] {
			missing = append(missing, m)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"costs":          costs,
			"mounted_models": mounted,
			// 挂载了但未配成本的模型，供页面标红
			"missing_models": missing,
		},
	})
}

// SaveChannelCosts PUT /api/channel/:id/cost
//
// 整体替换该渠道的成本配置（`ReplaceChannelModelCosts` 的语义）：编辑页提交的是
// 一份完整报价单，页面上删掉的行就该真的删掉。
func SaveChannelCosts(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorMsg(c, "无效的渠道 id")
		return
	}
	if _, err := model.GetChannelById(id, false); err != nil {
		common.ApiErrorMsg(c, "渠道不存在")
		return
	}

	var req channelCostSaveRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}

	seen := make(map[string]bool, len(req.Costs))
	rows := make([]*model.ChannelModelCost, 0, len(req.Costs))
	for _, item := range req.Costs {
		name := strings.TrimSpace(item.ModelName)
		if name == "" {
			common.ApiErrorMsg(c, "模型名不能为空")
			return
		}
		// 成本表只认精确模型名（§5.1）：成本是从供应商账单抄来的确定数字，
		// 通配会让「未列出的模型成本是多少」变成需要推断的问题
		if strings.Contains(name, "*") {
			common.ApiErrorMsg(c, fmt.Sprintf("模型名 %s 不支持通配符，成本需逐个模型填写", name))
			return
		}
		if seen[name] {
			common.ApiErrorMsg(c, fmt.Sprintf("模型 %s 重复", name))
			return
		}
		// 负成本没有任何业务含义，且会让选路排序把它排到最前，静默抢走全部流量
		if item.CostRatio < 0 {
			common.ApiErrorMsg(c, fmt.Sprintf("模型 %s 的成本比不能为负", name))
			return
		}
		seen[name] = true
		rows = append(rows, &model.ChannelModelCost{
			ModelName: name,
			CostRatio: item.CostRatio,
			Remark:    item.Remark,
		})
	}

	if err := model.ReplaceChannelModelCosts(id, rows); err != nil {
		common.ApiError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
}

// GetChannelsMissingCost GET /api/channel/missing_cost
//
// 路径不用 /cost/missing：那会让静态段 "cost" 与同层的 :id 参数段冲突，
// 部分 Gin 版本在注册时直接 panic，属于启动才暴露的那类问题。
//
// 返回「有启用模型、但成本没配全」的渠道，供渠道列表页打红标。
//
// 这个接口存在的理由与 §5.5 是同一条：成本参与选路后，「没配」的后果从记账不准
// 升级成路由行为异常，而它没有任何运行时症状——不报错、不进日志、只是流量为零。
// 唯一能发现它的位置就是管理页上一直亮着的那个角标。
func GetChannelsMissingCost(c *gin.Context) {
	channels, err := model.GetAllChannels(0, 0, true, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	result := make([]gin.H, 0)
	for _, ch := range channels {
		if ch.Status != common.ChannelStatusEnabled {
			continue
		}
		missing := 0
		total := 0
		for _, m := range strings.Split(ch.Models, ",") {
			if m = strings.TrimSpace(m); m == "" {
				continue
			}
			total++
			if _, ok := model.GetChannelModelCostRatio(ch.Id, m); !ok {
				missing++
			}
		}
		if missing > 0 {
			result = append(result, gin.H{
				"id":            ch.Id,
				"name":          ch.Name,
				"missing_count": missing,
				"model_count":   total,
			})
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
	})
}
