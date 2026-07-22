package service

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// GrantKycPoints 实名认证通过后发放积分（本人 + 邀请人）。
// 一次占位、两笔发放：用 User.KycPointsGranted 原子占位保证「一个用户的实名事件最多
// 触发一次（本人+邀请人）奖励」，防 KYC reset 后重复领取（§8.2）。发放失败仅记日志，
// 不影响审核主流程。
func GrantKycPoints(userId int) {
	ps := operation_setting.GetPointsSetting()
	if !ps.Enabled {
		return
	}

	// 原子占位闸门（本人+邀请人共用同一事件）
	ok, err := model.TryMarkKycPointsGranted(userId)
	if err != nil {
		common.SysLog("GrantKycPoints: mark granted failed: " + err.Error())
		return
	}
	if !ok {
		return // 已发放过
	}

	user, err := model.GetUserById(userId, false)
	if err != nil {
		common.SysLog("GrantKycPoints: get user failed: " + err.Error())
		return
	}

	// 本人奖励（本人天然已实名，无需再判资格）
	if ps.KycVerifiedPoints > 0 {
		q := common.PointsToQuota(ps.KycVerifiedPoints)
		if err := model.IncreaseUserPoints(userId, q, true); err != nil {
			common.SysLog("GrantKycPoints: grant self failed: " + err.Error())
		} else {
			model.RecordLog(userId, model.LogTypeSystem,
				fmt.Sprintf("实名认证赠送 %d 积分", ps.KycVerifiedPoints))
		}
	}

	// 邀请人奖励：邀请人本人也须已实名，否则未实名老号靠拉人头薅积分
	if user.InviterId != 0 && ps.KycInviterPoints > 0 && model.IsUserPointsEligible(user.InviterId) {
		q := common.PointsToQuota(ps.KycInviterPoints)
		if err := model.IncreaseUserPoints(user.InviterId, q, true); err != nil {
			common.SysLog("GrantKycPoints: grant inviter failed: " + err.Error())
		} else {
			// 累加邀请人「累计邀请积分」统计（展示用），与积分发放绑定同一次占位，不重复。
			if err := model.AddUserAffPointsEarned(user.InviterId, q); err != nil {
				common.SysLog("GrantKycPoints: add aff points earned failed: " + err.Error())
			}
			model.RecordLog(user.InviterId, model.LogTypeSystem,
				fmt.Sprintf("邀请用户(ID:%d)完成实名认证赠送 %d 积分", userId, ps.KycInviterPoints))
		}
	}
}
