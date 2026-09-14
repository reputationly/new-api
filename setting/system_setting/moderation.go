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
//
// 注意它**只对提供中间档的 dialect 有意义**。qwen3guard 有 Controversial 这一档，
// zhongsen-text 是二分的（sec vs 28 个风险码），严格度对后者完全无效——
// 松紧只能靠类别处置表调。配置页必须把这件事写出来，否则它会被当成一个全局旋钮。
const (
	StrictnessLoose    = "loose"
	StrictnessStandard = "standard"
	StrictnessStrict   = "strict"
)

// 判定协议（dialect）。**审核节点上部署的是哪个模型，决定了请求怎么发、输出怎么解析。**
//
// 做成节点的一个属性而不是全局开关：节点就是部署实体，协议是它的固有属性，
// 不引入第二个真相来源。切换模型 = 改这个下拉框 / 切 enabled，不需要改代码。
const (
	DialectQwen3Guard   = "qwen3guard"      // 文本：Qwen3Guard-Gen，输出 "Safety: X\nCategories: Y"
	DialectZhongsenText = "zhongsen-text"   // 文本：Zhongsen-Text-8b，输出首行缩写码 + <explanation>
	DialectShieldGemma2 = "shieldgemma2"    // 图片：ShieldGemma 2，逐策略问 Yes/No 取 logprobs
	DialectZSWS         = "zsws-multimodal" // 图片：ZSWS-Multimodal-4b，单次调用返回 <answer>大类</answer>
)

// TextDialects / ImageDialects **可选**的 dialect，按模态分。
//
// 分模态登记而不是一张总表：文本节点配成 shieldgemma2 是配置事故，
// 而「保存成功但每次判定都失败」是这套系统最不该有的失效方式（见 validateModerationEndpoints）。
//
// **这两个列表的语义是「已经有解析实现、可以安全选中」，不是「我们认识这个名字」。**
// DialectZSWS 故意不在 ImageDialects 里：常量和覆盖表都已就位，但图片侧还没有
// dialect 分派（service/moderation/media.go 写死 shieldGemmaModerator），
// 一旦放进来就会出现：
//
//	图片节点选 zsws-multimodal → 保存通过 → 给 ZSWS 发 ShieldGemma 的三条策略
//	+ logprobs 请求 → 每次判定都 ActionError → FailOpen 默认 true
//	→ 图片审核静默停摆，而「测试连接」走的还是 ShieldGemma 协议，照样报绿
//
// 界面上标一句「暂未实现」不是护栏——能被点到的选项就会被点。
// 第二步补上 imageDialect 分派时，把它加回这里，并同步 l2 那侧的实现检查测试。
var (
	TextDialects  = []string{DialectQwen3Guard, DialectZhongsenText}
	ImageDialects = []string{DialectShieldGemma2}
)

// DefaultDialectForModality 该模态的默认 dialect。
//
// 存量配置里没有 dialect 字段，零值必须回落到接这个字段之前实际在跑的那个模型，
// 否则一次升级就会把所有存量节点的判定协议换掉。
func DefaultDialectForModality(modality string) string {
	if modality == ModalityImage {
		return DialectShieldGemma2
	}
	return DialectQwen3Guard
}

// 模态取值。与 service/moderation 的 Modality* 一致；放在这里是因为
// endpointsByModality 与 dialect 校验都要用，而 system_setting 不能反向依赖 service。
const (
	ModalityText  = "text"
	ModalityImage = "image"
)

// 风险类别（§4.1.1）。**类别集合是所有 dialect 的并集**，不是某一个模型的类别表。
//
// 前九类来自 Qwen3Guard；后五类是接 Zhongsen-Text-8b 时补的——它的 28 个风险码压不进
// 前九类，硬压会造成两类静默错判：恶意代码/黑客攻击落进 illegal 会连带拦掉正常的技术
// 咨询，而暴恐、虐待未成年人这两条红线落进 violent/unethical 只会被记录不会被拦。
//
// 单个 dialect 通常只覆盖其中一部分（textDialect.CoveredCategories 报告覆盖范围），
// 类别粒度仍由模型定，我们只能映射不能细分。
const (
	CategorySexual    = "sexual"    // Sexual Content or Sexual Acts —— 黄
	CategoryIllegal   = "illegal"   // Non-violent Illegal Acts —— 赌、毒合并在此类，无法分开配置
	CategoryPolitical = "political" // Politically Sensitive Topics —— 政治
	CategoryJailbreak = "jailbreak" // Jailbreak —— 仅输入分类有效；Zhongsen 无此类别
	CategoryViolent   = "violent"   // Violent
	CategorySelfHarm  = "self_harm" // Suicide & Self-Harm
	CategoryUnethical = "unethical" // Unethical Acts
	CategoryPII       = "pii"       // Personally Identifiable Information
	CategoryCopyright = "copyright" // Copyright Violation —— 模型自承偏弱，建议不拦；Zhongsen 无此类别
	// CategoryCyber 网络安全。Zhongsen 的 acc / mc / ha / ps 四码。
	//
	// 必须独立于 illegal：默认处置是 log 而非 block，因为「帮我看下这段渗透测试脚本」
	// 「这个 SQL 注入怎么修」是 coding 场景的常态提问，落进 illegal（默认 block）
	// 会造成大面积误杀。
	CategoryCyber = "cyber"
	// CategoryAdvice 违规建议。Zhongsen 的 fin / med / law 三码。
	//
	// 默认 ignore：这一类的边界极模糊（「帮我分析这只股票」算不算违规提供投资建议），
	// 而它命中的绝大多数是正常咨询。想拦的站点自己调成 log 或 block。
	CategoryAdvice = "advice"
	// CategoryMinor 未成年人保护。Zhongsen 的 cm / ma / md 三码。
	//
	// 默认 block：ma（教唆虐待、剥削未成年人）是高危红线，压进 unethical
	// （默认 log）等于只记录不拦。
	CategoryMinor = "minor"
	// CategoryTerror 暴恐极端。Zhongsen 的 ter 码。
	//
	// 默认 block：备案口径下暴恐是一票否决，而普通暴力内容（violent）默认只 log，
	// 两者合并会让红线跟着降级。
	CategoryTerror = "terror"
	// CategoryVulgar 低俗擦边。ZSWS 图片判定的 `4. sexual` 大类。
	//
	// 与 sexual 分开：ZSWS 把「直接的色情内容」（3. pornographic）和「大面积暴露、
	// 二次元擦边」（4. sexual）判成两个大类，后者厂商建议的处置就是「弹性阻断」。
	// 合并会让擦边内容跟着色情一起被硬拦，对图像生成平台是高频误杀源。
	CategoryVulgar  = "vulgar"
	CategoryKeyword = "keyword" // L0 关键词命中，非模型类别
	// CategoryUnknownUpstream 模型给出了我们没登记的类别。
	//
	// 单独留一个常量而不是丢弃：丢了等于把未知风险当成安全放行。它不在 AllCategories 里
	// （运营界面不需要为它配处置），也**故意不进 defaultCategoryActions**，
	// 于是 CategoryAction 对它返回 block——「模型报了个新类别」的默认行为是拦下来
	// 并留下记录，而不是静默通过。
	CategoryUnknownUpstream = "unknown_upstream"
)

// AllCategories 供运营界面渲染类别处置表。
var AllCategories = []string{
	CategorySexual, CategoryIllegal, CategoryPolitical, CategoryJailbreak,
	CategoryViolent, CategorySelfHarm, CategoryUnethical, CategoryPII, CategoryCopyright,
	CategoryCyber, CategoryAdvice, CategoryMinor, CategoryTerror, CategoryVulgar,
}

// defaultCategoryActions 每个类别在策略里没被显式配置时的处置。
//
// 存在的理由是**新增类别不能悄悄变成 block**。CategoryAction 原先对任何查不到的类别
// 都返回 block，那条规则对「模型报了个没见过的类别」是对的，但对「我们自己新加了类别、
// 而存量策略还是老的九项」就是灾难：升级当天 cyber 和 advice 会开始拦技术咨询和投资
// 提问，而运营界面上那两行看起来根本没配过。
//
// 有了这张表就不需要写数据迁移——存量策略原样留着，缺的类别按这里的值走。
// 注意 CategoryUnknownUpstream 故意不在表里，它必须保持「未登记即 block」。
var defaultCategoryActions = map[string]string{
	CategorySexual:    CategoryActionBlock,
	CategoryIllegal:   CategoryActionBlock,
	CategoryPolitical: CategoryActionBlock,
	CategoryJailbreak: CategoryActionBlock,
	CategoryViolent:   CategoryActionLog,
	CategorySelfHarm:  CategoryActionLog,
	CategoryUnethical: CategoryActionLog,
	CategoryPII:       CategoryActionIgnore,
	CategoryCopyright: CategoryActionIgnore,
	CategoryCyber:     CategoryActionLog,
	CategoryAdvice:    CategoryActionIgnore,
	CategoryMinor:     CategoryActionBlock,
	CategoryTerror:    CategoryActionBlock,
	CategoryVulgar:    CategoryActionLog,
}

// DefaultCategoryAction 单个类别的默认处置，供配置页渲染「未配置」时的实际行为。
// 查不到时返回 block，与 CategoryAction 的兜底保持一致。
func DefaultCategoryAction(category string) string {
	if a, ok := defaultCategoryActions[category]; ok {
		return a
	}
	return CategoryActionBlock
}

// dialectCoveredCategories 每个 dialect 实际能产出的类别。
//
// 这张表是给配置页用的：类别处置表有 14 行，而**任何一个 dialect 都只覆盖其中一部分**。
// 不标出来的话，运营配了「政治 → 直接拒绝」会以为涉政图片能拦住，而 ShieldGemma 对
// 涉政一点覆盖都没有，连 L0 关键词那样的兜底都没有（AC 自动机扫不了图）。
// 这种「以为配了其实没有」正是这套系统最不能出的错。
//
// 各 dialect 的覆盖边界都是模型定的，我们只能如实标注：
//
//   - qwen3guard：官方九类，与 Qwen3Guard-Gen 的类别表一一对应。
//   - zhongsen-text：28 个风险码收敛到 11 类。**没有 jailbreak 和 copyright**
//     （码表里没有对应项——换到这个 dialect 会失去越狱检测能力），
//     也没有 vulgar（那是图片侧的类别）。
//   - shieldgemma2：三条固定策略（色情 / 危险内容 / 暴力血腥），训练时定死，
//     加不了也改不了——改写策略文本不会报错，只会让判定悄悄失效（§4.5 有实测证据）。
//   - zsws-multimodal：六个大类，比 ShieldGemma 多覆盖了涉政与违禁品，
//     并把色情与低俗擦边拆成 sexual / vulgar 两类。
//
// 前端 MODERATION_DIALECT_COVERED 是这张表的镜像，改这里必须同步改那里，
// 否则界面上的覆盖标注会和实际判定能力对不上。
var dialectCoveredCategories = map[string]map[string]bool{
	DialectQwen3Guard: {
		CategorySexual: true, CategoryIllegal: true, CategoryPolitical: true,
		CategoryJailbreak: true, CategoryViolent: true, CategorySelfHarm: true,
		CategoryUnethical: true, CategoryPII: true, CategoryCopyright: true,
	},
	DialectZhongsenText: {
		CategorySexual: true, CategoryIllegal: true, CategoryPolitical: true,
		CategoryViolent: true, CategorySelfHarm: true, CategoryUnethical: true,
		CategoryPII: true, CategoryCyber: true, CategoryAdvice: true,
		CategoryMinor: true, CategoryTerror: true,
	},
	DialectShieldGemma2: {
		CategorySexual:  true,
		CategoryIllegal: true, // ShieldGemma 的「危险内容」，注意它还混着自杀教程
		CategoryViolent: true,
	},
	DialectZSWS: {
		CategoryPolitical: true, CategoryViolent: true, CategorySexual: true,
		CategoryVulgar: true, CategoryIllegal: true,
	},
}

// DialectCoveredCategories 报告某个 dialect 能产出哪些类别。
// 未知 dialect 返回 nil —— 调用方（配置页）据此不做覆盖标注，而不是谎称全覆盖。
func DialectCoveredCategories(dialect string) map[string]bool {
	return dialectCoveredCategories[dialect]
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
	Name      string `json:"name"`
	BaseURL   string `json:"base_url"`
	Model     string `json:"model"`
	Modality  string `json:"modality"` // text | image，零值按 text（第二期用）
	APIKey    string `json:"api_key"`  // 加密入库，读取走 GetAPIKey()
	TimeoutMS int    `json:"timeout_ms"`
	// Dialect 该节点部署的模型说哪种判定协议。零值按模态回落（见 ResolveDialect）。
	//
	// Model 字段是**给上游的模型名**（要和 GPUStack 里注册的名字逐字一致），
	// 两者不能合并：同一个模型在不同部署里可以叫不同名字，而协议是由权重决定的。
	// 拿模型名去猜协议就是在用一个运营可以随手改的字符串决定「输出怎么解析」，
	// 而解析错的后果是每次判定都变成 ActionError，fail-open 下静默全量放行。
	Dialect    string `json:"dialect"`
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

// ResolveModality 该节点的模态，零值按 text。
func (e *ModerationEndpoint) ResolveModality() string {
	if e.Modality == "" {
		return ModalityText
	}
	return e.Modality
}

// ResolveDialect 该节点的判定协议，零值按模态回落。
//
// 回落值必须是接 dialect 字段之前那个模态实际在跑的模型（text→qwen3guard、
// image→shieldgemma2），否则升级会静默改掉所有存量节点的解析方式。
func (e *ModerationEndpoint) ResolveDialect() string {
	if e.Dialect != "" {
		return e.Dialect
	}
	return DefaultDialectForModality(e.ResolveModality())
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
//  3. dialect 与模态不匹配（文本节点配了 shieldgemma2）：请求形状和解析方式都会错，
//     每次判定都失败。
//  4. 同模态下启用的节点用了不同 dialect：见下面 activeDialect 的说明。
func validateModerationEndpoints(endpoints []ModerationEndpoint) error {
	seen := make(map[string]bool, len(endpoints))
	// 同模态下已启用节点的 dialect，用于下面的唯一性检查。
	activeDialect := make(map[string]string, 2)
	dialectOwner := make(map[string]string, 2)
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

		modality := e.ResolveModality()
		dialect := e.ResolveDialect()
		allowed := TextDialects
		if modality == ModalityImage {
			allowed = ImageDialects
		}
		if !containsString(allowed, dialect) {
			return fmt.Errorf("审核节点 %s 的判定协议「%s」不适用于%s节点；可选：%s",
				name, dialect, modalityLabel(modality), strings.Join(allowed, " / "))
		}

		if !e.Enabled {
			// 停用的节点不参与调用，字段不全无所谓——运营常把配了一半的节点先停用留着。
			// dialect 唯一性也只看启用的：**切换模型的正常操作就是「新节点先配好停用着、
			// 旧节点停用、新节点启用」**，如果停用的也参与检查，这条路就走不通了。
			continue
		}
		if strings.TrimSpace(e.BaseURL) == "" || strings.TrimSpace(e.Model) == "" {
			return fmt.Errorf("审核节点 %s 已启用但地址或模型名为空；启用的节点会进入调用轮换，配不全会让每次审核都失败", name)
		}

		// 同模态下启用的节点必须使用同一个 dialect。
		//
		// 混用不是「更灵活」，是**判定不可复现**：节点轮换（moderateSegment 逐个试）
		// 决定了同一段文本这次可能由 qwen3guard 判、下次由 zhongsen 判，而两者的严格度
		// 语义根本不同（zhongsen 没有 Controversial 这一档）。于是同一个输入会随机
		// 得到不同结论，申诉和误杀率统计都无从下手。
		//
		// 想比两个模型的准召，要的是「双跑 + 记录两份」，那是另一个特性，
		// 不是让生产流量随机落到其中一个上。
		if prev, ok := activeDialect[modality]; ok && prev != dialect {
			return fmt.Errorf(
				"%s节点「%s」用的判定协议是「%s」，而「%s」用的是「%s」；"+
					"同一模态下启用的节点必须使用同一协议，否则同一份输入会因为落到不同节点而得到不同判定。"+
					"切换模型请先停用旧协议的节点，再启用新协议的节点",
				modalityLabel(modality), name, dialect, dialectOwner[modality], prev)
		}
		activeDialect[modality] = dialect
		dialectOwner[modality] = name
	}
	return nil
}

func containsString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func modalityLabel(modality string) string {
	if modality == ModalityImage {
		return "图片"
	}
	return "文本"
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
			// 与 defaultCategoryActions 逐条一致。写全而不是留空靠兜底：
			// 新装站点的配置页要能看见每一类当前是什么处置，留空会显示成未配置。
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
				CategoryCyber:     CategoryActionLog,
				CategoryAdvice:    CategoryActionIgnore,
				CategoryMinor:     CategoryActionBlock,
				CategoryTerror:    CategoryActionBlock,
				CategoryVulgar:    CategoryActionLog,
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

// CategoryAction 查类别处置。
//
// 三级查找，顺序有讲究：
//
//  1. 策略里显式配了 —— 用它，运营的配置永远优先；
//  2. 策略里没配但这是个**我们登记过**的类别 —— 用 defaultCategoryActions。
//     新增类别时存量策略必然走到这里，而让它们默认 block 就是升级当天开始误杀
//     （cyber 会拦技术咨询、advice 会拦投资提问），且界面上那几行看起来根本没配过；
//  3. 连登记都没有（CategoryUnknownUpstream、或上游报了个新类别）—— block。
//     模型返回了我们没见过的类别时，宁可误拦一次也不能因为「配置里没写」就放行。
func (p *ModerationPolicy) CategoryAction(category string) string {
	if p == nil {
		return CategoryActionBlock
	}
	if a, ok := p.Categories[category]; ok && a != "" {
		return a
	}
	return DefaultCategoryAction(category)
}

// TextEndpoints 返回启用的文本审核节点（modality 零值按 text）。
func (s *ModerationSettings) TextEndpoints() []ModerationEndpoint {
	return s.endpointsByModality(ModalityText)
}

// ImageEndpoints 返回启用的图片审核节点（第二期用）。
func (s *ModerationSettings) ImageEndpoints() []ModerationEndpoint {
	return s.endpointsByModality(ModalityImage)
}

func (s *ModerationSettings) endpointsByModality(modality string) []ModerationEndpoint {
	result := make([]ModerationEndpoint, 0, len(s.Endpoints))
	for _, e := range s.Endpoints {
		if !e.Enabled {
			continue
		}
		if e.ResolveModality() == modality {
			result = append(result, e)
		}
	}
	return result
}

// TextDialect / ImageDialect 当前生效的判定协议，没有启用节点时返回空串。
//
// 取第一个启用节点的 dialect 就够：validateModerationEndpoints 保证了同模态下启用
// 节点的 dialect 唯一。这里不重复做一致性检查——**校验只在保存这一条路上做**，
// 判定路径每个请求都跑，在热路径上重算一遍一致性只是白花 CPU，
// 而真要出现不一致，那说明 options 表被手改过，那种情况下报错也无处可报。
func (s *ModerationSettings) TextDialect() string {
	return s.dialectByModality(ModalityText)
}

func (s *ModerationSettings) ImageDialect() string {
	return s.dialectByModality(ModalityImage)
}

func (s *ModerationSettings) dialectByModality(modality string) string {
	for _, e := range s.Endpoints {
		if e.Enabled && e.ResolveModality() == modality {
			return e.ResolveDialect()
		}
	}
	return ""
}

// ContentRetentionReady 报告原文加密留存是否可用。运营界面据此提示。
func (s *ModerationSettings) ContentRetentionReady() bool {
	return common.ModerationKeyReady()
}
