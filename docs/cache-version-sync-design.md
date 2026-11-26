# 多副本缓存秒级同步：Redis 版本号脏标记 —— 设计与开发自测文档

> 基线：`1089f758a`。本文是开发与验收的契约：**改动范围、接口、测试用例都以本文为准**，
> 不要扩大范围，不要换方案。遇到与本文描述不符的代码现状，停下来报告，不要自行改设计。
>
> **分工**：开发方负责 §4 全部改动 + §5.1/§5.2 自测；**§5.3 端到端由验收方执行，开发方不做**。
> 现网数据库是 **PostgreSQL**。

---

## 1. 背景

现网 1 master + 3 slave 挂在同一个 Service 后面，配了 `REDIS_CONN_STRING`（因此
`MemoryCacheEnabled` 被强制打开，`main.go:135-138`），`SYNC_FREQUENCY` 未设置，默认 60 秒。

每个 Pod 的进程内存里有五份"从库里全量加载"的缓存，**唯一的跨 Pod 同步手段是每
`SYNC_FREQUENCY` 秒全量重建一次**（`main.go:158-175`）：

| 轮询 | 数据来源 | 代码 |
| --- | --- | --- |
| `SyncOptions` → `loadOptionsFromDatabase(false)` | `options` 表 | `model/option.go:253,274` |
| `SyncChannelCache` → `InitChannelCache` | `channels` + `abilities` | `model/channel_cache.go:22,97` |
| `SyncChannelModelCostCache` → `InitChannelModelCostCache` | `channel_model_costs` | `model/channel_model_cost.go:54,74` |
| `SyncModelVisibilityCache` → `InitModelVisibilityCache` | `models`（模型元数据） | `model/model_visibility.go:29,76` |
| `SyncModelAliasCache` → `InitModelAliasCache` | `channels`（`model_alias`/`settings` 列） | `model/model_alias.go:95,172` |

另有定价缓存 `GetPricing()` 写死 1 分钟 TTL（`model/pricing.go:101`），它由内存里的倍率 +
库里的 abilities/models/vendors 构建。

写操作只会刷新**处理该请求的那一个 Pod**；其余 Pod 最多滞后 60 秒。负载均衡下，用户看到的是
"时灵时不灵"（请求随机落到新值/旧值 Pod），而不是整齐的延迟。整个代码库目前**没有任何**
跨 Pod 失效通知（无 pub/sub）。

### 为什么不直接把 `SYNC_FREQUENCY` 调小

`SYNC_FREQUENCY` 同时是 Redis 中令牌/用户缓存的 TTL（`common/redis.go:19-21` →
`model/token_cache.go:14`、`model/user_cache.go:89`）。调到 10 秒会让热路径上令牌/用户
缓存的回源查库频率变为 6 倍，并且每个 Pod 每 10 秒全量重建一次 channels/abilities/options
并使定价缓存失效。**本方案不改 `SYNC_FREQUENCY`，也不碰令牌/用户缓存。**

---

## 2. 目标与非目标

**目标**

1. 配置项、渠道相关（渠道/abilities/别名/成本）、模型元数据（可见性）三类改动，在其他 Pod
   上 **约 1～2 秒**生效（默认轮询 1 秒）。
2. 兼容 Redis 3.0（华为云 DCS，见 AGENTS.md Rule 8）。
3. 任何 Redis 故障都**不影响**保存操作成功与否，也不影响请求处理；退化为现状（60 秒兜底）。
4. 不增加数据库压力：只在"确有改动"时才重建，空闲时每 Pod 每秒 1 次 Redis `MGET`。

**非目标（不要做）**

- 不改 `SYNC_FREQUENCY` 默认值，不改 60 秒全量轮询（保留为兜底）。
- 不改令牌/用户 Redis 缓存及其 TTL。
- 不做 Redis pub/sub。
- 不做按 key / 按渠道的增量更新：收到变化就**整类全量重建**（复用现有 `Init*` 函数）。
- 不处理目前本地都不刷新的写路径（例如直接改库、`vendors` 表编辑）——它们维持现状。
- 不改 `GetPricing` 的 1 分钟 TTL（但远端重建后会顺带 `InvalidatePricingCache()`，见 §3.4）。
- 不动前端。

---

## 3. 方案

### 3.1 核心机制

Redis 中为每一类缓存维护一个**单调计数**（版本号），**永不过期**：

| 域（domain） | Redis key | 写方何时 `INCR` | 远端收到变化后重建什么 |
| --- | --- | --- | --- |
| `options` | `cache_version:options` | 配置写库成功后 | `loadOptionsFromDatabase(false)`；`InvalidatePricingCache()` |
| `channels` | `cache_version:channels` | 渠道/abilities/别名/成本写库成功后 | `InitChannelCache()`；`InitModelAliasCache()`；`InitChannelModelCostCache()`；`InvalidatePricingCache()` |
| `model_meta` | `cache_version:model_meta` | 模型元数据写库成功后 | `InitModelVisibilityCache()`；`RefreshPricing()`（与写方 `controller/model_meta.go` 一致，立即重建） |

- **写方**：写库提交成功 → （原有的本地刷新）→ `INCR` 对应 key。
- **读方**：每个 Pod 一个后台 goroutine，每 `CACHE_VERSION_POLL_INTERVAL` 秒（默认 1）
  `MGET` 三个 key，与上次看到的值比较，**不相等**就重建该域，然后记住新值。
- 60 秒全量轮询原样保留，作为兜底（Redis 故障、`INCR` 失败、启动窗口等情况）。

> 别名与成本归到 `channels` 域：别名数据本身就存在 `channels` 表上；成本绝大多数随渠道写一起改。
> 三个 key 而不是一个，是为了不让一次渠道自动禁用触发 options 的全表重放（后者持有
> `pricingWriteMu/pricingApplyMu`，并逐项解析所有 JSON 配置）。

### 3.2 一致性论证（实现时不得破坏这些前提）

1. **`INCR` 必须发生在写库提交之后。** 读方看到新版本时，去库里读一定能读到这次提交。
   绝不能在事务内、GORM hook 里、或提交前 `INCR`——否则读方可能在提交前重建、读到旧数据，
   并把新版本号记为"已处理"，直到 60 秒兜底才纠正。
2. **读方先读版本号，再重建，再记住"重建前读到的那个版本号"。** 重建期间若有新的写入，
   它的 `INCR` 一定产生更大的值，下一拍必然再次重建。不允许在重建之后重新读版本号再记录。
3. **用 `!=` 比较，不用 `>`。** DCS 主备切换（异步复制）可能让计数回退，key 被淘汰/`FLUSHDB`
   会变成不存在（`MGET` 得到 nil，记为 `""`）。任何"不相等"都触发重建，自愈。
4. **重建函数绝不能 `INCR`。** 否则 A 重建 → `INCR` → B 重建 → `INCR` → … 形成无限风暴。
   所有 `Init*` / `loadOptionsFromDatabase` 保持不通知；通知只出现在写路径上（§4.3）。
5. **天然合并（coalescing）。** 两次轮询之间无论 `INCR` 多少次，每个 Pod 每个域最多重建一次。
   因此自动禁用风暴（上游故障时批量禁用渠道/多 key）下，每 Pod 的 `InitChannelCache` 频率
   上限是 1 次/秒。
6. **写方 Pod 自己也会再重建一次**（它也在轮询，看到自己的 `INCR`）。这是有意接受的冗余，
   不做"跳过自己"的优化——那需要判断 `INCR` 返回值是否恰好等于上次所见 +1，容易引入丢更新。
7. **数据库假设**：所有 Pod 读写同一个主库，没有读写分离（代码里没有 `dbresolver`/replica）。
   若未来引入只读副本，副本延迟可能让远端读到旧数据，由 60 秒兜底纠正——在设计里注明即可。

### 3.3 启动与故障

- **启动**：第一次成功的 `MGET` 只记录基线、不重建（启动时各 `Init*` 刚跑过）。
  从启动加载完成到第一次 `MGET` 之间若有改动，会被漏掉——由 60 秒兜底覆盖，可接受。
  （options 在 `InitResources` 里先于 Redis 初始化加载，无法把基线读取挪到加载之前。）
- **Redis 未启用**（未配 `REDIS_CONN_STRING`）：不启动轮询，`NotifyCacheChanged` 为空操作。
- **`CACHE_VERSION_POLL_INTERVAL=0`**：不启动轮询（应急开关）。写方仍然 `INCR`（便宜，且让其他开着轮询的 Pod 受益）。
- **`MGET` 失败**：不重建、不更新已见值；只在"正常 → 失败"时打一条错误日志、"失败 → 恢复"时
  打一条恢复日志（不要每秒刷屏）。恢复后照常比较，期间的改动在恢复后第一拍被发现。
- **`INCR` 失败**：只记 `SysError`，**不向调用方返回错误**，保存照常成功；该次改动退化为 60 秒兜底。
  `INCR` 使用 **500ms** 超时的 context（常量 `cacheVersionNotifyTimeout`，所有调用点共用），
  避免 Redis 卡住拖慢保存或 relay 热路径（自动禁用在失败请求的路径上）。同步调用，不做异步。
- **`CACHE_VERSION_POLL_INTERVAL` 从 0 恢复为非 0 时**：关闭期间写方仍在 `INCR`，恢复后各 Pod
  第一次成功 poll 只记基线（新进程启动）——**不会**补重建；60 秒兜底已经覆盖关闭期间的改动。
  若以后改成运行时热切换，第一拍会对所有变化过的域重建一次并打出一批 `cache version changed`
  日志，属预期，不是异常。
- **重建 panic**：每个域的重建单独 `recover`，记日志后继续处理其他域与后续轮询；
  goroutine 里未恢复的 panic 会杀掉整个进程，必须防住。
- **重建失败**：现有 `Init*` 大多不返回错误（吞掉或只记日志），因此无论成败都推进已见值；
  失败由 60 秒兜底纠正。不要为此修改 `Init*` 的签名。

### 3.4 为什么远端重建后要 `InvalidatePricingCache()`

定价缓存由"内存倍率 + 库里的 abilities/models/vendors"构建，有 1 分钟 TTL。三个域任一变化都可能
影响定价（倍率/计费配置、渠道分组与 abilities、模型元数据）。远端重建后让定价失效（惰性重建，
下一次 `GetPricing()` 才查库），使模型广场/定价接口也秒级跟上。
注意锁序：`InvalidatePricingCache()` 必须在 `loadOptionsFromDatabase` **返回之后**调用，
不能在持有 `OptionMapRWMutex` 时调用（见 `model/pricing.go:102-105` 注释）。

**边界（验收时不要误判为缺陷）**：`InvalidatePricingCache()` 只是置空、惰性重建。依赖
`updatePricing()` 的派生表——尤其 `GetModelSupportEndpointTypes`（`model/pricing.go:366` 附近
构建，`/v1/models`、`service/ovcaps` 能力目录在读）——在 options / channels 域变化后，要等
下一次 `GetPricing()` 被调用（`/api/pricing`、模型广场等）才更新。这与写方 Pod 现状一致，
不在本次范围。model_meta 域远端改用 `RefreshPricing()` 立即重建，与写方行为对齐。

### 3.5 开销

- 空闲：每 Pod 每秒 1 次 `MGET`（3 个 key），4 副本合计 4 ops/s。
- 每次改动：每个 Pod 额外一次该域全量重建（与 60 秒轮询的一拍等价）；写方 Pod 两次。
- 数据库：空闲时零额外查询。
- **风暴场景不是零成本**：上游故障引发批量自动禁用时，每 Pod 每秒最多一次 channels 域重建
  （`channels`、`abilities` 两次全表读 + 别名的全渠道扫描 + 成本表全读）。这是有意接受的上限
  （§3.2-5），换取禁用秒级扩散。

### 3.6 Redis key 不加命名空间

仓库里没有 Redis key 前缀机制，`cache_version:*` 是实例全局的。生产 Redis 是 HCSO DCS 主备实例
（`docs/cce-architecture.md` 里"HCSO 无 DCS、自建单台"的描述已过时）。不加前缀：即使有其他环境
共用同一实例，后果也只是对方的写入会让本方多一次幂等重建，不会写错数据；届时再加前缀。

### 3.7 Redis 3.0 兼容

只使用 `INCR`（1.0）、`MGET`（1.0）。不设 TTL（`volatile-*` 淘汰策略不会淘汰无 TTL 的 key；
`allkeys-*` 淘汰后按 §3.2-3 自愈）。

> **不要使用 `common.RedisIncr`**：它只在 key 已存在且有 TTL 时才自增（`common/redis.go:242`），
> 对永不过期的版本 key 是空操作。直接用 `common.RDB.Incr(ctx, key)`。

---

## 4. 具体改动

### 4.1 配置项（common）

`common/constants.go`，紧挨 `SyncFrequency`（约 203 行）新增：

```go
var CacheVersionPollInterval int // unit is second; 0 disables cross-pod cache version polling
```

`common/init.go`，紧挨 `SyncFrequency = GetEnvOrDefault("SYNC_FREQUENCY", 60)`（116 行）新增：

```go
CacheVersionPollInterval = GetEnvOrDefault("CACHE_VERSION_POLL_INTERVAL", 1)
```

### 4.2 新文件 `model/cache_version.go`

新建文件，**不要加 QuantumNous 许可证头**（AGENTS.md Rule 5 例外条款），直接从 `package model` 开始。

对外接口（名字、签名固定）：

```go
const (
	CacheDomainOptions   = "options"
	CacheDomainChannels  = "channels"
	CacheDomainModelMeta = "model_meta"
)

// NotifyCacheChanged 在写库提交成功之后调用，通知其他副本重建该域缓存。
// Redis 未启用时为空操作；失败只记日志，不返回错误。
func NotifyCacheChanged(domain string)

// RefreshChannelCache 供写路径使用：重建本副本渠道缓存并通知其他副本。
// 启动、定时同步、远端重建一律调用 InitChannelCache（不通知），否则会形成通知风暴。
func RefreshChannelCache()

// StartCacheVersionWatcher 启动版本号轮询；Redis 未启用或 interval<=0 时直接返回。
func StartCacheVersionWatcher(interval time.Duration)
```

内部结构（为可测试性，轮询逻辑与 goroutine 分离，测试直接调 `poll`）：

```go
const cacheVersionKeyPrefix = "cache_version:"

// 固定顺序，保证一次 poll 内的重建顺序可预测。
var cacheVersionDomains = []string{CacheDomainOptions, CacheDomainChannels, CacheDomainModelMeta}

type cacheVersionWatcher struct {
	reloaders map[string]func() // domain -> 远端重建
	seen      map[string]string // domain -> 上次看到的版本（key 不存在记为 ""）
	ready     bool              // 是否已记录基线
	healthy   bool              // 上一次 MGET 是否成功，仅用于日志去重
}

func newCacheVersionWatcher(reloaders map[string]func()) *cacheVersionWatcher

// poll 执行一次 MGET + 比较 + 重建。返回 MGET 的错误（失败时不改 seen）。
func (w *cacheVersionWatcher) poll(ctx context.Context) error

// defaultCacheReloaders 返回生产用的重建表（见 §3.1 表格最后一列）。
func defaultCacheReloaders() map[string]func()
```

`poll` 的语义（必须严格按此实现）：

```go
vals, err := common.RDB.MGet(ctx, keys...).Result() // keys 按 cacheVersionDomains 顺序
if err != nil { return err }
for i, d := range cacheVersionDomains {
	v, _ := vals[i].(string) // nil -> ""
	if w.ready && v != w.seen[d] {
		// 单域 recover；记录一条日志：
		// "cache version changed: domain=%s %q -> %q, reloaded in %s"
		runReload(d, w.reloaders[d])
	}
	w.seen[d] = v
}
w.ready = true
return nil
```

`StartCacheVersionWatcher`：`!common.RedisEnabled || common.RDB == nil || interval <= 0` 时返回；否则
`go` 一个 `time.Ticker` 循环调用 `poll`（每次 `context.WithTimeout(…, 2*time.Second)`；这是后台
轮询，不在请求路径上，保持 2 秒），
按 §3.3 做"状态翻转才打日志"。启动时打一条 `SysLog("cache version watcher started, interval=…")`。

`NotifyCacheChanged`：`!common.RedisEnabled || common.RDB == nil` 时返回；
`common.RDB.Incr(ctx, cacheVersionKeyPrefix+domain)`，超时 `cacheVersionNotifyTimeout = 500 * time.Millisecond`；失败
`common.SysError(fmt.Sprintf("cache version bump failed: domain=%s: %v", domain, err))`。

`defaultCacheReloaders()`：

```go
CacheDomainOptions:   func() { loadOptionsFromDatabase(false); InvalidatePricingCache() },
CacheDomainChannels:  func() { InitChannelCache(); InitModelAliasCache(); InitChannelModelCostCache(); InvalidatePricingCache() },
CacheDomainModelMeta: func() { InitModelVisibilityCache(); RefreshPricing() },
```

注意 channels 域里调用 `InitModelAliasCache()` 而**不是** `refreshModelAliasCache()`：
后者会重建 abilities（写共享库）并通知，只能由写方执行（见 `model/model_alias.go:166-171` 注释）。

### 4.3 写路径接入（完整清单）

原则：**凡是写路径上"写库后刷新本地缓存"的地方，都要通知；所有重建/启动/定时路径都不通知。**

#### options 域

| 位置 | 改法 |
| --- | --- |
| `model/option.go` `UpdateOption`（320 行起） | `DB.Save` 成功后：`err := updateOptionMap(key, value)`；`NotifyCacheChanged(CacheDomainOptions)`；`return err`。库已写入就必须通知，即使内存应用失败。 |
| `model/option.go` `UpdateOptionsWithAudit`（362 行起） | 在 `DB.Transaction(...)` 之后的 `if err != nil { return err }` **之后**紧接着加 `defer NotifyCacheChanged(CacheDomainOptions)`，覆盖后续审计写入失败、`applyErr` 提前返回等所有分支。 |

（`UpdateOptions` 委托给 `UpdateOptionsWithAudit`，无需单独改。启动迁移类直接写 `options` 表的代码不改。）

#### channels 域

1. **`model/channel_cache.go` 或 `model/cache_version.go`**：实现 `RefreshChannelCache()` =
   `InitChannelCache(); NotifyCacheChanged(CacheDomainChannels)`。
2. **把以下写路径上的 `InitChannelCache()` 替换为 `RefreshChannelCache()`**：
   - `controller/channel.go`：723、737、774、797、898、926、1061、1209、1306、1523、1565、1589、1636、1716、1784（共 15 处，全部）
   - `controller/channel_upstream_update.go:479`（`refreshChannelRuntimeCache`）
   - `controller/codex_oauth.go:220`
   - `controller/codex_usage.go:95`
   - `model/ability.go:487`（`FixAbility`；`main.go` 启动恢复分支也会调到它，启动时多一次通知无害）
   - `service/codex_credential_refresh_task.go:140`
   - `service/codex_credential_refresh.go:99`

   **不替换**：`main.go:155`（启动）、`model/channel_cache.go:101`（`SyncChannelCache`）、
   `defaultCacheReloaders`、测试文件。改完后 `grep -rn "InitChannelCache()" --include=*.go`
   的非测试结果应只剩这三处 + 函数定义。
3. **`model/model_alias.go` `refreshModelAliasCache()`（339 行）**：函数第一行加
   `defer NotifyCacheChanged(CacheDomainChannels)`（覆盖 `len(changed)==0` 的提前返回）。
   它是 model 层所有渠道写（`BatchInsertChannels`、`BatchDeleteChannels`、`Insert`、`Update`、
   `Delete`、`EditChannelByTag`、`DeleteChannelByStatus`、`DeleteDisabledChannel`）提交后的公共出口。
   这一条同时修好了 `AddChannel` 目前连本 Pod 都要等 60 秒的问题（`controller/channel.go:615-714`
   全程不刷新本地渠道缓存）——本 Pod 的轮询也会看到这次 `INCR`。
4. **成本单独写入**：`model/channel_model_cost.go` 的 `ReplaceChannelModelCosts`（133 行）与
   `DeleteChannelModelCosts`（143 行）在 `InitChannelModelCostCache()` 后加
   `NotifyCacheChanged(CacheDomainChannels)`。`model/channel.go:470`、`:888` 两处后面紧跟
   `refreshModelAliasCache()`，已被第 3 条覆盖，不再加。
5. **自动禁用/启用 `model/channel.go` `UpdateChannelStatus`（700 行）**：
   函数里 abilities 的更新放在一个 `defer` 里，在 `return true` **之后**才执行。通知必须在
   abilities 更新之后，所以用"先注册、后执行"的 defer：在**现有的 abilities defer 之前**注册：

   ```go
   saved := false
   defer func() {
   	if saved {
   		NotifyCacheChanged(CacheDomainChannels)
   	}
   }()
   shouldUpdateAbilities := false
   defer func() { /* 原有 abilities 更新，不动 */ }()
   ...
   err = channel.SaveWithoutKey()
   if err != nil { ...; return false }
   saved = true // 紧挨 return true 之前
   ```

   提前返回 `false` 的分支（缓存里已是目标状态、库里已是目标状态、读库失败、保存失败）都不通知。
   这条在 relay 热路径上（上游报错触发自动禁用），只增加一次带 500ms 超时的 `INCR`。
6. **按标签批量启停**（`model.EnableChannelByTag` / `DisableChannelByTag`）：无需单独处理。
   它们的入口 `controller/channel.go` 的 `DisableTagChannels`（774 行）/ `EnableTagChannels`
   （797 行）写库后已调用 `InitChannelCache()`，在第 2 条的替换清单里，换成
   `RefreshChannelCache()` 即覆盖。

#### model_meta 域

`controller/model_meta.go` 的 `CreateModelMeta`（函数 81 行起，调用在 106 行）、
`UpdateModelMeta`（111 行起，调用在 147 行）、`DeleteModelMeta`（152 行起，调用在 165 行）：
在各自的 `model.InitModelVisibilityCache()` 之后加
`model.NotifyCacheChanged(model.CacheDomainModelMeta)`。

### 4.4 启动

`main.go`：在 `go model.SyncOptions(common.SyncFrequency)`（175 行）之后加：

```go
// 跨副本秒级失效：写方 INCR 版本号，各副本轮询发现变化即重建；60 秒全量同步保留作兜底。
model.StartCacheVersionWatcher(time.Duration(common.CacheVersionPollInterval) * time.Second)
```

（若 `main.go` 未导入 `time`，按需添加。）

### 4.5 文档同步

`model/option.go:283-297` 与 `controller/option.go:80-87` 的注释写着"其余副本最多滞后
`SYNC_FREQUENCY`（默认 60 秒）"。**不要删改这些注释原文**，只在 `model/option.go` 的
`SyncOptions` 上方补一句：配了 Redis 时，跨副本通常 1～2 秒经 `cache_version:*` 生效
（见 `model/cache_version.go`），60 秒轮询是兜底。设置页"读库"的逻辑保持不变。

---

## 5. 自测

### 5.1 单元/集成测试：新文件 `model/cache_version_test.go`

model 包已有 `TestMain`（`model/task_cas_test.go:17`，SQLite 内存库，`RedisEnabled=false`，
`MaxOpenConns=1`），直接复用。Redis 用 `miniredis`（`go.mod` 已有 `github.com/alicebob/miniredis/v2`，
用法参考 `common/redis_hash_test.go:14-24`）。在测试文件里写一个 helper：

```go
func withCacheVersionRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	mr := miniredis.RunT(t)
	prevRDB, prevEnabled := common.RDB, common.RedisEnabled
	common.RDB = redis.NewClient(&redis.Options{Addr: mr.Addr()})
	common.RedisEnabled = true
	t.Cleanup(func() { _ = common.RDB.Close(); common.RDB, common.RedisEnabled = prevRDB, prevEnabled })
	return mr
}
```

watcher 测试用假的 reloaders（计数器），不要用生产重建函数。必须覆盖的用例：

| # | 用例 | 断言 |
| --- | --- | --- |
| W1 | 首次 poll（key 不存在 / 已有值各一次） | 不调用任何 reloader；`ready=true` |
| W2 | 基线后对 `channels` `INCR` 一次，poll | 只有 channels reloader 被调 1 次；再 poll 一次不再调用 |
| W3 | 两次 poll 之间对同一域 `INCR` 5 次 | 只重建 1 次（合并） |
| W4 | 基线 `"3"` 后 `mr.Del` 该 key，poll | 触发重建（`"3"` → `""`，验证 `!=` 而非 `>`） |
| W5 | 基线 `"5"` 后 `mr.Set` 为 `"2"`（模拟主备切换回退），poll | 触发重建 |
| W6 | `mr.SetError("boom")` 后 `INCR`… poll 返回 error、不重建、`seen` 不变；`mr.SetError("")` 后 poll | 恢复后重建 1 次 |
| W7 | options reloader panic，同时 channels 也有变化 | `poll` 不 panic；channels reloader 仍被调用；下次 poll 不重复重建 options |
| N1 | `RedisEnabled=false`（且 `RDB=nil`）调 `NotifyCacheChanged` | 不 panic |
| N2 | Redis 可用时 `NotifyCacheChanged(CacheDomainOptions)` 两次 | key 值为 `"2"`；`mr.TTL(key)==0`（无过期） |
| N3 | `StartCacheVersionWatcher` 在 `RedisEnabled=false` 或 `interval=0` 时 | 立即返回，不启动 goroutine（可通过不 panic + 不访问 nil RDB 验证） |
| S1 | **防风暴**：Redis 可用，**`common.MemoryCacheEnabled = true`**（否则 `InitChannelCache` 直接 return，等于空跑；`t.Cleanup` 还原），seed 一个渠道 + ability，依次调用 `defaultCacheReloaders()` 里的每个函数，以及 `InitChannelCache`、`InitModelAliasCache`、`InitChannelModelCostCache`、`InitModelVisibilityCache`、`loadOptionsFromDatabase(false)` | 三个版本 key 全部仍不存在 |
| O1 | `UpdateOption("SystemName", "x")` | `cache_version:options == "1"` |
| O2 | `UpdateOption("general_setting.quota_display_type", "USD")`（被 `IsFixedQuotaDisplayOption` 拒绝） | 返回 error；options key 不存在 |
| O3 | `UpdateOptions(map{"ModelRatio": `{"m":1}`}, nil, func(map[string]string) error { return nil })` | options key `== "1"`（只通知一次）。`stateKeys=nil` 可用（`getOptionValues` 返回空 map） |
| O4 | 同 O3，但 `validate` 返回 error | 返回 error；options key 不存在 |
| C1 | `MemoryCacheEnabled=true`，seed 启用渠道 + ability，`InitChannelCache()`，`UpdateChannelStatus(id, "", common.ChannelStatusAutoDisabled, "t")` | 返回 true；channels key `== "1"`；库里 abilities 已禁用 |
| C2 | 紧接 C1 再调用一次相同状态 | 返回 false；channels key 仍 `== "1"` |
| C3 | `RefreshChannelCache()` | channels key +1 |
| C4 | `(&Channel{...}).Insert()` | channels key 至少 +1（经 `refreshModelAliasCache`） |
| C5 | `ReplaceChannelModelCosts` / `DeleteChannelModelCosts` | channels key 各 +1（如果测试库未迁移 `ChannelModelCost`，在测试里 `DB.AutoMigrate(&ChannelModelCost{})`，不要改 `TestMain`） |

注意测试间隔离：每个测试用独立 miniredis；涉及表数据的用 `truncateTables(t)`；修改的全局变量
（`MemoryCacheEnabled`、`RedisEnabled`、`RDB`）一律 `t.Cleanup` 还原。

运行（开发期按需窄跑）：

```bash
go test ./model/ -run 'CacheVersion|NotifyCache|RefreshChannelCache' -count=1 -v
```

### 5.2 编译与回归（交付前必须全部通过）

```bash
gofmt -l ./common ./model ./controller ./service main.go   # 输出必须为空
go build ./...
go vet ./common/... ./model/... ./controller/... ./service/...
go test ./model/... ./common/... ./controller/... ./service/... -count=1
```

`go build` 依赖 `web/classic/dist`、`web/mobile/dist` 的 embed，仓库里当前已存在；若缺失，
`cd web/classic && bun install && bun run build`（mobile 同理），不要为了编译改 embed 指令。

如果某个包的测试在**基线 `1089f758a` 上就失败**，先在基线上确认（`git stash` 前后对比），
报告中列出，不要顺手修。

### 5.3 端到端：两进程 + Redis 3.0.7 + PostgreSQL（**验收方执行，开发方不做**）

目的：证明"A 进程改、B 进程秒级生效"，并证明测量的确实是本机制而不是 60 秒兜底。
现网是 PostgreSQL，端到端只用 PostgreSQL，不用 SQLite 兜底（两进程共写一个 SQLite 文件会
`database is locked`，结果不可信）。

1. 起依赖（需要本机 Docker daemon 运行）：
   ```bash
   docker run -d --name cvs-redis -p 6390:6379 redis:3.0.7
   docker run -d --name cvs-pg -p 5442:5432 -e POSTGRES_PASSWORD=pw -e POSTGRES_DB=newapi postgres:15
   ```
2. 编译：`go build -o /tmp/new-api-cvs .`
3. 起两个进程（**`SYNC_FREQUENCY=3600`**，屏蔽 60 秒兜底）：
   ```bash
   DSN='postgres://postgres:pw@127.0.0.1:5442/newapi?sslmode=disable'
   # A: master
   PORT=3101 NODE_TYPE=master SQL_DSN="$DSN" REDIS_CONN_STRING='redis://127.0.0.1:6390' \
     SYNC_FREQUENCY=3600 /tmp/new-api-cvs > /tmp/cvs-a.log 2>&1 &
   # 等 A 完成迁移后再起 B
   PORT=3102 NODE_TYPE=slave  SQL_DSN="$DSN" REDIS_CONN_STRING='redis://127.0.0.1:6390' \
     SYNC_FREQUENCY=3600 /tmp/new-api-cvs > /tmp/cvs-b.log 2>&1 &
   ```
4. 在 A 上登录 root（首次启动自动创建，`root / 123456`，见 `model/main.go:72`），保存 cookie；
   管理接口需带 `New-Api-User: 1` 头。
5. 用例（每条记录：在 A 操作的时间、B 上生效的时间、B 日志中对应的
   `cache version changed: domain=...` 行）：
   - **E1 options**：A 上 `PUT /api/option/` `{"key":"SystemName","value":"cvs-<时间戳>"}`；
     循环请求 B 的 `GET /api/status`，`data.system_name` 应在 **≤2 秒**内变为新值。
   - **E2 channels**：A 上新增一个渠道（`POST /api/channel/`，任意类型、假 key 即可）；B 日志应在
     ≤2 秒内出现 `domain=channels` 重建行。再在 A 上把该渠道禁用（`PUT /api/channel/` 改 `status`），同样验证。
   - **E3 model_meta**：A 上新增一条模型元数据（`POST /api/models/`，以 `router/` 实际路由为准）；
     B 日志 ≤2 秒内出现 `domain=model_meta`。
   - **E4 对照组**：停 B，用 `CACHE_VERSION_POLL_INTERVAL=0` 重启 B，重复 E1：B 的 `system_name`
     在 10 秒内**不应**变化（证明 E1 测到的是本机制）。之后恢复默认重启 B。
   - **E5 Redis 故障**：`docker pause cvs-redis`，在 A 上执行一次 E1 的保存——**接口必须照常返回成功**
     （允许变慢不超过约 0.5 秒）；A/B 日志各出现一次错误日志而不是每秒刷屏。`docker unpause` 后，B 日志出现恢复日志。
     再执行一次 E1，B ≤2 秒生效。
   - **E6 兼容性**：`grep -i -E "ERR unknown command|wrong number of arguments" /tmp/cvs-*.log` 必须为空。
6. 结束后保留 `/tmp/cvs-a.log`、`/tmp/cvs-b.log` 与 E1～E6 的命令输出（写到 `/tmp/cvs-e2e-report.txt`），
   然后停掉两个进程、删掉两个容器。
7. 端到端发现的缺陷，由验收方整理后一次性交回开发方修复，修复后验收方重跑受影响的用例。

---

## 6. 上线与回滚

- 默认开启，不需要改 ConfigMap；**`SYNC_FREQUENCY` 不动**。
- 滚动发布期间新旧 Pod 混跑：旧 Pod 写入不 `INCR`，新 Pod 对这部分改动仍走 60 秒兜底；无错误。
- 观测：Pod 日志 `cache version watcher started`、每次改动的 `cache version changed: domain=…` 行。
- 应急关闭：ConfigMap 加 `CACHE_VERSION_POLL_INTERVAL: "0"` 并滚动，即回到现状（写方仍 `INCR`，无副作用）。
- 回滚代码：直接回退即可；Redis 里残留的三个 `cache_version:*` key 无害，可不清理。

---

## 7. 交付物与验收标准

开发方交付：

1. 代码改动（不提交 commit，留在工作区，由验收方 review 后提交）。
2. 报告，包含：
   - 改动文件清单与每处改动对应本文的哪一条（§4.x）；
   - `grep -rn "InitChannelCache()" --include=*.go . | grep -v _test` 的输出；
   - §5.1 测试的 `-v` 输出；§5.2 四条命令的完整输出；
   - （不需要端到端证据，§5.3 由验收方执行）
   - 与本文不一致或无法按本文实施之处（如有），以及基线上就失败的测试（如有）。

验收方（我）检查：

- [ ] 改动范围与 §4 完全一致，无额外重构、无无关格式化、无删改原有注释；新文件无许可证头。
- [ ] 所有 `NotifyCacheChanged` 都在写库提交之后；没有任何重建/启动/定时路径会通知（S1 通过 + 代码审读）。
- [ ] `UpdateChannelStatus` 的通知在 abilities 更新之后执行，提前返回 false 的分支不通知。
- [ ] `poll` 语义与 §4.2 完全一致：先读后重建、`!=` 比较、单域 recover、失败不推进已见值。
- [ ] 只用 `INCR`/`MGET`；未使用 `common.RedisIncr`；key 无 TTL。
- [ ] `INCR` 失败不影响保存返回值，超时为 500ms；Redis 故障日志不刷屏。
- [ ] `StartCacheVersionWatcher` 守卫含 `common.RDB == nil`；model_meta 远端重建调用 `RefreshPricing()`。
- [ ] §5.1 全部用例存在且通过；§5.2 全绿（或基线即失败的已列明）。
- [ ] （验收方自跑）§5.3 E1～E6 在 PostgreSQL + Redis 3.0.7 上通过：E1/E2/E3 生效 ≤2 秒，E4 对照组未生效，E5 保存成功且日志不刷屏，E6 为空。
