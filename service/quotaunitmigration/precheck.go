package quotaunitmigration

import (
	"fmt"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"gorm.io/gorm"
)

// M1.0 列类型预检：数值放大 14.6 倍后，32 位列在值 ≥ 147,087,541（旧单位，约 ¥2,147）时会溢出
// （PostgreSQL 报 out of range，MySQL 静默截断/报错）。迁移程序不自动改表结构，只拒绝执行
// 并打印对应方言的 DDL，由运维在停服窗口内手动执行后重跑。
//
// 项目里 users.aff_quota / aff_history 的标签是 gorm:"type:int"，但 GORM v1.25 把它识别成
// 通用整数类型 schema.Int，宽度取 Go 字段的 64 位，三库建出来都是 64 位整数
// （gorm.io/gorm/schema/field.go 的 TYPE 标签处理 + gorm.io/driver/postgres 的 DataTypeOf）。
// 预检只是兜底，防止有人手工建过表。
const maxSafeNumeric = 9.2e18

type columnRef struct {
	table  string
	column string
}

// numericColumnRefs 返回 M1.5 里所有需要检查的列（去重，含 logs）。
func numericColumnRefs() []columnRef {
	seen := map[columnRef]bool{}
	refs := make([]columnRef, 0, 32)
	add := func(table string, columns []string) {
		for _, c := range columns {
			ref := columnRef{table: table, column: c}
			if seen[ref] {
				continue
			}
			seen[ref] = true
			refs = append(refs, ref)
		}
	}
	for _, scope := range smallNumericScopes {
		add(scope.table, scope.columns)
	}
	add("logs", logsNumericColumns)
	return refs
}

// jsSafeInteger 是 JS 能精确表示的最大整数（2⁵³-1）。前端用 Number 读 quota，
// 超过这个值就开始丢精度，所以迁移后（×100）逼近它的余额必须在报告里点名。
const jsSafeInteger = int64(1)<<53 - 1

// precheckJSSafeInteger 检查迁移后的余额是否逼近 JS 安全整数。
//
// 只警告不拒绝：前端展示不准不会算错账（计费在服务端按 int64 做），但运营需要知道
// 哪个用户的余额已经大到页面显示会漂。2⁵³ quota ≈ ¥9,007 万。
func precheckJSSafeInteger(rep *report) error {
	multiplier := currentPlan().maxMultiplier()
	for _, table := range []struct{ table, column string }{
		{"users", "quota"},
		{"tokens", "remain_quota"},
		{"users", "used_quota"},
	} {
		var maxAbs int64
		query := fmt.Sprintf("SELECT COALESCE(MAX(ABS(%s)), 0) FROM %s", table.column, table.table)
		if err := dbForTable(table.table).Raw(query).Scan(&maxAbs).Error; err != nil {
			// 表可能不存在（老库），不是致命问题
			rep.line("（跳过 %s.%s 的安全整数检查：%v）", table.table, table.column, err)
			continue
		}
		projected := float64(maxAbs) * multiplier
		if projected <= float64(jsSafeInteger) {
			continue
		}
		rep.line("⚠️ %s.%s 最大值 %d，迁移后约 %.0f，超过 JS 安全整数 2⁵³（前端展示会丢精度，服务端计费不受影响）",
			table.table, table.column, maxAbs, projected)
	}
	return nil
}

func precheckColumnTypes(rep *report) error {
	refs := numericColumnRefs()

	rep.blank()
	rep.line("[M1.0 列类型预检]")
	// JS 安全整数检查与方言无关，放在 SQLite 早退之前。
	if err := precheckJSSafeInteger(rep); err != nil {
		return err
	}
	if common.UsingSQLite {
		rep.line("SQLite：INTEGER 本身是 64 位，跳过类型检查。")
		return nil
	}

	bad := make([]columnRef, 0)
	for _, ref := range refs {
		dataType, found, err := columnDataType(dbForTable(ref.table), ref)
		if err != nil {
			return fmt.Errorf("检查 %s.%s 的列类型失败: %w", ref.table, ref.column, err)
		}
		if !found {
			bad = append(bad, ref)
			continue
		}
		if !is64BitInteger(dataType) {
			bad = append(bad, ref)
		}
	}
	if len(bad) > 0 {
		sort.Slice(bad, func(i, j int) bool {
			if bad[i].table != bad[j].table {
				return bad[i].table < bad[j].table
			}
			return bad[i].column < bad[j].column
		})
		rep.line("以下列不是 64 位整数，×14.6 会溢出，拒绝执行：")
		for _, ref := range bad {
			rep.line("  - %s.%s", ref.table, ref.column)
		}
		rep.line("请在停服窗口内先执行下面的 DDL（迁移程序不自动改表结构），再重跑：")
		for _, ref := range bad {
			rep.line("  %s", alterToBigintDDL(ref))
		}
		return fmt.Errorf("列类型预检未通过：%d 列不是 64 位整数", len(bad))
	}

	// 溢出保险：理论上不会触发（64 位列上限 9.2e18 / 14.6 ≈ 6.3e17 quota）。
	for _, ref := range refs {
		maxAbs, err := columnMaxAbs(dbForTable(ref.table), ref)
		if err != nil {
			return fmt.Errorf("检查 %s.%s 的最大值失败: %w", ref.table, ref.column, err)
		}
		multiplier := currentPlan().maxMultiplier()
		if float64(maxAbs)*multiplier > maxSafeNumeric {
			return fmt.Errorf("%s.%s 最大值 %d × %v 会溢出 int64，拒绝执行", ref.table, ref.column, maxAbs, multiplier)
		}
	}

	rep.line("%d 列全部是 64 位整数，且放大 %v 倍后不会溢出。", len(refs), currentPlan().maxMultiplier())
	return nil
}

func is64BitInteger(dataType string) bool {
	switch strings.ToLower(strings.TrimSpace(dataType)) {
	case "bigint", "int8", "integer", "int4", "int", "mediumint":
		// PG 的 bigint = int8；MySQL 的 bigint；SQLite 不走到这里。
		// integer/int4/int/mediumint 都小于 64 位，必须拒绝。
		return strings.EqualFold(dataType, "bigint") || strings.EqualFold(dataType, "int8")
	default:
		return false
	}
}

// dbForTable logs 在配置了 LOG_SQL_DSN 时位于独立的日志库，主库里没有这张表。
// 检查它的列类型必须查日志库，否则会误报「不是 64 位整数」并拒绝执行。
func dbForTable(table string) *gorm.DB {
	if table == "logs" {
		return model.LOG_DB
	}
	return model.DB
}

func columnDataType(db *gorm.DB, ref columnRef) (string, bool, error) {
	var dataType string
	query := ""
	switch {
	case common.UsingPostgreSQL:
		query = "SELECT data_type FROM information_schema.columns " +
			"WHERE table_schema = current_schema() AND table_name = ? AND column_name = ?"
	case common.UsingMySQL:
		query = "SELECT data_type FROM information_schema.columns " +
			"WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?"
	default:
		return "", false, nil
	}
	res := db.Raw(query, ref.table, ref.column).Scan(&dataType)
	if res.Error != nil {
		return "", false, res.Error
	}
	if res.RowsAffected == 0 || dataType == "" {
		return "", false, nil
	}
	return dataType, true, nil
}

func columnMaxAbs(db *gorm.DB, ref columnRef) (int64, error) {
	var maxAbs int64
	query := fmt.Sprintf("SELECT COALESCE(MAX(ABS(%s)), 0) FROM %s", ref.column, ref.table)
	if err := db.Raw(query).Scan(&maxAbs).Error; err != nil {
		return 0, err
	}
	return maxAbs, nil
}

func alterToBigintDDL(ref columnRef) string {
	if common.UsingMySQL {
		return fmt.Sprintf("ALTER TABLE `%s` MODIFY COLUMN `%s` BIGINT;", ref.table, ref.column)
	}
	return fmt.Sprintf(`ALTER TABLE "%s" ALTER COLUMN "%s" TYPE bigint;`, ref.table, ref.column)
}
