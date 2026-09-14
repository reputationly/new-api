package dto

// MiniMax Design（内部代号 hilo）本地 gateway 向云端要的模型目录。
//
// # 这些字段是抄来的，不是设计出来的
//
// 官方本地 gateway 用 zod 校验这份响应，**校验不过就整份拒绝**（`invalidSchema()`），
// 表现是模型选择器全空、所有 backend 未注册、生成一律报
// `Backend "xxx" not registered`。所以字段名和可选性必须逐条对齐，
// 不能凭感觉增减。
//
// 对应的 schema（gateway/dist/main.js 里的 `catalogSchema`）：
//
//	catalogSchema = z.object({
//	  imageModels: z.array(mediaModelSchema),
//	  videoModels: z.array(mediaModelSchema),
//	  audioModels: z.array(mediaModelSchema),
//	  textModels:  z.array(textModelSchema),
//	  defaultTextModelId: z.string().optional(),
//	})
//
// # 为什么这条接口是总开关
//
// 官方 gateway 的 backend 注册表**由这份目录驱动**：目录里没有报出某个
// backend，对应的生成路径就整条不存在。画布、素材、队列、占位符那些都在
// 本地 gateway 里跑，我们只要把这份目录填对，生成链就通了。

// HiloModelParam 是模型的一个可调参数。
//
// zod 那边是按 `type` 区分的 discriminatedUnion（select / textarea / slider），
// 三者共用 label/placeholder/default/marks/optional，各自再加自己的字段。
// Go 这边合成一个结构体、靠 omitempty 裁剪 —— 分成三个类型的话，
// `map[string]HiloModelParam` 就没法写了。
//
// **`Default` 不能 omitempty**：zod 里它是必填的 `z.string()`，
// 而空字符串是合法默认值（"不预选"）。漏掉这个键会让整份目录被拒。
type HiloModelParam struct {
	Type        string   `json:"type"` // select | textarea | slider
	Label       string   `json:"label"`
	Default     string   `json:"default"`
	Placeholder string   `json:"placeholder,omitempty"`
	Marks       []string `json:"marks,omitempty"`
	Optional    bool     `json:"optional,omitempty"`
	// select 专有。zod 要求 `.min(1)` —— 空数组会被拒。
	Options []string `json:"options,omitempty"`
	// slider 专有。zod 还会额外校验 min <= max。
	Min  *float64 `json:"min,omitempty"`
	Max  *float64 `json:"max,omitempty"`
	Step *float64 `json:"step,omitempty"`
}

// HiloMediaModel 是图片 / 视频 / 音频模型的一条目录项。
//
// 必填的那几个（id / name / backend / max_refs / params / type /
// display_name / tool_names / description / visibility / icon_url / hot）
// 少一个都会让**整份目录**被拒，不是只丢这一条。
type HiloMediaModel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// 必须是官方那 25 个枚举值之一，见 [HiloBackends]。
	// 写一个不在枚举里的字符串 = 整份目录被拒。
	Backend string `json:"backend"`
	// 真正下发给上游的模型名。留空时官方用 `id`。
	ModelName string `json:"model_name,omitempty"`
	PricingID string `json:"pricingId,omitempty"`
	// 能接几张参考图。**不能 omitempty** —— 0 是合法值（不接参考图），
	// 而这个字段在 zod 里是必填的。
	MaxRefs           int  `json:"max_refs"`
	MaxVideoRefs      *int `json:"max_video_refs,omitempty"`
	MaxAudioRefs      *int `json:"max_audio_refs,omitempty"`
	MaxVideoAudioRefs *int `json:"max_video_audio_refs,omitempty"`

	// image | video | audio
	Type string `json:"type"`
	// 参数表。**不能 omitempty** —— zod 是 `z.record(modelParamSchema)`
	// 必填，没有参数的模型要给一个空对象 `{}`，不是缺这个键。
	Params map[string]HiloModelParam `json:"params"`

	// 首尾帧 / 参考 / 文生视频 …… 决定界面上哪些参数可见。
	ImageMode string `json:"imageMode,omitempty"`
	// 某种玩法下要隐藏哪些参数。官方注释说这里**故意放宽**：
	// 未知的 key 在运行时无害，而严格校验会把整份目录否掉。
	HiddenParamsByImageMode map[string][]string `json:"hiddenParamsByImageMode,omitempty"`

	PromptRequired    bool   `json:"promptRequired,omitempty"`
	PromptLabel       string `json:"promptLabel,omitempty"` // prompt | text | musicStyle
	PromptMaxLength   int    `json:"promptMaxLength,omitempty"`
	HideInModelPicker bool   `json:"hideInModelPicker,omitempty"`

	// 展示用。`description` / `visibility` / `icon_url` 在 zod 里有
	// `.default("")`，但**那是给缺键兜底的**；我们显式给空串，语义更清楚。
	DisplayName string   `json:"display_name"`
	Description string   `json:"description"`
	Visibility  string   `json:"visibility"`
	IconURL     string   `json:"icon_url"`
	ToolNames   []string `json:"tool_names"`
	Hot         bool     `json:"hot"`
	SeriesID    string   `json:"series_id,omitempty"`
	MentionName string   `json:"mention_name,omitempty"`
	Region      string   `json:"region,omitempty"` // domestic | overseas

	// 只给了首帧也能生成（不强制要尾帧）。
	SupportsLastFrameOnly bool `json:"supportsLastFrameOnly,omitempty"`
	// 参数之间的互斥规则。**数据驱动，不是写死在界面里** ——
	// 见 [HiloParamConstraint]。
	ParamConstraints []HiloParamConstraint `json:"paramConstraints,omitempty"`
	// 输入图的尺寸与比例限制。界面拿它在**发请求之前**拦下不合规的底图。
	InputMediaLimits *HiloInputMediaLimits `json:"inputMediaLimits,omitempty"`
	// 视频续写的时长范围。
	VideoExtension *HiloVideoExtension `json:"videoExtension,omitempty"`
}

// HiloParamConstraint 是一条参数互斥规则：**某个参数取某值时，禁掉另一个
// 参数的某些选项**。
//
// 典型用例是首尾帧驱动的视频 —— 画幅由那张图定，这时所有比例选项都要禁掉。
// 这条规则以前我们是写死在界面代码里的，官方把它放在目录数据里，
// 换模型时不用改前端。
type HiloParamConstraint struct {
	If      HiloParamCond    `json:"if"`
	Disable HiloParamDisable `json:"disable"`
}

type HiloParamCond struct {
	Param string `json:"param"`
	Eq    string `json:"eq"`
}

type HiloParamDisable struct {
	Param   string   `json:"param"`
	Options []string `json:"options"`
}

// HiloInputMediaLimits 输入图的硬限制。字段名是驼峰（和同层的
// `max_refs` 下划线混用）—— 官方就是这样，照抄。
type HiloInputMediaLimits struct {
	ImageMinWidth       int     `json:"imageMinWidth,omitempty"`
	ImageMinHeight      int     `json:"imageMinHeight,omitempty"`
	ImageMaxWidth       int     `json:"imageMaxWidth,omitempty"`
	ImageMaxHeight      int     `json:"imageMaxHeight,omitempty"`
	ImageMinAspectRatio float64 `json:"imageMinAspectRatio,omitempty"`
	ImageMaxAspectRatio float64 `json:"imageMaxAspectRatio,omitempty"`
}

// HiloVideoExtension 视频续写的时长范围。四个字段在 zod 里**都是必填**。
type HiloVideoExtension struct {
	InputMinDurationSec  float64 `json:"inputMinDurationSec"`
	InputMaxDurationSec  float64 `json:"inputMaxDurationSec"`
	OutputMinDurationSec float64 `json:"outputMinDurationSec"`
	OutputMaxDurationSec float64 `json:"outputMaxDurationSec"`
}

// HiloTextModel 是文本模型的目录项。字段比媒体模型少得多。
type HiloTextModel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Provider 前缀由**持有 provider 配置的那一方**填(见
	// setting/hilo_catalog_text.go)。new-api 不下发 provider 配置,
	// 所以这里留空 —— omitempty 让它整个字段不出现,而不是下发一个
	// 空字符串:空串会让客户端把模型归到一个叫 "" 的 provider 下。
	Provider        string                    `json:"provider,omitempty"`
	Params          map[string]HiloModelParam `json:"params,omitempty"`
	PromptMaxLength int                       `json:"promptMaxLength,omitempty"`
	SupportsVideo   bool                      `json:"supportsVideo,omitempty"`
	SupportsAudio   bool                      `json:"supportsAudio,omitempty"`
	Region          string                    `json:"region,omitempty"`
}

// HiloModelsConfig 是 `/api/v1/models/config` 的完整响应。
//
// **四个数组都不能 omitempty**：zod 要的是 `z.array(...)` 必填，
// 缺键会被判 invalid schema，而 `nil` 序列化出来是 `null` 不是 `[]` ——
// 所以构造时必须用 `make([]T, 0)` 而不是 `var x []T`。
type HiloModelsConfig struct {
	ImageModels []HiloMediaModel `json:"imageModels"`
	VideoModels []HiloMediaModel `json:"videoModels"`
	AudioModels []HiloMediaModel `json:"audioModels"`
	TextModels  []HiloTextModel  `json:"textModels"`
	// 留空时官方自己挑第一个。
	DefaultTextModelID string `json:"defaultTextModelId,omitempty"`
}

// HiloBackends 是官方 `backendSchema` 的全部枚举值。
//
// 目录里的 `backend` 不在这个集合里，**整份目录会被拒**（不是只丢那一条）。
// 所以映射表里写错一个名字，表现是"模型一个都没有"，而不是"少了一个模型"。
var HiloBackends = map[string]bool{
	"nano_banana": true, "kling": true, "kontext": true, "openai": true,
	"midjourney": true, "seedream": true, "qwen": true, "minimax": true,
	"minimax_v3": true, "veo3": true, "wan_i2v": true, "minimax_tts": true,
	"seedaudio": true, "minimax_music": true, "minimax_music_cover": true,
	"elevenlabs_music": true, "seedance": true, "kling_avatar": true,
	"kling_motion_control": true, "jimeng_motion_control": true,
	"text_anthropic": true, "text_openai": true, "text_gemini": true,
	"mediakit_enhance": true, "mediakit_erase_subtitle": true,
}
