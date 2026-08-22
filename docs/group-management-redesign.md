# 分组管理改造设计（方案 C）

> 状态：P0 / P1 / P2 / P3 已落地（commit 9f739cf45、a53874ed9 及本次）
> 关联：`docs/group-model-isolation.md`（分组 × 模型 隔离，本次的业务前提）、`pkg/billingexpr/expr.md`

## 1. 问题

### 1.1 折扣只能整组一个数（本次真正要解的）

`GroupRatio` 是 `map[分组名]float64`，整组一个标量，对所有模型一视同仁。

但业务上根本不是这样：`default` 挂自建 GPUStack、`premium` 挂并行科技中转，两条供应链的**成本结构逐模型不同**——不是「premium 整体贵 1.5 倍」，而是「premium 的 GLM-5 贵、但 premium 的 wan2.2 反而便宜」。现在只能取一个折中的整组倍率；逐模型的价差只能靠改 `ModelRatio`，而 `ModelRatio` 是全局的、所有分组共享，一改就把另一条供应链的价也改了。

现有维度覆盖：

| 想表达 | 现在能否 |
|---|---|
| premium 分组整体贵 1.5 倍 | ✅ `GroupRatio` |
| vip 用户用 premium 时打 7 折 | ✅ `GroupGroupRatio` |
| premium 分组下 GLM-5 单独定价 | ❌ |
| premium 分组下 `wan2.2-*` 全线 8 折 | ❌ |

### 1.2 配置项散落在五个页面

| 配置项 | option key | 现在在哪 |
|---|---|---|
| 分组倍率 | `GroupRatio` | 系统设置 → 分组与模型定价 |
| 用户可选分组 + 描述 | `UserUsableGroups` | 同上 |
| 分组特殊倍率 | `GroupGroupRatio` | 同上 |
| 分组特殊可用分组 | `group_ratio_setting.group_special_usable_group` | 同上 |
| 自动分组 | `AutoGroups` / `DefaultUseAutoGroup` | 同上 |
| 充值分组倍率 | `TopupGroupRatio` | 系统设置 → 支付设置 |
| 分组请求速率限制 | `ModelRequestRateLimitGroup` | 系统设置 → 速率限制 |
| ~~分组审核策略~~ | `moderation.group_policies` | 后端有字段，**前端从未实现**（见 §7.1） |
| 积分抵扣分组白名单 | `points_setting.enabled_groups` | 系统设置 → 运营设置 |

新建一个分组要在五个页面之间来回跳，没有任何地方能一眼看全「premium 这个分组到底是什么配置」。

## 2. 方案取舍

这是两个风险完全不对称的需求：

| 需求 | 风险来源 | 必要改动 |
|---|---|---|
| §1.1 每模型折扣 | **计费链路** | 一个新配置 + 5 个接入点 |
| §1.2 配置聚合 | **纯 UI** | 前端 Section 搬位置 |

曾评估过「新建 `groups` / `group_ratio_rules` / `group_usable_rules` 三张表 + 一次性迁移 + 快照缓存 + `common`/`setting`/`model` 包依赖重排」的全量重构（方案 A，~3000 行）。**放弃**，理由是那部分改动量既不服务 §1.1 也不服务 §1.2，只服务「存储也一起换掉」这个没人提出的目标，却把风险面从「一个新配置项」放大到「全站分组配置的迁移正确性」。

方案 C 保留 A 的**全部解析语义**（§3 一个字不改），只是第三层换个存放位置：不建表、不迁移、不加缓存层、不动包依赖。

C 相比 A 唯一的实质损失是「多管理员同时编辑同一个 option 会互相覆盖」——单人运维的站点不存在这个问题。

### 2.1 已确认决策（2026-08-22）

1. **存储**：沿用 option JSON，新增一个 key，不建表
2. **折扣语义**：`multiply`（叠加）与 `override`（覆盖）两种模式，**按条选**
3. **聚合范围**：分组倍率四件套 + 自动分组 + `TopupGroupRatio` + `ModelRequestRateLimitGroup` + `points_setting.enabled_groups`，**聚合只搬 UI，不搬存储**（原定还包含 `moderation.group_policies`，落地时发现前端从未实现它，见 §7.1）
4. **前端**：管理页只做 classic（与 `group-model-isolation.md` 记录的「default 适配延后」约定一致）；但**价格展示三个主题都要改**（§6）
5. **模型名匹配**：精确 + 前缀通配，**不复用** `GetModelRatio` 的模糊匹配

## 3. 倍率解析模型（核心）

### 3.1 两层解析

```
Layer 0  base  = GroupRatio[X]                    分组基础倍率
Layer 1  base ← GroupGroupRatio[U][X]             身份折扣（现有，语义不变）
Layer 2  final ← GroupModelRatio[X][M]            ★ 本次新增
```

输入 `(userGroup U, usingGroup X, model M)`，输出 `final`：

```go
base := GetGroupRatio(X)

// Layer 1：现有 GroupGroupRatio，命中即覆盖（保持现有运行时语义）
if v, ok := GetGroupGroupRatio(U, X); ok {
    base = v
}

// Layer 2：新增，按模型特异性取最具体的一条
if r := pickModelRule(X, M); r != nil {
    if r.Mode == "override" { return r.Value }   // 精确定价，无视 base
    return base * r.Value                         // 折扣，叠在 base 上
}
return base
```

**模型特异性排序**（降序取第一条，同级按模式串长度降序，保证 `wan2.2-t2v-*` 胜过 `wan2.2-*`）：

| 模式串 | 权重 |
|---|---|
| `GLM-5`（精确） | 2 |
| `wan2.2-*`（前缀通配） | 1 |
| 未命中 | — |

通配只支持**前缀 `*`**，不引入正则——与 `setting/system_setting/moderation.go` 的 `ModelFilter.Match` 保持同一约定（正则写错不报错、只静默算错价，是这类系统最不该出的错）。匹配对象是 `OriginModelName`，**不复用** `ratio_setting` 的模糊匹配（`compact_suffix.go` 那套），因为两套语义叠加后通配行为不可预测。

### 3.2 为什么分两层，而不是「取最具体的一条」

假设配了：

- `GroupGroupRatio: {vip: {premium: 0.7}}` — vip 用户在 premium 全线 7 折
- `GroupModelRatio: {premium: {GLM-5: ×0.5}}` — GLM-5 半价促销

若把两者拍平成一个规则集「取最具体的一条」，模型精确 > 分组级，会命中第二条 → `1.5 × 0.5 = 0.75`，**vip 身份被静默丢掉，vip 反而比预期贵**。

分两层则：`base = 0.7` → `final = 0.7 × 0.5 = 0.35`。身份折扣与促销折扣正交叠加，这才是运营的心智模型。

这条是整个设计里唯一「想当然会写错」的地方，实现时必须有对应单测。

### 3.3 两种模式的定位

| 模式 | 语义 | 用在哪 |
|---|---|---|
| `multiply` | `final = base × value` | **促销折扣**。改分组基础倍率时，所有模型的相对优惠自动跟随 |
| `override` | `final = value` | **精确定价**。该分组该模型就是这个价，与分组基础倍率、与身份折扣全部脱钩 |

供应链价差（premium 的 GLM-5 成本就是高）用 `override`；限时促销、VIP 优惠用 `multiply`。UI 上每条一个下拉，默认 `multiply`。

`override` 的风险是改分组基础倍率时它不跟随、容易漏改，且会**吃掉 Layer 1 的身份折扣**。管理页在含 `override` 规则的分组上给角标提示，试算器（§7.3）里明示是哪条规则拍的板。

## 4. 存储：一个新 option key

### 4.1 数据结构

新增顶层 option key `GroupModelRatio`，与现有 `GroupGroupRatio` 同形状（前端编辑器可照 `GroupGroupRatioRules.jsx` 的模式写）：

```json
{
  "premium": {
    "GLM-5":      { "mode": "override", "value": 2.2 },
    "wan2.2-*":   { "mode": "multiply", "value": 0.8 }
  },
  "default": {
    "gpt-4o-mini": { "mode": "multiply", "value": 0.5 }
  }
}
```

Go 侧：

```go
// setting/ratio_setting/group_ratio.go
type ModelRatioRule struct {
    Mode   string  `json:"mode"`             // multiply | override，空值按 multiply
    Value  float64 `json:"value"`
    Remark string  `json:"remark,omitempty"` // 运营备注：「并行科技成本高」
}

var groupModelRatioMap = types.NewRWMap[string, map[string]ModelRatioRule]()
```

沿用 `types.RWMap` + `types.LoadFromJsonString`，与 `groupGroupRatioMap` 完全一致的读写方式，热更新、多节点同步（`model.SyncOptions` 轮询）全部复用现成机制，**不引入任何新的同步路径**。

用顶层 key 而非塞进 `group_ratio_setting.*`：`group_ratio_setting` 走的是 `config.GlobalConfig` 的反射式扁平化（`setting/config/config.go`），嵌套 map 的序列化路径更绕；`GroupGroupRatio` 这个先例已经证明顶层 key 够用。

`Option.Value` 列在三库分别是 `longtext` / `text` / `text`（GORM 对无 `size` 标注的 string 的默认映射），现有 `ModelRatio` 已存着同量级 JSON，容量不是问题。

### 4.2 为什么值是对象而不是裸数字

`{"GLM-5": 0.5}` 更省事，但 `mode` 必须逐条可选（决策 2），而且 `remark` 在运营上是刚需——半年后没人记得 premium 的 GLM-5 为什么是 2.2。

解析时对裸数字做兼容（`json.RawMessage` 判类型，数字按 `multiply` 处理），这样手工编辑 JSON 的人少踩一个坑。

### 4.3 无迁移

存量配置一律不动。`GroupModelRatio` 缺省为空 `{}`，空表示「Layer 2 全部未命中」，解析结果与改造前**逐位相同**。这是本方案最重要的性质：**P0 上线时行为零变化**，不需要迁移自检、不需要回滚预案。

## 5. 计费链路接入点

### 5.1 五个点

真正需要「模型感知」的只有现在调 `GetGroupRatio` / `GetGroupGroupRatio` 的位置：

| 位置 | 现状 | 改法 | 模型名 |
|---|---|---|---|
| `relay/helper/price.go:39 HandleGroupRatio` | 主链路，产出 `GroupRatioInfo` | 改调 `ResolveGroupRatio` | `relayInfo.OriginModelName` ✅ |
| `service/quota.go:110` | Realtime/WSS 预扣 | 同上 | `relayInfo.OriginModelName` ✅ |
| `controller/task_video.go:171` | 视频任务预估 | 同上 | 上下文有 ✅ |
| `service/task_billing.go:518 taskGroupRatio` | 任务结算 | **改冻结**，见 §5.3 ⚠️ | `taskModelName(task)` ✅ |
| `controller/pricing.go:42-66` | 模型广场价格 | 见 §6 | 逐模型遍历 ✅ |

新访问器（`setting/ratio_setting`，无新包、无依赖变化）：

```go
type RatioResolution struct {
    Final     float64
    Base      float64  // Layer 0/1 之后
    RuleMatch string   // 命中的模型模式串，"" = 未命中
    RuleMode  string
    RuleValue float64
}

func ResolveGroupRatio(userGroup, usingGroup, modelName string) RatioResolution
```

返回整条链而非一个数——日志可解释性（§5.4）、价格展示（§6）、试算器（§7.3）全靠它。

`HandleGroupRatio` 里必须**先处理 `auto_group` 覆盖 `relayInfo.UsingGroup`，再解析**（现有代码已是这个顺序，保持即可），否则 auto 令牌会按 `auto` 这个伪分组名去查模型规则，永远查不到。

### 5.2 对其他计费模式的覆盖是免费的

`HandleGroupRatio` 是整条计费链路**唯一**的分组倍率产出点，下游（文本 / 图像 / 音频 / 视频 / MJ / Task / `pkg/billingexpr` 分段计费）一律读 `PriceData.GroupRatioInfo.GroupRatio` 这一个标量。

因此模型级折扣天然对所有计费模式生效，**包括分段计费**——`relay/helper/price.go:274` 的 `quotaBeforeGroup × groupRatio` 拿到的就是解析后的 `final`，`pkg/billingexpr` 一行不用改。

### 5.3 ⚠️ 任务结算的冻结（最大风险点）

任务结算发生在提交后几百秒。`service/task_billing.go:564-570` 的注释写明了必须用**提交时冻结**的分组倍率，理由三条：跨分组信息会丢、期间管理员改配置会前后两价、日志反算要自洽。

这三条对模型级折扣**同样成立且更严重**——促销规则的改动频率天然高于分组基础倍率。

但现状是两条路径不一致：

| 路径 | 用的是 |
|---|---|
| `RecalculateTaskQuotaByVideoMatrix:576` | `bc.GroupRatio` 冻结值 ✅ |
| `RecalculateTaskQuotaByTokens:479` → `taskGroupRatio()` | **结算时重新解析** ❌ |

`TaskBillingContext.GroupRatio` 字段已经存在、提交时也已经在写。所以修法很小：

```go
// taskGroupRatio：优先用提交时冻结的值，只有老任务（无 BillingContext）才回退重解析
func taskGroupRatio(task *model.Task) (float64, bool) {
    if bc := task.PrivateData.BillingContext; bc != nil && bc.GroupRatio > 0 {
        return bc.GroupRatio, true
    }
    ... // 现有回退逻辑，额外传入 taskModelName(task) 走 ResolveGroupRatio
}
```

这一改**顺带修掉了一个既有缺陷**：现在同一个任务走 token 重算还是走视频矩阵，用的倍率来源不同，管理员在任务执行期间改了倍率就会得到两个价。

另有一个存量陷阱：现有回退逻辑调的是 `GetGroupGroupRatio(group, group)`——把使用分组同时当用户分组传。旧语义下无害（查不到就退回基础倍率），改造时照搬到新解析器要显式确认行为不变。

**验收要求**：本节的改动必须做回退验证——把实现改回旧逻辑，对应测试必须见红。绿灯不证明任何事。

### 5.4 日志与可审计性

`types.GroupRatioInfo` 扩三个字段（不动现有三个，存量日志不受影响）：

```go
type GroupRatioInfo struct {
    GroupRatio        float64  // 最终值。下游计费只读这个，语义不变
    GroupSpecialRatio float64  // 保留
    HasSpecialRatio   bool     // 保留

    BaseRatio      float64     // 新增：Layer 0/1 之后的基准
    ModelRuleMatch string      // 新增：命中的模式串，如 "wan2.2-*"，"" = 未命中
    ModelRuleMode  string      // 新增
    ModelRuleValue float64     // 新增
}
```

日志 `other` 增加 `group_base_ratio` 与 `group_model_rule`（形如 `"wan2.2-*:×0.8"`）。

`other.group_ratio` 继续写**最终值**——运营按日志上这个数反算金额必须永远自洽，这条不能破。存量日志无新字段，前端缺字段时退回老展示，不报错。

## 6. 价格展示一致性（必须与 P0 一起上）

`controller/pricing.go` 的 `GetPricing` 返回扁平 `group_ratio: {分组: 倍率}`，前端据此算展示价。**加了模型级折扣后这个 map 不再充分**，不处理会给用户看错价——这是用户可见的错误，比后端算错更难挽回信任。

响应增加一个稀疏 map，只含命中模型规则的组合，且**值是后端算好的最终倍率**（已含 Layer 0/1/2），前端只做查表：

```json
{
  "group_ratio":       { "default": 1, "premium": 1.5 },
  "group_model_ratio": { "premium": { "GLM-5": 2.2, "wan2.2-t2v": 1.2 } }
}
```

前端取值统一为 `group_model_ratio[g]?.[m] ?? group_ratio[g]`。

注意后端要**展开通配**：响应里给的是具体模型名，不是 `wan2.2-*`，前端不实现通配匹配（三个主题实现三遍通配 = 三份可能算错的价）。

需改的展示点：

- **classic**：`hooks/model-pricing/useModelPricingData.jsx`、`model-pricing/modal/components/ModelPricingTable.jsx:74`、`model-pricing/filter/PricingGroups.jsx:57`
- **default**：`features/pricing/lib/price.ts`、`dynamic-price.ts`
- **mobile**：`pages/Models.jsx`

`PricingGroups.jsx` 的分组筛选器现在在分组名旁挂「×1.5」倍率标签——分组内倍率不再唯一后改为「基础倍率 + 部分模型专属价」角标。`controller/group.go:35` 的 `GetUserGroups`（令牌页分组下拉的倍率标签）同样加这个标记位。

**倍率标签一共有四处，不是两处**——模型广场分组筛选器、模型详情的分组价格表、令牌列表的分组列、令牌创建/操练场的分组下拉。加了「有效倍率」这个概念之后，容易只排查「算价」的路径而漏掉「展示倍率数字」的路径：后两处走的是 `/api/user/self/groups`，与 `/api/pricing` 完全是两条链。

**标签措辞必须方向中立。** 曾经标成「1.5x 起」/`x1.5+`，是错的：`multiply < 1` 的折扣会让实际倍率落在基础倍率**下方**，而「起」「+」都是下界断言，在最常见的打折场景下方向正好相反。`override` 更是可以落在任意一侧。现用「基准」/`base`——只说明这个数是什么，不断言实际值在哪一侧。`setting/ratio_setting` 的 `TestResolveGroupRatio_DiscountGoesBelowBase` 钉的就是这个方向事实，是该措辞约束的依据。

## 7. 管理端（classic）

### 7.0 交互主线

#### 现状：「创建分组」这个动作没有归属地

一个分组名今天同时诞生在三个互不相干的地方：

| 位置 | 怎么产生 | 校验 |
|---|---|---|
| 渠道编辑 → 分组 | `Form.Select` 开了 `allowAdditions`，**能直接打一个新名字进去** | 无 |
| `GroupRatio` 的 key | 分组表格里加一行 | 无 |
| `UserUsableGroups` 的 key | 同一张表格勾「用户可选」 | 无 |

渠道页的 `additionLabel` 写着「请在系统设置页面编辑分组倍率以添加新的分组」——UI 自己知道这是个坑，但只能靠一句提示。三处不一致的后果在 `docs/group-model-isolation.md` 已有记录，两个方向都会静默失败：

- **渠道有、配置无**：渠道挂了 `volcano`，但 `GroupRatio` 里没有 → `middleware/auth.go:412` 判「分组已被弃用」，渠道成了死渠道，管理员在任何页面都看不到异常
- **配置有、渠道无**：`GroupRatio` 配了 `volcano` 但没渠道挂载 → 用户能选中，一调就「无可用渠道」

本次把分组管理页确立为**分组唯一的出生地**，并把上面两种失配变成页面上看得见的状态。

#### 主线：接第三家供应商（火山引擎）

**① 分组管理 → 新建分组**

填分组名 `volcano`、描述「火山引擎直连」、基础倍率、勾「用户可选」。带重名与命名规范校验（现在一个都没有）。

建完后该行立刻显示 🔴 **`无渠道挂载 — 用户选中会报「无可用渠道」`**，旁边一个「去挂渠道」按钮。这一步刻意不让人误以为"建完分组就完事了"。

**② 渠道管理 → 新建/编辑渠道 → 分组选 `volcano`**

从上一步的按钮跳过去。回到分组管理，🔴 自动变成 🟢 `3 个渠道 · 覆盖 47 个模型`。

配套改动：渠道页分组下拉**关掉 `allowAdditions`**（分组有归属地之后，渠道页不该再能凭空造分组），下拉选项带上倍率标签，让挂渠道的人知道这个分组是什么价位。

**③ 分组管理 → 模型折扣 Tab**

关键约束：**模型下拉只列该分组实际有渠道覆盖的模型**，不是全站模型。给一个本分组根本没有的模型配折扣是纯废配置，不该让人配得出来。数据源是 `abilities` 表按 `group` 过滤，`(group, model, channel_id, enabled)` 一条聚合查询。

逐个或批量设 `override` / `multiply`。

**④ 同页其余 Tab，按需**

充值倍率、限流、积分白名单——都有合理缺省，不配也能跑，UI 上标「可选」，不阻塞主线。

**⑤ 跨分组规则 Tab**

「哪些用户分组能看到 volcano」（可用性）、「vip 用 volcano 打几折」（身份折扣）。

**⑥ 试算器验证**

输入 `(default 用户, volcano 令牌, doubao-pro)` 看完整解析链。这是"敢不敢上线"的最后一关，尤其在配过 `override` 之后——它会吃掉身份折扣（§3.3），只有试算器能让人一眼看见。

#### 反向入口：先有渠道后有分组

现实里管理员常常是先拿到渠道。这条路也得走得通：

分组管理页顶部常驻一个**失配提示条**，扫 `abilities` 里出现过、但 `GroupRatio` 里没有的分组名：

```
⚠ 检测到 2 个分组被渠道引用但未配置：parallel（4 个渠道）、volcano（1 个渠道）
   这些渠道当前不可用 —— [一键补建]
```

「一键补建」按缺省值（倍率 1、不勾用户可选）建好，管理员再去调。这同时解决存量数据的对齐——现网大概率已经存在这类失配，现在没有任何地方能发现。

#### 分组列表的健康状态列

每行一个健康标记，纯读现有数据（`abilities` + 三个 option JSON），无新存储：

| 标记 | 含义 |
|---|---|
| 🔴 无渠道挂载 | 用户选中必报「无可用渠道」 |
| 🟡 无人可选 | 既没勾「用户可选」，也没有任何用户属于该分组，且不在任何可用性规则里 → 死配置 |
| 🟡 废折扣规则 | 配了模型折扣，但该模型在本分组无渠道覆盖 |
| 🟢 正常 | — |

这几个检查是本次交互设计里**性价比最高的部分**：不新增任何存储和接口，却把现在只能靠用户报错反推的失配，变成管理员打开页面就看见。

#### 删除分组

现在从 JSON 删一行没有任何检查，而分组被 `user.group` / `token.group` / `channel.group` / `subscription_plan.upgrade_group` 四处引用。删除前先统计这四类引用数，有引用就列明细并要求二次确认。

（真正的"停用而非删除"需要分组状态位，属于升表范围，见 §9.3。）

### 7.1 聚合：只搬 UI，不搬存储

新增一级页面 **`分组管理`**。原「系统设置 → 分组与模型定价设置」留下模型定价部分，改名「模型定价设置」。

搬过来的 Section **继续读写各自原本的 option key**，后端零改动：

```
分组管理
├─ 分组列表        GroupRatio + UserUsableGroups        （现 GroupTable.jsx，原样搬）
├─ 模型折扣    ★  GroupModelRatio                       （新建，见 §7.2）
├─ 自动分组        AutoGroups + DefaultUseAutoGroup      （现 AutoGroupList.jsx，原样搬）
├─ 跨分组规则      GroupGroupRatio + group_special_usable_group
│                                                       （现两个组件合并成一个 Tab）
├─ 充值倍率        TopupGroupRatio                       （从支付设置页搬）
├─ 速率限制        ModelRequestRateLimitGroup            （从速率限制页搬）
├─ 积分白名单      points_setting.enabled_groups         （从运营设置页搬）
└─ 倍率试算器  ★  纯前端计算，见 §7.3
```

保存仍走现有的 `PUT /api/option/`（`GroupRatioSettings.jsx` 已有的 `compareObjects` + 批量 PUT 模式），**不新增任何管理接口**。

原页面留跳转链接。

**审核策略没有搬——因为它不存在。** 决策时把 `moderation.group_policies` 列进了聚合范围，但落地时才发现前端从来没有实现过这个配置项：内容审核第一期的提交（`c82c07881`）明确写了「policies / group_policies / endpoints 未纳入——它们服务于 L1 及以上的远程分类器，第一期没有消费方，做出来只会是没人填的空壳」。这里不去补那个空壳：真要做，它属于内容审核的 L1 期，而不是分组管理的搬家工作。

### 7.2 模型折扣编辑器（本次核心 UI）

站点模型数三位数，不能做成一个大表单。但因为数据是一次性全量拉到前端的（option JSON），分页、搜索、筛选全部在**前端内存里做**，不需要服务端分页接口。

- 分组选择器（Tab 或下拉）→ 只编辑当前分组的规则
- 模型下拉从 `/api/pricing`（或已有的模型列表接口）取，避免手打模型名打错
- 每行：模型名 · 模式下拉 · 值 · 备注 · **折算后实际倍率**（前端实时算，含 Layer 0/1）
- **批量操作**：多选模型 → 统一设模式和值（「选中的 12 个全部 ×0.8」）
- **通配规则**单独一块列在精确规则上方，视觉区分——通配影响面大，混在长列表里会被漏看
- 含 `override` 规则的分组给角标（§3.3）

### 7.3 倍率试算器

两层叠加 + 通配的可解释性必须有工具兜底，否则运营改完价不敢上线。

数据全在前端（`GroupRatio` / `GroupGroupRatio` / `GroupModelRatio` 三个 JSON 都已加载），**纯前端实现，无需接口**。输入 `(用户分组, 令牌分组, 模型名)`，输出整条链：

```
用户分组 vip · 令牌分组 premium · 模型 GLM-5

Layer 0  分组基础倍率                             1.50
Layer 1  GroupGroupRatio[vip][premium]  覆盖      0.70   ← 命中
Layer 2  GroupModelRatio[premium][GLM-5] 覆盖     2.20   ← 命中（无视 Layer 1）
──────────────────────────────────────────────────────
最终倍率                                          2.20
```

未命中的层显式标「未命中」，不留白。

**前端解析逻辑必须与后端 `ResolveGroupRatio` 逐位一致**，两处实现不同会让试算器变成误导源。实现时前端解析函数单独成文件、注释指向本节，改后端时一并改。

### 7.4 web/default 不做管理页，也不需要防误写

方案 A 里 default 主题的三个分组编辑器（`group-ratio-form.tsx` 等）会因真值源迁移而静默失效，需要专门改成只读。

**方案 C 下这个问题不存在**：option key 仍是唯一真值源，default 的编辑器继续正常工作。它只是**没有** `GroupModelRatio` 的编辑入口——是功能缺失，不是数据丢失，符合「default 适配延后」的既有约定。

## 8. 分期

| 期 | 内容 | 验收 |
|---|---|---|
| **P0** | `GroupModelRatio` + `ResolveGroupRatio` 两层解析 + 4 个计费接入点 + `GroupRatioInfo` 扩展 + 日志字段 + §5.3 冻结修正 | 两层解析单测（尤其 §3.2 的叠加用例）；`taskGroupRatio` 冻结的**回退验证**；空配置下所有既有计费测试逐位不变 |
| **P1** | 价格展示：pricing 接口下发 `group_model_ratio` + classic/default/mobile 三处读取 | 模型广场显示价 == 实际扣费 |
| **P2** | classic 分组管理页：新建分组 + 健康状态列 + 失配提示条 + 折扣编辑器 + 试算器 + 删除引用检查；渠道页关 `allowAdditions` | 手工验收：走一遍 §7.0 主线 ①→⑥ |
| **P3** | 配置聚合：五个页面的 Section 搬家 + 原页跳转链接 | 手工验收 |

P0 + P1 是一个**必须一起上线**的整体（后端能算新价、前端就得能显示新价）。P2 之前可以先手工编辑 JSON 验证 P0 是否算对。

P3 是纯 UI 搬家、零后端风险，随时可以停——前面三期已经把能力交付完了。

**落地情况**：四期均已完成（`9f739cf45` / `a53874ed9` / `893588a59`），另补了一次前端测试基建（`4f497ef63`）——classic 此前没有任何测试脚本与测试文件，「光标跳不跳」这类判断只能靠人眼。

## 9. 已知限制

1. **并发编辑互相覆盖**：option 是全量字符串覆盖保存，两个管理员同时改分组配置会互相盖掉。单人运维不触发；真出现多人运维时按 §10 升表
2. **无分组停用态**：只能删不能停售。要「停售但保留配置」需要分组状态位，属于升表范围（§10）
3. **健康检查是快照式的**：§7.0 的失配提示依赖 `abilities` 表与 pricing 缓存（1 分钟刷新），刚改完渠道挂载的短时窗口内可能显示滞后
4. ~~**前后端两份解析实现**~~ —— **已不适用**。试算器最终走 `POST /api/group/resolve`
   而不是在前端复算，模型广场那份 `getEffectiveGroupRatio` 只是查后端算好的终值表，
   不含解析逻辑。全站只有一个解析实现
5. **default / mobile** 只改价格展示，不做管理入口
6. **模型名匹配口径**与 `ratio_setting.GetModelRatio` 的模糊匹配不同（决策 5），运营需要知道「模型定价页的模型名写法」与「分组折扣页的写法」不完全通用
7. **`auto` 会被写进 `GroupRatio`**：它本只存在于 `UserUsableGroups`，但分组表把两者的并集当作行来编辑，保存时会给它写一个倍率。这是改造前那个页面就有的行为，不是本次引入。影响面很小（`middleware/auth.go` 对 auto 有独立分支，计费前 auto 已被替换成真实分组），代价是渠道分组下拉里会多出一个选不得的 `auto`。健康判定已特判为「伪分组」，不再误报红灯
8. **E2E 未跑绿**：`web/classic/e2e/` 的 5 个用例需要管理员账号才能跑，编写时无凭据可用，只验到「浏览器能起、跳过逻辑正确、选择器已逐个核对源码」。首跑需要校准

## 10. 未来可扩展（本次不做）

- **升表**：若 §9.1/9.2/9.3 真的成为痛点，把三个 option key 迁进 `groups` / `group_ratio_rules` 表。因为 §3 的解析语义与存储无关，届时只换 `ResolveGroupRatio` 的取数来源，**计费接入点、日志、前端展示一行不用动**——这是选 C 而非 B（只加折扣不聚合）的主要原因，C 把升级路径留好了
- **三维规则**：`(用户分组, 使用分组, 模型)`，把 `GroupModelRatio` 加一层嵌套即可，`ResolveGroupRatio` 的两层结构已经为此留好形状
- **规则生效时间窗**：促销规则加 `start` / `end`，限时活动到点自动失效
- **分组 × 模型屏蔽**：`mode: "disabled"`，实现「premium 分组下不提供 GLM-5」，比现在只能靠渠道挂载间接控制更直接
