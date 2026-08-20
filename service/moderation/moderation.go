package moderation

import (
	"context"
	"strings"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

// 内容审核。见 docs/content-moderation-design.md §6。

type Action string

const (
	ActionPass   Action = "pass"
	ActionBlock  Action = "block"
	ActionReview Action = "review" // 放行但入人工复核队列
	ActionMask   Action = "mask"   // 仅文本：替换命中词后放行
	ActionError  Action = "error"  // 审核未能完成，处置交给 §6.4 的 fail 策略
)

// Stage 审核阶段。
const (
	StagePrompt     = "prompt"
	StageInputMedia = "input_media" // 第二期
)

// Modality 模态。
const (
	ModalityText  = "text"
	ModalityImage = "image" // 第二期
)

// Verdict 单层审核的判定结果。
type Verdict struct {
	Action     Action
	Categories []string
	Score      float64 // Qwen3Guard-Gen 不提供分数，此字段对 L1 恒为 0（§4.1.1）
	Provider   string
	Detail     string // JSON，落库用；只含脱敏内容，不得带原文（§10）
	// Words L0 命中的关键词。只进 moderation_log 和管理端，
	// 绝不能回显给用户——那等于送一个免费的绕过探测器（§9.2.2）。
	Words []string
}

// Moderator 一层审核。第一期只有 L0（进程内关键词）。
type Moderator interface {
	Name() string
	ModerateText(ctx context.Context, normalized string) (*Verdict, error)
}

// Request 一次审核请求。字段既是审核输入，也是 moderation_log 的来源。
type Request struct {
	Texts []string // 待审文本（各平台字段不同，见 §7.A 的提取器）

	UserId    int
	TokenId   int
	ChannelId int
	Username  string
	Group     string
	ModelName string
	TaskId    string
	RequestId string

	Stage    string // 零值按 prompt
	Modality string // 零值按 text
}

// Result 审核的最终结果。
type Result struct {
	Action     Action
	Categories []string
	Provider   string
	// Reason 面向用户的中文原因，已按类别映射到可展示的措辞。
	// 只到类别，不含命中片段（§9.2.2）。
	Reason string
	// Blocked 是否应当拒绝。observe 模式下恒为 false，即使 Action 是 block。
	Blocked bool
}

// keywordActive L0 是否生效。
//
// 两个开关都要满足：
//   - KeywordEnabled 是本模块的开关；
//   - ShouldCheckPromptSensitive 是现网既有的「敏感词检查」总开关，运营早就在用它。
//     忽略它意味着一个已经关掉敏感词检查的站点，只要开了内容审核就会被重新拦，
//     那不是升级，是行为倒退。
func keywordActive(s *system_setting.ModerationSettings) bool {
	return s.KeywordEnabled && setting.ShouldCheckPromptSensitive()
}

// Active 报告当前配置下这次请求是否会真正进入审核链。
//
// 调用方据此决定要不要构造送审文本（CombineText 对大请求不便宜）。
// 必须由本包回答：调用方自己拼条件必然漏——上一版就是拿 legacy 的
// ShouldCheckPromptSensitive 当总闸，结果运营把 mode 调成 blocking 之后，
// 同步链路依然一条记录都不产生，而任务链路照跑，两条路悄悄给出不同结果。
func Active(group, modelName string) bool {
	s := system_setting.GetModerationSettings()
	if s.ModelFilter.Match(modelName) && s.ResolveMode(group) != system_setting.ModerationModeOff {
		return true
	}
	return keywordActive(s)
}

// Moderate 审核入口。返回值永不为 nil。
//
// 调用位置必须在预扣费之前——这不是约定，是「审核拒绝不扣款」的结构性保证（§9.1）。
func Moderate(ctx context.Context, req *Request) *Result {
	pass := &Result{Action: ActionPass}
	if req == nil || len(req.Texts) == 0 {
		return pass
	}

	s := system_setting.GetModerationSettings()
	mode := s.ResolveMode(req.Group)

	// 被 ModelFilter 排除的模型退回「只有 L0」的既有行为，而不是完全不审：
	// 排除 text-embedding-* 是为了省掉分类器调用，不是为了让它绕开关键词表（§8.6）。
	if !s.ModelFilter.Match(req.ModelName) {
		mode = system_setting.ModerationModeOff
	}
	l0 := keywordActive(s)
	if mode == system_setting.ModerationModeOff && !l0 {
		return pass
	}

	joined := strings.Join(req.Texts, "\n")
	normalized := Normalize(joined)
	if normalized == "" {
		return pass
	}

	verdict := runChain(ctx, normalized, mode, s, l0)
	if verdict == nil {
		return pass
	}

	result := &Result{
		Action:     verdict.Action,
		Categories: verdict.Categories,
		Provider:   verdict.Provider,
	}
	// observe 是决策产出后、返回错误前的唯一收口：决策照算、日志照写，就是不拒（§8.2）。
	// 注意这对 L0 同样生效——切到 observe 会让关键词也只记不拦。这是 observe 的定义
	// 决定的：不这样就量不出关键词表自身的误杀率，而那正是灰度期要回答的问题。
	result.Blocked = verdict.Action == ActionBlock && mode != system_setting.ModerationModeObserve
	if result.Blocked {
		result.Reason = ReasonText(verdict.Categories)
	}

	policy := s.ResolvePolicy(req.Group)

	// 落库要原文和归一化两份，不能只给归一化那份：
	// Normalize 会转小写、折叠空白、把同形字映射成拉丁字母，喂给检测器正合适，
	// 但存进 ContentEnc 就成了「取证材料是被我们改写过的版本」。
	// 页面上那一列写的是「原文」，就得真是原文。
	recordLog(req, verdict, joined, normalized, mode, policy, result.Blocked)
	return result
}

// runChain 按 L0→L3 顺序执行。
//
// 短路规则带模式条件（§6.1）：
//   - blocking 下任一层判 block 即短路，不再调用后续层——关键词表本来就是「确定要拦」
//     的名单，再花一次 GPU 调用复核既无意义又是纯粹的时延浪费。
//   - observe 下不短路，整条链跑完。否则关键词命中的那批内容永远拿不到 L1 判定，
//     而「关键词表误杀了多少」正是灰度期最该回答的问题，observe 就白做了。
func runChain(
	ctx context.Context,
	normalized string,
	mode system_setting.ModerationMode,
	s *system_setting.ModerationSettings,
	l0 bool,
) *Verdict {
	var worst *Verdict

	for _, m := range activeModerators(mode, s, l0) {
		v, err := m.ModerateText(ctx, normalized)
		if err != nil {
			// 审核未能完成不是「通过」。处置交给 §6.4 的 fail 策略，
			// 第一期只有进程内的 L0，走不到这里；L1 上线后这里要接 fail-close。
			v = &Verdict{Action: ActionError, Provider: m.Name(), Detail: err.Error()}
		}
		if v == nil {
			continue
		}
		if worst == nil || severity(v.Action) > severity(worst.Action) {
			worst = v
		}
		if mode == system_setting.ModerationModeBlocking && v.Action == ActionBlock {
			return v
		}
	}
	return worst
}

// activeModerators 按配置装配生效的层。
//
// 第一期只有 L0。L1 及以上要在这里按 mode != off 装配——GPU 调用有成本，关了就是关了。
func activeModerators(mode system_setting.ModerationMode, s *system_setting.ModerationSettings, l0 bool) []Moderator {
	var chain []Moderator
	if l0 {
		chain = append(chain, keywordModerator{})
	}
	return chain
}

func severity(a Action) int {
	switch a {
	case ActionBlock:
		return 4
	case ActionError:
		return 3
	case ActionReview:
		return 2
	case ActionMask:
		return 1
	default:
		return 0
	}
}

// recordLog 异步落一条审核记录。
//
// 这是 §1.0 那个盲区的补丁：在此之前关键词拦截不产生任何 DB 记录，
// 运营查「某用户说他发不出去」时一无所获。
func recordLog(
	req *Request,
	v *Verdict,
	raw string,
	normalized string,
	mode system_setting.ModerationMode,
	policy *system_setting.ModerationPolicy,
	enforced bool,
) {
	if v.Action == ActionPass {
		// mode=off 时 L0 仍然跑（关键词拦截是现网既有行为，不该被「审核默认关闭」带走），
		// 但既然没有任何灰度在观测，pass 抽样就只是噪音：从没开过审核的部署也会持续攒记录。
		// 真正的 L0 拦截照记不误——补 §1.0 那个盲区本来就与 mode 无关。
		if mode == system_setting.ModerationModeOff {
			return
		}
		if !model.ShouldSampleModerationPass() {
			return
		}
	}

	stage := req.Stage
	if stage == "" {
		stage = StagePrompt
	}
	modality := req.Modality
	if modality == "" {
		modality = ModalityText
	}
	policyName := ""
	if policy != nil {
		policyName = policy.Name
	}

	entry := &model.ModerationLog{
		UserId:     req.UserId,
		TokenId:    req.TokenId,
		ChannelId:  req.ChannelId,
		Username:   req.Username,
		Group:      req.Group,
		Policy:     policyName,
		TaskId:     req.TaskId,
		RequestId:  req.RequestId,
		ModelName:  req.ModelName,
		Source:     model.ModerationSourceSelf,
		Stage:      stage,
		Modality:   modality,
		Action:     string(v.Action),
		Enforced:   enforced,
		Categories: strings.Join(v.Categories, ","),
		Words:      truncateWords(v.Words),
		Score:      v.Score,
		Provider:   v.Provider,
		Detail:     buildDetail(v, mode),
	}
	// 必须在 Action 与 Enforced 都已赋值之后调用：
	// 全文留存看的是「真拦下来了吗」，不是「判成 block 了吗」（§10.1）。
	entry.SetModerationContent(raw, normalized)
	model.RecordModerationLog(entry)
}
