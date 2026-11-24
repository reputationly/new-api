package controller

import (
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

// pointsTask 积分获取任务项，供 §8ter 引导卡片渲染。
// status: "actionable"=可参与 / "locked"=未实名锁定（需先实名）。
// 已完成的项（本人已实名、当天已签到）不下发，由前端「完成即隐藏」。
type pointsTask struct {
	Type      string `json:"type"`                 // kyc / checkin / invite / redemption
	Points    int    `json:"points"`               // 可得积分数（0=不定，如兑换码面值随码）
	PointsMax int    `json:"points_max,omitempty"` // 区间上限（签到随机奖励）
	Status    string `json:"status"`               // actionable / locked
}

// GetPointsOverview 聚合积分余额 + 获取任务清单（§8ter）。
// 前端引导卡片直接渲染，避免分散组合多个接口。
func GetPointsOverview(c *gin.Context) {
	ps := operation_setting.GetPointsSetting()
	if !ps.Enabled {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data":    gin.H{"enabled": false, "tasks": []pointsTask{}},
		})
		return
	}

	userId := c.GetInt("id")
	cache, err := model.GetUserCache(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	// 企业子账号不参与积分（积分是主账号资产：消费走主账号令牌计费，获取渠道全被
	// SubAccountForbidden 封死）——引导卡片整体隐藏，避免展示永远为 0 的余额与 403 的 CTA
	if cache.ParentUserId > 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data":    gin.H{"enabled": false, "tasks": []pointsTask{}},
		})
		return
	}
	kycVerified := cache.KycStatus == model.KYCStatusApproved ||
		cache.EnterpriseStatus == model.EnterpriseStatusApproved

	tasks := make([]pointsTask, 0, 4)

	// 实名认证（本人）：一次性，通过后永久隐藏。
	if ps.KycVerifiedPoints > 0 && !kycVerified {
		tasks = append(tasks, pointsTask{Type: "kyc", Points: ps.KycVerifiedPoints, Status: "actionable"})
	}

	// 每日签到：仅积分模式；当天已签则隐藏、次日重现；未实名（要求实名时）锁定。
	cs := operation_setting.GetCheckinSetting()
	if cs.Enabled && cs.RewardType == "points" && cs.MaxPoints > 0 {
		checkedToday, _ := model.HasCheckedInToday(userId)
		if !checkedToday {
			status := "actionable"
			if ps.RequireKyc && !kycVerified {
				status = "locked"
			}
			tasks = append(tasks, pointsTask{Type: "checkin", Points: cs.MinPoints, PointsMax: cs.MaxPoints, Status: status})
		}
	}

	// 邀请好友实名：常驻，无完成态。
	if ps.KycInviterPoints > 0 {
		tasks = append(tasks, pointsTask{Type: "invite", Points: ps.KycInviterPoints, Status: "actionable"})
	}

	// 兑换积分码：常驻，面值随码不定。
	tasks = append(tasks, pointsTask{Type: "redemption", Points: 0, Status: "actionable"})

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"enabled":      true,
			"require_kyc":  ps.RequireKyc,
			"kyc_verified": kycVerified,
			"balance":      common.QuotaToPoints(cache.PointsBalance),
			"tasks":        tasks,
		},
	})
}

// pointsLotsPageSize 批次明细的分页上限。批次行数本来就不多（签到按周合并），
// 限制上限是为了挡住恶意的超大 page_size。
const pointsLotsPageSizeMax = 50

func parsePointsLotsPage(c *gin.Context) (page, pageSize, offset int) {
	page, _ = strconv.Atoi(c.DefaultQuery("p", "1"))
	if page < 1 {
		page = 1
	}
	pageSize, _ = strconv.Atoi(c.DefaultQuery("page_size", "10"))
	if pageSize < 1 {
		pageSize = 10
	}
	if pageSize > pointsLotsPageSizeMax {
		pageSize = pointsLotsPageSizeMax
	}
	return page, pageSize, (page - 1) * pageSize
}

// pointsLotsForbidden 子账号与关闭积分系统时统一的空响应。
//
// 企业子账号不参与积分（积分是主账号资产，消费走主账号令牌计费），展示永远为 0 的
// 余额与批次只会误导；积分系统关闭时批次不参与任何决策，也没有展示意义。
func pointsLotsForbidden(c *gin.Context) bool {
	if !operation_setting.GetPointsSetting().Enabled {
		return true
	}
	cache, err := model.GetUserCache(c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return true
	}
	return cache.ParentUserId > 0
}

// GetPointsLotsSummary GET /api/user/points/lots/summary
// 返回 30 天内到期合计与最近 3 个到期日，供充值页临期标签使用。
func GetPointsLotsSummary(c *gin.Context) {
	empty := model.PointsExpirySummary{NextExpiries: []model.PointsExpiring{}}
	if pointsLotsForbidden(c) {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": empty})
		return
	}
	summary, err := model.GetPointsExpirySummary(c.GetInt("id"), common.GetTimestamp(), 30*86400, 3)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": summary})
}

// GetPointsLots GET /api/user/points/lots?status=active|history&p=1&page_size=10
//
// active：剩余 > 0 的批次（只读模拟分摊之后的视图），按到期升序、永不过期最后；
// history：已过期 / 已用完，按 updated_at 降序。
func GetPointsLots(c *gin.Context) {
	_, pageSize, offset := parsePointsLotsPage(c)
	status := c.DefaultQuery("status", "active")
	if status != "active" && status != "history" {
		common.ApiErrorMsg(c, "status 只能是 active 或 history")
		return
	}
	userId := c.GetInt("id")
	if pointsLotsForbidden(c) {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"total": 0, "items": []model.PointsLotView{}}})
		return
	}

	if status == "history" {
		items, total, err := model.GetPointsLotHistory(userId, offset, pageSize)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"total": total, "items": items}})
		return
	}

	all, _, err := model.GetActivePointsLotViews(userId, common.GetTimestamp())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	total := len(all)
	items := []model.PointsLotView{}
	if offset < total {
		end := offset + pageSize
		if end > total {
			end = total
		}
		items = all[offset:end]
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"total": total, "items": items}})
}

// GetPointsSurplusLots GET /api/user/points/lots/surplus?p=&page_size=（管理端）
//
// 余额溢出批次核查：正常运营下应恒为空。一旦出现，优先排查是否有发放路径绕过
// GrantPoints（新增发放点、或整行回写复发）；确认异常后可用现有「减少积分」扣回。
func GetPointsSurplusLots(c *gin.Context) {
	_, pageSize, offset := parsePointsLotsPage(c)
	items, total, err := model.GetSurplusPointsLots(offset, pageSize)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"total": total, "items": items}})
}
