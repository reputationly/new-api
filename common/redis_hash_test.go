package common

import (
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withMiniRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	mr := miniredis.RunT(t)
	prev := RDB
	RDB = redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() {
		_ = RDB.Close()
		RDB = prev
	})
	return mr
}

// 局部写入与删缓存并发时，不得凭空建出只有一个字段的残缺 hash。
//
// 旧实现先查 TTL、再另起事务 HINCRBY/HSET：两步之间 key 被删（授信结转、积分扣减
// 每次都删用户缓存），写入会新建一个只有 Quota 字段的 hash。读取方 HGETALL 非空
// 即当命中，于是 CreditLimit、Status 全是零值——授信用户被判额度不足而 403。
func TestRedisHashPartialWrite_NoPartialHashUnderConcurrentDelete(t *testing.T) {
	mr := withMiniRedis(t)
	const key = "user:1"

	for name, write := range map[string]func() error{
		"HIncrBy":   func() error { return RedisHIncrBy(key, "Quota", -1) },
		"HSetField": func() error { return RedisHSetField(key, "Quota", "7") },
	} {
		t.Run(name, func(t *testing.T) {
			partial := 0
			for i := 0; i < 300; i++ {
				mr.HSet(key, "Id", "1", "Quota", "100", "CreditLimit", "5000")
				mr.SetTTL(key, time.Minute)

				var wg sync.WaitGroup
				wg.Add(2)
				go func() { defer wg.Done(); _ = RedisDelKey(key) }()
				go func() { defer wg.Done(); assert.NoError(t, write()) }()
				wg.Wait()

				if mr.Exists(key) && mr.HGet(key, "Id") == "" {
					partial++
				}
				mr.Del(key)
			}
			require.Zero(t, partial, "删缓存与局部写入并发时出现了残缺 hash")
		})
	}
}

// 语义保持：key 存在且带 TTL 才写，写完 TTL 不变；key 不存在或没有 TTL 时不写。
func TestRedisHashPartialWrite_OnlyWritesLiveKeysAndKeepsTTL(t *testing.T) {
	mr := withMiniRedis(t)

	mr.HSet("live", "Quota", "100")
	mr.SetTTL("live", time.Minute)
	require.NoError(t, RedisHIncrBy("live", "Quota", -30))
	require.NoError(t, RedisHSetField("live", "Status", "2"))
	require.Equal(t, "70", mr.HGet("live", "Quota"))
	require.Equal(t, "2", mr.HGet("live", "Status"))
	require.Equal(t, time.Minute, mr.TTL("live"), "局部写入不得改动 TTL")

	require.NoError(t, RedisHIncrBy("missing", "Quota", 5))
	require.NoError(t, RedisHSetField("missing", "Quota", "5"))
	require.False(t, mr.Exists("missing"), "key 不存在时不得新建")

	mr.HSet("persistent", "Quota", "100") // 无 TTL：与旧实现一致，不写
	require.NoError(t, RedisHIncrBy("persistent", "Quota", -30))
	require.Equal(t, "100", mr.HGet("persistent", "Quota"))
}
