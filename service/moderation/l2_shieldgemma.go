package moderation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

// L2：视觉审核层（ShieldGemma 2）。见 docs/content-moderation-design.md §4.5、§12。
//
// 与文本链最大的结构性差别：**图片这条路没有 L0 兜底**。L0 是进程内的 AC 自动机，
// 只能扫文本，扫不了图。文本侧 L1 挂了还有关键词表拦住最确定的那批，图片侧 L2 挂了
// 就是完全不设防——所以这一层的 fail-close 比 L1 更要紧，也更不能有「解析不出来就当
// 通过」的路径（§4.5 代价一）。

// shieldGemmaPolicy 一条安全策略。
//
// Text 必须是官方原文，不能改写、不能翻译成中文。实测证据：拿一张纯红图，
// 配「The image shall not contain any red pixels」这条自造策略，模型照样回答 No
// （P(Yes) 仅从 -7.6 抬到 -5.0，远不足以翻转结论）。
//
// 原因是它不是通用 VLM——ShieldGemma 2 是在这三条策略上微调出来的分类器，策略文本
// 更像一把「选哪个分类头」的钥匙，而不是真的被阅读的规则。改写文本不会报错，
// 只会让判定悄悄退化成对不上号的分类，是这一层最隐蔽的失效方式。
type shieldGemmaPolicy struct {
	Key      string
	Text     string
	Category string
}

// shieldGemmaPolicies 官方三条策略（§4.5）。
//
// 覆盖不到的类别在这里没有对应项，也补不出来：涉政、隐私、版权等图片本期完全不覆盖。
// 别试图靠加一条自造策略来扩类别——见 shieldGemmaPolicy.Text 的说明。
var shieldGemmaPolicies = []shieldGemmaPolicy{
	{
		Key:      "sexual",
		Category: system_setting.CategorySexual,
		Text:     `"No Sexually Explicit content": The image shall not contain content that depicts explicit or graphic sexual acts (e.g., pornography, erotic nudity, depictions of rape or sexual assault).`,
	},
	{
		Key: "dangerous",
		// 官方 Dangerous Content 的例子里同时含「制枪制爆、宣扬恐怖主义」和「自杀教程」，
		// 跨了我方的 illegal 与 self_harm 两类。映射到 illegal 是因为前者是这条策略的主体，
		// 但这意味着**一张自杀相关的图会被记成 illegal**——按类别配处置时要知道这个偏差，
		// 想单独放宽自伤类图片是做不到的（§4.5「三类判定与九类的映射边界」）。
		Category: system_setting.CategoryIllegal,
		Text:     `"No Dangerous Content": The image shall not contain content that facilitates or encourages activities that could cause real-world harm (e.g., building firearms and explosive devices, promotion of terrorism, instructions for suicide).`,
	},
	{
		Key:      "violence",
		Category: system_setting.CategoryViolent,
		Text:     `"No Violence/Gore": The image shall not contain content that depicts shocking, sensational, or gratuitous violence (e.g., excessive blood and gore, gratuitous violence against animals, extreme injury or moment of death).`,
	},
}

// yesThreshold 判定为违规的 P(Yes) 阈值，按严格度取。
//
// 模型自身的切点是 argmax，即 P(Yes) > 0.5 才输出 "Yes"。standard 与之对齐；
// strict / loose 是在**不重新部署**的前提下移动切点——这正是 §8.2 第二个旋钮对 L2
// 的落地方式。文本侧靠 Controversial 这一档实现严格度，图片侧没有中间档，只能靠阈值。
//
// 这三个数是拍的，必须用标注样本实测后再定（§15.9）。它们的方向是确定的：
// 阈值越低召回越高、误杀越多；艺术与医学裸体是误杀高发区，收紧前先看这一类的分布。
func yesThreshold(strictness string) float64 {
	switch strictness {
	case system_setting.StrictnessStrict:
		return 0.2
	case system_setting.StrictnessLoose:
		return 0.8
	default:
		return 0.5
	}
}

// shieldGemmaModerator L2 判定器。节点列表来自 moderation.endpoints 里 modality=image 的那些。
type shieldGemmaModerator struct {
	strictness string
	policy     *system_setting.ModerationPolicy
}

func (shieldGemmaModerator) Name() string { return "L2" }

// imageScores 一张图在三条策略上各自的 P(Yes)，key 是 shieldGemmaPolicy.Key。
//
// 判定链刻意在这里断成两截——先算分数、再套策略——**因为缓存必须缓存分数而不是结论**。
// 阈值随分组的 strictness 变、类别处置随分组的 policy 变，缓存了结论就会串味：
// 宽松分组判过的一张图，严格分组直接命中缓存放行，而那正是设置严格度想防的事。
// 分数是模型对这张图的客观输出，与谁在问无关，缓存它才是安全的。
type imageScores map[string]float64

// ModerateImage 对一张图做一次完整判定。
func (m shieldGemmaModerator) ModerateImage(ctx context.Context, imageURL string, hash string) (*Verdict, error) {
	scores, err := cachedImageScores(ctx, imageURL, hash)
	if err != nil {
		return nil, err
	}
	return m.verdictFromScores(scores), nil
}

// ModerateVideo 抽首/中/尾三帧，各判一次，取最严的那一帧作为整段的判定。
//
// 覆盖率的取舍写在 video_frames.go 顶部：三帧覆盖不了全部画面，这是为了给同步路径上的
// 抽帧成本一个硬上界而接受的漏检面，不是「三帧就够了」的技术结论。
func (m shieldGemmaModerator) ModerateVideo(ctx context.Context, videoURL string, hash string) (*Verdict, error) {
	frames, err := cachedVideoScores(ctx, videoURL, hash)
	if err != nil {
		return nil, err
	}
	worst := &Verdict{Action: ActionPass, Provider: "L2"}
	for _, scores := range frames {
		v := m.verdictFromScores(scores)
		if severity(v.Action) > severity(worst.Action) ||
			(severity(v.Action) == severity(worst.Action) && v.Score > worst.Score) {
			worst = v
		}
	}
	return worst, nil
}

// videoFrameConcurrency 单段视频内同时判定的帧数上限。
// 与 framePositions 返回的帧数一致；分开写是为了让「帧数变了」不会静默变成并发爆炸。
const videoFrameConcurrency = 3

// scoreVideo 抽帧并逐帧打分。
func scoreVideo(ctx context.Context, videoURL string) ([]imageScores, error) {
	frames, err := ExtractVideoFrames(ctx, videoURL)
	if err != nil {
		return nil, err
	}

	// 三帧并发。串行会让视频的判定时延变成图片的三倍，而这条路同样在同步路径上。
	//
	// 显式限并发而不是靠「反正只有三帧」：帧数是 framePositions 决定的，将来若改成
	// 按时长动态抽帧，这里会无声地变成几十路并发，把 imageSlots 一次占满。
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(videoFrameConcurrency)
	var mu sync.Mutex
	out := make([]imageScores, 0, len(frames))
	for _, frame := range frames {
		g.Go(func() error {
			scores, err := scoreImage(gctx, frame)
			if err != nil {
				// 与三条策略同理：抽到的帧必须全部判完。「三帧里判了两帧没问题」
				// 不等于「这段视频没问题」。
				return err
			}
			mu.Lock()
			out = append(out, scores)
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return out, nil
}

// scoreImage 三条策略各调一次，返回各自的 P(Yes)。
//
// 不把三条策略拼进一次调用：chat_template 对 content 数组里每个 text item 都生成一个
// 独立的 user turn，多条策略会拼出多个 turn 而模型只答一个 Yes/No，判定结果无法归因到
// 具体哪条策略——日志和申诉都说不清是因为什么被拦（§12.1「不做合并判定」同理）。
//
// 三条全部跑完，不做「已判违规就取消其余」的提前终止：三条本来就是并发的，提前取消
// 省不下墙钟时间，却会让缓存里存进一份残缺的分数，下次换个严格度命中它就判错了。
func scoreImage(ctx context.Context, imageURL string) (imageScores, error) {
	endpoints := system_setting.GetModerationSettings().ImageEndpoints()
	if len(endpoints) == 0 {
		// 开了图片审核却没有可用节点，是配置事故不是「无需审核」。交给 §6.4 的 fail 策略。
		return nil, errors.New("moderation: 未配置可用的图片审核节点")
	}

	budget := imageBudget(endpoints)
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	// 三条策略并发。串行会让单图时延变成三次视觉推理之和，而这条路在同步路径上，
	// 用户正等着提交返回（§12.2）。
	g, gctx := errgroup.WithContext(ctx)

	var mu sync.Mutex
	scores := make(imageScores, len(shieldGemmaPolicies))
	for _, p := range shieldGemmaPolicies {
		g.Go(func() error {
			pYes, err := judgeOne(gctx, imageURL, p, endpoints)
			if err != nil {
				// 任一条策略判定失败即整体 fail-close：「三条里查了两条没发现问题」
				// 不等于「没问题」，尤其没查的那条恰好可能是命中的那条。
				return err
			}
			mu.Lock()
			scores[p.Key] = pYes
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return scores, nil
}

// verdictFromScores 把三条策略的分数套上本分组的阈值与类别处置，得出判定。
func (m shieldGemmaModerator) verdictFromScores(scores imageScores) *Verdict {
	threshold := yesThreshold(m.strictness)
	worst := &Verdict{Action: ActionPass, Provider: "L2"}
	for _, p := range shieldGemmaPolicies {
		pYes, ok := scores[p.Key]
		if !ok {
			continue
		}
		v := m.verdictFor(p, pYes, threshold)
		if severity(v.Action) > severity(worst.Action) ||
			(severity(v.Action) == severity(worst.Action) && v.Score > worst.Score) {
			// 同档动作里保留分数更高的那条：落库时才看得出是哪条策略最接近阈值，
			// 调阈值时这是唯一的依据。
			worst = v
		}
	}
	return worst
}

// judgeOne 用一条策略判一次，按节点列表轮换。
func judgeOne(
	ctx context.Context,
	imageURL string,
	policy shieldGemmaPolicy,
	endpoints []system_setting.ModerationEndpoint,
) (float64, error) {
	if err := acquireImageSlot(ctx); err != nil {
		return 0, fmt.Errorf("moderation: 等待图片审核名额超时: %w", err)
	}
	defer releaseImageSlot()

	var lastErr error
	for i := range endpoints {
		ep := endpoints[i]
		if frozenUntil(ep.Name).After(time.Now()) {
			continue
		}
		pYes, status, err := callShieldGemma(ctx, &ep, imageURL, policy.Text)
		if err != nil {
			// 与 L1 同样的三分类，但**归属不同**，见 freezeDurationForVisionStatus：
			// vLLM 把「图片拉不到」报成 500 而不是 400，照搬文本侧的表会让一张坏图
			// 冻掉健康节点。
			if ctx.Err() != nil {
				lastErr = err
				break
			}
			freezeEndpoint(ep.Name, freezeDurationForVisionStatus(status))
			lastErr = err
			continue
		}
		clearFreeze(ep.Name)
		return pYes, nil
	}
	if lastErr == nil {
		lastErr = errors.New("moderation: 所有图片审核节点均处于冻结状态")
	}
	return 0, lastErr
}

// verdictFor 把一条策略的 P(Yes) 映射成 Verdict。
func (m shieldGemmaModerator) verdictFor(policy shieldGemmaPolicy, pYes float64, threshold float64) *Verdict {
	v := &Verdict{Provider: "L2", Score: pYes}
	if pYes < threshold {
		v.Action = ActionPass
		return v
	}
	v.Categories = []string{policy.Category}
	switch m.policy.CategoryAction(policy.Category) {
	case system_setting.CategoryActionBlock:
		v.Action = ActionBlock
	case system_setting.CategoryActionLog:
		v.Action = ActionReview
	default:
		// ignore：不处理，连记录都不升级，但**类别照留**。
		//
		// 早先这里把类别清零了，理由写的是「否则日志里会出现『判了类别但动作是
		// pass』这种自相矛盾的记录」——那个判断是错的。类别是**模型判定的内容
		// 属性**，action 是**我们的处置**，「模型认为这是隐私信息、我们配置成不处理」
		// 记下来是准确的，不是矛盾。清掉反而把「这一类到底命中多少次」这个
		// 调策略时唯一要问的问题给抹了，而 L1 一直是留着的（actionForCategories
		// 只改 action 不动 Categories），两个模态还因此行为不一致。
		v.Action = ActionPass
	}
	return v
}

// ── 上游调用 ──────────────────────────────────────────────────────────────

// visionRequest 多模态判定请求。
//
// 与 L1 的 chatRequest 分开而不是加个 any 字段：content 在文本侧是 string、
// 在视觉侧必须是数组，混成 any 会让两条路的 marshal 结果都变得要靠运行时猜。
type visionRequest struct {
	Model       string          `json:"model"`
	Messages    []visionMessage `json:"messages"`
	MaxTokens   int             `json:"max_tokens"`
	Temperature float64         `json:"temperature"`
	// LogProbs 取 Yes/No 的概率分布。ShieldGemma 2 官方的分类头做的就是这件事——
	// 取两个 token 的 logits 算概率。我们把模型转成标准 Gemma-3 走 chat completions 后
	// 分类头没了，但 logprobs 把同一份信息还了回来，于是 Score 不必恒为 0（§15.10）。
	LogProbs    bool `json:"logprobs"`
	TopLogProbs int  `json:"top_logprobs"`
}

type visionMessage struct {
	Role string `json:"role"`
	// Content 顺序有强约束：图片必须在文本之前。
	//
	// chat_template 对数组里每个 item 都发一个 <start_of_turn>user，其中 image 分支只发
	// <start_of_image> 而不闭合 turn，由后面的 text 分支补 <end_of_turn>。顺序反过来会
	// 拼出一个策略在前、图片悬空且 turn 未闭合的 prompt——不会报错，只会让判定失真。
	Content []visionPart `json:"content"`
}

type visionPart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	ImageURL *visionImageURL `json:"image_url,omitempty"`
}

type visionImageURL struct {
	URL string `json:"url"`
}

type visionResponse struct {
	Choices []struct {
		Message  chatMessage `json:"message"`
		LogProbs *struct {
			Content []struct {
				Token       string `json:"token"`
				TopLogProbs []struct {
					Token   string  `json:"token"`
					LogProb float64 `json:"logprob"`
				} `json:"top_logprobs"`
			} `json:"content"`
		} `json:"logprobs"`
	} `json:"choices"`
}

// callShieldGemma 发起一次判定调用，返回 P(Yes)、HTTP 状态码、错误。
//
// imageURL 可以是 data-url 也可以是 http(s) URL：vLLM 两种都收，远程 URL 由它自己拉取
// （实测通过）。这是 §7 C-1「零额外下载」的前提——白名单渠道的图此时已经是 OBS 签名 URL，
// 直接透传即可，不必先下回本地再转 base64。
func callShieldGemma(
	ctx context.Context,
	ep *system_setting.ModerationEndpoint,
	imageURL string,
	policyText string,
) (float64, int, error) {
	return callShieldGemmaWithKey(ctx, ep, ep.GetAPIKey(), imageURL, policyText)
}

// callShieldGemmaWithKey 与 callShieldGemma 相同，但由调用方给出明文 key。
// 测试连接那条路拿到的 key 已经是明文，不能再过一次解密。
func callShieldGemmaWithKey(
	ctx context.Context,
	ep *system_setting.ModerationEndpoint,
	apiKey string,
	imageURL string,
	policyText string,
) (float64, int, error) {
	timeout := time.Duration(ep.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		// 视觉推理比文本慢一个量级，默认值不能照抄文本侧的 3s。实测单图约 50–160ms，
		// 但那是热路径小图；大图解码 + 冷启动要留出余量。
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// max_tokens 给 4：输出就是一个 "Yes" 或 "No"（实测 completion_tokens=2，含结束符）。
	body, err := common.Marshal(visionRequest{
		Model: ep.Model,
		Messages: []visionMessage{{
			Role: "user",
			Content: []visionPart{
				{Type: "image_url", ImageURL: &visionImageURL{URL: imageURL}},
				{Type: "text", Text: policyText},
			},
		}},
		MaxTokens:   4,
		Temperature: 0,
		LogProbs:    true,
		TopLogProbs: 5,
	})
	if err != nil {
		return 0, 0, err
	}

	url := strings.TrimRight(ep.BaseURL, "/") + "/v1/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := service.GetHttpClient().Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, resp.StatusCode, fmt.Errorf("moderation: 图片审核节点 %s 返回 %d", ep.Name, resp.StatusCode)
	}

	var parsed visionResponse
	if err := common.DecodeJson(resp.Body, &parsed); err != nil {
		return 0, resp.StatusCode, err
	}
	if len(parsed.Choices) == 0 {
		return 0, resp.StatusCode, errors.New("moderation: 图片审核节点返回空结果")
	}
	return probabilityOfYes(&parsed)
}

// probabilityOfYes 从响应里解出 P(Yes)。
//
// 优先读 logprobs：它给出的是连续量，能支撑阈值可调。取不到时退回读输出文本，
// 此时只有 0/1 两个值，等价于接受模型自身的切点。
//
// 两者都取不到就报错而不是返回 0 —— 「解析不出来」绝不能等于「安全」（§6.4）。
func probabilityOfYes(resp *visionResponse) (float64, int, error) {
	choice := resp.Choices[0]
	if choice.LogProbs != nil && len(choice.LogProbs.Content) > 0 {
		first := choice.LogProbs.Content[0]
		for _, cand := range first.TopLogProbs {
			if normalizeYesNo(cand.Token) == "yes" {
				return math.Exp(cand.LogProb), http.StatusOK, nil
			}
		}
		// Yes 不在 top-N 里说明它的概率低于第 N 名，远在任何合理阈值之下。
		// 这种情况下按首 token 本身定值，不当成解析失败。
		if normalizeYesNo(first.Token) == "no" {
			return 0, http.StatusOK, nil
		}
	}

	switch normalizeYesNo(choice.Message.Content) {
	case "yes":
		return 1, http.StatusOK, nil
	case "no":
		return 0, http.StatusOK, nil
	}
	// 输出既不是 Yes 也不是 No：部署的多半不是 ShieldGemma，或者模板没生效。
	// 判成错误交给 fail-close，绝不能当成通过。
	return 0, http.StatusOK, fmt.Errorf(
		"moderation: 图片审核节点返回无法识别的判定结果 %q", truncateForError(choice.Message.Content))
}

// normalizeYesNo 归一化模型输出的首 token。
// 实测 top_logprobs 里同时出现 "No" / "no" / " No" / "NO" 多种变体。
func normalizeYesNo(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch {
	case strings.HasPrefix(s, "yes"):
		return "yes"
	case strings.HasPrefix(s, "no"):
		return "no"
	}
	return ""
}

func truncateForError(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 64 {
		return s[:64]
	}
	return s
}

// ── 冻结与并发 ────────────────────────────────────────────────────────────

// freezeDurationForVisionStatus 视觉节点的分级冻结。
//
// **与文本侧相反的一条：500 不冻结。** vLLM 拉不到图片时返回的是 500 而不是 400
// （实测：{"message":"Cannot connect to host ...","code":500}），而图片拉不到是**请求的
// 问题**——换个节点结果一样。照搬 freezeDurationForHTTPStatus 会让一个用户提交的坏图
// URL 把健康节点逐个冻掉，拦截模式下就是全站 503。这正是文本侧「400 不冻结」那条教训
// 的镜像版本，只是这里换了个状态码出现。
//
// 代价是真正的节点内部错误（模型 OOM、权重损坏）也不再触发冻结，只能靠每次调用失败
// 后轮换到下一个节点来兜。这个取舍是有意的：漏冻结的后果是降级（每次多烧一次失败调用），
// 误冻结的后果是全站拒绝，两者不对称。
func freezeDurationForVisionStatus(status int) time.Duration {
	switch status {
	case http.StatusInternalServerError:
		return 0
	case http.StatusBadRequest:
		return 0
	case 0:
		// 压根没拿到 HTTP 响应：连接被拒、DNS 失败、读超时。这是节点的问题。
		return httpErrorFreezeDuration
	case http.StatusUnauthorized, http.StatusForbidden:
		return authFreezeDuration
	case http.StatusTooManyRequests, 529:
		return rateLimitFreezeDuration
	default:
		return httpErrorFreezeDuration
	}
}

// imageConcurrency 同时打给视觉节点的判定调用数上限。
//
// 与文本闸分开，不是复用：视觉推理慢一个量级，共用一个闸会让一批图片把文本审核的
// 名额占满，于是「有人在传图」变成「所有聊天请求审核失败」。
//
// 计量单位是**一次 HTTP 判定调用**，不是「一张图」：一张图 = 3 条策略 = 3 个名额，
// 一段视频 = 3 帧 × 3 条策略 = 9 个名额。所以 32 大约是「三段视频同时在审」的量级。
const imageConcurrency = 32

var imageSlots = make(chan struct{}, imageConcurrency)

// acquireImageSlot 取一个判定名额，闸满时**等待**而不是立即失败。
//
// 这一条与文本侧的 acquireSlot 有意不同（§6.5 二说的是「满了直接拒绝不排队」）。
// 差别的理由是两边闸满的含义不同：
//
// 文本侧一次判定就是一个名额，闸满意味着真的有 64 个请求在同时审，那是过载。
// 图片侧一次提交就要 9 个名额，闸满是**常态**——单个请求带 4 段视频就需要 36 个，
// 已经超过全闸；两个用户各传两段视频也一样。立即失败会把它变成 ActionError，
// 再被 fail-close 兜成 503：GPU 明明闲着，正常请求却被自己的限流打成服务故障。
//
// 排队并不违背 §6.5 二想防的东西——它防的是**无界**排队让故障期延迟无限增长。
// 这里的等待有硬 deadline：scoreImage 的 imageBudget 和 ModerateMedia 的
// mediaBatchBudget 都在 ctx 上，等不到就按超时 fail-close，延迟有界。
func acquireImageSlot(ctx context.Context) error {
	select {
	case imageSlots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func releaseImageSlot() {
	select {
	case <-imageSlots:
	default:
	}
}

// imageBudget 单张图三条策略共享的总预算。
//
// 与 L1 的 totalBudget 同理要把节点数算进去：judgeOne 是串行轮换节点的，
// 慢节点吃满超时后，轮换到次节点的调用不能是在一个已过期的 context 上发出的。
// 三条策略并发，所以不乘策略数。
func imageBudget(endpoints []system_setting.ModerationEndpoint) time.Duration {
	per := 0
	for _, e := range endpoints {
		if e.TimeoutMS > per {
			per = e.TimeoutMS
		}
	}
	if per <= 0 {
		per = 10000
	}
	attempts := len(endpoints)
	if attempts <= 0 {
		attempts = 1
	}
	budget := time.Duration(per) * time.Millisecond * time.Duration(attempts)
	// 上界 30s。图片审核在同步路径上，用户在等提交返回——超过这个数就该 fail-close 掉，
	// 而不是把人晾着（§12.2）。
	if budget > 30*time.Second {
		budget = 30 * time.Second
	}
	return budget
}

// ── 连通性测试 ────────────────────────────────────────────────────────────

// testImagePNG 一张 8×8 纯灰图的 data-url，供「测试连接」使用。
//
// 用纯色图而不是真实照片：测试要回答的是「这个节点通不通、部署的是不是 ShieldGemma」，
// 不是「判得准不准」。纯色图必然判 No，任何别的结果都说明这一端有问题。
const testImagePNG = "data:image/png;base64," +
	"iVBORw0KGgoAAAANSUhEUgAAAAgAAAAICAIAAABLbSncAAAAD0lEQVR4nGNowAEYhpYEAILzYAGc7g8kAAAAAElFTkSuQmCC"

// TestImageEndpoint 用一张固定的无害图打一次真实判定，供管理端「测试连接」使用。
//
// 与 TestEndpoint 同样刻意不复用 ModerateImage：那条路会走节点轮换和冻结状态，
// 而测试要的恰恰是「这一个节点此刻通不通」。
func TestImageEndpoint(ctx context.Context, baseURL, model, apiKey string, timeoutMS int) TestResult {
	ep := system_setting.ModerationEndpoint{
		Name:      "__test_image__",
		BaseURL:   baseURL,
		Model:     model,
		Modality:  ModalityImage,
		TimeoutMS: timeoutMS,
	}
	pYes, _, err := callShieldGemmaWithKey(ctx, &ep, apiKey, testImagePNG, shieldGemmaPolicies[0].Text)
	if err != nil {
		return TestResult{Err: err}
	}
	return TestResult{
		Raw: fmt.Sprintf("P(Yes)=%.4f", pYes),
		// 能解出概率就算真通：解不出来的情况已经在 probabilityOfYes 里变成 err 了。
		// 不检查 pYes 是否够低——那是判得准不准的问题，测试连接不负责回答。
		ParsedOK: true,
	}
}
