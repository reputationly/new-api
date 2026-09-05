package mediastore

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// OBS 对象 Key 约定（§4.2）：<功能>-<模型>/yyyy/mm/dd/<user_id>/<task_id>.<ext>
// 与 GPUStack 侧 nfs_path 一一对应：nfs_path 去掉 NFSOutputRoot 前缀即为 Key。

// KeyFromNFSPath 把一个已校验的 nfs_path 转成 OBS Key（去掉挂载根前缀）。
// 调用前须先经 ValidateNFSPath 保证 nfsPath 在 root 之下、无路径逃逸。
func KeyFromNFSPath(root, nfsPath string) string {
	rel := strings.TrimPrefix(filepath.Clean(nfsPath), filepath.Clean(root))
	return strings.TrimPrefix(filepath.ToSlash(rel), "/")
}

// NFSPathFromKey 是 KeyFromNFSPath 的逆：由 OBS Key 还原共享 NFS 上的绝对路径。
// 两者构成 1:1 映射（TestKeyNFSPathRoundTrip 钉住），这条不变量是「自家产物 URL 直接
// 解析成 NFS 路径、免去一次 HTTP 全量下载」的承重点——key 与 NFS 相对路径同构，所以不
// 需要任何 URL→路径的映射表，一次字符串拼接即可。
//
// key 来自客户端可控的 URL path，故先做形态收敛：空、绝对路径、含 ".." 段的一律返回 ""
// （调用方视为「不是自家产物」并回退下载）。这只是廉价前置过滤，最终越权判定仍由
// ValidateNFSPath 的 Clean+前缀+symlink 三道负责。
func NFSPathFromKey(root, key string) string {
	key = strings.TrimSpace(key)
	if key == "" || strings.HasPrefix(key, "/") || filepath.IsAbs(key) {
		return ""
	}
	for _, seg := range strings.Split(filepath.ToSlash(key), "/") {
		if seg == ".." {
			return ""
		}
	}
	return filepath.Join(root, filepath.FromSlash(key))
}

// KeyUserIDSegment 取 Key 约定里的 <user_id> 段（倒数第二段）；取不到返回 ""。
// 归属校验用：Key 形如 <功能>-<模型>/yyyy/mm/dd/<user_id>/<file>，攻击者无法构造出
// 「倒数第二段是自己的 user_id、却指向他人目录」的路径，故按段校验即已充分，
// 不必回查 task 表。
func KeyUserIDSegment(key string) string {
	segs := strings.Split(strings.Trim(filepath.ToSlash(key), "/"), "/")
	if len(segs) < 2 {
		return ""
	}
	return segs[len(segs)-2]
}

// BuildKey 为无 nfs_path 的来源（第三方渠道）按同一约定构造 Key。
// feature 形如 t2i/i2i/t2v/i2v；model 为模型标识；ext 不含点。
func BuildKey(feature, model string, userID int, taskID, ext string, at time.Time) string {
	seg := sanitizeSeg(feature)
	if model != "" {
		seg = seg + "-" + sanitizeSeg(model)
	}
	ext = strings.TrimPrefix(strings.ToLower(ext), ".")
	name := sanitizeSeg(taskID)
	if ext != "" {
		name = name + "." + ext
	}
	return fmt.Sprintf("%s/%04d/%02d/%02d/%d/%s",
		seg, at.Year(), int(at.Month()), at.Day(), userID, name)
}

// sanitizeSeg 清理路径段：去斜杠与首尾空白，防止段内注入分隔符改变层级。
func sanitizeSeg(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	if s == "" {
		return "unknown"
	}
	return s
}

// ValidateNFSPath 校验 nfsPath 落在 root 之下且 Clean 后不逃逸（防越权读任意文件，§9）。
// 两道检查：先做纯字符串的 Clean+前缀校验（不依赖文件存在），再解析符号链接后复查——
// SFS 上若被植入指向根外的 symlink（如 → /etc），仅前缀校验会放行，读到宿主机任意文件。
// 返回符号链接解析后的真实绝对路径；路径不存在时报错（上传场景文件必须已存在）。
func ValidateNFSPath(root, nfsPath string) (string, error) {
	if nfsPath == "" {
		return "", ErrInvalidSource
	}
	cleanRoot := filepath.Clean(root)
	cleanPath := filepath.Clean(nfsPath)
	if !strings.HasPrefix(cleanPath, cleanRoot+string(filepath.Separator)) && cleanPath != cleanRoot {
		return "", fmt.Errorf("%w: %q escapes root %q", ErrInvalidSource, nfsPath, root)
	}
	resolvedRoot, err := filepath.EvalSymlinks(cleanRoot)
	if err != nil {
		return "", fmt.Errorf("%w: resolve root %q: %v", ErrInvalidSource, root, err)
	}
	resolved, err := filepath.EvalSymlinks(cleanPath)
	if err != nil {
		return "", fmt.Errorf("%w: resolve %q: %v", ErrInvalidSource, nfsPath, err)
	}
	if !strings.HasPrefix(resolved, resolvedRoot+string(filepath.Separator)) && resolved != resolvedRoot {
		return "", fmt.Errorf("%w: %q resolves outside root %q", ErrInvalidSource, nfsPath, root)
	}
	return resolved, nil
}

// InferContentType 由 key/文件扩展名推断 Content-Type，覆盖常见图片/视频/音频类型。
func InferContentType(key string) string {
	switch strings.ToLower(strings.TrimPrefix(path.Ext(key), ".")) {
	case "png":
		return "image/png"
	case "jpg", "jpeg":
		return "image/jpeg"
	case "webp":
		return "image/webp"
	case "gif":
		return "image/gif"
	case "bmp":
		return "image/bmp"
	case "mp4":
		return "video/mp4"
	case "webm":
		return "video/webm"
	case "mov":
		return "video/quicktime"
	case "mkv":
		return "video/x-matroska"
	case "wav":
		return "audio/wav"
	case "mp3":
		return "audio/mpeg"
	case "flac":
		return "audio/flac"
	case "ogg":
		return "audio/ogg"
	default:
		return "application/octet-stream"
	}
}
