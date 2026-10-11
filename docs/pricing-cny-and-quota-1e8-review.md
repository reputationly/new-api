# V2 设计文档评审意见（pricing-cny-and-quota-1e8.md）

> 评审对象：[pricing-cny-and-quota-1e8.md](./pricing-cny-and-quota-1e8.md)（commit `c0c3d60cf`，621 行）
> 评审基准：main `c0c3d60cf` 的代码，对全文的改造面清单逐项 grep 核对
> 评审时间：2026-10-11
>
> ⚠️ **单位后续又改过一次**：2026-10-11 用户决定把 1 quota 从 ¥10⁻⁸ 再细化到 ¥10⁻⁹
> （`QuotaPerYuan = 10⁹`），因此本文里所有与单位相关的数字（如"×100""1/100""实收 1/100"）
> 都已过时；以设计文档 [§13](./pricing-cny-and-quota-1e8.md) 为准。结论与代码位置不受影响。
>
> **结论**：方向认可（计费彻底脱离汇率 / 配置即人民币 / 单位细化到 10⁻⁸）。改造面清单质量高——
> `QuotaPerUnit` 与 `QuotaScale()` 的全部非测试消费点都能对应到 §3.1 / §3.4 / §3.5 的某一类，无遗漏。
> 但有 **4 处必须改**（其中 3 处会直接让实现或测试写错），**1 项需先定口径**，另有 4 条建议。

---

## 一、必须改

### R1（算术错误）§2.3 E3 与 §7.1 的"低价"期望值仍是 V1 单位

文档按 §2.2 的公式 `quota = tokens × ratio × 100`（ratio = ¥/百万 tokens），但 E3 与 §7.1 写的是
**V1 单位**（1 quota = ¥0.000001）下的数字：

| 场景 | 文档写的 | V2 正确值 |
|---|---|---|
| E3：¥0.01/百万、149 tokens | 「1.49 → 四舍五入为 1」→ 1 quota | **149 quota**（¥0.00000149） |
| §7.1：¥0.01/百万 的 1 / 50 / 149 / 150 tokens | 「1、1、1、2 quota」 | **1、50、149、150 quota** |

`ratio × 100 = 1`，即该单价下 **1 token 恰好 1 quota**，与 E2/G1 的自述一致；而 1.49 / 1,1,1,2 是
`ratio × 1`（V1 换算）的结果。照文档写断言会得到 149 vs 1 的失败，或误把 149 当成"最低扣费"场景。

**建议订正**：

- E3 → `149 quota = ¥0.00000149`；§7.1 低价 → `1 / 50 / 149 / 150 → 1 / 50 / 149 / 150 quota`。
- 最低扣费只留 E4（¥0.003/百万、1 token → 0.3 → 0 → 1 quota）。触发条件可写成
  `tokens × ratio × 100 < 0.5`，即 `ratio < ¥0.005/百万`。
- 顺带把 `.5` 的舍入方向写进测试（`decimal.Round` 是 half-away-from-zero；`ratio = ¥0.005/百万`、
  1 token = 0.5 → 应得 1 quota），避免以后换实现时口径漂移。

### R2（口径矛盾，会静默少收 100 倍）v1 表达式在 V2 之后不能"保持原样仍然正确"

现状 `pkg/billingexpr/settle.go` `quotaConversion` **只有 v1 一个分支**：

```go
return exprOutput / 1_000_000 * snap.QuotaPerUnit   // 美元 → quota，输出单位 = V1 的 ¥1e-6
```

设表达式系数 `c`、tokens `T`（v1 语义：`c` 是 $/百万）：

| | 公式 | 结果 |
|---|---|---|
| v1（冻结 `snap.QuotaPerUnit = 7.3e6`） | `c·T/10⁶ × 7.3×10⁶` | `7.3·c·T` quota（**V1 单位**） |
| v2（提案） | `c·T/10⁶ × 10⁸` | `100·c·T` quota（**V2 单位**） |

同一份文档（D3：不改写）在 V2 之后用 v1 分支结算，输出的是 **V1 单位**的数字，而余额/quota
已全部 ×100 变成 ¥1e-8 单位 → 实收只有应有值的 **1/100**。

这与两处自述冲突：

- §3.1「v1 保持原样（快照里冻结了 QPU，仍然正确）」——**不成立**；
- B4 完成标准「v1 快照与 v2 快照的结算结果符合 `v1 = v2 × 汇率`」——只在**各自单位的 ¥**上成立
  （`7.3e-6·cT / 1e-6·cT = 7.3`），在 **quota** 上是 `v1 = v2 × 0.073`（= 汇率/100）。

生产目前没有存量表达式（§5.0 `billing_expr = {}`），所以不是生产事故，但兼容分支必须先定口径，
否则它就是一个会静默少收的路径。三个选项：

| 选项 | 做法 | 代价 |
|---|---|---|
| (a) 保持人民币价不变 | v1 分支再乘 `QuotaPerYuan / 10⁶`（= 100），判据改为 `v1¥ = v2¥ × 汇率` | 多一个乘数，未来容易再写错 |
| **(b) 拒绝结算 v1（推荐）** | v1 直接返回错误，提示管理员在编辑器重录为 v2 | 要处理在途任务的 v1 快照；生产 0 条 |
| (c) 就地当人民币 | 价格降 7.3 倍 | 与 §2.6/§6.2「历史数据 = 美元口径」冲突，需另写说明 |

**另外**：在途任务的 v1 快照同样受影响（§5.3 只迁 `billing_context` 的 `model_price` / `model_ratio`，
没有提表达式快照里冻结的 `quota_per_unit`）。无论选哪个口径，都要在 §5.3 补一句。

### R3（内部矛盾）§3.6 与 §5.3 对"任务定价字段迁不迁"说法不一致

- §3.6 表格：「任务 `private_data.billing_context.model_price` / `model_ratio` … 历史数据：
  **迁移 ×7.3 / ×14.6 并打标记**（§5.3）」；
- §5.3 表格行：「**在途任务** `tasks.private_data.billing_context`」。

若只迁在途任务（现网 0 条），则**全部已完成任务**（§5.0：SUCCESS 2,547 / FAILURE 194）的
`private_data` 仍是美元价、也没有 `price_unit`；而任务详情页正是从 `billing_context` 渲染计费过程的
→ 美元数字会被当人民币读，与实际收费不一致。

**二选一，请定**：

- (a)（推荐）**全部任务一起迁**：`tasks.private_data` 共 2,741 行，成本可忽略，前端不必为任务详情
  再写一套 normalize；
- (b) 只迁在途，但要在 §6.2 明确"任务详情也走 `normalizeLogPricing`"，并把它写进 F2 的清单与测试。

### R4（事实描述错误）§3.1 extraContent 现在拼的是 quota，不是美元

`service/text_quota.go:357` 起的文案实际是：

```go
summary.WebSearchPrice × 次数 / 1000 × GroupRatio × common.QuotaPerUnit
```

即输出 **quota 数字**（Web Search / Claude Web Search / File Search / Audio Input / Image Generation
五条同理）。文档写"现状拼的是美元花费，改为人民币保留 8 位小数"，照它实现会多写一层换算。

**正确改法**：`quota / QuotaPerYuan`（8 位小数），或直接拼 `price × 次数 / 1000 × 分组`
（迁移后本身就是人民币）。

---

## 二、需你确认的口径

### Q1 B9 启动保护改变了上线顺序，并把"误升级"从静默错价变成停机

V1 的中间态是安全的（新代码 + 旧数据 = 等价），所以当时可以先升级镜像、再择机迁移。
V2 的 B9 会让**新镜像 + 未迁移数据直接拒绝启动**，于是：

- 不能再提前拉 V2 镜像；页面的**在线升级**（watchtower 已是 API-only、不会自动升级）一旦在窗口前
  被点，服务会起来即退出 → **计划外停机**，直到跑完迁移；
- 反过来这也更安全：V2 镜像在 V1 数据上必然拒绝启动，不会静默按 1/14.6 少收；
- 回滚只能是"恢复备份 + 换回 V1 镜像"（§5.5 已写 ✅）。

**请确认两点**：

1. 拒绝范围是「**整个进程拒绝启动**」还是「只停计费、管理页可用」？
   我倾向**整进程拒绝**：管理页在旧数据上也会按新口径显示（余额、定价、日志全是旧单位数字），
   能打开反而更容易误判。并在错误信息里直接打印 `--migrate-quota-unit-v2=dry-run|apply`
   两条命令与"恢复备份"提示。
2. §9 补一条硬性提醒：**窗口前不要点在线升级 / 不要 pull V2 镜像**（这是本次与 V1 最大的流程差异）。

---

## 三、建议（不阻塞）

- **S1 `hasCustomModelRatio` 的护栏**。`service/quota.go:42` 用
  `GetDefaultModelRatioMap()[name] != 当前 ratio` 判断"管理员是否改过价"。换算必须**同时**作用在
  `InitRatioSettings` 与 `GetDefaultModelRatioMap`、且用同一条公式；只改一处会让**所有默认价模型都被
  判定为"已改价"**（定价页到处显示"已修改"）。B3 建议加断言：默认价模型
  `hasCustomModelRatio(...) == false`。
- **S2 定价 JSON 写完读回校验**。§5.4 已有"解析失败直接拒绝执行" ✅；建议再加一条读回比对
  （键集合不变 + 值在合理量级），因为这类 JSON 写坏的唯一表现是"模型价格全变 0 或天价"。
- **S3 顺手清掉死代码**。`setting/operation_setting/general_setting.go:77 GetUsdToCurrencyRate`
  已经**没有任何调用者**（只有定义），可在 B8 一起删，避免以后有人误用。
- **S4 测试补充**：
  - F2 的 `normalizeLogPricing` 要覆盖 **V1 期日志**：V1 没有换比率语义，`other.model_ratio` 仍是
    美元口径，而 `logs.quota` 已是 ×14.6 后的值 → 断言"计费过程合计 == 花费列"必须同时覆盖 V0 / V1
    两代历史，别把 V1 期当成"已是人民币"；
  - §5.0 的 41% 低价日志（quota 2–100）在 ×100 后是 200–10,000，建议把"迁移后 quota 分布"也打进
    报告，便于与迁移前分布直接对比；
  - R1 提到的 `.5` 舍入边界。

---

## 四、已核实无误（可直接复用，避免重复劳动）

| 项 | 结论 |
|---|---|
| §2.1 / §2.2 常量与公式（含 E1 / E2 / E5 / E6 / E7） | ✅ 按公式重算全部一致；E1 = V1 样例 ×100 |
| §2.3 E1 与 V1 §2.3 对照 | ✅ 16,583 → 1,658,300，人民币相同 |
| §3.1 `QuotaPerUnit` / `QuotaScale()` 消费点全量清单 | ✅ 与全量 grep 完全吻合，无遗漏（见附录） |
| §3.1 OpenRouter 缓存反推 | ✅ `ratio/5×10⁵`（美元/token）× 汇率 与 `ratio_new/10⁶`（人民币/token）数值等价，确实需要读汇率 |
| §3.2 默认倍率表"整表 × 2 × USD2RMB" | ✅ 表内 `* RMB` 写法只是把人民币价写进同一单位（`0.120 * RMB` = ¥120/百万），可统一换算 |
| §3.2 `usdToCNY` 与 `ratio_setting.USD2RMB` 重复 | ✅ 受导入方向所迫（`ratio_setting` → `operation_setting`），无法共用，可接受 |
| §3.4 支付路径"仅随 QPU 变数值" | ✅ topup / alipay / wxpay / package / stripe / waffo / pancake / bank_transfer 逐个看过：先乘后除 + 最后一次取整 |
| §3.7 B6 删除 ratio_sync | ✅ 后端引用只有 `router/api-router.go:358-362` 与文件自身；无其它调用者、定时器、数据表 |
| §3.7 B7 删除 `/api/ratio_config` | ✅ 引用点恰好是文档列出的那 5 处（路由 / controller / expose_ratio / option.go / 两个前端文件） |
| §6.1 CUSTOM 展示系数 `custom_rate / usd_exchange_rate` | ✅ 与现状语义等价（CUSTOM 的 rate 是"1 USD = X 自定义币"），显示值不变 |
| §5.2 options ×100 / §5.3 ×14.6、×7.3、`-1` 哨兵 | ✅ 与 V1 清单一致；`checkin` 1 / 29 → 100 / 2,900 |
| §5.4 JS 安全整数（2⁵³ ≈ ¥9,007 万） | ✅ 现网最大值 ×100 后离 2⁵³ 还有 3,000 倍余量 |
| B4 `ParseExprVersion` 可行性 | ✅ 现有实现只认 `v1:`、其余按 v1，加 `v2:` 前缀可行（`pkg/billingexpr/compile.go:22`） |
| §5.0 快照与 V1 实测一致性 | ✅ QPU 7,300,000 / 迁移 done / 游标 851,506 / ~87 秒，与 V1 dev-report §12 的实测（logs 段 84.6 秒）吻合 |

### 附：文档里"待核实"的问题已有答案

§3.1 让开发时核实 `service/entitlement_cost.go` 的 `modelUnitPrice` 口径——**已核实**：
它只返回**按次单价**或**视频 per_call 矩阵最贵格**（`p.ModelPrice`、`p.VideoPricing.PerCall[...]`），
倍率类模型直接返回 `EntitlementCostReasonPerToken`（不支持）；**不含任何 `× 2`**。
所以 `unitPrice × QuotaPerYuan × cost_ratio` 正确，配置迁完后自然就是人民币，
只需同步 `service/entitlement_cost.go:182` 那句"美元口径"注释。

---

## 五、附录：本次核对方式

```bash
# 1) 后端消费点全量清单（比对 §3.1 / §3.4 / §3.5）
grep -rn "QuotaPerUnit" --include=*.go . | grep -v _test
grep -rn "QuotaScale()\|RatioQuotaBase" --include=*.go .
grep -rn "USDExchangeRate" --include=*.go . | grep -v _test

# 2) 删除项的安全性（B6 / B7）
grep -rn "ratio_sync\|RatioSync\|FetchUpstreamRatios\|pricingSyncFields" .
grep -rn "ExposeRatio\|ratio_config\|GetRatioConfig" .

# 3) 定价字段写入点是否齐全（§3.6）
grep -rn 'other\["' --include=*.go service/ | grep -E "price|ratio|amount|cost"

# 4) 表达式版本与换算
sed -n '1,20p' pkg/billingexpr/settle.go        # quotaConversion 只有 v1
sed -n '14,30p' pkg/billingexpr/compile.go      # ParseExprVersion
```

关键位置速查：

| 事项 | 位置 |
|---|---|
| v1 表达式换算（R2） | `pkg/billingexpr/settle.go` `quotaConversion` |
| v1 快照字段（R2） | `pkg/billingexpr/types.go` `BillingSnapshot.QuotaPerUnit / ExprVersion` |
| extraContent 文案（R4） | `service/text_quota.go:357` 起 5 条 |
| 任务定价字段（R3） | `model/task.go` `TaskBillingContext` / `TaskVideoBilling`（`:412` 注释写的是"美元"） |
| 默认价是否被改过的判定（S1） | `service/quota.go:42` `hasCustomModelRatio` |
| 默认表换算点（§3.2） | `setting/ratio_setting/model_ratio.go` `InitRatioSettings` / `GetDefaultModelRatioMap` / `DefaultModelRatio2JSONString` |
| 死代码（S3） | `setting/operation_setting/general_setting.go:77` `GetUsdToCurrencyRate` |
| 预扣语义（§10.3） | `relay/helper/price.go` `ModelPriceHelperPerCall` 的 `modelRatio / 2 × QuotaPerUnit` |
