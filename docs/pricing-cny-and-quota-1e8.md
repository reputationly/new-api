# 定价全面人民币化 + 1 quota = ¥0.00000001 设计文档

> 状态：设计稿，待评审
> 前置：[quota-unit-cny-micro-migration.md](./quota-unit-cny-micro-migration.md)（下称"V1 文档"，1 quota = ¥0.000001，已于 2026-10-10 上线并完成生产迁移）
> 依据：main `4e50e02c7` 的代码。行号会漂移，以函数名为准。
> 范围：本文只做设计，不含实现。实现按 §4 任务清单另行开发。

---

## 0. 必读与约束

- 开发前先读：`AGENTS.md`、`pkg/billingexpr/expr.md`（规则 7）、V1 文档 §2/§4/§10。
- JSON 一律用 `common.Marshal` / `common.Unmarshal`；SQL 必须同时兼容 SQLite、MySQL、PostgreSQL。
- 新文件不加版权头；新增文案只用中文 key。
- **文中把单位写在名字里**：`¥` 表示人民币金额，`$` 表示美元金额，`quota` 表示内部整数单位。评审时只要看到某个变量的单位与名字不符，就是 bug。

### 0.1 已确认的决策（2026-10-11）

| # | 问题 | 决定 |
|---|---|---|
| D1 | 定价口径 | 方案 B：所有模型定价配置都按人民币存储 |
| D2 | quota 粒度 | 1 quota = ¥0.00000001（10⁻⁸ 元），支持单价 < ¥1/百万 tokens 的模型 |
| D3 | 分段表达式 | 生产目前没有在用。代码支持 v2（人民币系数）；迁移时不改写 v1，只报告 |
| D4 | `/api/ratio_config` | 没有下游在用，**删除**接口和"暴露倍率"开关（B7） |
| D7 | 上游倍率同步 | 管理员都是手动填价，**删除**整个功能（B6），不做美元 → 人民币换算 |
| D5 | 金额展示精度 | 人民币金额统一显示 8 位小数（与 1 quota 对齐） |
| D6 | 倍率换算的舍入 | 迁移时 `ModelRatio × 14.6`，结果四舍五入到 6 位小数（见 §5.4） |

---

## 1. 背景与目标

### 1.1 问题一：单价 < ¥1/百万 tokens 计不准

`service/text_quota.go` `calculateTextQuotaSummary` 按请求逐笔四舍五入（`Round(0)`）。倍率非 0、有 token 但结果为 0 时，强制扣 1 quota。

当前 1 quota = ¥0.000001。若单价是 ¥0.01/百万，1 token 只值 0.01 quota：
- 1–149 tokens 的请求都扣 1 quota，单个 token 实际按 ¥1/百万计价，是标价的 100 倍；
- 零头不会跨请求累计。

### 1.2 问题二：定价里混着美元和"14.6"

quota 已经是人民币口径，但定价配置大多还是美元口径：

| 配置 | 现在的单位 |
|---|---|
| 倍率 `ModelRatio` | 1 = $2/百万 tokens |
| 按次价 `ModelPrice` | $/次 |
| 视频矩阵 | $ |
| 表达式系数 | $/百万 tokens |
| 工具价 | $/千次 |
| 违规罚金 | $ |

所以代码里有：
- 后端：`QuotaScale()`（= QPU / 500000 = 14.6）、`RatioQuotaBase`；
- 前端：约 45 处"倍率 × 2 × 汇率"，编辑器保存时"人民币价 ÷ 汇率 ÷ 2"。

库里存的值人看不懂。V1 文档 §10 里"倍率路径必须乘 scale、价格路径不能乘"这条约定也最容易写错：PreConsumedQuota 就差点被重复放大。

### 1.3 目标

- **G1**：1 quota = ¥0.00000001。¥0.01/百万 时 1 token = 1 quota；最低扣费从 ¥0.000001 降到 ¥0.00000001。
- **G2**：每一项定价配置存的数值就是人民币价格：
  - 倍率：¥/百万 tokens
  - 按次价：¥/次
  - 视频：¥/百万 tokens、¥/次、¥/秒
  - 表达式 v2 系数：¥/百万 tokens
  - 工具价：¥/千次
  - 违规罚金：¥/次

  计费代码里不再出现 2、14.6、`QuotaScale()`，也不再依赖汇率。
- **G3**：迁移前后保持不变的有三样：用户余额的人民币值、每个模型的人民币单价、历史日志的人民币花费。
- **G4**：汇率只影响三处：美元支付（Stripe / Waffo）、`USD` 展示模式、换算上游的美元报价。改汇率不再影响模型价格和余额。

### 1.4 不在范围

- **支付侧语义**：`Price`、`USDExchangeRate`、`StripeUnitPrice`、Waffo 单价、充值套餐、对公转账。代码只随 QPU 数值变化，公式不改（§3.4）。
- **无量纲倍数**：`CompletionRatio`、`CacheRatio`、`CreateCacheRatio`、`ImageRatio`、`AudioRatio`、`AudioCompletionRatio`、各类分组倍率、渠道成本比 `cost_ratio`。它们本来就是倍数，不改。
- **展示模式**：`quota_display_type` 的 `USD` / `CUSTOM` / `TOKENS` 继续保留，只是换算基准从美元改成人民币（§6.1）。
- **历史日志的定价字段**：`logs.other` 里冻结的 `model_ratio`、`model_price` 等不改写，靠 `price_unit` 标记区分新旧（§2.6）。

---

## 2. 单位与口径

### 2.1 常量

```go
// common/constants.go
const QuotaPerYuan = 100_000_000          // 1 元 = 10⁸ quota；计费换算只用它
var   QuotaPerUnit = 730_000_000.0        // 1 美元对应的 quota = 汇率 × QuotaPerYuan，运行时配置
```

- 计费路径一律用 `¥ × QuotaPerYuan`，**不读汇率，不读 QPU**。
- `QuotaPerUnit` 只留给"美元金额 ↔ quota"的地方用（§3.4 支付、§3.5 美元展示）。它和汇率之间的约束是 `QPU = 汇率 × 10⁸`；即使这个约束被破坏，也只会影响美元支付和美元展示，不会影响计费。
- `QuotaScale()`、`RatioQuotaBase` 删除。

### 2.2 各定价项的新含义

| 配置（options key） | 旧含义 | 新含义 | quota 计算公式 | 迁移换算 |
|---|---|---|---|---|
| `ModelRatio` | 1 = $2/百万 tokens | ¥/百万 tokens（输入价） | `tokens × ratio × 100`（100 = 10⁸ / 10⁶） | × 14.6，保留 6 位小数 |
| `ModelPrice` | $/次 | ¥/次 | `price × 10⁸` | × 7.3，保留 6 位小数 |
| `VideoPricingConfig`（所有格子） | $/百万、$/次、$/秒 | ¥/百万、¥/次、¥/秒 | 同上 | × 7.3，保留 6 位小数 |
| `billing_setting.billing_expr` | v1 系数 $/百万 | v2 系数 ¥/百万（v1 仍兼容） | v2：`out / 10⁶ × 10⁸` | 不改写，只报告（D3） |
| `tool_price_setting.prices` | $/千次 | ¥/千次 | `price / 1000 × 次数 × 10⁸` | × 7.3，保留 6 位小数 |
| `grok.violation_deduction_amount` | $/次 | ¥/次 | `amount × 10⁸` | × 7.3，保留 6 位小数 |

上表各公式最后都还要乘分组倍率（`groupRatio`），表中省略。

**代码内的美元常量**也要换成人民币（§3.2），包括：
- GPT-Image-1 按次价；
- Gemini 音频输入 $/百万 tokens；
- 默认工具价 `defaultToolPrices` / `defaultToolPriceOverrides`；
- 默认倍率表和默认按次价表 `defaultModelRatio` / `defaultModelPrice`。

**无量纲倍数不变**：`CompletionRatio = 4` 还是"输出价 = 输入价 × 4"，只是输入价的单位从美元换成了人民币。

### 2.3 计费例子（实现与测试的回归样例）

| # | 场景 | 新配置 | 新 quota | 人民币 |
|---|---|---|---|---|
| E1 | deepseek：输入 ¥2/百万、输出 ×4、分组 0.5，prompt 199 / completion 4096 | ratio = 2，completion_ratio = 4 | (199 + 4096×4) × 2 × 100 × 0.5 = 1,658,300 | ¥0.016583 |
| E2 | ¥0.01/百万，1 token | ratio = 0.01 | 1 × 0.01 × 100 = 1 | ¥0.00000001 |
| E3 | ¥0.01/百万，149 tokens | ratio = 0.01 | 1.49 → 四舍五入为 1 | ¥0.00000001 |
| E4 | ¥0.003/百万，1 token | ratio = 0.003 | 0.3 → 0 → 最低扣费 1 | ¥0.00000001 |
| E5 | 按次生图 ¥0.2/次，分组 1 | ModelPrice = 0.2 | 0.2 × 10⁸ = 20,000,000 | ¥0.2 |
| E6 | 联网搜索 ¥73/千次（原 $10），调用 3 次，分组 1 | prices.web_search = 73 | 73 / 1000 × 3 × 10⁸ = 21,900,000 | ¥0.219 |
| E7 | 视频按秒 ¥0.73/秒，5 秒，分组 0.8 | 矩阵格子 = 0.73 | 0.73 × 5 × 10⁸ × 0.8 = 292,000,000 | ¥2.92 |

E1 与 V1 文档 §2.3 的同一样例做对照：迁移前是 16,583 quota（¥0.016583），迁移后是 1,658,300 quota，人民币金额相同。

### 2.4 取整与最低扣费

- 逐笔四舍五入、以及"倍率非 0 且有 token、算出 0 时扣 1 quota"的规则**保持不变**。最低扣费从 ¥0.000001 降到 ¥0.00000001。
- 不做跨请求的零头累计。单价低于 ¥0.005/百万 tokens（即 1 token 不足 0.5 quota）时，单 token 请求仍会被抬到 1 quota。这是有意的取舍，记入 §10。
- 所有价格路径都**先乘后除**，最后只取整一次；`int(...)` 截断统一改为 `math.Round` / `decimal.Round(0)`。这沿用 V1 文档 B4 的规则。

### 2.5 展示精度（D5）

人民币金额统一显示 8 位小数，包括：
- 日志花费列；
- 计费过程合计；
- 后端 `logger.LogQuota` 文案；
- `controller/log.go` 导出的 `cost_cny`。

余额、充值这类"大额"展示仍是 2 位小数。单价展示（¥/百万 tokens）采用"最多 6 位、去掉末尾的 0"。

### 2.6 新旧数据的区分标记

新代码写出的每条消费日志 `other` 和每个任务的 `private_data.billing_context` 都带上：

```json
"price_unit": "cny"
```

- **有标记**：`model_ratio` 是 ¥/百万；`model_price`、`*_price`、`video_unit_price` 都是人民币。
- **无标记**（历史数据）：旧的美元口径，即 `model_ratio × 2` = $/百万，其余 `*_price` 是美元。

前端在一个地方统一把旧数据换算成人民币（§6.2），后面的渲染代码只处理人民币一种口径。

日志里的 **quota 数值**（`logs.quota`，以及 `other` 里以 quota 为单位的 12 个键）由迁移统一 ×100，不依赖这个标记。

---
## 3. 现状盘点（改造面全量清单）

逐项分类说明：
- **计费**：内部 quota 的计算，按 §2.2 改。
- **支付**：美元或支付金额与 quota 之间的换算，公式不改。
- **展示**：quota 换算成金额后给人看，按 §2.5 改。

### 3.1 计费路径（改为 `¥ × QuotaPerYuan`）

| 位置 | 现状 | 改为 |
|---|---|---|
| `service/text_quota.go` `calculateTextQuotaSummary`（倍率路径，约 :226-231） | `× QuotaScale()` | `× QuotaPerYuan / 10⁶`（等于 ×100） |
| 同上 `calculateTextToolCallSurcharge`（:84-138），共 5 处 | `$/千次 × 次数 / 1000 × QPU`；GPT-Image-1 `$/次 × QPU` | `¥ × QuotaPerYuan` |
| 同上 Gemini 音频（:271-276） | `$/百万 / 10⁶ × tokens × QPU` | `¥/百万 / 10⁶ × tokens × QuotaPerYuan` |
| 同上 按次价（:296） | `ModelPrice × QPU` | `ModelPrice × QuotaPerYuan` |
| 同上 extraContent 文案（:357-369） | 拼的是美元花费 | 改为人民币，保留 8 位小数 |
| `service/quota.go` `calculateAudioQuota`（:53-67） | 倍率 `× QuotaScale()`，价格 `× QPU` | 同上两条 |
| `service/quota.go` `CalcOpenRouterCacheCreateTokens`（:265-267） | `ModelRatio / RatioQuotaBase` 得到"$/token"，用来和 OpenRouter 回报的美元成本反推 tokens | `ModelRatio / 10⁶` 得到"¥/token"，OpenRouter 的美元成本先 × 汇率。这是**合法保留汇率**的地方之一（上游报的是美元） |
| `service/quota.go:43` | 读默认倍率表兜底 | 默认表已在加载时换成人民币（§3.2），这里不用再改 |
| `relay/helper/price.go` `ModelPriceHelper`（:126-134） | 预扣：倍率 `× QuotaScale()`，按次 `× QPU` | 倍率 `× 100`，按次 `× QuotaPerYuan`。`PreConsumedQuota` 是 token 下限，不是 quota，**不动** |
| 同上 `ModelPriceHelperPerCall`（:189-221） | 按次 `× QPU`；按量预扣 `modelRatio / 2 × QPU`（= 25 万 tokens 的输入价） | 按次 `× QuotaPerYuan`；按量预扣 `250000 × ratio × 100`，即 `ratio × 2.5×10⁷`，语义还是"预扣 25 万 tokens 的输入价" |
| 同上 `modelPriceHelperTiered`（:288、:311） | `rawCost / 10⁶ × QPU`，快照记录 `QuotaPerUnit` | 改为调用 `billingexpr` 按版本换算（§4 B4），预扣与结算共用同一个函数 |
| `pkg/billingexpr/settle.go` `quotaConversion` | 只有 v1：`out / 10⁶ × snap.QuotaPerUnit` | 新增 v2：`out / 10⁶ × QuotaPerYuan`；v1 保持原样（快照里冻结了 QPU，仍然正确） |
| `relay/video_billing.go` `applyVideoPricing`（:190、:214） | `$ × QPU × 分组` | `¥ × QuotaPerYuan × 分组` |
| `service/task_billing.go` `RecalculateTaskQuotaByTokens`（:654） | `tokens × ratio × … × QuotaScale()` | `× 100` |
| `service/task_billing.go` `RecalculateTaskQuotaByVideoMatrix`（:754） | `tokens / 10⁶ × UnitPrice($) × QPU` | `UnitPrice(¥) × QuotaPerYuan`。**历史任务**里冻结的 `UnitPrice` 由迁移 ×7.3（§5.3） |
| `service/tool_billing.go`（:47-73） | `$/千次 × QPU`；GPT-Image-1 `$ × QPU` | `¥ × QuotaPerYuan` |
| `service/violation_fee.go` `calcViolationFeeQuota`（:92） | `$ × QPU × 分组` | `¥ × QuotaPerYuan × 分组` |
| `service/entitlement_cost.go`（:177） | `unitPrice × QPU × cost_ratio` | `unitPrice(¥) × QuotaPerYuan × cost_ratio`。开发时要核实 `modelUnitPrice` 的来源口径，确认它返回的是 ¥/次或 ¥/百万，而不是"倍率 × 2" |
| `controller/channel-test.go`（:539、:546） | 倍率 `× QuotaScale()`，按次 `× QPU` | `× 100`、`× QuotaPerYuan` |

### 3.2 代码内的美元常量与默认表

| 位置 | 现状 | 处理 |
|---|---|---|
| `setting/operation_setting/tools.go` `defaultToolPrices` / `defaultToolPriceOverrides`（:22-34） | $/千次（10、2.5、14、25） | 用 `× usdToCNY` 表达式写成人民币，例如 `10.0 * usdToCNY`，注释保留美元原价 |
| 同上 `GPTImage1*`（:146-156） | $/次 | 同上 |
| 同上 Gemini 音频 `*InputAudioPrice`（:196-215） | $/百万 tokens | 同上 |
| `setting/model_setting/grok.go:13` `ViolationDeductionAmount: 0.05` | $ | 改为 `0.365`（= 0.05 × 7.3） |
| `setting/ratio_setting/model_ratio.go` `defaultModelRatio`（:26）、`defaultModelPrice`（:289） | 美元口径（`USD=500`、`RMB=USD/7.3` 等常量） | **原表不动**（方便以后合并上游）。`InitRatioSettings`（:353）和 `GetDefaultModelRatioMap` / `GetDefaultModelPriceMap`（:437-442）返回换算后的副本：倍率 × 2 × `USD2RMB`，价格 × `USD2RMB`，保留 6 位小数。`:430` 导出默认 JSON 时同样走换算 |

`usdToCNY` 是常量，定义在 `operation_setting` 里，值为 7.3，与 `ratio_setting.USD2RMB` 一致。它只用于把代码里静态的美元报价换成人民币，**不跟随运行时汇率**：这些默认价是写死的数据，不应该随运营改汇率而漂移。

### 3.3 定价配置项（options 表；迁移范围见 §5）

`ModelRatio`、`ModelPrice`、`VideoPricingConfig`、`tool_price_setting.prices`、`grok.violation_deduction_amount`、`billing_setting.billing_expr`。

无量纲、不改的配置：`CompletionRatio`、`CacheRatio`、`CreateCacheRatio`、`ImageRatio`、`AudioRatio`、`AudioCompletionRatio`、`GroupRatio`、`GroupGroupRatio`、`group_ratio_setting.*`、渠道成本比。

### 3.4 支付路径（只随 QPU 变数值，公式不改）

以下位置的公式本身是"金额 × QPU ÷ Price"或"金额 × QPU"，QPU 变成 7.3 亿后自动正确，**不改代码**。但评审时要逐个确认三点：没有 `int()` 截断；`IntPart()` 发生在乘法之后；中间结果不溢出 int64。

- `controller/topup.go`
- `controller/topup_alipay.go`、`controller/topup_wxpay.go`
- `controller/topup_package.go`
- `controller/topup_stripe.go`（:401 `amount / QPU`；:422 `minTopup × int(QPU)`）
- `controller/topup_waffo.go`、`controller/topup_waffo_pancake.go`
- `model/topup.go`（:151、:374-379、:528、:699）
- `model/bank_transfer.go:170`

其他按美元语义使用 QPU、不改的位置：

| 位置 | 用法 | 说明 |
|---|---|---|
| `controller/token.go:382/483` | `maxQuotaValue = 10⁹ × QPU` | = 7.3×10¹⁷，在 int64 范围内 |
| `common/quota.go` `GetTrustQuota` | `10 × QPU` | 信任额度 $10 |
| `model/user.go:586` | 转账最低额度 `QPU` | $1 |

这三处继续按美元语义使用 QPU，不在本次范围。如需改成人民币语义，另提需求。

### 3.5 展示、报表与对账（改为 `quota / QuotaPerYuan` 得到人民币）

| 位置 | 现状 | 改为 |
|---|---|---|
| `logger/logger.go` `LogQuota` 一族（:125-234） | CNY 模式：`quota / QPU × 汇率`，`%.6f` | CNY：`quota / QuotaPerYuan`，`%.8f`（D5）；USD：`quota / QPU` |
| `controller/log.go:287-303` | `cost_cny = quota / QPU × 汇率`，6 位小数 | `quota / QuotaPerYuan`，8 位小数 |
| `controller/billing.go:45-100` | CNY 分支 `quota / QPU × 汇率` | CNY 分支 `quota / QuotaPerYuan`；USD 分支不变 |
| `service/reconcile_helpers.go:194/202` | `quota / QPU × 汇率` | `quota / QuotaPerYuan` |
| `service/plan_fulfillment.go:81` `quotaToFen` | `quota / QPU × 汇率 × 100` | `quota × 100 / QuotaPerYuan`（先乘后除） |
| `common/points.go`、`common/compute_points.go` | 默认换算率 `QPU / (汇率 × 100)`；`YuanToComputePoints` 用 `QPU / 汇率` | `QuotaPerYuan / 100 = 1,000,000`；`YuanToComputePoints` 用 `QuotaPerYuan` |
| `controller/misc.go:74` `/api/status` | `quota_per_unit` | 新增 `quota_per_yuan: 100000000`。`quota_per_unit` 保留（730000000） |

### 3.6 日志与任务里冻结的定价字段

| 字段 | 写入位置 | 新数据 | 历史数据 |
|---|---|---|---|
| `other.model_ratio` / `model_price` / `completion_ratio` / `cache_ratio` / `image_ratio` / `cache_creation_ratio` | `service/log_info_generate.go:76-82,339-342` | ¥ + `price_unit` | 不改，前端按 §6.2 换算 |
| `other.web_search_price` / `file_search_price` / `audio_input_price` / `image_generation_call_price` | `service/text_quota.go:415-443` | ¥ | 同上 |
| `other.video_unit_price` / `video_price_mode` | `service/task_billing.go:81-82` | ¥ | 同上 |
| `other.base_amount`（违规罚金） | `service/violation_fee.go:142` | ¥ | 同上 |
| `other.expr_b64`（分段表达式原文） | `service/log_info_generate.go:362-363` | 原文带 `v2:` 前缀 | v1 原文，前端按版本区分 |
| 任务 `private_data.billing_context.model_price` / `model_ratio` | `model/task.go:388-390`，写入见 `service/task_billing.go:147-149` | ¥ + `price_unit` | 迁移 ×7.3 / ×14.6 并打标记（§5.3） |
| 任务 `private_data.video_billing.unit_price` | `model/task.go:412-416`（注释写的是"美元"） | ¥ | 迁移 ×7.3 |
| `BillingSnapshot.quota_per_unit` / `expr_version` | `pkg/billingexpr/types.go:50-51` | v2 不读 `quota_per_unit` | v1 快照继续按冻结的 QPU 结算 |

### 3.7 对外接口与同步

| 接口 | 处理 |
|---|---|
| `/api/pricing`（`model/pricing.go:452-490`） | `model_ratio`、`model_price`、`video_pricing`、`billing_expr` 原样下发，现在都是人民币口径。前端去掉换算 |
| `/api/ratio_config`（`controller/ratio_config.go`） | **删除**（D4、B7） |
| 上游倍率同步 `/api/ratio_sync/*`（`controller/ratio_sync.go`，含 OpenRouter、models.dev 转换） | **删除**（D7、B6）。删除后不存在"外部美元报价进入系统"的入口，§10 中与同步相关的汇率用途随之消失 |

### 3.8 前端换算点

所有"倍率 × 2"、"× 汇率"、"÷ 汇率 ÷ 2"、`quota / quota_per_unit × usd_exchange_rate` 都要处理。下表中的数字是 `grep` 命中数，完整行号清单见附录 A。

| 文件 | 命中 | 内容 |
|---|---|---|
| `web/classic/src/helpers/render.jsx` | 48 | 计费过程、价格展示：`modelRatio * 2.0`，`* rate`，`toFixed(6)`；`renderQuota`、`getQuotaWithUnit`、`getCurrencyConfig`、`convertUSDToCurrency`；工具价 `usdAmount` / `renderDisplayAmountFromUsd`；视频 `unitPrice * rate` |
| `web/classic/src/pages/Setting/Ratio/hooks/useModelPricingEditorState.js` | 16 | 编辑器：`ratioToBasePrice` 用 `× 2 × 汇率`；保存时 `÷ 汇率 ÷ 2`、按次价 `÷ 汇率`、视频格子 `× / ÷ 汇率`；`DEFAULT_USD2RMB_RATE` |
| `web/classic/src/helpers/utils.jsx` | 4 | 定价页单价：`model_ratio * 2 * groupRatio`；`rate = usd_exchange_rate` |
| `web/classic/src/helpers/computePointPreview.js` | 2 | `USD_PER_MILLION_TOKENS_FACTOR = 2` |
| `web/classic/src/helpers/videoMatrix.js` | 2 | `priceUSD` / `originalPriceUSD` |
| `web/classic/src/helpers/quota.js` | 2 | 兜底值 `FALLBACK_QUOTA_PER_UNIT = 500000`、`FALLBACK_QUOTA_PER_POINT = 684.93` |
| `web/classic/src/helpers/planPrice.js` | 1 | `usd_exchange_rate` |
| `web/classic/src/hooks/model-pricing/useModelPricingData.jsx` | 1 | 同上 |
| `web/classic/src/components/settings/personal/cards/NotificationSettings.jsx` | 1 | 同上 |
| `web/classic/src/components/topup/RechargeCard.jsx` | 1 | 同上 |
| `web/classic/src/pages/BankTransfer/TransferTab.jsx` | 1 | 同上 |
| `web/classic/src/pages/Setting/Ratio/components/TieredPricingEditor.jsx` | 多处 | 预设表达式（:791-878）系数是美元；说明文案（:1579）写的是"$/1M tokens" |
| `web/classic/src/pages/Setting/Ratio/ToolPriceSettings.jsx` | 2 | 文案"$/1K次"（:167、:201） |
| `web/classic/src/pages/Setting/Ratio/UpstreamRatioSync.jsx` | — | **删除**（B6），不需要改换算 |
| `web/classic/src/components/table/model-pricing/**` | 若干 | 定价表、详情侧栏、价格卡片，走 `utils.jsx` 的价格函数，随之改 |
| `web/mobile/src/pages/Models.jsx` | 3 | `model_ratio * 2`、`usd_exchange_rate` |
| `web/mobile/src/utils/quota.js` | 2 | `renderQuota` |

---
## 4. 开发任务

### B 后端

**B1 单位常量**
- 新增 `common.QuotaPerYuan = 100_000_000`。
- `QuotaPerUnit` 默认值改为 `730_000_000`。
- 删除 `QuotaScale()` 和 `RatioQuotaBase`，删掉后编译报错的地方就是待改清单。
- 积分、算力点的默认换算率改为 `QuotaPerYuan / 100`。
- 完成标准：`rg "QuotaScale|RatioQuotaBase"` 为空；`/api/status` 返回 `quota_per_yuan=100000000`、`quota_per_point=1000000`、`quota_per_compute_point=1000000`。

**B2 计费路径**
- 按 §3.1 逐处修改。倍率路径统一调用新 helper `common.RatioQuotaPerToken(ratio) = ratio × QuotaPerYuan / 10⁶`，不要在各处手写 `100`。
- 完成标准：
  - §2.3 的 E1–E7 每个样例至少有一个单测，断言精确的 quota 值；
  - `rg "QuotaPerUnit" service relay pkg/billingexpr controller/channel-test.go` 只剩 v1 快照这一处。

**B3 代码内美元常量**
- 按 §3.2 修改。
- 完成标准：
  - 工具默认价、GPT-Image-1、Gemini 音频的单测断言人民币值，例如 `web_search = 73`；
  - `GetDefaultModelRatioMap()["gpt-4.1"] == 14.6`；
  - `defaultModelRatio` 原表 diff 为空。

**B4 表达式 v2**
- `ParseExprVersion` 识别 `v2:`。v2 的编译环境、token 归一化与 v1 相同，只有换算不同。
- `quotaConversion` 增加 v2 分支。预扣（`relay/helper/price.go` `modelPriceHelperTiered`）改为调用同一个换算函数，不再自己写 `rawCost / 10⁶ × QPU`。
- 无前缀的表达式仍按 v1 处理，以兼容历史数据。新保存的表达式由编辑器强制加 `v2:`；后端保存校验拒绝新写入 v1。
- 更新 `pkg/billingexpr/expr.md`：原则 3 改为"系数是 ¥/1M"，同时补写 Quota Conversion 和 Versioning 两节。
- 完成标准：
  - 同一表达式，v1 快照与 v2 快照的结算结果符合 `v1 = v2 × 汇率`；
  - 预扣与结算使用同一个换算函数；
  - 保存 v1 表达式被拒。

**B5 日志与任务标记**
- §3.6 中所有"新数据"的写入点都加上 `price_unit: "cny"`。
- `extraContent` 等文案改成人民币、8 位小数。
- 完成标准：消费日志、任务日志、违规罚金日志的单测都断言带有这个标记。

**B6 删除上游倍率同步（D7）**
- 后端：
  - 删除 `controller/ratio_sync.go` 整个文件。已核实文件里的函数、类型和变量（`pricingSyncFields`、`upstreamResult`、`modelsDev*` 等）在其他地方都没有引用；
  - 删除 `router/api-router.go` 中的 `ratioSyncRoute` 路由组（`/api/ratio_sync/channels`、`/api/ratio_sync/fetch`）。
- 前端：
  - 删除 `web/classic/src/pages/Setting/Ratio/UpstreamRatioSync.jsx`；
  - 删除 `web/classic/src/components/settings/ChannelSelectorModal.jsx`（只有同步页在用）；
  - `web/classic/src/components/settings/RatioSetting.jsx` 去掉 import 和"上游价格同步"这个 tab（`itemKey=upstream_sync`）。
- **不能删**：
  - `controller/model_sync.go`，它是另一个功能（模型列表同步），和定价无关；
  - `billing_setting.BillingModeField` / `BillingExprField`，计费本身在用。
- 完成标准：`go build ./...`、`go vet ./...` 通过；`rg "ratio_sync|UpstreamRatioSync|ChannelSelectorModal|FetchUpstreamRatios"` 在 `controller/`、`router/`、`web/` 中为空；倍率设置页正常打开，没有空 tab。

**B7 删除 `/api/ratio_config`（D4）**
- 后端：
  - 删除 `controller/ratio_config.go`、`setting/ratio_setting/expose_ratio.go`；
  - 删除 `router/api-router.go` 中的 `/ratio_config` 路由；
  - 删除 `model/option.go` 中 `ExposeRatioEnabled` 的注册（:213）和更新分支（:364-365）。
- 前端：删除 `ModelRatioSettings.jsx` 中的"暴露倍率"开关（:51、:326-328），以及 `RatioSetting.jsx` 中 `ExposeRatioEnabled` 的默认值和布尔解析（:45、:63）。
- 库里没有 `ExposeRatioEnabled` 这个 key（§5.0），不需要清理数据。如果别的环境里有这个 key，留着也无害：`updateOptionMap` 只会把它写进 `OptionMap`，不会再有任何作用。
- 完成标准：`rg "ExposeRatio|ratio_config|GetRatioConfig"` 在后端和前端都为空（`docs/` 除外）。

**B8 展示、报表、对账**
- 按 §3.5 修改。
- 完成标准：`controller/log.go` 的 `cost_cny`、`LogQuota`、对账金额、套餐履约的"分"都有单测，并且三项都要覆盖：
  - 1 quota → ¥0.00000001；
  - 10⁸ quota → ¥1；
  - 修改汇率不影响结果。

**B9 启动保护**
- 新镜像启动时检查 `options.QuotaUnitMigrationV2`：
  - 值不是 `done`，且库里已有用户数据（`users` 表非空）时，打印明确的错误并拒绝启动计费服务。这一条防止"新代码配旧数据"导致按 1/14.6 少收费。
  - 全新空库直接写入 `done`。
  - 只有带 `--migrate-quota-unit-v2=...` 参数运行时放行。
- 完成标准：三种情况各有一个测试。

### F 前端（`web/classic`、`web/mobile`）

**F1 货币换算基准改为人民币**
- 新增 `getDisplayCurrency()`，返回 `{ symbol, ratePerCny, type }`：
  - CNY：`1`；
  - USD：`1 / usd_exchange_rate`；
  - CUSTOM：`custom_currency_exchange_rate / usd_exchange_rate`（自定义汇率原本是相对美元的）。
- `renderQuota`、`getQuotaWithUnit`、`renderQuotaWithAmount` 的计算链改为 `quota / quota_per_yuan × ratePerCny`。
- 删除 `convertUSDToCurrency` / `renderDisplayAmountFromUsd`，替换为基于人民币的同名函数（如 `convertCNYToCurrency`）。
- 完成标准：`rg "\* 2(\.0)?\b" web/classic/src/helpers web/mobile/src` 里不再有任何与倍率相关的命中；`usd_exchange_rate` 只出现在 `getDisplayCurrency` 和支付相关组件中。

**F2 日志计费过程**
- 日志数据进入渲染之前，统一经过 `normalizeLogPricing(other)`：
  - 有 `price_unit === 'cny'` 时原样返回；
  - 否则视为历史美元口径：`model_ratio × 2 × usd_exchange_rate`，各 `*_price` 和 `video_unit_price` 乘 `usd_exchange_rate`；
  - 分段表达式按 `expr_b64` 的版本前缀判断口径。
- `render.jsx` 中所有计费过程函数只处理人民币。
- 完成标准：新旧两条日志的计费过程合计都等于花费列（沿用 V1 文档 F2 的口径），旧日志的展示结果与改造前逐字相同（快照测试）。

**F3 编辑器所见即所存**
- `useModelPricingEditorState.js`：
  - 输入价直接作为 `ModelRatio` 保存；
  - 按次价、视频格子直接保存；
  - 删除 `DEFAULT_USD2RMB_RATE` 和所有 `÷ 汇率 ÷ 2`。
- `TieredPricingEditor.jsx`：
  - 预设表达式改为人民币系数并加 `v2:` 前缀；
  - 说明文案改为"¥/百万 tokens"；
  - 打开 v1 表达式时提示"这是美元口径的旧表达式"。
- `ToolPriceSettings.jsx`：文案改为"¥/千次"。
- 完成标准：编辑器"读 → 不改 → 保存"后，提交的 JSON 与原 JSON 逐键相同（往返测试）。

**F4 定价页、模型详情、移动端**
- `utils.jsx`、`model-pricing/**`、`computePointPreview.js`、`videoMatrix.js`、`mobile/src/pages/Models.jsx` 去掉 `× 2` 和 `× 汇率`。
- `priceUSD` 等字段一并改名为 `priceCNY`。

**F5 精度**
- 人民币花费 8 位小数（D5）：日志花费列、计费过程合计、`roundToDisplay(…, 8)`。
- 单价展示保留最多 6 位小数，并去掉末尾的 0。

**F6 兜底值**
- `helpers/quota.js`：
  - `FALLBACK_QUOTA_PER_UNIT = 730000000`；
  - 新增 `FALLBACK_QUOTA_PER_YUAN = 100000000`；
  - `FALLBACK_QUOTA_PER_POINT = 1000000`。
- `helpers/data.js` 把 `quota_per_yuan` 写入 localStorage。

### M 迁移程序

**M2** 详见 §5。

---

## 5. 数据迁移（M2）

### 5.0 现网数据快照（PostgreSQL 只读查询，2026-10-11）

结论：现网数据不需要调整迁移方案，只需按下表修正细节。

| 项目 | 现网值 | 对迁移的影响 |
|---|---|---|
| `QuotaPerUnit` / `USDExchangeRate` / `Price` | 7300000 / 7.3 / 7.3 | 满足 §5.1 的前置条件 |
| `QuotaUnitMigration` | `done`，logs 游标 851,506，耗时 87 秒 | V1 已完成；V2 的 logs 量（851,623 行）与 V1 相同，预计耗时也相近 |
| `QuotaUnitMigrationV2` | 不存在 | 符合预期 |
| `billing_setting.billing_expr` / `billing_mode` | `{}` / `{}` | 现网**没有**分段表达式，D3 的"只报告"在现网会报 0 条 |
| `ModelRatio` | 417 个模型，其中 28 个为 0，36 个低于 ¥1/百万，最低 ¥0.01/百万（GLM-4-Flash 等 8 个） | ¥0.01/百万 正是本次改造要解决的场景；×14.6 后按 6 位小数舍入，最大舍入误差为 7.2×10⁻⁸ ¥/百万（D6 验证通过） |
| `ModelRatio` 中舍不干净的 16 个 | 例如 `glm-3-turbo` 0.3572 → ¥5.21512、`SparkDesk-*` 1.2858 → ¥18.77268、`qwen-turbo` 0.8572 → ¥12.51512 | 都是上游老默认表按"汇率 7"折算的旧值，迁移后人民币价**与现在实际收费完全一致**，所以照常迁移、不做取整；是否改成整数价由管理员在编辑器里自行决定 |
| `ModelPrice` | 约 90 个；×7.3 后全部在 3 位小数内收敛（如 0.41095890411 → ¥3，0.04 → ¥0.292） | 无异常 |
| `VideoPricingConfig` | 14 个模型：2 个 `token` 模式（doubao-seedance-2-0 / 2-5，各 3 档分辨率 × with/without_video），12 个 `per_second` 模式；没有 `per_call`。全部 26 个格子 ×7.3 后按 6 位小数舍入都是干净的人民币价，最大舍入误差 3.3×10⁻¹²，例如 6.301369863014 → ¥46/百万、0.068493150685 → ¥0.5/秒、0.001369863014 → ¥0.01/秒 | 无异常。分辨率键要原样保留，大小写不能动（`ltx2.5-hd` 用 `2K`，`seedvr2`/`swiftvr` 用 `2k` 和通配 `*`）；迁移只改数值，不改键 |
| `tool_price_setting.prices`、`grok.violation_deduction_amount` | 库里没有这两个 key | 走代码默认值，迁移无需改写，但 B2 的默认常量必须改成人民币 |
| `ExposeRatioEnabled` 等暴露开关 | 库里没有（默认关闭） | B7 删除 `/api/ratio_config` 不影响现网 |
| `points_setting.quota_per_point`、`compute_point_setting.quota_per_compute_point` | 10000 / 10000 | V2 改为 1000000 |
| `QuotaRemindThreshold` | 14600 | ×100 → 1460000 |
| `QuotaForNewUser` | 0；`QuotaForInviter` / `QuotaForInvitee` 不在库里 | ×100 后仍为 0；不在库里的不写 |
| `checkin_setting.min_quota` / `max_quota` | 1 / 29，签到功能现网**关闭**（已确认） | 不影响计费；迁移照常 ×100（→ 100 / 2900），以后要启用签到时再按 1 quota = ¥0.00000001 重新设置 |
| 数值上限 | `users.quota` 最大 9,597,646,121，全站余额合计 13,461,287,423；`logs.quota` 最大 25,674,961，合计 24,786,271,981 | ×100 后最大约 2.5×10¹²，离 JS 安全整数（9×10¹⁵）还有 3,000 多倍余量，离 int64 更远；§5.4 的警告在现网不会触发 |
| `tasks` | SUCCESS 2,547 / FAILURE 194，**没有进行中的任务** | 在途任务换算代码照常实现并测试；上线时按 §9 停服前再查一次，有在途任务就等它们结束 |
| 近 30 天消费日志 | 102,388 条；quota = 1 的 0 条；**quota 在 2–100 之间的 42,237 条（41%）**；按倍率 101,932 条；视频矩阵 176 条；工具附加费 1 条；生图调用、Gemini 音频、分段表达式、违规罚金均为 0 | 41% 的请求单笔花费不到 ¥0.0001，细化 100 倍有实际收益。测试重点放在倍率路径和视频矩阵；工具附加费、生图、音频、违规罚金几乎不用，只要单测覆盖换算即可 |

### 5.1 入口与状态

- 在 `service/quotaunitmigration` 中新增 V2：
  - 命令行参数 `--migrate-quota-unit-v2=dry-run|apply`；
  - 状态 key 为 `QuotaUnitMigrationV2`，状态机、分批、游标、断点续跑、报告格式全部复用 V1。
- 换算倍数不再写死：`rewrite.go:52` 的 `×146/10` 和 `migration.go:42` 的 `scale` 改为参数。V2 的 quota 倍数是整数 `100`，SQL 写成 `col * 100`，不加 `ROUND`，三种数据库都能精确计算。
- 前置条件，任一不满足即拒绝执行：
  - `QuotaUnitMigration.state = done`；
  - `QuotaPerUnit = 7300000`；
  - `USDExchangeRate = 7.3`。汇率不是 7.3 时，倍率换算的 14.6 也会不同，需要人工确认。

### 5.2 quota 数值 × 100（全部复用 V1 清单，`scope.go`）

| 范围 | 内容 |
|---|---|
| 小表（单事务） | V1 `smallNumericScopes` 全部 15 张表的所有列（含 `top_ups.amount` 的 provider 限定条件） |
| logs | `quota`、`points_consumed`、`credit_consumed`（分批） |
| `logs.other` | `logOtherScaledKeys` 中的 12 个键 |
| `tasks.private_data` | `points_consumed`、`credit_consumed`、`stage1_adaptor_quota`、`entitlement_spent[].amount` |
| `users.setting` | `quota_warning_threshold` |
| options × 100 | `QuotaForNewUser`、`QuotaForInviter`、`QuotaForInvitee`、`QuotaRemindThreshold`、`checkin_setting.min_quota`、`checkin_setting.max_quota` |
| options 固定值 | `points_setting.quota_per_point = 1000000`、`compute_point_setting.quota_per_compute_point = 1000000`、`QuotaPerUnit = 730000000` |

`PreConsumedQuota` 仍然**不在**清单中：它是 token 下限，不是 quota（V1 文档 §10）。

### 5.3 定价配置换算（V2 新增）

| key | 换算 | 舍入 |
|---|---|---|
| `ModelRatio`（JSON map） | 每个值 × 14.6（= 2 × 7.3） | 6 位小数（D6） |
| `ModelPrice`（JSON map） | 每个值 × 7.3。保留负值哨兵（如 `-1`），不换算 | 6 位小数 |
| `VideoPricingConfig` | 所有价格格子 × 7.3，结构和非价格字段原样保留 | 6 位小数 |
| `tool_price_setting.prices` | 每个值 × 7.3；库中没有该 key 时不写，使用代码默认值（已是人民币） | 6 位小数 |
| `grok.violation_deduction_amount` | × 7.3；库中没有该 key 时不写 | 6 位小数 |
| `billing_setting.billing_expr` | **不改写**（D3）。dry-run 和 apply 报告逐条列出 v1 表达式；数量大于 0 时报告标红，提示管理员在编辑器中重录为 v2 | — |
| 在途任务 `tasks.private_data.billing_context` | `model_price × 7.3`（跳过 `-1`）、`model_ratio × 14.6`，写入 `price_unit = cny` | 6 位小数 |
| 在途任务 `tasks.private_data.video_billing.unit_price` | × 7.3 | 6 位小数 |

**舍入的理由**：现有倍率是编辑器按"¥ ÷ 7.3 ÷ 2"计算后，用 `toNormalizedNumber` 存下的，例如 ¥2 存为 `0.13698630137`。乘 14.6 得到 `1.99999999…`，四舍五入到 6 位小数后正好还原为 `2`。6 位小数对应 ¥0.000001/百万 tokens 的精度，比任何实际报价都细。

**报告要求**：
- 逐个模型列出"旧值 → 新值 → 换算前人民币价 → 换算后人民币价 → 差值"。
- 任一模型的差值超过 `1e-6` 时，报告单列出来，交由人工确认。
- 定价配置写在小表事务里，与 options 一起提交。

### 5.4 预检

- 列类型与溢出预检（V1 `precheck.go`）改为按 ×100 检查：`max(|列|) × 100 < 9.2×10¹⁸`。
- 新增 JS 安全整数检查：若任一 `users.quota` 或 `tokens.remain_quota` 乘 100 后超过 `2⁵³`（约 ¥9,007 万），报告警告，但不阻断。
- 新增 `ModelRatio`、`ModelPrice` JSON 的解析检查：解析失败直接拒绝执行，不允许跳过。

### 5.5 幂等与回滚

- **幂等**：状态 key 为 `done` 时，再次执行不做任何修改，退出码为 0。定价换算与 quota 换算放在同一个状态机里，断点续跑时不会重复换算。
- **回滚**：与 V1 相同，即"恢复备份 + 换回旧镜像"，两件事必须同时做。apply 之后只换回旧镜像，会按 14.6 倍多收费。

---

## 6. 前端口径细节

### 6.1 展示模式

| `quota_display_type` | 金额 | 单价 |
|---|---|---|
| CNY（推荐） | `quota / 10⁸` | 原样（¥/百万、¥/次） |
| USD | `quota / 10⁸ / usd_exchange_rate` | ÷ `usd_exchange_rate` |
| CUSTOM | `quota / 10⁸ / usd_exchange_rate × custom_rate` | 同左，按同一系数换算 |
| TOKENS | quota 原值 | — |

### 6.2 旧日志换算（`normalizeLogPricing`）

旧日志只能用当前的 `usd_exchange_rate` 换算，因为日志里没有冻结当时的汇率。这与现在线上的行为一致：生产自 V1 起汇率一直是 7.3。如果以后修改汇率，旧日志计费过程里的人民币单价会跟着变，但花费列（quota）不受影响。这一点写进代码注释即可，不必另外处理。

---

## 7. 测试标准

### 7.1 后端（`go test ./...` 全绿）

- **计费样例**：E1–E7 各至少一个单测，覆盖文本、音频、预扣、按次、视频（按 token / 按次 / 按秒）、工具附加费、违规罚金、渠道测试、表达式 v1 / v2。
- **金额守恒**：同一个模型、同一请求，"V1 配置 + V1 代码"算出的人民币金额，与"迁移后配置 + V2 代码"算出的人民币金额，差值不超过 1 个 V1 quota（¥0.000001）。测试数据取 `defaultModelRatio` 的全部模型，逐个跑一遍。
- **低价**：¥0.01/百万 时 1、50、149、150 tokens 的结果（分别是 1、1、1、2 quota）；¥0.001/百万 时 1 token 扣 1 quota（最低扣费）。
- **溢出**：单笔 10⁷ tokens × ¥1000/百万 = ¥10,000 = 10¹² quota，算到 int 不溢出。
- **迁移**：
  - V1 迁移测试矩阵的 SQLite、PostgreSQL、MySQL 三库全部复用：dry-run 不写库、apply 结果正确、幂等、断点续跑、日志库独立。
  - 新增：定价 JSON 换算与舍入；`-1` 哨兵不换算；未知结构的 JSON 原样保留；v1 表达式只报告、不改写；前置条件不满足时拒绝执行；B9 启动保护。
- **删除项**：`/api/ratio_sync/*` 和 `/api/ratio_config` 都返回 404；倍率设置页的 tab 数量正确。

### 7.2 前端（`cd web/classic && bun install && bun run test`）

- `normalizeLogPricing` 新旧日志的快照测试。
- 编辑器往返测试（F3）。
- `getDisplayCurrency` 四种展示模式的测试。
- 计费过程合计等于花费列（8 位小数）。
- 定价页单价与后端 E1 / E5 一致。

### 7.3 端到端（PostgreSQL 本地）

1. 用 V1 镜像造数据：用户、令牌、充值、按倍率 / 按次 / 视频 / 工具的消费日志、在途视频任务。
2. 记下以下数据：各用户余额的人民币值、`/api/pricing` 中各模型的人民币单价、日志页每条日志的花费和计费过程截图。
3. 切换到 V2 镜像，执行 dry-run 和 apply。
4. 比对第 2 步的三项数据，必须完全一致（人民币单价按 6 位小数比对）。
5. 新发起一次 ¥0.01/百万、1 token 的请求，花费为 ¥0.00000001。
6. 检查 `/api/status`：`quota_per_yuan=100000000`、`quota_per_unit=730000000`、`quota_per_point=1000000`。

---

## 8. 代码审查清单

以下每条 grep 的结果都要附在 PR 描述里：

- `rg "QuotaScale|RatioQuotaBase"` 为空。
- `rg "QuotaPerUnit" --type go` 只出现在以下位置：
  - §3.4 列出的支付文件；
  - §3.5 的 USD 展示分支；
  - `pkg/billingexpr` 的 v1 分支；
  - `model/option.go`、`controller/misc.go`；
  - 迁移程序。
- `rg "USD2RMB|usdToCNY"` 只出现在默认表换算和代码常量中。
- 前端 `rg "\* 2(\.0)?\b"` 中不再有与倍率相关的命中；`rg "usd_exchange_rate"` 只出现在 `getDisplayCurrency`、`normalizeLogPricing` 和支付组件中。
- 前端 `rg "500000|684\.93|7300000"` 为空。
- 所有价格路径都是先乘后除、最后一次 `Round`，不使用 `int()` 截断。
- 所有 JSON 读写都使用 `common.*`；迁移 SQL 在三种数据库中都能执行。

---

## 9. 生产上线步骤（runbook）

1. 备份：`pg_dump` 全库，并记录行数和 `SUM(users.quota)`。
2. 用生产备份在本地完整演练一遍 §7.3，记录 apply 耗时。V1 的实测耗时约 1.5 分钟（dev-report §12）。
3. 公告停服窗口。
4. **停服**：`docker compose stop new-api`，并确认容器状态为 `Exited`。V1 的记录无法确认当时是否已停服，这次必须显式执行这一步。
5. 拉取 V2 镜像，执行 `--migrate-quota-unit-v2=dry-run`，人工核对报告：
   - 定价差值清单为空，或已逐条确认；
   - v1 表达式数量为 0。
6. 执行 `--migrate-quota-unit-v2=apply`，保存报告。
7. 启动服务。B9 启动保护会检查状态是否为 `done`。
8. 冒烟测试：
   - `/api/status`；
   - 抽查 3 个用户余额的人民币值；
   - 定价页抽查 3 个模型；
   - 发起 1 次真实的低价请求和 1 次按次请求；
   - 发起 1 次小额真实充值（支付宝或微信）。
9. 观察 24 小时：对账页上游成本与收入的比例是否与迁移前同期一致。

回滚：只能恢复第 1 步的备份，并换回旧镜像。

---

## 10. 注意事项与已知取舍

1. **不要把 14.6 带进新代码。** 14.6 只出现在迁移程序和"静态美元默认表的换算"里。新代码中看到 `× 2 ×` 或 `× 14.6` 就是 bug。
2. **`PreConsumedQuota` 是 token 下限**，不是 quota。V1 和 V2 都不迁移它、不缩放它。
3. **按量预扣的 `modelRatio / 2 × QPU`**：表面上是价格路径，实际语义是"预扣 25 万 tokens 的输入价"，要改写成 token 语义（§3.1），不能简单换成 `QuotaPerYuan`。
4. **OpenRouter 缓存创建反推**（`CalcOpenRouterCacheCreateTokens`）：上游回报的是美元，这里必须乘汇率，这是计费路径中唯一合法读取汇率的地方。
5. **低价最低扣费**：单价低于 ¥0.005/百万 tokens 时，1 token 请求仍扣 1 quota（¥0.00000001）。如果以后需要更细的粒度，只能再细化单位或做零头累计，本次不做。
6. **JS 安全整数**：单个数值上限约 ¥9,007 万。全站多年累计的汇总接口（资金报表、`SUM(logs.quota)`）如果接近这个值，应改为后端按元返回。
7. **合并上游代码**：上游新增的计费代码默认按"美元 × QPU"编写。合并时要用 §8 的 grep 规则把关。
8. **汇率变更**：改汇率只影响美元支付、USD 展示、OpenRouter 缓存反推和旧日志的计费过程展示，不影响计费和余额。同时仍要遵守 `QPU = 汇率 × 10⁸`，否则美元支付到账的额度会算错。

---

## 11. 待确认

无。D1–D7 已全部确认，现网数据核对见 §5.0。

---

## 附录 A：前端命中清单

用 `rg -n -E "ratio\w*\s*\*\s*2(\.0)?\b|Ratio\w*\s*\*\s*2(\.0)?\b|usd_exchange_rate|quota_per_unit|usd2rmb|USD2RMB" web/classic/src web/mobile/src` 生成，共 92 行（2026-10-11）。开发开始时重新生成一遍，逐行勾选。

## 附录 B：后端命中清单

用 `rg -n -E "QuotaPerUnit|QuotaScale\(\)|RatioQuotaBase" --type go -g '!*_test.go' -g '!service/quotaunitmigration/**'` 生成，共 132 行，涉及 34 个文件（2026-10-11）。§3.1 / §3.4 / §3.5 已对其中每个文件做了分类。
