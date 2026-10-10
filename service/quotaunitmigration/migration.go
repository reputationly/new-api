package quotaunitmigration

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 额度单位迁移：QuotaPerUnit 500000 → 7300000，于是 1 quota 从 ¥0.0000146 变成 ¥0.000001，
// 计费过程合计（前端公式，6 位小数人民币）与新日志的花费列逐位一致。
//
// 入口、状态机、列类型预检、小表单事务、logs 分批、dry-run / apply 报告都在这里。
// 迁移程序不会自己挑时机执行——生产由运维在停服窗口内显式跑 apply，且只在该模式下
// 动作（见 main.go 的分支）。
const (
	ModeDryRun = "dry-run"
	ModeApply  = "apply"

	stateOptionKey = "QuotaUnitMigration"
	stateLogs      = "logs"
	stateDone      = "done"

	baseQuotaPerUnit   = 500000.0
	targetQuotaPerUnit = 7300000.0

	// scale 旧 quota → 新 quota 的倍数：1 quota 的人民币价值缩小 14.6 倍，
	// 于是同额人民币对应的 quota 数放大 14.6 倍。
	scale = targetQuotaPerUnit / baseQuotaPerUnit
)

const (
	optionExchangeRateFallback = 7.3
	// logBatchSize logs 阶段每批推进的 id 跨度（§M1.4）。
	logBatchSize = 5000
	// maxListedIDs 报告里最多列出多少个「无法解析」的行 id。
	maxListedIDs = 50
	// jsonChunkSize JSON 列每块读多少行（keyset 分页，控制内存）。
	jsonChunkSize = 500
)

// testHookAfterLogBatch 仅供测试注入「第 N 批后中断」，生产恒为 nil。
var testHookAfterLogBatch func(cursor int64) error

// migrationState 存在 options.QuotaUnitMigration 里的迁移进度（§M1.2）。
type migrationState struct {
	State      string `json:"state"`
	LogsCursor int64  `json:"logs_cursor"`
	LogsCutoff int64  `json:"logs_cutoff"`
	StartedAt  int64  `json:"started_at"`
	FinishedAt int64  `json:"finished_at"`
}

// Run 执行迁移。mode 必须是 dry-run 或 apply。
//
//	状态 key 不存在 且 QuotaPerUnit = 500000  → 小表阶段 + 日志阶段
//	状态 key 不存在 且 QuotaPerUnit ≠ 500000  → 拒绝执行（库已是新单位或被人手工改过）
//	state = logs                              → 从 logs_cursor 续跑
//	state = done                              → 已完成，退出码 0，不改任何数据
func Run(mode string, out io.Writer) error {
	if mode != ModeDryRun && mode != ModeApply {
		return fmt.Errorf("未知的迁移模式 %q：只支持 dry-run / apply", mode)
	}
	dryRun := mode == ModeDryRun

	rate, _, err := loadOptionFloat("USDExchangeRate", optionExchangeRateFallback)
	if err != nil {
		return err
	}
	rep := newReport(out, dryRun, rate)
	rep.line("=== 额度单位迁移（%s）===", mode)
	rep.line("时间: %s", time.Now().Format("2006-01-02 15:04:05"))
	rep.line("QuotaPerUnit: %.0f → %.0f（于是 1 quota = ¥%.6f）",
		baseQuotaPerUnit, targetQuotaPerUnit, rate/targetQuotaPerUnit)
	rep.line("汇率（只用于报告里的人民币对照）: %v", rate)
	if model.LOG_DB == model.DB {
		rep.line("日志库: 与主库同库")
	} else {
		rep.line("日志库: 独立（LOG_SQL_DSN），事务边界按库分别处理，游标仍存主库 options")
	}

	state, hasState, err := loadState()
	if err != nil {
		return err
	}
	if hasState && state.State == stateDone {
		rep.blank()
		rep.line("迁移已完成（options.%s.state = done），不做任何改动。", stateOptionKey)
		return rep.flush()
	}

	// M1.0：dry-run 与 apply 开始前都要检查列类型与溢出。
	if err := precheckColumnTypes(rep); err != nil {
		rep.line("预检未通过：%v", err)
		_ = rep.flush()
		return err
	}

	qpu, _, err := loadOptionFloat("QuotaPerUnit", baseQuotaPerUnit)
	if err != nil {
		return err
	}
	if !hasState && qpu != baseQuotaPerUnit {
		return fmt.Errorf("options.QuotaPerUnit = %v（不是 %v）且没有迁移状态，拒绝执行："+
			"这个库要么已经是新单位，要么被人手工改过，无法判断要不要缩放", qpu, baseQuotaPerUnit)
	}

	needSmallStage := !hasState
	startedAt := time.Now()

	// ---- 阶段一：要么落地，要么只扫描；报告统一到最后打印，两种模式格式一致 ----
	var (
		smallColumns   []columnStat
		logColumns     []columnStat
		jsonRows       []jsonStatRow
		optionRows     []optionRow
		logStage       logStageSummary
		smallStageTime time.Duration
	)

	if needSmallStage {
		// 迁移前合计必须在写之前采集。
		var err error
		if smallColumns, err = collectColumnStats(model.DB, smallNumericScopes); err != nil {
			return err
		}
		if optionRows, err = collectOptionRows(); err != nil {
			return err
		}
	}

	if dryRun {
		var err error
		if logColumns, err = collectColumnStats(model.LOG_DB, logsScopes()); err != nil {
			return err
		}
		if needSmallStage {
			if jsonRows, err = scanJSONRows(model.DB, smallJSONScopes); err != nil {
				return err
			}
		}
		var logJSON []jsonStatRow
		if logJSON, err = scanJSONRows(model.LOG_DB, []jsonScope{logsJSONScope}); err != nil {
			return err
		}
		jsonRows = append(jsonRows, logJSON...)
		if err = fillAfter(model.DB, smallColumns, true); err != nil {
			return err
		}
		if err = fillAfter(model.LOG_DB, logColumns, true); err != nil {
			return err
		}
		if state.LogsCursor > 0 {
			// 续跑：id ≤ cursor 的行上一轮已迁移，「迁移前合计」不可得；
			// 预计值也是按「全表都没迁过」算的，只能当量级参考。
			for i := range logColumns {
				logColumns[i].beforeKnown = false
			}
		}
		// 首次 dry-run 时状态 key 还不存在，分批计划要按库里实际的 logs 最大 id 报，
		// 否则「预计批次数」会永远是 0（那正是运维核对量级的依据）。
		planFrom, planCutoff := state.LogsCursor, state.LogsCutoff
		if !hasState {
			planFrom = 0
			if planCutoff, err = maxLogID(); err != nil {
				return err
			}
		}
		logStage = logStageSummary{
			planOnly: true,
			batches:  batchCount(planFrom, planCutoff),
			from:     planFrom,
			cutoff:   planCutoff,
		}
	} else {
		if needSmallStage {
			var err error
			if jsonRows, err = applySmallStage(); err != nil {
				return err
			}
			smallStageTime = time.Since(startedAt)
			if err = fillAfter(model.DB, smallColumns, false); err != nil {
				return err
			}
			if state, hasState, err = loadState(); err != nil {
				return err
			}
			if !hasState || state.State != stateLogs {
				return errors.New("小表阶段结束后没读到迁移状态，事务可能没提交")
			}
		}
		// 「迁移前合计」必须在分批改写**之前**采集，否则报告里前后两列会一模一样。
		// 断点续跑时 id ≤ cursor 的行上一轮已经迁过，真实前值不可得 —— 报告里标注。
		logsBeforeKnown := state.LogsCursor == 0
		if logsBeforeKnown {
			if logColumns, err = collectColumnStats(model.LOG_DB, logsScopes()); err != nil {
				return err
			}
		}

		summary, err := runLogStage(state)
		if err != nil {
			return err
		}
		logStage = summary
		jsonRows = append(jsonRows, jsonStatRow{scope: logsJSONScope, stat: summary.otherStat})

		if !logsBeforeKnown {
			// 续跑：前值不可得，只采集迁移后的实际值。
			if logColumns, err = collectColumnStats(model.LOG_DB, logsScopes()); err != nil {
				return err
			}
		}
		if err = fillAfter(model.LOG_DB, logColumns, false); err != nil {
			return err
		}
		if !logsBeforeKnown {
			for i := range logColumns {
				logColumns[i].beforeKnown = false
			}
		}
	}

	// ---- 阶段二：报告 ----
	rep.blank()
	if needSmallStage {
		rep.line("[数值列] 小表阶段（单事务，%d 表 %d 列）%s",
			len(smallNumericScopes), len(smallColumns), stageElapsed(dryRun, smallStageTime))
		writeColumnStats(rep, smallColumns, dryRun)
	} else {
		rep.line("[数值列] 小表阶段已在本轮之前完成，跳过。")
	}
	rep.blank()
	rep.line("[数值列] logs（分批，%d 列）", len(logColumns))
	writeColumnStats(rep, logColumns, dryRun)
	if logStage.from > 0 {
		rep.line("注意：断点续跑（cursor = %d，id ≤ %d 的行在上一轮已迁移）：「迁移前合计」不可得（标为 —）；"+
			"「迁移后合计」%s。", logStage.from, logStage.from,
			logsAfterColumnMeaning(dryRun))
	}

	rep.blank()
	writeJSONStats(rep, jsonRows)
	if path, err := unparsableIDFilePath(); err != nil {
		rep.line("（无法写完整 id 清单文件：%v）", err)
	} else if err := writeUnparsableIDFile(path, jsonRows); err != nil {
		rep.line("（无法写完整 id 清单文件 %s：%v）", path, err)
	} else {
		rep.line("完整 id 清单已写入 %s", path)
	}

	rep.blank()
	if needSmallStage {
		writeOptionRows(rep, optionRows)
	} else {
		rep.line("[options] 小表阶段已在本轮之前完成，跳过。")
	}

	rep.blank()
	if dryRun {
		rep.line("[logs] 分批计划：id (%d, %d]，预计 %d 批，每批 %d 行",
			logStage.from, logStage.cutoff, logStage.batches, logBatchSize)
		rep.blank()
		rep.line("dry-run 结束：未写任何数据。执行 --migrate-quota-unit=apply 才会落地。")
		return rep.flush()
	}

	rep.line("[logs] id (%d, %d] 共 %d 批，改写 other %d 行，耗时 %s",
		logStage.from, logStage.cutoff, logStage.batches, logStage.otherStat.rewritten,
		logStage.elapsed.Round(time.Millisecond))
	if err := finishState(); err != nil {
		return err
	}
	rep.blank()
	rep.line("apply 结束：options.%s.state = done。", stateOptionKey)
	return rep.flush()
}

// logsAfterColumnMeaning 说明日志「迁移后合计」这一列的来源，避免续跑时被误读。
func logsAfterColumnMeaning(dryRun bool) string {
	if dryRun {
		return "是按「全表未迁移」算出的预计值，仅供量级参考"
	}
	return "是库里当前的实际值"
}

func stageElapsed(dryRun bool, d time.Duration) string {
	if dryRun {
		return ""
	}
	return "耗时 " + d.Round(time.Millisecond).String()
}

// logStageSummary 日志阶段的结果，供报告使用。
type logStageSummary struct {
	planOnly  bool
	from      int64
	cutoff    int64
	batches   int64
	elapsed   time.Duration
	otherStat jsonStat
}

// ---------------------------------------------------------------------------
// options
// ---------------------------------------------------------------------------

// loadOption 读取 options 表的单个 key，缺失返回 found=false。
//
// 用 Find 而不是 First/Take：后者在缺行时返回 ErrRecordNotFound，GORM 会把它按 ERROR
// 级别打进日志——迁移报告是给运维读的，满屏 "record not found" 会淹掉真正的内容。
func loadOption(db *gorm.DB, key string) (string, bool, error) {
	var options []model.Option
	if err := db.Where(model.Option{Key: key}).Limit(1).Find(&options).Error; err != nil {
		return "", false, err
	}
	if len(options) == 0 {
		return "", false, nil
	}
	return options[0].Value, true, nil
}

func loadOptionFloat(key string, fallback float64) (float64, bool, error) {
	raw, found, err := loadOption(model.DB, key)
	if err != nil || !found {
		return fallback, false, err
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return 0, true, fmt.Errorf("options.%s 不是数字: %q", key, raw)
	}
	return v, true, nil
}

func loadState() (migrationState, bool, error) {
	raw, found, err := loadOption(model.DB, stateOptionKey)
	if err != nil || !found {
		return migrationState{}, false, err
	}
	var state migrationState
	if err := common.UnmarshalJsonStr(raw, &state); err != nil {
		return migrationState{}, false, fmt.Errorf("options.%s 不是合法 JSON: %w", stateOptionKey, err)
	}
	return state, true, nil
}

func saveState(db *gorm.DB, state migrationState) error {
	raw, err := common.Marshal(state)
	if err != nil {
		return err
	}
	return upsertOption(db, stateOptionKey, string(raw))
}

func upsertOption(db *gorm.DB, key, value string) error {
	// options.key 是保留字，交给 GORM 按方言加引号（CLAUDE.md Rule 2）。
	return db.Clauses(clause.OnConflict{UpdateAll: true}).
		Create(&model.Option{Key: key, Value: value}).Error
}

type optionRow struct {
	spec    optionSpec
	old     string
	found   bool
	target  string
	changed bool
}

func collectOptionRows() ([]optionRow, error) {
	rows := make([]optionRow, 0, len(optionSpecs))
	for _, spec := range optionSpecs {
		raw, found, err := loadOption(model.DB, spec.key)
		if err != nil {
			return nil, err
		}
		old, target, changed, err := optionChange(spec, raw, found)
		if err != nil {
			return nil, err
		}
		rows = append(rows, optionRow{spec: spec, old: old, found: found, target: target, changed: changed})
	}
	return rows, nil
}

// optionChange 计算一个 key 的目标值。changed = false 表示库里已是目标值，
// 或缺失且不在「缺失时写入」清单里。
func optionChange(spec optionSpec, raw string, found bool) (string, string, bool, error) {
	if !found {
		if spec.missingValue == "" {
			return "", "", false, nil
		}
		return "", spec.missingValue, true, nil
	}
	target := spec.fixedValue
	if spec.mode != optionFixed {
		v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil {
			return raw, "", false, fmt.Errorf("options.%s 不是数字: %q", spec.key, raw)
		}
		target = strconv.FormatInt(scaleValue(v), 10)
	}
	if strings.TrimSpace(raw) == target {
		return raw, raw, false, nil
	}
	return raw, target, true, nil
}

func finishState() error {
	state, found, err := loadState()
	if err != nil {
		return err
	}
	if !found {
		return errors.New("收尾时找不到迁移状态")
	}
	state.State = stateDone
	state.FinishedAt = common.GetTimestamp()
	return saveState(model.DB, state)
}

// ---------------------------------------------------------------------------
// 小表阶段
// ---------------------------------------------------------------------------

// applySmallStage 在**一个事务**内完成 M1.5（除 logs）、M1.6（tasks/users）、M1.7 与
// 状态写入（§M1.3），任一步失败整体回滚。返回 JSON 列的改写统计。
func applySmallStage() ([]jsonStatRow, error) {
	cutoff, err := maxLogID()
	if err != nil {
		return nil, err
	}
	now := common.GetTimestamp()
	jsonRows := make([]jsonStatRow, 0, len(smallJSONScopes))

	err = model.DB.Transaction(func(tx *gorm.DB) error {
		for _, scope := range smallNumericScopes {
			if err := execScaleUpdate(tx, scope.table, scope.columns, scope.where); err != nil {
				return err
			}
		}
		for _, scope := range smallJSONScopes {
			stat, err := rewriteJSONRows(tx, scope, "", true)
			if err != nil {
				return err
			}
			jsonRows = append(jsonRows, jsonStatRow{scope: scope, stat: stat})
		}
		for _, spec := range optionSpecs {
			raw, found, err := loadOption(tx, spec.key)
			if err != nil {
				return err
			}
			_, target, changed, err := optionChange(spec, raw, found)
			if err != nil {
				return err
			}
			if !changed {
				continue
			}
			if err := upsertOption(tx, spec.key, target); err != nil {
				return err
			}
		}
		return saveState(tx, migrationState{
			State:      stateLogs,
			LogsCursor: 0,
			LogsCutoff: cutoff,
			StartedAt:  now,
		})
	})
	if err != nil {
		return nil, err
	}
	return jsonRows, nil
}

func logsScopes() []numericScope {
	return []numericScope{{table: "logs", columns: logsNumericColumns}}
}

func execScaleUpdate(db *gorm.DB, table string, columns []string, where string) error {
	if len(columns) == 0 {
		return nil
	}
	sets := make([]string, 0, len(columns))
	for _, column := range columns {
		// n × 14.6 的小数部分只可能是 0/0.2/0.4/0.6/0.8，永远不落在 .5 上，
		// 所以三库的 ROUND 舍入规则差异不影响结果（§M1.4）。
		sets = append(sets, fmt.Sprintf("%s = ROUND(%s * %v)", column, column, scale))
	}
	sql := fmt.Sprintf("UPDATE %s SET %s", table, strings.Join(sets, ", "))
	if where != "" {
		sql += " WHERE " + where
	}
	return db.Exec(sql).Error
}

// ---------------------------------------------------------------------------
// JSON 列
// ---------------------------------------------------------------------------

type jsonStatRow struct {
	scope jsonScope
	stat  jsonStat
}

func scanJSONRows(db *gorm.DB, scopes []jsonScope) ([]jsonStatRow, error) {
	rows := make([]jsonStatRow, 0, len(scopes))
	for _, scope := range scopes {
		stat, err := rewriteJSONRows(db, scope, "", false)
		if err != nil {
			return nil, err
		}
		rows = append(rows, jsonStatRow{scope: scope, stat: stat})
	}
	return rows, nil
}

// rewriteJSONRows 改写一处 JSON 文本列里命中清单的键。apply=false 时只统计不写。
//
// 按 id 分块（keyset 分页）读、读完一块就关掉游标再回写：
//   - 内存有界：logs.other 全表是几百 MB，一次性读进内存不可接受；
//   - `*sql.Rows` 未关闭时同一连接不能执行 UPDATE，而部分部署（与全部测试）把
//     MaxOpenConns 设成 1，边读边写会直接死锁。
//
// 分页游标与「是否读完」都必须按**本页实际读到的行**判断（含空串 / NULL 行）：
// 充值、系统、管理类日志的 other 经常为空，用「过滤后的行数」判断会让一页不满 500
// 就提前收尾、或让游标停在空格子上，结果是大批日志静默漏迁移。
//
// 不做 SQL 侧的键名预筛：那样「非法 JSON 的行」如果没提到任何清单键就会被静默跳过，
// 而报告需要如实列出所有解析不了的行（§M1.6）。
func rewriteJSONRows(db *gorm.DB, scope jsonScope, where string, apply bool) (jsonStat, error) {
	var stat jsonStat
	updateSQL := fmt.Sprintf("UPDATE %s SET %s = ? WHERE id = ?", scope.table, scope.jsonCol)

	lastID := int64(0)
	for {
		// 不在 SQL 里判空：`json <> ''` 在 PostgreSQL/MySQL 的 json 列上都是非法运算符
		// （tasks.private_data 就是 json 列）。空串与 NULL 在 Go 侧跳过。
		query := fmt.Sprintf("SELECT id, %s FROM %s WHERE id > %d",
			scope.jsonCol, scope.table, lastID)
		if where != "" {
			query += " AND " + where
		}
		query += fmt.Sprintf(" ORDER BY id LIMIT %d", jsonChunkSize)

		type row struct {
			id  int64
			raw sql.NullString
		}
		var (
			chunk     []row // 只有非空行参与改写
			pageCount int   // 本页实际读到的行数（含空行）
			pageLast  int64 // 本页最后一行的 id（含空行）
		)
		result, err := db.Raw(query).Rows()
		if err != nil {
			return stat, fmt.Errorf("读取 %s.%s 失败: %w", scope.table, scope.jsonCol, err)
		}
		for result.Next() {
			var r row
			if err := result.Scan(&r.id, &r.raw); err != nil {
				_ = result.Close()
				return stat, err
			}
			pageCount++
			pageLast = r.id
			if !r.raw.Valid || strings.TrimSpace(r.raw.String) == "" {
				// 空串 / NULL：跳过，不算「无法解析」。但游标仍要越过它。
				continue
			}
			chunk = append(chunk, r)
		}
		if err := result.Err(); err != nil {
			_ = result.Close()
			return stat, err
		}
		_ = result.Close()

		if pageCount == 0 {
			return stat, nil
		}
		lastID = pageLast

		for _, r := range chunk {
			stat.scanned++
			var fields map[string]json.RawMessage
			if err := common.Unmarshal([]byte(r.raw.String), &fields); err != nil || fields == nil {
				stat.unparsable = append(stat.unparsable, r.id)
				continue
			}
			// 没有目标键被改写就**不写回**：整行重写既浪费又可能改动原文写法。
			if !scope.rewrite(fields, &stat) {
				continue
			}
			stat.rewritten++
			if !apply {
				continue
			}
			encoded, err := common.Marshal(fields)
			if err != nil {
				return stat, fmt.Errorf("序列化 %s.id=%d 失败: %w", scope.table, r.id, err)
			}
			if err := db.Exec(updateSQL, string(encoded), r.id).Error; err != nil {
				return stat, fmt.Errorf("回写 %s.id=%d 失败: %w", scope.table, r.id, err)
			}
		}

		if pageCount < jsonChunkSize {
			return stat, nil
		}
	}
}

func maxLogID() (int64, error) {
	var maxID int64
	if err := model.LOG_DB.Raw("SELECT COALESCE(MAX(id), 0) FROM logs").Scan(&maxID).Error; err != nil {
		return 0, fmt.Errorf("读取 logs 最大 id 失败: %w", err)
	}
	return maxID, nil
}

// ---------------------------------------------------------------------------
// 日志阶段（分批）
// ---------------------------------------------------------------------------

func runLogStage(state migrationState) (logStageSummary, error) {
	summary := logStageSummary{from: state.LogsCursor, cutoff: state.LogsCutoff}
	start := time.Now()

	cursor := state.LogsCursor
	cutoff := state.LogsCutoff
	if cutoff <= cursor {
		summary.elapsed = time.Since(start)
		return summary, nil
	}

	for cursor < cutoff {
		next := cursor + logBatchSize
		if next > cutoff {
			next = cutoff
		}
		stat, err := applyLogBatch(cursor, next, state)
		if err != nil {
			return summary, err
		}
		summary.otherStat.scanned += stat.scanned
		summary.otherStat.rewritten += stat.rewritten
		summary.otherStat.unparsable = append(summary.otherStat.unparsable, stat.unparsable...)
		cursor = next
		summary.batches++
		if testHookAfterLogBatch != nil {
			if err := testHookAfterLogBatch(cursor); err != nil {
				return summary, err
			}
		}
	}
	summary.elapsed = time.Since(start)
	return summary, nil
}

// applyLogBatch 处理一个 id 区间。
//
// 同库时数值列、other 与 logs_cursor 在**一个事务**里提交，断点续跑不会重复缩放；
// 日志库独立时只能先提交日志事务再写主库游标——中间崩溃会留下「已缩放但游标没动」
// 的窗口，所以那种情况下的游标写失败会返回明确要求人工介入的错误，而不是静默继续。
func applyLogBatch(from, to int64, state migrationState) (jsonStat, error) {
	var stat jsonStat
	sameDB := model.LOG_DB == model.DB
	idRange := fmt.Sprintf("id > %d AND id <= %d", from, to)

	tx := model.LOG_DB.Begin()
	if tx.Error != nil {
		return stat, tx.Error
	}
	fail := func(err error) (jsonStat, error) {
		_ = tx.Rollback()
		return stat, err
	}

	if err := execScaleUpdate(tx, "logs", logsNumericColumns, idRange); err != nil {
		return fail(err)
	}
	var err error
	if stat, err = rewriteJSONRows(tx, logsJSONScope, idRange, true); err != nil {
		return fail(err)
	}
	if sameDB {
		state.LogsCursor = to
		if err := saveState(tx, state); err != nil {
			return fail(err)
		}
	}
	if err := tx.Commit().Error; err != nil {
		return stat, err
	}

	if !sameDB {
		state.LogsCursor = to
		if err := saveState(model.DB, state); err != nil {
			return stat, fmt.Errorf("日志批次 (id %d, %d] 已提交，但主库 logs_cursor 更新失败: %w。"+
				"人工把 options.%s 的 logs_cursor 改成 %d 再重跑 apply，否则这批会被重复缩放",
				from, to, err, stateOptionKey, to)
		}
	}
	return stat, nil
}

// ---------------------------------------------------------------------------
// 统计与报告
// ---------------------------------------------------------------------------

type columnStat struct {
	scope  numericScope
	column string
	rows   int64
	before int64
	after  int64
	// beforeKnown=false 表示「迁移前合计」不可得（断点续跑：这部分行上一轮已迁移）。
	beforeKnown bool
}

// sumInt 把 SUM 的结果统一成 64 位整数：PG 的 SUM(bigint) 是 numeric，驱动层无法
// 直接扫描进 int64；MySQL 的 SUM 是 DECIMAL；SQLite 的 SUM 在大值/REAL 上是浮点
// （会出现 1.46e+07 这种科学计数法，database/sql 转换 int64 直接报错）。
func sumInt(expr string) string {
	inner := fmt.Sprintf("COALESCE(SUM(%s), 0)", expr)
	switch {
	case common.UsingPostgreSQL:
		return inner + "::bigint"
	case common.UsingMySQL:
		return "CAST(" + inner + " AS SIGNED)"
	default:
		return "CAST(" + inner + " AS INTEGER)"
	}
}

func collectColumnStats(db *gorm.DB, scopes []numericScope) ([]columnStat, error) {
	stats := make([]columnStat, 0, 32)
	for _, scope := range scopes {
		for _, column := range scope.columns {
			sql := fmt.Sprintf("SELECT COUNT(*), %s FROM %s", sumInt(column), scope.table)
			if scope.where != "" {
				sql += " WHERE " + scope.where
			}
			var rows, before int64
			if err := db.Raw(sql).Row().Scan(&rows, &before); err != nil {
				return nil, fmt.Errorf("统计 %s.%s 失败: %w", scope.table, column, err)
			}
			stats = append(stats, columnStat{
				scope: scope, column: column, rows: rows, before: before, beforeKnown: true,
			})
		}
	}
	return stats, nil
}

// fillAfter 填充「迁移后」合计：projected = true 时在 SQL 里算预计值（dry-run），
// 否则读库里的实际值（apply 之后）。
func fillAfter(db *gorm.DB, stats []columnStat, projected bool) error {
	for i := range stats {
		st := &stats[i]
		expr := st.column
		if projected {
			expr = fmt.Sprintf("ROUND(%s * %v)", st.column, scale)
		}
		sql := fmt.Sprintf("SELECT %s FROM %s", sumInt(expr), st.scope.table)
		if st.scope.where != "" {
			sql += " WHERE " + st.scope.where
		}
		if err := db.Raw(sql).Scan(&st.after).Error; err != nil {
			return fmt.Errorf("统计 %s.%s 的迁移后合计失败: %w", st.scope.table, st.column, err)
		}
	}
	return nil
}

func writeColumnStats(rep *report, stats []columnStat, projected bool) {
	rows := make([][]string, 0, len(stats))
	for _, st := range stats {
		before, beforeCNY := strconv.FormatInt(st.before, 10),
			formatCNY(cny(float64(st.before), baseQuotaPerUnit, rep.rate))
		if !st.beforeKnown {
			before, beforeCNY = "—", "—"
		}
		rows = append(rows, []string{
			st.scope.table + "." + st.column,
			strconv.FormatInt(st.rows, 10),
			before,
			strconv.FormatInt(st.after, 10),
			beforeCNY,
			formatCNY(cny(float64(st.after), targetQuotaPerUnit, rep.rate)),
		})
	}
	rep.table([]string{"表.列", "行数", "迁移前合计", "迁移后合计", "迁移前 CNY", "迁移后 CNY"}, rows)
	if projected {
		rep.line("（迁移后合计 = SUM(ROUND(列 × %v))，为预计值；CNY = quota ÷ QPU × 汇率）", scale)
	}
}

func writeJSONStats(rep *report, rows []jsonStatRow) {
	rep.line("[JSON 键]")
	table := make([][]string, 0, len(rows))
	var unparsable []int64
	for _, r := range rows {
		table = append(table, []string{
			r.scope.table + "." + r.scope.jsonCol,
			strings.Join(r.scope.keys, ", "),
			strconv.FormatInt(r.stat.scanned, 10),
			strconv.FormatInt(r.stat.rewritten, 10),
			strconv.FormatInt(r.stat.nonNumeric, 10),
			strconv.Itoa(len(r.stat.unparsable)),
		})
		unparsable = append(unparsable, r.stat.unparsable...)
	}
	rep.table([]string{"存储", "清单键", "扫描行数", "已改写行", "值非数字未改", "无法解析"}, table)
	rep.line("无法解析（非 JSON 对象）的行：共 %d 行，前 %d 个 id：%s",
		len(unparsable), maxListedIDs, formatIDList(unparsable))
}

// writeUnparsableIDFile 把无法解析的行 id 全量落盘：报告里最多列 100 个，
// 排查时需要完整清单。
func writeUnparsableIDFile(path string, rows []jsonStatRow) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	var ids []int64
	for _, r := range rows {
		ids = append(ids, r.stat.unparsable...)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	var b strings.Builder
	fmt.Fprintf(&b, "# 额度单位迁移：无法解析成 JSON 对象的行 id（共 %d 行）\n", len(ids))
	fmt.Fprintf(&b, "# 生成时间: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "# 这些行未被改动，需要人工确认是否含待改的 quota 键。\n")
	for _, id := range ids {
		fmt.Fprintf(&b, "%d\n", id)
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}

func writeOptionRows(rep *report, rows []optionRow) {
	rep.line("[options]")
	table := make([][]string, 0, len(rows))
	for _, r := range rows {
		old := r.old
		if !r.found {
			old = "（缺失，走代码默认）"
		}
		target := r.target
		if !r.changed {
			if !r.found && r.target == "" {
				target = "（缺失，本次不写入）"
			} else {
				target = old + "（已是目标值）"
			}
		}
		table = append(table, []string{r.spec.key, old, target})
	}
	rep.table([]string{"key", "旧值", "新值"}, table)
	rep.line("PreConsumedQuota 刻意不在清单里：它是预扣 token 数下限而不是 quota，" +
		"倍率路径乘 scale 已经发生在它之后，再 ×14.6 会把冻结额放大两次。")
}

func batchCount(cursor, cutoff int64) int64 {
	if cutoff <= cursor {
		return 0
	}
	return (cutoff - cursor + logBatchSize - 1) / logBatchSize
}

func formatIDList(ids []int64) string {
	if len(ids) == 0 {
		return "（无）"
	}
	parts := make([]string, 0, len(ids))
	for i, id := range ids {
		if i == maxListedIDs {
			parts = append(parts, fmt.Sprintf("…（共 %d 行）", len(ids)))
			break
		}
		parts = append(parts, strconv.FormatInt(id, 10))
	}
	return strings.Join(parts, ", ")
}
