package model

import (
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 三库锁实现的真机测试（docs/points-expiry-design.md §十八 S8、AC11、AC12）。
//
// 这三条是 SQLite 测不出来的：`clause.Locking` 在 SQLite 上被驱动静默丢弃，
// 所以 SQLite 通过不代表 MySQL/PG 真的加了行锁；反过来 MySQL 的 RowsAffected
// 语义与 SQLite 不同，`exists` 的取法必须按方言分支——这条也只有在真库上才验得出。
//
// 未设置 TEST_MYSQL_DSN / TEST_POSTGRES_DSN 时跳过；S8 验收要求两者都设置。

func openPointsExternalDB(t *testing.T, dialect, dsn string) *gorm.DB {
	t.Helper()

	var db *gorm.DB
	var err error
	switch dialect {
	case "mysql":
		db, err = gorm.Open(mysql.Open(dsn), &gorm.Config{
			Logger: logger.Default.LogMode(logger.Silent),
		})
	case "postgres":
		db, err = gorm.Open(postgres.Open(dsn), &gorm.Config{
			Logger: logger.Default.LogMode(logger.Silent),
		})
	default:
		t.Fatalf("unknown dialect %q", dialect)
	}
	require.NoError(t, err, "打开 %s 失败", dialect)

	prevDB, prevLogDB := DB, LOG_DB
	prevSq, prevMy, prevPg := common.UsingSQLite, common.UsingMySQL, common.UsingPostgreSQL
	prevRedis := common.RedisEnabled
	t.Cleanup(func() {
		DB, LOG_DB = prevDB, prevLogDB
		common.UsingSQLite, common.UsingMySQL, common.UsingPostgreSQL = prevSq, prevMy, prevPg
		common.RedisEnabled = prevRedis
		initCol()
	})

	DB, LOG_DB = db, db
	common.UsingSQLite = false
	common.UsingMySQL = dialect == "mysql"
	common.UsingPostgreSQL = dialect == "postgres"
	common.RedisEnabled = false
	// group / key 这类保留字列名要按方言重算
	initCol()

	// 每次跑都是干净状态：这三张表的形状在外部库里只有本测试用
	for _, stmt := range []string{
		"DROP TABLE IF EXISTS points_lots",
		"DROP TABLE IF EXISTS fund_entries",
		"DROP TABLE IF EXISTS users",
	} {
		require.NoError(t, db.Exec(stmt).Error, stmt)
	}
	require.NoError(t, db.AutoMigrate(&User{}, &PointsLot{}, &FundEntry{}))
	return db
}

func seedExternalPointsUser(t *testing.T, id int, balance int64) {
	t.Helper()
	require.NoError(t, DB.Create(&User{
		Id: id, Username: fmt.Sprintf("ext_%d", id), Role: 1, Status: 1,
		Group: "default", PointsBalance: int(balance),
		AffCode: fmt.Sprintf("ex%06d", id),
	}).Error)
}

func readExternalBalance(t *testing.T, id int) int64 {
	t.Helper()
	var b int64
	require.NoError(t, DB.Model(&User{}).Where("id = ?", id).Select("points_balance").Scan(&b).Error)
	return b
}

// assertPointsLockBehaviour 验 AC11：存在的用户必须被判为存在（MySQL 的 RowsAffected
// 语义与 SQLite 不同，取错了会把所有用户判成不存在、进而删光批次）。
func assertPointsLockBehaviour(t *testing.T) {
	t.Helper()
	const id = 9001
	seedExternalPointsUser(t, id, 1234)
	insertLot(t, PointsLot{UserId: id, Source: FundSourceCheckin, Amount: 1234, Status: PointsLotStatusActive})

	// 存在的用户：exists=true 且余额读得到
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		balance, exists, err := lockUserPointsTx(tx, id)
		require.NoError(t, err)
		require.True(t, exists, "存在的用户不得被判为不存在（否则会造成误删批次）")
		require.EqualValues(t, 1234, balance)
		return nil
	}))

	// 不存在的用户：exists=false
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		_, exists, err := lockUserPointsTx(tx, id+999999)
		require.NoError(t, err)
		require.False(t, exists)
		return nil
	}))

	// 同步一次：余额与批次不变，不变式成立
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return syncPointsLotsTx(tx, id, common.GetTimestamp())
	}))
	require.EqualValues(t, 1234, readExternalBalance(t, id))
	require.EqualValues(t, 1234, sumActiveRemaining(listLots(t, id)))
}

// assertPointsForfeitOnce 验 AC12：多个执行者并发作废同一用户的到期批次，
// 只作废一次、余额只扣一次、流水只一条。
func assertPointsForfeitOnce(t *testing.T) {
	t.Helper()
	const id = 9002
	exp := time.Now().Unix() - 3600
	seedExternalPointsUser(t, id, 900)
	insertLot(t, PointsLot{
		UserId: id, Source: FundSourceCheckin, Amount: 900, ExpiresAt: exp,
		GrantedAt: exp - 86400, Status: PointsLotStatusActive,
	})

	const workers = 6
	var wg sync.WaitGroup
	results := make([]int64, workers)
	errs := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], _, errs[i] = ExpireUserPointsLots(id, time.Now().Unix())
		}(i)
	}
	wg.Wait()

	var total int64
	for i := 0; i < workers; i++ {
		require.NoError(t, errs[i], "并发作废不应报错")
		total += results[i]
	}
	require.EqualValues(t, 900, total, "多个执行者合计只能作废一次")
	require.EqualValues(t, 0, readExternalBalance(t, id), "余额只能被扣一次，且不得为负")

	var entries int64
	require.NoError(t, DB.Model(&FundEntry{}).Where("user_id = ? AND kind = ?", id, FundKindExpire).
		Count(&entries).Error)
	require.EqualValues(t, 1, entries, "expire 流水只能有一条")
}

// assertPointsGrantConcurrency 并发发放 + 热路径扣减后，sync 仍须让不变式成立。
func assertPointsGrantConcurrency(t *testing.T) {
	t.Helper()
	const id = 9003
	seedExternalPointsUser(t, id, 0)
	now := time.Now().Unix()

	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			refId := fmt.Sprintf("9003:g%d", i)
			_, err := GrantPoints(PointsGrant{
				UserId: id, Quota: 1000, Source: FundSourceCheckin, RefId: refId,
				ExpiresAt: ExpireAtDays(now, 10), Now: now,
				Fund: FundEntry{
					Kind: FundKindGift, Source: FundSourceCheckin,
					RefType: FundRefCheckin, RefId: refId,
				},
			})
			require.NoError(t, err)
		}(i)
	}
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 5; k++ {
				_, err := TryDecreaseUserPoints(id, 100)
				require.NoError(t, err)
			}
		}()
	}
	wg.Wait()

	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return syncPointsLotsTx(tx, id, now+10)
	}))
	balance := readExternalBalance(t, id)
	require.GreaterOrEqual(t, balance, int64(0), "余额不得为负")
	require.Equal(t, balance, sumActiveRemaining(listLots(t, id)), "sync 后不变式必须成立")
}

func TestPointsLotsLockMySQL(t *testing.T) {
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set TEST_MYSQL_DSN to run the MySQL lock test")
	}
	openPointsExternalDB(t, "mysql", dsn)
	assertPointsLockBehaviour(t)
}

func TestPointsLotsLockPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set TEST_POSTGRES_DSN to run the Postgres lock test")
	}
	openPointsExternalDB(t, "postgres", dsn)
	assertPointsLockBehaviour(t)
}

func TestPointsLotsConcurrentForfeitMySQL(t *testing.T) {
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set TEST_MYSQL_DSN to run the MySQL concurrent forfeit test")
	}
	openPointsExternalDB(t, "mysql", dsn)
	assertPointsForfeitOnce(t)
}

func TestPointsLotsConcurrentForfeitPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set TEST_POSTGRES_DSN to run the Postgres concurrent forfeit test")
	}
	openPointsExternalDB(t, "postgres", dsn)
	assertPointsForfeitOnce(t)
}

func TestPointsLotsGrantConcurrencyMySQL(t *testing.T) {
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set TEST_MYSQL_DSN to run the MySQL grant concurrency test")
	}
	openPointsExternalDB(t, "mysql", dsn)
	assertPointsGrantConcurrency(t)
}

func TestPointsLotsGrantConcurrencyPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set TEST_POSTGRES_DSN to run the Postgres grant concurrency test")
	}
	openPointsExternalDB(t, "postgres", dsn)
	assertPointsGrantConcurrency(t)
}
