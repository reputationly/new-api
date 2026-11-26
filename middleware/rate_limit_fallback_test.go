package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
)

const rateLimitFallbackClientIP = "192.0.2.1"

func setupRateLimitFallbackRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	mr := miniredis.RunT(t)
	prevRDB := common.RDB
	prevRedisEnabled := common.RedisEnabled
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	common.RDB = client
	common.RedisEnabled = true
	t.Cleanup(func() {
		_ = client.Close()
		common.RDB = prevRDB
		common.RedisEnabled = prevRedisEnabled
	})
	return mr
}

func resetRateLimitFallbackState(t *testing.T) {
	t.Helper()
	inMemoryRateLimiter = common.InMemoryRateLimiter{}
	redisRateLimitDegraded.Store(false)
	t.Cleanup(func() {
		inMemoryRateLimiter = common.InMemoryRateLimiter{}
		redisRateLimitDegraded.Store(false)
	})
}

func newRateLimitTestRouter(handlers ...gin.HandlerFunc) *gin.Engine {
	all := append([]gin.HandlerFunc{}, handlers...)
	all = append(all, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	r := gin.New()
	r.GET("/", all...)
	return r
}

func rateLimitTestRequest(r *gin.Engine) int {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = rateLimitFallbackClientIP + ":1234"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

func setRateLimitTestUserID(id int) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("id", id)
	}
}

func TestRateLimitFallbackM1RedisNormal(t *testing.T) {
	mr := setupRateLimitFallbackRedis(t)
	resetRateLimitFallbackState(t)

	r := newRateLimitTestRouter(rateLimitFactory(3, 60, "T1"))
	for i := 0; i < 3; i++ {
		if got := rateLimitTestRequest(r); got != http.StatusOK {
			t.Fatalf("request %d status = %d, want 200", i+1, got)
		}
	}
	if got := rateLimitTestRequest(r); got != http.StatusTooManyRequests {
		t.Fatalf("request 4 status = %d, want 429", got)
	}
	if !mr.Exists("rateLimit:T1" + rateLimitFallbackClientIP) {
		t.Fatal("redis rate limit key does not exist")
	}
}

func TestRateLimitFallbackM2RedisErrorFallsBackToMemory(t *testing.T) {
	mr := setupRateLimitFallbackRedis(t)
	resetRateLimitFallbackState(t)

	r := newRateLimitTestRouter(rateLimitFactory(3, 60, "T2"))
	mr.SetError("boom")
	for i := 0; i < 3; i++ {
		if got := rateLimitTestRequest(r); got != http.StatusOK {
			t.Fatalf("request %d status = %d, want 200", i+1, got)
		}
	}
	if got := rateLimitTestRequest(r); got != http.StatusTooManyRequests {
		t.Fatalf("request 4 status = %d, want 429", got)
	}
	if !redisRateLimitDegraded.Load() {
		t.Fatal("redisRateLimitDegraded = false, want true")
	}
}

func TestRateLimitFallbackM3RedisRecovery(t *testing.T) {
	mr := setupRateLimitFallbackRedis(t)
	resetRateLimitFallbackState(t)

	r := newRateLimitTestRouter(rateLimitFactory(3, 60, "T3"))
	mr.SetError("boom")
	if got := rateLimitTestRequest(r); got != http.StatusOK {
		t.Fatalf("degraded request status = %d, want 200", got)
	}
	if !redisRateLimitDegraded.Load() {
		t.Fatal("redisRateLimitDegraded = false after redis error, want true")
	}

	mr.SetError("")
	if got := rateLimitTestRequest(r); got != http.StatusOK {
		t.Fatalf("recovered request status = %d, want 200", got)
	}
	if redisRateLimitDegraded.Load() {
		t.Fatal("redisRateLimitDegraded = true after recovery, want false")
	}
	if !mr.Exists("rateLimit:T3" + rateLimitFallbackClientIP) {
		t.Fatal("redis rate limit key does not exist after recovery")
	}
}

func TestRateLimitFallbackM4CorruptRedisDataFallsBack(t *testing.T) {
	mr := setupRateLimitFallbackRedis(t)
	resetRateLimitFallbackState(t)

	key := "rateLimit:T4" + rateLimitFallbackClientIP
	if _, err := mr.RPush(key, "bad", "bad", "bad"); err != nil {
		t.Fatalf("seed redis list: %v", err)
	}
	r := newRateLimitTestRouter(rateLimitFactory(3, 60, "T4"))
	if got := rateLimitTestRequest(r); got != http.StatusOK {
		t.Fatalf("status = %d, want 200", got)
	}
}

func TestRateLimitFallbackM5UserRateLimitRedisErrorFallsBack(t *testing.T) {
	mr := setupRateLimitFallbackRedis(t)
	resetRateLimitFallbackState(t)

	r := newRateLimitTestRouter(setRateLimitTestUserID(7), userRateLimitFactory(2, 60, "T5"))
	mr.SetError("boom")
	for i := 0; i < 2; i++ {
		if got := rateLimitTestRequest(r); got != http.StatusOK {
			t.Fatalf("request %d status = %d, want 200", i+1, got)
		}
	}
	if got := rateLimitTestRequest(r); got != http.StatusTooManyRequests {
		t.Fatalf("request 3 status = %d, want 429", got)
	}
}

func TestRateLimitFallbackM6ModelRateLimitRedisErrorFallsBack(t *testing.T) {
	mr := setupRateLimitFallbackRedis(t)
	resetRateLimitFallbackState(t)

	prevEnabled := setting.ModelRequestRateLimitEnabled
	prevDuration := setting.ModelRequestRateLimitDurationMinutes
	prevCount := setting.ModelRequestRateLimitCount
	prevSuccessCount := setting.ModelRequestRateLimitSuccessCount
	setting.ModelRequestRateLimitEnabled = true
	setting.ModelRequestRateLimitDurationMinutes = 1
	setting.ModelRequestRateLimitCount = 0
	setting.ModelRequestRateLimitSuccessCount = 2
	t.Cleanup(func() {
		setting.ModelRequestRateLimitEnabled = prevEnabled
		setting.ModelRequestRateLimitDurationMinutes = prevDuration
		setting.ModelRequestRateLimitCount = prevCount
		setting.ModelRequestRateLimitSuccessCount = prevSuccessCount
	})

	mr.SetError("boom")
	r := newRateLimitTestRouter(setRateLimitTestUserID(7), ModelRequestRateLimit())
	if got := rateLimitTestRequest(r); got != http.StatusOK {
		t.Fatalf("request 1 status = %d, want 200", got)
	}
	if got := rateLimitTestRequest(r); got != http.StatusOK {
		t.Fatalf("request 2 status = %d, want 200", got)
	}
	if got := rateLimitTestRequest(r); got != http.StatusTooManyRequests {
		t.Fatalf("request 3 status = %d, want 429", got)
	}
}

func TestRateLimitFallbackM7EmailVerificationRedisErrorDoesNotPanic(t *testing.T) {
	mr := setupRateLimitFallbackRedis(t)
	resetRateLimitFallbackState(t)

	mr.SetError("boom")
	r := newRateLimitTestRouter(EmailVerificationRateLimit())
	for i := 0; i < 2; i++ {
		if got := rateLimitTestRequest(r); got != http.StatusOK {
			t.Fatalf("request %d status = %d, want 200", i+1, got)
		}
	}
	if got := rateLimitTestRequest(r); got != http.StatusTooManyRequests {
		t.Fatalf("request 3 status = %d, want 429", got)
	}
}

func TestRateLimitFallbackM8RedisWindowSemantics(t *testing.T) {
	mr := setupRateLimitFallbackRedis(t)
	resetRateLimitFallbackState(t)

	withinKey := "rateLimit:T8W" + rateLimitFallbackClientIP
	within := time.Now().Add(-10 * time.Second).Format(timeFormat)
	if _, err := mr.RPush(withinKey, within, within, within); err != nil {
		t.Fatalf("seed within-window redis list: %v", err)
	}
	rWithin := newRateLimitTestRouter(rateLimitFactory(3, 60, "T8W"))
	if got := rateLimitTestRequest(rWithin); got != http.StatusTooManyRequests {
		t.Fatalf("within-window status = %d, want 429", got)
	}

	outsideKey := "rateLimit:T8O" + rateLimitFallbackClientIP
	outside := time.Now().Add(-120 * time.Second).Format(timeFormat)
	if _, err := mr.RPush(outsideKey, outside, outside, outside); err != nil {
		t.Fatalf("seed outside-window redis list: %v", err)
	}
	rOutside := newRateLimitTestRouter(rateLimitFactory(3, 60, "T8O"))
	if got := rateLimitTestRequest(rOutside); got != http.StatusOK {
		t.Fatalf("outside-window status = %d, want 200", got)
	}
}
