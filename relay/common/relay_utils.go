package common

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"

	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
)

type HasPrompt interface {
	GetPrompt() string
}

type HasImage interface {
	HasImage() bool
}

func GetFullRequestURL(baseURL string, requestURL string, channelType int) string {
	fullRequestURL := fmt.Sprintf("%s%s", baseURL, requestURL)

	if strings.HasPrefix(baseURL, "https://gateway.ai.cloudflare.com") {
		switch channelType {
		case constant.ChannelTypeOpenAI:
			fullRequestURL = fmt.Sprintf("%s%s", baseURL, strings.TrimPrefix(requestURL, "/v1"))
		case constant.ChannelTypeAzure:
			fullRequestURL = fmt.Sprintf("%s%s", baseURL, strings.TrimPrefix(requestURL, "/openai/deployments"))
		}
	}
	return fullRequestURL
}

func GetAPIVersion(c *gin.Context) string {
	query := c.Request.URL.Query()
	apiVersion := query.Get("api-version")
	if apiVersion == "" {
		apiVersion = c.GetString("api_version")
	}
	return apiVersion
}

func createTaskError(err error, code string, statusCode int, localError bool) *dto.TaskError {
	return &dto.TaskError{
		Code:       code,
		Message:    err.Error(),
		StatusCode: statusCode,
		LocalError: localError,
		Error:      err,
	}
}

func storeTaskRequest(c *gin.Context, info *RelayInfo, action string, requestObj TaskSubmitReq) {
	info.Action = action
	c.Set("task_request", requestObj)
}
func GetTaskRequest(c *gin.Context) (TaskSubmitReq, error) {
	v, exists := c.Get("task_request")
	if !exists {
		return TaskSubmitReq{}, fmt.Errorf("request not found in context")
	}
	req, ok := v.(TaskSubmitReq)
	if !ok {
		return TaskSubmitReq{}, fmt.Errorf("invalid task request type")
	}
	return req, nil
}

func validatePrompt(prompt string) *dto.TaskError {
	if strings.TrimSpace(prompt) == "" {
		return createTaskError(fmt.Errorf("prompt is required"), "invalid_request", http.StatusBadRequest, true)
	}
	return nil
}

func validateMultipartTaskRequest(c *gin.Context, info *RelayInfo, action string) (TaskSubmitReq, error) {
	var req TaskSubmitReq
	if _, err := c.MultipartForm(); err != nil {
		return req, err
	}

	formData := c.Request.PostForm
	req = TaskSubmitReq{
		Prompt:   formData.Get("prompt"),
		Model:    formData.Get("model"),
		Mode:     formData.Get("mode"),
		Image:    formData.Get("image"),
		Size:     formData.Get("size"),
		Metadata: make(map[string]interface{}),
	}

	if durationStr := formData.Get("seconds"); durationStr != "" {
		if duration, err := strconv.Atoi(durationStr); err == nil {
			req.Duration = duration
		}
	}

	if images := formData["images"]; len(images) > 0 {
		req.Images = images
	}

	for key, values := range formData {
		if len(values) > 0 && !isKnownTaskField(key) {
			if intVal, err := strconv.Atoi(values[0]); err == nil {
				req.Metadata[key] = intVal
			} else if floatVal, err := strconv.ParseFloat(values[0], 64); err == nil {
				req.Metadata[key] = floatVal
			} else {
				req.Metadata[key] = values[0]
			}
		}
	}
	return req, nil
}

func ValidateMultipartDirect(c *gin.Context, info *RelayInfo) *dto.TaskError {
	var prompt string
	var model string
	var seconds int
	var size string
	var hasInputReference bool

	var req TaskSubmitReq
	if err := common.UnmarshalBodyReusable(c, &req); err != nil {
		return createTaskError(err, "invalid_json", http.StatusBadRequest, true)
	}

	prompt = req.Prompt
	model = req.Model
	size = req.Size
	seconds, _ = strconv.Atoi(req.Seconds)
	if seconds == 0 {
		seconds = req.Duration
	}
	if req.InputReference != "" {
		req.Images = []string{req.InputReference}
	}

	if strings.TrimSpace(req.Model) == "" {
		return createTaskError(fmt.Errorf("model field is required"), "missing_model", http.StatusBadRequest, true)
	}

	if req.HasImage() {
		hasInputReference = true
	}

	if taskErr := validatePrompt(prompt); taskErr != nil {
		return taskErr
	}

	action := constant.TaskActionTextGenerate
	if hasInputReference {
		action = constant.TaskActionGenerate
	}
	if strings.HasPrefix(model, "sora-2") {

		if size == "" {
			size = "720x1280"
		}

		if seconds <= 0 {
			seconds = 4
		}

		if model == "sora-2" && !lo.Contains([]string{"720x1280", "1280x720"}, size) {
			return createTaskError(fmt.Errorf("sora-2 size is invalid"), "invalid_size", http.StatusBadRequest, true)
		}
		if model == "sora-2-pro" && !lo.Contains([]string{"720x1280", "1280x720", "1792x1024", "1024x1792"}, size) {
			return createTaskError(fmt.Errorf("sora-2 size is invalid"), "invalid_size", http.StatusBadRequest, true)
		}
		// OtherRatios 已移到 Sora adaptor 的 EstimateBilling 中设置
	}

	storeTaskRequest(c, info, action, req)

	return nil
}

func isKnownTaskField(field string) bool {
	knownFields := map[string]bool{
		"prompt":          true,
		"model":           true,
		"mode":            true,
		"image":           true,
		"images":          true,
		"size":            true,
		"duration":        true,
		"input_reference": true, // Sora 特有字段
	}
	return knownFields[field]
}

// TaskValidateOption 让**知道上游契约的那一层**（各渠道适配器）微调基础校验。
//
// 存在的理由：「空 prompt 合不合法」各上游并不一致 —— 火山方舟对图生视频 / 参考生视频
// 明确写着「文本（可选）+ 图片」，而我们自建 H3 的 MiniMax v2 兼容层反过来强制要求
// 非空文本。在这个共用函数里全局放宽，会把本该被网关就地拦下的请求放到不接受它的
// 引擎上去失败；全局收紧则砍掉了上游明确支持的用法。两难的根因是判据放错了层。
type TaskValidateOption func(*taskValidateConfig)

type taskValidateConfig struct {
	promptOptionalWithMedia bool
}

// PromptOptionalWithMedia 声明「该上游在带了视觉输入时不要求提示词」。
// 纯文生视频不受影响，仍然必填 —— 没有任何输入决定输出时，空 prompt 就是错的。
func PromptOptionalWithMedia() TaskValidateOption {
	return func(cfg *taskValidateConfig) { cfg.promptOptionalWithMedia = true }
}

// taskRequestHasMedia 判断请求是否带了视觉 / 听觉输入。
//
// ⚠️ 不能只看 req.Images：`image` 与 `input_reference` 到 Images 的归一化发生在
// validatePrompt **之后**（见本函数调用点下方那两段兼容代码），只看 Images 会把
// 单图上传和 OpenAI 风格的条件图判成「没有媒体」，于是豁免失效、报一个自相矛盾的
// 「传了图却说缺提示词」。
func taskRequestHasMedia(req *TaskSubmitReq) bool {
	if len(req.Images) > 0 ||
		strings.TrimSpace(req.Image) != "" ||
		strings.TrimSpace(req.InputReference) != "" {
		return true
	}
	// metadata 侧的参考媒体：src_ref_images 是多模态参考图，另两个键与 VideoHasVideoInput
	// 同名同源。content 是调用方自排 Ark content[] 的逃生口，里面也可能只有媒体。
	for _, key := range []string{"src_ref_images", "reference_videos", "reference_video",
		"reference_images", "reference_audios", "reference_audio", "content"} {
		switch v := req.Metadata[key].(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				return true
			}
		case []any:
			if len(v) > 0 {
				return true
			}
		}
	}
	return false
}

func ValidateBasicTaskRequest(c *gin.Context, info *RelayInfo, action string, opts ...TaskValidateOption) *dto.TaskError {
	cfg := taskValidateConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	var err error
	contentType := c.GetHeader("Content-Type")
	var req TaskSubmitReq
	if strings.HasPrefix(contentType, "multipart/form-data") {
		req, err = validateMultipartTaskRequest(c, info, action)
		if err != nil {
			return createTaskError(err, "invalid_multipart_form", http.StatusBadRequest, true)
		}
	}
	// 为了metadata字段的兼容性，统一UnmarshalBodyReusable
	if err := common.UnmarshalBodyReusable(c, &req); err != nil {
		return createTaskError(err, "invalid_request", http.StatusBadRequest, true)
	}

	// 这些任务类型的输出不依赖提示词,允许空 prompt:
	//   sr —— 视频超分,输出由源视频(metadata.video)决定;
	//   v2a/v2m —— AudioX 视频→音效/音乐,纯视频输入(metadata.video),无需文本;
	//   svs —— SoulX 歌声合成,输入是参考音+目标曲(metadata.prompt_audio/target_audio),
	//          文本仅占位(gpustackplus adaptor 会为空时兜底一个 label)。
	// 其余任务类型仍必填提示词。
	promptOptionalTaskTypes := map[string]bool{
		"sr": true, "v2a": true, "v2m": true, "svs": true,
	}
	// 有效 task_type:显式 metadata.task_type 优先;缺失时对靠模型名推断的场景做最小兜底,
	// 避免直连(省略 task_type)的空 prompt 请求在 gpustackplus adaptor 推断出 task_type
	// 之前就被本函数误拒:
	//   soulx-singer 系 → svs(无文本歌声合成);
	//   v2a/dub 系   → v2a(视频配乐,LTX-2.3,2026-07 契约:prompt 可选,空=按画面自由
	//                  配环境音)。token 与 gpustackplus adaptor.inferTaskType /
	//                  gpustack-ui task-inputs.ts 保持镜像(仅任务 token v2a/dub,不匹配
	//                  模型家族名),三处必须同步演进。
	// v2m 仍需显式 task_type(AudioX 视频生音乐,行为不变);audiox 默认 t2a 必填 prompt。
	effectiveTaskType, _ := req.Metadata["task_type"].(string)
	if effectiveTaskType == "" {
		m := strings.ToLower(req.Model)
		if strings.Contains(m, "soulx") || strings.Contains(m, "singer") {
			effectiveTaskType = "svs"
		} else if strings.Contains(m, "v2a") || strings.Contains(m, "dub") {
			effectiveTaskType = "v2a"
		}
	}
	// 第二条豁免来自适配器（PromptOptionalWithMedia）：判据是「这个上游带媒体时不要求
	// 提示词」，与上面按 task_type 的豁免正交 —— 那张表管的是"这类任务的输出不看文本"，
	// 这一条管的是"这个上游允许只给图"。
	promptOptional := promptOptionalTaskTypes[effectiveTaskType] ||
		(cfg.promptOptionalWithMedia && taskRequestHasMedia(&req))
	if !promptOptional {
		if taskErr := validatePrompt(req.Prompt); taskErr != nil {
			return taskErr
		}
	}

	if len(req.Images) == 0 && strings.TrimSpace(req.Image) != "" {
		// 兼容单图上传
		req.Images = []string{req.Image}
	}
	// OpenAI /v1/videos 风格用 input_reference 传条件图。这里不归一化的话,除 gpustackplus
	// (在自己的 adaptor 里补了一遍)之外的任务渠道都拿不到条件图,图生视频会被静默降级成
	// 文生视频——同一份请求换个渠道行为就变了,正是网关该抹平的差异。
	if len(req.Images) == 0 && strings.TrimSpace(req.InputReference) != "" {
		req.Images = []string{strings.TrimSpace(req.InputReference)}
	}

	storeTaskRequest(c, info, action, req)
	return nil
}
