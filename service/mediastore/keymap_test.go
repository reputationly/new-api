package mediastore

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/setting/system_setting"
)

// Key ↔ NFS 相对路径 1:1 是「自家产物 URL 直接解析成 NFS 路径」的承重不变量：它成立，
// 才不需要任何 URL→存储位置的映射表。谁改了 Key 约定而没同步反向函数，这里当场见红，
// 而不是让线上的快路径静默 miss、退化成每次重新下载。
func TestKeyNFSPathRoundTrip(t *testing.T) {
	const root = "/nfs-output"
	at := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)

	for _, key := range []string{
		BuildKey("i2i", "qwen-image-edit", 42, "task-abc", "png", at),
		BuildKey("t2v", "wan2.2", 7, "task-xyz", "mp4", at),
		"inputs/i2v-wan/2026/09/05/42/gid-image-0.png",
	} {
		abs := NFSPathFromKey(root, key)
		if abs == "" {
			t.Fatalf("NFSPathFromKey(%q) 返回空", key)
		}
		if got := KeyFromNFSPath(root, abs); got != key {
			t.Errorf("往返不恒等: key=%q → path=%q → key=%q", key, abs, got)
		}
	}
}

// 反向函数吃的是客户端可控的 URL path，必须先做形态收敛，不能直接拼进文件路径。
func TestNFSPathFromKeyRejectsEscapes(t *testing.T) {
	for _, key := range []string{
		"",
		"   ",
		"/etc/passwd",
		"../../etc/passwd",
		"t2i-x/2026/09/05/42/../../../../etc/passwd",
	} {
		if got := NFSPathFromKey("/nfs-output", key); got != "" {
			t.Errorf("NFSPathFromKey(%q)=%q, 应拒绝", key, got)
		}
	}
}

func TestKeyUserIDSegment(t *testing.T) {
	cases := map[string]string{
		"i2i-qwen/2026/09/05/42/task-abc.png":         "42",
		"inputs/i2v-wan/2026/09/05/7/gid-image-0.png": "7",
		"ingest/2026/09/05/1024/deadbeef.jpg":         "1024",
		"single":                                      "",
		"":                                            "",
		"a/b":                                         "a",
	}
	for key, want := range cases {
		if got := KeyUserIDSegment(key); got != want {
			t.Errorf("KeyUserIDSegment(%q)=%q want %q", key, got, want)
		}
	}
}

// KeyFromOwnOBSURL 必须按 <bucket>.<endpoint> 全等匹配。用户素材桶（独立桶、**不涉及
// NFS**）通常与主桶共用同一个 endpoint：口径一放宽到裸 endpoint，素材 URL 就会被当成
// 自家产物，拿它的 key 去主桶 NFS 上找，撞名即读到另一个文件。
func TestKeyFromOwnOBSURL(t *testing.T) {
	s := system_setting.GetMediaStorageSettings()
	origEnabled, origEndpoint, origBucket := s.Enabled, s.Endpoint, s.Bucket
	defer func() { s.Enabled, s.Endpoint, s.Bucket = origEnabled, origEndpoint, origBucket }()

	s.Enabled = true
	s.Endpoint = "https://obs.cn-central-221.ovaijisuan.com"
	s.Bucket = "maas-obs-output"

	const key = "i2i-qwen-image-edit/2026/09/05/42/task-abc.png"
	if got := KeyFromOwnOBSURL("https://maas-obs-output.obs.cn-central-221.ovaijisuan.com/" + key + "?AccessKeyId=x&Signature=y"); got != key {
		t.Errorf("主桶签名 URL 应解析出 key，得到 %q", got)
	}

	// 用户素材桶：同 endpoint，不同 bucket → 必须不认。
	if got := KeyFromOwnOBSURL("https://maas-user-assets.obs.cn-central-221.ovaijisuan.com/" + key); got != "" {
		t.Errorf("素材桶 URL 不应被认成自家产物，得到 %q", got)
	}
	// 裸 endpoint（path-style）同样不认——我方签名 URL 是 virtual-hosted 形态。
	if got := KeyFromOwnOBSURL("https://obs.cn-central-221.ovaijisuan.com/maas-obs-output/" + key); got != "" {
		t.Errorf("裸 endpoint 不应命中，得到 %q", got)
	}
	// 追加后缀的仿冒 host。
	if got := KeyFromOwnOBSURL("https://maas-obs-output.obs.cn-central-221.ovaijisuan.com.evil.com/" + key); got != "" {
		t.Errorf("仿冒 host 不应命中，得到 %q", got)
	}
	// 媒体存储关闭 → 快路径整体失效。
	s.Enabled = false
	if got := KeyFromOwnOBSURL("https://maas-obs-output.obs.cn-central-221.ovaijisuan.com/" + key); got != "" {
		t.Errorf("媒体存储关闭时不应命中，得到 %q", got)
	}
}

// 拼接结果必须是平台原生分隔符，否则 os.Stat 在非 unix 平台上会失效。
func TestNFSPathFromKeyUsesNativeSeparator(t *testing.T) {
	got := NFSPathFromKey("/nfs-output", "i2i-x/2026/09/05/42/a.png")
	want := filepath.Join("/nfs-output", "i2i-x", "2026", "09", "05", "42", "a.png")
	if got != want {
		t.Errorf("NFSPathFromKey = %q want %q", got, want)
	}
}
