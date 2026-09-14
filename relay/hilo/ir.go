package hilo

// Context-IR：LLM 改写视频提示词时产出的**中间表示**。
//
// # 为什么要中间这一层
//
// 直接让 LLM 写提示词正文的话，「必须有哪几段」「顺序对不对」「镜头时长
// 加起来等不等于请求时长」「素材标签有没有引用错」这些**只能靠它记得**，
// 漏了我们不知道 —— 拿到的是一段读起来通顺的文本。
//
// 让它先产结构化 IR，这些就都是确定性可查的，而提示词正文由**代码**拼装,
// 段落名和顺序不可能错。
//
// 移植自 XINGSHEN2/minimax-H3-context-IR。字段名保持一致，便于对照它的
// 校验规则与渲染器。
//
// # 五种任务，两种渲染
//
//	t2v / i2v / flf2v / l2va   →  三字段（base）
//	r2va                       →  六段式（ref），带 retention_analysis
//
// **六段式那条是目录里的默认玩法**（image_mode 默认 reference），
// 不能当边角情况对待。retention_analysis 正是"编辑"的表达方式：
// 四个标记从"原样保留"到"只借个风格"覆盖全谱，所以 H3 不需要单独的
// v2v —— 参考生视频加上 retention 标记就覆盖了编辑。

// RetentionMode 参考内容在目标视频里被保留到什么程度。
//
// 这四个值是**官方输出格式里的固定英文字面量**（ref-en.txt 的表格），
// 不能改写或翻译 —— 它们会原样出现在最终提示词里。
type RetentionMode string

const (
	// RetentionFull 定义的角色完整保留。
	RetentionFull RetentionMode = "fully_preserved"
	// RetentionPartial 还在用，但部分已定义的特征被改了或只保留了一部分。
	// **这是"编辑"最常用的那个**。
	RetentionPartial RetentionMode = "partially_preserved"
	// RetentionTransfer 特征被转移到另一个可识别的目标主体上（换装、换脸）。
	RetentionTransfer RetentionMode = "attribute_transfer"
	// RetentionWeak 只保留风格、类别、构图或氛围上的宽泛相似。
	RetentionWeak RetentionMode = "weak_reference"
)

// ContextIR 一次改写的完整中间表示。
type ContextIR struct {
	SchemaVersion string `json:"schema_version"`

	// Task 这次要生成什么。**由我们填，不由 LLM 决定** —— 它是从客户端
	// 请求的素材字段推出来的（见 VideoRequest.TaskTypeOf），LLM 改了就是
	// 把玩法换掉了。
	Task IRTask `json:"task"`

	// SemanticPlan 先形成的决策记录，其余字段都从它推导。
	// 它是**紧凑的决策记录，不是第二条互相打架的时间线**。
	SemanticPlan IRSemanticPlan `json:"semantic_plan"`

	// Protocol 改写语言约定。**理解语言和改写语言是两件事**：
	// 正文用改写语言，只有逐字台词、歌词和画面内可见文字保留源语言。
	Protocol IRProtocol `json:"protocol"`

	// Assets 本次请求的素材清单,**按提交顺序**。
	//
	// **由我们填,不由 LLM 决定**(见 applyAuthoritativeFacts)。
	// <Picture N> / <Video N> 的标号就按这个顺序发 —— 模型的书写顺序
	// 不作数:它先写 image_2 再写 image_1,标号就整体错位一位,提示词
	// 指着的素材和它描述的不是同一个,而且完全不报错。
	Assets []IRAsset `json:"assets"`

	// Subjects 需要在后续各段里被单独追踪的实体。
	Subjects []IRSubject `json:"subjects"`
	// AssetBindings 每个素材绑到哪个语义目标、提供什么、排除什么。
	AssetBindings []IRAssetBinding `json:"asset_bindings"`
	// ReferenceRelationships 参考素材在目标视频里的角色（六段式用）。
	ReferenceRelationships []IRReferenceRelationship `json:"reference_relationships"`
	// KeyframeRoles 关键帧的时间角色与控制维度。
	KeyframeRoles []IRKeyframeRole `json:"keyframe_roles"`

	CreativeFocus IRCreativeFocus `json:"creative_focus"`
	Constraints   IRConstraints   `json:"constraints"`

	// Timeline 按时间推进的镜头序列。
	Timeline []IRShot `json:"timeline"`

	AudioPlan IRAudioPlan `json:"audio_plan"`

	// GenerationDescription 全局基准：整片共享的摄影/光线/材质/表演/连续性。
	GenerationDescription IRGenerationDescription `json:"generation_description"`

	// Intent 编译过程中的假设与不确定性。**不进最终提示词**，是排障用的 ——
	// 客户报"生成的跟我写的不一样"时，这里是唯一能解释清楚的记录。
	Intent IRIntent `json:"intent"`
}

type IRTask struct {
	// Type 平台的 task_type：t2v / i2v / flf2v / l2va / r2va。
	Type string `json:"type"`
	// DurationSeconds 请求的时长。镜头时长加起来必须等于它。
	DurationSeconds float64 `json:"duration_seconds"`
	// GenerateAudio 关掉时两个声音段都渲染成 N/A，而不是留空。
	GenerateAudio bool `json:"generate_audio"`
}

// IRAsset 一件素材及其**已经确定的**角色。
type IRAsset struct {
	AssetID   string `json:"asset_id"`
	MediaType string `json:"media_type"` // image | video | audio
	Role      string `json:"role"`       // first_frame | last_frame | reference
}

type IRProtocol struct {
	// RewriteLanguage 正文用什么语言写。
	RewriteLanguage string `json:"rewrite_language"`
	// PreserveSourceLanguageFor 这几类**保留源语言**：逐字台词、歌词、
	// 画面内可见文字。翻译它们是这一步最容易犯、也最难发现的错。
	PreserveSourceLanguageFor []string `json:"preserve_source_language_for"`
}

type IRSemanticPlan struct {
	PrimaryFocus string `json:"primary_focus"`
	// SubjectPriority 多个主体时谁主谁次。用户并列呈现几个人/产品时标
	// co_equal，**不要因为 schema 需要一个主体就把别人降级**。
	SubjectPriority IRSubjectPriority `json:"subject_priority"`
	// TextPolicy 画面内文字的权限。**"素材里有文字"不等于"可以用"** ——
	// 只有用户要求的内容、指定的帧、或编辑基底的保留范围包含它时才继承。
	TextPolicy IRTextPolicy `json:"text_policy"`
	// EditScope 这次是生成、参考迁移，还是局部编辑。
	EditScope IREditScope `json:"edit_scope"`
	// CompletionAuthority 允许补全到什么程度。技术性补全（声音、连续性）
	// 和创作性补全（新剧情、品牌主张）是两回事。
	CompletionAuthority IRCompletionAuthority `json:"completion_authority"`
	// ShotFunctions 每个镜头承担什么功能、观众能多知道什么、为什么在这切。
	ShotFunctions []IRShotFunction `json:"shot_functions"`
}

type IRSubjectPriority struct {
	// Mode single | co_equal | explicit_hierarchy
	Mode       string   `json:"mode"`
	SubjectIDs []string `json:"subject_ids"`
	Reason     string   `json:"reason"`
}

type IRTextPolicy struct {
	// Mode exact | preserve | generate | omit
	Mode string `json:"mode"`
	// AllowInvention 允许不允许编字。**默认不允许** —— OCR 不全时猜字符
	// 会产出看起来合理的错字。
	AllowInvention bool     `json:"allow_invention"`
	ProvidedText   []string `json:"provided_text"`
}

type IREditScope struct {
	// Mode generate | reference_transfer | minimal_edit
	//
	// minimal_edit：**只改明确可编辑的那一处**，源视频的运镜、动作、主体、
	// 场景元素和效果全部锁住。这是"局部编辑"的表达。
	Mode     string   `json:"mode"`
	Editable []string `json:"editable"`
	// Locked 有来源依据的约束，不是"你选的解法"。每条都要有明确指令、
	// 授权的参考保留、或真实的连续性需求作依据。
	Locked []string `json:"locked"`
}

type IRCompletionAuthority struct {
	// Technical 声音、连续性这类技术性补全。
	Technical bool `json:"technical"`
	// Timeline 时间线补全。
	Timeline bool `json:"timeline"`
	// StoryContinuation 续写剧情。默认关。
	StoryContinuation bool `json:"story_continuation"`
	// NewCoreEntities 新增核心实体（人物、产品）。默认关。
	NewCoreEntities bool `json:"new_core_entities"`
	// BrandClaims 品牌主张。默认关 —— 编出来的功效宣称是真实风险。
	BrandClaims bool `json:"brand_claims"`
}

type IRShotFunction struct {
	ShotID string `json:"shot_id"`
	// Purpose 这个镜头唯一的叙事或呈现功能。
	Purpose string `json:"purpose"`
	// AudienceGain 到这个镜头结束时，观众新看到了什么。
	AudienceGain string `json:"audience_gain"`
	// CutReason 为什么在这里切：new_information | action_match |
	// state_change | viewpoint_change | reference_match | user_locked | none
	CutReason string `json:"cut_reason"`
}

type IRSubject struct {
	SubjectID string `json:"subject_id"`
	Name      string `json:"name"`
	// Kind person | product | animal | object | environment | other
	Kind        string `json:"kind"`
	Primary     bool   `json:"primary"`
	Description string `json:"description"`
	// SourceAssetIDs 外观来源。
	SourceAssetIDs []string `json:"source_asset_ids"`
	// AppearanceShotIDs 在哪几个镜头里出现。
	AppearanceShotIDs []string `json:"appearance_shot_ids"`
	// RetentionMode / RetentionDescription 保留到什么程度、保留了什么。
	// **六段式的 retention_analysis 就是从这两个字段渲染出来的。**
	RetentionMode        RetentionMode `json:"retention_mode"`
	RetentionDescription string        `json:"retention_description"`
}

type IRAssetBinding struct {
	AssetID string `json:"asset_id"`
	Target  string `json:"target"`
	// Role identity | outfit | product | motion | voice | music | rhythm |
	// camera | scene | style | first_frame | last_frame
	Role string `json:"role"`
	// Priority hard | soft
	Priority string `json:"priority"`
	// Inherit 从这个素材继承哪些维度。
	Inherit []string `json:"inherit"`
	// Exclude **明确排除**哪些维度。
	//
	// 这条最要紧：图片提供外观、构图、场景、风格或关键帧，**不提供运动、
	// 剪辑节奏或音乐**；视频提供动作/运镜/剪辑，**但只在用户授权时** ——
	// 「授权了动作」不等于「授权了它的运镜和切点」。
	Exclude []string `json:"exclude"`
}

type IRReferenceRelationship struct {
	AssetID string `json:"asset_id"`
	// Relationship source_video_edit | reference_generation |
	// keyframe_completion | video_continuation | audio_reuse | audio_reference
	Relationship string   `json:"relationship"`
	SubjectRefs  []string `json:"subject_refs"`
	// Definition 一个名词短语，说明这个 Picture/Video/Audio 的确切角色。
	// **不要用 "is" 开头** —— 渲染时它会被接在标签后面。
	Definition           string        `json:"definition"`
	RetentionMode        RetentionMode `json:"retention_mode"`
	RetentionDescription string        `json:"retention_description"`
}

type IRKeyframeRole struct {
	AssetID string `json:"asset_id"`
	// Role appearance_source | scene_anchor | action_keyframe |
	// product_detail | first_frame | last_frame | composition_anchor |
	// style_reference
	Role        string   `json:"role"`
	SubjectRefs []string `json:"subject_refs"`
	ShotRefs    []string `json:"shot_refs"`
	// Controls 这张图提供哪些可见维度。
	Controls []string `json:"controls"`
	// Excludes 不提供哪些 —— 通常是 motion / camera / editing / music /
	// performance rhythm。
	Excludes    []string `json:"excludes"`
	Description string   `json:"description"`
}

type IRCreativeFocus struct {
	PrimaryTarget    string `json:"primary_target"`
	PrimarySubjectID string `json:"primary_subject_id"`
	// Objective 最要紧的那个最终可见结果。
	Objective string `json:"objective"`
	// RequiredShotIDs 必须存在的镜头。
	RequiredShotIDs []string `json:"required_shot_ids"`
	// PresentationRequirements 可执行的可见性/构图/材质/连续性要求。
	PresentationRequirements []string `json:"presentation_requirements"`
}

type IRConstraints struct {
	// Preserve 必须保留的。
	Preserve []string `json:"preserve"`
	// AllowChange 只在用户要求的范围内可以改的。
	AllowChange []string `json:"allow_change"`
	// Prohibit 不得引入的。
	Prohibit []string `json:"prohibit"`
}

type IRShot struct {
	ShotID       string  `json:"shot_id"`
	StartSeconds float64 `json:"start_seconds"`
	EndSeconds   float64 `json:"end_seconds"`
	// PrimaryChange 这一拍里唯一的主要可见变化。
	PrimaryChange string `json:"primary_change"`
	// Event 一个可执行的可见事件。渲染时它是这个镜头的正文主体。
	Event string `json:"event"`
	// Camera 一条可执行的固定或有动机的运镜指令。
	Camera     string `json:"camera"`
	Lighting   string `json:"lighting"`
	Transition string `json:"transition"`
	// ObservableEndState 这一拍结束时观众能指出来的具体状态。
	ObservableEndState string `json:"observable_end_state"`
	// StateChanges 连续性关键的属性变化。
	StateChanges []IRStateChange `json:"state_changes"`
	SubjectRefs  []string        `json:"subject_refs"`
	// AssetRefs **只在这个镜头真的用到该素材的运动、运镜、节奏、风格、
	// 音频或场景指引时**才写。
	AssetRefs []string `json:"asset_refs"`
}

type IRStateChange struct {
	SubjectID string `json:"subject_id"`
	Property  string `json:"property"`
	From      string `json:"from"`
	To        string `json:"to"`
}

type IRAudioPlan struct {
	// Voice 对白/旁白。**逐字保留原文与语言**，并写清说话人。
	Voice string `json:"voice"`
	// Music 只有观众听得到的配乐（非剧情内）。
	Music string `json:"music"`
	// SoundEffects 有物理动机的动作音。
	SoundEffects string `json:"sound_effects"`
	// AmbientSound 持续的环境音。跟着场景走，不要每次切镜都重置。
	AmbientSound string `json:"ambient_sound"`
	// SyncRules 声画同步点。
	SyncRules FlexStrings `json:"sync_rules"`
}

type IRGenerationDescription struct {
	Cinematography string `json:"cinematography"`
	Lighting       string `json:"lighting"`
	Materials      string `json:"materials"`
	Performance    string `json:"performance"`
	Continuity     string `json:"continuity"`
}

type IRIntent struct {
	Assumptions   []string `json:"assumptions"`
	Uncertainties []string `json:"uncertainties"`
}
