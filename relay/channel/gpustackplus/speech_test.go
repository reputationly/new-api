package gpustackplus

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
			wantMsg: "不支持 response_format=midi",
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

// 回写的 Content-Type 必须与**字节的实际容器**一致 —— 标错容器的字节流,
// 客户端会按错误的容器去解,失败得又晚又难查。
//
// 优先级:真实字节(魔数) > 成品扩展名 > 请求格式 > octet-stream。
// 字节优先是这条契约的要点:扩展名由门面按 task_type 派生,曾经硬编码成 .wav,
// 一旦引擎按 response_format 出了 mp3,扩展名就会和字节打架。
func TestSpeechContentType(t *testing.T) {
	// 各容器"能认出自己"的最小字节头。pcm/opus 没有独立的魔数:
	// pcm 只能靠扩展名或请求格式;opus 落在 OggS 里,魔数只能到"Ogg"这一层,
	// 需要扩展名/请求格式在同一容器内收敛(见 speechContentType 的注释)。
	magic := map[string][]byte{
		"wav":  []byte("RIFF\x24\x00\x00\x00WAVEfmt "),
		"mp3":  []byte("ID3\x04\x00\x00\x00\x00\x00\x00"),
		"flac": []byte("fLaC\x00\x00\x00\x22"),
		"opus": []byte("OggS\x00\x02\x00\x00"),
	}
	for format, want := range speechContentTypes {
		t.Run("ext:"+format, func(t *testing.T) {
			require.Equal(t, want, speechContentType(nil, "/nfs/out/speech."+format, ""))
		})
		t.Run("requested:"+format, func(t *testing.T) {
			// 引擎落了个认不出扩展名的文件时,回落到客户端请求的格式。
			require.Equal(t, want, speechContentType(nil, "/nfs/out/speech.bin", format))
		})
	}
	// 字节说了算:扩展名和请求格式都在说 wav,mp3 字节也必须标 audio/mpeg。
	for format, head := range magic {
		if format == "opus" {
			continue // OggS 需要额外提示才能收敛到 opus,单独用例覆盖
		}
		t.Run("magic:"+format, func(t *testing.T) {
			require.Equal(t, speechContentTypes[format], speechContentType(head, "/nfs/out/a.wav", "wav"))
		})
	}
	// OggS + opus 提示 → audio/opus;没有提示时如实标 audio/ogg(不能谎称 opus)。
	require.Equal(t, "audio/opus", speechContentType(magic["opus"], "/nfs/out/a.opus", ""))
	require.Equal(t, "audio/opus", speechContentType(magic["opus"], "/nfs/out/a.bin", "opus"))
	require.Equal(t, "audio/ogg", speechContentType(magic["opus"], "/nfs/out/a.bin", ""))

	// 只有魔数、没有扩展名(门面落了 .bin)时也能标对。
	require.Equal(t, "audio/mpeg", speechContentType(magic["mp3"], "/nfs/out/a.bin", ""))
	// 魔数认不出才看扩展名:扩展名优先于请求格式。
	require.Equal(t, "audio/wav", speechContentType([]byte{}, "/nfs/out/a.wav", "mp3"))
	// 大小写与空格容错。
	require.Equal(t, "audio/flac", speechContentType(nil, "/nfs/out/a.FLAC", ""))
	require.Equal(t, "audio/opus", speechContentType(nil, "/nfs/out/a", "  OPUS "))
	// 三档都认不出时宁可 octet-stream,也不谎称 MP3。
	require.Equal(t, "application/octet-stream", speechContentType(nil, "/nfs/out/a.bin", ""))
	require.Equal(t, "application/octet-stream", speechContentType([]byte("not audio"), "/nfs/out/a.bin", "midi"))
}

// 请求侧的可选项由模型能力决定,不再用 MIME 表当白名单:
// 网关以前对外承诺 8 种容器,引擎只认 5 种(aac/m4a/ogg 到引擎是 pydantic 422)。
func TestCheckAudioOutputFormatForModel(t *testing.T) {
	for _, format := range common.EngineAudioOutputFormats {
		got, err := common.CheckAudioOutputFormatForModel(strings.ToUpper(format), "tts2")
		require.NoError(t, err)
		require.Equal(t, format, got)
	}
	// 未传 → 空串(交给引擎默认),不是错误。
	got, err := common.CheckAudioOutputFormatForModel("  ", "tts2")
	require.NoError(t, err)
	require.Equal(t, "", got)

	for _, bad := range []string{"aac", "m4a", "ogg", "midi"} {
		t.Run("拒绝:"+bad, func(t *testing.T) {
			_, err := common.CheckAudioOutputFormatForModel(bad, "tts2")
			require.Error(t, err)
			require.Contains(t, err.Error(), "允许:")
			require.Contains(t, err.Error(), "mp3", "文案要列全可选项,否则客户端不知道能用什么")
		})
	}
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
