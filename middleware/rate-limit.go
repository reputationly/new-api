package middleware

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

var timeFormat = "2006-01-02T15:04:05.000Z"

var inMemoryRateLimiter common.InMemoryRateLimiter

var defNext = func(c *gin.Context) {
	c.Next()
}

// 降级后需连续这么久没有 Redis 限流错误才记"恢复"，避免时好时坏或单个坏 key 时成对刷日志。
var redisRateLimitRecoverQuietPeriod = 30 * time.Second

var redisRateLimitDegraded atomic.Bool
var redisRateLimitLastErrorNano atomic.Int64

func noteRedisRateLimitError(err error) {
	redisRateLimitLastErrorNano.Store(time.Now().UnixNano())
	if redisRateLimitDegraded.CompareAndSwap(false, true) {
		common.SysError("rate limit: redis unavailable, falling back to per-pod in-memory limiting: " + err.Error())
	}
}

func noteRedisRateLimitOK() {
	if !redisRateLimitDegraded.Load() ||
		time.Since(time.Unix(0, redisRateLimitLastErrorNano.Load())) < redisRateLimitRecoverQuietPeriod {
		return
	}
	if redisRateLimitDegraded.CompareAndSwap(true, false) {
		common.SysLog("rate limit: redis recovered, back to redis limiting")
	}
}

// redisListRateLimit 列表滑动窗口限流。err != nil 表示 Redis 不可用（或数据损坏），
// 这次没能判定，由调用方降级到内存限流。算法与原 redisRateLimiter 完全一致。
func redisListRateLimit(ctx context.Context, key string, maxRequestNum int, duration int64) (bool, error) {
	rdb := common.RDB
	listLength, err := rdb.LLen(ctx, key).Result()
	if err != nil {
		return false, err
	}
	if listLength < int64(maxRequestNum) {
		rdb.LPush(ctx, key, time.Now().Format(timeFormat))
		rdb.Expire(ctx, key, common.RateLimitKeyExpirationDuration)
		return true, nil
	}

	oldTimeStr, err := rdb.LIndex(ctx, key, -1).Result()
	if err != nil {
		return false, err
	}
	oldTime, err := time.Parse(timeFormat, oldTimeStr)
	if err != nil {
		return false, err
	}
	nowTimeStr := time.Now().Format(timeFormat)
	nowTime, err := time.Parse(timeFormat, nowTimeStr)
	if err != nil {
		return false, err
	}
	// time.Since will return negative number!
	// See: https://stackoverflow.com/questions/50970900/why-is-time-since-returning-negative-durations-on-windows
	if int64(nowTime.Sub(oldTime).Seconds()) < duration {
		rdb.Expire(ctx, key, common.RateLimitKeyExpirationDuration)
		return false, nil
	}
	rdb.LPush(ctx, key, time.Now().Format(timeFormat))
	rdb.LTrim(ctx, key, 0, int64(maxRequestNum-1))
	rdb.Expire(ctx, key, common.RateLimitKeyExpirationDuration)
	return true, nil
}

func redisRateLimiter(c *gin.Context, maxRequestNum int, duration int64, mark string) {
	allowed, err := redisListRateLimit(context.Background(), "rateLimit:"+mark+c.ClientIP(), maxRequestNum, duration)
	if err != nil {
		noteRedisRateLimitError(err)
		memoryRateLimiter(c, maxRequestNum, duration, mark)
		return
	}
	noteRedisRateLimitOK()
	if !allowed {
		c.Status(http.StatusTooManyRequests)
		c.Abort()
	}
}

func memoryRateLimiter(c *gin.Context, maxRequestNum int, duration int64, mark string) {
	key := mark + c.ClientIP()
	if !inMemoryRateLimiter.Request(key, maxRequestNum, duration) {
		c.Status(http.StatusTooManyRequests)
		c.Abort()
		return
	}
}

func rateLimitFactory(maxRequestNum int, duration int64, mark string) func(c *gin.Context) {
	// It's safe to call multi times.
	inMemoryRateLimiter.Init(common.RateLimitKeyExpirationDuration)
	if common.RedisEnabled {
		return func(c *gin.Context) {
			redisRateLimiter(c, maxRequestNum, duration, mark)
		}
	}
	return func(c *gin.Context) {
		memoryRateLimiter(c, maxRequestNum, duration, mark)
	}
}

func GlobalWebRateLimit() func(c *gin.Context) {
	if common.GlobalWebRateLimitEnable {
		return rateLimitFactory(common.GlobalWebRateLimitNum, common.GlobalWebRateLimitDuration, "GW")
	}
	return defNext
}

func GlobalAPIRateLimit() func(c *gin.Context) {
	if common.GlobalApiRateLimitEnable {
		return rateLimitFactory(common.GlobalApiRateLimitNum, common.GlobalApiRateLimitDuration, "GA")
	}
	return defNext
}

// HiloRateLimit 给 MiniMax Design 客户端那组**匿名**接口单独一个桶。
//
// 配额沿用 `GlobalApiRateLimit`，但 mark 用 "HL" 而不是 "GA" —— 桶的 key 是
// `mark + ClientIP`，共用 "GA" 的话这组会和整个 dashboard/user API 抢同一份
// 每 IP 配额。
//
// **这不是理论问题**:桌面客户端在轮询 `/api/v1/models/config`，而公网部署下
// 它和同一个用户的浏览器往往走同一个出口 IP —— 轮询把配额吃掉，浏览器那边
// 正常的 `/api/*` 就开始 429，而且看不出和客户端有什么关系。
func HiloRateLimit() func(c *gin.Context) {
	if common.GlobalApiRateLimitEnable {
		return rateLimitFactory(common.GlobalApiRateLimitNum, common.GlobalApiRateLimitDuration, "HL")
	}
	return defNext
}

// OvRateLimit 给 `/v1/ov/*` 单独一个桶，并在拒绝时给出**带错误码的 JSON 体**。
//
// 单独一个桶（mark "OV"）的理由同 HiloRateLimit：桶的 key 是 `mark + ClientIP`，
// 与 dashboard/user API 共用 "GA" 的话，能力目录的轮询会把同一出口 IP 下浏览器的
// 正常请求挤成 429。
//
// 与 Hilo/Global 那两条的差别只有拒绝时的响应体：那两条 `c.Status(429)` 是**空体**，
// 客户端分不出"被限流"和"服务出错"（见 ovaijisuandesign 的 L-4）。这里补一个
// `rate_limited` 码，仍然只用 3.0 兼容的 list 原语（复用同一个 limiter）。
// OvRateLimit `/v1/ov/*` 的限流。**跟随总开关**（与 HiloRateLimit 同口径）：
// 管理员关掉 API 限流后，这个新命名空间不该还偷偷限。
func OvRateLimit() func(c *gin.Context) {
	if !common.GlobalApiRateLimitEnable {
		return defNext
	}
	limit := rateLimitFactory(common.GlobalApiRateLimitNum, common.GlobalApiRateLimitDuration, "OV")
	return func(c *gin.Context) {
		limit(c)
		if !c.IsAborted() {
			return
		}
		// 限流器只写过状态码（gin 的 WriteHeader 在真正 Write 之前不会落盘），
		// 所以这里还能补一个 JSON 体。500（Redis 故障）不补 —— 那不是限流。
		if c.Writer.Status() == http.StatusTooManyRequests {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error": gin.H{
					"message": common.MessageWithRequestId("请求过于频繁，请稍后重试", c.GetString(common.RequestIdKey)),
					"type":    "new_api_error",
					"code":    string(types.ErrorCodeRateLimited),
				},
			})
		}
	}
}

func CriticalRateLimit() func(c *gin.Context) {
	if common.CriticalRateLimitEnable {
		return rateLimitFactory(common.CriticalRateLimitNum, common.CriticalRateLimitDuration, "CT")
	}
	return defNext
}

// AssistantFeedbackRateLimit 给小助手反馈单独一个桶，配额沿用 CriticalRateLimit。
// 理由同 HiloRateLimit：桶 key 是 mark + ClientIP，共用 "CT" 的话反馈流量和
// 工单/充值等关键写操作会互相挤占——公网反馈被刷时，同一出口 IP 下的其他写操作
// 会一起 429。
func AssistantFeedbackRateLimit() func(c *gin.Context) {
	if common.CriticalRateLimitEnable {
		return rateLimitFactory(common.CriticalRateLimitNum, common.CriticalRateLimitDuration, "AF")
	}
	return defNext
}

func DownloadRateLimit() func(c *gin.Context) {
	return rateLimitFactory(common.DownloadRateLimitNum, common.DownloadRateLimitDuration, "DW")
}

func UploadRateLimit() func(c *gin.Context) {
	return rateLimitFactory(common.UploadRateLimitNum, common.UploadRateLimitDuration, "UP")
}

// userRateLimitFactory creates a rate limiter keyed by authenticated user ID
// instead of client IP, making it resistant to proxy rotation attacks.
// Must be used AFTER authentication middleware (UserAuth).
func userRateLimitFactory(maxRequestNum int, duration int64, mark string) func(c *gin.Context) {
	// It's safe to call multi times.
	inMemoryRateLimiter.Init(common.RateLimitKeyExpirationDuration)
	if common.RedisEnabled {
		return func(c *gin.Context) {
			userId := c.GetInt("id")
			if userId == 0 {
				c.Status(http.StatusUnauthorized)
				c.Abort()
				return
			}
			redisKey := fmt.Sprintf("rateLimit:%s:user:%d", mark, userId)
			memoryKey := fmt.Sprintf("%s:user:%d", mark, userId)
			userRedisRateLimiter(c, maxRequestNum, duration, redisKey, memoryKey)
		}
	}
	return func(c *gin.Context) {
		userId := c.GetInt("id")
		if userId == 0 {
			c.Status(http.StatusUnauthorized)
			c.Abort()
			return
		}
		key := fmt.Sprintf("%s:user:%d", mark, userId)
		if !inMemoryRateLimiter.Request(key, maxRequestNum, duration) {
			c.Status(http.StatusTooManyRequests)
			c.Abort()
			return
		}
	}
}

// userRedisRateLimiter is like redisRateLimiter but accepts pre-built Redis and
// in-memory keys (to support user-ID-based keys).
func userRedisRateLimiter(c *gin.Context, maxRequestNum int, duration int64, redisKey, memoryKey string) {
	allowed, err := redisListRateLimit(context.Background(), redisKey, maxRequestNum, duration)
	if err != nil {
		noteRedisRateLimitError(err)
		if !inMemoryRateLimiter.Request(memoryKey, maxRequestNum, duration) {
			c.Status(http.StatusTooManyRequests)
			c.Abort()
		}
		return
	}
	noteRedisRateLimitOK()
	if !allowed {
		c.Status(http.StatusTooManyRequests)
		c.Abort()
	}
}

// SearchRateLimit returns a per-user rate limiter for search endpoints.
// Configurable via SEARCH_RATE_LIMIT_ENABLE / SEARCH_RATE_LIMIT / SEARCH_RATE_LIMIT_DURATION.
func SearchRateLimit() func(c *gin.Context) {
	if !common.SearchRateLimitEnable {
		return defNext
	}
	return userRateLimitFactory(common.SearchRateLimitNum, common.SearchRateLimitDuration, "SR")
}
