package common

import (
	"os"
	"testing"
)

// 密钥读取走 sync.Once，一个进程里只能测一种配置状态。
// 这里测「配了密钥」那一支；「没配密钥」那一支在 model 包的测试进程里测
// （见 model/moderation_log_test.go TestBlockWithoutKeyKeepsPreviewButNoCiphertext）。
func TestMain(m *testing.M) {
	os.Setenv("MODERATION_ENCRYPT_KEY",
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	os.Exit(m.Run())
}

func TestModerationContentRoundTrip(t *testing.T) {
	if !ModerationKeyReady() {
		t.Fatal("测试密钥没生效，后面的断言都无意义")
	}

	for _, plain := range []string{
		"我想赌博",
		"mixed 中英文 with sk-1234567890/+abcDEF",
		"带换行\n和制表\t的多行内容",
	} {
		enc, err := EncryptModerationContent(plain)
		if err != nil {
			t.Fatalf("encrypt %q: %v", plain, err)
		}
		if enc == plain {
			t.Errorf("密文等于明文: %q", plain)
		}
		got, err := DecryptModerationContent(enc)
		if err != nil {
			t.Fatalf("decrypt %q: %v", plain, err)
		}
		if got != plain {
			t.Errorf("往返不一致: got %q want %q", got, plain)
		}
	}
}

// TestEncryptModerationContentIsNonDeterministic 同一明文两次加密必须不同。
// GCM 的 nonce 若被复用，密文可比对，等于把「这两条被拦的是同一段内容」
// 白送给任何能读到这张表的人。
func TestEncryptModerationContentIsNonDeterministic(t *testing.T) {
	a, err := EncryptModerationContent("同一段违规内容")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	b, err := EncryptModerationContent("同一段违规内容")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if a == b {
		t.Error("两次加密结果相同，nonce 可能被复用")
	}
}

func TestDecryptModerationContentRejectsGarbage(t *testing.T) {
	if _, err := DecryptModerationContent("modenc:not-base64!!!"); err == nil {
		t.Error("非法 base64 应当报错")
	}
	if _, err := DecryptModerationContent("modenc:YWJj"); err == nil {
		t.Error("过短的密文应当报错")
	}
	// 空串是「没存原文」，不是错误——调用方据此提示「该记录未留存原文」。
	if got, err := DecryptModerationContent(""); err != nil || got != "" {
		t.Errorf("空密文应返回空串且无错误，实际 (%q, %v)", got, err)
	}
}

// TestHashModerationContentIsStable hash 要能跨进程、跨重启命中，
// 否则 §11 的 hash 黑名单和重发判定都不成立。
func TestHashModerationContentIsStable(t *testing.T) {
	const s = "我想赌博"
	if HashModerationContent(s) != HashModerationContent(s) {
		t.Error("同一输入的 hash 不稳定")
	}
	if HashModerationContent(s) == HashModerationContent(s+"x") {
		t.Error("不同输入的 hash 相同")
	}
	if len(HashModerationContent(s)) != 64 {
		t.Error("SHA-256 hex 应为 64 字符")
	}
}
