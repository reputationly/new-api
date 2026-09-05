package nfsinput

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/setting/system_setting"
)

// 自家 OBS host 用不可解析的 .invalid 顶级域:一旦某条路径退化成真的走网络,DNS 立刻失败,
// 测试就会红。这是"没有发生 HTTP 下载"唯一可靠的断言方式——没有网络桩,只能让网络必坏。
const testOBSHost = "maas-out.obs.test.invalid"

// setupOwnURL 把媒体存储指向一个临时 NFS root,返回 root 与「key → 自家签名 URL」构造器。
func setupOwnURL(t *testing.T) (string, func(string) string) {
	t.Helper()
	s := system_setting.GetMediaStorageSettings()
	orig := *s
	t.Cleanup(func() { *s = orig })

	root := t.TempDir()
	s.Enabled = true
	s.Endpoint = "https://obs.test.invalid"
	s.Bucket = "maas-out"
	s.NFSOutputRoot = root
	s.NFSZeroCopyInput = false
	s.MaxObjectSizeMB = 200

	return root, func(key string) string {
		return "https://" + testOBSHost + "/" + key + "?AccessKeyId=x&Signature=y"
	}
}

// writeProduct 在 NFS root 下按 key 造一个产物文件。
func writeProduct(t *testing.T, root, key string, data []byte) string {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return abs
}

// fakePNG 合法 PNG 文件头 + 填充,够过 magicOK。
func fakePNG() []byte {
	return append([]byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}, make([]byte, 64)...)
}

const ownKey = "i2i-qwen-image-edit/2026/09/05/42/task-abc.png"

// L2:URL 指向的产物就在同一块 NFS 上时,直接同盘读,不出网。
func TestOwnURLReadsFromNFSWithoutNetwork(t *testing.T) {
	root, urlFor := setupOwnURL(t)
	writeProduct(t, root, ownKey, fakePNG())

	m := NewMaterializer("i2i", "qwen-image-edit", "42", "gid1")
	if err := m.AddString(context.Background(), FieldImage, 0, false, urlFor(ownKey)); err != nil {
		t.Fatalf("应走 NFS 直读并成功,却失败了(说明退化成了网络下载): %v", err)
	}
	refs := m.Refs()["image"]
	if len(refs) != 1 || !strings.HasPrefix(refs[0], "inputs/") {
		t.Fatalf("L2 应把字节写进 inputs/ 并登记该 ref,得到 %v", refs)
	}
	// 产物真实扩展名要保留下来(便于运维在 NFS 上辨认),不能被字段默认值盖掉。
	if !strings.HasSuffix(refs[0], ".png") {
		t.Errorf("应保留产物扩展名 .png,得到 %q", refs[0])
	}
}

// "NFS 上没找到就用 URL 下载":产物已被 janitor 清掉时必须回退,而不是直接报错。
// 这里的下载注定失败(host 不可解析),断言点是错误来自下载而非 NFS——证明回退确实发生了。
func TestOwnURLFallsBackToDownloadWhenAbsent(t *testing.T) {
	_, urlFor := setupOwnURL(t)

	m := NewMaterializer("i2i", "qwen-image-edit", "42", "gid1")
	err := m.AddString(context.Background(), FieldImage, 0, false, urlFor(ownKey))
	if err == nil {
		t.Fatal("产物不在 NFS 上且 host 不可达,应当失败")
	}
	// downloadURL 会在 SSRF 校验阶段就因 host 解析不了而失败,报错未必含"下载"二字;
	// 关键是它必须是一条 URL 路径上的错误(NFS 侧的错误不会提 URL)。
	if !strings.Contains(err.Error(), "URL") {
		t.Errorf("应回退到下载路径并由它报错,实际错误: %v", err)
	}
}

// 归属校验:文件确实存在,但 key 的 <user_id> 段不是当前用户 —— 绝不能读它。
func TestOwnURLRejectsCrossTenantKey(t *testing.T) {
	root, urlFor := setupOwnURL(t)
	const otherKey = "i2i-qwen-image-edit/2026/09/05/99/victim.png"
	victim := writeProduct(t, root, otherKey, fakePNG())

	// 当前用户是 42,URL 指向 99 的产物。
	m := NewMaterializer("i2i", "qwen-image-edit", "42", "gid1")
	err := m.AddString(context.Background(), FieldImage, 0, false, urlFor(otherKey))
	if err == nil {
		t.Fatal("跨租户 key 不应成功")
	}
	if len(m.Refs()) != 0 {
		t.Fatalf("跨租户 key 不应产生任何 input_ref,得到 %v", m.Refs())
	}
	if _, statErr := os.Stat(victim); statErr != nil {
		t.Errorf("他人产物不应被动过: %v", statErr)
	}
}

// L1 零拷贝:直接把产物相对路径当 input_ref 下发,一个字节都不读写。
func TestZeroCopyUsesProductRefAndWritesNothing(t *testing.T) {
	root, urlFor := setupOwnURL(t)
	system_setting.GetMediaStorageSettings().NFSZeroCopyInput = true
	writeProduct(t, root, ownKey, fakePNG())

	m := NewMaterializer("i2i", "qwen-image-edit", "42", "gid1")
	if err := m.AddString(context.Background(), FieldImage, 0, false, urlFor(ownKey)); err != nil {
		t.Fatalf("零拷贝应成功: %v", err)
	}
	refs := m.Refs()["image"]
	if len(refs) != 1 || refs[0] != ownKey {
		t.Fatalf("input_ref 应就是产物相对路径 %q,得到 %v", ownKey, refs)
	}
	if _, err := os.Stat(filepath.Join(root, "inputs")); !os.IsNotExist(err) {
		t.Error("零拷贝路径不应在 inputs/ 下写出任何副本")
	}
}

// 零拷贝登记的路径是用户既有的产物,绝不能进 Cleanup 的删除名单——否则同一批次里任何
// 一个输入失败,都会顺手删掉用户的历史产物。这是本次改动最危险的一处。
func TestCleanupNeverDeletesReferencedProduct(t *testing.T) {
	root, urlFor := setupOwnURL(t)
	system_setting.GetMediaStorageSettings().NFSZeroCopyInput = true
	abs := writeProduct(t, root, ownKey, fakePNG())

	m := NewMaterializer("i2i", "qwen-image-edit", "42", "gid1")
	if err := m.AddString(context.Background(), FieldImage, 0, false, urlFor(ownKey)); err != nil {
		t.Fatalf("零拷贝应成功: %v", err)
	}
	m.Cleanup()

	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("Cleanup 删掉了被引用的用户产物: %v", err)
	}
}

// 配了时长上限的音频字段必须退回 L2:那道闸要整段解码才能量时长,零拷贝拿不到字节就
// 执行不了。宁可多一次同盘读,也不能让音频绕过时长护栏(s2v 过长音频会长时间占卡)。
func TestZeroCopySkippedForAudioDurationGate(t *testing.T) {
	root, urlFor := setupOwnURL(t)
	system_setting.GetMediaStorageSettings().NFSZeroCopyInput = true
	const audioKey = "t2a-ace/2026/09/05/42/song.wav"
	writeProduct(t, root, audioKey, makeWAV(2))

	m := NewMaterializer("s2v", "wan-s2v", "42", "gid1").SetMaxAudioSeconds(30)
	if err := m.AddString(context.Background(), FieldAudio, 0, false, urlFor(audioKey)); err != nil {
		t.Fatalf("应退回 L2 同盘直读并成功: %v", err)
	}
	refs := m.Refs()["audio"]
	if len(refs) != 1 || !strings.HasPrefix(refs[0], "inputs/") {
		t.Fatalf("配了时长闸的音频字段应写进 inputs/,得到 %v", refs)
	}

	// 而且那道闸确实在起作用:超长音频照样被拒。
	m2 := NewMaterializer("s2v", "wan-s2v", "42", "gid2").SetMaxAudioSeconds(1)
	const longKey = "t2a-ace/2026/09/05/42/long.wav"
	writeProduct(t, root, longKey, makeWAV(10))
	if err := m2.AddString(context.Background(), FieldAudio, 0, false, urlFor(longKey)); err == nil {
		t.Error("10 秒音频应被 1 秒上限拦住(零拷贝不得绕过时长闸)")
	}
}

// 零拷贝路径下读文件失败(stat 之后被 janitor 清掉、NFS 瞬时 ESTALE/抖动)必须落回下载,
// 不能变成 400:那种时刻 OBS 上的对象通常还在(NFS janitor TTL 与 OBS 桶生命周期是两套独立
// 策略),回退是能成功的。用不可读文件模拟打开失败。
func TestZeroCopyReadFailureFallsBackToDownload(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root 无视权限位,造不出打开失败")
	}
	root, urlFor := setupOwnURL(t)
	system_setting.GetMediaStorageSettings().NFSZeroCopyInput = true
	abs := writeProduct(t, root, ownKey, fakePNG())
	if err := os.Chmod(abs, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(abs, 0o644) })

	m := NewMaterializer("i2i", "qwen-image-edit", "42", "gid1")
	err := m.AddString(context.Background(), FieldImage, 0, false, urlFor(ownKey))
	if err == nil {
		t.Fatal("host 不可达,回退下载后应当失败")
	}
	// 关键断言:错误必须来自下载路径,而不是那次读失败本身。
	if !strings.Contains(err.Error(), "URL") {
		t.Errorf("读失败应回退下载,而不是直接报错: %v", err)
	}
	if len(m.Refs()) != 0 {
		t.Errorf("读失败不应登记任何 ref,得到 %v", m.Refs())
	}
}

// 与上一条对照:内容不符是确定性结论,回退下载拿到同一份字节也是同样结果,故硬错误。
func TestZeroCopyMagicMismatchIsHardError(t *testing.T) {
	root, urlFor := setupOwnURL(t)
	system_setting.GetMediaStorageSettings().NFSZeroCopyInput = true
	// 扩展名是 .png,内容却是纯文本 —— 典型的改后缀上传。
	writeProduct(t, root, ownKey, []byte("this is definitely not an image"))

	m := NewMaterializer("i2i", "qwen-image-edit", "42", "gid1")
	err := m.AddString(context.Background(), FieldImage, 0, false, urlFor(ownKey))
	if err == nil {
		t.Fatal("文件头校验不通过应被拒")
	}
	if strings.Contains(err.Error(), "URL") {
		t.Errorf("内容不符不应退化成一次网络下载,实际错误: %v", err)
	}
}

// 超限是硬错误,不该回退去下载同一份字节再被拒一次。
func TestOwnURLOverSizeIsHardError(t *testing.T) {
	root, urlFor := setupOwnURL(t)
	writeProduct(t, root, ownKey, append(fakePNG(), make([]byte, 4096)...))

	m := NewMaterializer("i2i", "qwen-image-edit", "42", "gid1").SetMaxBytes(1024)
	err := m.AddString(context.Background(), FieldImage, 0, false, urlFor(ownKey))
	if err == nil {
		t.Fatal("超过 per-model 上限应被拒")
	}
	if strings.Contains(err.Error(), "下载") {
		t.Errorf("超限不应退化成一次网络下载,实际错误: %v", err)
	}
}

// 代理 URL 反解必须与 proxyTaskContentURL 严格互逆。
func TestOwnProxyTaskID(t *testing.T) {
	orig := system_setting.ServerAddress
	defer func() { system_setting.ServerAddress = orig }()
	system_setting.ServerAddress = "https://api.example.com"

	if got := ownProxyTaskID(proxyTaskContentURL("task-123")); got != "task-123" {
		t.Errorf("应与 proxyTaskContentURL 互逆,得到 %q", got)
	}
	for _, raw := range []string{
		"https://api.example.com/v1/videos/task-123",         // 缺 /content
		"https://api.example.com/v1/videos//content",         // 空 id
		"https://api.example.com/v1/videos/a/b/content",      // id 含斜杠
		"https://evil.com/v1/videos/task-123/content",        // 他人 host
		"https://api.example.com/v1/images/task-123/content", // 别的端点
	} {
		if got := ownProxyTaskID(raw); got != "" {
			t.Errorf("ownProxyTaskID(%q)=%q, 应不匹配", raw, got)
		}
	}
}
