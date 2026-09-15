package nfsinput

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/setting/system_setting"
)

// **真实故障**：聚合流水线的超分段以客户身份自调用 /v1/videos，把生成段产物的
// NFS 绝对路径放在 metadata.video 里。改造前 AddString 没有绝对路径分支，这个路径
// 落到最后的 base64 分支被解码，回 400
// 「输入 video 既非 http(s) URL 也非合法 base64/data-uri: illegal base64 data at input byte 16」。
// 流水线按设计降级交付生成段成品 —— 于是客户拿到的一直是 768P，任务却显示成功，
// 线上连着四个任务都是这样，日志里只有一句「超分提交返回 400」。
func TestOwnNFSPathAcceptedForOwner(t *testing.T) {
	root, _ := setupOwnURL(t)
	abs := writeProduct(t, root, ownKey, fakePNG())

	m := NewMaterializer("i2i", "qwen-image-edit", "42", "gid1")
	if err := m.AddString(context.Background(), FieldImage, 0, false, abs); err != nil {
		t.Fatalf("自己的产物路径被拒: %v", err)
	}
}

// **symlink 绕过。** 用户在自己的目录下放一个指向他人产物的软链：路径的倒数第二段
// 仍是自己的 uid，而 ValidateNFSPath 只管「没逃出 root」—— 他人产物同样在 root 之下，
// 它拦不住。所以 Key 必须从解析后的路径算。
//
// 这条是整个改动里最容易做漏的一环：漏了它，功能照样跑通，测试也照样绿，
// 只是任何用户都能读走别人的产物。
func TestOwnNFSPathRejectsSymlinkToOtherUser(t *testing.T) {
	root, _ := setupOwnURL(t)
	victim := writeProduct(t, root, "i2i-qwen-image-edit/2026/09/05/99/victim.png", fakePNG())

	// 攻击者（uid 42）在自己的目录下放软链
	link := filepath.Join(root, "i2i-qwen-image-edit/2026/09/05/42/stolen.png")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, link); err != nil {
		t.Skipf("本环境不支持 symlink: %v", err)
	}

	m := NewMaterializer("i2i", "qwen-image-edit", "42", "gid1")
	err := m.AddString(context.Background(), FieldImage, 0, false, link)
	if err == nil {
		t.Fatal("软链指向他人产物却被放行 —— 任何用户都能读走别人的产物")
	}
}

// 别人目录下的路径直接给出来，同样要拒。
func TestOwnNFSPathRejectsOtherUsersDirectory(t *testing.T) {
	root, _ := setupOwnURL(t)
	victim := writeProduct(t, root, "i2i-qwen-image-edit/2026/09/05/99/victim.png", fakePNG())

	m := NewMaterializer("i2i", "qwen-image-edit", "42", "gid1")
	if err := m.AddString(context.Background(), FieldImage, 0, false, victim); err == nil {
		t.Fatal("他人目录下的产物被放行")
	}
}

// 逃出 root 的路径要拒（../../etc/passwd 这类）。
func TestOwnNFSPathRejectsEscape(t *testing.T) {
	root, _ := setupOwnURL(t)

	m := NewMaterializer("i2i", "qwen-image-edit", "42", "gid1")
	for _, p := range []string{
		"/etc/passwd",
		filepath.Join(root, "i2i-qwen-image-edit/2026/09/05/42/../../../../../../etc/passwd"),
	} {
		if err := m.AddString(context.Background(), FieldImage, 0, false, p); err == nil {
			t.Errorf("逃出 root 的路径被放行: %s", p)
		}
	}
}

// **NFS root 下的路径**不过关时必须报错，不能悄悄落到 base64 分支。
//
// 落下去的话报错是「illegal base64 data at input byte N」——与真实原因毫无
// 关系，而这正是线上超分段那个 400 排查绕远路的根源。
//
// 注意判据是「在 root 下」而不是「是绝对路径」：root 之外的绝对路径与裸
// base64 分不开（JPEG 的 base64 就以 "/9j/" 开头），只能放行给 base64 分支，
// 见 TestBareBase64JPEGNotTreatedAsPath。
func TestOwnNFSPathErrorMentionsPathNotBase64(t *testing.T) {
	root, _ := setupOwnURL(t)

	m := NewMaterializer("i2i", "qwen-image-edit", "42", "gid1")
	// 形态与线上那条自调用一致：root 下、带 uid 段，但文件不存在
	p := filepath.Join(root, "t2v-minimax-h3-fl2va/2026/09/15/42/gone.mp4")
	err := m.AddString(context.Background(), FieldImage, 0, false, p)
	if err == nil {
		t.Fatal("root 下不存在的路径被放行")
	}
	if strings.Contains(err.Error(), "base64") {
		t.Errorf("NFS 路径被当成 base64 报错，排查时会被带偏: %v", err)
	}
}

// **裸 base64 的 JPEG 以 "/9j/" 开头。**
//
// JPEG 的magic 是 FF D8 FF，标准 base64 编码后恰好是 "/9j/…" —— 而
// filepath.IsAbs 对任何以 "/" 开头的字符串都返回 true。所以绝对路径分支
// 会把每一张裸 base64 JPEG 都截走，而它当然不在 NFS root 下，于是硬报
// 「不是当前用户在共享存储上的产物路径」。
//
// 裸 base64 是 AddString 明确支持的形态（注释里写着"data-uri 或裸 base64"），
// 改造前工作正常。这条测试钉住它不被绝对路径分支吃掉。
func TestBareBase64JPEGNotTreatedAsPath(t *testing.T) {
	setupOwnURL(t)

	jpeg := append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, make([]byte, 128)...)
	raw := base64.StdEncoding.EncodeToString(jpeg)
	if !strings.HasPrefix(raw, "/9j/") {
		t.Fatalf("测试前提不成立，JPEG base64 应以 /9j/ 开头，实际 %.8s", raw)
	}

	m := NewMaterializer("i2i", "qwen-image-edit", "42", "gid1")
	if err := m.AddString(context.Background(), FieldImage, 0, false, raw); err != nil {
		t.Fatalf("裸 base64 JPEG 被当成绝对路径拒掉: %v", err)
	}
}

// **零拷贝时登记的 input_ref 必须是真正的 OBS key。**
//
// 这条以前测不出来：setupOwnURL 把 NFSZeroCopyInput 关着，而 key 算错只在
// 开零拷贝时才有后果 —— L2 直读走的是 abs 不是 key，归属校验取倒数第二段
// 也照样是对的。于是 key 退化成「整条绝对路径去掉开头斜杠」却一路绿灯，
// 只有门面按这串垃圾找不到文件。
//
// macOS 上 t.TempDir() 必然踩中（/var → /private/var），Linux 上则要 root
// 自身是软链才会。所以这条测试在 macOS 上是硬约束，在 Linux 上靠下面显式
// 造的软链 root 保证同样有效。
func TestOwnNFSPathZeroCopyRefIsRealKey(t *testing.T) {
	root, _ := setupOwnURL(t)
	s := system_setting.GetMediaStorageSettings()
	s.NFSZeroCopyInput = true

	abs := writeProduct(t, root, ownKey, fakePNG())

	m := NewMaterializer("i2i", "qwen-image-edit", "42", "gid1")
	if err := m.AddString(context.Background(), FieldImage, 0, false, abs); err != nil {
		t.Fatalf("自己的产物路径被拒: %v", err)
	}
	refs := m.Refs()[string(FieldImage)]
	if len(refs) != 1 {
		t.Fatalf("应登记 1 个 input_ref，实际 %v", refs)
	}
	if refs[0] != ownKey {
		t.Errorf("登记的 input_ref 不是 OBS key，门面会按它找不到文件\n  期望 %q\n  实际 %q",
			ownKey, refs[0])
	}
}

// root 自身是软链时同样要算对（Linux 上的显式版本）。
func TestOwnNFSPathZeroCopyUnderSymlinkedRoot(t *testing.T) {
	real, _ := setupOwnURL(t)
	s := system_setting.GetMediaStorageSettings()
	s.NFSZeroCopyInput = true

	link := filepath.Join(t.TempDir(), "root-link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("本环境不支持 symlink: %v", err)
	}
	s.NFSOutputRoot = link

	abs := filepath.Join(link, filepath.FromSlash(ownKey))
	writeProduct(t, real, ownKey, fakePNG())

	m := NewMaterializer("i2i", "qwen-image-edit", "42", "gid1")
	if err := m.AddString(context.Background(), FieldImage, 0, false, abs); err != nil {
		t.Fatalf("软链 root 下的产物路径被拒: %v", err)
	}
	if refs := m.Refs()[string(FieldImage)]; len(refs) != 1 || refs[0] != ownKey {
		t.Errorf("软链 root 下 key 算错\n  期望 [%q]\n  实际 %v", ownKey, refs)
	}
}
