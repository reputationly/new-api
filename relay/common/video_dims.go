package common

// 视频任务的计费维度解析。设计见 docs/video-billing-matrix-design.md §4.2。
//
// 分辨率归档逻辑原本只存在于 doubao 适配器里(applyTopLevelSize),现在上移到这里
// 由计费与适配器共用。**必须共用**:一旦「计费认的档位」与「发给上游的档位」分叉,
// 就会出现按 720p 收费却生成 1080p 这类静默错账,是最难发现的一类计费 bug。

import (
	"encoding/json"
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
// metadata 原生键压过统一契约的顶层 size),其次由 size 归档,最后看 target_short_edge。
//
// 为什么需要第三级:**超分请求根本不带 size**。它的入参是倍率(sr_ratio)与目标短边
// (target_short_edge),画幅跟随源视频——这跟"生成类"请求由客户指定画幅是两种模型。
// 少了这一级,超分的 resolution 恒为空,计费矩阵的空行名守卫直接判未命中,按秒计费
// 配了也永远不生效(见 TestSRResolutionFromTargetShortEdge)。
//
// target_short_edge 是**声明的输出短边**,正是该拿来定价的那个量:客户最终拿到的画质
// 由它决定。它只在 task_type=sr 的超分段出现(见 videoPlayground.constants.js 对三个
// short_edge 字段的辨析),所以不必再按 task_type 设闸——带了它就是这个语义。
//
// 它仍可能返回空:体验区只在"精确像素"那条路才发 target_short_edge,档位词那条不发。
// 空值怎么计费**不在这里决定** —— 本函数只负责"从请求里读出画幅",读不出就是读不出。
// 兜底行名是计费口径,由 relay/video_billing.go 的 videoBillingResolution 决定。
func videoResolution(req *TaskSubmitReq) string {
	if s, _ := req.Metadata["resolution"].(string); strings.TrimSpace(s) != "" {
		return strings.ToLower(strings.TrimSpace(s))
	}
	if tier := VideoResolutionTier(req.Size); tier != "" {
		return tier
	}
	// target_short_edge 只在**有源视频**的请求上采信。
	//
	// 它是 SwiftVR 超分段的字段,生成类请求(t2v/i2v)的引擎根本不认,而且是**静默忽略**
	// —— 引擎侧没有 extra="forbid",多传的键不报错(见 videoPlayground.constants.js
	// 对 delivery_short_edge 的那段辨析)。metadata 又是直连调用方完全可控、且被
	// adaptor.go:290 整体透传的:一个不带 size 的生成请求塞 target_short_edge:1,
	// 就能把计费行名压到 480p,上游照样按默认档出片 —— 不报错的少收。
	//
	// 判据用「有没有源视频」而**不是显式 task_type**:sr 可以由模型名推断
	// (adaptor.go:911 的 swiftvr/seedvr/-sr),直连调用方不写 task_type 也能正常跑超分;
	// 按显式 task_type 设闸会把这些合法请求的分辨率维度砍掉、整单退回固定价。
	// 而 metadata.video 是 sr/v2a 的必填项(materializeSRInputs / materializeDubInputs
	// 缺了它直接报错),生成类请求则从不携带它(VideoHasVideoInput 特意把它排除在
	// "视频输入"之外)—— 正好切开这两类,且不依赖计费阶段拿不到的推断结果。
	if metadataNonEmptyString(req.Metadata, "video") == "" {
		return ""
	}
	return tierFromTargetShortEdge(metadataInt(req.Metadata, "target_short_edge"))
}

// metadataNonEmptyString 取一个去空白后的字符串值(非字符串或缺失返回空)。
func metadataNonEmptyString(md map[string]any, key string) string {
	if md == nil {
		return ""
	}
	s, _ := md[key].(string)
	return strings.TrimSpace(s)
}

// tierFromTargetShortEdge 把**声明的目标短边**归一成计费行名。
//
// 与 tierFromShortEdge(按像素粗分桶)的差别只在 2K,但那一档非改不可:
// gpustackplus/minimax_h3.go 的 h3ShortEdgeFromSizeToken 与前端 videoSizeShortEdge
// 是刻意同口径的一对,把档位词映成短边 —— 2k→1440、4k→2160。本函数是它们的**逆向**,
// 必须原样对上。按 tierFromShortEdge 的分桶走,1440 会落进 ">1080 即 4k",于是用户在
// 体验区选 2K 超分、后端按 4k 行计费(配了 4k 就多收,没配就整个未命中回退固定价)。
//
// **不把 1440 并进 tierFromShortEdge**:那条路服务的是 VideoResolutionTier 对**任意
// 像素串**的归档,改它会让既有 token/per_call 模型的 2560x1440 请求从 4k 行挪到 2k 行
// —— 那是现网计费口径的改变,不属于本次改动。两个函数服务两种输入,不该合并。
//
// 只精确匹配这两个值、其余仍走分桶:target_short_edge 也可能由像素串算出(见前端
// videoSizeShortEdge 的 min(w,h) 分支),那些值没有权威档位表可对,交给 "*" 兜底行
// 比在这里自造一套档位边界诚实。
func tierFromTargetShortEdge(shortEdge int) string {
	switch shortEdge {
	case 1440:
		return "2k"
	case 2160:
		return "4k"
	}
	return tierFromShortEdge(shortEdge)
}

// metadataInt 从 metadata 取一个正整数。JSON 数字按解析方式可能落成 float64 /
// json.Number / 字符串,逐一容忍——只认其中一种会让"配了却不生效"随解析路径变化。
func metadataInt(md map[string]any, key string) int {
	if md == nil {
		return 0
	}
	switch v := md[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return int(n)
		}
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return 0
}

// tierFromShortEdge 按短边归档,与 VideoResolutionTier 的像素分支同一套档位边界。
// 两处必须同源:分叉了就会出现"同一段视频,走 size 归到 1080p、走 target_short_edge
// 归到 4k",而这种错账不报错、只体现在账单上。
func tierFromShortEdge(shortEdge int) string {
	if shortEdge <= 0 {
		return ""
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
	return tierFromShortEdge(shortEdge)
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
