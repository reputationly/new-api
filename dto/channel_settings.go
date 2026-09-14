package dto

type ChannelSettings struct {
	ForceFormat            bool   `json:"force_format,omitempty"`
	ThinkingToContent      bool   `json:"thinking_to_content,omitempty"`
	Proxy                  string `json:"proxy"`
	PassThroughBodyEnabled bool   `json:"pass_through_body_enabled,omitempty"`
	SystemPrompt           string `json:"system_prompt,omitempty"`
	SystemPromptOverride   bool   `json:"system_prompt_override,omitempty"`
	// PassThroughResultURL 异步任务成品直接透传上游 URL，不搬进 OBS。
	// 覆盖全局的 media_storage.ingest_upstream_url（仅本渠道、仅收紧方向）。
	//
	// 用途：第三方渠道的成品本来就有公网可直达的地址，中转一道既费我们的带宽，
	// 用户看到的域名也不是供应商的、容易以为被我们动过手脚。
	//
	// 代价：上游 URL 大多带过期时间（如火山 TOS 的 X-Tos-Expires=86400），到期后
	// 历史记录里的成品就打不开了——搬 OBS 正是在解决这个。所以默认关，按渠道单开。
	// base64 / data: URI 不受影响：那条路本来就不走这里，必须落盘才有 URL 可给。
	PassThroughResultURL bool `json:"pass_through_result_url,omitempty"`
	// GPUStackAffinity 把同一段对话的后续轮次钉回同一个 GPUStack 模型实例。
	//
	// 为什么需要：GPUStack 的网关按百分比在各 RUNNING 实例之间随机分流（每个实例
	// 注册成独立 service），而 vLLM 的前缀缓存是**实例本地**的——同一会话的第二轮
	// 落到别的实例就要重算整段前缀。线上前缀命中率 77.3%，而计费产能约等于
	// 实算产能 ÷ (1 − 命中率)，随机分流到 N 个实例后命中率约摊薄成 1/N。
	//
	// 做法：取对话的稳定前缀做 HRW 哈希选出一个实例，下发
	// X-GPUStack-Model-Instance 头，GPUStack 网关据此直接路由。实例列表来自
	// GPUStack 的 /v2/model-instances，所以实例重启换 ID 会自动跟上。
	//
	// 默认关：只对以 GPUStack 为上游的渠道有意义。任何一步失败（拉不到实例、
	// 消息为空）都不发这个头，行为退回 GPUStack 自己的随机分流。
	//
	// 作用范围仅限 **OpenAI 格式的 chat completions**：路由头在 TextHelper 里计算，
	// 而 /v1/messages（ClaudeHelper）与 /v1/responses（ResponsesHelper）是另外两条
	// 独立链路，不经过那里。这两种格式上开这个开关不会报错，只是没有效果——
	// UI 的说明文字里写明了这一点。要扩到那两条链路，在对应 helper 的
	// ModelMappedHelper 之后照抄 TextHelper 里那一段即可。
	GPUStackAffinity bool `json:"gpustack_affinity,omitempty"`
	// GPUStackAffinityKey 调 GPUStack 管理 API 用的 key（/v2/model-instances 需要
	// org owner 角色，渠道自身的推理 key 权限不够）。地址复用渠道的 Base URL。
	GPUStackAffinityKey string `json:"gpustack_affinity_key,omitempty"`
}

type VertexKeyType string

const (
	VertexKeyTypeJSON   VertexKeyType = "json"
	VertexKeyTypeAPIKey VertexKeyType = "api_key"
)

type AwsKeyType string

const (
	AwsKeyTypeAKSK   AwsKeyType = "ak_sk" // 默认
	AwsKeyTypeApiKey AwsKeyType = "api_key"
)

type ChannelOtherSettings struct {
	AzureResponsesVersion                 string        `json:"azure_responses_version,omitempty"`
	VertexKeyType                         VertexKeyType `json:"vertex_key_type,omitempty"` // "json" or "api_key"
	OpenRouterEnterprise                  *bool         `json:"openrouter_enterprise,omitempty"`
	ClaudeBetaQuery                       bool          `json:"claude_beta_query,omitempty"`         // Claude 渠道是否强制追加 ?beta=true
	AllowServiceTier                      bool          `json:"allow_service_tier,omitempty"`        // 是否允许 service_tier 透传（默认过滤以避免额外计费）
	AllowInferenceGeo                     bool          `json:"allow_inference_geo,omitempty"`       // 是否允许 inference_geo 透传（仅 Claude，默认过滤以满足数据驻留合规
	AllowSpeed                            bool          `json:"allow_speed,omitempty"`               // 是否允许 speed 透传（仅 Claude，默认过滤以避免意外切换推理速度模式）
	AllowSafetyIdentifier                 bool          `json:"allow_safety_identifier,omitempty"`   // 是否允许 safety_identifier 透传（默认过滤以保护用户隐私）
	DisableStore                          bool          `json:"disable_store,omitempty"`             // 是否禁用 store 透传（默认允许透传，禁用后可能导致 Codex 无法使用）
	AllowIncludeObfuscation               bool          `json:"allow_include_obfuscation,omitempty"` // 是否允许 stream_options.include_obfuscation 透传（默认过滤以避免关闭流混淆保护）
	AwsKeyType                            AwsKeyType    `json:"aws_key_type,omitempty"`
	UpstreamModelUpdateCheckEnabled       bool          `json:"upstream_model_update_check_enabled,omitempty"`        // 是否检测上游模型更新
	UpstreamModelUpdateAutoSyncEnabled    bool          `json:"upstream_model_update_auto_sync_enabled,omitempty"`    // 是否自动同步上游模型更新
	UpstreamModelUpdateLastCheckTime      int64         `json:"upstream_model_update_last_check_time,omitempty"`      // 上次检测时间
	UpstreamModelUpdateLastDetectedModels []string      `json:"upstream_model_update_last_detected_models,omitempty"` // 上次检测到的可加入模型
	UpstreamModelUpdateLastRemovedModels  []string      `json:"upstream_model_update_last_removed_models,omitempty"`  // 上次检测到的可删除模型
	UpstreamModelUpdateIgnoredModels      []string      `json:"upstream_model_update_ignored_models,omitempty"`       // 手动忽略的模型
}

func (s *ChannelOtherSettings) IsOpenRouterEnterprise() bool {
	if s == nil || s.OpenRouterEnterprise == nil {
		return false
	}
	return *s.OpenRouterEnterprise
}
