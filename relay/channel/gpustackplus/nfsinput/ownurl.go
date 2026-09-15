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
	"path/filepath"
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

// resolveOwnNFSPath 判断 raw 是否为「共享 NFS 上、且归当前用户所有」的绝对路径。
//
// # 为什么需要它
//
// 聚合流水线的超分段以客户身份**自调用** /v1/videos,输入是生成段产物在 NFS 上的
// 绝对路径 —— 那一刻产物还没落 OBS(落盘发生在流水线收尾之后),既没有 task: 可引用
// (任务尚未 SUCCESS 终态),也没有 URL 可下载。不认这种形态的后果实测过:超分段每次
// 提交都被 AddString 当成裸 base64 解码,回 400
// 「输入 video 既非 http(s) URL 也非合法 base64/data-uri」,而流水线按设计降级交付
// 生成段成品 —— 于是**客户拿到的一直是未超分的 768P,任务却显示成功**。
//
// # 归属校验为什么够
//
// raw 是请求体里的任意字符串(与 resolveOwnOBSURL 的输入同性质),所以光有「落在 root
// 之下」不够 —— 别人的产物也在 root 之下。这里用与 resolveOwnOBSURL 完全相同的那道闸:
// Key 形如 <功能>-<模型>/yyyy/mm/dd/<user_id>/<file>,倒数第二段必须等于当前用户。
// 构造不出「该段是自己、却指向他人目录」的路径。
//
// **Key 必须从 symlink 解析后的路径算。** 用解析前的算会留一个绕过:用户在自己的
// 目录下放一个指向他人产物的软链,倒数第二段仍是自己的 uid,闸就形同虚设 ——
// 而 ValidateNFSPath 只管「没逃出 root」,他人产物同样在 root 之下,它拦不住这个。
func (m *Materializer) resolveOwnNFSPath(raw string) (*ownSource, bool) {
	if !IsOwnNFSPath(raw, m.root) {
		return nil, false
	}
	resolved, err := mediastore.ValidateNFSPath(m.root, raw)
	if err != nil {
		return nil, false
	}
	// **Key 要相对解析后的 root 算,不能相对配置里那个 root。**
	//
	// KeyFromNFSPath 是纯字符串裁前缀,而 ValidateNFSPath 返回的是
	// EvalSymlinks 之后的路径。root 自身只要有一段是软链(软链过来的 SFS
	// 挂载点;macOS 上 /var → /private/var,所有 t.TempDir() 都是),两者就对不上,
	// 裁不掉前缀 —— key 退化成"整条绝对路径去掉开头的斜杠"。
	//
	// 阴险之处在于**这样也不报错**:KeyUserIDSegment 取的是倒数第二段,
	// 那一段仍然是 uid 目录,归属校验照过;L2 直读也用的是 abs 不是 key。
	// 只有开了 NFSZeroCopyInput 时,这串垃圾才会被 addOwnSourceRef 登记成
	// input_ref 发给门面,而门面按它找不到文件。
	//
	// 兄弟函数 resolveOwnOBSURL 没有这个问题,因为它是**由 key 推 abs**,
	// 方向相反。
	resolvedRoot, err := filepath.EvalSymlinks(filepath.Clean(m.root))
	if err != nil {
		return nil, false
	}
	key := mediastore.KeyFromNFSPath(resolvedRoot, resolved)
	if key == "" {
		return nil, false
	}
	if seg := mediastore.KeyUserIDSegment(key); seg == "" || seg != m.userID {
		return nil, false
	}
	fi, err := os.Stat(resolved)
	if err != nil || !fi.Mode().IsRegular() {
		return nil, false
	}
	return &ownSource{key: key, abs: resolved, size: fi.Size()}, true
}

// IsOwnNFSPath 判断 raw 是否**形如**共享 NFS root 下的绝对路径 —— 只看形态,
// 不做归属与存在性校验(那些在 resolveOwnNFSPath 里)。
//
// # 为什么必须带 root 前缀这道门槛
//
// 光判 filepath.IsAbs 会把**裸 base64 的 JPEG 全部截走**:JPEG 的 magic 是
// FF D8 FF,标准 base64 编码后恰好以 "/9j/" 开头,而 IsAbs 对任何以 "/" 开头的
// 字符串都返回 true。裸 base64 是 AddString 明确支持的形态,这么一截,原本
// 好好的请求会变成 400「不是当前用户在共享存储上的产物路径」—— 一个与真实
// 原因毫无关系的报错。上线过一版才发现。
//
// 判据与 ValidateNFSPath 的第一道检查刻意保持一致(都用**未解析**的 root 比
// 前缀),这样"进得了这条分支"与"过得了校验"用的是同一个坐标系,不会出现
// 进来了却必然失败的中间状态。
func IsOwnNFSPath(raw, root string) bool {
	if root == "" || !filepath.IsAbs(raw) {
		return false
	}
	cleanRoot := filepath.Clean(root)
	cleanPath := filepath.Clean(raw)
	return cleanPath == cleanRoot ||
		strings.HasPrefix(cleanPath, cleanRoot+string(filepath.Separator))
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
