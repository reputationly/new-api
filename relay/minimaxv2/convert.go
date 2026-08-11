package minimaxv2

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// 官方 content[].role 枚举。
const (
	roleFirstFrame     = "first_frame"
	roleLastFrame      = "last_frame"
	roleReferenceImage = "reference_image"
	roleReferenceVideo = "reference_video"
	roleReferenceAudio = "reference_audio"
)

// 官方 content[].type 枚举。
const (
	contentTypeText  = "text"
	contentTypeImage = "image_url"
	contentTypeVideo = "video_url"
	contentTypeAudio = "audio_url"
)

// 官方 ratio 枚举中的具名值(adaptive 单列)。与引擎的六个具名比例一一对应
// (vllm-omni pipeline_minimax_h3.MINIMAX_H3_SUPPORTED_ASPECT_RATIOS)。
var officialNamedRatios = map[string]bool{
	"21:9": true, "16:9": true, "4:3": true, "1:1": true, "3:4": true, "9:16": true,
}

const ratioAdaptive = "adaptive"

// r2va 的 adaptive 落到哪个具名比例。
//
// 这不是我们编的默认值:引擎对 Ref2VA 不传 aspect_ratio 时就是按 16:9 走
// (见 gpustackplus/minimax_h3.go 里 applyMiniMaxH3Request 的说明)。显式解析成 16:9
// 的好处是画布能算出来 —— 留空的话 h3ApplyCanvas 找不到具名比例就不推 width/height,
// 于是档位词被丢掉、引擎按 short_edge=768 自算,用户点的 480P 会静默变成 768P。
const r2vaAdaptiveRatio = "16:9"

// 分辨率档。768P 是官方与我们的重叠档;480P 是**我们的扩展档**(官方 schema 里没有),
// 作为输入可接受、回显时如实返回 480P —— 绝不映射成 720p 之类去迎合官方枚举,
// 那会让回显与实际产物不符。2K 依赖闭源的 H3-Regenerate-2K,自建跑不出来。
const (
	resolution768P = "768P"
	resolution480P = "480P"
	resolution2K   = "2K"
)

// 官方 duration 是 4–15 的整数。官方把 17n+5 帧对齐藏了起来,我们也照藏(回显请求值),
// 只是实际产物会多出 ≤0.083 秒。
const (
	minDurationSec = 4
	maxDurationSec = 15
)

// 官方媒体数量上限。引擎侧还有一道总数 ≤12 的闸(gpustackplus 的 maxR2VARefTotal),
// 这里不重复实现——那是引擎能力边界,归适配器管。
const (
	maxReferenceImages = 9
	maxReferenceVideos = 3
	maxReferenceAudios = 3
)

// 我们内部的 task_type(门面词表)。
const (
	taskTypeT2V   = "t2v"
	taskTypeI2V   = "i2v"
	taskTypeL2VA  = "l2va"
	taskTypeFLF2V = "flf2v"
	taskTypeR2VA  = "r2va"
)

// Snapshot 是提交时从请求里推出的回显 / 用量快照。
//
// 必须在提交时冻结:官方查询接口要回显 resolution / ratio / duration 与 usage,
// 而那几个值只存在于请求里 —— 查询发生在几百秒后,请求体早已不在,任务记录里
// 也没有(Task.Data 存的是**上游提交响应**,不含这些)。
type Snapshot struct {
	Resolution      string
	Ratio           string
	Duration        int
	InputImageCount int
	InputVideoCount int
}

// ConvertCreateRequest 把官方 POST /v2/video_generation 的 body 转成本仓的统一
// 任务契约 body(顶层 model/prompt/images/size/duration + metadata)。
//
// **这是真转换,不是「原样塞进 metadata」**:Kling / 即梦那两个兼容层可以那么做,
// 因为它们的上游就是对应厂商;我们上游是自建引擎,官方那套 content[]+role 的形态
// 引擎一个字都不认。
func ConvertCreateRequest(raw []byte) (map[string]any, *Snapshot, *APIError) {
	var req CreateRequest
	if err := common.Unmarshal(raw, &req); err != nil {
		return nil, nil, badRequest(fmt.Sprintf("invalid params: request body is not valid JSON: %s", err.Error()))
	}

	model := strings.TrimSpace(req.Model)
	if model == "" {
		return nil, nil, badRequest("invalid params: model is required")
	}
	// 回调是独立基础设施,本兼容层不做。显式拒绝而不是静默丢弃 —— 静默丢弃会让调用方
	// 一直等一个永远不会来的推送。
	if strings.TrimSpace(req.CallbackURL) != "" {
		return nil, nil, badRequest("callback_url is not supported by this gateway: task status must be polled via GET /v2/query/video_generation/{task_id}")
	}
	if len(req.Content) == 0 {
		return nil, nil, badRequest("invalid params: content is required")
	}

	parsed, apiErr := parseContent(req.Content)
	if apiErr != nil {
		return nil, nil, apiErr
	}

	taskType, images := parsed.resolveTaskType()

	resolution, apiErr := resolveResolution(req.Resolution, taskType)
	if apiErr != nil {
		return nil, nil, apiErr
	}

	aspectRatio, echoRatio, apiErr := resolveRatio(req.Ratio, taskType)
	if apiErr != nil {
		return nil, nil, apiErr
	}

	if req.Duration == nil {
		return nil, nil, badRequest("invalid params: duration is required")
	}
	duration := *req.Duration
	if duration < minDurationSec || duration > maxDurationSec {
		return nil, nil, badRequest(fmt.Sprintf("invalid params: duration must be an integer between %d and %d seconds, got %d",
			minDurationSec, maxDurationSec, duration))
	}

	metadata := map[string]any{
		// 显式下发 task_type:适配器的 taskTypeOfRequest 第一优先级就读它,
		// 免得走「按输入形态推断」—— 单张图推不出「这张是尾帧」(l2va)。
		"task_type": taskType,
	}
	if aspectRatio != "" {
		metadata["aspect_ratio"] = aspectRatio
	}
	// ⚠️ 帧约束与多模态参考的落点不同,不能统一成一个:
	//   帧约束(first/last_frame)走**顶层 images[]**,顺序即语义([0]=首帧、[1]=尾帧);
	//   多模态参考走 metadata 的三个键,与 doubao/Ark 渠道共用同一套字段名。
	// 绝不能把参考图放进顶层 images[] —— doubao 侧对顶层 images 按**张数**推断 role
	// (1 张 = first_frame),单张参考图会被误判成首帧约束。
	if len(parsed.refImages) > 0 {
		metadata["src_ref_images"] = toAnySlice(parsed.refImages)
	}
	if len(parsed.refVideos) > 0 {
		metadata["reference_videos"] = toAnySlice(parsed.refVideos)
	}
	if len(parsed.refAudios) > 0 {
		metadata["reference_audios"] = toAnySlice(parsed.refAudios)
	}

	body := map[string]any{
		"model":  model,
		"prompt": parsed.prompt,
		// 分辨率必须以**档位词**("768P")而不是像素串下发:适配器转发顶层 size 时会用
		// AspectRatioFromSize 反推一个 aspect_ratio 覆盖掉具名比例,而档位词匹配不到
		// WxH 正则、返回空串,于是不会覆盖。详见 gpustackplus/minimax_h3.go。
		"size":     resolution,
		"duration": duration,
		"metadata": metadata,
	}
	if len(images) > 0 {
		body["images"] = toAnySlice(images)
	}

	snapshot := &Snapshot{
		Resolution:      resolution,
		Ratio:           echoRatio,
		Duration:        duration,
		InputImageCount: len(images) + len(parsed.refImages),
		InputVideoCount: len(parsed.refVideos),
	}
	return body, snapshot, nil
}

// parsedContent 是 content[] 解析后的中间形态。
type parsedContent struct {
	prompt     string
	firstFrame string
	lastFrame  string
	refImages  []string
	refVideos  []string
	refAudios  []string
}

func (p *parsedContent) hasFrames() bool {
	return p.firstFrame != "" || p.lastFrame != ""
}

func (p *parsedContent) hasReferences() bool {
	return len(p.refImages)+len(p.refVideos)+len(p.refAudios) > 0
}

// resolveTaskType 由 role 组合定出我们的 task_type,并给出顶层 images 的顺序。
func (p *parsedContent) resolveTaskType() (string, []string) {
	switch {
	case p.hasReferences():
		return taskTypeR2VA, nil
	case p.firstFrame != "" && p.lastFrame != "":
		return taskTypeFLF2V, []string{p.firstFrame, p.lastFrame}
	case p.firstFrame != "":
		return taskTypeI2V, []string{p.firstFrame}
	case p.lastFrame != "":
		// 「只给尾帧」必须独立成 l2va:与 i2v 的输入形态完全相同(都是 1 张图),
		// 只有语义不同,靠张数推不出「这张是尾帧」。
		return taskTypeL2VA, []string{p.lastFrame}
	default:
		return taskTypeT2V, nil
	}
}

func parseContent(items []ContentItem) (*parsedContent, *APIError) {
	out := &parsedContent{}
	textCount := 0

	for i, item := range items {
		switch strings.ToLower(strings.TrimSpace(item.Type)) {
		case contentTypeText:
			textCount++
			out.prompt = item.Text
		case contentTypeImage:
			url, apiErr := mediaURL(i, contentTypeImage, item.ImageURL)
			if apiErr != nil {
				return nil, apiErr
			}
			switch defaultRole(item.Role, roleFirstFrame) {
			case roleFirstFrame:
				if out.firstFrame != "" {
					return nil, badRequest("invalid params: at most one image with role=first_frame is allowed")
				}
				out.firstFrame = url
			case roleLastFrame:
				if out.lastFrame != "" {
					return nil, badRequest("invalid params: at most one image with role=last_frame is allowed")
				}
				out.lastFrame = url
			case roleReferenceImage:
				out.refImages = append(out.refImages, url)
			default:
				return nil, badRequest(fmt.Sprintf(
					"invalid params: content[%d].role=%q is not valid for type=image_url (expected first_frame / last_frame / reference_image)", i, item.Role))
			}
		case contentTypeVideo:
			url, apiErr := mediaURL(i, contentTypeVideo, item.VideoURL)
			if apiErr != nil {
				return nil, apiErr
			}
			if defaultRole(item.Role, roleReferenceVideo) != roleReferenceVideo {
				return nil, badRequest(fmt.Sprintf(
					"invalid params: content[%d].role=%q is not valid for type=video_url (expected reference_video)", i, item.Role))
			}
			out.refVideos = append(out.refVideos, url)
		case contentTypeAudio:
			url, apiErr := mediaURL(i, contentTypeAudio, item.AudioURL)
			if apiErr != nil {
				return nil, apiErr
			}
			if defaultRole(item.Role, roleReferenceAudio) != roleReferenceAudio {
				return nil, badRequest(fmt.Sprintf(
					"invalid params: content[%d].role=%q is not valid for type=audio_url (expected reference_audio)", i, item.Role))
			}
			out.refAudios = append(out.refAudios, url)
		default:
			return nil, badRequest(fmt.Sprintf(
				"invalid params: content[%d].type=%q is not supported (expected text / image_url / video_url / audio_url)", i, item.Type))
		}
	}

	if textCount != 1 || strings.TrimSpace(out.prompt) == "" {
		return nil, badRequest("invalid params: content must contain exactly one non-empty text item")
	}
	// 官方规定「首帧/首尾帧」与「多模态参考」是互斥场景,引擎行为一致
	// (fl2va 与 ref2va 是两个不同的 task,一个请求只能是其中之一)。
	if out.hasFrames() && out.hasReferences() {
		return nil, badRequest("invalid params: frame roles (first_frame / last_frame) and reference roles (reference_image / reference_video / reference_audio) are mutually exclusive")
	}
	if len(out.refImages) > maxReferenceImages {
		return nil, badRequest(fmt.Sprintf("invalid params: at most %d reference_image items are allowed, got %d", maxReferenceImages, len(out.refImages)))
	}
	if len(out.refVideos) > maxReferenceVideos {
		return nil, badRequest(fmt.Sprintf("invalid params: at most %d reference_video items are allowed, got %d", maxReferenceVideos, len(out.refVideos)))
	}
	if len(out.refAudios) > maxReferenceAudios {
		return nil, badRequest(fmt.Sprintf("invalid params: at most %d reference_audio items are allowed, got %d", maxReferenceAudios, len(out.refAudios)))
	}
	// 独立音频参考必须搭配视觉参考(引擎 pipeline 同样硬校验)。
	if len(out.refAudios) > 0 && len(out.refImages) == 0 && len(out.refVideos) == 0 {
		return nil, badRequest("invalid params: reference_audio requires at least one reference_image or reference_video")
	}
	return out, nil
}

func defaultRole(role, fallback string) string {
	r := strings.ToLower(strings.TrimSpace(role))
	if r == "" {
		return fallback
	}
	return r
}

func mediaURL(index int, contentType string, ref *MediaRef) (string, *APIError) {
	if ref == nil || strings.TrimSpace(ref.URL) == "" {
		return "", badRequest(fmt.Sprintf("invalid params: content[%d].%s.url is required", index, contentType))
	}
	url := strings.TrimSpace(ref.URL)
	// mm_file:// 是 MiniMax 平台自己的文件库指针,我们没有那个文件库,解析不了。
	if strings.HasPrefix(strings.ToLower(url), "mm_file://") {
		return "", badRequest(fmt.Sprintf(
			"invalid params: content[%d].%s.url uses mm_file:// which references the MiniMax platform file store; this gateway only accepts a public URL or a base64 data URI",
			index, contentType))
	}
	return url, nil
}

// resolveResolution 校验并归一分辨率档。
func resolveResolution(raw, taskType string) (string, *APIError) {
	res := strings.ToUpper(strings.TrimSpace(raw))
	if res == "" {
		return "", badRequest("invalid params: resolution is required")
	}
	switch res {
	case resolution2K:
		return "", badRequest("resolution=2K is not supported by this gateway: the self-hosted MiniMax-H3 deployment tops out at 768P (2K depends on the closed-source H3-Regenerate-2K model)")
	case resolution768P:
		return res, nil
	case resolution480P:
		// 480P 是我们的扩展档,靠网关自己把「档位 + 具名比例」换算成 width/height 下发
		// (不下发尺寸的话引擎硬校验 short_edge==768,只能出 768P)。
		//
		// 而**帧约束模式算不出这个画布**:FL2VA 的画幅永远跟随第一张图,网关拿到的是
		// URL / base64,不解码就不知道原图宽高比。与其静默出 768P 却回显 480P
		// (回显与产物不符),不如就地说清楚。
		if isFrameTaskType(taskType) {
			return "", badRequest("resolution=480P is not available for first_frame / last_frame requests: the output canvas follows the input image, and this gateway does not decode input images to derive it — use 768P, or send an explicit width/height via the native /v1/video/generations endpoint")
		}
		return res, nil
	default:
		return "", badRequest(fmt.Sprintf("invalid params: resolution=%q is not supported (expected 768P, or 480P as a gateway extension)", raw))
	}
}

// resolveRatio 校验比例,返回 (下发给引擎的 aspect_ratio, 回显给调用方的 ratio)。
//
// 三种场景的官方规定,我们引擎的行为与之完全一致,照抄即可:
//   - t2va:必填,且不能是 adaptive;
//   - i2va/fl2va:输入图决定画幅,传了也忽略;
//   - ref2va:可选,缺省 adaptive,也可显式给具名值。
func resolveRatio(raw, taskType string) (aspectRatio, echoRatio string, apiErr *APIError) {
	ratio := strings.ToLower(strings.TrimSpace(raw))
	if ratio != "" && ratio != ratioAdaptive && !officialNamedRatios[ratio] {
		return "", "", badRequest(fmt.Sprintf(
			"invalid params: ratio=%q is not supported (expected adaptive / 21:9 / 16:9 / 4:3 / 1:1 / 3:4 / 9:16)", raw))
	}

	switch taskType {
	case taskTypeT2V:
		if ratio == "" || ratio == ratioAdaptive {
			return "", "", badRequest("invalid params: text-to-video requires an explicit named ratio (adaptive is not allowed)")
		}
		return ratio, ratio, nil
	case taskTypeR2VA:
		if ratio == "" || ratio == ratioAdaptive {
			// 回显解析后的具名值而不是 adaptive:这就是实际产物的比例,回显它更诚实。
			return r2vaAdaptiveRatio, r2vaAdaptiveRatio, nil
		}
		return ratio, ratio, nil
	default:
		// 帧约束:画幅永远跟随第一张图。不下发 aspect_ratio(引擎静默忽略,发过去只是噪音),
		// 回显 adaptive —— 输出比例确实是随输入自适应的。
		return "", ratioAdaptive, nil
	}
}

func isFrameTaskType(taskType string) bool {
	return taskType == taskTypeI2V || taskType == taskTypeL2VA || taskType == taskTypeFLF2V
}

// toAnySlice 把 []string 转成 []any。
//
// 统一契约的 body 是 map[string]any 再整体 Marshal,[]string 本可直接放进去;
// 但 metadata 在下游会被反序列化成 map[string]any 再按 []any 解析
// (gpustackplus 的 metadataStringList 只认 string / []any / []string),
// 这里统一成 []any 让两侧形态一致,少一层「为什么这条路径不走那个 case」的疑问。
func toAnySlice(in []string) []any {
	out := make([]any, 0, len(in))
	for _, v := range in {
		out = append(out, v)
	}
	return out
}
