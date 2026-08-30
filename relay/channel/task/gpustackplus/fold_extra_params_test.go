package gpustackplus

import "testing"

// extra_params 折叠的回归测试。
//
// 折叠本身是「静默」类逻辑:漏折 → 键留在 body 顶层,被引擎 AudioTaskRequest
// (继承 OpenAICreateSpeechRequest,Pydantic extra=ignore)丢掉,不报错;
// 多折 → 键从顶层消失,读顶层的引擎拿不到,同样不报错。所以必须由测试守住。

// 情感标量对所有 tts 请求无条件折叠(既有行为)。
func TestFoldEmotionKeysUnconditional(t *testing.T) {
	body := map[string]any{
		"emo_vector":   []float64{0.7, 0, 0, 0, 0, 0.4, 0, 0},
		"emo_alpha":    0.65,
		"use_emo_text": true,
		"input":        "文本",
	}
	foldParamsIntoExtra(body, indexTTS2EmotionKeys)

	extra, ok := body["extra_params"].(map[string]any)
	if !ok {
		t.Fatal("extra_params 未生成")
	}
	for _, k := range []string{"emo_vector", "emo_alpha", "use_emo_text"} {
		if _, in := extra[k]; !in {
			t.Fatalf("%s 未折进 extra_params", k)
		}
		if _, still := body[k]; still {
			t.Fatalf("%s 仍留在顶层(会造成既顶层又嵌套的歧义)", k)
		}
	}
	// 非折叠键不动
	if body["input"] != "文本" {
		t.Fatal("input 被误折")
	}
}

// **本次的关键约束**:lang / text_normalization 只在模型声明了 2.5 引擎族时才折。
//
// 折叠对所有 taskType=="tts" 的请求都跑,不止 IndexTTS 系。lang 是个足够通用的
// 键名——无条件折等于把任何一个读顶层 lang 的 TTS 引擎的参数搬走并从顶层删掉。
func TestFold25KeysOnlyWhenGated(t *testing.T) {
	// 未加档(非 2.5 模型):lang 必须原样留在顶层
	body := map[string]any{"lang": "ja", "text_normalization": false}
	foldParamsIntoExtra(body, indexTTS2EmotionKeys)
	if body["lang"] != "ja" {
		t.Fatalf("非 2.5 模型的顶层 lang 被搬走了: %v", body["lang"])
	}
	if _, ok := body["extra_params"]; ok {
		t.Fatal("没有可折的键时不该凭空造出 extra_params")
	}

	// 加档(2.5):两个键都折进去
	keys := append(append([]string{}, indexTTS2EmotionKeys...), indexTTS25ExtraKeys...)
	body = map[string]any{"lang": "ja", "text_normalization": false}
	foldParamsIntoExtra(body, keys)
	extra, ok := body["extra_params"].(map[string]any)
	if !ok {
		t.Fatal("extra_params 未生成")
	}
	if extra["lang"] != "ja" {
		t.Fatalf("lang 未折进 extra_params: %v", extra["lang"])
	}
	if extra["text_normalization"] != false {
		t.Fatalf("text_normalization 未折进 extra_params: %v", extra["text_normalization"])
	}
	if _, still := body["lang"]; still {
		t.Fatal("lang 仍留在顶层")
	}
}

// speed 永远不折:它是 OpenAICreateSpeechRequest 的顶层字段
// (2.5 的 native_speed_control 把它映射成 duration_factor),折进去反而到不了引擎。
func TestSpeedNeverFolded(t *testing.T) {
	keys := append(append([]string{}, indexTTS2EmotionKeys...), indexTTS25ExtraKeys...)
	body := map[string]any{"speed": 1.25}
	foldParamsIntoExtra(body, keys)
	if body["speed"] != 1.25 {
		t.Fatalf("speed 被折走了,引擎将拿不到语速: %v", body["speed"])
	}
}

// caller 已有的 extra_params 保留,同名不覆盖(显式值优先)。
func TestFoldKeepsExistingExtra(t *testing.T) {
	body := map[string]any{
		"extra_params": map[string]any{"lang": "zh", "custom": 1},
		"lang":         "ja",
	}
	foldParamsIntoExtra(body, append(append([]string{}, indexTTS2EmotionKeys...), indexTTS25ExtraKeys...))
	extra := body["extra_params"].(map[string]any)
	if extra["lang"] != "zh" {
		t.Fatalf("已有 extra_params.lang 被顶层值覆盖了: %v", extra["lang"])
	}
	if extra["custom"] != 1 {
		t.Fatal("已有的其他 extra_params 键丢失")
	}
	if _, still := body["lang"]; still {
		t.Fatal("顶层 lang 未删除")
	}
}

// MiniMax-Music3 借用 task_type=tts 抵达引擎的 /v1/tasks/audio/,但它不是语音模型。
// tts 路径上有三处专属逻辑,其中两处必须按引擎族让开 —— 这一组锁住"让开"这件事:
//
//  1. 参考音物化:IndexTTS 那支硬要 metadata.voice(语音克隆的必填项),而 Music3
//     的人声是按歌词自己唱出来的。曾经这里就是照 IndexTTS 判的,现网报
//     「任务类型 tts 需要参考音色」,界面上根本没有这个上传位。
//  2. 字数上限:Music3 配在 MusicModelConfig 里(体验区挂在音乐页),拿
//     AudioModelConfig 去查会查不到、落到全局默认 —— 运营设的上限成了摆设,且不报错。
//
// 判据是**配置声明的引擎族**,不是模型名 substring:IsOmniTTSModel 正是靠名字判的,
// 而 minimax-music3 不含它白名单里的任何一个 token,这才掉进了 IndexTTS 分支。
func TestIsOmniTTSModelDoesNotCoverMusic3(t *testing.T) {
	// 锁住"名字判不出来"这个事实本身 —— 它是上面那条 bug 的成因。
	// 哪天有人把 music3 加进那个白名单,这条会提醒他:那不是正确的修法,
	// Music3 不该被当成 Omni TTS 模型(它没有 speaker 预设、也没有 ref_audio 语义)。
	if IsOmniTTSModel("minimax-music3") {
		t.Fatal("minimax-music3 不该被 IsOmniTTSModel 判为真 —— 它不是 TTS 模型;" +
			"tts 路径上的让开应按引擎族(MusicEngineFamilyForModel)判")
	}
	// 反向:真正的 Omni TTS 家族仍要判得出来,别把白名单改坏了
	for _, m := range []string{"qwen3-tts", "voxcpm2", "cosyvoice3", "glm-tts", "moss-ttsd"} {
		if !IsOmniTTSModel(m) {
			t.Fatalf("%s 应判为 Omni TTS", m)
		}
	}
	// IndexTTS 仍走旧路径(必填 voice)
	if IsOmniTTSModel("indextts-2.5") {
		t.Fatal("indextts 系应走旧的必填参考音路径")
	}
}
