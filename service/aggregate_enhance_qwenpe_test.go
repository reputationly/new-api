package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/common"
)

func qpCfg() *common.AggregateModel {
	return &common.AggregateModel{
		Name: "qwen-image-pro-enhanced", Type: "image",
		PromptEnhance: &common.AggregatePromptEnhance{Model: "enh", Mode: common.EnhanceModeQwenPE},
		Generate:      common.AggregateGenerate{Model: "qwen-image-pro"},
	}
}

func qpReply(fields map[string]any) string {
	b, _ := json.Marshal(fields)
	return string(b)
}

// pngDataURI 一张 w×h 的小 PNG,用来走 ratio_follow 的读图路径。
func pngDataURI(t *testing.T, w, h int) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h))))
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

// requestUserText 取某次请求里 user 消息的文字部分(纯文本或多模态数组里的 text)。
func requestUserText(t *testing.T, raw []byte) string {
	t.Helper()
	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(raw, &req))
	for _, m := range req.Messages {
		if m.Role != "user" {
			continue
		}
		switch c := m.Content.(type) {
		case string:
			return c
		case []any:
			for _, p := range c {
				if pm, ok := p.(map[string]any); ok && pm["type"] == "text" {
					return pm["text"].(string)
				}
			}
		}
	}
	return ""
}

// **按有无输入图选官方提示词,转义说明在编辑那份里必须插在收尾句之前。**
//
// 收尾句的下一行就是用户指令;转义说明插在它后面,模型会把这段英文当成要改写的指令。
func TestQwenPESystemPicksPromptAndEscapeRule(t *testing.T) {
	t2i := buildQwenPESystem(0)
	require.True(t, strings.HasPrefix(t2i, strings.TrimRight(qwenPESystemT2I, "\n")), "t2i 原文必须原样在前")
	require.Contains(t, t2i, qwenPEEscapeRule, "t2i 要带转义说明")
	require.True(t, strings.HasSuffix(strings.TrimSpace(t2i), qwenPEAnalysisRule), "t2i 的先分析后改写追加在最末")
	require.Less(t, strings.Index(t2i, qwenPEEscapeRule), strings.Index(t2i, qwenPEAnalysisRule), "转义说明在分析规则之前")

	edit := buildQwenPESystem(2)
	require.NotContains(t, edit, "Step 2 — Fix the frame", "有图时不能发 t2i 那份")
	require.NotContains(t, edit, qwenPEAnalysisRule, "编辑不加先分析后改写：实测会拖累多图 <imageN> 标签")
	require.True(t, strings.HasSuffix(strings.TrimSpace(edit), qwenPEEditClosing), "收尾句必须仍在最后")
	require.Less(t, strings.Index(edit, qwenPEEscapeRule), strings.LastIndex(edit, qwenPEEditClosing),
		"转义说明必须在收尾句之前")
	head := strings.TrimRight(strings.TrimSuffix(strings.TrimRight(qwenPESystemEdit, "\n"), qwenPEEditClosing), "\n")
	require.True(t, strings.HasPrefix(edit, head), "编辑原文(收尾句之前的部分)必须原样在前")
}

// 内置的是官方原文。有人顺手改了措辞,这里要能看出来。
func TestQwenPEEmbeddedPromptsAreOfficial(t *testing.T) {
	require.True(t, strings.HasSuffix(strings.TrimRight(qwenPESystemEdit, "\n"), qwenPEEditClosing))
	require.Contains(t, qwenPESystemT2I, `{"rewritten_prompt": "<the description>", "wh_ratio": "<e.g. 3:2>"}`)
	require.Contains(t, qwenPESystemEdit, "## Output Size Determination")
}

// 文生图让模型先写 analysis 再写改写结果:analysis 是第一个键,里面还带一个
// 叫 ratio 的字段。严格解析要忽略它只取顶层字段;裸引号兜底不能被它的键名带偏。
func TestExtractQwenPEIgnoresLeadingAnalysis(t *testing.T) {
	raw := `{"analysis": {"fixed": ["\"OPEN\""], "canvas": "none", "orientation": "horizontal — a wide shop sign", "ratio": "3:2"}, "rewritten_prompt": "a wide shot of a sign that reads \"OPEN\"", "wh_ratio": "3:2"}`
	out, ok := extractQwenPE(raw)
	require.True(t, ok)
	require.Equal(t, `a wide shot of a sign that reads "OPEN"`, out.Prompt)
	require.Equal(t, "3:2", out.WHRatio)

	// 正文里一个裸引号把 JSON 弄坏,兜底仍要取到顶层字段而不是 analysis 里的。
	broken := `{"analysis": {"fixed": ["OPEN"], "ratio": "9:16"}, "rewritten_prompt": "a sign that reads "OPEN" at dusk", "wh_ratio": "3:2"}`
	out, ok = extractQwenPE(broken)
	require.True(t, ok)
	require.Equal(t, `a sign that reads "OPEN" at dusk`, out.Prompt)
	require.Equal(t, "3:2", out.WHRatio, "要的是顶层 wh_ratio,不是 analysis.ratio")
}

// 围栏、前后缀要能恢复;裸引号(实测四家模型都会犯)要靠兜底把字段取出来。
func TestExtractQwenPE(t *testing.T) {
	good := qpReply(map[string]any{"rewritten_prompt": `a sign that reads "OPEN"`, "wh_ratio": "3:2"})
	for name, raw := range map[string]string{
		"纯 JSON": good,
		"代码围栏":   "```json\n" + good + "\n```",
		"前后有话":   "好的：\n" + good + "\n以上。",
	} {
		out, ok := extractQwenPE(raw)
		require.True(t, ok, name)
		require.Equal(t, `a sign that reads "OPEN"`, out.Prompt, name)
		require.Equal(t, "3:2", out.WHRatio, name)
	}

	bare := `{"rewritten_prompt": "a menu board reads "今日特调" in chalk.\nWarm light.", "wh_ratio": "", "ratio_follow": "<image1>"}`
	out, ok := extractQwenPE(bare)
	require.True(t, ok, "裸引号要靠兜底恢复")
	require.Equal(t, "a menu board reads \"今日特调\" in chalk.\nWarm light.", out.Prompt)
	require.Equal(t, "<image1>", out.RatioFollow)

	_, ok = extractQwenPE("The image is a vertical photograph of a cat.")
	require.False(t, ok, "没有 JSON 就是失败,不能把散文当结果")
}

func TestQwenPEIssues(t *testing.T) {
	ok := func(p, wh, rf string) qwenPEOutput { return qwenPEOutput{Prompt: p, WHRatio: wh, RatioFollow: rf} }

	errs, ratio := qwenPEIssues(ok("a cat", "3:2", ""), 0, "一只猫", false)
	require.Empty(t, errs)
	require.Empty(t, ratio)

	_, ratio = qwenPEIssues(ok("a cat", "", ""), 0, "一只猫", false)
	require.NotEmpty(t, ratio, "文生图缺 wh_ratio")
	_, ratio = qwenPEIssues(ok("a cat", "", ""), 0, "一只猫", true)
	require.Empty(t, ratio, "客户传了 size 时比例字段不用,不查")

	_, ratio = qwenPEIssues(ok("edit", "3:2", "<image1>"), 1, "x", false)
	require.NotEmpty(t, ratio, "编辑时两个比例字段只能有一个")
	_, ratio = qwenPEIssues(ok("<image1> <image2> edit", "", "<image3>"), 2, "x", false)
	require.NotEmpty(t, ratio, "ratio_follow 指向不存在的图")

	errs, _ = qwenPEIssues(ok("put <image2> into the poster", "", "<image1>"), 2, "x", false)
	require.Len(t, errs, 1, "多图时每张都要被指到")
	require.Contains(t, errs[0], "<image1>")

	errs, _ = qwenPEIssues(ok("<image1> and <image3>", "3:2", ""), 1, "x", false)
	require.Contains(t, strings.Join(errs, ";"), "<image3>", "不能引用不存在的图")
}

// **只查用户明确要画进图里的文字**,风格词不查 —— 它们正常会被译成英文描述。
func TestQwenPEMissingRenderedText(t *testing.T) {
	user := `一块霓虹灯招牌，上面写着"通义千问"四个大字，"赛博朋克"风格`
	require.Empty(t, qwenPEMissingText(user, `A neon sign reads "通义千问" in a cyberpunk street.`))
	require.Equal(t, []string{"通义千问"}, qwenPEMissingText(user, `A neon sign reads "蘭亭" in a cyberpunk street.`))

	en := `A movie poster titled "IMAGINATION UNLEASHED", subtitle "Enter a world beyond"`
	require.Empty(t, qwenPEMissingText(en,
		`The title reads "IMAGINATION UNLEASHED"; a tagline reads "ENTER A WORLD BEYOND".`),
		"全大写排版不算改字")
}

func TestQwenPESizeForRatio(t *testing.T) {
	require.Equal(t, "2528x1696", qwenPESizeForRatio(3, 2))
	require.Equal(t, "2752x1536", qwenPESizeForRatio(16, 9))
	require.Equal(t, "2048x2048", qwenPESizeForRatio(600, 600), "约分后查表")

	w, h, ok := common.DimsFromSize(qwenPESizeForRatio(21, 9))
	require.True(t, ok)
	require.Zero(t, w%16)
	require.Zero(t, h%16)
	require.InDelta(t, 21.0/9.0, float64(w)/float64(h), 0.02, "表外比例要保持比例")
	require.InDelta(t, 2048*2048, w*h, 2048*2048*0.03, "面积约 2048²")

	s, err := qwenPESize(qwenPEOutput{RatioFollow: "<image2>"},
		[]string{pngDataURI(t, 10, 10), pngDataURI(t, 300, 200)})
	require.NoError(t, err)
	require.Equal(t, "2528x1696", s, "ratio_follow 跟随被引用那张图(3:2)")

	s, err = qwenPESize(qwenPEOutput{RatioFollow: "<image1>"}, []string{"data:image/png;base64,bm90IGFuIGltYWdl"})
	require.Error(t, err)
	require.Empty(t, s, "读不到图就不定 size")
}

func TestQwenPEFixedRatio(t *testing.T) {
	require.Equal(t, "16:9", qwenPEFixedRatio("2752x1536"), "官方 2K 尺寸按标准比例说,不说 43:24")
	require.Equal(t, "16:9", qwenPEFixedRatio("16:9"))
	require.Equal(t, "1:1", qwenPEFixedRatio("1024x1024"))
	require.Empty(t, qwenPEFixedRatio("2K"), "档位词说不出比例,不声明")
	require.Empty(t, qwenPEFixedRatio(""))
}

// 文生图:用改写给的 wh_ratio 定尺寸,且请求里不带 response_format。
func TestQwenPEEnhanceSetsSize(t *testing.T) {
	got := fakeSequence(t, chatResponse(qpReply(map[string]any{"rewritten_prompt": "A wide photo of a cat.", "wh_ratio": "16:9"})))

	res := EnhancePrompt(context.Background(), qpCfg(), "Bearer sk-x", EnhanceInput{Prompt: "一只猫"})

	require.False(t, res.Degraded, res.DegradeReason)
	require.Equal(t, common.EnhanceModeQwenPE, res.Mode)
	require.Equal(t, "A wide photo of a cat.", res.EnhancedPrompt)
	require.Equal(t, "2752x1536", res.Size)
	require.Len(t, *got, 1)
	require.NotContains(t, string((*got)[0]), "response_format",
		"qwen3.8(MTP)+json_object 约一半请求被引擎终止,不能带")
	require.Contains(t, string((*got)[0]), `"enable_thinking":false`)
}

// **客户传了 size 就以接口为准**:不另定尺寸,并把画幅写进用户消息,让改写按它构图。
func TestQwenPEClientSizeIsDeclaredAndKept(t *testing.T) {
	got := fakeSequence(t, chatResponse(qpReply(map[string]any{"rewritten_prompt": "A wide photo.", "wh_ratio": "16:9"})))

	res := EnhancePrompt(context.Background(), qpCfg(), "Bearer sk-x",
		EnhanceInput{Prompt: "一只猫，1:1 方形构图", ClientSize: "2752x1536"})

	require.False(t, res.Degraded, res.DegradeReason)
	require.Empty(t, res.Size, "客户传了 size,不能另定")
	user := requestUserText(t, (*got)[0])
	require.True(t, strings.HasPrefix(user, "一只猫，1:1 方形构图"), "原话在前")
	require.Contains(t, user, "fixed by the caller at 16:9")
}

// 第一轮漏了图标签 → 重修成功:用重修的结果,usage 两轮累加,重修请求带着错误清单。
func TestQwenPERepairAccumulatesUsage(t *testing.T) {
	bad := qpReply(map[string]any{"rewritten_prompt": "Put the perfume into the poster.", "wh_ratio": "", "ratio_follow": "<image1>"})
	good := qpReply(map[string]any{"rewritten_prompt": "Put the perfume from <image2> into <image1>.", "wh_ratio": "", "ratio_follow": "<image1>"})
	got := fakeSequence(t, chatResponse(bad), chatResponse(good))

	res := EnhancePrompt(context.Background(), qpCfg(), "Bearer sk-x", EnhanceInput{
		Prompt: "把图2的香水放到图1海报中间", ImageURLs: []string{pngDataURI(t, 200, 300), pngDataURI(t, 100, 100)},
	})

	require.False(t, res.Degraded, res.DegradeReason)
	require.True(t, res.Repaired)
	require.Equal(t, "Put the perfume from <image2> into <image1>.", res.EnhancedPrompt)
	require.Equal(t, "1696x2528", res.Size, "跟随 <image1>(2:3)")
	require.Equal(t, 60, res.Usage.TotalTokens, "两轮各 30,必须累加")
	require.Len(t, *got, 2)
	require.Contains(t, string((*got)[1]), "Refer to every input image by its tag")
	require.Contains(t, string((*got)[1]), "Repair ONLY these transport errors")
}

// 两轮都拿不到 JSON → 降级为原始提示词,不定 size、不回落 text。
func TestQwenPEDegradesAfterTwoFailures(t *testing.T) {
	got := fakeSequence(t, chatResponse("Let me work through the steps."), chatResponse("Still thinking."))

	res := EnhancePrompt(context.Background(), qpCfg(), "Bearer sk-x", EnhanceInput{Prompt: "一只猫"})

	require.True(t, res.Degraded)
	require.Equal(t, "一只猫", res.EnhancedPrompt)
	require.Empty(t, res.Size)
	require.Contains(t, res.DegradeReason, "rewritten_prompt")
	require.Len(t, *got, 2, "首轮 + 一轮重修,不再回落 text")
}

// 最后一轮只剩比例字段有问题:提示词可用,别丢 —— 接受它、不定 size。
func TestQwenPEAcceptsPromptWhenOnlyRatioFails(t *testing.T) {
	noRatio := qpReply(map[string]any{"rewritten_prompt": "A cat on a windowsill."})
	fakeSequence(t, chatResponse(noRatio), chatResponse(noRatio))

	res := EnhancePrompt(context.Background(), qpCfg(), "Bearer sk-x", EnhanceInput{Prompt: "一只猫"})

	require.False(t, res.Degraded, res.DegradeReason)
	require.Equal(t, "A cat on a windowsill.", res.EnhancedPrompt)
	require.Empty(t, res.Size)
	require.True(t, res.Repaired)
}

func TestDryRunQwenPE(t *testing.T) {
	withFakeModels(t, map[string]fakeModel{"qwen-image-pro": {}, "enh": {}})

	res := DryRunAggregateModel(qpCfg(), map[string]int{"qwen-image-pro-enhanced": 1})
	require.Nil(t, checkByKey(res, "enhance_template"), "qwen_pe 自带提示词,不该报缺模板")
	require.Nil(t, checkByKey(res, "enhance_mode"), "合法的 qwen_pe 不该有模式告警")
	require.True(t, res.Passed, "%+v", res.Checks)

	video := qpCfg()
	video.Type = "video"
	requireLevel(t, DryRunAggregateModel(video, map[string]int{"qwen-image-pro-enhanced": 1}),
		"enhance_mode", AggregateCheckError)

	withPrompt := qpCfg()
	withPrompt.PromptEnhance.SystemPrompt = "改写"
	requireLevel(t, DryRunAggregateModel(withPrompt, map[string]int{"qwen-image-pro-enhanced": 1}),
		"enhance_mode", AggregateCheckWarn)
}
