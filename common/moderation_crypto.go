package common

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"sync"
)

// 审核原文加密。见 docs/content-moderation-design.md §10.1。
//
// 与 kyc_crypto.go 的关键差异：密钥缺失时**不生成随机密钥**。
// kyc_crypto.go:37 那条 WARNING 记着的坑是真实的——随机密钥意味着服务一重启，
// 此前写入的密文全部永久不可读，而调用方看到的是「加密成功」。违规原文是备案取证
// 材料，静默失效等于合规能力在某次重启后消失且无人察觉。
// 因此这里的策略是：密钥没配就明确不加密（返回 ErrModerationKeyMissing），
// 由调用方把 ContentEnc 留空、只存脱敏预览，宁可少存也不存解不开的东西。

var (
	moderationEncryptKey []byte
	moderationKeyOnce    sync.Once
	moderationKeyWarned  bool
)

// ErrModerationKeyMissing 表示 MODERATION_ENCRYPT_KEY 未配置或格式非法。
// 调用方应当据此跳过 ContentEnc 的写入，而不是当作普通错误重试。
var ErrModerationKeyMissing = errors.New("moderation: MODERATION_ENCRYPT_KEY not configured")

const moderationCipherPrefix = "modenc:"

// IsModerationCipher 报告一个值是否带本机制的密文标记。
// 调用方据此区分「解不开的密文」和「尚未加密的明文历史值」——
// 前者绝不能当凭证使用（对照 common.IsOBSCipher 的同款用途）。
func IsModerationCipher(v string) bool {
	return strings.HasPrefix(v, moderationCipherPrefix)
}

// InitModerationKey 从环境变量 MODERATION_ENCRYPT_KEY 读取 64 位 hex（32 字节）。
// 幂等，可在 main() 启动时调用一次让配置问题立刻暴露。
func InitModerationKey() {
	moderationKeyOnce.Do(func() {
		encHex := strings.TrimSpace(GetEnvOrDefaultString("MODERATION_ENCRYPT_KEY", ""))
		if len(encHex) != 64 {
			if encHex != "" {
				SysLog("WARNING: MODERATION_ENCRYPT_KEY 长度不是 64 位 hex，审核原文将不加密留存（仅存脱敏预览）")
				moderationKeyWarned = true
			}
			return
		}
		key, err := hex.DecodeString(encHex)
		if err != nil {
			SysLog("WARNING: MODERATION_ENCRYPT_KEY 不是合法 hex，审核原文将不加密留存（仅存脱敏预览）: " + err.Error())
			moderationKeyWarned = true
			return
		}
		moderationEncryptKey = key
	})
}

// ModerationKeyReady 报告是否具备加密能力。运营界面据此提示「原文留存未启用」。
func ModerationKeyReady() bool {
	InitModerationKey()
	return moderationEncryptKey != nil
}

// ModerationKeyMisconfigured 报告密钥配了但配错了（区别于压根没配）。
func ModerationKeyMisconfigured() bool {
	InitModerationKey()
	return moderationEncryptKey == nil && moderationKeyWarned
}

// EncryptModerationContent 用 AES-256-GCM 加密违规原文，输出 modenc:base64(nonce||ciphertext)。
// 密钥未配置时返回 ErrModerationKeyMissing，调用方应把 ContentEnc 留空。
func EncryptModerationContent(plain string) (string, error) {
	InitModerationKey()
	if moderationEncryptKey == nil {
		return "", ErrModerationKeyMissing
	}
	if plain == "" {
		return "", nil
	}
	block, err := aes.NewCipher(moderationEncryptKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nil, nonce, []byte(plain), nil)
	combined := append(nonce, ciphertext...)
	return moderationCipherPrefix + base64.StdEncoding.EncodeToString(combined), nil
}

// DecryptModerationContent 解密。只供带鉴权与操作留痕的取原文接口调用（§10.1 访问控制第 2 条）。
func DecryptModerationContent(enc string) (string, error) {
	if enc == "" {
		return "", nil
	}
	InitModerationKey()
	if moderationEncryptKey == nil {
		return "", ErrModerationKeyMissing
	}
	combined, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(enc, moderationCipherPrefix))
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(moderationEncryptKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonceSize := gcm.NonceSize()
	if len(combined) < nonceSize {
		return "", errors.New("moderation: ciphertext too short")
	}
	nonce, ciphertext := combined[:nonceSize], combined[nonceSize:]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// HashModerationContent 计算归一化原文的 SHA-256，用于同内容重发判定与 hash 黑名单（§11）。
// 不加盐：黑名单要能跨用户、跨重启命中，加盐反而做不到。
func HashModerationContent(normalized string) string {
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}
