package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// withIsolatedSQLiteDB 临时把包级 DB 换成一个全新的独立内存库，跑完后还原，
// 避免影响 task_cas_test.go 里 TestMain 建好的共享 DB 和其他测试。
func withIsolatedSQLiteDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	origDB := DB
	origUsingSQLite := common.UsingSQLite
	DB = db
	common.UsingSQLite = true
	t.Cleanup(func() {
		DB = origDB
		common.UsingSQLite = origUsingSQLite
	})
	return db
}

func tableInfoColumns(t *testing.T, db *gorm.DB, table string) map[string]struct{} {
	t.Helper()
	var cols []struct {
		Name string `gorm:"column:name"`
	}
	require.NoError(t, db.Raw("PRAGMA table_info(`"+table+"`)").Scan(&cols).Error)
	set := make(map[string]struct{}, len(cols))
	for _, c := range cols {
		set[c.Name] = struct{}{}
	}
	return set
}

// TestEnsureSubscriptionPlanTableSQLite_FreshInstall 直接调用 SQLite 手工建表路径
// （而不是像 task_cas_test.go 的 TestMain 那样用 db.AutoMigrate 绕过它），
// 验证全新安装场景下 compute_points_per_period 列确实被建出来、且能正常读写。
func TestEnsureSubscriptionPlanTableSQLite_FreshInstall(t *testing.T) {
	db := withIsolatedSQLiteDB(t)

	require.False(t, db.Migrator().HasTable("subscription_plans"))
	require.NoError(t, ensureSubscriptionPlanTableSQLite())
	require.True(t, db.Migrator().HasTable("subscription_plans"))

	cols := tableInfoColumns(t, db, "subscription_plans")
	require.Contains(t, cols, "compute_points_per_period")

	plan := SubscriptionPlan{
		Title:                  "算力点套餐",
		PriceAmount:            9.9,
		Currency:               "USD",
		DurationUnit:           "month",
		DurationValue:          1,
		ComputePointsPerPeriod: 500,
	}
	require.NoError(t, db.Create(&plan).Error)

	var got SubscriptionPlan
	require.NoError(t, db.First(&got, plan.Id).Error)
	require.Equal(t, int64(500), got.ComputePointsPerPeriod)
}

// TestEnsureSubscriptionPlanTableSQLite_UpgradeExistingTable 模拟「表已存在但缺
// compute_points_per_period 列」的存量升级场景（老安装在这个字段加入前建的表），
// 验证 ALTER TABLE ADD COLUMN 补列路径同样生效。
func TestEnsureSubscriptionPlanTableSQLite_UpgradeExistingTable(t *testing.T) {
	db := withIsolatedSQLiteDB(t)

	legacyCreateSQL := "CREATE TABLE `subscription_plans` (" +
		"`id` integer," +
		"`title` varchar(128) NOT NULL," +
		"`subtitle` varchar(255) DEFAULT ''," +
		"`price_amount` real NOT NULL," +
		"`currency` varchar(8) NOT NULL DEFAULT 'USD'," +
		"`duration_unit` varchar(16) NOT NULL DEFAULT 'month'," +
		"`duration_value` integer NOT NULL DEFAULT 1," +
		"`custom_seconds` bigint NOT NULL DEFAULT 0," +
		"`enabled` numeric DEFAULT 1," +
		"`sort_order` integer DEFAULT 0," +
		"`stripe_price_id` varchar(128) DEFAULT ''," +
		"`creem_product_id` varchar(128) DEFAULT ''," +
		"`max_purchase_per_user` integer DEFAULT 0," +
		"`upgrade_group` varchar(64) DEFAULT ''," +
		"`total_amount` bigint NOT NULL DEFAULT 0," +
		"`quota_reset_period` varchar(16) DEFAULT 'never'," +
		"`quota_reset_custom_seconds` bigint DEFAULT 0," +
		"`created_at` bigint," +
		"`updated_at` bigint," +
		"PRIMARY KEY (`id`)" +
		")"
	require.NoError(t, db.Exec(legacyCreateSQL).Error)

	preCols := tableInfoColumns(t, db, "subscription_plans")
	require.NotContains(t, preCols, "compute_points_per_period")

	require.NoError(t, ensureSubscriptionPlanTableSQLite())

	postCols := tableInfoColumns(t, db, "subscription_plans")
	require.Contains(t, postCols, "compute_points_per_period")

	plan := SubscriptionPlan{
		Title:                  "存量升级套餐",
		PriceAmount:            19.9,
		Currency:               "USD",
		DurationUnit:           "year",
		DurationValue:          1,
		ComputePointsPerPeriod: 1200,
	}
	require.NoError(t, db.Create(&plan).Error)

	var got SubscriptionPlan
	require.NoError(t, db.First(&got, plan.Id).Error)
	require.Equal(t, int64(1200), got.ComputePointsPerPeriod)
}
