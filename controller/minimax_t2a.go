package controller

// MiniMax 官方语音协议（POST /v1/t2a_v2）的兼容层。
//
// MiniMax Design 的内置 minimax 适配器「语音」正是打这条路径（同步、output_format=url），
// 请求体是 {model, text, stream, voice_setting, audio_setting, ...}。我们自己的语音入口是
// OpenAI 形状的 POST /v1/audio/speech（返回二进制音频），两者字段与响应形态都不同，
// 所以这里做一次真转换：
//
//	t2a_v2 请求 → AudioRequest（input/voice/response_format/speed）→ 复用 controller.Relay
//	二进制音频  → hex 字符串，塞进 {data:{audio}}（MiniMax 客户端认 hex，见适配器 audioOutputs）
//
// 为什么不返回 URL：返回 URL 需要把音频落盘 + 签发，而 hex 是官方原生形态（默认就是 hex），
// 适配器也能直接解。一条 30 秒语音约 0.5 MB，hex 后 1 MB，可接受。

import (
	"bytes"
	"encoding/hex"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/channel/gpustackplus/nfsinput"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

// minimaxT2ARequest 是 MiniMax 语音请求的字段子集。
type minimaxT2ARequest struct {
	Model string `json:"model"`
	Text  string `json:"text"`
	// Stream 我们只支持同步：官方 stream=true 是 SSE，网关这条链没有做流式透传。
	Stream       *bool `json:"stream"`
	VoiceSetting struct {
		VoiceID string   `json:"voice_id"`
		Speed   *float64 `json:"speed"`
		Vol     *float64 `json:"vol"`
		Pitch   *float64 `json:"pitch"`
		Emotion string   `json:"emotion"`
	} `json:"voice_setting"`
	AudioSetting struct {
		Format string `json:"format"`
	} `json:"audio_setting"`
	LanguageBoost string `json:"language_boost"`
	OutputFormat  string `json:"output_format"`
}

// MiniMaxV2Speech 处理 POST /v1/t2a_v2。
//
// 路由上必须已经跑过 TokenAuth + Distribute；本函数负责改写请求体与路径，
// 然后交给既有的语音链路（controller.Relay），最后把二进制响应转成 MiniMax 形态。
func MiniMaxV2Speech(c *gin.Context) {
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		minimaxSpeechError(c, "failed to read request body: "+err.Error())
		return
	}
	raw, err := storage.Bytes()
	if err != nil {
		minimaxSpeechError(c, "failed to read request body: "+err.Error())
		return
	}
	audioReq, message := convertT2ARequest(raw)
	if message != "" {
		minimaxSpeechError(c, message)
		return
	}
	body, err := common.Marshal(audioReq)
	if err != nil {
		minimaxSpeechError(c, "failed to build upstream request: "+err.Error())
		return
	}
	if err := common.ReplaceRequestBody(c, body); err != nil {
		minimaxSpeechError(c, "failed to replace request body: "+err.Error())
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	c.Request.ContentLength = int64(len(body))
	c.Request.Header.Set("Content-Type", "application/json")
	// relay mode 由路径推出：必须改写成我们自己的语音入口。
	c.Request.URL.Path = "/v1/audio/speech"

	// 捕获下游写出的二进制音频（语音链路直接写 c.Writer）。
	capture := &binaryCaptureWriter{ResponseWriter: c.Writer}
	c.Writer = capture
	func() {
		defer func() { c.Writer = capture.ResponseWriter }()
		Relay(c, types.RelayFormatOpenAIAudio)
	}()

	if capture.status != 0 && (capture.status < 200 || capture.status >= 300) {
		// 失败：下游已经写了 OpenAI 形状的错误体，原样透传，客户端能读到 message。
		capture.flush(c)
		return
	}
	audio := capture.buf.Bytes()
	if len(audio) == 0 {
		minimaxSpeechError(c, "upstream returned an empty audio stream")
		return
	}
	// **必须清掉下游留下的 Content-Type / Content-Length**：语音适配器在写二进制时
	// 显式设了 audio/wav + 音频字节数，而这几百 KB 的音频只进了我们的缓冲区。
	// 不清的话真响应会带着"audio/wav + 音频长度"的头去装一段 JSON（实测客户端拿到
	// 200 但 body 为空）——头是共享的 header map，替换 writer 挡不住。
	resetResponseHeaders(c)
	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"audio":  hex.EncodeToString(audio),
			"status": 0,
		},
		"extra_info": gin.H{
			"audio_format": minimaxAudioFormat(audio, audioReq.ResponseFormat),
			"audio_size":   len(audio),
		},
		"base_resp": gin.H{"status_code": 0, "status_msg": "success"},
	})
}

// minimaxAudioFormat 报**字节的真实容器**,不是请求值。
//
// 两者可以合法地不一致(客户端省略 audio_setting.format、或上游按能力回落到默认容器),
// 而 MiniMax 客户端会拿这个字段决定怎么落盘/解码:报错了就是"扩展名与字节不符",
// 症状是播放器报错、服务端却一切正常。认不出魔数时才回落到请求格式。
func minimaxAudioFormat(audio []byte, requested string) string {
	if f := strings.TrimPrefix(nfsinput.AudioExtFromMagic(audio), "."); f != "" {
		return f
	}
	return requested
}

// binaryCaptureWriter 缓冲下游写出的响应（状态码 + body），供调用方决定怎么回写。
type binaryCaptureWriter struct {
	gin.ResponseWriter
	buf    bytes.Buffer
	status int
}

func (w *binaryCaptureWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
}

func (w *binaryCaptureWriter) WriteHeaderNow() {}

func (w *binaryCaptureWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.buf.Write(b)
}

func (w *binaryCaptureWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }

func (w *binaryCaptureWriter) Status() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

func (w *binaryCaptureWriter) Size() int     { return w.buf.Len() }
func (w *binaryCaptureWriter) Written() bool { return w.status != 0 || w.buf.Len() > 0 }
func (w *binaryCaptureWriter) Flush()        {}

// flush 把捕获到的内容原样写到真 writer（失败路径用）。
func (w *binaryCaptureWriter) flush(c *gin.Context) {
	target := w.ResponseWriter
	// Content-Length 一律重算：下游可能是流式写出的，长度与缓冲后的字节数未必一致。
	target.Header().Del("Content-Length")
	target.WriteHeader(w.Status())
	_, _ = target.Write(w.buf.Bytes())
}

// resetResponseHeaders 清掉下游写响应时留下的实体头。
//
// 我们替换了 c.Writer 但**没有隔离 header map**（gin 的 ResponseWriter 共享同一份），
// 所以二进制路径设过的 Content-Type / Content-Length 会残留到我们改写后的 JSON 上。
func resetResponseHeaders(c *gin.Context) {
	h := c.Writer.Header()
	h.Del("Content-Type")
	h.Del("Content-Length")
}

// minimaxSpeechError 用 MiniMax 的错误信封回错（base_resp 是官方约定的位置）。
func minimaxSpeechError(c *gin.Context, message string) {
	c.JSON(http.StatusBadRequest, gin.H{
		"base_resp": gin.H{"status_code": 1004, "status_msg": message},
	})
	c.Abort()
}

// convertT2ARequest 把 MiniMax 语音请求转成我们自己的语音请求。
//
// 只映射我们真会用的字段：text → input、voice_id → voice、audio_setting.format →
// response_format、voice_setting.speed → speed。vol / pitch / emotion / language_boost
// 我们的上游链路没有对应开关，忽略它们（不报错：这些是音色微调项，缺了不影响可用性）。
//
// 返回 (请求, "") 或 (零值, 错误文案)；抽成纯函数是为了能单测——控制器本身要跑完整条
// 计费/转发链路，很难在单测里覆盖。
func convertT2ARequest(raw []byte) (dto.AudioRequest, string) {
	var req minimaxT2ARequest
	if err := common.Unmarshal(raw, &req); err != nil {
		return dto.AudioRequest{}, "invalid params: request body is not valid JSON: " + err.Error()
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		return dto.AudioRequest{}, "invalid params: model is required"
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		return dto.AudioRequest{}, "invalid params: text is required"
	}
	if req.Stream != nil && *req.Stream {
		return dto.AudioRequest{}, "invalid params: stream=true is not supported by this gateway; set stream=false"
	}
	out := dto.AudioRequest{
		Model:          model,
		Input:          text,
		Voice:          strings.TrimSpace(req.VoiceSetting.VoiceID),
		ResponseFormat: strings.ToLower(strings.TrimSpace(req.AudioSetting.Format)),
		Speed:          req.VoiceSetting.Speed,
	}
	if out.ResponseFormat == "" {
		out.ResponseFormat = "mp3"
	}
	return out, ""
}
