package model

import (
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"

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

// SettleOverdraftToCredit 把透支的负余额结转为信用欠款，返回本次结转额。
//
// 这是「授信接入扣费链路」的核心一步，也是唯一一步。扣费侧一行未改——照常扣
// User.Quota、照常允许短暂透支为负（DecreaseUserQuota 本就是无条件递减）；结算完成后
// 由本函数把负的那部分挪进 CreditUsed 并让 quota 归零。
//
// 为什么不在扣费时按「先现金后信用」分流：那需要在扣费热路径上做两步条件更新，
// 且要改 HybridFunding 里那段被多轮 review 打磨过的分支。而结转方案同时避开了
// 「负余额即欠款」的两个坑——十余处无条件递减不会绕过授信上限（它们只在透支窗口内
// 有效，结转时统一归拢），User.Quota 对其余几十处读取方仍是「预付余额」而非净额。
//
// 透支窗口只存在于「扣费完成 → 结算结转」之间，且 quota 短暂为负在现有代码里本就允许。
//
// 用读-条件更新-重试而非单条 UPDATE：单条语句写得出
// （SET credit_used = credit_used - quota, quota = 0）但拿不到结转额，
// 而日志要记 CreditConsumed。乐观锁失败说明并发扣费正在进行，重试即可；
// 三次耗尽就放弃，留给下一次结算或每日自洽校验发现——这是统计口径，不是扣费依据。
// maxAmount 封顶本次结转额，取本次请求的实际消费额。
//
// 不封顶会导致并发归因错乱：同一客户两个请求都透支、都还没结算时，先结算的那个
// 会把两笔透支一起扫进自己的 CreditConsumed（日志记 2000 而它只消费了 1000），
// 后结算的记 0，于是前者的 CashConsumed = Quota − Points − Credit 变成负数。
// 聚合总额仍然对，但单条日志和按用户/模型的下钻会出现负值。
// 封顶后每个请求只认领自己那部分，剩下的留给对应请求的结算去结转。
func SettleOverdraftToCredit(id int, maxAmount int64) (settled int64, err error) {
	if maxAmount <= 0 {
		return 0, nil
	}
	for attempt := 0; attempt < 3; attempt++ {
		var row struct {
			Quota       int
			CreditLimit int64
		}
		if err = DB.Model(&User{}).Where("id = ?", id).
			Select("quota", "credit_limit").Scan(&row).Error; err != nil {
			return 0, err
		}
		// 未开授信的用户不结转。他们的负余额来自结算补扣（服务已交付、允许欠费，
		// 见 WalletFunding.Settle），是预估不准造成的系统性透支，不是授信——
		// 把它记成应收账款没有依据，还会让「授信敞口」= limit − used 变成负数。
		// 保持为负 quota 即改动前的既有语义：下次充值自然填平，现金账照样自洽
		// （那笔超支已计入现金消耗，期初 − 消耗 == 负余额，等式成立）。
		if row.CreditLimit <= 0 {
			return 0, nil
		}
		current := row.Quota
		if current >= 0 {
			// 批量更新模式下这里可能读到**陈旧**值：Settle 补扣走
			// DecreaseUserQuota(..., db=false)，开启 BATCH_UPDATE_ENABLED 时它只入队、
			// 不立即落库，等队列刷新后 quota 才变负——而那时结转早已返回 0，
			// 这笔透支就永远不会折进 credit_used（应收低估、授信上限对这部分失效）。
			//
			// 预扣路径不受影响：WalletFunding/HybridFunding 的预扣都走
			// TryDecreaseUserQuotaWithinCredit，那是条件更新、恒直写。
			// 受影响的只有结算补扣的差额，金额远小于预扣，且 BATCH_UPDATE_ENABLED
			// 默认关闭。真要根治需要在结转前 flush 该用户的批量队列，
			// 那要动批量更新的架构，收益不抵成本。此处留日志供排查。
			if common.BatchUpdateEnabled {
				common.SysLog(fmt.Sprintf(
					"overdraft settle read non-negative quota under batch mode (user=%d); "+
						"a queued decrement may settle late", id))
			}
			return 0, nil
		}
		amount := min(int64(-current), maxAmount)
		res := DB.Model(&User{}).
			Where("id = ? AND quota = ?", id, current).
			Updates(map[string]interface{}{
				"credit_used": gorm.Expr("credit_used + ?", amount),
				// 加而非置 0：封顶后可能只结转了一部分，余下的透支留给并发的
				// 那个请求结算时处理。
				"quota": gorm.Expr("quota + ?", amount),
			})
		if res.Error != nil {
			return 0, res.Error
		}
		if res.RowsAffected == 1 {
			// 缓存失效失败**不能**让结转结果丢失：DB 里 credit_used 已经加上了，
			// 调用方若因这个错误把 settled 当成 0，消费日志就会记 CreditConsumed=0，
			// 而欠款真实存在——每日自洽校验会对信用账报假不平。
			// 缓存陈旧是可自愈的小问题（有 TTL、下次写入也会失效），只记日志。
			if cerr := invalidateUserCache(id); cerr != nil {
				common.SysLog(fmt.Sprintf(
					"credit settled but cache invalidation failed: user=%d amount=%d err=%s",
					id, amount, cerr.Error()))
			}
			return amount, nil
		}
	}
	return 0, nil
}

// ReduceUserCreditUsed 冲销欠款（退款原路返回授信，不是回款核销）。
//
// 与 SettleUserCredit 的区别：那个是客户真的付了钱、要计营收并累加 CreditSettled；
// 这个是服务没交付所以欠款本就不该存在，只减 CreditUsed、不动 CreditSettled、不计营收。
// 混用会让「累计已回款」虚高，对账时看到一笔并不存在的回款。
//
// 条件更新保证不减成负数：并发下若欠款已被回款核销掉，这里就什么都不做——
// 宁可少冲一笔（客户仍欠着、可人工核）也不要把应收做成负数。
func ReduceUserCreditUsed(id int, amount int64) error {
	if amount <= 0 {
		return nil
	}
	result := DB.Model(&User{}).
		Where("id = ? AND credit_used >= ?", id, amount).
		Update("credit_used", gorm.Expr("credit_used - ?", amount))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		// 欠款已不足以冲销（被回款核销或并发冲销过），不视为错误：
		// 退款主流程不该因此中断，差额由每日自洽校验暴露。
		common.SysLog(fmt.Sprintf("credit reduce skipped: user=%d amount=%d (insufficient credit_used)", id, amount))
		return nil
	}
	return invalidateUserCache(id)
}

// TryDecreaseUserQuotaWithinCredit 条件扣减：允许透支，但透支后的负余额不得超过
// 可用授信（credit_limit − credit_used）。未开授信的用户等价于原来的「余额必须充足」。
//
// 给混扣路径用。HybridFunding 的预扣走 TryDecreaseUserQuota（要求余额充足、绝不透支），
// 那道门对授信客户是关死的：可用额检查把授信算进去放行了，预扣却因为钱包不足而
// 403——持有营销积分的授信客户会被完全挡在门外。
//
// 条件写在 WHERE 里，三库通吃且并发安全：两个请求同时扣时，第二个会因为
// credit_used 已被第一个推高而自动落在上限内或失败，不会双双越过授信上限。
func TryDecreaseUserQuotaWithinCredit(id int, amount int) (ok bool, err error) {
	if amount <= 0 {
		return true, nil
	}
	result := DB.Model(&User{}).
		Where("id = ? AND quota - ? >= -(credit_limit - credit_used)", id, amount).
		Update("quota", gorm.Expr("quota - ?", amount))
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected == 0 {
		return false, nil
	}
	if cerr := cacheDecrUserQuota(id, int64(amount)); cerr != nil {
		common.SysLog("failed to sync quota cache after credit-aware decrease: " + cerr.Error())
	}
	return true, nil
}
