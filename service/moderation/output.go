package moderation

import (
	"context"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	"github.com/QuantumNous/new-api/service/mediastore"
	"github.com/QuantumNous/new-api/types"
)

// 产物审核（挂载点 D-1）。见 docs/content-moderation-design.md §12.4。
//
// 只审图片与视频，不审文本输出——文本是流式的，已经发出去的字收不回，
// 那条路的取舍写在 §12.4.1，本期不做。
//
// 挂在异步任务这一侧是因为它**零延迟代价**：任务完成到用户来取之间天然有时间差，
// 审核塞在这个空档里用户完全无感。同步图片那条路要多等一次视觉推理（实测约 160ms），
// 留到 P2 等这边的数据出来再定。

// OutputModerationResult 产物审核的结论。
type OutputModerationResult struct {
	// Blocked 是否应当把任务判失败。
	Blocked bool
	// Reason 面向用户的失败原因。
	Reason string
}

// ModerateTaskOutput 审一个已完成任务的产物。
//
// 返回值告诉调用方要不要把任务转成失败。审核不可用、拿不到产物、或者压根没开，
// 都返回「不拦」——产物审核是新增能力，它自己的故障不该把本来能交付的任务判失败。
func ModerateTaskOutput(ctx context.Context, task *model.Task, resultURL, upstreamURL string) OutputModerationResult {
	pass := OutputModerationResult{}
	if task == nil {
		return pass
	}
	group := task.Group
	modelName := task.Properties.OriginModelName
	if !OutputMediaActive(group, modelName) {
		return pass
	}

	url := outputCandidateURL(ctx, task, resultURL, upstreamURL)
	if url == "" {
		// 拿不到可送审的地址。三种形态会走到这里：内部代理 URL（BuildProxyURL，
		// 模型访问不了）、空 URL、以及 OBS 未启用时的 obs:// 占位符。
		//
		// 这是**漏审**，所以要出声——静默跳过会让「产物都审过了」和「一张都没审」
		// 在管理端长得一模一样，而后者是这套系统最不能出的错。
		common.SysLog("moderation: 任务 " + task.TaskID + " 的产物无法送审（拿不到可访问地址），本次跳过")
		return pass
	}

	mediaType, ok := outputMediaType(task)
	if !ok {
		// Suno 音乐/歌词等音频产物：本期不审（§2.3），拿视觉模型判只会误判。
		return pass
	}
	item := MediaItem{
		URL:   url,
		Type:  mediaType,
		Field: "result",
	}
	res := ModerateMedia(ctx, &Request{
		UserId:    task.UserId,
		ChannelId: task.ChannelId,
		Group:     group,
		ModelName: modelName,
		TaskId:    task.TaskID,
		// Stage 决定读哪个开关、以及记录落在哪一档统计里。
		Stage: StageOutput,
	}, []MediaItem{item})

	if !res.Blocked {
		return pass
	}
	if res.Action == ActionError {
		// **产物侧永不 fail-close。**
		//
		// ModerateMedia 在 blocking + FailOpen=false 时会把 ActionError 提升成
		// Blocked（media.go），那套语义是给输入侧设计的：那边最坏是拒掉一个可重试
		// 的请求，用户再发一次就是了。产物侧的最坏情况完全不同——任务已经跑完、
		// 算力已经烧掉、钱已经扣了，这里判失败还会清空 ResultURL 和 Data，
		// 于是**判定节点抖一下就等于永久销毁用户已付费的交付物**，而且按当前策略
		// 不退费。拿基础设施的故障去销毁用户的东西，任何严格度都不值这个代价。
		//
		// 这也正是这个函数开头写的契约：「审核不可用、拿不到产物、或者压根没开，
		// 都返回不拦」。只看 res.Blocked 会让那句话变成一句空话。
		common.SysLog("moderation: 任务 " + task.TaskID + " 的产物审核异常（" + res.Reason + "），按不拦放行")
		return pass
	}
	return OutputModerationResult{
		Blocked: true,
		// 措辞刻意不说「你的内容违规」：产物是**我们的模型**生成的，用户的 prompt
		// 可能完全无辜（文生图偶尔会产出意外内容）。把模型的问题说成用户的问题，
		// 既不准确，也会让申诉无从谈起。
		Reason: "生成结果未通过内容安全检查，请调整描述后重试",
	}
}

// outputCandidateURL 在产物地址与上游原始地址之间选出一个能送审的。
//
// ResultURL 拿不到不等于内容拿不到：产物是 data: URI 时（Vertex 的 base64 视频），
// 轮询会把 ResultURL 改写成需要本站鉴权的代理 URL，而真正的内容在上游返回的那个
// data: 里。只看 ResultURL 会让这一类产物**全部**静默漏审。
func outputCandidateURL(ctx context.Context, task *model.Task, resultURL, upstreamURL string) string {
	if url := outputMediaURL(ctx, task, resultURL); url != "" {
		return url
	}
	return outputMediaURL(ctx, task, upstreamURL)
}

// outputMediaURL 把任务产物换成判定模型能访问的地址。
func outputMediaURL(ctx context.Context, task *model.Task, resultURL string) string {
	resultURL = strings.TrimSpace(resultURL)
	switch {
	case resultURL == "":
		return ""
	case isOwnProxyURL(task, resultURL):
		// 我们自己的取件端点。**必须排在 http(s) 分支前面**：BuildProxyURL 返回的是
		// 绝对地址（ServerAddress + /v1/videos/<id>/content），不是相对路径，
		// 所以它会被下面的「上游直链」分支照单全收。
		//
		// 那一步错的代价不是慢，是**静默漏审**：这个路由挂在 TokenOrUserAuth 后面，
		// 判定节点必然拉失败 → ActionError → 按不拦放行，而 upstreamURL 那条回落
		// 永远走不到。data: 产物（Vertex 的 base64 视频）恰恰全部经由这里，
		// 于是「加了 upstreamURL 参数来堵漏」这件事本身被彻底架空，
		// 还白烧掉一次判定调用和一部分每轮预算。
		return ""
	case mediastore.IsOBSRef(resultURL):
		// obs://<key> → 实时签名。签不出来（存储没开/凭证坏了）时返回空，
		// 由调用方按「拿不到就跳过」处理，而不是把 obs:// 原样送给模型。
		signed := mediastore.ResolveResultURL(ctx, resultURL)
		if mediastore.IsOBSRef(signed) {
			return ""
		}
		return signed
	case strings.HasPrefix(resultURL, "http://"), strings.HasPrefix(resultURL, "https://"):
		// 上游直链（Kling / Ali / 豆包等透传渠道）。判定模型自己去拉，
		// SSRF 校验在 moderation 包里做。
		return resultURL
	case strings.HasPrefix(resultURL, "data:"):
		return resultURL
	}
	// 其它形态（相对路径等）：判定节点拿不到内容。
	return ""
}

// isOwnProxyURL 判断这个地址是不是本站给这个任务签发的取件端点。
//
// 直接跟 taskcommon.BuildProxyURL 精确比对，而不是猜 URL 长什么样：
// 这个函数就是生成方，它改了这里自动跟着改。ServerAddress 为空时 BuildProxyURL
// 退化成相对路径，同样能对上。
func isOwnProxyURL(task *model.Task, url string) bool {
	if task == nil || task.TaskID == "" {
		return false
	}
	return url == taskcommon.BuildProxyURL(task.TaskID)
}

// outputMediaType 判断产物是图片、视频，还是不该送审的其它形态。
//
// 按 action 白名单而不是子串匹配：早先这里写的是
// `strings.Contains(action, "video"/"i2v"/"t2v")`，而**这些取值在本仓库里根本不存在**
// —— 视频任务的 action 恒为 constant.TaskActionGenerate 等五个值之一
// （relay/channel/task/gpustackplus/adaptor.go 明确写着「视频恒为 generate」）。
// 后果是每个视频都被判成图片：ModerateVideo 的抽帧永远不跑，原始 .mp4 被直接
// 丢给图片判定路径，缺 ffmpeg 时 dropVideosIfNoFFmpeg 也摘不掉它，
// 记录里的 modality 还会把视频记成 image——正是 §12.1 要求必须分清的那条界线。
//
// 第二个返回值为 false 表示不送审：tasks 表里混装着 Suno 音乐/歌词
// （action = MUSIC / LYRICS），音频本期明确不审（§2.3），不能拿视觉模型去判。
func outputMediaType(task *model.Task) (types.FileType, bool) {
	if constant.IsImageTaskAction(task.Action) {
		return types.FileTypeImage, true
	}
	for _, a := range videoTaskActions {
		if task.Action == a {
			return types.FileTypeVideo, true
		}
	}
	return "", false
}

// videoTaskActions 视频类任务的 action 取值。
//
// 与 model.videoTaskActions 同源（那个是包私有的），改动必须两边同步——
// 漏了这边的后果是新玩法的视频产物被静默跳过审核。
var videoTaskActions = []string{
	constant.TaskActionGenerate,
	constant.TaskActionTextGenerate,
	constant.TaskActionFirstTailGenerate,
	constant.TaskActionReferenceGenerate,
	constant.TaskActionRemix,
}

// ModerateImageOutput 审一张同步生成的图片（挂载点 D-2，§12.4.3）。
//
// 与 D-1 的区别是**这条在用户的请求上**：审核耗时直接加在响应时延里（单张实测约
// 160ms），拦截也不是把任务改成失败，而是整个请求返回错误、一个字节都不写出去。
// 所以调用方必须在写响应**之前**调它。
//
// 同样永不 fail-close：判定服务抖一下不该让一次已经烧掉 GPU 的生图变成报错。
func ModerateImageOutput(ctx context.Context, req *Request, imageURL string) OutputModerationResult {
	pass := OutputModerationResult{}
	if req == nil || strings.TrimSpace(imageURL) == "" {
		return pass
	}
	if !OutputMediaActive(req.Group, req.ModelName) {
		return pass
	}
	req.Stage = StageOutput

	res := ModerateMedia(ctx, req, []MediaItem{{
		URL:   imageURL,
		Type:  types.FileTypeImage,
		Field: "result",
	}})
	if !res.Blocked {
		return pass
	}
	if res.Action == ActionError {
		// 与 ModerateTaskOutput 同口径，理由见那边：拿基础设施的故障去毁用户的交付物
		// 不值。这条路上「毁」的形式是把一次成功的生图变成 4xx。
		common.SysLog("moderation: 同步生图产物审核异常（" + res.Reason + "），按不拦放行")
		return pass
	}
	return OutputModerationResult{
		Blocked: true,
		Reason:  "生成结果未通过内容安全检查，请调整描述后重试",
	}
}
