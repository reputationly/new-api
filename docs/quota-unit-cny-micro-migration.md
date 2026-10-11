# 额度单位改造：1 quota = ¥0.000001（开发任务书）

> 状态：已评审，待开发
> 读者：负责开发和自测的 agent；验收由方案作者执行（见 §7）
> 依据：所有代码位置均按 2026-10-10 的实际代码核对。行号会随开发漂移，以函数名为准。关联文档（[currency-fx-architecture.md](./currency-fx-architecture.md)、[model-pricing-rmb-input-design.md](./model-pricing-rmb-input-design.md)、[pkg/billingexpr/expr.md](../pkg/billingexpr/expr.md)）可能滞后，冲突时以代码和本文为准。

---

## 0. 开发 agent 必读

### 0.1 你要做什么

完成 §4 的全部任务（后端 B1–B6、前端 F1–F2、迁移程序 M1），按 §5 自测，按 §6 提交交付物。**不要执行 §8 的生产升级**，那一步由运维在验收（含本地演练）通过后执行。

### 0.2 硬性约束

1. **新代码在 `QuotaPerUnit = 500000` 下计费结果必须与现在完全相同**（此时 `scale = 1`）。除 §4 B4 列出的"截断改四舍五入"路径允许 ±1 quota 外，任何请求的扣费不得变化。生产升级时会在迁移前用新镜像实测这一点（§8.4 第 11 步）。
2. **不要修改任何倍率配置、模型价格、`Price`、`USDExchangeRate`。**
3. **迁移程序（M1）只实现，不在任何真实库上执行 apply。** 本地 docker 库、测试库除外。
4. 遵守仓库 `AGENTS.md`：JSON 用 `common.Marshal/Unmarshal`；SQL 三库兼容（SQLite / MySQL / PostgreSQL）；前端用 `bun`；新文件不加版权头；不改 QuantumNous / new-api 相关标识；新增文案只写中文 key。
5. 改动保持外科式：只改本文列出的位置。发现本文未覆盖但同类的位置（例如新的"倍率 → quota"路径），**先在交付物里列出，不要擅自扩大范围**。
6. 不确定时停下来，在交付物"疑问"一节写明，不要猜。

### 0.3 名词

| 名词 | 含义 |
|---|---|
| quota | 系统内部整数记账单位，存于 `users.quota`、`logs.quota` 等 |
| QPU | `common.QuotaPerUnit`，每 1 "内部美元"对应的 quota 数。当前 500000，目标 7300000 |
| 汇率 | `operation_setting.Price` 与 `operation_setting.USDExchangeRate`，生产均为 7.3，两者必须相等 |
| scale | `QPU / 500000`。当前 1，切换后 14.6 |
| 倍率路径 | 用 `ModelRatio × GroupRatio × tokens` 直接得到 quota 的计费路径（不经过 QPU） |
| 价格路径 | 用 `美元单价 × QPU` 得到 quota 的路径（按次价、视频、分层表达式等） |

---

## 1. 背景

### 1.1 问题

用户 junm1888（id 219）反馈：使用日志「计费过程」的合计与「花费」列不一致。

| 项 | 值 |
|---|---|
| 用量 | 输入 199 tokens，输出 4096 tokens，`deepseek-v4-1-flash-260910` |
| 单价 | 输入 ¥2 / 1M，输出 ¥8 / 1M，分组倍率 0.5 |
| 计费过程（前端公式） | (199×2 + 4096×8) × 0.5 ÷ 10⁶ = **¥0.016583** |
| 花费列（实扣） | **¥0.016586** |

### 1.2 根因

- QPU = 500000、汇率 7.3，所以 **1 quota = ¥0.0000146**。
- 后端 `service/text_quota.go` 算出 1135.8219 quota，`Round(0)` 得 1136 quota = ¥0.0165856，6 位显示 ¥0.016586。
- 前端公式不取整，得 ¥0.016583。差值是取整误差，最大 ±0.5 quota（±¥0.0000073）。生产核对用户 219 近一月 2077 条日志，全部 `|实扣 − 公式| ≤ 0.4986 quota`，没有其他差异来源。
- 列表、导出（`controller/log.go`）、左上角消耗额度（`model/log.go`）都基于同一整数 `logs.quota`，彼此一致；只有前端公式没取整。

本质：quota 粒度（¥0.0000146）与展示精度（6 位小数，¥0.000001）不在同一网格上。

### 1.3 目标

让 **1 quota = ¥0.000001**：

- 切换后新日志的计费过程合计（6 位）与花费列、导出、消耗额度**逐位一致**；
- 用户人民币余额、模型人民币价格、倍率配置都不变。

已否决的替代方案见 §10.2。

---

## 2. 核心设计

### 2.1 不变量

```
QPU = 汇率 × 1,000,000 = 7.3 × 10⁶ = 7,300,000
```

于是 `1 quota = 汇率 / QPU = ¥0.000001`。代码里有两个"汇率"option，必须相等：

| option | 用途 |
|---|---|
| `Price` | 支付宝 / 微信直连充值、充值套餐（`controller/topup_alipay.go`、`topup_wxpay.go`、`topup_package.go`：¥ × QPU ÷ Price）；算力点「元 → 点」（`common/compute_points.go` 的 `USDExchangeRateFunc` 注入的是 `Price`）；渠道余额换算（`controller/channel-billing.go`） |
| `USDExchangeRate` | 对公转账入账（`model/bank_transfer.go`）；日志 / 导出 / 对账的 quota → ¥；前端展示 |

### 2.2 scale

约定"倍率 1 = $2 / 1M tokens"。由此：

```
quota/token = ratio × 2 / 10⁶ × QPU = ratio × (QPU / 500000) = ratio × scale
```

现在倍率路径直接把 `ratio` 当作 quota/token，是 QPU = 500000 时的巧合。改造后倍率路径统一乘 `scale`；价格路径已经乘了 QPU，**不能再乘 scale**。

### 2.3 切换后的同一条日志

```
quota = (199 + 4096×4) × 0.13698630137 × 0.5 × 14.6 = 16582.99999998 → Round → 16583
花费  = 16583 × ¥0.000001 = ¥0.016583 = 公式结果
```

### 2.4 一致性成立的条件

1. 后端所有结算路径对最终 quota 使用**四舍五入**（不是截断）；
2. 前端合计在**人民币金额**上按 6 位四舍五入（远离零，带浮点容差），规则与后端 `decimal.Round(0)` 相同。

满足后，「计费过程合计 = 花费列 = 导出 = 消耗额度分量」。

---

## 3. 流程

代码与数据**一次停服升级**，不分阶段上线，**不做生产数据演练**（生产库不导出，2026-10-10 决定）。"新代码在旧单位下扣费不变"改为在停服窗口内、迁移之前用新镜像实测（§8.4 第 9–11 步）；真实数据的意外情况由 dry-run 报告的检查点拦截（§8.4 第 15 步），检查点不通过时数据尚未改动，换回旧镜像即可恢复服务。

| 步骤 | 内容 | 谁做 | 状态 |
|---|---|---|---|
| 1 | 开发 B1–B6、F1–F2、M1，自测（§5），提交交付物（§6） | 开发 agent | 已完成 |
| 2 | 代码审查与自动化验收（§7.1、§7.2） | 方案作者 | 2026-10-10 通过 |
| 3 | 生产停服升级（§8） | 运维 | 待执行 |
| 4 | 收尾（§9） | 开发 agent | 待执行 |

§7.3 本地演练保留作参考，本次不执行。

**新镜像必须打明确的版本号**（不要只用 `:latest`），并记录当前生产镜像的版本号或 digest，回滚时使用（§8.6）。迁移后的库只能由新镜像连接：旧代码不认 scale，按倍率计费的模型价格会变成原来的 1/14.6。

---

## 4. 开发任务

每个任务给出：改动位置、要求、完成标准。完成标准里的测试必须写成自动化测试。

### B1 新增 `common.QuotaScale()`

- 位置：`common/constants.go`（`QuotaPerUnit` 定义旁）。
- 要求：

  ```go
  // RatioQuotaBase 是"倍率 1 = $2 / 1M tokens = 1 quota / token"成立时的 QuotaPerUnit。
  const RatioQuotaBase = 500000.0

  func QuotaScale() float64 { return QuotaPerUnit / RatioQuotaBase }
  ```

- 完成标准：单测覆盖 QPU = 500000 → 1、QPU = 7300000 → 14.6。

### B2 倍率路径乘 scale

| 位置 | 函数 | 要求 |
|---|---|---|
| `service/text_quota.go` | `calculateTextQuotaSummary` 按倍率分支 | 在 `ratio := dModelRatio.Mul(dGroupRatio)` 处再乘 `decimal.NewFromFloat(common.QuotaScale())`。文本 / Claude / Embedding / Rerank / 图片结算都经过这里 |
| `service/quota.go` | `calculateAudioQuota` 按倍率分支 | 同上。WSS / Realtime / 音频预扣与结算都经过这里 |
| `relay/helper/price.go` | `ModelPriceHelper` 按倍率预扣 | `preConsumedTokens × ratio` 再乘 scale。`preConsumedTokens` 的下限 `common.PreConsumedQuota` 是 token 数，**不乘 scale**，迁移也不改它（见 M1.7） |
| `service/task_billing.go` | `RecalculateTaskQuotaByTokens` | 再乘 scale |
| `controller/channel-test.go` | `settleTestQuota` | 再乘 scale |

**禁止**乘 scale 的位置（已经乘 QPU，自动跟随）：按次价 / UsePrice 分支（`ModelPriceHelperPerCall`，含 `modelRatio / 2 × QPU` 兜底）、视频矩阵（`relay/video_billing.go`、`RecalculateTaskQuotaByVideoMatrix`）、分层表达式（`pkg/billingexpr`、`service/tiered_settle.go`）、工具附加费（`service/tool_billing.go`）、违规罚金（`service/violation_fee.go`）、权益成本（`service/entitlement_cost.go`）、Gemini 音频 $/M、充值入账、对账、日志金额换算、导出。

完成标准：

- 每个位置各一组单测，QPU = 500000 与 7300000 两种取值。7300000 下断言 **quota = round(公式人民币金额 × 10⁶)**。
- 必含回归样例：prompt 199、completion 4096、model_ratio 0.13698630137、completion_ratio 4、group_ratio 0.5：QPU = 500000 → 1136；QPU = 7300000 → 16583。
- `ModelPriceHelper` 预扣：`promptTokens = 100`（小于 `PreConsumedQuota = 500`）、`MaxTokens = 0`，断言 QPU = 500000 时预扣 = round(500 × ratio × group)，QPU = 7300000 时 = round(500 × ratio × group × 14.6)，即两者人民币冻结额相同。
- `calculateTextQuotaSummary` 覆盖：纯文本、缓存命中、缓存创建（5m / 1h）、图片 token、音频 token、`OtherRatios`、Claude 语义、quota ≤ 0 兜底为 1。

### B3 OpenRouter 缓存换算与 QPU 解耦

- 位置：`service/quota.go` `CalcOpenRouterCacheCreateTokens`，`quotaPrice := priceData.ModelRatio / common.QuotaPerUnit`。
- 要求：这里的语义是"倍率 → 美元/token"，改为 `priceData.ModelRatio / common.RatioQuotaBase`。
- 完成标准：单测断言 QPU = 7300000 时结果与 500000 时**完全相同**。

### B4 截断改四舍五入

切换后 `价格 × QPU` 常落在 `99999.9999…`，`int()` 截断会少扣 1 quota。统一改为 `int(math.Round(x))` 或 decimal `.Round(0).IntPart()`。

| 位置 | 现状 |
|---|---|
| `service/quota.go` `calculateAudioQuota` UsePrice 分支 | `quota.IntPart()` 未 Round |
| `relay/helper/price.go` `ModelPriceHelperPerCall` | `int(modelPrice × QPU × group)` 及倍率兜底 `int(modelRatio / 2 × QPU × group)` |
| `relay/helper/price.go` `ModelPriceHelper` 预扣 | `int(float64(preConsumedTokens) * ratio)` 等（只影响冻结量，同步改为 Round） |
| `relay/video_billing.go` | `int(price × QPU × group)`、`int(total × QPU × group)` |
| `service/task_billing.go` | `RecalculateTaskQuotaByTokens`、视频矩阵重算中的 `int(...)` |
| `relay/relay_task.go` | `int(float64(quota) * ra)`、`int(float64(quota) / ra)` |
| `controller/channel-test.go` `settleTestQuota` 按次分支 | `int(priceData.ModelPrice * common.QuotaPerUnit)`（渠道测试实扣）。只改舍入；该分支不乘分组倍率是现状，不在本次范围 |
| `service/entitlement_cost.go` `EntitlementModelCost` 计算 | `int64(unitPrice * common.QuotaPerUnit * ratio)`（套餐权益成本估算，只用于后台展示与风险提示，不扣费）。为口径一致一并改 |

完成标准：每处构造一个会落在 `N − 1e-9` 的输入（例：¥0.1 按次价 → `0.1 / 7.3` 美元，QPU = 7300000），断言得到 100000 而不是 99999。

#### B4.1 充值入账：先乘后除

充值换算是"金额 → quota"，现有代码有意截断（用户拿整数部分，误差 < 1 quota），**保持截断语义不变**。但必须先乘 QPU 再除汇率，否则十进制除法的有限精度会让结果落在 `N − ε`、截断后少 1：

| 位置 | 现状 | 问题 |
|---|---|---|
| `model/bank_transfer.go` `BankTransferQuotaForFen` | `fen ÷ 100 ÷ rate × QPU`，最后 `IntPart()` | **先除后乘**。`decimal` 除法默认保留 16 位小数：100 分 → `1 ÷ 7.3 = 0.1369863013698630`（已截断）`× 7300000 = 999999.9999999999` → 999999，少 1。改为 `fen × QPU ÷ 100 ÷ rate` |

已确认先乘后除、切换后结果为精确整数、无需改动：`controller/topup_alipay.go`、`controller/topup_wxpay.go`、`controller/topup_package.go`（`金额 × QPU ÷ Price`）；`model/topup.go`、`controller/topup.go` 中 `整数金额 × QPU` 的入账（Waffo / Epay / Stripe / 手动补单，B6 另行修正渠道判断）。

完成标准：QPU = 7300000、`USDExchangeRate = 7.3` 时 `BankTransferQuotaForFen(100) = 1000000`、`BankTransferQuotaForFen(1) = 10000`、`BankTransferQuotaForFen(12345) = 123450000`；QPU = 500000 时结果与改动前相同。

#### B4.2 已核对无需改动

- 已经是四舍五入：`pkg/billingexpr/round.go` `QuotaRound`、`service/text_quota.go` `composeTieredTextQuota`、`service/tool_billing.go`、`service/violation_fee.go`。
- 非计费换算：`controller/token.go` 令牌额度上限、`controller/topup_stripe.go` / `controller/topup.go` 最低充值额、`model/user.go` 提示文案、`common/quota.go`，都是"整数 × QPU"，没有舍入问题。

验收时会用 `rg -n "int\(.*QuotaPerUnit|int64\(.*QuotaPerUnit|IntPart\(\)" --type go --glob '!*_test.go'` 全量扫描，每一条结果都必须落在 B4 表、B4.1 或 B4.2 中。新发现的位置写进交付物"疑问"。

### B5 积分 / 算力点换算率改为运行时计算

现状问题：

- `common/points.go` `QuotaPerPoint = QuotaPerUnit / 730.0`、`common/compute_points.go` `QuotaPerComputePoint = QuotaPerUnit / 730.0`、`setting/operation_setting/points_setting.go` 与 `compute_point_setting.go` 的默认值，都在**包初始化时**按当时的 QPU（500000）算出并固定。之后从数据库加载新的 QPU 不会重算。
- `controller/misc.go`（`/api/status`）与 `controller/pricing.go` 直接读配置结构体字段，绕过了 `common.getQuotaPerPoint()`。
- 生产 `options` 表里没有 `points_setting.quota_per_point` 和 `compute_point_setting.quota_per_compute_point`，用的正是这个固定默认值。

要求：

1. `points_setting.go`、`compute_point_setting.go` 默认值改为 `0`，含义"自动"。`compute_point_setting.quota_per_compute_point` 前端没有编辑界面；`points_setting.quota_per_point` 在后台「积分设置」有编辑框（`SettingsPoints.jsx`），其默认值与文案见 F1。
2. `common` 新增导出函数 `GetQuotaPerPoint()`、`GetQuotaPerComputePoint()`：配置值 > 0 用配置值；否则返回 `QuotaPerUnit / (getUSDExchangeRate() × 100)`。原有 `getQuotaPerPoint` / `getQuotaPerComputePoint` 改为调用它们。
3. `controller/misc.go`、`controller/pricing.go` 下发的 `quota_per_point`、`quota_per_compute_point` 改为调用上面两个函数。
4. 删除或改写 `common.QuotaPerPoint`、`common.QuotaPerComputePoint` 包级变量的引用，确保没有代码再读初始化时固定的值。

完成标准：

- 单测：QPU = 500000、汇率 7.3 → 684.93…（与现在相同）；QPU = 7300000、汇率 7.3 → 10000；配置值 > 0 时用配置值。
- `YuanToComputePoints(¥1) = 100` 在两种 QPU 下都成立。
- `/api/status` 返回值在生产配置下与现在相同。

**语义决定（替换 `common/compute_points_test.go` 原有的 `TestComputePoints_ModelCostUnaffectedByExchangeRate`）：**

原测试断言"同一笔 quota 换出的算力点数不随汇率变化"，靠初始化期冻结 `QuotaPerUnit / 730` 实现。新语义是"**1 点 = 1 分钱**"，分两条路径：

| 路径 | 何时生效 | 语义 | 测试 |
|---|---|---|---|
| 配置值（`quota_per_point` / `quota_per_compute_point` > 0） | 生产迁移后（M1.7 显式写入 10000） | 换算率固定，与汇率、QPU 都无关；同一笔 quota 换出的点数不随汇率变化——**原测试的业务意图在这条路径上完整保留** | 新增：配置值 = 10000 时，把 `USDExchangeRateFunc` 从 7.3 改为 8.5，`QuotaToComputePoints(1000000)` 仍为 100；积分同样一条 |
| 自动（配置值为 0） | 未配置的环境、新部署 | `QPU / (汇率 × 100)`；在 §10.1 不变量 `QPU = 汇率 × 10⁶` 下恒为 10000 | 用不变量下的等价断言替换原测试：汇率 / QPU 取 (7.3, 7300000)、(8.5, 8500000)、(7, 7000000)，换算率均为 10000，`YuanToComputePoints(1) = 100`，`QuotaToComputePoints(1000000) = 100` |

代价：自动路径下"只改汇率、不改 QPU"会改变点数消耗速度。这本身违反 §10.1，不为它设计兼容。

### B6 修正 `ManualCompleteTopUp`

- 位置：`model/topup.go` `ManualCompleteTopUp`。
- 现状：除 Stripe 外一律 `Amount × QuotaPerUnit`。但 `alipay_direct`、`wxpay_direct`、`creem`、`bank_transfer_manual` 的 `amount` 已经是 quota（见 `RechargeAlipay`、`RechargeWxpay` 的 `quotaToAdd = int(topUp.Amount)`，`RechargeCreem` 的 `quota = topUp.Amount`，`model/bank_transfer.go` 写入 `Amount: quotaToAdd`）。管理员对这些订单手动补单会多发 QPU 倍。
- 要求：按 `payment_provider` 区分，上述渠道直接用 `Amount`；`epay`、`waffo`、`waffo_pancake` 保持 `Amount × QPU`；Stripe 保持现状。
- 完成标准：每个 `payment_provider` 一条单测，断言到账 quota 与对应自动回调路径一致。

### F1 硬编码 500000 / 684.93 改为读配置

| 位置 | 问题 | 改法 |
|---|---|---|
| `web/classic/src/components/settings/personal/cards/NotificationSettings.jsx` 提醒阈值预设 | 写死 `100000 → $0.2` 等 | 按人民币金额（如 ¥1 / ¥10 / ¥100）× `quota_per_unit` ÷ 汇率 生成，标签用当前币种 |
| `web/classic/src/components/settings/PersonalSetting.jsx` 默认阈值 | `\|\| 500000` | 改为 `getQuotaPerUnit()` |
| `web/classic/src/components/table/tokens/modals/EditTokenModal.jsx`、`table/users/modals/EditUserModal.jsx` | `step={500000}` | `step={getQuotaPerUnit()}` |
| `web/classic/src/pages/BankTransfer/TransferTab.jsx`、`pages/Setting/Ratio/components/TieredPricingEditor.jsx` | 读不到配置时 fallback 500000 | 统一走 `helpers/quota.js` `getQuotaPerUnit()` |
| `web/classic/src/helpers/quota.js` | `getQuotaPerUnit` fallback `'1'`，与 `TransferTab.jsx`、`TieredPricingEditor.jsx` 的 500000 不一致（`render.jsx` 直接读 localStorage，没有 fallback，本次不改）；`quota_per_point` / `quota_per_compute_point` fallback `'684.93'` | 这些值正常情况下都由 `/api/status` 写入 localStorage，fallback 只在读不到时生效。要求：fallback 只在 `quota.js` 定义一处，`TransferTab.jsx`、`TieredPricingEditor.jsx` 改为调用 `getQuotaPerUnit()`；读不到时 `console.warn` 一次。fallback 取值在交付物中说明理由 |
| `web/classic/src/pages/Setting/Operation/SettingsGeneral.jsx` | `QuotaPerUnit !== 500000` 时显示 Tokens 选项；"默认值500000"文案 | 去掉哨兵判断；更新文案 |
| `web/classic/src/components/settings/OperationSetting.jsx` 选项默认值、`pages/Setting/Operation/SettingsPoints.jsx` 表单初始值 | `'points_setting.quota_per_point': 684.93` | 改为 `0`，与后端默认（0 = 自动）一致。后端 `/api/option/` 经 `ExportAllConfigs` 会下发实际值，这里只是加载前的占位 |
| `SettingsPoints.jsx`「每积分对应额度」编辑框的 `extraText` | 「1 积分 = 1 分钱时约为 684.93，上线后不建议修改」。B5 之后编辑框在迁移前显示 0、迁移后显示 10000，这句文案会误导管理员手动填回 684.93；迁移后填 684.93 会让积分价值缩水为 1/14.6 | 改为不含写死数值的说明，并显示当前生效值，例如：「0 = 自动（1 积分 = 1 分钱）；当前生效 {{value}}，上线后不建议修改」，`value` 取 localStorage 的 `quota_per_point`（`/api/status` 经 B5 的 `GetQuotaPerPoint()` 下发）。只加中文 key；`en.json` 中旧 key 的条目不必同步 |

完成标准：`localStorage.quota_per_unit` 分别为 `'500000'`、`'7300000'` 时的组件 / 函数测试；500000 下渲染结果与现在一致。积分设置页：`localStorage.quota_per_point` 为 `'684.93'` 与 `'10000'` 时，说明文案分别显示对应的当前生效值。

### F2 计费过程合计按人民币 6 位四舍五入

背景：此前曾临时加过 `settleUsd`（合计直接显示实扣值），已撤回，不要再加。

范围原则：**只改生产展示配置（`quota_display_type = CNY`）下用户实际能看到的"算式 = 合计"**。不可达分支、没有合计的渲染函数都不改。

#### F2.1 改动范围（均在 `web/classic/src/helpers/render.jsx`）

| # | 函数 | 合计 | 说明 |
|---|---|---|---|
| 1 | `renderModelPrice` | 按次分支 `按次 {{symbol}}{{price}} * {{ratioType}} {{ratio}} = {{symbol}}{{total}}` 的 total；按 token 分支 `{{inputDesc}} + {{outputDesc}}{{extraServices}} = {{symbol}}{{total}}` 的 total | 两者都在 `if (!shouldUseRatioBillingProcess(modelPrice))` 块内，由 `formatBillingDisplayPrice(...)` 生成 |
| 2 | `renderAudioModelPrice` | 同上：按次分支 `模型价格 ... / 次 * ... = total`；按 token 分支 `文字输入 ... + 音频输出 ... = total` | 同上 |
| 3 | `renderClaudeModelPrice` | 同上：按次分支；`{{breakdown}} * {{ratioType}} {{ratio}} = {{symbol}}{{total}}` | 同上 |
| 4 | `buildVideoMatrixDetailRows` | 三种计价方式（按秒 / 按 token / 按次）「计费过程」行末尾的 `= {{symbol}}{{(amount * rate).toFixed(6)}}` | 视频详情 |
| 5 | `renderVideoMatrixPriceSimple` | 按秒、按 token 两个 formula 的 `amount`（`Number((amount * rate).toFixed(6))`） | 列表里的视频摘要 |

#### F2.2 明确不改

| 位置 | 原因 |
|---|---|
| 倍率模式算式（`renderDisplayAmountFromUsd` 生成的分项与「合计：」） | 只在 `shouldUseRatioBillingProcess` 为真时进入，即 `quota_display_type = TOKENS`；生产是 CNY，不可达。TOKENS 模式下显示的是 token 数，不涉及人民币取整 |
| 上述三个函数里 `shouldUseRatioBillingProcess` 块之后的 `if (modelPrice !== -1)` 按次分支（`displayTotal = (modelPrice * groupRatio * rate).toFixed(6)`） | 进入该块要求 `modelPrice === -1`，此分支是死代码。不改、不删（不在本次范围，交付物中列出即可） |
| `renderTieredModelPrice` | 只展示命中档位与各档单价，没有合计 |
| `renderTaskBillingProcess` | 只包装传入的 `content` 或显示预扣说明，自身没有合计 |
| 所有单价、分项金额 | 沿用现有格式 |
| `web/mobile` | 不展示计费过程 |

开发时若发现 CNY 展示下还有其他可达的"= 合计"，列入交付物"疑问"，不要擅自扩大范围。

#### F2.3 要求

1. 新增 `roundToDisplay(value, digits = 6)`：四舍五入、远离零，带浮点容差（例如对 `|value| × 10^digits` 先加一个相对 `Number.EPSILON` 量级的修正再 `Math.round`），不得用 `toFixed` 做舍入。
2. 在**显示币种金额**上舍入（美元金额 × 汇率之后），不是在美元上舍入。
3. 舍入后的输出格式与现状一致：F2.1 第 1–4 项保持固定 6 位小数；第 5 项保持现有的"去掉末尾 0"写法。
4. `quota_display_type` 为 USD / CUSTOM 时同样走 `roundToDisplay`（规则不变，只是避免 `toFixed` 的二进制舍入问题）；TOKENS 不涉及。

#### F2.4 完成标准

- `roundToDisplay` 单测：`0.0165835 → 0.016584`、`0.0165825 → 0.016583`、`-0.0000005 → -0.000001`、`1.0000005 → 1.000001`、`0.1 + 0.2 → 0.3`。
- F2.1 每一项至少一个单测：`quota_per_unit = '7300000'`、汇率 7.3、CNY 展示，断言合计 = `round(金额 × 10⁶) / 10⁶` 的 6 位字符串，即与花费列（`renderQuota(quota, 6)`）一致。
- `renderModelPrice` 额外做一组固定随机种子的 20 组随机 token / 单价 / 分组倍率用例。
- 截图样例：199 / 4096 tokens、输入 ¥2 / 1M、输出 ¥8 / 1M、分组 0.5 → 合计 `0.016583`。
- `quota_per_unit = '500000'` 下现有测试全部通过。

### M1 迁移程序

#### M1.1 入口

- 在主程序增加启动参数 `--migrate-quota-unit=<dry-run|apply>`（`common/init.go` 的 flag 体系）。
- 带该参数时：完成环境与数据库初始化（`model.InitDB`、`model.InitLogDB`，不启动 HTTP 服务、不启动定时任务、不连 Redis 也能运行），执行迁移，打印报告，退出码 0 / 非 0。
- 生产用法（§8）：`docker compose run --rm --no-deps new-api --migrate-quota-unit=dry-run`。

#### M1.0 列类型预检

数值放大 14.6 倍后，32 位整数列在值 ≥ 147,087,541（旧单位，约 ¥2,147）时会溢出。dry-run 与 apply 开始前都要检查：

- PostgreSQL / MySQL：查 `information_schema.columns`，M1.5 中每一列的类型必须是 64 位整数（PG `bigint`；MySQL `bigint`）。任一列不是，**拒绝执行**，退出码非 0，并打印该库方言下的 DDL（PG `ALTER TABLE ... ALTER COLUMN ... TYPE bigint`；MySQL `ALTER TABLE ... MODIFY COLUMN ... BIGINT ...`），由运维在停服窗口内手动执行后重跑。迁移程序不自动执行 DDL。
- SQLite：`INTEGER` 本身是 64 位，跳过。
- 同时检查每列 `max(abs(col)) × 14.6` 不超过 `9.2e18`（理论上不会触发，作为保险）。

关于 `users.aff_quota` / `users.aff_history` 的 `gorm:"type:int"` 标签：GORM v1.25 把 `type:int` 识别为通用整数类型，宽度仍取 Go 字段的 64 位，三库建出来分别是 PG `bigint`、MySQL `bigint`、SQLite `integer`（`gorm.io/gorm/schema/field.go` 对 TYPE 标签的处理 + `gorm.io/driver/postgres` / `mysql` 的 `DataTypeOf`）。生产查询也确认两列都是 bigint。所以这两列**不需要改标签**；预检只是兜底，防止有人手工建过表。

#### M1.2 状态机与幂等

使用 `options` 表的一个 key `QuotaUnitMigration` 记录状态（JSON：`{"state": "...", "logs_cursor": N, "logs_cutoff": M, "started_at": ..., "finished_at": ...}`）。

| 当前状态 | 行为 |
|---|---|
| key 不存在，且 QPU（库里的 `QuotaPerUnit`，不存在则为 500000）= 500000 | 执行小表阶段 |
| key 不存在，且 QPU ≠ 500000 | 拒绝执行，报错退出 |
| `state = logs` | 从 `logs_cursor` 继续日志阶段 |
| `state = done` | 打印"已完成"，退出码 0，不改任何数据 |

#### M1.3 小表阶段（单事务）

一个数据库事务内完成以下全部内容，任一步失败整体回滚：

1. §4 M1.5 中除 `logs` 外的所有列。
2. §4 M1.6 中 `tasks.private_data`、`users.setting` 的 JSON 键。
3. §4 M1.7 的 `options`。
4. 写入 `QuotaPerUnit = 7300000`。
5. 写入 `QuotaUnitMigration = {"state":"logs","logs_cursor":0,"logs_cutoff":max(logs.id)}`。

#### M1.4 日志阶段（分批）

- 按 `id` 升序，每批 5000 行（`id > cursor AND id <= min(cursor + 5000, cutoff)`）。
- 每批一个事务：数值列用一条 `UPDATE ... SET col = ROUND(col * 14.6) WHERE id BETWEEN ...`；`other` 读出该区间内所有非空行，在 Go 中逐行解析（不用键名 `LIKE` 预筛，以便完整统计非法 JSON），**只对至少一个 M1.6 键被改写的行执行 UPDATE**；最后更新 `logs_cursor`。
- 未命中任何 M1.6 键的行一律不写回，哪怕它能被成功解析。否则 80 多万行会被整体重写：WAL 与耗时成倍增加，而且重新序列化可能改变原文（键顺序、数字格式、转义）。
- 生产预期：被改写的 `other` 行数与 §11 的 22,858 行（`LIKE` 统计）同量级。dry-run 报告中的"将改写行数"与该值偏差较大时，停下排查，不要 apply。
- 全部完成后写 `state = done`、`finished_at`。
- `LOG_SQL_DSN` 单独配置时，日志在另一个库，事务边界按库分别处理，cursor 仍存主库 `options`。

舍入说明：`n × 14.6` 的小数部分只可能是 0、0.2、0.4、0.6、0.8，不会出现 .5，因此三库 `ROUND` 的舍入规则差异不影响结果；SQLite 浮点误差只会出现在整数附近，`ROUND` 仍得到正确整数。

#### M1.5 数值列（× 14.6 四舍五入）

| 表 | 列 |
|---|---|
| `users` | `quota`, `used_quota`, `aff_quota`, `aff_history`, `points_balance`, `points_used`, `aff_points_earned`, `credit_limit`, `credit_used`, `credit_settled` |
| `tokens` | `remain_quota`, `used_quota` |
| `channels` | `used_quota` |
| `logs` | `quota`, `points_consumed`, `credit_consumed` |
| `tasks` | `quota` |
| `midjourneys` | `quota` |
| `quota_data` | `quota` |
| `redemptions` | `quota` |
| `checkins` | `quota_awarded` |
| `fund_entries` | `quota_delta` |
| `bank_transfer_orders` | `quota_granted` |
| `subscription_plans` | `total_amount`, `compute_points_per_period` |
| `user_subscriptions` | `amount_total`, `amount_used` |
| `subscription_pre_consume_records` | `pre_consumed` |
| `compute_point_lots` | `points_total`, `points_used` |
| `top_ups` | `amount`，**仅** `payment_provider IN ('alipay_direct','wxpay_direct','creem','bank_transfer_manual')`，所有状态 |

不动：`top_ups.money`、所有 `*_fen`、`price_amount`、`subscription_orders.money`（金额）；`top_ups` 中 `epay`、`stripe`、`waffo`、`waffo_pancake` 的 `amount`（金额，入账时再 × QPU）；`topup_packages.grant_points` 等"多少个积分"的字段。

开发前用 `rg` 核对每张表的真实表名（GORM 默认复数蛇形，如 `TopUp` → `top_ups`；有 `TableName()` 的以其为准），交付物里列出核对结果。若发现本表未列出、但以 quota 为单位的列，列入"疑问"，不要擅自加入。

#### M1.6 JSON 键

| 存储 | 键 | 处理 |
|---|---|---|
| `logs.other` | `fee_quota`, `cost_quota`, `compute_points_quota`, `pre_consumed_quota`, `actual_quota`, `wallet_quota_deducted`, `subscription_pre_consumed`, `subscription_post_delta`, `subscription_consumed`, `subscription_remain`, `subscription_total`, `subscription_used` | × 14.6 四舍五入 |
| `logs.other` | `compute_points`, `entitlement_fallback.points_needed`, `entitlement_fallback.points_available` | 不动（展示点数） |
| `tasks.private_data` | `points_consumed`, `credit_consumed`, `entitlement_spent[].amount`, `aggregate.stage1_adaptor_quota` | × 14.6 四舍五入 |
| `tasks.private_data` | `entitlement_discount`, `billing_context.model_price`, `video_billing.unit_price` | 不动（比率 / 美元单价） |
| `users.setting` | `quota_warning_threshold` | × 14.6 四舍五入 |

要求：

- 只改上表的键，其余键的值保持语义不变（键顺序、空白、HTML 转义可能变化，可以接受）：顶层解析为 `map[string]json.RawMessage`（嵌套对象 `aggregate`、数组 `entitlement_spent` 同理逐层取 RawMessage），只替换目标键的值，其他键的 RawMessage 原样写回。不要把整行解析成 `map[string]any` 再序列化——大整数会经 float64 丢精度，数字格式也可能变化。
- 目标键的值按数字解析（兼容 `1136` 与 `1136.0`），× 14.6 四舍五入后写为整数。值为 `null`、字符串或其他非数字类型时不改，计入报告。
- 空字符串跳过，不计为非法。非法 JSON 的行跳过并计数；报告列出总数与前 100 个 id，完整 id 列表写入报告同目录的文件。
- JSON 编解码走 `common.Marshal` / `common.Unmarshal`。

#### M1.7 `options`

规则：库里有该 key 则按"处理"列改写；库里没有则写入"缺失时写入"列的值。

| key | 处理 | 缺失时写入 |
|---|---|---|
| `QuotaForNewUser`, `QuotaForInviter`, `QuotaForInvitee` | × 14.6 四舍五入 | 不写 |
| `QuotaRemindThreshold` | × 14.6 四舍五入 | 14600（代码默认 1000 × 14.6） |
| `checkin_setting.min_quota`, `checkin_setting.max_quota` | 按浮点解析 × 14.6 四舍五入，写整数 | 不写 |
| `points_setting.quota_per_point`, `compute_point_setting.quota_per_compute_point` | 写 10000 | 10000 |
| `QuotaPerUnit` | 写 7300000 | 7300000 |

**`PreConsumedQuota` 不动、不写入。** 名字叫 quota，实际是 **token 数下限**：唯一的计费用法是 `relay/helper/price.go` `ModelPriceHelper` 中 `preConsumedTokens := common.Max(promptTokens, common.PreConsumedQuota)`，随后 `preConsumedTokens × ratio` 才得到 quota，B2 会在这一步乘 scale。若再把它 × 14.6，冻结额会被放大两次（变成 14.6 倍）。后台「请求预扣费额度」这个标签有误导，本次不改，记入 §10.3。

不动：`*_points` 配置、`MinTopUp`、`payment_setting.*`、`bank_transfer_setting.*`、`Price`、`USDExchangeRate`、各类倍率、`ModelPrice`、`grok.violation_deduction_amount`。

#### M1.8 dry-run

不写库。输出：

- 每张表 / 每个 JSON 键的受影响行数；
- 每个数值列迁移前合计、迁移后预计合计，以及两者换算成人民币（前 `÷ 500000 × 汇率`，后 `÷ 7300000 × 汇率`）的差；
- `options` 每个 key 的旧值 → 新值；
- 非法 JSON 行数与 id；
- `logs` 预计批次数。

#### M1.9 apply 报告

与 dry-run 同格式，并附每阶段耗时。

---

## 5. 开发自测

### 5.1 环境

- 后端单测：`go test ./...`。
- 前端单测：`cd web/classic && bun install && bun run test`。
- 本地 PostgreSQL + Redis：`docker compose -f docker-compose.dev.yml up -d`（PG 15，`postgresql://root:123456@localhost:5432/new-api`，需自行映射端口或在容器网络内运行）。
- MySQL / SQLite：迁移程序测试至少覆盖 SQLite（内存库即可）与 PostgreSQL；MySQL 用临时 docker 容器跑一次。

### 5.2 必须通过的测试

| 范围 | 内容 |
|---|---|
| 全量回归 | `go test ./...`、`bun run test` 全部通过 |
| 现有写死 500000 的测试 | `service/tiered_settle_test.go`、`pkg/billingexpr/billingexpr_test.go`、`service/plan_fulfillment_test.go`、`service/billing_session_channel_test.go`、`relay/helper/price_test.go`、`service/text_quota_test.go`、`common/points_test.go`、`common/compute_points_test.go` 原断言不改，作为 scale = 1 的回归；另加 7300000 参数化用例 |
| B1–B6、F1–F2 | 各任务"完成标准"中的测试 |
| 分层表达式 / 视频 / 按次 | QPU = 7300000 下各一组，断言 quota = round(人民币金额 × 10⁶) |
| 充值入账 | QPU = 7300000、汇率 7.3：支付宝 / 微信 ¥1 → 1000000；对公转账 100 分 → 1000000；充值套餐同理 |
| M1 迁移 | 见 §5.3 |

测试中修改 `common.QuotaPerUnit`、`USDExchangeRateFunc` 等全局变量后必须 `t.Cleanup` 还原，不得影响其他测试。

### 5.3 迁移程序测试

1. **全覆盖 fixture**：为 M1.5、M1.6、M1.7 的每一项构造数据（含 `top_ups` 每种 `payment_provider` 与状态、`logs.other` 全部键与不动键、`tasks.private_data` 嵌套数组、`users.setting`、非法 JSON、空字符串、options 存在与缺失两种情况）。断言迁移后每个值 = `round(旧 × 14.6)` 或保持不变。
2. **幂等**：apply 两次，第二次不改任何数据，退出码 0。
3. **断点续跑**：日志阶段第 N 批后人为中断（测试里注入错误），再次 apply，结果与一次跑完相同，没有任何行被缩放两次。
4. **拒绝执行**：QPU ≠ 500000 且无状态 key 时退出码非 0，不改数据；PostgreSQL 上把 `users.aff_quota` 手工改成 `integer` 后执行，预检（M1.0）拒绝并打印正确的 DDL。
5. **dry-run**：执行前后数据库逐行相同；报告数字与 apply 后实际结果一致。
6. 三库：SQLite、PostgreSQL 必跑；MySQL 跑一次并在交付物附输出。
7. **JSON 原样保留**：fixture 中放入不含目标键的行、含 `9007199254740993` 这类超过 2^53 的整数的行、含中文与 `<>&` 的行、键顺序非字母序的行。断言：不含目标键的行迁移前后**字节相同**且未被 UPDATE；含目标键的行，每个非目标键用 `UnmarshalWithNumber` 解析后与迁移前**语义相同**，大整数逐位不变（键顺序、空白、`<>&` 的 `\u003c` 转义允许变化：`common.Marshal` 即 `json.Marshal`，会按键名排序并做 HTML 转义）。

### 5.4 本地端到端

在本地 PG 上：

1. 用 QPU = 500000 启动服务，造用户、充值、调用（可用 mock 渠道）、产生日志与任务；记录各用户人民币余额、若干日志的花费。
2. 停服务，`--migrate-quota-unit=dry-run`，再 `apply`。
3. 清 Redis，启动服务。
4. 核对：人民币余额不变；旧日志花费不变；新调用的计费过程合计与花费列逐位一致；`/api/status` 中 `quota_per_unit = 7300000`、`quota_per_point = quota_per_compute_point = 10000`。

---

## 6. 交付物

开发完成后提交以下内容（写在 PR 描述或 `docs/quota-unit-cny-micro-migration-dev-report.md`）：

1. **改动清单**：按任务编号列出改动的文件与函数。
2. **任务对照表**：B1–B6、F1–F2、M1 每一项的完成标准 → 对应测试名称 → 结果。
3. **测试输出**：`go test ./...`、`bun run test` 的结果摘要；三库迁移测试的输出。
4. **dry-run 样例输出**：§5.4 中本地库的 dry-run 与 apply 报告全文。
5. **表名核对结果**（M1.5）。
6. **F2 范围核对**：F2.1 每一项改动前后的输出对比（文本即可）；F2.2 中死代码分支的位置清单；CNY 展示下是否还有其他可达的合计。
7. **F1 的 fallback 策略说明**。
8. **疑问**：本文未覆盖或与代码不符的地方。

---

## 7. 验收（方案作者执行）

### 7.1 代码审查

| # | 检查 | 方法 |
|---|---|---|
| 1 | 倍率路径全部乘 scale，价格路径都没乘 | 逐个核对 §4 B2 两张清单；`rg -n "QuotaScale\(\)"` 结果与清单一一对应 |
| 2 | 没有残留的截断 | `rg -n "int\(.*QuotaPerUnit"`、`rg -n "IntPart\(\)"` 在计费路径上逐条确认 |
| 3 | 没有残留的初始化期固定换算率 | `rg -n "QuotaPerUnit / 730\|/ 730\.0"` 无结果；`/api/status` 走 B5 的函数 |
| 4 | 前端无硬编码 | `rg -n "500000\|684\.93" web/classic/src --glob '!**/__tests__/**'` 逐条确认 |
| 5 | 不再存在 `settleUsd` | `rg -n settleUsd web/` 无结果 |
| 6 | 迁移程序 JSON 走 `common.*`，SQL 三库兼容 | 审查代码 |
| 7 | 没有超出本文范围的改动 | `git diff --stat` 与改动清单一致 |

### 7.2 自动化

| # | 检查 | 通过标准 |
|---|---|---|
| 1 | `go test ./...` | 全部通过 |
| 2 | `bun run test`（`web/classic`） | 全部通过 |
| 3 | 截图样例 | QPU = 500000 → 1136；7300000 → 16583 |
| 4 | 迁移程序三库测试 | 全部通过，幂等、断点续跑、拒绝执行、dry-run 用例存在且通过 |

### 7.3 本地演练（本次不执行，保留作参考）

> 2026-10-10 决定不导出生产库、不做演练，由 §8.4 窗口内的检查点替代。以下内容仅在将来需要演练时参考。

目的：用真实生产数据，在本地把"新代码在旧单位下扣费不变 → 迁移 → 新单位下扣费与展示一致 → 回滚可用"完整跑一遍。演练全部通过才能进入 §8。

#### 7.3.1 准备

1. **镜像**：新镜像（打版本号，如 `new-api:quota-cny-1`）与当前生产镜像（按版本号或 digest 拉取，记为 `new-api:prod-current`）都拉到本地。
2. **导出生产库**（运维在能访问 HCSO 的主机上执行，只读，不影响线上）：

   ```bash
   docker run --rm -v "$PWD:/out" postgres:14 \
     pg_dump "<生产 SQL_DSN>" -Fc --no-owner --no-acl -f /out/prod-$(date +%Y%m%d%H%M).dump
   ```

   导出文件包含用户信息、令牌、渠道密钥。只通过加密渠道传输，只放在本地加密磁盘，演练结束后删除，不得提交到仓库。
3. **本地 PostgreSQL 14**（与生产大版本一致）与 Redis，放在一个**不能出网**的 docker 网络里，避免本地实例用真实渠道密钥调用上游、给用户发通知：

   ```bash
   docker network create --internal quota-rehearsal
   docker run -d --name rh-pg --network quota-rehearsal \
     -e POSTGRES_USER=root -e POSTGRES_PASSWORD=123456 -e POSTGRES_DB=new-api postgres:14
   docker run -d --name rh-redis --network quota-rehearsal redis:7-alpine
   docker run --rm --network quota-rehearsal -v "$PWD:/in" postgres:14 \
     pg_restore -h rh-pg -U root -d new-api --no-owner --no-acl /in/prod-XXXX.dump
   ```

4. **假上游**：在同一网络里运行 `scripts/e2e/mock_upstream.py`（固定返回 prompt 1000 / completion 500，需把监听地址从 127.0.0.1 改为 0.0.0.0，或用启动参数覆盖），容器名 `rh-mock`。
5. **访问本地实例**：内部网络无法从宿主机直接访问，再起一个只做端口转发的容器（如 `alpine/socat`），同时接入 `quota-rehearsal` 与默认网络，把宿主机 `127.0.0.1:3300` 转到 `rh-api:3000`。
6. 启动 new-api 时统一使用：

   ```bash
   docker run -d --name rh-api --network quota-rehearsal \
     -e SQL_DSN=postgresql://root:123456@rh-pg:5432/new-api \
     -e REDIS_CONN_STRING=redis://rh-redis:6379 \
     -e BATCH_UPDATE_ENABLED=true -e TZ=Asia/Shanghai \
     -e KYC_ENCRYPT_KEY=<任意64位hex> -e KYC_HASH_KEY=<任意64位hex> \
     <镜像>
   ```

#### 7.3.2 演练前准备数据

用生产管理员账号登录本地实例（密码与生产相同），然后：

1. 新建一个测试渠道，地址指向 `http://rh-mock:18080`，挂上生产中代表性的模型：至少一个按倍率计费的文本模型（含缓存比例）、一个按次计费模型、一个 Claude 语义模型；生产若配置了分层表达式模型，也挂一个。
2. 新建测试用户（分组倍率 ≠ 1，例如 0.5）、充值 ¥10、创建令牌。
3. 记录 3~5 个真实用户的人民币余额（截图或 SQL），作为迁移前基线。

这些操作产生的数据会参与后续校验，所以 §8.3 的"迁移前快照"要在这一步之后再取。

#### 7.3.3 演练步骤与通过标准

| # | 步骤 | 通过标准 |
|---|---|---|
| 1 | 用**旧镜像** `prod-current` 启动，测试令牌对每个代表模型各调用 3 次，记录每条日志的 `quota` | 调用成功 |
| 2 | 停旧镜像，用**新镜像**启动（库里仍是 QPU = 500000），重复第 1 步的调用 | 每条 `quota` 与第 1 步对应调用相同；只有 B4 涉及的路径允许 ±1，且要能逐条说明原因 |
| 3 | 新镜像下打开日志页、令牌页、用户页、个人设置 | 显示与旧镜像一致（计费过程合计按 F2 改为 6 位人民币后，与花费列在旧单位下仍可能差 1 quota，属预期） |
| 4 | 停服务，执行 §8.3 SQL 保存迁移前快照 | — |
| 5 | `docker run --rm --network quota-rehearsal -e SQL_DSN=... <新镜像> --migrate-quota-unit=dry-run` | 报告行数与 §11 量级一致；`options` 新旧值符合 M1.7 |
| 6 | 同上 `--migrate-quota-unit=apply`，记录耗时 | 退出码 0；耗时作为生产停服时长的参考（本地与 HCSO 性能不同，按 2 倍预留） |
| 7 | 再次 apply | 输出"已完成"，数据无变化 |
| 8 | 清本地 Redis，执行 §8.3 SQL 与迁移前比对 | 全部满足 §8.3 通过标准 |
| 9 | 新镜像启动，执行 §8.4 冒烟表中可本地完成的项（1、3、4、5、6、7、9、10） | 全部通过；第 3、4 项新调用的花费列 = 计费过程合计，逐位一致 |
| 10 | 抽查 7.3.2 记录的真实用户 | 人民币余额与基线一致；其历史日志的花费、导出金额与迁移前一致 |
| 11 | **回滚演练**：删库重新 `pg_restore` 导出文件，清 Redis，用旧镜像启动 | 服务正常，余额与 7.3.2 记录的基线一致 |
| 12 | 清理：删除容器、网络、数据卷与导出文件 | — |

本地无法验证的项（支付宝 / 微信真实回调、真实上游调用）留到 §8.4 生产冒烟。

演练记录（每步结果、耗时、截图）写入 `docs/quota-unit-cny-micro-migration-rehearsal.md`。

---

## 8. 生产升级 runbook（运维执行）

### 8.1 现网部署信息

| 项 | 值 | 来源 |
|---|---|---|
| 部署方式 | Docker Compose，容器 `new-api`，镜像 `…/reputationly/new-api:latest` | `docker-compose.yml` |
| 数据库 | HCSO PostgreSQL 14.22 主备 | 运维确认 |
| 日志库 | 待确认是否配置 `LOG_SQL_DSN` | §8.2 命令 |
| Redis | compose 内 `redis` 容器（待确认是否共用） | §8.2 命令 |
| 批量更新 | compose 模板为 `BATCH_UPDATE_ENABLED=true`，间隔默认 5 秒 | 待 §8.2 确认 |
| 实例数 | 待确认 | §8.2 命令 |
| 在线升级 | `watchtower` 容器，API 模式（后台「检查更新 → 立即安装」触发），不自动轮询 | `docker-compose.yml` |
| 外部系统读库 | 无 | 运维确认 |

### 8.2 获取部署参数

在每台部署了 new-api 的主机上执行：

```bash
# 1. 本机运行的容器
docker ps --format 'table {{.Names}}\t{{.Image}}\t{{.Status}}'

# 2. new-api 关键环境变量（密码已打码）
docker inspect new-api --format '{{range .Config.Env}}{{println .}}{{end}}' \
  | grep -E '^(SQL_DSN|LOG_SQL_DSN|REDIS_CONN_STRING|BATCH_UPDATE_ENABLED|BATCH_UPDATE_INTERVAL|NODE_TYPE|NODE_NAME|MEMORY_CACHE_ENABLED|SYNC_FREQUENCY)=' \
  | sed -E 's#(://[^:/@]*:)[^@]*@#\1***@#; s#(password=)[^ &]*#\1***#'

# 3. Redis 是否只有 new-api 在用：看同网络有哪些容器、库里有哪些 key 前缀
docker network inspect $(docker inspect new-api --format '{{range $k,$v := .NetworkSettings.Networks}}{{$k}} {{end}}') \
  --format '{{range .Containers}}{{.Name}} {{end}}'
docker exec redis sh -c 'redis-cli -a "$REDIS_PASSWORD" --no-auth-warning INFO keyspace' 2>/dev/null \
  || docker exec redis redis-cli INFO keyspace
docker exec redis sh -c 'redis-cli -a "$REDIS_PASSWORD" --no-auth-warning --scan --count 1000 2>/dev/null || redis-cli --scan --count 1000' \
  | sed -E 's/[:].*//' | sort | uniq -c | sort -rn | head -20
```

Redis 密码如果不在 `REDIS_PASSWORD` 环境变量里，从第 2 步的 `REDIS_CONN_STRING` 中取（`redis://:密码@redis:6379`），手动替换 `-a` 参数。

判断：

- 第 2 步没有 `LOG_SQL_DSN` → 日志与主库同库。
- 第 2 步没有 `NODE_TYPE` 且只有一台主机运行 `new-api` → 单实例。
- 第 3 步网络里只有 `new-api`、`redis`、`watchtower`，且 key 前缀都是 `user`、`token`、`new-api` 等本系统前缀 → Redis 独占，切换时可以 `FLUSHDB`。否则按前缀精确删除（见 §8.4 第 17 步）。

### 8.3 迁移校验 SQL

迁移前执行一次并保存结果，迁移后再执行一次对比。

```sql
-- A. 资金类合计
SELECT 'users' AS t, count(*) AS n,
       SUM(quota)::numeric AS a, SUM(used_quota)::numeric AS b,
       SUM(points_balance)::numeric AS c, SUM(credit_limit)::numeric AS d
FROM users
UNION ALL SELECT 'tokens', count(*), SUM(remain_quota), SUM(used_quota), 0, 0 FROM tokens
UNION ALL SELECT 'logs', count(*), SUM(quota), SUM(points_consumed), SUM(credit_consumed), 0 FROM logs
UNION ALL SELECT 'top_ups', count(*), SUM(amount), SUM(money), 0, 0 FROM top_ups
UNION ALL SELECT 'checkins', count(*), SUM(quota_awarded), 0, 0, 0 FROM checkins;

-- B. 逐用户快照（迁移前导出，迁移后 join 比对）
SELECT id, quota, used_quota, points_balance, credit_limit, credit_used FROM users ORDER BY id;

-- C. options
SELECT "key", value FROM options
WHERE "key" IN ('QuotaPerUnit','Price','USDExchangeRate','QuotaRemindThreshold','PreConsumedQuota',
                'QuotaForNewUser','points_setting.quota_per_point',
                'compute_point_setting.quota_per_compute_point',
                'checkin_setting.min_quota','checkin_setting.max_quota','QuotaUnitMigration')
ORDER BY 1;
```

通过标准：

- 逐用户：每个数值列 `新 = round(旧 × 14.6)`。
- 每个合计：`|新合计 − round 后旧值之和| = 0`；人民币口径 `新合计 ÷ 7300000 × 7.3` 与 `旧合计 ÷ 500000 × 7.3` 之差 ≤ `行数 × ¥0.0000005`。
- `top_ups.money` 合计不变。
- `options`：`QuotaPerUnit = 7300000`；两个换算率 = 10000；`QuotaRemindThreshold = 14600`；`PreConsumedQuota` 与迁移前相同（库里没有则仍不存在）；`QuotaUnitMigration.state = done`。
- 抽查 20 条 `logs.other`、20 条 `tasks.private_data`：quota 键 = round(旧 × 14.6)，展示点数键不变。

### 8.4 切换步骤

窗口前：

1. §7.1、§7.2 验收已通过（2026-10-10）。
2. 新镜像基于**当前生产版本的代码 + 本次改动**构建，打固定版本号（如 `new-api:quota-cny-1`），不要夹带其他未上线功能。
3. 记录当前生产镜像：`docker inspect new-api --format '{{.Image}} {{.Config.Image}}'`，给它打固定标签推到镜像仓库（如 `new-api:prod-before-quota-cny`），作为回滚镜像。
4. 在服务器上提前 `docker pull` 两个镜像。**不要**提前用新镜像连生产库（包括 dry-run）：新镜像启动时会执行 AutoMigrate，旧版本在线时不能改表结构。
5. 准备一个内部测试账号与令牌，选好两个代表模型：一个按倍率计费的文本模型，一个按次计费模型。
6. 公告维护窗口 **1 小时**（迁移预计几分钟，其余留给检查与冒烟）。

窗口内：

7. 维护窗口内**不要**在后台点「检查更新 / 立即安装」，避免 watchtower 中途替换容器。
8. 反向代理切到维护页（或关闭对外端口），等待在途请求结束（流式长连接最长 `STREAMING_TIMEOUT`，默认 120 秒）。之后的内部调用一律在服务器上走 `http://127.0.0.1:3000`。
9. 等待至少 2 个 `BATCH_UPDATE_INTERVAL`（默认 5 秒，等 10 秒以上）让批量更新落库，`docker compose stop new-api`。**多实例时全部停止。**
10. 把 `docker-compose.yml` 中 `new-api` 的 `image` 改为新镜像版本号，`docker compose up -d new-api`。此时库里仍是 QPU = 500000，新代码 scale = 1，行为应与旧版本相同。
11. **旧单位检查点**（代替演练中的"扣费不变"验证）：
    - `curl -s http://127.0.0.1:3000/api/status`：`quota_per_unit = 500000`，`quota_per_point`、`quota_per_compute_point` 约为 684.93；
    - 用内部令牌对两个代表模型各调用 1 次，查这两条日志：按倍率模型的 `quota` 满足 §8.5 复算 SQL（把 `* 14.6` 换成 `* 1`）；按次模型的 `quota = round(单价 × 500000 × 分组倍率)`。
    - **不通过**：`image` 改回回滚镜像，`docker compose up -d new-api`，撤维护页。数据未迁移，服务恢复原状。
12. 等待 10 秒以上，`docker compose stop new-api`。
13. 在 HCSO 控制台创建手动备份，记录备份名与时间点。备份完成后再继续。
14. 执行 §8.3 SQL，保存结果（迁移前快照）。
15. `docker compose run --rm --no-deps new-api --migrate-quota-unit=dry-run`，保存报告，逐项核对下表。**任一项不通过**：不执行 apply，`image` 改回回滚镜像并启动，撤维护页，带报告排查。此时数据未改动。

    | 报告位置 | 通过标准（生产快照 2026-10-10，见 §11） |
    |---|---|
    | `[M1.0 列类型预检]` | 31 列全部是 64 位整数，未拒绝执行 |
    | 头部「日志库」 | 与 §8.2 查到的 `LOG_SQL_DSN` 情况一致 |
    | `[数值列]` 行数 | `users` ≈ 229、`top_ups.amount`（限定渠道）≈ 55、`tasks` ≈ 2,728、`logs` ≈ 85 万；只多不少（升级前新增的数据） |
    | `[数值列]` CNY 两列 | 每一行「迁移前 CNY」与「迁移后 CNY」相差不超过 `行数 × ¥0.0000005` |
    | `[JSON 键]` `logs.other` 已改写行 | ≈ 22,858（只多不少）。明显偏少说明有行被漏掉，停 |
    | `[JSON 键]` `users.setting` 已改写行 | ≈ 5 |
    | `[JSON 键]` 无法解析 | 期望为 0。不为 0 时打开 id 清单文件抽查几行：若内容里**不含** M1.6 的键，可继续；含则停 |
    | `[options]` | `QuotaPerUnit` 缺失 → 7300000；`QuotaRemindThreshold` 缺失 → 14600；`points_setting.quota_per_point`、`compute_point_setting.quota_per_compute_point` 缺失 → 10000；`QuotaForNewUser` 0 → 0；`checkin_setting.min_quota` 0.1 → 1、`max_quota` 2 → 29；报告注明 `PreConsumedQuota` 不在清单里 |
    | `[logs] 分批计划` | 批数 ≈ `max(logs.id) ÷ 5000` |

16. `docker compose run --rm --no-deps new-api --migrate-quota-unit=apply`，保存报告。报告中各项应与 dry-run 一致，`logs` 的迁移前合计与第 14 步快照一致。若中途失败：**不要**手工改数据，直接重跑 apply（会从游标续跑）；重跑仍失败则执行 §8.6 回滚。
17. 清 Redis：Redis 独占时 `docker exec redis redis-cli -a <密码> FLUSHDB`；否则删除 `user:*`、`token:*`、`new-api:subscription_plan*` 前缀的 key。
18. 再执行 §8.3 SQL，与第 14 步快照按通过标准比对。**不通过则停止，执行 §8.6 回滚。**
19. `docker compose up -d new-api`，保持维护页，内部账号执行冒烟（下表）。**不通过则停止，执行 §8.6 回滚。**
20. 撤维护页，开放流量。进入 24 小时观察（§8.5）。

冒烟项（内部账号）：

| # | 操作 | 通过标准 |
|---|---|---|
| 1 | 查看余额 | 与迁移前人民币余额一致（6 位小数内） |
| 2 | 支付宝或微信充值 ¥1 | 余额增加 ¥1.000000，`top_ups.amount = 1000000` |
| 3 | 调用按 token 计费模型（含缓存命中、分组倍率 ≠ 1） | 花费列 = 计费过程合计（6 位逐位一致）；`logs.quota = round(公式金额 × 10⁶)` |
| 4 | 调用按次计费模型 | 花费 = 单价 × 分组倍率，逐位一致 |
| 5 | 视频 / 分层表达式 / 音频 / 异步任务各一次（有对应渠道时） | 花费与计费过程一致 |
| 6 | 订阅抵扣、积分抵扣各一次 | 余量扣减正确，日志归因金额正确 |
| 7 | 导出日志 | 「费用(CNY)」与列表逐行一致；合计与左上角消耗额度一致 |
| 8 | 兑换码兑换 | 到账人民币金额与迁移前面值一致 |
| 9 | 令牌额度用尽 / 余额不足 | 拦截行为与迁移前一致 |
| 10 | `/api/status` | `quota_per_unit = 7300000`、`quota_per_point = quota_per_compute_point = 10000` |

### 8.5 复算对账 SQL（切换后观察用）

```sql
WITH c AS (
  SELECT id, quota, prompt_tokens, completion_tokens,
         COALESCE((NULLIF(other,'')::json->>'model_ratio')::numeric, 0)       AS mr,
         COALESCE((NULLIF(other,'')::json->>'completion_ratio')::numeric, 0)  AS cr,
         COALESCE((NULLIF(other,'')::json->>'cache_ratio')::numeric, 1)       AS kr,
         COALESCE((NULLIF(other,'')::json->>'cache_tokens')::numeric, 0)      AS kt,
         COALESCE((NULLIF(other,'')::json->>'group_ratio')::numeric, 1)       AS gr,
         COALESCE((NULLIF(other,'')::json->>'user_group_ratio')::numeric, -1) AS ugr,
         COALESCE((NULLIF(other,'')::json->>'model_price')::numeric, -1)      AS mp
  FROM logs
  WHERE type = 2 AND created_at >= :switch_ts
)
SELECT count(*) AS total,
       SUM(CASE WHEN quota = round((prompt_tokens - kt + kt*kr + completion_tokens*cr)
                 * mr * (CASE WHEN ugr <> -1 THEN ugr ELSE gr END) * 14.6) THEN 1 ELSE 0 END) AS matched
FROM c WHERE mp = -1;
```

仅覆盖普通文本模型；Claude 语义、音频、工具附加费、`other_ratios` 的行单独核对。切换后 24 小时内每小时执行一次，`matched = total`。同时观察错误率、余额不足拦截率与切换前同期持平。

### 8.6 回滚

迁移有舍入，`÷ 14.6` 不能精确还原，**apply 之后的回滚 = 恢复第 13 步的 HCSO 备份 + 换回旧镜像，两者必须同时做**。apply 之前（第 11、15 步检查点）不通过只需换回旧镜像，见各步说明。

1. 保持停服：`docker compose stop new-api`。
2. 在 HCSO 控制台用第 13 步备份恢复数据库（恢复到原实例；若恢复到新实例，同步修改 `SQL_DSN`）。
3. `docker-compose.yml` 中 `image` 改回第 3 步记录的回滚镜像。
4. 清 Redis（同第 17 步）。
5. `docker compose up -d new-api`，确认余额、日志与升级前一致后撤维护页。

第 16、18、19 步不通过时按上面回滚，无数据损失。开放流量后才发现问题时，恢复备份会丢失开放后的交易，需按 `logs` 增量人工补账，所以第 18、19 步不通过绝不开放流量。

**禁止只换回旧镜像而不恢复数据库**：旧代码不认 scale，在迁移后的库上按倍率计费的模型价格会变成原来的 1/14.6。

---

## 9. 收尾（开发 agent）

- 更新 [currency-fx-architecture.md](./currency-fx-architecture.md)：补充 `QPU = 汇率 × 10⁶` 约束。
- 更新 `common/points.go`、`common/compute_points.go`、`setting/operation_setting/checkin_setting.go` 中写死 500000、"约 0.002 USD"、684.93 的注释。
- 后台 `QuotaPerUnit` 保持锁定；若后续开放汇率修改，必须按 §10.1 联动。

---

## 10. 注意事项与附录

### 10.1 汇率必须修改时

例：7.3 → 7。

1. `QuotaPerUnit` 同步改为 7000000，`Price`、`USDExchangeRate` 同步改为 7。
2. 余额不需要迁移：人民币余额 = quota × 汇率 / QPU = quota × 10⁻⁶，与汇率无关。
3. 按倍率定价的人民币价格会变（人民币单价 = ratio × 2 × 汇率）。要保持人民币价格不变，所有 `ModelRatio` × (7.3 / 7)，`ModelPrice`、视频单价等美元价同理。
4. 积分 / 算力点换算率仍为 `QPU / (汇率 × 100) = 10000`。

对比现状：QPU 固定 500000 时只改汇率，所有用户人民币余额会立即变化约 4%。

### 10.2 为什么不只改前端

| 方案 | 结果 | 结论 |
|---|---|---|
| 计费过程合计直接显示实扣值 | 等号两边算不平 | 否决 |
| 公式后追加"取整后实扣" | 两个数并存，用户仍会问 | 否决 |
| 展示精度降到 4 位 | 约 96% 行一致，不能保证 | 否决 |
| 不展示公式合计（上游 default 主题做法） | 失去计算过程 | 否决 |
| **1 quota = ¥0.000001** | 新日志逐位一致 | 采用 |

### 10.3 注意事项

1. **不要对 scale 双重计算。** 价格路径已乘 QPU，再乘 scale 会让价格翻 14.6 倍。这是代码评审第一重点。
2. **`Price` 与 `USDExchangeRate` 必须相等。**
3. **历史日志的取整差不会消失。** 迁移只是把旧 quota × 14.6，旧日志的人民币花费不变，仍与当时公式差最多 ¥0.0000073。只有切换后的新日志逐位一致。对外说明时注明切换日期。
4. **倍率配置、模型定价编辑器、前台定价页不需要改。** 倍率 ↔ 美元关系不变。
5. **"多少个积分 / 算力点"不缩放，换算率才变。**
6. **停服是硬要求。** 进程内批量更新队列与 Redis 缓存中的旧单位额度会在迁移后写回或被读取。
7. **上游合并风险。** `text_quota.go`、`quota.go`、`price.go`、`task_billing.go` 是上游高频改动文件；合并时检查新增的倍率路径是否乘了 scale。QPU = 7300000 的回归用例作为合并护栏，不得删除。
8. **签到。** 当前关闭（`enabled = false`、`reward_type = points`）。`min_quota = 0.1` 加载时会被截断为 0（`setting/config/config.go` 对 int 字段的浮点截断），属于无效配置，积分模式下不生效；迁移按 × 14.6 处理。将来若改用额度奖励，由运营按人民币金额重新配置（¥0.1 = 100000）。
9. **积分精确性。** 积分模式下 `checkins.quota_awarded`、`users.points_balance` 存的是 `PointsToQuota(n)`（向上取整）。× 14.6 后按新换算率 10000 用 `QuotaToPoints`（向下取整）展示，仍精确还原为 n：`ceil(n × 684.93) × 14.6` 落在 `[n × 10000, n × 10000 + 14.6)` 内。
10. **名字带 quota 不等于单位是 quota。** `PreConsumedQuota` 实际是预扣 token 数下限，不迁移（M1.7）；后台标签「请求预扣费额度」有误导，后续可单独改为「预扣最少 token 数」。迁移程序新增任何 key 前，先查它在计费代码里是当 quota 还是当 token / 个数用。

---

## 11. 生产数据快照（2026-10-10 查询）

| 项 | 结果 | 结论 |
|---|---|---|
| 数据库 | HCSO PostgreSQL 14.22 主备 | 迁移语法均支持；备份用 HCSO 手动备份 |
| 列类型 | `information_schema.columns` 两次查询合计覆盖 M1.5 全部列，均为 bigint，其中 `users.aff_quota`、`users.aff_history` 已逐列确认为 bigint | 无需 DDL；M1.0 预检在执行时再确认一次 |
| `aff_history` | 最大值 0 | 无溢出风险 |
| `reconcile_hourly.quota` | 存在（bigint） | v2.1 对账的遗留表，代码已删除（commit `a77bc26fb`），当前无读写，不迁移 |
| `top_ups` | 55 行，仅 `alipay_direct` / `wxpay_direct`，状态 `success` / `expired`，无 `pending`；`amount / money ≈ 68493 = 500000 / 7.3` | `amount` 是 quota，全部 × 14.6；无在途订单 |
| `logs` | 850,552 行，696 MB；`other` 含待改键 22,858 行 | 全程停服迁移可行 |
| `tasks` | 2,728 行，仅 SUCCESS / FAILURE | 无在途任务 |
| `subscription_pre_consume_records` | 0 行 | 无在途预扣 |
| `users` | 229 行；`setting` 含 `quota_warning_threshold` 5 行 | |
| `checkins` | 86 行，均为 points，最后一次 2026-09-21 | `quota_awarded` × 14.6 |
| `options` 已有 | `USDExchangeRate = 7.3`、`quota_display_type = CNY`、`QuotaForNewUser = 0`、`checkin_setting.*`（见 10.3 第 8 条）、`points_setting.enabled = true` | |
| `options` 缺失（走代码默认） | `QuotaPerUnit`(500000)、`Price`(7.3)、`QuotaRemindThreshold`(1000)、`PreConsumedQuota`(500，token 数，不迁移)、`points_setting.quota_per_point`、`compute_point_setting.quota_per_compute_point`（684.93） | 除 `PreConsumedQuota` 外由 M1.7 显式写入 |
| 外部系统读库 | 无 | |
