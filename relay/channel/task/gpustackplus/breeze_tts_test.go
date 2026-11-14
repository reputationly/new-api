package gpustackplus

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relay/channel/gpustackplus/nfsinput"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

// Breeze TTS 2 链路的单元测试。
//
// 这组逻辑的共同特征:**错了两端都不报错**。
//   - voice 被当成说话人标签原样下发 → 引擎把它拼进 prompt,音色劣化但 200;
//   - 参考音物化到 voice 而不是 ref_audio → 门面映射成 spk_audio_path,Breeze 读不到,200;
//   - 高级参数没折进 extra_params → 引擎 extra=ignore 丢掉,200 但参数不生效;
//   - 引擎必拒项没前移 → 用户拿到一条英文 400,排查成本高。
// 所以每一类都由下面的用例钉住。

// breezeWavDataURL 构造一段带合法 WAV 头的最小音频(物化器按魔数校验类别)。
func breezeWavDataURL() string {
	return "data:audio/wav;base64," + base64.StdEncoding.EncodeToString(
		append([]byte("RIFF\x24\x00\x00\x00WAVEfmt "), make([]byte, 32)...))
}

func breezeTestCtx() *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
	return c
}

func breezeTestInfo(model string) *relaycommon.RelayInfo {
	// TaskRelayInfo 必须给:task 链路上它由 GenRelayInfo 恒初始化,适配器据此取
	// PublicTaskID 当 input-group id。
	return &relaycommon.RelayInfo{
		UserId:          7,
		OriginModelName: model,
		TaskRelayInfo:   &relaycommon.TaskRelayInfo{PublicTaskID: "task_breeze"},
	}
}

func useTempNFSRoot(t *testing.T) {
	t.Helper()
	settings := system_setting.GetMediaStorageSettings()
	old := settings.NFSOutputRoot
	settings.NFSOutputRoot = t.TempDir()
	t.Cleanup(func() { settings.NFSOutputRoot = old })
}

// setAudioModelConfig 注入 AudioModelConfig(raw JSON),测试结束还原。
func setAudioModelConfig(t *testing.T, raw string) {
	t.Helper()
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	old, had := common.OptionMap["AudioModelConfig"]
	common.OptionMap["AudioModelConfig"] = raw
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		if had {
			common.OptionMap["AudioModelConfig"] = old
		} else {
			delete(common.OptionMap, "AudioModelConfig")
		}
		common.OptionMapRWMutex.Unlock()
	})
}

// TestLooksLikeAudioRef 锁住「音频引用 vs 说话人标签」的判定边界:
// voice 是两者的共用位置,判错的方向只有两种,都很糟 ——
// 把 S0 当 URL 去下载,或把一段 base64 当标签拼进 prompt。
func TestLooksLikeAudioRef(t *testing.T) {
	for _, s := range []string{"", "   ", "S0", "[S0]", "s12", "alloy", "vivian"} {
		if looksLikeAudioRef(s) {
			t.Fatalf("%q 不该被判成音频引用(它是音色名/说话人标签)", s)
		}
	}
	for _, s := range []string{
		"https://cdn.example.com/a.wav",
		"http://cdn.example.com/a.wav",
		"data:audio/wav;base64,UklGRg==",
		"task:3f2a9c",
		strings.Repeat("A", 257), // 裸 base64
	} {
		if !looksLikeAudioRef(s) {
			t.Fatalf("%q 应判成音频引用", s)
		}
	}
	// file:// 必须判否:物化器(nfsinput.AddString)没有这个分支,判成参考音只会
	// 在后续报一条指错方向的错(见 materializeBreezeTTSInputs 里的显式拒绝)。
	if looksLikeAudioRef("file:///tmp/a.wav") {
		t.Fatal("file:// 不该判成音频引用 —— 物化器不支持这个 scheme")
	}
}

// TestIsBreezeSpeakerTag 锁住标签形态。引擎把 speaker 直接拼成 "[speaker]",
// 拼错不报错但音色劣化,所以这个判定就是唯一的闸。
func TestIsBreezeSpeakerTag(t *testing.T) {
	for _, s := range []string{"S0", "[S0]", "s12", "[s3]", " S1 "} {
		if !IsBreezeSpeakerTag(s) {
			t.Fatalf("%q 应判为说话人标签", s)
		}
	}
	for _, s := range []string{"", "S", "[S0", "S0]", "alloy", "S0x", "[S0]x", "0S"} {
		if IsBreezeSpeakerTag(s) {
			t.Fatalf("%q 不该判为说话人标签", s)
		}
	}
}

// TestNormalizeBreezeSpeaker 比标签判定多认一个 "default",并且**必须归一成 S0**:
// 引擎那行 `voice != "default"` 是大小写敏感的,"Default" 原样下发会变成
// "[Default]" 这种垃圾前缀 —— 不报错但音色静默劣化,所以归一不能交给引擎。
func TestNormalizeBreezeSpeaker(t *testing.T) {
	for _, v := range []string{"default", "DEFAULT", " Default "} {
		tag, ok := NormalizeBreezeSpeaker(v)
		if !ok || tag != "S0" {
			t.Fatalf("%q 应归一成 S0,实际 (%q, %v)", v, tag, ok)
		}
	}
	for _, v := range []string{"S0", "[S0]", "s12", " [S1] "} {
		tag, ok := NormalizeBreezeSpeaker(v)
		if !ok {
			t.Fatalf("%q 应被接受", v)
		}
		if tag != strings.TrimSpace(v) {
			t.Fatalf("%q 应原样返回,实际 %q", v, tag)
		}
	}
	for _, v := range []string{"", "alloy", "vivian", "default0", "defaul", "[default]", "S"} {
		if tag, ok := NormalizeBreezeSpeaker(v); ok {
			t.Fatalf("%q 不该被接受,实际归一成 %q", v, tag)
		}
	}
}

// TestValidateBreezeRefPair 锁住成对校验的两个方向与**唯一一套文案**。
// 引擎 breeze_tts_2.py 的 validate() 两个方向都拒,这里前移成中文 400。
func TestValidateBreezeRefPair(t *testing.T) {
	const model = "tts2"
	if err := ValidateBreezeRefPair("https://x/a.wav", "逐字稿", model); err != nil {
		t.Fatalf("成对时不该报错: %v", err)
	}
	if err := ValidateBreezeRefPair("", "", model); err != nil {
		t.Fatalf("都为空(音色设计)时不该报错: %v", err)
	}
	err := ValidateBreezeRefPair("https://x/a.wav", "   ", model)
	if err == nil || !strings.Contains(err.Error(), "需要参考音频的逐字文稿") {
		t.Fatalf("有音频无文稿应报「需要参考音频的逐字文稿」,实际: %v", err)
	}
	if !strings.Contains(err.Error(), model) {
		t.Errorf("文案应带模型名,实际: %v", err)
	}
	err = ValidateBreezeRefPair("", "逐字稿", model)
	if err == nil || !strings.Contains(err.Error(), "收到了 ref_text 但没有参考音频") {
		t.Fatalf("有文稿无音频应报「收到了 ref_text 但没有参考音频」,实际: %v", err)
	}
}

// TestBreezeExtraParamsFold 锁住高级参数的折叠:
// 键留在顶层会被引擎 extra=ignore 丢掉(200 但参数不生效),
// 覆盖调用方显式给的 extra_params 则会让「显式值优先」这条约定失效。
func TestBreezeExtraParamsFold(t *testing.T) {
	body := map[string]any{
		"guidance_scale": 4.0,
		"temperature":    0.9,
		"seed":           42,
		"max_new_tokens": 512,
		"extra_params":   map[string]any{"guidance_scale": 2.0, "top_k": 40.0},
	}
	foldParamsIntoExtra(body, breezeExtraParamKeys)

	extra, ok := body["extra_params"].(map[string]any)
	if !ok {
		t.Fatal("extra_params 未生成")
	}
	// 已有同名不覆盖:调用方显式给的 2.0 优先于顶层折进来的 4.0
	if got := extra["guidance_scale"]; got != 2.0 {
		t.Fatalf("既有 extra_params.guidance_scale 被覆盖成 %v", got)
	}
	if got := extra["temperature"]; got != 0.9 {
		t.Fatalf("temperature 未折进 extra_params: %v", extra)
	}
	if got := extra["top_k"]; got != 40.0 {
		t.Fatalf("既有 extra_params.top_k 被改动: %v", extra)
	}
	for _, k := range []string{"guidance_scale", "temperature"} {
		if _, still := body[k]; still {
			t.Fatalf("%s 仍留在顶层(既顶层又嵌套)", k)
		}
	}
	// seed / max_new_tokens 是引擎**顶层**字段,折进 extra_params 反而到不了。
	for _, k := range []string{"seed", "max_new_tokens"} {
		if _, in := extra[k]; in {
			t.Fatalf("%s 不该被折进 extra_params", k)
		}
		if _, ok := body[k]; !ok {
			t.Fatalf("%s 应留在顶层", k)
		}
	}
}

// TestValidateBreezeParams 覆盖引擎必然会拒的全部输入,并确认合法值放行。
func TestValidateBreezeParams(t *testing.T) {
	const model = "tts2"
	cases := []struct {
		name    string
		body    map[string]any
		wantMsg string // 空 = 应放行
	}{
		{name: "response_format=mp3", body: map[string]any{"response_format": "mp3"}, wantMsg: "只支持 response_format=wav"},
		{name: "speed=1.2", body: map[string]any{"speed": 1.2}, wantMsg: "不支持 speed"},
		{name: "language=zh", body: map[string]any{"language": "zh"}, wantMsg: "不接受 language"},
		{name: "x_vector_only_mode", body: map[string]any{"x_vector_only_mode": true}, wantMsg: "不支持 x_vector_only_mode"},
		{name: "speaker_embedding", body: map[string]any{"speaker_embedding": []any{0.1}}, wantMsg: "不支持 speaker_embedding"},
		{name: "speaker 非标签", body: map[string]any{"speaker": "alloy"}, wantMsg: "只接受说话人标签"},
		{name: "extra_params 未知键", body: map[string]any{"extra_params": map[string]any{"emo_vector": []any{0.1}}}, wantMsg: "不支持 extra_params 键"},
		// 放行
		{name: "speed=1.0", body: map[string]any{"speed": 1.0}},
		{name: "response_format=wav", body: map[string]any{"response_format": "WAV"}},
		{name: "response_format 未传", body: map[string]any{}},
		{name: "标签 speaker", body: map[string]any{"speaker": "[S0]"}},
		{name: "speaker=default", body: map[string]any{"speaker": "default"}},
		{name: "speaker=DEFAULT", body: map[string]any{"speaker": " DEFAULT "}},
		{name: "合法 extra_params", body: map[string]any{"extra_params": map[string]any{"guidance_scale": 4.0, "top_k": 40.0}}},
		{name: "language 空串", body: map[string]any{"language": "  "}},
		{name: "x_vector_only_mode=false", body: map[string]any{"x_vector_only_mode": false}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateBreezeParams(tc.body, model)
			if tc.wantMsg == "" {
				if err != nil {
					t.Fatalf("应放行,实际报错: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("应报错(%s),实际放行", tc.wantMsg)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("文案应含 %q,实际: %v", tc.wantMsg, err)
			}
			if !strings.Contains(err.Error(), model) {
				t.Errorf("文案应带模型名,实际: %v", err)
			}
		})
	}
}

// TestToFloat speed 在两条链路上的落地类型不同(异步 metadata 解成 float64,
// 同步 DTO 是 float64 指针),这里列出全部可能形态,避免漏判后放给引擎。
func TestToFloat(t *testing.T) {
	if f, ok := toFloat(float64(1.5)); !ok || f != 1.5 {
		t.Fatalf("float64: %v %v", f, ok)
	}
	if f, ok := toFloat(2); !ok || f != 2 {
		t.Fatalf("int: %v %v", f, ok)
	}
	if f, ok := toFloat(int64(3)); !ok || f != 3 {
		t.Fatalf("int64: %v %v", f, ok)
	}
	if f, ok := toFloat(json.Number("4.5")); !ok || f != 4.5 {
		t.Fatalf("json.Number: %v %v", f, ok)
	}
	if _, ok := toFloat("1.2"); ok {
		t.Fatal("字符串不该被当成数字")
	}
	if _, ok := toFloat(nil); ok {
		t.Fatal("nil 不该被当成数字")
	}
}

// TestIsBreezeTTSRequest 锁住「配置声明优先、名字兜底」两条路:
// 声明了却认不出 → 克隆走 IndexTTS 分支撞「voice 必填」;
// 没声明但名字对 → 必须仍然认得出来(运维改名/多环境部署)。
func TestIsBreezeTTSRequest(t *testing.T) {
	setAudioModelConfig(t, "")
	// 名字兜底
	for _, m := range []string{"tts2", "ttx2", "breeze-tts-2"} {
		if !IsBreezeTTSRequest(m, m, m) {
			t.Fatalf("%s 应判为 Breeze(名字兜底)", m)
		}
	}
	if IsBreezeTTSRequest("qwen3-tts", "qwen3-tts", "qwen3-tts") {
		t.Fatal("qwen3-tts 不该判为 Breeze")
	}

	// 配置声明:公开名与上游名(任一命中即可),名字完全不含 breeze/tts2 也能认
	setAudioModelConfig(t, `{"models":{"tts2":{"engine":"breeze"}}}`)
	if !IsBreezeTTSRequest("some-opaque-upstream-name", "tts2", "some-opaque-upstream-name") {
		t.Fatal("声明 engine=breeze 时应按配置判真(公开名命中)")
	}
	// 声明成别的引擎 → 不判真,**即使名字就是 tts2**:声明非空就以声明为准,
	// 否则运维把部署迁到别的引擎后,名字会把声明盖过去、静默走错分支。
	setAudioModelConfig(t, `{"models":{"tts2":{"engine":"indextts2.5"}}}`)
	if IsBreezeTTSRequest("some-opaque-upstream-name", "tts2", "some-opaque-upstream-name") {
		t.Fatal("声明成 indextts2.5 时不该判为 Breeze")
	}
	if IsBreezeTTSRequest("tts2", "tts2", "tts2") {
		t.Fatal("声明成 indextts2.5 时,就算模型名是 tts2 也不该判为 Breeze(声明优先)")
	}
}

// TestMaterializeBreezeTTSInputs 锁住物化字段:
// 必须是 ref_audio(门面映射 ref_audio→ref_audio_path),写成 voice 会被映射成
// spk_audio_path 而 Breeze 不认 —— 表现是"克隆了但输出与参考音无关",不报错。
func TestMaterializeBreezeTTSInputs(t *testing.T) {
	useTempNFSRoot(t)
	const model = "tts2"
	wav := breezeWavDataURL()

	t.Run("克隆:物化到 ref_audio", func(t *testing.T) {
		refs, err := materializeBreezeTTSInputs(breezeTestCtx(), breezeTestInfo(model), "tts", model,
			relaycommon.TaskSubmitReq{
				Model:    model,
				Metadata: map[string]any{"ref_audio": wav, "ref_text": "参考音频里的字"},
			})
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if len(refs[string(nfsinput.FieldRefAudio)]) == 0 {
			t.Fatalf("ref_audio 未物化: %v", refs)
		}
		if len(refs[string(nfsinput.FieldVoice)]) != 0 {
			t.Fatal("不该物化到 voice —— Breeze 引擎不读 spk_audio_path")
		}
	})

	t.Run("voice 里的音频引用按参考音物化", func(t *testing.T) {
		refs, err := materializeBreezeTTSInputs(breezeTestCtx(), breezeTestInfo(model), "tts", model,
			relaycommon.TaskSubmitReq{
				Model:    model,
				Metadata: map[string]any{"voice": wav, "ref_text": "参考音频里的字"},
			})
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if len(refs[string(nfsinput.FieldRefAudio)]) == 0 {
			t.Fatalf("voice 里的音频引用未落到 ref_audio: %v", refs)
		}
	})

	t.Run("音色设计:无参考音时不物化", func(t *testing.T) {
		refs, err := materializeBreezeTTSInputs(breezeTestCtx(), breezeTestInfo(model), "tts", model,
			relaycommon.TaskSubmitReq{Model: model, Metadata: map[string]any{"instructions": "低沉的男声"}})
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if len(refs) != 0 {
			t.Fatalf("无参考音时不该物化: %v", refs)
		}
	})

	// 错误必须是**裸 error**:由 switch 之后统一包 localBadRequest。若这里自己返回
	// *types.NewAPIError,调用点会二次包装,错误码/重试语义容易分叉。
	t.Run("有音频无文稿:裸 error", func(t *testing.T) {
		_, err := materializeBreezeTTSInputs(breezeTestCtx(), breezeTestInfo(model), "tts", model,
			relaycommon.TaskSubmitReq{Model: model, Metadata: map[string]any{"ref_audio": wav}})
		if err == nil {
			t.Fatal("缺 ref_text 应报错")
		}
		if !strings.Contains(err.Error(), "需要参考音频的逐字文稿") {
			t.Fatalf("文案不对: %v", err)
		}
		if _, isAPIErr := err.(*types.NewAPIError); isAPIErr {
			t.Fatal("物化函数应返回裸 error,由调用方统一包 localBadRequest")
		}
	})

	t.Run("ref_audio_2 拒绝", func(t *testing.T) {
		_, err := materializeBreezeTTSInputs(breezeTestCtx(), breezeTestInfo(model), "tts", model,
			relaycommon.TaskSubmitReq{Model: model, Metadata: map[string]any{
				"ref_audio": wav, "ref_text": "字", "ref_audio_2": wav,
			}})
		if err == nil || !strings.Contains(err.Error(), "只接受一条参考音频") {
			t.Fatalf("ref_audio_2 应被拒,实际: %v", err)
		}
	})

	t.Run("file:// 显式拒绝", func(t *testing.T) {
		_, err := materializeBreezeTTSInputs(breezeTestCtx(), breezeTestInfo(model), "tts", model,
			relaycommon.TaskSubmitReq{Model: model, Metadata: map[string]any{
				"ref_audio": "file:///tmp/a.wav", "ref_text": "字",
			}})
		if err == nil || !strings.Contains(err.Error(), "不支持 file://") {
			t.Fatalf("file:// 应被显式拒绝,实际: %v", err)
		}
	})
}

// TestApplyBreezeSpeakerTag 锁住 voice→speaker 的转换:
// 标签要补进 body(voice 已被 legacyInputKeys 剥掉),非标签值必须报错而不是静默下发。
func TestApplyBreezeSpeakerTag(t *testing.T) {
	const model = "tts2"

	t.Run("标签转 speaker", func(t *testing.T) {
		body := map[string]any{}
		if err := applyBreezeSpeakerTag(body, map[string]any{"voice": "[S2]"}, model); err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if body["speaker"] != "[S2]" {
			t.Fatalf("speaker 未写入: %v", body)
		}
	})

	t.Run("音频引用不当标签", func(t *testing.T) {
		body := map[string]any{}
		if err := applyBreezeSpeakerTag(body, map[string]any{"voice": breezeWavDataURL()}, model); err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if _, ok := body["speaker"]; ok {
			t.Fatal("音频引用不该被当成标签下发")
		}
	})

	t.Run("voice=default 归一成 S0", func(t *testing.T) {
		body := map[string]any{}
		if err := applyBreezeSpeakerTag(body, map[string]any{"voice": " Default "}, model); err != nil {
			t.Fatalf("default 不该报错: %v", err)
		}
		if body["speaker"] != "S0" {
			t.Fatalf("default 应归一成 S0(引擎的识别是大小写敏感的): %v", body)
		}
	})

	t.Run("非标签值报错", func(t *testing.T) {
		err := applyBreezeSpeakerTag(map[string]any{}, map[string]any{"voice": "alloy"}, model)
		if err == nil || !strings.Contains(err.Error(), "只接受说话人标签") {
			t.Fatalf("alloy 应被拒,实际: %v", err)
		}
	})

	t.Run("显式 metadata.speaker 优先", func(t *testing.T) {
		body := map[string]any{"speaker": "S1"}
		if err := applyBreezeSpeakerTag(body, map[string]any{"voice": "S0"}, model); err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if body["speaker"] != "S1" {
			t.Fatalf("显式 speaker 被覆盖成 %v", body["speaker"])
		}
	})
}

// TestBuildRequestBodyBreeze 是链路级的拼装测试:验的不是单个函数,而是
// 分支选择 → 物化 → 折叠 → 校验 这一串接起来之后的**最终提交体**。
// 单测各自绿但接线错了,正是这类改动最容易出的问题(物化到 voice、参数没折、
// 校验漏调,三种都不会报错)。
func TestBuildRequestBodyBreeze(t *testing.T) {
	useTempNFSRoot(t)
	const model = "tts2"

	build := func(t *testing.T, metadata map[string]any) (map[string]any, error) {
		t.Helper()
		req := relaycommon.TaskSubmitReq{Model: model, Prompt: "今天天气不错。", Metadata: metadata}
		c := breezeTestCtx()
		c.Set("task_request", req)
		info := breezeTestInfo(model)
		info.ChannelMeta = &relaycommon.ChannelMeta{UpstreamModelName: model}
		reader, err := (&TaskAdaptor{}).BuildRequestBody(c, info)
		if err != nil {
			return nil, err
		}
		raw, readErr := io.ReadAll(reader)
		if readErr != nil {
			t.Fatalf("读请求体失败: %v", readErr)
		}
		var body map[string]any
		if unmarshalErr := common.Unmarshal(raw, &body); unmarshalErr != nil {
			t.Fatalf("解请求体失败: %v", unmarshalErr)
		}
		return body, nil
	}

	t.Run("克隆 + 高级参数", func(t *testing.T) {
		body, err := build(t, map[string]any{
			"task_type":      "tts",
			"ref_audio":      breezeWavDataURL(),
			"ref_text":       "参考音频里的字",
			"instructions":   "保持音色",
			"guidance_scale": 4.0,
			"temperature":    0.9,
			"seed":           42,
			"max_new_tokens": 512,
		})
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		refs, _ := body["input_refs"].(map[string]any)
		if len(refs) == 0 || refs[string(nfsinput.FieldRefAudio)] == nil {
			t.Fatalf("参考音未物化到 ref_audio: %v", body["input_refs"])
		}
		extra, _ := body["extra_params"].(map[string]any)
		if extra["guidance_scale"] != 4.0 || extra["temperature"] != 0.9 {
			t.Fatalf("高级参数未折进 extra_params: %v", body["extra_params"])
		}
		if _, still := body["guidance_scale"]; still {
			t.Fatal("guidance_scale 仍留在顶层,会被引擎丢弃")
		}
		// seed / max_new_tokens 是引擎顶层字段,不能被折走
		if body["seed"] != 42.0 || body["max_new_tokens"] != 512.0 {
			t.Fatalf("seed/max_new_tokens 应留在顶层: seed=%v max_new_tokens=%v",
				body["seed"], body["max_new_tokens"])
		}
		if body["ref_text"] != "参考音频里的字" {
			t.Fatalf("ref_text 应随 metadata 透传: %v", body["ref_text"])
		}
		if _, hasVoice := body["voice"]; hasVoice {
			t.Fatal("voice 必须被 legacyInputKeys 剥掉")
		}
	})

	t.Run("voice 标签转 speaker", func(t *testing.T) {
		body, err := build(t, map[string]any{"task_type": "tts", "voice": "S0"})
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if body["speaker"] != "S0" {
			t.Fatalf("voice=S0 应转成 speaker: %v", body)
		}
		if _, hasVoice := body["voice"]; hasVoice {
			t.Fatal("voice 必须被剥掉")
		}
	})

	// voice="Default"(大小写混写)必须归一成 S0 再下发:引擎的 default 识别是
	// 大小写敏感的,原样发过去会拼出 "[Default]" 这种垃圾前缀且不报错。
	t.Run("voice=Default 归一", func(t *testing.T) {
		body, err := build(t, map[string]any{"task_type": "tts", "voice": "Default"})
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if body["speaker"] != "S0" {
			t.Fatalf("voice=Default 应归一成 speaker=S0: %v", body)
		}
	})

	t.Run("metadata.speaker=default 归一", func(t *testing.T) {
		body, err := build(t, map[string]any{"task_type": "tts", "speaker": " DEFAULT "})
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if body["speaker"] != "S0" {
			t.Fatalf("metadata.speaker=default 应归一成 S0: %v", body)
		}
	})

	// 引擎必拒项必须在网关变成 400 skip-retry,而不是透传到引擎换一条英文报错。
	for _, tc := range []struct {
		name string
		meta map[string]any
		want string
	}{
		{"speed 非 1", map[string]any{"task_type": "tts", "speed": 1.2}, "不支持 speed"},
		{"language", map[string]any{"task_type": "tts", "language": "zh"}, "不接受 language"},
		{"非 wav 格式", map[string]any{"task_type": "tts", "response_format": "mp3"}, "只支持 response_format=wav"},
		{"emo_vector 未知键", map[string]any{"task_type": "tts", "emo_vector": []any{0.1}}, "不支持 extra_params 键"},
		{"voice 非标签", map[string]any{"task_type": "tts", "voice": "alloy"}, "只接受说话人标签"},
	} {
		t.Run("拒绝:"+tc.name, func(t *testing.T) {
			_, err := build(t, tc.meta)
			if err == nil {
				t.Fatal("应被网关拒绝")
			}
			apiErr, ok := err.(*types.NewAPIError)
			if !ok {
				t.Fatalf("应是 *types.NewAPIError(400 skip-retry),实际 %T: %v", err, err)
			}
			if apiErr.StatusCode != http.StatusBadRequest {
				t.Fatalf("状态码应为 400,实际 %d", apiErr.StatusCode)
			}
			if !strings.Contains(apiErr.Error(), tc.want) {
				t.Fatalf("文案应含 %q,实际: %v", tc.want, apiErr)
			}
		})
	}
}
