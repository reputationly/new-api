package relay

// 客户端上传媒体的出站卸载:把 data-url 落到 OBS、换成签名 URL 再发给上游。
// 设计与背景见 docs/inbound-media-offload-design.md。
//
// 起因:体验区(网页与手机端共用 useVideoGeneration / useAudioGeneration /
// useMusicGeneration 三个 hook)上传的媒体是 base64 data-url,被原样内联进发给上游的
// JSON。第三方中转商的网关普遍有请求体上限(实测某火山兼容中转 1 MiB),一张 790 KB 的图
// base64 后即顶穿,返回 gateway_outcome_unknown。换成签名 URL 后请求体只剩几百字节。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service/mediastore"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
)

// offloadChannelTypes 能吃 http(s) URL 媒体输入的 task 渠道。
// 每一项都必须有适配器侧的证据,改这张表前先读对应适配器。
//
// 显式排除、且不要加进来:
//   - GPUStackPlus:自建,走 NFS 物化(activeMediaResolvers 已在更上层拦掉)
//   - Gemini / VertexAi:Veo 只吃 bytesBase64Encoded 或 gs:// URI;
//     geminitask.ParseImageInput 对 http URL 返回 nil,参考图会被**静默丢弃**
//   - Sora / OpenAI:multipart 原样透传整包 body
//   - SunoAPI:自有 DTO,不走 TaskSubmitReq
//
// 承重假设:没有任何白名单渠道的适配器从原始 body 重建上游请求——它们全部从
// c.Get("task_request") 构建。已核实 relay/channel/task/ 下只有 sora 与 suno 读原始 body,
// 二者都在排除名单。若将来给某个白名单适配器加了读 body 的路径,
// metadata 以 JSON 字符串形态下发时 body 无法被外科手术式改写(体积不缩小),
// 请求体过大的问题会悄悄回来。
var offloadChannelTypes = map[int]bool{
	constant.ChannelTypeDoubaoVideo: true, // Ark content[].image_url.url 原样透传,官方支持公网 URL
	constant.ChannelTypeVolcEngine:  true, // 与 DoubaoVideo 共用适配器
	constant.ChannelTypeJimeng:      true, // http 前缀 → image_urls,转 URL 后走更优路径
	constant.ChannelTypeKling:       true, // 原样透传
	constant.ChannelTypeMiniMax:     true, // hailuo,metadata 原样透传
	constant.ChannelTypeVidu:        true, // Images 原样透传
	constant.ChannelTypeAli:         true, // InputReference → Input.ImgURL
	constant.ChannelTypeParatera:    true, // Image/Images[0] → FirstFrameImage
}

// mediaUploader 落盘 + 签名的接缝。mediastore 的包级函数无法在没有真 OBS 时打桩,
// 单测通过替换 defaultUploader 注入假实现。
type mediaUploader interface {
	Persist(ctx context.Context, key string, src mediastore.PersistSource, meta map[string]string) error
	Sign(ctx context.Context, key string) (string, error)
}

type realUploader struct{}

func (realUploader) Persist(ctx context.Context, key string, src mediastore.PersistSource, meta map[string]string) error {
	return mediastore.Persist(ctx, key, src, meta)
}

func (realUploader) Sign(ctx context.Context, key string) (string, error) {
	return mediastore.Sign(ctx, key)
}

var defaultUploader mediaUploader = realUploader{}

// offloadEnabled 三道门:媒体存储总开关、入站卸载开关、渠道白名单。
func offloadEnabled(info *relaycommon.RelayInfo) bool {
	// ChannelMeta 是嵌入指针，InitChannelMeta 之前为 nil。生产上本函数总在其后调用，
	// 但不值得让一次调用顺序失误变成 panic。
	if info == nil || info.ChannelMeta == nil || !offloadChannelTypes[info.ChannelType] {
		return false
	}
	if !mediastore.Enabled() {
		return false
	}
	return system_setting.GetMediaStorageSettings().IngestClientUpload
}

// dataURLResolver 把 data-url 落 OBS 换签名 URL。
type dataURLResolver struct {
	c      *gin.Context
	userID int

	mu       sync.Mutex
	items    int
	failed   int
	bytesIn  int64
	bytesOut int64
	started  time.Time
}

func newDataURLResolver(c *gin.Context, info *relaycommon.RelayInfo) mediaResolver {
	return &dataURLResolver{c: c, userID: info.UserId, started: time.Now()}
}

func (r *dataURLResolver) Name() string { return "dataurl" }

func (r *dataURLResolver) Want(value string) bool { return mediastore.IsDataURL(value) }

// Resolve 失败一律回退原值 + nil error:卸载是纯优化,失败最多退回改造前的行为,
// 不该让原本能跑通的请求变成跑不通。硬失败只会把用户送回上游的请求体超限报错,
// 那正是本改动要修的东西。
func (r *dataURLResolver) Resolve(ctx context.Context, value string) (string, error) {
	limit := int64(system_setting.GetMediaStorageSettings().MaxObjectSizeMB) * 1024 * 1024
	parsed, err := mediastore.ParseDataURL(value, limit)
	if err != nil {
		r.fail(value, "解析失败: "+err.Error())
		return value, nil
	}

	sum := sha256.Sum256(parsed.Data)
	digest := hex.EncodeToString(sum[:])[:32]
	// 不带 model 段:同一张图给两个模型用不该存两份,那会摧毁摘要去重。
	// 用内容摘要而非 task id 当文件名:用户反复调 prompt 复用同一张参考图时,
	// 当天只上传一次(PutObject 覆盖同 key 幂等)。顺带绕开 info.PublicTaskID
	// 此刻尚未生成的时序问题(它在本函数之后才赋值)。
	key := mediastore.BuildKey("ingest", "", r.userID, digest, parsed.Ext, time.Now())

	if err := defaultUploader.Persist(ctx, key, mediastore.PersistSource{
		Data:        parsed.Data,
		ContentType: parsed.MIME,
	}, map[string]string{"ingest-user": strconv.Itoa(r.userID)}); err != nil {
		r.fail(value, "落盘失败: "+err.Error())
		return value, nil
	}
	signed, err := defaultUploader.Sign(ctx, key)
	if err != nil {
		r.fail(value, "签名失败: "+err.Error())
		return value, nil
	}

	r.mu.Lock()
	r.items++
	r.bytesIn += int64(len(value))
	r.bytesOut += int64(len(signed))
	r.mu.Unlock()
	return signed, nil
}

func (r *dataURLResolver) fail(value, reason string) {
	r.mu.Lock()
	r.failed++
	r.mu.Unlock()
	// 超限那一类透传后必然被上游拒,别让运维对着一条 INFO 猜。
	hint := ""
	if len(value) > 1024*1024 {
		hint = "(该字段 base64 后已超过 1 MiB,透传预计会被上游网关拒绝)"
	}
	logger.LogWarn(r.c.Request.Context(),
		fmt.Sprintf("media offload: 回退原值透传,%s%s", reason, hint))
}

// Report 汇总本次请求的卸载结果。骨架在改写结束时调用(见 mediaResolverReporter)。
func (r *dataURLResolver) Report(c *gin.Context) {
	r.mu.Lock()
	items, failed, in, out := r.items, r.failed, r.bytesIn, r.bytesOut
	r.mu.Unlock()
	if items == 0 && failed == 0 {
		return
	}
	// bytes=A→B 是唯一能一眼解释「为什么上游的请求体超限报错消失了」的数字。
	logger.LogInfo(c.Request.Context(), fmt.Sprintf(
		"media offload: items=%d failed=%d bytes=%d→%d elapsed=%dms",
		items, failed, in, out, time.Since(r.started).Milliseconds()))
	c.Header("X-New-Api-Media-Offload", strconv.Itoa(items))
}
