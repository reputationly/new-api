# 定价人民币化 + 额度单位细化到 ¥0.000000001 —— 开发报告

> 任务书：[pricing-cny-and-quota-1e8.md](./pricing-cny-and-quota-1e8.md)（含 §12 评审结论）
> 评审意见：[pricing-cny-and-quota-1e8-review.md](./pricing-cny-and-quota-1e8-review.md)
> 代码基线：`bd5b5d21a`（本报告对应的工作树）
> 状态：**B1–B9、M2、F1–F6 全部实现**；§8 的生产升级**未执行**（等运维窗口）。

---

## 1. 已定口径（评审 R1–R4 / Q1）

| # | 结论 | 落地位置 |
|---|---|---|
| R1 | E3 与 §7.1 的低价期望值订正为 V2 单位（149 tokens → 1490 quota） | 文档订正；测试断言按订正后写 |
| R2 | **v1 表达式拒绝结算**（返回 `ErrExprV1Unsupported`，要求重录为 v2） | `pkg/billingexpr/settle.go`、`relay/helper/price.go` |
| R3 | **全部任务**（含已完成）一起迁 `tasks.private_data` 并写 `price_unit = cny` | `service/quotaunitmigration/rewrite.go` `rewriteTaskPrivateDataV2` |
| R4 | extraContent 现状是 quota 数字，改为直接拼人民币金额（9 位小数） | `service/text_quota.go`（5 条文案） |
| Q1 | B9 **整进程拒绝启动**，并在提示里给出迁移命令与回滚方式 | `service/quotaunitmigration/guard.go`、`main.go` |

---

## 2. 实现清单

### 2.1 B1 单位常量（`common/constants.go`）

- `QuotaPerYuan = 1_000_000_000`（1 quota = ¥0.000000001）、`QuotaPerMillionTokens = 1000`、`RatioQuotaPerToken(ratio)`、`PriceUnitCNY = "cny"`。
- `QuotaPerUnit` 默认值改为 `7_300_000_000`，并注明它**只**用于美元支付与美元展示。
- **删除** `RatioQuotaBase` 与 `QuotaScale()`。
- 积分/算力点默认换算率改为 `QuotaPerYuan / 100 = 10_000_000`，不再依赖汇率；随之删除 `common.USDExchangeRateFunc` / `getUSDExchangeRate()` 与 `operation_setting` 里的注入点（我的改动造成的孤儿导入一并清理）。

### 2.2 B2 计费路径

按 §3.1 清单逐点改完（倍率路径 ×1000、价格路径 ×QuotaPerYuan），全部编译通过后复核：

| 位置 | 改法 |
|---|---|
| `service/text_quota.go`（倍率路径 / 按次 / Gemini 音频 / 工具附加费 5 处 / extraContent 5 处） | 倍率 ×`QuotaPerMillionTokens`；价格 ×`QuotaPerYuan`；文案直接拼人民币 9 位 |
| `service/quota.go`（音频、OpenRouter 缓存反推） | 同上；OpenRouter 的上游美元成本先 ×汇率（计费路径里唯一合法读汇率处） |
| `relay/helper/price.go`（预扣 ×2、按次、按量、分段预扣） | 按量预扣写成显式 `250_000 × ratio × 1000`；分段预扣与结算共用 `QuotaFromExprOutput` |
| `service/task_billing.go`（按 token / 视频矩阵） | ×1000 / ×`QuotaPerYuan` |
| `service/tool_billing.go`、`service/violation_fee.go`、`service/entitlement_cost.go` | ×`QuotaPerYuan`（`entitlement_cost` 的注释同步纠正为人民币口径） |
| `relay/video_billing.go`、`controller/channel-test.go` | ×`QuotaPerYuan` / ×1000 |

**未改动**（符合 §1.4/§3.4）：所有支付路径、`common.GetTrustQuota`、`controller/token.go` 的 `maxQuotaValue`、`users`/`tokens` 的整数列（迁移负责放大）。

### 2.3 B3 代码内美元常量与默认表

- `setting/operation_setting/tools.go`：`defaultToolPrices`(10/10/2.5/14)、`defaultToolPriceOverrides`(25)、GPT-Image-1 九档、Gemini 音频 6 档全部写成 `× usdToCNY`（新增常量 `usdToCNY = 7.3`，与 `ratio_setting.USD2RMB` 同值——导入方向决定了不能共用）。
- `setting/model_setting/grok.go`：罚金 `0.05 → 0.365`。
- `setting/ratio_setting/model_ratio.go`：原表保持美元口径不动，新增 `cnyModelRatios/cnyModelPrices/roundTo6`，在 `InitRatioSettings`、`DefaultModelRatio2JSONString`、`GetDefaultModelRatioMap`、`GetDefaultModelPriceMap` 四处统一换算（**必须同一函数**，否则 `hasCustomModelRatio` 会把所有默认价模型判成"已改价"）。
  已验证：`GetDefaultModelRatioMap()["gpt-4.1"] == 14.6`。

### 2.4 B4 分段表达式

- `pkg/billingexpr/compile.go`：新增 `ExprVersionV2 = 2`，`ParseExprVersion` 认 `v2:`；无前缀仍按 **v1**（历史数据）。
- `pkg/billingexpr/settle.go`：`QuotaFromExprOutput(out, version)` —— v2 = `out / 10⁶ × QuotaPerYuan`，v1 = `ErrExprV1Unsupported`；`quotaConversion` 改为返回 error 并向上传播。
- 预扣与结算共用该函数，杜绝"预扣按 v1、结算按 v2"这类漂移。
- `pkg/billingexpr/expr.md` 同步更新（原则 3、原则 5、Quota Conversion、Expression Versioning 四节）。

### 2.5 B5 `price_unit` 标记

写标记的位置：`GenerateTextOtherInfo`、`GenerateMjOtherInfo`、`LogTaskConsumption`、`taskBillingOther`（**透传**任务冻结的 `bc.PriceUnit`，不硬写 cny）、违规罚金日志、以及 `TaskBillingContext.PriceUnit`（新任务在 `controller/relay.go` 冻结时写 `cny`）。**历史日志不打标记**（前端按美元口径 normalize）。

### 2.6 B6 / B7 删除项

- 后端删除 `controller/ratio_sync.go`、`controller/ratio_config.go`、`setting/ratio_setting/expose_ratio.go`，移除路由与 `model/option.go` 里的 `ExposeRatioEnabled` 注册/热更新。
- 前端删除 `UpstreamRatioSync.jsx`、`ChannelSelectorModal.jsx`，去掉 `RatioSetting.jsx` 的同步 tab 与 `ModelRatioSettings.jsx` 的暴露倍率开关。

### 2.7 B8 展示 / 报表

- `logger/logger.go` 四个函数新增 `cnyFromQuota` / `customFromQuota`：CNY = `quota / QuotaPerYuan`（不再读汇率），CUSTOM = 人民币 ÷ 汇率 × 自定义系数，USD 分支不变；`LogQuota`/`FormatQuota` 用 9 位小数，`*Short`（兑换码、充值的用户面值）保持 2 位。
- `controller/log.go` 导出「费用(CNY)」= `quota / QuotaPerYuan`（9 位，不再随汇率漂）；`controller/billing.go` 的 CNY 分支同理。
- `service/reconcile_helpers.go`、`service/plan_fulfillment.go`（分，先乘后除）改人民币。
- `/api/status` 新增 `quota_per_yuan`；删除无调用者的 `GetUsdToCurrencyRate`。

### 2.8 B9 启动保护（`service/quotaunitmigration/guard.go`）

`EnsurePriceUnitReady()`：`QuotaUnitMigrationV2 != done` 且 `users` 非空 → 返回错误（`main.go` 在 `InitResources()` 之后、HTTP 服务之前 `FatalLog`，**整进程拒绝启动**），错误信息里直接打印停服/备份、dry-run、apply、启动四步与回滚要求；空库自动写入 `done`。
`model.CheckSetup()` 只在有 root 用户时写 setup 记录、不会建用户，所以全新部署不会被误拦（冒烟 §4.2 已验证）。

### 2.9 M2 迁移程序 V2（`service/quotaunitmigration/`）

- 新增 `plan.go`：把 V1/V2 的差异（状态 key、倍数、清单、前置条件、是否做定价、命令行参数名）收敛成一个 `plan`；`Run`/`RunV2` 共用同一套状态机、分批、断点续跑与报告骨架。V2 缩放在 SQL 侧为 `col * 1000`（整数倍，三库精确），JSON 数字字面量走 decimal（保留全部有效位）。
- 新增 `pricing.go`：`ModelRatio ×14.6`、`ModelPrice ×7.3`（跳过 `-1` 哨兵）、`VideoPricingConfig` 全格 ×7.3（结构与分辨率键原样保留）、`tool_price_setting.prices` ×7.3、`grok.violation_deduction_amount` ×7.3，全部保留 6 位小数；解析失败**拒绝执行**；定价写入与 options 在**同一事务**内。
  报告给出「旧值 → 新值 → 换算前人民币 → 换算后人民币 → 差值」，并单列仍为 v1 口径的表达式（**只报告不改写**，D3）。
- 新增 `guard.go`：见 §2.8。
- `scope.go` 新增 `optionSpecsV2`（quota 类 ×1000、`points_setting.quota_per_point`/`compute_point_setting.quota_per_compute_point`/`QuotaPerUnit` 写固定值）与 `smallJSONScopesV2`（tasks 的 quota 键 ×1000 **与**定价换算合并成一次改写）。
- `precheck.go` 新增 JS 安全整数提醒（2⁵³ ≈ ¥900.7 万，只警告不拒绝）；倍数改为按方案取。
- 前置条件：`QuotaUnitMigration.state = done`、`QuotaPerUnit = 7300000`、`USDExchangeRate = 7.3`；`!hasState && QuotaPerUnit != 7300000` 时拒绝执行并给出"状态被删但数据已是新单位"的恢复办法（防止二次放大）。
- 新增启动参数 `--migrate-quota-unit-v2=dry-run|apply`。
- **验收后补强（评审 5 条）**：
  1. **apply 遇到 v1 表达式直接拒绝**（`checkV1Expressions`，只读前置检查，dry-run 仍只报告）——
     否则迁移完这些模型的预扣会一律报错、模型整体不可用；
  2. **定价 JSON 写入后在同一事务里读回校验**（`verifyReadBack`）：键集合不变、每个值等于
     `round(旧值 × 因子, 6)`、非哨兵值非负，任一不符整个事务回滚；
  3. **任务行级幂等**：`rewriteTaskPrivateDataV2` 遇到已带 `price_unit` 的行整行跳过，
     避免续跑/重跑把三个价格字段再乘一遍；
  4. B9 文案的倍数与方向纠正为「配置仍是美元口径而代码按人民币/更细单位计算 → 单位不一致、
      **按错误倍数多扣费**：倍率路径约 68.5 倍（1000/14.6）、按次/视频/工具约 137 倍
     （10⁹/7.3×10⁶），余额与报表的人民币金额同时错位」；
  5. 修正 `rewriteTaskPrivateDataV2` 的注释：真实理由是「视频矩阵任务按冻结单价做差额结算
     （真的在算钱）、退款/差额日志也会带这些字段」，`PrivateData` 本身是 `json:"-"`，不是为了接口展示；
  6. **`maxQuotaValue` 溢出**：`controller/token.go` 两处不再写 `int(1000000000 * QuotaPerUnit)`，
     改用 `common.MaxTokenQuota()`（int64 + 超出时饱和到 `math.MaxInt64`）。`QuotaPerUnit` 是
     运行时可改的 option，只要被调到 > 9.22e9（例如按汇率 9.3 调美元换算），`float64 → int`
     越界在 **x86-64 上得到 MinInt64**（负上界 → 令牌创建/更新全部被拒、报错 Max 也是负数），
     而 **ARM64 上饱和成 MaxInt64**（看不出问题）——行为随架构不同，必须显式钳制。
     边界用例：默认 QPU → `7.3e18`；QPU = 1e10 → `MaxInt64` 且正额度能通过令牌更新校验。

### 2.10 F1–F6 前端

（见 §4.4 的文件清单与测试结论。）

---

## 3. 与设计文档的偏差（都是实现时的取舍）

1. **`LogQuotaShort` / `FormatQuotaShort` 保持 2 位小数**。这两个函数的语义就是"用户面值金额"（兑换码、充值），2 位是它们存在的理由（避免 ¥99.999999 这类噪音）；普通人民币展示与 extraContent 使用 9 位小数。
2. **定价换算报告全量列出**（模型级的每一行），不做抽样。换来的代价是报告较长，但迁移核对要的就是全量可 diff。
3. **代码默认常量（`QuotaRemindThreshold=1000`、`checkin_setting` 默认值等）没有改**：现网库里这些 key 存在，迁移会写库；只有"全新库且运维从未配过"才会读到偏小的默认值。列为遗留项（§6）。
4. **新增 `plan.go`/`pricing.go`/`guard.go` 三个文件**：文档只写了"参数化复用"，没有规定文件划分。
5. **v1 快照在「结算」路径上是"按预扣额结账 + 告警"，不是直接报错**。评审 R2 的原话是"v1 表达式与 v1 快照一律返回可读错误"：
   - 新请求：预扣阶段（`ModelPriceHelper` → `QuotaFromExprOutput`）**确实直接报错**，用户看到明确错误 ✓；
   - 但在途任务（升级前提交、升级后产出）已经烧掉上游成本，若在结算阶段报错会让计费流水卡住，
     因此 `TryTieredSettle` 保持"退回预扣额"的既有降级行为，并新增一条 `SysError` 告警（含模型名、
     表达式 hash、预扣额与原因）保证**不静默**。现网表达式为 0 条（§5.0），这条路径当前不会触发。

---

## 4. 验证

### 4.1 设计文档 §8 的审查 grep（四条全部符合预期）

```
① QuotaScale / RatioQuotaBase 残留（非测试）        → 空
② 计费路径里的 ×2 / ratio÷2 / RatioQuotaBase        → 空
③ QuotaPerUnit 剩余位置                             → 仅：支付路径（topup*/model/topup.go/bank_transfer/
                                                       user/token/quota.go）、美元展示（logger、
                                                       billing.go、misc.go）、v1 快照（billingexpr/
                                                       types.go、price.go）、迁移程序
④ USD2RMB / usdToCNY                                → 仅：默认表换算与代码内美元常量
```

### 4.2 端到端冒烟（SQLite，独立于 Go 测试矩阵）

脚本 `/tmp/v2smoke.py`：空库跑 V1 迁移 → 造 V1 口径数据（用户/令牌/日志/任务/五类定价配置）→
V2 dry-run → V2 apply → 复查 → 再 apply（幂等）→ B9 三态。约 40 条断言**全部通过**，其中：

- dry-run 后 `users.quota`、`ModelRatio` 未变、无 V2 状态 ✓
- apply 后：`users.quota 9.6e9 → 9.6e12`、`tokens.remain_quota ×1000`、`logs.quota ×1000`、
  `logs.other.pre_consumed_quota ×1000` 而 `model_ratio` **保持美元口径且不打标记** ✓
- 任务：`points_consumed ×1000`、`model_price ×7.3`、`model_ratio ×14.6`、`video_billing.unit_price ×7.3`、
  `price_unit = cny`、非价格字段（分辨率）原样保留 ✓
- 定价：`0.13698630137 → 2`（¥2/百万）、`0.3572 → 5.21512`、`0 → 0`、`-1` 哨兵不换算、
  视频 token/per_call/per_second 三处 ×7.3、`grok 0.05 → 0.365` ✓
- 选项：`QuotaPerUnit → 7300000000`、`quota_per_point → 10000000`、`QuotaRemindThreshold → 14600000` ✓
- 报告点名 `v1:tier(p*2)` 需重录 ✓；`tool_price_setting.prices` 缺失时跳过 ✓
- B9：未迁移且有用户 → 拒绝启动且提示迁移命令 ✓；状态丢失但数据已是新单位 → 迁移拒绝且提示恢复办法、
  quota 未被二次放大 ✓；空库 → 正常启动并写入 done ✓

### 4.3 Go 测试矩阵

`go build ./...` 通过；`go test -count=1 ./...` 全部通过。日志分别为
`/home/ubuntu/v2-verify/1e9-go-build.log` 与 `/home/ubuntu/v2-verify/1e9-go-test.log`。

三库用例用 `QUOTA_MIGRATION_TEST_PG_DSN` / `QUOTA_MIGRATION_TEST_MYSQL_DSN` 触发；本次环境未设置
PG/MySQL DSN，因此相应子用例跳过，SQLite 用例执行。跳过的外部数据库用例包括：
`controller.TestTokenMigrationFromChar48ToVarchar128{MySQL,Postgres}`、
`model.TestGetGroupsEnabledModelsOrderedPostgres`、
`service/quotaunitmigration.TestExternalDatabaseFullMigration/{postgresql,mysql}`、
`TestExternalDatabasePrecheckRejectsNarrowInteger/{postgresql,mysql}`、
`TestV2MigrationAcrossDatabases/{postgresql,mysql}` 与
`TestV2PrerequisitesRejected/{external-postgresql,external-mysql}`。

**同步更新既有测试断言**（清单不穷举；V1 期旧口径断言 → 新口径）：`common/{quota,points,compute_points}_scale_test`、
`relay/helper/price_scale_test`、`relay/helper/price_test`、`relay/video_billing_test`、
`service/{quota,task_billing,entitlement_cost,text_quota,tiered_settle,task_billing_video,funding_hybrid,
plan_fulfillment}_test`、`controller/channel_test{_scale,_internal}_test`、
`pkg/billingexpr/billingexpr_test`、`model/{redemption_points,bank_transfer_quota,topup_manual_complete}_test`、
`service/quotaunitmigration/migration_test`、Classic 的积分换算提示与提醒阈值测试。
**没有任何测试函数被删除**（逐文件比对 old/new 计数），`func Test` 净增 **+37**。

**新增 6 个文件 33 例**：

| 文件 | 覆盖 |
|---|---|
| `service/pricing_cny_examples_test.go`（9） | E2–E6 精确 quota、低价 1/50/149/150、最低扣费（0.001/0.0003/0.0001/0.0005）、**0.5 half-away → 1**、零 token 不扣费、1e12 溢出、音频/工具费/违规罚金、S1「默认价模型不被判成已改价」 |
| `relay/video_billing_cny_test.go`（3） | E7 按秒 ¥0.73×5×0.8=2,920,000,000、按次、改 `QuotaPerUnit` 不影响矩阵 |
| `pkg/billingexpr/version_test.go`（7） | v2 换算、**v1/无前缀拒绝并返回 `ErrExprV1Unsupported`**、未知版本报错、v2 端到端结算、v1 快照结算被拒 |
| `setting/ratio_setting/model_ratio_conservation_test.go`（3） | **金额守恒**：`defaultModelRatio` 全表逐个「V1 配置+V1 公式」vs「迁移后+V2 公式」，差 ≤¥1e-6；舍入上界 5e-7；默认表两处换算同一函数 + `gpt-4.1 == 14.6` |
| `service/quotaunitmigration/migration_v2_test.go`（9） | V2 三库适配的 dry-run 不写库 / apply（倍率×14.6、价格×7.3、`-1` 哨兵、视频未知键与分辨率键保留、tool 价、罚金、**全部任务 private_data** + `price_unit`、options 固定值）/ 幂等 / 断点续跑 / 独立日志库 / v1 表达式只报告不改写 / 前置条件拒绝 / 定价 JSON 写坏拒绝 / **B9 三态**；本次验证环境仅执行 SQLite，PG/MySQL 用例因无 DSN 跳过 |
| `router/api-router_removed_routes_test.go`（2） | 路由表里没有 `/api/ratio_config`、`/api/ratio_sync/*`（用 `engine.Routes()`，避免牵动全局限流中间件） |

另由我在**验收后**补 5 条：`service/tiered_settle_test.go` 的 v1 快照结算回退（见 §3.5）、以及
`migration_v2_test.go` 的「apply 拒绝 v1 表达式」「任务行级幂等（同一行改写两次值不变）」
「读回校验失败 → 整体回滚」三条（对应用户验收意见里"建议补的测试"）。

**4 处语义重写**（旧断言在新口径下已无意义，不是弱化；逐条说明）：

1. `common/quota_scale_test.go`：`QuotaScale()==14.6`、`RatioQuotaBase×2/10⁶==1` → `RatioQuotaPerToken(ratio)==ratio×1000`、
   倍率路径在 QPU 三档下不变、价格路径 ×`QuotaPerYuan` 且与 QPU 无关。
2. `common/points_scale_test.go` / `compute_points_test.go`：`USDExchangeRateFunc` 已删除 → 「跟随汇率」改为
   「换算率恒为 `QuotaPerYuan/100 = 10⁷`，改汇率/改 QPU 都不影响」，覆盖 500000 / 7300000 / 7300000000 三档。
3. `relay/video_billing_test.go`：`TestVideoMatrix_RateCancelsWhenReentered` → `TestVideoMatrix_IgnoresExchangeRate`
   （改汇率而账单完全不变，更强，直接验证 G4）。
4. `pkg/billingexpr`：快照的 `QuotaPerUnit` 改 `common.QuotaPerYuan`；两处 rounding-edge 用例改为在 ×1000 下仍有小数。

**未覆盖 / 仍不确定**：B9 只在 SQLite 验（`EnsurePriceUnitReady` 只读 options+users，与方言无关）；
V2 迁移测试复用 V1 的 `seedSmallTables` fixture 而非真实 V1 快照；删除项用路由表断言而非真发 HTTP。

`go vet ./service/quotaunitmigration/ ./pkg/billingexpr/ ./relay/helper/ ./service/ ./common/` 未通过；
`common/custom-event.go` 的 `encode` / `Render` / `WriteContentType` 按值复制含 `sync.Mutex` 的
`CustomEvent`，共 4 条 vet 报告。该文件未在本次改动中修改。

### 4.4 前端测试

`cd web/classic && bun run test` → **89 文件 / 702 用例全绿**（基线 84 文件 / 658 用例）；我独立复核过一遍，确认通过。

新增 6 个测试文件 42 例，覆盖 §7.2 要求的四类（另加 v1 表达式展示）：

| 文件 | 例数 | 覆盖 |
|---|---|---|
| `helpers/__tests__/displayCurrency.test.js` | 13 | 四种展示模式（CNY/USD/CUSTOM/TOKENS）+ **改汇率不影响人民币金额** |
| `helpers/__tests__/normalizeLogPricing.test.js` | 10 | 新旧两代日志快照、V0/V1 各自「计费过程合计 == 花费列」、`-1` 哨兵不换算 |
| `components/table/model-pricing/__tests__/pricingPagePrice.test.js` | 5 | 定价页单价与后端样例一致（E1 = 16,583,000 quota = ¥0.016583、E5 = ¥0.2/次） |
| `pages/Setting/Ratio/hooks/__tests__/editorRoundTrip.test.jsx` | 8 | 编辑器「读 → 不改 → 保存」提交 JSON 逐键相同、v1 表达式保存后变 `v2:` |
| `helpers/__tests__/pricingPagePrice.test.js`（追加） | +3 | v1 / 无前缀 / v2 的系数展示倍数 |
| `components/table/model-pricing/__tests__/dynamicPricingV1.test.jsx` | 3 | 组件级：v1 显示 ¥14.60、v2 显示 ¥14.60、并锁 v1 不出现 ¥2.00 |

v1/v2 的口径判定被抽成**单一实现**：`helpers/exprVersion.js`（前缀解析，叶子模块，避开 `render → utils` 的环）+ `helpers/exprPrice.js`（`getExprPriceScale`），日志侧与定价页共用同一份，不存在第二处前缀语法。

另更新 13 个因换算基准变化而失败的既有测试（`billingTotalRounding` 整体重写为 V2 口径：9 位、人民币、保留 `.5` 边界与 20 组随机值）。
**没有弱化任何断言**（例如 `planComparison` 的 token 数从 200 万变 400 万，是"倍率不再 ×2"的正确结果，不是放宽）。

前端改动共 42 改 / 2 删 / 4 新增（`git status --porcelain web/`）。prettier：原本干净的改动文件全干净，`HEAD` 本来就脏的 4 个文件（`NotificationSettings`、`UsageLogsColumnDefs`、`ToolPriceSettings`、`useModelPricingEditorState`）**格式未动**；eslint 对改动文件无输出；`web/classic` 与 `web/mobile` 均能构建（输出到 `/tmp`，`dist` 零改动）。

---

## 5. 上线流程的变化（与 V1 最大的不同）

1. **V2 镜像不能在窗口前拉取/在线升级**：B9 会让未迁移的库拒绝启动 → 提前升级 = 计划外停机。
   §9 已补这条提醒。
2. 因此 V2 没有"先升级镜像、再择机迁移"的安全中间态；窗口内必须一次做完：停服 → 备份 → dry-run →
   apply → 启动。
3. **发布镜像本身也要看时机**：本仓库的发版工作流会同时推 `:latest` 与日期标签。推送 `:latest` 不会
   自动更新生产（watchtower 是 API-only、不轮询），但只要有人在窗口前点了「在线升级」或手工 `pull`，
   就会拿到 V2 镜像并停机。**建议窗口内一起发版**，或发布后明确通知所有人不要升级/拉取。
4. 回滚只有一条路：**恢复备份 + 换回上一版镜像**，两者必须同时做（V2 镜像在 V1 数据上必然拒绝启动，
   这一点反而是保护：不会静默按 1/100 少收）。
5. 若状态被误删但数据已是新单位：手工写回 `options.QuotaUnitMigrationV2 = {"state":"done"}`，
   **不要重跑迁移**（会二次放大）。

---

## 6. 遗留项与风险

| 项 | 说明 | 建议 |
|---|---|---|
| 代码默认常量未跟着单位走 | `common.QuotaRemindThreshold=1000`、`checkin_setting` 默认值等在"全新库 + 从未配置"时表现为偏小的金额。**全新库要特别注意：迁移程序不会在空库上跑（B9 直接写 done），这些 key 会一直用代码默认值 → 需要手工设置 `QuotaRemindThreshold`（建议 14600000 = ¥0.0146）与签到上下限** | 后续单独一次改动把它们改成人民币口径的合理默认；短期在全新部署的初始化清单里加一步 |
| 历史日志的人民币回算 | 日志只存整型 quota，`logs.other` 的定价字段仍是美元口径，前端 `normalizeLogPricing` 按固定 7.3 换算；**历史上改过汇率的那部分日志会有偏差** | 与 V1 报告 §6 同一限制，短期接受 |
| `tasks.private_data` 的旧 `TaskVideoBilling` 注释 | 原注释写"单位是美元"，已改为人民币；库里已迁移的任务与新任务口径一致 | 无 |
| 表达式 v1 拒绝结算 | 现网没有存量表达式（§5.0），但**若有渠道/模型配过 v1**，那个模型会直接报错直到重录 | 迁移报告会点名，窗口内处理 |
| `LogQuotaShort` 的 2 位小数 | 见 §3.1 | 保持用户面值展示语义 |
| 渠道余额（`channels.balance`）在 CNY 展示下挂 ¥ 前缀显示的是上游美元数值 | 既有问题：`renderQuotaWithAmount` 的入参是「已经是美元/展示币种的金额」，渠道余额本身是上游美元余额、不在迁移清单里（§1.4） | 不在本次范围；要改需要先决定渠道余额的展示语义 |
| 用户/令牌额度输入框的步长仍是 $1 | `editUserModal`/`editTokenModal` 用 `step={getQuotaPerUnit()}`（= 7.3e9 quota = ¥7.3） | 可后续统一成 ¥1（`quota_per_yuan`），纯 UX |
| 四个「显示币种 → quota」输入框仍走美元 | `RechargeCard`、`EditRedemptionModal`、`EditUserModal`、`EditTokenModal` 用 `getCurrencyConfig().rate × quota_per_unit` 反算；因为 `QuotaPerUnit = 汇率 × QuotaPerYuan`，今天与「人民币 × quota_per_yuan」数值完全等价 | 若将来运维只改汇率而不改 `QuotaPerUnit`，这四处会与定价口径不一致；建议后续统一改成人民币路径 |

---

## 7. 生产窗口清单（copy-paste）

```bash
# 0) 窗口前：不要点「在线升级」，不要 docker compose pull
cd /opt/new-api

# 1) 停服并确认
docker compose --env-file .env.prod stop new-api
docker compose --env-file .env.prod ps new-api      # 状态必须是 Exited

# 2) 备份（记下行数与余额合计，dry-run/report 要用它们对数）
docker exec <pg-container> pg_dump -U <user> <db> > /opt/new-api/backup-$(date +%F-%H%M).sql

# 3) 预演（不写库），保存输出
docker compose --env-file .env.prod run --rm --no-deps new-api \
  --log-dir /app/logs --migrate-quota-unit-v2=dry-run | tee /opt/new-api/v2-dry-run.txt

# 4) 落地（写库），保存输出
docker compose --env-file .env.prod run --rm --no-deps new-api \
  --log-dir /app/logs --migrate-quota-unit-v2=apply | tee /opt/new-api/v2-apply.txt

# 5) 启动（B9 看到 state=done 才放行）
docker compose --env-file .env.prod up -d new-api

# 6) 启动后核对：quota_per_unit=7300000000、quota_per_yuan=1000000000、
#    quota_per_point=quota_per_compute_point=10000000，并做一次小额充值 + 一次真实调用
curl -s https://<host>/api/status | jq '{version,quota_per_unit,quota_per_yuan,quota_per_point,quota_per_compute_point,usd_exchange_rate}'
```

dry-run 报告要重点核对：`logs` 分批计划的行数、`[数值列]` 前后 CNY 两列是否相等、`[定价配置]` 是否有
差值行、是否点名了 v1 表达式。

---

## 8. 单位修订为 10⁹

本次将此前 `1 quota = ¥10⁻⁸` 的记账单位再细化一位为 `1 quota = ¥10⁻⁹`。权威值及行为如下：

- `QuotaPerYuan = 1_000_000_000`，`QuotaPerMillionTokens = 1000`，默认 `QuotaPerUnit = 7_300_000_000`。
- V2 quota 迁移为 ×1000、`targetQPU = 7_300_000_000`；points 与 compute-points 固定换算率均为 `10_000_000`。V1 的 ×14.6、定价字段的 ×7.3 和 6 位小数精度均保持不变。
- B9 启动保护提示现为倍率路径约 68.5 倍、按次/视频/工具路径约 137 倍。
- 本分支引入的人民币展示精度由 8 位改为 9 位；`LogQuotaShort` 与 `FormatQuotaShort` 仍为 2 位。
- Classic/mobile fallback 同步为 QPY 1,000,000,000、QPU 7,300,000,000、每积分/算力点 10,000,000。
- 回归断言涵盖低价 token 计费、E1 16,583,000、¥0.2 按次 200,000,000、联网搜索 219,000,000、视频 2,920,000,000，以及 V2 迁移 quota ×1000、QPU 与 points 常量。
- 同步修订银行转账、手动补单和积分换算提示测试的当前单位期望值，并更正倍率路径测试命名、迁移/任务历史单位注释及分到 quota 的注释。
- 本次验证：Go build 与全量 Go 测试通过；指定范围 `go vet` 因 `common/custom-event.go` 的既有锁值复制告警失败；Classic 前端测试 89 个文件 / 702 项全部通过。日志保存在 `/home/ubuntu/v2-verify/1e9-*.log`。
- 精确残留扫描的匹配均为新单位的常量/期望值、表示 ¥0.1 的 quota 例子、旧额度数值中仍代表 ¥0.73 的 fixture，或与 quota 无关的上限/API 哨兵/长度参数；没有遗留 `StringFixed(8)`、`toFixed(8)` 或 8 位人民币展示调用。逐项输出见 `/home/ubuntu/v2-verify/1e9-residual-scan.log`。

### int32 / 列宽审查

- Go quota 字段未发现 `int32`、`uint32` 等 32 位承载。`User.Quota`、`UsedQuota`、points/credit 余额、token/channel/task/log 等 quota 字段使用 Go `int`/`int64`；对应持久化 GORM 列为 bigint。`User.AffQuota` 与 `AffHistoryQuota` 虽为 Go `int`，标签是 `type:int`，但 GORM v1.25 按 Go 字段宽度建成 64 位整数（见 `precheck.go` 注释），因此保持不改，避免已有库的 AutoMigrate 改列；迁移 precheck 仍会拒绝任何非 64 位列。
- `topup_package.GrantPoints` 是积分数量（点数），不是 quota 记账值；其 `int` / `type:int` 不属于 quota 列。
- 迁移预检的 `numericColumnRefs()` 由 `smallNumericScopes` 中全部 scope columns 与 `logsNumericColumns` 合并去重：涵盖 users、tokens、channels、tasks、midjourneys、quota_data、redemptions、checkins、fund_entries、bank_transfer_orders、subscription_plans、user_subscriptions、subscription_pre_consume_records、compute_point_lots、限定 provider 的 top_ups，以及 logs.quota / points_consumed / credit_consumed。非 SQLite 方言逐列要求 bigint/int8 并检查 ×1000 后溢出；SQLite 使用 64 位 INTEGER。

本次验证的完整日志按任务书保存于 `/home/ubuntu/v2-verify/1e9-*.log`；提交与推送信息由后续工作记录。
