package relay

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service/mediastore"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// ---------- 测试替身 ----------

type fakeUploader struct {
	mu         sync.Mutex
	persisted  []string // 落盘过的 key，顺序无关但计数是断言重点
	persistErr error
	signErr    error
}

func (f *fakeUploader) Persist(_ context.Context, key string, _ mediastore.PersistSource, _ map[string]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.persistErr != nil {
		return f.persistErr
	}
	f.persisted = append(f.persisted, key)
	return nil
}

func (f *fakeUploader) Sign(_ context.Context, key string) (string, error) {
	if f.signErr != nil {
		return "", f.signErr
	}
	return "https://obs.example.com/" + key + "?sig=fake", nil
}

func (f *fakeUploader) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.persisted)
}

// installFakeUploader 替换落盘接缝并打开两个开关，返回还原函数。
func installFakeUploader(t *testing.T) *fakeUploader {
	t.Helper()
	fake := &fakeUploader{}
	origUploader := defaultUploader
	defaultUploader = fake

	s := system_setting.GetMediaStorageSettings()
	origEnabled, origIngest, origMax := s.Enabled, s.IngestClientUpload, s.MaxObjectSizeMB
	s.Enabled, s.IngestClientUpload = true, true
	if s.MaxObjectSizeMB <= 0 {
		s.MaxObjectSizeMB = 200
	}

	t.Cleanup(func() {
		defaultUploader = origUploader
		s.Enabled, s.IngestClientUpload, s.MaxObjectSizeMB = origEnabled, origIngest, origMax
	})
	return fake
}

// ---------- 请求构造 ----------

var testPNG = []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D}

func testDataURL(sizeBytes int) string {
	data := make([]byte, sizeBytes)
	copy(data, testPNG)
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
}

// newTaskCtx 造一个带 JSON body 的 gin.Context，并按真实链路先解析出 task_request。
func newTaskCtx(t *testing.T, channelType int, body map[string]any) (*gin.Context, *relaycommon.RelayInfo) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	raw, err := json.Marshal(body)
	require.NoError(t, err)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewReader(raw))
	c.Request.Header.Set("Content-Type", "application/json")

	info := &relaycommon.RelayInfo{UserId: 10086}
	info.ChannelMeta = &relaycommon.ChannelMeta{ChannelType: channelType}
	require.NoError(t, reparseTaskRequest(c))
	return c, info
}

// reparseTaskRequest 复刻 ValidateBasicTaskRequest 中与本测试相关的部分：
// 从缓存 body 重建 task_request（含 image→images 归一化）。
func reparseTaskRequest(c *gin.Context) error {
	var req relaycommon.TaskSubmitReq
	if err := common.UnmarshalBodyReusable(c, &req); err != nil {
		return err
	}
	if len(req.Images) == 0 && req.Image != "" {
		req.Images = []string{req.Image}
		req.Image = ""
	}
	if len(req.Images) == 0 && req.InputReference != "" {
		req.Images = []string{req.InputReference}
		req.InputReference = ""
	}
	c.Set("task_request", req)
	return nil
}

func currentBody(t *testing.T, c *gin.Context) []byte {
	t.Helper()
	storage, err := common.GetBodyStorage(c)
	require.NoError(t, err)
	raw, err := storage.Bytes()
	require.NoError(t, err)
	return raw
}

// ---------- 用例 ----------

//  1. 白名单渠道：顶层 images、metadata 数组、doubao 深层嵌套逃生口全部换成签名 URL，
//     且 task_request 与缓存 body 双双反映改动。
func TestOffload_RewritesAllMediaFields(t *testing.T) {
	fake := installFakeUploader(t)
	img := testDataURL(2048)

	c, info := newTaskCtx(t, constant.ChannelTypeDoubaoVideo, map[string]any{
		"model":  "doubao-seedance-2-0-260128",
		"prompt": "海边的纸飞机",
		"images": []any{img},
		"metadata": map[string]any{
			"src_ref_images": []any{testDataURL(3072)},
			// doubao 的整包覆盖逃生口：媒体藏在 content[].video_url.url 里
			"content": []any{
				map[string]any{"type": "text", "text": "保持主体一致"},
				map[string]any{
					"type":      "video_url",
					"video_url": map[string]any{"url": testDataURL(4096)},
				},
			},
		},
	})

	require.Nil(t, rewriteTaskMedia(c, info))

	req, err := relaycommon.GetTaskRequest(c)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(req.Images[0], "https://obs.example.com/"), "顶层 images 未改写")

	md := req.Metadata
	refs := md["src_ref_images"].([]any)
	require.True(t, strings.HasPrefix(refs[0].(string), "https://obs.example.com/"), "metadata 数组未改写")

	content := md["content"].([]any)
	nested := content[1].(map[string]any)["video_url"].(map[string]any)["url"].(string)
	require.True(t, strings.HasPrefix(nested, "https://obs.example.com/"), "深层嵌套未改写")

	body := string(currentBody(t, c))
	require.NotContains(t, body, "data:image/png;base64,", "缓存 body 仍残留 data-url")
	require.Contains(t, body, "https://obs.example.com/", "缓存 body 未反映改写")
	require.Equal(t, 3, fake.count(), "三个不同的媒体各落盘一次")
}

//  2. 非白名单渠道完全不动，且 body 逐字节等于客户端原始输入。
//     这条守卫的是最难发现的失败模式：gemini 的 ParseImageInput 对 http URL 返回 nil，
//     参考图会被静默丢弃、图生视频降级成文生视频，没有任何报错。
func TestOffload_ExcludedChannelsUntouched(t *testing.T) {
	for _, ch := range []int{
		constant.ChannelTypeGPUStackPlus,
		constant.ChannelTypeVertexAi,
		constant.ChannelTypeGemini,
		constant.ChannelTypeSora,
	} {
		t.Run(fmt.Sprintf("channel_%d", ch), func(t *testing.T) {
			fake := installFakeUploader(t)
			img := testDataURL(2048)
			c, info := newTaskCtx(t, ch, map[string]any{
				"model": "m", "prompt": "p", "images": []any{img},
			})
			original := append([]byte(nil), currentBody(t, c)...)

			require.Nil(t, rewriteTaskMedia(c, info))

			req, err := relaycommon.GetTaskRequest(c)
			require.NoError(t, err)
			require.Equal(t, img, req.Images[0], "被排除的渠道不该被改写")
			require.Equal(t, 0, fake.count(), "被排除的渠道不该落盘")

			RestoreOriginalTaskBody(c)
			require.Equal(t, original, currentBody(t, c), "body 必须与客户端原始字节逐字节相等")
		})
	}
}

// 3. 落盘失败 → 该项保留原 data-url，不返回错误，其余项照常卸载。
func TestOffload_PersistFailureFallsBackPerItem(t *testing.T) {
	fake := installFakeUploader(t)
	fake.persistErr = fmt.Errorf("boom")

	img := testDataURL(2048)
	c, info := newTaskCtx(t, constant.ChannelTypeDoubaoVideo, map[string]any{
		"model": "m", "prompt": "p", "images": []any{img},
	})

	require.Nil(t, rewriteTaskMedia(c, info), "卸载失败不该让请求失败")

	req, err := relaycommon.GetTaskRequest(c)
	require.NoError(t, err)
	require.Equal(t, img, req.Images[0], "失败项应回退原值透传")
}

func TestOffload_SignFailureFallsBack(t *testing.T) {
	fake := installFakeUploader(t)
	fake.signErr = fmt.Errorf("sign boom")

	img := testDataURL(2048)
	c, info := newTaskCtx(t, constant.ChannelTypeDoubaoVideo, map[string]any{
		"model": "m", "prompt": "p", "images": []any{img},
	})
	require.Nil(t, rewriteTaskMedia(c, info))

	req, err := relaycommon.GetTaskRequest(c)
	require.NoError(t, err)
	require.Equal(t, img, req.Images[0])
}

// 4. 两个开关任一关闭 → 完全 no-op，零落盘。
func TestOffload_DisabledIsNoOp(t *testing.T) {
	cases := map[string]func(*system_setting.MediaStorageSettings){
		"媒体存储总开关关闭": func(s *system_setting.MediaStorageSettings) { s.Enabled = false },
		"入站卸载开关关闭":  func(s *system_setting.MediaStorageSettings) { s.IngestClientUpload = false },
	}
	for name, disable := range cases {
		t.Run(name, func(t *testing.T) {
			fake := installFakeUploader(t)
			disable(system_setting.GetMediaStorageSettings())

			img := testDataURL(2048)
			c, info := newTaskCtx(t, constant.ChannelTypeDoubaoVideo, map[string]any{
				"model": "m", "prompt": "p", "images": []any{img},
			})
			require.Nil(t, rewriteTaskMedia(c, info))

			req, err := relaycommon.GetTaskRequest(c)
			require.NoError(t, err)
			require.Equal(t, img, req.Images[0])
			require.Equal(t, 0, fake.count())
		})
	}
}

// 5. 同一张图出现在多个字段 → 只落盘一次（内容摘要去重）。
func TestOffload_DeduplicatesIdenticalMedia(t *testing.T) {
	fake := installFakeUploader(t)
	img := testDataURL(2048)

	c, info := newTaskCtx(t, constant.ChannelTypeDoubaoVideo, map[string]any{
		"model": "m", "prompt": "p",
		"images":   []any{img},
		"metadata": map[string]any{"src_ref_images": []any{img}},
	})
	require.Nil(t, rewriteTaskMedia(c, info))
	require.Equal(t, 1, fake.count(), "同一内容出现在两个字段，只该落盘一次")
}

//  6. 跨尝试：复位 → 重新改写，memo 命中，仍只落盘一次。
//     对应 RelayTask 的重试循环（同一个 *gin.Context 跑多轮）。
func TestOffload_MemoSurvivesRetry(t *testing.T) {
	fake := installFakeUploader(t)
	img := testDataURL(2048)

	c, info := newTaskCtx(t, constant.ChannelTypeDoubaoVideo, map[string]any{
		"model": "m", "prompt": "p", "images": []any{img},
	})
	require.Nil(t, rewriteTaskMedia(c, info))
	require.Equal(t, 1, fake.count())

	// 第二轮：复位 body → 重建 task_request → 再次改写（复刻 RelayTaskSubmit 的顺序）
	RestoreOriginalTaskBody(c)
	require.NoError(t, reparseTaskRequest(c))
	require.Nil(t, rewriteTaskMedia(c, info))

	require.Equal(t, 1, fake.count(), "重试不该重复上传")
	req, err := relaycommon.GetTaskRequest(c)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(req.Images[0], "https://obs.example.com/"), "第二轮仍应产出 URL")
}

// 6b. 重试换到被排除的渠道 → 拿到的必须是原始 base64，而不是上一轮的 URL。
func TestOffload_RetryToExcludedChannelGetsOriginalBytes(t *testing.T) {
	installFakeUploader(t)
	img := testDataURL(2048)

	c, info := newTaskCtx(t, constant.ChannelTypeDoubaoVideo, map[string]any{
		"model": "m", "prompt": "p", "images": []any{img},
	})
	require.Nil(t, rewriteTaskMedia(c, info))

	// 换渠道重试
	RestoreOriginalTaskBody(c)
	require.NoError(t, reparseTaskRequest(c))
	info.ChannelType = constant.ChannelTypeGemini
	require.Nil(t, rewriteTaskMedia(c, info))

	req, err := relaycommon.GetTaskRequest(c)
	require.NoError(t, err)
	require.Equal(t, img, req.Images[0], "被排除的渠道必须拿到原始 base64，否则参考图会被静默丢弃")
}

// 7. 超过 MaxObjectSizeMB → 透传，零落盘（且在解码前就被拒）。
func TestOffload_OversizeFallsBack(t *testing.T) {
	fake := installFakeUploader(t)
	system_setting.GetMediaStorageSettings().MaxObjectSizeMB = 1

	img := testDataURL(3 * 1024 * 1024) // 3 MB > 1 MB 上限
	c, info := newTaskCtx(t, constant.ChannelTypeDoubaoVideo, map[string]any{
		"model": "m", "prompt": "p", "images": []any{img},
	})
	require.Nil(t, rewriteTaskMedia(c, info))

	req, err := relaycommon.GetTaskRequest(c)
	require.NoError(t, err)
	require.Equal(t, img, req.Images[0])
	require.Equal(t, 0, fake.count())
}

// 8. 需求本身：~1.1 MB 的 body 改写后必须小到能过上游 1 MiB 的网关限制。
func TestOffload_ShrinksBodyBelowGatewayLimit(t *testing.T) {
	installFakeUploader(t)

	img := testDataURL(790 * 1024) // 复现故障量级：790 KB 原图
	c, info := newTaskCtx(t, constant.ChannelTypeDoubaoVideo, map[string]any{
		"model": "doubao-seedance-2-0-260128", "prompt": "海边的纸飞机",
		"images": []any{img},
	})
	require.Greater(t, len(currentBody(t, c)), 1048576, "前置条件：这份 body 本来就顶穿 1 MiB")

	require.Nil(t, rewriteTaskMedia(c, info))
	require.Less(t, len(currentBody(t, c)), 4096, "改写后 body 应只剩几百字节的 URL")
}

// 9. json.Number 保真：大整数经一轮改写后不能被 float64 round-trip 破坏。
func TestOffload_PreservesLargeIntegers(t *testing.T) {
	installFakeUploader(t)

	c, info := newTaskCtx(t, constant.ChannelTypeDoubaoVideo, map[string]any{
		"model": "m", "prompt": "p",
		"images":   []any{testDataURL(2048)},
		"metadata": map[string]any{"seed": json.Number("4294967295")},
	})
	require.Nil(t, rewriteTaskMedia(c, info))
	require.Contains(t, string(currentBody(t, c)), "4294967295", "大整数被精度损坏")
}

// 10. 无媒体的纯文生视频请求：body 一个字节都不该动。
func TestOffload_TextOnlyRequestUntouched(t *testing.T) {
	fake := installFakeUploader(t)

	c, info := newTaskCtx(t, constant.ChannelTypeDoubaoVideo, map[string]any{
		"model": "m", "prompt": "纯文生视频", "metadata": map[string]any{"ratio": "16:9"},
	})
	original := append([]byte(nil), currentBody(t, c)...)

	require.Nil(t, rewriteTaskMedia(c, info))
	require.Equal(t, original, currentBody(t, c))
	require.Equal(t, 0, fake.count())
}

// 11. 语音/音乐体验区的字段走同一条路（它们与视频区共用 /pg/videos 端点）。
func TestOffload_CoversAudioAndMusicFields(t *testing.T) {
	fake := installFakeUploader(t)

	c, info := newTaskCtx(t, constant.ChannelTypeDoubaoVideo, map[string]any{
		"model": "m", "prompt": "p",
		"metadata": map[string]any{
			"voice":           testDataURL(1024), // 语音区
			"emotion_audio":   testDataURL(1280),
			"reference_audio": testDataURL(1536), // 音乐区
			"src_audio":       testDataURL(1792),
		},
	})
	require.Nil(t, rewriteTaskMedia(c, info))

	req, err := relaycommon.GetTaskRequest(c)
	require.NoError(t, err)
	for _, k := range []string{"voice", "emotion_audio", "reference_audio", "src_audio"} {
		require.True(t, strings.HasPrefix(req.Metadata[k].(string), "https://obs.example.com/"), k)
	}
	require.Equal(t, 4, fake.count())
}
