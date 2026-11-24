package model

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/bytedance/gopkg/util/gopool"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PointsLot 积分批次。设计见 docs/points-expiry-design.md。
//
// 与算力点批次（compute_point_lot.go）不同，**批次不是扣费依据**：能不能花只看
// users.points_balance，批次只回答「余额里每一部分属于哪一笔发放、什么时候到期」。
// 归属在低频时刻（发放、过期）惰性完成，热路径零改动。
//
// 这条路线的前提是不变式：每次 sync 之后 points_balance == Σ(amount - attributed)
// over status='active'。过期积分在「到期时刻 → 过期任务执行」之间仍可花（约 5 分钟），
// 这是为换取热路径零改动而明确接受的代价（§3.1）。
type PointsLot struct {
	Id     int    `json:"id"`
	UserId int    `json:"user_id" gorm:"index:idx_plot_user_exp,priority:1"`
	Source string `json:"source" gorm:"type:varchar(24)"`
	// RefId 业务引用：签到周 key / 兑换码 id / 管理员单号 / 订单号…；展示与审计用。
	RefId string `json:"ref_id" gorm:"type:varchar(64)"`

	Amount int64 `json:"amount" gorm:"bigint"` // 发放总额（quota unit）；签到周批次为累加值
	// Attributed 已归属给本批次的消耗。
	// ⚠️ 与 users.points_used（用户累计已用积分）不是一回事：过期不影响后者，
	// 管理员扣减影响前者不影响后者。刻意不叫 used，就是为了不让人混。
	Attributed int64 `json:"attributed" gorm:"bigint"`
	Forfeited  int64 `json:"forfeited" gorm:"bigint"` // 到期作废额（仅 status=expired 时非 0）

	GrantedAt int64 `json:"granted_at" gorm:"bigint"`
	// ExpiresAt 0 = 永不过期。
	ExpiresAt int64  `json:"expires_at" gorm:"bigint;index:idx_plot_user_exp,priority:2;index:idx_plot_status_exp,priority:2"`
	Status    string `json:"status" gorm:"type:varchar(16);default:'active';index:idx_plot_status_exp,priority:1"`
	// UpdatedAt 每次改 Attributed / Status 时写入，排障用（「这笔是哪天被扣的」）。
	// 刻意不用 GORM 的 autoUpdateTime：这一列的语义是「归属变动时间」，
	// 而 Amount 累加（签到合并）不改它。
	UpdatedAt int64 `json:"updated_at" gorm:"bigint"`
}

const (
	PointsLotStatusActive    = "active"    // 有剩余
	PointsLotStatusExhausted = "exhausted" // Attributed == Amount，可被回补复活
	PointsLotStatusExpired   = "expired"   // 已作废，终态，不可复活
)

// 这两个是批次独有的来源，其余来源与 FundSource* 同值复用。
const (
	// PointsLotSourceLegacy 上线前就存在的存量余额。用户首次 sync 时按
	// 「余额 − 已有批次剩余」补建。永不过期。
	//
	// 它同时充当「已初始化」标记：若用户已有 legacy 批次，之后再出现的 surplus
	// 就不可能是存量了，只能是余额溢出（漏建批次 / 跨到期退款）。
	PointsLotSourceLegacy = "legacy"
	// PointsLotSourceSurplus 余额多于批次剩余的部分。**永不过期 + 告警**，理由见
	// docs/points-expiry-design.md §3.4：它的主要成因是「某条发放路径漏建批次」，
	// 那是我们的缺陷，用自动作废去响应等于把证据和用户的积分一起清掉。
	PointsLotSourceSurplus = "surplus"
)

func (PointsLot) TableName() string { return "points_lots" }

// remaining 本批次当前还剩多少（未归属、未作废）。
func (l PointsLot) remaining() int64 { return l.Amount - l.Attributed - l.Forfeited }

// ---------------------------------------------------------------------------
// 纯函数：分摊
// ---------------------------------------------------------------------------

// lotMutation 对单个现有批次的一处改动。写库路径照此逐条 UPDATE。
type lotMutation struct {
	LotId int
	// AttributedDelta 正 = 归属消耗，负 = 回补（退款）。
	AttributedDelta int64
	NewStatus       string
}

// allocatePointsLots 把「余额与批次之间的差额」分摊到各批次上。**纯函数，不碰数据库**：
// 写库路径、展示路径、性质测试都调它，保证三处口径只有一份实现。
//
// lots 需含：该用户 status='active' 的全部批次，以及 status='exhausted' 且未过期
// （expires_at = 0 OR expires_at > now）的批次（回补候选）。
//
// 返回的 mutations 只描述对**现有**批次的改动；回补不完的部分以 surplus 返回，
// 由调用方决定建不建批次、用什么 Source——新批次的 id 只有写库后才有。
//
// 语义：D = Σ(active 剩余) − balance。
//   - D > 0：有未归属的净消耗，按「到期最早的先扣、永不过期的最后」贪心归属。
//   - D < 0：余额多于批次（退款晚于上次归属 / 漏建批次），按上述顺序的**逆序**回补，
//     已到期待作废的批次不复活。
//
// 与「每笔当场扣」并不等价（见 §3.4 的反例），但惰性实现按前缀支配它：快到期批次
// 烧得只多不少，因此累计作废额不会更多。等价性的正确表述见设计文档，别在这里"优化"成
// 逐批精确——那需要把批次拆分贯穿整个分层回滚引擎。
func allocatePointsLots(lots []PointsLot, balance int64, now int64) (mutations []lotMutation, surplus int64) {
	if balance < 0 {
		balance = 0
	}

	var sumActive int64
	for _, l := range lots {
		if l.Status == PointsLotStatusActive {
			sumActive += l.Amount - l.Attributed
		}
	}
	d := sumActive - balance

	if d > 0 {
		ordered := make([]PointsLot, 0, len(lots))
		for _, l := range lots {
			if l.Status == PointsLotStatusActive {
				ordered = append(ordered, l)
			}
		}
		// 到期最早的先扣；永不过期的排最后（它们没有「更快过期」这回事）。
		sort.SliceStable(ordered, func(i, j int) bool {
			return earlierExpiryFirst(ordered[i], ordered[j])
		})
		for _, l := range ordered {
			if d <= 0 {
				break
			}
			avail := l.Amount - l.Attributed
			if avail <= 0 {
				continue
			}
			take := d
			if take > avail {
				take = avail
			}
			newAttributed := l.Attributed + take
			status := PointsLotStatusActive
			if newAttributed >= l.Amount {
				status = PointsLotStatusExhausted
			}
			mutations = append(mutations, lotMutation{
				LotId:           l.Id,
				AttributedDelta: take,
				NewStatus:       status,
			})
			d -= take
		}
		return mutations, 0
	}

	if d == 0 {
		return nil, 0
	}

	// d < 0：回补。候选与扣减顺序互为逆序，所以「最后被扣的」最先还。
	surplus = -d
	ordered := make([]PointsLot, 0, len(lots))
	for _, l := range lots {
		if l.Attributed <= 0 {
			continue
		}
		if l.Status != PointsLotStatusActive && l.Status != PointsLotStatusExhausted {
			continue
		}
		// 已到期待作废的批次不复活：那些积分马上要归零，回补进去等于白送一张过期券。
		if l.ExpiresAt > 0 && l.ExpiresAt <= now {
			continue
		}
		ordered = append(ordered, l)
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		return laterExpiryFirst(ordered[i], ordered[j])
	})
	for _, l := range ordered {
		if surplus <= 0 {
			break
		}
		give := surplus
		if give > l.Attributed {
			give = l.Attributed
		}
		if give <= 0 {
			continue
		}
		mutations = append(mutations, lotMutation{
			LotId:           l.Id,
			AttributedDelta: -give,
			// 回补必然腾出余量（Amount > 0 且 Attributed 减少），批次重新可用。
			NewStatus: PointsLotStatusActive,
		})
		surplus -= give
	}
	return mutations, surplus
}

// earlierExpiryFirst 扣减顺序：到期早的优先，永不过期（expires_at=0）最后，同到期按 id 升序。
func earlierExpiryFirst(a, b PointsLot) bool {
	ae, be := a.ExpiresAt == 0, b.ExpiresAt == 0
	if ae != be {
		return !ae // 有明确到期时间的排前
	}
	if a.ExpiresAt != b.ExpiresAt {
		return a.ExpiresAt < b.ExpiresAt
	}
	return a.Id < b.Id
}

// laterExpiryFirst 回补顺序：恰为 earlierExpiryFirst 的逆序——永不过期的先还，
// 其余按到期时间倒序，同到期按 id 倒序。
func laterExpiryFirst(a, b PointsLot) bool {
	ae, be := a.ExpiresAt == 0, b.ExpiresAt == 0
	if ae != be {
		return ae // 永不过期的排前
	}
	if a.ExpiresAt != b.ExpiresAt {
		return a.ExpiresAt > b.ExpiresAt
	}
	return a.Id > b.Id
}

// ---------------------------------------------------------------------------
// 加锁与同步
// ---------------------------------------------------------------------------

// lockUserPointsTx 在事务内取得该用户的串行化点，并返回加锁后读到的余额。
//
// 所有会写批次的路径（发放、过期任务）都必须先调它，否则两个并发 sync 会把同一笔 D
// 分摊两次。
//
// 为什么不是统一的 FOR UPDATE：SQLite 驱动（glebarez/sqlite）在 ClauseBuilders 里
// 遇到 clause.Locking 直接 return、不输出任何 SQL，也就是**静默不加锁**。所以 SQLite
// 必须用「第一条语句就是写入」来取得库级写锁（先读后写会遇上锁升级死锁，busy_timeout
// 不生效）。
//
// exists 的取法也必须按分支区分：SQLite 取空操作 UPDATE 的 RowsAffected（SQLite 的
// changes() 按命中行计，不比较值是否变化）；MySQL 驱动默认按「实际改变行数」计，
// 同样的空操作 UPDATE 在 MySQL 上恒为 0，若误用会把所有用户判成「不存在」而删光批次，
// 所以 MySQL/PG 从 Scan 的结果判断。
func lockUserPointsTx(tx *gorm.DB, userId int) (balance int64, exists bool, err error) {
	if common.UsingSQLite {
		res := tx.Model(&User{}).Where("id = ?", userId).
			Update("points_balance", gorm.Expr("points_balance"))
		if res.Error != nil {
			return 0, false, res.Error
		}
		if res.RowsAffected == 0 {
			return 0, false, nil
		}
		if err := tx.Model(&User{}).Where("id = ?", userId).
			Select("points_balance").Scan(&balance).Error; err != nil {
			return 0, false, err
		}
		return balance, true, nil
	}

	var row struct {
		PointsBalance int64
	}
	res := tx.Model(&User{}).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ?", userId).
		Select("points_balance").
		Scan(&row)
	if res.Error != nil {
		return 0, false, res.Error
	}
	if res.RowsAffected == 0 {
		return 0, false, nil
	}
	return row.PointsBalance, true, nil
}

// loadPointsLotsForSync 取出分摊所需的批次集合：active 全部 + exhausted 且未过期
// （后者是回补候选）。
func loadPointsLotsForSync(tx *gorm.DB, userId int, now int64) ([]PointsLot, error) {
	var lots []PointsLot
	err := tx.Where(
		"user_id = ? AND (status = ? OR (status = ? AND (expires_at = 0 OR expires_at > ?)))",
		userId, PointsLotStatusActive, PointsLotStatusExhausted, now,
	).Order("id").Find(&lots).Error
	return lots, err
}

func applyLotMutations(tx *gorm.DB, mutations []lotMutation, now int64) error {
	for _, m := range mutations {
		if m.AttributedDelta == 0 && m.NewStatus == "" {
			continue
		}
		updates := map[string]interface{}{"updated_at": now}
		if m.AttributedDelta != 0 {
			updates["attributed"] = gorm.Expr("attributed + ?", m.AttributedDelta)
		}
		if m.NewStatus != "" {
			updates["status"] = m.NewStatus
		}
		if err := tx.Model(&PointsLot{}).Where("id = ?", m.LotId).Updates(updates).Error; err != nil {
			return err
		}
	}
	return nil
}

// syncPointsLotsTx 把「余额与批次之间的差额」落到批次上，并在末尾断言不变式。
//
// 必须在调用方事务内执行；它自己负责加锁。
func syncPointsLotsTx(tx *gorm.DB, userId int, now int64) error {
	balance, exists, err := lockUserPointsTx(tx, userId)
	if err != nil {
		return err
	}
	if !exists {
		// 用户已被硬删除：清掉残留批次。不依赖「B=0 时 surplus 恰好为 0」的巧合。
		return tx.Where("user_id = ?", userId).Delete(&PointsLot{}).Error
	}
	return syncPointsLotsAfterLockTx(tx, userId, balance, now)
}

// syncPointsLotsAfterLockTx 前提：调用方已在同一事务内加过用户锁并拿到余额。
// sync 本身不改余额，所以传入的 balance 全程有效。管理员「覆盖」需要在算差额前
// 先 sync，用它避免重复加锁与重复读取。
func syncPointsLotsAfterLockTx(tx *gorm.DB, userId int, balance int64, now int64) error {
	effective := balance
	if effective < 0 {
		// 新代码里不可达（override 有非负校验、TryDecrease 有 >= 守卫、Decrease 钳到 0），
		// 但存量库里可能有（曾经允许写负数）。这里不能报错：GrantPointsTx 第一步就是
		// sync，报错会让该用户永久无法签到、无法被发放任何积分且无自愈路径。
		// 按 0 归属 = 「所有批次视为已用完」；之后的发放先填平负余额，归属随之自洽。
		common.SysError(fmt.Sprintf(
			"points lot sync: user %d has negative points_balance %d, attributing as 0", userId, balance))
		effective = 0
	}

	lots, err := loadPointsLotsForSync(tx, userId, now)
	if err != nil {
		return err
	}
	mutations, surplus := allocatePointsLots(lots, effective, now)
	if err := applyLotMutations(tx, mutations, now); err != nil {
		return err
	}

	// hasLegacy 不需要额外查询：legacy 的 expires_at=0，必在加载范围内。
	hasLegacy := false
	for _, l := range lots {
		if l.Source == PointsLotSourceLegacy {
			hasLegacy = true
			break
		}
	}
	switch {
	case !hasLegacy:
		// 首次 sync：把多出来的余额补建成存量批次。surplus 为 0 时也建一条
		// Amount=0 的「已初始化」标记，否则用户此后第一笔真正的 surplus 会被
		// 误标成存量（漏掉告警）。
		status := PointsLotStatusActive
		if surplus <= 0 {
			status = PointsLotStatusExhausted
		}
		if err := tx.Create(&PointsLot{
			UserId:    userId,
			Source:    PointsLotSourceLegacy,
			RefId:     fmt.Sprintf("legacy:%d", userId),
			Amount:    surplus,
			GrantedAt: now,
			ExpiresAt: 0,
			Status:    status,
			UpdatedAt: now,
		}).Error; err != nil {
			return err
		}
	case surplus > 0:
		// 已有存量标记，再出现 surplus 只能是余额溢出（漏建批次 / 跨到期退款）。
		// 永不过期 + 由 §十一 的健康检查告警。
		if err := tx.Create(&PointsLot{
			UserId:    userId,
			Source:    PointsLotSourceSurplus,
			RefId:     fmt.Sprintf("surplus:%d:%d", userId, now),
			Amount:    surplus,
			GrantedAt: now,
			ExpiresAt: 0,
			Status:    PointsLotStatusActive,
			UpdatedAt: now,
		}).Error; err != nil {
			return err
		}
		common.SysError(fmt.Sprintf(
			"points balance surplus: user=%d amount=%d (no matching lot; check for a grant path bypassing GrantPoints)",
			userId, surplus))
	}

	// 断言：sync 之后不变式必须精确成立。
	//
	// 能力边界——它能抓「写库遗漏 / 多写 / 算术错」（合计 ≠ 余额），**抓不到「记错了
	// 批次」**：分摊到 A 还是 B，合计照样相等。归属顺序的正确性只由性质测试保证。
	var sum int64
	if err := tx.Model(&PointsLot{}).
		Where("user_id = ? AND status = ?", userId, PointsLotStatusActive).
		Select("COALESCE(SUM(amount - attributed), 0)").Scan(&sum).Error; err != nil {
		return err
	}
	if sum != effective {
		common.SysError(fmt.Sprintf(
			"points lot invariant violated: user=%d sum_active=%d balance=%d", userId, sum, effective))
		return fmt.Errorf("积分批次不变式不成立: user=%d sum=%d balance=%d", userId, sum, effective)
	}
	return nil
}

// ---------------------------------------------------------------------------
// 发放
// ---------------------------------------------------------------------------

// PointsGrant 一次积分发放的完整描述。
type PointsGrant struct {
	UserId int
	// Quota 是 **quota unit**，不是积分数。调用方必须先 common.PointsToQuota 换算。
	Quota     int64
	Source    string
	RefId     string
	ExpiresAt int64 // 0 = 永不过期；由 ExpireAt* 算出
	// MergeWeek 仅签到使用：并入同用户、同 Source、同 ExpiresAt、status IN (active, exhausted)
	// 的现有批次；Amount += Quota、status 置回 active、GrantedAt 保持首笔、RefId 不变。
	MergeWeek bool
	// Now 0 = common.GetTimestamp()。测试注入用；GrantedAt 与 sync 的 now 都取它。
	Now int64

	// Fund 流水模板。UserId / Account / QuotaDelta / CashFen 由 GrantPointsTx 填。
	Fund FundEntry
}

var ErrPointsGrantDuplicate = errors.New("points grant already recorded")

// GrantPointsTx 在调用方事务内发放。顺序固定：先 sync（把此前的消耗归属给旧批次，
// 新批次不背锅），再插批次、加余额、记流水。
//
// 不碰 Redis——缓存必须在事务提交后更新（见 GrantPoints）。
func GrantPointsTx(tx *gorm.DB, g PointsGrant) (lotId int, err error) {
	g.Now = grantNow(g.Now)
	if err := syncPointsLotsTx(tx, g.UserId, g.Now); err != nil {
		return 0, err
	}
	return grantPointsAfterSyncTx(tx, g)
}

// grantPointsAfterSyncTx 前提：同一事务内已完成 syncPointsLotsTx。
// 顺序：插入/合并批次 → 加余额 → 记流水（幂等键撞了整笔回滚）。
//
// 只有已自行 sync 的调用方（管理员 override 调高）直接用它，其余一律走 GrantPointsTx，
// 免得出现「没 sync 就插批次」把历史消耗算到新批次头上。
func grantPointsAfterSyncTx(tx *gorm.DB, g PointsGrant) (lotId int, err error) {
	if tx == nil {
		return 0, errors.New("grant points: tx is nil")
	}
	if g.Quota <= 0 {
		return 0, errors.New("发放积分必须大于 0")
	}
	if g.Source == "" {
		return 0, errors.New("发放积分必须指定来源")
	}
	now := grantNow(g.Now)

	var merged *PointsLot
	if g.MergeWeek {
		var existing PointsLot
		err := tx.Where(
			"user_id = ? AND source = ? AND expires_at = ? AND status IN ?",
			g.UserId, g.Source, g.ExpiresAt,
			[]string{PointsLotStatusActive, PointsLotStatusExhausted},
		).Order("id").First(&existing).Error
		switch {
		case err == nil:
			merged = &existing
		case errors.Is(err, gorm.ErrRecordNotFound):
			// 新建
		default:
			return 0, err
		}
	}

	if merged != nil {
		if err := tx.Model(&PointsLot{}).Where("id = ?", merged.Id).Updates(map[string]interface{}{
			"amount":     gorm.Expr("amount + ?", g.Quota),
			"status":     PointsLotStatusActive,
			"updated_at": now,
		}).Error; err != nil {
			return 0, err
		}
		lotId = merged.Id
	} else {
		lot := &PointsLot{
			UserId:    g.UserId,
			Source:    g.Source,
			RefId:     g.RefId,
			Amount:    g.Quota,
			GrantedAt: now,
			ExpiresAt: g.ExpiresAt,
			Status:    PointsLotStatusActive,
			UpdatedAt: now,
		}
		if err := tx.Create(lot).Error; err != nil {
			return 0, err
		}
		lotId = lot.Id
	}

	if err := tx.Model(&User{}).Where("id = ?", g.UserId).
		Update("points_balance", gorm.Expr("points_balance + ?", g.Quota)).Error; err != nil {
		return 0, err
	}

	entry := g.Fund
	entry.UserId = g.UserId
	entry.Account = FundAccountPoints
	entry.QuotaDelta = g.Quota
	entry.CashFen = 0
	inserted, err := insertFundEntryTx(tx, &entry)
	if err != nil {
		return 0, err
	}
	if !inserted {
		// 幂等键已存在：这是发放的原子闸门。整笔回滚，余额与批次一并撤销。
		return 0, ErrPointsGrantDuplicate
	}
	return lotId, nil
}

// GrantPoints 自开事务发放，提交后异步更新 Redis 缓存（与 IncreaseUserPoints 同语义）。
//
// 发放**必须**走这里或 GrantPointsTx：直接 IncreaseUserPoints 会让余额与批次脱节，
// 表现为一次 surplus（§3.4）。IncreaseUserPoints 只保留给退款 / 回补。
func GrantPoints(g PointsGrant) (lotId int, err error) {
	g.Now = grantNow(g.Now)
	err = DB.Transaction(func(tx *gorm.DB) error {
		var txErr error
		lotId, txErr = GrantPointsTx(tx, g)
		return txErr
	})
	if err != nil {
		return 0, err
	}
	gopool.Go(func() {
		if cacheErr := cacheIncrUserPoints(g.UserId, g.Quota); cacheErr != nil {
			common.SysLog("failed to increase user points cache: " + cacheErr.Error())
		}
	})
	return lotId, nil
}

// SetUserPointsBalance 管理员「覆盖」积分：在事务内把余额设为 target（绝对值）。
//
// 与旧的 Update("points_balance", v) 相比，这里修掉三件事：
//   - 先 sync（含加锁），差额按加锁后读到的余额计算，消除与并发扣费的竞态；
//   - 调高时走发放路径建批次——余额必须有批次，否则下次 sync 直接表现为 surplus；
//   - 流水在事务内写，与实际余额变化同值（旧路径是提交后 best-effort 补记）。
//
// 调低时只减余额，不改批次：归属在下次 sync 惰性完成，自然按「最快到期优先」扣。
//
// grant 提供调高时的来源 / 有效期 / 幂等键与流水模板；返回的 diff 是实际差额，
// 供调用方写日志（记请求值会和流水、和实际余额三者对不上）。
func SetUserPointsBalance(userId int, target int64, now int64, grant PointsGrant) (diff int64, err error) {
	if target < 0 {
		return 0, errors.New("积分余额不能为负")
	}
	now = grantNow(now)
	err = DB.Transaction(func(tx *gorm.DB) error {
		balance, exists, lockErr := lockUserPointsTx(tx, userId)
		if lockErr != nil {
			return lockErr
		}
		if !exists {
			return errors.New("用户不存在")
		}
		if syncErr := syncPointsLotsAfterLockTx(tx, userId, balance, now); syncErr != nil {
			return syncErr
		}
		diff = target - balance
		switch {
		case diff > 0:
			g := grant
			g.UserId = userId
			g.Quota = diff
			g.Now = now
			if g.Source == "" {
				g.Source = FundSourceAdminAdjust
			}
			_, grantErr := grantPointsAfterSyncTx(tx, g)
			return grantErr
		case diff < 0:
			if upErr := tx.Model(&User{}).Where("id = ?", userId).
				Update("points_balance", gorm.Expr("points_balance + ?", diff)).Error; upErr != nil {
				return upErr
			}
			entry := grant.Fund
			entry.UserId = userId
			entry.Account = FundAccountPoints
			entry.QuotaDelta = diff
			entry.CashFen = 0
			_, insErr := insertFundEntryTx(tx, &entry)
			return insErr
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	if diff != 0 {
		// 绝对值写入，增量无法表达，直接失效缓存下次回源。
		if cErr := invalidateUserCache(userId); cErr != nil {
			common.SysLog(fmt.Sprintf("failed to invalidate user cache after points override (user=%d): %s", userId, cErr.Error()))
		}
	}
	return diff, nil
}

func grantNow(now int64) int64 {
	if now > 0 {
		return now
	}
	return common.GetTimestamp()
}

// ---------------------------------------------------------------------------
// 到期时间计算
// ---------------------------------------------------------------------------

// pointsExpiryLoc 积分到期口径的时区。部署环境设了 TZ=Asia/Shanghai，但代码不能
// 依赖主机时区（参照 service/reconcile_helpers.go 的做法）。
var pointsExpiryLoc = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("CST", 8*3600)
	}
	return loc
}()

// ExpireAtDays 发放后 N 天，算到当天 23:59:59（北京时间）。days <= 0 → 0（永不过期）。
// 例：12-01 任意时刻发放、N=30 → 12-31 23:59:59。
func ExpireAtDays(now int64, days int) int64 {
	if days <= 0 {
		return 0
	}
	t := time.Unix(now, 0).In(pointsExpiryLoc)
	y, m, d := t.Date()
	return time.Date(y, m, d, 23, 59, 59, 0, pointsExpiryLoc).AddDate(0, 0, days).Unix()
}

// ExpireAtWeeks 获得所在周（周一~周日，北京时间）之后第 N 周的周日 23:59:59。
// weeks <= 0 → 0（永不过期）。
//
// N=1 时实际有效期 7~13 天（周日获得 7 天、周一获得 13 天），是「周清」语义，
// 配不出月级有效期——设置页文案必须写明这一点。
func ExpireAtWeeks(now int64, weeks int) int64 {
	if weeks <= 0 {
		return 0
	}
	t := time.Unix(now, 0).In(pointsExpiryLoc)
	y, m, d := t.Date()
	// 本周周一 00:00 所在日
	offsetFromMonday := (int(t.Weekday()) + 6) % 7 // 周一=0 … 周日=6
	monday := time.Date(y, m, d, 23, 59, 59, 0, pointsExpiryLoc).AddDate(0, 0, -offsetFromMonday)
	// 之后第 N 周的周日 = 本周周一 + (7N + 6) 天
	return monday.AddDate(0, 0, 7*weeks+6).Unix()
}

// ExpireAtDate 管理员「指定日期」：该日 23:59:59（北京时间）。日期早于今天报错。
func ExpireAtDate(date string) (int64, error) {
	date = trimSpaceASCII(date)
	parsed, err := time.ParseInLocation("2006-01-02", date, pointsExpiryLoc)
	if err != nil {
		return 0, fmt.Errorf("日期格式应为 YYYY-MM-DD: %s", date)
	}
	now := time.Now().In(pointsExpiryLoc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, pointsExpiryLoc)
	if parsed.Before(today) {
		return 0, errors.New("过期日期不能早于今天")
	}
	return time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 23, 59, 59, 0, pointsExpiryLoc).Unix(), nil
}

func trimSpaceASCII(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}

// ---------------------------------------------------------------------------
// 过期作废
// ---------------------------------------------------------------------------

// pointsLotExpiry 一次作废里按来源汇总的金额，供写使用日志。
type PointsLotExpiry struct {
	Source    string
	Forfeited int64
	ExpiresAt int64
}

// ExpireUserPointsLots 作废某用户所有已到期批次。
//
// 幂等性不依赖锁：每笔作废都是条件更新（WHERE status='active'），只有真正改到 1 行
// 的才累加作废额并记流水。即使锁实现有问题、或出现两个主节点，最坏后果是某次归属偏移
// （下次 sync 修正），不会双倍作废、不会把余额扣成负数。
func ExpireUserPointsLots(userId int, now int64) (forfeited int64, bySource []PointsLotExpiry, err error) {
	now = grantNow(now)
	err = DB.Transaction(func(tx *gorm.DB) error {
		// 先把到期前的消耗归属给到期批次，再算作废额。
		if err := syncPointsLotsTx(tx, userId, now); err != nil {
			return err
		}

		var due []PointsLot
		if err := tx.Where(
			"user_id = ? AND status = ? AND expires_at > 0 AND expires_at <= ?",
			userId, PointsLotStatusActive, now,
		).Order("expires_at, id").Find(&due).Error; err != nil {
			return err
		}

		perSource := map[string]*PointsLotExpiry{}
		for _, lot := range due {
			f := lot.Amount - lot.Attributed
			if f < 0 {
				f = 0
			}
			res := tx.Model(&PointsLot{}).
				Where("id = ? AND status = ?", lot.Id, PointsLotStatusActive).
				Updates(map[string]interface{}{
					"forfeited":  f,
					"status":     PointsLotStatusExpired,
					"updated_at": now,
				})
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected != 1 {
				// 已被另一个执行者作废（多主节点 / 重跑）。跳过，不重复扣减。
				continue
			}
			if f > 0 {
				if _, err := insertFundEntryTx(tx, &FundEntry{
					UserId:     userId,
					Account:    FundAccountPoints,
					Kind:       FundKindExpire,
					QuotaDelta: -f,
					CashFen:    0,
					Source:     lot.Source,
					RefType:    FundRefPointsLot,
					RefId:      fmt.Sprintf("%d", lot.Id),
					Remark:     fmt.Sprintf("批次到期作废，到期时间 %d", lot.ExpiresAt),
				}); err != nil {
					return err
				}
				if agg, ok := perSource[lot.Source]; ok {
					agg.Forfeited += f
				} else {
					perSource[lot.Source] = &PointsLotExpiry{
						Source: lot.Source, Forfeited: f, ExpiresAt: lot.ExpiresAt,
					}
				}
			}
			forfeited += f
		}

		if forfeited > 0 {
			// 锁内 B == Σactive剩余 ≥ 本次作废额，不会扣成负数。
			if err := tx.Model(&User{}).Where("id = ?", userId).
				Update("points_balance", gorm.Expr("points_balance - ?", forfeited)).Error; err != nil {
				return err
			}
		}
		if len(perSource) == 0 {
			return nil
		}
		for _, agg := range perSource {
			bySource = append(bySource, *agg)
		}
		sort.SliceStable(bySource, func(i, j int) bool { return bySource[i].Source < bySource[j].Source })
		return nil
	})
	if err != nil {
		return 0, nil, err
	}
	if forfeited > 0 {
		if cErr := invalidateUserCache(userId); cErr != nil {
			common.SysLog(fmt.Sprintf("failed to invalidate user cache after points expiry (user=%d): %s", userId, cErr.Error()))
		}
	}
	return forfeited, bySource, nil
}

// CleanupArchivedPointsLots 删除早已到期/用尽的批次行。
//
// 只删 expires_at > 0 的：永不过期的批次（legacy / surplus / 永不失效的管理员发放）
// 用完后也留着——数量少，且 legacy 承担「已初始化」标记，删了会让之后的 surplus
// 被误判成存量。
//
// 先 Pluck id 再按 id 批量删：这个 GORM 版本的 UPDATE/DELETE 不消费 Limit 子句
// （见 compute_point_lot.go 的同名说明），.Limit(n).Delete() 会在三个库上静默全表删。
func CleanupArchivedPointsLots(now int64, keepSeconds int64, batchLimit int) (int, error) {
	now = grantNow(now)
	if batchLimit <= 0 {
		batchLimit = 500
	}
	cutoff := now - keepSeconds
	var ids []int
	if err := DB.Model(&PointsLot{}).
		Where("status IN ? AND expires_at > 0 AND expires_at < ?",
			[]string{PointsLotStatusExpired, PointsLotStatusExhausted}, cutoff).
		Limit(batchLimit).Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	if err := DB.Where("id IN ?", ids).Delete(&PointsLot{}).Error; err != nil {
		return 0, err
	}
	return len(ids), nil
}

// PointsCheckinWeekKey 签到周批次的合并键：获得所在周的周一日期（北京时间）。
// 同一周内多次签到合并到同一笔批次，避免每人每年 365 行。
func PointsCheckinWeekKey(now int64) string {
	t := time.Unix(now, 0).In(pointsExpiryLoc)
	y, m, d := t.Date()
	offsetFromMonday := (int(t.Weekday()) + 6) % 7 // 周一=0 … 周日=6
	monday := time.Date(y, m, d, 0, 0, 0, 0, pointsExpiryLoc).AddDate(0, 0, -offsetFromMonday)
	return fmt.Sprintf("checkin-week:%s", monday.Format("2006-01-02"))
}
