package model

import (
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/bytedance/gopkg/util/gopool"
	"gorm.io/gorm"
)

// IsUserPointsEligible 判断用户是否有资格参加积分**活动**（发放侧的门）。
// RequireKyc 关闭时恒 true；开启时要求「个人 KYC 通过 或 企业认证通过」（二者其一），
// 与 middleware/kyc.go 的强制实名判定口径一致。查不到用户时保守拒绝。
//
// 只管「能不能赚」，不管「能不能花」：签到、邀请人赠分走这道门，而兑换码兑入、注册礼
// 发放、以及扣费侧的积分抵扣都刻意不走——进了账的积分一律可花，否则等于没收用户资产。
func IsUserPointsEligible(userId int) bool {
	if !operation_setting.GetPointsSetting().RequireKyc {
		return true
	}
	cache, err := GetUserCache(userId)
	if err != nil {
		return false
	}
	return cache.KycStatus == KYCStatusApproved || cache.EnterpriseStatus == EnterpriseStatusApproved
}

// NewUserPointsGrant 返回新用户注册应发放的积分数、对应 quota unit，以及命中的渠道备注
// （未命中渠道规则时为空串）。未配置则前两个为 0。
//
// 只受积分系统总开关约束，**不受 RequireKyc 约束**：注册礼是拉新钩子，注册那一刻就要
// 落进账户、在使用日志里看得见、并且能直接花，跟着实名门走就没有钩子可言。规模由积分
// 数额与注册门槛（邮箱验证/Turnstile）控制，损失面由白名单分组兜底。渠道奖励沿用同一
// 口径——它的宣传话术就是「注册立得」，加实名门会让对外承诺与实际到账时机对不上。
//
// inviterId 命中渠道奖励时**彻底覆盖** NewUserPoints，包括覆盖成 0（该渠道不送）。
// 想让某渠道回落到默认值，把那条规则删掉或停用，而不是填 0。
func NewUserPointsGrant(inviterId int) (points int, quota int, channel string) {
	ps := operation_setting.GetPointsSetting()
	if !ps.Enabled {
		return 0, 0, ""
	}
	p := ps.NewUserPoints
	if r, ok := operation_setting.GetChannelPointsReward(inviterId); ok {
		p = r.Points
		channel = r.Remark
		if channel == "" {
			channel = r.Username
		}
	}
	if p <= 0 {
		return 0, 0, ""
	}
	return p, common.PointsToQuota(p), channel
}

// newUserPointsLog 拼注册赠分的日志文案。命中渠道奖励时带上渠道名——渠道奖励额度
// 通常远高于默认值，不标注的话对账时只看见一个偏大的数字，查不出该记到谁的推广账上。
//
// 两个注册路径（Insert / FinalizeOAuthUserCreation）共用，避免文案分叉后
// 按关键字统计推广效果时漏掉其中一条。
func newUserPointsLog(points int, channel string) string {
	if channel == "" {
		return fmt.Sprintf("新用户注册赠送 %d 积分", points)
	}
	return fmt.Sprintf("新用户注册赠送 %d 积分（渠道：%s）", points, channel)
}

// 积分账户原子增减，镜像 user.go 的 quota 操作（Redis Hash 异步缓存 + 可选批量更新）。
// 积分内部以 quota unit 记账，与 User.Quota 同单位；混扣扣减用 TryDecreaseUserPoints
// 条件更新以防透支（§6.4），积分永不为负。
//
// ⚠️ 下列函数的数量参数一律是 **quota unit，不是积分数**，二者相差
// QuotaPerPoint ≈ 685 倍。传入前必须用 common.PointsToQuota 换算。
// 参数名原本叫 points，与「积分数」同名而语义不同，已造成过一次真实事故：
// GrantTopupPackageBonus 直传积分数，结果配置「赠送 500 积分」实发 500 quota unit，
// 用户账户页按 floor 展示就是 0 积分。故统一改名为 quotaUnits 以正视听。

// IncreaseUserPoints 增加积分（发放/退款）。
// ⚠️ 批量模式陷阱：db=false 且 BatchUpdateEnabled 时 DB 写入进队列延迟落库，而
// TryDecreaseUserPoints 条件扣减直击 DB——窗口内 Redis 超前、DB 滞后，会误判积分
// 不足（混扣积分优先失效甚至误拒请求）。凡与混扣扣减同账户交织的回补
// （funding_hybrid 退款/回滚）必须传 db=true 直写；纯发放路径本就直写。
func IncreaseUserPoints(id int, quotaUnits int, db bool) (err error) {
	if quotaUnits < 0 {
		return errors.New("points 不能为负数！")
	}
	if quotaUnits == 0 {
		return nil
	}
	gopool.Go(func() {
		if err := cacheIncrUserPoints(id, int64(quotaUnits)); err != nil {
			common.SysLog("failed to increase user points: " + err.Error())
		}
	})
	if !db && common.BatchUpdateEnabled {
		addNewRecord(BatchUpdateTypeUserPoints, id, quotaUnits)
		return nil
	}
	return increaseUserPoints(id, quotaUnits)
}

func increaseUserPoints(id int, points int) (err error) {
	return DB.Model(&User{}).Where("id = ?", id).Update("points_balance", gorm.Expr("points_balance + ?", points)).Error
}

// DecreaseUserPoints 减少积分并钳到 0（积分永不为负）。用于管理员 subtract 等低频场景；
// 混扣扣减请用 TryDecreaseUserPoints。低频操作直查 DB 权威余额并失效缓存，避免与
// HIncrBy 增量写竞态。db 参数保留以对齐签名，低频不入批量队列。
//
// 返回 applied 为**实际扣减量**：扣减请求超过余额时会被钳到余额，applied < quotaUnits。
// 调用方若要落流水，必须记 applied 而非请求值——流水金额与实际余额变化不一致，
// 每日自洽校验就会不平，而那个告警本来是用来发现「有代码绕过流水表」的。
func DecreaseUserPoints(id int, quotaUnits int, db bool) (applied int, err error) {
	if quotaUnits < 0 {
		return 0, errors.New("points 不能为负数！")
	}
	if quotaUnits == 0 {
		return 0, nil
	}
	var current int
	if err = DB.Model(&User{}).Where("id = ?", id).Select("points_balance").Find(&current).Error; err != nil {
		return 0, err
	}
	dec := quotaUnits
	if dec > current {
		dec = current // 钳到 0
	}
	if dec <= 0 {
		return 0, invalidateUserCache(id)
	}
	if err = decreaseUserPoints(id, dec); err != nil {
		return 0, err
	}
	// 失效缓存下次回源，避免绝对值/增量写竞态
	return dec, invalidateUserCache(id)
}

func decreaseUserPoints(id int, points int) (err error) {
	return DB.Model(&User{}).Where("id = ?", id).Update("points_balance", gorm.Expr("points_balance - ?", points)).Error
}

// TryDecreaseUserPoints 条件扣减：仅当余额充足才扣。原子条件更新
// （WHERE points_balance >= ?），RowsAffected==1 视为成功；扣不到则返回 ok=false，
// 由调用方降级（如混扣时把该部分转由钱包承担）。三库兼容、并发安全。
// 注意：只减 points_balance，不动 points_used —— points_used 由结算完成后按最终消费
// 一次性累加（AddUserPointsUsed），否则预扣后退款会使 points_used 虚高（§6.2）。
func TryDecreaseUserPoints(id int, quotaUnits int) (ok bool, err error) {
	if quotaUnits <= 0 {
		return true, nil
	}
	result := DB.Model(&User{}).
		Where("id = ? AND points_balance >= ?", id, quotaUnits).
		Update("points_balance", gorm.Expr("points_balance - ?", quotaUnits))
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected == 0 {
		return false, nil
	}
	gopool.Go(func() {
		if err := cacheDecrUserPoints(id, int64(quotaUnits)); err != nil {
			common.SysLog("failed to sync user points cache after TryDecrease: " + err.Error())
		}
	})
	return true, nil
}

// GetUserPoints 读取积分余额（Redis-first，回源 DB），镜像 GetUserQuota。
func GetUserPoints(id int, fromDB bool) (points int, err error) {
	defer func() {
		if shouldUpdateRedis(fromDB, err) {
			gopool.Go(func() {
				if err := updateUserPointsCache(id, points); err != nil {
					common.SysLog("failed to update user points cache: " + err.Error())
				}
			})
		}
	}()
	if !fromDB && common.RedisEnabled {
		p, cacheErr := getUserPointsCache(id)
		if cacheErr == nil {
			return p, nil
		}
		// 回源 DB
	}
	fromDB = true
	err = DB.Model(&User{}).Where("id = ?", id).Select("points_balance").Find(&points).Error
	if err != nil {
		return 0, err
	}
	return points, nil
}

// AddUserPointsUsed 结算完成后累加已用积分（quota unit），用于对账。
func AddUserPointsUsed(id int, points int) error {
	if points <= 0 {
		return nil
	}
	return DB.Model(&User{}).Where("id = ?", id).Update("points_used", gorm.Expr("points_used + ?", points)).Error
}

// AddUserAffPointsEarned 累加邀请人通过邀请累计获得的积分（quota unit），仅用于展示统计。
// 在被邀请人实名后给邀请人发放邀请积分时同步累加（GrantKycPoints），有 KycPointsGranted 占位保证不重复。
func AddUserAffPointsEarned(id int, points int) error {
	if points <= 0 {
		return nil
	}
	return DB.Model(&User{}).Where("id = ?", id).Update("aff_points_earned", gorm.Expr("aff_points_earned + ?", points)).Error
}

// TryMarkKycPointsGranted 原子占位闸门：仅当 kyc_points_granted 仍为 false 时置 true，
// 返回 true 表示首次（可发放）。用于防 KYC reset 后重新提交再获批导致重复发积分（§8.2）。
// 本人与邀请人两笔发放绑定在同一次占位成功之后。布尔值经 GORM 抽象，三库兼容。
func TryMarkKycPointsGranted(userId int) (bool, error) {
	result := DB.Model(&User{}).Where("id = ? AND kyc_points_granted = ?", userId, false).
		Update("kyc_points_granted", true)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}
