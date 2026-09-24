package model

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

// 收入对账报表查询。设计见 docs/revenue-reconciliation-design.md §七。
//
// 三个维度：入账（fund_entries）、消耗（logs）、余额（users）。
// 记账铁律决定了营收口径：只有 CashFen > 0 的流水计入营收，即 prepay 与 ar_settle
// 两种 Kind；赠送与授信开额恒为 0，它们单独成栏但不计营收。

// FundInflowRow 入账侧的一行汇总（按 账户×性质×来源 分组）。
type FundInflowRow struct {
	Account    string `json:"account"`
	Kind       string `json:"kind"`
	Source     string `json:"source"`
	QuotaTotal int64  `json:"quota_total"`
	CashTotal  int64  `json:"cash_total"` // 分
	Count      int64  `json:"count"`
}

// FundConsumeStats 消耗侧汇总。取自 logs，按扣费来源拆分。
type FundConsumeStats struct {
	TotalQuota     int64 `json:"total_quota"`     // 本期消费总额（quota unit）
	PointsConsumed int64 `json:"points_consumed"` // 其中积分抵扣
	CreditConsumed int64 `json:"credit_consumed"` // 其中由授信承担（应收账款增加）
	CashConsumed   int64 `json:"cash_consumed"`   // 其中现金承担：逐条 max(消费 − 积分 − 授信, 0) 的净额
}

// FundBalanceStats 余额侧汇总（当前值，无时间维度）。
type FundBalanceStats struct {
	Cash          int64 `json:"cash"`           // 预收账款（负债）
	Points        int64 `json:"points"`         // 待核销的市场费用
	CreditUsed    int64 `json:"credit_used"`    // 应收账款
	CreditLimit   int64 `json:"credit_limit"`   // 授信总额
	CreditExposed int64 `json:"credit_exposed"` // 授信敞口 = limit - used
}

// FundSummary 报表总装。
type FundSummary struct {
	Start   int64             `json:"start"`
	End     int64             `json:"end"`
	Inflow  []FundInflowRow   `json:"inflow"`
	Consume FundConsumeStats  `json:"consume"`
	Balance FundBalanceStats  `json:"balance"`
	Revenue FundRevenueDigest `json:"revenue"`
}

// FundRevenueDigest 营收速览——报表顶部那个「真实入账」数字。
type FundRevenueDigest struct {
	CashFenTotal int64 `json:"cash_fen_total"` // 真实入账（分）= prepay + ar_settle
	GiftQuota    int64 `json:"gift_quota"`     // 赠送发放（不计营收）
	CreditGrant  int64 `json:"credit_grant"`   // 授信开额（不计营收）
	AdjustQuota  int64 `json:"adjust_quota"`   // 性质不明的调整，异常监控项
}

// GetFundSummary 汇总指定时间窗内的入账与消耗，并附当前余额快照。
//
// 余额是当前值而非期末值：users 表不存历史快照，要做期末余额得靠流水回溯，
// 成本远高于收益——日常对账看的是「现在欠多少、现在还剩多少」。
func GetFundSummary(start, end int64) (*FundSummary, error) {
	s := &FundSummary{Start: start, End: end}

	if err := DB.Model(&FundEntry{}).
		Where("created_at >= ? AND created_at <= ?", start, end).
		Select("account, kind, source, " +
			"COALESCE(SUM(quota_delta),0) as quota_total, " +
			"COALESCE(SUM(cash_fen),0) as cash_total, " +
			"COUNT(*) as count").
		Group("account, kind, source").
		Order("account, kind, source").
		Scan(&s.Inflow).Error; err != nil {
		return nil, err
	}

	for _, r := range s.Inflow {
		switch r.Kind {
		case FundKindPrepay, FundKindARSettle:
			s.Revenue.CashFenTotal += r.CashTotal
		case FundKindGift:
			s.Revenue.GiftQuota += r.QuotaTotal
		case FundKindCreditGrant:
			s.Revenue.CreditGrant += r.QuotaTotal
		case FundKindAdjust:
			s.Revenue.AdjustQuota += r.QuotaTotal
		}
	}

	consume, err := getFundConsumeStats(start, end)
	if err != nil {
		return nil, err
	}
	s.Consume = *consume

	balance, err := GetFundBalanceStats()
	if err != nil {
		return nil, err
	}
	s.Balance = *balance

	return s, nil
}

// getFundConsumeStats 从消费日志汇总本期**净**消耗。
//
// 必须把退款算成负消费：异步任务（MJ/视频/Suno）失败时 RefundTaskQuota 会把预扣额
// 退回 User.quota 并写一条 LogTypeRefund（Quota 为正数）。只统计 LogTypeConsume 的话，
// 消耗被高估、期望余额偏低，自洽校验会在每次任务失败后报「账不平」——那是正常业务
// 路径，不是流水被绕过，假告警会把真信号淹掉。成本对账（reconcile_compare.go）
// 对同一批日志也是按负消费处理的，口径一致。
//
// 走 LOG_DB 而非 DB：日志可能被配置到独立库（common.LogSqlDSN），查错库会得到 0。
func getFundConsumeStats(start, end int64) (*FundConsumeStats, error) {
	var row struct {
		TotalQuota     int64
		PointsConsumed int64
		CreditConsumed int64
		CashConsumed   int64
	}
	// CASE WHEN 而非两次查询：一次扫表拿净额，且三库通吃（SQLite 无 FILTER 语法）。
	//
	// 排除订阅扣费的日志：SubscriptionFunding 扣的是 UserSubscription.AmountUsed，
	// **不动 User.Quota**，而它的售卖收入记在 FundAccountSubscription 而非 cash 账户。
	// 不排除的话，现金校验会减去一笔从未减少过 users.quota 的消费，任何订阅客户产生
	// 用量都会触发假不平。
	//
	// 套餐权益同理：EntitlementFunding 扣的是次数与算力点批次，同样不动 User.Quota。
	// 不排除的话，任何走套餐权益的请求都会被算成现金消耗，现金校验必然假不平——
	// 与订阅那条是同一个问题的同一个形态。
	//
	// 用 other 的 JSON 子串匹配是权宜之计——Log 没有 billing_source 索引列，而它已经
	// 写在 Other 里（service/log_info_generate.go:164）。COALESCE 不可少：other 为
	// NULL 时 `NOT LIKE` 求值为 NULL，会把那些行一并排除掉。
	//
	// 排除项随资金来源增加而线性增长，已经是第二条了。再加第三条时应当把
	// billing_source 提成独立索引列，改成「只统计 wallet/points_wallet」的白名单，
	// 免得哪天新增来源漏改这里、对账静默报假不平。
	if err := LOG_DB.Model(&Log{}).
		Where("type IN ? AND created_at >= ? AND created_at <= ?",
			[]int{LogTypeConsume, LogTypeRefund}, start, end).
		Where("COALESCE(other, '') NOT LIKE ?", `%"billing_source":"subscription"%`).
		Where("COALESCE(other, '') NOT LIKE ?", `%"billing_source":"entitlement"%`).
		// 现金部分**按行**算、截到 0，不能用总额相减：积分抵扣会向上取整到整积分
		// （HybridFunding.roundUpToWholePoints，加速营销积分消耗），一笔 15000 的请求
		// 可能扣掉 15069 的积分。那多出的 69 出自积分账户、不是现金，相减会得到 −69，
		// 等于凭空给现金账户记了一笔入账——每次取整都让现金自洽多偏一点。
		// 取整只在积分已经覆盖整笔时才可能发生（积分不够时先被扣光，取整拿不到多余的），
		// 所以「积分 + 授信 > 消费额」只来自取整，按行截到 0 是精确的。
		Select(fmt.Sprintf(
			"COALESCE(SUM(CASE WHEN type = %[1]d THEN -quota ELSE quota END),0) as total_quota, "+
				"COALESCE(SUM(CASE WHEN type = %[1]d THEN -points_consumed ELSE points_consumed END),0) as points_consumed, "+
				"COALESCE(SUM(CASE WHEN type = %[1]d THEN -credit_consumed ELSE credit_consumed END),0) as credit_consumed, "+
				"COALESCE(SUM((CASE WHEN type = %[1]d THEN -1 ELSE 1 END) * "+
				"(CASE WHEN quota - points_consumed - credit_consumed > 0 THEN quota - points_consumed - credit_consumed ELSE 0 END)),0) as cash_consumed",
			LogTypeRefund)).
		Scan(&row).Error; err != nil {
		return nil, err
	}
	return &FundConsumeStats{
		TotalQuota:     row.TotalQuota,
		PointsConsumed: row.PointsConsumed,
		CreditConsumed: row.CreditConsumed,
		// 现金承担：每条日志 max(消费 − 积分 − 授信, 0) 的净额，见上面 SQL 的说明
		CashConsumed: row.CashConsumed,
	}, nil
}

// GetFundBalanceStats 当前余额快照。
func GetFundBalanceStats() (*FundBalanceStats, error) {
	var row struct {
		Cash        int64
		Points      int64
		CreditUsed  int64
		CreditLimit int64
	}
	// Unscoped：**包含软删用户**。这是为了和 ledger 口径对齐——用户被软删时
	// fund_entries 不会跟着消失，余额若排除软删用户，自洽校验就会凭空差出
	// 「该用户注销时的残留余额」，报出与流水被绕过无关的假不平。
	// 会计上也说得通：软删可恢复，那笔预收账款的负债并未消灭。
	if err := DB.Unscoped().Model(&User{}).
		Select("COALESCE(SUM(quota),0) as cash, " +
			"COALESCE(SUM(points_balance),0) as points, " +
			"COALESCE(SUM(credit_used),0) as credit_used, " +
			"COALESCE(SUM(credit_limit),0) as credit_limit").
		Scan(&row).Error; err != nil {
		return nil, err
	}
	return &FundBalanceStats{
		Cash:          row.Cash,
		Points:        row.Points,
		CreditUsed:    row.CreditUsed,
		CreditLimit:   row.CreditLimit,
		CreditExposed: row.CreditLimit - row.CreditUsed,
	}, nil
}

// ---------------------------------------------------------------------------
// 自洽校验
// ---------------------------------------------------------------------------

// FundCheckItem 一条校验结果。
type FundCheckItem struct {
	Name     string `json:"name"`
	Expected int64  `json:"expected"` // 按流水推算的余额
	Actual   int64  `json:"actual"`   // 账户实际余额
	Diff     int64  `json:"diff"`     // Actual - Expected
	OK       bool   `json:"ok"`
	Detail   string `json:"detail"`
}

// FundConsistencyReport 每日自洽校验。
type FundConsistencyReport struct {
	HasBaseline bool            `json:"has_baseline"`
	Items       []FundCheckItem `json:"items"`
	AllOK       bool            `json:"all_ok"`
}

// CheckFundConsistency 校验「流水推算的余额」是否等于「账户实际余额」。
//
// 不平几乎不是算错，而是**有代码绕过流水表直接改了余额**——这是发现此类 bug 最早的
// 信号，所以不平必须告警到人，不能只在页面上标红。
//
// 前提是存在期初余额基线（FundKindOpening）：流水表是后加的，没有基线时历史余额
// 无从解释，校验恒不平。未初始化时直接返回 HasBaseline=false，不产出假告警。
func CheckFundConsistency() (*FundConsistencyReport, error) {
	rep := &FundConsistencyReport{}

	var openingCount int64
	if err := DB.Model(&FundEntry{}).Where("kind = ?", FundKindOpening).Count(&openingCount).Error; err != nil {
		return nil, err
	}
	rep.HasBaseline = openingCount > 0
	if !rep.HasBaseline {
		return rep, nil
	}

	// 流水只算基线及之后的：基线前的流水效果已拍进期初快照，再求和就算了两遍。
	// 与下面消耗只取基线之后是同一条理由。用 id 而非时间戳切分——基线标记是建基线时
	// 第一条写入，同一秒内先操作、后建基线的流水按时间戳分不开，按 id 分得开。
	baselineId, err := fundBaselineStartId()
	if err != nil {
		return nil, err
	}

	// 各账户的流水净额（含期初基线）
	var ledger []struct {
		Account string
		Total   int64
	}
	if err := DB.Model(&FundEntry{}).Where("id >= ?", baselineId).
		Select("account, COALESCE(SUM(quota_delta),0) as total").
		Group("account").Scan(&ledger).Error; err != nil {
		return nil, err
	}
	ledgerBy := map[string]int64{}
	for _, l := range ledger {
		ledgerBy[l.Account] = l.Total
	}

	balance, err := GetFundBalanceStats()
	if err != nil {
		return nil, err
	}

	// 全量消耗（基线之后的所有消费）。基线时刻之前的消费已体现在期初余额里。
	baselineAt, err := fundBaselineTimestamp()
	if err != nil {
		return nil, err
	}
	consume, err := getFundConsumeStats(baselineAt, common.GetTimestamp())
	if err != nil {
		return nil, err
	}

	// 现金：期初 + 入账 - 现金消耗 == 当前 quota
	cashExpected := ledgerBy[FundAccountCash] - consume.CashConsumed
	rep.Items = append(rep.Items, buildCheckItem("现金", cashExpected, balance.Cash,
		"期初 + 入账 - 现金消耗"))

	// 赠送：期初 + 发放 - 积分消耗 == 当前 points_balance
	pointsExpected := ledgerBy[FundAccountPoints] - consume.PointsConsumed
	rep.Items = append(rep.Items, buildCheckItem("赠送积分", pointsExpected, balance.Points,
		"期初 + 发放 - 积分消耗"))

	// 信用：消耗累加、回款核销抵减，净额即当前应收。
	// ledgerBy[credit] 里 credit_grant 是开额（不构成欠款）、ar_settle 是回款（为负），
	// 故欠款 = 信用消耗 + 回款净额（后者为负，相当于减去已收回的部分）。
	if balance.CreditLimit > 0 || balance.CreditUsed != 0 {
		// 只取 ar_settle（回款，QuotaDelta 为负），不能用整个 credit 账户的净额——
		// credit_grant 是开额度，它既不构成欠款也不抵减欠款，混进来会让等式凭空偏移。
		settled, serr := sumFundEntryQuota(FundAccountCredit, FundKindARSettle, baselineId)
		if serr != nil {
			return nil, serr
		}
		// 期初欠款要算进来：建基线时已存在的 credit_used 不是本期消耗产生的。
		opening, oerr := sumFundEntryQuota(FundAccountCredit, FundKindOpening, baselineId)
		if oerr != nil {
			return nil, oerr
		}
		creditExpected := opening + consume.CreditConsumed + settled
		rep.Items = append(rep.Items, buildCheckItem("信用", creditExpected, balance.CreditUsed,
			"期初欠款 + 信用消耗 − 已核销回款"))
	}

	rep.AllOK = true
	for _, it := range rep.Items {
		if !it.OK {
			rep.AllOK = false
			break
		}
	}
	return rep, nil
}

func buildCheckItem(name string, expected, actual int64, detail string) FundCheckItem {
	return FundCheckItem{
		Name:     name,
		Expected: expected,
		Actual:   actual,
		Diff:     actual - expected,
		OK:       actual == expected,
		Detail:   detail,
	}
}

// fundBaselineTimestamp 取期初基线的时间点。
func fundBaselineTimestamp() (int64, error) {
	var at int64
	err := DB.Model(&FundEntry{}).Where("kind = ?", FundKindOpening).
		Select("COALESCE(MAX(created_at),0)").Scan(&at).Error
	return at, err
}

// fundBaselineStartId 基线的起点：最早一条期初流水（即基线标记）的 id。
func fundBaselineStartId() (int64, error) {
	var id int64
	err := DB.Model(&FundEntry{}).Where("kind = ?", FundKindOpening).
		Select("COALESCE(MIN(id),0)").Scan(&id).Error
	return id, err
}

// InitFundBaseline 把当前所有用户的余额记成期初流水，作为自洽校验的起点。
//
// 只能初始化一次：重复执行会把余额再记一遍，校验从此恒偏一倍。靠 (ref_type, ref_id)
// 唯一索引挡住——ref 取 userId，同一用户的期初只可能有一条。
//
// 分批处理，避免用户量大时一次性载入内存。
func InitFundBaseline(operatorId int) (created int, err error) {
	const batchSize = 500
	now := common.GetTimestamp()

	// 基线建立标记。余额为 0 的用户不写期初流水（没有意义），但若全站恰好无人持有
	// 余额——全新部署就是这样——基线就会是空集，HasBaseline 判定为 false，
	// 自洽校验从此永远不工作且没有任何提示。用一条 QuotaDelta=0 的标记兜住：
	// 它不参与任何等式，只证明「基线已建立」。
	if _, err = InsertFundEntry(&FundEntry{
		UserId: 0, CreatedAt: now,
		Account: FundAccountCash, Kind: FundKindOpening,
		QuotaDelta: 0, CashFen: 0,
		Source:  FundSourceAdminAdjust,
		RefType: FundRefUser, RefId: "opening-marker",
		OperatorId: operatorId, Remark: "对账基线建立标记",
	}); err != nil {
		return 0, err
	}

	var users []User
	// Unscoped：与 GetFundBalanceStats 一致地涵盖软删用户。若基线漏掉他们而余额统计
	// 算上了，初始化完成的那一刻就会差出「软删用户的残留余额」——一个开局就存在的假不平。
	err = DB.Unscoped().Model(&User{}).
		Select("id, quota, points_balance, credit_used").
		FindInBatches(&users, batchSize, func(tx *gorm.DB, batch int) error {
			for _, u := range users {
				if u.Quota != 0 {
					ok, e := InsertFundEntry(&FundEntry{
						UserId: u.Id, CreatedAt: now,
						Account: FundAccountCash, Kind: FundKindOpening,
						QuotaDelta: int64(u.Quota), CashFen: 0,
						Source:  FundSourceAdminAdjust,
						RefType: FundRefUser, RefId: fmt.Sprintf("opening-cash:%d", u.Id),
						OperatorId: operatorId, Remark: "期初余额基线",
					})
					if e != nil {
						return e
					}
					if ok {
						created++
					}
				}
				if u.CreditUsed != 0 {
					// 信用期初：建基线时已存在的欠款。不记的话信用校验
					// （信用消耗 − 回款 == credit_used）隐含假设期初欠款为 0，
					// 任何已有欠款的部署建完基线就永久报「信用账不平」。
					ok, e := InsertFundEntry(&FundEntry{
						UserId: u.Id, CreatedAt: now,
						Account: FundAccountCredit, Kind: FundKindOpening,
						QuotaDelta: u.CreditUsed, CashFen: 0,
						Source:  FundSourceAdminAdjust,
						RefType: FundRefUser, RefId: fmt.Sprintf("opening-credit:%d", u.Id),
						OperatorId: operatorId, Remark: "期初余额基线",
					})
					if e != nil {
						return e
					}
					if ok {
						created++
					}
				}
				if u.PointsBalance != 0 {
					ok, e := InsertFundEntry(&FundEntry{
						UserId: u.Id, CreatedAt: now,
						Account: FundAccountPoints, Kind: FundKindOpening,
						QuotaDelta: int64(u.PointsBalance), CashFen: 0,
						Source:  FundSourceAdminAdjust,
						RefType: FundRefUser, RefId: fmt.Sprintf("opening-points:%d", u.Id),
						OperatorId: operatorId, Remark: "期初余额基线",
					})
					if e != nil {
						return e
					}
					if ok {
						created++
					}
				}
			}
			return nil
		}).Error
	return created, err
}

// ---------------------------------------------------------------------------
// 流水明细
// ---------------------------------------------------------------------------

// FundEntryQuery 流水明细的筛选条件。零值表示不限。
type FundEntryQuery struct {
	Start      int64
	End        int64
	UserId     int
	Account    string
	Kind       string
	Source     string
	OperatorId int
}

// ListFundEntries 分页查询流水明细。
func ListFundEntries(q FundEntryQuery, offset, limit int) ([]*FundEntry, int64, error) {
	tx := DB.Model(&FundEntry{})
	if q.Start > 0 {
		tx = tx.Where("created_at >= ?", q.Start)
	}
	if q.End > 0 {
		tx = tx.Where("created_at <= ?", q.End)
	}
	if q.UserId > 0 {
		tx = tx.Where("user_id = ?", q.UserId)
	}
	if q.Account != "" {
		tx = tx.Where("account = ?", q.Account)
	}
	if q.Kind != "" {
		tx = tx.Where("kind = ?", q.Kind)
	}
	if q.Source != "" {
		tx = tx.Where("source = ?", q.Source)
	}
	if q.OperatorId > 0 {
		tx = tx.Where("operator_id = ?", q.OperatorId)
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []*FundEntry
	err := tx.Order("created_at desc, id desc").Offset(offset).Limit(limit).Find(&list).Error
	return list, total, err
}

// sumFundEntryQuota 汇总某账户某性质、id >= minId 的流水净额。
func sumFundEntryQuota(account, kind string, minId int64) (int64, error) {
	var total int64
	err := DB.Model(&FundEntry{}).
		Where("account = ? AND kind = ? AND id >= ?", account, kind, minId).
		Select("COALESCE(SUM(quota_delta),0)").Scan(&total).Error
	return total, err
}
