package ovcaps

// 能力的"玩法"（modes）与可交付时长阶梯。
//
// 这一层只做**转述**：目录里 `params.image_mode.options` 说明模型有哪些玩法，参考上限
// 与 task_type 由引擎族的既有契约决定（H3 的 9/3/3 与 2/1 见
// relay/channel/task/gpustackplus 的 r2va 上限与官方 inputContract）。不在这里新造
// 规则 —— 新造就会与网关的真实校验漂移，而漂移的症状是"目录说能传 9 张，提交才 400"。

import (
	"math"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting"
)

// H3 各玩法的参考上限（与引擎/门面成对约定，改一处必须改两边）：
//   - 首尾帧：1–2 张图，不吃视频/音频（frame_indices 只有 [0] / [0,-1]）
//   - 参考族：≤9 图 + ≤3 视频 + ≤3 音频（门面 _TASK_INPUT_CAPS["r2va"]）
//   - 续写：恰好 1 段视频，不吃图/音频（官方 video-extension 契约）
const (
	h3ModeMaxFrameImages = 2
	h3ModeMaxRefImages   = 9
	h3ModeMaxRefVideos   = 3
	h3ModeMaxRefAudios   = 3
)

// modesOf 把目录里的玩法选项翻成能力目录的 modes。
//
// 只在视频任务类传输、且目录显式声明了 `image_mode` 时输出：
//   - 没声明 image_mode 的模型（wan/seedance 等）保持"单一玩法"，返回 nil，
//     客户端按顶层 limits 走 —— 与改造前逐位一致；
//   - 图片/音乐能力没有玩法概念，也返回 nil。
func modesOf(entry setting.HiloCatalogEntry, probe, transport string) []Mode {
	if transport != setting.TransportVideosTask {
		return nil
	}
	modeParam, ok := entry.Model.Params["image_mode"]
	if !ok || len(modeParam.Options) == 0 {
		return nil
	}
	h3 := common.IsH3VideoModel(probe, entry.Model.ModelName)
	modes := make([]Mode, 0, len(modeParam.Options))
	for _, raw := range modeParam.Options {
		id := strings.ToLower(strings.TrimSpace(raw))
		mode, ok := modeDescriptor(id, h3)
		if !ok {
			// 目录里出现了我们不认识的玩法：**跳过而不是编一个** —— 报一个我们其实
			// 不支持提交的 mode，比不报更糟（客户端会照着填，然后 400）。
			continue
		}
		modes = append(modes, mode)
	}
	if len(modes) == 0 {
		return nil
	}
	return modes
}

// modeDescriptor 单个玩法的契约。h3 决定用 H3 的真实上限还是保守通用值。
func modeDescriptor(id string, h3 bool) (Mode, bool) {
	frameMax := h3ModeMaxFrameImages
	if !h3 {
		frameMax = 1 // 非 H3 的"首尾帧"没有成对约定，保守给 1（等价 i2v）
	}
	switch id {
	case "text-to-video":
		return Mode{
			ID:       id,
			Label:    Localized{Zh: "文生视频", En: "Text to video"},
			TaskType: "t2v",
			References: ModeReferences{
				Image: MediaRefRule{Min: 0, Max: 0},
				Video: MediaRefRule{Min: 0, Max: 0},
				Audio: MediaRefRule{Min: 0, Max: 0},
			},
		}, true
	case "first-last-frame":
		return Mode{
			ID:                    id,
			Label:                 Localized{Zh: "首尾帧", En: "First and last frame"},
			TaskType:              "flf2v",
			TaskTypeIfSingleImage: "i2v",
			References: ModeReferences{
				Image: MediaRefRule{Min: 1, Max: frameMax},
				Video: MediaRefRule{Min: 0, Max: 0},
				Audio: MediaRefRule{Min: 0, Max: 0},
			},
			Note: Localized{
				Zh: "两张图 = 首帧+尾帧（task_type=flf2v）；一张图 = 只给首帧（task_type=i2v）。",
				En: "Two images = first+last frame (task_type=flf2v); one image = first frame only (task_type=i2v).",
			},
		}, true
	case "reference":
		img, vid, aud := h3ModeMaxRefImages, h3ModeMaxRefVideos, h3ModeMaxRefAudios
		if !h3 {
			img, vid, aud = 1, 0, 0
		}
		return Mode{
			ID:       id,
			Label:    Localized{Zh: "参考生视频", En: "Reference to video"},
			TaskType: "r2va",
			References: ModeReferences{
				Image: MediaRefRule{Min: 0, Max: img},
				Video: MediaRefRule{Min: 0, Max: vid},
				Audio: MediaRefRule{Min: 0, Max: aud},
			},
			Note: Localized{
				Zh: "参考图/参考视频/参考音频至少给一项；视频与音频合计不超过 3 个。",
				En: "At least one reference; video and audio combined must not exceed 3.",
			},
		}, true
	case "video-extension":
		if !h3 {
			return Mode{}, false
		}
		return Mode{
			ID:                  id,
			Label:               Localized{Zh: "视频续写", En: "Video extension"},
			TaskType:            "continuation",
			RequiresSourceVideo: true,
			References: ModeReferences{
				Image: MediaRefRule{Min: 0, Max: 0},
				Video: MediaRefRule{Min: 1, Max: 1},
				Audio: MediaRefRule{Min: 0, Max: 0},
			},
			Note: Localized{
				Zh: "给 metadata.source_video（URL / data: / task:<id>）+ duration=最终总时长（不是延长多少秒）。源片 2–15 s，总时长须大于源片。",
				En: "Send metadata.source_video (URL / data: / task:<id>) and duration = final total length, not the added seconds. Source must be 2–15 s and the total longer than it.",
			},
		}, true
	}
	return Mode{}, false
}

// deliveryDurationsOf 实际可交付的时长阶梯。
//
// 只有带帧栅格的引擎族（H3）才有偏差：请求 1 s 交付 1.625 s。客户端拿这份表做进度条
// 与"我会拿到多长"的展示；为空表示按请求值交付。
func deliveryDurationsOf(probe string, entry setting.HiloCatalogEntry) DeliveryDurations {
	if !common.IsH3VideoModel(probe, entry.Model.ModelName) {
		return nil
	}
	maxSeconds := 0
	if p, ok := entry.Model.Params["duration"]; ok {
		for _, opt := range p.Options {
			if n, err := strconv.Atoi(strings.TrimSpace(opt)); err == nil && n > maxSeconds {
				maxSeconds = n
			}
		}
	}
	if maxSeconds <= 0 {
		return nil
	}
	// 引擎标准档上限 20 s；目录配得更大时也不会真的有产出，按 20 截断。
	if maxSeconds > 20 {
		maxSeconds = 20
	}
	out := make(DeliveryDurations, 0, maxSeconds)
	for sec := 1; sec <= maxSeconds; sec++ {
		_, delivered := common.H3DeliveredDuration(sec)
		// 三位小数：与回显的 metadata.duration_seconds 同精度，避免客户端两条数据对不上。
		out = append(out, math.Round(delivered*1000)/1000)
	}
	return out
}
