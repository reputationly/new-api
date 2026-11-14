package model

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 数据看板「分组消耗」的按小时预聚合。
//
// 为什么不直查 logs：这张表的查询成本 = 窗口内行数 × 每行成本，而窗口可以到 90 天。
// 线上实测（43 万行 / 90 天）带 other 两次 LIKE 的版本要 1.1s，30 天 0.39s；行数涨 100 倍
// 就是分钟级，且页数会超出 shared_buffers，实际更差。汇总表把「行数」从日志行数压成
// 「小时数 × 分组数」，读路径与业务量解耦。
//
// 与 quota_data 的区别（那一套踩过的两个坑这里都避开了）：
//   - 不挂在请求热路径上：由后台任务按小时重算，计费/写日志路径一行不改；
//   - 有唯一键 (group_name, hour_ts)，写入是「重算这一小时再绝对值覆盖」，不是 += 增量，
//     所以重跑、重试、多副本同时写都幂等（quota_data 的重复行竞态正是缺了这两点）。
const (
	// groupStatsBackfillDays 首次部署回填的历史深度，与看板 90 天窗口上限对齐。
	// 更早的历史不覆盖；需要时把这个常量调大重跑即可（写入幂等，重跑无副作用）。
	groupStatsBackfillDays = 90
	// groupStatsRollupInterval 汇总任务的运行间隔。
	groupStatsRollupInterval = 10 * time.Minute
	// groupStatsRerollHours 每次运行额外重算几个已完结小时，吸收多副本时钟偏差。
	groupStatsRerollHours = 2
	// groupStatsRollupChunkHours 单次写库覆盖的小时数上限，避免停机很久后一把扫太多。
	groupStatsRollupChunkHours = 24
	// logHourBucketExpr 小时分桶表达式，与 quota_data / 子账户看板同一口径。
	logHourBucketExpr = "(logs.created_at - logs.created_at % 3600)"
)

// GroupStatsHourly 一条「线路分组 × 小时」的消耗汇总。
type GroupStatsHourly struct {
	Id         int    `json:"id" gorm:"primaryKey"`
	GroupName  string `json:"group_name" gorm:"column:group_name;uniqueIndex:uk_group_stats_hour,priority:1;size:64;not null;default:''"`
	HourTs     int64  `json:"hour_ts" gorm:"column:hour_ts;uniqueIndex:uk_group_stats_hour,priority:2;not null"`
	ReqCount   int64  `json:"req_count" gorm:"column:req_count;not null;default:0"`
	Tokens     int64  `json:"tokens" gorm:"column:tokens;not null;default:0"`
	TotalQuota int64  `json:"total_quota" gorm:"column:total_quota;not null;default:0"`
	CashQuota  int64  `json:"cash_quota" gorm:"column:cash_quota;not null;default:0"`
	UpdatedAt  int64  `json:"updated_at" gorm:"column:updated_at;not null;default:0"`
}

func (GroupStatsHourly) TableName() string { return "group_stats_hourly" }

// GroupStatsMeta 汇总进度：只有 [FirstRolledHour, LastRolledHour] 这个闭区间内的小时是
// 已物化的。两端都要记：
//
//   - 记终点（而不是取 MAX(hour_ts)）：某段时间没有请求就不会有行写入，MAX 会停在旧值，
//     读路径会一直把整段区间退回 logs 现算（等于白建表）。
//   - 记起点：回填只覆盖「部署时刻往前 groupStatsBackfillDays 天」。更早的区间虽然能通过
//     「跨度 ≤ 90 天」的校验（那只限跨度、不限多早），但表里没有行——不把起点当成未汇总，
//     查历史月份会显示「无消耗记录」而 logs 里其实有数据，属于静默少报。
//
// 任一字段为 -1 表示「未知/不可信」，读路径退回 logs。
type GroupStatsMeta struct {
	Id              int   `json:"id" gorm:"primaryKey"`
	FirstRolledHour int64 `json:"first_rolled_hour" gorm:"column:first_rolled_hour;not null;default:-1"`
	LastRolledHour  int64 `json:"last_rolled_hour" gorm:"column:last_rolled_hour;not null;default:-1"`
	UpdatedAt       int64 `json:"updated_at" gorm:"column:updated_at;not null;default:0"`
}

func (GroupStatsMeta) TableName() string { return "group_stats_meta" }

// GroupQuotaStat 看板「分组消耗」的一行：一条线路分组在窗口内的汇总。
type GroupQuotaStat struct {
	Group      string
	Count      int64
	Tokens     int64
	TotalQuota int64
	CashQuota  int64
}

// ---------------------------------------------------------------------------
// 日志聚合（汇总任务与直查降级共用同一条 SQL，口径只有一份）
// ---------------------------------------------------------------------------

// aggregateLogsByGroupHour 按 (线路分组, 小时) 聚合消费与退款日志。
//
// 口径与设计文档一致：
//   - 调用次数只数消费行（type=2），退款行不算一次调用；
//   - tokens 只统计消费行：退款行里的 CompletionTokens 存的是「实际用量」而不是差额，
//     按类型取负会把它减成负数；
//   - 金额按类型取负净额化（退款行 quota 是正数）；
//   - 余额（现金）逐行 max(quota - 积分 - 授信, 0)：积分抵扣会向上取整，先总额相减会
//     算出负数现金；套餐/订阅的日志 quota 同样是正的，但它们不动钱包，必须从现金里剔除，
//     只能靠 other.billing_source 判断（这也是本表唯一需要读 other 的地方）。
func aggregateLogsByGroupHour(start, endExclusive int64, username string) ([]*GroupStatsHourly, error) {
	if endExclusive <= start {
		return nil, nil
	}
	var rows []*GroupStatsHourly
	if err := groupStatsLogQuery(LOG_DB, start, endExclusive, username).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// groupStatsLogQuery 构造按 (线路分组, 小时) 聚合的日志查询。
//
// 单独抽出来是为了让方言测试能拿到未执行的 SQL（见 group_stats_hourly_test.go 的
// TestGroupStatsLogQuery_DialectQuoting）：`group` 是保留字，三种库的引用方式不同，
// 而本地只有 SQLite 可跑，PG/MySQL 的引号与占位符只能靠生成结果来兜底验证。
func groupStatsLogQuery(db *gorm.DB, start, endExclusive int64, username string) *gorm.DB {
	cashExpr := "COALESCE(SUM((CASE WHEN logs.type = ? THEN 1 ELSE -1 END) * " +
		"CASE WHEN COALESCE(logs.other, '') NOT LIKE ? AND COALESCE(logs.other, '') NOT LIKE ? " +
		"THEN CASE WHEN logs.quota - COALESCE(logs.points_consumed, 0) - COALESCE(logs.credit_consumed, 0) > 0 " +
		"THEN logs.quota - COALESCE(logs.points_consumed, 0) - COALESCE(logs.credit_consumed, 0) ELSE 0 END " +
		"ELSE 0 END), 0) as cash_quota"
	selectSQL := "COALESCE(" + logGroupCol + ", '') as group_name, " +
		logHourBucketExpr + " as hour_ts, " +
		"COUNT(CASE WHEN logs.type = ? THEN 1 END) as req_count, " +
		"COALESCE(SUM(CASE WHEN logs.type = ? THEN logs.prompt_tokens + logs.completion_tokens ELSE 0 END), 0) as tokens, " +
		"COALESCE(SUM(CASE WHEN logs.type = ? THEN logs.quota ELSE -logs.quota END), 0) as total_quota, " +
		cashExpr

	tx := db.Table("logs").
		Select(selectSQL,
			LogTypeConsume,
			LogTypeConsume,
			LogTypeConsume,
			LogTypeConsume,
			logOtherSubscriptionPattern,
			logOtherEntitlementPattern).
		Where("logs.type IN ?", []int{LogTypeConsume, LogTypeRefund}).
		Where("logs.created_at >= ? AND logs.created_at < ?", start, endExclusive).
		// GROUP BY 必须与 SELECT 用同一个 COALESCE 表达式：裸的 group 会把 NULL 和 ''
		// 拆成两行、都叫 ''，PG 的 upsert 会直接报「cannot affect row a second time」，
		// MySQL/SQLite 则是后一行覆盖前一行（少报）。
		// group 是保留字：logGroupCol 已按方言预引用；带逗号的 Group 参数 GORM 会原样输出。
		Group("COALESCE(" + logGroupCol + ", ''), " + logHourBucketExpr)
	if username != "" {
		tx = tx.Where("logs.username = ?", username)
	}
	return tx
}

// ---------------------------------------------------------------------------
// 汇总任务
// ---------------------------------------------------------------------------

var groupStatsRollupMu sync.Mutex

// StartGroupStatsRollupTask 启动小时汇总任务（仅主节点）。
//
// 只在主节点跑：多副本各跑一遍虽然因为写入幂等不会写坏数据，但等于把同一小时的日志
// 扫 N 遍。主节点切换时新主节点会从 meta 记录的进度继续，不会漏。
func StartGroupStatsRollupTask() {
	if !common.IsMasterNode {
		return
	}
	go func() {
		groupStatsRollupOnce()
		ticker := time.NewTicker(groupStatsRollupInterval)
		defer ticker.Stop()
		for range ticker.C {
			groupStatsRollupOnce()
		}
	}()
}

// RollGroupStatsOnce 供测试与运维手动触发一次汇总。
func RollGroupStatsOnce() { groupStatsRollupOnce() }

func groupStatsRollupOnce() {
	if !groupStatsRollupMu.TryLock() {
		return
	}
	defer groupStatsRollupMu.Unlock()

	now := common.GetTimestamp()
	currentHour := now - now%3600
	lastCompleted := currentHour - 3600
	if lastCompleted <= 0 {
		return
	}

	firstRolled, lastRolled, err := getGroupStatsMeta()
	if err != nil {
		common.SysError("读取分组消耗汇总进度失败: " + err.Error())
		return
	}

	from := lastRolled + 3600
	if lastRolled < 0 || firstRolled < 0 {
		// 首次部署（或进度只写了一端、不可信时）：回填最近 groupStatsBackfillDays 天。
		from = currentHour - int64(groupStatsBackfillDays)*86400
	}
	// 顺带重算最近几个已完结小时，吸收多副本时钟偏差导致的迟到写入。
	if rerollFrom := lastCompleted - int64(groupStatsRerollHours-1)*3600; from > rerollFrom {
		from = rerollFrom
	}
	if from > lastCompleted {
		return
	}

	// 稳态下每次只重算 2 个小时，不值得刷日志；回填/停机补洞时才打一行进度。
	isCatchUp := (lastCompleted-from)/3600+1 > int64(groupStatsRerollHours)+1
	total := 0
	for start := from; start <= lastCompleted; start += groupStatsRollupChunkHours * 3600 {
		end := start + groupStatsRollupChunkHours*3600
		if end > lastCompleted+3600 {
			end = lastCompleted + 3600
		}
		n, err := rollGroupStatsRange(start, end)
		if err != nil {
			common.SysError("分组消耗汇总写入失败: " + err.Error())
			return
		}
		total += n
		// 两端都只往「更覆盖」的方向扩展：终点前进、起点后退；重算历史小时不会把终点拉回去。
		chunkFirst, chunkLast := start, end-3600
		newFirst, newLast := firstRolled, lastRolled
		if newFirst < 0 || chunkFirst < newFirst {
			newFirst = chunkFirst
		}
		if chunkLast > newLast {
			newLast = chunkLast
		}
		if newFirst != firstRolled || newLast != lastRolled {
			if err := saveGroupStatsMeta(newFirst, newLast); err != nil {
				common.SysError("保存分组消耗汇总进度失败: " + err.Error())
				return
			}
			firstRolled, lastRolled = newFirst, newLast
		}
	}
	if isCatchUp {
		common.SysLog(fmt.Sprintf("分组消耗汇总: 回填至 %s，本次写入 %d 行",
			time.Unix(lastRolled, 0).Format("2006-01-02 15:04"), total))
	}
}

// rollGroupStatsRange 重算 [startHour, endHourExclusive) 内的小时并绝对值覆盖。
func rollGroupStatsRange(startHour, endHourExclusive int64) (int, error) {
	rows, err := aggregateLogsByGroupHour(startHour, endHourExclusive, "")
	if err != nil || len(rows) == 0 {
		return 0, err
	}
	now := common.GetTimestamp()
	for _, row := range rows {
		row.UpdatedAt = now
	}
	err = DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "group_name"},
			{Name: "hour_ts"},
		},
		DoUpdates: clause.AssignmentColumns([]string{"req_count", "tokens", "total_quota", "cash_quota", "updated_at"}),
	}).CreateInBatches(rows, 200).Error
	if err != nil {
		return 0, err
	}
	return len(rows), nil
}

// getGroupStatsMeta 读汇总进度（起点、终点）；没有记录时两端都返回 -1。
//
// 用 Find 而不是 First：首次部署的头十几分钟里这条记录不存在，First 会把
// ErrRecordNotFound 当错误打进日志，读接口每来一次就刷一条噪音。
func getGroupStatsMeta() (int64, int64, error) {
	var meta GroupStatsMeta
	if err := DB.Where("id = ?", 1).Limit(1).Find(&meta).Error; err != nil {
		return -1, -1, err
	}
	if meta.Id == 0 {
		return -1, -1, nil
	}
	return meta.FirstRolledHour, meta.LastRolledHour, nil
}

func saveGroupStatsMeta(firstHour, lastHour int64) error {
	meta := &GroupStatsMeta{
		Id:              1,
		FirstRolledHour: firstHour,
		LastRolledHour:  lastHour,
		UpdatedAt:       common.GetTimestamp(),
	}
	return DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"first_rolled_hour", "last_rolled_hour", "updated_at",
		}),
	}).Create(meta).Error
}

// ---------------------------------------------------------------------------
// 读路径
// ---------------------------------------------------------------------------

// GetGroupQuotaStats 汇总 [start, end]（含两端）内按线路分组的消耗。
//
// 读路径把区间拆成三段：头部零头小时（logs）、整小时（汇总表）、尾部零头与未汇总的小时
// （logs）。正常情况下只有「当前这一小时」会落到 logs 上，成本与窗口长度无关。
//
// 未汇总的区间一律走 logs 而不是返回汇总表的残缺结果：定时任务停机、首次回填未完成时，
// 宁可慢也不能少报——一张会静默少报的看板比没有看板更糟。
//
// username 非空时整段走 logs：汇总表键里没有用户维度（加上会把行数放大到「用户数 × 小时数」），
// 而单用户查询被 username 索引收窄，90 天也撑得住，且属于低频操作。
func GetGroupQuotaStats(start, end int64, username string) ([]*GroupQuotaStat, error) {
	endExclusive := end + 1
	if endExclusive <= start {
		return []*GroupQuotaStat{}, nil
	}
	stats := make(map[string]*GroupQuotaStat)

	if username != "" {
		rows, err := aggregateLogsByGroupHour(start, endExclusive, username)
		if err != nil {
			return nil, err
		}
		mergeGroupStats(stats, rows)
		return sortedGroupStats(stats), nil
	}

	firstRolled, lastRolled, err := getGroupStatsMeta()
	if err != nil {
		return nil, err
	}
	// 已物化的是 [firstRolled, lastRolled] 这个**闭区间**（两端都是整点时间戳），
	// 换算成半开区间 [coveredStart, coveredEnd)。任一为 -1 表示进度不可信，整段退回 logs。
	//
	// 这里曾经把整点时间戳又乘了一次 3600（rolledEnd = (lastRolled+1)*3600），
	// 算出个天文数字，于是 tableEnd 永远等于 floor(endExclusive)、所有整小时都去查汇总表，
	// 未汇总的小时被静默漏掉——「宁可慢、不少报」在这条路径上其实没成立。
	coveredStart, coveredEnd := int64(0), int64(0)
	if firstRolled >= 0 && lastRolled >= firstRolled {
		coveredStart = firstRolled
		coveredEnd = lastRolled + 3600
	}

	tableStart := ceilToHour(start)
	if tableStart < coveredStart {
		tableStart = coveredStart
	}
	tableEnd := floorToHour(endExclusive)
	if tableEnd > coveredEnd {
		tableEnd = coveredEnd
	}

	if tableStart < tableEnd {
		rows, err := queryGroupStatsHours(tableStart, tableEnd)
		if err != nil {
			return nil, err
		}
		mergeGroupStats(stats, rows)
		if start < tableStart {
			if err := mergeLogsRange(stats, start, tableStart, username); err != nil {
				return nil, err
			}
		}
		if tableEnd < endExclusive {
			if err := mergeLogsRange(stats, tableEnd, endExclusive, username); err != nil {
				return nil, err
			}
		}
	} else if err := mergeLogsRange(stats, start, endExclusive, username); err != nil {
		return nil, err
	}

	return sortedGroupStats(stats), nil
}

func mergeLogsRange(stats map[string]*GroupQuotaStat, start, endExclusive int64, username string) error {
	if endExclusive <= start {
		return nil
	}
	rows, err := aggregateLogsByGroupHour(start, endExclusive, username)
	if err != nil {
		return err
	}
	mergeGroupStats(stats, rows)
	return nil
}

// queryGroupStatsHours 从汇总表读整小时区间。
func queryGroupStatsHours(startHour, endHourExclusive int64) ([]*GroupStatsHourly, error) {
	var rows []*GroupStatsHourly
	err := DB.Table(GroupStatsHourly{}.TableName()).
		Select("group_name, COALESCE(SUM(req_count), 0) as req_count, "+
			"COALESCE(SUM(tokens), 0) as tokens, "+
			"COALESCE(SUM(total_quota), 0) as total_quota, "+
			"COALESCE(SUM(cash_quota), 0) as cash_quota").
		Where("hour_ts >= ? AND hour_ts < ?", startHour, endHourExclusive).
		Group("group_name").
		Scan(&rows).Error
	return rows, err
}

func mergeGroupStats(dst map[string]*GroupQuotaStat, rows []*GroupStatsHourly) {
	for _, row := range rows {
		if row == nil {
			continue
		}
		item := dst[row.GroupName]
		if item == nil {
			item = &GroupQuotaStat{Group: row.GroupName}
			dst[row.GroupName] = item
		}
		item.Count += row.ReqCount
		item.Tokens += row.Tokens
		item.TotalQuota += row.TotalQuota
		item.CashQuota += row.CashQuota
	}
}

func sortedGroupStats(stats map[string]*GroupQuotaStat) []*GroupQuotaStat {
	rows := make([]*GroupQuotaStat, 0, len(stats))
	for _, item := range stats {
		rows = append(rows, item)
	}
	// 金额降序；同额按分组名升序，保证多次刷新顺序稳定。
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].TotalQuota != rows[j].TotalQuota {
			return rows[i].TotalQuota > rows[j].TotalQuota
		}
		return rows[i].Group < rows[j].Group
	})
	return rows
}

func ceilToHour(ts int64) int64 {
	if rem := ts % 3600; rem != 0 {
		return ts - rem + 3600
	}
	return ts
}

func floorToHour(ts int64) int64 {
	return ts - ts%3600
}
