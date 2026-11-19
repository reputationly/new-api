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
// PLAYGROUND_UNSUPPORTED_ENDPOINTS **逐字一致** —— 前端按那份键过滤模型下拉，后端按它
// 解释"为什么看不到"。漂移的后果是诊断页与页面给出相反结论（曾经多了一个
// audio-speech：tts 模型在页面里其实照常可选，后端却说它不可见）。
//
// 因此这里刻意**不含 audio-speech**：TTS 模型的调用方式不对（不是 chat completions），
// 但文本体验区并不过滤它。需要"是否适合 chat"的判断另有 isChatModel，别混用。
//
// 两份键集合由 controller 的用例直接读前端文件比对（见 TestPlaygroundNonChatEndpointKeys）。
var PlaygroundNonChatEndpointLabels = map[EndpointType]string{
	EndpointTypeImageGeneration: "图像生成",
	EndpointTypeOpenAIVideo:     "视频生成",
	EndpointTypeEmbeddings:      "嵌入向量",
	EndpointTypeJinaRerank:      "重排序",
}

// IsPlaygroundNonChatEndpoint 报告某端点类型是否会让模型从文本体验区消失。
func IsPlaygroundNonChatEndpoint(endpointType EndpointType) bool {
	_, ok := PlaygroundNonChatEndpointLabels[endpointType]
	return ok
}
