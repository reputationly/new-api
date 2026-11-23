package gemini

import (
	"context"
	"encoding/base64"
	"strings"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"

	"github.com/gin-gonic/gin"
)

// Gemini 的两种响应形态（原生 generateContent 与 OpenAI 兼容 chat）都把生成的图片以
// inlineData(base64) 塞在响应体里，而 RelayMode 里没有对应的图片模式，任务日志因此
// 查不到这类调用（见 relay/image_sync_task.go）。这里做两件事：
//
//  1. 非流式：把第一张图落盘到 OBS，任务记录存 obs://key，预览走查询时实时签发的 URL；
//  2. 流式：只标记「产出了图片」，任务记录照写但没有图——SSE 分片里的 base64 要跨块
//     拼接才能还原，留待后续。
//
// 客户端拿到的响应体一个字都不改：chat 的 markdown data URI 与 Gemini 的 inlineData
// 都保持原样，落盘只作留档。
func captureGeminiInlineImages(c *gin.Context, info *relaycommon.RelayInfo, candidates []dto.GeminiChatCandidate) {
	if c == nil || info == nil || len(candidates) == 0 {
		return
	}
	part := firstInlineImagePart(candidates)
	if part == nil {
		return
	}
	relaycommon.MarkSyncImageProduced(c)
	// 任务表一行只存一张图（与同步生图链路同口径），已有 key 就不再落第二张。
	if len(relaycommon.GetSyncImageOBSKeys(c)) > 0 {
		return
	}
	persistOneInlineImage(c, info, part)
}

// persistGeminiInlineImages 逐张落盘响应里的内联图片，返回与「图片出现顺序」对齐的签名 URL。
//
// 落盘失败的项对应位置给空串：/v1/images/generations 的 response_format=url 依赖这个
// 列表逐张降级（拿不到 URL 的那张退回 b64），不能因为一张失败就整单报错。
func persistGeminiInlineImages(c *gin.Context, info *relaycommon.RelayInfo, candidates []dto.GeminiChatCandidate) []string {
	if c == nil || info == nil || len(candidates) == 0 {
		return nil
	}
	var urls []string
	for i := range candidates {
		parts := candidates[i].Content.Parts
		for j := range parts {
			part := &parts[j]
			if !isInlineImagePart(part) {
				continue
			}
			relaycommon.MarkSyncImageProduced(c)
			signed, ok := persistOneInlineImage(c, info, part)
			if !ok {
				signed = ""
			}
			urls = append(urls, signed)
		}
	}
	return urls
}

// persistOneInlineImage 落盘单张内联图，成功时返回签名 URL 并登记 OBS key。
// 任何失败都只记日志并返回 false——图片照常以 base64 回给客户端。
func persistOneInlineImage(c *gin.Context, info *relaycommon.RelayInfo, part *dto.GeminiPart) (string, bool) {
	raw, err := base64.StdEncoding.DecodeString(part.InlineData.Data)
	if err != nil {
		logger.LogError(c, "decode inline image for task log failed: "+err.Error())
		return "", false
	}
	// c.Request 在个别调用路径（如单测或内部重放）可能为空，落盘不需要请求作用域。
	ctx := context.Background()
	if c.Request != nil {
		ctx = c.Request.Context()
	}
	signed, key, err := service.PersistInlineImageToOBS(
		ctx, info.UserId, info.UsingGroup, info.OriginModelName,
		raw, extFromImageMime(part.InlineData.MimeType),
	)
	if err != nil {
		// 落盘失败只丢预览/URL，任务记录本身照写（与同步生图的「没有 key 也记录」一致）。
		logger.LogError(c, "persist inline image for task log failed: "+err.Error())
		return "", false
	}
	relaycommon.AppendSyncImageOBSKeys(c, key)
	return signed, true
}

// wantsImageURL 决定 /v1/images/generations 回 url 还是 b64_json。
//
// OpenAI 语义：response_format 缺省即 url，只有显式 b64_json 才回 base64。
// 注意上游 Gemini 只给 inlineData（base64），没有 URL 可透传——这里的 url 一律是
// 我方 OBS 的签名链接（有效期取 SignedURLTTLHours，默认 7 天）。
func wantsImageURL(responseFormat string) bool {
	return !strings.EqualFold(strings.TrimSpace(responseFormat), "b64_json")
}

// imageResponseFormat 取客户端原始请求里的 response_format；拿不到时按缺省（url）处理。
func imageResponseFormat(info *relaycommon.RelayInfo) string {
	if info == nil || info.Request == nil {
		return ""
	}
	if req, ok := info.Request.(*dto.ImageRequest); ok && req != nil {
		return req.ResponseFormat
	}
	return ""
}

func isInlineImagePart(part *dto.GeminiPart) bool {
	if part == nil || part.InlineData == nil || part.InlineData.Data == "" {
		return false
	}
	return strings.HasPrefix(strings.ToLower(part.InlineData.MimeType), "image/")
}

// markGeminiImageProducedForStream 流式路径拿不到完整响应体，只能按模型判定：命中
// 「支持的图像模型」就认为本次会出图，任务记录留一条没有预览的记录。
func markGeminiImageProducedForStream(c *gin.Context, info *relaycommon.RelayInfo) {
	// ChannelMeta 是嵌入字段，未初始化时读 UpstreamModelName 会空指针。
	if c == nil || info == nil || info.ChannelMeta == nil {
		return
	}
	if model_setting.IsGeminiModelSupportImagine(info.UpstreamModelName) {
		relaycommon.MarkSyncImageProduced(c)
	}
}

func firstInlineImagePart(candidates []dto.GeminiChatCandidate) *dto.GeminiPart {
	for i := range candidates {
		parts := candidates[i].Content.Parts
		for j := range parts {
			if isInlineImagePart(&parts[j]) {
				return &parts[j]
			}
		}
	}
	return nil
}

func extFromImageMime(mime string) string {
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "image/jpeg", "image/jpg":
		return "jpg"
	case "image/webp":
		return "webp"
	case "image/gif":
		return "gif"
	default:
		return "png"
	}
}
