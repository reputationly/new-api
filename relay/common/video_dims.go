package common

// 视频任务的计费维度解析。设计见 docs/video-billing-matrix-design.md §4.2。
//
// 分辨率归档逻辑原本只存在于 doubao 适配器里(applyTopLevelSize),现在上移到这里
// 由计费与适配器共用。**必须共用**:一旦「计费认的档位」与「发给上游的档位」分叉,
// 就会出现按 720p 收费却生成 1080p 这类静默错账,是最难发现的一类计费 bug。

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// videoSizeTierRe 匹配 size 的档位形态:720P / 1080p / 2K / 4K。
//
// k 档写成 `\d+k` 而不是字面 `4k`:LTX-2.5 的对外档位里有 2K
// (relay/channel/task/gpustackplus/ltx25.go 的 ltx25SizeTiers),写死 4k 会让它
// 落到像素归档、DimsFromSize("2K") 失败、返回空串,计费矩阵的 lookupCell 见到空
// 行名判未命中,静默回退固定单价——而 2K 恰好是最贵的那档。
var videoSizeTierRe = regexp.MustCompile(`^(?i)(\d+p|\d+k)$`)

// ResolveVideoDims 从统一契约的 TaskSubmitReq 解析计费维度。
// 任一维解析不出就返回零值,由调用方决定回退。
//
// 秒数这一维给的是**最保守**的口径(只认 Duration)。上游会把 Seconds 当 Duration
// 回落的渠道要自己补一层 VideoSecondsFallback——判据是渠道而非请求内容,
// 而渠道只有 relay 包知道,见 relay/video_billing.go 的 videoBillingSeconds。
func ResolveVideoDims(req *TaskSubmitReq) (resolution string, seconds int, hasVideoInput bool) {
	if req == nil {
		return "", 0, false
	}
	return videoResolution(req), videoPerCallSeconds(req), VideoHasVideoInput(req.Metadata)
}

// VideoSecondsFallback 解析 OpenAI 风格的 Seconds 字段(正整数秒,解析不出返回 0)。
//
// **只有上游确实会读它的渠道才能用**。gpustackplus 在 adaptor.go:513-517 明确
// 「Duration 为 0 时回落 Seconds」,不跟就是「引擎按 10 秒出片、计费拿到 0 秒、
// 矩阵未命中、按固定价收」;而 kling/vidu/jimeng 完全忽略 Seconds,跟了就是
// 「按 10 秒收费、上游只生成 5 秒」。两个方向都是错账,所以判据必须是渠道。
func VideoSecondsFallback(req *TaskSubmitReq) int {
	if req == nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(req.Seconds))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// videoResolution 优先取 metadata 里显式给的 resolution(与适配器的优先级一致:
// metadata 原生键压过统一契约的顶层 size),其次由 size 归档。
func videoResolution(req *TaskSubmitReq) string {
	if s, _ := req.Metadata["resolution"].(string); strings.TrimSpace(s) != "" {
		return strings.ToLower(strings.TrimSpace(s))
	}
	return VideoResolutionTier(req.Size)
}

// VideoResolutionTier 把 size 归一成计费矩阵的行名。
//
// size 有三种合法形态(见火山「创建视频生成任务」API 文档):档位("720P")、
// 纯比例("16:9")、精确像素("1280x720")。比例形态不含分辨率信息,返回 ""。
//
//   - 档位形态:**原样小写**返回,不做归档。各模型的档位集合并不相同——LTX-2.5
//     是 544P/704P/1080P/2K,H3 是 480P/768p——强行归到 480/720/1080/4k 会把
//     544P 和 704P 压成同一行,两个成本不同的档收一样的钱。
//   - 像素形态:按**短边**归档到 480p/720p/1080p/4k,取不小于短边的最近档。
func VideoResolutionTier(size string) string {
	size = strings.TrimSpace(size)
	if size == "" {
		return ""
	}
	if videoSizeTierRe.MatchString(size) {
		return strings.ToLower(size)
	}
	w, h, ok := common.DimsFromSize(size)
	if !ok {
		return ""
	}
	shortEdge := h
	if w < h {
		shortEdge = w
	}
	switch {
	case shortEdge <= 480:
		return "480p"
	case shortEdge <= 720:
		return "720p"
	case shortEdge <= 1080:
		return "1080p"
	default:
		return "4k"
	}
}

// videoPerCallSeconds 按次计费的秒数**只认 req.Duration**。
//
// 不能沿用 sora 那种「Seconds 优先」的取值顺序:按次计费的目标渠道读的全是
// req.Duration,完全忽略 Seconds——
//
//	kling  adaptor.go:271  DefaultInt(req.Duration, 5)
//	vidu   adaptor.go:232  DefaultInt(req.Duration, 5)
//	jimeng adaptor.go:387  switch req.Duration { case 10: 241帧; default: 121帧 }
//
// 而 TaskSubmitReq.UnmarshalJSON 只把 duration 归一到 Duration,不会把 seconds
// 灌进去。所以客户端只给 seconds:"10" 时,上游实际生成 5 秒,按 Seconds 查表
// 就会「按 10 秒收费、只出 5 秒片子」。
//
// 取不到宁可返回 0 让矩阵未命中(回退改造前的计费路径),也不猜各渠道自己的默认值:
// kling/vidu 默认 5、jimeng 按帧数分档,硬编码任何一个都会在下一个渠道上出错。
//
// 代价:sora 的 Seconds 语义不被按次矩阵覆盖。sora 有自己的 EstimateBilling,
// 且不在按次矩阵的目标名单里,可接受。
func videoPerCallSeconds(req *TaskSubmitReq) int {
	if req.Duration > 0 {
		return req.Duration
	}
	return 0
}

// VideoHasVideoInput 判断请求是否带视频输入(供应商价目表按这一维分档)。
//
// 参考视频有两种下发形态:metadata.reference_videos(适配器会拼成 content 条目)
// 与客户端自己排好的 metadata.content[]。两者必须一并识别,否则计费与实际请求不一致。
func VideoHasVideoInput(metadata map[string]any) bool {
	if metadata == nil {
		return false
	}
	// 只认 reference_videos / reference_video。metadata.video、src_video 是自建流水线
	// (超分 / 配乐)的字段,第三方适配器不会把它们拼成视频输入,算进来会错判成更便宜的档。
	for _, key := range []string{"reference_videos", "reference_video"} {
		switch v := metadata[key].(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				return true
			}
		case []any:
			for _, item := range v {
				if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
					return true
				}
			}
		}
	}
	contentSlice, ok := metadata["content"].([]any)
	if !ok {
		return false
	}
	for _, item := range contentSlice {
		itemMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if itemMap["type"] == "video_url" {
			return true
		}
		if _, has := itemMap["video_url"]; has {
			return true
		}
	}
	return false
}
