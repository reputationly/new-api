package model

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/bytedance/gopkg/util/gopool"
	"gorm.io/gorm"
)

// IsUserPointsEligible 判断用户是否有资格参加积分（发放 + 使用双卡的统一判定）。
// RequireKyc 关闭时恒 true；开启时要求「个人 KYC 通过 或 企业认证通过」（二者其一），
// 与 middleware/kyc.go 的强制实名判定口径一致。查不到用户时保守拒绝。
// 放在 model 层以便发放（checkin/kyc_points）与扣费（billing_session）两侧共用。
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

// 积分账户原子增减，镜像 user.go 的 quota 操作（Redis Hash 异步缓存 + 可选批量更新）。
// 积分内部以 quota unit 记账，与 User.Quota 同单位；混扣扣减用 TryDecreaseUserPoints
// 条件更新以防透支（§6.4），积分永不为负。

// IncreaseUserPoints 增加积分（发放/退款）。
// ⚠️ 批量模式陷阱：db=false 且 BatchUpdateEnabled 时 DB 写入进队列延迟落库，而
// TryDecreaseUserPoints 条件扣减直击 DB——窗口内 Redis 超前、DB 滞后，会误判积分
// 不足（混扣积分优先失效甚至误拒请求）。凡与混扣扣减同账户交织的回补
// （funding_hybrid 退款/回滚）必须传 db=true 直写；纯发放路径本就直写。
func IncreaseUserPoints(id int, points int, db bool) (err error) {
	if points < 0 {
		return errors.New("points 不能为负数！")
	}
	if points == 0 {
		return nil
	}
	gopool.Go(func() {
		if err := cacheIncrUserPoints(id, int64(points)); err != nil {
			common.SysLog("failed to increase user points: " + err.Error())
		}
	})
	if !db && common.BatchUpdateEnabled {
		addNewRecord(BatchUpdateTypeUserPoints, id, points)
		return nil
	}
	return increaseUserPoints(id, points)
}

func increaseUserPoints(id int, points int) (err error) {
	return DB.Model(&User{}).Where("id = ?", id).Update("points_balance", gorm.Expr("points_balance + ?", points)).Error
}

// DecreaseUserPoints 减少积分并钳到 0（积分永不为负）。用于管理员 subtract 等低频场景；
// 混扣扣减请用 TryDecreaseUserPoints。低频操作直查 DB 权威余额并失效缓存，避免与
// HIncrBy 增量写竞态。db 参数保留以对齐签名，低频不入批量队列。
func DecreaseUserPoints(id int, points int, db bool) (err error) {
	if points < 0 {
		return errors.New("points 不能为负数！")
	}
	if points == 0 {
		return nil
	}
	var current int
	if err = DB.Model(&User{}).Where("id = ?", id).Select("points_balance").Find(&current).Error; err != nil {
		return err
	}
	dec := points
	if dec > current {
		dec = current // 钳到 0
	}
	if dec <= 0 {
		return invalidateUserCache(id)
	}
	if err = decreaseUserPoints(id, dec); err != nil {
		return err
	}
	// 失效缓存下次回源，避免绝对值/增量写竞态
	return invalidateUserCache(id)
}

func decreaseUserPoints(id int, points int) (err error) {
	return DB.Model(&User{}).Where("id = ?", id).Update("points_balance", gorm.Expr("points_balance - ?", points)).Error
}

// TryDecreaseUserPoints 条件扣减：仅当余额充足才扣。原子条件更新
// （WHERE points_balance >= ?），RowsAffected==1 视为成功；扣不到则返回 ok=false，
// 由调用方降级（如混扣时把该部分转由钱包承担）。三库兼容、并发安全。
// 注意：只减 points_balance，不动 points_used —— points_used 由结算完成后按最终消费
// 一次性累加（AddUserPointsUsed），否则预扣后退款会使 points_used 虚高（§6.2）。
func TryDecreaseUserPoints(id int, points int) (ok bool, err error) {
	if points <= 0 {
		return true, nil
	}
	result := DB.Model(&User{}).
		Where("id = ? AND points_balance >= ?", id, points).
		Update("points_balance", gorm.Expr("points_balance - ?", points))
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected == 0 {
		return false, nil
	}
	gopool.Go(func() {
		if err := cacheDecrUserPoints(id, int64(points)); err != nil {
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
