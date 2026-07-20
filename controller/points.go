package controller

import (
	"net/http"

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
