package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/common/limiter"
)

// 权益速率限制（设计文档 §5.1、P7）。
//
// 「不限次」「不消耗算力点」都不等于零成本——自有算力同样会被挤占。速率限制是这两类
// 权益唯一的兜底：每个用户在每条权益上每分钟最多命中 RateLimitRPM 次。
//
// 超出后**降级按余额计费**，而不是拒绝请求：与次数用尽、点数不足同一个语义——权益是
// 优惠不是准入门槛，超出速率的部分按原价付费。保护自有 GPU 总容量是全局限流
// （middleware.ModelRequestRateLimit）与渠道侧的事，不在这里。
//
// 有 Redis 用 common/limiter 的滑动窗口（多实例共享计数），没有就退回进程内滑动窗口。
// 两条路径同一个语义：任意 60 秒内最多 rpm 次。**不用令牌桶**：桶初始是满的、又在持续
// 补充，任意窗口内最多能放行 2 × rpm——全局限流用的就是它，对允许突发的全局限流可以
// 接受，对「免费用量的唯一兜底」不行。

const entitlementRPMWindowSeconds = 60

var (
	entitlementMemoryLimiter     common.InMemoryRateLimiter
	entitlementMemoryLimiterOnce sync.Once
)

func entitlementRPMKey(userId, entitlementId int) string {
	return fmt.Sprintf("entitlement_rpm:%d:%d", userId, entitlementId)
}

// allowEntitlementRequest 占用一次该用户在该权益上的速率额度。rpm<=0 表示不限。
//
// 放在预扣之前：预扣随后因点数不足失败时，这一次额度不会退回。可接受——那种情况下
// 请求本来就降级走余额了，后续请求多半也一样；为它引入「先扣后退」只会多一条出错路径。
//
// Redis 出错时按「不放行」处理，也就是降级按余额计费：错判的代价是用户这一次付了
// 原价，而放行的代价是限流形同虚设。与「缓存故障不可放行免费请求」同一原则（§十一 3）。
func allowEntitlementRequest(userId, entitlementId, rpm int) bool {
	if rpm <= 0 {
		return true
	}
	key := entitlementRPMKey(userId, entitlementId)
	if common.RedisEnabled && common.RDB != nil {
		allowed, err := limiter.AllowSlidingWindow(context.Background(), common.RDB,
			key, rpm, entitlementRPMWindowSeconds)
		if err != nil {
			common.SysLog("entitlement rate limit check failed, falling back to paid: " + err.Error())
			return false
		}
		return allowed
	}
	entitlementMemoryLimiterOnce.Do(func() {
		entitlementMemoryLimiter.Init(2 * time.Minute)
	})
	return entitlementMemoryLimiter.Request(key, rpm, entitlementRPMWindowSeconds)
}
