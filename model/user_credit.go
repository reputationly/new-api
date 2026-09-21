package model

import (
	"errors"

	"gorm.io/gorm"
)

// 信用账户（先用后付）的余额操作。与 Quota / PointsBalance 并列但性质不同：
// 消耗产生应收账款，回款时才确认收入。扣费侧的信用层见 service/（尚未实现）。
//
// 三个字段的关系恒等式，也是每日自洽校验的第三条：
//
//	Σ信用消耗 - Σ回款核销 == CreditUsed
//	可用信用 = CreditLimit - CreditUsed
//	CreditUsed >= CreditLimit 即欠款阻断

var (
	ErrCreditLimitBelowUsed = errors.New("授信上限不得低于已用未结")
	ErrCreditSettleExceeds  = errors.New("核销额度超过已用未结或已被并发核销")
)

// SetUserCreditLimit 设置授信上限（绝对值语义：运营填的是「这个客户的额度是多少」）。
//
// 条件更新保证不会把上限设到已用未结之下——那会让客户瞬间处于超限停服状态，
// 且应收口径错乱。调用方已做过一次检查，这里是并发下的第二道门：两个管理员同时
// 操作（一个调低上限、一个客户正在消费推高 credit_used）时靠 WHERE 兜住。
func SetUserCreditLimit(id int, limit int64) error {
	if limit < 0 {
		return errors.New("授信上限不能为负")
	}
	result := DB.Model(&User{}).
		Where("id = ? AND credit_used <= ?", id, limit).
		Update("credit_limit", limit)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrCreditLimitBelowUsed
	}
	return invalidateUserCache(id)
}

// SettleUserCredit 回款核销：已用未结减少、累计已核销增加，额度随之恢复。
//
// WHERE credit_used >= ? 保证 credit_used 永不为负：核销额度超过欠款就是记错账，
// 宁可拒绝让运营重填，也不要把应收做成负数——那会让「客户欠平台多少」这个
// 风控指标失真，且无法从流水反推出错在哪一笔。三库兼容、并发安全。
func SettleUserCredit(id int, amount int64) error {
	if amount <= 0 {
		return errors.New("核销额度必须大于 0")
	}
	result := DB.Model(&User{}).
		Where("id = ? AND credit_used >= ?", id, amount).
		Updates(map[string]interface{}{
			"credit_used":    gorm.Expr("credit_used - ?", amount),
			"credit_settled": gorm.Expr("credit_settled + ?", amount),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrCreditSettleExceeds
	}
	return invalidateUserCache(id)
}

// GetUserCreditState 读取信用三元组（DB 权威值，不走缓存）。
// 低频场景：管理员弹窗展示、对账报表。扣费热路径的读取等信用层实现时再做缓存。
func GetUserCreditState(id int) (limit, used, settled int64, err error) {
	var u User
	if err = DB.Select("credit_limit", "credit_used", "credit_settled").
		Where("id = ?", id).First(&u).Error; err != nil {
		return 0, 0, 0, err
	}
	return u.CreditLimit, u.CreditUsed, u.CreditSettled, nil
}
