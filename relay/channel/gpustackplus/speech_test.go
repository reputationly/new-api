package gpustackplus

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/channel/gpustackplus/nfsinput"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func speechTestCtx(t *testing.T) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/speech", nil)
	return c
}

func speechInfo(model string) *relaycommon.RelayInfo {
	// ChannelMeta 在真实链路由 InitChannelMeta 初始化;这里手动装配,
	// 否则 info.UpstreamModelName 会解到 nil 指针。
	return &relaycommon.RelayInfo{
		UserId:          7,
		RelayMode:       relayconstant.RelayModeAudioSpeech,
		OriginModelName: model,
		ChannelMeta:     &relaycommon.ChannelMeta{UpstreamModelName: model},
	}
}

// convertSpeechBody 跑一遍转换并把提交体解回 map,方便断言。
func convertSpeechBody(t *testing.T, model string, request dto.AudioRequest) map[string]any {
	t.Helper()
	a := &Adaptor{}
	reader, err := a.convertSpeechRequest(speechTestCtx(t), speechInfo(model), request)
	require.NoError(t, err)
	raw, err := io.ReadAll(reader)
	require.NoError(t, err)
	var body map[string]any
	require.NoError(t, common.Unmarshal(raw, &body))
	return body
}

func TestConvertSpeechRequestOmniPresetVoice(t *testing.T) {
	speed := 1.25
	body := convertSpeechBody(t, "qwen3-tts", dto.AudioRequest{
		Model:          "qwen3-tts",
		Input:          "你好，世界",
		Voice:          "vivian",
		ResponseFormat: "WAV", // 大小写不敏感
		Speed:          &speed,
	})

	require.Equal(t, "qwen3-tts", body["model"])
	require.Equal(t, "tts", body["task_type"])
	require.Equal(t, "你好，世界", body["prompt"])
	require.Equal(t, "7", body["user_id"])
	// 预设音色走标量 speaker,不物化成参考音。
	require.Equal(t, "vivian", body["speaker"])
	require.NotContains(t, body, "input_refs")
	require.Equal(t, "wav", body["response_format"])
	require.InDelta(t, 1.25, body["speed"], 1e-9)
}

func TestConvertSpeechRequestPassesThroughOmniFields(t *testing.T) {
	body := convertSpeechBody(t, "cosyvoice3", dto.AudioRequest{
		Model:        "cosyvoice3",
		Input:        "hello",
		Voice:        "alloy",
		Instructions: "读慢一点",
		Language:     []byte(`"zh"`),
		MaxNewTokens: []byte(`2048`),
		RefText:      []byte(`null`), // null 视为未提供,不下发
	})

	require.Equal(t, "zh", body["language"])
	require.InDelta(t, float64(2048), body["max_new_tokens"], 1e-9)
	require.Equal(t, "读慢一点", body["instructions"])
	require.NotContains(t, body, "ref_text")
}

func TestConvertSpeechRequestRejectsBadInput(t *testing.T) {
	a := &Adaptor{}
	speedTooHigh := 9.0

	cases := []struct {
		name    string
		model   string
		request dto.AudioRequest
		wantMsg string
	}{
		{
			name:    "空文本",
			model:   "qwen3-tts",
			request: dto.AudioRequest{Model: "qwen3-tts", Input: "   ", Voice: "vivian"},
			wantMsg: "input is required",
		},
		{
			name:    "不支持的输出容器",
			model:   "qwen3-tts",
			request: dto.AudioRequest{Model: "qwen3-tts", Input: "hi", Voice: "vivian", ResponseFormat: "midi"},
			wantMsg: "不支持的 response_format",
		},
		{
			name:    "流式",
			model:   "qwen3-tts",
			request: dto.AudioRequest{Model: "qwen3-tts", Input: "hi", Voice: "vivian", StreamFormat: "sse"},
			wantMsg: "不支持流式",
		},
		{
			name:    "语速越界",
			model:   "qwen3-tts",
			request: dto.AudioRequest{Model: "qwen3-tts", Input: "hi", Voice: "vivian", Speed: &speedTooHigh},
			wantMsg: "speed 超出范围",
		},
		{
			// IndexTTS-2 的音色是一段参考音,没有预设名;给名字要明确报错而不是静默按预设下发。
			name:    "IndexTTS 给预设音色名",
			model:   "indextts2",
			request: dto.AudioRequest{Model: "indextts2", Input: "hi", Voice: "vivian"},
			wantMsg: "音色是一段参考音频而非预设名",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := a.convertSpeechRequest(speechTestCtx(t), speechInfo(tc.model), tc.request)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantMsg)
		})
	}
}

func TestIsMediaRef(t *testing.T) {
	require.True(t, isMediaRef("https://cdn.example.com/a.wav"))
	require.True(t, isMediaRef("data:audio/wav;base64,UklGRg=="))
	require.True(t, isMediaRef("task:abcd1234"))
	require.False(t, isMediaRef("alloy"))
	require.False(t, isMediaRef("  "))
}

func TestReadNFSResultRejectsPathOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	settings := system_setting.GetMediaStorageSettings()
	oldRoot := settings.NFSOutputRoot
	settings.NFSOutputRoot = root
	t.Cleanup(func() { settings.NFSOutputRoot = oldRoot })

	inside := filepath.Join(root, "speech.mp3")
	require.NoError(t, os.WriteFile(inside, []byte("ID3fake"), 0o600))
	escape := filepath.Join(outside, "secret.mp3")
	require.NoError(t, os.WriteFile(escape, []byte("secret"), 0o600))

	data, resolved, err := readNFSResult(inside, speechMaxResultBytes)
	require.NoError(t, err)
	require.Equal(t, []byte("ID3fake"), data)
	// 返回的是解析符号链接后的真实路径(macOS 的 /var → /private/var)。
	wantResolved, evalErr := filepath.EvalSymlinks(inside)
	require.NoError(t, evalErr)
	require.Equal(t, wantResolved, resolved)

	_, _, err = readNFSResult(escape, speechMaxResultBytes)
	require.Error(t, err)

	// 符号链接逃逸:根内的链接指向根外文件,同样必须拒绝。
	link := filepath.Join(root, "link.mp3")
	require.NoError(t, os.Symlink(escape, link))
	_, _, err = readNFSResult(link, speechMaxResultBytes)
	require.Error(t, err)

	_, _, err = readNFSResult(inside, 3) // 超过体积上限
	require.Error(t, err)
}

// 放行的每种 response_format 都必须能标出正确的 Content-Type ——
// 标错容器的字节流,客户端会按 MP3 去解一份 opus/aac/pcm,失败得又晚又难查。
func TestSpeechContentType(t *testing.T) {
	for format, want := range speechContentTypes {
		t.Run("ext:"+format, func(t *testing.T) {
			require.Equal(t, want, speechContentType("/nfs/out/speech."+format, ""))
		})
		t.Run("requested:"+format, func(t *testing.T) {
			// 引擎落了个认不出扩展名的文件时，回落到客户端请求的格式。
			require.Equal(t, want, speechContentType("/nfs/out/speech.bin", format))
		})
	}

	// 文件真实扩展名优先于请求格式：引擎最终产出什么就标什么。
	require.Equal(t, "audio/wav", speechContentType("/nfs/out/a.wav", "mp3"))
	// 大小写与空格容错。
	require.Equal(t, "audio/flac", speechContentType("/nfs/out/a.FLAC", ""))
	require.Equal(t, "audio/opus", speechContentType("/nfs/out/a", "  OPUS "))
	// 两头都认不出时宁可 octet-stream，也不谎称 MP3。
	require.Equal(t, "application/octet-stream", speechContentType("/nfs/out/a.bin", ""))
	require.Equal(t, "application/octet-stream", speechContentType("/nfs/out/a.bin", "midi"))
}

// convertSpeechRequest 必须把请求的 response_format 落到 context，DoResponse 才能兜底标注。
func TestConvertSpeechRequestStashesResponseFormat(t *testing.T) {
	c := speechTestCtx(t)
	_, err := (&Adaptor{}).convertSpeechRequest(c, speechInfo("qwen3-tts"), dto.AudioRequest{
		Model: "qwen3-tts", Input: "hi", Voice: "vivian", ResponseFormat: "OPUS",
	})
	require.NoError(t, err)
	require.Equal(t, "opus", c.GetString(speechFormatContextKey))
}

// 两条链路(同步 /v1/audio/speech 与任务 /v1/videos)必须把参考音物化到同一个 input_refs
// 键;按模型家族分键会让引擎在其中一条链路上取不到参考音。
func TestMaterializeSpeechInputsUsesRefAudioForBothFamilies(t *testing.T) {
	root := t.TempDir()
	settings := system_setting.GetMediaStorageSettings()
	oldRoot := settings.NFSOutputRoot
	settings.NFSOutputRoot = root
	t.Cleanup(func() { settings.NFSOutputRoot = oldRoot })

	// 1x1 PNG 不是音频,但物化只按魔数校验类别 —— 这里只关心落到哪个键,
	// 故用一段 WAV 头保证通过 isAudioBytes。
	wav := "data:audio/wav;base64," + base64.StdEncoding.EncodeToString(
		append([]byte("RIFF\x24\x00\x00\x00WAVEfmt "), make([]byte, 32)...))

	for _, model := range []string{"qwen3-tts", "indextts2"} {
		t.Run(model, func(t *testing.T) {
			refs, err := (&Adaptor{}).materializeSpeechInputs(
				speechTestCtx(t), speechInfo(model), model,
				dto.AudioRequest{Model: model, Input: "hi", Voice: wav},
			)
			require.NoError(t, err)
			require.Contains(t, refs, string(nfsinput.FieldRefAudio))
			require.NotContains(t, refs, string(nfsinput.FieldVoice))
		})
	}
}
