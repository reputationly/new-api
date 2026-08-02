package sharelink

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
)

func init() {
	// 测试内固定密钥：生产里它来自 CRYPTO_SECRET / SESSION_SECRET，
	// 默认值是每次启动新生成的 uuid。
	common.CryptoSecret = "test-secret-for-sharelink"
}

func TestSignVerifyRoundTrip(t *testing.T) {
	now := time.Unix(1700000000, 0)
	token, exp := Sign(42, "task_abc123", now)

	if exp != now.Add(TTL).Unix() {
		t.Fatalf("exp = %d, want %d", exp, now.Add(TTL).Unix())
	}
	userID, taskID, err := Verify(token, now)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if userID != 42 {
		t.Fatalf("userID = %d, want 42", userID)
	}
	if taskID != "task_abc123" {
		t.Fatalf("taskID = %q, want %q", taskID, "task_abc123")
	}
}

// task_id 是 varchar(191)、内容不受我们控制，含 '.' 时不能把 payload 切错。
func TestVerifyTaskIDContainingDot(t *testing.T) {
	now := time.Unix(1700000000, 0)
	token, _ := Sign(7, "vendor.task.7", now)

	userID, taskID, err := Verify(token, now)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if userID != 7 || taskID != "vendor.task.7" {
		t.Fatalf("got (%d, %q), want (7, %q)", userID, taskID, "vendor.task.7")
	}
}

func TestVerifyExpired(t *testing.T) {
	now := time.Unix(1700000000, 0)
	token, _ := Sign(1, "task_abc123", now)

	if _, _, err := Verify(token, now.Add(TTL+time.Second)); !errors.Is(err, ErrExpired) {
		t.Fatalf("err = %v, want ErrExpired", err)
	}
	// 边界：正好到期那一秒仍然有效。
	if _, _, err := Verify(token, now.Add(TTL)); err != nil {
		t.Fatalf("at exact expiry err = %v, want nil", err)
	}
}

func TestVerifyTamperedSignature(t *testing.T) {
	now := time.Unix(1700000000, 0)
	token, _ := Sign(1, "task_abc123", now)

	sep := strings.LastIndex(token, ".")
	tampered := token[:sep+1] + flipFirst(token[sep+1:])
	if _, _, err := Verify(tampered, now); !errors.Is(err, ErrSignature) {
		t.Fatalf("err = %v, want ErrSignature", err)
	}
}

// 换 payload 偷别人的任务：签名必须失效。
func TestVerifyTamperedPayload(t *testing.T) {
	now := time.Unix(1700000000, 0)
	token, _ := Sign(1, "task_abc123", now)
	other, _ := Sign(1, "task_victim", now)

	sep := strings.LastIndex(token, ".")
	otherSep := strings.LastIndex(other, ".")
	swapped := other[:otherSep+1] + token[sep+1:]

	if _, _, err := Verify(swapped, now); !errors.Is(err, ErrSignature) {
		t.Fatalf("err = %v, want ErrSignature", err)
	}
}

// 同一个 task_id 分属不同用户时，token 必须能区分出各自的 userID——解析端据此
// 按 (user_id, task_id) 查库，这是「task_id 撞车也不会越权」的依据。
func TestSameTaskIDDifferentUsersProduceDistinctTokens(t *testing.T) {
	now := time.Unix(1700000000, 0)
	tokenA, _ := Sign(1, "collide", now)
	tokenB, _ := Sign(2, "collide", now)

	if tokenA == tokenB {
		t.Fatal("tokens for different users must differ")
	}
	uidA, _, err := Verify(tokenA, now)
	if err != nil {
		t.Fatalf("Verify(tokenA) error = %v", err)
	}
	uidB, _, err := Verify(tokenB, now)
	if err != nil {
		t.Fatalf("Verify(tokenB) error = %v", err)
	}
	if uidA != 1 || uidB != 2 {
		t.Fatalf("got uidA=%d uidB=%d, want 1 and 2", uidA, uidB)
	}
}

// 改 userID 想读别人的同名任务：签名覆盖 userID，必须失效。
func TestVerifyTamperedUserID(t *testing.T) {
	now := time.Unix(1700000000, 0)
	victim, _ := Sign(2, "collide", now)
	attacker, _ := Sign(1, "collide", now)

	// 拿受害者的 payload 配攻击者的签名，反之亦然，都必须被拒。
	vSep := strings.LastIndex(victim, ".")
	aSep := strings.LastIndex(attacker, ".")
	if _, _, err := Verify(victim[:vSep+1]+attacker[aSep+1:], now); !errors.Is(err, ErrSignature) {
		t.Fatalf("err = %v, want ErrSignature", err)
	}
}

func TestVerifyMalformed(t *testing.T) {
	now := time.Unix(1700000000, 0)
	for _, tok := range []string{"", ".", "nodot", "abc.", ".abc"} {
		if _, _, err := Verify(tok, now); err == nil {
			t.Fatalf("Verify(%q) = nil error, want failure", tok)
		}
	}
}

func flipFirst(s string) string {
	if s == "" {
		return "x"
	}
	if s[0] == 'a' {
		return "b" + s[1:]
	}
	return "a" + s[1:]
}
