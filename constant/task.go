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

// VideoTaskActions 是视频类任务的 action 取值。tasks 表混装了视频、图片、Suno 等多种
// 任务，platform 存的是渠道类型编号、区分不出玩法，所以按 action 筛。
var VideoTaskActions = []string{
	TaskActionGenerate,
	TaskActionTextGenerate,
	TaskActionFirstTailGenerate,
	TaskActionReferenceGenerate,
	TaskActionRemix,
}

// IsVideoTaskAction 判断一个 task action 是否属于视频链路。
//
// ⚠️ 它的分辨力**到 action 为止**：gpustackplus 把所有非图片任务的 action 一律写成
// generate（taskActionOf），所以 TTS、音乐、配音、超分这些同走任务子系统的玩法在这里
// 与视频不可区分，一律判 true。能挡掉的只有 action 拼写不同的那几类 —— 图片
// （imageGenerate / imageEdit）与 Suno（MUSIC / LYRICS）。
//
// 拿它当端点闸门可以，但别把它当成「这一定是个视频」的断言：service/moderation 的
// outputMediaType 正是靠它选审核形态，音频产物因此会走视觉判定（既有缺口，见那边注释）。
func IsVideoTaskAction(action string) bool {
	for _, a := range VideoTaskActions {
		if a == action {
			return true
		}
	}
	return false
}

var SunoModel2Action = map[string]string{
	"suno_music":  SunoActionMusic,
	"suno_lyrics": SunoActionLyrics,
}
