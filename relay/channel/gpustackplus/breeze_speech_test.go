package gpustackplus

import (
	"encoding/base64"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/stretchr/testify/require"
)

// 同步口(/v1/audio/speech)对 Breeze TTS 2 的适配。
//
// 两条链路必须等能力:同步口漏一道闸,用户拿到的就是引擎的英文 400;
// 同步口多做一步(比如把 voice 里的音频引用也当标签),就会与异步口语义分叉。

func breezeSpeechWav() string {
	return "data:audio/wav;base64," + base64.StdEncoding.EncodeToString(
		append([]byte("RIFF\x24\x00\x00\x00WAVEfmt "), make([]byte, 32)...))
}

func useTempNFSRootForSpeech(t *testing.T) {
	t.Helper()
	settings := system_setting.GetMediaStorageSettings()
	old := settings.NFSOutputRoot
	settings.NFSOutputRoot = t.TempDir()
	t.Cleanup(func() { settings.NFSOutputRoot = old })
}

// TestConvertSpeechRequestBreezeClone 锁住克隆:参考音物化进 input_refs.ref_audio
// (不是 voice —— 那会被门面映射成 spk_audio_path,Breeze 不认),ref_text 随 body 下发。
func TestConvertSpeechRequestBreezeClone(t *testing.T) {
	useTempNFSRootForSpeech(t)
	wav := breezeSpeechWav()

	body := convertSpeechBody(t, "tts2", dto.AudioRequest{
		Model:          "tts2",
		Input:          "今天天气不错。",
		Voice:          wav, // OpenAI 客户端只有 voice 能放音色
		RefText:        []byte(`"参考音频里逐字说的话"`),
		Instructions:   "保持音色，语气平缓",
		ResponseFormat: "wav",
	})

	refs, ok := body["input_refs"].(map[string]any)
	require.True(t, ok, "input_refs 未生成: %v", body)
	require.NotEmpty(t, refs["ref_audio"], "参考音必须物化到 ref_audio")
	require.Empty(t, refs["voice"], "不该物化到 voice(Breeze 读 spk_audio_path 会拿不到)")
	require.Equal(t, "参考音频里逐字说的话", body["ref_text"])
	require.Equal(t, "保持音色，语气平缓", body["instructions"])
	require.NotContains(t, body, "speaker", "音频引用不该被当成说话人标签")
	require.Equal(t, "wav", body["response_format"])
}

// TestConvertSpeechRequestBreezeRefAudio 显式 ref_audio 字段优先于 voice。
func TestConvertSpeechRequestBreezeRefAudio(t *testing.T) {
	useTempNFSRootForSpeech(t)

	body := convertSpeechBody(t, "tts2", dto.AudioRequest{
		Model:    "tts2",
		Input:    "你好。",
		RefAudio: []byte(`"` + breezeSpeechWav() + `"`),
		RefText:  []byte(`"逐字稿"`),
	})

	refs, ok := body["input_refs"].(map[string]any)
	require.True(t, ok, "input_refs 未生成: %v", body)
	require.NotEmpty(t, refs["ref_audio"])
}

// TestConvertSpeechRequestBreezePairing 成对校验两个方向都要拦(引擎两个方向都拒)。
func TestConvertSpeechRequestBreezePairing(t *testing.T) {
	useTempNFSRootForSpeech(t)
	a := &Adaptor{}

	// 有音频无文稿
	_, err := a.convertSpeechRequest(speechTestCtx(t), speechInfo("tts2"), dto.AudioRequest{
		Model:    "tts2",
		Input:    "你好。",
		RefAudio: []byte(`"` + breezeSpeechWav() + `"`),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "需要参考音频的逐字文稿")

	// 有文稿无音频
	_, err = a.convertSpeechRequest(speechTestCtx(t), speechInfo("tts2"), dto.AudioRequest{
		Model:   "tts2",
		Input:   "你好。",
		RefText: []byte(`"逐字稿"`),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "收到了 ref_text 但没有参考音频")
}

// TestConvertSpeechRequestBreezeSpeakerTag 锁住 voice 的标签语义与静默劣化风险:
// S0 要变成 speaker 标量,alloy 这类音色名必须 400 —— 引擎会把 speaker 直接拼进
// prompt,下发 "[alloy]" 不报错但音色劣化。
func TestConvertSpeechRequestBreezeSpeakerTag(t *testing.T) {
	body := convertSpeechBody(t, "tts2", dto.AudioRequest{
		Model:          "tts2",
		Input:          "你好。",
		Voice:          "S0",
		ResponseFormat: "wav",
	})
	require.Equal(t, "S0", body["speaker"])
	require.NotContains(t, body, "input_refs", "标签不是参考音,不该物化")

	// default:引擎自己会折成 S0,同步口必须放行(客户端显式发默认值很常见)
	defaultBody := convertSpeechBody(t, "tts2", dto.AudioRequest{
		Model: "tts2", Input: "你好。", Voice: " Default ", ResponseFormat: "wav",
	})
	require.Equal(t, "S0", defaultBody["speaker"], "default 必须归一成 S0 再下发")

	_, err := (&Adaptor{}).convertSpeechRequest(speechTestCtx(t), speechInfo("tts2"), dto.AudioRequest{
		Model: "tts2",
		Input: "你好。",
		Voice: "alloy",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "只接受说话人标签")
}

// TestConvertSpeechRequestBreezeParams 同步口不再漏阀(A3):language / speed /
// 引擎认不出的容器 / 未知 extra_params 都要在网关变中文 400,而不是引擎英文 400。
// 注意 wav 与 mp3 现在**都**是合法容器(门面按 response_format 命名产物),见下面的专项用例。
func TestConvertSpeechRequestBreezeParams(t *testing.T) {
	speed := 1.2
	one := 1.0
	cases := []struct {
		name    string
		request dto.AudioRequest
		wantMsg string
	}{
		{
			name:    "language",
			request: dto.AudioRequest{Model: "tts2", Input: "你好。", Voice: "S0", Language: []byte(`"zh"`)},
			wantMsg: "不接受 language",
		},
		{
			name:    "speed 非 1",
			request: dto.AudioRequest{Model: "tts2", Input: "你好。", Voice: "S0", Speed: &speed},
			wantMsg: "不支持 speed",
		},
		{
			name:    "x_vector_only_mode",
			request: dto.AudioRequest{Model: "tts2", Input: "你好。", Voice: "S0", XVectorOnlyMode: []byte(`true`)},
			wantMsg: "不支持 x_vector_only_mode",
		},
		{
			name:    "response_format=aac(引擎不认)",
			request: dto.AudioRequest{Model: "tts2", Input: "你好。", Voice: "S0", ResponseFormat: "aac"},
			wantMsg: "不支持 response_format=aac",
		},
		{
			name:    "extra_params 未知键",
			request: dto.AudioRequest{Model: "tts2", Input: "你好。", Voice: "S0", ExtraParams: []byte(`{"emo_vector":[0.1]}`)},
			wantMsg: "不支持 extra_params 键",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := (&Adaptor{}).convertSpeechRequest(speechTestCtx(t), speechInfo("tts2"), tc.request)
			require.Error(t, err)
			require.True(t, strings.Contains(err.Error(), tc.wantMsg),
				"文案应含 %q,实际: %v", tc.wantMsg, err)
		})
	}

	// speed=1.0 放行(引擎只拒 != 1.0)
	body := convertSpeechBody(t, "tts2", dto.AudioRequest{
		Model: "tts2", Input: "你好。", Voice: "S0", Speed: &one, ResponseFormat: "wav",
	})
	require.InDelta(t, 1.0, body["speed"], 1e-9)
}

// TestConvertSpeechRequestBreezeAcceptsEveryEngineContainer 锁住「wav 与 mp3 同时支持」:
// 引擎能编码的每一种容器都必须原样传到提交体,一个都不能在网关被吞掉或改写。
//
// 这条用例是 wav-only 限制拆除后的正样本 —— 之前 mp3 在这里是必须报错的,
// 现在必须放行,且**下发值要与请求值一致**(不能悄悄回落成 wav:那样客户端要 mp3
// 却拿到 wav,比报错更难发现)。
func TestConvertSpeechRequestBreezeAcceptsEveryEngineContainer(t *testing.T) {
	for _, format := range common.EngineAudioOutputFormats {
		t.Run(format, func(t *testing.T) {
			body := convertSpeechBody(t, "tts2", dto.AudioRequest{
				Model: "tts2", Input: "你好。", Voice: "S0", ResponseFormat: strings.ToUpper(format),
			})
			require.Equal(t, format, body["response_format"], "大小写应归一后原样下发")
		})
	}

	// 显式不传 → 不写该键,由引擎默认(wav)兜住。写死 wav 会让"请求没提格式"变成
	// 网关替客户端做决定,以后引擎改默认值就要改网关。
	body := convertSpeechBody(t, "tts2", dto.AudioRequest{Model: "tts2", Input: "你好。", Voice: "S0"})
	require.NotContains(t, body, "response_format")
}

// TestConvertSpeechRequestBreezeKeepsOtherModelsIntact 锁住 C2 的决策:
// Breeze 的特有校验**只对 Breeze 生效**,同渠道其它模型的能力不能被连带影响。
func TestConvertSpeechRequestBreezeKeepsOtherModelsIntact(t *testing.T) {
	body := convertSpeechBody(t, "qwen3-tts", dto.AudioRequest{
		Model: "qwen3-tts", Input: "hi", Voice: "vivian", ResponseFormat: "mp3",
	})
	require.Equal(t, "mp3", body["response_format"])
	require.Equal(t, "vivian", body["speaker"])

	// IndexTTS 的「给预设音色名」仍是原来的报错(没有被 Breeze 分支吞掉)
	_, err := (&Adaptor{}).convertSpeechRequest(speechTestCtx(t), speechInfo("indextts2"), dto.AudioRequest{
		Model: "indextts2", Input: "hi", Voice: "vivian",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "音色是一段参考音频而非预设名")
}

// TestConvertSpeechRequestPassesSeedAndExtraParams 同步口补齐 seed / extra_params。
// seed 用显式 0 验证「显式零值必须下发」(否则调用方拿不到可复现行为)。
func TestConvertSpeechRequestPassesSeedAndExtraParams(t *testing.T) {
	body := convertSpeechBody(t, "tts2", dto.AudioRequest{
		Model:          "tts2",
		Input:          "你好。",
		Voice:          "S0",
		Seed:           []byte(`0`),
		ExtraParams:    []byte(`{"guidance_scale":4.0,"top_k":40}`),
		ResponseFormat: "wav",
	})
	require.Contains(t, body, "seed", "显式 seed=0 必须下发")
	require.InDelta(t, 0.0, body["seed"], 1e-9)

	extra, ok := body["extra_params"].(map[string]any)
	require.True(t, ok, "extra_params 未透传: %v", body)
	require.InDelta(t, 4.0, extra["guidance_scale"], 1e-9)
	require.InDelta(t, 40.0, extra["top_k"], 1e-9)
}

// TestConvertSpeechRequestBreezeRejectsFileURI ref_audio 用了物化器不支持的 scheme,
// 必须在网关给出可操作文案,而不是一路错到 base64 解析。
func TestConvertSpeechRequestBreezeRejectsFileURI(t *testing.T) {
	_, err := (&Adaptor{}).convertSpeechRequest(speechTestCtx(t), speechInfo("tts2"), dto.AudioRequest{
		Model:    "tts2",
		Input:    "你好。",
		RefAudio: []byte(`"file:///tmp/a.wav"`),
		RefText:  []byte(`"逐字稿"`),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "不支持 file://")
}

// TestConvertAudioRequestReturns400ThroughHandlerWrapping 锁住同步口的**状态码**:
// 组包阶段的错误(参数非法、参考音下不到)是客户端问题,必须是 400 ——
// 实测现网旧代码把 voice=S0 报成 500,客户端只会以为网关挂了。
//
// 这里连"handler 再包一层会不会变回 500"一起测:relay/audio_handler.go 用
// types.NewError(..., ErrorCodeConvertRequestFailed, skipRetry) 包,靠 errors.As
// 命中这里准备好的 *types.NewAPIError 才能保住 400。
func TestConvertAudioRequestReturns400ThroughHandlerWrapping(t *testing.T) {
	for _, tc := range []struct {
		name    string
		req     dto.AudioRequest
		wantMsg string
	}{
		{"空文本", dto.AudioRequest{Model: "tts2", Input: "   "}, "input is required"},
		{"voice 非标签", dto.AudioRequest{Model: "tts2", Input: "你好。", Voice: "alloy"}, "只接受说话人标签"},
		{"缺逐字文稿", dto.AudioRequest{Model: "tts2", Input: "你好。", RefAudio: []byte(`"https://example.com/a.wav"`)}, "需要参考音频的逐字文稿"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := (&Adaptor{}).ConvertAudioRequest(speechTestCtx(t), speechInfo("tts2"), tc.req)
			require.Error(t, err)
			apiErr, ok := err.(*types.NewAPIError)
			require.True(t, ok, "应是 *types.NewAPIError,实际 %T: %v", err, err)
			require.Equal(t, http.StatusBadRequest, apiErr.StatusCode)

			wrapped := types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
			require.Equal(t, http.StatusBadRequest, wrapped.StatusCode,
				"handler 的再包装不能把 400 变回 500")
			require.Contains(t, wrapped.Error(), tc.wantMsg, "文案不能被包装吃掉")
		})
	}
}

func TestConvertAudioRequestBreezeRejectsBeforeMaterialization(t *testing.T) {
	for _, name := range []string{"speed", "voice"} {
		t.Run(name, func(t *testing.T) {
			useTempNFSRootForSpeech(t)
			request := dto.AudioRequest{
				Model: "tts2", Input: "你好",
				RefAudio: []byte(`"` + breezeSpeechWav() + `"`), RefText: []byte(`"逐字稿"`),
			}
			if name == "speed" {
				speed := 1.2
				request.Speed = &speed
			} else {
				request.Voice = "alloy"
			}
			_, err := (&Adaptor{}).ConvertAudioRequest(speechTestCtx(t), speechInfo("tts2"), request)
			require.Error(t, err)
			apiErr, ok := err.(*types.NewAPIError)
			require.True(t, ok)
			require.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
			entries, err := os.ReadDir(system_setting.GetMediaStorageSettings().NFSOutputRoot)
			require.NoError(t, err)
			require.Empty(t, entries)
		})
	}
}
