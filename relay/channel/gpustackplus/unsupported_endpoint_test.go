package gpustackplus

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
)

// setPlaygroundOpt 直接写体验区四份配置(全局 OptionMap),故本文件的用例不并行。
func setPlaygroundOpt(t *testing.T, image, video, audio, music string) {
	t.Helper()
	common.OptionMapRWMutex.Lock()
	defer common.OptionMapRWMutex.Unlock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	common.OptionMap["ImageModelSizeConfig"] = image
	common.OptionMap["VideoModelConfig"] = video
	common.OptionMap["AudioModelConfig"] = audio
	common.OptionMap["MusicModelConfig"] = music
}

func infoWithModel(model string) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		OriginModelName: model,
		ChannelMeta:     &relaycommon.ChannelMeta{UpstreamModelName: model},
	}
}

// 音乐模型被拿去打 chat/completions:报错要点出它的玩法和该走的任务端点。
func TestUnsupportedEndpointErrorPointsAtMusicEndpoint(t *testing.T) {
	setPlaygroundOpt(t, "", "", "",
		`{"models":{"minimax-music3":{"tabs":{"t2m":{},"cover":{}}}}}`)

	err := unsupportedEndpointError(infoWithModel("minimax-music3"), "/v1/chat/completions")
	msg := err.Error()

	for _, want := range []string{
		"minimax-music3",
		"/v1/chat/completions",
		videoTaskEndpoint,
		selfHostedDocsURL,
		"t2m",
		"cover",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error message missing %q, got: %s", want, msg)
		}
	}
}

// 图片模型要指向图片端点,不能一律甩给视频任务口。
func TestUnsupportedEndpointErrorPointsAtImageEndpoint(t *testing.T) {
	setPlaygroundOpt(t, `{"models":{"z-image":{"tabs":{"text2image":{}}}}}`, "", "", "")

	msg := unsupportedEndpointError(infoWithModel("z-image"), "/v1/chat/completions").Error()

	if !strings.Contains(msg, taskTypeEndpoints["t2i"]) {
		t.Fatalf("error message should point at the image endpoint, got: %s", msg)
	}
	if strings.Contains(msg, videoTaskEndpoint) {
		t.Fatalf("error message should not mention the video endpoint for a t2i model, got: %s", msg)
	}
}

// 名字也推不出任务类型时(InferTaskType 落 t2v 兜底):不拿兜底值冒充答案,
// 也不列一串候选端点让用户自己挑 —— 只说清不能用哪个端点,剩下交给文档。
func TestUnsupportedEndpointErrorFallsBackToDocsOnly(t *testing.T) {
	setPlaygroundOpt(t, "", "", "", "")

	msg := unsupportedEndpointError(infoWithModel("some-unconfigured-model"), "/v1/messages").Error()
	if strings.Contains(msg, "推断它是") {
		t.Fatalf("must not present the t2v fallback as an inferred answer, got: %s", msg)
	}

	for _, want := range []string{
		"some-unconfigured-model",
		"/v1/messages",    // 正在报错的端点
		selfHostedDocsURL, // 自查入口
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error message missing %q, got: %s", want, msg)
		}
	}
	// 判不出来就别给建议。
	for _, unwanted := range []string{videoTaskEndpoint, taskTypeEndpoints["t2i"], taskTypeEndpoints["tts"]} {
		if strings.Contains(msg, unwanted) {
			t.Fatalf("should not offer an endpoint guess, got %q in: %s", unwanted, msg)
		}
	}
}

// 用错端点是客户端问题:必须是 400,而且经 handler 的 types.NewError 包装后仍是 400
// (靠 errors.As 命中已有的 *NewAPIError),否则用户看到的还是 500。
func TestUnsupportedEndpointErrorIsBadRequestThroughHandlerWrapping(t *testing.T) {
	setPlaygroundOpt(t, "", "", "", "")

	err := unsupportedEndpointError(infoWithModel("minimax-h3-ref2va"), "/v1/chat/completions")

	apiErr, ok := err.(*types.NewAPIError)
	if !ok {
		t.Fatalf("unsupportedEndpointError returned %T, want *types.NewAPIError", err)
	}
	if apiErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("StatusCode = %d, want %d", apiErr.StatusCode, http.StatusBadRequest)
	}

	// relay/compatible_handler.go 收到 ConvertOpenAIRequest 的错误后就是这么包的。
	wrapped := types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	if wrapped.StatusCode != http.StatusBadRequest {
		t.Fatalf("wrapped StatusCode = %d, want %d — handler wrapping must not turn it back into 500",
			wrapped.StatusCode, http.StatusBadRequest)
	}
	if !strings.Contains(wrapped.Error(), selfHostedDocsURL) {
		t.Fatalf("wrapped error lost the docs link: %s", wrapped.Error())
	}
}

func TestConvertOpenAIRequestReturnsGuidance(t *testing.T) {
	setPlaygroundOpt(t, "", "", "", "")

	adaptor := &Adaptor{}
	_, err := adaptor.ConvertOpenAIRequest(nil, infoWithModel("swiftvr"), nil)
	if err == nil {
		t.Fatal("ConvertOpenAIRequest returned nil error for a media-only channel")
	}
	if !strings.Contains(err.Error(), selfHostedDocsURL) {
		t.Fatalf("error message should carry the docs link, got: %s", err.Error())
	}
}

// 直连 API 的模型多半没配进体验区 —— 这时必须靠模型名推断出端点,
// 而不是甩一句"未配置在体验区"就让用户自己猜。截图里报错的就是这几个。
func TestUnsupportedEndpointErrorInfersFromModelNameWhenUnconfigured(t *testing.T) {
	setPlaygroundOpt(t, "", "", "", "")

	cases := []struct {
		model    string
		wantTask string
		wantEP   string
	}{
		{"minimax-h3-ref2va", "r2va", videoTaskEndpoint},
		{"swiftvr", "sr", videoTaskEndpoint},
		{"qwen3-tts", "tts", taskTypeEndpoints["tts"]},
		{"qwen-image-edit", "i2i", taskTypeEndpoints["i2i"]},
	}
	for _, tc := range cases {
		msg := unsupportedEndpointError(infoWithModel(tc.model), "/v1/chat/completions").Error()
		if !strings.Contains(msg, tc.wantTask) {
			t.Errorf("%s: message should name the inferred task %q, got: %s", tc.model, tc.wantTask, msg)
		}
		if !strings.Contains(msg, tc.wantEP) {
			t.Errorf("%s: message should point at %q, got: %s", tc.model, tc.wantEP, msg)
		}
	}
}

// 体验区有配置时以配置为准,不能被名字推断盖掉 —— 与任务 Adaptor 的解析同序。
func TestPlaygroundConfigWinsOverNameInference(t *testing.T) {
	// 名字推断会把它判成 i2i(含 "edit"),但体验区声明它只做 t2i。
	setPlaygroundOpt(t, `{"models":{"qwen-image-edit":{"tabs":{"text2image":{}}}}}`, "", "", "")

	msg := unsupportedEndpointError(infoWithModel("qwen-image-edit"), "/v1/chat/completions").Error()

	if !strings.Contains(msg, "它支持的任务类型是 t2i") {
		t.Fatalf("playground config should win over name inference, got: %s", msg)
	}
	if strings.Contains(msg, "按模型名推断") {
		t.Fatalf("should not fall back to name inference when configured, got: %s", msg)
	}
}

// fl2va 分区同时服务 t2va 与 fl2va,名字给不出答案(task Adaptor 的注释写明了),
// 落到 t2v 兜底 —— 这类模型不该被当成"推断成功"。
func TestFl2vaAndMusicFallThroughInsteadOfGuessing(t *testing.T) {
	setPlaygroundOpt(t, "", "", "", "")

	for _, model := range []string{"minimax-h3-fast-fl2va", "minimax-music3"} {
		msg := unsupportedEndpointError(infoWithModel(model), "/v1/chat/completions").Error()
		if strings.Contains(msg, "推断它是") {
			t.Errorf("%s: t2v fallback presented as an answer, got: %s", model, msg)
		}
		// 判不出来就只留"不能用哪个端点 + 文档",不猜端点。
		if !strings.Contains(msg, selfHostedDocsURL) {
			t.Errorf("%s: message should still carry the docs link, got: %s", model, msg)
		}
	}
}

// 渠道做了模型重定向时,任务 token 只在映射后的上游名里 ——
// 拿公开名去推断只会落 t2v 兜底,用户就拿不到精确端点了。
func TestInferenceUsesUpstreamNameAfterModelMapping(t *testing.T) {
	setPlaygroundOpt(t, "", "", "", "")

	info := &relaycommon.RelayInfo{
		OriginModelName: "my-video-model", // 公开名不带任何任务 token
		ChannelMeta:     &relaycommon.ChannelMeta{UpstreamModelName: "minimax-h3-ref2va"},
	}
	msg := unsupportedEndpointError(info, "/v1/chat/completions").Error()

	if !strings.Contains(msg, "r2va") {
		t.Fatalf("inference must run on the mapped upstream name, got: %s", msg)
	}
}

// 体验区配置按公开名键控,映射后的上游名查不到 —— 查配置只能用公开名。
func TestPlaygroundLookupUsesOriginNameNotUpstream(t *testing.T) {
	setPlaygroundOpt(t, `{"models":{"my-image-model":{"tabs":{"text2image":{}}}}}`, "", "", "")

	info := &relaycommon.RelayInfo{
		OriginModelName: "my-image-model",
		ChannelMeta:     &relaycommon.ChannelMeta{UpstreamModelName: "z-image-turbo-deployment"},
	}
	msg := unsupportedEndpointError(info, "/v1/chat/completions").Error()

	if !strings.Contains(msg, "它支持的任务类型是 t2i") {
		t.Fatalf("playground lookup must use the public model name, got: %s", msg)
	}
}

// 名字里真带 t2v 的模型要被采信,不能因为撞上兜底值就当成"没认出来"。
func TestExplicitT2vTokenIsConclusive(t *testing.T) {
	setPlaygroundOpt(t, "", "", "", "")

	msg := unsupportedEndpointError(infoWithModel("wan2.2-t2v"), "/v1/chat/completions").Error()

	if !strings.Contains(msg, "推断它是 t2v 类任务") {
		t.Fatalf("an explicit t2v token should be trusted, got: %s", msg)
	}
}

// 文档链接必须活着到客户端手里。
//
// 之前这里只断言 err.Error(),那是**未打码**的路径;客户端实际拿到的是
// ToOpenAIError()/ToClaudeError() 的产物,两者都会过 common.MaskSensitiveInfo,
// 会把 URL 压成 https://***.com/***/***/*** —— 整条指引最值钱的部分正好没了。
func TestDocsURLSurvivesClientFacingRendering(t *testing.T) {
	setPlaygroundOpt(t, "", "", "", "")

	apiErr, ok := unsupportedEndpointError(infoWithModel("minimax-h3-ref2va"), "/v1/chat/completions").(*types.NewAPIError)
	if !ok {
		t.Fatal("unsupportedEndpointError must return *types.NewAPIError")
	}

	if got := apiErr.ToOpenAIError().Message; !strings.Contains(got, selfHostedDocsURL) {
		t.Fatalf("ToOpenAIError masked the docs URL, client gets: %s", got)
	}
	if got := apiErr.ToClaudeError().Message; !strings.Contains(got, selfHostedDocsURL) {
		t.Fatalf("ToClaudeError masked the docs URL, client gets: %s", got)
	}
	if got := apiErr.MaskSensitiveError(); !strings.Contains(got, selfHostedDocsURL) {
		t.Fatalf("MaskSensitiveError masked the docs URL, log gets: %s", got)
	}
}

// handler 用 ErrorCodeConvertRequestFailed 重新包装时,errorCode 不能被覆盖 ——
// 一旦覆盖就掉出 skipsMasking 豁免集,链接又会被打码。
func TestDocsURLSurvivesHandlerRewrapping(t *testing.T) {
	setPlaygroundOpt(t, "", "", "", "")

	err := unsupportedEndpointError(infoWithModel("swiftvr"), "/v1/chat/completions")
	wrapped := types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())

	if got := wrapped.ToOpenAIError().Message; !strings.Contains(got, selfHostedDocsURL) {
		t.Fatalf("docs URL lost after handler rewrapping, client gets: %s", got)
	}
}

// renderLikeController 复现 controller/relay.go:92-97 的客户端渲染管线。
// 之前的测试只调 ToOpenAIError(),漏掉了它前面那道 ReplaceUpstreamModelPath ——
// 而正是那道把端点提示和文档链接整段吃成了模型名。
func renderLikeController(apiErr *types.NewAPIError, originModelName string) string {
	if originModelName != "" && !apiErr.IsSelfAuthoredGuidance() {
		apiErr.SetMessage(service.ReplaceUpstreamModelPath(apiErr.Error(), originModelName))
	}
	return apiErr.ToOpenAIError().Message
}

// 端到端:指引必须完整活到客户端手里。
func TestGuidanceSurvivesFullControllerPipeline(t *testing.T) {
	setPlaygroundOpt(t, "", "", "", "")

	for _, model := range []string{"minimax-h3-ref2va", "swiftvr"} {
		apiErr := unsupportedEndpointError(infoWithModel(model), "/v1/chat/completions").(*types.NewAPIError)
		got := renderLikeController(apiErr, model)

		for _, want := range []string{
			"/v1/chat/completions", // 被打的端点
			videoTaskEndpoint,      // 推荐端点
			selfHostedDocsURL,      // 文档链接
		} {
			if !strings.Contains(got, want) {
				t.Errorf("%s: client message lost %q, got: %s", model, want, got)
			}
		}
	}
}

// 反向约束:豁免只对自造指引生效,普通错误里的上游模型路径仍要被替换掉,
// 否则等于把防泄露机制整个关了。
func TestUpstreamPathStillRewrittenForOrdinaryErrors(t *testing.T) {
	ordinary := types.NewError(
		errors.New("model /NFS_LLM/GLM-5-w4a8/ is not a multimodal model"),
		types.ErrorCodeBadResponseBody)

	got := renderLikeController(ordinary, "glm-5")

	if strings.Contains(got, "/NFS_LLM/") {
		t.Fatalf("upstream model path leaked to the client: %s", got)
	}
	if !strings.Contains(got, "glm-5") {
		t.Fatalf("friendly model name missing: %s", got)
	}
}

// 上游能控制 error.code:WithOpenAIError 把响应体里的 code 原样抄进 errorCode。
// 豁免只看 errorCode 的话,上游回一个 endpoint_not_supported 就能让含内网地址的
// 消息免打码直达客户端 —— 正是打码要防的泄露。所以判据必须同时看 errorType。
func TestUpstreamCannotForgeMaskingExemption(t *testing.T) {
	forged := types.WithOpenAIError(types.OpenAIError{
		Message: "backend unreachable at http://10.0.3.7:8000/v1/videos",
		Code:    string(types.ErrorCodeEndpointNotSupported),
	}, http.StatusBadGateway)

	if forged.IsSelfAuthoredGuidance() {
		t.Fatal("upstream-authored error must not qualify as gateway guidance")
	}
	got := forged.ToOpenAIError().Message
	if strings.Contains(got, "10.0.3.7") {
		t.Fatalf("internal address leaked unmasked to the client: %s", got)
	}
}

// 报错是给终端用户看的:不该出现渠道实现名、后台概念这类内部信息。
func TestGuidanceLeaksNoInternalNames(t *testing.T) {
	setPlaygroundOpt(t, "", "", `{"models":{"some-voice-model":{"tabs":{"synthesis":{}}}}}`, "")

	for _, model := range []string{"minimax-h3-ref2va", "some-voice-model", "unknown-model"} {
		msg := unsupportedEndpointError(infoWithModel(model), "/v1/chat/completions").Error()
		for _, leaked := range []string{"gpustackplus", "渠道", "体验区", "玩法"} {
			if strings.Contains(msg, leaked) {
				t.Errorf("%s: message exposes internal term %q: %s", model, leaked, msg)
			}
		}
	}
}

// AudioModelConfig 的 tab key(emotion/synthesis/dialogue/design)不在
// taskTypeToPlaygroundTab 里,配了体验区的语音模型照样查不到候选 ——
// 措辞不能因此断言「未配置」,那是假话。
func TestConfiguredAudioModelGetsNoFalseUnconfiguredClaim(t *testing.T) {
	setPlaygroundOpt(t, "", "", `{"models":{"some-voice-model":{"tabs":{"synthesis":{}}}}}`, "")

	msg := unsupportedEndpointError(infoWithModel("some-voice-model"), "/v1/chat/completions").Error()

	if strings.Contains(msg, "未配置") {
		t.Fatalf("model IS configured in the playground; claim is false: %s", msg)
	}
	// 判不出来就不猜端点,但文档链接要在。
	if !strings.Contains(msg, selfHostedDocsURL) {
		t.Fatalf("docs link missing: %s", msg)
	}
}
