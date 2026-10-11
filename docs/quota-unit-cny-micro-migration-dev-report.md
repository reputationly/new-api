# 额度单位改造：开发交付报告

> 对应任务书：[quota-unit-cny-micro-migration.md](./quota-unit-cny-micro-migration.md)（§6 交付物清单）
> 范围：§4 B1–B6、F1–F2、M1 已实现并通过自测。**未执行 §8 生产升级**，未在任何真实库上 apply。

---

## 1. 改动清单（§6.1）

### 1.1 后端：计费路径

| 任务 | 文件 | 函数 / 符号 |
|---|---|---|
| B1 | `common/constants.go` | 新增 `RatioQuotaBase`、`QuotaScale()` |
| B2 | `service/text_quota.go` | `calculateTextQuotaSummary`：`ratio := dModelRatio.Mul(dGroupRatio).Mul(dQuotaScale)` |
| B2 | `service/quota.go` | `calculateAudioQuota` 倍率分支：`ratio` 乘 `QuotaScale()` |
| B2 | `relay/helper/price.go` | `ModelPriceHelper` 倍率预扣：`ratio := modelRatio * groupRatioInfo.GroupRatio * common.QuotaScale()` |
| B2 | `service/task_billing.go` | `RecalculateTaskQuotaByTokens`：`actualQuota` 乘 `QuotaScale()` |
| B2 | `controller/channel-test.go` | `settleTestQuota` 倍率分支：乘 `QuotaScale()` |
| B3 | `service/quota.go` | `CalcOpenRouterCacheCreateTokens`：`ModelRatio / common.RatioQuotaBase` |
| B4 | `service/quota.go` | `calculateAudioQuota` UsePrice 分支：`IntPart()` → `Round(0).IntPart()` |
| B4 | `relay/helper/price.go` | `ModelPriceHelperPerCall` 两个分支、`ModelPriceHelper` UsePrice 与倍率预扣：`math.Round` |
| B4 | `relay/video_billing.go` | `applyVideoPricing` per_call / per_second 两处 `math.Round` |
| B4 | `service/task_billing.go` | `RecalculateTaskQuotaByTokens`、`RecalculateTaskQuotaByVideoMatrix`：`math.Round` |
| B4 | `relay/relay_task.go` | `recalcQuotaFromRatios`：除、乘两处 `math.Round` |
| B4 | `controller/channel-test.go` | `settleTestQuota` 按次分支：`math.Round` |
| B4 | `service/entitlement_cost.go` | `estimateModelCost`：`int64(math.Round(...))` |
| B4.1 | `model/bank_transfer.go` | `BankTransferQuotaForFen`：改成先乘 QPU 再除 |
| B5 | `common/points.go` | 删掉初始化期冻结的 `QuotaPerPoint`；新增 `DefaultQuotaPerPoint()`、`GetQuotaPerPoint()` |
| B5 | `common/compute_points.go` | 同上（算力点） |
| B5 | `setting/operation_setting/points_setting.go` | 默认值 `684.93` → `QuotaPerPointAuto = 0`（自动） |
| B5 | `setting/operation_setting/compute_point_setting.go` | 默认值 `684.93` → `QuotaPerComputePointAuto = 0`（自动） |
| B5 | `controller/misc.go` | `/api/status` 改用 `common.GetQuotaPerPoint()` / `GetQuotaPerComputePoint()` |
| B5 | `controller/pricing.go` | 同上 |
| B6 | `model/topup.go` | `ManualCompleteTopUp`：按 `payment_provider` 分三类处理 `Amount` / `Money` |

### 1.2 后端：迁移程序（新包 `service/quotaunitmigration/`）

| 文件 | 内容 |
|---|---|
| `migration.go` | 入口 `Run(mode, out)`、状态机、小表单事务、logs 分批、dry-run/apply 报告、完整 id 清单落盘 |
| `scope.go` | 「改什么」清单：`smallNumericScopes` / `logsNumericColumns` / `jsonScope` / `optionSpecs` |
| `precheck.go` | M1.0 列类型预检（PG/MySQL 查 `information_schema`，SQLite 跳过）+ 溢出保险 + 方言 DDL |
| `rewrite.go` | JSON 键改写（`map[string]json.RawMessage` 逐层手术，不经过 float64） |
| `report.go` | 报告与对齐表格、人民币对照、id 清单路径 |
| `common/init.go` | 新增 `--migrate-quota-unit` flag |
| `main.go` | `InitResources()` 之后、起 HTTP/定时任务之前接管：跑迁移并退出 |

### 1.3 前端

| 任务 | 文件 | 改动 |
|---|---|---|
| F1 | `web/classic/src/helpers/quota.js` | fallback 收敛到一处（`FALLBACK_QUOTA_PER_UNIT / _POINT`）、读不到时 `console.warn` 一次、新增 `quotaPerPointHintText()` |
| F1 | `helpers/__tests__/quotaFallback.test.js`（新） | fallback 与告警行为（QPU 500000 / 7300000） |
| F1 | `pages/BankTransfer/TransferTab.jsx` | `parseFloat(localStorage…) \|\| 500000` → `getQuotaPerUnit()` |
| F1 | `pages/Setting/Ratio/components/TieredPricingEditor.jsx` | 同上 |
| F1 | `components/settings/PersonalSetting.jsx` | 默认预警阈值 → `getQuotaPerUnit()` |
| F1 | `components/table/tokens/modals/EditTokenModal.jsx`、`table/users/modals/EditUserModal.jsx` | `step={500000}` → `step={getQuotaPerUnit()}` |
| F1 | `components/settings/personal/cards/NotificationSettings.jsx` | 阈值预设按「人民币金额 ÷ 汇率 × quota_per_unit」生成，标签跟随展示币种 |
| F1 | `pages/Setting/Operation/SettingsGeneral.jsx` | 去掉 `!== 500000` 哨兵判断；更新 QuotaPerUnit 文案 |
| F1 | `components/settings/OperationSetting.jsx`、`pages/Setting/Operation/SettingsPoints.jsx` | `points_setting.quota_per_point` 表单初值 `684.93` → `0`（0 = 自动）；换算率输入框文案改为显示当前生效值 |
| F2 | `web/classic/src/helpers/render.jsx` | 新增 `roundToDisplay()`、`formatBillingDisplayTotal()`；5 处合计改用它们 |
| F2 | `helpers/__tests__/billingTotalRounding.test.jsx`（新） | `roundToDisplay` 单测 + F2.1 逐项 + 20 组随机用例 + `.5` 边界 |

### 1.4 测试（新增文件）

`common/quota_scale_test.go`、`common/points_scale_test.go`、`service/quota_scale_test.go`、
`service/task_billing_scale_test.go`、`service/entitlement_cost_scale_test.go`、
`relay/helper/price_scale_test.go`、`relay/relay_task_test.go`、
`controller/channel_test_scale_test.go`、`model/bank_transfer_quota_test.go`、
`model/topup_manual_complete_test.go`、`service/quotaunitmigration/{migration,rewrite,migration_external}_test.go`。

**既有测试的改动（两处，均在任务书授权范围内）**：

- `common/compute_points_test.go`：`TestComputePoints_ModelCostUnaffectedByExchangeRate` 改写为 `TestComputePoints_RateAndQuotaPerUnitInvariant`（B5 的语义决定，见 §8.1）。
- `service/task_billing_video_test.go`：期望值公式由 `int(...)` 同步为 `int(math.Round(...))`（B4 明确列出的视频矩阵路径）；`TestQuotaTruncationBoundIsNegligible` 更名 `TestQuotaRoundingBoundIsNegligible`，断言由「只少收」改为「|偏差| ≤ 1 quota」。

### 1.5 验收反馈的修复（评审后追加）

| 级别 | 问题 | 修法 | 回归测试 |
|---|---|---|---|
| 阻塞 | `rewriteJSONRows` 按 id 分页时用「过滤掉空行后的行数」判断页满与页尾，遇到空 `other` 就提前收尾 / 游标停在空格子上，大批日志漏迁移（实测 2000 行漏 1286 / 1301） | 游标取本页**实际读到的最后一行 id**（含空行），是否读完用**本页原始行数**判断，空行只跳过改写不跳过游标 | 验收方提供的 `empty_json_paging_test.go`（两种空行分布）+ 既有 `TestJSONRewriteLeavesUntargetedRowsUntouched` |
| 必须 | apply 报告的 logs「迁移前合计」在分批改写**之后**才采集，与「迁移后」相同（样例里 `logs.quota` 前后都是 438000，换算 ¥6.39 会误导核对） | 前值改为改写之前采集；断点续跑时前值不可得，表格标 `—` 并在表下注明原因与「迁移后」列的含义 | `TestApplyReportShowsRealBeforeSums`、`TestApplyReportMarksBeforeSumUnknownOnResume`、`TestSeparateLogDatabaseMigration` |
| 必须 | M1.0 预检固定查 `model.DB`，配置 `LOG_SQL_DSN` 时主库没有 `logs` 表，会误报「不是 64 位整数」并拒绝执行 | 新增 `dbForTable`：`logs` 走 `model.LOG_DB`，其余走 `model.DB`；列类型与最大值检查都按它选库 | `TestSeparateLogDatabaseMigration`（主库无 logs 表 + 独立日志库，dry-run 不报错、apply 跨库正确、游标仍写主库、幂等） |
| 小 | 生产代码注释里 8 处引用 `docs/quota-unit-cny-micro-migration.md`，而 `.gitignore` 忽略了整个 `docs/`，注释指向的文件默认不在仓库里 | 生产代码与测试代码里的文档引用全部删除，只保留约束本身 | `grep -rn "quota-unit-cny-micro-migration" --include=*.go .` 为空 |
| 小 | `NotificationSettings.jsx` 里 `handleFormChange('upstreamModelUpdateNotifyEnabled', …)` 被无关重排成多行 | 还原为原来的单行写法（`git show HEAD:… \| prettier --check` 确认该文件在改动前就不是 prettier-clean，还原即与仓库既有风格一致；`quota.js` 改动前是干净的，所以那里的新增按 prettier 排过版） | 前端全量测试 84 文件 / 658 用例 |
| — | `roundToDisplay` 的浮点容差只有 1 个 `Number.EPSILON`（评审确认生产倍率按 `toFixed(12)` 存储、偏差远大于浮点噪声，可接受） | 不改 | — |

---

## 2. 任务对照表（§6.2）

| 任务 | 完成标准 | 对应测试 | 结果 |
|---|---|---|---|
| B1 | QPU 500000 → 1；7300000 → 14.6 | `common.TestQuotaScale`；`TestRatioQuotaBaseMatchesTwoDollarsPerMillion` | ✅ |
| B2 | 回归样例 1136 / 16583 | `service.TestCalculateTextQuotaSummaryReporterSample` | ✅ |
| B2 | 每处一组双 QPU 单测 | `TestCalculateTextQuotaSummaryRatioPathScale`（7 场景）、`TestCalculateAudioQuotaRatioPathScale`、`TestRecalculateTaskQuotaByTokensScalesRatioPath`、`TestSettleTestQuotaRatioPathScalesWithQuotaPerUnit`、`relay/helper.TestModelPriceHelperPreConsumeScalesRatioNotTokenFloor` | ✅ |
| B2 | 文本路径覆盖：纯文本/缓存命中/缓存创建 5m+1h/图片/`OtherRatios`/Claude 语义/兜底 1 | `TestCalculateTextQuotaSummaryRatioPathScale` + `TestCalculateTextQuotaSummaryMinimumOneQuotaSurvivesScale` | ✅（音频 token 的分档见 §8.3） |
| B2 | 预扣：prompt 100 < PreConsumedQuota，两种单位人民币冻结额相同 | `TestModelPriceHelperPreConsumeScalesRatioNotTokenFloor`（下限仍是 500 tokens，只倍率乘 scale） | ✅ |
| B2 | 价格路径不得乘 scale | `TestCalculateTextQuotaSummaryPricePathDoesNotScale`、`TestModelPriceHelperPerCallRatioFallbackDoesNotDoubleScale` | ✅ |
| B3 | QPU 7300000 与 500000 结果完全相同 | `TestCalcOpenRouterCacheCreateTokensIgnoresQuotaPerUnit`（顺带钉死 3000 的换算值） | ✅ |
| B4 | 每处构造 `N − 1e-9` 输入得 `N` | `TestCalculateAudioQuotaUsePriceRoundsUp`、`TestModelPriceHelperPerCallRoundsPerCallPrice`、`TestSettleTestQuotaPerCallRoundsUp`、`TestEstimateModelCostRoundsUnitQuota`、`TestApplyVideoPricing_PerCallRoundsUp`、`TestRecalcQuotaFromRatiosRounds`（均断言 100000 而非 99999） | ✅ |
| B4.1 | `BankTransferQuotaForFen(100)=1000000`、`(1)=10000`、`(12345)=123450000`；旧单位不变 | `model.TestBankTransferQuotaForFenMultipliesBeforeDividing`、`TestBankTransferQuotaForFenRejectsInvalidRate` | ✅ |
| B5 | 500000/7.3 → 684.93…；7300000/7.3 → 10000；配置值优先 | `common.TestQuotaPerPointFollowsRuntimeConfig`、`TestGetQuotaPerPointPrefersConfiguredValue`、`TestConfiguredPointRateIsIndependentOfExchangeRate`（配置值路径，汇率 7.3 → 8.5 不变） | ✅ |
| B5 | `YuanToComputePoints(¥1)=100` 两种 QPU 下都成立 | 同上两个测试 | ✅ |
| B5 | `/api/status` 生产配置下与现在相同 | §5.4 端到端第 6 步（PG 实测 `quota_per_unit=7300000`、`quota_per_point=quota_per_compute_point=10000`）；旧单位下的等价断言见 `TestQuotaPerPointFollowsRuntimeConfig` | ✅（迁移前 684.93 的实测见 §4） |
| B6 | 每个 provider 一条单测，与自动回调一致 | `model.TestManualCompleteTopUpMatchesProviderSemantics`（8 个 provider + 幂等重放） | ✅ |
| F1 | QPU 500000 / 7300000 下组件与函数测试 | `quotaFallback.test.js`(7)、`warningThresholdPresets.test.js`(4)、`quotaPerPointHint.test.js`(3)；500000 下既有 84 文件 658 用例全通过 | ✅ |
| F2 | `roundToDisplay` 五个单测 | `billingTotalRounding.test.jsx`（含 `.0165835`、`.0165825`、`-0.0000005`、`1.0000005`、`0.1+0.2`） | ✅ |
| F2 | F2.1 每一项至少一条 + 20 组随机 + 样例 0.016583 | 同上（47 条；样例断言为 `¥0.016583`） | ✅ |
| M1.0 | 列类型预检 + 溢出保险 + 拒绝执行打印 DDL | `TestExternalDatabasePrecheckRejectsNarrowInteger`（PG/MySQL 实测拒绝并打印正确 DDL）、`TestDialectSpecificSQLIsPortable` | ✅ |
| M1.5 | 全覆盖 fixture，逐列 `round(旧 × 14.6)` | `TestApplyScalesEveryM15Column`（31 列 + 8 种 provider 的 `top_ups.amount` 取舍） | ✅ |
| M1.6 | 全部键改写 / 不动键保留 / 非法 JSON 计数 | `TestApplyRewritesEveryM16JSONKey`、`TestJSONRewriteLeavesUntargetedRowsUntouched`、`TestApplyPreservesUntargetedKeysAndHugeIntegers` | ✅ |
| M1.7 | options 存在与缺失两种情况 | `TestApplyRewritesM17Options`、`TestCheckinMinQuotaUsesFloatParsing` | ✅ |
| M1.2 | 幂等：第二次 apply 不改数据、退出码 0 | `TestApplyIsIdempotent` | ✅ |
| M1.4 | 断点续跑：注入中断后续跑结果与一次跑完相同 | `TestApplyResumesAfterInterruptionWithoutDoubleScaling`（11001 行 → 3 批，第 1 批后中断） | ✅ |
| M1.2 | 拒绝执行：QPU ≠ 500000 且无状态 key | `TestRefusesWhenQuotaPerUnitAlreadyChanged` | ✅ |
| M1.8 | dry-run 不写库、报告与 apply 一致 | `TestDryRunWritesNothingAndMatchesApply`、`TestDryRunReportsLogBatchPlanWithoutStateKey` | ✅ |
| M1.4 | 空 `other` 行不得中断分页 | `TestJSONPagingDoesNotStopAtEmptyRows`（验收方提供，两种空行分布） | ✅ |
| M1.8 | dry-run 的「已改写行」不得因空行变少，且与 apply 一致 | `TestDryRunRewriteCountMatchesApplyAcrossEmptyRows` | ✅ |
| M1.8 | apply 报告的 logs 前值必须是改写前采集；续跑时标注不可得 | `TestApplyReportShowsRealBeforeSums`、`TestApplyReportMarksBeforeSumUnknownOnResume` | ✅ |
| M1.0 | 独立日志库时预检查日志库的 logs | `TestSeparateLogDatabaseMigration` | ✅ |

---

## 3. 测试输出（§6.3）

### 3.1 后端全量

```
$ go test -count=1 ./...
（全部包 ok，无 FAIL）
ok  github.com/QuantumNous/new-api/common           …
ok  github.com/QuantumNous/new-api/service          …
ok  github.com/QuantumNous/new-api/relay            …
ok  github.com/QuantumNous/new-api/relay/helper     …
ok  github.com/QuantumNous/new-api/controller       …
ok  github.com/QuantumNous/new-api/model            …
ok  github.com/QuantumNous/new-api/service/quotaunitmigration  5.8s
ok  github.com/QuantumNous/new-api/pkg/billingexpr  …
…（其余包同为 ok；无测试文件的包为 "no test files"）
```

### 3.2 前端全量

```
$ cd web/classic && bun run test
 Test Files  84 passed (84)
      Tests  658 passed (658)
   Duration  24.04s
```

（开发前基线：80 文件 / 597 用例；本次 +4 文件 / +61 用例。）

### 3.3 迁移程序三库

```
$ go test ./service/quotaunitmigration/            # SQLite（内存库）
--- PASS: TestExternalDatabaseFullMigration (0.00s)          # 子用例 t.Skip（未设 PG/MySQL DSN）
--- PASS: TestDialectSpecificSQLIsPortable (0.00s)
--- PASS: TestApplyScalesEveryM15Column (0.01s)
--- PASS: TestApplyRewritesEveryM16JSONKey (0.01s)
--- PASS: TestApplyRewritesM17Options (0.01s)
--- PASS: TestCheckinMinQuotaUsesFloatParsing (0.01s)
--- PASS: TestApplyIsIdempotent (0.01s)
--- PASS: TestApplyResumesAfterInterruptionWithoutDoubleScaling (2.93s)
--- PASS: TestDryRunReportsLogBatchPlanWithoutStateKey (1.45s)
--- PASS: TestRefusesWhenQuotaPerUnitAlreadyChanged (0.01s)
--- PASS: TestDryRunWritesNothingAndMatchesApply (0.01s)
--- PASS: TestJSONRewriteLeavesUntargetedRowsUntouched (0.01s)
--- PASS: TestApplyPreservesUntargetedKeysAndHugeIntegers (0.01s)
--- PASS: TestJSONPagingDoesNotStopAtEmptyRows (0.30s)          # 验收方提供的回归测试
--- PASS: TestSeparateLogDatabaseMigration (1.43s)
--- PASS: TestApplyReportShowsRealBeforeSums (0.06s)
--- PASS: TestApplyReportMarksBeforeSumUnknownOnResume (1.49s)
--- PASS: TestDryRunRewriteCountMatchesApplyAcrossEmptyRows (0.09s)
ok  github.com/QuantumNous/new-api/service/quotaunitmigration  8.5s

$ docker run -d --name quota-mig-pg -e POSTGRES_PASSWORD=123456 -e POSTGRES_USER=root \
    -e POSTGRES_DB=quota_migration_test -p 55432:5432 postgres:15-alpine
$ docker run -d --name quota-mig-mysql -e MYSQL_ROOT_PASSWORD=123456 \
    -e MYSQL_DATABASE=quota_migration_test -p 53306:3306 mysql:8.0 --default-authentication-plugin=mysql_native_password
$ QUOTA_MIGRATION_TEST_PG_DSN='postgresql://root:123456@127.0.0.1:55432/quota_migration_test' \
  QUOTA_MIGRATION_TEST_MYSQL_DSN='root:123456@tcp(127.0.0.1:53306)/quota_migration_test?charset=utf8mb4&parseTime=True&loc=Local' \
  go test ./service/quotaunitmigration/ -run 'TestExternalDatabase|TestDialectSpecific' -v
=== RUN   TestExternalDatabaseFullMigration
=== RUN   TestExternalDatabaseFullMigration/postgresql
=== RUN   TestExternalDatabaseFullMigration/mysql
--- PASS: TestExternalDatabaseFullMigration (5.58s)
=== RUN   TestExternalDatabasePrecheckRejectsNarrowInteger
=== RUN   TestExternalDatabasePrecheckRejectsNarrowInteger/postgresql
=== RUN   TestExternalDatabasePrecheckRejectsNarrowInteger/mysql
--- PASS: TestExternalDatabasePrecheckRejectsNarrowInteger (0.90s)
=== RUN   TestDialectSpecificSQLIsPortable
--- PASS: TestDialectSpecificSQLIsPortable (0.00s)
ok  github.com/QuantumNous/new-api/service/quotaunitmigration  15.82s
```

三库各自的覆盖范围一致：dry-run 不写库 → apply → M1.5/M1.6/M1.7 逐项断言 → 幂等 → 预检拒绝。
PG 用例覆盖 11005 行 logs（跨 3 批），MySQL 同。

---

## 4. dry-run / apply 样例输出（§6.4）

§5.4 本地端到端（PostgreSQL 15 容器 + 真实二进制 + 假上游，脚本见 §8.7）。已去掉启动期的
`[SYS]` 行，其余为报告全文。

### 4.1 造数据（旧单位，QPU = 500000）

```
旧日志 quota: ['0', '15000', '15000']  余额: 970000  options.QuotaPerUnit: （缺失，走代码默认 500000）
```

（`quota=0` 是注册礼那条非消费日志；两次调用各 15000 quota：假上游 prompt 1000 / completion 500、
倍率 10、补全倍率 1 → (1000+500)×10。）

### 4.2 dry-run

```
=== 额度单位迁移（dry-run）===
时间: 2026-10-10 16:20:29
QuotaPerUnit: 500000 → 7300000（于是 1 quota = ¥0.000001）
汇率（只用于报告里的人民币对照）: 7.3
日志库: 与主库同库

[M1.0 列类型预检]
31 列全部是 64 位整数，且放大 14.6 倍后不会溢出。

[数值列] 小表阶段（单事务，15 表 28 列）
表.列                                            行数  迁移前合计      迁移后合计       迁移前 CNY      迁移后 CNY
users.quota                                    2   100970000  1474162000  1474.162000  1474.162000
users.used_quota                               2   30000      438000      0.438000     0.438000
users.aff_quota                                2   0          0           0.000000     0.000000
users.aff_history                              2   0          0           0.000000     0.000000
users.points_balance                           2   0          0           0.000000     0.000000
users.points_used                              2   0          0           0.000000     0.000000
users.aff_points_earned                        2   0          0           0.000000     0.000000
users.credit_limit                             2   0          0           0.000000     0.000000
users.credit_used                              2   0          0           0.000000     0.000000
users.credit_settled                           2   0          0           0.000000     0.000000
tokens.remain_quota                            1   -30000     -438000     -0.438000    -0.438000
tokens.used_quota                              1   30000      438000      0.438000     0.438000
channels.used_quota                            1   30000      438000      0.438000     0.438000
tasks.quota                                    0   0          0           0.000000     0.000000
midjourneys.quota                              0   0          0           0.000000     0.000000
quota_data.quota                               0   0          0           0.000000     0.000000
redemptions.quota                              0   0          0           0.000000     0.000000
checkins.quota_awarded                         0   0          0           0.000000     0.000000
fund_entries.quota_delta                       1   1000000    14600000    14.600000    14.600000
bank_transfer_orders.quota_granted             0   0          0           0.000000     0.000000
subscription_plans.total_amount                0   0          0           0.000000     0.000000
subscription_plans.compute_points_per_period   0   0          0           0.000000     0.000000
user_subscriptions.amount_total                0   0          0           0.000000     0.000000
user_subscriptions.amount_used                 0   0          0           0.000000     0.000000
subscription_pre_consume_records.pre_consumed  0   0          0           0.000000     0.000000
compute_point_lots.points_total                0   0          0           0.000000     0.000000
compute_point_lots.points_used                 0   0          0           0.000000     0.000000
top_ups.amount                                 0   0          0           0.000000     0.000000
（迁移后合计 = SUM(ROUND(列 × 14.6))，为预计值；CNY = quota ÷ QPU × 汇率）

[数值列] logs（分批，3 列）
表.列                   行数  迁移前合计  迁移后合计   迁移前 CNY   迁移后 CNY
logs.quota            3   30000  438000  0.438000  0.438000
logs.points_consumed  3   0      0       0.000000  0.000000
logs.credit_consumed  3   0      0       0.000000  0.000000
（迁移后合计 = SUM(ROUND(列 × 14.6))，为预计值；CNY = quota ÷ QPU × 汇率）

[JSON 键]
存储                  清单键                                                          扫描行数  已改写行  值非数字未改  无法解析
tasks.private_data  points_consumed, credit_consumed, stage1_adaptor_quota, entitlement_spent[].amount   0     0     0       0
users.setting       quota_warning_threshold                                             1     0     0       0
logs.other          fee_quota, cost_quota, …（共 12 键）                                 2     0     0       0
无法解析（非 JSON 对象）的行：共 0 行，前 50 个 id：（无）
完整 id 清单已写入 /private/tmp/e2e/logs/quota-unit-migration-unparsable-ids.txt

[options]
key                                            旧值          新值
QuotaForNewUser                                1000000     14600000
QuotaForInviter                                （缺失，走代码默认）  （缺失，本次不写入）
QuotaForInvitee                                （缺失，走代码默认）  （缺失，本次不写入）
QuotaRemindThreshold                           （缺失，走代码默认）  14600
checkin_setting.min_quota                      （缺失，走代码默认）  （缺失，本次不写入）
checkin_setting.max_quota                      （缺失，走代码默认）  （缺失，本次不写入）
points_setting.quota_per_point                 （缺失，走代码默认）  10000
compute_point_setting.quota_per_compute_point  （缺失，走代码默认）  10000
QuotaPerUnit                                   （缺失，走代码默认）  7300000
PreConsumedQuota 刻意不在清单里：它是预扣 token 数下限而不是 quota，倍率路径乘 scale 已经发生在它之后，再 ×14.6 会把冻结额放大两次。

[logs] 分批计划：id (0, 3]，预计 1 批，每批 5000 行

dry-run 结束：未写任何数据。执行 --migrate-quota-unit=apply 才会落地。
```

### 4.3 apply

```
=== 额度单位迁移（apply）===
…（头部与预检、[数值列] 小表阶段同上，此处从略）

[数值列] logs（分批，3 列）
表.列                   行数  迁移前合计  迁移后合计   迁移前 CNY   迁移后 CNY
logs.quota            3   30000  438000  0.438000  0.438000
logs.points_consumed  3   0      0       0.000000  0.000000
logs.credit_consumed  3   0      0       0.000000  0.000000

（评审指出过：这段样例曾经前后两列相同（438000 / 438000），因为前值是在分批改写之后
采集的。现已改为改写前采集；断点续跑时前值不可得，表格标 `—` 并在表下注明。）

[JSON 键]
存储                  清单键                                                          扫描行数  已改写行  值非数字未改  无法解析
tasks.private_data  points_consumed, credit_consumed, stage1_adaptor_quota, entitlement_spent[].amount   0     0     0       0
users.setting       quota_warning_threshold                                             1     0     0       0
logs.other          fee_quota, cost_quota, …（共 12 键）                                 2     0     0       0
无法解析（非 JSON 对象）的行：共 0 行，前 50 个 id：（无）
完整 id 清单已写入 /private/tmp/e2e/logs/quota-unit-migration-unparsable-ids.txt

[options]
…（与 dry-run 相同；QuotaPerUnit 500000 → 7300000 等）

[logs] id (0, 3] 共 1 批，改写 other 0 行，耗时 2ms
apply 结束：options.QuotaUnitMigration.state = done。
```

耗时（本地 PG 容器、29 个小表列 + 3 行日志）：小表事务 22ms，日志阶段 2ms。
生产 85 万行 logs 的耗时以 §7.3 演练为准。

### 4.4 迁移后复查（同一次端到端）

```
余额 970000 → 14162000（期望 14162000）：¥14.162000 → ¥14.162000
日志 [0, 15000, 15000] → [0, 219000, 219000]（期望 [0, 219000, 219000]）
/api/status: quota_per_unit=7300000 quota_per_point=10000 quota_per_compute_point=10000 usd_exchange_rate=7.3
新日志 quota=219000（期望 round(¥0.219000 × 10⁶)=219000）
旧日志花费 ¥0.219000 vs 新日志花费 ¥0.219000
```

结论：人民币余额与历史日志花费逐位不变；切换后新日志的 quota 恰好落在 ¥10⁻⁶ 网格上，
「计费过程合计 = 花费列」成立。

---

## 5. 表名核对结果（§6.5）

| 任务书表名 | 实际表名 | 依据 |
|---|---|---|
| `users` | `users` | `model.User`（无 `TableName()`，GORM 复数蛇形） |
| `tokens` | `tokens` | `model.Token` |
| `channels` | `channels` | `model.Channel` |
| `logs` | `logs` | `model.Log` |
| `tasks` | `tasks` | `model.Task` |
| `midjourneys` | `midjourneys` | `model.Midjourney` |
| `quota_data` | `quota_data` | `model.QuotaData` |
| `redemptions` | `redemptions` | `model.Redemption` |
| `checkins` | `checkins` | `model.Checkin.TableName()` 显式覆盖 |
| `fund_entries` | `fund_entries` | `model.FundEntry` |
| `bank_transfer_orders` | `bank_transfer_orders` | `model.BankTransferOrder` |
| `subscription_plans` | `subscription_plans` | `model.SubscriptionPlan` |
| `user_subscriptions` | `user_subscriptions` | `model.UserSubscription` |
| `subscription_pre_consume_records` | `subscription_pre_consume_records` | `model.SubscriptionPreConsumeRecord` |
| `compute_point_lots` | `compute_point_lots` | `model.ComputePointLot` |
| `top_ups` | `top_ups` | `model.TopUp` |
| `options` | `options` | `model.Option`（`key` 是保留字，全部走 GORM 由方言加引号） |

**列名核对**：`users.aff_history` 是 `AffHistoryQuota` 的列名（`column:aff_history`），
`users.aff_quota` 是 `AffQuota` 的列名；两者字段标签写的是 `gorm:"type:int"`，但 GORM
把 `int` 识别为通用整数类型、宽度取 Go 字段的 64 位，三库建出来都是 64 位整数
（`gorm.io/gorm/schema/field.go` 的 TYPE 标签处理 + `gorm.io/driver/postgres` 的
`DataTypeOf`），所以 M1.0 预检在生产上不会拦它们。

未列入 M1.5 但同名的 `reconcile_hourly.quota`：v2.1 对账的遗留表，代码已在
commit `a77bc26fb` 删除，当前无读写，不迁移（与任务书 §11 一致）。

新增（不在任务书清单里、也未迁移）的观察：`model.Properties` / `model.TaskPrivateData`
以外的 JSON 列都没有 quota 键；AutoMigrate 列表里的其余表（`feedback_*`、`invoice_*`、
`perf_metrics`、`media_storage_stats`、`moderation_logs`、`channel_model_costs`、
`subscription_plan_entitlements`、`user_subscription_entitlements`、`user_enterprises`、
`sub_account_token_bindings`、`topup_packages`、`abilities`、`models`、`vendors`、
`prefill_groups`、`setups`、`two_fa*`、`passkey_credentials`、`oauth_providers`、
`kyc*`、`bank_transfer_receipts` 等）经逐表核对**没有 quota 单位列**：
金额列（`*_fen` / `money` / `price_amount`）、比例列（`cost_ratio`、`consume_discount`）、
计数列（`limit_count`、`used_count`、`grant_points`）都按任务书要求原样保留。

---

## 6. F2 范围核对（§6.6）

### 6.1 F2.1 五处改动的前后对比

同一组输入（CNY 展示、`quota_per_unit = '7300000'`、汇率 7.3）：

| F2.1 | 场景 | 改动前合计 | 改动后合计 |
|---|---|---|---|
| 1 `renderModelPrice` | 按 token：1050/350 tokens、倍率 0.1、补全 4、分组 0.5 | `¥0.001788` | `¥0.001789` |
| 1 `renderModelPrice` | 按次：$0.000005 × 1 | `¥0.000036` | `¥0.000037` |
| 2 `renderAudioModelPrice` | 按次 | `¥0.000036` | `¥0.000037` |
| 2 `renderAudioModelPrice` | 按 token：550 文本 + 200 音频 ×2、分组 0.5 | `¥0.000693` | `¥0.000694` |
| 3 `renderClaudeModelPrice` | 缓存 600 + 100×0.5、分组 0.5 | `¥0.000474` | `¥0.000475` |
| 3 `renderClaudeModelPrice` | 按次 | `¥0.000036` | `¥0.000037` |
| 4 `buildVideoMatrixDetailRows` | 按 token：¥46 ×0.5 ×5 tokens | `¥0.000839` | `¥0.000840` |
| 5 `renderVideoMatrixPriceSimple` | 按 token：同上（保留去尾 0 写法） | `¥0.000839` | `¥0.00084` |

用户反馈的那条样例（199 / 4096 tokens、¥2 / ¥8 per 1M、分组 0.5）改动前后都是 `¥0.016583`
（它本来不落在第 6 位小数的 `.5` 边界上），已在测试里按字面值断言。

判据：改动前用 `toFixed(6)`，在「精确值是 `x.xxxxx5`、而 double 落在略小的一侧」时会向下
取整，比花费列少 1 quota —— 上表每一行都是这类边界。改动后按「人民币金额 × 10⁶ 四舍五入、
远离零」输出，与花费列同口径。变异测试：把改动回退成 `toFixed(6)`，47 条里有 11 条失败
（F2.1 每一行至少 1 条）。

### 6.2 F2.2 死代码位置清单（只列出，未删除）

- `renderModelPrice`：`if (modelPrice !== -1) { … displayTotal = (modelPrice * groupRatio * rate).toFixed(6) … }`（`shouldUseRatioBillingProcess` 分支之后那一段）
- `renderAudioModelPrice`：同结构
- `renderClaudeModelPrice`：同结构

进入该块的前提是 `modelPrice === -1`，而分支内部又要求 `modelPrice !== -1`，条件互斥。

### 6.3 CNY 展示下是否还有其他可达的「= 合计」

逐个核对了 `web/classic/src/helpers/render.jsx` 里所有产出金额的渲染函数：
`renderModelPrice` / `renderAudioModelPrice` / `renderClaudeModelPrice` 的价格模式合计、
按次分支、`buildVideoMatrixDetailRows`、`renderVideoMatrixPriceSimple` 之外没有其他可达合计。
`renderTieredModelPrice` 只输出「命中档位 + 各档单价」，`renderTaskBillingProcess` 只包装
传入内容或显示预扣说明，两者都没有合计；倍率模式算式（`renderDisplayAmountFromUsd` 那条）
只在 `quota_display_type = TOKENS` 下可达，且显示的是 token 当量。

---

## 7. F1 fallback 策略说明（§6.7）

- `getQuotaPerUnit` 的 fallback 取 **500000**。理由：它等于此前两处硬编码调用点用过的值，
  也等于后端 `common.QuotaPerUnit` 的出厂默认；`localStorage.quota_per_unit` 正常情况下由
  `/api/status` 在页面渲染前写入，fallback 只在「从未取到 status」时生效。
- `getQuotaPerPoint` / `getQuotaPerComputePoint` 的 fallback 保持 **684.93**（旧单位下的历史值）。
- fallback 只在 `helpers/quota.js` 定义一处（`FALLBACK_QUOTA_PER_UNIT` /
  `FALLBACK_QUOTA_PER_POINT`），`TransferTab.jsx`、`TieredPricingEditor.jsx` 已改为调用
  `getQuotaPerUnit()`；读不到时按 key `console.warn` 一次（模块级 `Set` 去重，最多 3 条）。
- `helpers/render.jsx` 里那个同名的 `getQuotaPerUnit` 本次不改（F1 明确排除），它直接读
  localStorage、没有 fallback；`helpers/index.js` 只 re-export `./render`，所以从 barrel
  导入拿到的是它——四处 F1 调用点都显式从 `helpers/quota` 导入，避免混用。
- `SettingsPoints.jsx` 换算率输入框的文案改为 `quotaPerPointHintText()`，显示 `quota_per_point`
  的当前生效值（0 = 自动时显示推算值），避免管理员照着写死的 684.93 填回去。

---

## 8. 疑问 / 与任务书的偏差（§6.8）

1. **`common/compute_points_test.go` 的语义改写（已获批）**。原测试断言「同一笔 quota 消耗的
   算力点数不随汇率变化」，靠初始化期冻结 684.93 实现；B5 的公式让自动路径跟随实时汇率，
   两者不可能同时成立。现按 §10.1 不变量（`QPU = 汇率 × 10⁶`）改写为
   `TestComputePoints_RateAndQuotaPerUnitInvariant`，并新增
   `TestConfiguredPointRateIsIndependentOfExchangeRate` 把「配置值路径不受汇率影响」这条
   业务性质钉在生产实际走的路径上。
2. **B4 的清单按评审意见扩充了两处**（任务书已更新）：`controller/channel-test.go` 按次分支、
   `service/entitlement_cost.go`；另发现并修掉 `model/bank_transfer.go` 的先除后乘（B4.1），
   该项在切换后会让每笔对公转账少到账 1 quota。
3. **音频 token 的「分档覆盖」**：`calculateTextQuotaSummary` 里 Gemini 音频 $/M 是一条
   独立的价格路径（`audioInputQuota`），它不在倍率路径上，因此断言的是「已经 ×QPU、
   不得再乘 scale」而不是双单位缩放；B2 要求的「音频 token」场景由
   `TestCalculateAudioQuotaRatioPathScale`（WSS/音频倍率路径）与
   `TestCalculateTextQuotaSummaryPricePathDoesNotScale` 覆盖。
4. **`logs.other` 改写按评审意见重做**：去掉 SQL 侧键名预筛（否则「不含目标键的非法 JSON 行」
   会被静默跳过）；顶层解成 `map[string]json.RawMessage`、嵌套层逐层取 RawMessage，
   **不经过 `map[string]interface{}`**，因此 >2⁵³ 的大整数逐位保留（有专门测试）；
   只有真的改写了目标键的行才 `UPDATE`（`TestJSONRewriteLeavesUntargetedRowsUntouched`
   用 `total_changes()` 证明零次写回）；无法解析的行：报告列总数与前 50 个 id，
   完整清单写入 `<LogDir>/quota-unit-migration-unparsable-ids.txt`。
5. **`TaskPrivateData.Scan` 只接受 `[]byte`（既有实现的脆弱点，未改）**。SQLite 驱动把 TEXT
   回成 `string`，`Scan` 走 `val.([]byte)` 失败后静默返回 nil，于是 GORM 读回来的
   `task.PrivateData` 是零值——排查迁移时踩到过（迁移本身写库正确，是读取端的问题）。
   生产用 PG 时该列是 `json`、驱动回 `[]byte`，不受影响；测试改为直接读原始列再解析。
   建议后续把 `Scan` 用 `common.Unmarshal` 兼容 `string`/`[]byte` 两种入参。
6. **`SettingsGeneral.jsx` 里 `setShowQuotaWarning` 从未被调用**（只有声明与读取），
   F1 要求更新的那段 Modal 文案目前是不可达 UI。按任务书更新了文案，未删除该状态。
7. **§5.4 端到端脚本未入库**。它跑在本地 PG 容器上（`docs/quota-unit-cny-micro-migration.md`
   §5.4 的全部步骤 + `/api/status` 核对），保存在 `/tmp/qunit_e2e.py`；演练（§7.3）如果
   想直接复用，把它挪到 `scripts/e2e/` 即可，本次没有提交以避免超出改动范围。
8. **SQLite 下 `ROUND(col × 14.6)` 是浮点运算**，值超过 2⁵³ 时理论上会丢整数精度。
   生产是 PostgreSQL（`bigint × numeric` 精确），MySQL 是 decimal 精确，测试数据量级很小，
   因此未做额外处理；如需在 SQLite 上跑生产级数据，应改用整数运算
   （`n × 146 ÷ 10`）或走 Go 侧逐行改写。
9. **`TransferTab.jsx` 的 `quotaPreview` useMemo 依赖未变**（`[creditedAmount, usdRate]`），
   现在它还会读 `getQuotaPerUnit()`；若在弹窗打开期间 `/api/status` 更新了 `quota_per_unit`，
   预览不会重算。实际不会发生（status 在渲染前写入），未改动。
10. **仓库根目录有个残留的 `node_modules/.vite/vitest` 缓存**（时间戳 9 月 29 日，
    早于本次改动），且未被 `.gitignore` 覆盖。不属于本次范围，未清理。

---

## 9. 迁移程序的实现要点（补充，供 §7.1 代码审查参考）

- **状态机**（任务书 §M1.2）落在 `options.QuotaUnitMigration`，JSON 形如
  `{"state":"logs","logs_cursor":N,"logs_cutoff":M,"started_at":…,"finished_at":…}`。
  `state=done` 时直接打印「已完成」并退出 0，**连列类型预检都跳过**（省掉一次全表扫描）。
- **小表阶段是单事务**：15 张表的数值列、`tasks.private_data` / `users.setting` 的键、
  `options` 的 9 个 key、`QuotaPerUnit` 与状态 key 全在一个事务里，任一步失败整体回滚。
- **日志阶段按 id 分块**（每批 5000）：同库时数值列、`other` 与 `logs_cursor` 在同一事务提交，
  断点续跑不会重复缩放；`LOG_SQL_DSN` 独立时只能先提交日志事务再写主库游标，
  那种情况下游标写失败会返回**明确要求人工改游标后重跑**的错误，而不是静默继续。
- **JSON 列按块读、读完再写**：`*sql.Rows` 未关闭时同一连接不能执行 UPDATE，
  而测试与部分部署把 `MaxOpenConns` 设成 1，边读边写会死锁；块大小 500。
- **分页游标与「是否读完」按本页原始行数判断**（含空串 / NULL 行）：充值、系统、管理类
  日志的 `other` 经常为空，用过滤后的行数判断会让一页不满 500 就收尾、或让游标停在
  空格子上，结果是大批日志静默漏迁移。
- **三库兼容**：数值列更新用 `ROUND(col × 14.6)`（三库语法一致，且 `n × 14.6` 的小数部分
  只可能是 0/0.2/0.4/0.6/0.8，不落在 `.5` 上）；`SUM` 结果按方言转成 64 位整数
  （PG `::bigint`、MySQL `CAST(… AS SIGNED)`、SQLite `CAST(… AS INTEGER)`）；
  `options.key` 等保留字一律走 GORM 由方言加引号；JSON 判空不在 SQL 里做
  （PG/MySQL 的 `json` 列不支持 `<> ''`，这是 PG 实测暴露出来的）。
- **选区不写入**：`PreConsumedQuota` 不在任何清单里（它是 token 数下限）。

---

## 10. 未完成 / 未验证

- **§7.3 本地演练、§8 生产升级**：按分工由方案作者与运维执行，本次未做。
- **真实支付回调、真实上游调用**：留到 §8.4 生产冒烟。
- **MySQL 的 `information_schema` 预检只在 `DATABASE()` 范围内生效**：若目标库与当前连接的
  默认库不一致（DSN 里指定了 db 名，所以正常不会），需要人工确认。
- **生产 85 万行 logs 的实际耗时**：本地 3 行数据无法外推，以 §7.3 演练第 6 步为准。

---

## 11. 发布记录（本次出包）

| 项 | 值 |
|---|---|
| 提交 | `b73201095` feat(billing): 内部额度单位切换为 1 quota = ¥0.000001（QPU 7,300,000） |
| 推送 | `origin/main`（fork `reputationly/new-api`） |
| 镜像版本 | `v0.13.2-ovaijisuan-20261010-b7320109`（CI 由 UPSTREAM `v0.13.2` + UTC 日期 + `b7320109` 拼出，容器内 `/new-api --version` 已复核） |
| 多架构 digest | `sha256:5a6a3487613de808d25965e312df234fd7126b583c86510693c3797bd4780365`（linux/amd64 + linux/arm64） |
| 发布方式 | `gh workflow run docker-image-ovaijisuan.yml --ref main`（run `38051717618`，success；与上一版同一条流水线，原生双架构构建） |
| ACR 标签 | `:latest`、`:ovaijisuan`、`:v0.13.2-ovaijisuan-20261010-b7320109`（三者指向同一 digest） |
| 上一版 `:latest`（回滚参照） | `sha256:a8db6c90c051ad759b3ff27cd068e84db4810aeb9ee2d5bb97a53b90fbf357c5`（amd64 `sha256:10be67dc…` / arm64 `sha256:e28dfd2549e4…`，版本字符串 `v0.13.2-ovaijisuan-20261009-b2bd29ac`） |
| 生产影响 | 生产 compose 用的是 `:latest` 且跑着 watchtower，**会自动升级到本次镜像**。在 QPU 仍为 500000 的库上 `QuotaScale() = 1`，计费结果与上一版一致（B4 的取整与手动补单修复除外），所以提前上线是安全的 |
| 数据迁移 | **未执行**。等 §7.3 演练与停服窗口，用 `--migrate-quota-unit=dry-run` / `apply` |

未打 git tag：本次走 `workflow_dispatch`（上一版也是），因此没有创建 GitHub Release、也没有触发上游
`docker-build.yml`（它推 `calciumion/new-api`，在 fork 上必然失败）。若要给 classic 的「检查更新」
按钮提供新版本条目，再打一次 tag 即可：`git tag v0.13.2-ovaijisuan-20261010-b7320109 b73201095 && git push origin --tags`。

---

## 12. 生产迁移实测记录（2026-10-10）

生产（`bms-newapi`，Docker Compose + PostgreSQL）实测数据，作为 §7.3 演练的替代证据。

### 12.1 执行方式

```bash
cd /opt/new-api
docker compose --env-file .env.prod run -T --rm --no-deps new-api \
  --log-dir /app/logs --migrate-quota-unit=apply
docker exec redis redis-cli ... FLUSHDB
docker compose --env-file .env.prod up -d new-api
```

- 镜像：`v0.13.2-ovaijisuan-20261010-b7320109`（digest `sha256:5a6a3487…`，多架构 amd64+arm64）
- 未单独跑 dry-run（直接 apply）；迁移窗口实测 **约 1.5 分钟**（小表事务 685ms + logs 批次 1m24.556s）
- `LOG_SQL_DSN` 未配置 → 报告显示「日志库: 与主库同库」✓ 与 §8.2 的待确认项一致

### 12.2 apply 报告要点

| 项 | 实测 |
|---|---|
| M1.0 列类型预检 | 31 列全部 64 位整数、不溢出 |
| logs 分批 | `id (0, 851506]` 共 **171 批**，每批 5000 |
| `logs.other` 改写 | 扫描 850,968 行 → 改写 **22,865 行**（§11 预估 22,858，同一量级 ✓） |
| `tasks.private_data` | 扫描 2,734 行 → 改写 631 行 |
| `users.setting` | 扫描 228 行 → 改写 5 行 |
| 无法解析的行 | **0** |
| `[options]` | `QuotaForNewUser` 0→0；`QuotaRemindThreshold` 缺失→14600；`checkin_setting.min_quota` 0.1→1、`max_quota` 2→29；两个换算率缺失→10000；`QuotaPerUnit` 缺失→7300000 |
| 终态 | `options.QuotaUnitMigration.state = done` |

### 12.3 人民币一致性复核（独立重算）

按 `CNY = quota ÷ QPU × 7.3` 重算报告中的两列，与报告完全一致；前后差额等于逐行 `ROUND(x × 14.6)` 的累积漂移，
全部落在 `行数 × 0.5 quota` 的上界内：

| 列 | 迁移前 CNY | 迁移后 CNY | 漂移(quota) | 上界(行数×0.5) |
|---|---|---|---|---|
| `users.quota` | 13461.287439 | 13461.287423 | −16.4 | 114 |
| `logs.quota` | 24776.663419 | 24776.648202 | −15216.6 | 425738 |
| `channels.used_quota` | 22885.180464 | 22885.180465 | +0.6 | 9 |
| `tasks.quota` | 1938.875270 | 1938.875037 | −232.6 | 1367 |

### 12.4 迁移后状态

`/api/status`：`version = v0.13.2-ovaijisuan-20261010-b7320109`、`quota_per_unit = 7300000`、
`quota_per_point = quota_per_compute_point = 10000`、`usd_exchange_rate = 7.3` ✓

### 12.5 一处过程风险（量级极小，需用一条查询定性）

apply 命令块里没有 `stop`，而 `up -d` 的输出现为 `Container new-api Started`（非 `Running`），
因此无法从记录判断 apply 的 1.5 分钟内服务是否已停。若服务当时仍在跑：

- 窗口内产生的日志行 id > 851506、**不会被缩放**（按旧单位入账），人民币显示会偏小 14.6 倍；
- 对应扣款也是旧单位数值 → 少扣 14.6 倍（金额几分钱量级）；
- **不会**污染余额：批量更新是增量写法（`increaseUserQuota` → `quota = quota + ?`），不存在把已缩放余额写回旧量级的路径；
- 量级：生产日均约 4,700 行（≈0.05 行/秒），90 秒窗口最多几行。

定性命令（只读）：

```sql
SELECT count(*) FROM logs
WHERE id > 851506
  AND created_at < extract(epoch from timestamptz '2026-10-10 22:31:00+08')::bigint;
```

0 = 窗口内零写入，迁移完全干净；>0 = 按 `×14.6` 修正那几行的三列与 `other` 键，或记录在案接受。
