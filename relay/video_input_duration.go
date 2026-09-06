package relay

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/mediastore"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

// 从输入视频探测计费秒数。
//
// 超分(sr)、配乐(v2a)这类"输入多长、输出就多长"的任务,时长是**输入文件的固有属性**:
// 客户不会传 seconds,也不该要求他传 —— 他多半不知道自己那段视频几秒。于是
// videoBillingSeconds 拿到 0、视频计费矩阵查不到价、整单回退固定价,这就是超分一直
// 只能按次计费的原因。
//
// 探测放在**预扣费之前**,后面完全复用既有的 per_second 机制,一行计费代码都不用改。
// 放提交阶段而不是结算阶段是有意的:per_second 是"提交时定死终价"的模式,若等上游回执
// 再补,预扣就只能按固定价走 —— 一段 10 分钟的视频和一段 5 秒的视频预扣一样多,
// 余额闸门形同虚设。
//
// **任何一步探测不到都返回 0**,让整单回退到原来的固定价路径:拿不到时长时宁可按老
// 口径收钱,也不能把一个本来能跑的请求拦下来(与矩阵未命中即回退的既有设计同口径)。

const (
	// probeSecondsCap 探测结果的合理上限(秒)。超过它一律当作探测失败。
	//
	// 不是防守畸形文件那么简单:mvhd version 1 的 duration 是 uint64,配一个极小的
	// timescale 就能算出接近 2^64 的秒数,而 Go 在 float→int 越界时的行为是
	// implementation-defined(arm64 饱和成 MaxInt64,amd64 给负数)。per_second 又是
	// 提交时定死终价、没有结算侧兜底的,于是一个畸形 mp4 就能冻结出一个荒谬的价钱。
	//
	// 钳到上限也不对(那同样是个错价),所以超限直接判探测失败、回退固定价。
	// 一小时也足够覆盖任何正常的超分输入 —— 真有更长的素材,按固定价收反而更安全。
	probeSecondsCap = 3600

	// probeRangeBytes 远程探测每次拉取的字节数。moov 通常在文件头(faststart)或尾部,
	// 1 MB 足以覆盖绝大多数 mvhd 的位置。
	probeRangeBytes = 1 << 20

	// probeRemoteTimeout 远程探测超时。这段跑在同步提交路径上,不能被慢上游拖住;
	// 超时即回退固定价。
	probeRemoteTimeout = 5 * time.Second
)

// probeTaskTypes 只有这些玩法的**输出长度等于输入长度**,才能按输入时长计费。
//
//	sr  超分:逐帧放大,时长不变
//	v2a 配乐:原视频画面逐帧不动,只补音轨(见 materializeDubInputs 的契约)
//
// **v2v / rv2v / mv2v / ads2v 这些编辑类玩法不在此列**:客户不传 duration 时,
// 适配器根本不下发 target_video_length,输出长度由引擎自己的默认值决定。拿输入时长
// 给它们计费,就会出现"60 秒素材、输出 5 秒、按 60 秒收钱"。
//
// 判据只认**显式** metadata.task_type:推断逻辑在适配器里(taskTypeOfRequest),
// 计费阶段拿不到。没显式传就不探测,回退固定价 —— 少赚好过错账。
var probeTaskTypes = map[string]bool{"sr": true, "v2a": true}

// videoBillingResolution 决定计费矩阵的**行名**,在 ResolveVideoDims 读不出画幅时兜底。
//
// 为什么需要它:sr(超分)/ v2a(配乐)的输出画幅**跟随源视频**,客户根本没有画幅入参
// (超分只有 sr_ratio,配乐连这个都没有)。ResolveVideoDims 于是返回空,而计费矩阵的
// 空行名守卫会直接判未命中 —— 按秒计费配了也永不生效,超分只能一直按次收。
//
// **不去放宽 LookupPerSecond 的空行名守卫**:那道守卫保护的是生成类模型 —— size 解析
// 不出时,谁也不知道这是 544P 还是 2K,而兜底行通常配在最便宜那档,按它收就是少收
// (video_pricing_test.go 的 fallbackRowCfg 里 1080p=0.0685、*=0.02,差 3 倍多)。
// 那条契约对生成类仍然成立,只是对"画幅非入参"的玩法不成立,所以按玩法定向解决。
//
// 复用 probeTaskTypes 而不是另写一份 {sr, v2a}:这两处判的是同一件事 ——「输出跟随
// 输入,因而可以拿输入的属性来计费」。分成两份迟早漂移,而漂移的症状是错账。
func videoBillingResolution(req *relaycommon.TaskSubmitReq, resolution string) string {
	if resolution != "" || req == nil {
		return resolution
	}
	if !probeTaskTypes[strings.TrimSpace(metadataStringValue(req.Metadata, "task_type"))] {
		return ""
	}
	return ratio_setting.VideoPriceRowFallback
}

// videoDurationInputField 源视频所在的 metadata 键。
//
// 只有 metadata.video 一个:sr 与 v2a(probeTaskTypes 的全部成员)在适配器里都走
// materializeSRInputs / materializeDubInputs 的 metadataString(req.Metadata, "video"),
// 单字符串,与这里的读法完全同源。
//
// **不含 src_video**:那是 Bernini(v2v/mv2v/ads2v)的字段,而这些玩法按上面的理由
// 本就不探测,列进来永远走不到。更要紧的是它在适配器里由 metadataStringList 读取,
// 支持数组与逗号串——真把 v2v 加进 probeTaskTypes 时,得连同这里的取值方式一起改,
// 而不是让一个只认单字符串的读法悄悄漏掉数组形态。
const videoDurationInputField = "video"

// ctxKeyProbedVideoSeconds 本次请求的探测结果缓存键。
//
// videoBillingSeconds 会被 videoPerCallPriceable 与 applyVideoPricing 各调一次,
// 而这恰好是本功能的目标配置(模型没有 legacy ModelPrice 时两处都会跑)。不缓存的话
// 一次提交要做两遍 ValidateNFSPath(每次两趟 EvalSymlinks)+ 两次打开读盘,
// 或对一个多 MB 的 base64 载荷解码两遍 —— 就在这段声称"不该拖慢"的同步路径上。
const ctxKeyProbedVideoSeconds = "probed_video_seconds"

// probeInputVideoSeconds 从请求的输入视频里探测计费秒数(向上取整)。
func probeInputVideoSeconds(c *gin.Context, req *relaycommon.TaskSubmitReq) int {
	if req == nil {
		return 0
	}
	if c != nil {
		if v, ok := c.Get(ctxKeyProbedVideoSeconds); ok {
			if cached, ok := v.(int); ok {
				return cached
			}
		}
	}
	seconds := probeInputVideoSecondsUncached(c, req)
	if c != nil {
		c.Set(ctxKeyProbedVideoSeconds, seconds)
	}
	return seconds
}

func probeInputVideoSecondsUncached(c *gin.Context, req *relaycommon.TaskSubmitReq) int {
	if !probeTaskTypes[strings.TrimSpace(metadataStringValue(req.Metadata, "task_type"))] {
		return 0
	}
	raw := strings.TrimSpace(metadataStringValue(req.Metadata, videoDurationInputField))
	if raw == "" {
		return 0
	}
	d := probeVideoDurationFromInput(c, raw)
	// 向上取整:不足一秒按一秒是计费惯例,也避免"0 秒 = 0 元"这种明显错误的账单。
	if d > 0 && d <= probeSecondsCap {
		return int(math.Ceil(d))
	}
	return 0
}

// probeVideoDurationFromInput 按输入的形态取到字节并解析时长;取不到返回 0。
func probeVideoDurationFromInput(c *gin.Context, raw string) float64 {
	switch {
	case strings.HasPrefix(raw, "/"):
		// 已经在共享 NFS 上的绝对路径(聚合流水线的超分段就是这种形态)。
		return probeVideoDurationFromNFS(c, raw)
	case strings.HasPrefix(raw, "http://"), strings.HasPrefix(raw, "https://"):
		// 自家 OBS 的产物 URL:key 与 NFS 相对路径 1:1,能换算成本地路径就地读,不走网络。
		if key := mediastore.KeyFromOwnOBSURL(raw); key != "" {
			return probeVideoDurationFromNFS(c,
				mediastore.NFSPathFromKey(system_setting.GetMediaStorageSettings().NFSRoot(), key))
		}
		// 第三方 URL:只拉文件头尾各 1 MB 找 mvhd,不整包下载。
		return probeVideoDurationFromRemote(c, raw)
	case strings.HasPrefix(raw, "data:"), looksLikeBase64Video(raw):
		return probeVideoDurationFromBase64(raw)
	}
	return 0
}

// probeVideoDurationFromNFS 读共享 NFS 上的文件解析时长。
//
// 两道闸缺一不可:
//   - ValidateNFSPath 挡路径越界与 symlink 逃逸;
//   - **租户归属**——路径里的 <user_id> 段必须等于本次请求的用户。
//
// 第二道是本仓所有"读客户给的 NFS 引用"都遵守的不变量(nfsinput 的 resolveOwnOBSURL
// 比对 KeyUserIDSegment,绝对路径则必须先由 loadTaskForRef 证明任务归属)。少了它,
// 任何登录用户猜一个 <root>/inputs/.../<别人的 user_id>/x.mp4 就能让我们读它,
// 并用它的时长决定账单 —— 内容虽不外泄,但这是一条存在性与时长的侧信道。
func probeVideoDurationFromNFS(c *gin.Context, path string) float64 {
	if strings.TrimSpace(path) == "" {
		return 0
	}
	root := system_setting.GetMediaStorageSettings().NFSRoot()
	resolved, err := mediastore.ValidateNFSPath(root, filepath.Clean(path))
	if err != nil {
		return 0
	}
	if !nfsPathBelongsToCaller(c, root, resolved) {
		return 0
	}
	f, err := os.Open(resolved)
	if err != nil {
		return 0
	}
	defer f.Close()
	d, err := common.GetVideoDuration(f)
	if err != nil {
		return 0
	}
	return d
}

// nfsPathBelongsToCaller 校验 NFS 路径的 <user_id> 段归属当前请求用户。
//
// 拿不到调用者身份时一律拒绝:此处的默认必须是"不读",否则一个没有用户上下文的
// 调用路径就成了绕过归属校验的口子。
func nfsPathBelongsToCaller(c *gin.Context, root, resolved string) bool {
	if c == nil {
		return false
	}
	userID := common.GetContextKeyInt(c, constant.ContextKeyUserId)
	if userID <= 0 {
		return false
	}
	seg := mediastore.KeyUserIDSegment(mediastore.KeyFromNFSPath(root, resolved))
	return seg != "" && seg == strconv.Itoa(userID)
}

// probeVideoDurationFromRemote 用 HTTP Range 只拉文件头尾各一小段来找 mvhd。
//
// 为什么不整包下载:这段跑在同步提交路径上,一个几十 MB 的远程文件会把每次提交都
// 拖慢数秒,而且失败率不由我们控制。moov(内含 mvhd)要么在文件头(faststart,为网络
// 分发准备的 mp4 基本都是),要么在文件尾,两头各 1 MB 足以覆盖绝大多数情况。
//
// 上游不支持 Range(整包 200 返回)时,读取仍受 probeRangeBytes 限制,多余的字节直接
// 丢弃 —— 宁可探测失败回退固定价,也不在计费路径上把整个文件拉进内存。
func probeVideoDurationFromRemote(c *gin.Context, rawURL string) float64 {
	// SSRF 校验不可省:这是唯一一处会按**客户给的 URL**发起外连的计费路径。
	fs := system_setting.GetFetchSetting()
	if err := common.ValidateURLWithFetchSetting(rawURL,
		fs.EnableSSRFProtection, fs.AllowPrivateIp, fs.DomainFilterMode, fs.IpFilterMode,
		fs.DomainList, fs.IpList, fs.AllowedPorts, fs.ApplyIPFilterForDomain); err != nil {
		return 0
	}

	ctx := context.Background()
	if c != nil && c.Request != nil {
		ctx = c.Request.Context()
	}
	reqCtx, cancel := context.WithTimeout(ctx, probeRemoteTimeout)
	defer cancel()

	for _, rng := range []string{
		fmt.Sprintf("bytes=0-%d", probeRangeBytes-1), // faststart:moov 在头部
		fmt.Sprintf("bytes=-%d", probeRangeBytes),    // 否则试文件尾部
	} {
		chunk := fetchRangeChunk(reqCtx, rawURL, rng)
		if len(chunk) == 0 {
			continue
		}
		if d, err := common.GetVideoDurationFromPartial(chunk); err == nil && d > 0 {
			return d
		}
	}
	return 0
}

// fetchRangeChunk 取一段字节;任何异常都返回 nil 交由调用方回退。
func fetchRangeChunk(ctx context.Context, rawURL, rng string) []byte {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Range", rng)

	client := service.GetHttpClient()
	if client == nil {
		// 兜底 client 必须自带重定向策略。service 缓存的那个 client 挂了 checkRedirect,
		// **每一跳**都重新过一遍 ValidateURLWithFetchSetting;裸 &http.Client{} 会无校验
		// 地跟随 3xx,于是上面那次 SSRF 校验只管住了初始 URL,一个 302 就能把我们引到
		// 169.254.169.254。这条是全仓唯一按"客户给的 URL"外连的计费路径,不能留这个口子。
		//
		// 与 nfsinput.downloadURL 同一处置:直接不跟随。3xx 会落到下面的状态码检查,
		// 当作探测失败回退固定价——比在这里重写一遍校验逻辑更难写错。
		client = &http.Client{
			Timeout: probeRemoteTimeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil
	}
	// 无论对方认不认 Range,读取都封顶 —— 不认 Range 时它会从头整包发,
	// 我们只取前 probeRangeBytes 个字节就够找 faststart 的 mvhd。
	data, err := io.ReadAll(io.LimitReader(resp.Body, probeRangeBytes))
	if err != nil {
		return nil
	}
	return data
}

// probeVideoDurationFromBase64 解码 data-uri / 裸 base64 后解析时长。
//
// 两处都必须与物化侧(mediastore.ParseDataURL)对齐:
//
//   - **解码前封顶**。请求体没有全局大小限制,而物化侧的 MaxObjectSizeMB 闸门要到
//     提交之后才生效;这里不封顶的话,一个超大的内联 base64 会先在同步计费路径上被
//     完整解码一遍(≈ 载荷长度 3/4 的堆分配),而这段代码声称"不该拖慢提交"。
//   - **容忍不带 padding 的 base64**。ParseDataURL 会回退 RawStdEncoding,只认
//     StdEncoding 就会出现"物化收得下、计费读不了"——不报错,静默回退固定价少收钱。
func probeVideoDurationFromBase64(raw string) float64 {
	limit := int64(system_setting.GetMediaStorageSettings().MaxObjectSizeMB) * 1024 * 1024

	if strings.HasPrefix(raw, "data:") {
		// 直接复用物化侧的解析,不自己再写一遍 header/limit/padding 的处理:
		// 两套口径迟早会漂移。
		parsed, err := mediastore.ParseDataURL(raw, limit)
		if err != nil {
			return 0
		}
		return durationFromBytes(parsed.Data)
	}

	payload := strings.TrimSpace(raw)
	// 与 ParseDataURL 同一个预判式:每 4 字符 3 字节,扣掉尾部 padding 才是精确上界。
	if limit > 0 && int64(len(payload))/4*3-int64(strings.Count(tailOf(payload, 2), "=")) > limit {
		return 0
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(payload)
		if err != nil {
			return 0
		}
	}
	return durationFromBytes(data)
}

func durationFromBytes(data []byte) float64 {
	if len(data) == 0 {
		return 0
	}
	d, err := common.GetVideoDuration(bytes.NewReader(data))
	if err != nil {
		return 0
	}
	return d
}

// tailOf 取末尾至多 n 个字符(串比 n 短时返回整串)。
func tailOf(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// looksLikeBase64Video 粗判一个裸串是否像 base64。只用于决定"要不要试着解码",
// 解错了也只是探测失败、回退固定价。
func looksLikeBase64Video(s string) bool {
	if len(s) < 64 {
		return false
	}
	for i := 0; i < len(s) && i < 64; i++ {
		c := s[i]
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' ||
			c == '+' || c == '/' || c == '=') {
			return false
		}
	}
	return true
}

// metadataStringValue 从 metadata 里取一个字符串值。
func metadataStringValue(md map[string]any, key string) string {
	if md == nil {
		return ""
	}
	if v, ok := md[key].(string); ok {
		return v
	}
	return ""
}
