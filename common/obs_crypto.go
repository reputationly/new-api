package common

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

// OBS AK/SK 落库加密。与 kyc_crypto.go 同款 AES-256-GCM，独立密钥
// OBS_ENCRYPT_KEY（hex-64 = 32 字节）避免与 KYC 密钥耦合。系统设置页填写的
// SecretAccessKey / AccessKeyID 加密后入 options 表，controller 层按 secret
// 后缀过滤不下发前端。
var (
	obsEncryptKey []byte
	obsKeyOnce    sync.Once
)

// InitOBSKeys 从环境变量加载 OBS AES 密钥。可安全多次调用（sync.Once）。
// 建议在 main() 启动时调用，让配置缺失的告警尽早暴露。
func InitOBSKeys() {
	obsKeyOnce.Do(func() {
		encHex := os.Getenv("OBS_ENCRYPT_KEY")
		if len(encHex) == 64 {
			key, err := hex.DecodeString(encHex)
			if err == nil {
				obsEncryptKey = key
			}
		}
		if obsEncryptKey == nil {
			// 区分「没设」与「设错了」：两者的修法不同，共用一条文案会让人对着一个
			// 已经填了的环境变量反复怀疑人生。格式要求（hex-64）也必须写进日志——
			// 只说 "invalid" 不告诉人对的样子是什么，等于没说。
			switch {
			case encHex == "":
				SysLog("WARNING: OBS_ENCRYPT_KEY 未设置，本次启动使用随机密钥。" +
					"后果：系统设置里保存的 OBS AK/SK 在下次重启后将无法解密，表现为「明明填过 AK/SK，重启后却报 access key/secret required」。" +
					"生成方式：openssl rand -hex 32")
			case len(encHex) != 64:
				SysLog(fmt.Sprintf("WARNING: OBS_ENCRYPT_KEY 长度为 %d，要求 64 个十六进制字符（32 字节），本次启动使用随机密钥。"+
					"生成方式：openssl rand -hex 32", len(encHex)))
			default:
				SysLog("WARNING: OBS_ENCRYPT_KEY 不是合法的十六进制字符串，本次启动使用随机密钥。" +
					"生成方式：openssl rand -hex 32")
			}
			obsEncryptKey = make([]byte, 32)
			if _, err := rand.Read(obsEncryptKey); err != nil {
				panic("obs: failed to generate random encrypt key: " + err.Error())
			}
		}
	})
}

// OBSCipherPrefix 密文标记前缀。带此前缀的值一定是本机制加密的密文，
// 解密失败时调用方（decryptOrRaw）可据此报错而非把密文当明文凭证使用。
const OBSCipherPrefix = "obsenc:"

// EncryptOBSSecret 用 AES-256-GCM 加密 OBS 凭证明文（AK / SK）。
// 输出为 obsenc: 前缀 + base64(nonce || ciphertext)。
func EncryptOBSSecret(plain string) (string, error) {
	InitOBSKeys()
	block, err := aes.NewCipher(obsEncryptKey)
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
	return OBSCipherPrefix + base64.StdEncoding.EncodeToString(combined), nil
}

// IsOBSCipher 判断一个存储值是否带密文标记（obsenc: 前缀）。
func IsOBSCipher(v string) bool {
	return strings.HasPrefix(v, OBSCipherPrefix)
}

// DecryptOBSSecret 解密 [obsenc:]base64(nonce || ciphertext) 形式的 AES-256-GCM 密文。
// 前缀可有可无（兼容加前缀标记之前入库的历史密文）。
func DecryptOBSSecret(enc string) (string, error) {
	InitOBSKeys()
	enc = strings.TrimPrefix(enc, OBSCipherPrefix)
	combined, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(obsEncryptKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonceSize := gcm.NonceSize()
	if len(combined) < nonceSize {
		return "", errors.New("obs: ciphertext too short")
	}
	nonce, ciphertext := combined[:nonceSize], combined[nonceSize:]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}
