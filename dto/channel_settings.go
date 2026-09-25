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
	// 落到别的实例就要重算整段前缀。计费产能约等于实算产能 ÷ (1 − 命中率)。
	// 4 副本上受控回放（8 会话 × 5 轮）实测命中率 34.1%，逐轮命中占比
	// 0/0/38/62/75%——随机分流的特征曲线。
	//
	// 做法：取对话的稳定前缀做 HRW 哈希选出一个实例，然后把本次请求的目标地址
	// **改写成该实例推理端口的直连地址**（http://<worker_ip>:<port>）。实例列表与
	// 地址都来自 GPUStack 的 /v2/model-instances，所以实例重启换地址会自动跟上。
	//
	// 为什么不是下发路由头：X-GPUStack-Model-Instance 是 GPUStack「网关 → worker」
	// 的内部信号——网关把自己选中的实例写进这个头告诉 worker，而它的目标选择**不读**
	// 这个头。客户端传进去只会让两端不一致：请求被负载均衡送到 worker X，头却说实例
	// 在 worker Y，worker X 查不到就 404（2026-09-15 实测 3/6 请求失败）。
	//
	// 前提：new-api 所在主机必须能直连 worker 的推理端口（同内网即可，vLLM 侧无
	// 鉴权）。直连绕过了网关的计量与 ingress 重试策略，所以任何一步失败——拉不到
	// 实例、消息为空、拨号不通、上游返回 404/502/503/504——都会自动退回渠道原本的
	// Base URL（网关）重试一次，可用性不低于不开这个开关。
	//
	// 默认关：只对以 GPUStack 为上游的渠道有意义。
	//
	// 作用范围仅限 **OpenAI 格式的 chat completions**：直连地址在 TextHelper 里计算，
	// 而 /v1/messages（ClaudeHelper）与 /v1/responses（ResponsesHelper）是另外两条
	// 独立链路，不经过那里。这两种格式上开这个开关不会报错，只是没有效果——
	// UI 的说明文字里写明了这一点。要扩到那两条链路，在对应 helper 的
	// ModelMappedHelper 之后照抄 TextHelper 里那一段即可。
	GPUStackAffinity bool `json:"gpustack_affinity,omitempty"`
	// GPUStackAffinityKey 调 GPUStack 管理 API 用的 key。地址复用渠道的 Base URL。
	//
	// 这个 key 的**作用域**必须能读到模型与实例：/v2/model-instances 按
	// owner_principal_id == 当前 principal 过滤，而 API key 认证时这个值恒等于
	// 密钥自己的 owner（X-Organization-Id 对 API key 无效）。个人作用域的密钥会
	// 返回 HTTP 200 + total=0 —— 不报错，就是空，于是亲和静默失效。需要由平台
	// 管理员在「无组织上下文」下创建的密钥（owner_principal_id 为空），或至少
	// 属于模型所在的组织；GPUStack 密钥列表页的「作用域」列可以直接看出来。
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

	// 方舟素材库（仅 DoubaoVideo / VolcEngine）：开启后参考图与参考视频先入素材库，再以 asset:// 调生成，
	// 避开 Seedance 对直传人脸素材的输入审核。AK/SK 为空时用渠道 API Key 走 Bearer（仅兼容网关支持）。
	ArkAssetEnabled     bool   `json:"ark_asset_enabled,omitempty"`
	ArkAssetAccessKey   string `json:"ark_asset_access_key,omitempty"`
	ArkAssetSecretKey   string `json:"ark_asset_secret_key,omitempty"`
	ArkAssetEndpoint    string `json:"ark_asset_endpoint,omitempty"`     // 空 = 按 Base URL 推断
	ArkAssetProjectName string `json:"ark_asset_project_name,omitempty"` // 空 = default
}

func (s *ChannelOtherSettings) IsOpenRouterEnterprise() bool {
	if s == nil || s.OpenRouterEnterprise == nil {
		return false
	}
	return *s.OpenRouterEnterprise
}
