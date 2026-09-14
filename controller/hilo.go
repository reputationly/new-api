package controller

import (
	"fmt"
	"net/http"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"

	"github.com/gin-gonic/gin"
)

// MiniMax Design（内部代号 hilo）本地 gateway 的云侧接口。
//
// # 这一层在做什么
//
// 官方桌面端的形态是「renderer → 本地 gateway → 云」。本地 gateway 是一个
// 独立的 NestJS 进程，画布 / 素材 / 文件 / 生成队列 / 占位符 / 重试恢复
// 全在它里面 —— 那些我们不需要重写。它只有**模型目录和实际的模型调用**
// 要发到云上，把 `CLOUD_GATEWAY_BASE_URL` 指到 new-api，这一层就接管了。
//
// # 目录是总开关
//
// `/api/v1/models/config` 决定界面上能选哪些模型、每个模型有哪些参数、
// 参数之间怎么互斥。官方 gateway 用 zod 校验这份响应，**校验不过就整份
// 拒绝**，表现是模型选择器全空。所以 [dto.HiloModelsConfig] 的字段必须
// 逐条对齐，见那边的注释。
//
// # 取值为什么不能照抄官方
//
// 结构抄官方，**取值必须用我们平台实测的**。两者的差别是真实存在的：
// 官方 H3 的时长档位是 4–15 秒，而我们这套部署单段最长 10 秒 ——
// 照抄的话用户选 12 秒，请求发出去被平台拒掉，而界面上那个档位一直在。
//
// 官方的真实响应存在 `ovaijisuandesign/reference/hilo/` 下，用来对照升级。

// GetHiloModelsConfig 处理 `GET /api/v1/models/config`。
//
// 只报出**平台上真的有渠道的**模型。报一个没渠道的出来，用户能在界面上
// 选中它，点生成才失败 —— 而失败信息是渠道层的，说不清"这个模型没部署"。
func GetHiloModelsConfig(c *gin.Context) {
	enabled := availableForHilo()

	// **一律 make 而不是 var**：nil 切片序列化出来是 `null`，而 zod 要的是
	// 数组，`null` 会让整份目录被判 invalid schema。
	out := dto.HiloModelsConfig{
		ImageModels: make([]dto.HiloMediaModel, 0),
		VideoModels: make([]dto.HiloMediaModel, 0),
		AudioModels: make([]dto.HiloMediaModel, 0),
		TextModels:  make([]dto.HiloTextModel, 0),
	}

	// 目录由管理员配置（`HiloCatalog` 选项），不写死在代码里。
	catalog := setting.GetHiloCatalog()
	collect := func(entries []setting.HiloCatalogEntry, dst *[]dto.HiloMediaModel) {
		for _, e := range entries {
			// 主模型不可用 → 整条不报。
			if why := unavailable(enabled, e.PlatformModel); why != "" {
				logSkipOnce(e.Model.ID, why)
				continue
			}
			// **某个玩法不可用 → 只禁那个玩法，不是整条消失。**
			//
			// 客户端对一个模型只发一个接口，玩法靠 `image_mode` 区分，而我们
			// 平台上不同玩法是不同的 checkpoint（见 ModeModels）。参考族那条
			// 流水线停掉时，首尾帧玩法其实还好好的 —— 把整个 H3 撤下来是
			// 过度杀伤，用户只会看到"模型没了"，而且日志里只有一行。
			//
			// 用 `paramConstraints` 禁掉单个选项，正是官方给比例用的那套机制。
			// **必须拷一份再改。** `e.Model` 是浅拷贝，直接 append 会写进
			// 全局缓存目录那个切片的底层数组（GetHiloCatalog 返回共享值，
			// 它的注释写明「调用方只读」）。而这是个被轮询的匿名接口，
			// 并发请求会互相踩：一个响应里可能出现另一个请求禁用的选项。
			m := e.Model
			m.ParamConstraints = append(
				append([]dto.HiloParamConstraint(nil), e.Model.ParamConstraints...))
			for mode, pm := range e.ModeModels {
				if mode == "" || pm == "" || pm == e.PlatformModel {
					continue
				}
				if why := unavailable(enabled, pm); why != "" {
					logSkipOnce(e.Model.ID+"/"+mode, why)
					m.ParamConstraints = append(m.ParamConstraints, dto.HiloParamConstraint{
						// 「选了这个玩法就把它自己禁掉」—— 客户端读这张表时
						// 会把该选项置灰，而不是让用户选中之后才失败。
						If:      dto.HiloParamCond{Param: "image_mode", Eq: mode},
						Disable: dto.HiloParamDisable{Param: "image_mode", Options: []string{mode}},
					})
				}
			}
			*dst = append(*dst, m)
		}
	}
	collect(catalog.Image, &out.ImageModels)
	collect(catalog.Video, &out.VideoModels)
	collect(catalog.Audio, &out.AudioModels)

	// 对话模型。判据和媒体一样：**平台上真有渠道才报** —— 报一个没渠道的
	// 出来，用户能在界面上选中它，发消息才失败，而失败信息是渠道层的，
	// 说不清「这个模型没部署」。
	for _, e := range catalog.Text {
		if why := unavailable(enabled, e.PlatformModel); why != "" {
			logSkipOnce(e.Model.ID, why)
			continue
		}
		out.TextModels = append(out.TextModels, e.Model)
	}
	// 默认值指向第一个**真的报出去了**的，不是目录里的第一条 ——
	// 第一条可能因为没渠道被跳过，那时默认值会指向一个选择器里根本不存在
	// 的 id，客户端只能回落到它自己记着的上一次选择（多半是官方模型）。
	if len(out.TextModels) > 0 {
		out.DefaultTextModelID = out.TextModels[0].ID
	}

	c.JSON(http.StatusOK, out)
}

// unavailable 这个平台模型现在能不能用；不能用时返回原因。
func unavailable(enabled map[string]bool, platformModel string) string {
	if platformModel == "" {
		return ""
	}
	if !enabled[platformModel] {
		return fmt.Sprintf("平台上没有 %s", platformModel)
	}
	if why := pipelineCannotDeliver(platformModel); why != "" {
		return fmt.Sprintf("%s：%s", platformModel, why)
	}
	return ""
}

// skipLogged 已经报过的「跳过原因」。
//
// `/api/v1/models/config` 是**客户端在轮询**的匿名接口，而这个守卫要防的
// 恰恰是"配置一直没人改"的稳态 —— 每次请求都打一条，同一句话会刷出成千
// 上万行，反而把它淹没。按 (模型, 原因) 去重：原因变了会重新报一次，
// 配置修好之后也不会再有。
var skipLogged sync.Map

func logSkipOnce(modelID, reason string) {
	key := modelID + "\x00" + reason
	if _, loaded := skipLogged.LoadOrStore(key, struct{}{}); loaded {
		return
	}
	common.SysLog(fmt.Sprintf("[hilo] 跳过模型 %s：%s", modelID, reason))
}

// pipelineCannotDeliver 这个聚合流水线兑现不了它承诺的东西时，返回原因。
//
// # 为什么要在下发时再查一次
//
// 出厂目录（`defaultHiloCatalog`）是**代码**，跟着二进制升级；而出厂的聚合
// 配置（`DefaultAggregateModelConfig`）只是**种子**，`loadOptionsFromDatabase`
// 会用库里存过的值盖掉它。于是有一类部署：升级前保存过 AggregateModelConfig、
// 又从没保存过 HiloCatalog —— 目录按新代码只报 2K，而库里那条流水线还是
// 没有 `generate.overrides` 的旧版。
//
// 那种组合下客户端选 2K 会打到生成段，被 H3 直接拒（自建部署上限 768P）。
// 与其让用户点了才知道，不如不报出来 —— 和"平台上没有这个模型"同一个口径。
//
// **用运行时守卫而不是数据迁移**：迁移要靠人执行一次，漏了就没有任何提示；
// 守卫每次下发都自查，配置修好的那一刻自动恢复。
func pipelineCannotDeliver(platformModel string) string {
	agg := common.GetAggregateModel(platformModel)
	if agg == nil {
		return "" // 裸模型，没有这个问题
	}
	_, pinned := agg.Generate.Overrides["size"]
	hasUpscaleSection := agg.Upscale != nil
	upscaling := hasUpscaleSection && agg.Upscale.IsEnabled()

	// **两件事必须同时成立或同时不成立。**
	//
	// `generate.overrides.size` 的含义是「生成段只出中间尺寸，最终尺寸交给
	// 超分段」。所以它一旦存在，就等于声明了"后面还有一段"。
	switch {
	case pinned && hasUpscaleSection && !upscaling:
		// **只拦「有超分段但被停用」这一种。**
		//
		// 那是运营临时关掉了后一段，而生成段还钉在中间尺寸 —— 客户要 2K
		// 静默拿到 768P，不报错，比原本那个响亮的 400 更糟。
		//
		// 但**「压根没有超分段」是正当配置，不能一起拦**：
		//   · 图片聚合根本不允许有超分段（干跑校验里「图片类型不支持超分段」），
		//     而 overrides 正是图片钉尺寸的正常去处；
		//   · 视频聚合也可以就按生成分辨率交付，干跑校验对「未配置超分段」
		//     只当提示不当错误。
		// 一起拦的话，这两类会被整条撤下目录，而它们什么毛病都没有。
		return "生成段被 overrides 钉在中间尺寸，但超分段已停用，" +
			"客户会静默拿到中间尺寸的产物。请启用超分段，或同时去掉 overrides.size"
	case !pinned && upscaling:
		// 有超分段就意味着"客户传的是最终尺寸"，生成段必须收到被改写过的
		// 中间尺寸。键名是 `size` —— 生成段读的是 body["size"]。
		return "这条流水线有超分段却没有 generate.overrides.size，" +
			"客户要的最终尺寸会原样打到生成段并被拒。请在聚合模型配置里补上"
	}
	return ""
}

// availableForHilo 目录里的 `platform_model` 可以填哪些。
//
// # 两个来源，缺一不可
//
//   - **渠道模型**：`abilities` 表里启用的那些，也就是能直接调的裸模型。
//   - **聚合模型**：`AggregateModelConfig` 配的编排流水线。
//
// 只看 `abilities` 的话，聚合模型会被静默过滤掉 —— 它**没有渠道 ability**
// （见 controller/model.go 里那段注释），天然不在那张表里。表现是管理员在
// 设置页填了 `minimax-h3-2k`，保存成功，客户端上却没有这个模型。
//
// # 为什么聚合模型对这一层特别重要
//
// 我们这套部署的 H3 上限是 768P（`relay/minimaxv2` 的 resolveResolution
// 明说），2K 依赖闭源的 H3-Regenerate-2K。官方客户端能出 2K 是因为他们云端
// 有那个模型，我们没有 —— 但 `minimax-h3-2k` 这条聚合流水线
// （生成 → SwiftVR 超分）能达到同样的结果，而且对客户端来说**只是一个模型名**。
//
// 也就是说：**画质要追平官方，目录里该填的是聚合模型，不是裸的 H3。**
func availableForHilo() map[string]bool {
	out := make(map[string]bool)
	for _, name := range model.GetEnabledModels() {
		out[name] = true
	}
	// 隐藏的聚合模型照收。`Hidden` 管的是"不出现在对外的模型列表里"
	// （模型广场 / /v1/models），而这里不是对外列表 —— 是我们自己的客户端
	// 按管理员的配置取目录。过滤掉的话，定向发放的那些编排能力就永远用不上。
	// GetAggregateModels 已经只返回启用的项（见它的注释），这里不用再判一次。
	for name := range common.GetAggregateModels() {
		out[name] = true
	}
	return out
}

// GetHiloUserEquity 处理 `GET /api/v1/user/equity`。
//
// 官方用它判断用户的额度和权益。我们这边**没有计费概念**，一律回
// 「已登录、额度无限」—— 这就是"把登录 hack 掉"的全部内容：不需要改
// 客户端，我们在这一侧直接给一个恒真的答复。
//
// 字段名沿用官方那套（客户端只读它认识的几个，多余的会被忽略）。
func GetHiloUserEquity(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		// 负数在官方界面里表示"不限"。给 0 会被显示成"额度已用尽"，
		// 那时所有生成按钮都是灰的。
		"credits":   -1,
		"remaining": -1,
		"unlimited": true,
		"vip":       true,
	})
}

// GetHiloCatalogDefault 返回**出厂目录的存储格式**，管理员专用。
//
// 设置页的「载入出厂目录」要用它，而不能拿 `/api/v1/models/config` 的下发
// 结果反推 —— 那份响应里**没有 `platform_model`**（见 GetHiloModelsConfig,
// 只 append 了 e.Model）。反推只能靠 `model_name || id` 猜，而 H3 那条恰好
// 猜不对：存的是 `minimax-h3-2k`（聚合编排），而 model_name/id 是
// `MiniMax-H3`。猜错的后果是保存之后那条要么丢掉 2K 编排、要么被
// availableForHilo 直接过滤掉 —— 正是这个页面要防的"静默消失"。
func GetHiloCatalogDefault(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    setting.DefaultHiloCatalogJSON(),
	})
}
