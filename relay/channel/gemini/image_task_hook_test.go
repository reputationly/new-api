package gemini

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/model_setting"

	"github.com/gin-gonic/gin"
)

func hookCtx() *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1beta/models/m:generateContent", nil)
	return c
}

func imageCandidates() []dto.GeminiChatCandidate {
	return []dto.GeminiChatCandidate{{
		Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{
			{Text: "Here is the image"},
			{InlineData: &dto.GeminiInlineData{MimeType: "image/png", Data: "iVBORw0KGgoAAAANSUhEUg=="}},
		}},
	}}
}

// 只有 image/* 才算成图：音频、视频分片与纯文本都不能触发任务记录/落盘。
func TestFirstInlineImagePartSkipsNonImagePayloads(t *testing.T) {
	candidates := []dto.GeminiChatCandidate{{
		Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{
			{Text: "hello"},
			{InlineData: &dto.GeminiInlineData{MimeType: "audio/wav", Data: "AAAA"}},
			{InlineData: &dto.GeminiInlineData{MimeType: "image/png", Data: "iVBORw0KGgo="}},
		}},
	}}

	part := firstInlineImagePart(candidates)
	if part == nil {
		t.Fatal("应当跳过 audio 并找到 image/png")
	}
	if part.InlineData.MimeType != "image/png" {
		t.Fatalf("MimeType = %q, want image/png", part.InlineData.MimeType)
	}

	if got := firstInlineImagePart([]dto.GeminiChatCandidate{{Content: dto.GeminiChatContent{
		Parts: []dto.GeminiPart{{Text: "只有文字"}},
	}}}); got != nil {
		t.Fatalf("纯文本响应不应命中，实际 %+v", got)
	}
}

func TestExtFromImageMime(t *testing.T) {
	cases := map[string]string{
		"image/png":  "png",
		"image/jpeg": "jpg",
		"image/jpg":  "jpg",
		"IMAGE/WEBP": "webp",
		"image/gif":  "gif",
		"":           "png",
	}
	for mime, want := range cases {
		if got := extFromImageMime(mime); got != want {
			t.Errorf("extFromImageMime(%q) = %q, want %q", mime, got, want)
		}
	}
}

// 非流式：必须打上「产出了图片」标记，任务日志才认得出来这是一次出图调用。
// 测试环境没有 OBS，落盘失败只能丢预览，不能影响标记或 panic。
func TestCaptureGeminiInlineImagesMarksProducedWithoutOBS(t *testing.T) {
	c := hookCtx()
	info := &relaycommon.RelayInfo{OriginModelName: "gemini-nano-banana-2.1", UserId: 1}

	captureGeminiInlineImages(c, info, imageCandidates())

	if !relaycommon.SyncImageProduced(c) {
		t.Fatal("响应里有内联图片时必须打标记")
	}
	if keys := relaycommon.GetSyncImageOBSKeys(c); len(keys) != 0 {
		t.Fatalf("未启用 OBS 时不应写出 key，实际 %v", keys)
	}
}

func TestCaptureGeminiInlineImagesIgnoresTextOnlyResponse(t *testing.T) {
	c := hookCtx()
	info := &relaycommon.RelayInfo{OriginModelName: "gemini-2.5-pro", UserId: 1}

	captureGeminiInlineImages(c, info, []dto.GeminiChatCandidate{{Content: dto.GeminiChatContent{
		Parts: []dto.GeminiPart{{Text: "纯文本回复"}},
	}}})

	if relaycommon.SyncImageProduced(c) {
		t.Fatal("纯文本响应不能打图片标记")
	}
}

// 流式只能按模型判定：命中「支持的图像模型」才记，普通文本模型不能误记。
func TestMarkGeminiImageProducedForStreamOnlyForImagineModels(t *testing.T) {
	const imagineModel = "gemini-2.5-flash-image"
	if !model_setting.IsGeminiModelSupportImagine(imagineModel) {
		t.Skipf("%s 不在默认支持图像模型列表里，跳过", imagineModel)
	}

	// UpstreamModelName 来自嵌入的 ChannelMeta，必须显式构造。
	streamInfo := func(model string) *relaycommon.RelayInfo {
		return &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: model}}
	}

	c := hookCtx()
	markGeminiImageProducedForStream(c, streamInfo(imagineModel))
	if !relaycommon.SyncImageProduced(c) {
		t.Fatalf("%s 流式应当打图片标记", imagineModel)
	}

	textCtx := hookCtx()
	markGeminiImageProducedForStream(textCtx, streamInfo("gemini-2.5-pro"))
	if relaycommon.SyncImageProduced(textCtx) {
		t.Fatal("普通文本模型的流式不能打图片标记")
	}

	// ChannelMeta 缺失时不能 panic。
	bare := hookCtx()
	markGeminiImageProducedForStream(bare, &relaycommon.RelayInfo{})
	if relaycommon.SyncImageProduced(bare) {
		t.Fatal("没有 ChannelMeta 时不应打标记")
	}
}

// OpenAI 语义：response_format 缺省即 url，只有显式 b64_json 才回 base64。
func TestWantsImageURL(t *testing.T) {
	cases := map[string]bool{
		"":           true,
		"url":        true,
		"URL":        true,
		" url ":      true,
		"b64_json":   false,
		"B64_JSON":   false,
		" b64_json ": false,
		"b64json":    true, // 非法值不 panic，按缺省 url 处理
	}
	for format, want := range cases {
		if got := wantsImageURL(format); got != want {
			t.Errorf("wantsImageURL(%q) = %v, want %v", format, got, want)
		}
	}
}

// 返回的 URL 列表必须与「图片出现顺序」一一对齐（含落盘失败的空串），
// 否则 n>1 时会把第 2 张的链接贴到第 1 张上。
func TestPersistGeminiInlineImagesAlignsWithImageOrder(t *testing.T) {
	c := hookCtx()
	info := &relaycommon.RelayInfo{OriginModelName: "gemini-nano-banana-2.1", UserId: 1}

	urls := persistGeminiInlineImages(c, info, []dto.GeminiChatCandidate{{
		Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{
			{Text: "图一"},
			{InlineData: &dto.GeminiInlineData{MimeType: "image/png", Data: "iVBORw0KGgo="}},
			{Text: "图二"},
			{InlineData: &dto.GeminiInlineData{MimeType: "image/png", Data: "iVBORw0KGgo="}},
		}},
	}})

	if len(urls) != 2 {
		t.Fatalf("urls = %v, want 2 entries（每张图一个位置）", urls)
	}
	// 测试环境没有 OBS：占位为空串，调用方据此逐张降级到 b64。
	for i, u := range urls {
		if u != "" {
			t.Errorf("urls[%d] = %q, want empty when OBS is unavailable", i, u)
		}
	}
	if !relaycommon.SyncImageProduced(c) {
		t.Error("有内联图片就必须打「产出了图片」标记，任务日志才认得出来")
	}
}

// 客户端要 url 但 OBS 不可用：必须降级成 b64 返回，不能报错、不能返回空图。
func TestGeminiImageChatHandlerFallsBackToB64WhenOBSUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/images/generations", nil)

	body := `{"candidates":[{"content":{"role":"model","parts":[
		{"text":"here"},
		{"inlineData":{"mimeType":"image/png","data":"iVBORw0KGgoAAAANSUhEUg=="}}
	]},"finishReason":"STOP"}],
	"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":10,"totalTokenCount":15}}`
	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{},
	}

	info := &relaycommon.RelayInfo{
		Request:     &dto.ImageRequest{ResponseFormat: "url"},
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeGemini},
	}
	info.OriginModelName = "gemini-nano-banana-2.1"
	info.UpstreamModelName = "gemini-nano-banana-2.1"

	if _, apiErr := GeminiImageChatHandler(c, info, resp); apiErr != nil {
		t.Fatalf("降级路径不应报错：%v", apiErr)
	}

	out := w.Body.String()
	if !strings.Contains(out, "b64_json") {
		t.Fatalf("OBS 不可用时必须回 b64_json，实际响应：%s", out)
	}
	if strings.Contains(out, `"url"`) {
		t.Fatalf("OBS 不可用时不该出现 url 字段，实际响应：%s", out)
	}
}
