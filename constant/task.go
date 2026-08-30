package constant

type TaskPlatform string

const (
	TaskPlatformSuno       TaskPlatform = "suno"
	TaskPlatformMidjourney              = "mj"
)

const (
	SunoActionMusic  = "MUSIC"
	SunoActionLyrics = "LYRICS"

	TaskActionGenerate          = "generate"
	TaskActionTextGenerate      = "textGenerate"
	TaskActionFirstTailGenerate = "firstTailGenerate"
	TaskActionReferenceGenerate = "referenceGenerate"
	TaskActionRemix             = "remixGenerate"

	// 异步图片任务(见 docs/image-async-task-design.md)。与视频 action 分开是因为
	// 查询端点要按它决定回 OpenAI video 对象还是 image job 对象,而 task 表里两者
	// 共用同一个 platform(渠道类型数字)。
	TaskActionImageGenerate = "imageGenerate"
	TaskActionImageEdit     = "imageEdit"
)

// IsImageTaskAction 判断一个 task action 是否属于异步图片链路。
func IsImageTaskAction(action string) bool {
	return action == TaskActionImageGenerate || action == TaskActionImageEdit
}

var SunoModel2Action = map[string]string{
	"suno_music":  SunoActionMusic,
	"suno_lyrics": SunoActionLyrics,
}
