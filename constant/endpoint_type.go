package constant

type EndpointType string

const (
	EndpointTypeOpenAI                EndpointType = "openai"
	EndpointTypeOpenAIResponse        EndpointType = "openai-response"
	EndpointTypeOpenAIResponseCompact EndpointType = "openai-response-compact"
	EndpointTypeAnthropic             EndpointType = "anthropic"
	EndpointTypeGemini                EndpointType = "gemini"
	EndpointTypeJinaRerank            EndpointType = "jina-rerank"
	EndpointTypeImageGeneration       EndpointType = "image-generation"
	EndpointTypeEmbeddings            EndpointType = "embeddings"
	EndpointTypeOpenAIVideo           EndpointType = "openai-video"
	EndpointTypeAudioSpeech           EndpointType = "audio-speech"
	//EndpointTypeMidjourney     EndpointType = "midjourney-proxy"
	//EndpointTypeSuno           EndpointType = "suno-proxy"
	//EndpointTypeKling          EndpointType = "kling"
	//EndpointTypeJimeng         EndpointType = "jimeng"
)

// PlaygroundNonChatEndpointLabels 是「文本体验区会整体排除」的端点类型及其中文名。
//
// 键必须与 web/classic/src/constants/playground.constants.js 的
// PLAYGROUND_UNSUPPORTED_ENDPOINTS 完全一致 —— 前端按这份键过滤模型下拉，后端按它
// 解释"为什么看不到"。两处漂移的后果是诊断页与页面给出相反结论，所以放在 constant 里
// 只有一份，并由用例锁住这几个键。
var PlaygroundNonChatEndpointLabels = map[EndpointType]string{
	EndpointTypeImageGeneration: "图像生成",
	EndpointTypeOpenAIVideo:     "视频生成",
	EndpointTypeEmbeddings:      "嵌入向量",
	EndpointTypeJinaRerank:      "重排序",
	EndpointTypeAudioSpeech:     "语音合成",
}

// IsPlaygroundNonChatEndpoint 报告某端点类型是否会让模型从文本体验区消失。
func IsPlaygroundNonChatEndpoint(endpointType EndpointType) bool {
	_, ok := PlaygroundNonChatEndpointLabels[endpointType]
	return ok
}
