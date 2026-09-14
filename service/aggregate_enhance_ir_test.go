package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relay/hilo"
)

// ── 夹具 ────────────────────────────────────────────────────────────

// validIRObject 一份能过校验、能渲染的 IR。
//
// 用结构体构造再 Marshal,而不是手写 JSON 字面量:手写的话字段名靠人眼
// 对齐 ir.go 的 tag,漏一个字母就是零值 —— 而零值往往恰好合法,测试照过,
// 真实路径上却少一段内容。
func validIRObject() *hilo.ContextIR {
	return &hilo.ContextIR{
		SchemaVersion: "0.1.0",
		Task:          hilo.IRTask{Type: string(hilo.TaskR2VA), DurationSeconds: 6, GenerateAudio: true},
		Protocol:      hilo.IRProtocol{RewriteLanguage: "English"},
		Subjects: []hilo.IRSubject{{
			SubjectID: "subject_1", Name: "the dancer", Description: "a young woman",
			SourceAssetIDs: []string{"image_1"}, AppearanceShotIDs: []string{"01"},
			RetentionMode: hilo.RetentionFull, RetentionDescription: "her identity is retained",
		}},
		AssetBindings: []hilo.IRAssetBinding{{AssetID: "image_1", Role: "identity"}},
		CreativeFocus: hilo.IRCreativeFocus{Objective: "she completes the turn", PrimarySubjectID: "subject_1"},
		Timeline: []hilo.IRShot{
			{ShotID: "01", StartSeconds: 0, EndSeconds: 3, Event: "she starts the turn",
				ObservableEndState: "mid-turn", SubjectRefs: []string{"subject_1"}},
			{ShotID: "02", StartSeconds: 3, EndSeconds: 6, Event: "she lands",
				ObservableEndState: "standing still"},
		},
		AudioPlan:             hilo.IRAudioPlan{AmbientSound: "studio room tone"},
		GenerationDescription: hilo.IRGenerationDescription{Cinematography: "handheld", Lighting: "soft"},
	}
}

func irJSON(t *testing.T, ir *hilo.ContextIR) string {
	t.Helper()
	b, err := json.Marshal(ir)
	require.NoError(t, err)
	return string(b)
}

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
// IR 模式天然是多轮的(第一轮不过 → 重修 → 仍不过 → 回落 text),
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

func irCfg(model string) *common.AggregateModel {
	return &common.AggregateModel{
		Name: "agg", Type: "video",
		PromptEnhance: &common.AggregatePromptEnhance{
			Model: model, Mode: common.EnhanceModeIR,
			SystemPrompt: "文本模板:改写以下提示词",
		},
		Generate: common.AggregateGenerate{Model: "minimax-h3-fl2va"},
	}
}

func irInput(ir *hilo.ContextIR) EnhanceInput {
	return EnhanceInput{
		Prompt: "a cat",
		Compiler: &hilo.CompilerInput{
			UserRequest:     "a cat",
			TaskType:        hilo.TaskType(ir.Task.Type),
			DurationSeconds: ir.Task.DurationSeconds,
			GenerateAudio:   ir.Task.GenerateAudio,
			Assets: []hilo.CompilerAsset{
				{AssetID: "image_1", MediaType: "image", Role: "reference"},
			},
		},
	}
}

// ── IR 主路径 ───────────────────────────────────────────────────────

// IR 模式下最终提示词是**渲染出来的**,不是模型那段自由文本。
//
// 这是整件事的目的:模型只出结构化判断,成品文本由代码确定性地生成。
// 若最终提示词等于模型回复原文,说明分支根本没走进 IR 那条路。
func TestEnhanceIRModeRendersFromIR(t *testing.T) {
	ir := validIRObject()
	raw := irJSON(t, ir)
	fakeSequence(t, chatResponse(raw))

	res := EnhancePrompt(context.Background(), irCfg("qwen3.8-27b"), "Bearer sk-x", irInput(ir))

	require.False(t, res.Degraded, "IR 成功不该降级: %s", res.DegradeReason)
	require.Equal(t, common.EnhanceModeIR, res.Mode)
	require.Empty(t, res.IRFallbackReason)
	require.NotEqual(t, raw, res.EnhancedPrompt, "最终提示词不该是模型那段 JSON 原文")
	// r2va 走六段式,段落名是 H3 认的协议字面量。
	require.Contains(t, res.EnhancedPrompt, "retention_analysis:")
	require.Contains(t, res.EnhancedPrompt, "the dancer")
}

// **task 段由我们盖回去,不采信模型填的那份。**
//
// 模型把 r2va 写成 i2v 时,渲染器会从六段式掉到三字段 —— 参考素材的
// 保留分析整段消失,而 IR 本身依然自洽,校验器拦不住。
func TestEnhanceIROverridesModelChosenTaskType(t *testing.T) {
	ir := validIRObject()
	lying := validIRObject()
	lying.Task.Type = string(hilo.TaskI2V) // 模型谎报玩法
	lying.Task.DurationSeconds = 99
	fakeSequence(t, chatResponse(irJSON(t, lying)))

	res := EnhancePrompt(context.Background(), irCfg("qwen3.8-27b"), "Bearer sk-x", irInput(ir))

	require.Equal(t, common.EnhanceModeIR, res.Mode, "回落了: %s", res.IRFallbackReason)
	require.Contains(t, res.EnhancedPrompt, "retention_analysis:",
		"task.type 没被盖回 r2va,六段式塌成了三字段")
	require.Equal(t, string(hilo.TaskR2VA), res.IR.Task.Type)
	require.Equal(t, float64(6), res.IR.Task.DurationSeconds)
}

// 校验不过要把**具名问题**递回去重修,而不是笼统说一句"格式不对"。
func TestEnhanceIRRepairsWithNamedProblems(t *testing.T) {
	ir := validIRObject()
	bad := validIRObject()
	bad.Timeline[1].EndSeconds = 5 // 总时长 5 ≠ 请求的 6
	got := fakeSequence(t,
		chatResponse(irJSON(t, bad)),
		chatResponse(irJSON(t, ir)),
	)

	res := EnhancePrompt(context.Background(), irCfg("qwen3.8-27b"), "Bearer sk-x", irInput(ir))

	require.Equal(t, common.EnhanceModeIR, res.Mode, "回落了: %s", res.IRFallbackReason)
	require.True(t, res.IRRepaired, "应标记为靠重修才成功")
	require.Len(t, *got, 2, "应恰好两轮:首轮 + 一轮重修")

	// 第二轮必须带上问题代码,否则模型只会整篇重写、连对的创作判断一起换掉。
	require.Contains(t, string((*got)[1]), "TOTAL_DURATION_MISMATCH")
	// 也必须把它自己那份原样回放,它才知道要改哪一处。
	require.Contains(t, string((*got)[1]), "assistant")
}

// 重修轮的 token 必须累加 —— 每一轮都是一次真实 relay 调用,客户已经被
// 计过费了。只报最后一轮会让聚合日志的用量小于实际扣费,对账对不上。
func TestEnhanceIRAccumulatesUsageAcrossRepair(t *testing.T) {
	ir := validIRObject()
	bad := validIRObject()
	bad.Timeline[1].EndSeconds = 5
	fakeSequence(t, chatResponse(irJSON(t, bad)), chatResponse(irJSON(t, ir)))

	res := EnhancePrompt(context.Background(), irCfg("qwen3.8-27b"), "Bearer sk-x", irInput(ir))

	require.True(t, res.IRRepaired)
	require.NotNil(t, res.Usage)
	require.Equal(t, 60, res.Usage.TotalTokens, "两轮各 30,应累加而不是取最后一轮")
}

// ── 三级降级 ────────────────────────────────────────────────────────

// IR 修不好时**回落到 text 改写**,不是直接掉回原始提示词。
//
// 直接掉到原始提示词会让开 IR 比不开还差,于是没人敢开 —— 而不开就
// 永远收不到真实失败样本,IR 层也就永远不收敛。
func TestEnhanceIRFallsBackToTextNotToOriginal(t *testing.T) {
	ir := validIRObject()
	bad := validIRObject()
	bad.Timeline[1].EndSeconds = 5
	got := fakeSequence(t,
		chatResponse(irJSON(t, bad)), // 首轮不过
		chatResponse(irJSON(t, bad)), // 重修仍不过
		chatResponse("文本改写的结果"),      // 第三次:text 模式
	)

	res := EnhancePrompt(context.Background(), irCfg("qwen3.8-27b"), "Bearer sk-x", irInput(ir))

	require.False(t, res.Degraded, "回落到 text 成功了就不算降级: %s", res.DegradeReason)
	require.Equal(t, common.EnhanceModeText, res.Mode)
	require.Contains(t, res.IRFallbackReason, "TOTAL_DURATION_MISMATCH",
		"回落原因要能指认是哪条规则挡下的 —— 这是 IR 层唯一的反馈来源")
	require.Equal(t, "文本改写的结果", res.EnhancedPrompt)
	require.Len(t, *got, 3, "两轮 IR + 一次 text")
	// 第三次走的是文本模板,不是编译器提示词。
	require.Contains(t, string((*got)[2]), "文本模板")
}

// 拿不到请求事实就编译不了 IR(task_type 靠猜会把尾帧当首帧),
// 直接走 text。
func TestEnhanceIRWithoutCompilerInputFallsBackToText(t *testing.T) {
	fakeSequence(t, chatResponse("文本改写的结果"))

	res := EnhancePrompt(context.Background(), irCfg("qwen3.8-27b"), "Bearer sk-x",
		EnhanceInput{Prompt: "a cat"}) // 没有 Compiler

	require.Equal(t, common.EnhanceModeText, res.Mode)
	require.NotEmpty(t, res.IRFallbackReason)
	require.Equal(t, "文本改写的结果", res.EnhancedPrompt)
}

// IR 失败、text 也失败,才回到客户的原始提示词。
func TestEnhanceIRAndTextBothFailKeepsOriginal(t *testing.T) {
	ir := validIRObject()
	bad := validIRObject()
	bad.Timeline[1].EndSeconds = 5
	fakeSequence(t,
		chatResponse(irJSON(t, bad)),
		chatResponse(irJSON(t, bad)),
		chatResponse(""), // text 也返回空
	)

	res := EnhancePrompt(context.Background(), irCfg("qwen3.8-27b"), "Bearer sk-x", irInput(ir))

	require.True(t, res.Degraded)
	require.Equal(t, "a cat", res.EnhancedPrompt)
}

// 配了 mode=ir 但整段没启用时,一次调用都不该发出去。
func TestEnhanceIRRespectsDisabled(t *testing.T) {
	got := fakeSequence(t, chatResponse("x"))
	cfg := irCfg("qwen3.8-27b")
	off := false
	cfg.PromptEnhance.Enabled = &off

	res := EnhancePrompt(context.Background(), cfg, "Bearer sk-x", irInput(validIRObject()))

	require.False(t, res.Degraded)
	require.Equal(t, "a cat", res.EnhancedPrompt)
	require.Empty(t, *got)
}

// 默认(不写 mode)仍是 text —— 老配置不能因为新增一个字段就改变行为。
func TestEnhanceDefaultModeIsText(t *testing.T) {
	got := fakeSequence(t, chatResponse("文本改写的结果"))
	cfg := irCfg("qwen3.8-27b")
	cfg.PromptEnhance.Mode = ""

	res := EnhancePrompt(context.Background(), cfg, "Bearer sk-x", irInput(validIRObject()))

	require.Equal(t, common.EnhanceModeText, res.Mode)
	require.Len(t, *got, 1, "text 模式只该有一次调用")
	require.NotContains(t, string((*got)[0]), "schema_version",
		"发出去的是文本模板,不该是 IR 编译器提示词")
}

// 模式名拼错时退回 text,而不是让整条生成链路挂掉。
func TestEnhanceUnknownModeFallsBackToText(t *testing.T) {
	fakeSequence(t, chatResponse("文本改写的结果"))
	cfg := irCfg("qwen3.8-27b")
	cfg.PromptEnhance.Mode = "IRR"

	res := EnhancePrompt(context.Background(), cfg, "Bearer sk-x", irInput(validIRObject()))

	require.Equal(t, common.EnhanceModeText, res.Mode)
	require.Equal(t, "文本改写的结果", res.EnhancedPrompt)
}

// IR 模式必须把素材真的发出去。
//
// asset_bindings 的 provides/excludes、reference_relationships 的保留模式
// 都是"看着素材才能填"的字段;没有素材,模型编出来的 IR 结构完全合法,
// 校验器抓不到 —— 这正是 IR 兜不住的那一类错。
func TestEnhanceIRSendsMedia(t *testing.T) {
	ir := validIRObject()
	got := fakeSequence(t, chatResponse(irJSON(t, ir)))

	in := irInput(ir)
	in.ImageURLs = []string{"https://example.com/a.png"}
	in.VideoURLs = []string{"https://example.com/v.mp4"}
	EnhancePrompt(context.Background(), irCfg("qwen3.8-27b"), "Bearer sk-x", in)

	require.Len(t, *got, 1)
	body := string((*got)[0])
	require.Contains(t, body, "image_url")
	require.Contains(t, body, "video_url")
	require.Contains(t, body, "https://example.com/v.mp4")
}

// ── JSON 提取 ───────────────────────────────────────────────────────

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

func TestExtractIRRejectsNonObject(t *testing.T) {
	_, err := extractIR("完全没有 JSON")
	require.Error(t, err)
	require.Contains(t, err.Error(), "找不到 JSON")
}

// 重修指令要让模型**只改被点名的那处**,别整篇重写。
func TestBuildRepairInstructionScopesTheFix(t *testing.T) {
	msg := buildRepairInstruction(errors.New("IR 校验未通过: [TOTAL_DURATION_MISMATCH] timeline"))
	require.Contains(t, msg, "TOTAL_DURATION_MISMATCH")
	require.Contains(t, strings.ToLower(msg), "only")
	require.Contains(t, strings.ToLower(msg), "byte-identical")
}

// ── 实测打回来的真实缺陷 ────────────────────────────────────────────

// 模型**偶发**地在开头多吐一个游离的花括号。
//
// 实测 qwen3.8-27b 五次采样中了两次,形如 `{{"schema_version":…"]}}`。
//
// **它不是"多包了一层"** —— 整串花括号的净深度是 1,只有开头那个是多余的。
// 我最早按"双层包裹"写过一版修复(掐头去尾各削一个字符),对真实样本一点用
// 没有,而且测试是绿的:夹具是我按自己的假设造的真·双层,不是实测的形状。
// 这条用例现在按实测数据构造。
func TestExtractIRRecoversStrayLeadingBrace(t *testing.T) {
	raw := irJSON(t, validIRObject())
	strayed := "{" + raw // 只在开头多一个,结尾不动

	// 先确认它确实配不平 —— 取对象那步会直接返回空,连解析都到不了。
	require.Empty(t, extractJSONObject(strayed),
		"净深度不为 0,按配平找结尾必然失败")

	ir, err := extractIR(strayed)
	require.NoError(t, err)
	require.Equal(t, "the dancer", ir.Subjects[0].Name)
}

// 真·双层包裹也要能恢复(它是配平的,走的是另一条候选)。
func TestExtractIRRecoversDoubledBraces(t *testing.T) {
	raw := irJSON(t, validIRObject())
	ir, err := extractIR("{" + raw + "}")
	require.NoError(t, err)
	require.Equal(t, "the dancer", ir.Subjects[0].Name)
}

// 围栏 + 游离前导括号同时出现时也要恢复 —— 候选是在去掉围栏之后算的。
func TestExtractIRRecoversStrayBraceInsideCodeFence(t *testing.T) {
	raw := irJSON(t, validIRObject())
	ir, err := extractIR("```json\n{" + raw + "\n```")
	require.NoError(t, err)
	require.Equal(t, "the dancer", ir.Subjects[0].Name)
}

// 截断的输出不该被"修"成半份 IR:恢复的是格式噪音,不是缺失的内容。
func TestExtractIRRejectsTruncatedOutput(t *testing.T) {
	raw := irJSON(t, validIRObject())
	_, err := extractIR(raw[:len(raw)/2])
	require.Error(t, err)
}

func TestStripCodeFence(t *testing.T) {
	require.Equal(t, `{"a":1}`, stripCodeFence("```json\n{\"a\":1}\n```"))
	require.Equal(t, `{"a":1}`, stripCodeFence("```\n{\"a\":1}\n```"))
	require.Equal(t, `{"a":1}`, stripCodeFence(`{"a":1}`), "没有围栏时原样返回")
}

// IR 超时后,text 改写**仍然要跑得起来**。
//
// 这条测的是两级共用一个 ctx 的坑:共用的话 IR 把预算用满、ctx 被取消,
// 回落进来的 text 改写会拿着一个已死的 ctx 立刻失败 —— 三级降级里的
// 中间那级永远走不到,而表现只是"增强没生效"。
func TestEnhanceIRTimeoutStillAllowsTextFallback(t *testing.T) {
	orig := irCompileTimeout
	t.Cleanup(func() { irCompileTimeout = orig })
	irCompileTimeout = 30 * time.Millisecond

	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) == 1 {
			time.Sleep(200 * time.Millisecond) // 第一轮:拖过 IR 预算
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(chatResponse("文本改写的结果")))
	}))
	t.Cleanup(srv.Close)
	origEP := enhanceEndpoint
	t.Cleanup(func() { enhanceEndpoint = origEP })
	enhanceEndpoint = func() string { return srv.URL }

	res := EnhancePrompt(context.Background(), irCfg("qwen3.8-27b"), "Bearer sk-x",
		irInput(validIRObject()))

	require.False(t, res.Degraded, "IR 超时不该让整个增强降级: %s", res.DegradeReason)
	require.Equal(t, common.EnhanceModeText, res.Mode)
	require.NotEmpty(t, res.IRFallbackReason)
	require.Equal(t, "文本改写的结果", res.EnhancedPrompt)
}

// **素材清单也要盖回去,不只是 task 段。**
//
// <Picture N> 的标号按这份清单的顺序发。不盖回去的话标号跟着模型的书写
// 顺序走:它先写 image_2 再写 image_1,<Picture 1> 就指向第二张图 ——
// 而素材是按提交顺序发给模型和 H3 的,两边一错位,提示词指着的素材和它
// 描述的不是同一个,完全不报错。
func TestEnhanceIROverwritesAssetInventory(t *testing.T) {
	ir := validIRObject()
	lying := validIRObject()
	lying.Assets = []hilo.IRAsset{{AssetID: "image_9", MediaType: "image"}} // 模型自己编的清单
	fakeSequence(t, chatResponse(irJSON(t, lying)))

	res := EnhancePrompt(context.Background(), irCfg("qwen3.8-27b"), "Bearer sk-x", irInput(ir))

	require.Equal(t, common.EnhanceModeIR, res.Mode, "回落了: %s", res.IRFallbackReason)
	require.Len(t, res.IR.Assets, 1)
	require.Equal(t, "image_1", res.IR.Assets[0].AssetID,
		"清单应是请求侧那份,不是模型写的")
}

// ── 时间预算 ───────────────────────────────────────────────────────

// 配置优先,没配用内置默认。
func TestIRBudgetPrefersConfig(t *testing.T) {
	require.Equal(t, irCompileTimeout, irBudget(nil))
	require.Equal(t, irCompileTimeout, irBudget(&common.AggregatePromptEnhance{}))
	require.Equal(t, 600*time.Second,
		irBudget(&common.AggregatePromptEnhance{TimeoutSeconds: 600}))
}

// **IR 的预算不能顺带放宽 text。**
//
// 运营为 IR 配的是几分钟级的数字(一整份结构化 IR 要跑几十秒),而 text
// 改写只吐一段提示词。拿几分钟去兜一次本该几秒完成的调用,只会让失败
// 的那次把客户吊更久。
func TestTextBudgetNotWidenedByIRConfig(t *testing.T) {
	require.Equal(t, enhanceTimeout,
		textBudget(&common.AggregatePromptEnhance{TimeoutSeconds: 600}))
}

// 但显式调小要生效 —— 那是运营明确的意图。
func TestTextBudgetHonoursSmallerConfig(t *testing.T) {
	require.Equal(t, 5*time.Second,
		textBudget(&common.AggregatePromptEnhance{TimeoutSeconds: 5}))
	require.Equal(t, enhanceTimeout, textBudget(nil))
}

// 配置的预算要真的传到编译那一层,不是只存不用。
func TestEnhanceIRUsesConfiguredBudget(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) == 1 {
			time.Sleep(200 * time.Millisecond) // 拖过配置的预算
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(chatResponse("文本改写的结果")))
	}))
	t.Cleanup(srv.Close)
	orig := enhanceEndpoint
	t.Cleanup(func() { enhanceEndpoint = orig })
	enhanceEndpoint = func() string { return srv.URL }

	cfg := irCfg("qwen3.8-27b")
	cfg.PromptEnhance.TimeoutSeconds = 0 // 先用默认(240s):不该超时
	// 再用一个极小的配置值,验证它确实被采纳 —— 默认值下这条路走不到。
	cfg.PromptEnhance.TimeoutSeconds = 1
	irCompileTimeout = 240 * time.Second // 确认不是默认值在起作用

	res := EnhancePrompt(context.Background(), cfg, "Bearer sk-x", irInput(validIRObject()))

	require.Equal(t, common.EnhanceModeText, res.Mode, "配置的 1 秒预算没生效")
	require.NotEmpty(t, res.IRFallbackReason)
	require.Equal(t, "文本改写的结果", res.EnhancedPrompt)
}

// 整份跑偏时重修指令要**换一套说法**:让它从头照 schema 重写,
// 而不是"保持其余字段逐字不变" —— 那句话在这里是反作用的,
// 没有任何值得保留的东西,它反而会让模型守住错的形状。
func TestRepairInstructionForUnrecognizedShape(t *testing.T) {
	msg := buildRepairInstruction(errors.New("context-ir 校验未通过: [IR_SHAPE_UNRECOGNIZED] …"))
	require.Contains(t, strings.ToLower(msg), "none of it can be reused")
	require.NotContains(t, strings.ToLower(msg), "byte-identical",
		"整份跑偏时不能再叫它保留其余字段")
}

// 普通的逐项问题仍然走"只改被点名的"那套。
func TestRepairInstructionForNamedProblemsKeepsScope(t *testing.T) {
	msg := buildRepairInstruction(errors.New("[TOTAL_DURATION_MISMATCH] timeline"))
	require.Contains(t, strings.ToLower(msg), "byte-identical")
}

// IR 编译这条路同样默认关思考 —— 整份跑偏就是在这条路上实测到的。
func TestIRCompileDisablesThinkingByDefault(t *testing.T) {
	got := fakeSequence(t, chatResponse(irJSON(t, validIRObject())))
	EnhancePrompt(context.Background(), irCfg("qwen3.8-flash-fp8"), "Bearer sk-x",
		irInput(validIRObject()))
	require.Len(t, *got, 1)
	require.Contains(t, string((*got)[0]), `"enable_thinking":false`)
}

// **递给模型的重修指令里不能有中文。**
//
// 这条走的是**真实链路**:让校验真的失败,拿 compileViaIR 实际发出的第二轮
// 请求体来检查。早先的用例是直接拿合成 error 喂 buildRepairInstruction,
// 于是漏掉了"校验失败那条路根本没造出 ReportError"这件事 —— 类型丢了,
// errors.As 失败,整段中文原样递了出去。
//
// 最要命的是 PROMPT_REWRITE_LANGUAGE_VIOLATION:一边要求模型"正文不要出现
// 中文",一边把一整段中文摆在它面前。
func TestRepairRoundSendsNoChineseToModel(t *testing.T) {
	ir := validIRObject()
	bad := validIRObject()
	bad.Timeline[1].EndSeconds = 5 // 总时长对不上,触发 ValidateIR 失败
	got := fakeSequence(t, chatResponse(irJSON(t, bad)), chatResponse(irJSON(t, ir)))

	res := EnhancePrompt(context.Background(), irCfg("qwen3.8-27b"), "Bearer sk-x", irInput(ir))
	require.True(t, res.IRRepaired, "应走到重修轮")
	require.Len(t, *got, 2)

	repair := string((*got)[1])
	require.Contains(t, repair, "TOTAL_DURATION_MISMATCH", "具名代码要递过去")
	require.NotRegexp(t, `[\p{Han}]`, extractRepairInstruction(t, repair),
		"重修指令里出现了中文")
}

// extractRepairInstruction 取出最后一条 user 消息的正文(即重修指令)。
//
// 不能直接在整个请求体上查中文:第一轮的 assistant 回放里本来就带着 IR,
// 而 IR 的正文里可能有合法的中文台词。要查的是**我们写的那段指令**。
func extractRepairInstruction(t *testing.T, body string) string {
	t.Helper()
	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
	}
	require.NoError(t, common.Unmarshal([]byte(body), &req))
	require.NotEmpty(t, req.Messages)
	last := req.Messages[len(req.Messages)-1]
	require.Equal(t, "user", last.Role, "最后一条应是重修指令")
	s, ok := last.Content.(string)
	require.True(t, ok)
	return s
}
