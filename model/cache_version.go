package model

import (
	"context"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// 跨副本缓存失效：Redis 版本号脏标记。设计见 docs/cache-version-sync-design.md。
//
// # 为什么需要它
//
// 现网 1 master + 3 slave 挂在同一个 Service 后面。写操作只刷新**处理该请求的那个
// Pod** 的进程内缓存（options / channels+abilities+别名+成本 / 模型可见性），其余
// Pod 只能等 SYNC_FREQUENCY（默认 60 秒）全量重建 —— 负载均衡下表现成"时灵时不灵"。
//
// # 机制
//
//   - 写方在**写库提交之后** INCR 对应域的版本号；
//   - 每个副本每 CACHE_VERSION_POLL_INTERVAL 秒 MGET 三个版本号，
//     与自己上次看到的**不相等**就整类重建；
//   - 60 秒全量轮询（SyncOptions / SyncChannelCache / …）保留作兜底。
//
// # 三条不能破的纪律
//
//  1. INCR 必须在写库提交之后。否则读方可能在提交前重建、读到旧数据，并把新版本号
//     记成"已处理"，要等 60 秒兜底才纠正。
//  2. 重建函数**绝不能** INCR（见 §"防风暴"注释）。重建只调用 Init*/load* 这些不通知的函数。
//  3. 比较用 `!=` 而不是 `>`：DCS 主备切换（异步复制）可能让计数回退，key 被淘汰/删除
//     会变成不存在，任何"不相等"都该触发重建，自愈。
const (
	CacheDomainOptions   = "options"
	CacheDomainChannels  = "channels"
	CacheDomainModelMeta = "model_meta"
)

const cacheVersionKeyPrefix = "cache_version:"

// cacheVersionNotifyTimeout 写方 INCR 的超时。
//
// 写方可能在上游报错触发的**自动禁用**路径上（relay 热路径），所以比后台轮询的 2 秒
// 更短：Redis 卡住时最多拖慢一次写请求 500ms，而不是 2 秒。
const cacheVersionNotifyTimeout = 500 * time.Millisecond

// cacheVersionPollTimeout 后台轮询 MGET 的超时（不在请求路径上，可以给宽一点）。
const cacheVersionPollTimeout = 2 * time.Second

// cacheVersionDomains 固定顺序，保证一次 poll 内的重建顺序可预测。
var cacheVersionDomains = []string{CacheDomainOptions, CacheDomainChannels, CacheDomainModelMeta}

// NotifyCacheChanged 在**写库提交成功之后**调用，通知其他副本重建该域缓存。
//
// Redis 未启用（未配 REDIS_CONN_STRING）时为空操作；失败只记日志、**不向调用方返回
// 错误** —— 保存的成功与否不能被缓存通知拖累，那次改动退化为 60 秒兜底。
func NotifyCacheChanged(domain string) {
	if !common.RedisEnabled || common.RDB == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), cacheVersionNotifyTimeout)
	defer cancel()
	if err := common.RDB.Incr(ctx, cacheVersionKeyPrefix+domain).Err(); err != nil {
		common.SysError(fmt.Sprintf("cache version bump failed: domain=%s: %v", domain, err))
	}
}

// RefreshChannelCache 重建**本副本**渠道缓存，并通知其他副本。
//
// 只给写路径用。启动、定时同步、远端重建一律调 InitChannelCache（不通知）：
// 重建函数自己再 INCR 会让各副本互相触发，形成通知风暴。
func RefreshChannelCache() {
	InitChannelCache()
	NotifyCacheChanged(CacheDomainChannels)
}

// cacheVersionWatcher 版本号轮询器。
//
// 轮询逻辑与 goroutine 分离：用例直接调 poll，不起后台循环。
type cacheVersionWatcher struct {
	reloaders map[string]func() // domain -> 重建函数（都不许 INCR）
	seen      map[string]string // domain -> 上次看到的版本（key 不存在记为 ""）
	ready     bool              // 是否已记录基线（第一次成功的 poll 只记基线、不重建）
	healthy   bool              // 上一次 MGET 是否成功，仅用于日志去重
}

func newCacheVersionWatcher(reloaders map[string]func()) *cacheVersionWatcher {
	// healthy 初值为 true：进程刚起来时若 Redis 就是坏的，第一拍要留下一条错误日志。
	return &cacheVersionWatcher{
		reloaders: reloaders,
		seen:      make(map[string]string, len(cacheVersionDomains)),
		healthy:   true,
	}
}

// poll 执行一次 MGET + 比较 + 重建，返回 MGET 的错误（失败时不改 seen）。
//
// 语义（不要简化）：
//   - 先读版本号、再重建、最后记录**重建之前**读到的那个版本号。重建期间若有新的写入，
//     它的 INCR 一定产生更大的值，下一拍必然再次重建。
//   - 失败不推进 seen，恢复后第一拍就能发现期间的改动。
func (w *cacheVersionWatcher) poll(ctx context.Context) error {
	keys := make([]string, len(cacheVersionDomains))
	for i, domain := range cacheVersionDomains {
		keys[i] = cacheVersionKeyPrefix + domain
	}
	values, err := common.RDB.MGet(ctx, keys...).Result()
	if err == nil && len(values) != len(cacheVersionDomains) {
		err = fmt.Errorf("unexpected MGET reply length: %d", len(values))
	}
	if err != nil {
		if w.healthy {
			common.SysError("cache version polling failed: " + err.Error())
		}
		w.healthy = false
		return err
	}
	if !w.healthy {
		common.SysLog("cache version polling recovered")
	}
	w.healthy = true

	for i, domain := range cacheVersionDomains {
		value, _ := values[i].(string) // nil（key 不存在）-> ""
		if w.ready && value != w.seen[domain] {
			start := time.Now()
			w.runReload(domain)
			common.SysLog(fmt.Sprintf("cache version changed: domain=%s %q -> %q, reloaded in %s",
				domain, w.seen[domain], value, time.Since(start)))
		}
		w.seen[domain] = value
	}
	w.ready = true
	return nil
}

// runReload 重建单个域。**单域 recover**：goroutine 里漏出去的 panic 会杀掉整个进程，
// 而这里跑的是启动/定时的缓存重建，一个域出错不该拖垮其他域与后续轮询。
func (w *cacheVersionWatcher) runReload(domain string) {
	defer func() {
		if r := recover(); r != nil {
			common.SysError(fmt.Sprintf("cache version reload panicked: domain=%s: %v", domain, r))
		}
	}()
	reloader := w.reloaders[domain]
	if reloader == nil {
		common.SysError("cache version reloader missing: domain=" + domain)
		return
	}
	reloader()
}

// defaultCacheReloaders 生产用的重建表。**每一个都不许 NotifyCacheChanged**（防风暴）。
func defaultCacheReloaders() map[string]func() {
	return map[string]func(){
		CacheDomainOptions: func() {
			loadOptionsFromDatabase(false)
			// InvalidatePricingCache 必须在 loadOptionsFromDatabase **返回之后**：
			// 后者持 OptionMapRWMutex 逐项 apply，而定价失效要取 updatePricingLock，
			// 顺序颠倒会形成锁序反转（见 model/pricing.go 里 GetPricing 的注释）。
			InvalidatePricingCache()
		},
		CacheDomainChannels: func() {
			InitChannelCache()
			// 这里只能 InitModelAliasCache，不能 refreshModelAliasCache：后者会重建
			// abilities（写共享库）并通知，只能由写方执行（见 model_alias.go 的注释）。
			InitModelAliasCache()
			InitChannelModelCostCache()
			InvalidatePricingCache()
		},
		CacheDomainModelMeta: func() {
			InitModelVisibilityCache()
			// 与写方 controller/model_meta.go 一致：立即重建定价缓存，不依赖 1 分钟 TTL。
			RefreshPricing()
		},
	}
}

// StartCacheVersionWatcher 启动版本号轮询。
//
// Redis 未启用、客户端未初始化或 interval<=0（应急开关）时直接返回，行为与本机制
// 引入前完全一样（只有 60 秒全量同步）。
func StartCacheVersionWatcher(interval time.Duration) {
	if !common.RedisEnabled || common.RDB == nil || interval <= 0 {
		return
	}
	watcher := newCacheVersionWatcher(defaultCacheReloaders())
	common.SysLog(fmt.Sprintf("cache version watcher started, interval=%s", interval))
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			ctx, cancel := context.WithTimeout(context.Background(), cacheVersionPollTimeout)
			_ = watcher.poll(ctx)
			cancel()
		}
	}()
}
