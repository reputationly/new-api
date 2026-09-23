package limiter

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
)

func newTestRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return mr, rdb
}

func allow(t *testing.T, rdb *redis.Client, key string, limit int) bool {
	t.Helper()
	ok, err := AllowSlidingWindow(context.Background(), rdb, key, limit, 60)
	require.NoError(t, err)
	return ok
}

// 任意 60 秒内最多 limit 次——硬上限。
//
// 半个窗口后那一次是关键：令牌桶此时已经补回了令牌会放行（任意窗口最多 2 × limit），
// 滑动窗口必须拒绝。
func TestAllowSlidingWindow_HardCapWithinWindow(t *testing.T) {
	mr, rdb := newTestRedis(t)
	t0 := time.Unix(1_800_000_000, 0)
	mr.SetTime(t0)

	for i := 0; i < 3; i++ {
		require.True(t, allow(t, rdb, "k", 3), "第 %d 次应放行", i+1)
	}
	require.False(t, allow(t, rdb, "k", 3), "窗口内第 4 次")

	mr.SetTime(t0.Add(30 * time.Second))
	require.False(t, allow(t, rdb, "k", 3), "半个窗口后仍在窗口内，不能因为「补充」而放行")

	// 恰好一个窗口后，最早那几次过期
	mr.SetTime(t0.Add(60 * time.Second))
	require.True(t, allow(t, rdb, "k", 3))
}

// 被拒的请求不计数：否则持续超限的调用方会把自己永远锁死在窗口里
func TestAllowSlidingWindow_RejectedNotCounted(t *testing.T) {
	mr, rdb := newTestRedis(t)
	t0 := time.Unix(1_800_000_000, 0)
	mr.SetTime(t0)
	require.True(t, allow(t, rdb, "k", 1))
	// 被拒的发生在窗口中段：若被计数，它们会在 t0+60 时仍留在窗口里继续挡人
	mr.SetTime(t0.Add(30 * time.Second))
	for i := 0; i < 5; i++ {
		require.False(t, allow(t, rdb, "k", 1))
	}
	mr.SetTime(t0.Add(60 * time.Second))
	require.True(t, allow(t, rdb, "k", 1), "放行的那次已过期，被拒的不该占位")
}

func TestAllowSlidingWindow_KeysIndependentAndZeroUnlimited(t *testing.T) {
	_, rdb := newTestRedis(t)
	require.True(t, allow(t, rdb, "a", 1))
	require.False(t, allow(t, rdb, "a", 1))
	require.True(t, allow(t, rdb, "b", 1))
	for i := 0; i < 10; i++ {
		require.True(t, allow(t, rdb, "c", 0))
	}
}

// 同一时刻的多次请求不能互相覆盖成员而少计
func TestAllowSlidingWindow_SameInstantCountsEach(t *testing.T) {
	mr, rdb := newTestRedis(t)
	mr.SetTime(time.Unix(1_800_000_000, 0))
	for i := 0; i < 5; i++ {
		require.True(t, allow(t, rdb, "k", 5))
	}
	require.False(t, allow(t, rdb, "k", 5))
}
