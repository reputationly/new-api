package arkv3

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// 官方 content[].type 枚举。
const (
	contentTypeText      = "text"
	contentTypeImage     = "image_url"
	contentTypeVideo     = "video_url"
	contentTypeAudio     = "audio_url"
	contentTypeDraftTask = "draft_task"
)

// 官方 content[].role 枚举。
const (
	roleFirstFrame     = "first_frame"
	roleLastFrame      = "last_frame"
	roleReferenceImage = "reference_image"
	roleReferenceVideo = "reference_video"
	roleReferenceAudio = "reference_audio"
)

// 官方 ratio 枚举（adaptive 单列）。
var officialRatios = map[string]bool{
	"21:9": true, "16:9": true, "4:3": true, "1:1": true, "3:4": true, "9:16": true,
}

const ratioAdaptive = "adaptive"

// 我们内部的 task_type（跨渠道统一玩法词表）。下发它是为了让适配器不必按输入形态推断
// role —— 单张图既可能是首帧也可能是尾帧，张数推断只会给出 first_frame。
const (
	taskTypeT2V   = "t2v"
	taskTypeI2V   = "i2v"
	taskTypeL2VA  = "l2va"
	taskTypeFLF2V = "flf2v"
	taskTypeR2VA  = "r2va"
)

// 官方媒体数量上限（见「创建视频生成任务」的素材限制）。
const (
	maxReferenceImages = 9
	maxReferenceVideos = 3
)

// 官方明确写死的参数边界。只校验这些 —— 时长档位、分辨率与模型强相关且各模型不同，
// 自己编一套范围会把合法请求挡在网关里，交给上游拒绝更准。
const (
	minPriority              = 0
	maxPriority              = 9
	minExecutionExpiresAfter = 3600
	maxExecutionExpiresAfter = 259200
	maxSafetyIdentifierLen   = 64
	minSeed                  = -1
	maxSeed                  = 4294967295 // 2^32-1
)

// Snapshot 是提交时冻结的回显快照，定义在 model 包 —— 它要随任务落进 Properties 这个
// JSON 列，而 model 不能反向依赖 relay。
type Snapshot = model.ArkV3Properties

// ConvertCreateRequest 把官方请求体转成本仓的统一任务契约 body
// （顶层 model/prompt/images/duration + metadata）。
//
// metadata 里的键是 doubao 适配器的**原生 Ark 字段名**：适配器的 UnmarshalMetadata
// 会把它们直接灌回 Ark 的 requestPayload，所以这一段是原名透传，不是又一层翻译。
// 帧约束与参考媒体则要拆开落点，理由见下面的注释。
func ConvertCreateRequest(raw []byte) (map[string]any, *Snapshot, *APIError) {
	var req CreateRequest
	if err := common.Unmarshal(raw, &req); err != nil {
		// 区分「JSON 语法坏了」与「JSON 合法但某个字段的类型对不上」。混为一谈的后果是
		// 调用方拿着一份自己能解析的 body，看到一句「不是合法 JSON」，去查根本不存在的
		// 语法问题 —— 真正该看的是哪个字段填错了类型。
		var probe any
		if common.Unmarshal(raw, &probe) != nil {
			return nil, nil, badRequest("request body is not valid JSON: " + err.Error())
		}
		return nil, nil, badRequest("request body could not be decoded: " + err.Error())
	}

	modelName := strings.TrimSpace(req.Model)
	if modelName == "" {
		return nil, nil, badRequest("model is required")
	}
	if strings.TrimSpace(req.CallbackURL) != "" {
		return nil, nil, NewNotSupported("callback_url is not supported by this gateway: the callback would be delivered by the upstream provider and would carry the upstream task id, which cannot be used against this gateway; poll GET /api/v3/contents/generations/tasks/{task_id} instead")
	}
	if len(req.Content) == 0 {
		return nil, nil, badRequest("content is required")
	}

	parsed, apiErr := parseContent(req.Content)
	if apiErr != nil {
		return nil, nil, apiErr
	}
	taskType, frameImages := parsed.resolveTaskType()

	resolution := normalizeResolution(req.Resolution)
	ratio, apiErr := normalizeRatio(req.Ratio)
	if apiErr != nil {
		return nil, nil, apiErr
	}
	if apiErr = validateScalars(&req); apiErr != nil {
		return nil, nil, apiErr
	}

	metadata := map[string]any{
		// 显式下发 task_type：适配器的 imageRole 第二优先级读它，免得走「按张数推断」——
		// 单张图推不出「这张是尾帧」（role=last_frame 的 l2va 玩法）。
		"task_type": taskType,
	}
	putString(metadata, "resolution", resolution)
	putString(metadata, "ratio", ratio)
	putString(metadata, "service_tier", req.ServiceTier)
	putString(metadata, "safety_identifier", req.SafetyIdentifier)
	putString(metadata, "omni_reference_task_type", req.OmniReferenceTaskType)
	putString(metadata, "output_format", req.OutputFormat)
	putInt(metadata, "duration", req.Duration.intPtr())
	putInt(metadata, "frames", req.Frames.intPtr())
	putInt(metadata, "seed", req.Seed.intPtr())
	putInt(metadata, "priority", req.Priority.intPtr())
	putInt(metadata, "execution_expires_after", req.ExecutionExpiresAfter.intPtr())
	putBool(metadata, "generate_audio", req.GenerateAudio)
	putBool(metadata, "watermark", req.Watermark)
	putBool(metadata, "camera_fixed", req.CameraFixed)
	putBool(metadata, "return_last_frame", req.ReturnLastFrame)
	putBool(metadata, "draft", req.Draft)
	if len(req.Tools) > 0 {
		metadata["tools"] = toolsToAny(req.Tools)
	}

	// ⚠️ 帧约束与参考媒体的落点不同，不能统一成一个：
	//   帧约束（first/last_frame）走**顶层 images[]**，顺序即语义（[0]=首帧、[1]=尾帧）；
	//   参考媒体走 metadata 的三个键，适配器据此拼 role=reference_* 的 content 条目。
	// 把参考图塞进顶层 images[] 会被按张数推断成首帧约束，语义完全不同。
	if len(parsed.refImages) > 0 {
		metadata["src_ref_images"] = toAnySlice(parsed.refImages)
	}
	// reference_videos 还兼任计费判据：relaycommon.VideoHasVideoInput 只认这个键，
	// 含视频输入的单价与不含是两档（doubao.GetVideoInputRatio）。换个键名就会静默少收。
	if len(parsed.refVideos) > 0 {
		metadata["reference_videos"] = toAnySlice(parsed.refVideos)
	}
	if len(parsed.refAudios) > 0 {
		metadata["reference_audios"] = toAnySlice(parsed.refAudios)
	}

	body := map[string]any{
		"model":    modelName,
		"prompt":   parsed.prompt,
		"metadata": metadata,
	}
	if len(frameImages) > 0 {
		body["images"] = toAnySlice(frameImages)
	}
	// 顶层 duration 是计费维度的唯一来源（relaycommon.videoPerCallSeconds 只认它，
	// 不看 metadata）。没传就不下发 —— 替上游编一个默认值，等上游改了默认就成了错账。
	if req.Duration != nil {
		body["duration"] = int(*req.Duration)
	}

	return body, &Snapshot{
		Resolution:            resolution,
		Ratio:                 ratio,
		Duration:              req.Duration.intOrZero(),
		Frames:                req.Frames.intOrZero(),
		Seed:                  req.Seed.intPtr(),
		GenerateAudio:         req.GenerateAudio,
		Draft:                 req.Draft,
		Tools:                 toolTypes(req.Tools),
		SafetyIdentifier:      req.SafetyIdentifier,
		Priority:              req.Priority.intPtr(),
		ServiceTier:           req.ServiceTier,
		ExecutionExpiresAfter: req.ExecutionExpiresAfter.intOrZero(),
		OmniReferenceTaskType: req.OmniReferenceTaskType,
		OutputFormat:          req.OutputFormat,
	}, nil
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

// resolveTaskType 由 role 组合定出我们的 task_type，并给出顶层 images 的顺序。
func (p *parsedContent) resolveTaskType() (string, []string) {
	switch {
	case p.hasReferences():
		return taskTypeR2VA, nil
	case p.firstFrame != "" && p.lastFrame != "":
		return taskTypeFLF2V, []string{p.firstFrame, p.lastFrame}
	case p.firstFrame != "":
		return taskTypeI2V, []string{p.firstFrame}
	case p.lastFrame != "":
		// 「只给尾帧」必须独立成 l2va：输入形态与 i2v 完全相同（都是 1 张图），
		// 只有语义不同，靠张数推不出「这张是尾帧」。
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
			if textCount > 1 {
				return nil, badRequest("content may contain at most one text item")
			}
			out.prompt = strings.TrimSpace(item.Text)
		case contentTypeImage:
			url, apiErr := mediaURL(i, contentTypeImage, item.ImageURL)
			if apiErr != nil {
				return nil, apiErr
			}
			switch defaultRole(item.Role, roleFirstFrame) {
			case roleFirstFrame:
				if out.firstFrame != "" {
					return nil, badRequest(fmt.Sprintf(
						"content[%d]: at most one image may carry role=first_frame (role defaults to first_frame when omitted; set an explicit role such as reference_image)", i))
				}
				out.firstFrame = url
			case roleLastFrame:
				if out.lastFrame != "" {
					return nil, badRequest(fmt.Sprintf("content[%d]: at most one image may carry role=last_frame", i))
				}
				out.lastFrame = url
			case roleReferenceImage:
				out.refImages = append(out.refImages, url)
			default:
				return nil, badRequest(fmt.Sprintf(
					"content[%d].role=%q is not valid for type=image_url (expected first_frame / last_frame / reference_image)", i, item.Role))
			}
		case contentTypeVideo:
			url, apiErr := mediaURL(i, contentTypeVideo, item.VideoURL)
			if apiErr != nil {
				return nil, apiErr
			}
			if defaultRole(item.Role, roleReferenceVideo) != roleReferenceVideo {
				return nil, badRequest(fmt.Sprintf(
					"content[%d].role=%q is not valid for type=video_url (expected reference_video)", i, item.Role))
			}
			out.refVideos = append(out.refVideos, url)
		case contentTypeAudio:
			url, apiErr := mediaURL(i, contentTypeAudio, item.AudioURL)
			if apiErr != nil {
				return nil, apiErr
			}
			if defaultRole(item.Role, roleReferenceAudio) != roleReferenceAudio {
				return nil, badRequest(fmt.Sprintf(
					"content[%d].role=%q is not valid for type=audio_url (expected reference_audio)", i, item.Role))
			}
			out.refAudios = append(out.refAudios, url)
		case contentTypeDraftTask:
			return nil, NewNotSupported(fmt.Sprintf(
				"content[%d]: draft_task is not supported by this gateway: a draft task id belongs to the upstream provider's namespace, while this gateway only issues its own task_xxx ids — the two are not interchangeable", i))
		default:
			return nil, badRequest(fmt.Sprintf(
				"content[%d].type=%q is not supported (expected text / image_url / video_url / audio_url)", i, item.Type))
		}
	}

	if textCount == 0 && !out.hasFrames() && !out.hasReferences() {
		return nil, badRequest("content must contain at least one text, image_url, video_url or audio_url item")
	}
	if textCount > 0 && out.prompt == "" && !out.hasFrames() && !out.hasReferences() {
		return nil, badRequest("content[].text must not be empty for a text-only request")
	}
	// 官方规定首帧/首尾帧与多模态参考是互斥场景。
	if out.hasFrames() && out.hasReferences() {
		return nil, badRequest("frame roles (first_frame / last_frame) and reference roles (reference_image / reference_video / reference_audio) are mutually exclusive")
	}
	if len(out.refImages) > maxReferenceImages {
		return nil, badRequest(fmt.Sprintf("at most %d reference_image items are allowed, got %d", maxReferenceImages, len(out.refImages)))
	}
	if len(out.refVideos) > maxReferenceVideos {
		return nil, badRequest(fmt.Sprintf("at most %d reference_video items are allowed, got %d", maxReferenceVideos, len(out.refVideos)))
	}
	// 官方：不能单独输入音频。
	if len(out.refAudios) > 0 && len(out.refImages) == 0 && len(out.refVideos) == 0 {
		return nil, badRequest("reference_audio requires at least one reference_image or reference_video")
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
		return "", badRequest(fmt.Sprintf("content[%d].%s.url is required", index, contentType))
	}
	url := strings.TrimSpace(ref.URL)
	// asset:// 指向方舟自己的素材库，本网关没有那套素材库，解析不了。静默透传的话
	// 上游会以一个看不懂的素材 ID 报错，不如就地说清楚。
	if strings.HasPrefix(strings.ToLower(url), "asset://") {
		return "", NewNotSupported(fmt.Sprintf(
			"content[%d].%s.url uses asset:// which references the provider's asset library; this gateway only accepts a public URL or a base64 data URI", index, contentType))
	}
	return url, nil
}

// normalizeResolution 归一分辨率档位。
//
// 归一成小写是刻意的：计费矩阵的行名就是小写（relaycommon.VideoResolutionTier 返回
// strings.ToLower），回显与计费口径同一个字符串才不会出现「账单写 720p、响应写 720P」。
// 不校验具体档位 —— 各模型支持的档不同（Fast 没有 1080p，2.0 才有 4k），网关自己维护
// 一份档位表必然与上游漂移，交给上游拒绝更准。
func normalizeResolution(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func normalizeRatio(raw string) (string, *APIError) {
	ratio := strings.ToLower(strings.TrimSpace(raw))
	if ratio == "" {
		return "", nil
	}
	if ratio != ratioAdaptive && !officialRatios[ratio] {
		return "", badRequest(fmt.Sprintf(
			"ratio=%q is not supported (expected 16:9 / 4:3 / 1:1 / 3:4 / 9:16 / 21:9 / adaptive)", raw))
	}
	return ratio, nil
}

// validateScalars 校验官方写死了边界的那几个标量。
func validateScalars(req *CreateRequest) *APIError {
	if tier := strings.ToLower(strings.TrimSpace(req.ServiceTier)); tier != "" {
		if tier != ServiceTierDefault && tier != ServiceTierFlex {
			return badRequest(fmt.Sprintf("service_tier=%q is not supported (expected default / flex)", req.ServiceTier))
		}
		req.ServiceTier = tier
	}
	if req.Priority != nil {
		if v := int(*req.Priority); v < minPriority || v > maxPriority {
			return badRequest(fmt.Sprintf("priority must be between %d and %d, got %d", minPriority, maxPriority, v))
		}
	}
	if req.ExecutionExpiresAfter != nil {
		if v := int(*req.ExecutionExpiresAfter); v < minExecutionExpiresAfter || v > maxExecutionExpiresAfter {
			return badRequest(fmt.Sprintf("execution_expires_after must be between %d and %d seconds, got %d",
				minExecutionExpiresAfter, maxExecutionExpiresAfter, v))
		}
	}
	if req.Seed != nil {
		if v := int(*req.Seed); v < minSeed || v > maxSeed {
			return badRequest(fmt.Sprintf("seed must be between %d and %d, got %d", minSeed, maxSeed, v))
		}
	}
	if v := strings.ToLower(strings.TrimSpace(req.OmniReferenceTaskType)); v != "" {
		if !omniReferenceTaskTypes[v] {
			return badRequest(fmt.Sprintf("omni_reference_task_type=%q is not supported (expected auto / reference / edit / extend)", req.OmniReferenceTaskType))
		}
		req.OmniReferenceTaskType = v
	}
	if v := strings.ToLower(strings.TrimSpace(req.OutputFormat)); v != "" {
		if !outputFormats[v] {
			return badRequest(fmt.Sprintf("output_format=%q is not supported (expected mp4 / mov)", req.OutputFormat))
		}
		req.OutputFormat = v
	}
	if len(req.SafetyIdentifier) > maxSafetyIdentifierLen {
		return badRequest(fmt.Sprintf("safety_identifier must be at most %d characters, got %d",
			maxSafetyIdentifierLen, len(req.SafetyIdentifier)))
	}
	// duration 与 frames 官方明说二选一（frames 优先级更高）。同时给会让「回显哪个」
	// 与「按哪个计费」产生分歧，就地拒绝比事后解释便宜。
	if req.Duration != nil && req.Frames != nil {
		return badRequest("duration and frames are mutually exclusive; provide only one")
	}
	return nil
}

func putString(m map[string]any, key, val string) {
	if strings.TrimSpace(val) != "" {
		m[key] = val
	}
}

func putInt(m map[string]any, key string, val *int) {
	if val != nil {
		m[key] = *val
	}
}

func putBool(m map[string]any, key string, val *bool) {
	if val != nil {
		m[key] = *val
	}
}

func toolsToAny(tools []Tool) []any {
	out := make([]any, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]any{"type": t.Type})
	}
	return out
}

// toolTypes 落盘时只留 type —— 官方当前的工具对象也只有这一个字段。
func toolTypes(tools []Tool) []string {
	if len(tools) == 0 {
		return nil
	}
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		out = append(out, t.Type)
	}
	return out
}

// toAnySlice 把 []string 转成 []any —— metadata 在下游会被反序列化成 map[string]any
// 再按 []any 解析（doubao 的 metadataStringList），两侧形态一致少一层疑问。
func toAnySlice(in []string) []any {
	out := make([]any, 0, len(in))
	for _, v := range in {
		out = append(out, v)
	}
	return out
}
