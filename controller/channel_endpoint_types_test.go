package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 渠道编辑页的保存前预警：端点类型由「渠道类型 + 模型名」共同决定，前端不能自己复刻。
// 这些分支正是"模型广场有、文本体验区没有"的成因，逐个锁住。
func TestGetChannelModelEndpointTypes(t *testing.T) {
	gin.SetMode(gin.TestMode)

	call := func(query string) (int, map[string]channelModelEndpointType) {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodGet, "/api/channel/endpoint_types?"+query, nil)
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
	code, models := call("type=1&models=qwen3.8-max")
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, []string{"openai"}, models["qwen3.8-max"].EndpointTypes)
	require.False(t, models["qwen3.8-max"].PlaygroundBlocked)

	// GPUStackPlus(59)：名字不像图片的一律当视频 —— 文本模型挂上去就会"消失"
	_, models = call("type=59&models=wan2.2-t2v,qwen-image,qwen3.8-max")
	require.Equal(t, []string{string("openai-video")}, models["wan2.2-t2v"].EndpointTypes)
	require.True(t, models["wan2.2-t2v"].PlaygroundBlocked)
	require.Equal(t, []string{string(constant.EndpointTypeImageGeneration)}, models["qwen-image"].EndpointTypes)
	require.True(t, models["qwen-image"].PlaygroundBlocked)
	require.True(t, models["qwen3.8-max"].PlaygroundBlocked)

	// Sora(55) → openai-video；Jina(38) → jina-rerank
	_, models = call("type=55&models=sora-2")
	require.True(t, models["sora-2"].PlaygroundBlocked)
	_, models = call("type=38&models=jina-reranker-v3")
	require.Equal(t, []string{string(constant.EndpointTypeJinaRerank)}, models["jina-reranker-v3"].EndpointTypes)
	require.True(t, models["jina-reranker-v3"].PlaygroundBlocked)

	// 名字启发式：即便是 OpenAI 渠道，gpt-image-1 / tts 系也会被标成非 chat 端点
	_, models = call("type=1&models=gpt-image-1,qwen3-tts")
	require.True(t, models["gpt-image-1"].PlaygroundBlocked)
	require.True(t, models["qwen3-tts"].PlaygroundBlocked)

	// 参数校验与空列表
	code, _ = call("type=abc&models=x")
	require.Equal(t, http.StatusBadRequest, code)
	code, models = call("type=1&models=")
	require.Equal(t, http.StatusOK, code)
	require.Empty(t, models)
}

// 这份键集合必须与前端 PLAYGROUND_UNSUPPORTED_ENDPOINTS 一致（前端按它过滤模型下拉）。
// 用显式列表锁住，任何增删都会在这里显形，而不是等运营发现"页面没警告但模型选不到"。
func TestPlaygroundNonChatEndpointKeys(t *testing.T) {
	require.Equal(t, map[constant.EndpointType]string{
		constant.EndpointTypeImageGeneration: "图像生成",
		constant.EndpointTypeOpenAIVideo:     "视频生成",
		constant.EndpointTypeEmbeddings:      "嵌入向量",
		constant.EndpointTypeJinaRerank:      "重排序",
		constant.EndpointTypeAudioSpeech:     "语音合成",
	}, constant.PlaygroundNonChatEndpointLabels)
	require.True(t, constant.IsPlaygroundNonChatEndpoint(constant.EndpointTypeOpenAIVideo))
	require.False(t, constant.IsPlaygroundNonChatEndpoint(constant.EndpointTypeOpenAI))
}
