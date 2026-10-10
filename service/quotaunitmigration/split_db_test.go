package quotaunitmigration

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func newSQLite(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	return db
}

// openSplitTestDB 主库（**没有 logs 表**）+ 独立日志库，模拟配置了 LOG_SQL_DSN 的部署。
func openSplitTestDB(t *testing.T) (*gorm.DB, *gorm.DB) {
	t.Helper()
	mainDB, logDB := newSQLite(t), newSQLite(t)

	prevDB, prevLogDB := model.DB, model.LOG_DB
	prevPg, prevMy, prevSq := common.UsingPostgreSQL, common.UsingMySQL, common.UsingSQLite
	prevLogDir := *common.LogDir
	t.Cleanup(func() {
		model.DB, model.LOG_DB = prevDB, prevLogDB
		common.UsingPostgreSQL, common.UsingMySQL, common.UsingSQLite = prevPg, prevMy, prevSq
		*common.LogDir = prevLogDir
	})
	model.DB, model.LOG_DB = mainDB, logDB
	common.UsingPostgreSQL, common.UsingMySQL, common.UsingSQLite = false, false, true
	*common.LogDir = t.TempDir()

	testHookAfterLogBatch = nil
	t.Cleanup(func() { testHookAfterLogBatch = nil })

	require.NoError(t, mainDB.AutoMigrate(
		&model.Option{}, &model.User{}, &model.Token{}, &model.Channel{},
		&model.Task{}, &model.Midjourney{}, &model.QuotaData{}, &model.Redemption{},
		&model.Checkin{}, &model.FundEntry{}, &model.BankTransferOrder{},
		&model.SubscriptionPlan{}, &model.UserSubscription{},
		&model.SubscriptionPreConsumeRecord{}, &model.ComputePointLot{}, &model.TopUp{},
	))
	require.NoError(t, logDB.AutoMigrate(&model.Log{}))
	return mainDB, logDB
}

// 独立日志库的部署形态：预检必须查日志库的 logs，主库里根本没有这张表；
// 日志与其游标分属两库时，游标仍写主库，且批次提交后不会重复缩放。
func TestSeparateLogDatabaseMigration(t *testing.T) {
	mainDB, logDB := openSplitTestDB(t)
	seedSmallTables(t, mainDB)
	seedLogs(t, logDB)
	seedManyLogs(t, logDB, 11001) // 跨 3 批

	// dry-run：预检不得因为主库没有 logs 表而误报
	dryReport, err := runMigration(t, ModeDryRun)
	require.NoError(t, err, "主库没有 logs 表不应导致预检失败")
	require.Contains(t, dryReport, "id (0, 11005]，预计 3 批")

	applyReport, err := runMigration(t, ModeApply)
	require.NoError(t, err)
	require.Contains(t, applyReport, "apply 结束")
	// 新跑（cursor = 0）时日志的迁移前合计必须是真实值，不能被标注为不可得
	_, before, after := reportNumericRow(t, applyReport, "logs.quota")
	require.Greater(t, before, int64(0), "独立日志库下也要采集到真实的迁移前合计")
	require.NotEqual(t, before, after)

	// 日志在日志库里被缩放（数值列 + other），主库的小表也照常
	var scaledQuota int64
	require.NoError(t, logDB.Raw("SELECT quota FROM logs WHERE id = 1").Scan(&scaledQuota).Error)
	require.Equal(t, expectScaled(fQuota), scaledQuota)
	var scaledOther string
	require.NoError(t, logDB.Raw("SELECT other FROM logs WHERE id = 1").Scan(&scaledOther).Error)
	require.Contains(t, scaledOther, fmt.Sprintf(`"fee_quota":%d`, expectScaled(fUsed)))

	var userQuota int64
	require.NoError(t, mainDB.Raw("SELECT quota FROM users WHERE id = 1").Scan(&userQuota).Error)
	require.Equal(t, expectScaled(fQuota), userQuota)

	// 游标与终态在主库 options 里，且指向日志库的最大 id
	state, found, err := loadState()
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, stateDone, state.State)
	require.Equal(t, int64(11005), state.LogsCutoff)
	require.Equal(t, int64(11005), state.LogsCursor)

	// 幂等
	second, err := runMigration(t, ModeApply)
	require.NoError(t, err)
	require.Contains(t, second, "已完成")
	require.NoError(t, logDB.Raw("SELECT quota FROM logs WHERE id = 1").Scan(&scaledQuota).Error)
	require.Equal(t, expectScaled(fQuota), scaledQuota, "第二次 apply 不得重复缩放")
}

// apply 报告的日志「迁移前合计」必须是分批改写之前采集的真实值，不能与「迁移后」相同。
func TestApplyReportShowsRealBeforeSums(t *testing.T) {
	db := openTestDB(t)
	seedFixture(t, db)
	const extraLogs = 600
	seedManyLogs(t, db, extraLogs)

	report, err := runMigration(t, ModeApply)
	require.NoError(t, err)

	rows, before, after := reportNumericRow(t, report, "logs.quota")
	require.Equal(t, int64(4+extraLogs), rows)
	require.Equal(t, int64(4+extraLogs)*fQuota, before, "迁移前合计应是改写之前的真实值")
	require.Equal(t, int64(4+extraLogs)*expectScaled(fQuota), after)
	require.NotEqual(t, before, after, "前后两列不得相同（那正是采集时机写错的症状）")
	require.Equal(t, tableSum(t, db, "logs", "quota"), after, "迁移后合计应与库里的实际值一致")
}

// 断点续跑时 id ≤ cursor 的行上一轮已迁移，真实前值不可得：报告必须标注而不是
// 拿迁移后的值冒充。
func TestApplyReportMarksBeforeSumUnknownOnResume(t *testing.T) {
	db := openTestDB(t)
	seedFixture(t, db)
	seedManyLogs(t, db, 11001)

	testHookAfterLogBatch = func(cursor int64) error {
		if cursor == logBatchSize {
			return fmt.Errorf("注入的中断")
		}
		return nil
	}
	_, err := runMigration(t, ModeApply)
	require.Error(t, err)
	testHookAfterLogBatch = nil

	report, err := runMigration(t, ModeApply)
	require.NoError(t, err)
	require.Contains(t, report, "断点续跑")
	require.Contains(t, report, "「迁移前合计」不可得（标为 —）")
	_, _, after := reportNumericRow(t, report, "logs.quota")
	require.Equal(t, tableSum(t, db, "logs", "quota"), after, "迁移后合计仍须是实际值")
}

// reportNumericRow 从报告里抠出某一行的 (行数, 迁移前合计, 迁移后合计)；
// 前值被标注为不可得时返回 -1。
func reportNumericRow(t *testing.T, report, path string) (int64, int64, int64) {
	t.Helper()
	for _, line := range splitReportLines(report) {
		fields := splitReportFields(line)
		if len(fields) < 4 || fields[0] != path {
			continue
		}
		rows := mustInt(t, fields[1])
		before := int64(-1)
		if fields[2] != "—" {
			before = mustInt(t, fields[2])
		}
		return rows, before, mustInt(t, fields[3])
	}
	t.Fatalf("报告里没有 %s 的合计行", path)
	return 0, 0, 0
}

// dry-run 与 apply 共用分页函数，所以空 other 行同样会让 dry-run 的「已改写行」偏小
// （运维正是拿这个数与生产快照的 22,858 行对比）。两份报告的数字必须一致且正确。
func TestDryRunRewriteCountMatchesApplyAcrossEmptyRows(t *testing.T) {
	db := openTestDB(t)
	seedOption(t, db, "USDExchangeRate", "7.3")

	const rows = 1200
	logs := make([]model.Log, 0, rows)
	wantRewritten := int64(0)
	for id := 1; id <= rows; id++ {
		other := ""
		if id%3 != 0 { // 每 3 行留 1 行空 other
			other = fmt.Sprintf(`{"fee_quota":%d}`, fUsed)
			wantRewritten++
		}
		logs = append(logs, model.Log{Id: id, UserId: 1, Type: model.LogTypeConsume, Other: other, CreatedAt: int64(id)})
	}
	require.NoError(t, db.CreateInBatches(logs, 500).Error)

	dryReport, err := runMigration(t, ModeDryRun)
	require.NoError(t, err)
	dryScanned, dryRewritten := jsonScopeNumbers(t, dryReport, "logs.other")
	require.Equal(t, wantRewritten, dryScanned, "扫描行数是全部非空行（空行在解析前就跳过）")
	require.Equal(t, wantRewritten, dryRewritten, "dry-run 的已改写行数不得因空行变少")

	applyReport, err := runMigration(t, ModeApply)
	require.NoError(t, err)
	applyScanned, applyRewritten := jsonScopeNumbers(t, applyReport, "logs.other")
	require.Equal(t, dryRewritten, applyRewritten, "dry-run 与 apply 的改写行数必须一致")
	require.Equal(t, dryScanned, applyScanned)
}

// jsonScopeNumbers 取 [JSON 键] 表里某一行末尾四个数值列中的「扫描行数 / 已改写行」。
// 从行尾取而不是按列序取：中间的「清单键」列本身含空格与逗号，按 Fields 切会串列。
func jsonScopeNumbers(t *testing.T, report, scope string) (int64, int64) {
	t.Helper()
	for _, line := range splitReportLines(report) {
		fields := splitReportFields(line)
		if len(fields) < 5 || fields[0] != scope {
			continue
		}
		n := len(fields)
		return mustInt(t, fields[n-4]), mustInt(t, fields[n-3])
	}
	t.Fatalf("报告里没有 %s 的 JSON 行", scope)
	return 0, 0
}
