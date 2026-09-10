package system_setting

import (
	"errors"
	"fmt"
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
	// CategoryUnknownUpstream 模型给出了我们没登记的类别。
	//
	// 单独留一个常量而不是丢弃：丢了等于把未知风险当成安全放行。它不在 AllCategories 里
	// （运营界面不需要为它配处置），CategoryAction 对未登记类别返回 block，
	// 于是「模型报了个新类别」的默认行为是拦下来并留下记录，而不是静默通过。
	CategoryUnknownUpstream = "unknown_upstream"
)

// AllCategories 供运营界面渲染类别处置表。
var AllCategories = []string{
	CategorySexual, CategoryIllegal, CategoryPolitical, CategoryJailbreak,
	CategoryViolent, CategorySelfHarm, CategoryUnethical, CategoryPII, CategoryCopyright,
}

// ImageCoveredCategories 图片/视频判定实际能产出的类别。
//
// ShieldGemma 2 只有三条固定策略（色情 / 危险内容 / 暴力血腥），是训练时定死的，
// 加不了也改不了——改写策略文本不会报错，只会让判定悄悄失效（§4.5 有实测证据）。
//
// 单独导出是给配置页用的：九个类别的处置表对图片只有这三行真正生效。
// 不标出来的话，运营配了「政治 → 直接拒绝」会以为涉政图片能拦住，
// 而实际上图片侧对涉政**一点覆盖都没有**，连 L0 关键词那样的兜底都没有
// （AC 自动机扫不了图）。这种「以为配了其实没有」正是这套系统最不能出的错。
var ImageCoveredCategories = map[string]bool{
	CategorySexual:  true,
	CategoryIllegal: true, // ShieldGemma 的「危险内容」，注意它还混着自杀教程
	CategoryViolent: true,
}

// ValidateModerationPolicies 保存策略前的校验。
//
// 这是策略落库的唯一必经之路，而三种错配的后果都不是「这条策略不生效」：
//
//  1. 类别名写错 → CategoryAction 查不到，按「未登记即 block」处置，
//     于是一个本想放宽的类别反而变成了最严的那档；
//  2. 动作值写错 → 同样落到未登记分支，同上；
//  3. 严格度写错 → parseVerdict 的 switch 落到 default，Controversial 那一档
//     按 standard 处理，运营以为调了严格度其实没有。
//
// 三种都是**静默**的：保存成功、页面正常、判定悄悄按别的规则走。
func ValidateModerationPolicies(policies []ModerationPolicy) error {
	if len(policies) == 0 {
		return errors.New("至少要保留一条策略；一条都没有时分组会退回「只跑关键词层」")
	}
	valid := make(map[string]bool, len(AllCategories))
	for _, c := range AllCategories {
		valid[c] = true
	}
	seen := make(map[string]bool, len(policies))
	for i := range policies {
		p := &policies[i]
		name := strings.TrimSpace(p.Name)
		if name == "" {
			return fmt.Errorf("第 %d 条策略没有名称；分组是按名称绑定策略的", i+1)
		}
		if seen[name] {
			return fmt.Errorf("策略名称重复：%s；分组按名称查找，重名会让绑定指向哪一条变得不确定", name)
		}
		seen[name] = true

		switch p.Strictness {
		case StrictnessLoose, StrictnessStandard, StrictnessStrict, "":
		default:
			return fmt.Errorf("策略 %s 的严格度取值非法：%s（只能是 loose / standard / strict）", name, p.Strictness)
		}

		for cat, action := range p.Categories {
			if !valid[cat] {
				return fmt.Errorf("策略 %s 含未知类别：%s；未登记的类别会按「直接拒绝」处置，多半不是你想要的", name, cat)
			}
			switch action {
			case CategoryActionBlock, CategoryActionLog, CategoryActionIgnore:
			default:
				return fmt.Errorf("策略 %s 中类别 %s 的处置非法：%s（只能是 block / log / ignore）", name, cat, action)
			}
		}
	}
	return nil
}

// ValidateModerationPoliciesJSON 校验单独提交的策略列表。
//
// **只校验策略自身**，不做交叉引用检查。交叉引用（默认策略、分组绑定是否指向
// 存在的策略）必须由 ValidateModerationPolicyConfig 在**三者一起提交**时做——
// 分三次 PUT 逐个校验会死锁：改一条默认策略的名字时，先写 policies 会因为
// 旧的 default_policy 还指着旧名而被拒，先写 default_policy 又会因为新名
// 还不存在而被拒，两个方向都走不通。
func ValidateModerationPoliciesJSON(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return errors.New("策略配置不能为空")
	}
	var policies []ModerationPolicy
	if err := common.UnmarshalJsonStr(raw, &policies); err != nil {
		return fmt.Errorf("策略配置不是合法的 JSON：%w", err)
	}
	return ValidateModerationPolicies(policies)
}

// ValidateGroupPoliciesJSON 校验单独提交的分组绑定（只校验取值合法性）。
func ValidateGroupPoliciesJSON(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return nil // 空 = 所有分组跟随全局，是合法状态
	}
	var groups map[string]GroupPolicy
	if err := common.UnmarshalJsonStr(raw, &groups); err != nil {
		return fmt.Errorf("分组策略不是合法的 JSON：%w", err)
	}
	for group, gp := range groups {
		switch gp.Mode {
		case ModerationModeInherit, ModerationModeOff, ModerationModeObserve, ModerationModeBlocking:
		default:
			return fmt.Errorf("分组「%s」的运行模式取值非法：%s（只能是空 / off / observe / blocking）", group, gp.Mode)
		}
	}
	return nil
}

// ValidateModerationPolicyConfig 校验一次性提交的策略三件套。
//
// 三者互相引用，必须**放在同一次提交里对照同一份快照**校验：
//   - 默认策略与分组绑定都要指向 policies 里真实存在的一条；
//   - 找不到时 ResolvePolicy 会**静默**回退默认策略、再回退第一条，
//     某个分组的判定规则悄悄换了一套而界面上什么都看不出来。
//
// 之所以不能拆成三次校验：改名、「先解绑再删策略」这类最自然的组合操作，
// 在任何一种拆分顺序下都会被中间态卡住。
func ValidateModerationPolicyConfig(policies []ModerationPolicy, defaultPolicy string, groups map[string]GroupPolicy) error {
	if err := ValidateModerationPolicies(policies); err != nil {
		return err
	}

	names := make(map[string]bool, len(policies))
	for i := range policies {
		names[strings.TrimSpace(policies[i].Name)] = true
	}

	defaultPolicy = strings.TrimSpace(defaultPolicy)
	if defaultPolicy == "" {
		return errors.New("默认策略不能为空；没有默认策略时，未绑定的分组会静默落到列表里的第一条")
	}
	if !names[defaultPolicy] {
		return fmt.Errorf("默认策略「%s」不在策略列表里", defaultPolicy)
	}

	for group, gp := range groups {
		switch gp.Mode {
		case ModerationModeInherit, ModerationModeOff, ModerationModeObserve, ModerationModeBlocking:
		default:
			return fmt.Errorf("分组「%s」的运行模式取值非法：%s（只能是空 / off / observe / blocking）", group, gp.Mode)
		}
		if gp.Policy != "" && !names[gp.Policy] {
			return fmt.Errorf("分组「%s」绑定了不存在的策略「%s」；"+
				"绑定不存在的策略会让它静默回退到默认策略", group, gp.Policy)
		}
	}
	return nil
}

// ValidateDefaultPolicyName 校验默认策略名指向一条真实存在的策略。
func ValidateDefaultPolicyName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("默认策略不能为空；没有默认策略时，未绑定的分组会静默落到列表里的第一条")
	}
	for i := range GetModerationSettings().Policies {
		if GetModerationSettings().Policies[i].Name == name {
			return nil
		}
	}
	return fmt.Errorf("默认策略「%s」不存在", name)
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

	// HasAPIKey 该节点是否存有凭证。仅用于回显：RedactModerationEndpoints 在 GET 时
	// 按 APIKey 现算，EncryptModerationEndpoints 在保存时清零，因此它永不入库。
	//
	// 存在的理由：回显时 APIKey 一律被抹成空，前端因此分不清「这个节点没有密钥」和
	// 「有密钥但没给你看」。而凭证是按 name 回捞的，改名会让回捞落空、把密钥静默清空，
	// 前端要能拦住这一步，就必须知道这一行原本有没有密钥。
	HasAPIKey bool `json:"has_api_key,omitempty"`
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

// validateModerationEndpoints 保存前的字段校验。
//
// 校验必须放在这里而不是只做前端提示：这是配置落库的唯一必经之路，
// 而下面三条错配的后果都不是「这个节点不可用」，是**全站拒绝**——
// 拦截模式下节点调用失败会判 ActionError，再被 §6.4 的 fail-close 兜成拒绝。
//
//  1. 启用了却没填地址或模型名：endpointsByModality 只看 enabled，会照样把它选进
//     轮换列表，于是每次审核都必然失败。「点添加 → 直接保存」就能踩到。
//  2. 名字为空或重名：name 是这套配置事实上的主键——EncryptModerationEndpoints
//     按它回捞密钥、freezeUntil 按它记冻结。重名会让两个节点共用一条冻结记录，
//     甚至互相拿到对方的凭证。
func validateModerationEndpoints(endpoints []ModerationEndpoint) error {
	seen := make(map[string]bool, len(endpoints))
	for i := range endpoints {
		e := &endpoints[i]
		name := strings.TrimSpace(e.Name)
		if name == "" {
			return fmt.Errorf("第 %d 个审核节点没有填名称；名称是节点的标识，凭证保管与故障冻结都按它区分", i+1)
		}
		if seen[name] {
			return fmt.Errorf("审核节点名称重复：%s；重名会导致两个节点共用冻结状态、并可能取到对方的凭证", name)
		}
		seen[name] = true
		if !e.Enabled {
			// 停用的节点不参与调用，字段不全无所谓——运营常把配了一半的节点先停用留着。
			continue
		}
		if strings.TrimSpace(e.BaseURL) == "" || strings.TrimSpace(e.Model) == "" {
			return fmt.Errorf("审核节点 %s 已启用但地址或模型名为空；启用的节点会进入调用轮换，配不全会让每次审核都失败", name)
		}
	}
	return nil
}

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

	if err := validateModerationEndpoints(endpoints); err != nil {
		return "", err
	}

	existing := make(map[string]string, len(moderationSettings.Endpoints))
	for _, e := range moderationSettings.Endpoints {
		existing[e.Name] = e.APIKey
	}

	for i := range endpoints {
		// HasAPIKey 只是回显用的标记，每次 GET 由 RedactModerationEndpoints 按
		// APIKey 现算。绝不能让它入库：存下来的值会和事实不符——密钥轮换后
		// GetAPIKey() 返回空，而存着的标记仍宣称有密钥。
		endpoints[i].HasAPIKey = false
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
		endpoints[i].HasAPIKey = endpoints[i].APIKey != ""
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

	// OutputMode 产物侧的运行模式（第三期，§12.4）。零值 = off，升级后默认不开。
	//
	// 与 Mode 分开而不是共用一个：产物违规是**我们的模型生成的**，用户的 prompt
	// 可能完全无辜，两侧的容忍度和处置口径本来就不同（§12.4.5）。而且「输入拦、
	// 产物只观察」这类组合在灰度期几乎必然要用——共用一个开关就做不到。
	//
	// 注意**图片跟输入走**，不跟产物走：MediaActive 审的是用户**上传**的图，属于输入侧。
	OutputMode ModerationMode `json:"output_mode"`

	Endpoints     []ModerationEndpoint   `json:"endpoints"`
	Policies      []ModerationPolicy     `json:"policies"`
	GroupPolicies map[string]GroupPolicy `json:"group_policies"`
	DefaultPolicy string                 `json:"default_policy"`
	ModelFilter   ModelFilter            `json:"model_filter"`

	// KeywordEnabled L0 关键词层开关。独立于 Mode：关键词拦截是现网既有行为，
	// 不能因为「内容审核默认 off」就被一起关掉（§14 P0 要求 L0 行为不变）。
	KeywordEnabled bool `json:"keyword_enabled"`

	// FailOpen 审核服务不可用时是否放行（§15.8 的业务侧结论）。
	//
	// 默认 true —— 这是业务侧拍的板：GPUStack 升级、模型挂掉这类**我方运维事件**
	// 不该变成用户可见的全站拒绝。原设计是 fail-close（拒绝），理由是「拿短暂拒绝
	// 换不漏审这条合规底线」，但那条理由成立的前提是故障短暂且有人立刻处理，
	// 而计划内升级本身就会持续几分钟到几十分钟。
	//
	// **代价必须知情，两条**：
	//
	//  1. 文本侧还有 L0 关键词层兜底（它是进程内的，不受模型服务故障影响），
	//     所以最确定的那批违规词仍然拦得住；
	//  2. **图片/视频侧没有任何兜底** —— L0 是 AC 自动机，扫不了图。L2 一放开
	//     就是完全不设防，这段时间上传什么都进得去。
	//
	// 关掉它就回到 fail-close：审核服务挂掉时拦截模式下全站 503。
	FailOpen bool `json:"fail_open"`

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
	Mode: ModerationModeOff,
	// 显式写 off，不能靠零值。零值是 ModerationModeInherit（""），
	// 而 configToMap 会把它原样导出成 moderation.output_mode = ""，
	// 前端拿到空串既选不中「关闭」，也过不了「!== 'off'」这类判断。
	OutputMode:         ModerationModeOff,
	KeywordEnabled:     true,
	FailOpen:           true,
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

// ResolveOutputMode 解析分组的产物审核模式。
//
// 分组绑定里只有一个 Mode 字段，它管的是输入侧；产物侧目前只有全局开关。
// 不给分组加第二个 Mode 是有意的：产物审核刚上线、准召未验，先让它全局一致，
// 等真需要按分组区分再加——那时 GroupPolicy 加个字段即可，存量配置不受影响。
func (s *ModerationSettings) ResolveOutputMode(group string) ModerationMode {
	if s.OutputMode == ModerationModeInherit {
		return ModerationModeOff
	}
	return s.OutputMode
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
