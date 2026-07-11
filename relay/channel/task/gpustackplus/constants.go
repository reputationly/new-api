package gpustackplus

// ChannelName 渠道内部标识。
const ChannelName = "gpustackplus"

// ModelList GPUStackPlus 暴露的模型（自建增强引擎：LightX2V 系 + IndexTTS-2）。
// 实际以渠道配置的模型映射为准，此处仅作默认展示 / 模型广场标签。task_type 由模型名
// 推断（inferTaskType）：i2v→i2v、infinitetalk→s2v、seedvr2→sr、vace→vace、indextts→tts。
var ModelList = []string{
	"wan2.2-t2v",
	"wan2.2-i2v",
	"infinitetalk-480p",
	"infinitetalk-720p",
	"seedvr2",
	"wan2.2-vace",
	"indextts2",
}
