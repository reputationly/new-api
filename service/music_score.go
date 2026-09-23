package service

import (
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/service/mediastore"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

// maxScoreSidecarBytes 乐谱旁挂的读取上限。YuE2 引擎自己把外部传入的谱限制在 16000 字符,
// 实测扒出/规划的谱 0.5~2.5 KB;64 KiB 只是防御性封顶,正常永远碰不到。
const maxScoreSidecarBytes = 64 << 10

// ReadScoreSidecar 读取音乐引擎写在成品旁边的 ABC 乐谱(<stem>.mp3 → <stem>.abc),
// 没有则返回 ""。
//
// YuE2 每首歌都会把它据以渲染的乐谱(模型规划的 / 翻唱时扒出来的 / 调用方给的)写成同名
// .abc。把它原样回给调用方,「改谱 → 作为 metadata.abc 交回 → 重渲染」的闭环才走得通;
// 否则谱只躺在 NFS 上,用户拿不到。同 seed + 原样交回的谱,重渲染出的 MP3 逐字节相同。
//
// best-effort:读不到(别的引擎、cot=off 没有谱、越出挂载根、超限、非 UTF-8)一律当作
// 没有谱,绝不影响任务本身的成功与计费。
func ReadScoreSidecar(nfsPath string) string {
	if nfsPath == "" {
		return ""
	}
	sidecar := strings.TrimSuffix(nfsPath, filepath.Ext(nfsPath)) + ".abc"
	if sidecar == nfsPath {
		return ""
	}
	root := system_setting.GetMediaStorageSettings().NFSRoot()
	resolved, err := mediastore.ValidateNFSPath(root, sidecar)
	if err != nil {
		return ""
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > maxScoreSidecarBytes {
		return ""
	}
	data, err := os.ReadFile(resolved)
	if err != nil || !utf8.Valid(data) {
		return ""
	}
	return string(data)
}
