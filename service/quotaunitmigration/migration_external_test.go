package quotaunitmigration

import (
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// 三库覆盖（任务书 §5.3 第 6 条）。SQLite 在 migration_test.go 里用内存库跑，
// PostgreSQL / MySQL 需要外部实例，由环境变量提供 DSN：
//
//	QUOTA_MIGRATION_TEST_PG_DSN='postgresql://root:123456@127.0.0.1:5432/quota_migration_test' \
//	QUOTA_MIGRATION_TEST_MYSQL_DSN='root:123456@tcp(127.0.0.1:3306)/quota_migration_test?charset=utf8mb4&parseTime=True&loc=Local' \
//	go test ./service/quotaunitmigration/ -run TestExternalDatabase -v
//
// 未设置 DSN 的方言会被 Skip（不是静默通过），这样交付物里能明确看到哪个库没跑。
//
// ⚠️ 用例会先 DROP 目标库里的表再重建：DSN 必须指向一次性测试库，绝不能指生产库。
const (
	envPGDSN    = "QUOTA_MIGRATION_TEST_PG_DSN"
	envMySQLDSN = "QUOTA_MIGRATION_TEST_MYSQL_DSN"
)

type externalTarget struct {
	name    string
	envKey  string
	dialect func(dsn string) gorm.Dialector
	dropAll func(db *gorm.DB) error
}

func externalTargets() []externalTarget {
	return []externalTarget{
		{
			name:    "postgresql",
			envKey:  envPGDSN,
			dialect: func(dsn string) gorm.Dialector { return postgres.Open(dsn) },
			dropAll: func(db *gorm.DB) error {
				if err := db.Exec("DROP SCHEMA public CASCADE").Error; err != nil {
					return err
				}
				return db.Exec("CREATE SCHEMA public").Error
			},
		},
		{
			name:    "mysql",
			envKey:  envMySQLDSN,
			dialect: func(dsn string) gorm.Dialector { return mysql.Open(dsn) },
			dropAll: func(db *gorm.DB) error {
				var tables []string
				if err := db.Raw("SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE()").
					Scan(&tables).Error; err != nil {
					return err
				}
				if err := db.Exec("SET FOREIGN_KEY_CHECKS = 0").Error; err != nil {
					return err
				}
				for _, table := range tables {
					if err := db.Exec("DROP TABLE IF EXISTS `" + table + "`").Error; err != nil {
						return err
					}
				}
				return db.Exec("SET FOREIGN_KEY_CHECKS = 1").Error
			},
		},
	}
}

func openExternalDB(t *testing.T, target externalTarget, dsn string) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(target.dialect(dsn), &gorm.Config{})
	require.NoError(t, err, "连接 %s 失败", target.name)

	prevDB, prevLogDB := model.DB, model.LOG_DB
	prevPg, prevMy, prevSq := common.UsingPostgreSQL, common.UsingMySQL, common.UsingSQLite
	t.Cleanup(func() {
		model.DB, model.LOG_DB = prevDB, prevLogDB
		common.UsingPostgreSQL, common.UsingMySQL, common.UsingSQLite = prevPg, prevMy, prevSq
	})
	model.DB, model.LOG_DB = db, db
	prevLogDir := *common.LogDir
	*common.LogDir = t.TempDir()
	t.Cleanup(func() { *common.LogDir = prevLogDir })
	switch target.name {
	case "postgresql":
		common.UsingPostgreSQL, common.UsingMySQL, common.UsingSQLite = true, false, false
	case "mysql":
		common.UsingPostgreSQL, common.UsingMySQL, common.UsingSQLite = false, true, false
	}

	require.NoError(t, target.dropAll(db), "清空 %s 的测试库失败（DSN 必须指向一次性测试库）", target.name)
	require.NoError(t, db.AutoMigrate(
		&model.Option{}, &model.User{}, &model.Token{}, &model.Channel{}, &model.Log{},
		&model.Task{}, &model.Midjourney{}, &model.QuotaData{}, &model.Redemption{},
		&model.Checkin{}, &model.FundEntry{}, &model.BankTransferOrder{},
		&model.SubscriptionPlan{}, &model.UserSubscription{},
		&model.SubscriptionPreConsumeRecord{}, &model.ComputePointLot{}, &model.TopUp{},
	), "在 %s 上建表失败", target.name)

	testHookAfterLogBatch = nil
	t.Cleanup(func() { testHookAfterLogBatch = nil })
	return db
}

func TestExternalDatabaseFullMigration(t *testing.T) {
	for _, target := range externalTargets() {
		t.Run(target.name, func(t *testing.T) {
			dsn := os.Getenv(target.envKey)
			if dsn == "" {
				t.Skipf("未设置 %s，跳过 %s 用例", target.envKey, target.name)
			}
			db := openExternalDB(t, target, dsn)
			seedFixture(t, db)
			seedManyLogs(t, db, 11001) // 跨批，覆盖日志阶段

			// dry-run：不写库，且预计合计与 apply 实际值一致
			before := snapshotTables(t)
			report, err := runMigration(t, ModeDryRun)
			require.NoError(t, err, "%s：dry-run 失败", target.name)
			requireSameSnapshot(t, before, snapshotTables(t))
			wantSums := parseReportAfterSums(t, report, []string{
				"users.quota", "logs.quota", "logs.points_consumed", "top_ups.amount",
			})

			// apply
			report, err = runMigration(t, ModeApply)
			require.NoError(t, err, "%s：apply 失败", target.name)
			assertM15(t, db)
			assertM16(t, db, report)
			assertM17(t, db)
			for path, wantAfter := range wantSums {
				table, column := splitPath(path)
				require.Equal(t, wantAfter, tableSum(t, db, table, column),
					"%s：%s 的 dry-run 预计值与 apply 实际值不一致", target.name, path)
			}

			// 幂等：第二次 apply 不改数据、退出码 0
			after := snapshotTables(t)
			second, err := runMigration(t, ModeApply)
			require.NoError(t, err, "%s：第二次 apply 必须成功", target.name)
			require.Contains(t, second, "已完成")
			requireSameSnapshot(t, after, snapshotTables(t))
		})
	}
}

// M1.0 预检在三库上都必须拦住非 64 位列并打印对应方言的 DDL。
func TestExternalDatabasePrecheckRejectsNarrowInteger(t *testing.T) {
	for _, target := range externalTargets() {
		t.Run(target.name, func(t *testing.T) {
			dsn := os.Getenv(target.envKey)
			if dsn == "" {
				t.Skipf("未设置 %s，跳过 %s 用例", target.envKey, target.name)
			}
			db := openExternalDB(t, target, dsn)
			seedFixture(t, db)

			// 手工把 aff_quota 改成 32 位列，模拟「有人手工建过表」
			var alter string
			var wantDDL string
			if target.name == "postgresql" {
				alter = `ALTER TABLE users ALTER COLUMN aff_quota TYPE integer`
				wantDDL = `ALTER TABLE "users" ALTER COLUMN "aff_quota" TYPE bigint;`
			} else {
				alter = "ALTER TABLE users MODIFY COLUMN aff_quota INT"
				wantDDL = "ALTER TABLE `users` MODIFY COLUMN `aff_quota` BIGINT;"
			}
			require.NoError(t, db.Exec(alter).Error)

			before := snapshotTables(t)
			report, err := runMigration(t, ModeApply)
			require.Error(t, err, "%s：列类型不对时必须拒绝执行", target.name)
			require.Contains(t, err.Error(), "预检")
			require.Contains(t, report, "users.aff_quota")
			require.Contains(t, report, wantDDL, "报告里必须给出可执行的 DDL")
			requireSameSnapshot(t, before, snapshotTables(t))
		})
	}
}

// 三库的 SQL 方言差异集中在 ROUND / SUM / 保留字引号上，这里直接钉住关键 SQL 片段。
func TestDialectSpecificSQLIsPortable(t *testing.T) {
	sqlite := func() string { return sumInt("quota") }()
	require.Contains(t, sqlite, "CAST(")

	prevPg, prevMy := common.UsingPostgreSQL, common.UsingMySQL
	t.Cleanup(func() { common.UsingPostgreSQL, common.UsingMySQL = prevPg, prevMy })

	common.UsingPostgreSQL, common.UsingMySQL = true, false
	require.True(t, strings.HasSuffix(sumInt("quota"), "::bigint"))

	common.UsingPostgreSQL, common.UsingMySQL = false, true
	require.Contains(t, sumInt("quota"), "AS SIGNED")

	common.UsingPostgreSQL, common.UsingMySQL = false, false
	require.Contains(t, sumInt("quota"), "AS INTEGER")
}
