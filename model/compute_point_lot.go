package model

import (
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

// ComputePointLot 算力点批次。设计见 docs/subscription-entitlement-design.md §四。
//
// 按批次而非单一余额记账，是为了给「先烧快过期的」和「精确原路退款」两件事一次性
// 打好地基：一期虽然算力点只随套餐发放，但记账结构现在就按批次设计，将来开放
// 单卖算力点包（独立有效期、大额加赠）时，扣费逻辑一行不改，只需加一个购买入口。
//
// 有效性判定是查询时的时间条件（expires_at = 0 OR expires_at > now），不依赖 Status
// 字段——Status 只用于展示与报表，后台任务同步它，绝不能反过来拿它当扣费依据。
// 这是第一原则的直接体现（见设计文档 §2.3 / §七）：即使后台任务完全停摆，
// 过期批次也不可能被扣费程序放行。
type ComputePointLot struct {
	Id     int `json:"id"`
	UserId int `json:"user_id" gorm:"index:idx_lot_user_exp,priority:1"`

	Source string `json:"source" gorm:"type:varchar(24)"` // subscription | purchase | admin_grant
	RefId  int    `json:"ref_id"`                         // user_subscription_id / order_id

	PointsTotal int64 `json:"points_total"`
	PointsUsed  int64 `json:"points_used"`

	GrantedAt int64 `json:"granted_at"`
	// ExpiresAt 0 = 不过期（极少见，仅当套餐既发算力点又设了「永不重置」且未设结束时间时才会出现）。
	ExpiresAt int64 `json:"expires_at" gorm:"index:idx_lot_user_exp,priority:2"`

	// Status 纯展示字段，由后台任务定期同步（见 ExpireDueComputePointLots），
	// 不参与任何扣费判定——判定一律看 ExpiresAt 与 PointsUsed<PointsTotal 的实时值。
	Status string `json:"status" gorm:"type:varchar(16);default:'active'"`
}

const (
	ComputePointLotStatusActive    = "active"
	ComputePointLotStatusExhausted = "exhausted"
	ComputePointLotStatusExpired   = "expired"
)

const (
	ComputePointLotSourceSubscription = "subscription"
	ComputePointLotSourcePurchase     = "purchase" // 结构已预留，一期不开放调用入口
	ComputePointLotSourceAdminGrant   = "admin_grant"
)

// ComputePointSpend 一次消费在某个批次上实际扣减的量，用于精确原路退款。
//
// 一次请求的消费额可能跨越多个批次（一个批次余量不够、需要从下一个批次接续扣），
// 所以退款不能只记一个总额——必须知道具体扣了哪几个批次各多少，原路退回哪个批次，
// 否则快过期的批次被提前烧光、退款却进了长期批次，用户凭空损失额度。
type ComputePointSpend struct {
	LotId  int   `json:"lot_id"`
	Amount int64 `json:"amount"`
}

var ErrComputePointsInsufficient = errors.New("算力点余额不足")

// GrantComputePointLotTx 在给定事务内发放一批算力点。points<=0 时是 no-op。
//
// 幂等性由调用方保证，本函数不做重复判定：
//   - 周期性重置：maybeResetUserSubscriptionWithPlanTx 用 next_reset_time 的条件
//     更新抢占决定谁完成这次重置，只有抢赢（RowsAffected=1）的事务才走到这里；
//   - 初始发放：挂在 CompleteSubscriptionOrder 的事务里。⚠️ 那里判重读的是
//     order.Status，而它取自 gorm:query_option FOR UPDATE 的查询——GORM v2 不消费
//     这个 v1 遗留设置，等于没锁，两个并发回调都读到 Pending 时并不互斥。真正拦住
//     重复的是 topups.trade_no 的唯一索引：两笔同时在途时，后提交的那笔插影子记录
//     会撞唯一键、整个事务回滚，连这里发的批次一起撤销。但若前一笔已提交、后一笔
//     才走到 upsertSubscriptionTopUpTx 的 SELECT，它会走更新分支而非插入，这层兜底
//     就不生效。订单完成路径不是本次改动引入的，是否也改成条件更新抢占
//     （WHERE status=pending）需单独评估——它会同时影响订阅创建与收款记账。
//
// expiresAt<=0 表示不过期；调用方应尽量避免这个值——不过期的算力点批次意味着
// 客户的套餐权益跨周期累积不清零，与「按期发放」的产品语义不符，只在无法算出
// 明确到期时间时才使用（例如套餐既不设重置周期又没有结束时间）。
func GrantComputePointLotTx(tx *gorm.DB, userId int, source string, refId int, points int64, expiresAt int64) error {
	if points <= 0 {
		return nil
	}
	if tx == nil {
		return errors.New("tx is nil")
	}
	lot := &ComputePointLot{
		UserId:      userId,
		Source:      source,
		RefId:       refId,
		PointsTotal: points,
		PointsUsed:  0,
		// GrantedAt 用应用时钟而非 GetDBTimestamp()：它只是展示/审计字段，从不参与
		// 任何 WHERE 时间比较（那是 ExpiresAt 的职责，调用方传入时已用 DB 时钟算好），
		// 没必要为它单独发一次查询。这也避开了一个真实的坑：GetDBTimestamp() 永远
		// 查包级 DB（连接池），本函数总是在调用方已开好的 tx 内执行——若连接池恰好
		// 只有一个连接且被那个 tx 占用（测试环境的 MaxOpenConns(1) 就是这种配置），
		// 再发一次 DB 查询会永久阻塞，等一个永远不会被释放的连接。
		GrantedAt: common.GetTimestamp(),
		ExpiresAt: expiresAt,
		Status:    ComputePointLotStatusActive,
	}
	return tx.Create(lot).Error
}

// TryConsumeComputePoints 按到期时间由近到远，从用户的批次中原子扣减 amount。
//
// 全额扣不到时整体失败（ok=false），不做部分扣减——调用方要的是「这笔预算是否够」
// 的二元判断，部分成功对预扣/结算都没有意义，且会让已扣的那部分留下一笔不完整的账。
//
// 返回的 spent 是本次消费在各批次上的精确拆分，退款时必须原样传给
// RefundComputePoints——不能只退总额，理由见 ComputePointSpend 的注释。
//
// 并发安全说明：不使用 FOR UPDATE（GORM v2 已忽略 v1 的 gorm:query_option 机制，
// 且裸 FOR UPDATE 不兼容 SQLite，见 bank_transfer.go 的同名说明）。改用条件更新
// 抢占：单个批次的扣减带上取出时的 points_used 作 WHERE 条件，数据库保证同一行
// 并发 UPDATE 串行执行；一旦某个批次的条件更新落空（说明它被并发请求改过），
// 不能就地丢弃这份额度去查下一个批次——那会把「这个批次其实还有余量」错判成
// 「不够」，白白拒绝一笔本该成功的请求。正确做法是整体重试：丢弃本轮已扣的
// 部分（靠返回 sentinel error 触发事务回滚），用最新数据重新走一遍批次选择。
var errComputePointsConflict = errors.New("compute point lot conflict, retry")

func TryConsumeComputePoints(userId int, amount int64) (ok bool, spent []ComputePointSpend, err error) {
	if amount <= 0 {
		return true, nil, nil
	}
	for attempt := 0; attempt < 3; attempt++ {
		var attemptSpent []ComputePointSpend
		now := GetDBTimestamp()
		txErr := DB.Transaction(func(tx *gorm.DB) error {
			var lots []ComputePointLot
			// 未过期（含永不过期）且还有余量的批次，按到期时间升序——快过期的先烧。
			// 永不过期的批次（expires_at=0）排在最后：它们没有「更快过期」这回事，
			// 优先烧有明确期限的批次才是「先烧快过期的」的正确含义。
			if err := tx.
				Where("user_id = ? AND points_used < points_total AND (expires_at = 0 OR expires_at > ?)", userId, now).
				Order("CASE WHEN expires_at = 0 THEN 1 ELSE 0 END, expires_at ASC, id ASC").
				Find(&lots).Error; err != nil {
				return err
			}

			remaining := amount
			for _, lot := range lots {
				if remaining <= 0 {
					break
				}
				avail := lot.PointsTotal - lot.PointsUsed
				if avail <= 0 {
					continue
				}
				take := remaining
				if take > avail {
					take = avail
				}
				res := tx.Model(&ComputePointLot{}).
					Where("id = ? AND points_used = ?", lot.Id, lot.PointsUsed).
					Update("points_used", gorm.Expr("points_used + ?", take))
				if res.Error != nil {
					return res.Error
				}
				if res.RowsAffected == 0 {
					return errComputePointsConflict
				}
				attemptSpent = append(attemptSpent, ComputePointSpend{LotId: lot.Id, Amount: take})
				remaining -= take
			}
			if remaining > 0 {
				return ErrComputePointsInsufficient
			}
			return nil
		})
		if txErr == nil {
			return true, attemptSpent, nil
		}
		if errors.Is(txErr, errComputePointsConflict) {
			continue
		}
		if errors.Is(txErr, ErrComputePointsInsufficient) {
			return false, nil, nil
		}
		return false, nil, txErr
	}
	return false, nil, errors.New("compute point consume conflict retry exhausted")
}

// RefundComputePoints 按 TryConsumeComputePoints 返回的拆分逐笔原路退还。
//
// 必须逐笔退回原批次，不能笼统退总额——否则快过期的批次被提前烧光、
// 退款却进了长期批次，用户凭空损失额度（设计文档 §十一 风险 5）。
//
// 条件更新保证不把 PointsUsed 减成负数：若批次已被后台任务清理或数据异常，
// 宁可跳过这一笔（记日志、留给对账发现）也不产生负的已用量。
func RefundComputePoints(spent []ComputePointSpend) error {
	for _, s := range spent {
		if s.Amount <= 0 {
			continue
		}
		result := DB.Model(&ComputePointLot{}).
			Where("id = ? AND points_used >= ?", s.LotId, s.Amount).
			Update("points_used", gorm.Expr("points_used - ?", s.Amount))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			common.SysLog(fmt.Sprintf(
				"compute point refund skipped: lot=%d amount=%d (insufficient points_used)",
				s.LotId, s.Amount))
			continue
		}
		// ExpireDueComputePointLots 只会把 active 单向同步成 exhausted/expired，
		// 不会反向纠正——退款把某个已被标记 exhausted 的批次重新腾出余量后，
		// 若不在这里补一手，Status 会永远停在 exhausted，报表/管理端看到的是
		// 「已耗尽」但实际余量>0 的假象（扣费判定本身不看 Status，不受影响）。
		// 只纠正 exhausted，不碰 expired——批次即使因退款腾出余量，过了有效期
		// 依然不可用，不能把它救回「看起来能用」的 active。
		if err := DB.Model(&ComputePointLot{}).
			Where("id = ? AND status = ? AND points_used < points_total", s.LotId, ComputePointLotStatusExhausted).
			Update("status", ComputePointLotStatusActive).Error; err != nil {
			return err
		}
	}
	return nil
}

// ComputePointBalance 算力点余额汇总，供用户侧展示与对账使用。
type ComputePointBalance struct {
	Total     int64 `json:"total"`     // 所有未过期批次的发放总额
	Used      int64 `json:"used"`      // 所有未过期批次的已用量
	Available int64 `json:"available"` // Total - Used
}

// GetComputePointBalance 汇总用户当前可用的算力点批次。
//
// 只统计未过期批次：expires_at 是查询时间条件，过期批次即使 points_used<points_total
// 也不计入可用余额——这与 TryConsumeComputePoints 的筛选条件必须保持一致，
// 否则用户会看到「显示有余额但花不出去」。
func GetComputePointBalance(userId int) (*ComputePointBalance, error) {
	now := GetDBTimestamp()
	var row struct {
		Total int64
		Used  int64
	}
	if err := DB.Model(&ComputePointLot{}).
		Where("user_id = ? AND (expires_at = 0 OR expires_at > ?)", userId, now).
		Select("COALESCE(SUM(points_total),0) as total, COALESCE(SUM(points_used),0) as used").
		Scan(&row).Error; err != nil {
		return nil, err
	}
	return &ComputePointBalance{
		Total:     row.Total,
		Used:      row.Used,
		Available: row.Total - row.Used,
	}, nil
}

// ExpireDueComputePointLots 把到期批次的展示状态同步为 expired，用尽的同步为 exhausted。
//
// 纯粹的展示/报表同步，不是扣费判定的一部分——即使这个任务从不运行，
// TryConsumeComputePoints 的查询条件也会正确地拒绝已过期批次。
// 与 model.ExpireDueSubscriptions 同一节奏，供 service 层的定时任务调用。
//
// 先 SELECT id 再按 id 批量 UPDATE，不能写成 .Limit(n).Update(...)：GORM 这个版本
// 的 UPDATE/DELETE callback 根本不消费 Limit 子句（只有 Find/SELECT 会用到，见
// callbacks.go 的 queryClauses），那种写法在三个库上都会静默忽略 limit、
// 全表更新——不是「MySQL 能跑 PG 报错」的方言问题，是限流直接失效。
func ExpireDueComputePointLots(limit int) (int, error) {
	if limit <= 0 {
		limit = 500
	}
	now := GetDBTimestamp()

	var expiredIds []int
	if err := DB.Model(&ComputePointLot{}).
		Where("status = ? AND expires_at > 0 AND expires_at <= ?", ComputePointLotStatusActive, now).
		Limit(limit).Pluck("id", &expiredIds).Error; err != nil {
		return 0, err
	}
	if len(expiredIds) > 0 {
		if err := DB.Model(&ComputePointLot{}).Where("id IN ?", expiredIds).
			Update("status", ComputePointLotStatusExpired).Error; err != nil {
			return 0, err
		}
	}

	var exhaustedIds []int
	if err := DB.Model(&ComputePointLot{}).
		Where("status = ? AND points_used >= points_total", ComputePointLotStatusActive).
		Limit(limit).Pluck("id", &exhaustedIds).Error; err != nil {
		return len(expiredIds), err
	}
	if len(exhaustedIds) > 0 {
		if err := DB.Model(&ComputePointLot{}).Where("id IN ?", exhaustedIds).
			Update("status", ComputePointLotStatusExhausted).Error; err != nil {
			return len(expiredIds), err
		}
	}
	return len(expiredIds) + len(exhaustedIds), nil
}
