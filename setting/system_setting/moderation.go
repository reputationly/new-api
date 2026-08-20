package system_setting

import (
	"errors"
	"os"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

// 内容审核配置。见 docs/content-moderation-design.md §8。
// 落 options 表（前缀 moderation.），内存单例，controller/option.go GET/PUT 读写。
//
// 注意 config manager 用 `Tag.Get("json")` 的**整串**当 DB key（setting/config/config.go:116），
// 不切逗号。所以顶层字段的 json tag 一律不带 ,omitempty，否则 key 会变成
// "moderation.mode,omitempty"。嵌套结构走 json.Marshal，不受此限。

// ModerationMode 运行模式。三态而非开关：observe 是灰度期唯一能在零业务风险下
// 拿到真实误杀率的手段（§8.2）。
type ModerationMode string

const (
	ModerationModeInherit  ModerationMode = ""         // 分组零值 = 跟随全局
	ModerationModeOff      ModerationMode = "off"      // 不审
	ModerationModeObserve  ModerationMode = "observe"  // 审但不拦，只记录
	ModerationModeBlocking ModerationMode = "blocking" // 审且拦
)

// 类别处置动作（§8.2 第三个旋钮）。
const (
	CategoryActionBlock  = "block"  // 直接拒绝
	CategoryActionLog    = "log"    // 仅记录
	CategoryActionIgnore = "ignore" // 不处理
)

// 判定严格度（§8.2 第二个旋钮）：决定 Controversial 算不算违规。
const (
	StrictnessLoose    = "loose"
	StrictnessStandard = "standard"
	StrictnessStrict   = "strict"
)

// Qwen3Guard 的九类（§4.1.1）。类别粒度由模型定，我们只能映射不能细分。
const (
	CategorySexual    = "sexual"    // Sexual Content or Sexual Acts —— 黄
	CategoryIllegal   = "illegal"   // Non-violent Illegal Acts —— 赌、毒合并在此类，无法分开配置
	CategoryPolitical = "political" // Politically Sensitive Topics —— 政治
	CategoryJailbreak = "jailbreak" // Jailbreak —— 仅输入分类有效
	CategoryViolent   = "violent"   // Violent
	CategorySelfHarm  = "self_harm" // Suicide & Self-Harm
	CategoryUnethical = "unethical" // Unethical Acts
	CategoryPII       = "pii"       // Personally Identifiable Information
	CategoryCopyright = "copyright" // Copyright Violation —— 模型自承偏弱，建议不拦
	CategoryKeyword   = "keyword"   // L0 关键词命中，非模型类别
)

// AllCategories 供运营界面渲染类别处置表。
var AllCategories = []string{
	CategorySexual, CategoryIllegal, CategoryPolitical, CategoryJailbreak,
	CategoryViolent, CategorySelfHarm, CategoryUnethical, CategoryPII, CategoryCopyright,
}

// ModerationEndpoint 审核服务节点。第一期只有 L0（进程内），节点列表为空也能跑。
type ModerationEndpoint struct {
	Name       string `json:"name"`
	BaseURL    string `json:"base_url"`
	Model      string `json:"model"`
	Modality   string `json:"modality"` // text | image，零值按 text（第二期用）
	APIKey     string `json:"api_key"`  // 加密入库，读取走 GetAPIKey()
	TimeoutMS  int    `json:"timeout_ms"`
	InputLimit int    `json:"input_limit"` // 分段长度上限（rune），仅 text 有意义
	Enabled    bool   `json:"enabled"`
}

// GetAPIKey 解密入库凭证。
//
// 用 MODERATION_ENCRYPT_KEY 而不是 OBS 那套：common/obs_crypto.go:36 在密钥缺失时
// 会**生成随机密钥**，于是「加密成功」但服务一重启密文就永久不可读。
// moderation_crypto.go 的整个设计前提就是不接受这种静默失效——凭证配丢了，
// 表现是审核节点调用鉴权失败，排查时根本不会想到是加密密钥没配。
// 兼容明文历史值：无密文标记时原样返回。
func (e *ModerationEndpoint) GetAPIKey() string {
	if v := os.Getenv("MODERATION_API_KEY"); v != "" {
		return v
	}
	if e.APIKey == "" {
		return ""
	}
	plain, err := common.DecryptModerationContent(e.APIKey)
	if err == nil {
		return plain
	}
	if common.IsModerationCipher(e.APIKey) {
		// 带密文标记却解不开：密钥缺失或已变更。绝不能把密文当凭证发出去，
		// 那只会换来一堆看不懂的上游鉴权错误。
		common.SysError("moderation: 审核节点凭证解密失败（MODERATION_ENCRYPT_KEY 未设置或已变更），请重新保存: " + err.Error())
		return ""
	}
	return e.APIKey
}

// ModerationEndpointsOptionKey options 表里存 endpoints 的 key。
//
// config manager 把结构体拍扁成 "模块名.json tag"，切片字段整体存成一个 JSON 数组，
// 所以嵌套的 api_key 不会单独成键——它既不命中 controller/option.go 写入侧那组
// 按键名加密的 case，也不命中 GetOptions 里按后缀做的敏感字段过滤。
// 结果是凭证明文入库、明文出站。下面两个函数就是补这两个洞的。
const ModerationEndpointsOptionKey = "moderation.endpoints"

// EncryptModerationEndpoints 把 endpoints JSON 里每一条的 api_key 加密后返回新 JSON。
//
// 空 api_key 按「保持不变」处理，从当前已存配置里按 name 取回原密文——
// 否则前端拿到的是被抹掉的值（见 RedactModerationEndpoints），
// 原样提交回来就会把凭证清空。
func EncryptModerationEndpoints(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return raw, nil
	}
	var endpoints []ModerationEndpoint
	if err := common.UnmarshalJsonStr(raw, &endpoints); err != nil {
		return "", err
	}

	existing := make(map[string]string, len(moderationSettings.Endpoints))
	for _, e := range moderationSettings.Endpoints {
		existing[e.Name] = e.APIKey
	}

	for i := range endpoints {
		if endpoints[i].APIKey == "" {
			endpoints[i].APIKey = existing[endpoints[i].Name]
			continue
		}
		enc, err := common.EncryptModerationContent(endpoints[i].APIKey)
		if err != nil {
			// 密钥没配就直接拒绝保存，而不是用随机密钥「成功」一次。
			// 存进去的东西重启后解不开，运营看到的却是保存成功——
			// 这正是 common/obs_crypto.go:36 那条路的失效方式。
			if err == common.ErrModerationKeyMissing {
				return "", errors.New("未配置 MODERATION_ENCRYPT_KEY，无法安全保存审核节点凭证；请先配置该环境变量")
			}
			return "", err
		}
		endpoints[i].APIKey = enc
	}

	b, err := common.Marshal(endpoints)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// RedactModerationEndpoints 抹掉 endpoints JSON 里的 api_key，供 GET /api/option/ 回显。
//
// 不能像其它凭证那样整条 option 不返回：这个键里还装着 base_url / model / enabled
// 等运营界面必须渲染的字段。解析失败时返回空数组而不是原文——
// 宁可让配置页显示为空（可见故障），也不要把没看懂的内容原样吐出去。
func RedactModerationEndpoints(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return raw
	}
	var endpoints []ModerationEndpoint
	if err := common.UnmarshalJsonStr(raw, &endpoints); err != nil {
		common.SysError("moderation.endpoints 解析失败，已按空列表回显以免泄露凭证: " + err.Error())
		return "[]"
	}
	for i := range endpoints {
		endpoints[i].APIKey = ""
	}
	b, err := common.Marshal(endpoints)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// ModerationPolicy 命名策略。分组绑策略名，避免 N 个分组 × 9 个类别的配置矩阵（§8.3）。
type ModerationPolicy struct {
	Name       string            `json:"name"`       // 标准 / 宽松 / 严格 …
	Strictness string            `json:"strictness"` // loose | standard | strict
	Categories map[string]string `json:"categories"` // 类别 → block|log|ignore
}

// GroupPolicy 分组级配置。
type GroupPolicy struct {
	Mode   ModerationMode `json:"mode"`   // "" = 跟随全局
	Policy string         `json:"policy"` // 引用 ModerationPolicy.Name，空 = 用 DefaultPolicy
}

// ModelFilter 模型维度的生效范围（§8.6）。全局一份，不做每分组一份。
type ModelFilter struct {
	Mode   string   `json:"mode"`   // all | include | exclude
	Models []string `json:"models"` // 支持前缀通配，如 "text-embedding-*"
}

// Match 报告该模型是否在审核范围内。只支持前缀 * 通配，不引入正则——
// 正则写错不会报错，只会静默漏审，而漏审是这套系统最不能出的错（§8.6）。
func (f *ModelFilter) Match(modelName string) bool {
	switch f.Mode {
	case "include":
		return matchAnyPattern(f.Models, modelName)
	case "exclude":
		return !matchAnyPattern(f.Models, modelName)
	default: // all 或零值
		return true
	}
}

func matchAnyPattern(patterns []string, name string) bool {
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.HasSuffix(p, "*") {
			if strings.HasPrefix(name, strings.TrimSuffix(p, "*")) {
				return true
			}
			continue
		}
		if p == name {
			return true
		}
	}
	return false
}

type ModerationSettings struct {
	// Mode 全局模式。不参与继承，零值按 off 处理（默认不审，与 §8.5 灰度流程一致）。
	Mode ModerationMode `json:"mode"`

	Endpoints     []ModerationEndpoint   `json:"endpoints"`
	Policies      []ModerationPolicy     `json:"policies"`
	GroupPolicies map[string]GroupPolicy `json:"group_policies"`
	DefaultPolicy string                 `json:"default_policy"`
	ModelFilter   ModelFilter            `json:"model_filter"`

	// KeywordEnabled L0 关键词层开关。独立于 Mode：关键词拦截是现网既有行为，
	// 不能因为「内容审核默认 off」就被一起关掉（§14 P0 要求 L0 行为不变）。
	KeywordEnabled bool `json:"keyword_enabled"`

	// LogPassSampleRate Pass 记录的抽样比例（0~1）。Block/Review/error 恒全量落库。
	// observe 模式下全量 Pass 会在一周内把表撑到不可维护（§10）。
	LogPassSampleRate float64 `json:"log_pass_sample_rate"`

	// LogQueueSize 异步落库队列长度。满了丢日志而不是阻塞请求（§9.2）。
	LogQueueSize int `json:"log_queue_size"`

	// RetentionBlockDays / RetentionPassDays 分档保留期。
	//
	// Block 档取 180 天（六个月），对齐备案口径，不是工程侧拍的数。
	// 这个值只能往大调不能往小调：清理是物理删除，删过头没有第二份可恢复。
	RetentionBlockDays int `json:"retention_block_days"`
	RetentionPassDays  int `json:"retention_pass_days"`
}

var moderationSettings = ModerationSettings{
	Mode:               ModerationModeOff,
	KeywordEnabled:     true,
	DefaultPolicy:      "标准",
	ModelFilter:        ModelFilter{Mode: "all"},
	LogPassSampleRate:  0.01,
	LogQueueSize:       2048,
	RetentionBlockDays: 180,
	RetentionPassDays:  3,
	Policies: []ModerationPolicy{
		{
			Name:       "标准",
			Strictness: StrictnessStandard,
			Categories: map[string]string{
				CategorySexual:    CategoryActionBlock,
				CategoryIllegal:   CategoryActionBlock,
				CategoryPolitical: CategoryActionBlock,
				CategoryJailbreak: CategoryActionBlock,
				CategoryViolent:   CategoryActionLog,
				CategorySelfHarm:  CategoryActionLog,
				CategoryUnethical: CategoryActionLog,
				CategoryPII:       CategoryActionIgnore,
				CategoryCopyright: CategoryActionIgnore,
			},
		},
	},
}

func init() {
	config.GlobalConfig.Register("moderation", &moderationSettings)
}

// GetModerationSettings 返回全局单例（config manager 已按 DB 覆盖）。
func GetModerationSettings() *ModerationSettings {
	return &moderationSettings
}

// ResolveMode 解析分组的生效模式：分组零值跟随全局，全局零值按 off。
func (s *ModerationSettings) ResolveMode(group string) ModerationMode {
	if gp, ok := s.GroupPolicies[group]; ok && gp.Mode != ModerationModeInherit {
		return gp.Mode
	}
	if s.Mode == ModerationModeInherit {
		return ModerationModeOff
	}
	return s.Mode
}

// ResolvePolicy 解析分组生效的策略。找不到时回退 DefaultPolicy，再找不到回退第一条；
// 一条都没有则返回 nil —— 调用方按「无策略 = 只跑 L0」处理，不能因此放行。
func (s *ModerationSettings) ResolvePolicy(group string) *ModerationPolicy {
	name := s.DefaultPolicy
	if gp, ok := s.GroupPolicies[group]; ok && gp.Policy != "" {
		name = gp.Policy
	}
	for i := range s.Policies {
		if s.Policies[i].Name == name {
			return &s.Policies[i]
		}
	}
	if s.DefaultPolicy != "" && name != s.DefaultPolicy {
		for i := range s.Policies {
			if s.Policies[i].Name == s.DefaultPolicy {
				return &s.Policies[i]
			}
		}
	}
	if len(s.Policies) > 0 {
		return &s.Policies[0]
	}
	return nil
}

// CategoryAction 查类别处置。未登记的类别按 block 处理 ——
// 模型返回了我们没见过的类别时，宁可误拦一次也不能因为「配置里没写」就放行。
func (p *ModerationPolicy) CategoryAction(category string) string {
	if p == nil {
		return CategoryActionBlock
	}
	if a, ok := p.Categories[category]; ok && a != "" {
		return a
	}
	return CategoryActionBlock
}

// TextEndpoints 返回启用的文本审核节点（modality 零值按 text）。
func (s *ModerationSettings) TextEndpoints() []ModerationEndpoint {
	return s.endpointsByModality("text")
}

// ImageEndpoints 返回启用的图片审核节点（第二期用）。
func (s *ModerationSettings) ImageEndpoints() []ModerationEndpoint {
	return s.endpointsByModality("image")
}

func (s *ModerationSettings) endpointsByModality(modality string) []ModerationEndpoint {
	result := make([]ModerationEndpoint, 0, len(s.Endpoints))
	for _, e := range s.Endpoints {
		if !e.Enabled {
			continue
		}
		m := e.Modality
		if m == "" {
			m = "text"
		}
		if m == modality {
			result = append(result, e)
		}
	}
	return result
}

// ContentRetentionReady 报告原文加密留存是否可用。运营界面据此提示。
func (s *ModerationSettings) ContentRetentionReady() bool {
	return common.ModerationKeyReady()
}
