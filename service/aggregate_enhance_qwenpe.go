// qwen_pe 增强：Qwen-Image-2.1（qwen-image-pro）的官方 PE 协议。
//
// 官方给 Qwen-Image-2.1 配了两份改写系统提示词（文生图 / 编辑，原文在
// service/qwenpe/，逐字照抄），约定模型回一个 JSON：
//
//	t2i   {"rewritten_prompt": "...", "wh_ratio": "3:2"}
//	edit  {"rewritten_prompt": "...", "wh_ratio": "", "ratio_follow": "<image1>"}
//
// 改写结果**不止是提示词**：wh_ratio / ratio_follow 决定画幅。官方 README 说得很直白：
// 忽略它们、按底图比例出图，等于丢掉了改写的一部分 —— 一段描述宽幅双人构图的
// 提示词画在竖版画布上，是另一张图。
//
// # 流程（照 H3 singlecall 的模式）
//
//	选提示词 → 调用 → 取 JSON → 传输检查 → 不过就把错误清单发回去重修一轮
//	→ 仍不过则降级为原始提示词
//
// 失败**不回落 text**：text 模式用的是通用模板，对 Q21 没有意义，
// 回落过去只是多等一次、换来一段同样不对的提示词。
//
// # 画幅以谁为准（与百炼 qwen-image 的 size 语义一致）
//
//  1. 客户传了 size：以接口为准，不改 size；并在发给增强模型的用户消息末尾声明
//     画幅已固定，让改写按这个画幅构图。实测不声明时，提示词写着 1:1 而接口要
//     16:9，改写照样写 "a square photograph"，而图按 16:9 出 —— 描述与画布打架。
//  2. 没传 size：用改写给的 wh_ratio（按官方 2K 表换算）或 ratio_follow
//     （跟随被引用那张输入图的比例）。
//  3. 增强失败：size 保持不传，交给生成段自己的默认值。
package service

import (
	"context"
	_ "embed"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
)

var (
	//go:embed qwenpe/system_prompt_t2i.txt
	qwenPESystemT2I string
	//go:embed qwenpe/system_prompt_edit.txt
	qwenPESystemEdit string
)

// qwenPEEscapeRule 追加在官方提示词后面的 JSON 转义说明与五条补充规则。
//
// 两份原文都要求画面文字放进直双引号，又要求回复是严格合法的 JSON，却从没说过
// 引号要转义。官方 PE 模型训练过会转义，通用模型不会 —— `reads "今日特调"`
// 这种裸引号直接把 JSON 弄坏。实测（qwen3.8 关思考，易错用例 × 5 次）：
// 不加 34/40 可直接解析，加上 40/40。
//
// 后面五条是拿 qwen3.8 与官方 PE 逐项对比（27 用例 × 3 次）后补的，只针对
// qwen3.8 偏离官方要求的地方，不改官方原文：
//
//   - 比例只会默认 3:2：营养表、春联、菜单这类天然竖版题材也给 3:2
//   - 位置含糊不下结论："directly above or next to"、"如画面中心或黄金分割点"
//   - 先写纸张纹理和光线、再写内容，与官方"首句点明主体、先走画面"相反
//   - 多图合成是新构图却给 ratio_follow，官方规定此时要给 wh_ratio
//   - 编辑范围理解偏宽（"只改主标题"连副标题一起改），保留条款一句带过
//
// 加上之后（同样 27 × 3）：代码围栏 11→4、比例与 PE 众数一致 47→51、多图误用
// ratio_follow 1→0、副标题误改 1→0、编辑里逐字列出的保留文字 2→7 串（PE 是 8），
// 保字仍 100%。含糊词没有改善（0.6→0.8/条，多是 "stone or tile" 这类材质二选一，
// PE 自己也有 1.3/条），留着这条规则是因为它至少把位置写死了。
//
// 最后三条是端到端出图后补的（第三版）：首轮出图里香水被"蓝色环境光"染成蓝玻璃、
// 静物合成把手表叠在香水瓶上、改电话把"电话"标签一起删掉。三条各对一处。同一条
// 直连管线（改写 → 定尺寸 → qwen-image-pro）v2/v3 各出 3 张 × 3 用例：静物合成
// v3 3/3 互不重叠且完整入镜（v2 有 1 张裁掉了物体、1 张没给比例），香水与名片两
// 版都 3/3 正确 —— 首轮那两处缺陷有随机成分，但规则不伤画面。
const qwenPEEscapeRule = `JSON escaping: the whole reply is parsed with a strict JSON parser. Every straight double quote that appears INSIDE a string value — including the quotes around text rendered in the image — must be escaped as \" (for example: {"rewritten_prompt": "a sign that reads \"OPEN\""}). Never emit a bare " inside a value. The reply must begin with { and end with } — no code fences, no tags, no words before or after the object.

Additional rules (they refine, never override, the instructions above):
- Orientation first. Before writing, decide the canvas from the subject's natural shape: labels, tables, menus, couplets, posters, phone screens, book pages and standing figures are vertical; landscapes, desks, storefronts and wide signs are horizontal; icons, dials and badges are square. Never fall back to 3:2 out of habit — pick it only when the subject is genuinely horizontal.
- Commit. Give every object and every string of text one exact position and size. Never hedge with "or", "such as", "possibly", "或", "如", "可能" — decide, and state it as fact.
- Content before finish. The opening sentence names the medium, style, subject and orientation. Then describe what is in the frame — subjects, layout, every text string in quotes — and only after that the lighting, materials and mood.
- When several input images are combined into a new scene, there is no canvas: set wh_ratio to the ratio you chose and leave ratio_follow empty. ratio_follow is only for editing one existing picture.
- Read the request narrowly. Change exactly the element the user named and nothing next to it (a "main title" is only the largest title line, not the subtitle). When the input contains readable text that must survive, list each of those strings verbatim in the preservation clause instead of summarising them.
- Objects taken from an input image keep their own materials and colours exactly: clear glass stays clear, a liquid keeps its tint, a label keeps its text and layout. Scene lighting may add highlights, reflections and shadows on them, but it never recolours them — describe the object's own colour, then the light falling on it, as two separate facts.
- When several objects are arranged together, give each its own non-overlapping spot on the surface with a concrete position ("upper left", "lower right", "centre"), resting flat or standing on the surface. Objects never stack on, lean against or overlap each other unless the user asks for that.
- When editing text, replace only the characters the user changed; the label or words next to them (such as "电话" before a phone number) stay exactly where they are.`

// qwenPEEditClosing 官方编辑提示词的收尾句，紧跟着就是用户的指令。
const qwenPEEditClosing = "The user's edit instruction to rewrite is:"

// buildQwenPESystem 按输入图张数选官方提示词，并补上转义说明。
//
// 编辑那份的转义说明**必须插在收尾句之前**：收尾句的下一行就是用户指令，
// 插在它后面，模型会把这段英文当成要改写的指令本身。
func buildQwenPESystem(numImages int) string {
	if numImages == 0 {
		return strings.TrimRight(qwenPESystemT2I, "\n") + "\n\n" + qwenPEEscapeRule + "\n"
	}
	s := strings.TrimRight(qwenPESystemEdit, "\n")
	if strings.HasSuffix(s, qwenPEEditClosing) {
		head := strings.TrimRight(strings.TrimSuffix(s, qwenPEEditClosing), "\n")
		return head + "\n\n" + qwenPEEscapeRule + "\n\n" + qwenPEEditClosing + "\n"
	}
	// 上游改了收尾句：退回追加末尾，不因为一句措辞变化丢掉转义说明。
	return s + "\n\n" + qwenPEEscapeRule + "\n"
}

// qwenPETimeout 两轮（首轮 + 一轮重修）共用的内置预算。
//
// 实测 qwen3.8 关思考单轮中位 4~9 秒、P90 约 12 秒；两轮留 60 秒足够，
// 又不至于在增强卡死时把客户的出图请求吊太久。做成 var 供测试压短。
var qwenPETimeout = 60 * time.Second

func qwenPEBudget(cfg *common.AggregatePromptEnhance) time.Duration {
	if cfg != nil && cfg.TimeoutSeconds > 0 {
		return time.Duration(cfg.TimeoutSeconds) * time.Second
	}
	return qwenPETimeout
}

// qwenPEResult 一次成功改写的产物。
type qwenPEResult struct {
	Prompt string
	// Size 定下来的输出尺寸（"WxH"）。空 = 不改客户请求里的 size。
	Size     string
	Warnings []string
	Repaired bool
	Usage    *dto.Usage
}

type qwenPEOutput struct {
	Prompt      string
	WHRatio     string
	RatioFollow string
}

// qwenPEFixedRatio 客户显式给的 size 对应的比例（"16:9"）；给的是档位词
// （"2K"）或没给时返回空串 —— 说不出比例就不声明，不猜。
//
// 像素尺寸按官方比例词说：官方 2K 表里的 2752x1536 直接约分是 "43:24"，
// 模型不认这种写法；与某个标准比例相差 2% 以内就用那个标准比例。
func qwenPEFixedRatio(size string) string {
	s := strings.TrimSpace(size)
	if s == "" {
		return ""
	}
	if common.IsAspectRatio(s) {
		return common.NormalizeAspectRatio(s)
	}
	w, h, ok := common.DimsFromSize(s)
	if !ok {
		return ""
	}
	r := float64(w) / float64(h)
	for _, std := range qwenPEStandardRatios {
		if math.Abs(r/(float64(std[0])/float64(std[1]))-1) <= 0.02 {
			return fmt.Sprintf("%d:%d", std[0], std[1])
		}
	}
	return common.AspectRatioFromSize(s)
}

// qwenPEStandardRatios 官方 t2i 提示词 Step 2 列出的全部比例。
var qwenPEStandardRatios = [][2]int{
	{1, 1}, {3, 2}, {2, 3}, {4, 3}, {3, 4}, {16, 9}, {9, 16}, {21, 9}, {9, 21},
	{2, 1}, {1, 2}, {4, 5}, {5, 4}, {3, 1}, {1, 3},
}

func compileQwenPEWithTimeout(
	ctx context.Context, authHeader, model string, in EnhanceInput, budget time.Duration,
) (*qwenPEResult, error) {
	cctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	return compileQwenPE(cctx, authHeader, model, in)
}

func compileQwenPE(ctx context.Context, authHeader, model string, in EnhanceInput) (*qwenPEResult, error) {
	var images []string
	for _, u := range in.ImageURLs {
		if strings.TrimSpace(u) != "" {
			images = append(images, u)
		}
	}
	n := len(images)
	sizeFixed := strings.TrimSpace(in.ClientSize) != ""

	userText := in.Prompt
	if fixed := qwenPEFixedRatio(in.ClientSize); fixed != "" {
		// 官方提示词的规则是「用户说了比例就照用」，所以把接口的画幅写成用户的话。
		userText += "\n\n(The output canvas is fixed by the caller at " + fixed + " — compose for it.)"
	}
	msgs := []map[string]any{
		{"role": "system", "content": buildQwenPESystem(n)},
		{"role": "user", "content": buildUserContent(userText, images, nil, in.SendMedia)},
	}

	total := &dto.Usage{}
	var lastErrs []string
	for round := 0; round <= singleCallMaxRepairRounds; round++ {
		// **不带 response_format。** qwen3.8（MTP 投机解码）+ 关思考 + json_object
		// 实测约一半请求被引擎以 grammar rejected 终止、返回 500；取 JSON 靠下面的容错解析。
		body := map[string]any{"model": model, "messages": msgs, "stream": false}
		applyThinking(body, in.Thinking)
		payload, err := common.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("构造改写请求失败: %w", err)
		}
		raw, usage, err := callEnhance(ctx, authHeader, payload)
		accumulateUsage(total, usage)
		if err != nil {
			return nil, err
		}
		last := round == singleCallMaxRepairRounds

		out, ok := extractQwenPE(raw)
		if !ok {
			lastErrs = []string{qwenPEFormatError(n)}
		} else {
			errs, ratioErrs := qwenPEIssues(out, n, in.Prompt, sizeFixed)
			if len(errs) == 0 && (len(ratioErrs) == 0 || last) {
				res := &qwenPEResult{Prompt: out.Prompt, Repaired: round > 0, Usage: total}
				// 只剩比例字段有问题:提示词本身可用,别为了画幅丢掉它 ——
				// 不定 size,交给生成段的默认值。
				res.Warnings = append(res.Warnings, ratioErrs...)
				if !sizeFixed && len(ratioErrs) == 0 {
					size, err := qwenPESize(out, images)
					if err != nil {
						res.Warnings = append(res.Warnings, err.Error())
					}
					res.Size = size
				}
				return res, nil
			}
			lastErrs = append(errs, ratioErrs...)
		}
		if last {
			break
		}
		msgs = append(msgs,
			map[string]any{"role": "assistant", "content": raw},
			map[string]any{"role": "user", "content": buildSingleCallRepair(lastErrs)},
		)
	}
	return nil, fmt.Errorf("传输检查未通过: %s", strings.Join(lastErrs, "; "))
}

func qwenPEFormatError(numImages int) string {
	if numImages == 0 {
		return "Response must be a single JSON object with rewritten_prompt / wh_ratio"
	}
	return "Response must be a single JSON object with rewritten_prompt / wh_ratio / ratio_follow"
}

// qwenPELoosePattern 裸引号兜底：JSON 坏在正文里的一个未转义引号，
// 但字段本身完好。实测四家模型都会犯，按字段名把整段取出来即可。
var qwenPELoosePattern = regexp.MustCompile(
	`(?s)"rewritten_prompt"\s*:\s*"(.*?)"\s*,\s*"wh_ratio"\s*:\s*"([^"]*)"(?:\s*,\s*"ratio_follow"\s*:\s*"([^"]*)")?`)

var qwenPEUnescaper = strings.NewReplacer(`\"`, `"`, `\n`, "\n", `\t`, "\t", `\\`, `\`)

// extractQwenPE 从回复里取出改写结果。先按合法 JSON 取（围栏、前后缀、
// 游离花括号由 jsonCandidates 处理），不行再用裸引号兜底。
func extractQwenPE(raw string) (qwenPEOutput, bool) {
	for _, cand := range jsonCandidates(raw) {
		var o struct {
			RewrittenPrompt string `json:"rewritten_prompt"`
			WHRatio         string `json:"wh_ratio"`
			RatioFollow     string `json:"ratio_follow"`
		}
		if err := common.Unmarshal([]byte(cand), &o); err == nil && strings.TrimSpace(o.RewrittenPrompt) != "" {
			return qwenPEOutput{Prompt: strings.TrimSpace(o.RewrittenPrompt),
				WHRatio: strings.TrimSpace(o.WHRatio), RatioFollow: strings.TrimSpace(o.RatioFollow)}, true
		}
	}
	if m := qwenPELoosePattern.FindStringSubmatch(raw); m != nil {
		if p := strings.TrimSpace(qwenPEUnescaper.Replace(m[1])); p != "" {
			return qwenPEOutput{Prompt: p, WHRatio: strings.TrimSpace(m[2]), RatioFollow: strings.TrimSpace(m[3])}, true
		}
	}
	return qwenPEOutput{}, false
}

// ── 传输检查 ────────────────────────────────────────────────────────

var (
	qwenPERatioPattern  = regexp.MustCompile(`^([1-9]\d*):([1-9]\d*)$`)
	qwenPEFollowPattern = regexp.MustCompile(`^<image([1-9]\d*)>$`)
	qwenPETagPattern    = regexp.MustCompile(`<image(\d+)>`)
)

// qwenPEIssues 只核对运得出去，不核对写得好不好。错误信息是英文 —— 要原样发回给模型重修。
//
// 比例字段的问题单独返回：客户传了 size 时它们根本不用，不查；最后一轮只剩它们时
// 调用方接受提示词、不定 size（见 compileQwenPE）。
func qwenPEIssues(out qwenPEOutput, numImages int, userPrompt string, sizeFixed bool) (errs, ratioErrs []string) {
	if strings.TrimSpace(out.Prompt) == "" {
		return []string{"rewritten_prompt must be nonempty"}, nil
	}
	if !sizeFixed {
		ratioErrs = qwenPERatioIssues(out, numImages)
	}

	// 标签：不能引用不存在的图；多图时每张都要被指到 —— 不指明哪张是画布、
	// 哪张提供素材，生成段只能猜。
	present := map[int]bool{}
	var ghosts []string
	for _, m := range qwenPETagPattern.FindAllStringSubmatch(out.Prompt, -1) {
		k, _ := strconv.Atoi(m[1])
		if k < 1 || k > numImages {
			ghosts = append(ghosts, m[0])
			continue
		}
		present[k] = true
	}
	if len(ghosts) > 0 {
		errs = append(errs, "Prompt references nonexistent image tags: "+strings.Join(uniqueStrings(ghosts), ", "))
	}
	if numImages >= 2 {
		var missing []string
		for k := 1; k <= numImages; k++ {
			if !present[k] {
				missing = append(missing, fmt.Sprintf("<image%d>", k))
			}
		}
		if len(missing) > 0 {
			errs = append(errs, "Refer to every input image by its tag; missing: "+strings.Join(missing, ", "))
		}
	}

	if missing := qwenPEMissingText(userPrompt, out.Prompt); len(missing) > 0 {
		errs = append(errs, "Text the user asked to appear must be copied verbatim (same characters, same script): missing \""+
			strings.Join(missing, "\", \"")+"\"")
	}
	return errs, ratioErrs
}

func qwenPERatioIssues(out qwenPEOutput, numImages int) []string {
	if numImages == 0 {
		if !qwenPERatioPattern.MatchString(out.WHRatio) {
			return []string{`wh_ratio must be an aspect ratio such as "3:2"`}
		}
		return nil
	}
	if (out.WHRatio == "") == (out.RatioFollow == "") {
		return []string{"Exactly one of wh_ratio and ratio_follow must be nonempty"}
	}
	if out.WHRatio != "" {
		if !qwenPERatioPattern.MatchString(out.WHRatio) {
			return []string{`wh_ratio must be an aspect ratio such as "3:2"`}
		}
		return nil
	}
	m := qwenPEFollowPattern.FindStringSubmatch(out.RatioFollow)
	if m == nil {
		return []string{fmt.Sprintf("ratio_follow must be one of <image1>..<image%d>", numImages)}
	}
	if k, _ := strconv.Atoi(m[1]); k > numImages {
		return []string{fmt.Sprintf("ratio_follow must be one of <image1>..<image%d>", numImages)}
	}
	return nil
}

// qwenPERenderedTextPattern 用户明确要画进图里的文字。
//
// **引号前必须有渲染文字的语境**（写着 / 印有 / 标题 / 上联 / reads / titled …）。
// 中文提示词里引号最常见的用途是风格词和强调词（生成一段"赛博朋克"风格的夜景），
// 那些会被正常地译成英文描述，按「引号里的都要原样出现」查会把正常改写当错、白烧一轮
// 重修 —— dialogueIssues 踩过同一个坑。宁可漏，不可错。
var qwenPERenderedTextPattern = regexp.MustCompile(
	`(?i)(?:写着|写有|写上|印着|印有|印上|副标题|标题|标语|字样|文字|题字|题着|上联|下联|横批|` +
		`\breads?|\breading|\btitled|\bsubtitle|\bsays|\bsaying|\blabell?ed|\bcaption)` +
		`\s*(?:为|是|[:：])?\s*` +
		"[「『\"“]([^「」『』\"“”]{1,60})[」』\"”]")

// qwenPEMissingText 用户要画进图里的文字，改写里缺了哪些。
//
// 按空白拆段逐段查：把一行拆成几处写（"能量" / "272千焦"）不算丢。ASCII 大小写
// 不敏感 —— 海报把副标题排成全大写是正常的排版决定，不是改字。
func qwenPEMissingText(userPrompt, rewritten string) []string {
	hay := strings.ToLower(rewritten)
	var missing []string
	seen := map[string]bool{}
	for _, m := range qwenPERenderedTextPattern.FindAllStringSubmatch(userPrompt, -1) {
		s := strings.TrimSpace(m[1])
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		for _, seg := range strings.Fields(s) {
			if !strings.Contains(hay, strings.ToLower(seg)) {
				missing = append(missing, s)
				break
			}
		}
	}
	return missing
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// ── 画幅 ────────────────────────────────────────────────────────────

// qwenPE2KSizes 官方推荐尺寸（Qwen-Image-2.1 README「Supported Aspect Ratios」，
// 原生 2K）。
var qwenPE2KSizes = map[string][2]int{
	"1:1":  {2048, 2048},
	"4:3":  {2400, 1792},
	"3:4":  {1792, 2400},
	"3:2":  {2528, 1696},
	"2:3":  {1696, 2528},
	"16:9": {2752, 1536},
	"9:16": {1536, 2752},
}

// qwenPESizeForRatio 比例 → 2K 尺寸。表内直接取；表外（官方提示词允许 21:9、4:5、
// 3:1 等）按面积 ≈ 2048² 算，宽高取 16 的倍数。
func qwenPESizeForRatio(w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	g := w
	for b := h; b != 0; {
		g, b = b, g%b
	}
	if v, ok := qwenPE2KSizes[fmt.Sprintf("%d:%d", w/g, h/g)]; ok {
		return fmt.Sprintf("%dx%d", v[0], v[1])
	}
	r := float64(w) / float64(h)
	fw := math.Sqrt(2048 * 2048 * r)
	round16 := func(x float64) int { return max(16, int(math.Round(x/16))*16) }
	return fmt.Sprintf("%dx%d", round16(fw), round16(fw/r))
}

// qwenPESize 按改写结果定尺寸。返回空串表示不定（交给生成段默认）。
func qwenPESize(out qwenPEOutput, images []string) (string, error) {
	if m := qwenPERatioPattern.FindStringSubmatch(out.WHRatio); m != nil {
		w, _ := strconv.Atoi(m[1])
		h, _ := strconv.Atoi(m[2])
		return qwenPESizeForRatio(w, h), nil
	}
	m := qwenPEFollowPattern.FindStringSubmatch(out.RatioFollow)
	if m == nil {
		return "", nil
	}
	k, _ := strconv.Atoi(m[1])
	if k < 1 || k > len(images) {
		return "", nil
	}
	w, h, err := qwenPEImageDims(images[k-1])
	if err != nil {
		return "", fmt.Errorf("ratio_follow=%s 但读不到该图尺寸,不定 size: %v", out.RatioFollow, err)
	}
	return qwenPESizeForRatio(w, h), nil
}

// qwenPEImageDims 读输入图的宽高：data URI 直接解，URL 只下载到能读出头信息为止。
func qwenPEImageDims(u string) (int, int, error) {
	u = strings.TrimSpace(u)
	if strings.HasPrefix(u, "data:") {
		cfg, _, _, err := DecodeBase64ImageData(u)
		if err != nil {
			return 0, 0, err
		}
		return cfg.Width, cfg.Height, nil
	}
	cfg, _, err := DecodeUrlImageData(u)
	if err != nil {
		return 0, 0, err
	}
	return cfg.Width, cfg.Height, nil
}
