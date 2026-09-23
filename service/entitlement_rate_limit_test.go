package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

// 权益速率限制。两条实现（Redis 滑动窗口 / 进程内滑动窗口）必须同一个语义：
// 下面的「两条路径同一组断言」守的就是这件事——此前 Redis 路径用的是令牌桶，任意
// 窗口最多放行 2 × rpm，而测试只覆盖了进程内那条，于是两边悄悄不一致。

// withRedis 把 common.RDB 换成 miniredis，走 Redis 路径。
func withRedis(t *testing.T) *miniredis.Miniredis {
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

// 同一组断言分别跑在两条路径上。进程内实现用真实时钟、没法快进，所以这里只断言
// 窗口内的行为；窗口滑动由 common/limiter 的测试守。
func assertRPMSemantics(t *testing.T, userBase int) {
	t.Helper()
	for i := 0; i < 3; i++ {
		require.True(t, allowEntitlementRequest(userBase, 1, 3), "第 %d 次应放行", i+1)
	}
	require.False(t, allowEntitlementRequest(userBase, 1, 3), "窗口内第 4 次")
	require.True(t, allowEntitlementRequest(userBase+1, 1, 3), "别的用户不受影响")
	require.True(t, allowEntitlementRequest(userBase, 2, 3), "别的权益不受影响")
	require.True(t, allowEntitlementRequest(userBase, 3, 0), "0 表示不限")
}

func TestAllowEntitlementRequest_MemoryAndRedisAgree(t *testing.T) {
	t.Run("进程内", func(t *testing.T) { assertRPMSemantics(t, 95101) })
	t.Run("Redis", func(t *testing.T) {
		withRedis(t)
		assertRPMSemantics(t, 95201)
	})
}

// Redis 路径的硬上限：半个窗口后仍在窗口内，必须拒绝（令牌桶会在这里放行）
func TestAllowEntitlementRequest_RedisHardCap(t *testing.T) {
	mr := withRedis(t)
	t0 := time.Unix(1_800_000_000, 0)
	mr.SetTime(t0)
	for i := 0; i < 3; i++ {
		require.True(t, allowEntitlementRequest(95301, 1, 3))
	}
	mr.SetTime(t0.Add(30 * time.Second))
	require.False(t, allowEntitlementRequest(95301, 1, 3))
	mr.SetTime(t0.Add(60 * time.Second))
	require.True(t, allowEntitlementRequest(95301, 1, 3))
}

// Redis 故障按「不放行」处理：用户这次付原价，而不是让限流形同虚设
func TestAllowEntitlementRequest_RedisErrorFallsBackToPaid(t *testing.T) {
	mr := withRedis(t)
	mr.Close()
	require.False(t, allowEntitlementRequest(95401, 1, 3))
}

func TestAllowEntitlementRequest_CapsPerUserPerEntitlement(t *testing.T) {
	for i := 0; i < 3; i++ {
		require.True(t, allowEntitlementRequest(95001, 1, 3), "第 %d 次应放行", i+1)
	}
	require.False(t, allowEntitlementRequest(95001, 1, 3), "一分钟内第 4 次超出 3 RPM")

	// 桶按「用户 × 权益」隔离：别人的、别的权益的不受影响
	require.True(t, allowEntitlementRequest(95002, 1, 3))
	require.True(t, allowEntitlementRequest(95001, 2, 3))
}

func TestAllowEntitlementRequest_ZeroMeansUnlimited(t *testing.T) {
	for i := 0; i < 100; i++ {
		require.True(t, allowEntitlementRequest(95003, 1, 0))
	}
}

// 超出速率 → 降级按余额计费，而不是拒绝；并且不占次数、不扣点。
func TestNewBillingSession_FallsBackWhenRateLimited(t *testing.T) {
	truncate(t)
	seedBillingUser(t, 95010, 1000000, 0)
	seedMatchableEntitlement(t, 95010, 10, 100000)
	require.NoError(t, model.DB.Model(&model.SubscriptionPlanEntitlement{}).
		Where("models = ?", "gpt-test").Update("rate_limit_rpm", 2).Error)

	for i := 0; i < 2; i++ {
		session, apiErr := NewBillingSession(newTestGinContext(1), relayInfoForModel(95010, "gpt-test"), 1000)
		require.Nil(t, apiErr)
		require.Equal(t, BillingSourceEntitlement, session.funding.Source())
	}

	info := relayInfoForModel(95010, "gpt-test")
	session, apiErr := NewBillingSession(newTestGinContext(1), info, 1000)
	require.Nil(t, apiErr, "超出速率不该让请求失败")
	require.Equal(t, BillingSourceWallet, session.funding.Source())

	fb := info.EntitlementFallback
	require.NotNil(t, fb)
	require.Equal(t, EntitlementFallbackRateLimited, fb.Reason)
	require.Equal(t, 2, fb.RateLimitRPM)

	var counter model.UserSubscriptionEntitlement
	require.NoError(t, model.DB.Where("user_id = ?", 95010).First(&counter).Error)
	require.Equal(t, int64(2), counter.UsedCount, "被限速的那次不能占用次数")
}
