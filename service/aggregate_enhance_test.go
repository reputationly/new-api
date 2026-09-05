package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/common"
)

func enhanceCfg(sysPrompt, mdl string) *common.AggregateModel {
	return &common.AggregateModel{
		Name: "agg", Type: "video",
		PromptEnhance: &common.AggregatePromptEnhance{
			Model:        mdl,
			SystemPrompt: sysPrompt,
		},
	}
}

// 增强段没启用时原样返回,且不算降级 —— 「没配增强」和「增强失败了」是两回事,
// 混在一起会让日志里全是无意义的降级记录。
func TestEnhanceDisabledReturnsOriginal(t *testing.T) {
	agg := &common.AggregateModel{Name: "agg", Type: "video"}

	res := EnhancePrompt(context.Background(), agg, "Bearer sk-test", "a cat", nil)

	require.Equal(t, "a cat", res.EnhancedPrompt)
	require.False(t, res.Degraded, "未配置增强段不该被记成降级")
}

// 以下每一条都必须**降级为原始提示词**,而不是让整个请求失败。
// 增强是锦上添花,为它牺牲一次本来能成功的生成,任何场景下都不划算。
func TestEnhanceDegradesInsteadOfFailing(t *testing.T) {
	cases := map[string]struct {
		agg        *common.AggregateModel
		prompt     string
		wantReason string
	}{
		"未配模板":    {enhanceCfg("", "gpt-4o-mini"), "a cat", "模板"},
		"未配模型":    {enhanceCfg("改写以下提示词", ""), "a cat", "增强模型"},
		"原始提示词为空": {enhanceCfg("改写以下提示词", "gpt-4o-mini"), "   ", "为空"},
		"缺少调用者身份": {enhanceCfg("改写以下提示词", "gpt-4o-mini"), "a cat", "身份"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			auth := "Bearer sk-test"
			if name == "缺少调用者身份" {
				auth = ""
			}
			res := EnhancePrompt(context.Background(), tc.agg, auth, tc.prompt, nil)

			require.True(t, res.Degraded, "应降级而不是失败")
			require.NotEmpty(t, res.DegradeReason, "降级必须留下原因,否则排障时无从下手")
			require.Equal(t, tc.prompt, res.EnhancedPrompt, "降级后必须原样使用客户的提示词")
			// 断言原因**指认到具体那一项**。只查 Degraded 是不够的:少了任何一道
			// 前置检查,请求照样会因为别的原因(如未配 ServerAddress)降级,
			// 测试仍然通过,而那道检查其实已经没了。
			require.Contains(t, res.DegradeReason, tc.wantReason)
		})
	}
}

// 带图时必须构造多模态 content,并且图片**真的**进到请求里。
// 只在文字里说"用户传了一张图"是不够的:增强模型看不到底图就会凭空臆造,
// 而生成模型看得见底图,两边产出直接打架。
func TestBuildEnhanceRequestSendsImages(t *testing.T) {
	body := buildEnhanceRequest("m", "sys", "a cat",
		[]string{"https://example.com/a.png", "", "https://example.com/b.png"}, true)

	msgs := body["messages"].([]map[string]any)
	require.Len(t, msgs, 2)
	require.Equal(t, "sys", msgs[0]["content"])

	parts, ok := msgs[1]["content"].([]map[string]any)
	require.True(t, ok, "带图时 content 必须是多模态数组")
	require.Equal(t, "text", parts[0]["type"])
	require.Equal(t, "a cat", parts[0]["text"])
	// 空串图片要被跳过,不能发一个 url 为空的 image_url 上去。
	require.Len(t, parts, 3, "两张有效图 + 一段文字")
	require.Equal(t, "image_url", parts[1]["type"])
}

// 关掉传图时退回纯文本 content —— 不能因为调用方传了 imageURLs 就擅自发出去。
func TestBuildEnhanceRequestRespectsSendImagesOff(t *testing.T) {
	body := buildEnhanceRequest("m", "sys", "a cat", []string{"https://example.com/a.png"}, false)

	msgs := body["messages"].([]map[string]any)
	require.Equal(t, "a cat", msgs[1]["content"], "关掉传图时应是纯文本 content")
}

// 没有图片时也应是纯文本,不要构造一个只有 text 一项的多模态数组 ——
// 部分上游对单元素 content 数组的处理与纯字符串不一致。
func TestBuildEnhanceRequestPlainWhenNoImages(t *testing.T) {
	body := buildEnhanceRequest("m", "sys", "a cat", nil, true)

	msgs := body["messages"].([]map[string]any)
	require.Equal(t, "a cat", msgs[1]["content"])
}

// 降级原因要能指认问题,而不是一句笼统的"增强失败"。
func TestEnhanceDegradeReasonIsActionable(t *testing.T) {
	res := EnhancePrompt(context.Background(), enhanceCfg("", "gpt-4o-mini"), "Bearer sk-test", "a cat", nil)

	require.True(t, strings.Contains(res.DegradeReason, "模板"),
		"未配模板时降级原因应点名模板,实得 %q", res.DegradeReason)
}

// withFakeEnhanceEndpoint 把增强调用指向一个本地假服务端,返回它收到的请求体。
// self-call 的一个额外好处:整条链路可以用 httptest 真正跑通,
// 而直连渠道那版连"成功路径"都测不了。
func withFakeEnhanceEndpoint(t *testing.T, status int, respBody string) *[]byte {
	t.Helper()
	var got []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(respBody))
	}))
	t.Cleanup(srv.Close)

	orig := enhanceEndpoint
	t.Cleanup(func() { enhanceEndpoint = orig })
	enhanceEndpoint = func() string { return srv.URL }
	return &got
}

// 成功路径:增强后的文本要被采用,usage 要带回来(它是计费的依据)。
func TestEnhanceSuccessUsesRewrittenPrompt(t *testing.T) {
	withFakeEnhanceEndpoint(t, http.StatusOK,
		`{"choices":[{"message":{"content":"  a majestic cat, cinematic lighting  "}}],
		  "usage":{"prompt_tokens":12,"completion_tokens":8}}`)

	res := EnhancePrompt(context.Background(), enhanceCfg("改写以下提示词", "gpt-4o-mini"),
		"Bearer sk-test", "a cat", nil)

	require.False(t, res.Degraded, "成功时不该标记降级: %s", res.DegradeReason)
	require.Equal(t, "a majestic cat, cinematic lighting", res.EnhancedPrompt, "应去掉首尾空白")
	require.Equal(t, "a cat", res.OriginalPrompt, "原始提示词要留档,排障时要能对照")
	require.NotNil(t, res.Usage, "usage 是计费依据,必须带回")
}

// 客户的 Authorization 必须原样透传 —— 增强以客户身份计费全靠它。
func TestEnhanceForwardsCallerIdentity(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"x"}}]}`))
	}))
	t.Cleanup(srv.Close)
	orig := enhanceEndpoint
	t.Cleanup(func() { enhanceEndpoint = orig })
	enhanceEndpoint = func() string { return srv.URL }

	EnhancePrompt(context.Background(), enhanceCfg("改写", "gpt-4o-mini"), "Bearer sk-customer", "a cat", nil)

	require.Equal(t, "Bearer sk-customer", gotAuth,
		"必须带客户身份,否则计费落不到他账上")
}

// 输入图要真的出现在发出去的请求体里,而不只是构造函数里正确。
func TestEnhanceActuallySendsImages(t *testing.T) {
	got := withFakeEnhanceEndpoint(t, http.StatusOK, `{"choices":[{"message":{"content":"x"}}]}`)

	EnhancePrompt(context.Background(), enhanceCfg("改写", "gpt-4o-mini"),
		"Bearer sk-test", "a cat", []string{"https://example.com/base.png"})

	require.Contains(t, string(*got), "https://example.com/base.png",
		"输入图必须真的发给增强模型,否则它会凭空臆造并与底图打架")
	require.Contains(t, string(*got), "image_url")
}

// 403 几乎总是"客户令牌白名单没放行增强模型"。降级原因必须指认它 ——
// 否则运营会对着一条笼统的调用失败去查渠道,查不出任何问题。
func TestEnhanceDegradesWithActionableReasonOn403(t *testing.T) {
	withFakeEnhanceEndpoint(t, http.StatusForbidden, `{"error":"forbidden"}`)

	res := EnhancePrompt(context.Background(), enhanceCfg("改写", "gpt-4o-mini"),
		"Bearer sk-test", "a cat", nil)

	require.True(t, res.Degraded)
	require.Equal(t, "a cat", res.EnhancedPrompt)
	require.Contains(t, res.DegradeReason, "白名单")
}

// 上游回了 200 但没有 choices —— 不能把空内容当成增强结果写回 prompt。
func TestEnhanceDegradesOnEmptyChoices(t *testing.T) {
	withFakeEnhanceEndpoint(t, http.StatusOK, `{"choices":[]}`)

	res := EnhancePrompt(context.Background(), enhanceCfg("改写", "gpt-4o-mini"),
		"Bearer sk-test", "a cat", nil)

	require.True(t, res.Degraded)
	require.Equal(t, "a cat", res.EnhancedPrompt)
}

// 返回空字符串同样要降级,不能拿一个空 prompt 去生成。
func TestEnhanceDegradesOnBlankContent(t *testing.T) {
	withFakeEnhanceEndpoint(t, http.StatusOK, `{"choices":[{"message":{"content":"   "}}]}`)

	res := EnhancePrompt(context.Background(), enhanceCfg("改写", "gpt-4o-mini"),
		"Bearer sk-test", "a cat", nil)

	require.True(t, res.Degraded)
	require.Equal(t, "a cat", res.EnhancedPrompt)
}
