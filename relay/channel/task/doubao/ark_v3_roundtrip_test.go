package doubao

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/arkv3"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 火山方舟兼容层（/api/v3/contents/generations/tasks）的往返验证。
//
// 这个测试要证明的是**整条链是闭合的**：调用方发来的 Ark 请求经
// arkv3.ConvertCreateRequest 拆成统一任务契约，再经本适配器拼回 Ark 请求，最终
// 发给上游的那一份必须与调用方发来的那一份语义等价。
//
// 为什么非得端到端跑一遍、而不是在两侧各断言一次：这两段代码靠的是一组**约定的
// metadata 键名**（task_type / src_ref_images / reference_videos …）对接，而键名对不上
// 是不会报错的 —— 值被静默丢弃，请求照常 200，只是参数没生效。任何一侧单独测都测不
// 出这件事，只有让下游真的消费上游的产出才行。所以这里的 fixture 是**转换器自己
// 产出的**，不是手写的。
func arkRoundTrip(t *testing.T, raw string) *requestPayload {
	t.Helper()
	payload, taskErr := arkRoundTripE(t, raw)
	require.Nil(t, taskErr, "adaptor rejected the converted request: %v", taskErr)
	return payload
}

// arkRoundTripE 与 arkRoundTrip 相同，但把入口校验的拒绝作为值返回，供「该拒的要拒」
// 那类用例断言。
func arkRoundTripE(t *testing.T, raw string) (*requestPayload, *dto.TaskError) {
	t.Helper()

	body, _, apiErr := arkv3.ConvertCreateRequest([]byte(raw))
	require.Nil(t, apiErr, "convert failed: %v", apiErr)

	jsonData, err := common.Marshal(body)
	require.NoError(t, err)

	// ⚠️ 必须走**适配器的真实入口**，不能手工把 jsonData Unmarshal 成 TaskSubmitReq。
	//
	// 生产链路是：中间件改写 body + 路径 → adaptor.ValidateRequestAndSetAction →
	// relaycommon.ValidateBasicTaskRequest（校验并把请求存进 context）→ adaptor 再从
	// context 取。跳过中间那道校验，测的就只是转换器与适配器两端对不对得上，而**校验
	// 本身会不会把请求挡在门外测不到** —— 「文本可选 + 图片」这个官方组合此前就是这样
	// 漏掉的：两端都正确，请求却根本到不了适配器。
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/video/generations", bytes.NewReader(jsonData))
	c.Request.Header.Set("Content-Type", "application/json")

	a := &TaskAdaptor{}
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	if taskErr := a.ValidateRequestAndSetAction(c, info); taskErr != nil {
		return nil, taskErr
	}

	req, err := relaycommon.GetTaskRequest(c)
	require.NoError(t, err)

	payload, err := a.convertToRequestPayload(&req)
	require.NoError(t, err)
	return payload, nil
}

// contentByRole 把 content[] 按 role 归类，便于断言（顺序由适配器决定，不该硬编码）。
func contentByRole(items []ContentItem) map[string][]ContentItem {
	out := map[string][]ContentItem{}
	for _, item := range items {
		out[item.Role] = append(out[item.Role], item)
	}
	return out
}

func TestArkV3RoundTripTextToVideo(t *testing.T) {
	p := arkRoundTrip(t, `{
		"model": "doubao-seedance-2-0-260128",
		"content": [{"type": "text", "text": "一艘木船穿过晨雾中的湖面，写实电影镜头"}],
		"resolution": "720p",
		"ratio": "16:9",
		"duration": 5,
		"generate_audio": true
	}`)

	require.Equal(t, "doubao-seedance-2-0-260128", p.Model)
	require.Equal(t, "720p", p.Resolution)
	require.Equal(t, "16:9", p.Ratio)
	require.NotNil(t, p.Duration)
	require.Equal(t, 5, int(*p.Duration))
	require.NotNil(t, p.GenerateAudio)
	require.True(t, bool(*p.GenerateAudio))

	require.Len(t, p.Content, 1)
	require.Equal(t, "text", p.Content[0].Type)
	require.Equal(t, "一艘木船穿过晨雾中的湖面，写实电影镜头", p.Content[0].Text)
}

// 帧约束的 role 必须原样回到上游。这是最容易静默出错的一项：丢了 role 的多图上游
// 无法解释，而丢了 task_type 的单张尾帧会被张数推断当成首帧 —— 两种都不报错。
func TestArkV3RoundTripPreservesFrameRoles(t *testing.T) {
	t.Run("首尾帧", func(t *testing.T) {
		p := arkRoundTrip(t, `{
			"model": "m",
			"content": [
				{"type": "text", "text": "保持主体一致"},
				{"type": "image_url", "image_url": {"url": "https://x/first.jpg"}, "role": "first_frame"},
				{"type": "image_url", "image_url": {"url": "https://x/last.jpg"}, "role": "last_frame"}
			]
		}`)
		byRole := contentByRole(p.Content)
		require.Len(t, byRole["first_frame"], 1)
		require.Equal(t, "https://x/first.jpg", byRole["first_frame"][0].ImageURL.URL)
		require.Len(t, byRole["last_frame"], 1)
		require.Equal(t, "https://x/last.jpg", byRole["last_frame"][0].ImageURL.URL)
	})

	t.Run("只给尾帧不能变成首帧", func(t *testing.T) {
		p := arkRoundTrip(t, `{
			"model": "m",
			"content": [
				{"type": "text", "text": "从这一帧倒推"},
				{"type": "image_url", "image_url": {"url": "https://x/last.jpg"}, "role": "last_frame"}
			]
		}`)
		byRole := contentByRole(p.Content)
		require.Empty(t, byRole["first_frame"], "单张尾帧被误判成首帧：视频会从错误的一端生成，且全链路无报错")
		require.Len(t, byRole["last_frame"], 1)
		require.Equal(t, "https://x/last.jpg", byRole["last_frame"][0].ImageURL.URL)
	})
}

// 多模态参考：三类媒体都要带着 reference_* 的 role 回到上游。
func TestArkV3RoundTripPreservesReferences(t *testing.T) {
	p := arkRoundTrip(t, `{
		"model": "doubao-seedance-2-0-260128",
		"content": [
			{"type": "text", "text": "保持人物动作，替换为傍晚海边场景"},
			{"type": "image_url", "image_url": {"url": "https://x/a.jpg"}, "role": "reference_image"},
			{"type": "image_url", "image_url": {"url": "https://x/b.jpg"}, "role": "reference_image"},
			{"type": "video_url", "video_url": {"url": "https://x/ref.mp4"}, "role": "reference_video"},
			{"type": "audio_url", "audio_url": {"url": "https://x/ref.mp3"}, "role": "reference_audio"}
		]
	}`)

	byRole := contentByRole(p.Content)
	require.Len(t, byRole["reference_image"], 2)
	require.Equal(t, "https://x/a.jpg", byRole["reference_image"][0].ImageURL.URL)
	require.Equal(t, "https://x/b.jpg", byRole["reference_image"][1].ImageURL.URL)
	require.Len(t, byRole["reference_video"], 1)
	require.Equal(t, "https://x/ref.mp4", byRole["reference_video"][0].VideoURL.URL)
	require.Len(t, byRole["reference_audio"], 1)
	require.Equal(t, "https://x/ref.mp3", byRole["reference_audio"][0].AudioURL.URL)
}

// Ark 的标量参数全部原名透传，一个都不能在中途掉队。
func TestArkV3RoundTripPreservesScalars(t *testing.T) {
	p := arkRoundTrip(t, `{
		"model": "doubao-seedance-2-0-260128",
		"content": [{"type": "text", "text": "x"}],
		"resolution": "1080p",
		"ratio": "9:16",
		"duration": 10,
		"seed": 42,
		"camera_fixed": true,
		"watermark": true,
		"return_last_frame": true,
		"generate_audio": false,
		"draft": false,
		"service_tier": "default",
		"execution_expires_after": 7200,
		"priority": 7,
		"safety_identifier": "sha256-of-end-user",
		"tools": [{"type": "web_search"}]
	}`)

	require.Equal(t, "1080p", p.Resolution)
	require.Equal(t, "9:16", p.Ratio)
	require.Equal(t, 10, int(*p.Duration))
	require.Equal(t, 42, int(*p.Seed))
	require.True(t, bool(*p.CameraFixed))
	require.True(t, bool(*p.Watermark))
	require.True(t, bool(*p.ReturnLastFrame))
	// 显式 false 必须挺过整条链：丢了它调用方会拿到带声音的片子。
	require.NotNil(t, p.GenerateAudio)
	require.False(t, bool(*p.GenerateAudio))
	require.NotNil(t, p.Draft)
	require.False(t, bool(*p.Draft))
	require.Equal(t, "default", p.ServiceTier)
	require.Equal(t, 7200, int(*p.ExecutionExpiresAfter))
	require.Equal(t, 7, int(*p.Priority))
	require.Equal(t, "sha256-of-end-user", p.SafetyIdentifier)
	require.Len(t, p.Tools, 1)
	require.Equal(t, "web_search", p.Tools[0].Type)
}

// 「文本（可选）+ 图片 / 视频 / 音频」是方舟官方明确支持的输入组合。
//
// 两件事必须同时成立，缺一个这个组合就是不可用的：
//  1. 请求要**穿过入口校验** —— ValidateBasicTaskRequest 默认无差别要求非空 prompt，
//     适配器不声明 PromptOptionalWithMedia 的话，这里会拿到 400 prompt is required；
//  2. 发给上游时不能带一个空的 text 条目 —— 上游按空提示词拒绝。
func TestArkV3RoundTripMediaOnlyRequests(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		wantRole string
	}{
		{"只给首帧", `{"model":"m","content":[
			{"type":"image_url","image_url":{"url":"https://x/a.jpg"},"role":"first_frame"}]}`, "first_frame"},
		{"只给尾帧", `{"model":"m","content":[
			{"type":"image_url","image_url":{"url":"https://x/l.jpg"},"role":"last_frame"}]}`, "last_frame"},
		{"只给参考视频", `{"model":"m","content":[
			{"type":"video_url","video_url":{"url":"https://x/r.mp4"},"role":"reference_video"}]}`, "reference_video"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := arkRoundTrip(t, tc.raw)
			for _, item := range p.Content {
				require.NotEqual(t, "text", item.Type, "没有提示词时不该发出 text 条目")
			}
			require.Len(t, p.Content, 1)
			require.Equal(t, tc.wantRole, p.Content[0].Role)
		})
	}
}

// PromptOptionalWithMedia 的边界：豁免的条件是**带了媒体**，没有任何输入决定输出时
// 空 prompt 仍然必须被挡在网关里。把闸开得太大和关得太死是同一类错误，只是方向相反。
//
// 直接打适配器入口而不经 arkv3：转换层对「纯文本且文本为空」另有一道更靠前的闸
// （content[].text must not be empty），从那条路进来测不到这里要锁的东西。而
// /v1/video/generations 原生端点是可以直接发这种 body 的。
func TestDoubaoPromptOptionalOnlyAppliesWithMedia(t *testing.T) {
	validate := func(t *testing.T, body string) *dto.TaskError {
		t.Helper()
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/video/generations", bytes.NewReader([]byte(body)))
		c.Request.Header.Set("Content-Type", "application/json")
		return (&TaskAdaptor{}).ValidateRequestAndSetAction(c,
			&relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}})
	}

	t.Run("无媒体的空 prompt 仍被拒", func(t *testing.T) {
		taskErr := validate(t, `{"model":"doubao-seedance-2-0-260128","prompt":"  ","metadata":{"task_type":"t2v"}}`)
		require.NotNil(t, taskErr, "纯文生视频的空 prompt 必须被拒")
		require.Contains(t, taskErr.Message, "prompt is required")
	})

	// 媒体的四种落点都要认。漏掉任何一种，对应玩法的「不写提示词」就会被 400 挡下，
	// 而 image / input_reference 归一化到 Images 发生在这道校验**之后** —— 只看
	// req.Images 的话这两条恰好是漏的。
	for _, tc := range []struct{ name, body string }{
		{"顶层 images", `{"model":"m","prompt":"","images":["https://x/a.jpg"],"metadata":{"task_type":"i2v"}}`},
		{"单图 image", `{"model":"m","prompt":"","image":"https://x/a.jpg","metadata":{"task_type":"i2v"}}`},
		{"OpenAI 风格 input_reference", `{"model":"m","prompt":"","input_reference":"https://x/a.jpg","metadata":{"task_type":"i2v"}}`},
		{"metadata 参考媒体", `{"model":"m","prompt":"","metadata":{"task_type":"r2va","reference_videos":["https://x/r.mp4"]}}`},
	} {
		t.Run(tc.name+"：空 prompt 放行", func(t *testing.T) {
			require.Nil(t, validate(t, tc.body))
		})
	}
}

// 计费维度必须从转换产物里读得出来：分辨率走 metadata.resolution，秒数走**顶层**
// duration（relaycommon.videoPerCallSeconds 只认顶层），视频输入走 reference_videos。
// 任一维读不出都只体现在账单上，不报错。
func TestArkV3RoundTripKeepsBillingDimensionsReadable(t *testing.T) {
	body, _, apiErr := arkv3.ConvertCreateRequest([]byte(`{
		"model": "doubao-seedance-2-0-260128",
		"content": [
			{"type": "text", "text": "x"},
			{"type": "video_url", "video_url": {"url": "https://x/ref.mp4"}, "role": "reference_video"}
		],
		"resolution": "720p",
		"duration": 8
	}`))
	require.Nil(t, apiErr)

	jsonData, err := common.Marshal(body)
	require.NoError(t, err)
	var req relaycommon.TaskSubmitReq
	require.NoError(t, common.Unmarshal(jsonData, &req))

	resolution, seconds, hasVideoInput := relaycommon.ResolveVideoDims(&req)
	require.Equal(t, "720p", resolution)
	require.Equal(t, 8, seconds)
	require.True(t, hasVideoInput, "含视频输入的请求按不含视频的档计费就是多收")

	// 适配器的计费钩子读的是同一份 metadata，两处必须同时成立。
	ratio, ok := GetVideoInputRatio("doubao-seedance-2-0-260128")
	require.True(t, ok)
	require.True(t, hasVideoInMetadata(req.Metadata))
	require.Less(t, ratio, 1.0)
}
