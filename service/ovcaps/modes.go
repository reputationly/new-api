package ovcaps

// 能力的"玩法"（modes）与可交付时长阶梯。
//
// 这一层只做**转述**：目录里 `params.image_mode.options` 说明模型有哪些玩法，参考上限
// 与 task_type 由引擎族的既有契约决定（H3 的 9/3/3 与 2/1 见
// relay/channel/task/gpustackplus 的 r2va 上限与官方 inputContract）。不在这里新造
// 规则 —— 新造就会与网关的真实校验漂移，而漂移的症状是"目录说能传 9 张，提交才 400"。

import (
	"math"
	"sort"
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
func modesOf(entry setting.HiloCatalogEntry, platformModel, probe, transport string) []Mode {
	return modesOfFor(entry, platformModel, []string{probe}, transport, nil, nil)
}

// modesOfFor 同 modesOf，另外两个参数决定**聚合模型**该报哪些玩法。
//
// 两条分派机制不能混淆：
//
//	裸模型          看目录的 `mode_models`（一条目录项按玩法派到多个平台模型）
//	聚合模型        看它自己的 `generate.task_models`（按门面 task_type 派到生成段），
//	                `mode_models` 对它没有约束力
//
// 聚合不能继续用 mode_models 过滤：聚合名（`MiniMax-H3`）不等于任何 target，
// 四个玩法会被全部跳过 —— 能力目录里于是出现"有参数、没玩法"的半条条目，
// 客户端一个玩法都选不了。而反过来，`minimax-h3-2k` 这种"名字叫帧族其实也能出
// 参考片"的聚合，用 mode_models 判定会把参考玩法藏掉。
//
// callable 为 nil 时只能按"配了就有"判定（没有渠道信息），用于不涉及聚合的调用点。
func modesOfFor(entry setting.HiloCatalogEntry, platformModel string, probes []string, transport string, agg *common.AggregateModel, callable func(string) bool) []Mode {
	if transport != setting.TransportVideosTask {
		return nil
	}
	modeParam, ok := entry.Model.Params["image_mode"]
	if !ok || len(modeParam.Options) == 0 {
		return nil
	}
	// **全部候选模型一起判引擎族。** 只判 probes[0] 会让结果取决于顺序：H3 的两个
	// checkpoint 里若只有一个在「视频模型配置」里声明过 engine，另一个先排到就会
	// 被判成非 H3 —— 玩法还在，但上限退回保守值（参考族 1 图而不是 9 图），
	// 客户端于是把本来传得上去的素材藏掉。
	h3 := common.IsH3VideoModel(append(append([]string{}, probes...), entry.Model.ModelName)...)
	modes := make([]Mode, 0, len(modeParam.Options))
	for _, raw := range modeParam.Options {
		id := strings.ToLower(strings.TrimSpace(raw))
		if agg == nil {
			// 一条目录项可能按玩法分派到多个平台模型（H3 帧族 / 参考族是两个 checkpoint）。
			// 每个平台模型只报**真正落在自己身上**的玩法：参考族报"首尾帧"是错的 ——
			// 客户端照着填，请求会被派到另一个 checkpoint 上。
			if target, ok := entry.ModeModels[id]; ok && strings.TrimSpace(target) != strings.TrimSpace(platformModel) {
				continue
			}
		}
		mode, ok := modeDescriptor(id, h3)
		if !ok {
			// 目录里出现了我们不认识的玩法：**跳过而不是编一个** —— 报一个我们其实
			// 不支持提交的 mode，比不报更糟（客户端会照着填，然后 400）。
			continue
		}
		if agg != nil && !aggregateServesMode(agg, mode.TaskType, callable) {
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
	durationParam, ok := entry.Model.Params["duration"]
	if !ok {
		return nil
	}
	// 阶梯按**目录里真实存在的档位**生成，不是 1..max：面板对外是官方的 4–15，
	// 从 1 铺到 15 会多报三个客户端根本选不到的档位。
	seconds := make([]int, 0, len(durationParam.Options))
	for _, opt := range durationParam.Options {
		n, err := strconv.Atoi(strings.TrimSpace(opt))
		if err != nil || n <= 0 {
			continue
		}
		seconds = append(seconds, n)
	}
	if len(seconds) == 0 {
		return nil
	}
	sort.Ints(seconds)
	out := make(DeliveryDurations, 0, len(seconds))
	for _, sec := range seconds {
		_, delivered := common.H3DeliveredDuration(sec)
		// 三位小数：与回显的 metadata.duration_seconds 同精度，避免客户端两条数据对不上。
		out = append(out, math.Round(delivered*1000)/1000)
	}
	return out
}

// aggregateServesMode 这条聚合流水线能不能真的出这个玩法。
//
// 判据是 **task_type → 生成段**：`generate.task_models` 里配了、而且那个段当前可调用。
// 续写要按 i2v 查 —— 它在流水线里第一步就被改写成一次 i2v（源片末帧当首帧），
// `task_models` 里不会有 "continuation" 这个 key。直接拿 mode.TaskType 去查会让
// 续写**静默消失**：目录里那条玩法一夜之间没了，没有任何日志。
func aggregateServesMode(agg *common.AggregateModel, taskType string, callable func(string) bool) bool {
	key, ok := common.NormalizeRoutedTaskType(taskType)
	if !ok {
		return false
	}
	model := agg.Generate.ModelFor(key)
	if strings.TrimSpace(model) == "" {
		return false
	}
	if callable == nil {
		return true
	}
	return callable(model)
}

// catalogEntryForAggregate 找一条聚合模型该复用哪条目录项（参数表、上限、展示名）。
//
// **先按聚合自己的名字找**（`model_name` / `id`）：`MiniMax-H3` 这种"官方名 + 聚合名
// 同名"的情形靠它才能拿到参数表与玩法；只按生成段找的话，`catalogEntryForModel`
// 的候选是 `PlatformModelsOf()`（目录里的 platform_model 与 mode_models），
// 而生成段（`minimax-h3-fl2va`）从来不在那份名单里，于是这条聚合会以"无参数"的
// 裸能力出现。
//
// 找不到再退回生成段——那是老行为，服务的是"聚合名不在目录里"的其余模型。
func catalogEntryForAggregate(agg *common.AggregateModel) (setting.HiloCatalogEntry, string, bool) {
	if agg == nil {
		return setting.HiloCatalogEntry{}, "", false
	}
	name := strings.TrimSpace(agg.Name)
	if name != "" {
		for _, it := range catalogEntriesInOrder() {
			entry := it.entry
			if strings.TrimSpace(entry.Model.ModelName) == name || strings.TrimSpace(entry.Model.ID) == name {
				return entry, it.group, true
			}
		}
	}
	return catalogEntryForModel(agg.Generate.Model)
}

// catalogEntryForModel 找"这个平台模型对应哪条目录项"。
//
// 用途：目录里没给某条聚合配置条目时，用它的**生成段模型**的条目补参数/玩法/上限。
// 一个平台模型可能出现在多条目录项里（例如别的模型的 mode_models 也指向它），
// 取第一条即可 —— 参数表本就来自同一个模型。
func catalogEntryForModel(model string) (setting.HiloCatalogEntry, string, bool) {
	model = strings.TrimSpace(model)
	if model == "" {
		return setting.HiloCatalogEntry{}, "", false
	}
	for _, it := range catalogEntriesInOrder() {
		for _, platformModel := range it.entry.PlatformModelsOf() {
			if platformModel == model {
				return it.entry, it.group, true
			}
		}
	}
	return setting.HiloCatalogEntry{}, "", false
}

// catalogEntriesInOrder 目录项及其分组名，顺序固定（image → video → audio，
// 组内保持管理员排的顺序）。目录项被多条能力共用，遍历顺序必须稳定 ——
// 能力目录的版本哈希与"取第一条"的语义都靠它。
func catalogEntriesInOrder() []struct {
	group string
	entry setting.HiloCatalogEntry
} {
	catalog := setting.GetHiloCatalog()
	out := make([]struct {
		group string
		entry setting.HiloCatalogEntry
	}, 0, len(catalog.Image)+len(catalog.Video)+len(catalog.Audio))
	for _, group := range []struct {
		name    string
		entries []setting.HiloCatalogEntry
	}{
		{"image", catalog.Image}, {"video", catalog.Video}, {"audio", catalog.Audio},
	} {
		for _, entry := range group.entries {
			out = append(out, struct {
				group string
				entry setting.HiloCatalogEntry
			}{group: group.name, entry: entry})
		}
	}
	return out
}
