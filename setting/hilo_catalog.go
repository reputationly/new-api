package setting

import (
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
)

// MiniMax Design（内部代号 hilo）客户端的模型目录，管理员可配。
//
// # 这份配置决定什么
//
// 官方桌面端的模型选择器、每个模型的参数表、参数之间的互斥规则，全部由
// `GET /api/v1/models/config` 的响应驱动。管理员改这里，客户端重启后
// 就能选到不同的模型 —— 不用重新编译。
//
// # 为什么默认值要写死在代码里
//
// 空配置不能退化成"没有模型"：那样客户端的选择器全空，而用户看不出是
// 「管理员没配」还是「功能坏了」。所以默认值是一份**能直接用的**目录，
// 管理员在它基础上改。
//
// # 改的时候要注意的两件事
//
//   - `backend` 必须是官方那 25 个枚举值之一（见 [dto.HiloBackends]）。
//     写一个不在枚举里的，官方 gateway 的 zod 校验会拒掉**整份目录**，
//     表现是一个模型都没有，而不是少一个模型。
//   - `backend` 决定客户端往哪个路径发、发什么字段。选 `qwen` 它会 POST
//     `/api/v2/image/qwen/generate`，选 `seedream` 则是另一个路径且多带
//     `model` 和 `size`。所以它不是标签，是协议选择。

// HiloCatalogEntry 目录里的一条：把**我们平台上的模型**映射成客户端认识的样子。
type HiloCatalogEntry struct {
	// 平台上的模型名，要和渠道里配的一致。
	//
	// 平台上没有这个模型时，这一条**不会出现在目录里** —— 报出去的话
	// 用户能选中它，点生成才失败，而失败信息是渠道层的，说不清
	// 「这个模型没部署」。
	PlatformModel string `json:"platform_model"`
	// 客户端看到的定义。字段含义见 dto/hilo.go。
	Model dto.HiloMediaModel `json:"model"`

	// ModeModels 按玩法分派到不同的平台模型。
	//
	// # 为什么需要它
	//
	// 客户端对一个模型只发**一个接口**，玩法靠 `params.image_mode` 区分
	// （实测：`first-last-frame` 时请求体里是 `first_frame_image`，
	// `reference` 时是 `reference_images`）。但我们平台这两种是**两个
	// checkpoint**，聚合配置的注释也写着「参考族是另一个 checkpoint，
	// 不能和帧族共用一条流水线」。
	//
	// 只有一个 `PlatformModel` 的话，可用性判断只查得到其中一条 —— 另一条
	// 停掉或配坏时，这个模型照样报给客户端，用户切到那个玩法才失败。
	//
	// 键是 `params.image_mode` 的取值；这里没配的玩法回落到 PlatformModel。
	ModeModels map[string]string `json:"mode_models,omitempty"`
}

// PlatformModelFor 这次请求该发给哪个平台模型。
//
// 客户端对一个模型只发一个接口，玩法靠素材字段区分，而我们平台上不同玩法
// 是不同的 checkpoint。没为这个玩法配分派时回落到 PlatformModel ——
// **回落而不是报错**：大多数模型只有一条流水线，为它们逐个写满 ModeModels
// 是噪音。
func (e HiloCatalogEntry) PlatformModelFor(mode string) string {
	if m, ok := e.ModeModels[mode]; ok && m != "" {
		return m
	}
	return e.PlatformModel
}

// FindHiloEntry 按客户端发来的模型名找目录项。
//
// 客户端发回来的是 `model`，对应目录里的 `model_name`（没配则是 `id`）——
// **不是 platform_model**。那个是我们内部的平台模型名，客户端不知道它的存在。
func FindHiloEntry(modelName string) (HiloCatalogEntry, bool) {
	c := GetHiloCatalog()
	for _, group := range [][]HiloCatalogEntry{c.Image, c.Video, c.Audio} {
		for _, e := range group {
			if e.Model.ModelName == modelName || e.Model.ID == modelName {
				return e, true
			}
		}
	}
	return HiloCatalogEntry{}, false
}

// PlatformModelsOf 这一条目录项依赖的全部平台模型（含按玩法分派的那些）。
//
// **可用性判断要看全部** —— 少查一个的后果是：目录照样报出这个模型，
// 而用户切到那个玩法才失败，且失败信息来自渠道层，说不清原因。
func (e HiloCatalogEntry) PlatformModelsOf() []string {
	seen := map[string]bool{}
	out := make([]string, 0, 1+len(e.ModeModels))
	add := func(m string) {
		if m == "" || seen[m] {
			return
		}
		seen[m] = true
		out = append(out, m)
	}
	add(e.PlatformModel)
	for _, m := range e.ModeModels {
		add(m)
	}
	return out
}

// HiloCatalog 四个分类。文本模型走 OpenCode，不在这里配。
type HiloCatalog struct {
	Image []HiloCatalogEntry `json:"image"`
	Video []HiloCatalogEntry `json:"video"`
	Audio []HiloCatalogEntry `json:"audio"`
}

var (
	hiloCatalogMu sync.RWMutex
	hiloCatalog   = defaultHiloCatalog()
)

// GetHiloCatalog 取当前目录。返回的是副本引用，调用方只读。
func GetHiloCatalog() HiloCatalog {
	hiloCatalogMu.RLock()
	defer hiloCatalogMu.RUnlock()
	return hiloCatalog
}

// UpdateHiloCatalogByJsonString 管理员改配置时调用。
//
// **解析失败时保持原样、返回错误**，不是清空 —— 管理员手滑写坏一个逗号
// 就让所有客户端的模型选择器变空，这个代价太大了。
func UpdateHiloCatalogByJsonString(jsonString string) error {
	if strings.TrimSpace(jsonString) == "" {
		hiloCatalogMu.Lock()
		hiloCatalog = defaultHiloCatalog()
		hiloCatalogMu.Unlock()
		return nil
	}
	var next HiloCatalog
	if err := common.Unmarshal([]byte(jsonString), &next); err != nil {
		return err
	}
	if err := normalizeHiloCatalog(&next); err != nil {
		return err
	}
	hiloCatalogMu.Lock()
	hiloCatalog = next
	hiloCatalogMu.Unlock()
	return nil
}

// normalizeHiloCatalog 在**保存时**校验并补齐管理员写的目录。
//
// # 为什么不能等到下发时再管
//
// 客户端那头是 zod 校验，而它的失败粒度是**整份目录**，不是单条 ——
// 一个条目漏写 `params`，所有客户端的模型选择器一起变空。管理员看到的是
// 「保存成功了，但客户端什么都没有」，而两件事隔着一次重启，根本联系不起来。
//
// 放在保存时就变成一次当场的、说得清原因的失败。
//
// # 补齐 vs 报错的分界
//
//   - **缺省值能推出来的就补**（`params` / `tool_names` 为 nil）：Go 把 nil
//     map/slice 序列化成 `null`，而 zod 要的是对象和数组。这是 JSON 表达
//     习惯的差异，不是管理员写错了，没必要拦。
//   - **写错了的就报错**（`backend` 不在枚举里、缺 `id`）：这些补不出来，
//     静默放过去等于把问题推到客户端那头。
func normalizeHiloCatalog(c *HiloCatalog) error {
	groups := []struct {
		name    string
		entries []HiloCatalogEntry
	}{
		{"image", c.Image}, {"video", c.Video}, {"audio", c.Audio},
	}
	for _, g := range groups {
		for i := range g.entries {
			e := &g.entries[i]
			id := e.Model.ID
			if strings.TrimSpace(id) == "" {
				return fmt.Errorf("%s 分组第 %d 条缺少 model.id", g.name, i+1)
			}
			if strings.TrimSpace(e.PlatformModel) == "" {
				return fmt.Errorf("%s 的 platform_model 是空的，无法判断平台上有没有这个模型", id)
			}
			// backend 写错一个字母，客户端那头拒的是**整份目录**。
			if !dto.HiloBackends[e.Model.Backend] {
				return fmt.Errorf("%s 的 backend %q 不是官方支持的值", id, e.Model.Backend)
			}
			// nil → 空对象 / 空数组。不补的话序列化出来是 null，
			// 而 zod 要的是 `z.record(...)` 和 `z.array(...)`。
			if e.Model.Params == nil {
				e.Model.Params = map[string]dto.HiloModelParam{}
			}
			if e.Model.ToolNames == nil {
				e.Model.ToolNames = []string{}
			}
			// select 的默认值必须在自己的选项里 —— 不在的话客户端打开就是
			// "选中了一个不存在的项"，选不回来也不报错。
			for name, p := range e.Model.Params {
				if p.Type != "select" {
					continue
				}
				if len(p.Options) == 0 {
					return fmt.Errorf("%s 的参数 %s 是下拉框但没有选项", id, name)
				}
				if !slices.Contains(p.Options, p.Default) {
					return fmt.Errorf("%s 的参数 %s 默认值 %q 不在选项 %v 里",
						id, name, p.Default, p.Options)
				}
			}
			// 约束引用的参数必须真的存在，否则规则静默失效。
			for _, pc := range e.Model.ParamConstraints {
				if _, ok := e.Model.Params[pc.If.Param]; !ok {
					return fmt.Errorf("%s 的约束条件引用了不存在的参数 %q", id, pc.If.Param)
				}
				if _, ok := e.Model.Params[pc.Disable.Param]; !ok {
					return fmt.Errorf("%s 的约束要禁用不存在的参数 %q", id, pc.Disable.Param)
				}
			}
		}
	}
	return nil
}

// DefaultHiloCatalogJSON 出厂目录的存储格式，给设置页的「载入出厂目录」用。
//
// **不是当前生效的配置**，是代码里写死的那份出厂值。管理员想在出厂值基础上
// 改时，需要的正是这个。
func DefaultHiloCatalogJSON() string {
	b, err := common.Marshal(defaultHiloCatalog())
	if err != nil {
		return "{}"
	}
	return string(b)
}

func HiloCatalog2JsonString() string {
	hiloCatalogMu.RLock()
	defer hiloCatalogMu.RUnlock()
	b, err := common.Marshal(hiloCatalog)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// hiloSelect 造一个下拉参数。
//
// `def` 必须在 `options` 里 —— 不在的话客户端打开就是"选中了一个不存在
// 的项"，而且不报错。
func hiloSelect(label string, options []string, def string) dto.HiloModelParam {
	return dto.HiloModelParam{Type: "select", Label: label, Options: options, Default: def}
}

// defaultHiloCatalog 出厂默认目录。
//
// **结构抄官方，取值用我们平台实测的。** 两者的差别是真实存在的：
// 官方 H3 的时长档位是 4–15 秒，而我们这套部署单段最长 10 秒 ——
// 照抄的话用户选 12 秒，请求发出去被平台拒掉，而界面上那个档位一直在。
//
// 官方的真实响应存在 `ovaijisuandesign/reference/hilo/` 下，用来对照升级。
func defaultHiloCatalog() HiloCatalog {
	imageRatios := []string{"adaptive", "1:1", "16:9", "9:16", "4:3", "3:4", "3:2", "2:3"}
	// 平台对 H3 的比例白名单，取自它拒绝请求时原样列出来的那一串。
	videoRatios := []string{"adaptive", "16:9", "9:16", "1:1", "4:3", "3:4", "21:9"}

	return HiloCatalog{
		Image: []HiloCatalogEntry{
			{
				PlatformModel: "qwen-image",
				Model: dto.HiloMediaModel{
					ID: "qwen-image", Name: "Qwen Image", Backend: "qwen",
					ModelName: "qwen-image", Type: "image",
					DisplayName: "Qwen Image", SeriesID: "Qwen", MentionName: "qwen-image",
					ToolNames: []string{"hub_generate_image"}, MaxRefs: 0,
					Params: map[string]dto.HiloModelParam{
						"aspect_ratio": hiloSelect("宽高比", imageRatios, "adaptive"),
						// 1K/2K 是我们 `resolve_size` 认识的两档；给 4K 会落进
						// 兜底，出图尺寸和界面显示对不上。
						"resolution": hiloSelect("分辨率", []string{"1K", "2K"}, "1K"),
					},
				},
			},
			{
				PlatformModel: "qwen-image-edit",
				Model: dto.HiloMediaModel{
					ID: "qwen-image-edit", Name: "Qwen Image Edit", Backend: "qwen",
					ModelName: "qwen-image-edit", Type: "image",
					DisplayName: "Qwen 图像编辑", SeriesID: "Qwen", MentionName: "qwen-image-edit",
					ToolNames: []string{"hub_generate_image"},
					// 图生图要收底图。**必须大于 0** —— 为 0 的话界面不给拖图
					// 进来，这个模型就退化成文生图了，而且不报错。
					MaxRefs: 4,
					Params: map[string]dto.HiloModelParam{
						// 图生图的画幅由底图决定，只留分辨率。
						"resolution": hiloSelect("分辨率", []string{"1K", "2K"}, "1K"),
					},
				},
			},
		},
		Video: []HiloCatalogEntry{
			{
				// **填聚合模型，不是裸的 minimax-h3-fl2va。**
				//
				// 我们这套部署的 H3 上限是 768P（relay/minimaxv2 的
				// resolveResolution 明说），2K 依赖闭源的 H3-Regenerate-2K,
				// 我们没有。官方客户端能出 2K 是因为他们云端有那个模型。
				//
				// `minimax-h3-2k` 是 AggregateModelConfig 里的编排流水线:
				// 生成(minimax-h3-fl2va) → SwiftVR 超分到 2K。对客户端来说
				// 它**只是一个模型名、一个任务**，但产出追得平官方。
				//
				// 这条要求 AggregateModelConfig 里 minimax-h3-2k 是启用的
				// （出厂默认启用）。停掉的话这一条会从目录里消失 ——
				// 那是对的：编排没了，就不该再承诺 2K。
				PlatformModel: "minimax-h3-2k",
				// 参考族是另一个 checkpoint、另一条流水线。客户端对两种玩法
				// 只发一个接口，靠 `image_mode` 区分，所以分派在这里表达。
				ModeModels: map[string]string{
					"reference":        "minimax-h3-ref-2k",
					"first-last-frame": "minimax-h3-2k",
				},
				Model: dto.HiloMediaModel{
					ID: "MiniMax-H3", Name: "MiniMax H3", Backend: "minimax_v3",
					ModelName: "MiniMax-H3", PricingID: "MiniMax-H3", Type: "video",
					DisplayName: "MiniMax H3", SeriesID: "MiniMax", MentionName: "MiniMax-H3",
					ToolNames: []string{"hub_generate_video"},
					MaxRefs:   9, PromptMaxLength: 7000,
					Params: map[string]dto.HiloModelParam{
						"image_mode": hiloSelect("生成方式",
							[]string{"reference", "first-last-frame"}, "reference"),
						// **官方是 4–15，我们只到 10** —— 本机单段最长 10 秒。
						"duration": hiloSelect("时长", []string{"4", "5", "6", "7", "8", "9", "10"}, "5"),
						"ratio":    hiloSelect("宽高比", videoRatios, "adaptive"),
						// **只给 2K，因为这条聚合流水线只产出 2K。**
						//
						// 超分段是无条件跑的（buildTaskAggregateInfo 只看
						// `Upscale.IsEnabled()`，不看客户要的档位），所以摆出
						// 768P 也兑现不了：选了照样被超到 2K，而且付 2K 的钱。
						//
						// 而 2K 能成立，靠的是出厂聚合配置里的
						// `generate.overrides: {size: 768P}` —— 没有它的话
						// 客户传的 2K 会原样到达生成段，被 resolveResolution
						// 直接 400（2K 依赖闭源的 H3-Regenerate-2K）。
						// 两者是一对，改一个必须看另一个。
						//
						// 要同时给 768P 和 2K（官方就是这样），得让编排层按档位
						// 决定"跑不跑超分"。那是生成转发那一层的事，还没做。
						"resolution": hiloSelect("分辨率", []string{"2K"}, "2K"),
						// **默认开。** 官方也是 true。我们之前完全没有这个参数,
						// 于是 H3 的原生音轨一直没生成 —— 表现是「视频没有声音」。
						"generate_audio": hiloSelect("有声视频", []string{"true", "false"}, "true"),
					},
					// 首尾帧驱动时**画幅由那张图定**，不能再选比例。
					//
					// 这条以前是我们写死在界面代码里的分支；官方是数据驱动 ——
					// 强塞一个比例的后果是平台把 size 反推成 `32:57` 然后拒掉
					// 整个任务。
					ParamConstraints: []dto.HiloParamConstraint{
						{
							If:      dto.HiloParamCond{Param: "image_mode", Eq: "first-last-frame"},
							Disable: dto.HiloParamDisable{Param: "ratio", Options: videoRatios[1:]},
						},
					},
					InputMediaLimits: &dto.HiloInputMediaLimits{
						ImageMinWidth: 256, ImageMinHeight: 256,
						ImageMaxWidth: 5760, ImageMaxHeight: 5760,
						ImageMinAspectRatio: 0.4, ImageMaxAspectRatio: 2.5,
					},
				},
			},
		},
		Audio: []HiloCatalogEntry{
			{
				PlatformModel: "minimax-music3",
				Model: dto.HiloMediaModel{
					ID: "music-3.0", Name: "Music 3.0", Backend: "minimax_music",
					ModelName: "minimax-music3", Type: "audio",
					DisplayName: "音乐生成", SeriesID: "MiniMax", MentionName: "music-3.0",
					ToolNames: []string{"hub_generate_audio_music"}, MaxRefs: 0,
					// 曲风写在 prompt、唱词单独一个参数。**两者必须分开** ——
					// 揉在一起会让引擎把风格描述也唱出来，而且不报错。
					PromptLabel: "musicStyle",
					Params: map[string]dto.HiloModelParam{
						"is_instrumental": hiloSelect("纯器乐", []string{"true", "false"}, "false"),
						"lyrics":          {Type: "textarea", Label: "歌词", Default: "", Optional: true},
					},
				},
			},
			{
				PlatformModel: "indextts-2.5",
				Model: dto.HiloMediaModel{
					ID: "speech-2.5", Name: "Speech 2.5", Backend: "minimax_tts",
					ModelName: "indextts-2.5", Type: "audio",
					DisplayName: "语音合成", SeriesID: "IndexTTS", MentionName: "speech-2.5",
					ToolNames: []string{"hub_generate_speech"}, MaxRefs: 0,
					PromptLabel: "text", PromptMaxLength: 10000,
					Params: map[string]dto.HiloModelParam{},
				},
			},
		},
	}
}
