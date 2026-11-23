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

// 流式只在**真的读到图片分片**时打标：图片模型回纯文本（拒答/只给说明）时，
// 不能凭空多出一条没有图的出图任务。
func TestMarkIfGeminiChunkHasImage(t *testing.T) {
	withImage := hookCtx()
	markIfGeminiChunkHasImage(withImage, &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{{Content: dto.GeminiChatContent{
			Parts: []dto.GeminiPart{
				{Text: "here"},
				{InlineData: &dto.GeminiInlineData{MimeType: "image/png", Data: "iVBORw0KGgo="}},
			},
		}}},
	})
	if !relaycommon.SyncImageProduced(withImage) {
		t.Fatal("读到了 image/* 分片，必须打标")
	}

	textOnly := hookCtx()
	markIfGeminiChunkHasImage(textOnly, &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{{Content: dto.GeminiChatContent{
			Parts: []dto.GeminiPart{{Text: "抱歉，我不画这个"}},
		}}},
	})
	if relaycommon.SyncImageProduced(textOnly) {
		t.Fatal("纯文本分片不能打图片标记")
	}

	audioOnly := hookCtx()
	markIfGeminiChunkHasImage(audioOnly, &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{{Content: dto.GeminiChatContent{
			Parts: []dto.GeminiPart{{InlineData: &dto.GeminiInlineData{MimeType: "audio/wav", Data: "AAAA"}}},
		}}},
	})
	if relaycommon.SyncImageProduced(audioOnly) {
		t.Fatal("音频分片不是图片")
	}

	// nil 入参不 panic
	markIfGeminiChunkHasImage(hookCtx(), nil)
}

const geminiImageBody = `{"candidates":[{"content":{"role":"model","parts":[
	{"text":"here"},
	{"inlineData":{"mimeType":"image/png","data":"iVBORw0KGgoAAAANSUhEUg=="}}
]},"finishReason":"STOP"}],
"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":10,"totalTokenCount":15}}`

func callGeminiImagesHandler(t *testing.T, responseFormat string) string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/images/generations", nil)

	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(geminiImageBody)),
		Header:     http.Header{},
	}
	info := &relaycommon.RelayInfo{
		Request:     &dto.ImageRequest{ResponseFormat: responseFormat},
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeGemini},
	}
	info.OriginModelName = "gemini-nano-banana-2.1"
	info.UpstreamModelName = "gemini-nano-banana-2.1"

	if _, apiErr := GeminiImageChatHandler(c, info, resp); apiErr != nil {
		t.Fatalf("handler 不应报错：%v", apiErr)
	}
	return w.Body.String()
}

// 客户端要 url 但 OBS 不可用：必须降级成 b64 返回，不能报错、不能返回空图。
func TestGeminiImageChatHandlerFallsBackToB64WhenOBSUnavailable(t *testing.T) {
	out := callGeminiImagesHandler(t, "url")
	if !strings.Contains(out, "b64_json") {
		t.Fatalf("OBS 不可用时必须回 b64_json，实际响应：%s", out)
	}
	if strings.Contains(out, `"url"`) {
		t.Fatalf("OBS 不可用时不该出现 url 字段，实际响应：%s", out)
	}
}

// 不传 response_format 时维持历史上的 b64 形态，老客户端不会因为这次改动而坏。
func TestGeminiImageChatHandlerDefaultsToB64(t *testing.T) {
	out := callGeminiImagesHandler(t, "")
	if !strings.Contains(out, "b64_json") {
		t.Fatalf("缺省必须回 b64_json，实际响应：%s", out)
	}
}
