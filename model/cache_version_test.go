package model

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------- 测试脚手架

// withCacheVersionRedis 把 common.RDB / RedisEnabled 指向一个进程内 miniredis。
func withCacheVersionRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	mr := miniredis.RunT(t)
	prevRDB, prevEnabled := common.RDB, common.RedisEnabled
	common.RDB = redis.NewClient(&redis.Options{Addr: mr.Addr()})
	common.RedisEnabled = true
	t.Cleanup(func() {
		_ = common.RDB.Close()
		common.RDB, common.RedisEnabled = prevRDB, prevEnabled
	})
	return mr
}

// withOptionMap 保证 updateOptionMap 写 common.OptionMap 时不是 nil map。
func withOptionMap(t *testing.T) {
	t.Helper()
	prev := common.OptionMap
	common.OptionMap = map[string]string{}
	t.Cleanup(func() { common.OptionMap = prev })
}

// withMemoryCache 打开内存缓存（InitChannelCache 在关闭时直接 return）。
func withMemoryCache(t *testing.T) {
	t.Helper()
	prev := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() { common.MemoryCacheEnabled = prev })
}

func versionKey(domain string) string { return cacheVersionKeyPrefix + domain }

func versionValue(t *testing.T, mr *miniredis.Miniredis, domain string) string {
	t.Helper()
	value, err := mr.Get(versionKey(domain))
	if err != nil {
		return ""
	}
	return value
}

type fakeReloaders struct {
	calls map[string]int
	fns   map[string]func()
}

func newFakeReloaders() *fakeReloaders {
	f := &fakeReloaders{calls: map[string]int{}}
	f.fns = make(map[string]func(), len(cacheVersionDomains))
	for _, domain := range cacheVersionDomains {
		domain := domain
		f.fns[domain] = func() { f.calls[domain]++ }
	}
	return f
}

func (f *fakeReloaders) total() int {
	total := 0
	for _, n := range f.calls {
		total += n
	}
	return total
}

func pollOnce(t *testing.T, w *cacheVersionWatcher) {
	t.Helper()
	require.NoError(t, w.poll(context.Background()))
}

// ---------------------------------------------------------------- watcher 行为

// W1 首次 poll 只记基线，不重建。
func TestCacheVersionWatcherFirstPollRecordsBaselineOnly(t *testing.T) {
	t.Run("key 不存在", func(t *testing.T) {
		withCacheVersionRedis(t)
		fake := newFakeReloaders()
		w := newCacheVersionWatcher(fake.fns)

		pollOnce(t, w)
		assert.True(t, w.ready)
		assert.Equal(t, 0, fake.total(), "首拍不该重建任何域")
		for _, domain := range cacheVersionDomains {
			assert.Equal(t, "", w.seen[domain])
		}
	})

	t.Run("key 已有值", func(t *testing.T) {
		mr := withCacheVersionRedis(t)
		mr.Set(versionKey(CacheDomainChannels), "7")
		fake := newFakeReloaders()
		w := newCacheVersionWatcher(fake.fns)

		pollOnce(t, w)
		assert.Equal(t, 0, fake.total(), "首拍即便有值也只记基线")
		assert.Equal(t, "7", w.seen[CacheDomainChannels])
	})
}

// W2 基线后某域变化 → 只重建该域一次，再 poll 不重复。
func TestCacheVersionWatcherReloadsOnlyChangedDomain(t *testing.T) {
	mr := withCacheVersionRedis(t)
	fake := newFakeReloaders()
	w := newCacheVersionWatcher(fake.fns)
	pollOnce(t, w)

	mr.Incr(versionKey(CacheDomainChannels), 1)
	pollOnce(t, w)
	assert.Equal(t, 1, fake.calls[CacheDomainChannels])
	assert.Equal(t, 0, fake.calls[CacheDomainOptions])
	assert.Equal(t, 0, fake.calls[CacheDomainModelMeta])

	pollOnce(t, w)
	assert.Equal(t, 1, fake.calls[CacheDomainChannels], "版本没再变就不该重复重建")
}

// W3 两次 poll 之间多次 INCR 只重建一次（天然合并）。
func TestCacheVersionWatcherCoalescesBurst(t *testing.T) {
	mr := withCacheVersionRedis(t)
	fake := newFakeReloaders()
	w := newCacheVersionWatcher(fake.fns)
	pollOnce(t, w)

	for i := 0; i < 5; i++ {
		mr.Incr(versionKey(CacheDomainChannels), 1)
	}
	pollOnce(t, w)
	assert.Equal(t, 1, fake.calls[CacheDomainChannels])
}

// W4 key 被删（视为 ""）也要重建：比较用 != 而不是 >。
func TestCacheVersionWatcherReloadsWhenKeyDisappears(t *testing.T) {
	mr := withCacheVersionRedis(t)
	mr.Set(versionKey(CacheDomainOptions), "3")
	fake := newFakeReloaders()
	w := newCacheVersionWatcher(fake.fns)
	pollOnce(t, w)
	require.Equal(t, "3", w.seen[CacheDomainOptions])

	mr.Del(versionKey(CacheDomainOptions))
	pollOnce(t, w)
	assert.Equal(t, 1, fake.calls[CacheDomainOptions])
	assert.Equal(t, "", w.seen[CacheDomainOptions])
}

// W5 计数回退（主备切换后的异步复制）也要重建。
func TestCacheVersionWatcherReloadsWhenCounterGoesBackwards(t *testing.T) {
	mr := withCacheVersionRedis(t)
	mr.Set(versionKey(CacheDomainChannels), "5")
	fake := newFakeReloaders()
	w := newCacheVersionWatcher(fake.fns)
	pollOnce(t, w)

	mr.Set(versionKey(CacheDomainChannels), "2")
	pollOnce(t, w)
	assert.Equal(t, 1, fake.calls[CacheDomainChannels])
}

// W6 MGET 失败：不重建、不推进 seen；恢复后照常发现期间的改动。
func TestCacheVersionWatcherKeepsSeenOnRedisError(t *testing.T) {
	mr := withCacheVersionRedis(t)
	fake := newFakeReloaders()
	w := newCacheVersionWatcher(fake.fns)
	pollOnce(t, w)
	before := map[string]string{}
	for domain, value := range w.seen {
		before[domain] = value
	}

	mr.SetError("boom")
	err := w.poll(context.Background())
	require.Error(t, err)
	assert.Equal(t, 0, fake.total(), "失败时不得重建")
	assert.Equal(t, before, w.seen, "失败时不得推进已见版本")

	mr.SetError("")
	mr.Incr(versionKey(CacheDomainOptions), 1)
	pollOnce(t, w)
	assert.Equal(t, 1, fake.calls[CacheDomainOptions], "恢复后第一拍应发现期间的改动")
}

// W7 单个域重建 panic 不扩散，也不影响后续 poll。
func TestCacheVersionWatcherRecoversPerDomain(t *testing.T) {
	mr := withCacheVersionRedis(t)
	fake := newFakeReloaders()
	fake.fns[CacheDomainOptions] = func() { panic("boom") }
	w := newCacheVersionWatcher(fake.fns)
	pollOnce(t, w)

	mr.Incr(versionKey(CacheDomainOptions), 1)
	mr.Incr(versionKey(CacheDomainChannels), 1)
	require.NotPanics(t, func() { pollOnce(t, w) })
	assert.Equal(t, 1, fake.calls[CacheDomainChannels], "别的域不受影响")
	assert.Equal(t, "1", w.seen[CacheDomainOptions], "失败的域也要推进已见值，避免每次 poll 都重试")

	pollOnce(t, w)
	assert.Equal(t, 1, fake.calls[CacheDomainChannels])
}

// ---------------------------------------------------------------- 通知

// N1 Redis 未启用时通知是空操作。
func TestNotifyCacheChangedWithoutRedis(t *testing.T) {
	prevRDB, prevEnabled := common.RDB, common.RedisEnabled
	common.RDB, common.RedisEnabled = nil, false
	t.Cleanup(func() { common.RDB, common.RedisEnabled = prevRDB, prevEnabled })

	assert.NotPanics(t, func() { NotifyCacheChanged(CacheDomainOptions) })
}

// N2 通知累加到无 TTL 的 key 上。
func TestNotifyCacheChangedIncrementsForever(t *testing.T) {
	mr := withCacheVersionRedis(t)
	NotifyCacheChanged(CacheDomainOptions)
	NotifyCacheChanged(CacheDomainOptions)

	assert.Equal(t, "2", versionValue(t, mr, CacheDomainOptions))
	assert.Equal(t, time.Duration(0), mr.TTL(versionKey(CacheDomainOptions)), "版本 key 不能带过期时间")
}

// N3 关闭开关 / 没有 Redis 时不启动轮询。
func TestStartCacheVersionWatcherDisabled(t *testing.T) {
	prevRDB, prevEnabled := common.RDB, common.RedisEnabled
	common.RDB, common.RedisEnabled = nil, false
	t.Cleanup(func() { common.RDB, common.RedisEnabled = prevRDB, prevEnabled })

	assert.NotPanics(t, func() { StartCacheVersionWatcher(time.Second) }, "Redis 未启用时不该碰 nil RDB")

	withCacheVersionRedis(t)
	assert.NotPanics(t, func() { StartCacheVersionWatcher(0) }, "interval=0 是应急关闭开关")
}

// M1 三个域的通知落在**字面量** key 上（防止常量打错字导致写读两侧对不上）。
func TestCacheVersionKeyNames(t *testing.T) {
	mr := withCacheVersionRedis(t)
	for _, domain := range cacheVersionDomains {
		NotifyCacheChanged(domain)
	}
	for _, key := range []string{"cache_version:options", "cache_version:channels", "cache_version:model_meta"} {
		assert.True(t, mr.Exists(key), "缺少版本 key: %s", key)
	}
}

// M2 生产重建表必须恰好覆盖三个域（多一个/少一个都说明写读两侧脱节）。
func TestDefaultCacheReloadersCoverAllDomains(t *testing.T) {
	reloaders := defaultCacheReloaders()
	require.Len(t, reloaders, len(cacheVersionDomains))
	for _, domain := range cacheVersionDomains {
		reloader, ok := reloaders[domain]
		assert.True(t, ok, "缺少 reloader: %s", domain)
		assert.NotNil(t, reloader, "reloader 为空: %s", domain)
	}
}

// ---------------------------------------------------------------- 防风暴

// S1 所有重建函数都不许通知：否则 A 重建 → INCR → B 重建 → … 形成风暴。
func TestCacheVersionReloadersNeverNotify(t *testing.T) {
	mr := withCacheVersionRedis(t)
	withOptionMap(t)
	withMemoryCache(t)
	seedCacheVersionChannel(t)

	for _, domain := range cacheVersionDomains {
		reloader := defaultCacheReloaders()[domain]
		require.NotNil(t, reloader)
		reloader()
	}
	InitChannelCache()
	InitModelAliasCache()
	InitChannelModelCostCache()
	InitModelVisibilityCache()
	loadOptionsFromDatabase(false)

	for _, domain := range cacheVersionDomains {
		assert.False(t, mr.Exists(versionKey(domain)), "重建路径不得 INCR: %s", domain)
	}
}

// ---------------------------------------------------------------- options 域写路径

// O1 单键保存通知一次。
func TestUpdateOptionNotifiesOnce(t *testing.T) {
	mr := withCacheVersionRedis(t)
	withOptionMap(t)

	prevSystemName := common.SystemName
	t.Cleanup(func() { common.SystemName = prevSystemName })
	resetCacheVersionTables(t)

	require.NoError(t, UpdateOption("SystemName", "cache-version-test"))
	assert.Equal(t, "1", versionValue(t, mr, CacheDomainOptions))
}

// O2 被拒绝的键（额度展示固定人民币）不写库、不通知。
func TestUpdateOptionRejectedKeyDoesNotNotify(t *testing.T) {
	mr := withCacheVersionRedis(t)
	withOptionMap(t)
	resetCacheVersionTables(t)

	require.Error(t, UpdateOption("general_setting.quota_display_type", "USD"))
	assert.False(t, mr.Exists(versionKey(CacheDomainOptions)))
}

// O3 批量保存只通知一次。
func TestUpdateOptionsNotifiesOnce(t *testing.T) {
	mr := withCacheVersionRedis(t)
	withOptionMap(t)
	resetCacheVersionTables(t)

	err := UpdateOptions(
		map[string]string{"ModelRatio": `{"cache-version-model":1}`},
		nil,
		func(map[string]string) error { return nil },
	)
	require.NoError(t, err)
	assert.Equal(t, "1", versionValue(t, mr, CacheDomainOptions))
}

// O4 校验失败不写库、不通知。
func TestUpdateOptionsRejectedDoesNotNotify(t *testing.T) {
	mr := withCacheVersionRedis(t)
	withOptionMap(t)
	resetCacheVersionTables(t)

	err := UpdateOptions(
		map[string]string{"ModelRatio": `{"cache-version-model":1}`},
		nil,
		func(map[string]string) error { return assert.AnError },
	)
	require.Error(t, err)
	assert.False(t, mr.Exists(versionKey(CacheDomainOptions)))
}

// ---------------------------------------------------------------- channels 域写路径

// C1 自动禁用：通知发生在 abilities 更新之后。
func TestUpdateChannelStatusNotifiesAfterAbilities(t *testing.T) {
	mr := withCacheVersionRedis(t)
	withOptionMap(t)
	withMemoryCache(t)
	channel := seedCacheVersionChannel(t)

	ok := UpdateChannelStatus(channel.Id, "", common.ChannelStatusAutoDisabled, "cache-version-test")
	require.True(t, ok)
	assert.Equal(t, "1", versionValue(t, mr, CacheDomainChannels))

	var ability Ability
	require.NoError(t, DB.Where("channel_id = ?", channel.Id).First(&ability).Error)
	assert.False(t, ability.Enabled, "abilities 必须已经按新状态更新（通知排在它之后）")
}

// C2 库里/缓存里已经是目标状态 → 提前返回 false，不通知。
func TestUpdateChannelStatusNoopDoesNotNotify(t *testing.T) {
	mr := withCacheVersionRedis(t)
	withOptionMap(t)
	withMemoryCache(t)
	channel := seedCacheVersionChannel(t)

	require.True(t, UpdateChannelStatus(channel.Id, "", common.ChannelStatusAutoDisabled, "t"))
	require.Equal(t, "1", versionValue(t, mr, CacheDomainChannels))

	assert.False(t, UpdateChannelStatus(channel.Id, "", common.ChannelStatusAutoDisabled, "t"))
	assert.Equal(t, "1", versionValue(t, mr, CacheDomainChannels), "无变化的调用不该通知")
}

// C3 RefreshChannelCache 重建本地并通知。
func TestRefreshChannelCacheNotifies(t *testing.T) {
	mr := withCacheVersionRedis(t)
	withOptionMap(t)
	withMemoryCache(t)
	seedCacheVersionChannel(t)

	RefreshChannelCache()
	assert.Equal(t, "1", versionValue(t, mr, CacheDomainChannels))
}

// C4 model 层渠道写经 refreshModelAliasCache 通知（新增渠道过去连本 Pod 都要等 60 秒）。
func TestChannelInsertNotifies(t *testing.T) {
	mr := withCacheVersionRedis(t)
	withOptionMap(t)
	withMemoryCache(t)
	resetCacheVersionTables(t)

	channel := &Channel{
		Name:   "cache-version-insert",
		Type:   1,
		Key:    "sk-cache-version",
		Status: common.ChannelStatusEnabled,
		Group:  "default",
		Models: "cache-version-model",
	}
	require.NoError(t, channel.Insert())
	assert.True(t, mr.Exists(versionKey(CacheDomainChannels)))
}

// C5 渠道成本单独写入也要通知。
func TestChannelModelCostWritesNotify(t *testing.T) {
	mr := withCacheVersionRedis(t)
	withOptionMap(t)
	withMemoryCache(t)
	channel := seedCacheVersionChannel(t)

	require.NoError(t, ReplaceChannelModelCosts(channel.Id, []*ChannelModelCost{
		{ChannelId: channel.Id, ModelName: "cache-version-model", CostRatio: 0.5},
	}))
	assert.Equal(t, "1", versionValue(t, mr, CacheDomainChannels))

	require.NoError(t, DeleteChannelModelCosts(channel.Id))
	assert.Equal(t, "2", versionValue(t, mr, CacheDomainChannels))
}

// ---------------------------------------------------------------- 辅助

// seedCacheVersionChannel 建一个启用渠道 + ability，并把渠道装进内存缓存。
func seedCacheVersionChannel(t *testing.T) *Channel {
	t.Helper()
	require.NoError(t, DB.AutoMigrate(&ChannelModelCost{}, &Model{}))
	resetCacheVersionTables(t)

	channel := &Channel{
		Name:   "cache-version-channel",
		Type:   1,
		Key:    "sk-cache-version",
		Status: common.ChannelStatusEnabled,
		Group:  "default",
		Models: "cache-version-model",
	}
	require.NoError(t, DB.Create(channel).Error)
	require.NoError(t, DB.Create(&Ability{
		ChannelId: channel.Id,
		Group:     "default",
		Model:     "cache-version-model",
		Enabled:   true,
	}).Error)
	// 状态判定走内存缓存：内存缓存里没有这个渠道时 UpdateChannelStatus 直接返回 false。
	InitChannelCache()
	return channel
}

// resetCacheVersionTables 清掉本组用例涉及的表（TestMain 的 truncateTables 不覆盖 abilities 等）。
func resetCacheVersionTables(t *testing.T) {
	t.Helper()
	cleanup := func() {
		for _, table := range []string{"channel_model_costs", "abilities", "channels", "options", "models"} {
			if DB.Migrator().HasTable(table) {
				DB.Exec("DELETE FROM " + table)
			}
		}
	}
	cleanup()
	t.Cleanup(cleanup)
}
