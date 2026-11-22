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
func TestBuildMusicTaskBodyMusic3RequiresLyricsUnlessInstrumental(t *testing.T) {
	withMusicEngineConfig(t)
	if _, msg := buildMusicTaskBody("minimax-music3", "chill lo-fi", "", nil, nil); !strings.Contains(msg, "needs lyrics") {
		t.Fatalf("应提示缺歌词，实际 %q", msg)
	}
	instrumental := true
	body, msg := buildMusicTaskBody("minimax-music3", "chill lo-fi", "", &instrumental, nil)
	if msg != "" {
		t.Fatalf("纯器乐不该要求歌词: %s", msg)
	}
	meta, _ := body["metadata"].(map[string]any)
	if meta["is_instrumental"] != true {
		t.Fatalf("is_instrumental 应透传: %+v", meta)
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
