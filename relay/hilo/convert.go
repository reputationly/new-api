// Package hilo 把 MiniMax Design（内部代号 hilo）客户端发来的官方形状请求，
// 转成本仓的统一任务契约。
//
// # 形态
//
//	客户端 → 本地 gateway（官方那个 NestJS 进程，原样跑）→ 我们
//	         POST /api/v1/video/minimax-v3/generate
//
// 路径和字段都不是我们定的，是官方 gateway 写死往外发的 —— 下面每条都是
// 抓包实测出来的，不是照文档抄的。
//
// # 为什么转成统一契约而不是自己调流水线
//
// 转完之后改写 URL 交给既有链路（TokenAuth → Distribute → RelayTask），
// **聚合展开、分段计费、日志、限流全部白拿**。自己调的话这些每一样都要
// 重写一份，而计费那份尤其危险：主链路的 PostTextConsumeQuota 还有分层
// 计费、视频计费矩阵等分支，抄一份出来将来主逻辑加了新维度这里不会跟上，
// 而且不报错 —— 只是账悄悄算少了。
//
// 这条路子有先例：`middleware/minimax_v2_adapter.go` 的 MiniMaxV2CreateConvert
// 就是「官方 body → 统一契约 → 复用 RelayTask」。
package hilo

import (
	"fmt"
	"strings"
)

// VideoRequest 官方 gateway 发来的视频生成请求。
//
// 实测抓到的形态（两种玩法在**同一个接口**上，靠字段区分）：
//
//	first-last-frame:
//	  { "model":"MiniMax-H3", "prompt":"…", "generate_audio":true,
//	    "duration":5, "resolution":"768P",
//	    "ratio":"adaptive",                    ← gateway 自己改成 adaptive
//	    "first_frame_image":"https://…" }      ← 单数、字符串
//
//	reference:
//	  { …, "ratio":"16:9",                     ← 原样保留
//	    "reference_images":["https://…"] }     ← 复数、数组
//
// 素材是 URL：带图的生成会先打 `POST /api/v1/files/upload` 换成 URL，
// 再发这个请求。所以这里不用处理 base64。
type VideoRequest struct {
	Model         string   `json:"model"`
	Prompt        string   `json:"prompt"`
	GenerateAudio *bool    `json:"generate_audio"`
	Ratio         string   `json:"ratio"`
	Duration      int      `json:"duration"`
	Resolution    string   `json:"resolution"`
	FirstFrame    string   `json:"first_frame_image"`
	LastFrame     string   `json:"last_frame_image"`
	RefImages     []string `json:"reference_images"`
}

// TaskType 平台的任务类型，下发在 `metadata.task_type`。
//
// **必须显式给。** 适配器的 taskTypeOfRequest 第一优先级就读它；不给就退回
// 「按输入形态推断」，而形态推不出语义 —— l2va（只给尾帧）和 i2v 的输入
// 完全相同（都是 1 张图），区别纯在"这张是首帧还是尾帧"。
// 适配器的注释把 l2va **故意排除**在形态推断之外，正是为此。
//
// 不给的后果：仅尾帧被当成首帧，视频从结尾往后长；给两张帧则落进二义
// 分支直接 400。
type TaskType string

const (
	TaskT2V   TaskType = "t2v"   // 纯文生
	TaskI2V   TaskType = "i2v"   // 只给首帧
	TaskL2VA  TaskType = "l2va"  // 只给尾帧
	TaskFLF2V TaskType = "flf2v" // 首尾帧都给
	TaskR2VA  TaskType = "r2va"  // 参考生视频
)

// ImageMode 这次请求是哪种玩法。
//
// **客户端不直接发 image_mode，得从素材字段反推** —— 它把玩法编码在
// 「用哪个字段装图」上（见 VideoRequest 的注释）。
type ImageMode string

const (
	ModeFirstLastFrame ImageMode = "first-last-frame"
	ModeReference      ImageMode = "reference"
	ModeTextToVideo    ImageMode = "text-to-video"
)

// DetectMode 判断玩法。
//
// 判据是**哪个字段有值**，不是有没有图：两种玩法都可能只给一张图，
// 区别在于它装在 `first_frame_image` 还是 `reference_images` 里。
//
// 首尾帧优先：同时给了两种时，首尾帧的语义更强（它直接决定画幅），
// 而参考图只是风格参考。真实请求里不会同时出现，这里只是不让它变成
// 一个"看起来随机"的选择。
func (r *VideoRequest) DetectMode() ImageMode {
	return ResolveFrameRoles(r).Mode
}

// TaskTypeOf 这次请求的平台任务类型。
//
// 帧族要再按**填了哪个槽**细分：首帧 / 尾帧 / 两者都有，是三种不同的
// task_type，而它们的输入形态有两种是一样的。
func (r *VideoRequest) TaskTypeOf() TaskType {
	return ResolveFrameRoles(r).TaskType
}

// Images 这次请求要带的素材，按玩法取对应的字段。
//
// 首尾帧的顺序**必须是首帧在前**：下游按位置认，颠倒了会让视频倒着长 ——
// 而且不报错。只给尾帧时不能补一个空串占位（那会被当成一张读不出的图），
// 而是要靠 metadata 说明这是"仅尾帧"。
func (r *VideoRequest) Images() []string {
	roles := ResolveFrameRoles(r)
	if roles.Mode == ModeReference {
		return roles.Refs
	}
	return roles.FrameImages()
}

// ToTaskSubmit 转成统一任务契约的 body。
//
// `model` 传的是**平台模型名**（多半是一个聚合模型名，比如 minimax-h3-2k），
// 由调用方按玩法查目录得出 —— 见 controller 那边的 dispatch。
//
// # size 而不是 resolution
//
// 统一契约里档位词的字段名是 `size`，H3 也从 `body["size"]` 取
// （h3ApplyCanvas → h3ShortEdgeFromSizeToken）。`resolution` 是官方形状
// 那条路上的名字，转过来就不该再出现 —— 这个坑在聚合 overrides 上踩过。
//
// # 比例不透传给 size
//
// `ratio` 是具名比例（16:9），不是尺寸。塞进 size 的话 H3 的 adaptor 会用
// AspectRatioFromSize 反推并**覆盖掉**用户选的比例；档位词匹配不到 WxH
// 正则才不会覆盖。所以比例走 metadata。
func (r *VideoRequest) ToTaskSubmit(platformModel string) (map[string]any, error) {
	prompt := strings.TrimSpace(r.Prompt)
	if prompt == "" {
		// 平台侧 ValidateBasicTaskRequest 会拒空 prompt，与其让它在更远的地方
		// 失败，不如就地说清楚。
		return nil, fmt.Errorf("prompt is required")
	}
	if strings.TrimSpace(platformModel) == "" {
		return nil, fmt.Errorf("platform model is empty")
	}

	body := map[string]any{
		"model":  platformModel,
		"prompt": prompt,
	}
	// ⚠️ **帧约束和多模态参考的落点不同，不能统一成一个。**
	//
	// 帧约束（首帧/尾帧）走**顶层 images[]**，顺序即语义（[0]=首帧、[1]=尾帧）；
	// 参考素材走 `metadata.src_ref_images`，和 doubao/Ark 渠道共用字段名。
	//
	// 把参考图放进顶层 images[] 的后果：下游按**张数**推断 role（1 张 =
	// first_frame），单张参考图会被误判成首帧约束 —— 而 reference 恰好是
	// 目录里的默认玩法。这条在 relay/minimaxv2/convert.go 里是加粗警告过的。
	meta := map[string]any{}
	imgs := r.Images()
	if len(imgs) > 0 {
		if r.DetectMode() == ModeReference {
			meta["src_ref_images"] = imgs
		} else {
			body["images"] = imgs
		}
	}
	if res := strings.TrimSpace(r.Resolution); res != "" {
		body["size"] = res
	}
	if r.Duration > 0 {
		body["duration"] = r.Duration
	}

	// **显式下发 task_type。** 见 TaskType 的注释：不给就退回形态推断，
	// 而形态推不出"这张是尾帧"。
	meta["task_type"] = string(r.TaskTypeOf())
	// `adaptive` 是"跟随输入素材"，不是一个比例值 —— 原样传下去会被当成
	// 未知比例。不传即为自适应。
	if ratio := strings.TrimSpace(r.Ratio); ratio != "" && ratio != "adaptive" {
		meta["aspect_ratio"] = ratio
	}
	// **有声视频默认开。** 官方目录里 generate_audio 的 default 就是 "true",
	// 我们之前完全没有这个参数，于是 H3 的原生音轨一直没生成 ——
	// 表现是"视频没有声音"。漏传按 true，和官方一致。
	meta["generate_audio"] = r.GenerateAudio == nil || *r.GenerateAudio
	body["metadata"] = meta
	return body, nil
}
