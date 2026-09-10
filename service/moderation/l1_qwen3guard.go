package moderation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

// L1：远程分类器层（Qwen3Guard-Gen）。见 docs/content-moderation-design.md §4.1、§6.3–§6.5。
//
// 与 L0 的分工：L0 全文扫关键词（进程内、微秒级），L1 做语义判定（GPU 调用、有长度上限）。
// 两者不能互相替代——模型防语义，词库和归一化防编码层面的花招。

// segmentConcurrency 单个请求内分段的并发上限。
//
// 不封上界的话，一个 50k 字符的输入会一次性打出十几个并发调用，把 §6.5 的全局
// 并发闸占满，后面的正常请求全被拒——单个请求占用的审核资源必须有界。
const segmentConcurrency = 4

// errSegmentBlocked 分段判 block 的哨兵。借 errgroup 的 cancel 机制提前终止其余段，
// 它表示「结论已定」而不是「调用失败」，Wait 之后要和真错误分开处理。
var errSegmentBlocked = errors.New("moderation: segment blocked")

// qwen3GuardModerator L1 判定器。节点列表来自 moderation.endpoints。
type qwen3GuardModerator struct {
	strictness string
	policy     *system_setting.ModerationPolicy
	// priority 最新一轮用户输入（已归一化，可空）。它会被单独先判一次，见 ModerateText。
	priority string
}

func (qwen3GuardModerator) Name() string { return "L1" }

// safetyLine / categoryLine 解析模型输出。
//
// Qwen3Guard-Gen 的官方 chat template 已经内置了判定 prompt（实测一次调用 prompt_tokens
// 约 308，而输入只有几个字），所以这里**不需要自己拼模板**，把待审文本作为 user message
// 直接发过去即可。输出形如：
//
//	Safety: Unsafe
//	Categories: Violent
//
// 大小写与空白都按模型实际输出容错，别指望它永远规整。
var (
	safetyLine   = "safety:"
	categoryLine = "categories:"
)

// categoryAliases 模型返回的类别名 → 本项目的类别常量（§4.1.1）。
//
// 官方类别是长名（"Non-violent Illegal Acts"），但实测返回的是短名（"Violent"），
// 两种都要认。认不出来的类别**不能丢弃**——那等于把未知风险当成安全，
// 所以统一落到 CategoryUnknownUpstream，由 CategoryAction 按「未登记即 block」处置。
var categoryAliases = map[string]string{
	"sexual content or sexual acts":       system_setting.CategorySexual,
	"sexual":                              system_setting.CategorySexual,
	"non-violent illegal acts":            system_setting.CategoryIllegal,
	"illegal":                             system_setting.CategoryIllegal,
	"politically sensitive topics":        system_setting.CategoryPolitical,
	"political":                           system_setting.CategoryPolitical,
	"jailbreak":                           system_setting.CategoryJailbreak,
	"violent":                             system_setting.CategoryViolent,
	"violence":                            system_setting.CategoryViolent,
	"suicide & self-harm":                 system_setting.CategorySelfHarm,
	"suicide and self-harm":               system_setting.CategorySelfHarm,
	"self-harm":                           system_setting.CategorySelfHarm,
	"unethical acts":                      system_setting.CategoryUnethical,
	"unethical":                           system_setting.CategoryUnethical,
	"personally identifiable information": system_setting.CategoryPII,
	"pii":                                 system_setting.CategoryPII,
	"copyright violation":                 system_setting.CategoryCopyright,
	"copyright":                           system_setting.CategoryCopyright,
}

// chatRequest / chatResponse 只声明用得上的字段。审核调用不走 relay 的 DTO：
// 那套结构为业务请求设计，字段多且会随上游演进，这里只需要最小子集。
type chatRequest struct {
	Model     string        `json:"model"`
	Messages  []chatMessage `json:"messages"`
	MaxTokens int           `json:"max_tokens"`
	// Temperature 固定 0：判定要可复现，同一段文本两次调用给出不同结论是没法排查的。
	Temperature float64 `json:"temperature"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
}

// ModerateText 对归一化后的文本做一次 L1 判定。
func (m qwen3GuardModerator) ModerateText(ctx context.Context, normalized string) (*Verdict, error) {
	endpoints := system_setting.GetModerationSettings().TextEndpoints()
	if len(endpoints) == 0 {
		// 开了 L1 却没有可用节点，是配置事故不是「无需审核」。交给 §6.4 的 fail 策略。
		return nil, errors.New("moderation: 未配置可用的文本审核节点")
	}

	// 分段长度取所有启用节点的最小 input_limit，保证任何一个节点都吃得下（§6.3 四）。
	limit := minInputLimit(endpoints)

	// L1 只审最新一轮用户输入，不扫历史与 tool_result（§6.3 二）。
	//
	// 这不是为省钱而缩小检测面，是分工：模型这层贵且有长度上限，把几十 KB 的文件
	// 内容、grep 输出全喂进去，一个 coding 请求就要切十几段、打十几次 GPU 调用，
	// 而那些内容不是用户此刻的意图表达。历史与 tool_result 的绕过由 L0 兜底——
	// 它是进程内 AC 自动机，扫全文是 O(n) 的微秒级操作，不设长度限制（§6.3 一、三）。
	//
	// 代价要说清楚：语义变形的违规内容若藏在伪造的历史里，L1 看不到，只有 L0 的
	// 关键词表能拦。sub2api 让 L1 全扫正是为了防这个，但它没有 L0 这层进程内兜底。
	//
	// 拿不到最新一轮时（任务提交等非对话链路）退回扫全文，与之前行为一致。
	scanTarget := normalized
	if m.priority != "" {
		scanTarget = m.priority
	}
	segments := splitByRunes(scanTarget, limit)

	// 总预算而不是段数封顶（§6.3 五）：段数封顶会制造「把违规内容推到第 N 段之后」
	// 这个可预测的盲区，超时封顶的后果则是整体 fail-close，攻击者构造超长输入
	// 只会让自己的请求被拒。
	budget := totalBudget(endpoints, len(segments))
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	// 分段必须并发，不能串行（§6.3 六）。串行下墙钟是各段之和，而所有段共享同一个
	// 总预算，50k 字符切十几段串行跑必然超时——超时又会被 §6.4 的 fail-close 兜成
	// 拒绝，于是长输入（coding 场景的常态）在拦截模式下被随机拒掉。
	// 文档把「串行 + 总预算」这个组合直接点名为「对本项目尤其致命」。
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(segmentConcurrency)

	var mu sync.Mutex
	worst := &Verdict{Action: ActionPass, Provider: "L1"}
	for _, seg := range segments {
		g.Go(func() error {
			v, err := m.moderateSegment(gctx, seg, endpoints)
			if err != nil {
				// 任一段失败即整体 fail-close，禁止部分放行（§6.4）：
				// 「审了一半没发现问题」≠「没问题」。
				return err
			}
			mu.Lock()
			defer mu.Unlock()
			if severity(v.Action) > severity(worst.Action) {
				worst = v
			}
			if v.Action == ActionBlock {
				// 判 block 可提前终止：返回哨兵错误让 errgroup cancel 掉其余段，
				// 省下没必要的 GPU 调用。它不是失败，在 Wait 之后单独识别。
				return errSegmentBlocked
			}
			return nil
		})
	}
	err := g.Wait()

	// 已经判出 block 就直接返回：拦截结论已经确定，其余段成功与否都不改变它。
	// 这一步必须排在错误检查之前，否则「一段判 block、另一段恰好超时」会被
	// 当成审核失败，把一个本该按违规拒绝的请求记成服务故障。
	if worst.Action == ActionBlock {
		return worst, nil
	}
	if err != nil && !errors.Is(err, errSegmentBlocked) {
		return nil, err
	}
	return worst, nil
}

// moderateSegment 对单段文本调用一次，按节点列表轮换。
func (m qwen3GuardModerator) moderateSegment(
	ctx context.Context,
	text string,
	endpoints []system_setting.ModerationEndpoint,
) (*Verdict, error) {
	if !acquireSlot() {
		// bulkhead 满了直接拒绝，不排队（§6.5 二）：审核在关键路径上，
		// 排队会把审核的拥塞传导成业务请求的延迟，且故障期延迟无界增长。
		return nil, errors.New("moderation: 审核并发已达上限")
	}
	defer releaseSlot()

	var lastErr error
	for i := range endpoints {
		ep := endpoints[i]
		if frozenUntil(ep.Name).After(time.Now()) {
			continue
		}
		content, status, err := callGuard(ctx, &ep, text)
		if err != nil {
			// 失败分三类，只有一类该归咎于节点：
			//
			//   1. 节点坏了（连不上、不响应、5xx、401）→ 冻结，把它摘出轮换
			//   2. 请求坏了（400，比如超长输入）      → 不冻结，换节点结果一样
			//   3. 我们放弃了（父 ctx 取消或预算耗尽）→ 不冻结，节点没有任何问题
			//
			// 第 3 类必须单独拎出来：父 ctx 一死，后面每个节点的调用都会立刻失败并
			// 带回 status=0，而 status=0 是要冻结的——于是一次超预算或被客户端中断的
			// 请求会把**所有**节点一起冻上，拦截模式下接下来整个冻结窗口内全站 503，
			// 且冻结失效后同一个慢请求再来一次就重新武装。
			//
			// 这也是为什么这里 break 而不是 continue：父 ctx 已死时轮换是空转。
			if ctx.Err() != nil {
				lastErr = err
				break
			}
			freezeEndpoint(ep.Name, freezeDurationForHTTPStatus(status))
			lastErr = err
			continue
		}
		clearFreeze(ep.Name)
		return m.parseVerdict(content), nil
	}
	if lastErr == nil {
		// 一个节点都没试成——列表非空却全被冻结，这是 §6.5 四要求「显式暴露」的
		// 降级态，不能和普通调用失败混为一谈。
		lastErr = errors.New("moderation: 所有审核节点均处于冻结状态")
	}
	return nil, lastErr
}

// parseVerdict 把模型输出映射成 Verdict。
//
// 判定分两层（§8.2 的两个正交旋钮）：Safety 答「有多严重」，Categories 答「什么类型」，
// 严重度先经 strictness 过滤，再由类别处置决定动作。
func (m qwen3GuardModerator) parseVerdict(content string) *Verdict {
	safety, cats := parseGuardOutput(content)

	v := &Verdict{Provider: "L1", Categories: cats}
	switch safety {
	case "safe":
		v.Action = ActionPass
		v.Categories = nil
		return v
	case "controversial":
		// Controversial 算不算违规由严格度决定（§8.2 第二个旋钮）。
		switch m.strictness {
		case system_setting.StrictnessLoose:
			v.Action = ActionPass
			v.Categories = nil
			return v
		case system_setting.StrictnessStrict:
			// 按 unsafe 处置，落到下面的类别映射
		default:
			// standard：不拦但留全量记录，供灰度期评估这一档的量级
			v.Action = ActionReview
			return v
		}
	case "unsafe":
		// 落到下面的类别映射
	default:
		// 模型输出不符合预期格式。「模型没说安全」≠「安全」（§6.4），判 error 交给 fail 策略。
		v.Action = ActionError
		v.Detail = "unrecognized safety level: " + safety
		return v
	}

	v.Action = m.actionForCategories(cats)
	return v
}

// actionForCategories 按策略把类别映射成动作。多个类别时取最严的那个。
func (m qwen3GuardModerator) actionForCategories(cats []string) Action {
	worst := ActionPass
	for _, c := range cats {
		switch m.policy.CategoryAction(c) {
		case system_setting.CategoryActionBlock:
			return ActionBlock
		case system_setting.CategoryActionLog:
			// 仅记录：放行但恒全量落库，这正是 ActionReview 的语义
			if severity(ActionReview) > severity(worst) {
				worst = ActionReview
			}
		}
		// ignore：不处理，连记录都不升级
	}
	if len(cats) == 0 {
		// 判成 unsafe 却没给类别。不能当安全放过去——CategoryAction 对 nil 策略
		// 和未登记类别都返回 block，这里保持一致。
		return ActionBlock
	}
	return worst
}

// callGuard 发起一次判定调用。返回模型输出、HTTP 状态码（用于分级冻结）、错误。
func callGuard(
	ctx context.Context,
	ep *system_setting.ModerationEndpoint,
	text string,
) (string, int, error) {
	return callGuardWithKey(ctx, ep, ep.GetAPIKey(), text)
}

// callGuardWithKey 与 callGuard 相同，但由调用方给出明文 key。
// 测试连接那条路拿到的 key 已经是明文，不能再过一次解密。
func callGuardWithKey(
	ctx context.Context,
	ep *system_setting.ModerationEndpoint,
	apiKey string,
	text string,
) (string, int, error) {
	timeout := time.Duration(ep.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// max_tokens 给 64 足够：实测判定输出只有 8–9 个 token（"Safety: Unsafe\nCategories: Violent"）。
	body, err := common.Marshal(chatRequest{
		Model:       ep.Model,
		Messages:    []chatMessage{{Role: "user", Content: text}},
		MaxTokens:   64,
		Temperature: 0,
	})
	if err != nil {
		return "", 0, err
	}

	url := strings.TrimRight(ep.BaseURL, "/") + "/v1/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := service.GetHttpClient().Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", resp.StatusCode, fmt.Errorf("moderation: 审核节点 %s 返回 %d", ep.Name, resp.StatusCode)
	}

	var parsed chatResponse
	if err := common.DecodeJson(resp.Body, &parsed); err != nil {
		return "", resp.StatusCode, err
	}
	if len(parsed.Choices) == 0 {
		return "", resp.StatusCode, errors.New("moderation: 审核节点返回空结果")
	}
	return parsed.Choices[0].Message.Content, resp.StatusCode, nil
}

// parseGuardOutput 从模型输出里提取安全等级与类别。
func parseGuardOutput(content string) (safety string, categories []string) {
	for _, line := range strings.Split(content, "\n") {
		lower := strings.ToLower(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(lower, safetyLine):
			safety = strings.TrimSpace(strings.TrimPrefix(lower, safetyLine))
		case strings.HasPrefix(lower, categoryLine):
			raw := strings.TrimSpace(strings.TrimPrefix(lower, categoryLine))
			if raw == "" || raw == "none" {
				continue
			}
			for _, c := range strings.Split(raw, ",") {
				c = strings.TrimSpace(c)
				if c == "" {
					continue
				}
				if mapped, ok := categoryAliases[c]; ok {
					categories = append(categories, mapped)
					continue
				}
				// 认不出来的类别不丢弃：丢了就等于把未知风险当安全放行。
				categories = append(categories, system_setting.CategoryUnknownUpstream)
			}
		}
	}
	return safety, categories
}

// minInputLimit 取所有启用节点的最小分段长度（§6.3 四）。
func minInputLimit(endpoints []system_setting.ModerationEndpoint) int {
	limit := 0
	for _, e := range endpoints {
		if e.InputLimit <= 0 {
			continue
		}
		if limit == 0 || e.InputLimit < limit {
			limit = e.InputLimit
		}
	}
	if limit <= 0 {
		// 节点没填时给一个对中文也打不穿 8192 窗口的保守值（§6.3 四）。
		limit = 4000
	}
	return limit
}

// segmentOverlap 相邻分段的重叠长度（rune）。
//
// 重叠是必要的（§6.3 五）：不重叠时，违规表述正好卡在切分点会被切成两段各自
// 无害的片段，而 input_limit 是可知的，攻击者把内容精确放到 k×limit 附近就能
// 稳定绕过。200 rune 足够覆盖一句完整表述，相对 24000 的 limit 只多出不到 1%
// 的调用量。
const segmentOverlap = 200

// splitByRunes 按 rune 切段，段间保留 segmentOverlap 的重叠。
// 用 rune 而不是 byte：按字节切会切出半个汉字，送进模型就是乱码，判定结果不可信。
func splitByRunes(s string, limit int) []string {
	runes := []rune(s)
	if len(runes) <= limit {
		return []string{s}
	}
	step := limit - segmentOverlap
	if step <= 0 {
		// limit 比重叠还小（运营把 input_limit 填得极小）。此时退回不重叠切分，
		// 否则 step<=0 会让循环永不前进。
		step = limit
	}
	out := make([]string, 0, (len(runes)+step-1)/step)
	for start := 0; start < len(runes); start += step {
		end := start + limit
		if end > len(runes) {
			end = len(runes)
		}
		out = append(out, string(runes[start:end]))
		if end == len(runes) {
			break
		}
	}
	return out
}

// totalBudget 所有分段共享的截止时间（§6.3 五）。
func totalBudget(endpoints []system_setting.ModerationEndpoint, segments int) time.Duration {
	per := 0
	for _, e := range endpoints {
		if e.TimeoutMS > per {
			per = e.TimeoutMS
		}
	}
	if per <= 0 {
		per = 3000
	}
	// 必须把节点数算进去：moderateSegment 是串行轮换节点的，每次尝试各花一份
	// ep.TimeoutMS。只按 segments 算的话，单段场景下预算恰好等于一个节点的超时——
	// 节点 A 挂起吃满超时后，轮换到 B 的调用是在一个已过期的 context 上发出的，
	// 秒失败。那样多节点轮换在「慢节点」这个最需要它的故障形态下完全不起作用。
	attempts := len(endpoints)
	if attempts <= 0 {
		attempts = 1
	}
	budget := time.Duration(per) * time.Millisecond * time.Duration(segments) * time.Duration(attempts)
	// 上界 10s：实测最长输入 P99 才 415ms，10s 已是极宽松的兜底；
	// 再长就该让请求 fail-close 掉，而不是把用户晾在那里。
	if budget > 10*time.Second {
		budget = 10 * time.Second
	}
	return budget
}

// ── 节点冻结状态（§6.5 一） ────────────────────────────────────────────────

const (
	authFreezeDuration      = 10 * time.Minute
	rateLimitFreezeDuration = time.Minute
	httpErrorFreezeDuration = 15 * time.Second
)

var (
	freezeMu    sync.RWMutex
	freezeUntil = map[string]time.Time{}
)

// freezeDurationForHTTPStatus 按状态码决定冻结时长。
//
// 400 不冻结是最容易漏的一条：请求本身的问题（比如超长输入）换个节点结果一样，
// 冻结只会误伤健康节点，把一个用户的畸形输入放大成全站可用节点减少。
func freezeDurationForHTTPStatus(status int) time.Duration {
	switch status {
	case http.StatusBadRequest:
		// 请求本身的问题（比如超长输入），换个节点结果一样，冻结只会误伤健康节点。
		return 0
	case 0:
		// 压根没拿到 HTTP 响应：连接被拒、DNS 失败、或读超时。这与 400 语义相反——
		// 400 说明「这个请求有问题」，0 说明「这个节点有问题」，而后者正是熔断存在的理由。
		//
		// 有意偏离 §6.5 一 给出的代码（那里把 0 和 400 并列返回 0）：把两者等同会让
		// 「TCP 连得上但不响应」这种最常见的故障形态永远摘不掉，每个请求都在它身上
		// 烧满超时，运行态页面上却始终看不到任何冻结节点。
		return httpErrorFreezeDuration
	case http.StatusUnauthorized, http.StatusForbidden:
		return authFreezeDuration
	case http.StatusTooManyRequests, 529:
		return rateLimitFreezeDuration
	default:
		return httpErrorFreezeDuration
	}
}

func freezeEndpoint(name string, d time.Duration) {
	if d <= 0 {
		return
	}
	freezeMu.Lock()
	freezeUntil[name] = time.Now().Add(d)
	freezeMu.Unlock()
}

func clearFreeze(name string) {
	freezeMu.Lock()
	delete(freezeUntil, name)
	freezeMu.Unlock()
}

func frozenUntil(name string) time.Time {
	freezeMu.RLock()
	t := freezeUntil[name]
	freezeMu.RUnlock()
	return t
}

// ClearAllFreezes 清空全部冻结状态。
//
// 保存节点配置时调用：熔断结论是基于旧配置得出的，配置一换它就作废。
// 不清的话会出现最难解释的那种状态——管理员改对了密钥、点测试连接是绿的，
// 生产流量却继续 fail-close，还要再熬满一个冻结窗口（401 那档是 10 分钟）。
func ClearAllFreezes() {
	freezeMu.Lock()
	freezeUntil = map[string]time.Time{}
	freezeMu.Unlock()
}

// ClearEndpointFreeze 清掉单个节点的冻结。测试连接成功后调用：
// 管理员刚亲手验证过它是活的，没有理由让它继续被熔断挡在轮换之外。
func ClearEndpointFreeze(name string) { clearFreeze(name) }

// FrozenEndpoints 供运行态展示：哪些节点正在冻结、到什么时候（§6.5 四「降级要显式」）。
func FrozenEndpoints() map[string]time.Time {
	freezeMu.RLock()
	defer freezeMu.RUnlock()
	out := make(map[string]time.Time, len(freezeUntil))
	now := time.Now()
	for k, v := range freezeUntil {
		if v.After(now) {
			out[k] = v
		}
	}
	return out
}

// ── bulkhead（§6.5 二） ────────────────────────────────────────────────────

// globalConcurrency 全局并发上限。满了直接拒绝而不是排队——排队会让故障期的
// 延迟无界增长，比直接拒绝更难诊断。
const globalConcurrency = 64

var slots = make(chan struct{}, globalConcurrency)

func acquireSlot() bool {
	select {
	case slots <- struct{}{}:
		return true
	default:
		return false
	}
}

func releaseSlot() {
	select {
	case <-slots:
	default:
	}
}

// ── 连通性测试（§8.4 P1） ──────────────────────────────────────────────────

// TestResult 一次节点连通性测试的结果。
type TestResult struct {
	Err      error
	Raw      string
	ParsedOK bool
}

// TestEndpoint 用一段固定的无害文本打一次真实判定，供管理端「测试连接」使用。
//
// 刻意不复用 ModerateText：那条路会走节点轮换和冻结状态，而测试要的恰恰是
// 「这一个节点此刻通不通」——被冻结的节点更需要能测，否则运营改完配置无法验证。
func TestEndpoint(ctx context.Context, baseURL, model, apiKey string, timeoutMS int) TestResult {
	ep := system_setting.ModerationEndpoint{
		Name:      "__test__",
		BaseURL:   baseURL,
		Model:     model,
		APIKey:    apiKey,
		TimeoutMS: timeoutMS,
	}
	// 走 testAPIKey 而不是 ep.GetAPIKey()：传进来的已经是明文，
	// 再解密一次会把它当密文处理然后失败。
	raw, _, err := callGuardWithKey(ctx, &ep, apiKey, "今天天气怎么样")
	if err != nil {
		return TestResult{Err: err}
	}
	safety, _ := parseGuardOutput(raw)
	return TestResult{
		Raw: strings.TrimSpace(raw),
		// 解析得出安全等级才算真通。返回 200 但输出格式不对，说明部署的不是 guard 模型
		// ——这种情况下审核链路会把每次判定都当成 ActionError，fail-close 下就是全站拒绝。
		ParsedOK: safety == "safe" || safety == "unsafe" || safety == "controversial",
	}
}

// ── 降级可见性（§6.5 四） ──────────────────────────────────────────────────

// failCloseCount 因审核未完成而拒绝请求的累计次数。
//
// §6.5 四要求「管理端要能一眼看出『现在是审核挂了在拒绝』而不是『用户都在违规』」。
// 没有这个计数，两者在管理端长得一模一样——都是请求被拒——只能靠翻进程日志区分，
// 而那正是审核最需要被快速判断的时刻。
var failCloseCount atomic.Int64

// RecordFailClose 记一次 fail-close 拒绝。
func RecordFailClose() { failCloseCount.Add(1) }

// FailCloseCount 读累计值。
func FailCloseCount() int64 { return failCloseCount.Load() }

// failOpenCount 因审核未完成而**放行**的累计次数（FailOpen 开启时）。
//
// 这个数比 failCloseCount 更需要被看见：fail-close 会被用户投诉推到台前，
// 而 fail-open 是彻底静默的——审核服务挂了一整天，业务毫无异常，
// 只有这个计数能说明「这段时间有多少请求其实没审」。
var failOpenCount atomic.Int64

// RecordFailOpen 记一次因审核不可用而放行。
func RecordFailOpen() { failOpenCount.Add(1) }

// FailOpenCount 读累计值。
func FailOpenCount() int64 { return failOpenCount.Load() }
