// speech.go —— 语音合成(TTS)的**同步**兼容层:把 OpenAI 的 POST /v1/audio/speech 映射到
// GPUStack 门面的异步 tts 任务上,对下游完全隐藏「自部署引擎是异步任务制」这件事。
//
// 链路与同目录的同步图片链路同构:
//
//	POST /v1/audio/speech(OpenAI 形状)
//	  → ConvertAudioRequest 组门面提交体 {model, task_type:"tts", prompt, user_id, speaker, ...}
//	  → POST {base}/v1/videos(门面统一提交入口,与视频/图片共用)
//	  → 服务端阻塞轮询 GET {base}/v1/videos/{id}
//	  → done 后拿 nfs_path(成品音频在共享 SFS 上的绝对路径)
//	  → 读盘 → 直接回二进制音频(Content-Type 取成品扩展名,认不出时回落请求的 response_format)
//
// 与图片链路的两点差异:
//  1. 图片必须经 OBS 才能对外给 URL,所以强制要求 mediastore.Enabled();TTS 回的是**字节流**,
//     不需要对外 URL,因此只要求 NFS 挂载可读(路径经 ValidateNFSPath 防越权/符号链接逃逸)。
//  2. 图片与 TTS 各用独立信号量:两者产能与耗时量级不同(生图 8~40s、TTS 通常 <10s),
//     共用一个池会让短任务被长任务饿死。
//
// 音色语义(与 relay/channel/task/gpustackplus 的 tts 任务链路保持一致):
//   - vLLM-Omni 系(qwen3-tts / voxcpm / cosyvoice / glm-tts / moss*):OpenAI 的 voice 是**预设
//     音色名**,映射为标量 speaker;零样本克隆走可选的 ref_audio(URL/base64/data-uri/task: 引用),
//     物化落 NFS 后以 input_refs 下发。
//   - IndexTTS-2 系:音色本身就是一段参考音频,没有预设名可言。voice 给的是可取用的音频引用时
//     按参考音色物化;给的是普通名字则就地 400,并指引改用任务 API。
//
// 两个家族的参考音都物化到同一个 input_refs 键 ref_audio —— IndexTTS-2 现在同样跑在
// vLLM-Omni 引擎上,任务链路的 materializeTTSInputs 也是这么写的。
package gpustackplus

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/channel/gpustackplus/nfsinput"
	taskgpustackplus "github.com/QuantumNous/new-api/relay/channel/task/gpustackplus"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service/mediastore"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

// speechBlockingConcurrency TTS 阻塞路径并发上限;满 → 快速 429 skip-retry,不排队占 goroutine。
// 与图片信号量独立(见文件头注释)。可用环境变量按集群产能热调。
var speechBlockingConcurrency = common.GetEnvOrDefault("GPUSTACKPLUS_TTS_BLOCKING_CONCURRENCY", 32)

var speechBlockingSem = make(chan struct{}, speechBlockingConcurrency)

// TTS 轮询预算:合成本身通常 2~10s,但引擎冷启(权重加载)可达分钟级,故 QUEUED 容忍比
// 生图(25s)略宽;总预算 3s 起轮 + 每 3s 一次 × 100 步 ≈ 5 分钟,与生图默认一致。
var (
	speechQueuedWaitSeconds = common.GetEnvOrDefault("GPUSTACKPLUS_TTS_QUEUED_WAIT_SECONDS", 90)
	speechPollMaxSteps      = common.GetEnvOrDefault("GPUSTACKPLUS_TTS_POLL_MAX_STEPS", 100)
)

// speechMaxResultBytes 成品音频读盘上限(字节),防御异常巨大的产物撑爆内存。
var speechMaxResultBytes = int64(common.GetEnvOrDefault("GPUSTACKPLUS_TTS_MAX_RESULT_MB", 64)) << 20

// speechContentTypes 输出容器 → Content-Type。既是「允许透传给引擎的 response_format
// 白名单」(引擎 AudioTaskRequest 继承 OpenAICreateSpeechRequest,原生认 response_format),
// 也是回写响应头的依据 —— 两者共用一张表,避免出现「放行了某种格式却标不出正确 MIME」。
// 不做转码:不在表内即 400,免得客户端拿到与 response_format 不符的字节还以为成功。
var speechContentTypes = map[string]string{
	"mp3":  "audio/mpeg",
	"wav":  "audio/wav",
	"flac": "audio/flac",
	"opus": "audio/opus",
	"ogg":  "audio/ogg",
	"aac":  "audio/aac",
	"m4a":  "audio/mp4",
	"pcm":  "audio/pcm",
}

// speechFormatContextKey 把客户端请求的 response_format 从 ConvertAudioRequest 传到
// DoResponse:成品扩展名不可靠(引擎可能落 .bin / 无扩展名)时,用请求格式兜底标注 MIME。
const speechFormatContextKey = "gpustackplus_speech_response_format"

// speechContentType 决定回写的 Content-Type:成品真实扩展名优先(引擎最终产出什么就标什么),
// 其次回落到客户端请求的格式;都认不出时宁可标 octet-stream,也不谎称是 MP3 —— 客户端按错误
// 的容器去解码,失败得比拿到 octet-stream 更晚也更难查。
func speechContentType(resolvedPath, requestedFormat string) string {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(resolvedPath), "."))
	if ct, ok := speechContentTypes[ext]; ok {
		return ct
	}
	if ct, ok := speechContentTypes[strings.ToLower(strings.TrimSpace(requestedFormat))]; ok {
		return ct
	}
	return "application/octet-stream"
}

// speechPassthroughFields 原样透传给门面的 vLLM-Omni 语音参数(dto.AudioRequest 已建模为
// json.RawMessage)。门面对这些键是通用透传,引擎 AudioTaskRequest 自行消费。
func speechPassthroughFields(request dto.AudioRequest) map[string]any {
	out := make(map[string]any, 6)
	for key, raw := range map[string][]byte{
		"language":                   request.Language,
		"ref_text":                   request.RefText,
		"x_vector_only_mode":         request.XVectorOnlyMode,
		"max_new_tokens":             request.MaxNewTokens,
		"initial_codec_chunk_frames": request.InitialCodecChunkFrames,
	} {
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}
		var v any
		if err := common.Unmarshal(raw, &v); err == nil && v != nil {
			out[key] = v
		}
	}
	return out
}

// rawJSONString 把 json.RawMessage 解成字符串(非字符串或 null 返回 "")。
func rawJSONString(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := common.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return strings.TrimSpace(s)
}

// isMediaRef 判断一个字符串是否是「可取用的音频引用」——URL / data-uri / base64 / task: 引用。
// 用于区分 OpenAI 的预设音色名(如 "alloy")与一段参考音。
func isMediaRef(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if nfsinput.IsTaskRef(s) || strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") ||
		strings.HasPrefix(s, "data:") {
		return true
	}
	// 裸 base64:预设音色名不可能这么长,长度阈值足以区分。
	return len(s) > 256
}

// convertSpeechRequest 组门面 tts 提交体。返回的 error 由 AudioHelper 包成 400 skip-retry。
func (a *Adaptor) convertSpeechRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.AudioRequest) (io.Reader, error) {
	text := strings.TrimSpace(request.Input)
	if text == "" {
		return nil, errors.New("input is required(合成文本不能为空)")
	}
	modelName := firstNonEmpty(info.UpstreamModelName, request.Model, info.OriginModelName)
	if modelName == "" {
		return nil, errors.New("model is required(渠道模型映射与请求 model 均为空)")
	}
	// 字数上限(AudioModelConfig,按模型/全局默认;0=不限制):与任务链路同一把闸,
	// 防止绕开 /v1/videos 走 /v1/audio/speech 提交超长文本。
	if err := common.ValidateAudioTextForModel("tts", text, request.Model, info.OriginModelName, modelName); err != nil {
		return nil, err
	}
	if request.StreamFormat != "" {
		// 门面是「提交 → 轮询 → 取成品」的异步任务,拿不到增量音频帧;与其吐一份
		// 伪装成流的整包,不如明确拒绝,让客户端自己决定降级。
		return nil, errors.New("gpustackplus 语音合成暂不支持流式(stream_format),请去掉该参数")
	}

	body := map[string]any{
		"model":     modelName,
		"task_type": "tts",
		"prompt":    text,
		"user_id":   userIDStr(info),
	}
	for k, v := range speechPassthroughFields(request) {
		body[k] = v
	}
	if s := strings.TrimSpace(request.Instructions); s != "" {
		body["instructions"] = s
	}
	if f := strings.ToLower(strings.TrimSpace(request.ResponseFormat)); f != "" {
		if _, ok := speechContentTypes[f]; !ok {
			return nil, fmt.Errorf("不支持的 response_format: %q(允许:mp3/wav/flac/opus/ogg/aac/m4a/pcm)", f)
		}
		body["response_format"] = f
		// 供 DoResponse 在成品扩展名认不出来时兜底标注 Content-Type。
		c.Set(speechFormatContextKey, f)
	}
	if request.Speed != nil {
		if *request.Speed < 0.25 || *request.Speed > 4 {
			return nil, fmt.Errorf("speed 超出范围 [0.25, 4]:%v", *request.Speed)
		}
		body["speed"] = *request.Speed
	}

	refs, err := a.materializeSpeechInputs(c, info, modelName, request)
	if err != nil {
		return nil, err
	}
	if len(refs) > 0 {
		body["input_refs"] = refs
	}
	// 预设音色:仅 vLLM-Omni 系有,且 voice 不是一段参考音时才作为 speaker 标量下发。
	if voice := strings.TrimSpace(request.Voice); voice != "" && !isMediaRef(voice) {
		if !taskgpustackplus.IsOmniTTSModel(modelName) {
			return nil, fmt.Errorf("模型 %s 的音色是一段参考音频而非预设名:voice 请传音频 URL / data-uri / task: 引用,或改用任务 API(POST /v1/videos, task_type=tts)", modelName)
		}
		body["speaker"] = voice
	}

	data, err := common.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal speech request failed: %w", err)
	}
	return bytes.NewReader(data), nil
}

// materializeSpeechInputs 物化参考音输入,返回 input_refs(field → 相对 NFS 路径数组)。
// 参考音是可选的:预设音色 / 纯文本模型(MOSS-VoiceGenerator、MOSS-SoundEffect)没有参考音。
//
// 来源既可以是显式的 ref_audio 字段,也可以是 voice —— OpenAI 客户端只有 voice 一个位置
// 能放音色,给的是音频引用时按参考音处理。双人对话(MOSS-TTSD 的 ref_audio_2)不在同步
// 链路支持,请走任务 API。中途失败回滚已写文件,避免孤儿(与任务链路 §N2 一致)。
func (a *Adaptor) materializeSpeechInputs(c *gin.Context, info *relaycommon.RelayInfo, modelName string, request dto.AudioRequest) (map[string][]string, error) {
	refAudio := rawJSONString(request.RefAudio)
	if refAudio == "" && isMediaRef(request.Voice) {
		refAudio = strings.TrimSpace(request.Voice)
	}
	if refAudio == "" {
		return nil, nil
	}

	m := nfsinput.NewMaterializer("tts", modelName, userIDStr(info), inputGroupID(info))
	if maxBytes, ok := common.AudioRefAudioMaxBytesForModel("tts", request.Model, info.OriginModelName, modelName); ok {
		m.SetMaxBytes(maxBytes)
	}
	// 两个家族都物化到 ref_audio:IndexTTS-2 现在同样由 vLLM-Omni 引擎服务,任务链路的
	// materializeTTSInputs 也是把 metadata.voice 写进 FieldRefAudio(见 task/gpustackplus)。
	// 按家族分字段会让同一模型在 /v1/audio/speech 与 /v1/videos 两条链路上拿到不同的
	// input_refs 键,引擎那边就取不到参考音了。
	if err := m.AddString(c.Request.Context(), nfsinput.FieldRefAudio, 0, false, refAudio); err != nil {
		m.Cleanup()
		return nil, err
	}
	return m.Refs(), nil
}

// doSpeechResponse 处理门面的提交响应:轮询到成品 → 读 NFS → 回二进制音频。
// 返回的 usage 是占位(按次计费,真实扣费由模型价决定),与同步图片链路口径一致。
func (a *Adaptor) doSpeechResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (any, *types.NewAPIError) {
	taskID, apiErr := parseSubmitTaskID(resp)
	if apiErr != nil {
		return nil, apiErr
	}

	// 并发上限:满 → 快速 429 skip-retry;尽力取消已提交但排不上号的门面任务,不留孤儿。
	select {
	case speechBlockingSem <- struct{}{}:
		defer func() { <-speechBlockingSem }()
	default:
		a.cancelTask(c.Request.Context(), taskID)
		return nil, busyRetryErr("系统繁忙,请稍后再试", http.StatusTooManyRequests)
	}

	st, pErr := a.pollUntilDone(c, taskID, imagePollBudget{
		queuedWait: time.Duration(speechQueuedWaitSeconds) * time.Second,
		maxSteps:   speechPollMaxSteps,
	})
	if pErr != nil {
		if be, ok := pErr.(*types.NewAPIError); ok {
			return nil, be
		}
		return nil, types.NewError(pErr, types.ErrorCodeBadResponse)
	}
	if st.NFSPath == "" {
		return nil, types.NewError(errors.New("语音合成完成但门面未返回 nfs_path"), types.ErrorCodeBadResponse)
	}

	data, resolvedPath, rErr := readNFSResult(st.NFSPath, speechMaxResultBytes)
	if rErr != nil {
		return nil, types.NewError(rErr, types.ErrorCodeBadResponse)
	}
	contentType := speechContentType(resolvedPath, c.GetString(speechFormatContextKey))

	// 直接回二进制音频:不能复用 IOCopyBytesGracefully(它会把提交响应的
	// Content-Type: application/json 一并搬过来,客户端会按 JSON 解析而失败)。
	c.Writer.Header().Set("Content-Type", contentType)
	c.Writer.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	c.Writer.WriteHeader(http.StatusOK)
	if _, wErr := c.Writer.Write(data); wErr != nil {
		return nil, types.NewOpenAIError(wErr, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	// 计费口径与 OpenAI TTS 处理器一致(relay/channel/openai/audio.go):用预扣费阶段算好的
	// prompt token 数结算。按次固定价的模型走 UsePrice 分支、忽略 token;按量倍率计价的模型
	// 则按合成文本长度收费 —— 返回常量 1 会让后者把任意长的文本当成一个 token 结算。
	tokens := info.GetEstimatePromptTokens()
	return &dto.Usage{PromptTokens: tokens, TotalTokens: tokens}, nil
}

// readNFSResult 读取门面产物,返回内容与解析后的真实路径(调用方据此判定 Content-Type)。
// 安全边界:必须落在配置的 NFS 挂载根之下,且解析符号链接后仍在根内(ValidateNFSPath),
// 否则被植入的 symlink 可读到宿主机任意文件。
func readNFSResult(nfsPath string, maxBytes int64) (data []byte, resolvedPath string, err error) {
	root := system_setting.GetMediaStorageSettings().NFSRoot()
	if strings.TrimSpace(root) == "" {
		return nil, "", errors.New("未配置 NFS 挂载根(系统设置→媒体存储),无法读取自部署模型的成品")
	}
	resolved, err := mediastore.ValidateNFSPath(root, nfsPath)
	if err != nil {
		return nil, "", fmt.Errorf("成品路径校验失败: %w", err)
	}
	f, err := os.Open(resolved)
	if err != nil {
		return nil, "", fmt.Errorf("打开成品失败: %w", err)
	}
	defer func() { _ = f.Close() }()

	// 多读 1 字节用于判断是否超限,避免把超大文件整份读进内存。
	data, err = io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("读取成品失败: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, "", fmt.Errorf("成品体积超过上限 %d 字节", maxBytes)
	}
	if len(data) == 0 {
		return nil, "", errors.New("成品为空文件")
	}
	return data, resolved, nil
}

// parseSubmitTaskID 读门面提交响应里的 task_id;图片与语音两条同步链路共用。
func parseSubmitTaskID(resp *http.Response) (string, *types.NewAPIError) {
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return "", types.NewOpenAIError(readErr, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	_ = resp.Body.Close()
	var sr submitResponse
	if uErr := common.Unmarshal(body, &sr); uErr != nil {
		return "", types.NewOpenAIError(fmt.Errorf("unmarshal submit resp: %w, body: %s", uErr, string(body)), types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	if sr.TaskID == "" {
		return "", types.NewError(fmt.Errorf("upstream task_id empty, body: %s", string(body)), types.ErrorCodeBadResponse)
	}
	return sr.TaskID, nil
}
