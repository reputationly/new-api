package quotaunitmigration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 期望值独立于实现：直接按「旧值 × 14.6 四舍五入」算，不复用 scaleValue。
func expectScaled(v int64) int64 {
	return int64(math.Round(float64(v) * 14.6))
}

func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	// MaxOpenConns(1)：与生产部分部署一致，顺带压出「边读 Rows 边写」的死锁风险。
	sqlDB.SetMaxOpenConns(1)

	prevDB, prevLogDB := model.DB, model.LOG_DB
	prevPg, prevMy, prevSq := common.UsingPostgreSQL, common.UsingMySQL, common.UsingSQLite
	t.Cleanup(func() {
		model.DB, model.LOG_DB = prevDB, prevLogDB
		common.UsingPostgreSQL, common.UsingMySQL, common.UsingSQLite = prevPg, prevMy, prevSq
	})
	model.DB, model.LOG_DB = db, db
	common.UsingPostgreSQL, common.UsingMySQL, common.UsingSQLite = false, false, true

	// 完整 id 清单写进日志目录：测试里换成临时目录，别往仓库里丢文件。
	prevLogDir := *common.LogDir
	*common.LogDir = t.TempDir()
	t.Cleanup(func() { *common.LogDir = prevLogDir })

	require.NoError(t, db.AutoMigrate(
		&model.Option{}, &model.User{}, &model.Token{}, &model.Channel{}, &model.Log{},
		&model.Task{}, &model.Midjourney{}, &model.QuotaData{}, &model.Redemption{},
		&model.Checkin{}, &model.FundEntry{}, &model.BankTransferOrder{},
		&model.SubscriptionPlan{}, &model.UserSubscription{},
		&model.SubscriptionPreConsumeRecord{}, &model.ComputePointLot{}, &model.TopUp{},
	))

	testHookAfterLogBatch = nil
	t.Cleanup(func() { testHookAfterLogBatch = nil })
	return db
}

const (
	fQuota      = int64(1136)  // 用户余额：1136 × 14.6 = 16585.6 → 16586
	fUsed       = int64(16583) // 16583 × 14.6 = 242111.8 → 242112
	fNegative   = int64(-500)  // fund_entries.quota_delta 允许为负：-7300
	fZero       = int64(0)
	fSmall      = int64(7) // 7 × 14.6 = 102.2 → 102
	fAff        = int64(3)
	fPoints     = int64(100)
	fAmount     = int64(1_000_000)
	fTopUpMoney = 1.0
)

// seedFixture 为 M1.5 / M1.6 / M1.7 的每一项造数据（日志与其它表同库）。
func seedFixture(t *testing.T, db *gorm.DB) {
	t.Helper()
	seedSmallTables(t, db)
	seedLogs(t, db)
}

// seedSmallTables options、各类额度表与 top_ups（不含 logs，便于独立日志库的用例）。
func seedSmallTables(t *testing.T, db *gorm.DB) {
	t.Helper()

	// options：一部分存在、一部分缺失（M1.7 的两种情况）
	seedOption(t, db, "QuotaPerUnit", "500000")
	seedOption(t, db, "USDExchangeRate", "7.3")
	seedOption(t, db, "QuotaForNewUser", strconvI(fUsed))
	seedOption(t, db, "QuotaRemindThreshold", "1000")
	seedOption(t, db, "checkin_setting.min_quota", "0.1")
	seedOption(t, db, "checkin_setting.max_quota", "10000")
	// 缺失：QuotaForInviter / QuotaForInvitee / points_setting.quota_per_point /
	// compute_point_setting.quota_per_compute_point（后者由 M1.7 写入 10000）
	seedOption(t, db, "PreConsumedQuota", "500") // 不在清单里，必须原样不动

	require.NoError(t, db.Create(&model.User{
		Id: 1, Username: "u1", AffCode: "aff1", Quota: int(fQuota), UsedQuota: int(fUsed),
		AffQuota: int(fAff), AffHistoryQuota: int(fSmall), PointsBalance: int(fPoints),
		PointsUsed: int(fZero), AffPointsEarned: int(fPoints), CreditLimit: int64(fAmount),
		CreditUsed: int64(fSmall), CreditSettled: int64(fNegative),
		Setting: `{"quota_warning_threshold":16583,"notify_type":"email","notify_threshold":7,` +
			`"nested":{"quota_warning_threshold":999}}`,
	}).Error)
	require.NoError(t, db.Create(&model.User{
		Id: 2, Username: "u2", AffCode: "aff2", Setting: `not json at all`,
	}).Error)
	require.NoError(t, db.Create(&model.User{Id: 3, Username: "u3", AffCode: "aff3", Setting: ""}).Error)

	require.NoError(t, db.Create(&model.Token{
		Id: 1, UserId: 1, Key: "sk-a", Name: "t1", RemainQuota: int(fQuota), UsedQuota: int(fUsed),
	}).Error)
	require.NoError(t, db.Create(&model.Channel{Id: 1, Name: "c1", Key: "k", UsedQuota: fUsed}).Error)
	require.NoError(t, db.Create(&model.Task{
		ID: 1, TaskID: "task-1", UserId: 1, ChannelId: 1, Quota: int(fQuota),
		PrivateData: model.TaskPrivateData{
			PointsConsumed: int(fSmall),
			CreditConsumed: int(fPoints),
			EntitlementSpent: []model.ComputePointSpend{
				{LotId: 1, Amount: fQuota},
				{LotId: 2, Amount: fSmall},
			},
			Aggregate: &model.TaskAggregateInfo{
				Stage1AdaptorQuota: int(fUsed),
				UpscaleTarget:      "1080p",
			},
			EntitlementDiscount: 0.5,
			BillingContext: &model.TaskBillingContext{
				GroupRatio:      1,
				OriginModelName: "m",
				ModelPrice:      0.04,
			},
			TokenName: "keep-me",
		},
	}).Error)
	require.NoError(t, db.Create(&model.Midjourney{Id: 1, UserId: 1, Quota: int(fUsed)}).Error)
	require.NoError(t, db.Create(&model.QuotaData{Id: 1, UserID: 1, Quota: int(fQuota)}).Error)
	require.NoError(t, db.Create(&model.Redemption{Id: 1, Key: "r1", Quota: int(fAmount)}).Error)
	require.NoError(t, db.Create(&model.Checkin{Id: 1, UserId: 1, QuotaAwarded: int(fSmall)}).Error)
	require.NoError(t, db.Create(&model.FundEntry{Id: 1, QuotaDelta: fNegative}).Error)
	require.NoError(t, db.Create(&model.BankTransferOrder{Id: 1, UserId: 1, QuotaGranted: fAmount}).Error)
	require.NoError(t, db.Create(&model.SubscriptionPlan{
		Id: 1, Title: "p1", TotalAmount: fAmount, ComputePointsPerPeriod: fAmount,
	}).Error)
	require.NoError(t, db.Create(&model.UserSubscription{
		Id: 1, UserId: 1, AmountTotal: fAmount, AmountUsed: fSmall,
	}).Error)
	require.NoError(t, db.Create(&model.SubscriptionPreConsumeRecord{
		Id: 1, UserId: 1, PreConsumed: fSmall,
	}).Error)
	require.NoError(t, db.Create(&model.ComputePointLot{
		Id: 1, UserId: 1, PointsTotal: fAmount, PointsUsed: fSmall,
	}).Error)

	// top_ups：每种 provider 各一行，amount 单位不同（M1.5 的 WHERE 条件）
	for i, provider := range []string{
		model.PaymentProviderAlipayDirect, model.PaymentProviderWxpayDirect,
		model.PaymentProviderCreem, model.PaymentProviderBankTransfer,
		model.PaymentProviderEpay, model.PaymentProviderStripe,
		model.PaymentProviderWaffo, model.PaymentProviderWaffoPancake,
	} {
		require.NoError(t, db.Create(&model.TopUp{
			Id: i + 1, UserId: 1, Amount: fAmount, Money: fTopUpMoney,
			TradeNo: fmt.Sprintf("trade-%d", i), PaymentProvider: provider,
			Status: common.TopUpStatusSuccess,
		}).Error)
	}
	// 未完成状态也要一并缩放（doc §M1.5「所有状态」）
	require.NoError(t, db.Create(&model.TopUp{
		Id: 100, UserId: 1, Amount: fAmount, Money: fTopUpMoney, TradeNo: "trade-pending",
		PaymentProvider: model.PaymentProviderAlipayDirect, Status: common.TopUpStatusPending,
	}).Error)

}

// logOtherSeed 与 logOtherScaledKeys 一一对应，用于同时构造 fixture 与断言期望值。
var logOtherScaledSeed = map[string]int64{
	"fee_quota": fUsed, "cost_quota": fQuota, "compute_points_quota": fPoints,
	"pre_consumed_quota": fSmall, "actual_quota": fAff, "wallet_quota_deducted": fPoints,
	"subscription_pre_consumed": fQuota, "subscription_post_delta": fSmall,
	"subscription_consumed": fAff, "subscription_remain": fPoints,
	"subscription_total": fQuota, "subscription_used": fSmall,
}

func buildLogOtherFixture(t *testing.T) string {
	t.Helper()
	payload := map[string]interface{}{
		"compute_points": 50,
		"entitlement_fallback": map[string]interface{}{
			"points_needed": 3, "points_available": 7,
		},
		"model_ratio":  0.13698630137,
		"group_ratio":  0.5,
		"cache_tokens": 199,
	}
	for key, value := range logOtherScaledSeed {
		payload[key] = value
	}
	encoded, err := common.Marshal(payload)
	require.NoError(t, err)
	return string(encoded)
}

// seedLogs 覆盖 M1.6 的全部待改键、不动键、非法 JSON 与空串。
func seedLogs(t *testing.T, db *gorm.DB) {
	t.Helper()
	// logs：全部待改键 + 不动键 + 非法 JSON + 空串
	seedLog(t, db, 1, buildLogOtherFixture(t))
	seedLog(t, db, 2, ``)                  // 空串：跳过
	seedLog(t, db, 3, `{oops`)             // 非法 JSON：跳过并计数
	seedLog(t, db, 4, `{"model_ratio":1}`) // 无待改键：原样保留
}

func seedOption(t *testing.T, db *gorm.DB, key, value string) {
	t.Helper()
	require.NoError(t, db.Create(&model.Option{Key: key, Value: value}).Error)
}

func seedLog(t *testing.T, db *gorm.DB, id int, other string) {
	t.Helper()
	require.NoError(t, db.Create(&model.Log{
		Id: id, UserId: 1, Type: model.LogTypeConsume, Quota: int(fQuota),
		PointsConsumed: int(fSmall), CreditConsumed: int(fPoints),
		Other: other, CreatedAt: int64(id),
	}).Error)
}

func strconvI(v int64) string { return fmt.Sprintf("%d", v) }

func runMigration(t *testing.T, mode string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	err := Run(mode, &buf)
	return buf.String(), err
}

func optionValue(t *testing.T, key string) (string, bool) {
	t.Helper()
	value, found, err := loadOption(model.DB, key)
	require.NoError(t, err)
	return value, found
}

// ---------------------------------------------------------------------------
// §5.3.1 全覆盖 fixture
// ---------------------------------------------------------------------------

func TestApplyScalesEveryM15Column(t *testing.T) {
	db := openTestDB(t)
	seedFixture(t, db)

	_, err := runMigration(t, ModeApply)
	require.NoError(t, err)

	assertM15(t, db)
}

// assertM15 断言 M1.5 全部列；三库共用同一份检查。
func assertM15(t *testing.T, db *gorm.DB) {
	t.Helper()
	type check struct {
		table  string
		column string
		id     int64
		want   int64
	}
	checks := []check{
		{"users", "quota", 1, expectScaled(fQuota)},
		{"users", "used_quota", 1, expectScaled(fUsed)},
		{"users", "aff_quota", 1, expectScaled(fAff)},
		{"users", "aff_history", 1, expectScaled(fSmall)},
		{"users", "points_balance", 1, expectScaled(fPoints)},
		{"users", "points_used", 1, expectScaled(fZero)},
		{"users", "aff_points_earned", 1, expectScaled(fPoints)},
		{"users", "credit_limit", 1, expectScaled(fAmount)},
		{"users", "credit_used", 1, expectScaled(fSmall)},
		{"users", "credit_settled", 1, expectScaled(fNegative)},
		{"tokens", "remain_quota", 1, expectScaled(fQuota)},
		{"tokens", "used_quota", 1, expectScaled(fUsed)},
		{"channels", "used_quota", 1, expectScaled(fUsed)},
		{"logs", "quota", 1, expectScaled(fQuota)},
		{"logs", "points_consumed", 1, expectScaled(fSmall)},
		{"logs", "credit_consumed", 1, expectScaled(fPoints)},
		{"tasks", "quota", 1, expectScaled(fQuota)},
		{"midjourneys", "quota", 1, expectScaled(fUsed)},
		{"quota_data", "quota", 1, expectScaled(fQuota)},
		{"redemptions", "quota", 1, expectScaled(fAmount)},
		{"checkins", "quota_awarded", 1, expectScaled(fSmall)},
		{"fund_entries", "quota_delta", 1, expectScaled(fNegative)},
		{"bank_transfer_orders", "quota_granted", 1, expectScaled(fAmount)},
		{"subscription_plans", "total_amount", 1, expectScaled(fAmount)},
		{"subscription_plans", "compute_points_per_period", 1, expectScaled(fAmount)},
		{"user_subscriptions", "amount_total", 1, expectScaled(fAmount)},
		{"user_subscriptions", "amount_used", 1, expectScaled(fSmall)},
		{"subscription_pre_consume_records", "pre_consumed", 1, expectScaled(fSmall)},
		{"compute_point_lots", "points_total", 1, expectScaled(fAmount)},
		{"compute_point_lots", "points_used", 1, expectScaled(fSmall)},
	}
	for _, c := range checks {
		var got int64
		sql := fmt.Sprintf("SELECT %s FROM %s WHERE id = %d", c.column, c.table, c.id)
		require.NoError(t, db.Raw(sql).Scan(&got).Error, "%s.%s", c.table, c.column)
		require.Equal(t, c.want, got, "%s.%s 应 = round(旧值 × 14.6)", c.table, c.column)
	}

	// top_ups：金额单位的 provider 不动，quota 单位的 provider 缩放
	for i, provider := range []string{
		model.PaymentProviderAlipayDirect, model.PaymentProviderWxpayDirect,
		model.PaymentProviderCreem, model.PaymentProviderBankTransfer,
	} {
		var got int64
		require.NoError(t, db.Raw("SELECT amount FROM top_ups WHERE id = ?", i+1).Scan(&got).Error)
		require.Equal(t, expectScaled(fAmount), got, "%s 的 amount 是 quota，应缩放", provider)
	}
	for i, provider := range []string{
		model.PaymentProviderEpay, model.PaymentProviderStripe,
		model.PaymentProviderWaffo, model.PaymentProviderWaffoPancake,
	} {
		var got int64
		require.NoError(t, db.Raw("SELECT amount FROM top_ups WHERE id = ?", i+5).Scan(&got).Error)
		require.Equal(t, fAmount, got, "%s 的 amount 是金额，不应缩放", provider)
	}
	var pending int64
	require.NoError(t, db.Raw("SELECT amount FROM top_ups WHERE id = 100").Scan(&pending).Error)
	require.Equal(t, expectScaled(fAmount), pending, "未完成订单也要缩放")

	// 状态
	state, found, err := loadState()
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, stateDone, state.State)
	// cutoff 必须是迁移开始时的 logs 最大 id（不同 fixture 行数不同，所以按实际值断言）
	var maxLogID int64
	require.NoError(t, db.Raw("SELECT COALESCE(MAX(id), 0) FROM logs").Scan(&maxLogID).Error)
	require.Equal(t, maxLogID, state.LogsCutoff)
}

func TestApplyRewritesEveryM16JSONKey(t *testing.T) {
	db := openTestDB(t)
	seedFixture(t, db)

	report, err := runMigration(t, ModeApply)
	require.NoError(t, err)

	assertM16(t, db, report)
}

// assertM16 断言 M1.6 全部键（含必须不动的键）。report 用于核对非法 JSON 的行 id。
func assertM16(t *testing.T, db *gorm.DB, report string) {
	t.Helper()
	var log1 model.Log
	require.NoError(t, db.First(&log1, 1).Error)
	var other map[string]interface{}
	require.NoError(t, common.UnmarshalJsonStr(log1.Other, &other))

	require.Len(t, logOtherScaledSeed, len(logOtherScaledKeys), "fixture 必须覆盖全部待改键")
	for key, seed := range logOtherScaledSeed {
		require.Equal(t, float64(expectScaled(seed)), other[key].(float64), "logs.other.%s 应被缩放", key)
	}
	// 不动键：展示点数与倍率原样保留
	require.Equal(t, 50.0, other["compute_points"])
	require.Equal(t, 3.0, other["entitlement_fallback"].(map[string]interface{})["points_needed"])
	require.Equal(t, 7.0, other["entitlement_fallback"].(map[string]interface{})["points_available"])
	require.InDelta(t, 0.13698630137, other["model_ratio"].(float64), 1e-12)
	require.Equal(t, 0.5, other["group_ratio"])
	require.Equal(t, 199.0, other["cache_tokens"])

	// 无待改键的行原样保留；非法 JSON / 空串不动
	var log4 model.Log
	require.NoError(t, db.First(&log4, 4).Error)
	require.JSONEq(t, `{"model_ratio":1}`, log4.Other)

	var log2, log3 model.Log
	require.NoError(t, db.First(&log2, 2).Error)
	require.Equal(t, "", log2.Other)
	require.NoError(t, db.First(&log3, 3).Error)
	require.Equal(t, "{oops", log3.Other)
	require.Contains(t, report, "无法解析（非 JSON 对象）的行：共 2 行，前 50 个 id：2, 3",
		"非法 JSON 的行 id 必须出现在报告里（users.id=2 与 logs.id=3）")

	// tasks.private_data：嵌套数组与嵌套对象。
	// 直接读原始列再自己解析，而不是读 task.PrivateData：TaskPrivateData.Scan 只接
	// []byte，SQLite 驱动把 TEXT 回成 string，于是 GORM 读回来的结构体是零值
	// （Scan 静默返回 nil，不报错）。那是既有实现的脆弱点，与本次迁移无关。
	var taskRaw string
	require.NoError(t, db.Raw("SELECT private_data FROM tasks WHERE id = 1").Scan(&taskRaw).Error)
	var private map[string]interface{}
	require.NoError(t, common.UnmarshalJsonStr(taskRaw, &private))

	require.Equal(t, float64(expectScaled(fSmall)), private["points_consumed"])
	require.Equal(t, float64(expectScaled(fPoints)), private["credit_consumed"])
	require.Equal(t, 0.5, private["entitlement_discount"], "折扣系数不是 quota，不得缩放")
	require.Equal(t, "keep-me", private["token_name"])

	spent, ok := private["entitlement_spent"].([]interface{})
	require.True(t, ok, "entitlement_spent 必须是数组")
	require.Len(t, spent, 2)
	require.Equal(t, float64(expectScaled(fQuota)), spent[0].(map[string]interface{})["amount"])
	require.Equal(t, float64(expectScaled(fSmall)), spent[1].(map[string]interface{})["amount"])

	aggregate, ok := private["aggregate"].(map[string]interface{})
	require.True(t, ok, "aggregate 必须是对象")
	require.Equal(t, float64(expectScaled(fUsed)), aggregate["stage1_adaptor_quota"])
	require.Equal(t, "1080p", aggregate["upscale_target"])

	bc, ok := private["billing_context"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, 0.04, bc["model_price"], "美元单价不得缩放")

	// users.setting：只改本层的 quota_warning_threshold，嵌套的同名键不动
	var user1 model.User
	require.NoError(t, db.First(&user1, 1).Error)
	var setting map[string]interface{}
	require.NoError(t, common.UnmarshalJsonStr(user1.Setting, &setting))
	require.Equal(t, float64(expectScaled(fUsed)), setting["quota_warning_threshold"])
	require.Equal(t, "email", setting["notify_type"])
	require.Equal(t, 7.0, setting["notify_threshold"])
	require.Equal(t,
		float64(999),
		setting["nested"].(map[string]interface{})["quota_warning_threshold"],
		"嵌套的同名键不在清单里，不得改写")

	// 非法 setting 行原样保留
	var user2 model.User
	require.NoError(t, db.First(&user2, 2).Error)
	require.Equal(t, "not json at all", user2.Setting)
}

func TestApplyRewritesM17Options(t *testing.T) {
	db := openTestDB(t)
	seedFixture(t, db)

	_, err := runMigration(t, ModeApply)
	require.NoError(t, err)

	assertM17(t, db)
}

// assertM17 断言 M1.7 的 options（含缺失时写入与刻意不动的两项）。
func assertM17(t *testing.T, db *gorm.DB) {
	t.Helper()
	expect := map[string]string{
		"QuotaPerUnit":                                  "7300000",
		"QuotaForNewUser":                               strconvI(expectScaled(fUsed)),
		"QuotaRemindThreshold":                          strconvI(expectScaled(1000)),
		"checkin_setting.min_quota":                     "1", // 0.1 × 14.6 = 1.46 → 1
		"checkin_setting.max_quota":                     strconvI(expectScaled(10000)),
		"points_setting.quota_per_point":                "10000", // 缺失 → 写入
		"compute_point_setting.quota_per_compute_point": "10000",
	}
	for key, want := range expect {
		got, found := optionValue(t, key)
		require.True(t, found, "options.%s 应存在", key)
		require.Equal(t, want, got, "options.%s", key)
	}

	// 缺失且不在「缺失时写入」清单里：不写
	for _, key := range []string{"QuotaForInviter", "QuotaForInvitee"} {
		_, found := optionValue(t, key)
		require.False(t, found, "options.%s 不在缺失写入清单里", key)
	}
	// PreConsumedQuota 是 token 数下限，原样不动
	got, found := optionValue(t, "PreConsumedQuota")
	require.True(t, found)
	require.Equal(t, "500", got, "PreConsumedQuota 是 token 数下限，不得缩放")
}

// checkin 的 min_quota 期望值手写：0.1 × 14.6 = 1.46 → 1（不是 0——按浮点解析后四舍五入）。
func TestCheckinMinQuotaUsesFloatParsing(t *testing.T) {
	db := openTestDB(t)
	seedFixture(t, db)
	_, err := runMigration(t, ModeApply)
	require.NoError(t, err)

	got, found := optionValue(t, "checkin_setting.min_quota")
	require.True(t, found)
	require.Equal(t, "1", got)
}

// ---------------------------------------------------------------------------
// §5.3.2 幂等
// ---------------------------------------------------------------------------

func TestApplyIsIdempotent(t *testing.T) {
	db := openTestDB(t)
	seedFixture(t, db)

	_, err := runMigration(t, ModeApply)
	require.NoError(t, err)
	before := snapshotTables(t)

	report, err := runMigration(t, ModeApply)
	require.NoError(t, err, "第二次 apply 必须退出码 0")
	require.Contains(t, report, "已完成")
	requireSameSnapshot(t, before, snapshotTables(t))
}

// ---------------------------------------------------------------------------
// §5.3.3 断点续跑
// ---------------------------------------------------------------------------

func TestApplyResumesAfterInterruptionWithoutDoubleScaling(t *testing.T) {
	// 基准：一次跑完
	dbClean := openTestDB(t)
	seedFixture(t, dbClean)
	seedManyLogs(t, dbClean, 11001)
	_, err := runMigration(t, ModeApply)
	require.NoError(t, err)
	baseline := snapshotTables(t)

	// 重来一遍，换一个干净的库：第 1 批后注入中断
	dbInterrupted := openTestDB(t)
	seedFixture(t, dbInterrupted)
	seedManyLogs(t, dbInterrupted, 11001)

	testHookAfterLogBatch = func(cursor int64) error {
		if cursor == logBatchSize {
			return fmt.Errorf("注入的中断")
		}
		return nil
	}
	_, err = runMigration(t, ModeApply)
	require.Error(t, err, "注入的中断必须让 apply 失败")

	state, found, err := loadState()
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, stateLogs, state.State)
	require.Equal(t, int64(logBatchSize), state.LogsCursor, "游标应停在第 1 批末尾")

	testHookAfterLogBatch = nil
	_, err = runMigration(t, ModeApply)
	require.NoError(t, err, "续跑必须成功")
	requireSameSnapshot(t, baseline, snapshotTables(t))
}

// seedManyLogs 造足够跨批的日志行（默认每批 5000，11001 行 → 3 批）。
func seedManyLogs(t *testing.T, db *gorm.DB, count int) {
	t.Helper()
	logs := make([]model.Log, 0, count)
	for id := 5; id <= count+4; id++ {
		logs = append(logs, model.Log{
			Id: id, UserId: 1, Type: model.LogTypeConsume, Quota: int(fQuota),
			PointsConsumed: int(fSmall), CreditConsumed: int(fPoints),
			Other:     fmt.Sprintf(`{"fee_quota":%d}`, fUsed),
			CreatedAt: int64(id),
		})
	}
	require.NoError(t, db.CreateInBatches(logs, 1000).Error)
}

// 首次 dry-run（状态 key 还不存在）也必须报出按库内实际最大 id 算出的批次数：
// 运维就是靠它核对「报告量级与生产快照一致」，报 0 批等于没这个信号。
func TestDryRunReportsLogBatchPlanWithoutStateKey(t *testing.T) {
	db := openTestDB(t)
	seedFixture(t, db)
	seedManyLogs(t, db, 11001)

	report, err := runMigration(t, ModeDryRun)
	require.NoError(t, err)
	require.Contains(t, report, "id (0, 11005]，预计 3 批", "dry-run 必须报出真实批次数")
}

// ---------------------------------------------------------------------------
// §5.3.4 拒绝执行
// ---------------------------------------------------------------------------

func TestRefusesWhenQuotaPerUnitAlreadyChanged(t *testing.T) {
	db := openTestDB(t)
	seedFixture(t, db)
	require.NoError(t, db.Model(&model.Option{}).
		Where(&model.Option{Key: "QuotaPerUnit"}).
		Update("value", "7000000").Error)
	before := snapshotTables(t)

	_, err := runMigration(t, ModeApply)
	require.Error(t, err)
	require.Contains(t, err.Error(), "拒绝执行")
	requireSameSnapshot(t, before, snapshotTables(t))
}

// ---------------------------------------------------------------------------
// §5.3.5 dry-run
// ---------------------------------------------------------------------------

func TestDryRunWritesNothingAndMatchesApply(t *testing.T) {
	db := openTestDB(t)
	seedFixture(t, db)
	before := snapshotTables(t)

	report, err := runMigration(t, ModeDryRun)
	require.NoError(t, err)
	require.Contains(t, report, "dry-run 结束")
	requireSameSnapshot(t, before, snapshotTables(t))

	// dry-run 报出的「迁移后合计」必须与 apply 之后的实际值一致
	wantSums := parseReportAfterSums(t, report, []string{
		"users.quota", "logs.quota", "logs.points_consumed", "top_ups.amount",
	})

	_, err = runMigration(t, ModeApply)
	require.NoError(t, err)

	gotState, _, err := loadState()
	require.NoError(t, err)
	require.Equal(t, stateDone, gotState.State)

	for path, wantAfter := range wantSums {
		table, column := splitPath(path)
		got := tableSum(t, db, table, column)
		require.Equal(t, wantAfter, got, "%s：dry-run 预计值与 apply 实际值不一致", path)
	}
}

// ---------------------------------------------------------------------------
// 快照与报告解析
// ---------------------------------------------------------------------------

var snapshotTableList = []string{
	"options", "users", "tokens", "channels", "logs", "tasks", "midjourneys",
	"quota_data", "redemptions", "checkins", "fund_entries", "bank_transfer_orders",
	"subscription_plans", "user_subscriptions", "subscription_pre_consume_records",
	"compute_point_lots", "top_ups",
}

// volatileColumns 在快照里跳过：它们由插入时刻决定，两次独立运行必然不同，
// 与「迁移有没有改动数据」无关。
var volatileColumns = map[string]bool{
	"created_at": true, "updated_at": true, "last_login_at": true, "accessed_time": true,
	"granted_at": true, "submitted_at": true, "reviewed_at": true, "complete_time": true,
	"create_time": true, "start_time": true, "finish_time": true, "expires_at": true,
}

// snapshotTables 返回「表 → 规范化后的全部行」。用于证明 dry-run / 幂等 / 续跑
// 没有改动该动的以外的任何东西。
func snapshotTables(t *testing.T) map[string]string {
	t.Helper()
	out := make(map[string]string, len(snapshotTableList))
	for _, table := range snapshotTableList {
		out[table] = dumpTable(t, table)
	}
	return out
}

func dumpTable(t *testing.T, table string) string {
	t.Helper()
	rows, err := model.DB.Raw("SELECT * FROM " + table + " ORDER BY 1").Rows()
	require.NoError(t, err)
	cols, err := rows.Columns()
	require.NoError(t, err)
	keep := make([]int, 0, len(cols))
	for i, c := range cols {
		if volatileColumns[c] {
			continue
		}
		keep = append(keep, i)
	}

	var b strings.Builder
	for rows.Next() {
		values := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		require.NoError(t, rows.Scan(ptrs...))
		cells := make([]string, 0, len(keep))
		for _, i := range keep {
			cell := cellString(values[i])
			if table == "options" && cols[i] == "value" {
				cell = normalizeOptionValue(t, values, cell)
			}
			cells = append(cells, cell)
		}
		b.WriteString(strings.Join(cells, "|"))
		b.WriteByte('\n')
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	return b.String()
}

func cellString(v interface{}) string {
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return fmt.Sprintf("%v", v)
}

// isMigrationStateKey 判断 options 的某个 key 是不是迁移进度键。
// V1 与 V2 都要命中：快照辅助函数会在两种方案的测试里复用。
func isMigrationStateKey(key string) bool {
	return key == planV1().stateKey || key == planV2().stateKey
}

// normalizeOptionValue 抹掉迁移状态里的时间戳：状态机的进度值参与比对，
// 但 started_at / finished_at 天然随运行时刻变化。
func normalizeOptionValue(t *testing.T, values []interface{}, cell string) string {	t.Helper()
	isStateKey := false
	for _, v := range values {
		if isMigrationStateKey(cellString(v)) {
			isStateKey = true
			break
		}
	}
	if !isStateKey {
		return cell
	}
	var state map[string]interface{}
	if err := common.UnmarshalJsonStr(cell, &state); err != nil {
		return cell
	}
	delete(state, "started_at")
	delete(state, "finished_at")
	normalized, err := common.Marshal(state)
	if err != nil {
		return cell
	}
	return string(normalized)
}

func requireSameSnapshot(t *testing.T, want, got map[string]string) {
	t.Helper()
	for table, wantDump := range want {
		gotDump, ok := got[table]
		require.True(t, ok, "快照缺少表 %s", table)
		if wantDump == gotDump {
			continue
		}
		t.Fatalf("%s 与期望不一致：\n%s", table, firstDifferingLines(wantDump, gotDump, 5))
	}
}

func firstDifferingLines(want, got string, limit int) string {
	wantLines := strings.Split(want, "\n")
	gotLines := strings.Split(got, "\n")
	var b strings.Builder
	shown := 0
	for i := 0; i < len(wantLines) || i < len(gotLines); i++ {
		var w, g string
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if i < len(gotLines) {
			g = gotLines[i]
		}
		if w == g {
			continue
		}
		fmt.Fprintf(&b, "  行%d\n    期望: %s\n    实际: %s\n", i+1, truncate(w, 400), truncate(g, 400))
		shown++
		if shown >= limit {
			fmt.Fprintf(&b, "  …（还有更多差异）\n")
			break
		}
	}
	return b.String()
}

func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "…"
}

// parseReportAfterSums 从报告里抠出「表.列 → 迁移后合计」，用于比对 dry-run 与 apply。
func parseReportAfterSums(t *testing.T, report string, wanted []string) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	for _, line := range strings.Split(report, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		for _, key := range wanted {
			if fields[0] != key {
				continue
			}
			v, err := strconv.ParseInt(fields[3], 10, 64)
			require.NoError(t, err, "解析 %s 的迁移后合计: %q", key, line)
			out[key] = v
		}
	}
	for _, key := range wanted {
		require.Contains(t, out, key, "报告里缺少 %s 的合计行", key)
	}
	return out
}

func splitPath(path string) (string, string) {
	idx := strings.LastIndex(path, ".")
	return path[:idx], path[idx+1:]
}

func tableSum(t *testing.T, db *gorm.DB, table, column string) int64 {
	t.Helper()
	sql := fmt.Sprintf("SELECT COALESCE(SUM(%s),0) FROM %s", column, table)
	for _, scope := range smallNumericScopes {
		if scope.table == table && scope.where != "" {
			sql += " WHERE " + scope.where
		}
	}
	var sum int64
	require.NoError(t, db.Raw(sql).Scan(&sum).Error)
	return sum
}

// ---------------------------------------------------------------------------
// §5.3.7 JSON 原样保留
// ---------------------------------------------------------------------------

// totalChanges 返回当前连接累计改动的行数（SQLite 内置函数）。测试库是单连接，
// 因此它可以直接用来证明「某些行确实没有被 UPDATE」。
func totalChanges(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Raw("SELECT total_changes()").Scan(&n).Error)
	return n
}

// decodeWithNumber 用 json.Number 解析，避免 float64 把 >2^53 的大整数抹平；
// 值类型保持 interface{}，因为同一对象里既有数字也有字符串 / null / 对象。
func decodeWithNumber(t *testing.T, raw string) map[string]interface{} {
	t.Helper()
	out := map[string]interface{}{}
	require.NoError(t, common.UnmarshalWithNumber([]byte(raw), &out))
	return out
}

// 不含目标键的行必须一个字节都不动，而且根本不发 UPDATE。
func TestJSONRewriteLeavesUntargetedRowsUntouched(t *testing.T) {
	db := openTestDB(t)
	seedFixture(t, db)

	const untouched = `{"zeta":1,"alpha":"<a>&中文","big":9007199254740993,"nested":{"quota_warning_threshold":1}}`
	require.NoError(t, db.Create(&model.User{
		Id: 10, Username: "u10", AffCode: "aff10", Setting: untouched,
	}).Error)

	before := totalChanges(t, db)
	stat, err := rewriteJSONRows(db, smallJSONScopes[1], "id = 10", true)
	require.NoError(t, err)
	require.Zero(t, stat.rewritten)
	require.Zero(t, stat.nonNumeric)
	require.Equal(t, int64(0), totalChanges(t, db)-before, "不含目标键的行不得被 UPDATE")

	var got string
	require.NoError(t, db.Raw("SELECT setting FROM users WHERE id = 10").Scan(&got).Error)
	require.Equal(t, untouched, got, "字节必须完全相同")
}

// 含目标键的行：只有目标键换值，其余键（含 >2^53 的大整数、中文与 <>&）含义不变；
// 目标键的值是 null / 字符串时不改，并计入报告。
func TestApplyPreservesUntargetedKeysAndHugeIntegers(t *testing.T) {
	db := openTestDB(t)
	seedFixture(t, db)

	const (
		untargeted = `{"zeta":1,"alpha":"<a>&中文","big":9007199254740993,"nested":{"quota_warning_threshold":1}}`
		targeted   = `{"quota_warning_threshold":1136,"big":9007199254740993,"s":"<x>&","n":null,"scalar":"1136","nested":{"quota_warning_threshold":1}}`
		nullValue  = `{"quota_warning_threshold":null,"big":9007199254740993}`
		stringVal  = `{"quota_warning_threshold":"1136","big":9007199254740993}`
	)
	require.NoError(t, db.Create(&model.User{Id: 10, Username: "u10", AffCode: "aff10", Setting: untargeted}).Error)
	require.NoError(t, db.Create(&model.User{Id: 11, Username: "u11", AffCode: "aff11", Setting: targeted}).Error)
	require.NoError(t, db.Create(&model.User{Id: 12, Username: "u12", AffCode: "aff12", Setting: nullValue}).Error)
	require.NoError(t, db.Create(&model.User{Id: 13, Username: "u13", AffCode: "aff13", Setting: stringVal}).Error)

	report, err := runMigration(t, ModeApply)
	require.NoError(t, err)

	settingOf := func(id int) string {
		var got string
		require.NoError(t, db.Raw("SELECT setting FROM users WHERE id = ?", id).Scan(&got).Error)
		return got
	}

	// 不含目标键：逐字节不变
	require.Equal(t, untargeted, settingOf(10), "不含目标键的行字节必须不变")

	// 含目标键：目标键换成整数，其余键含义完全相同
	after := decodeWithNumber(t, settingOf(11))
	require.Equal(t, json.Number("16586"), after["quota_warning_threshold"], "1136 × 14.6 = 16585.6 → 16586")
	require.Equal(t, json.Number("9007199254740993"), after["big"], ">2^53 的大整数必须逐位保留")
	// 原始字节层面的逐位比对（json.Number 只证明解析结果，这里证明落库文本本身）
	require.Equal(t, "9007199254740993", string(mustRaw(t, targeted)["big"]))
	require.Equal(t, "9007199254740993", string(mustRaw(t, settingOf(11))["big"]),
		">2^53 的大整数在落库文本里也必须逐位不变")

	before := decodeWithNumber(t, targeted)
	for _, key := range []string{"big", "n", "scalar"} {
		require.Equal(t, before[key], after[key], "非目标键 %s 不得变", key)
	}
	// nested.quota_warning_threshold 是嵌套的同名键，不在清单里
	require.Equal(t, `{"quota_warning_threshold":1}`, string(mustRaw(t, settingOf(11))["nested"]),
		"嵌套的同名键不在清单里，必须原样保留")
	require.Contains(t, settingOf(11), `"s":"\u003cx\u003e\u0026"`)

	// 目标键的值是 null / 字符串：不改写，且计入「值非数字未改」
	require.Equal(t, nullValue, settingOf(12))
	require.Equal(t, stringVal, settingOf(13))
	require.Contains(t, report, "值非数字未改")
	require.Contains(t, report, "users.setting")
}

// mustRaw 把顶层对象解成原始字节，供逐键比对。
func mustRaw(t *testing.T, raw string) map[string]json.RawMessage {
	t.Helper()
	out := map[string]json.RawMessage{}
	require.NoError(t, common.Unmarshal([]byte(raw), &out))
	return out
}

// splitReportLines / splitReportFields / mustInt 供报告解析使用（表格列以多空格对齐）。
func splitReportLines(report string) []string {
	return strings.Split(report, "\n")
}

func splitReportFields(line string) []string {
	return strings.Fields(line)
}

func mustInt(t *testing.T, s string) int64 {
	t.Helper()
	v, err := strconv.ParseInt(s, 10, 64)
	require.NoError(t, err, "解析 %q", s)
	return v
}
