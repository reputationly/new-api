package moderation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/QuantumNous/new-api/types"
)

// 媒体审核入口。见 docs/content-moderation-design.md §7 C、§11、§12。
//
// 与文本入口分开而不是塞进 Moderate：两条链的装配条件不同（TextEndpoints vs
// ImageEndpoints）、失败语义不同、日志按项而不是按请求记一条。合成一个入口只会让
// 每个字段都要先问一句「这次是文本还是图片」。

// MediaItem 一个待审的媒体对象。
type MediaItem struct {
	// URL 可以是 data-url，也可以是 http(s) URL。两种都直接透传给判定模型，
	// 由 vLLM 自己拉取（实测通过），这是 §7 C-1「零额外下载」的落地方式。
	URL  string
	Type types.FileType
	// Hash 内容指纹，缓存的 key。
	//
	// **目前所有调用方都留空**，由 mediaHash 对 URL 现算。§11 设想的是复用
	// `rewriteTaskMedia` 已经算好的那份（它的 pending map 正是以 SHA256(值) 为 key），
	// 但那份 hash 只覆盖被 mediaResolver 认领的值——GPUStackPlus 和全部非白名单渠道
	// 的媒体压根不进那个 map，拿不到。真要复用得先把它从 resolver 里拆出来。
	//
	// 保留这个字段是因为它对 http URL 有实际价值：现算的是「链接的指纹」，
	// 同一张图换个签名参数就会重新审一次。调用方哪天能拿到字节的 hash，填进来即可。
	Hash string
	// Field 这个媒体在请求里的位置，如 "image" / "images[2]"。只进日志，用于定位是哪一张。
	Field string
}

// MediaResult 一次媒体审核的结果。
type MediaResult struct {
	Result
	// BlockedItem 被拦下的那一项在请求里的位置，供拒绝文案定位到具体某张图。
	BlockedItem string
}

// MediaActive 报告当前配置下媒体审核是否会真正执行。
//
// 与 Active 分开：文本链有 L0 兜底，「没有模型节点」时仍然要跑；媒体链没有任何
// 进程内层，没有 image 节点就是完全不审，调用方据此跳过提取媒体的开销。
func MediaActive(group, modelName string) bool {
	s := system_setting.GetModerationSettings()
	if !s.ModelFilter.Match(modelName) {
		return false
	}
	if s.ResolveMode(group) == system_setting.ModerationModeOff {
		return false
	}
	return len(s.ImageEndpoints()) > 0
}

// ModerateMedia 审一组媒体。返回值永不为 nil。
//
// 调用位置同样必须在预扣费之前（§9.1）。
func ModerateMedia(ctx context.Context, req *Request, items []MediaItem) *MediaResult {
	pass := &MediaResult{Result: Result{Action: ActionPass}}
	if req == nil || len(items) == 0 {
		return pass
	}

	s := system_setting.GetModerationSettings()
	mode := s.ResolveMode(req.Group)
	if !s.ModelFilter.Match(req.ModelName) {
		mode = system_setting.ModerationModeOff
	}
	if mode == system_setting.ModerationModeOff {
		return pass
	}
	if len(s.ImageEndpoints()) == 0 {
		// 没有可用节点时**不做任何事**，而不是判 ActionError 触发 fail-close。
		//
		// 图片节点是可选部署：一个只开了文本审核的站点把 mode 调成 blocking 之后，
		// 若这里判错误，所有带图请求会立刻全部被拒——升级审核能力反而先造成事故。
		// 代价是「配了却配错」和「根本没配」在这里长得一样，由配置页的校验去区分。
		return pass
	}

	// ffmpeg 缺失时摘掉视频项，而不是让它们判 ActionError 触发 fail-close。
	//
	// 与上面「没有图片节点就整体跳过」同一条理由：这是部署形态问题（自定义镜像、
	// 裸机运行、本地开发），不是运行时故障。判错误会让每一次视频提交都被拒，
	// 「升级审核能力反而先造成事故」。
	//
	// 摘掉是漏审，所以必须是**显式**的漏审：计数进运行态，图片照审不受影响。
	items = dropVideosIfNoFFmpeg(items)
	if len(items) == 0 {
		return pass
	}

	policy := s.ResolvePolicy(req.Group)
	strictness := system_setting.StrictnessStandard
	if policy != nil && policy.Strictness != "" {
		strictness = policy.Strictness
	}
	m := shieldGemmaModerator{strictness: strictness, policy: policy}

	// 整批媒体共享一个总预算。
	//
	// 没有它的话，单张图的预算（imageBudget）乘上项数就是这次请求的最坏时延——
	// 一个十轮带图的对话能把用户晾在那里几十秒。撞顶按 fail-close 处置：
	// 「审到一半超时了」不等于「没问题」。
	ctx, cancel := context.WithTimeout(ctx, mediaBatchBudget)
	defer cancel()

	// 多个媒体项并发。一次任务提交带三五张参考图是常态，串行会让时延线性叠加，
	// 而这条路在同步路径上（§12.2）。
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(mediaItemConcurrency)

	var mu sync.Mutex
	type judged struct {
		item MediaItem
		v    *Verdict
	}
	results := make([]judged, 0, len(items))
	for _, it := range items {
		g.Go(func() error {
			v, err := moderateOne(gctx, m, &it)
			if err != nil {
				// 与文本链一致：审核未能完成不是「通过」，判 ActionError 交给下面的
				// fail-close 收口，而不是在这里直接拒——observe 期不该因审核故障拒请求。
				v = &Verdict{Action: ActionError, Provider: "L2", Detail: err.Error()}
			}
			mu.Lock()
			results = append(results, judged{item: it, v: v})
			mu.Unlock()
			return nil
		})
	}
	_ = g.Wait() // 每个 g.Go 都吞掉了错误，Wait 不会有非 nil 返回

	enforcing := mode != system_setting.ModerationModeObserve
	out := &MediaResult{Result: Result{Action: ActionPass}}
	var worst *Verdict
	for _, r := range results {
		if worst == nil || severity(r.v.Action) > severity(worst.Action) {
			worst = r.v
			out.BlockedItem = r.item.Field
		}
	}
	if worst == nil {
		return pass
	}

	out.Action = worst.Action
	out.Categories = worst.Categories
	out.Provider = worst.Provider
	out.Blocked = worst.Action == ActionBlock && enforcing
	if out.Blocked {
		out.Reason = ReasonText(worst.Categories)
	}
	// fail-close 的提升必须算在落库**之前**。
	//
	// 它决定的是 enforced 那一列，而那一列的存在理由就是「本周拦了多少要按它数，
	// 不能按 action 数」（model/moderation_log.go:44）。先记日志再提升，会让
	// blocking 下因审核故障被拒的请求留下一条 action=error, enforced=false 的记录：
	// 请求实实在在被拒了，统计里却查无此事，而文本链对同一件事记的是 enforced=true。
	// 两条链对同一个事件给出不同答案，比两条都记错更难查。
	if worst.Action == ActionError && mode == system_setting.ModerationModeBlocking {
		out.Blocked = true
		out.Reason = "内容审核服务暂时不可用，请稍后重试"
		RecordFailClose()
	}

	for _, r := range results {
		// 每一项各记一条：判定必须能归因到具体某张图，否则日志与申诉都说不清
		// 是哪个被拦（§12.1）。
		//
		// enforced 只对**导致这次拒绝**的那一项为真。同一次请求里三张图、一张判错
		// 两张通过时，被拒的原因是那一张，另外两张照实记 enforced=false——
		// 否则「这张图被拦了」会凭空多出两条。
		recordMediaLog(req, &r.item, r.v, mode, policy, itemEnforced(r.v, out.Blocked, enforcing))
	}

	if !out.Blocked {
		out.BlockedItem = ""
	}
	return out
}

// itemEnforced 这一项的判定是否真的被执行了。
func itemEnforced(v *Verdict, requestBlocked bool, enforcing bool) bool {
	switch v.Action {
	case ActionBlock:
		return enforcing
	case ActionError:
		// 审核未完成本身不拦请求，是 fail-close 才拦的——所以要看整体决策，
		// 而不是这一项自己的动作。observe 下 requestBlocked 恒为 false，自动为假。
		return requestBlocked
	default:
		return false
	}
}

// videoSkippedCount 因缺少 ffmpeg 而未送审的视频数。
//
// 有这个计数才谈得上「显式降级」（§6.5 四）：不然「视频都审过了」和
// 「视频一个都没审」在管理端长得一模一样，而后者是这套系统最不能出的错。
var videoSkippedCount atomic.Int64

// VideoSkippedCount 读累计值，供运行态展示。
func VideoSkippedCount() int64 { return videoSkippedCount.Load() }

// dropVideosIfNoFFmpeg 在 ffmpeg 不可用时摘掉视频项。
func dropVideosIfNoFFmpeg(items []MediaItem) []MediaItem {
	hasVideo := false
	for i := range items {
		if items[i].Type == types.FileTypeVideo {
			hasVideo = true
			break
		}
	}
	if !hasVideo {
		return items
	}
	ok, missing := FFmpegAvailable()
	if ok {
		return items
	}

	out := make([]MediaItem, 0, len(items))
	skipped := 0
	for _, it := range items {
		if it.Type == types.FileTypeVideo {
			skipped++
			continue
		}
		out = append(out, it)
	}
	n := videoSkippedCount.Add(int64(skipped))
	if shouldLogSkip(n-int64(skipped), n) {
		common.SysError(fmt.Sprintf(
			"moderation: 缺少 %s，视频未经审核（图片不受影响），累计跳过 %d 个", missing, n))
	}
	return out
}

// shouldLogSkip 累计数从 prev 涨到 now 时要不要打一条日志。
//
// 首次必报，之后每跨过一个 100 的边界报一次——与 model.RecordModerationLog 里
// 丢弃日志的处置一致（那里的注释是「每丢 100 条报一次，避免队列持续满时把日志刷爆」）。
//
// 为什么要限流：ffmpeg 缺不缺是**进程生命周期内的静态事实**（FFmpegAvailable 是
// sync.Once），启动时已经报过、运行态页面上也一直挂着。每个请求再报一条就不是信号
// 而是噪音，真出别的问题时反而被它淹掉。累计数由 video_skipped_count 承载，
// 不需要靠数日志行数得到。
func shouldLogSkip(prev, now int64) bool {
	return prev == 0 || prev/100 != now/100
}

// moderateOne 按媒体类型分派。
//
// 视频与图片在判定层是同一个模型，区别只在「先抽帧」这一步——所以类型分派只在这里
// 出现一次，不往下渗透到判定、缓存、落库任何一层。
func moderateOne(ctx context.Context, m shieldGemmaModerator, it *MediaItem) (*Verdict, error) {
	hash := mediaHash(it)
	if it.Type == types.FileTypeVideo {
		return m.ModerateVideo(ctx, it.URL, hash)
	}
	return m.ModerateImage(ctx, it.URL, hash)
}

// mediaItemConcurrency 单个请求内并发审核的媒体项上限。
//
// 与 segmentConcurrency 同理：不封上界的话，一次带二十张图的提交会一口气打满
// imageSlots，后面的正常请求全被拒——单个请求占用的审核资源必须有界。
const mediaItemConcurrency = 4

// mediaBatchBudget 一次请求内全部媒体共享的总预算（§12.2）。
//
// 30s 与 imageBudget 的上界对齐：单张图最坏 30s，整批也是 30s——多带几张图不该
// 让这个上界跟着涨。取值参照 relay 侧 mediaResolveBudget（20s）的量级，
// 略宽是因为视觉推理比「下载并上传一张图」慢。
//
// 不按项数放大是有意的：那等于告诉攻击者「多传几张图就能拿到更长的时间窗口」。
const mediaBatchBudget = 30 * time.Second

// mediaHash 取媒体的内容指纹。
//
// 调用方给了就用调用方的（那是对**字节**算的，见 MediaItem.Hash）。没给时退回对 URL
// 字符串算——对 data-url 这与对字节算等价，对 http URL 则只是「同一个链接」的指纹：
// 同一张图换个签名参数就会重新审一次。这是有意的保守取舍，宁可多审也不能因为
// URL 相同就跳过（签名 URL 指向的对象是可以被替换的）。
func mediaHash(it *MediaItem) string {
	if it.Hash != "" {
		return it.Hash
	}
	sum := sha256.Sum256([]byte(it.URL))
	return hex.EncodeToString(sum[:])
}

// MediaItemsFromFiles 把 TokenCountMeta.Files 转成待审媒体列表（挂载点 C-3）。
//
// 设计文档把 `TokenCountMeta.Files` 记成「没接上的脚手架」，要求 C-3 顺手把它填上——
// **这条已经过时**：dto/openai_request.go:211 与 dto/claude.go:375 早就在填了，
// service/token_counter.go 也在读它算图片 token。所以这里不需要再写三个格式提取器，
// 直接复用现成的那一份，避免 service/mediastore/dataurl.go:13 警告的「第七处剥头」。
//
// 审的是**全部**图片而不是只审最新一轮——与 L1 的文本策略相反。理由是图片这条路
// 没有 L0 兜底（AC 自动机扫不了图），把历史里的图排除掉就等于给「把违规图塞进伪造
// 历史」留一条没有任何拦截的路。多轮对话重复送审同一张图的成本由 hash 缓存吸收：
// 第二轮起同样的图直接命中，不产生模型调用。
func MediaItemsFromFiles(files []*types.FileMeta) []MediaItem {
	items := make([]MediaItem, 0, len(files))
	seen := make(map[string]bool, len(files))
	for i, f := range files {
		if f == nil || f.Source == nil {
			continue
		}
		// 只有图片和视频送审。音频与通用文件本期不审（§2.3），送进去只会解码失败，
		// 在 blocking 下变成 fail-close——一个正常的语音请求被拒。
		if f.FileType != types.FileTypeImage && f.FileType != types.FileTypeVideo {
			continue
		}
		url := fileSourceURL(f)
		if url == "" || seen[url] {
			continue
		}
		seen[url] = true
		items = append(items, MediaItem{
			URL:   url,
			Type:  f.FileType,
			Field: fmt.Sprintf("file[%d]", i),
		})
	}
	return items
}

// fileSourceURL 把 FileSource 还原成判定模型能吃的形态。
//
// **Base64Source 里装的不一定是裸 base64。** types.NewFileSourceFromData 只特判
// http(s)://，其余一律原样塞进 Base64Data，于是同一个类型承载了两种形态：
//
//   - 裸 base64 + 单独的 MimeType —— Claude 走这条（dto/claude.go:112 传
//     source.data + media_type），Gemini 原生同理；
//   - **完整 data-url**，MimeType 为空 —— OpenAI 格式走这条
//     （dto/openai_request.go:401 把 image_url.url 整串传进去，而 MimeType 读的是
//     OpenAI 根本不存在的 mime_type 键，恒为空）。
//
// 不区分就会拼出 data:image/jpeg;base64,data:image/png;base64,... —— payload 不是
// 合法 base64，节点解不开，blocking 下每个带图请求都 503，observe 下每张图静默不审。
// 这个坑仓库里早有先例：service/file_service.go:324 的 loadFromBase64 正是为它
// 显式剥的头。
func fileSourceURL(f *types.FileMeta) string {
	switch s := f.Source.(type) {
	case *types.URLSource:
		return s.URL
	case *types.Base64Source:
		if s.Base64Data == "" {
			return ""
		}
		if strings.HasPrefix(s.Base64Data, "data:") {
			return s.Base64Data
		}
		// 到这里才是真正的裸 base64。MIME 缺失时按类型给个默认值：
		// vLLM 侧实际按内容嗅探解码，MIME 只用于走通格式校验。
		mime := s.MimeType
		if mime == "" {
			mime = "image/jpeg"
			if f.FileType == types.FileTypeVideo {
				mime = "video/mp4"
			}
		}
		return "data:" + mime + ";base64," + s.Base64Data
	}
	// 未登记的 FileSource 实现。GetRawData 对 URL 形态返回 URL、对 base64 形态返回
	// 裸 base64——后者拼不出 data-url，只能放弃。返回空让调用方跳过，而不是拼一个
	// 必然解码失败的串去触发 fail-close。
	raw := f.Source.GetRawData()
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") || strings.HasPrefix(raw, "data:") {
		return raw
	}
	return ""
}

// ── 媒体类型识别 ──────────────────────────────────────────────────────────

var (
	imageExts = map[string]bool{
		"jpg": true, "jpeg": true, "png": true, "gif": true, "webp": true,
		"bmp": true, "tif": true, "tiff": true, "heic": true, "heif": true, "avif": true,
	}
	videoExts = map[string]bool{
		"mp4": true, "mov": true, "webm": true, "mkv": true, "avi": true,
		"m4v": true, "flv": true, "wmv": true, "ts": true, "mpeg": true, "mpg": true,
	}
)

// ClassifyMedia 判断一个媒体值该不该送审、按什么模态送。
//
// 第二个返回值为 false 的三种情况都必须**跳过而不是当图片试**：
//
//   - 音频（TTS 参考音、音乐参考音频）：本期明确不审（§2.3）。当成图片送进 ShieldGemma
//     会解码失败，在 blocking 下变成 fail-close——一个正常的语音克隆请求被拒，
//     而拒绝理由会显示成「审核服务不可用」，无人能从这条信息想到是音频被当成了图片。
//   - `task:<id>` 引用：不是 URL 也不是字节，模型拿不到（本期已知缺口，§13）。
//   - 认不出扩展名的 http URL：宁可漏审也不误拒，理由同上。
//
// 代价是「用户传了张没有扩展名的图」会绕过审核。这是有意的取舍：这套系统在同步路径上，
// 把不确定的东西一律送审会把不确定性变成用户可见的拒绝。
func ClassifyMedia(value string) (types.FileType, bool) {
	if value == "" {
		return "", false
	}
	if strings.HasPrefix(value, "data:") {
		return classifyByMIME(dataURLMIME(value))
	}
	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
		return classifyByExt(urlExt(value))
	}
	return "", false
}

// dataURLMIME 只取 data-url 头部的 MIME，不解码负载。
//
// 刻意不用 mediastore.ParseDataURL：那个函数会把整个负载解码成字节（它服务的是
// 「落 OBS」那条路，需要字节）。这里只要判个类型，为此把一段 50 MB 的视频解出来
// 纯属浪费。这不构成 dataurl.go:13 所说的「第七处剥头」——没有剥数据，只读了头。
func dataURLMIME(value string) string {
	end := strings.IndexAny(value, ";,")
	if end < 0 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(value[len("data:"):end]))
}

func classifyByMIME(mime string) (types.FileType, bool) {
	switch {
	case strings.HasPrefix(mime, "image/"):
		return types.FileTypeImage, true
	case strings.HasPrefix(mime, "video/"):
		return types.FileTypeVideo, true
	}
	return "", false
}

// urlExt 取 URL 路径部分的扩展名（小写，不含点）。
// 必须先去掉 query——签名 URL 一定带 ?AccessKeyId=...&Signature=...，
// 直接对整串取扩展名会得到签名串的尾巴。
func urlExt(rawURL string) string {
	path := rawURL
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	dot := strings.LastIndex(path, ".")
	if dot < 0 || dot < strings.LastIndex(path, "/") {
		return ""
	}
	return strings.ToLower(path[dot+1:])
}

func classifyByExt(ext string) (types.FileType, bool) {
	switch {
	case imageExts[ext]:
		return types.FileTypeImage, true
	case videoExts[ext]:
		return types.FileTypeVideo, true
	}
	return "", false
}

// ── 图片分数缓存（§11） ────────────────────────────────────────────────────

// imageScoreCacheTTL 图片判定分数的缓存期。
//
// 7 天来自 §11：图生图 / 视频续写场景同一张参考图会被反复提交，命中率高——
// 这是图片审核能塞进同步路径的主要原因。
const imageScoreCacheTTL = 7 * 24 * time.Hour

// imageScoreCacheVersion 缓存版本。
//
// 换模型、改策略文本、增删策略条目都必须把它 +1：缓存里存的是**那一版模型对那一组
// 策略文本**的输出，换了之后旧值依然能反序列化成功，只是含义已经不同了——
// 这种失效不会报错，只会让判定悄悄沿用旧模型的结论，最长七天。
const imageScoreCacheVersion = "v1"

func imageScoreCacheKey(hash string) string {
	return "moderation:img:" + imageScoreCacheVersion + ":" + hash
}

// cacheAvailable 缓存是否可用。
//
// 光看 RedisEnabled 不够：它的**包级初值就是 true**（common/redis.go:17），只有
// InitRedisClient 跑过之后才可能被改成 false，而 RedisGet 不做 nil 检查
// （common/redis.go:77 直接 RDB.Get）。所以在 InitRedisClient 之前调用就是空指针崩溃。
//
// 生产路径不会踩到（main.go:385 初始化失败即退出），但审核不该依赖一个隐式的
// 初始化顺序——多一个 nil 判断的成本是零，而漏掉它的表现是进程崩溃。
func cacheAvailable() bool {
	return common.RedisEnabled && common.RDB != nil
}

// cachedImageScores 带缓存的分数获取。
//
// 缓存的是分数而不是判定结论，理由见 imageScores 的说明。
func cachedImageScores(ctx context.Context, imageURL string, hash string) (imageScores, error) {
	if !cacheAvailable() || hash == "" {
		return scoreImage(ctx, imageURL)
	}

	key := imageScoreCacheKey(hash)
	if raw, err := common.RedisGet(key); err == nil && raw != "" {
		var cached imageScores
		if err := common.UnmarshalJsonStr(raw, &cached); err == nil && len(cached) == len(shieldGemmaPolicies) {
			// 条数对不上说明缓存是另一组策略下写的（多半是版本号忘了加），
			// 当成未命中重算，而不是拿一份残缺的分数去判。
			return cached, nil
		}
	}

	scores, err := scoreImage(ctx, imageURL)
	if err != nil {
		// 只缓存成功的判定。把失败也缓存下来，会让一次节点抖动变成七天的持续误判。
		return nil, err
	}
	if b, err := common.Marshal(scores); err == nil {
		if err := common.RedisSet(key, string(b), imageScoreCacheTTL); err != nil {
			// 缓存写失败不影响判定结果，记一条就够——审核本身已经完成了。
			common.SysLog("moderation: 图片判定缓存写入失败: " + err.Error())
		}
	}
	return scores, nil
}

func videoScoreCacheKey(hash string) string {
	return "moderation:vid:" + imageScoreCacheVersion + ":" + hash
}

// cachedVideoScores 带缓存的视频分数获取。
//
// 缓存整段视频的三帧分数，而不是让三帧各走一次图片缓存：后者虽然也能省下模型调用，
// 但**省不掉抽帧**——要算出帧的 hash 就得先把帧抽出来，而抽帧才是这条路上更贵的
// 那一半（实测 286 MB 视频抽三帧约 210 ms、传输 18 MB）。按视频 hash 缓存能让
// 重复提交的同一段视频完全零成本。
func cachedVideoScores(ctx context.Context, videoURL string, hash string) ([]imageScores, error) {
	if !cacheAvailable() || hash == "" {
		return scoreVideo(ctx, videoURL)
	}

	key := videoScoreCacheKey(hash)
	if raw, err := common.RedisGet(key); err == nil && raw != "" {
		var cached []imageScores
		if err := common.UnmarshalJsonStr(raw, &cached); err == nil && len(cached) > 0 {
			return cached, nil
		}
	}

	frames, err := scoreVideo(ctx, videoURL)
	if err != nil {
		return nil, err
	}
	if b, err := common.Marshal(frames); err == nil {
		if err := common.RedisSet(key, string(b), imageScoreCacheTTL); err != nil {
			common.SysLog("moderation: 视频判定缓存写入失败: " + err.Error())
		}
	}
	return frames, nil
}

// ── 落库 ──────────────────────────────────────────────────────────────────

// recordMediaLog 落一条媒体审核记录。
//
// 与 recordLog 分开而不是加参数：媒体记录不走 SetModerationContent。那条路会把 raw
// 当文本加密留存，而媒体的「原文」是几 MB 的图片字节，塞进 ContentEnc 既撑爆列宽也
// 没有取证价值——要复核的是那张图，而它已经由 ObjectKey 指着了。
func recordMediaLog(
	req *Request,
	item *MediaItem,
	v *Verdict,
	mode system_setting.ModerationMode,
	policy *system_setting.ModerationPolicy,
	enforced bool,
) {
	if v.Action == ActionPass && !model.ShouldSampleModerationPass() {
		return
	}

	policyName := ""
	if policy != nil {
		policyName = policy.Name
	}
	modality := ModalityImage
	if item.Type == types.FileTypeVideo {
		modality = ModalityVideo
	}

	entry := &model.ModerationLog{
		UserId:      req.UserId,
		TokenId:     req.TokenId,
		ChannelId:   req.ChannelId,
		Username:    req.Username,
		Group:       req.Group,
		Policy:      policyName,
		TaskId:      req.TaskId,
		RequestId:   req.RequestId,
		ModelName:   req.ModelName,
		Source:      model.ModerationSourceSelf,
		Stage:       StageInputMedia,
		Modality:    modality,
		Action:      string(v.Action),
		Enforced:    enforced,
		Categories:  strings.Join(v.Categories, ","),
		Score:       v.Score,
		Provider:    v.Provider,
		Detail:      buildDetail(v, mode),
		ContentHash: mediaHash(item),
		// ObjectKey 存可回溯的来源。data-url 存不进去也不该存——那是几 MB 的字节，
		// 而列宽只有 512。此时留空，靠 ContentHash 与请求 id 定位。
		ObjectKey: objectKeyForLog(item.URL),
	}
	model.RecordModerationLog(entry)
}

// objectKeyForLog 取可入库的来源标识。
//
// data-url 一律不存：它本身就是内容，不是引用，存前 512 字节既回溯不了也是把
// 用户上传的内容明文摊进一张无需审计就能列出来的表。
func objectKeyForLog(url string) string {
	if strings.HasPrefix(url, "data:") {
		return ""
	}
	if len(url) > 512 {
		return url[:512]
	}
	return url
}
