package service

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"
)

// HiloUnavailable 这个平台模型现在能不能用；不能用时返回原因。
//
// 两个出口共用：`/api/v1/models/config`（官方本地 gateway 的目录）与
// `/v1/ov/capabilities`（我们自己的能力目录）。判据只写一份 —— 两处判出不同结果，
// 表现是「目录里有、调的时候没有渠道」，最难自查。
func HiloUnavailable(enabled map[string]bool, platformModel string) string {
	if platformModel == "" {
		return ""
	}
	if !enabled[platformModel] {
		return fmt.Sprintf("平台上没有 %s", platformModel)
	}
	if why := PipelineCannotDeliver(platformModel); why != "" {
		return fmt.Sprintf("%s：%s", platformModel, why)
	}
	return ""
}

// PipelineCannotDeliver 这个聚合流水线兑现不了它承诺的东西时，返回原因。
//
// # 为什么要在下发时再查一次
//
// 出厂目录（`defaultHiloCatalog`）是**代码**，跟着二进制升级；而出厂的聚合
// 配置（`DefaultAggregateModelConfig`）只是**种子**，`loadOptionsFromDatabase`
// 会用库里存过的值盖掉它。于是有一类部署：升级前保存过 AggregateModelConfig、
// 又从没保存过 HiloCatalog —— 目录按新代码报出 2K，而库里那条流水线还是
// 没有 `generate.overrides` 的旧版。
//
// 那种组合下客户端选 2K 会打到生成段，被 H3 直接拒（自建部署上限 768P）。
// 与其让用户点了才知道，不如不报出来 —— 和"平台上没有这个模型"同一个口径。
//
// **用运行时守卫而不是数据迁移**：迁移要靠人执行一次，漏了就没有任何提示；
// 守卫每次下发都自查，配置修好的那一刻自动恢复。
func PipelineCannotDeliver(platformModel string) string {
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

