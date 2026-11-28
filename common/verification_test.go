package common

import (
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withVerificationRedis 起一个 miniredis 并把全局 RDB 指过去。
//
// 必须连 RedisEnabled 一起存还：内存降级用例会把它置 false，泄漏出去会让
// 同包其他用例悄悄走到内存路径上。
func withVerificationRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	mr := miniredis.RunT(t)
	prevRDB, prevEnabled := RDB, RedisEnabled
	RDB, RedisEnabled = redis.NewClient(&redis.Options{Addr: mr.Addr()}), true
	t.Cleanup(func() {
		_ = RDB.Close()
		RDB, RedisEnabled = prevRDB, prevEnabled
	})
	return mr
}

// withoutVerificationRedis 模拟「Redis 未启用」的部署（本地开发 / 单机）。
func withoutVerificationRedis(t *testing.T) {
	t.Helper()
	prevRDB, prevEnabled := RDB, RedisEnabled
	RDB, RedisEnabled = nil, false
	verificationMutex.Lock()
	verificationMap = make(map[string]verificationRecord)
	verificationMutex.Unlock()
	t.Cleanup(func() {
		RDB, RedisEnabled = prevRDB, prevEnabled
		verificationMutex.Lock()
		verificationMap = make(map[string]verificationRecord)
		verificationMutex.Unlock()
	})
}

func resetVerificationMemory() {
	verificationMutex.Lock()
	defer verificationMutex.Unlock()
	verificationMap = make(map[string]verificationRecord)
}

// 核心回归：发码和校验落在不同副本（不同进程、各自独立的内存）时，验证码必须仍然有效。
//
// 这正是线上 3 副本部署下「验证码错误」的成因——旧实现把码存在进程内存 map 里，
// 两个请求被负载均衡分到不同 Pod 就必然校验失败。
func TestVerificationCode_CrossReplicaIsVisible(t *testing.T) {
	mr := withVerificationRedis(t)
	const (
		email = "cross-replica@example.com"
		code  = "a1b2c3"
	)

	// Pod A：收到「发送验证码」请求。
	RegisterVerificationCodeWithKey(email, code, EmailVerificationPurpose)
	require.True(t, mr.Exists(verificationCodeKey(EmailVerificationPurpose, email)),
		"验证码必须写进共享存储（Redis），写进进程内存就注定跨副本不可见")

	// 清空本进程内存 = 模拟「提交注册」这次请求落到了另一个副本上：
	// 那个副本的进程内存是空的，能拿到验证码的唯一途径就是共享存储。
	resetVerificationMemory()

	// Pod B：收到「提交注册」请求，用的是另一个到同一 Redis 的连接。
	podB := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = podB.Close() })
	prev := RDB
	RDB = podB
	t.Cleanup(func() { RDB = prev })

	require.True(t, VerifyCodeWithKey(email, code, EmailVerificationPurpose),
		"发码与校验在不同副本时必须都能读到同一个验证码")
}

func TestVerificationCode_OneTimeUse(t *testing.T) {
	withVerificationRedis(t)
	const (
		email = "one-time@example.com"
		code  = "a1b2c3"
	)

	RegisterVerificationCodeWithKey(email, code, EmailVerificationPurpose)
	require.True(t, VerifyCodeWithKey(email, code, EmailVerificationPurpose))
	assert.False(t, VerifyCodeWithKey(email, code, EmailVerificationPurpose),
		"验证码用过一次后必须失效，否则同一个码能重复用（旧实现 10 分钟内可重放）")
}

// 旧实现的邮箱验证码在注册 / 邮箱绑定成功后都不会被删除，同一个码可以连续绑到多个账号。
func TestVerificationCode_CannotBeReplayedOnAnotherAccount(t *testing.T) {
	withVerificationRedis(t)
	const code = "ff00ff"

	RegisterVerificationCodeWithKey("first@example.com", code, EmailVerificationPurpose)
	require.True(t, VerifyCodeWithKey("first@example.com", code, EmailVerificationPurpose))
	require.False(t, VerifyCodeWithKey("first@example.com", code, EmailVerificationPurpose))
}

func TestVerificationCode_AttemptLimit(t *testing.T) {
	withVerificationRedis(t)
	const (
		email = "brute-force@example.com"
		code  = "a1b2c3"
	)

	RegisterVerificationCodeWithKey(email, code, EmailVerificationPurpose)
	for i := 0; i < VerificationMaxAttempts; i++ {
		require.False(t, VerifyCodeWithKey(email, "000000", EmailVerificationPurpose),
			"第 %d 次试错应当失败", i+1)
	}
	assert.False(t, VerifyCodeWithKey(email, code, EmailVerificationPurpose),
		"试错次数用尽后，即使给对了验证码也必须拒绝")
}

func TestVerificationCode_ResendResetsAttempts(t *testing.T) {
	withVerificationRedis(t)
	const email = "resend@example.com"

	RegisterVerificationCodeWithKey(email, "111111", EmailVerificationPurpose)
	for i := 0; i < VerificationMaxAttempts-1; i++ {
		require.False(t, VerifyCodeWithKey(email, "000000", EmailVerificationPurpose))
	}

	// 重发：旧码作废，计数清零，用户重新拿到完整额度。
	RegisterVerificationCodeWithKey(email, "222222", EmailVerificationPurpose)
	assert.False(t, VerifyCodeWithKey(email, "111111", EmailVerificationPurpose), "重发后旧码必须失效")
	assert.True(t, VerifyCodeWithKey(email, "222222", EmailVerificationPurpose))
}

func TestVerificationCode_ExpiresAfterTTL(t *testing.T) {
	mr := withVerificationRedis(t)
	const (
		email = "expired@example.com"
		code  = "a1b2c3"
	)

	RegisterVerificationCodeWithKey(email, code, EmailVerificationPurpose)
	mr.FastForward(verificationTTL() + time.Second)

	assert.False(t, VerifyCodeWithKey(email, code, EmailVerificationPurpose), "过期的验证码必须失效")
}

func TestVerificationCode_PurposeIsolation(t *testing.T) {
	withVerificationRedis(t)
	const (
		email = "purpose@example.com"
		code  = "a1b2c3"
	)

	RegisterVerificationCodeWithKey(email, code, EmailVerificationPurpose)
	assert.False(t, VerifyCodeWithKey(email, code, PasswordResetPurpose),
		"不同用途（注册 / 重置密码）之间必须互相隔离")
}

// 验证码不得以明文落库：即使 Redis 被导出，也不应直接读到可用的码。
func TestVerificationCode_StoredAsDigestNotPlaintext(t *testing.T) {
	mr := withVerificationRedis(t)
	const (
		email = "digest@example.com"
		code  = "a1b2c3"
	)

	RegisterVerificationCodeWithKey(email, code, EmailVerificationPurpose)
	stored, err := mr.Get(verificationCodeKey(EmailVerificationPurpose, email))
	require.NoError(t, err)
	assert.NotEqual(t, code, stored, "不能把明文验证码存进 Redis")
	assert.Len(t, stored, 64, "应存 sha256 十六进制摘要")
	assert.False(t, strings.Contains(stored, code))
}

// Redis 未启用时必须回落到进程内存，且语义与 Redis 路径一致——
// 否则本地开发和单机部署会跑在一套完全没有校验上限的实现上。
func TestVerificationCode_MemoryFallback(t *testing.T) {
	withoutVerificationRedis(t)
	const (
		email = "memory@example.com"
		code  = "a1b2c3"
	)

	RegisterVerificationCodeWithKey(email, code, EmailVerificationPurpose)

	assert.True(t, VerifyCodeWithKey(email, code, EmailVerificationPurpose))
	assert.False(t, VerifyCodeWithKey(email, code, EmailVerificationPurpose), "内存路径同样必须一次性")

	RegisterVerificationCodeWithKey(email, code, EmailVerificationPurpose)
	assert.False(t, VerifyCodeWithKey(email, code, PasswordResetPurpose), "内存路径同样按用途隔离")

	RegisterVerificationCodeWithKey(email, code, EmailVerificationPurpose)
	for i := 0; i < VerificationMaxAttempts; i++ {
		require.False(t, VerifyCodeWithKey(email, "000000", EmailVerificationPurpose))
	}
	assert.False(t, VerifyCodeWithKey(email, code, EmailVerificationPurpose), "内存路径同样有试错上限")
}

func TestVerificationCode_MemoryFallbackExpires(t *testing.T) {
	withoutVerificationRedis(t)
	prev := VerificationValidMinutes
	VerificationValidMinutes = 0
	t.Cleanup(func() { VerificationValidMinutes = prev })

	const (
		email = "memory-expired@example.com"
		code  = "a1b2c3"
	)
	RegisterVerificationCodeWithKey(email, code, EmailVerificationPurpose)
	assert.False(t, VerifyCodeWithKey(email, code, EmailVerificationPurpose))
}

func TestDeleteKey_RemovesCodeInBothStores(t *testing.T) {
	withVerificationRedis(t)
	const (
		email = "delete@example.com"
		code  = "a1b2c3"
	)

	RegisterVerificationCodeWithKey(email, code, EmailVerificationPurpose)
	DeleteKey(email, EmailVerificationPurpose)
	assert.False(t, VerifyCodeWithKey(email, code, EmailVerificationPurpose))

	resetVerificationMemory()
	RegisterVerificationCodeWithKey(email, code, EmailVerificationPurpose)
	DeleteKey(email, EmailVerificationPurpose)
	assert.False(t, VerifyCodeWithKey(email, code, EmailVerificationPurpose))
}

// 十六进制验证码的大小写与首尾空白不该占用户的试错额度。
//
// 每次断言前都要重新发码：验证码是一次性的，校验成功即失效。
func TestVerificationCode_CaseAndWhitespaceInsensitive(t *testing.T) {
	withVerificationRedis(t)
	const email = "normalize@example.com"

	RegisterVerificationCodeWithKey(email, "a1b2c3", EmailVerificationPurpose)
	assert.True(t, VerifyCodeWithKey(email, "A1B2C3", EmailVerificationPurpose), "大写必须认")

	RegisterVerificationCodeWithKey(email, "a1b2c3", EmailVerificationPurpose)
	assert.True(t, VerifyCodeWithKey(email, " a1b2c3 ", EmailVerificationPurpose), "首尾空白必须认")

	RegisterVerificationCodeWithKey(email, "A1B2C3", EmailVerificationPurpose)
	assert.True(t, VerifyCodeWithKey(email, "a1b2c3", EmailVerificationPurpose), "发码时的大小写也需归一化")
}
