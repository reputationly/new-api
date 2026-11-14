package model

import (
	"database/sql"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// `group` 是保留字，三种库的引用方式不同（PG 用双引号，MySQL/SQLite 用反引号）。
// 本地能跑的只有 SQLite，PG/MySQL 只能靠生成出来的 SQL 做兜底验证——写错引号在编译期
// 和 SQLite 测试里都不会暴露，上线才报语法错，那正是这条测试要拦的。
func TestGroupStatsLogQuery_DialectQuoting(t *testing.T) {
	origPG := common.UsingPostgreSQL
	origMySQL := common.UsingMySQL
	origSQLite := common.UsingSQLite
	origGroupCol := logGroupCol
	t.Cleanup(func() {
		common.UsingPostgreSQL = origPG
		common.UsingMySQL = origMySQL
		common.UsingSQLite = origSQLite
		logGroupCol = origGroupCol
	})

	cases := []struct {
		name       string
		dialector  gorm.Dialector
		groupCol   string
		wantSQL    string
		notWantSQL string
	}{
		{
			name:       "postgres 用双引号",
			dialector:  postgres.Open("postgres://u:p@127.0.0.1:1/none?sslmode=disable"),
			groupCol:   `"group"`,
			wantSQL:    `COALESCE("group", '')`,
			notWantSQL: "COALESCE(`group`, '')",
		},
		{
			name:       "mysql 用反引号",
			dialector:  mysqlDryRunDialector(t),
			groupCol:   "`group`",
			wantSQL:    "COALESCE(`group`, '')",
			notWantSQL: `COALESCE("group", '')`,
		},
		{
			name:       "sqlite 用反引号",
			dialector:  sqlite.Open(":memory:"),
			groupCol:   "`group`",
			wantSQL:    "COALESCE(`group`, '')",
			notWantSQL: `COALESCE("group", '')`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			logGroupCol = c.groupCol
			db, err := gorm.Open(c.dialector, &gorm.Config{
				DryRun:               true,
				DisableAutomaticPing: true,
			})
			require.NoError(t, err)

			sql := db.ToSQL(func(tx *gorm.DB) *gorm.DB {
				return groupStatsLogQuery(tx, 1000, 2000, "").Scan(&[]*GroupStatsHourly{})
			})
			require.Contains(t, sql, c.wantSQL)
			require.NotContains(t, sql, c.notWantSQL)
			require.Contains(t, sql, "GROUP BY")
			// 分桶表达式必须以原样输出（GORM 对多字段 Group 参数不额外加引号）
			require.Contains(t, sql, "logs.created_at - logs.created_at % 3600")
			// 两个 LIKE 条件必须带上，否则套餐/订阅会被算成现金。
			// 只查关键子串：驱动对字符串里的双引号转义方式各不相同。
			require.Contains(t, sql, "billing_source")
			require.Contains(t, sql, "subscription")
			require.Contains(t, sql, "entitlement")
		})
	}
}

// mysqlDryRunDialector 造一个不会真正连库的 MySQL dialector。
//
// 直接 mysql.Open(dsn) 会在初始化时执行 SELECT VERSION() 去连库（那一步不受
// DisableAutomaticPing 控制），本地没有 MySQL 就会直接失败。这里自己给 *sql.DB
// 并跳过版本探测：DryRun 下只会生成 SQL，不会真的执行。
func mysqlDryRunDialector(t *testing.T) gorm.Dialector {
	t.Helper()
	sqlDB, err := sql.Open("mysql", "u:p@tcp(127.0.0.1:1)/none")
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true})
}
