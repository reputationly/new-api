package doubao

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 统一契约:同一份 /v1/videos 请求(顶层 size/duration)在 Seedance 渠道上也要生效,
// 不必按渠道改写成 Ark 原生的 metadata.resolution/ratio。
func TestConvertToRequestPayloadMapsTopLevelSizeAndDuration(t *testing.T) {
	a := &TaskAdaptor{}

	t.Run("像素尺寸拆成比例+分辨率档", func(t *testing.T) {
		r, err := a.convertToRequestPayload(&relaycommon.TaskSubmitReq{
			Model: "doubao-seedance-1-0-pro", Prompt: "海边日落", Size: "1280x720", Duration: 5,
		})
		require.NoError(t, err)
		require.Equal(t, "16:9", r.Ratio)
		require.Equal(t, "720p", r.Resolution)
		require.NotNil(t, r.Duration)
		require.Equal(t, 5, int(*r.Duration))
	})

	t.Run("档位串直接当 resolution", func(t *testing.T) {
		// 文档里 size 允许 "720P" 这种档位形态。
		r, err := a.convertToRequestPayload(&relaycommon.TaskSubmitReq{
			Model: "doubao-seedance-1-0-pro", Prompt: "x", Size: "720P",
		})
		require.NoError(t, err)
		require.Equal(t, "720p", r.Resolution)
		require.Empty(t, r.Ratio)
	})

	t.Run("纯比例只设 ratio", func(t *testing.T) {
		r, err := a.convertToRequestPayload(&relaycommon.TaskSubmitReq{
			Model: "doubao-seedance-1-0-pro", Prompt: "竖屏", Size: "9:16",
		})
		require.NoError(t, err)
		require.Equal(t, "9:16", r.Ratio)
		require.Empty(t, r.Resolution)
	})

	t.Run("竖屏按短边归档", func(t *testing.T) {
		r, err := a.convertToRequestPayload(&relaycommon.TaskSubmitReq{
			Model: "doubao-seedance-1-0-pro", Prompt: "竖屏", Size: "1080x1920",
		})
		require.NoError(t, err)
		require.Equal(t, "9:16", r.Ratio)
		require.Equal(t, "1080p", r.Resolution)
	})

	t.Run("metadata 的 Ark 原生键优先", func(t *testing.T) {
		r, err := a.convertToRequestPayload(&relaycommon.TaskSubmitReq{
			Model: "doubao-seedance-1-0-pro", Prompt: "x", Size: "1280x720",
			Metadata: map[string]any{"resolution": "1080p", "ratio": "21:9"},
		})
		require.NoError(t, err)
		require.Equal(t, "21:9", r.Ratio)
		require.Equal(t, "1080p", r.Resolution)
	})

	t.Run("seconds 仍优先于顶层 duration", func(t *testing.T) {
		r, err := a.convertToRequestPayload(&relaycommon.TaskSubmitReq{
			Model: "doubao-seedance-1-0-pro", Prompt: "x", Seconds: "10", Duration: 5,
		})
		require.NoError(t, err)
		require.NotNil(t, r.Duration)
		require.Equal(t, 10, int(*r.Duration))
	})

	t.Run("无尺寸时不臆造", func(t *testing.T) {
		r, err := a.convertToRequestPayload(&relaycommon.TaskSubmitReq{
			Model: "doubao-seedance-1-0-pro", Prompt: "x",
		})
		require.NoError(t, err)
		require.Empty(t, r.Ratio)
		require.Empty(t, r.Resolution)
		require.Nil(t, r.Duration)
	})
}

// 计费矩阵按分辨率档位查价,发给上游的也是分辨率档位——两者必须是同一个数,
// 否则会出现「按 720p 收费、实际生成 1080p」这类静默错账。
// 这条断言钉住 applyTopLevelSize 与 relaycommon.VideoResolutionTier 不分叉。
func TestResolutionTierMatchesBillingResolver(t *testing.T) {
	a := &TaskAdaptor{}
	for _, size := range []string{
		"720P", "1080p", "4K", "480P",
		"1280x720", "1920x1080", "1080x1920", "854x480", "640x360", "3840x2160",
		"16:9", "9:16", "", "abc",
	} {
		r, err := a.convertToRequestPayload(&relaycommon.TaskSubmitReq{
			Model: "doubao-seedance-2-0-260128", Prompt: "x", Size: size,
		})
		require.NoErrorf(t, err, "size=%q", size)
		require.Equalf(t, relaycommon.VideoResolutionTier(size), r.Resolution,
			"size=%q：计费档位与上游请求档位不一致", size)
	}
}

// Ark 靠 content[].role 区分首帧/尾帧/多模态参考,不带 role 的多图上游无法解释。
func TestConvertToRequestPayloadAssignsContentRoles(t *testing.T) {
	a := &TaskAdaptor{}

	t.Run("单图=首帧", func(t *testing.T) {
		r, err := a.convertToRequestPayload(&relaycommon.TaskSubmitReq{
			Model: "doubao-seedance-1-5-pro", Prompt: "p", Images: []string{"https://x/a.jpg"},
		})
		require.NoError(t, err)
		require.Equal(t, "image_url", r.Content[0].Type)
		require.Equal(t, "first_frame", r.Content[0].Role)
		require.Equal(t, "text", r.Content[len(r.Content)-1].Type)
	})

	t.Run("双图=首尾帧", func(t *testing.T) {
		r, err := a.convertToRequestPayload(&relaycommon.TaskSubmitReq{
			Model: "doubao-seedance-1-5-pro", Prompt: "p",
			Images: []string{"https://x/first.jpg", "https://x/last.jpg"},
		})
		require.NoError(t, err)
		require.Equal(t, "first_frame", r.Content[0].Role)
		require.Equal(t, "last_frame", r.Content[1].Role)
	})

	t.Run("三图及以上=多模态参考图", func(t *testing.T) {
		r, err := a.convertToRequestPayload(&relaycommon.TaskSubmitReq{
			Model: "doubao-seedance-2-0-260128", Prompt: "p",
			Images: []string{"https://x/1.jpg", "https://x/2.jpg", "https://x/3.jpg"},
		})
		require.NoError(t, err)
		for i := range 3 {
			require.Equal(t, "reference_image", r.Content[i].Role)
		}
	})

	t.Run("image_role 显式覆盖推断", func(t *testing.T) {
		r, err := a.convertToRequestPayload(&relaycommon.TaskSubmitReq{
			Model: "doubao-seedance-2-0-260128", Prompt: "p", Images: []string{"https://x/ref.jpg"},
			Metadata: map[string]any{"image_role": "reference_image"},
		})
		require.NoError(t, err)
		require.Equal(t, "reference_image", r.Content[0].Role)
	})

	// 体验区「图生视频」发的是 metadata.src_ref_images(自建门面的字段名)而非顶层 images,
	// 不认就会把图整个丢掉、静默降级成文生视频。张数不设上限,用满 2.0 的 1~9 张。
	t.Run("src_ref_images=多模态参考图", func(t *testing.T) {
		r, err := a.convertToRequestPayload(&relaycommon.TaskSubmitReq{
			Model: "doubao-seedance-2-0-260128", Prompt: "p",
			Metadata: map[string]any{"src_ref_images": []any{"https://x/1.jpg", "https://x/2.jpg"}},
		})
		require.NoError(t, err)
		require.Len(t, r.Content, 3) // 2 张参考图 + 补上的文本
		for i := range 2 {
			require.Equal(t, "image_url", r.Content[i].Type)
			require.Equal(t, "reference_image", r.Content[i].Role)
		}
		require.Equal(t, "https://x/1.jpg", r.Content[0].ImageURL.URL)
	})

	// 单张参考图不能被 image_role 的"1 张=首帧"推断带偏:那条推断只服务顶层 images。
	t.Run("src_ref_images 单张仍是参考图", func(t *testing.T) {
		r, err := a.convertToRequestPayload(&relaycommon.TaskSubmitReq{
			Model: "doubao-seedance-2-0-260128", Prompt: "p",
			Metadata: map[string]any{"src_ref_images": "https://x/only.jpg"},
		})
		require.NoError(t, err)
		require.Equal(t, "reference_image", r.Content[0].Role)
	})

	t.Run("参考视频/音频走 metadata", func(t *testing.T) {
		r, err := a.convertToRequestPayload(&relaycommon.TaskSubmitReq{
			Model: "doubao-seedance-2-0-260128", Prompt: "p",
			Metadata: map[string]any{
				"reference_videos": []any{"https://x/m.mp4"},
				"reference_audio":  "https://x/bgm.mp3",
			},
		})
		require.NoError(t, err)
		require.Equal(t, "video_url", r.Content[0].Type)
		require.Equal(t, "reference_video", r.Content[0].Role)
		require.Equal(t, "https://x/m.mp4", r.Content[0].VideoURL.URL)
		require.Equal(t, "audio_url", r.Content[1].Type)
		require.Equal(t, "reference_audio", r.Content[1].Role)
		// 计费的视频输入折扣要认得这种写法。
		require.True(t, hasVideoInMetadata(map[string]any{"reference_videos": []any{"https://x/m.mp4"}}))
	})

	t.Run("metadata.content 整包覆盖仍然优先", func(t *testing.T) {
		r, err := a.convertToRequestPayload(&relaycommon.TaskSubmitReq{
			Model: "doubao-seedance-1-5-pro", Prompt: "p", Images: []string{"https://x/ignored.jpg"},
			Metadata: map[string]any{"content": []any{
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://x/custom.jpg"}, "role": "last_frame"},
			}},
		})
		require.NoError(t, err)
		require.Len(t, r.Content, 2) // 自定义图 + 补上的文本
		require.Equal(t, "https://x/custom.jpg", r.Content[0].ImageURL.URL)
		require.Equal(t, "last_frame", r.Content[0].Role)
	})

	t.Run("priority 与 safety_identifier 可透传", func(t *testing.T) {
		r, err := a.convertToRequestPayload(&relaycommon.TaskSubmitReq{
			Model: "doubao-seedance-2-0-260128", Prompt: "p",
			Metadata: map[string]any{"priority": 5, "safety_identifier": "u-hash"},
		})
		require.NoError(t, err)
		require.NotNil(t, r.Priority)
		require.Equal(t, 5, int(*r.Priority))
		require.Equal(t, "u-hash", r.SafetyIdentifier)
	})
}

// expired / cancelled 是终态:漏掉会让任务永远轮询下去,既不失败也不退款。
func TestParseTaskResultTerminalStatuses(t *testing.T) {
	a := &TaskAdaptor{}

	for _, status := range []string{"expired", "cancelled", "canceled"} {
		t.Run(status, func(t *testing.T) {
			info, err := a.ParseTaskResult([]byte(
				`{"id":"vt_1","status":"` + status + `","error":{"code":"video_task_expired","message":"任务超时"}}`))
			require.NoError(t, err)
			require.EqualValues(t, model.TaskStatusFailure, info.Status)
			require.Equal(t, "100%", info.Progress)
			require.Contains(t, info.Reason, "video_task_expired")
		})
	}

	t.Run("failed 带 error.code", func(t *testing.T) {
		info, err := a.ParseTaskResult([]byte(
			`{"id":"vt_2","status":"failed","error":{"code":"video_task_failed","message":"内容审核未通过"}}`))
		require.NoError(t, err)
		require.EqualValues(t, model.TaskStatusFailure, info.Status)
		require.Equal(t, "内容审核未通过 (video_task_failed)", info.Reason)
	})

	t.Run("succeeded 取 content.video_url", func(t *testing.T) {
		info, err := a.ParseTaskResult([]byte(
			`{"id":"vt_3","status":"succeeded","content":{"video_url":"https://x/o.mp4"},"usage":{"completion_tokens":109431}}`))
		require.NoError(t, err)
		require.EqualValues(t, model.TaskStatusSuccess, info.Status)
		require.Equal(t, "https://x/o.mp4", info.Url)
		require.Equal(t, 109431, info.CompletionTokens)
	})

	t.Run("running 继续轮询", func(t *testing.T) {
		info, err := a.ParseTaskResult([]byte(`{"id":"vt_4","status":"running"}`))
		require.NoError(t, err)
		require.EqualValues(t, model.TaskStatusInProgress, info.Status)
	})
}

// OpenAI /v1/videos 风格用 input_reference 传首帧;公共校验层不归一化的话，
// 图生视频会被静默降级成文生视频。
func TestValidateBasicTaskRequestNormalizesInputReference(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := `{"model":"doubao-seedance-1-5-pro","prompt":"p","input_reference":"https://x/first.jpg","seconds":"5"}`
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	// Action 挂在内嵌的 *TaskRelayInfo 上,真实链路由 GenRelayInfo 初始化。
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	require.Nil(t, relaycommon.ValidateBasicTaskRequest(c, info, constant.TaskActionGenerate))

	req, err := relaycommon.GetTaskRequest(c)
	require.NoError(t, err)
	require.Equal(t, []string{"https://x/first.jpg"}, req.Images)

	r, err := (&TaskAdaptor{}).convertToRequestPayload(&req)
	require.NoError(t, err)
	require.Equal(t, "https://x/first.jpg", r.Content[0].ImageURL.URL)
	require.Equal(t, "first_frame", r.Content[0].Role)
}

// duration 传 -1(模型自选)或用 frames 控帧时,实际时长只能从上游回执拿。
// 顶层 duration(火山方舟)与 output.duration(四海)两种落点都要认。
func TestConvertToOpenAIVideoEchoesActualDuration(t *testing.T) {
	a := &TaskAdaptor{}
	newTask := func(data string) *model.Task {
		return &model.Task{
			TaskID: "vt_1", Status: model.TaskStatusSuccess, Progress: "100%",
			Data: []byte(data),
		}
	}

	for name, data := range map[string]string{
		"顶层 duration":     `{"id":"vt_1","status":"succeeded","duration":11,"content":{"video_url":"https://x/o.mp4"}}`,
		"output.duration": `{"id":"vt_1","status":"succeeded","output":{"duration":11},"content":{"video_url":"https://x/o.mp4"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			raw, err := a.ConvertToOpenAIVideo(newTask(data))
			require.NoError(t, err)
			var video dto.OpenAIVideo
			require.NoError(t, common.Unmarshal(raw, &video))
			require.Equal(t, "11", video.Seconds)
		})
	}

	t.Run("上游没回时长则不输出 seconds", func(t *testing.T) {
		raw, err := a.ConvertToOpenAIVideo(newTask(`{"id":"vt_1","status":"succeeded"}`))
		require.NoError(t, err)
		require.NotContains(t, string(raw), `"seconds"`)
	})
}
