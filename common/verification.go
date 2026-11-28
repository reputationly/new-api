package common

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
)

const (
	EmailVerificationPurpose = "v"
	PasswordResetPurpose     = "r"

	// VerificationMaxAttempts 单个验证码允许的最大校验次数，超过即作废。
	// 6 位码的取值空间很小，没有试错上限就等于没有验证码。
	VerificationMaxAttempts = 5

	verificationKeyPrefix    = "verification:"
	verificationCodeInfix    = ":code:"
	verificationAttemptInfix = ":try:"

	// 内存降级的清扫阈值：超过后顺手把已过期的条目删掉，避免 map 无限增长。
	// 这条路径只在 Redis 未启用（本地开发/单机）或 Redis 故障时才会用到。
	verificationMemorySweepThreshold = 1024

	// 单次 Redis 调用的超时。验证码校验处在用户当面等待的同步链路上，
	// 宁可快速降级到内存，也不要把请求吊在这儿。
	verificationRedisTimeout = 500 * time.Millisecond
)

// VerificationValidMinutes 验证码 / 重置链接的有效期（分钟）。
// 同时被 controller/misc.go 用于渲染邮件正文里的提示文案，改这个值文案会跟着变。
var VerificationValidMinutes = 10

// verificationVerifyScript 原子地完成「比对 + 删除」。
//
// 必须是原子的：否则两个并发请求可以拿同一个码各通过一次，验证码就不再是一次性的。
// 脚本里只用 GET / DEL 两个确定性命令——Redis 3.0 会拒绝「非确定性命令之后接写操作」，
// 所以这里（以及任何验证码 Lua）都不能出现 TIME。
var verificationVerifyScript = redis.NewScript(`
local stored = redis.call('GET', KEYS[1])
if not stored then
	return 0
end
if stored ~= ARGV[1] then
	return -1
end
redis.call('DEL', KEYS[1])
return 1
`)

type verificationRecord struct {
	digest   string
	expireAt time.Time
	attempts int
}

var (
	verificationMutex sync.Mutex
	verificationMap   map[string]verificationRecord
)

// verificationRedisDegraded 只用于压制降级日志：Redis 抖一下不能让每个请求都刷一行错误，
// 与 middleware/rate-limit.go 的处理保持一致。
var verificationRedisDegraded atomic.Bool

// GenerateVerificationCode 生成验证码 / 一次性令牌。
//
// 底层是 uuid v4（crypto/rand），不是伪随机。length == 0 时返回完整的 32 位十六进制串，
// 用于重置密码链接里的令牌；否则截取前 length 位。
func GenerateVerificationCode(length int) string {
	code := uuid.New().String()
	code = strings.Replace(code, "-", "", -1)
	if length == 0 {
		return code
	}
	return code[:length]
}

// RegisterVerificationCodeWithKey 写入验证码，同时清空该 key 的试错计数。
//
// 清计数是「重发即作废旧码」的一部分：用户重发之后拿到的是一张干净的额度，
// 不会被上一个码打满的失败次数连坐。
func RegisterVerificationCodeWithKey(key string, code string, purpose string) {
	digest := hashVerificationCode(key, code)
	ttl := verificationTTL()

	if verificationRedisAvailable() {
		if err := storeVerificationCodeInRedis(purpose, key, digest); err != nil {
			noteVerificationRedisDegraded(err)
		} else {
			noteVerificationRedisRecovered()
			return
		}
	}

	// Redis 未启用，或本次写入失败：落到进程内存。
	// 多副本下这条路径不保证跨 Pod 可见（这正是它只能是降级兜底的原因）。
	verificationMutex.Lock()
	defer verificationMutex.Unlock()
	verificationMap[purpose+key] = verificationRecord{
		digest:   digest,
		expireAt: time.Now().Add(ttl),
	}
	if len(verificationMap) > verificationMemorySweepThreshold {
		removeExpiredVerificationLocked(time.Now())
	}
}

// VerifyCodeWithKey 校验验证码。校验通过后该码立即失效，不可重复使用。
func VerifyCodeWithKey(key string, code string, purpose string) bool {
	digest := hashVerificationCode(key, code)

	if verificationRedisAvailable() {
		ok, err := verifyCodeWithRedis(key, digest, purpose)
		if err == nil {
			noteVerificationRedisRecovered()
			return ok
		}
		noteVerificationRedisDegraded(err)
	}
	return verifyCodeWithMemory(key, digest, purpose)
}

// storeVerificationCodeInRedis 写入验证码，并清空该 key 的试错计数（重发即换新额度）。
//
// 计数清除失败不返回错误：最坏只是让用户少几次重试机会，不值得把整次发送判为失败。
func storeVerificationCodeInRedis(purpose string, key string, digest string) error {
	ctx, cancel := context.WithTimeout(context.Background(), verificationRedisTimeout)
	defer cancel()

	if err := RDB.Set(ctx, verificationCodeKey(purpose, key), digest, verificationTTL()).Err(); err != nil {
		return err
	}
	_ = RDB.Del(ctx, verificationAttemptKey(purpose, key)).Err()
	return nil
}

func deleteVerificationCodeInRedis(purpose string, key string) error {
	ctx, cancel := context.WithTimeout(context.Background(), verificationRedisTimeout)
	defer cancel()

	return RDB.Del(ctx,
		verificationCodeKey(purpose, key),
		verificationAttemptKey(purpose, key),
	).Err()
}

func verifyCodeWithRedis(key string, digest string, purpose string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), verificationRedisTimeout)
	defer cancel()

	attemptKey := verificationAttemptKey(purpose, key)
	attempts, err := RDB.Incr(ctx, attemptKey).Result()
	if err != nil {
		return false, err
	}
	if attempts == 1 {
		// 只在第一次校验时设过期，让计数和验证码同寿；码过期后计数自然消失。
		if err := RDB.Expire(ctx, attemptKey, verificationTTL()).Err(); err != nil {
			return false, err
		}
	}
	if attempts > VerificationMaxAttempts {
		// 已经试爆：连码带计数一起清掉，后续请求一律失败，逼用户重新获取。
		_ = RDB.Del(ctx, verificationCodeKey(purpose, key), attemptKey).Err()
		return false, nil
	}

	result, err := verificationVerifyScript.Run(ctx, RDB,
		[]string{verificationCodeKey(purpose, key)}, digest).Int()
	if err != nil {
		return false, err
	}
	if result == 1 {
		_ = RDB.Del(ctx, attemptKey).Err()
		return true, nil
	}
	return false, nil
}

// verifyCodeWithMemory 与 Redis 路径逐条对齐语义：先计次、超限删除、通过即删。
func verifyCodeWithMemory(key string, digest string, purpose string) bool {
	mapKey := purpose + key

	verificationMutex.Lock()
	defer verificationMutex.Unlock()

	record, ok := verificationMap[mapKey]
	if !ok {
		return false
	}
	now := time.Now()
	if !now.Before(record.expireAt) {
		delete(verificationMap, mapKey)
		return false
	}

	record.attempts++
	if record.attempts > VerificationMaxAttempts {
		delete(verificationMap, mapKey)
		return false
	}
	// 定长比较，避免用 == 比较摘要时的时序差异。
	if subtle.ConstantTimeCompare([]byte(record.digest), []byte(digest)) != 1 {
		verificationMap[mapKey] = record
		return false
	}
	delete(verificationMap, mapKey)
	return true
}

// DeleteKey 作废某个 key 下的验证码（发送方主动撤销时用）。
func DeleteKey(key string, purpose string) {
	if verificationRedisAvailable() {
		if err := deleteVerificationCodeInRedis(purpose, key); err != nil {
			noteVerificationRedisDegraded(err)
		}
	}

	verificationMutex.Lock()
	defer verificationMutex.Unlock()
	delete(verificationMap, purpose+key)
}

func verificationTTL() time.Duration {
	return time.Duration(VerificationValidMinutes) * time.Minute
}

// hashVerificationCode 加 key 作前缀再摘要。
//
// key（邮箱/手机号）参与摘要，是为了让同一个码在不同账号下得到不同摘要，
// 避免一份摘要被跨账号复用。真正的防线是有效期、试错上限和一次性使用——
// 6 位码的取值空间本来就不足以靠哈希本身对抗离线穷举。
//
// 输入先做大小写与首尾空白归一：验证码是十六进制串，用户从邮件里抄成大写、
// 或者粘贴时带进来一个空格，语义上都没错。这一条与 VerificationMaxAttempts
// 配套——有了试错上限之后，一次手误就是真扣一次额度，不能再让格式差异占额度。
func hashVerificationCode(key string, code string) string {
	normalized := strings.ToLower(strings.TrimSpace(code))
	sum := sha256.Sum256([]byte(key + "\x00" + normalized))
	return hex.EncodeToString(sum[:])
}

func verificationCodeKey(purpose string, key string) string {
	return verificationKeyPrefix + purpose + verificationCodeInfix + key
}

func verificationAttemptKey(purpose string, key string) string {
	return verificationKeyPrefix + purpose + verificationAttemptInfix + key
}

func verificationRedisAvailable() bool {
	return RedisEnabled && RDB != nil
}

func noteVerificationRedisDegraded(err error) {
	if verificationRedisDegraded.CompareAndSwap(false, true) {
		SysError("verification code: redis unavailable, degraded to per-pod memory storage; " +
			"verification will fail when the follow-up request lands on another replica: " + err.Error())
	}
}

func noteVerificationRedisRecovered() {
	if verificationRedisDegraded.CompareAndSwap(true, false) {
		SysLog("verification code: redis recovered")
	}
}

// removeExpiredVerificationLocked 调用方必须已持有 verificationMutex。
func removeExpiredVerificationLocked(now time.Time) {
	for key, record := range verificationMap {
		if !now.Before(record.expireAt) {
			delete(verificationMap, key)
		}
	}
}

func init() {
	verificationMutex.Lock()
	defer verificationMutex.Unlock()
	verificationMap = make(map[string]verificationRecord)
}
