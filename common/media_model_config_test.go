package common

import "testing"

func setOpt(img, vid string) {
	OptionMapRWMutex.Lock()
	if OptionMap == nil {
		OptionMap = map[string]string{}
	}
	OptionMap["ImageModelSizeConfig"] = img
	OptionMap["VideoModelConfig"] = vid
	OptionMapRWMutex.Unlock()
}

func TestVideoDurationValidation(t *testing.T) {
	setOpt("", `{"models":{"wan2.2-t2v":{"sizes":["1280x720"],"durations":["5","10"]}}}`)
	// 未配置模型:放行
	if err := ValidateVideoDurationForModel("t2v", 999, "", "other"); err != nil {
		t.Fatalf("unconfigured should pass, got %v", err)
	}
	// 时长命中
	if err := ValidateVideoDurationForModel("t2v", 5, "", "wan2.2-t2v"); err != nil {
		t.Fatalf("allowed duration should pass, got %v", err)
	}
	// 时长不中
	if err := ValidateVideoDurationForModel("t2v", 7, "", "wan2.2-t2v"); err == nil {
		t.Fatal("bad duration should reject")
	}
	// 时长走 secondsStr 且带单位 "10s" 兼容(前导整数匹配)
	setOpt("", `{"models":{"m":{"durations":["10s"]}}}`)
	if err := ValidateVideoDurationForModel("t2v", 10, "", "m"); err != nil {
		t.Fatalf("10 vs 10s should match, got %v", err)
	}
}

// 只配了 sizes、没配 durations 的模型:视为该模型未配置任何校验维度,时长一律放行。
func TestSizesOnlyConfigDoesNotGateDuration(t *testing.T) {
	setOpt("", `{"models":{"wan2.2-i2v":{"sizes":["720P","480P"]}}}`)
	if _, configured := VideoDurationsAllowedForModel("i2v", "wan2.2-i2v"); configured {
		t.Fatal("sizes-only config should not count as duration-configured")
	}
	if err := ValidateVideoDurationForModel("i2v", 4, "", "wan2.2-i2v"); err != nil {
		t.Fatalf("sizes-only config should not gate duration, got %v", err)
	}
}

// maxAudioSec:按模型优先于 default,两者都没配才算未配置。
func TestVideoMaxAudioSecForModel(t *testing.T) {
	setOpt("", `{"default":{"maxAudioSec":15},"models":{"infinitetalk-720p":{"maxAudioSec":30}}}`)
	if sec, ok := VideoMaxAudioSecForModel("s2v", "infinitetalk-720p"); !ok || sec != 30 {
		t.Fatalf("per-model should win, got %v %v", sec, ok)
	}
	// 未在 models 里列出的模型回落到 default
	if sec, ok := VideoMaxAudioSecForModel("s2v", "other"); !ok || sec != 15 {
		t.Fatalf("should fall back to default, got %v %v", sec, ok)
	}
	// 候选名按顺序匹配:第一个命中的模型配置生效(公开名 → 映射后的上游名)
	if sec, ok := VideoMaxAudioSecForModel("s2v", "other", "infinitetalk-720p"); !ok || sec != 30 {
		t.Fatalf("later candidate should still match, got %v %v", sec, ok)
	}
	// 只配了别的维度 → 未配置,不限制
	setOpt("", `{"models":{"infinitetalk-720p":{"maxInputMB":50}}}`)
	if sec, ok := VideoMaxAudioSecForModel("s2v", "infinitetalk-720p"); ok || sec != 0 {
		t.Fatalf("maxInputMB-only config should not count as audio-configured, got %v %v", sec, ok)
	}
	// 整体未配置
	setOpt("", "")
	if _, ok := VideoMaxAudioSecForModel("s2v", "infinitetalk-720p"); ok {
		t.Fatal("empty config should report unconfigured")
	}
}

// tab 级配置:同一个模型挂多个玩法时,各玩法的时长白名单互不串台。
// 改造前只按模型名取值,给「文生视频」配的 ["5","10"] 会连带把「图生视频」的 4 秒请求拒掉。
func TestVideoDurationTabScoped(t *testing.T) {
	setOpt("", `{"models":{"wan2.2":{"durations":["5","10"],"tabs":{
		"text2video":{"durations":["5","10"]},
		"image2video":{"durations":["3","4"]}}}}}`)
	// t2v → text2video 这一格
	if err := ValidateVideoDurationForModel("t2v", 10, "", "wan2.2"); err != nil {
		t.Fatalf("t2v 10s should pass, got %v", err)
	}
	if err := ValidateVideoDurationForModel("t2v", 4, "", "wan2.2"); err == nil {
		t.Fatal("t2v 4s should reject (not in text2video whitelist)")
	}
	// i2v → flf2v 这一格未配 → 退回模型级 ["5","10"]
	if err := ValidateVideoDurationForModel("i2v", 4, "", "wan2.2"); err == nil {
		t.Fatal("flf2v tab unset should fall back to model level")
	}
	// r2v → image2video 这一格:4 秒放行、10 秒拒绝(与 t2v 正好相反,证明确实分开取值)
	if err := ValidateVideoDurationForModel("r2v", 4, "", "wan2.2"); err != nil {
		t.Fatalf("r2v 4s should pass, got %v", err)
	}
	if err := ValidateVideoDurationForModel("r2v", 10, "", "wan2.2"); err == nil {
		t.Fatal("r2v 10s should reject (not in image2video whitelist)")
	}
	// 解析不出 tab 的 task_type(如 sr)退回模型级,不会比改造前更严
	if err := ValidateVideoDurationForModel("sr", 5, "", "wan2.2"); err != nil {
		t.Fatalf("unmapped task_type should fall back to model level, got %v", err)
	}
}

// tab 级标量:配了 tab 的取 tab,没配的退模型级,模型级也没有才退 default。
func TestVideoMaxInputBytesTabScoped(t *testing.T) {
	setOpt("", `{"default":{"maxInputMB":10},"models":{"bernini":{"maxInputMB":50,"tabs":{
		"vace":{"maxInputMB":200},
		"image2video":{}}}}}`)
	mb := int64(1024 * 1024)
	if got, ok := VideoMaxInputBytesForModel("v2v", "bernini"); !ok || got != 200*mb {
		t.Fatalf("vace tab should win, got %v %v", got, ok)
	}
	// image2video 这一格是空对象(= 模型挂了这个玩法但没配上限)→ 退模型级
	if got, ok := VideoMaxInputBytesForModel("r2v", "bernini"); !ok || got != 50*mb {
		t.Fatalf("empty tab should fall back to model level, got %v %v", got, ok)
	}
	// 未列出的模型 → default
	if got, ok := VideoMaxInputBytesForModel("v2v", "other"); !ok || got != 10*mb {
		t.Fatalf("should fall back to default, got %v %v", got, ok)
	}
}

// 回归:sizes 与请求尺寸对不上时不再影响放行结果。运营填档位词("720P")而客户端
// 发精确像素("720x1280")是常态,二者无法字符串比较,早前版本会把合法请求拒成 400。
// 这里用同一份配置跑两种请求尺寸,断言二者结果一致——即 size 已完全退出校验决策。
func TestSizeDoesNotAffectValidation(t *testing.T) {
	setOpt("", `{"models":{"wan2.2-i2v":{"sizes":["720P","480P"],"durations":["4","5"]}}}`)
	// 时长命中:无论请求尺寸是否与配置的档位词一致,都放行
	if err := ValidateVideoDurationForModel("i2v", 4, "", "wan2.2-i2v"); err != nil {
		t.Fatalf("duration in whitelist should pass regardless of size, got %v", err)
	}
	// 时长不中:仍然拒绝(证明本用例的配置确实生效,不是因为整体没配才放行)
	if err := ValidateVideoDurationForModel("i2v", 9, "", "wan2.2-i2v"); err == nil {
		t.Fatal("duration outside whitelist should still reject")
	}
}

func setMusicOpt(music string) {
	OptionMapRWMutex.Lock()
	if OptionMap == nil {
		OptionMap = map[string]string{}
	}
	OptionMap["MusicModelConfig"] = music
	OptionMapRWMutex.Unlock()
}

// 音乐引擎族按**配置声明**取,不按模型名 substring。
//
// 锁这条是因为它写错了不报错:模型名里带 "music3" 但没声明 engine 时若被判成
// minimax-music3,体验区就会给它下发 instructions 而不是 ACE-Step 的 lyrics/thinking;
// 反过来声明了却不生效,则 Music3 拿不到 instructions —— 引擎侧直接 400。
func TestMusicEngineFamilyForModel(t *testing.T) {
	setMusicOpt(`{"models":{"my-music":{"engine":"minimax-music3"},"plain":{"maxChars":500}}}`)

	// 声明了就返回,且大小写/空格归一
	if got := MusicEngineFamilyForModel("my-music"); got != MusicEngineMinimaxMusic3 {
		t.Fatalf("declared engine = %q, want %q", got, MusicEngineMinimaxMusic3)
	}
	// 多候选名(公开名 + 渠道重定向后的上游名),任一命中即可
	if got := MusicEngineFamilyForModel("public-alias", "my-music"); got != MusicEngineMinimaxMusic3 {
		t.Fatalf("second candidate = %q, want %q", got, MusicEngineMinimaxMusic3)
	}
	// 配了但没声明 engine → 空串(调用方回退 tab 默认引擎)
	if got := MusicEngineFamilyForModel("plain"); got != "" {
		t.Fatalf("model without engine = %q, want empty", got)
	}
	// 完全没配 → 空串
	if got := MusicEngineFamilyForModel("unknown"); got != "" {
		t.Fatalf("unconfigured model = %q, want empty", got)
	}
	// **名字里带 music3 但没声明 engine 的,不能被当成 minimax-music3**
	setMusicOpt(`{"models":{"minimax-music3-turbo":{"maxChars":500}}}`)
	if got := MusicEngineFamilyForModel("minimax-music3-turbo"); got != "" {
		t.Fatalf("name-only match = %q, want empty (判据必须是配置声明而非模型名)", got)
	}
	// 没有 MusicModelConfig 时不 panic
	setMusicOpt("")
	if got := MusicEngineFamilyForModel("my-music"); got != "" {
		t.Fatalf("empty config = %q, want empty", got)
	}
}

func setAudioOpt(audio string) {
	OptionMapRWMutex.Lock()
	if OptionMap == nil {
		OptionMap = map[string]string{}
	}
	OptionMap["AudioModelConfig"] = audio
	OptionMapRWMutex.Unlock()
}

// 语音引擎族按**配置声明**取,不按模型名 substring。
//
// 锁这条是因为它两个方向都静默:声明了却不生效 → IndexTTS-2.5 的 lang /
// text_normalization 留在 body 顶层,被引擎 AudioTaskRequest(extra=ignore)丢掉,
// 用户看到的是"选了语种但没生效";反过来把没声明的模型误判成 2.5 → 会把别的 TTS
// 引擎的顶层 lang 搬进 extra_params 并从顶层删掉。
func TestAudioEngineFamilyForModel(t *testing.T) {
	setAudioOpt(`{"models":{"my-tts":{"engine":"indextts2.5"},"plain":{"maxChars":500}}}`)

	if got := AudioEngineFamilyForModel("my-tts"); got != AudioEngineIndexTTS25 {
		t.Fatalf("declared engine = %q, want %q", got, AudioEngineIndexTTS25)
	}
	// 多候选名(公开名 + 渠道重定向后的上游名),任一命中即可
	if got := AudioEngineFamilyForModel("public-alias", "my-tts"); got != AudioEngineIndexTTS25 {
		t.Fatalf("second candidate = %q, want %q", got, AudioEngineIndexTTS25)
	}
	// 配了但没声明 engine → 空串
	if got := AudioEngineFamilyForModel("plain"); got != "" {
		t.Fatalf("model without engine = %q, want empty", got)
	}
	// 完全没配 / 配置为空 → 空串,不 panic
	if got := AudioEngineFamilyForModel("unknown"); got != "" {
		t.Fatalf("unconfigured model = %q, want empty", got)
	}
	setAudioOpt("")
	if got := AudioEngineFamilyForModel("my-tts"); got != "" {
		t.Fatalf("empty config = %q, want empty", got)
	}

	// **名字里带 2.5 但没声明 engine 的,不能被当成 IndexTTS-2.5**
	setAudioOpt(`{"models":{"indextts-2.5-preview":{"maxChars":500}}}`)
	if got := AudioEngineFamilyForModel("indextts-2.5-preview"); got != "" {
		t.Fatalf("name-only match = %q, want empty(判据是声明不是模型名)", got)
	}
}
