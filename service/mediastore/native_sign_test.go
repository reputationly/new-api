package mediastore

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// 待签串格式对拍 HCSO 接口参考 §3.2.3 表 3-14 官方示例：
// GET \n\n\n1532779451\n/examplebucket/objectkey
func TestBuildGetStringToSignDocExample(t *testing.T) {
	got := buildGetStringToSign("examplebucket", "objectkey", 1532779451, nil, nil)
	want := "GET\n\n\n1532779451\n/examplebucket/objectkey"
	if got != want {
		t.Errorf("stringToSign=%q want %q", got, want)
	}
}

func TestBuildGetStringToSignSubResources(t *testing.T) {
	sub := map[string]string{
		"response-content-disposition": `attachment; filename="a.mp4"`,
		"response-cache-control":       "private, max-age=86400, immutable",
	}
	// 字典序：response-cache-control < response-content-disposition；值为原始值
	got := buildGetStringToSign("b", "k", 100, []string{"response-cache-control", "response-content-disposition"}, sub)
	want := "GET\n\n\n100\n/b/k?response-cache-control=private, max-age=86400, immutable&response-content-disposition=attachment; filename=\"a.mp4\""
	if got != want {
		t.Errorf("stringToSign=%q want %q", got, want)
	}
}

// HMAC-SHA1+Base64 对拍 openssl：
// printf 'GET\n\n\n1532779451\n/examplebucket/objectkey' | openssl dgst -sha1 -hmac 'test-sk' -binary | base64
func TestHmacSHA1Base64Vector(t *testing.T) {
	got := hmacSHA1Base64("test-sk", "GET\n\n\n1532779451\n/examplebucket/objectkey")
	want := "7CEFYzQ6+jhL3ftKHZyu1KpX0ko="
	if got != want {
		t.Errorf("signature=%q want %q", got, want)
	}
}

func TestNativeSignedGetURLBucketStability(t *testing.T) {
	cfg := obsConfig{
		Endpoint:        "https://obs.example.com",
		Bucket:          "media",
		AccessKeyID:     "AKTEST",
		SecretAccessKey: "SKTEST",
	}
	ttl := 168 * time.Hour
	base := time.Date(2026, 7, 14, 10, 5, 0, 0, time.UTC)

	u1, err := nativeSignedGetURL(cfg, "t2v/2026/07/14/1/task.mp4", ttl, SignOptions{}, base)
	if err != nil {
		t.Fatal(err)
	}
	// 同一 24h 桶内（10:05 → 当日 23:50）URL 必须逐字节相同
	u2, err := nativeSignedGetURL(cfg, "t2v/2026/07/14/1/task.mp4", ttl, SignOptions{}, base.Add(13*time.Hour+45*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if u1 != u2 {
		t.Errorf("same-bucket URLs differ:\n%s\n%s", u1, u2)
	}
	// 跨桶（次日 00:05）必须轮换
	u3, err := nativeSignedGetURL(cfg, "t2v/2026/07/14/1/task.mp4", ttl, SignOptions{}, base.Add(14*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if u1 == u3 {
		t.Errorf("cross-bucket URL did not rotate: %s", u1)
	}

	// 完整 URL 黄金向量：expires/签名均以 openssl+python 独立算出（见对拍命令），
	// 钉死编码链的每一环。
	// E0=$(date -u ... "2026-07-14T00:00:00Z" +%s); EXP=$((E0+86400+604800))
	// printf 'GET\n\n\n%s\n/media/t2v/2026/07/14/1/task.mp4?response-cache-control=private, max-age=86400, immutable' $EXP \
	//   | openssl dgst -sha1 -hmac 'SKTEST' -binary | base64
	wantGolden := "https://media.obs.example.com/t2v/2026/07/14/1/task.mp4" +
		"?response-cache-control=private%2C%20max-age%3D86400%2C%20immutable" +
		"&AccessKeyId=AKTEST&Expires=1784678400&Signature=POEU%2FmLIT3%2BjKb52HxlffCaSiVI%3D"
	if u1 != wantGolden {
		t.Errorf("golden URL mismatch:\n got %s\nwant %s", u1, wantGolden)
	}

	// virtual-hosted 形态 + 必备查询参数
	if !strings.HasPrefix(u1, "https://media.obs.example.com/t2v/2026/07/14/1/task.mp4?") {
		t.Errorf("unexpected URL shape: %s", u1)
	}
	for _, part := range []string{"response-cache-control=", "AccessKeyId=AKTEST", "Expires=", "Signature="} {
		if !strings.Contains(u1, part) {
			t.Errorf("URL missing %q: %s", part, u1)
		}
	}
	// Cache-Control 值须编码进 URL（空格→%20）
	if !strings.Contains(u1, "response-cache-control=private%2C%20max-age%3D86400%2C%20immutable") {
		t.Errorf("cache-control not encoded as expected: %s", u1)
	}
}

// 剩余有效期恒 ≥ TTL（Expires 对齐桶终点再加 TTL）
func TestNativeSignedGetURLExpiresFloor(t *testing.T) {
	cfg := obsConfig{Endpoint: "obs.example.com", Bucket: "b", AccessKeyID: "ak", SecretAccessKey: "sk"}
	ttl := 2 * time.Hour
	// 桶尾时刻（23:59:59）签出的 URL 剩余有效期也必须 ≥ TTL
	now := time.Date(2026, 7, 14, 23, 59, 59, 0, time.UTC)
	u, err := nativeSignedGetURL(cfg, "k", ttl, SignOptions{}, now)
	if err != nil {
		t.Fatal(err)
	}
	wantExpires := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC).Add(ttl).Unix()
	if !strings.Contains(u, "Expires="+strconv.FormatInt(wantExpires, 10)) {
		t.Errorf("URL %s missing Expires=%d", u, wantExpires)
	}
	// 无 scheme 的 endpoint 默认 https
	if !strings.HasPrefix(u, "https://b.obs.example.com/") {
		t.Errorf("unexpected URL: %s", u)
	}
}

func TestNativeSignedGetURLDownloadName(t *testing.T) {
	cfg := obsConfig{Endpoint: "https://obs.example.com", Bucket: "b", AccessKeyID: "ak", SecretAccessKey: "sk"}
	u, err := nativeSignedGetURL(cfg, "k.mp4", time.Hour, SignOptions{DownloadName: "视频 1.mp4"}, time.Unix(1752480000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(u, "response-content-disposition=attachment%3B%20filename%3D") {
		t.Errorf("URL missing encoded disposition: %s", u)
	}
	// 子资源按字典序排列：cache-control 在 disposition 之前
	if strings.Index(u, "response-cache-control=") > strings.Index(u, "response-content-disposition=") {
		t.Errorf("sub-resources not sorted: %s", u)
	}
}

// 空 key / 非法 TTL 必须拒签（空 key 会签出可列桶的 /bucket/ URL）
func TestNativeSignedGetURLRejectsInvalidInput(t *testing.T) {
	cfg := obsConfig{Endpoint: "https://obs.example.com", Bucket: "b", AccessKeyID: "ak", SecretAccessKey: "sk"}
	now := time.Unix(1752480000, 0)
	if _, err := nativeSignedGetURL(cfg, "", time.Hour, SignOptions{}, now); err == nil {
		t.Error("empty key must be rejected")
	}
	if _, err := nativeSignedGetURL(cfg, "k", 0, SignOptions{}, now); err == nil {
		t.Error("zero ttl must be rejected")
	}
	if _, err := nativeSignedGetURL(cfg, "k", -time.Hour, SignOptions{}, now); err == nil {
		t.Error("negative ttl must be rejected")
	}
	// 超大 TTL:OBS 上限 20 年,且须在 duration 加法前拦截以防溢出
	if _, err := nativeSignedGetURL(cfg, "k", maxSignTTL+time.Second, SignOptions{}, now); err == nil {
		t.Error("oversized ttl must be rejected")
	}
}

// 非整秒 TTL 按秒向上取整,Expires 整秒截断后剩余有效期仍 ≥ TTL
func TestNativeSignedGetURLSubSecondTTLCeil(t *testing.T) {
	cfg := obsConfig{Endpoint: "https://obs.example.com", Bucket: "b", AccessKeyID: "ak", SecretAccessKey: "sk"}
	now := time.Date(2026, 7, 14, 23, 59, 59, 900_000_000, time.UTC)
	u, err := nativeSignedGetURL(cfg, "k", 500*time.Millisecond, SignOptions{}, now)
	if err != nil {
		t.Fatal(err)
	}
	// 500ms 取整为 1s:Expires = 次日 00:00:00 + 1s
	wantExpires := time.Date(2026, 7, 15, 0, 0, 1, 0, time.UTC).Unix()
	if !strings.Contains(u, "Expires="+strconv.FormatInt(wantExpires, 10)) {
		t.Errorf("URL %s missing Expires=%d", u, wantExpires)
	}
}

func TestEncodeObjectKey(t *testing.T) {
	cases := []struct{ in, want string }{
		{"t2v/2026/07/14/1/task.mp4", "t2v/2026/07/14/1/task.mp4"},
		{"a b/c+d", "a%20b/c%2Bd"},
		{"star*~/x", "star%2A~/x"},
		{"中文/f#g?h&i.mp4", "%E4%B8%AD%E6%96%87/f%23g%3Fh%26i.mp4"},
	}
	for _, c := range cases {
		if got := encodeObjectKey(c.in); got != c.want {
			t.Errorf("encodeObjectKey(%q)=%q want %q", c.in, got, c.want)
		}
	}
}
