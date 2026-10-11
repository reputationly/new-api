package quotaunitmigration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// V2 迁移（设计文档 §5.2 / §5.3 / §7.1）：quota ×1000 + 定价配置美元 → 人民币。
// 覆盖 SQLite 与外部 PostgreSQL / MySQL 三库（DSN 未设置时 Skip，与 V1 用例一致）。

func runMigrationV2(t *testing.T, mode string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	err := RunV2(mode, &buf)
	return buf.String(), err
}

// seedV2Fixture 造出「V1 已完成」的库：QuotaPerUnit 已是 7300000、状态 done、
// 定价配置仍是美元口径，等待 V2 换算。
func seedV2Fixture(t *testing.T, db *gorm.DB) {
	t.Helper()
	seedSmallTables(t, db)
	seedLogs(t, db)

	require.NoError(t, db.Model(&model.Option{}).
		Where(&model.Option{Key: "QuotaPerUnit"}).Update("value", "7300000").Error)
	seedOption(t, db, planV1().stateKey, `{"state":"done"}`)

	// 定价配置（美元口径）：倍率 × 14.6、价格 × 7.3、-1 哨兵、未知结构。
	seedOption(t, db, "ModelRatio", `{"m1":0.13698630137,"m2":1,"m3":0}`)
	seedOption(t, db, "ModelPrice", `{"p1":0.41095890411,"p2":-1}`)
	seedOption(t, db, "VideoPricingConfig",
		`{"v-sec":{"mode":"per_second","per_second":{"2K":0.001369863014},"note":"keep-structure"}}`)
	seedOption(t, db, "tool_price_setting.prices", `{"web_search":10,"file_search":2.5}`)
	seedOption(t, db, "grok.violation_deduction_amount", "0.05")
	// 分段表达式：都是 v2。库里若有 v1，apply 会被前置检查拒绝（见
	// TestV2ApplyRejectsV1Expressions），所以这份供三库 apply 复用的 fixture 不放 v1。
	seedOption(t, db, "billing_setting.billing_expr", `{"m1":"v2:p * 1.5","m2":"v2:p * 2"}`)

	// 冻结了完整计费上下文的已完成任务（V2 要把定价字段换成人民币并打 price_unit）
	require.NoError(t, db.Create(&model.Task{
		ID: 2, TaskID: "v2-task-2", UserId: 1, ChannelId: 1, Quota: 100,
		Status: model.TaskStatus(model.TaskStatusSuccess),
		PrivateData: model.TaskPrivateData{
			PointsConsumed:   7,
			EntitlementSpent: []model.ComputePointSpend{{LotId: 1, Amount: 50}},
			BillingContext: &model.TaskBillingContext{
				GroupRatio:      1,
				OriginModelName: "m1",
				ModelPrice:      0.04,
				ModelRatio:      0.13698630137,
				VideoBilling: &model.TaskVideoBilling{
					Mode: "token", UnitPrice: 0.068493150685, Resolution: "720p",
				},
			},
			TokenName: "keep-me-v2",
		},
	}).Error)
}

// rawTaskPrivateData 读原始 JSON 文本再解析：TaskPrivateData.Scan 只接 []byte，
// 部分驱动把 TEXT 回成 string，直接读结构体会是零值。
func rawTaskPrivateData(t *testing.T, db *gorm.DB, id int) map[string]interface{} {
	t.Helper()
	var raw string
	require.NoError(t, db.Raw("SELECT private_data FROM tasks WHERE id = ?", id).Scan(&raw).Error)
	out := map[string]interface{}{}
	require.NoError(t, common.UnmarshalJsonStr(raw, &out))
	return out
}

// assertV2Pricing 断言 §5.3 的全部定价换算。
func assertV2Pricing(t *testing.T, db *gorm.DB, report string) {
	t.Helper()

	// ModelRatio：×14.6，保留 6 位小数
	raw, found := optionValue(t, "ModelRatio")
	require.True(t, found)
	ratios := map[string]float64{}
	require.NoError(t, common.UnmarshalJsonStr(raw, &ratios))
	require.InDelta(t, 2, ratios["m1"], 1e-9, "0.13698630137 × 14.6 = 2（还原运营填的 ¥2/百万）")
	require.InDelta(t, 14.6, ratios["m2"], 1e-9)
	require.Equal(t, 0.0, ratios["m3"])

	// ModelPrice：×7.3；-1 哨兵不换算
	raw, found = optionValue(t, "ModelPrice")
	require.True(t, found)
	prices := map[string]float64{}
	require.NoError(t, common.UnmarshalJsonStr(raw, &prices))
	require.InDelta(t, 3, prices["p1"], 1e-9, "0.41095890411 × 7.3 = 3")
	require.Equal(t, -1.0, prices["p2"], "负值哨兵（按量计费）必须原样保留")

	// VideoPricingConfig：所有价格格子 ×7.3，结构与未知键原样保留
	raw, found = optionValue(t, "VideoPricingConfig")
	require.True(t, found)
	var video map[string]map[string]interface{}
	require.NoError(t, common.UnmarshalJsonStr(raw, &video))
	perSecond := video["v-sec"]["per_second"].(map[string]interface{})
	require.InDelta(t, 0.01, perSecond["2K"].(float64), 1e-9, "0.001369863014 × 7.3 = 0.01")
	require.Equal(t, "keep-structure", video["v-sec"]["note"], "未知结构与键名必须原样保留")
	require.Equal(t, "per_second", video["v-sec"]["mode"], "非价格字段原样保留")

	// tool_price_setting.prices：×7.3
	raw, found = optionValue(t, "tool_price_setting.prices")
	require.True(t, found)
	tools := map[string]float64{}
	require.NoError(t, common.UnmarshalJsonStr(raw, &tools))
	require.InDelta(t, 73, tools["web_search"], 1e-9, "原 $10 → ¥73")
	require.InDelta(t, 18.25, tools["file_search"], 1e-9, "原 $2.5 → ¥18.25")

	// grok.violation_deduction_amount：×7.3
	raw, found = optionValue(t, "grok.violation_deduction_amount")
	require.True(t, found)
	require.Equal(t, "0.365", raw, "原 $0.05 → ¥0.365")

	// 全部任务（含已完成）：quota 键 ×1000 + 定价字段换人民币 + price_unit
	private := rawTaskPrivateData(t, db, 2)
	require.Equal(t, float64(7000), private["points_consumed"], "7 × 1000")
	spent := private["entitlement_spent"].([]interface{})
	require.Equal(t, float64(50000), spent[0].(map[string]interface{})["amount"], "50 × 1000")
	require.Equal(t, "keep-me-v2", private["token_name"], "无关字段原样保留")

	bc := private["billing_context"].(map[string]interface{})
	require.InDelta(t, 0.292, bc["model_price"].(float64), 1e-9, "0.04 × 7.3")
	require.InDelta(t, 2, bc["model_ratio"].(float64), 1e-9, "0.13698630137 × 14.6")
	require.Equal(t, "cny", bc["price_unit"], "必须打上人民币标记（§2.6）")
	vb := bc["video_billing"].(map[string]interface{})
	require.InDelta(t, 0.5, vb["unit_price"].(float64), 1e-9, "0.068493150685 × 7.3")
	require.Equal(t, "token", vb["mode"], "非价格字段原样保留")
	require.Equal(t, "720p", vb["resolution"])

	// tasks.quota ×1000
	var taskQuota int64
	require.NoError(t, db.Raw("SELECT quota FROM tasks WHERE id = 2").Scan(&taskQuota).Error)
	require.Equal(t, int64(100000), taskQuota, "100 × 1000")

	// 表达式原文不得改动（D3）；这份 fixture 里都是 v2，所以没有 v1 告警
	raw, found = optionValue(t, "billing_setting.billing_expr")
	require.True(t, found)
	require.JSONEq(t, `{"m1":"v2:p * 1.5","m2":"v2:p * 2"}`, raw, "表达式原文不得改动（D3）")
	require.NotContains(t, report, "仍是 v1 口径的分段表达式")

	// options：quota 类 ×1000、换算率与 QuotaPerUnit 写固定值
	for key, want := range map[string]string{
		"QuotaPerUnit":                                  "7300000000",
		"QuotaRemindThreshold":                          "1000000",
		"checkin_setting.min_quota":                     "100",
		"checkin_setting.max_quota":                     "10000000",
		"points_setting.quota_per_point":                "10000000",
		"compute_point_setting.quota_per_compute_point": "10000000",
		"PreConsumedQuota":                              "500", // token 下限，不缩放
	} {
		got, found := optionValue(t, key)
		require.Truef(t, found, "options.%s 应存在", key)
		require.Equalf(t, want, got, "options.%s", key)
	}
	for _, key := range []string{"QuotaForInviter", "QuotaForInvitee"} {
		_, found := optionValue(t, key)
		require.Falsef(t, found, "options.%s 不在缺失写入清单里", key)
	}
}

// V2 的 dry-run / apply / 幂等：SQLite + 外部 PG / MySQL 三库共用同一份断言。
func TestV2MigrationAcrossDatabases(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) {
		db := openTestDB(t)
		seedV2Fixture(t, db)
		runV2DryRunApplyIdempotent(t, db)
	})

	for _, target := range externalTargets() {
		t.Run(target.name, func(t *testing.T) {
			dsn := os.Getenv(target.envKey)
			if dsn == "" {
				t.Skipf("未设置 %s，跳过 %s 用例", target.envKey, target.name)
			}
			db := openExternalDB(t, target, dsn)
			seedV2Fixture(t, db)
			runV2DryRunApplyIdempotent(t, db)
		})
	}
}

func runV2DryRunApplyIdempotent(t *testing.T, db *gorm.DB) {
	t.Helper()

	// dry-run：一个字节都不写，但报告要含定价换算与 v1 表达式告警
	before := snapshotTables(t)
	dryReport, err := runMigrationV2(t, ModeDryRun)
	require.NoError(t, err, "V2 dry-run 失败")
	require.Contains(t, dryReport, "dry-run 结束")
	require.Contains(t, dryReport, "[定价配置]")
	require.NotContains(t, dryReport, "仍是 v1 口径的分段表达式", "这份 fixture 没有 v1 表达式")
	require.Contains(t, dryReport, "倍率 ×14.6")
	requireSameSnapshot(t, before, snapshotTables(t))

	// dry-run 不能改动定价配置
	raw, _ := optionValue(t, "ModelRatio")
	require.Contains(t, raw, "0.13698630137", "dry-run 不得写库")

	// apply
	report, err := runMigrationV2(t, ModeApply)
	require.NoError(t, err, "V2 apply 失败")
	assertV2Pricing(t, db, report)

	// 幂等
	after := snapshotTables(t)
	second, err := runMigrationV2(t, ModeApply)
	require.NoError(t, err, "第二次 apply 必须退出码 0")
	require.Contains(t, second, "已完成")
	requireSameSnapshot(t, after, snapshotTables(t))
}

// 前置条件不满足即拒绝执行：V1 未完成、汇率不是 7.3。
func TestV2PrerequisitesRejected(t *testing.T) {
	assertRejects := func(t *testing.T, db *gorm.DB, want string) {
		t.Helper()
		before := snapshotTables(t)
		_, err := runMigrationV2(t, ModeApply)
		require.Error(t, err, "前置条件不满足必须拒绝执行")
		require.Contains(t, err.Error(), want)
		requireSameSnapshot(t, before, snapshotTables(t))
	}

	t.Run("sqlite-v1-not-done", func(t *testing.T) {
		db := openTestDB(t)
		seedV2Fixture(t, db)
		require.NoError(t, db.Model(&model.Option{}).
			Where(&model.Option{Key: planV1().stateKey}).Update("value", `{"state":"logs"}`).Error)
		assertRejects(t, db, "前置条件不满足")
	})

	t.Run("sqlite-rate-not-7.3", func(t *testing.T) {
		db := openTestDB(t)
		seedV2Fixture(t, db)
		require.NoError(t, db.Model(&model.Option{}).
			Where(&model.Option{Key: "USDExchangeRate"}).Update("value", "7.0").Error)
		assertRejects(t, db, "前置条件不满足")
	})

	for _, target := range externalTargets() {
		t.Run("external-"+target.name, func(t *testing.T) {
			dsn := os.Getenv(target.envKey)
			if dsn == "" {
				t.Skipf("未设置 %s，跳过 %s 用例", target.envKey, target.name)
			}
			db := openExternalDB(t, target, dsn)
			seedV2Fixture(t, db)
			require.NoError(t, db.Model(&model.Option{}).
				Where(&model.Option{Key: planV1().stateKey}).Update("value", `{"state":"logs"}`).Error)
			assertRejects(t, db, "前置条件不满足")
		})
	}
}

// 定价 JSON 解析失败必须拒绝执行（§5.4）：写坏的定价表会让所有模型价格失真。
func TestV2RejectsUnparsablePricingJSON(t *testing.T) {
	db := openTestDB(t)
	seedV2Fixture(t, db)
	require.NoError(t, db.Model(&model.Option{}).
		Where(&model.Option{Key: "ModelRatio"}).Update("value", "not json").Error)

	before := snapshotTables(t)
	_, err := runMigrationV2(t, ModeApply)
	require.Error(t, err)
	require.Contains(t, err.Error(), "ModelRatio")
	requireSameSnapshot(t, before, snapshotTables(t))
}

// 断点续跑：第 1 批日志后中断，游标停在该批末尾；续跑后与一次跑完的结果一致，
// 且定价配置不会被重复换算。
func TestV2ResumeAfterInterruption(t *testing.T) {
	// 基准：一次跑完
	dbClean := openTestDB(t)
	seedV2Fixture(t, dbClean)
	seedManyLogs(t, dbClean, 11001)
	_, err := runMigrationV2(t, ModeApply)
	require.NoError(t, err)
	baseline := snapshotTables(t)

	// 中断一轮
	dbInterrupted := openTestDB(t)
	seedV2Fixture(t, dbInterrupted)
	seedManyLogs(t, dbInterrupted, 11001)

	testHookAfterLogBatch = func(cursor int64) error {
		if cursor == logBatchSize {
			return fmt.Errorf("注入的中断")
		}
		return nil
	}
	_, err = runMigrationV2(t, ModeApply)
	require.Error(t, err, "注入的中断必须让 apply 失败")

	state, found, err := loadStateFor(planV2().stateKey)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, stateLogs, state.State)
	require.Equal(t, int64(logBatchSize), state.LogsCursor, "游标应停在第 1 批末尾")

	// 续跑前定价已经换算过一次：再跑一遍不得重复 ×14.6
	raw, _ := optionValue(t, "ModelRatio")
	ratios := map[string]float64{}
	require.NoError(t, common.UnmarshalJsonStr(raw, &ratios))
	require.InDelta(t, 2, ratios["m1"], 1e-9)

	testHookAfterLogBatch = nil
	_, err = runMigrationV2(t, ModeApply)
	require.NoError(t, err, "续跑必须成功")
	requireSameSnapshot(t, baseline, snapshotTables(t))
}

// 独立日志库：小表与定价在主库、logs 在日志库，游标仍写主库。
func TestV2SeparateLogDatabase(t *testing.T) {
	mainDB, logDB := openSplitTestDB(t)
	seedSmallTables(t, mainDB)
	seedLogs(t, logDB)
	seedManyLogs(t, logDB, 11001)

	require.NoError(t, mainDB.Model(&model.Option{}).
		Where(&model.Option{Key: "QuotaPerUnit"}).Update("value", "7300000").Error)
	seedOption(t, mainDB, planV1().stateKey, `{"state":"done"}`)
	seedOption(t, mainDB, "ModelRatio", `{"m1":0.13698630137}`)

	// dry-run 的预检不能因为主库没有 logs 表而失败
	dryReport, err := runMigrationV2(t, ModeDryRun)
	require.NoError(t, err, "主库没有 logs 表不应导致预检失败")
	require.Contains(t, dryReport, "id (0, 11005]，预计 3 批")

	report, err := runMigrationV2(t, ModeApply)
	require.NoError(t, err)
	require.Contains(t, report, "apply 结束")

	// 日志在日志库里被 ×1000
	var scaledLog int64
	require.NoError(t, logDB.Raw("SELECT quota FROM logs WHERE id = 1").Scan(&scaledLog).Error)
	require.Equal(t, fQuota*1000, scaledLog)

	// 小表与定价在主库
	var userQuota int64
	require.NoError(t, mainDB.Raw("SELECT quota FROM users WHERE id = 1").Scan(&userQuota).Error)
	require.Equal(t, fQuota*1000, userQuota)
	raw, _ := optionValue(t, "ModelRatio")
	require.Contains(t, raw, "2")

	// 游标与终态在主库
	state, found, err := loadStateFor(planV2().stateKey)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, stateDone, state.State)
	require.Equal(t, int64(11005), state.LogsCursor)
}

// ---- B9 启动保护 ----

func TestEnsurePriceUnitReadyEmptyDatabase(t *testing.T) {
	_ = openTestDB(t)

	require.NoError(t, EnsurePriceUnitReady(), "空库必须放行")
	state, found, err := loadStateFor(planV2().stateKey)
	require.NoError(t, err)
	require.True(t, found, "空库应直接写入 done")
	require.Equal(t, stateDone, state.State)
}

func TestEnsurePriceUnitReadyRefusesWhenUnmigratedWithUsers(t *testing.T) {
	db := openTestDB(t)
	require.NoError(t, db.Create(&model.User{Id: 1, Username: "u1", AffCode: "aff1"}).Error)

	err := EnsurePriceUnitReady()
	require.Error(t, err, "未迁移且 users 非空 → 整进程拒绝启动")
	require.Contains(t, err.Error(), "拒绝启动")
	require.Contains(t, err.Error(), "--"+planV2().flag+"=dry-run")
	require.Contains(t, err.Error(), "--"+planV2().flag+"=apply")
	require.Contains(t, err.Error(), "恢复第 1 步的备份")
}

func TestEnsurePriceUnitReadyAllowsWhenDone(t *testing.T) {
	db := openTestDB(t)
	require.NoError(t, db.Create(&model.User{Id: 1, Username: "u1", AffCode: "aff1"}).Error)
	seedOption(t, db, planV2().stateKey, `{"state":"done"}`)

	require.NoError(t, EnsurePriceUnitReady(), "state = done 时必须放行")
}

// 校验规模：V2 的倍数是整数 1000，SQL 直接写 col * 1000（三库精确），不带 ROUND。
func TestV2SQLScalingIsInteger(t *testing.T) {
	sql := planV2().sqlScale("quota")
	require.Equal(t, "quota * 1000", sql)
	require.Equal(t, "1000", planV2().scaleLabel())
	require.Equal(t, int64(1000), planV2().scaleValue(1))
	require.Equal(t, int64(400), planV2().scaleValue(0.4), "0.4 × 1000 = 400")
}

// apply 遇到 v1（美元口径）分段表达式必须拒绝：迁移后它们在预扣阶段会被拒
// （ErrExprV1Unsupported），相关模型会整体不可用，不能等线上报错才发现。
func TestV2ApplyRejectsV1Expressions(t *testing.T) {
	db := openTestDB(t)
	seedV2Fixture(t, db)
	// 覆盖 fixture 里的表达式：塞一条 v1（无前缀）进去
	require.NoError(t, db.Model(&model.Option{}).
		Where(&model.Option{Key: "billing_setting.billing_expr"}).
		Update("value", `{"m1":"p * 1.5","m2":"v2:p * 2"}`).Error)

	// dry-run 只报告、不报错
	dryReport, err := runMigrationV2(t, ModeDryRun)
	require.NoError(t, err, "dry-run 不应因为 v1 表达式失败")
	require.Contains(t, dryReport, "m1 = p * 1.5", "dry-run 要报告 v1 表达式")

	// apply 拒绝，且一个字节都不写
	before := snapshotTables(t)
	_, err = runMigrationV2(t, ModeApply)
	require.Error(t, err)
	require.Contains(t, err.Error(), "v1（美元口径）分段表达式")
	require.Contains(t, err.Error(), "m1 = p * 1.5")
	require.Contains(t, err.Error(), "重录为 v2")
	requireSameSnapshot(t, before, snapshotTables(t))

	ratios, _ := optionValue(t, "ModelRatio")
	require.Contains(t, ratios, "0.13698630137", "拒绝后定价配置不得被改写")
	_, found := optionValue(t, planV2().stateKey)
	require.False(t, found, "拒绝后不得写入 V2 状态")
}

// 行级幂等：同一行任务数据被改写两次，值不变。
func TestV2TaskRewriteIsRowIdempotent(t *testing.T) {
	db := openTestDB(t)
	seedV2Fixture(t, db)
	_, err := runMigrationV2(t, ModeApply)
	require.NoError(t, err)

	// apply 之后这一行已带 price_unit，再跑一次改写函数必须整行跳过
	migrated := rawTaskPrivateData(t, db, 2)
	encoded, err := common.Marshal(migrated)
	require.NoError(t, err)
	fields := map[string]json.RawMessage{}
	require.NoError(t, common.Unmarshal(encoded, &fields))

	require.False(t, rewriteTaskPrivateDataV2(fields, &jsonStat{}), "已有 price_unit 的行必须整行跳过")

	reencoded, err := common.Marshal(fields)
	require.NoError(t, err)
	require.JSONEq(t, string(encoded), string(reencoded), "二次改写不得改变任何字段")
}

// 读回校验发现异常值（负的倍率）时整个事务回滚，库里必须保持原样。
func TestV2ReadBackVerificationFailureRollsBack(t *testing.T) {
	db := openTestDB(t)
	seedV2Fixture(t, db)
	// 覆盖 fixture 里的 ModelRatio：负值在换算后仍为负，读回校验必须拦下
	require.NoError(t, db.Model(&model.Option{}).
		Where(&model.Option{Key: "ModelRatio"}).Update("value", `{"m1":-5}`).Error)

	before := snapshotTables(t)
	_, err := runMigrationV2(t, ModeApply)
	require.Error(t, err)
	require.Contains(t, err.Error(), "读回校验失败")
	requireSameSnapshot(t, before, snapshotTables(t))

	raw, _ := optionValue(t, "ModelRatio")
	require.JSONEq(t, `{"m1":-5}`, raw, "回滚后定价配置必须是原值")
	_, found := optionValue(t, planV2().stateKey)
	require.False(t, found, "回滚后不得写入 V2 状态")
}
