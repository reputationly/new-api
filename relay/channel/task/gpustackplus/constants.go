package gpustackplus

// ChannelName 渠道内部标识。
const ChannelName = "gpustackplus"

// ModelList GPUStackPlus 暴露的模型（自建增强引擎：LightX2V 系 + IndexTTS-2 + ACE-Step
// + vLLM-Omni 语音系）。实际以渠道配置的模型映射为准，此处仅作默认展示 / 模型广场标签。
// task_type 由模型名推断（inferTaskType）：i2v→i2v、infinitetalk→s2v、seedvr2→sr、
// bernini→v2v（视频编辑,rv2v/r2v 由 metadata.task_type 显式指定）、
// indextts/tts/voxcpm/cosyvoice/moss→tts、acestep→t2m（cover/repaint 由
// metadata.task_type 显式指定）。
var ModelList = []string{
	"wan2.2-t2v",
	"wan2.2-i2v",
	"infinitetalk-480p",
	"infinitetalk-720p",
	"seedvr2",
	"bernini",
	"indextts2",
	// 文生音乐（ACE-Step 1.5，生产默认 xl-turbo）
	"acestep-v15-xl-turbo",
	// 语音合成（vLLM-Omni，接管 IndexTTS 后的 TTS 引擎）。预设音色走标量 speaker，
	// 零样本克隆走 ref_audio；MOSS-TTSD 双人对话 ref_audio + ref_audio_2。
	"qwen3-tts",
	"voxcpm2",
	"cosyvoice3",
	"glm-tts",
	"moss-tts-nano",
	"moss-ttsd",
	"moss-voicegenerator",
	"moss-soundeffect",
	// 扩散音频(vLLM-Omni,Phase 2)。AudioX 文生音效/视频生音乐(t2a/v2m/tv2m,后两者由
	// metadata.task_type 指定;v2a/tv2a 已随视频配乐下线);SoulX-Singer 歌声合成(svs)。
	"audiox",
	"soulx-singer",
	// 视频配乐(task_type=v2a 新契约:视频→配好音的视频,LTX-2.3 首发)。
	"ltx2-v2a",
}
