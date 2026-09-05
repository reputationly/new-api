// ownurl.go — 自家产物 URL 的输入快路径。
//
// 背景:图生图 / 首尾帧生视频 / 参考生视频这类要"上传"文件的场景,客户端常常直接把我们
// 上一步返回给它的产物 URL 原样传回来。这类 URL 指向的字节**已经躺在同一块共享 NFS 上**,
// 此前却一律走 AddString 的 http(s) 分支整趟下载一遍(出网到 OBS → 整块读进内存 → 再写回
// NFS),一张图多一次 RTT 加一笔公网出向,一个 30 MB 的视频多好几秒。
//
// 承重的不变量只有一条:OBS Key 与 NFS 相对路径 1:1(mediastore.KeyFromNFSPath 与
// NFSPathFromKey 互逆,TestKeyNFSPathRoundTrip 钉住)。所以 URL → 文件路径是一次纯字符串
// 拼接,**不需要任何 URL→存储位置的映射表**:表既省不掉最后那次存在性 stat(janitor 随时
// 按 TTL 删 day dir),又会因为 gpustack 那侧删文件不通知本侧而持续积累假阳性行。
//
// 两级快路径,任一级不成立都静默回退到原来的下载路径("NFS 上没找到就用 URL 下载"):
//
//	L1 零拷贝  —— 产物相对路径直接当 input_ref 下发,不读不写,O(1)。需门面放开
//	              _validate_input_ref 的 inputs/ 前缀限制,故由 NFSZeroCopyInput 开关闸住。
//	L2 本地直读 —— 读 NFS 上的字节写进 inputs/,省掉 HTTP 往返。对门面完全透明,无条件生效。
//
// 覆盖两种"我们生成的 URL":主媒体桶的 OBS 签名 URL(key 即 NFS 相对路径),以及自家视频
// 代理 URL {ServerAddress}/v1/videos/{id}/content(等价于 task:<id>,转交既有解析链)。
// 用户素材桶的 URL **不在此列**:那是独立桶且不涉及 NFS,见 mediastore.KeyFromOwnOBSURL
// 为何必须用严格 host 口径。
package nfsinput

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/QuantumNous/new-api/service/mediastore"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

// magicPeekBytes 零拷贝路径下为做文件头校验而读取的字节数。只够认容器,不影响 O(1)。
const magicPeekBytes = 512

// ownSource 一个已确认属于当前用户、且在共享 NFS 上真实存在的自家产物。
type ownSource struct {
	key  string // OBS Key,同时就是相对 NFSRoot 的路径(1:1),零拷贝时即 input_ref
	abs  string // symlink 解析后的绝对路径
	size int64
}

// resolveOwnOBSURL 判断 raw 是否为「指向我方主媒体桶、且该产物此刻就在本机 NFS 上」的 URL。
// 不是 / 够不到 / 不归当前用户,一律返回 (nil, false) —— 调用方回退下载,行为与改造前一致。
//
// 归属校验按 Key 的 <user_id> 段做,而不是回查 task 表:Key 形如
// <功能>-<模型>/yyyy/mm/dd/<user_id>/<file>,构造不出「该段是自己、却指向他人目录」的路径。
// 校验不通过时回退下载而非报错——那条路今天就是通的(OBS 签名 URL 本身即凭证),这里不
// 收紧也不放宽它,只是不为它提供 NFS 捷径。
func (m *Materializer) resolveOwnOBSURL(raw string) (*ownSource, bool) {
	key := mediastore.KeyFromOwnOBSURL(raw)
	if key == "" {
		return nil, false
	}
	if seg := mediastore.KeyUserIDSegment(key); seg == "" || seg != m.userID {
		return nil, false
	}
	abs := mediastore.NFSPathFromKey(m.root, key)
	if abs == "" {
		return nil, false
	}
	// 越界/symlink 逃逸判定复用与 task: 引用同一道闸;路径不存在时它也会报错
	// (janitor 已按 TTL 清掉 day dir 的正常情形)。
	resolved, err := mediastore.ValidateNFSPath(m.root, abs)
	if err != nil {
		return nil, false
	}
	fi, err := os.Stat(resolved)
	if err != nil || !fi.Mode().IsRegular() {
		return nil, false
	}
	return &ownSource{key: key, abs: resolved, size: fi.Size()}, true
}

// ownProxyTaskID 反解自家视频代理 URL {ServerAddress}/v1/videos/{id}/content 里的 task id;
// 形态不匹配返回 ""。与 proxyTaskContentURL(taskref.go)严格互逆,两者必须同步维护。
//
// 命中后交给既有的 task: 分支处理,而不是另写一套:那条链已经带齐归属校验、SUCCESS 终态
// 校验、NFS 优先与四级退化,重写一遍只会多一份要同步的语义。
func ownProxyTaskID(raw string) string {
	base := strings.TrimRight(strings.TrimSpace(system_setting.ServerAddress), "/")
	if base == "" {
		return ""
	}
	const prefix, suffix = "/v1/videos/", "/content"
	if !strings.HasPrefix(raw, base+prefix) {
		return ""
	}
	rest := raw[len(base)+len(prefix):]
	if i := strings.IndexAny(rest, "?#"); i >= 0 {
		rest = rest[:i]
	}
	if !strings.HasSuffix(rest, suffix) {
		return ""
	}
	id := strings.TrimSuffix(rest, suffix)
	if id == "" || strings.Contains(id, "/") {
		return ""
	}
	return id
}

// canZeroCopy 该字段能否走 L1 零拷贝。
//
// 唯一的排除项是「配了时长上限的音频字段」:那道闸必须把整段音频解码出来才能量时长
// (checkAudioDuration),拿不到字节就无法执行。对 s2v 这类模型它不是可选优化而是硬护栏
// (音频越长越占卡、可能 OOM),宁可退回 L2 本地直读——反正也只是一次同盘读,不出网。
func (m *Materializer) canZeroCopy(field Field) bool {
	if !system_setting.GetMediaStorageSettings().NFSZeroCopyInput {
		return false
	}
	if isAudioField(field) && m.maxAudioSec > 0 {
		return false
	}
	return true
}

// addOwnSourceRef L1:把自家产物的相对路径直接登记为 input_ref,不读字节、不写盘。
//
// 刻意**不**把它记进 m.written —— 那个列表是给 Cleanup 回滚用的,而这里的路径是用户既有
// 的产物,不是本次写出来的临时文件。混进去会让同批次里任何一个输入失败都顺手删掉用户的
// 历史产物,是本文件最危险的一处,改动时务必留意。
//
// head 由调用点读入(尺寸闸同样在调用点,两级快路径共用)。**读文件这件事刻意留在外面**:
// 读失败要落回下载,而内容不符要硬错——两者性质不同,混在这个函数里就会像早前版本那样
// 把一次 NFS 抖动也变成 400。见 AddString 里那段规矩。
//
// 文件头校验的口径与另外两条路径一致:内容不符在这里就是硬错误,因为退回下载路径拿到的
// 是同一份字节、必然得出同样结论,回退只是白跑一趟。代价只有一次 512 字节的读,不破坏 O(1)。
func (m *Materializer) addOwnSourceRef(field Field, src *ownSource, head []byte) error {
	if !magicOK(field, head) {
		return fmt.Errorf("输入 %s 不是有效的媒体文件(文件头校验未通过,请勿改后缀上传)", field)
	}
	m.refs[field] = append(m.refs[field], src.key)
	return nil
}

// peekHead 读文件头若干字节(不足则返回实际长度)。
func peekHead(path string, n int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("读取 NFS 产物失败: %w", err)
	}
	defer f.Close()
	buf := make([]byte, n)
	read, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil, fmt.Errorf("读取 NFS 产物失败: %w", err)
	}
	return buf[:read], nil
}
