package moderation

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/setting/system_setting"
)

func TestScratchRootFallsBackWhenNFSMissing(t *testing.T) {
	s := system_setting.GetMediaStorageSettings()
	orig := s.NFSOutputRoot
	t.Cleanup(func() { s.NFSOutputRoot = orig })

	// 根目录不存在（NFS 没挂载）时必须回退。
	//
	// 不能靠 MkdirAll 的错误来判断：它会把缺失的父目录一并创建，于是没挂载的机器上
	// 这一步会「成功」，把几百 MB 的视频写进容器可写层——路径看起来还像 NFS，
	// 既撑爆容器磁盘，又躲开了 NFS 那边的清理规则。
	s.NFSOutputRoot = filepath.Join(t.TempDir(), "definitely-not-mounted")
	if dir, ok := scratchRoot(); ok {
		t.Fatalf("NFS 根目录不存在时必须回退，却返回了 %q", dir)
	}
	// 而且不能顺手把它创建出来
	if _, err := os.Stat(s.NFSOutputRoot); err == nil {
		t.Fatal("不该创建不存在的 NFS 根目录——那正是把文件写进容器可写层的那一步")
	}

	// 根目录存在（NFS 挂上了）时正常使用，并按日期分子目录。
	mounted := t.TempDir()
	s.NFSOutputRoot = mounted
	dir, ok := scratchRoot()
	if !ok {
		t.Fatal("NFS 根目录存在时应当使用它")
	}
	if !strings.HasPrefix(dir, mounted) {
		t.Fatalf("临时目录应落在 NFS 根下，得到 %q", dir)
	}
	if !strings.Contains(dir, moderationScratchDir) {
		t.Fatalf("临时目录应带独立前缀（与 inputs/ 平级），得到 %q", dir)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Fatalf("临时目录应已创建: %v", err)
	}

	// 根路径是个文件而不是目录时同样要回退，不能拿它去 join。
	fileAsRoot := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(fileAsRoot, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.NFSOutputRoot = fileAsRoot
	if _, ok := scratchRoot(); ok {
		t.Fatal("根路径是文件时必须回退")
	}
}

func TestVideoDataURLRespectsSizeLimit(t *testing.T) {
	s := system_setting.GetMediaStorageSettings()
	orig := s.MaxObjectSizeMB
	t.Cleanup(func() { s.MaxObjectSizeMB = orig })
	s.MaxObjectSizeMB = 1 // 1 MiB

	// 这条路是全仓唯一没有上游大小闸的：白名单渠道的 data-url 早在
	// relay/task_media_offload.go:112 被同一个 limit 卡过，而这里恰恰是
	// **非**白名单渠道才会走到的分支，客户端能一直塞到 MAX_REQUEST_BODY_MB。
	oversized := "data:video/mp4;base64," +
		base64.StdEncoding.EncodeToString(make([]byte, 2<<20)) // 2 MiB
	_, err := extractFramesFromDataURL(context.Background(), oversized)
	if err == nil {
		t.Fatal("超过 MaxObjectSizeMB 的视频 data-url 必须被拒绝，而不是解码进内存")
	}
	if !strings.Contains(err.Error(), "无法解析") {
		t.Fatalf("错误应来自 data-url 解析阶段，得到: %v", err)
	}

	// 反面：限额之内的应当能过解析这一关。它之后会因为不是真视频而抽帧失败，
	// 但那是 ffmpeg 的错，不是大小闸的错——两者的错误信息必须能区分开。
	small := "data:video/mp4;base64," +
		base64.StdEncoding.EncodeToString(make([]byte, 1024))
	_, err = extractFramesFromDataURL(context.Background(), small)
	if err != nil && strings.Contains(err.Error(), "无法解析") {
		t.Fatalf("限额之内的不该被大小闸拦下: %v", err)
	}
}

func TestShouldLogSkip(t *testing.T) {
	// 首次必报——不然「视频完全没审」这件事在日志里一次都不出现。
	if !shouldLogSkip(0, 1) {
		t.Fatal("第一次跳过必须报")
	}
	// 之后不能每次都报：ffmpeg 缺不缺是进程生命周期内的静态事实，
	// 每请求一条会把真正的问题淹掉。
	if shouldLogSkip(1, 2) || shouldLogSkip(50, 51) {
		t.Fatal("常规增长不该每次都报")
	}
	// 每跨过一个 100 的边界报一次
	if !shouldLogSkip(99, 100) {
		t.Fatal("跨过 100 应报一次")
	}
	if !shouldLogSkip(199, 200) {
		t.Fatal("跨过 200 应报一次")
	}
	// 一次请求跳过很多个时也要报（prev=0 或跨界都算）
	if !shouldLogSkip(0, 150) {
		t.Fatal("首次批量跳过必须报")
	}
	if !shouldLogSkip(90, 210) {
		t.Fatal("一次跨越多个边界应当报")
	}
}

func TestHTTPHardeningArgs(t *testing.T) {
	// -max_redirects 0 是 SSRF 校验的另一半：validateFetchURL 只验了我们看到的那个
	// 地址，而 ffmpeg 默认跟随最多 8 次跳转，302 到 169.254.169.254 就绕过了全部校验。
	args := httpHardeningArgs(ffmpegProtocolWhitelist)
	if len(args) != 2 || args[0] != "-max_redirects" || args[1] != "0" {
		t.Fatalf("http 输入必须禁止重定向，得到 %v", args)
	}
	// 选项名是实测出来的：ffmpeg 8.1 没有 -follow_redirects，写错会让每次抽帧都以
	// "Option not found" 失败——即所有 http 视频永远审不了。
	if args[0] == "-follow_redirects" {
		t.Fatal("ffmpeg 8.1 不认 -follow_redirects，只有 -max_redirects")
	}
	// 本地文件不能带这个参数：它是 http 协议的选项，file 协议下 ffmpeg 会直接报错退出。
	if got := httpHardeningArgs("file"); got != nil {
		t.Fatalf("file 协议不该带 http 选项，得到 %v", got)
	}
}
