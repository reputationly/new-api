package doubao

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
	"github.com/samber/lo"
)

// ============================
// Request / Response structures
// ============================

type ContentItem struct {
	Type     string    `json:"type,omitempty"`
	Text     string    `json:"text,omitempty"`
	ImageURL *MediaURL `json:"image_url,omitempty"`
	VideoURL *MediaURL `json:"video_url,omitempty"`
	AudioURL *MediaURL `json:"audio_url,omitempty"`
	Role     string    `json:"role,omitempty"`
}

type MediaURL struct {
	URL string `json:"url,omitempty"`
}

type requestPayload struct {
	Model                 string         `json:"model"`
	Content               []ContentItem  `json:"content,omitempty"`
	CallbackURL           string         `json:"callback_url,omitempty"`
	ReturnLastFrame       *dto.BoolValue `json:"return_last_frame,omitempty"`
	ServiceTier           string         `json:"service_tier,omitempty"`
	ExecutionExpiresAfter *dto.IntValue  `json:"execution_expires_after,omitempty"`
	GenerateAudio         *dto.BoolValue `json:"generate_audio,omitempty"`
	Draft                 *dto.BoolValue `json:"draft,omitempty"`
	Tools                 []struct {
		Type string `json:"type,omitempty"`
	} `json:"tools,omitempty"`
	Resolution  string         `json:"resolution,omitempty"`
	Ratio       string         `json:"ratio,omitempty"`
	Duration    *dto.IntValue  `json:"duration,omitempty"`
	Frames      *dto.IntValue  `json:"frames,omitempty"`
	Seed        *dto.IntValue  `json:"seed,omitempty"`
	CameraFixed *dto.BoolValue `json:"camera_fixed,omitempty"`
	Watermark   *dto.BoolValue `json:"watermark,omitempty"`
	// Priority 执行优先级 [0,9],仅 Seedance 2.0;SafetyIdentifier 终端用户标识(≤64 字符)。
	// 两者与上面的字段一样从 metadata 透传——结构体没有对应字段的话 UnmarshalMetadata 会直接丢弃。
	Priority         *dto.IntValue `json:"priority,omitempty"`
	SafetyIdentifier string        `json:"safety_identifier,omitempty"`
}

type responsePayload struct {
	ID string `json:"id"` // task_id
}

type responseTask struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Status  string `json:"status"`
	Content struct {
		VideoURL string `json:"video_url"`
	} `json:"content"`
	Seed       int    `json:"seed"`
	Resolution string `json:"resolution"`
	// Duration / Output.Duration 都是"实际生成时长(秒)"的回执。两处都读:火山方舟文档把它
	// 放在顶层,四海文档自己前后不一致(提交/查询对照表写顶层 duration,响应字段表写
	// output.duration)。只读一处的话,换个上游就静默取到 0。
	Duration int `json:"duration"`
	Output   struct {
		Duration int `json:"duration"`
	} `json:"output"`
	Ratio           string `json:"ratio"`
	FramesPerSecond int    `json:"framespersecond"`
	ServiceTier     string `json:"service_tier"`
	Tools           []struct {
		Type string `json:"type"`
	} `json:"tools"`
	Usage struct {
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
		ToolUsage        struct {
			WebSearch int `json:"web_search"`
		} `json:"tool_usage"`
	} `json:"usage"`
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	CreatedAt int64 `json:"created_at"`
	UpdatedAt int64 `json:"updated_at"`
}

// ============================
// Adaptor implementation
// ============================

type TaskAdaptor struct {
	taskcommon.BaseBilling
	ChannelType int
	apiKey      string
	baseURL     string
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.ChannelType = info.ChannelType
	a.baseURL = info.ChannelBaseUrl
	a.apiKey = info.ApiKey
}

// ValidateRequestAndSetAction parses body, validates fields and sets default action.
func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) (taskErr *dto.TaskError) {
	// Accept only POST /v1/video/generations as "generate" action.
	return relaycommon.ValidateBasicTaskRequest(c, info, constant.TaskActionGenerate)
}

// BuildRequestURL constructs the upstream URL.
func (a *TaskAdaptor) BuildRequestURL(_ *relaycommon.RelayInfo) (string, error) {
	return fmt.Sprintf("%s/api/v3/contents/generations/tasks", a.baseURL), nil
}

// BuildRequestHeader sets required headers.
func (a *TaskAdaptor) BuildRequestHeader(_ *gin.Context, req *http.Request, _ *relaycommon.RelayInfo) error {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	return nil
}

// EstimateBilling 检测请求 metadata 中是否包含视频输入，返回视频折扣 OtherRatio。
func (a *TaskAdaptor) EstimateBilling(c *gin.Context, info *relaycommon.RelayInfo) map[string]float64 {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil
	}
	if hasVideoInMetadata(req.Metadata) {
		if ratio, ok := GetVideoInputRatio(info.OriginModelName); ok {
			return map[string]float64{"video_input": ratio}
		}
	}
	return nil
}

// hasVideoInMetadata 见 relaycommon.VideoHasVideoInput——与计费矩阵共用同一份判定。
func hasVideoInMetadata(metadata map[string]interface{}) bool {
	return relaycommon.VideoHasVideoInput(metadata)
}

// BuildRequestBody converts request into Doubao specific format.
func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil, err
	}

	body, err := a.convertToRequestPayload(&req)
	if err != nil {
		return nil, errors.Wrap(err, "convert request payload failed")
	}
	if info.IsModelMapped {
		body.Model = info.UpstreamModelName
	} else {
		info.UpstreamModelName = body.Model
	}
	data, err := common.Marshal(body)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(data), nil
}

// DoRequest delegates to common helper.
func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	return channel.DoTaskApiRequest(a, c, info, requestBody)
}

// DoResponse handles upstream response, returns taskID etc.
func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (taskID string, taskData []byte, taskErr *dto.TaskError) {
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		taskErr = service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
		return
	}
	_ = resp.Body.Close()

	// Parse Doubao response
	var dResp responsePayload
	if err := common.Unmarshal(responseBody, &dResp); err != nil {
		taskErr = service.TaskErrorWrapper(errors.Wrapf(err, "body: %s", responseBody), "unmarshal_response_body_failed", http.StatusInternalServerError)
		return
	}

	if dResp.ID == "" {
		taskErr = service.TaskErrorWrapper(fmt.Errorf("task_id is empty"), "invalid_response", http.StatusInternalServerError)
		return
	}

	ov := dto.NewOpenAIVideo()
	ov.ID = info.PublicTaskID
	ov.TaskID = info.PublicTaskID
	ov.CreatedAt = time.Now().Unix()
	ov.Model = info.OriginModelName

	c.JSON(http.StatusOK, ov)
	return dResp.ID, responseBody, nil
}

// FetchTask fetch task status
func (a *TaskAdaptor) FetchTask(baseUrl, key string, body map[string]any, proxy string) (*http.Response, error) {
	taskID, ok := body["task_id"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid task_id")
	}

	uri := fmt.Sprintf("%s/api/v3/contents/generations/tasks/%s", baseUrl, taskID)

	req, err := http.NewRequest(http.MethodGet, uri, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)

	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		return nil, fmt.Errorf("new proxy http client failed: %w", err)
	}
	return client.Do(req)
}

func (a *TaskAdaptor) GetModelList() []string {
	return ModelList
}

func (a *TaskAdaptor) GetChannelName() string {
	return ChannelName
}

func (a *TaskAdaptor) convertToRequestPayload(req *relaycommon.TaskSubmitReq) (*requestPayload, error) {
	r := requestPayload{
		Model:   req.Model,
		Content: []ContentItem{},
	}

	metadata := req.Metadata
	if err := taskcommon.UnmarshalMetadata(metadata, &r); err != nil {
		return nil, errors.Wrap(err, "unmarshal metadata failed")
	}

	// metadata.content 是整包覆盖的逃生口:客户端自己排好了 Ark 的 content 数组(含 role),
	// 此时不再按统一契约拼装,只在最后补文本。没给才按 images / 参考媒体自动拼。
	if len(r.Content) == 0 {
		r.Content = buildArkContent(req, metadata)
	}

	if sec, _ := strconv.Atoi(req.Seconds); sec > 0 {
		r.Duration = lo.ToPtr(dto.IntValue(sec))
	}

	// 顶层 size / duration 是 new-api 统一视频契约(OpenAI /v1/videos 形状)的字段,
	// Ark 只认 resolution + ratio + duration。不映射的话,同一份请求发到自建渠道
	// (gpustackplus,读顶层 size)能出对分辨率,发到 Seedance 就整个丢掉——下游得按
	// 渠道写两套参数,这正是网关该屏蔽掉的差异。
	//
	// metadata 里显式给的 Ark 原生键优先,这里只在其缺省时兜底,老调用方行为不变。
	applyTopLevelSize(&r, req.Size)
	if r.Duration == nil && req.Duration > 0 {
		r.Duration = lo.ToPtr(dto.IntValue(req.Duration))
	}

	r.Content = lo.Reject(r.Content, func(c ContentItem, _ int) bool { return c.Type == "text" })
	r.Content = append(r.Content, ContentItem{
		Type: "text",
		Text: req.Prompt,
	})

	return &r, nil
}

// failureReason 组失败原因:优先上游描述,并带上机器可读的 error.code
// (video_task_failed / video_task_expired / video_task_billing_failed 等),
// 便于事后区分"生成失败"与"超时/计费收口失败"。
func failureReason(resTask *responseTask, fallback string) string {
	message := strings.TrimSpace(resTask.Error.Message)
	if message == "" {
		message = fallback
	}
	if code := strings.TrimSpace(resTask.Error.Code); code != "" {
		return fmt.Sprintf("%s (%s)", message, code)
	}
	return message
}

// actualDuration 取上游回执的实际生成时长(秒),顶层与 output 下取先命中的非零值。
func (r *responseTask) actualDuration() int {
	if r.Duration > 0 {
		return r.Duration
	}
	return r.Output.Duration
}

// buildArkContent 把统一契约的输入拼成 Ark 的 content[] 数组。
//
// Ark 靠每个 content 项的 role 区分语义(首帧 / 尾帧 / 多模态参考),不带 role 的多图上游
// 无法解释——这也是首尾帧、2.0 多模态参考此前发不出去的原因。参考视频 / 音频没有对应的
// 顶层字段,走 metadata.reference_videos / reference_audios(单串或数组均可)。
func buildArkContent(req *relaycommon.TaskSubmitReq, metadata map[string]any) []ContentItem {
	items := make([]ContentItem, 0, len(req.Images)+2)
	for i, imgURL := range req.Images {
		items = append(items, ContentItem{
			Type:     "image_url",
			ImageURL: &MediaURL{URL: imgURL},
			Role:     imageRole(len(req.Images), i, metadata),
		})
	}
	// 体验区「图生视频」把参考图放在 metadata.src_ref_images(自建门面的字段名),不走顶层
	// images。不认它的话图会被整个丢掉、请求静默降级成纯文生视频——用户传了图却看不出没生效。
	// 这批的语义就是多模态参考图,role 固定 reference_image:image_role 是给顶层 images 的
	// 张数推断兜底用的(1 张=首帧),套到参考图上只会把它误判成首帧。
	// 这里不设张数上限,让 API 调用方能用满 2.0 的 1~9 张,超了由上游报错;体验区自己另有
	// 3 张的 UI 上限(MAX_R2V_REF_IMAGES),两者互不影响。
	for _, imgURL := range metadataStringList(metadata, "src_ref_images") {
		items = append(items, ContentItem{
			Type: "image_url", ImageURL: &MediaURL{URL: imgURL}, Role: "reference_image",
		})
	}
	for _, videoURL := range metadataStringList(metadata, "reference_videos", "reference_video") {
		items = append(items, ContentItem{
			Type: "video_url", VideoURL: &MediaURL{URL: videoURL}, Role: "reference_video",
		})
	}
	for _, audioURL := range metadataStringList(metadata, "reference_audios", "reference_audio") {
		items = append(items, ContentItem{
			Type: "audio_url", AudioURL: &MediaURL{URL: audioURL}, Role: "reference_audio",
		})
	}
	return items
}

// imageRole 决定第 index 张图的 role。优先级:
//
//	metadata.image_role(显式)  >  metadata.task_type(统一契约)  >  张数推断
//
// **张数推断有个致命盲区**:1 张图既可能是首帧也可能是尾帧,推断只会给出 first_frame。
// 体验区「关键帧」tab 的「只给尾帧」玩法(门面 task_type=l2va)正是 1 张图,落到张数
// 推断上会被静默当成首帧渲染 —— 用户要的是"从尾帧反推开头",拿到的是从错误一端生成
// 的、看起来完全正常的视频,全链路无任何报错。
//
// 所以这里认 task_type:它是 new-api 的跨渠道统一玩法词表,前端对所有视频模型都会下发
// (见 useVideoGeneration 的关键帧三态派生)。让 doubao 读它,前端就不必按渠道分支发
// 不同字段 —— 那才是"传了却不生效"的温床。自建链路的等价物是门面回填
// extra_params.frame_indices(gpustack routes/videos.py 的 _H3_TASK_MAP)。
func imageRole(total, index int, metadata map[string]any) string {
	if explicit := metadataString(metadata, "image_role"); explicit != "" {
		return explicit
	}
	switch metadataString(metadata, "task_type") {
	case "l2va": // 只给尾帧,反推开头
		return "last_frame"
	case "i2v": // 只给首帧
		return "first_frame"
	case "flf2v": // 首帧 + 尾帧
		if index == 0 {
			return "first_frame"
		}
		return "last_frame"
	case "r2va": // 多模态参考(顶层 images 走这条时按参考图处理,不是帧约束)
		return "reference_image"
	}
	switch {
	case total == 1:
		return "first_frame"
	case total == 2:
		if index == 0 {
			return "first_frame"
		}
		return "last_frame"
	default:
		return "reference_image"
	}
}

// metadataString 取 metadata 里的字符串值(非字符串或缺失返回 "")。
func metadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	s, _ := metadata[key].(string)
	return strings.TrimSpace(s)
}

// metadataStringList 按 keys 顺序取第一个命中的值,支持单串与数组两种写法。
func metadataStringList(metadata map[string]any, keys ...string) []string {
	if metadata == nil {
		return nil
	}
	for _, key := range keys {
		switch v := metadata[key].(type) {
		case string:
			if s := strings.TrimSpace(v); s != "" {
				return []string{s}
			}
		case []any:
			var out []string
			for _, item := range v {
				if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
					out = append(out, strings.TrimSpace(s))
				}
			}
			if len(out) > 0 {
				return out
			}
		}
	}
	return nil
}

// applyTopLevelSize 把统一契约的顶层 size 翻译成 Ark 的 ratio / resolution。
// size 有三种合法形态(见 API 文档「创建视频生成任务」):档位("720P")、纯比例("16:9")
// 与精确像素("1280x720")。Ark 不吃像素,所以像素形态要拆成比例 + 分辨率档位。
// 已由 metadata 显式指定的字段不覆盖。
//
// 分辨率归档委托给 relaycommon.VideoResolutionTier——计费矩阵查的是同一个函数,
// 两边一旦分叉就会出现「按 720p 收费、实际生成 1080p」这类静默错账。
func applyTopLevelSize(r *requestPayload, size string) {
	size = strings.TrimSpace(size)
	if size == "" {
		return
	}
	if common.IsAspectRatio(size) {
		if r.Ratio == "" {
			r.Ratio = common.NormalizeAspectRatio(size)
		}
		return
	}
	if _, _, ok := common.DimsFromSize(size); ok && r.Ratio == "" {
		if ar := common.AspectRatioFromSize(size); ar != "" {
			r.Ratio = ar
		}
	}
	if r.Resolution == "" {
		r.Resolution = relaycommon.VideoResolutionTier(size)
	}
}

func (a *TaskAdaptor) ParseTaskResult(respBody []byte) (*relaycommon.TaskInfo, error) {
	resTask := responseTask{}
	if err := common.Unmarshal(respBody, &resTask); err != nil {
		return nil, errors.Wrap(err, "unmarshal task result failed")
	}

	taskResult := relaycommon.TaskInfo{
		Code: 0,
	}

	// Map Doubao status to internal status
	switch resTask.Status {
	case "pending", "queued":
		taskResult.Status = model.TaskStatusQueued
		taskResult.Progress = "10%"
	case "processing", "running":
		taskResult.Status = model.TaskStatusInProgress
		taskResult.Progress = "50%"
	case "succeeded":
		taskResult.Status = model.TaskStatusSuccess
		taskResult.Progress = "100%"
		taskResult.Url = resTask.Content.VideoURL
		// 解析 usage 信息用于按倍率计费
		taskResult.CompletionTokens = resTask.Usage.CompletionTokens
		taskResult.TotalTokens = resTask.Usage.TotalTokens
	case "failed":
		taskResult.Status = model.TaskStatusFailure
		taskResult.Progress = "100%"
		taskResult.Reason = failureReason(&resTask, "生成失败")
	case "expired", "cancelled", "canceled":
		// 超时结束 / 被取消同样是终态。漏掉的话 default 会把它当"生成中",任务永远轮询
		// 不到终点:既不落 FAILURE、也不触发退款(见 controller/task_video.go 的失败分支)。
		taskResult.Status = model.TaskStatusFailure
		taskResult.Progress = "100%"
		taskResult.Reason = failureReason(&resTask, "任务已"+resTask.Status)
	default:
		// Unknown status, treat as processing
		taskResult.Status = model.TaskStatusInProgress
		taskResult.Progress = "30%"
	}

	return &taskResult, nil
}

func (a *TaskAdaptor) ConvertToOpenAIVideo(originTask *model.Task) ([]byte, error) {
	var dResp responseTask
	if err := common.Unmarshal(originTask.Data, &dResp); err != nil {
		return nil, errors.Wrap(err, "unmarshal doubao task data failed")
	}

	openAIVideo := dto.NewOpenAIVideo()
	openAIVideo.ID = originTask.TaskID
	openAIVideo.TaskID = originTask.TaskID
	openAIVideo.Status = originTask.Status.ToVideoStatus()
	openAIVideo.SetProgressStr(originTask.Progress)
	openAIVideo.SetMetadata("url", dResp.Content.VideoURL)
	openAIVideo.CreatedAt = originTask.CreatedAt
	openAIVideo.CompletedAt = originTask.UpdatedAt
	openAIVideo.Model = originTask.Properties.OriginModelName

	// 回传实际时长:duration 传 -1(模型自选)或用 frames 精确控帧时,调用方无从得知最终几秒,
	// 上游回执是唯一来源。seconds 是统一视频契约(OpenAI /v1/videos 形状)里既有的字段。
	if seconds := dResp.actualDuration(); seconds > 0 {
		openAIVideo.Seconds = strconv.Itoa(seconds)
	}

	// 终态非成功都要把错误透出去:除 failed 外还有 expired / cancelled(见 ParseTaskResult),
	// 只判 failed 的话客户端只能看到一个没有原因的 failed 状态。
	if openAIVideo.Status == dto.VideoStatusFailed {
		message := strings.TrimSpace(dResp.Error.Message)
		if message == "" {
			message = strings.TrimSpace(originTask.FailReason)
		}
		openAIVideo.Error = &dto.OpenAIVideoError{Message: message, Code: dResp.Error.Code}
	}

	return common.Marshal(openAIVideo)
}
