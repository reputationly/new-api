package controller

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/common"
)

func withMusicEngineConfig(t *testing.T) {
	t.Helper()
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	prev, had := common.OptionMap["MusicModelConfig"]
	common.OptionMap["MusicModelConfig"] = `{"models":{"minimax-music3":{"engine":"minimax-music3"}}}`
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		if had {
			common.OptionMap["MusicModelConfig"] = prev
		} else {
			delete(common.OptionMap, "MusicModelConfig")
		}
		common.OptionMapRWMutex.Unlock()
	})
}

func withAggregateForMusic(t *testing.T) {
	t.Helper()
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	prev, had := common.OptionMap["AggregateModelConfig"]
	common.OptionMap["AggregateModelConfig"] = `[{"name":"minimax-music3-enhanced","type":"music","enabled":true,
	  "generate":{"model":"minimax-music3"}}]`
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		if had {
			common.OptionMap["AggregateModelConfig"] = prev
		} else {
			delete(common.OptionMap, "AggregateModelConfig")
		}
		common.OptionMapRWMutex.Unlock()
	})
}

// Music3 的字段语义与 ACE-Step 相反：顶层 prompt 是歌词、风格描述走 metadata.instructions。
// 映射反了会静默产出"把风格描述唱出来"的歌。
func TestBuildMusicTaskBodyMusic3SwapsPromptAndLyrics(t *testing.T) {
	withMusicEngineConfig(t)
	body, msg := buildMusicTaskBody("minimax-music3", "轻快的 city pop", "夜色落在窗台\n我把灯留着", nil, nil)
	if msg != "" {
		t.Fatalf("unexpected error: %s", msg)
	}
	if body["prompt"] != "夜色落在窗台\n我把灯留着" {
		t.Fatalf("Music3 顶层 prompt 应为歌词，实际 %v", body["prompt"])
	}
	meta, _ := body["metadata"].(map[string]any)
	if meta["instructions"] != "轻快的 city pop" {
		t.Fatalf("风格描述应进 metadata.instructions，实际 %+v", meta)
	}
	if _, has := meta["lyrics"]; has {
		t.Fatalf("Music3 不该发 metadata.lyrics（引擎不认，到了就丢）: %+v", meta)
	}
}

// 聚合名要先解析到生成段再判模型族，否则 enhanced 版会走 ACE-Step 的映射。
func TestBuildMusicTaskBodyMusic3AggregateResolvesFamily(t *testing.T) {
	withMusicEngineConfig(t)
	withAggregateForMusic(t)
	body, msg := buildMusicTaskBody("minimax-music3-enhanced", "温柔的女声", "第一句歌词", nil, nil)
	if msg != "" {
		t.Fatalf("unexpected error: %s", msg)
	}
	if body["prompt"] != "第一句歌词" {
		t.Fatalf("聚合 Music3 也应把歌词放顶层 prompt，实际 %v", body["prompt"])
	}
}

// ACE-Step：顶层 prompt 是描述、歌词走 metadata.lyrics（与官方字段一一对应）。
func TestBuildMusicTaskBodyAceStepKeepsPrompt(t *testing.T) {
	withMusicEngineConfig(t)
	body, msg := buildMusicTaskBody("ace-step", "dreamy shoegaze, slow tempo", "hold me now", nil, nil)
	if msg != "" {
		t.Fatalf("unexpected error: %s", msg)
	}
	if body["prompt"] != "dreamy shoegaze, slow tempo" {
		t.Fatalf("ACE-Step 顶层 prompt 应为描述，实际 %v", body["prompt"])
	}
	meta, _ := body["metadata"].(map[string]any)
	if meta["lyrics"] != "hold me now" {
		t.Fatalf("ACE-Step 歌词应进 metadata.lyrics，实际 %+v", meta)
	}
}

// Music3 无词又非要唱：明确拒绝，别把风格描述当歌词送进引擎。
// Music3 无歌词一律拒:纯器乐在该引擎上不支持(只给段落标记 9.8–22.7 秒,
// 给足段落 121–144 秒但有人声),is_instrumental=true 也不是出路。
func TestBuildMusicTaskBodyMusic3RejectsWithoutLyrics(t *testing.T) {
	withMusicEngineConfig(t)
	if _, msg := buildMusicTaskBody("minimax-music3", "chill lo-fi", "", nil, nil); !strings.Contains(msg, "纯器乐") {
		t.Fatalf("应说明纯器乐不支持，实际 %q", msg)
	}
	instrumental := true
	if _, msg := buildMusicTaskBody("minimax-music3", "chill lo-fi", "", &instrumental, nil); !strings.Contains(msg, "纯器乐") {
		t.Fatalf("器乐请求同样要拒，实际 %q", msg)
	}
}

func TestBuildMusicTaskBodyRequiresPrompt(t *testing.T) {
	if _, msg := buildMusicTaskBody("ace-step", "  ", "lyrics", nil, nil); !strings.Contains(msg, "prompt is required") {
		t.Fatalf("空描述应被拒，实际 %q", msg)
	}
}

// 接缝的另一半:展开阶段把增强后的描述写进 prompt 之后,兼容层必须把它当作描述使用。
// 这条断言的是**即将发出去的 taskBody**,不是记录 —— 之前就是只查记录才漏掉了丢数据。
func TestBuildMusicTaskBodyCarriesEnhancedCaption(t *testing.T) {
	taskBody, msg := buildMusicTaskBody("minimax-music3", "ENHANCED CAPTION", "[Verse] lyrics", nil, nil)
	require.Empty(t, msg)
	meta, ok := taskBody["metadata"].(map[string]any)
	require.True(t, ok, "必须带 metadata")
	require.Equal(t, "ENHANCED CAPTION", meta["instructions"], "增强后的描述要进 instructions")
	require.Equal(t, "[Verse] lyrics", taskBody["prompt"], "prompt 位是歌词")
}

// 跨层接缝:展开阶段写回后的官方形状 body,必须能被兼容层正确翻译成任务契约。
// 两边各自测各自证明不了"中间那份 body 长什么样"——以前就是只查记录才漏掉丢数据,
// 这条直接把**即将发出去的 taskBody** 钉死。
func TestOfficialRoutePostExpansionBodiesFlowThrough(t *testing.T) {
	withMusicEngineConfig(t)

	// Music3:描述在 prompt、歌词在 lyrics(纯器乐会被拒,所以这里带歌词)。
	var music3Req minimaxMusicRequest
	require.NoError(t, common.Unmarshal([]byte(
		`{"model":"minimax-music3","prompt":"NEW CAPTION","lyrics":"[Verse] rain","is_instrumental":false}`), &music3Req))
	prompt := strings.TrimSpace(music3Req.Prompt)
	require.NotEmpty(t, prompt, "prompt is required 之前的那道门")
	taskBody, msg := buildMusicTaskBody(music3Req.Model, prompt, music3Req.Lyrics,
		music3Req.IsInstrumental, music3Req.Metadata)
	require.Empty(t, msg)
	meta, ok := taskBody["metadata"].(map[string]any)
	require.True(t, ok, "必须带 metadata")
	require.Equal(t, "NEW CAPTION", meta["instructions"], "增强后的描述要进 instructions")
	require.Equal(t, "[Verse] rain", taskBody["prompt"], "prompt 位是歌词")

	// ACE-Step:prompt 是增强后的描述,歌词在顶层 lyrics → metadata.lyrics。
	var aceReq minimaxMusicRequest
	require.NoError(t, common.Unmarshal([]byte(
		`{"model":"ace-step","prompt":"NEW CAPTION","lyrics":"[Verse]\nrain on neon"}`), &aceReq))
	prompt = strings.TrimSpace(aceReq.Prompt)
	require.NotEmpty(t, prompt)
	taskBody, msg = buildMusicTaskBody(aceReq.Model, prompt, aceReq.Lyrics,
		aceReq.IsInstrumental, aceReq.Metadata)
	require.Empty(t, msg)
	require.Equal(t, "NEW CAPTION", taskBody["prompt"], "ACE-Step 的 prompt 位就是描述")
	meta, ok = taskBody["metadata"].(map[string]any)
	require.True(t, ok, "必须带 metadata")
	require.Equal(t, "[Verse]\nrain on neon", meta["lyrics"], "歌词要进 metadata.lyrics")
}

// 客户端 metadata 透传:扩展位要原样带到任务契约里(task_type / sample_query / reference_audio 等)。
// 与 prompt/lyrics 映射冲突的三个键必须被映射结果覆盖,不能出现两套说法。
func TestBuildMusicTaskBodyPassesThroughClientMetadata(t *testing.T) {
	extra := map[string]any{
		"task_type":       "cover",
		"sample_query":    "dusty piano",
		"reference_audio": "https://example.com/x.wav",
		"instructions":    "client instructions",
		"lyrics":          "client lyrics",
		"is_instrumental": true,
	}
	taskBody, msg := buildMusicTaskBody("ace-step", "warm lofi loop", "", nil, extra)
	require.Empty(t, msg)
	meta, _ := taskBody["metadata"].(map[string]any)
	require.Equal(t, "cover", meta["task_type"])
	require.Equal(t, "dusty piano", meta["sample_query"])
	require.Equal(t, "https://example.com/x.wav", meta["reference_audio"])
	// 非 Music3:prompt 就是描述,metadata 里不该再出现 instructions
	require.NotContains(t, meta, "instructions", "客户端传的 instructions 应被忽略")
	require.NotContains(t, meta, "lyrics", "没有歌词时不该写 lyrics")
	require.Equal(t, "warm lofi loop", taskBody["prompt"])
}

// 纯器乐在 Music3 上不支持:只给段落标记 9.8–22.7 秒,给足段落 121–144 秒但有人声。
// 明确 400,而不是给客户一条 10 秒的曲子。
func TestMusic3RejectsInstrumental(t *testing.T) {
	instrumental := true
	_, msg := buildMusicTaskBody("minimax-music3", "a calm piano solo", "", &instrumental, nil)
	require.Contains(t, msg, "纯器乐")
}

func TestMusic3RejectsMissingLyrics(t *testing.T) {
	_, msg := buildMusicTaskBody("minimax-music3", "a calm piano solo", "", nil, nil)
	require.Contains(t, msg, "纯器乐")
}

// 有歌词的请求完全不受影响。
func TestMusic3WithLyricsUnaffected(t *testing.T) {
	body, msg := buildMusicTaskBody("minimax-music3", "a warm pop song", "[Verse] hello", nil, nil)
	require.Empty(t, msg)
	require.Equal(t, "[Verse] hello", body["prompt"])
}
