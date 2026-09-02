package gpustackplus

import (
	"io"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// MiniMax-Music3 的对外 task_type 改写回归测试。
//
// 为什么必须由测试守住:改写漏了不会在网关侧报错,请求照发,是**引擎**回 405
// (t2m → kind "music" → POST /v1/tasks/music/,vLLM-Omni 没有这条路由)。
// 调用方看到的是一个来自上游、跟自己请求对不上的错误码,排查成本极高。
// 反向漏改(把别的音乐模型也改写成 tts)同样静默:ACE-Step 会被送去 /v1/tasks/audio/,
// 那是语音路由,出参扩展名和调用契约全错。

// setMusicConfig 只填 MusicModelConfig,其余三份清空 —— 引擎族判据是配置声明,
// 不是模型名 substring(见 common.MusicEngineFamilyForModel)。
//
// **用例结束要还原**:OptionMap 是进程级全局,而这里除了写 MusicModelConfig 还会把另外
// 三份清空。不还原的话,清空会顺着 go test 的执行顺序漏给同包后跑的用例 —— 那种失败与
// 被测逻辑毫无关系,且换个文件名(影响执行顺序)就会飘,是最难查的一类。
//
// 还原时把"原本不存在的键"写成空串:对所有读取方来说,键不存在与值为空串都取到 "",
// 两者等价,不值得为此再记一份存在性。
func setMusicConfig(t *testing.T, raw string) {
	t.Helper()
	keys := []string{
		"MusicModelConfig",
		"VideoModelConfig",
		"ImageModelSizeConfig",
		"AudioModelConfig",
	}
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	saved := make(map[string]string, len(keys))
	for _, k := range keys {
		saved[k] = common.OptionMap[k]
	}
	common.OptionMap["MusicModelConfig"] = raw
	common.OptionMap["VideoModelConfig"] = ""
	common.OptionMap["ImageModelSizeConfig"] = ""
	common.OptionMap["AudioModelConfig"] = ""
	common.OptionMapRWMutex.Unlock()

	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		for k, v := range saved {
			common.OptionMap[k] = v
		}
		common.OptionMapRWMutex.Unlock()
	})
}

const music3Config = `{"models":{"minimax-music3":{"engine":"minimax-music3","tabs":{"t2m":{}}},` +
	`"ace-step":{"tabs":{"t2m":{},"cover":{},"repaint":{}}}}}`

// buildTaskType 跑一遍 BuildRequestBody,取回下发给门面的 task_type。
func buildTaskType(t *testing.T, model, taskType string) string {
	t.Helper()
	c := newTestGinContext()
	c.Set("task_request", relaycommon.TaskSubmitReq{
		Model:    model,
		Prompt:   "晨光洒在窗前\n新的一天已经到来",
		Metadata: map[string]any{"task_type": taskType, "instructions": "pop, upbeat, 120bpm"},
	})
	info := &relaycommon.RelayInfo{
		UserId:        1,
		TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: "task_test"},
		ChannelMeta:   &relaycommon.ChannelMeta{UpstreamModelName: model},
	}
	info.OriginModelName = model

	a := &TaskAdaptor{}
	reader, err := a.BuildRequestBody(c, info)
	if err != nil {
		t.Fatalf("BuildRequestBody(%s, %s) 失败: %v", model, taskType, err)
	}
	raw, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("读请求体失败: %v", err)
	}
	var body map[string]any
	if err := common.Unmarshal(raw, &body); err != nil {
		t.Fatalf("解析请求体失败: %v", err)
	}
	got, _ := body["task_type"].(string)
	return got
}

// Music3 发 t2m,下发给引擎的必须是 tts。
func TestMusic3RewritesT2MToTTS(t *testing.T) {
	setMusicConfig(t, music3Config)
	if got := buildTaskType(t, "minimax-music3", "t2m"); got != "tts" {
		t.Fatalf("minimax-music3 的 t2m 未改写成 tts,实际下发 %q —— 引擎会回 405", got)
	}
}

// 已经发 tts 的调用方(体验区现状)不受影响,改写是幂等的。
func TestMusic3TTSStaysTTS(t *testing.T) {
	setMusicConfig(t, music3Config)
	if got := buildTaskType(t, "minimax-music3", "tts"); got != "tts" {
		t.Fatalf("minimax-music3 的 tts 被改坏成 %q", got)
	}
}

// **反向约束**:同在音乐页的 ACE-Step 没有声明 music3 引擎族,t2m 必须原样保留 ——
// 它的引擎就在 /v1/tasks/music/ 上,改写会把它送去语音路由。
func TestAceStepT2MNotRewritten(t *testing.T) {
	setMusicConfig(t, music3Config)
	if got := buildTaskType(t, "ace-step", "t2m"); got != "t2m" {
		t.Fatalf("ace-step 的 t2m 被误改写成 %q", got)
	}
}
