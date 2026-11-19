package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 渠道编辑页的保存前预警：端点类型由「渠道类型 + 模型名」共同决定，前端不能自己复刻。
// 这些分支正是"模型广场有、文本体验区没有"的成因，逐个锁住。
func TestGetChannelModelEndpointTypes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupModelListControllerTestDB(t)

	// 元数据里自定义 endpoints 会整体替换默认端点（与 pricing 同语义），预警必须反映它
	require.NoError(t, db.Create(&model.Model{
		ModelName: "zz-custom-ep", Status: 1,
		Endpoints: `{"openai":{"path":"/v1/chat/completions","method":"POST"}}`,
	}).Error)

	call := func(channelType int, models ...string) (int, map[string]channelModelEndpointType) {
		body, err := common.Marshal(channelEndpointTypesRequest{Type: channelType, Models: models})
		require.NoError(t, err)
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodPost, "/api/channel/endpoint_types", bytes.NewReader(body))
		ctx.Request.Header.Set("Content-Type", "application/json")
		GetChannelModelEndpointTypes(ctx)

		var payload struct {
			Success bool `json:"success"`
			Data    struct {
				Models map[string]channelModelEndpointType `json:"models"`
			} `json:"data"`
		}
		_ = common.Unmarshal(recorder.Body.Bytes(), &payload)
		return recorder.Code, payload.Data.Models
	}

	// 普通 OpenAI 渠道的文本模型：chat 端点，不会被文本体验区排除
	code, models := call(1, "qwen3.8-max")
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, []string{"openai"}, models["qwen3.8-max"].EndpointTypes)
	require.False(t, models["qwen3.8-max"].PlaygroundBlocked)

	// GPUStackPlus(59)：名字不像图片的一律当视频 —— 文本模型挂上去就会"消失"
	_, models = call(59, "wan2.2-t2v", "qwen-image", "qwen3.8-max")
	require.Equal(t, []string{string(constant.EndpointTypeOpenAIVideo)}, models["wan2.2-t2v"].EndpointTypes)
	require.True(t, models["wan2.2-t2v"].PlaygroundBlocked)
	require.Equal(t, []string{string(constant.EndpointTypeImageGeneration)}, models["qwen-image"].EndpointTypes)
	require.True(t, models["qwen-image"].PlaygroundBlocked)
	require.True(t, models["qwen3.8-max"].PlaygroundBlocked)

	// Sora(55) → openai-video；Jina(38) → jina-rerank
	_, models = call(55, "sora-2")
	require.True(t, models["sora-2"].PlaygroundBlocked)
	_, models = call(38, "jina-reranker-v3")
	require.Equal(t, []string{string(constant.EndpointTypeJinaRerank)}, models["jina-reranker-v3"].EndpointTypes)
	require.True(t, models["jina-reranker-v3"].PlaygroundBlocked)

	// 名字启发式：即便是 OpenAI 渠道，gpt-image-1 也会被标成图像端点。
	// tts 系会带 audio-speech 端点，但**文本体验区并不过滤它**（前端清单里没有这个键），
	// 所以这里不能报"不可见" —— 报了就是诊断与页面结论相反。
	_, models = call(1, "gpt-image-1", "qwen3-tts")
	require.True(t, models["gpt-image-1"].PlaygroundBlocked)
	require.Equal(t, []string{string(constant.EndpointTypeAudioSpeech), string(constant.EndpointTypeOpenAI)},
		models["qwen3-tts"].EndpointTypes)
	require.False(t, models["qwen3-tts"].PlaygroundBlocked)

	// 元数据自定义 endpoints 覆盖渠道类型的判断
	_, models = call(59, "zz-custom-ep")
	require.Equal(t, []string{"openai"}, models["zz-custom-ep"].EndpointTypes)
	require.False(t, models["zz-custom-ep"].PlaygroundBlocked)

	// 请求体非法 / 空列表
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/channel/endpoint_types", strings.NewReader("not-json"))
	GetChannelModelEndpointTypes(ctx)
	require.Equal(t, http.StatusBadRequest, recorder.Code)

	code, models = call(1)
	require.Equal(t, http.StatusOK, code)
	require.Empty(t, models)
}

// 这份键集合必须与前端 PLAYGROUND_UNSUPPORTED_ENDPOINTS 逐字一致：前端按它过滤模型
// 下拉，后端按它解释"为什么看不到"。这里**真读前端文件**再比，而不是拿常量的拷贝和
// 自己比 —— 后者只能证明常量没变，证明不了两边一致（上一版就是这么漏掉 audio-speech 的：
// tts 模型在页面里照常可选，诊断却说它不可见）。
func TestPlaygroundNonChatEndpointKeysMatchFrontend(t *testing.T) {
	const relPath = "../web/classic/src/constants/playground.constants.js"
	raw, err := os.ReadFile(relPath)
	if err != nil {
		// 只 checkout 了 Go 代码（或在别的目录下跑）时跳过：本用例守的是跨端一致性，
		// 没有前端源码就无从比较，跳过比误报失败更合适。
		t.Skipf("跳过：读不到前端常量文件 %s: %v", relPath, err)
	}
	source := string(raw)

	// 取 PLAYGROUND_UNSUPPORTED_ENDPOINTS = { ... }; 这一段，再抓其中的键。
	start := strings.Index(source, "PLAYGROUND_UNSUPPORTED_ENDPOINTS = {")
	require.GreaterOrEqual(t, start, 0, "前端常量 PLAYGROUND_UNSUPPORTED_ENDPOINTS 不见了？")
	rest := source[start:]
	end := strings.Index(rest, "\n};")
	require.Greater(t, end, 0, "没能定位前端常量的结束位置")
	block := rest[:end]

	keyRe := regexp.MustCompile(`(?m)^\s{2}'?([a-z0-9-]+)'?:\s*\{`)
	matches := keyRe.FindAllStringSubmatch(block, -1)
	frontendKeys := make(map[string]bool, len(matches))
	for _, m := range matches {
		frontendKeys[m[1]] = true
	}
	require.NotEmpty(t, frontendKeys, "没能从前端常量里解析出任何键")

	backendKeys := make(map[string]bool, len(constant.PlaygroundNonChatEndpointLabels))
	for key := range constant.PlaygroundNonChatEndpointLabels {
		backendKeys[string(key)] = true
	}

	require.Equal(t, frontendKeys, backendKeys,
		"后端 PlaygroundNonChatEndpointLabels 与前端 PLAYGROUND_UNSUPPORTED_ENDPOINTS 不一致：多出来的会被诊断误报为不可见，少的会漏报")

	require.True(t, constant.IsPlaygroundNonChatEndpoint(constant.EndpointTypeOpenAIVideo))
	require.False(t, constant.IsPlaygroundNonChatEndpoint(constant.EndpointTypeOpenAI))
	// TTS 不在前端过滤清单里，后端也不能报它不可见
	require.False(t, constant.IsPlaygroundNonChatEndpoint(constant.EndpointTypeAudioSpeech))
}
