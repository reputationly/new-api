package constant

import (
	"sort"
	"strings"
)

// 门面 task_type → 体验区 tab key。
//
// 体验区的模型参数(时长白名单、上传上限、字数上限……)已按 tab 分开配置,存在
// <XxxModelConfig>.models[<模型名>].tabs[<tab key>] 下。服务端护栏要读到"与用户当前
// 玩法同一格"的值,就必须先把请求的 task_type 还原成 tab —— 否则同一模型挂了多个玩法
// 时,给「文生视频」配的时长白名单会连带把「图生视频」的直连请求一起拒掉(这正是改造前
// 的实际行为)。
//
// tab key 与前端 web/classic/src/constants/playgroundAdmin.constants.js 的
// PLAYGROUND_CATEGORIES 一一对应,新增玩法时两处同步。
//
// 解析不出 tab(见下面的"多对一"与"无 tab")时返回空串,调用方退回模型级配置 ——
// 也就是改造前的语义,不会比原来更严。
var taskTypeToPlaygroundTab = map[string]string{
	// 图像(ImageModelSizeConfig)
	"t2i": "text2image",
	"i2i": "image2image",

	// 视频(VideoModelConfig)
	"t2v": "text2video",
	"r2v": "image2video", // Bernini 参考图生视频 = 体验区「图生视频」
	// 关键帧 tab 同时覆盖两种模型:flf2v 模型发 flf2v(首帧+尾帧),
	// 只吃首帧的 i2v 模型发 i2v —— 都属同一个玩法,配置也应共用一格。
	// l2va(只给尾帧,反推开头)是 MiniMax H3 带来的第三态:H3 的一个 FL2VA
	// checkpoint 同时吃首帧/尾帧/首尾帧,靠 extra_params.frame_indices 区分
	// ([0] / [-1] / [0,-1]),不像 wan 那样要靠两个启动参数不同的实例。
	// 它与 i2v 的输入形态完全一样(都是 1 张图),只有语义不同,所以必须是独立的
	// task_type —— 靠张数推不出"这张是尾帧"。
	"i2v":   "flf2v",
	"l2va":  "flf2v",
	"flf2v": "flf2v",
	"s2v":   "s2v",
	// 参考生视频(MiniMax H3 Ref2VA):参考图(+可选音色参考)→ 带语音的视频。
	// 独立于 s2v(InfiniteTalk 音频驱动)与 r2v(Bernini 纯参考图,归「图生视频」)。
	"r2va": "r2va",
	// Bernini 一个模型出四种编辑玩法,体验区合并为「视频编辑」一个 tab。
	"v2v":   "vace",
	"rv2v":  "vace",
	"mv2v":  "vace",
	"ads2v": "vace",
	// 视频配音:入口挂在语音页,模型配在 VideoModelConfig(见前端 tab 的 storeIn)。
	"v2a": "dub",

	// 音乐(MusicModelConfig)
	"t2m":     "t2m",
	"cover":   "cover",
	"repaint": "repaint",

	// 无对应 tab,一律走模型级:
	//   sr      —— 超分不单独开玩法,只作 1080P 两段流水线的内部一段;
	//   t2a/svs/v2m/tv2m —— AudioX 与 SoulX-Singer 已于 2026-08 下线,音乐页无入口;
	//   tts     —— 情感合成/语音合成/双人对话/声音设计四个 tab 共用同一个 task_type,
	//              从请求上无从区分是哪个玩法。这四格的配置在保存时会同时回写一份
	//              "最宽松"的模型级值(见体验区管理页的保存逻辑),故此处退回模型级
	//              既不会误拒,也不会比改造前更松。
}

// PlaygroundTabForTaskType 把门面 task_type 映射成体验区 tab key;无法确定返回空串。
func PlaygroundTabForTaskType(taskType string) string {
	return taskTypeToPlaygroundTab[strings.ToLower(strings.TrimSpace(taskType))]
}

// playgroundTabTaskTypes 是上表的反向索引:tab key → 该 tab 覆盖的 task_type 集合。
// 由 init 从正向表构建而非手写第二份,两者永不漂移。
//
// 反向是「一对多」的:同一个 tab 可能覆盖多个 task_type ——
//
//	flf2v(关键帧)→ {flf2v, i2v, l2va}:三类玩法共用一个玩法格;
//	vace(视频编辑)→ {v2v, rv2v, mv2v, ads2v}:Bernini 一个模型出四种编辑。
//
// 所以它给出的是「候选集」,不是答案;还要靠请求的输入形态或显式 task_type 收敛到一个。
//
// ⚠️ 关键帧这一格的候选集**无法只靠输入形态收敛**:i2v 与 l2va 都是 1 张图,
// 区别纯在语义(首帧 / 尾帧)。这两者之间必须由显式 task_type 定夺 ——
// 见 taskTypesCompatibleWithInputs 与前端关键帧三态的派生逻辑。
var playgroundTabTaskTypes = map[string][]string{}

func init() {
	for taskType, tab := range taskTypeToPlaygroundTab {
		playgroundTabTaskTypes[tab] = append(playgroundTabTaskTypes[tab], taskType)
	}
	// map 遍历序随机,排一下让候选集与报错文案稳定可测。
	for tab := range playgroundTabTaskTypes {
		sort.Strings(playgroundTabTaskTypes[tab])
	}
}

// PlaygroundTaskTypesForTab 返回该 tab 覆盖的 task_type 候选集(已排序)。
// 返回的是内部切片的副本,调用方可自由修改。
func PlaygroundTaskTypesForTab(tab string) []string {
	src := playgroundTabTaskTypes[strings.ToLower(strings.TrimSpace(tab))]
	if len(src) == 0 {
		return nil
	}
	return append([]string(nil), src...)
}
