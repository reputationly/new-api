package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relay/hilo"
)

func evidenceOf(in hilo.CompilerInput) singleCallEvidence { return buildSingleCallEvidence(in) }

// **素材编号必须和素材实际发出去的顺序一致。**
//
// 两边各编一套的后果是静默的：提示词里写 <Picture 2>，而 H3 收到的第二张
// 图是另一张 —— 生成出来的东西不对，但没有任何地方报错。
func TestEvidenceLabelsMatchRenderNumbering(t *testing.T) {
	ev := evidenceOf(hilo.CompilerInput{
		TaskType:        hilo.TaskR2VA,
		DurationSeconds: 5,
		Assets: []hilo.CompilerAsset{
			{AssetID: "image_1", MediaType: "image", Role: "reference"},
			{AssetID: "video_1", MediaType: "video", Role: "reference"},
			{AssetID: "image_2", MediaType: "image", Role: "reference"},
		},
	})
	want := []string{"<Picture 1>", "<Video 1>", "<Picture 2>"}
	for i, a := range ev.Assets {
		if a.OfficialLabel != want[i] {
			t.Errorf("第 %d 个素材标号 %q，期望 %q（图片和视频各自计数、按提交顺序）",
				i, a.OfficialLabel, want[i])
		}
	}
}

// 镜头时间是唯一真正"硬"的检查：加起来不等于请求时长时 H3 照样生成，
// 只是最后一段被拉长或截断，没有任何地方报错。
func TestShotTimingIssues(t *testing.T) {
	f := func(v float64) flexSeconds { return flexSeconds{Value: v, Set: true} }
	ok := []singleCallShot{{StartSeconds: f(0), EndSeconds: f(2)}, {StartSeconds: f(2), EndSeconds: f(5)}}
	if errs := shotTimingIssues(ok, 5); len(errs) != 0 {
		t.Errorf("合法的镜头计划被拒: %v", errs)
	}

	bad := map[string][]singleCallShot{
		"空数组":        {},
		"没到请求时长":     {{StartSeconds: f(0), EndSeconds: f(3)}},
		"超过请求时长":     {{StartSeconds: f(0), EndSeconds: f(7)}},
		"中间有空档":      {{StartSeconds: f(0), EndSeconds: f(2)}, {StartSeconds: f(3), EndSeconds: f(5)}},
		"不是从 0 开始":   {{StartSeconds: f(1), EndSeconds: f(5)}},
		"结束早于开始":     {{StartSeconds: f(0), EndSeconds: f(0)}},
		"缺 start 字段": {{EndSeconds: f(5)}},
	}
	for name, shots := range bad {
		if errs := shotTimingIssues(shots, 5); len(errs) == 0 {
			t.Errorf("%s：该被拦下却放过了", name)
		}
	}
}

// **缺字段不能被当成 0。** Go 的零值会把「没写 start_seconds」伪装成
// 「从 0 开始」，于是一份残缺的计划看起来完全合法。上游用 KeyError 区分，
// 我们用指针。
func TestMissingShotFieldIsNotZero(t *testing.T) {
	var plan singleCallPlan
	if err := json.Unmarshal([]byte(`{"shots":[{"end_seconds":5}]}`), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Shots[0].StartSeconds.Set {
		t.Fatal("缺失的 start_seconds 不该被标记为已设置")
	}
	if errs := shotTimingIssues(plan.Shots, 5); len(errs) == 0 {
		t.Error("缺 start_seconds 的镜头被当成了「从 0 开始」")
	}
}

// 提示词引用了不存在的素材：H3 拿不到第三张图，那一段描述落空，不报错。
func TestLabelIssues(t *testing.T) {
	assets := []singleCallEvidenceAsset{
		{AssetID: "image_1", MediaType: "image"},
		{AssetID: "video_1", MediaType: "video"},
	}
	if errs := labelIssues("uses <Picture 1> and <Video 1>", assets); len(errs) != 0 {
		t.Errorf("合法引用被拒: %v", errs)
	}
	if errs := labelIssues("uses <Picture 2>", assets); len(errs) == 0 {
		t.Error("只传了一张图却引用 <Picture 2>，没被拦")
	}
	if errs := labelIssues("uses <Video 3>", assets); len(errs) == 0 {
		t.Error("引用了不存在的 <Video 3>，没被拦")
	}
}

// **按玩法选节名单，不能一律用六节。**
//
// 上游无论什么玩法都拿 ref2va 那六节去比，于是每个 t2v 请求都会得到一条
// 必然为真的警告。警告恒真就等于没有 —— 真出问题时没人会多看一眼。
func TestSectionWarningsByTaskType(t *testing.T) {
	t2v := "integrated_multimodal_description: [Shot 1] ...\n\noverall_soundscape: ...\n\nnon_diegetic_music: N/A"
	if w := sectionWarnings(t2v, "t2v"); len(w) != 0 {
		t.Errorf("t2v 的三节齐全却报警告: %v", w)
	}
	if w := sectionWarnings(t2v, "r2va"); len(w) == 0 {
		t.Error("r2va 缺 subject_definitions 等节，应该警告")
	}
	if w := sectionWarnings("随便写的一段话", "t2v"); len(w) == 0 {
		t.Error("三节一个都没有，应该警告")
	}
}

// 传输检查通过时不该有任何错误；bindings 引用不存在的素材要拦。
func TestTransportIssues(t *testing.T) {
	f := func(v float64) flexSeconds { return flexSeconds{Value: v, Set: true} }
	ev := evidenceOf(hilo.CompilerInput{
		TaskType: hilo.TaskT2V, DurationSeconds: 5,
		Assets: []hilo.CompilerAsset{{AssetID: "image_1", MediaType: "image"}},
	})
	good := &singleCallOutput{
		H3Prompt: "integrated_multimodal_description: [Shot 1] x\n\noverall_soundscape: y\n\nnon_diegetic_music: N/A",
		plan: &singleCallPlan{
			Bindings: flexBindings{Items: []singleCallBinding{{AssetID: "image_1"}}},
			Shots:    []singleCallShot{{StartSeconds: f(0), EndSeconds: f(5)}},
		},
	}
	if errs, warns := transportIssues(good, ev); len(errs) != 0 || len(warns) != 0 {
		t.Errorf("合法产物被拒: errs=%v warns=%v", errs, warns)
	}

	bad := *good
	bad.plan = &singleCallPlan{
		Bindings: flexBindings{Items: []singleCallBinding{{AssetID: "image_9"}}},
		Shots:    good.plan.Shots,
	}
	errs, _ := transportIssues(&bad, ev)
	if len(errs) == 0 || !strings.Contains(strings.Join(errs, ";"), "image_9") {
		t.Errorf("绑定了不存在的素材却没被拦: %v", errs)
	}

	empty := *good
	empty.H3Prompt = "   "
	if errs, _ := transportIssues(&empty, ev); len(errs) == 0 {
		t.Error("空提示词没被拦")
	}

	// **必须经由 transportIssues 走一遍素材编号检查。**
	// 只在 TestLabelIssues 里直接调那个函数，证明不了它被接上了 ——
	// 把调用点删掉，那种测试照样全绿（变异校准里真漏过一次）。
	ghost := *good
	ghost.H3Prompt = "integrated_multimodal_description: [Shot 1] uses <Picture 3>\n\n" +
		"overall_soundscape: y\n\nnon_diegetic_music: N/A"
	errs, _ = transportIssues(&ghost, ev)
	if len(errs) == 0 || !strings.Contains(strings.Join(errs, ";"), "Picture") {
		t.Errorf("只传了一张图，提示词却引用 <Picture 3>，transportIssues 没拦: %v", errs)
	}

	// 镜头时间同样要经由入口走一遍。
	shortPlan := *good
	shortPlan.plan = &singleCallPlan{
		Bindings: good.plan.Bindings,
		Shots:    []singleCallShot{{StartSeconds: f(0), EndSeconds: f(3)}}, // 请求 5 秒
	}
	errs, _ = transportIssues(&shortPlan, ev)
	if len(errs) == 0 || !strings.Contains(strings.Join(errs, ";"), "duration") {
		t.Errorf("镜头只覆盖到 3 秒（请求 5 秒），transportIssues 没拦: %v", errs)
	}

	// 节名警告也要经由入口 —— 它是 warns 那一路，单独测函数证明不了接线。
	noSections := *good
	noSections.H3Prompt = "就是一段大白话，没有任何官方节名"
	_, warns := transportIssues(&noSections, ev)
	if len(warns) == 0 {
		t.Error("提示词里一个官方节名都没有，transportIssues 没给出警告")
	}

	// 台词检查同样要经由 transportIssues 走一遍，理由同上。
	evD := evidenceOf(hilo.CompilerInput{
		TaskType: hilo.TaskT2V, DurationSeconds: 5,
		UserRequest: `她转身说「你好」`,
	})
	translated := *good
	translated.H3Prompt = "integrated_multimodal_description: [Shot 1] She says: <d>[English] Hello.</d>\n\n" +
		"overall_soundscape: y\n\nnon_diegetic_music: N/A"
	translated.plan = &singleCallPlan{Shots: []singleCallShot{{StartSeconds: f(0), EndSeconds: f(5)}}}
	errs, _ = transportIssues(&translated, evD)
	if len(errs) == 0 || !strings.Contains(strings.Join(errs, ";"), "你好") {
		t.Errorf("台词被翻译，transportIssues 没拦: %v", errs)
	}
}

// 移植进来的两段提示词必须真的被带进指令里 —— go:embed 漏了或路径写错时
// 编译期不报错，运行时只是少了一整套规则，而输出看起来仍然像模像样。
func TestEmbeddedRulesArePresent(t *testing.T) {
	for name, body := range map[string]string{"rules": singleCallRules, "writing": singleCallWriting} {
		if len(body) < 5000 {
			t.Errorf("%s 只有 %d 字节，移植的原文没被带进来", name, len(body))
		}
	}
	// 上游原话里的锚点，改动后能立刻看出是否还是那份文本
	if !strings.Contains(singleCallRules, "Compile bindings, shot states and final wording in this ONE response") {
		t.Error("rules.txt 不是上游原文")
	}
	if !strings.Contains(singleCallWriting, "do not introduce an audit stage") {
		t.Error("writing.txt 不是上游原文")
	}
	if !strings.HasSuffix(strings.TrimRight(singleCallWriting, "\n"), "Evidence follows:") {
		t.Error("writing.txt 结尾必须是 'Evidence follows:'，证据要紧跟其后")
	}
}

// **形态漂移不能丢掉好提示词。**
//
// 这是这套解码存在的唯一理由。encoding/json 对已声明的字段是严格的，
// 早先只声明 bindings/shots 两个字段并不能换来宽容 —— 模型把
// start_seconds 写成字符串，整份反序列化就失败，连同一段完全可用的
// h3_prompt 一起丢掉，白烧一轮重修，最后静默回落 text。
// IR 那条路上就是这么栽的（sync_rules 被写成对象数组）。
func TestShapeDriftKeepsPrompt(t *testing.T) {
	cases := map[string]string{
		"start_seconds 是字符串": `{"h3_prompt":"kept","content_plan":{"shots":[{"start_seconds":"0","end_seconds":"5"}]}}`,
		"bindings 写成单个对象":    `{"h3_prompt":"kept","content_plan":{"bindings":{"asset_id":"image_1"},"shots":[]}}`,
		"content_plan 是字符串":  `{"h3_prompt":"kept","content_plan":"nope"}`,
		"多了没见过的字段":           `{"h3_prompt":"kept","content_plan":{"shots":[],"brand_new":{"x":1}}}`,
	}
	for name, body := range cases {
		out, err := extractSingleCall(body)
		if err != nil {
			t.Errorf("%s：整份被丢弃了，h3_prompt 白写: %v", name, err)
			continue
		}
		if out.H3Prompt != "kept" {
			t.Errorf("%s：h3_prompt 没保住，实得 %q", name, out.H3Prompt)
		}
	}

	// **容忍形态，不容忍语义。** 单个对象写法要真的被解成一条绑定，
	// 而不是悄悄当成"没有绑定" —— 那样它引用的 asset_id 就绕过了校验。
	out, err := extractSingleCall(
		`{"h3_prompt":"kept","content_plan":{"bindings":{"asset_id":"image_1"},"shots":[]}}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.plan.Bindings.Items) != 1 || out.plan.Bindings.Items[0].AssetID != "image_1" {
		t.Errorf("bindings 写成单个对象时没被解出来: %+v", out.plan.Bindings)
	}
}

// 数字字符串要能解出来——那是最常见的一种漂移，解不出就白丢一轮重修。
func TestFlexSecondsAcceptsNumericString(t *testing.T) {
	var p singleCallPlan
	if err := json.Unmarshal([]byte(`{"shots":[{"start_seconds":"0","end_seconds":"5.5"}]}`), &p); err != nil {
		t.Fatal(err)
	}
	s := p.Shots[0]
	if !s.StartSeconds.Set || !s.EndSeconds.Set || s.EndSeconds.Value != 5.5 {
		t.Fatalf("数字字符串没解出来: %+v", s)
	}
}

// content_plan 不是对象时要报具名问题，而不是整次编译作废。
func TestPlanNotAnObjectIsATransportError(t *testing.T) {
	out, err := extractSingleCall(`{"h3_prompt":"x","content_plan":"nope"}`)
	if err != nil {
		t.Fatal(err)
	}
	errs, _ := transportIssues(out, evidenceOf(hilo.CompilerInput{TaskType: hilo.TaskT2V, DurationSeconds: 5}))
	if len(errs) == 0 || !strings.Contains(strings.Join(errs, ";"), "content_plan") {
		t.Errorf("content_plan 不是对象却没报出来: %v", errs)
	}
}

// **生产记录里只能有 content_plan。**
//
// 早先存的是整份模型回复，于是名字叫 content_plan 的那一列里躺着
// h3_prompt 的副本 —— 而排障正是靠这两列对照「模型把什么当成 must_keep、
// 最终写成了什么」，混在一起就对照不了。
func TestPlanJSONHoldsOnlyContentPlan(t *testing.T) {
	out, err := extractSingleCall(`{"h3_prompt":"FINAL PROMPT","content_plan":{"must_keep":["cat"]},"uncertainties":[]}`)
	if err != nil {
		t.Fatal(err)
	}
	plan := out.planJSON()
	if strings.Contains(plan, "FINAL PROMPT") {
		t.Errorf("生产记录里混进了 h3_prompt: %s", plan)
	}
	if !strings.Contains(plan, "must_keep") {
		t.Errorf("生产记录里没有 content_plan 的内容: %s", plan)
	}
}

// **台词不能被翻译。**
//
// 正文改用英文之后，把「你好」顺手写成 "Hello" 是模型最自然的动作。
// 而这是这一步最难发现的错：成片口型对得上、时长对得上、读起来完全正常，
// 只有语言换了 —— 用户要的恰恰是那句话。
func TestDialogueMustSurviveVerbatim(t *testing.T) {
	req := `一个女孩转身对镜头说「你好，很高兴见到你」，然后挥手`

	translated := `[Shot 1] A girl turns to camera and says: <d>[English] Hello, nice to meet you.</d>`
	if errs := dialogueIssues(req, translated); len(errs) == 0 {
		t.Error("台词被翻译成英文却没被拦下")
	}

	kept := `[Shot 1] A girl turns to camera and says: <d>[Chinese] 你好，很高兴见到你</d> then waves.`
	if errs := dialogueIssues(req, kept); len(errs) != 0 {
		t.Errorf("台词原样保留了却被拦: %v", errs)
	}
}

// **引号里的风格词/强调词不是台词。**
//
// 中文提示词里引号最常见的用途就是这个。误报的代价不是"白等一次往返"，
// 是让这条检查对一大类最常见的输入实际不可用：要么把"赛博朋克"包进
// <d>[Chinese] …</d> 塞进最终提示词，要么重修还是不过、静默回落 text。
func TestDialogueCheckIgnoresStyleKeywords(t *testing.T) {
	out := "[Shot 1] A neon-lit city street at night, cinematic."
	for _, req := range []string{
		`生成一段"赛博朋克"风格的城市夜景`,
		`背景是"虚化"的树林`,
		`要「电影感」的调色`,
		`一只橘猫在窗台上打哈欠`,
		"a white dog running in snow",
	} {
		if errs := dialogueIssues(req, out); len(errs) != 0 {
			t.Errorf("不是台词却触发了重修: %q → %v", req, errs)
		}
	}
}

// 英文台词不在这道检查的职责内：被改写成另一句英文属于语义问题，
// 传输检查查不出来，也不该假装能查。
func TestDialogueCheckSkipsEnglish(t *testing.T) {
	req := `She says "Wait for me" and runs.`
	if errs := dialogueIssues(req, "[Shot 1] She says: <d>[English] Hold on.</d>"); len(errs) != 0 {
		t.Errorf("英文台词不该由这道检查负责: %v", errs)
	}
}

// **bindings 解不动时必须报出来，不能静默放行。**
//
// 只把列表置空的话，整道绑定校验会变成空操作 —— "没有绑定"当然挑不出
// 毛病，于是一份引用了不存在素材的计划照样编译成功。上游本来就有这条
// （`bindings must be an array`），移植时漏掉了。
func TestMalformedBindingsAreReported(t *testing.T) {
	out, err := extractSingleCall(
		`{"h3_prompt":"x","content_plan":{"bindings":"nope","shots":[{"start_seconds":0,"end_seconds":5}]}}`)
	if err != nil {
		t.Fatal(err)
	}
	if !out.plan.Bindings.Malformed {
		t.Fatal("既不是数组也不是对象的 bindings 没被标记")
	}
	errs, _ := transportIssues(out, evidenceOf(hilo.CompilerInput{TaskType: hilo.TaskT2V, DurationSeconds: 5}))
	if len(errs) == 0 || !strings.Contains(strings.Join(errs, ";"), "bindings must be an array") {
		t.Errorf("bindings 形态不对却没报出来: %v", errs)
	}
}

// **官方规范必须真的进到系统提示词里。**
//
// writing.txt 写着 "Follow the official H3 writing guide"，但普通 chat
// 端点没有文件系统工具，模型执行不了"去读那份指南"。漏掉的后果是静默
// 降质：模型只能凭记忆写，三节名称、[Shot N] 记法、<d>[Language] 台词
// 标签全无从谈起 —— 而产出看起来仍然像模像样。
func TestSystemPromptCarriesOfficialGuides(t *testing.T) {
	sys := buildSingleCallSystem("t2v", `{"task":{}}`)

	must := map[string]string{
		"三节名(base-en)":         "integrated_multimodal_description",
		"台词标签写法":               "<d>",
		"运镜词表":                 "Push In / Pull Out",
		"skill 的 Output Rules": "Write rewrite sections in English",
		"v20 规则":               "Compile bindings, shot states and final wording in this ONE response",
		"写作说明":                 "do not introduce an audit stage",
		"分镜 skill":             "h3-shot-planning",
	}
	for name, anchor := range must {
		if !strings.Contains(sys, anchor) {
			t.Errorf("系统提示词里缺 %s（锚点 %q）", name, anchor)
		}
	}
	// 证据必须紧跟在 "Evidence follows:" 之后
	i := strings.LastIndex(sys, "Evidence follows:")
	if i < 0 || !strings.Contains(sys[i:], `{"task":{}}`) {
		t.Error("证据没有紧跟在 'Evidence follows:' 后面")
	}
}

// ref-en.txt 有 23 KB 且只讲全参考那六节；帧族用不上，而这段提示词是
// **每个请求**都要发一遍的。
func TestRefGuideOnlySentForReferenceTasks(t *testing.T) {
	// 锚点必须是 ref-en.txt **独有**的：六个节名在 prompt-writing 的
	// SKILL.md 里也被列出来（它同时描述两种模式），拿节名当锚点会恒真。
	const refOnly = "Full-Reference Mode Rewrite Output Format Guide"
	t2v := buildSingleCallSystem("t2v", "{}")
	if strings.Contains(t2v, refOnly) {
		t.Error("t2v 也发了 ref-en.txt，每个请求白花 23 KB")
	}
	r2va := buildSingleCallSystem("r2va", "{}")
	if !strings.Contains(r2va, refOnly) {
		t.Error("参考族没发 ref-en.txt，六节规范模型看不到")
	}
	// base-en 一律要发：参考族也要用它的台词与运镜规则（上游注释点明）
	for name, sys := range map[string]string{"t2v": t2v, "r2va": r2va} {
		if !strings.Contains(sys, "integrated_multimodal_description") {
			t.Errorf("%s 没发 base-en.txt", name)
		}
	}
}

// ── 从已删除的 IR 测试里移植过来 ──────────────────────────────────
//
// 这些工具函数随 IR 架构一起搬进了 singlecall（jsonCandidates /
// extractJSONObject / stripCodeFence），但**它们的测试跟着 IR 的测试
// 文件一起被删了** —— 代码搬了、护栏没搬。而它们守的恰恰是
// extractSingleCall 这条出厂默认路径：围栏、JSON 后面拖着的散文、
// 以及模型偶发多吐的那个游离花括号。破了的表现是**静默回落 text**。

// chatResponse 把一段内容包成 chat completions 的回复。
func chatResponse(content string) string {
	b, _ := json.Marshal(map[string]any{
		"choices": []map[string]any{{"message": map[string]any{"content": content}}},
		"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 20, "total_tokens": 30},
	})
	return string(b)
}

// fakeSequence 按顺序返回预置回复的假上游,并录下每次收到的请求体。
//
// 编译天然是多轮的(第一轮传输检查不过 → 重修 → 仍不过 → 回落 text),
// 只能返回一种回复的假服务端连"重修成功"这条路都走不到。
func fakeSequence(t *testing.T, responses ...string) *[][]byte {
	t.Helper()
	var got [][]byte
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = append(got, b)
		w.Header().Set("Content-Type", "application/json")
		idx := n
		if idx >= len(responses) {
			idx = len(responses) - 1
		}
		n++
		_, _ = w.Write([]byte(responses[idx]))
	}))
	t.Cleanup(srv.Close)
	orig := enhanceEndpoint
	t.Cleanup(func() { enhanceEndpoint = orig })
	enhanceEndpoint = func() string { return srv.URL }
	return &got
}

// 模型很少只吐一个干净的 JSON。要了 response_format 也只是大概率 ——
// 不认这个字段的上游会**静默忽略**它,所以这里必须自己扛住。
func TestExtractJSONObjectTolerates(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"裸对象", `{"a":1}`, `{"a":1}`},
		{"围栏", "```json\n{\"a\":1}\n```", `{"a":1}`},
		{"前置寒暄", "Here is the IR:\n{\"a\":1}", `{"a":1}`},
		{"后置补话", "{\"a\":1}\n以上就是 IR。如需调整请告知 {不是 JSON}", `{"a":1}`},
		{"嵌套对象", `{"a":{"b":2}}`, `{"a":{"b":2}}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, extractJSONObject(c.in))
		})
	}
}

// **字符串里的花括号不算数。**
//
// 提示词正文里出现 `}` 完全正常。不跳过引号的话会在第一个带花括号的
// 描述那里提前收尾,截出来的片段解析失败 —— 一次本来成功的编译白跑,
// 而且报的是"IR 解析失败",指不到真正的原因。
func TestExtractJSONObjectIgnoresBracesInStrings(t *testing.T) {
	in := `{"event":"a sign reading } end", "next":1}`
	require.Equal(t, in, extractJSONObject(in))
}

// 花括号没配平 = 输出被截断,解析也不会成功,直接判定取不到。
func TestExtractJSONObjectRejectsTruncated(t *testing.T) {
	require.Empty(t, extractJSONObject(`{"a":{"b":2}`))
	require.Empty(t, extractJSONObject(`没有任何 JSON`))
}

// 转义引号不能被当成字符串结束。
func TestExtractJSONObjectHandlesEscapedQuote(t *testing.T) {
	in := `{"event":"she said \"} \" and left","n":1}`
	require.Equal(t, in, extractJSONObject(in))
	require.True(t, json.Valid([]byte(extractJSONObject(in))))
}

func TestStripCodeFence(t *testing.T) {
	require.Equal(t, `{"a":1}`, stripCodeFence("```json\n{\"a\":1}\n```"))
	require.Equal(t, `{"a":1}`, stripCodeFence("```\n{\"a\":1}\n```"))
	require.Equal(t, `{"a":1}`, stripCodeFence(`{"a":1}`), "没有围栏时原样返回")
}

// scEvidence 一份最小但合法的编译请求事实。
func scInput(prompt string) EnhanceInput {
	return EnhanceInput{
		Prompt: prompt,
		Compiler: &hilo.CompilerInput{
			UserRequest: prompt, TaskType: hilo.TaskT2V,
			DurationSeconds: 5, GenerateAudio: true,
		},
	}
}

func scCfg() *common.AggregateModel {
	return &common.AggregateModel{
		Name: "agg", Type: "video",
		PromptEnhance: &common.AggregatePromptEnhance{
			Model: "enh", Mode: common.EnhanceModeSingleCall,
			SystemPrompt: "文本模板:改写以下提示词",
		},
	}
}

// scReply 一份能过传输检查的 singlecall 产物。
func scReply(prompt string) string {
	b, _ := json.Marshal(map[string]any{
		"h3_prompt": prompt,
		"content_plan": map[string]any{
			"bindings": []any{},
			"shots":    []any{map[string]any{"start_seconds": 0, "end_seconds": 5}},
		},
		"uncertainties": []any{},
	})
	return string(b)
}

// **模型偶发在开头多吐一个游离的花括号。**
//
// 实测五次中两次（qwen3.8-27b）。它看起来像"多包了一层"，其实不是：整串
// 净深度是 1，只有开头那个多余，结尾并没有多。所以按配平找结尾必然失败，
// 掐头去尾还会把真正的收尾括号削掉。
//
// 这条原先钉在已删除的 IR 解析上，但那个候选提取（jsonCandidates）被搬进了
// singlecall，守的是 extractSingleCall —— 出厂默认路径。
func TestExtractSingleCallRecoversStrayLeadingBrace(t *testing.T) {
	raw := scReply("integrated_multimodal_description: [Shot 1] x")
	strayed := "{" + raw // 只在开头多一个，结尾不动

	require.Empty(t, extractJSONObject(strayed),
		"净深度不为 0，按配平找结尾必然失败 —— 这正是需要候选提取的原因")

	out, err := extractSingleCall(strayed)
	require.NoError(t, err)
	require.Contains(t, out.H3Prompt, "[Shot 1]")
}

// 围栏与 JSON 后面拖着的散文都要能恢复。
func TestExtractSingleCallToleratesFenceAndProse(t *testing.T) {
	body := scReply("integrated_multimodal_description: [Shot 1] y")
	for name, raw := range map[string]string{
		"代码围栏":  "```json\n" + body + "\n```",
		"前面有话":  "好的，这是结果：\n" + body,
		"后面拖散文": body + "\n\n以上就是编译结果。",
	} {
		out, err := extractSingleCall(raw)
		if err != nil {
			t.Errorf("%s：解析失败 %v", name, err)
			continue
		}
		if !strings.Contains(out.H3Prompt, "[Shot 1]") {
			t.Errorf("%s：h3_prompt 没取出来", name)
		}
	}
}

// **编译失败要回落 text 改写，不是掉回原始提示词。**
//
// 直接掉到原文会让开着比不开还差，于是没人敢开，于是永远收不到真实失败
// 样本 —— 这是整套降级设计的由来。
func TestSingleCallFallsBackToTextNotToOriginal(t *testing.T) {
	bad, _ := json.Marshal(map[string]any{
		"h3_prompt": "x",
		// 镜头只覆盖到 3 秒，而请求是 5 秒 —— 传输检查必拦
		"content_plan": map[string]any{"bindings": []any{},
			"shots": []any{map[string]any{"start_seconds": 0, "end_seconds": 3}}},
	})
	got := fakeSequence(t,
		chatResponse(string(bad)), // 首轮不过
		chatResponse(string(bad)), // 重修仍不过
		chatResponse("文本改写的结果"),   // 第三次：text 模式
	)

	res := EnhancePrompt(context.Background(), scCfg(), "Bearer sk-x", scInput("一只猫"))

	require.False(t, res.Degraded, "回落 text 成功了就不算降级：%s", res.DegradeReason)
	require.Equal(t, common.EnhanceModeText, res.Mode)
	require.Contains(t, res.FallbackReason, "duration",
		"回落原因要能指认是哪条检查挡下的 —— 这是唯一的反馈来源")
	require.Equal(t, "文本改写的结果", res.EnhancedPrompt)
	require.Len(t, *got, 3, "两轮 singlecall + 一次 text")
	require.Contains(t, string((*got)[2]), "文本模板", "第三次该走文本模板")
}

// **usage 必须累加，不能只报最后一轮。**
//
// 重修轮跑掉的 token 客户已经被计过费了（每一轮都是一次真实 relay 调用），
// 只报最后一轮会让聚合日志里的用量小于实际扣费，对账时对不上。
func TestSingleCallAccumulatesUsageAcrossRepair(t *testing.T) {
	bad, _ := json.Marshal(map[string]any{
		"h3_prompt": "x",
		"content_plan": map[string]any{"bindings": []any{},
			"shots": []any{map[string]any{"start_seconds": 0, "end_seconds": 3}}},
	})
	good := scReply("integrated_multimodal_description: [Shot 1] z")
	fakeSequence(t, chatResponse(string(bad)), chatResponse(good))

	res := EnhancePrompt(context.Background(), scCfg(), "Bearer sk-x", scInput("一只猫"))

	require.Equal(t, common.EnhanceModeSingleCall, res.Mode)
	require.True(t, res.Repaired, "第一轮没过、靠重修才成功")
	require.NotNil(t, res.Usage)
	require.Equal(t, 60, res.Usage.TotalTokens, "两轮各 30，必须累加")
}
