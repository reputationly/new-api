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
		q := int64(common.PointsToQuota(ps.KycVerifiedPoints))
		now := common.GetTimestamp()
		// 赠送流水（CashFen=0，不计营收）。幂等键用被实名用户的 id：
		// 一个用户的实名事件最多触发一次奖励，与 TryMarkKycPointsGranted 的占位同源。
		// 余额、批次、流水三者同成同败（GrantPoints 内部一个事务），失败仍只记日志。
		// 幂等键可能被「上一个用过同一个 id 的、已被硬删的用户」占着（id 复用）。
		// 撞键时退化成带发放时刻的键——否则这笔实名奖励会静默发不出去（占位已置位，
		// 重试也不会再发），用户永久失去它。
		refId := model.PointsUserRefId("kyc", userId, now, nil)
		if _, err := model.GrantPoints(model.PointsGrant{
			UserId:    userId,
			Quota:     q,
			Source:    model.FundSourceKyc,
			RefId:     refId,
			ExpiresAt: model.ExpireAtDays(now, ps.KycVerifiedExpireDays),
			Now:       now,
			Fund: model.FundEntry{
				Kind: model.FundKindGift, Source: model.FundSourceKyc,
				RefType: model.FundRefUser, RefId: refId,
			},
		}); err != nil {
			common.SysLog("GrantKycPoints: grant self failed: " + err.Error())
		} else {
			model.RecordLog(userId, model.LogTypeSystem,
				fmt.Sprintf("实名认证赠送 %d 积分", ps.KycVerifiedPoints))
		}
	}

	// 邀请人奖励：邀请人本人也须已实名，否则未实名老号靠拉人头薅积分
	if user.InviterId != 0 && ps.KycInviterPoints > 0 && model.IsUserPointsEligible(user.InviterId) {
		q := int64(common.PointsToQuota(ps.KycInviterPoints))
		now := common.GetTimestamp()
		// 邀请奖励流水记在**邀请人**账上（钱进的是他的池子），
		// 幂等键仍用被邀请人 id —— 触发这笔奖励的是后者的实名事件，只会发生一次。
		refId := model.PointsUserRefId("invite", userId, now, nil)
		if _, err := model.GrantPoints(model.PointsGrant{
			UserId:    user.InviterId,
			Quota:     q,
			Source:    model.FundSourceInvite,
			RefId:     refId,
			ExpiresAt: model.ExpireAtDays(now, ps.KycInviterExpireDays),
			Now:       now,
			Fund: model.FundEntry{
				Kind: model.FundKindGift, Source: model.FundSourceInvite,
				RefType: model.FundRefUser, RefId: refId,
				Remark: fmt.Sprintf("邀请用户 %d 实名", userId),
			},
		}); err != nil {
			common.SysLog("GrantKycPoints: grant inviter failed: " + err.Error())
		} else {
			// 累加邀请人「累计邀请积分」统计（展示用），与积分发放绑定同一次占位，不重复。
			if err := model.AddUserAffPointsEarned(user.InviterId, int(q)); err != nil {
				common.SysLog("GrantKycPoints: add aff points earned failed: " + err.Error())
			}
			model.RecordLog(user.InviterId, model.LogTypeSystem,
				fmt.Sprintf("邀请用户(ID:%d)完成实名认证赠送 %d 积分", userId, ps.KycInviterPoints))
		}
	}
}
